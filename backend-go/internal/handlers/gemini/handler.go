// Package gemini 提供 Gemini API 的处理器
package gemini

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/converters"
	"github.com/BenedictKing/ccx/internal/copilot"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// Handler Gemini API 代理处理器
// 支持多渠道调度：当配置多个渠道时自动启用
func Handler(
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
) gin.HandlerFunc {
	return gin.HandlerFunc(func(c *gin.Context) {
		// Gemini 代理端点统一使用代理访问密钥鉴权（x-api-key / Authorization: Bearer）
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}

		startTime := time.Now()

		// 读取原始请求体
		maxBodySize := envCfg.MaxRequestBodySize
		bodyBytes, err := common.ReadRequestBody(c, maxBodySize)
		if err != nil {
			return
		}
		c.Set("requestBodyBytes", bodyBytes)

		// 解析 Gemini 请求
		var geminiReq types.GeminiRequest
		if len(bodyBytes) > 0 {
			if err := json.Unmarshal(bodyBytes, &geminiReq); err != nil {
				c.JSON(400, types.GeminiError{
					Error: types.GeminiErrorDetail{
						Code:    400,
						Message: fmt.Sprintf("Invalid request body: %v", err),
						Status:  "INVALID_ARGUMENT",
					},
				})
				return
			}
		}

		// 从 URL 路径提取模型名称
		// 格式: /v1/models/{model}:generateContent 或 /v1/models/{model}:streamGenerateContent
		// 使用 *modelAction 通配符捕获整个后缀，如 /gemini-pro:generateContent
		modelAction := c.Param("modelAction")
		// 移除前导斜杠（Gin 的 * 通配符会保留前导斜杠）
		modelAction = strings.TrimPrefix(modelAction, "/")
		model := extractModelName(modelAction)
		if model == "" {
			c.JSON(400, types.GeminiError{
				Error: types.GeminiErrorDetail{
					Code:    400,
					Message: "Model name is required in URL path",
					Status:  "INVALID_ARGUMENT",
				},
			})
			return
		}

		// 判断是否流式
		isStream := strings.Contains(c.Request.URL.Path, "streamGenerateContent")

		// 提取统一会话标识用于 Trace 亲和性
		userID := utils.ExtractUnifiedSessionID(c, bodyBytes)
		agentCtx := utils.ExtractAgentContext(c, bodyBytes)
		c.Set("agentContext", agentCtx)
		common.SetRequestLogContextWithAgent(c, userID, countGeminiUserContents(geminiReq), agentCtx)

		// 记录原始请求信息
		common.LogOriginalRequest(c, bodyBytes, envCfg, "Gemini")

		// 检查是否为多渠道模式
		isMultiChannel := channelScheduler.IsMultiChannelMode(scheduler.ChannelKindGemini)

		if isMultiChannel {
			handleMultiChannel(c, envCfg, cfgManager, channelScheduler, bodyBytes, &geminiReq, model, isStream, userID, startTime)
		} else {
			handleSingleChannel(c, envCfg, cfgManager, channelScheduler, bodyBytes, &geminiReq, model, isStream, startTime)
		}
	})
}

// extractModelName 从 URL 参数提取模型名称
// 输入: "gemini-2.0-flash:generateContent" 或 "gemini-2.0-flash"
// 输出: "gemini-2.0-flash"
func extractModelName(param string) string {
	if param == "" {
		return ""
	}
	// 移除 :generateContent 或 :streamGenerateContent 后缀
	if idx := strings.Index(param, ":"); idx > 0 {
		return param[:idx]
	}
	return param
}

func countGeminiUserContents(req types.GeminiRequest) int {
	count := 0
	for _, content := range req.Contents {
		if content.Role != "user" {
			continue
		}
		if len(content.Parts) > 0 {
			count++
		}
	}
	return count
}

