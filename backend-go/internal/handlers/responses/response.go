package responses

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/converters"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/sjson"
)

func handleSuccess(
	c *gin.Context,
	resp *http.Response,
	provider *providers.ResponsesProvider,
	upstreamType string,
	envCfg *config.EnvConfig,
	sessionManager *session.SessionManager,
	startTime time.Time,
	originalReq *types.ResponsesRequest,
	originalRequestJSON []byte,
	fuzzyMode bool,
	timeouts common.StreamPreflightTimeouts,
) (*types.Usage, error) {
	defer resp.Body.Close()

	isStream := originalReq != nil && originalReq.Stream

	// Inject codex_tool_compat_enabled into raw JSON so converters can read it.
	// TransformerMetadata is json:"-" so it does not survive serialization.
	if originalReq != nil && originalReq.TransformerMetadata != nil {
		if enabled, ok := originalReq.TransformerMetadata["codex_tool_compat_enabled"].(bool); ok {
			if injected, err := sjson.SetBytes(originalRequestJSON, "transformer_metadata.codex_tool_compat_enabled", enabled); err == nil {
				originalRequestJSON = injected
			}
		}
	}
	if rawTools, ok := c.Get("codex_merged_raw_tools"); ok {
		if injected, err := sjson.SetBytes(originalRequestJSON, "tools", rawTools); err == nil {
			originalRequestJSON = injected
		}
	}

	if isStream {
		return handleStreamSuccess(c, resp, upstreamType, envCfg, sessionManager, startTime, originalReq, originalRequestJSON, timeouts)
	}

	// 非流式响应处理
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to read response"})
		return nil, err
	}

	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		common.RequestLogf(c, "[Responses-Timing] Responses 响应完成: %dms, 状态: %d", responseTime, resp.StatusCode)
		common.LogUpstreamResponse(c, resp, bodyBytes, envCfg, "Responses")
	}

	providerResp := &types.ProviderResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		Body:       bodyBytes,
		Stream:     false,
	}

	responsesResp, err := provider.ConvertToResponsesResponse(providerResp, upstreamType, "")
	if err != nil {
		// JSON 解析失败（如上游返回 HTML 错误页面）：不写 Header，返回可 failover 的错误
		preview := bodyBytes
		if len(preview) > 100 {
			preview = preview[:100]
		}
		common.RequestLogf(c, "[Responses-InvalidBody] 响应体解析失败: %v, body前100字节: %s", err, preview)
		return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
	}

	// 空响应拦截（仅 Fuzzy 模式）：上游 200 但 output 语义为空，
	// Header 未发送，可安全 failover 到下一个 Key/BaseURL/渠道
	if fuzzyMode && common.IsResponsesResponseEmpty(responsesResp) {
		common.RequestLogf(c, "[Responses-EmptyResponse] 上游返回空响应（非流式，upstreamType=%s），触发 failover", upstreamType)
		return nil, common.ErrEmptyNonStreamResponse
	}

	// Remap Codex custom tool proxy function calls to custom_tool_call items.
	if originalReq != nil {
		codexEnabled := false
		if originalReq.TransformerMetadata != nil {
			if v, ok := originalReq.TransformerMetadata["codex_tool_compat_enabled"].(bool); ok {
				codexEnabled = v
			}
		}
		if codexEnabled {
			codexCtx, ok := c.Get("codex_tool_context")
			if !ok {
				codexCtx, ok = originalReq.TransformerMetadata["codex_tool_context"]
			}
			typedCtx, ok := codexCtx.(converters.CodexToolContext)
			if !ok {
				typedCtx = converters.BuildCodexToolContext(originalReq.Tools)
			}
			if !ok && len(originalReq.RawTools) > 0 {
				typedCtx = converters.BuildCodexToolContextFromRaw(originalReq.RawTools)
			}
			typedCtx.RemapCustomToolCallsInResponse(responsesResp)
			typedCtx.RemapNamespaceFunctionCallsInResponse(responsesResp)
		}
	}

	// Token 补全逻辑
	originalUsage := responsesResp.Usage

	patchResponsesUsageWithContext(c, responsesResp, originalRequestJSON, envCfg)

	// 更新会话
	if originalReq.Store == nil || *originalReq.Store {
		sess, err := sessionManager.GetOrCreateSession(originalReq.PreviousResponseID)
		if err == nil {
			inputItems, _ := parseInputToItems(originalReq.Input)
			for _, item := range inputItems {
				sessionManager.AppendMessage(sess.ID, item, 0)
			}

			for _, item := range responsesResp.Output {
				sessionManager.AppendMessage(sess.ID, item, responsesResp.Usage.TotalTokens)
			}

			previousResponseID := sess.LastResponseID
			sessionManager.UpdateLastResponseID(sess.ID, responsesResp.ID)
			sessionManager.RecordResponseMapping(responsesResp.ID, sess.ID)

			if previousResponseID != "" {
				responsesResp.PreviousID = previousResponseID
			}
		}
	}

	// 改写 response.completed 事件中的 model 字段（仅非流式）
	// 流式路径在 processLine 中处理
	if envCfg.RewriteResponseModel && originalReq != nil && originalReq.Model != "" {
		responsesResp.Model = originalReq.Model
	}

	utils.ForwardResponseHeaders(resp.Header, c.Writer)
	c.JSON(200, responsesResp)

	// 返回 usage 数据用于指标记录
	promptTokensTotal := promptTokensTotalFromResponsesInput(
		originalUsage.InputTokens,
		upstreamType,
		responsesUsageHasClaudeCache(originalUsage),
	)
	return metricsUsageFromResponsesUsage(responsesResp.Usage, promptTokensTotal), nil
}

