package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/converters"
	"github.com/BenedictKing/ccx/internal/copilot"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

func countChatUserMessages(req map[string]interface{}) int {
	messages, ok := req["messages"].([]interface{})
	if !ok {
		return 0
	}
	count := 0
	for _, item := range messages {
		msg, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if role, _ := msg["role"].(string); role != "user" {
			continue
		}
		if content, exists := msg["content"]; exists && content != nil {
			count++
		}
	}
	return count
}

func buildChatCompletionRequestBody(
	bodyBytes []byte,
	model string,
	mappedModel string,
	upstream *config.UpstreamConfig,
	includeAdvancedOptions bool,
) ([]byte, error) {
	normalizeRoles := upstream.IsNormalizeNonstandardChatRolesEnabled()
	downgradeDeveloperRole := upstream.IsDowngradeDeveloperRoleEnabled()
	stripImageTool := upstream.IsStripImageGenerationToolEnabled()
	needsRewrite := includeAdvancedOptions || mappedModel != model || normalizeRoles || downgradeDeveloperRole || stripImageTool
	if !needsRewrite {
		return bodyBytes, nil
	}

	var reqMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &reqMap); err != nil {
		return nil, err
	}

	reqMap["model"] = mappedModel
	if stripImageTool {
		stripImageGenerationFromChatTools(reqMap)
	}

	if includeAdvancedOptions {
		if effort := config.ResolveReasoningEffort(model, upstream); effort != "" {
			config.ApplyReasoningParamStyle(reqMap, upstream.ReasoningParamStyle, effort)
		}
		if upstream.TextVerbosity != "" {
			reqMap["text"] = map[string]interface{}{"verbosity": upstream.TextVerbosity}
		}
		if upstream.FastMode {
			reqMap["service_tier"] = "priority"
		}
	}

	if normalizeRoles {
		// 用户显式开启的宽泛归一化：所有非标准 role 一律降为 user（既有契约）
		converters.NormalizeNonstandardChatRolesInRequest(reqMap)
	} else if downgradeDeveloperRole {
		// 自动学习到的窄改写：只把 developer 降为 system
		converters.DowngradeDeveloperRoleToSystem(reqMap)
	}
	providers.ApplyNativeToolStreaming(reqMap, upstream)

	return json.Marshal(reqMap)
}

func stripImageGenerationFromChatTools(reqMap map[string]interface{}) {
	rawTools, ok := reqMap["tools"].([]interface{})
	if !ok || len(rawTools) == 0 {
		return
	}

	kept := make([]interface{}, 0, len(rawTools))
	removed := 0
	for _, item := range rawTools {
		if providers.IsImageGenerationToolEntry(item) {
			removed++
			continue
		}
		kept = append(kept, item)
	}

	if removed == 0 {
		return
	}
	if len(kept) == 0 {
		delete(reqMap, "tools")
		delete(reqMap, "tool_choice")
		delete(reqMap, "parallel_tool_calls")
		return
	}
	reqMap["tools"] = kept
}

func extractRequestModel(bodyBytes []byte, fallback string) string {
	var reqMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &reqMap); err != nil {
		return fallback
	}
	model, _ := reqMap["model"].(string)
	model = strings.TrimSpace(model)
	if model == "" {
		return fallback
	}
	return model
}

// buildProviderRequest 构建上游请求

func stripThinkingBlocksFromBody(bodyBytes []byte) []byte {
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.UseNumber()

	var data map[string]interface{}
	if err := decoder.Decode(&data); err != nil {
		return bodyBytes
	}

	messages, ok := data["messages"].([]interface{})
	if !ok {
		return bodyBytes
	}

	modified := false
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]interface{})
		if !ok {
			continue
		}

		content, ok := msgMap["content"].([]interface{})
		if !ok {
			continue
		}

		filtered := make([]interface{}, 0, len(content))
		for _, block := range content {
			blockMap, ok := block.(map[string]interface{})
			if !ok {
				filtered = append(filtered, block)
				continue
			}
			blockType, _ := blockMap["type"].(string)
			if blockType == "thinking" || blockType == "redacted_thinking" {
				modified = true
				continue
			}
			filtered = append(filtered, block)
		}

		msgMap["content"] = filtered
	}

	if !modified {
		return bodyBytes
	}

	newBytes, err := json.Marshal(data)
	if err != nil {
		return bodyBytes
	}
	return newBytes
}