// handleMultiChannel 处理多渠道 Gemini 请求
func handleMultiChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	geminiReq *types.GeminiRequest,
	model string,
	isStream bool,
	userID string,
	startTime time.Time,
) {
	metricsManager := channelScheduler.GetGeminiMetricsManager()
	cfg := cfgManager.GetConfig()
	contextRequirement := common.BuildGeminiContextRequirement(bodyBytes, cfg.ContextRouting)
	common.ApplyAgentModelProfile(contextRequirement, model, cfg)
	common.LogContextEstimate(c, "Gemini", contextRequirement)
	agentRole := ""
	if ac := common.AgentContextFromGin(c); ac != nil {
		agentRole = ac.AgentRole
	}
	common.HandleMultiChannelFailoverWithContextRequirement(
		c,
		envCfg,
		channelScheduler,
		scheduler.ChannelKindGemini,
		"Gemini",
		userID,
		model,
		contextRequirement,
		agentRole,
		func(selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
			upstream := selection.Upstream
			channelIndex := selection.ChannelIndex

			if upstream == nil {
				return common.MultiChannelAttemptResult{}
			}

			baseURLs := upstream.GetAllBaseURLs()
			sortedURLResults := channelScheduler.GetSortedURLsForChannel(scheduler.ChannelKindGemini, channelIndex, baseURLs)

			handled, successKey, successBaseURLIdx, failoverErr, usage, lastErr := common.TryUpstreamWithAllKeys(
				c,
				envCfg,
				cfgManager,
				channelScheduler,
				scheduler.ChannelKindGemini,
				"Gemini",
				metricsManager,
				upstream,
				sortedURLResults,
				bodyBytes,
				contextRequirement,
				isStream,
				func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
					return cfgManager.GetNextGeminiAPIKey(upstream, failedKeys)
				},
				func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
					return buildProviderRequest(c, upstreamCopy, upstreamCopy.BaseURL, apiKey, bodyBytes, geminiReq, model, isStream)
				},
				func(apiKey string) {
					_ = cfgManager.DeprioritizeAPIKey(apiKey)
				},
				func(url string) {
					channelScheduler.MarkURLFailure(scheduler.ChannelKindGemini, channelIndex, url)
				},
				func(url string) {
					channelScheduler.MarkURLSuccess(scheduler.ChannelKindGemini, channelIndex, url)
				},
				func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
					return handleSuccess(c, resp, upstreamCopy.ServiceType, envCfg, startTime, geminiReq, model, isStream, cfgManager.GetFuzzyModeEnabled(), common.ResolveStreamPreflightTimeouts(upstreamCopy, metricsManager.GetCircuitBreakerConfig()))
				},
				model,
				"",
				selection.ChannelIndex,
				channelScheduler.GetChannelLogStore(scheduler.ChannelKindGemini),
			)

			return common.MultiChannelAttemptResult{
				Handled:           handled,
				Attempted:         true,
				SuccessKey:        successKey,
				SuccessBaseURLIdx: successBaseURLIdx,
				FailoverError:     failoverErr,
				Usage:             usage,
				LastError:         lastErr,
			}
		},
		nil,
		func(ctx *gin.Context, failoverErr *common.FailoverError, lastError error) {
			handleAllChannelsFailed(ctx, failoverErr, lastError)
		},
	)
}

