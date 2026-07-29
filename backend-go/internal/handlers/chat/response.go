package chat

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/converters"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// normalizeResponseUpstreamType 将 copilot 归一化为 responses，以便复用 Responses 协议的响应转换分支。
func normalizeResponseUpstreamType(t string) string {
	if t == "copilot" {
		return "responses"
	}
	return t
}

func handleSuccess(
	c *gin.Context,
	resp *http.Response,
	upstreamType string,
	envCfg *config.EnvConfig,
	startTime time.Time,
	model string,
	isStream bool,
	timeouts common.StreamPreflightTimeouts,
) (*types.Usage, error) {
	defer errutil.IgnoreDeferred(resp.Body.Close)

	// copilot 上游使用 Responses 协议，响应转换复用 responses 分支
	upstreamType = normalizeResponseUpstreamType(upstreamType)

	if isStream {
		return handleStreamSuccess(c, resp, upstreamType, envCfg, startTime, model, timeouts)
	}

	// 非流式响应处理
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		chatErrorResponse(c, 500, "Failed to read response", "server_error")
		return nil, err
	}

	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		common.RequestLogf(c, "[Chat-Timing] 响应完成: %dms, 状态: %d", responseTime, resp.StatusCode)
		common.LogUpstreamResponse(c, resp, bodyBytes, envCfg, "Chat")
	}

	switch upstreamType {
	case "claude":
		// 转换 Claude 响应为 OpenAI Chat 格式
		var claudeResp map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &claudeResp); err != nil {
			return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
		}
		// 空响应拦截：在原生 Claude 结构上判空，避免
		// convertClaudeResponseToChat 丢失 server_tool_use / redacted_thinking
		// 等语义块导致的误判。Header 未发送，可安全 failover。
		var claudeTyped types.ClaudeResponse
		if err := json.Unmarshal(bodyBytes, &claudeTyped); err == nil && common.IsClaudeResponseEmpty(&claudeTyped) {
			common.RequestLogf(c, "[Chat-EmptyResponse] 上游返回空响应（非流式，upstreamType=%s），触发 failover", upstreamType)
			return nil, common.ErrEmptyNonStreamResponse
		}
		openaiResp := convertClaudeResponseToChat(claudeResp, model)
		respBytes, err := json.Marshal(openaiResp)
		if err != nil {
			c.Data(resp.StatusCode, "application/json", bodyBytes)
			return nil, nil
		}
		c.Data(resp.StatusCode, "application/json", respBytes)

		// 提取 usage
		var usage *types.Usage
		if u, ok := claudeResp["usage"].(map[string]interface{}); ok {
			inputTokens, _ := u["input_tokens"].(float64)
			outputTokens, _ := u["output_tokens"].(float64)
			usage = &types.Usage{
				InputTokens:  int(inputTokens),
				OutputTokens: int(outputTokens),
			}
		}
		return usage, nil

	case "responses":
		// 转换 Responses 响应为 OpenAI Chat 格式
		chatRespBytes := converters.ConvertResponsesResponseToChatResponse(bodyBytes, model)
		// 空响应拦截
		var emptyCheckMap map[string]interface{}
		if err := json.Unmarshal(chatRespBytes, &emptyCheckMap); err == nil && common.IsChatResponseEmpty(emptyCheckMap) {
			common.RequestLogf(c, "[Chat-EmptyResponse] 上游返回空响应（非流式，upstreamType=%s），触发 failover", upstreamType)
			return nil, common.ErrEmptyNonStreamResponse
		}
		c.Data(resp.StatusCode, "application/json", chatRespBytes)
		// 提取 usage（Responses 格式：input_tokens / output_tokens）
		var usage *types.Usage
		var respMap map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &respMap); err == nil {
			if u, ok := respMap["usage"].(map[string]interface{}); ok {
				inputTokens, _ := u["input_tokens"].(float64)
				outputTokens, _ := u["output_tokens"].(float64)
				usage = &types.Usage{
					InputTokens:  int(inputTokens),
					OutputTokens: int(outputTokens),
				}
			}
		}
		return usage, nil

	default:
		// 先解析以判断空响应；再决定是 failover 还是透传
		var respMap map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &respMap); err != nil {
			// JSON 不可解析：维持原 ErrInvalidResponseBody 语义
			return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
		}
		// default 分支承接 gemini 等没有专属 case 的透传上游类型。IsChatResponseEmpty
		// 只认识 OpenAI 的 choices[].message 结构；直接套用到 Gemini 的 candidates 结构上，
		// respMap["choices"] 断言必然失败、被判定为空，导致所有合法 Gemini 响应都误触发 failover。
		// 因此按响应体是否具备 candidates 字段（Gemini 专属、非 omitempty）区分两种 schema；
		// 不用 usageMetadata 判断，因为该字段是 omitempty，缺省时会误判成 OpenAI 形态。
		if _, isGeminiShaped := respMap["candidates"]; isGeminiShaped {
			var geminiResp types.GeminiResponse
			if err := json.Unmarshal(bodyBytes, &geminiResp); err == nil && common.IsGeminiResponseEmpty(&geminiResp) {
				common.RequestLogf(c, "[Chat-EmptyResponse] 上游返回空响应（非流式，upstreamType=%s），触发 failover", upstreamType)
				return nil, common.ErrEmptyNonStreamResponse
			}
		} else if common.IsChatResponseEmpty(respMap) {
			common.RequestLogf(c, "[Chat-EmptyResponse] 上游返回空响应（非流式，upstreamType=%s），触发 failover", upstreamType)
			return nil, common.ErrEmptyNonStreamResponse
		}
		// 透传原始响应体（保留上游字段，避免 marshal 丢失）
		utils.ForwardResponseHeaders(resp.Header, c.Writer)
		c.Data(resp.StatusCode, "application/json", bodyBytes)
		if u, ok := respMap["usage"].(map[string]interface{}); ok {
			promptTokens, _ := u["prompt_tokens"].(float64)
			completionTokens, _ := u["completion_tokens"].(float64)
			return &types.Usage{
				InputTokens:  int(promptTokens),
				OutputTokens: int(completionTokens),
			}, nil
		}
		if u, ok := respMap["usageMetadata"].(map[string]interface{}); ok {
			promptTokens, _ := u["promptTokenCount"].(float64)
			completionTokens, _ := u["candidatesTokenCount"].(float64)
			return &types.Usage{
				InputTokens:  int(promptTokens),
				OutputTokens: int(completionTokens),
			}, nil
		}
		return nil, nil
	}
}