func responsesUsageHasClaudeCache(usage types.ResponsesUsage) bool {
	return usage.CacheCreationInputTokens > 0 ||
		usage.CacheReadInputTokens > 0 ||
		usage.CacheCreation5mInputTokens > 0 ||
		usage.CacheCreation1hInputTokens > 0
}

func promptTokensTotalFromResponsesInput(inputTokens int, upstreamType string, hasClaudeCache bool) int {
	if upstreamType != "responses" || inputTokens <= 0 {
		return 0
	}
	if inputTokens <= 1 && !hasClaudeCache {
		return 0
	}
	return inputTokens
}

func metricsUsageFromResponsesUsage(usage types.ResponsesUsage, promptTokensTotal int) *types.Usage {
	cacheReadTokens := usage.CacheReadInputTokens
	if cacheReadTokens == 0 && usage.InputTokensDetails != nil && usage.InputTokensDetails.CachedTokens > 0 {
		cacheReadTokens = usage.InputTokensDetails.CachedTokens
	}

	return &types.Usage{
		InputTokens:                usage.InputTokens,
		OutputTokens:               usage.OutputTokens,
		CacheCreationInputTokens:   usage.CacheCreationInputTokens,
		CacheReadInputTokens:       cacheReadTokens,
		PromptTokensTotal:          promptTokensTotal,
		CacheCreation5mInputTokens: usage.CacheCreation5mInputTokens,
		CacheCreation1hInputTokens: usage.CacheCreation1hInputTokens,
		CacheTTL:                   usage.CacheTTL,
	}
}

// patchResponsesUsage 补全 Responses 响应的 Token 统计
func patchResponsesUsage(resp *types.ResponsesResponse, requestBody []byte, envCfg *config.EnvConfig) {
	patchResponsesUsageWithContext(nil, resp, requestBody, envCfg)
}