// handleSingleChannel 处理单渠道 Gemini 请求
func handleSingleChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	geminiReq *types.GeminiRequest,
	model string,
	isStream bool,
	startTime time.Time,
) {
	upstream, channelIndex, err := cfgManager.GetCurrentGeminiUpstreamWithIndex()
	if err != nil {
		c.JSON(503, types.GeminiError{
			Error: types.GeminiErrorDetail{
				Code:    503,
				Message: "No Gemini upstream configured",
				Status:  "UNAVAILABLE",
			},
		})
		return
	}

	if len(upstream.APIKeys) == 0 {
		c.JSON(503, types.GeminiError{
			Error: types.GeminiErrorDetail{
				Code:    503,
				Message: fmt.Sprintf("No API keys configured for upstream \"%s\"", upstream.Name),
				Status:  "UNAVAILABLE",
			},
		})
		return
	}

	cfg := cfgManager.GetConfig()
	contextRequirement := common.BuildGeminiContextRequirement(bodyBytes, cfg.ContextRouting)
	common.ApplyAgentModelProfile(contextRequirement, model, cfg)
	common.LogContextEstimate(c, "Gemini", contextRequirement)
	if err := channelScheduler.ValidateUpstreamContext(scheduler.ChannelKindGemini, model, upstream, contextRequirement); err != nil {
		c.JSON(400, types.GeminiError{
			Error: types.GeminiErrorDetail{
				Code:    400,
				Message: err.Error(),
				Status:  "INVALID_ARGUMENT",
			},
		})
		return
	}

	metricsManager := channelScheduler.GetGeminiMetricsManager()
	baseURLs := upstream.GetAllBaseURLs()
	urlResults := common.BuildDefaultURLResults(baseURLs)

	handled, _, _, lastFailoverError, _, lastError := common.TryUpstreamWithAllKeys(
		c,
		envCfg,
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindGemini,
		"Gemini",
		metricsManager,
		upstream,
		urlResults,
		bodyBytes,
		contextRequirement,
		isStream,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextGeminiAPIKey(upstream, failedKeys)
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return buildProviderRequest(c, upstreamCopy, upstreamCopy.BaseURL, apiKey, bodyBytes, geminiReq, model, isStream)
		},
		func(apiKey string) {
			_ = cfgManager.DeprioritizeAPIKey(apiKey)
		},
		nil,
		nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			return handleSuccess(c, resp, upstreamCopy.ServiceType, envCfg, startTime, geminiReq, model, isStream, cfgManager.GetFuzzyModeEnabled(), common.ResolveStreamPreflightTimeouts(upstreamCopy, metricsManager.GetCircuitBreakerConfig()))
		},
		model,
		"",
		channelIndex,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindGemini),
	)
	if handled {
		return
	}

	common.RequestLogf(c, "[Gemini-Error] 所有 API密钥都失败了")
	handleAllKeysFailed(c, lastFailoverError, lastError)
}

// transformGeminiPassthroughBody applies the minimal Gemini-native body patch needed
// for per-channel thoughtSignature compatibility while preserving unrelated JSON fields.
func transformGeminiPassthroughBody(bodyBytes []byte, upstream *config.UpstreamConfig) ([]byte, error) {
	if !upstream.StripThoughtSignature && !upstream.InjectDummyThoughtSignature {
		return bodyBytes, nil
	}

	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()

	var data map[string]interface{}
	if err := decoder.Decode(&data); err != nil {
		return nil, err
	}

	walkGeminiParts(data, func(part map[string]interface{}) {
		functionCall, hasFunctionCall := part["functionCall"].(map[string]interface{})
		if upstream.StripThoughtSignature {
			delete(part, "thoughtSignature")
			delete(part, "thought_signature")
			if hasFunctionCall {
				delete(functionCall, "thoughtSignature")
				delete(functionCall, "thought_signature")
			}
			return
		}

		if !hasFunctionCall {
			return
		}
		if hasNonEmptyString(part["thoughtSignature"]) || hasNonEmptyString(part["thought_signature"]) {
			return
		}
		if hasNonEmptyString(functionCall["thoughtSignature"]) || hasNonEmptyString(functionCall["thought_signature"]) {
			return
		}
		part["thoughtSignature"] = types.DummyThoughtSignature
	})

	return utils.MarshalJSONNoEscape(data)
}

func walkGeminiParts(data map[string]interface{}, visit func(map[string]interface{})) {
	contents, ok := data["contents"].([]interface{})
	if !ok {
		return
	}
	for _, rawContent := range contents {
		content, ok := rawContent.(map[string]interface{})
		if !ok {
			continue
		}
		parts, ok := content["parts"].([]interface{})
		if !ok {
			continue
		}
		for _, rawPart := range parts {
			part, ok := rawPart.(map[string]interface{})
			if !ok {
				continue
			}
			visit(part)
		}
	}
}

func hasNonEmptyString(v interface{}) bool {
	s, ok := v.(string)
	return ok && s != ""
}