// convertClaudeResponseToChat 将 Claude 非流式响应转换为 OpenAI Chat 格式
func convertClaudeResponseToChat(claudeResp map[string]interface{}, model string) map[string]interface{} {
	// 提取文本内容和 tool_use blocks
	var text string
	var reasoningParts []string
	var toolCalls []map[string]interface{}
	toolCallIndex := 0

	if content, ok := claudeResp["content"].([]interface{}); ok {
		for _, block := range content {
			b, ok := block.(map[string]interface{})
			if !ok {
				continue
			}
			blockType, _ := b["type"].(string)
			switch blockType {
			case "thinking":
				if thinking, ok := b["thinking"].(string); ok && thinking != "" {
					reasoningParts = append(reasoningParts, thinking)
				}
			case "text":
				if t, ok := b["text"].(string); ok {
					text += t
				}
			case "tool_use":
				// Claude tool_use → OpenAI tool_calls
				toolID, _ := b["id"].(string)
				toolName, _ := b["name"].(string)
				inputRaw, _ := json.Marshal(b["input"])
				toolCalls = append(toolCalls, map[string]interface{}{
					"index": toolCallIndex,
					"id":    toolID,
					"type":  "function",
					"function": map[string]interface{}{
						"name":      toolName,
						"arguments": string(inputRaw),
					},
				})
				toolCallIndex++
			default:
				// 其他类型（如 image）提取 text 字段（如有）
				if t, ok := b["text"].(string); ok {
					text += t
				}
			}
		}
	}

	// 映射 stop_reason
	finishReason := "stop"
	if stopReason, ok := claudeResp["stop_reason"].(string); ok {
		switch stopReason {
		case "max_tokens":
			finishReason = "length"
		case "tool_use":
			finishReason = "tool_calls"
		default: // end_turn, stop_sequence
			finishReason = "stop"
		}
	}

	// 构建 message
	message := map[string]interface{}{
		"role": "assistant",
	}
	if text != "" {
		message["content"] = text
	} else {
		message["content"] = nil
	}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}
	if len(reasoningParts) > 0 {
		message["reasoning_content"] = strings.Join(reasoningParts, "\n")
	}

	// 构建 OpenAI Chat 格式响应
	result := map[string]interface{}{
		"id":      claudeResp["id"],
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]interface{}{
			{
				"index":         0,
				"message":       message,
				"finish_reason": finishReason,
			},
		},
	}

	// 转换 usage
	if u, ok := claudeResp["usage"].(map[string]interface{}); ok {
		inputTokens, _ := u["input_tokens"].(float64)
		outputTokens, _ := u["output_tokens"].(float64)
		result["usage"] = map[string]interface{}{
			"prompt_tokens":     int(inputTokens),
			"completion_tokens": int(outputTokens),
			"total_tokens":      int(inputTokens + outputTokens),
		}
	}

	return result
}