func patchResponsesUsageWithContext(c *gin.Context, resp *types.ResponsesResponse, requestBody []byte, envCfg *config.EnvConfig) {
	// 检查是否有 Claude 原生缓存 token（有时才跳过 input_tokens 修补）
	// 仅检测 Claude 原生字段：cache_creation_input_tokens, cache_read_input_tokens,
	// cache_creation_5m_input_tokens, cache_creation_1h_input_tokens
	// 注意：不检测 input_tokens_details.cached_tokens（OpenAI 格式），避免错误跳过
	hasClaudeCache := resp.Usage.CacheCreationInputTokens > 0 ||
		resp.Usage.CacheReadInputTokens > 0 ||
		resp.Usage.CacheCreation5mInputTokens > 0 ||
		resp.Usage.CacheCreation1hInputTokens > 0

	// 检查是否需要补全
	needInputPatch := resp.Usage.InputTokens <= 1 && !hasClaudeCache
	needOutputPatch := resp.Usage.OutputTokens <= 1

	// 如果 usage 完全为空，进行完整估算
	if resp.Usage.InputTokens == 0 && resp.Usage.OutputTokens == 0 && resp.Usage.TotalTokens == 0 {
		estimatedInput := utils.EstimateResponsesRequestTokens(requestBody)
		estimatedOutput := estimateResponsesOutputFromItems(resp.Output)
		resp.Usage.InputTokens = estimatedInput
		resp.Usage.OutputTokens = estimatedOutput
		resp.Usage.TotalTokens = calculateTotalTokensWithCache(
			estimatedInput,
			estimatedOutput,
			resp.Usage.CacheReadInputTokens,
			resp.Usage.CacheCreationInputTokens,
			resp.Usage.CacheCreation5mInputTokens,
			resp.Usage.CacheCreation1hInputTokens,
		)
		if envCfg.EnableResponseLogs {
			common.RequestLogf(c, "[Responses-Token] 上游无Usage, 本地估算: input=%d, output=%d", estimatedInput, estimatedOutput)
		}
		return
	}

	// 修补虚假值
	originalInput := resp.Usage.InputTokens
	originalOutput := resp.Usage.OutputTokens
	patched := false

	if needInputPatch {
		resp.Usage.InputTokens = utils.EstimateResponsesRequestTokens(requestBody)
		patched = true
	}
	if needOutputPatch {
		resp.Usage.OutputTokens = estimateResponsesOutputFromItems(resp.Output)
		patched = true
	}

	// 重新计算 TotalTokens（修补时或 total_tokens 为 0 但 input/output 有效时）
	if patched || (resp.Usage.TotalTokens == 0 && (resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0)) {
		resp.Usage.TotalTokens = calculateTotalTokensWithCache(
			resp.Usage.InputTokens,
			resp.Usage.OutputTokens,
			resp.Usage.CacheReadInputTokens,
			resp.Usage.CacheCreationInputTokens,
			resp.Usage.CacheCreation5mInputTokens,
			resp.Usage.CacheCreation1hInputTokens,
		)
	}

	if envCfg.EnableResponseLogs {
		if patched {
			common.RequestLogf(c, "[Responses-Token] 虚假值修补: InputTokens=%d->%d, OutputTokens=%d->%d",
				originalInput, resp.Usage.InputTokens, originalOutput, resp.Usage.OutputTokens)
		}
		common.RequestLogf(c, "[Responses-Token] InputTokens=%d, OutputTokens=%d, TotalTokens=%d, CacheCreation=%d, CacheRead=%d, CacheCreation5m=%d, CacheCreation1h=%d, CacheTTL=%s",
			resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.TotalTokens,
			resp.Usage.CacheCreationInputTokens, resp.Usage.CacheReadInputTokens,
			resp.Usage.CacheCreation5mInputTokens, resp.Usage.CacheCreation1hInputTokens,
			resp.Usage.CacheTTL)
	}
}

// estimateResponsesOutputFromItems 从 ResponsesItem 数组估算输出 token
func estimateResponsesOutputFromItems(output []types.ResponsesItem) int {
	if len(output) == 0 {
		return 0
	}

	total := 0
	for _, item := range output {
		// 处理 content
		if item.Content != nil {
			switch v := item.Content.(type) {
			case string:
				total += utils.EstimateTokens(v)
			case []interface{}:
				for _, block := range v {
					if b, ok := block.(map[string]interface{}); ok {
						if text, ok := b["text"].(string); ok {
							total += utils.EstimateTokens(text)
						}
					}
				}
			case []types.ContentBlock:
				// 处理结构化 ContentBlock 数组
				for _, block := range v {
					if block.Text != "" {
						total += utils.EstimateTokens(block.Text)
					}
				}
			default:
				// 回退：序列化后估算
				data, _ := json.Marshal(v)
				total += utils.EstimateTokens(string(data))
			}
		}

		// 处理 tool_use
		if item.ToolUse != nil {
			if item.ToolUse.Name != "" {
				total += utils.EstimateTokens(item.ToolUse.Name) + 2
			}
			if item.ToolUse.Input != nil {
				data, _ := json.Marshal(item.ToolUse.Input)
				total += utils.EstimateTokens(string(data))
			}
		}

		// 处理 function_call 类型（item.Type == "function_call"）
		if item.Type == "function_call" {
			// 在转换后的响应中，function_call 的参数可能在 Content 中
			if contentStr, ok := item.Content.(string); ok {
				total += utils.EstimateTokens(contentStr)
			}
		}
	}

	return total
}

// handleStreamSuccess 处理流式响应
//
// 流程：预读取行 → 检测空响应
//   - 空响应 → return nil, ErrEmptyStreamResponse（Header 未发送，可安全重试）