func buildProviderRequest(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	baseURL string,
	apiKey string,
	bodyBytes []byte,
	model string,
	isStream bool,
) (*http.Request, error) {
	skipVersionPrefix := strings.HasSuffix(baseURL, "#")
	baseURL = strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "#")
	// 应用模型映射
	effectiveModel := extractRequestModel(bodyBytes, model)
	mappedModel := config.RedirectModel(effectiveModel, upstream)

	var requestBody []byte
	var url string

	switch upstream.ServiceType {
	case "openai", "":
		// OpenAI 兼容上游：透传请求，仅替换 model 并注入高级参数
		var err error
		requestBody, err = buildChatCompletionRequestBody(bodyBytes, effectiveModel, mappedModel, upstream, true)
		if err != nil {
			return nil, err
		}
		// Gemini 兼容端点配置为 openai serviceType 时，也需要注入 thought_signature
		if strings.Contains(baseURL, "generativelanguage.googleapis.com") && !upstream.StripThoughtSignature {
			requestBody = injectGeminiThoughtSignatures(requestBody)
		}
		if skipVersionPrefix {
			url = fmt.Sprintf("%s/chat/completions", strings.TrimRight(baseURL, "/"))
		} else {
			url = fmt.Sprintf("%s/v1/chat/completions", strings.TrimRight(baseURL, "/"))
		}

	case "responses", "copilot":
		// Responses/Copilot 上游：转换 Chat 格式为 Responses 格式
		chatBody, err := buildChatCompletionRequestBody(bodyBytes, effectiveModel, mappedModel, upstream, true)
		if err != nil {
			return nil, err
		}
		requestBody = converters.ConvertChatRequestToResponsesRequest(chatBody)
		if upstream.ServiceType == "copilot" || skipVersionPrefix {
			url = fmt.Sprintf("%s/responses", strings.TrimRight(baseURL, "/"))
		} else {
			url = fmt.Sprintf("%s/v1/responses", strings.TrimRight(baseURL, "/"))
		}
		// copilot 使用 token exchange 返回的动态端点，而非渠道静态 baseURL
		if upstream.ServiceType == "copilot" {
			copilotToken, copilotBaseURL, err := copilot.ResolveTokenWithProxy(c.Request.Context(), apiKey, upstream.ProxyURL)
			if err != nil {
				return nil, fmt.Errorf("copilot token 交换失败: %w", err)
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

	case "claude":
		// Claude 上游：转换 OpenAI Chat 格式为 Claude Messages 格式
		claudeReq, err := convertChatToClaudeRequest(bodyBytes, mappedModel, isStream)
		if err != nil {
			return nil, err
		}
		requestBody, err = json.Marshal(claudeReq)
		if err != nil {
			return nil, err
		}
		if !upstream.IsPassbackReasoningContentEnabled() && !upstream.IsPassbackThinkingBlocksEnabled() {
			requestBody = stripThinkingBlocksFromBody(requestBody)
		}
		if skipVersionPrefix {
			url = fmt.Sprintf("%s/messages", strings.TrimRight(baseURL, "/"))
		} else {
			url = fmt.Sprintf("%s/v1/messages", strings.TrimRight(baseURL, "/"))
		}

	case "gemini":
		// Gemini 上游：透传为 OpenAI Chat 格式（大部分 Gemini 兼容端点支持 OpenAI 格式）
		var err error
		requestBody, err = buildChatCompletionRequestBody(bodyBytes, effectiveModel, mappedModel, upstream, false)
		if err != nil {
			return nil, err
		}
		// Gemini 3 要求 tool_calls 中包含 thought_signature，注入 dummy 值跳过验证
		// 尊重 stripThoughtSignature 配置：如果渠道明确要求移除 signature 则跳过注入
		if !upstream.StripThoughtSignature {
			requestBody = injectGeminiThoughtSignatures(requestBody)
		}
		if skipVersionPrefix {
			url = fmt.Sprintf("%s/chat/completions", strings.TrimRight(baseURL, "/"))
		} else {
			url = fmt.Sprintf("%s/v1/chat/completions", strings.TrimRight(baseURL, "/"))
		}

	default:
		// 默认当作 OpenAI 兼容处理
		var err error
		requestBody, err = buildChatCompletionRequestBody(bodyBytes, effectiveModel, mappedModel, upstream, false)
		if err != nil {
			return nil, err
		}
		if skipVersionPrefix {
			url = fmt.Sprintf("%s/chat/completions", strings.TrimRight(baseURL, "/"))
		} else {
			url = fmt.Sprintf("%s/v1/chat/completions", strings.TrimRight(baseURL, "/"))
		}
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), "POST", url, bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}

	// 使用统一的头部处理逻辑（透明代理）
	req.Header = utils.PrepareUpstreamHeaders(c, req.URL.Host)

	// 设置 Content-Type
	req.Header.Set("Content-Type", "application/json")

	// 设置认证头
	switch upstream.ServiceType {
	case "claude":
		utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, upstream.AuthHeader)
		req.Header.Set("anthropic-version", "2023-06-01")
	default:
		utils.SetAuthenticationHeaderWithOverride(req.Header, apiKey, upstream.AuthHeader)
	}

	// 应用自定义请求头
	utils.ApplyCustomHeaders(req.Header, upstream.CustomHeaders)

	return req, nil
}