func stripThoughtSignature(geminiReq *types.GeminiRequest) {
	for i := range geminiReq.Contents {
		for j := range geminiReq.Contents[i].Parts {
			part := &geminiReq.Contents[i].Parts[j]
			if part.FunctionCall != nil {
				part.FunctionCall.ThoughtSignature = types.StripThoughtSignatureMarker
			}
		}
	}
}

// buildProviderRequest 构建上游请求
func buildProviderRequest(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	baseURL string,
	apiKey string,
	bodyBytes []byte,
	geminiReq *types.GeminiRequest,
	model string,
	isStream bool,
) (*http.Request, error) {
	baseURL = strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "#")
	// 应用模型映射
	mappedModel := config.RedirectModel(model, upstream)

	// 使用 context 中的最新请求体（已经过 failover 内的历史图片轮次限制替换等处理）。
	// 若替换后的 body 与原始不同，需同步重新解析 geminiReq，使 claude/openai/responses
	// 等转换分支也使用替换后的数据，而非闭包捕获的原始 geminiReq。
	effectiveBody := common.GetEffectiveRequestBody(c, bodyBytes)
	if !bytes.Equal(effectiveBody, bodyBytes) {
		bodyBytes = effectiveBody
		var reparsed types.GeminiRequest
		if err := json.Unmarshal(effectiveBody, &reparsed); err == nil {
			geminiReq = &reparsed
		}
	}

	var requestBody []byte
	var url string
	var err error

	switch upstream.ServiceType {
	case "gemini":
		// Gemini 上游默认使用原始请求体透传；只有需要 thoughtSignature 兼容时才做最小 JSON patch。
		requestBody, err = transformGeminiPassthroughBody(bodyBytes, upstream)
		if err != nil {
			return nil, err
		}

		action := "generateContent"
		if isStream {
			action = "streamGenerateContent"
		}
		url = fmt.Sprintf("%s/v1beta/models/%s:%s", strings.TrimRight(baseURL, "/"), mappedModel, action)
		if isStream {
			url += "?alt=sse"
		}

	case "claude":
		// Claude 上游：需要转换
		claudeReq, err := converters.GeminiToClaudeRequest(geminiReq, mappedModel)
		if err != nil {
			return nil, err
		}
		claudeReq["stream"] = isStream
		requestBody, err = json.Marshal(claudeReq)
		if err != nil {
			return nil, err
		}
		url = fmt.Sprintf("%s/v1/messages", strings.TrimRight(baseURL, "/"))

	case "openai":
		// OpenAI 上游：需要转换
		openaiReq, err := converters.GeminiToOpenAIRequest(geminiReq, mappedModel)
		if err != nil {
			return nil, err
		}
		openaiReq["stream"] = isStream
		requestBody, err = json.Marshal(openaiReq)
		if err != nil {
			return nil, err
		}
		url = fmt.Sprintf("%s/v1/chat/completions", strings.TrimRight(baseURL, "/"))

	case "responses", "copilot":
		// Responses/Copilot 上游：需要转换
		responsesReq, err := converters.GeminiToResponsesRequest(geminiReq, mappedModel)
		if err != nil {
			return nil, err
		}
		responsesReq["stream"] = isStream
		requestBody, err = json.Marshal(responsesReq)
		if err != nil {
			return nil, err
		}
		if upstream.ServiceType == "copilot" {
			url = fmt.Sprintf("%s/responses", strings.TrimRight(baseURL, "/"))
		} else {
			url = fmt.Sprintf("%s/v1/responses", strings.TrimRight(baseURL, "/"))
		}
		// copilot 使用 token exchange 返回的动态端点，而非渠道静态 baseURL
		if upstream.ServiceType == "copilot" {
			copilotToken, copilotBaseURL, err := copilot.ResolveTokenWithProxy(c.Request.Context(), apiKey, upstream.ProxyURL)
			if err != nil {
				return nil, fmt.Errorf("Copilot token 交换失败: %w", err)
			}
			if copilotBaseURL != "" {
				url = strings.TrimRight(copilotBaseURL, "/") + "/responses"
			}
			req, err := http.NewRequestWithContext(c.Request.Context(), "POST", url, bytes.NewReader(requestBody))
			if err != nil {
				return nil, err
			}
			req.Header = utils.PrepareUpstreamHeaders(c, req.URL.Host)
			req.Header.Set("Content-Type", "application/json")
			copilot.ApplyRuntimeHeaders(req.Header, copilotToken)
			utils.ApplyCustomHeadersProtected(req.Header, upstream.CustomHeaders, utils.CopilotProtectedHeaders)
			copilot.ApplyRuntimeHeaders(req.Header, copilotToken)
			return req, nil
		}

	default:
		// 默认当作 Gemini 处理，保持与 Gemini 上游一致的透传语义。
		requestBody, err = transformGeminiPassthroughBody(bodyBytes, upstream)
		if err != nil {
			return nil, err
		}
		action := "generateContent"
		if isStream {
			action = "streamGenerateContent"
		}
		url = fmt.Sprintf("%s/v1beta/models/%s:%s", strings.TrimRight(baseURL, "/"), mappedModel, action)
		if isStream {
			url += "?alt=sse"
		}
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), "POST", url, bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}

	// 使用统一的头部处理逻辑（透明代理）
	// 保留客户端的大部分 headers，只移除/替换必要的认证和代理相关 headers
	req.Header = utils.PrepareUpstreamHeaders(c, req.URL.Host)

	// 设置 Content-Type（覆盖可能来自客户端的值）
	req.Header.Set("Content-Type", "application/json")

	// 设置认证头
	switch upstream.ServiceType {
	case "gemini":
		if utils.HasAuthenticationHeaderOverride(upstream.AuthHeader) {
			utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, upstream.AuthHeader)
		} else {
			utils.SetGeminiAuthenticationHeader(req.Header, apiKey)
		}
	case "claude":
		utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, upstream.AuthHeader)
		req.Header.Set("anthropic-version", "2023-06-01")
	case "openai", "responses":
		utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, upstream.AuthHeader)
	default:
		if utils.HasAuthenticationHeaderOverride(upstream.AuthHeader) {
			utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, upstream.AuthHeader)
		} else {
			utils.SetGeminiAuthenticationHeader(req.Header, apiKey)
		}
	}

	// 应用自定义请求头
	utils.ApplyCustomHeaders(req.Header, upstream.CustomHeaders)

	return req, nil
}

// handleSuccess 处理成功的响应
func handleSuccess(
	c *gin.Context,
	resp *http.Response,
	upstreamType string,
	envCfg *config.EnvConfig,
	startTime time.Time,
	geminiReq *types.GeminiRequest,
	model string,
	isStream bool,
	fuzzyMode bool,
	timeouts common.StreamPreflightTimeouts,
) (*types.Usage, error) {
	defer resp.Body.Close()

	// copilot 上游使用 Responses 协议，响应转换复用 responses 分支
	if upstreamType == "copilot" {
		upstreamType = "responses"
	}

	if isStream {
		return handleStreamSuccess(c, resp, upstreamType, envCfg, startTime, model, timeouts)
	}

	// 非流式响应处理
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(500, types.GeminiError{
			Error: types.GeminiErrorDetail{
				Code:    500,
				Message: "Failed to read response",
				Status:  "INTERNAL",
			},
		})
		return nil, err
	}

	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		common.RequestLogf(c, "[Gemini-Timing] 响应完成: %dms, 状态: %d", responseTime, resp.StatusCode)
		common.LogUpstreamResponse(c, resp, bodyBytes, envCfg, "Gemini")
	}

	// 根据上游类型转换响应
	var geminiResp *types.GeminiResponse

	switch upstreamType {
	case "gemini":
		// 直接解析 Gemini 响应
		if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
			preview := bodyBytes
			if len(preview) > 100 {
				preview = preview[:100]
			}
			common.RequestLogf(c, "[Gemini-InvalidBody] 响应体解析失败: %v, body前100字节: %s", err, preview)
			return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
		}

	case "claude":
		// 转换 Claude 响应为 Gemini 格式
		var claudeResp map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &claudeResp); err != nil {
			preview := bodyBytes
			if len(preview) > 100 {
				preview = preview[:100]
			}
			common.RequestLogf(c, "[Gemini-InvalidBody] Claude响应体解析失败: %v, body前100字节: %s", err, preview)
			return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
		}
		geminiResp, err = converters.ClaudeResponseToGemini(claudeResp)
		if err != nil {
			common.RequestLogf(c, "[Gemini-InvalidBody] Claude响应转换失败: %v", err)
			return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
		}

	case "openai":
		// 转换 OpenAI 响应为 Gemini 格式
		var openaiResp map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &openaiResp); err != nil {
			preview := bodyBytes
			if len(preview) > 100 {
				preview = preview[:100]
			}
			common.RequestLogf(c, "[Gemini-InvalidBody] OpenAI响应体解析失败: %v, body前100字节: %s", err, preview)
			return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
		}
		geminiResp, err = converters.OpenAIResponseToGemini(openaiResp)
		if err != nil {
			common.RequestLogf(c, "[Gemini-InvalidBody] OpenAI响应转换失败: %v", err)
			return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
		}

	case "responses":
		// 转换 Responses 响应为 Gemini 格式
		var responsesResp map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &responsesResp); err != nil {
			preview := bodyBytes
			if len(preview) > 100 {
				preview = preview[:100]
			}
			common.RequestLogf(c, "[Gemini-InvalidBody] Responses响应体解析失败: %v, body前100字节: %s", err, preview)
			return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
		}
		geminiResp, err = converters.ResponsesResponseToGemini(responsesResp)
		if err != nil {
			common.RequestLogf(c, "[Gemini-InvalidBody] Responses响应转换失败: %v", err)
			return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
		}

	default:
		// 默认直接透传，避免非必要整包读入内存
		return nil, common.PassthroughResponse(c, resp)
	}

	// 空响应拦截（仅 Fuzzy 模式）：上游 200 但 candidates 语义为空，
	// Header 未发送，可安全 failover 到下一个 Key/BaseURL/渠道
	if fuzzyMode && common.IsGeminiResponseEmpty(geminiResp) {
		common.RequestLogf(c, "[Gemini-EmptyResponse] 上游返回空响应（非流式，upstreamType=%s），触发 failover", upstreamType)
		return nil, common.ErrEmptyNonStreamResponse
	}

	// 返回 Gemini 格式响应
	respBytes, err := json.Marshal(geminiResp)
	if err != nil {
		c.Data(resp.StatusCode, "application/json", bodyBytes)
		return nil, nil
	}

	c.Data(resp.StatusCode, "application/json", respBytes)

	// 提取 usage 统计
	var usage *types.Usage
	if geminiResp.UsageMetadata != nil {
		usage = &types.Usage{
			InputTokens:  geminiResp.UsageMetadata.PromptTokenCount - geminiResp.UsageMetadata.CachedContentTokenCount,
			OutputTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
		}
	}

	return usage, nil
}

// handleAllChannelsFailed 处理所有渠道失败的情况
func handleAllChannelsFailed(c *gin.Context, failoverErr *common.FailoverError, lastError error) {
	if failoverErr != nil {
		c.Data(failoverErr.Status, "application/json", failoverErr.Body)
		return
	}

	errMsg := "All channels failed"
	if lastError != nil {
		errMsg = lastError.Error()
	}

	c.JSON(503, types.GeminiError{
		Error: types.GeminiErrorDetail{
			Code:    503,
			Message: errMsg,
			Status:  "UNAVAILABLE",
		},
	})
}

// handleAllKeysFailed 处理所有 Key 失败的情况
func handleAllKeysFailed(c *gin.Context, failoverErr *common.FailoverError, lastError error) {
	if failoverErr != nil {
		c.Data(failoverErr.Status, "application/json", failoverErr.Body)
		return
	}

	errMsg := "All API keys failed"
	if lastError != nil {
		errMsg = lastError.Error()
	}

	c.JSON(503, types.GeminiError{
		Error: types.GeminiErrorDetail{
			Code:    503,
			Message: errMsg,
			Status:  "UNAVAILABLE",
		},
	})
}