// convertChatToClaudeRequest 将 OpenAI Chat 请求转换为 Claude Messages 格式
func convertChatToClaudeRequest(bodyBytes []byte, model string, isStream bool) (map[string]interface{}, error) {
	var reqMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &reqMap); err != nil {
		return nil, err
	}

	claudeReq := map[string]interface{}{
		"model":  model,
		"stream": isStream,
	}

	// 转换 max_tokens
	if maxTokens, ok := reqMap["max_tokens"]; ok {
		claudeReq["max_tokens"] = maxTokens
	} else if maxCompletionTokens, ok := reqMap["max_completion_tokens"]; ok {
		claudeReq["max_tokens"] = maxCompletionTokens
	} else {
		claudeReq["max_tokens"] = 4096
	}

	// 转换 temperature
	if temp, ok := reqMap["temperature"]; ok {
		claudeReq["temperature"] = temp
	}

	// 转换 top_p
	if topP, ok := reqMap["top_p"]; ok {
		claudeReq["top_p"] = topP
	}

	// 转换 messages：提取 system 消息，其余转为 Claude 格式
	if messages, ok := reqMap["messages"].([]interface{}); ok {
		var claudeMessages []map[string]interface{}
		var systemParts []string

		for _, msg := range messages {
			m, ok := msg.(map[string]interface{})
			if !ok {
				continue
			}
			role, _ := m["role"].(string)
			content := m["content"]

			switch role {
			case "system":
				if text, ok := content.(string); ok {
					systemParts = append(systemParts, text)
				}
			case "user":
				claudeMessages = append(claudeMessages, map[string]interface{}{
					"role":    "user",
					"content": content,
				})
			case "assistant":
				// 检查是否包含 tool_calls（OpenAI → Claude tool_use）
				if toolCalls, ok := m["tool_calls"].([]interface{}); ok && len(toolCalls) > 0 {
					var contentBlocks []map[string]interface{}
					if reasoning, ok := m["reasoning_content"].(string); ok && reasoning != "" {
						contentBlocks = append(contentBlocks, map[string]interface{}{
							"type":     "thinking",
							"thinking": reasoning,
						})
					}
					// 先添加文本内容（如有）
					if text, ok := content.(string); ok && text != "" {
						contentBlocks = append(contentBlocks, map[string]interface{}{
							"type": "text",
							"text": text,
						})
					}
					// 转换 tool_calls → tool_use blocks
					for _, tc := range toolCalls {
						tcMap, ok := tc.(map[string]interface{})
						if !ok {
							continue
						}
						fn, _ := tcMap["function"].(map[string]interface{})
						toolID, _ := tcMap["id"].(string)
						toolName, _ := fn["name"].(string)
						argsStr, _ := fn["arguments"].(string)
						var argsObj interface{}
						if json.Unmarshal([]byte(argsStr), &argsObj) != nil {
							argsObj = map[string]interface{}{}
						}
						contentBlocks = append(contentBlocks, map[string]interface{}{
							"type":  "tool_use",
							"id":    toolID,
							"name":  toolName,
							"input": argsObj,
						})
					}
					claudeMessages = append(claudeMessages, map[string]interface{}{
						"role":    "assistant",
						"content": contentBlocks,
					})
				} else {
					if reasoning, ok := m["reasoning_content"].(string); ok && reasoning != "" {
						var contentBlocks []map[string]interface{}
						contentBlocks = append(contentBlocks, map[string]interface{}{
							"type":     "thinking",
							"thinking": reasoning,
						})
						if text, ok := content.(string); ok && text != "" {
							contentBlocks = append(contentBlocks, map[string]interface{}{
								"type": "text",
								"text": text,
							})
						}
						claudeMessages = append(claudeMessages, map[string]interface{}{
							"role":    "assistant",
							"content": contentBlocks,
						})
						continue
					}
					claudeMessages = append(claudeMessages, map[string]interface{}{
						"role":    "assistant",
						"content": content,
					})
				}
			case "tool":
				// OpenAI tool result → Claude tool_result（作为 user 消息）
				toolCallID, _ := m["tool_call_id"].(string)
				contentStr := ""
				if s, ok := content.(string); ok {
					contentStr = s
				}
				claudeMessages = append(claudeMessages, map[string]interface{}{
					"role": "user",
					"content": []map[string]interface{}{
						{
							"type":        "tool_result",
							"tool_use_id": toolCallID,
							"content":     contentStr,
						},
					},
				})
			default:
				claudeMessages = append(claudeMessages, map[string]interface{}{
					"role":    "user",
					"content": content,
				})
			}
		}

		if len(systemParts) > 0 {
			claudeReq["system"] = strings.Join(systemParts, "\n\n")
		}
		claudeReq["messages"] = claudeMessages
	}

	// 转换 tools：OpenAI function → Claude tools
	if userID, ok := reqMap["user_id"].(string); ok && userID != "" {
		claudeReq["metadata"] = map[string]interface{}{"user_id": userID}
	}

	if tools, ok := reqMap["tools"].([]interface{}); ok && len(tools) > 0 {
		var claudeTools []map[string]interface{}
		for _, tool := range tools {
			t, ok := tool.(map[string]interface{})
			if !ok {
				continue
			}
			fn, ok := t["function"].(map[string]interface{})
			if !ok {
				continue
			}
			claudeTool := map[string]interface{}{
				"name": fn["name"],
			}
			if desc, ok := fn["description"]; ok {
				claudeTool["description"] = desc
			}
			if params, ok := fn["parameters"]; ok {
				claudeTool["input_schema"] = params
			} else {
				claudeTool["input_schema"] = map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				}
			}
			claudeTools = append(claudeTools, claudeTool)
		}
		if len(claudeTools) > 0 {
			claudeReq["tools"] = claudeTools
		}
	}

	return claudeReq, nil
}
