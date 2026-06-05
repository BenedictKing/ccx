// Package chat 提供 Chat Completions API 的代理处理器
package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/converters"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// Handler Chat Completions API 代理处理器
// 支持多渠道调度：当配置多个渠道时自动启用
func Handler(
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
) gin.HandlerFunc {
	return gin.HandlerFunc(func(c *gin.Context) {
		// Chat 代理端点统一使用代理访问密钥鉴权（x-api-key / Authorization: Bearer）
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

		// 解析请求中的关键字段
		var reqMap map[string]interface{}
		if len(bodyBytes) > 0 {
			if err := json.Unmarshal(bodyBytes, &reqMap); err != nil {
				c.JSON(400, gin.H{
					"error": gin.H{
						"message": fmt.Sprintf("Invalid request body: %v", err),
						"type":    "invalid_request_error",
						"code":    "invalid_json",
					},
				})
				return
			}
		}

		// 从请求体提取 model
		model, _ := reqMap["model"].(string)
		if model == "" {
			c.JSON(400, gin.H{
				"error": gin.H{
					"message": "model is required",
					"type":    "invalid_request_error",
					"code":    "missing_parameter",
				},
			})
			return
		}

		// 从请求体提取 stream（默认 false）
		isStream, _ := reqMap["stream"].(bool)

		// 提取统一会话标识用于 Trace 亲和性
		userID := utils.ExtractUnifiedSessionID(c, bodyBytes)

		// 预处理：清理空 signature 字段，预防上游参数校验 400
		bodyBytes, modified := common.RemoveEmptySignatures(bodyBytes, envCfg.EnableRequestLogs, "Chat")
		_ = modified

		// 预处理：清理历史 thinking 内容块/字段，预防上游参数校验 400
		bodyBytes, thinkingModified := common.SanitizeMalformedThinkingBlocks(bodyBytes, envCfg.EnableRequestLogs, "Chat")
		_ = thinkingModified

		// 记录原始请求信息
		common.LogOriginalRequest(c, bodyBytes, envCfg, "Chat")

		// 检查是否为多渠道模式
		isMultiChannel := channelScheduler.IsMultiChannelMode(scheduler.ChannelKindChat)

		if isMultiChannel {
			handleMultiChannel(c, envCfg, cfgManager, channelScheduler, bodyBytes, model, isStream, userID, startTime)
		} else {
			handleSingleChannel(c, envCfg, cfgManager, channelScheduler, bodyBytes, model, isStream, startTime)
		}
	})
}

// handleMultiChannel 处理多渠道 Chat 请求
func handleMultiChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	model string,
	isStream bool,
	userID string,
	startTime time.Time,
) {
	metricsManager := channelScheduler.GetChatMetricsManager()
	common.HandleMultiChannelFailover(
		c,
		envCfg,
		channelScheduler,
		scheduler.ChannelKindChat,
		"Chat",
		userID,
		model,
		func(selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
			upstream := selection.Upstream
			channelIndex := selection.ChannelIndex

			if upstream == nil {
				return common.MultiChannelAttemptResult{}
			}

			baseURLs := upstream.GetAllBaseURLs()
			sortedURLResults := channelScheduler.GetSortedURLsForChannel(scheduler.ChannelKindChat, channelIndex, baseURLs)

			handled, successKey, successBaseURLIdx, failoverErr, usage, lastErr := common.TryUpstreamWithAllKeys(
				c,
				envCfg,
				cfgManager,
				channelScheduler,
				scheduler.ChannelKindChat,
				"Chat",
				metricsManager,
				upstream,
				sortedURLResults,
				bodyBytes,
				isStream,
				func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
					return cfgManager.GetNextChatAPIKey(upstream, failedKeys)
				},
				func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
					return buildProviderRequest(c, upstreamCopy, upstreamCopy.BaseURL, apiKey, bodyBytes, model, isStream)
				},
				func(apiKey string) {
					_ = cfgManager.DeprioritizeAPIKey(apiKey)
				},
				func(url string) {
					channelScheduler.MarkURLFailure(scheduler.ChannelKindChat, channelIndex, url)
				},
				func(url string) {
					channelScheduler.MarkURLSuccess(scheduler.ChannelKindChat, channelIndex, url)
				},
				func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
					return handleSuccess(c, resp, upstreamCopy.ServiceType, envCfg, startTime, model, isStream, cfgManager.GetFuzzyModeEnabled())
				},
				model,
				"",
				selection.ChannelIndex,
				channelScheduler.GetChannelLogStore(scheduler.ChannelKindChat),
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

// handleSingleChannel 处理单渠道 Chat 请求
func handleSingleChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	model string,
	isStream bool,
	startTime time.Time,
) {
	upstream, channelIndex, err := cfgManager.GetCurrentChatUpstreamWithIndex()
	if err != nil {
		chatErrorResponse(c, 503, "No Chat upstream configured", "service_unavailable")
		return
	}

	if len(upstream.APIKeys) == 0 {
		chatErrorResponse(c, 503, fmt.Sprintf("No API keys configured for upstream \"%s\"", upstream.Name), "service_unavailable")
		return
	}

	metricsManager := channelScheduler.GetChatMetricsManager()
	baseURLs := upstream.GetAllBaseURLs()
	urlResults := common.BuildDefaultURLResults(baseURLs)

	handled, _, _, lastFailoverError, _, lastError := common.TryUpstreamWithAllKeys(
		c,
		envCfg,
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindChat,
		"Chat",
		metricsManager,
		upstream,
		urlResults,
		bodyBytes,
		isStream,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextChatAPIKey(upstream, failedKeys)
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return buildProviderRequest(c, upstreamCopy, upstreamCopy.BaseURL, apiKey, bodyBytes, model, isStream)
		},
		func(apiKey string) {
			_ = cfgManager.DeprioritizeAPIKey(apiKey)
		},
		nil,
		nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			return handleSuccess(c, resp, upstreamCopy.ServiceType, envCfg, startTime, model, isStream, cfgManager.GetFuzzyModeEnabled())
		},
		model,
		"",
		channelIndex,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindChat),
	)
	if handled {
		return
	}

	log.Printf("[Chat-Error] 所有 API密钥都失败了")
	handleAllKeysFailed(c, lastFailoverError, lastError)
}

func buildChatCompletionRequestBody(
	bodyBytes []byte,
	model string,
	mappedModel string,
	upstream *config.UpstreamConfig,
	includeAdvancedOptions bool,
) ([]byte, error) {
	needsRewrite := includeAdvancedOptions || mappedModel != model || upstream.NormalizeNonstandardChatRoles
	if !needsRewrite {
		return bodyBytes, nil
	}

	var reqMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &reqMap); err != nil {
		return nil, err
	}

	reqMap["model"] = mappedModel

	if includeAdvancedOptions {
		if effort := config.ResolveReasoningEffort(model, upstream); effort != "" {
			if upstream.ReasoningParamStyle == "reasoning_effort" {
				reqMap["reasoning_effort"] = effort
			} else if upstream.ReasoningParamStyle == "thinking" {
				delete(reqMap, "reasoning")
				delete(reqMap, "reasoning_effort")
				if effort != "none" {
					reqMap["thinking"] = map[string]interface{}{"type": "enabled"}
				}
			} else {
				reqMap["reasoning"] = map[string]interface{}{"effort": effort}
			}
		}
		if upstream.TextVerbosity != "" {
			reqMap["text"] = map[string]interface{}{"verbosity": upstream.TextVerbosity}
		}
		if upstream.FastMode {
			reqMap["service_tier"] = "priority"
		}
	}

	if upstream.NormalizeNonstandardChatRoles {
		converters.NormalizeNonstandardChatRolesInRequest(reqMap)
	}

	return json.Marshal(reqMap)
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
	mappedModel := config.RedirectModel(model, upstream)

	var requestBody []byte
	var url string

	switch upstream.ServiceType {
	case "openai", "":
		// OpenAI 兼容上游：透传请求，仅替换 model 并注入高级参数
		var err error
		requestBody, err = buildChatCompletionRequestBody(bodyBytes, model, mappedModel, upstream, true)
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

	case "responses":
		// Responses 上游：转换 Chat 格式为 Responses 格式，发送到 /v1/responses
		chatBody, err := buildChatCompletionRequestBody(bodyBytes, model, mappedModel, upstream, true)
		if err != nil {
			return nil, err
		}
		requestBody = converters.ConvertChatRequestToResponsesRequest(chatBody)
		if skipVersionPrefix {
			url = fmt.Sprintf("%s/responses", strings.TrimRight(baseURL, "/"))
		} else {
			url = fmt.Sprintf("%s/v1/responses", strings.TrimRight(baseURL, "/"))
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
		if !upstream.PassbackReasoningContent && !upstream.PassbackThinkingBlocks {
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
		requestBody, err = buildChatCompletionRequestBody(bodyBytes, model, mappedModel, upstream, false)
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
		requestBody, err = buildChatCompletionRequestBody(bodyBytes, model, mappedModel, upstream, false)
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
		utils.SetAuthenticationHeader(req.Header, apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	default:
		// OpenAI / Gemini / Responses 等都使用 Bearer token
		utils.SetAuthenticationHeader(req.Header, apiKey)
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
			content, _ := m["content"]

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

// handleSuccess 处理成功的响应
func handleSuccess(
	c *gin.Context,
	resp *http.Response,
	upstreamType string,
	envCfg *config.EnvConfig,
	startTime time.Time,
	model string,
	isStream bool,
	fuzzyMode bool,
) (*types.Usage, error) {
	defer resp.Body.Close()

	if isStream {
		return handleStreamSuccess(c, resp, upstreamType, envCfg, startTime, model)
	}

	// 非流式响应处理
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		chatErrorResponse(c, 500, "Failed to read response", "server_error")
		return nil, err
	}

	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		log.Printf("[Chat-Timing] 响应完成: %dms, 状态: %d", responseTime, resp.StatusCode)
		common.LogUpstreamResponse(resp, bodyBytes, envCfg, "Chat")
	}

	switch upstreamType {
	case "claude":
		// 转换 Claude 响应为 OpenAI Chat 格式
		var claudeResp map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &claudeResp); err != nil {
			return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
		}
		// 空响应拦截（仅 Fuzzy 模式）：在原生 Claude 结构上判空，避免
		// convertClaudeResponseToChat 丢失 server_tool_use / redacted_thinking
		// 等语义块导致的误判。Header 未发送，可安全 failover。
		if fuzzyMode {
			var claudeTyped types.ClaudeResponse
			if err := json.Unmarshal(bodyBytes, &claudeTyped); err == nil && common.IsClaudeResponseEmpty(&claudeTyped) {
				log.Printf("[Chat-EmptyResponse] 上游返回空响应（非流式，upstreamType=%s），触发 failover", upstreamType)
				return nil, common.ErrEmptyNonStreamResponse
			}
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
		// 空响应拦截（仅 Fuzzy 模式）
		if fuzzyMode {
			var respMap map[string]interface{}
			if err := json.Unmarshal(chatRespBytes, &respMap); err == nil && common.IsChatResponseEmpty(respMap) {
				log.Printf("[Chat-EmptyResponse] 上游返回空响应（非流式，upstreamType=%s），触发 failover", upstreamType)
				return nil, common.ErrEmptyNonStreamResponse
			}
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
		if fuzzyMode && common.IsChatResponseEmpty(respMap) {
			log.Printf("[Chat-EmptyResponse] 上游返回空响应（非流式，upstreamType=%s），触发 failover", upstreamType)
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

// handleStreamSuccess 处理流式响应
func handleStreamSuccess(
	c *gin.Context,
	resp *http.Response,
	upstreamType string,
	envCfg *config.EnvConfig,
	startTime time.Time,
	model string,
) (*types.Usage, error) {
	var totalUsage *types.Usage
	logBuffer := common.NewLimitedLogBuffer(common.MaxUpstreamResponseLogBytes)
	streamLoggingEnabled := envCfg.EnableResponseLogs && envCfg.IsDevelopment()

	common.LogUpstreamResponseHeaders(resp, envCfg, "Chat")

	preflight, err := preflightChatStream(resp, upstreamType)
	if err != nil {
		return nil, err
	}
	if preflight.malformedToolName != "" {
		log.Printf("[Chat-EmptyResponse] 上游返回空或畸形 tool_call（流式，upstreamType=%s, tool=%s），触发 failover", upstreamType, preflight.malformedToolName)
		return nil, common.ErrEmptyStreamResponse
	}

	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		log.Printf("[Chat-Stream] 警告: ResponseWriter 不支持 Flusher")
	}

	switch upstreamType {
	case "claude":
		totalUsage = streamClaudeToChat(c, resp, flusher, model, logBuffer, streamLoggingEnabled, preflight.buffered)
	case "responses":
		totalUsage = streamResponsesToChat(c, resp, flusher, model, logBuffer, streamLoggingEnabled, preflight.buffered)
	default:
		// OpenAI / Gemini / Responses 等：直接透传 SSE 流
		totalUsage = streamPassthrough(c, resp, flusher, logBuffer, streamLoggingEnabled, preflight.buffered)
	}

	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		log.Printf("[Chat-Stream-Timing] 流式响应完成: %dms", responseTime)
		if logBuffer.Len() > 0 {
			log.Printf("[Chat-Stream] 上游流式响应原始内容:\n%s", logBuffer.String())
		}
	}

	return totalUsage, nil
}

type chatStreamPreflight struct {
	buffered          []byte
	malformedToolName string
}

type chatToolTracker interface {
	HasPendingToolCall() bool
	ProcessClaudeEvent(string) (bool, string)
	ProcessResponsesEvent(string) (bool, string)
}

func preflightChatStream(resp *http.Response, upstreamType string) (*chatStreamPreflight, error) {
	result := &chatStreamPreflight{}
	tracker := common.NewStreamToolCallTracker()
	chatTracker := newOpenAIChatToolCallTracker()
	buf := make([]byte, 32*1024)
	var remainder string
	const maxPreflightBytes = 1024 * 1024

	flushRemainder := func() {
		if remainder != "" {
			result.buffered = append(result.buffered, []byte(remainder)...)
			remainder = ""
		}
	}

	for result.malformedToolName == "" && len(result.buffered) < maxPreflightBytes {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			result.buffered = append(result.buffered, chunk...)
			data := remainder + string(chunk)
			lines := strings.Split(data, "\n")
			remainder = lines[len(lines)-1]
			completeLines := lines[:len(lines)-1]
			if malformed, name := detectMalformedChatStreamLines(completeLines, upstreamType, tracker, chatTracker); malformed {
				result.malformedToolName = name
				flushRemainder()
				break
			}
			if chatStreamHasTextContent(completeLines, upstreamType) && !tracker.HasPendingToolCall() && !chatTracker.HasPendingToolCall() {
				flushRemainder()
				break
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return result, err
		}
	}

	flushRemainder()
	return result, nil
}

func detectMalformedChatStreamLines(lines []string, upstreamType string, tracker chatToolTracker, chatTracker *openAIChatToolCallTracker) (bool, string) {
	for _, line := range lines {
		jsonData, ok := common.ExtractSSEJSONLine(line)
		if !ok {
			continue
		}
		if jsonData == "[DONE]" {
			continue
		}
		event := "data: " + jsonData + "\n\n"
		switch upstreamType {
		case "claude":
			if malformed, name := tracker.ProcessClaudeEvent(event); malformed {
				return true, name
			}
		case "responses":
			if malformed, name := tracker.ProcessResponsesEvent(event); malformed {
				return true, name
			}
		default:
			if malformed, name := chatTracker.ProcessLine(jsonData); malformed {
				return true, name
			}
		}
	}
	return false, ""
}

type openAIChatToolCallTracker struct {
	active map[int]*strings.Builder
	names  map[int]string
}

func newOpenAIChatToolCallTracker() *openAIChatToolCallTracker {
	return &openAIChatToolCallTracker{
		active: make(map[int]*strings.Builder),
		names:  make(map[int]string),
	}
}

func (t *openAIChatToolCallTracker) HasPendingToolCall() bool {
	return len(t.active) > 0
}

func (t *openAIChatToolCallTracker) ProcessLine(jsonData string) (bool, string) {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		return false, ""
	}

	choices, _ := data["choices"].([]interface{})
	for _, rawChoice := range choices {
		choice, _ := rawChoice.(map[string]interface{})
		if finish, _ := choice["finish_reason"].(string); finish == "tool_calls" || finish == "function_call" {
			if finish == "function_call" {
				if builder := t.active[0]; builder != nil && common.IsMalformedToolArguments(builder.String()) && t.toolRequiresArguments(0) {
					return true, fallbackChatToolName(t.names[0], 0)
				}
			} else {
				for idx, builder := range t.active {
					if common.IsMalformedToolArguments(builder.String()) && t.toolRequiresArguments(idx) {
						return true, fallbackChatToolName(t.names[idx], idx)
					}
				}
			}
			t.active = make(map[int]*strings.Builder)
			t.names = make(map[int]string)
			continue
		}

		delta, _ := choice["delta"].(map[string]interface{})
		if functionCall, ok := delta["function_call"].(map[string]interface{}); ok {
			builder := t.ensure(0)
			if name, ok := functionCall["name"].(string); ok && name != "" {
				t.names[0] = name
			}
			if args, ok := functionCall["arguments"].(string); ok {
				builder.WriteString(args)
			}
		}
		if calls, ok := delta["tool_calls"].([]interface{}); ok {
			for _, rawCall := range calls {
				call, _ := rawCall.(map[string]interface{})
				idx := 0
				if fidx, ok := call["index"].(float64); ok {
					idx = int(fidx)
				}
				builder := t.ensure(idx)
				function, _ := call["function"].(map[string]interface{})
				if name, ok := function["name"].(string); ok && name != "" {
					t.names[idx] = name
				}
				if args, ok := function["arguments"].(string); ok {
					builder.WriteString(args)
				}
			}
		}
	}
	return false, ""
}

func (t *openAIChatToolCallTracker) ensure(index int) *strings.Builder {
	builder := t.active[index]
	if builder == nil {
		builder = &strings.Builder{}
		t.active[index] = builder
	}
	return builder
}

func (t *openAIChatToolCallTracker) toolRequiresArguments(index int) bool {
	name := strings.ToLower(strings.TrimSpace(t.names[index]))
	switch name {
	case "read", "edit", "write", "bash", "grep", "glob", "webfetch", "websearch":
		return true
	default:
		return false
	}
}

func fallbackChatToolName(name string, index int) string {
	if name != "" {
		return name
	}
	return fmt.Sprintf("tool_%d", index)
}

func chatStreamHasTextContent(lines []string, upstreamType string) bool {
	for _, line := range lines {
		jsonData, ok := common.ExtractSSEJSONLine(line)
		if !ok {
			continue
		}
		if jsonData == "[DONE]" {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
			continue
		}
		if upstreamType == "claude" {
			if eventType, _ := data["type"].(string); eventType == "content_block_delta" {
				delta, _ := data["delta"].(map[string]interface{})
				if text, _ := delta["text"].(string); !common.IsEffectivelyEmptyStreamText(text) {
					return true
				}
			}
			continue
		}
		if upstreamType == "responses" {
			if eventType, _ := data["type"].(string); eventType == "response.output_text.delta" {
				if text, _ := data["delta"].(string); !common.IsEffectivelyEmptyStreamText(text) {
					return true
				}
			}
			continue
		}
		choices, _ := data["choices"].([]interface{})
		for _, rawChoice := range choices {
			choice, _ := rawChoice.(map[string]interface{})
			delta, _ := choice["delta"].(map[string]interface{})
			if content, _ := delta["content"].(string); !common.IsEffectivelyEmptyStreamText(content) {
				return true
			}
			if reasoning, _ := delta["reasoning_content"].(string); !common.IsEffectivelyEmptyStreamText(reasoning) {
				return true
			}
		}
	}
	return false
}

// streamPassthrough 直接透传 SSE 流（用于 OpenAI 兼容上游）
func streamPassthrough(
	c *gin.Context,
	resp *http.Response,
	flusher http.Flusher,
	logBuffer *common.LimitedLogBuffer,
	loggingEnabled bool,
	prefetched []byte,
) *types.Usage {
	var totalUsage *types.Usage
	buf := make([]byte, 32*1024)
	var remainder string
	pending := prefetched

	for {
		var chunk []byte
		var readErr error
		if len(pending) > 0 {
			chunk = pending
			pending = nil
		} else {
			n, err := resp.Body.Read(buf)
			readErr = err
			if n > 0 {
				chunk = buf[:n]
			}
		}

		if len(chunk) > 0 {
			if loggingEnabled {
				logBuffer.Write(chunk)
			}
			// 使用行缓冲机制避免跨 chunk 截断
			data := remainder + string(chunk)
			lines := strings.Split(data, "\n")
			remainder = lines[len(lines)-1]
			completeLines := lines[:len(lines)-1]

			// 尝试从完整行中提取 usage
			for _, line := range completeLines {
				jsonData, ok := common.ExtractSSEJSONLine(line)
				if !ok {
					continue
				}
				if jsonData == "[DONE]" {
					continue
				}
				var parsed map[string]interface{}
				if json.Unmarshal([]byte(jsonData), &parsed) == nil {
					if u, ok := parsed["usage"].(map[string]interface{}); ok {
						promptTokens, _ := u["prompt_tokens"].(float64)
						completionTokens, _ := u["completion_tokens"].(float64)
						totalUsage = &types.Usage{
							InputTokens:  int(promptTokens),
							OutputTokens: int(completionTokens),
						}
					}
				}
			}

			c.Writer.Write(chunk)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if remainder != "" {
				flushCompletePassthroughRemainder(c, flusher, remainder)
				remainder = ""
			}
			break
		}
	}

	return totalUsage
}

func flushCompletePassthroughRemainder(c *gin.Context, flusher http.Flusher, remainder string) {
	trimmed := strings.TrimSpace(remainder)
	jsonData, ok := common.ExtractSSEJSONLine(trimmed)
	if !ok {
		return
	}
	if jsonData != "[DONE]" && !json.Valid([]byte(jsonData)) {
		return
	}
	fmt.Fprintf(c.Writer, "%s\n\n", trimmed)
	if flusher != nil {
		flusher.Flush()
	}
}

// streamClaudeToChat Claude 流式响应转换为 OpenAI Chat 格式
func streamClaudeToChat(
	c *gin.Context,
	resp *http.Response,
	flusher http.Flusher,
	model string,
	logBuffer *common.LimitedLogBuffer,
	loggingEnabled bool,
	prefetched []byte,
) *types.Usage {
	var totalUsage *types.Usage
	var doneSent bool
	var currentToolCallID string
	currentToolIndex := 0
	buf := make([]byte, 32*1024)
	var remainder string
	pending := prefetched

	for {
		var chunk []byte
		var readErr error
		if len(pending) > 0 {
			chunk = pending
			pending = nil
		} else {
			n, err := resp.Body.Read(buf)
			readErr = err
			if n > 0 {
				chunk = buf[:n]
			}
		}

		if len(chunk) > 0 {
			if loggingEnabled {
				logBuffer.Write(chunk)
			}
			data := remainder + string(chunk)
			lines := strings.Split(data, "\n")
			remainder = lines[len(lines)-1]
			lines = lines[:len(lines)-1]
			for _, line := range lines {
				processClaudeChatStreamLine(c, flusher, model, line, &totalUsage, &doneSent, &currentToolCallID, &currentToolIndex)
			}
		}

		if readErr != nil {
			if remainder != "" {
				processClaudeChatStreamLine(c, flusher, model, remainder, &totalUsage, &doneSent, &currentToolCallID, &currentToolIndex)
				remainder = ""
			}
			break
		}
	}

	if !doneSent {
		fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}

	return totalUsage
}

// flushResponsesSSEEvent 处理缓存的 Responses SSE data 行，生成对应的 Chat chunk。
func flushResponsesSSEEvent(
	c *gin.Context,
	flusher http.Flusher,
	model string,
	chunkID string,
	dataLines []string,
	roleSent *bool,
	currentToolIndex *int,
	currentToolCallID *string,
	currentToolName *string,
	totalUsage **types.Usage,
	doneSent *bool,
	currentToolSeq *int,
) {
	jsonData := strings.Join(dataLines, "\n")
	if jsonData == "[DONE]" {
		return
	}

	var event map[string]interface{}
	if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
		return
	}

	evtType, _ := event["type"].(string)

	switch evtType {
	case "response.output_text.delta":
		if !*roleSent {
			roleChunk := map[string]interface{}{
				"id":      chunkID,
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   model,
				"choices": []map[string]interface{}{{
					"index":         0,
					"delta":         map[string]interface{}{"role": "assistant"},
					"finish_reason": nil,
				}},
			}
			writeChatSSEChunk(c, flusher, roleChunk)
			*roleSent = true
		}
		text, _ := event["delta"].(string)
		if text == "" {
			return
		}
		chatChunk := map[string]interface{}{
			"id":      chunkID,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]interface{}{{
				"index":         0,
				"delta":         map[string]interface{}{"content": text},
				"finish_reason": nil,
			}},
		}
		writeChatSSEChunk(c, flusher, chatChunk)

	case "response.output_item.added":
		item, _ := event["item"].(map[string]interface{})
		if item == nil {
			return
		}
		if item["type"] == "function_call" {
			*currentToolCallID, _ = item["call_id"].(string)
			*currentToolName, _ = item["name"].(string)
			*currentToolIndex = *currentToolSeq
			*currentToolSeq++
			toolChunk := map[string]interface{}{
				"id":      chunkID,
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   model,
				"choices": []map[string]interface{}{{
					"index": 0,
					"delta": map[string]interface{}{
						"tool_calls": []map[string]interface{}{{
							"index": *currentToolIndex,
							"id":    *currentToolCallID,
							"type":  "function",
							"function": map[string]interface{}{
								"name":      *currentToolName,
								"arguments": "",
							},
						}},
					},
					"finish_reason": nil,
				}},
			}
			writeChatSSEChunk(c, flusher, toolChunk)
		}

	case "response.function_call_arguments.delta":
		argsDelta, _ := event["delta"].(string)
		if argsDelta == "" {
			return
		}
		var arguments interface{} = argsDelta
		if json.Valid([]byte(argsDelta)) {
			var parsed interface{}
			if err := json.Unmarshal([]byte(argsDelta), &parsed); err == nil {
				arguments = parsed
			}
		}
		toolChunk := map[string]interface{}{
			"id":      chunkID,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]interface{}{{
				"index": 0,
				"delta": map[string]interface{}{
					"tool_calls": []map[string]interface{}{{
						"index": *currentToolIndex,
						"id":    *currentToolCallID,
						"function": map[string]interface{}{
							"arguments": arguments,
						},
					}},
				},
				"finish_reason": nil,
			}},
		}
		writeChatSSEChunk(c, flusher, toolChunk)

	case "response.completed":
		if usage, ok := event["usage"].(map[string]interface{}); ok {
			inputTokens, _ := usage["input_tokens"].(float64)
			outputTokens, _ := usage["output_tokens"].(float64)
			*totalUsage = &types.Usage{
				InputTokens:  int(inputTokens),
				OutputTokens: int(outputTokens),
			}
		}
		finishReason := "stop"
		if *currentToolCallID != "" {
			finishReason = "tool_calls"
		}
		if respObj, ok := event["response"].(map[string]interface{}); ok {
			if status, _ := respObj["status"].(string); status == "incomplete" {
				finishReason = "length"
			}
		}
		finalChunk := map[string]interface{}{
			"id":      chunkID,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]interface{}{{
				"index":         0,
				"delta":         map[string]interface{}{},
				"finish_reason": finishReason,
			}},
		}
		writeChatSSEChunk(c, flusher, finalChunk)
		fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		*doneSent = true
	}
}

// streamResponsesToChat 将 Responses SSE 流转换为 OpenAI Chat SSE 格式。
// 处理的 Responses 事件: response.output_text.delta, response.output_item.added,
// response.function_call_arguments.delta, response.completed 等。
func streamResponsesToChat(
	c *gin.Context,
	resp *http.Response,
	flusher http.Flusher,
	model string,
	logBuffer *common.LimitedLogBuffer,
	loggingEnabled bool,
	prefetched []byte,
) *types.Usage {
	var totalUsage *types.Usage
	var doneSent bool
	var roleSent bool
	buf := make([]byte, 32*1024)
	var remainder string
	pending := prefetched
	// 工具调用状态追踪
	var currentToolIndex int
	var currentToolCallID string
	var currentToolSeq int
	var currentToolName string
	chunkID := fmt.Sprintf("chatcmpl-resp-%d", time.Now().UnixNano())

	for {
		var chunk []byte
		var readErr error
		if len(pending) > 0 {
			chunk = pending
			pending = nil
		} else {
			n, err := resp.Body.Read(buf)
			readErr = err
			if n > 0 {
				chunk = buf[:n]
			}
		}

		if len(chunk) > 0 {
			if loggingEnabled {
				logBuffer.Write(chunk)
			}
			data := remainder + string(chunk)
			lines := strings.Split(data, "\n")
			remainder = lines[len(lines)-1]
			lines = lines[:len(lines)-1]

			var currentEventType string
			for _, line := range lines {
				// 记录 event: 行
				if strings.HasPrefix(line, "event: ") {
					currentEventType = strings.TrimPrefix(line, "event: ")
					continue
				}
				jsonData, ok := common.ExtractSSEJSONLine(line)
				if !ok {
					continue
				}
				if jsonData == "[DONE]" {
					continue
				}

				var event map[string]interface{}
				if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
					continue
				}

				// 从 data JSON 的 type 字段推断事件类型（兼容无 event: 行的格式）
				evtType := currentEventType
				if evtType == "" {
					evtType, _ = event["type"].(string)
				}
				currentEventType = ""

				switch evtType {
				case "response.output_text.delta":
					// 首次文本输出前发送 role delta
					if !roleSent {
						roleChunk := map[string]interface{}{
							"id":      chunkID,
							"object":  "chat.completion.chunk",
							"created": time.Now().Unix(),
							"model":   model,
							"choices": []map[string]interface{}{{
								"index":         0,
								"delta":         map[string]interface{}{"role": "assistant"},
								"finish_reason": nil,
							}},
						}
						writeChatSSEChunk(c, flusher, roleChunk)
						roleSent = true
					}
					text, _ := event["delta"].(string)
					if text == "" {
						continue
					}
					chatChunk := map[string]interface{}{
						"id":      chunkID,
						"object":  "chat.completion.chunk",
						"created": time.Now().Unix(),
						"model":   model,
						"choices": []map[string]interface{}{{
							"index":         0,
							"delta":         map[string]interface{}{"content": text},
							"finish_reason": nil,
						}},
					}
					writeChatSSEChunk(c, flusher, chatChunk)

				case "response.output_item.added":
					item, _ := event["item"].(map[string]interface{})
					if item == nil {
						continue
					}
					itemType, _ := item["type"].(string)
					if itemType == "function_call" {
						currentToolCallID, _ = item["call_id"].(string)
						currentToolName, _ = item["name"].(string)
						currentToolIndex = currentToolSeq
						currentToolSeq++
						toolChunk := map[string]interface{}{
							"id":      chunkID,
							"object":  "chat.completion.chunk",
							"created": time.Now().Unix(),
							"model":   model,
							"choices": []map[string]interface{}{{
								"index": 0,
								"delta": map[string]interface{}{
									"tool_calls": []map[string]interface{}{{
										"index": currentToolIndex,
										"id":    currentToolCallID,
										"type":  "function",
										"function": map[string]interface{}{
											"name":      currentToolName,
											"arguments": "",
										},
									}},
								},
								"finish_reason": nil,
							}},
						}
						writeChatSSEChunk(c, flusher, toolChunk)
					}

				case "response.function_call_arguments.delta":
					argsDelta, _ := event["delta"].(string)
					if argsDelta == "" {
						continue
					}
					toolChunk := map[string]interface{}{
						"id":      chunkID,
						"object":  "chat.completion.chunk",
						"created": time.Now().Unix(),
						"model":   model,
						"choices": []map[string]interface{}{{
							"index": 0,
							"delta": map[string]interface{}{
								"tool_calls": []map[string]interface{}{{
									"index": currentToolIndex,
									"function": map[string]interface{}{
										"arguments": argsDelta,
									},
								}},
							},
							"finish_reason": nil,
						}},
					}
					writeChatSSEChunk(c, flusher, toolChunk)

				case "response.completed":
					// 提取 usage
					if usage, ok := event["usage"].(map[string]interface{}); ok {
						inputTokens, _ := usage["input_tokens"].(float64)
						outputTokens, _ := usage["output_tokens"].(float64)
						totalUsage = &types.Usage{
							InputTokens:  int(inputTokens),
							OutputTokens: int(outputTokens),
						}
					}
					// 最终 chunk: 设置 finish_reason
					finishReason := "stop"
					if currentToolCallID != "" {
						finishReason = "tool_calls"
					}
					// 检查 incomplete 状态
					if respObj, ok := event["response"].(map[string]interface{}); ok {
						if status, _ := respObj["status"].(string); status == "incomplete" {
							finishReason = "length"
						}
					}
					finalChunk := map[string]interface{}{
						"id":      chunkID,
						"object":  "chat.completion.chunk",
						"created": time.Now().Unix(),
						"model":   model,
						"choices": []map[string]interface{}{{
							"index":         0,
							"delta":         map[string]interface{}{},
							"finish_reason": finishReason,
						}},
					}
					writeChatSSEChunk(c, flusher, finalChunk)
					// 发送 [DONE]
					fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
					if flusher != nil {
						flusher.Flush()
					}
					doneSent = true
				}
			}
		}

		if readErr != nil {
			if remainder != "" {
				// 处理剩余数据（忽略不完整的行）
			}
			break
		}
	}

	if !doneSent {
		fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}

	return totalUsage
}

// writeChatSSEChunk 将 Chat chunk 写为 SSE 格式并 flush。
func writeChatSSEChunk(c *gin.Context, flusher http.Flusher, chunk map[string]interface{}) {
	chunkBytes, _ := json.Marshal(chunk)
	fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
	if flusher != nil {
		flusher.Flush()
	}
}

func processClaudeChatStreamLine(c *gin.Context, flusher http.Flusher, model string, line string, totalUsage **types.Usage, doneSent *bool, currentToolCallID *string, currentToolIndex *int) {
	jsonData, ok := common.ExtractSSEJSONLine(line)
	if !ok {
		return
	}
	if jsonData == "[DONE]" {
		fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		*doneSent = true
		return
	}

	var event map[string]interface{}
	if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
		return
	}

	eventType, _ := event["type"].(string)
	switch eventType {
	case "content_block_start":
		cb, _ := event["content_block"].(map[string]interface{})
		if cb == nil {
			return
		}
		cbType, _ := cb["type"].(string)
		if cbType == "tool_use" {
			idx := 0
			if i, ok := event["index"].(float64); ok {
				idx = int(i)
			}
			name, _ := cb["name"].(string)
			tid, _ := cb["id"].(string)
			*currentToolCallID = tid
			*currentToolIndex = idx
			// 发送 tool_calls delta 起始块（包含 index/id/name）
			chatChunk := map[string]interface{}{
				"id":      "chatcmpl-claude",
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   model,
				"choices": []map[string]interface{}{{
					"index": 0,
					"delta": map[string]interface{}{
						"tool_calls": []map[string]interface{}{{
							"index": idx,
							"id":    tid,
							"type":  "function",
							"function": map[string]interface{}{
								"name":      name,
								"arguments": "",
							},
						}},
					},
					"finish_reason": nil,
				}},
			}
			chunkBytes, _ := json.Marshal(chatChunk)
			fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
			if flusher != nil {
				flusher.Flush()
			}
		}
		return
	case "content_block_delta":
		delta, ok := event["delta"].(map[string]interface{})
		if !ok {
			return
		}
		deltaType, _ := delta["type"].(string)
		switch deltaType {
		case "thinking_delta":
			thinking, _ := delta["thinking"].(string)
			if thinking == "" {
				return
			}
			chatChunk := map[string]interface{}{
				"id":      "chatcmpl-claude",
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   model,
				"choices": []map[string]interface{}{{
					"index": 0,
					"delta": map[string]interface{}{
						"reasoning_content": thinking,
					},
					"finish_reason": nil,
				}},
			}
			chunkBytes, _ := json.Marshal(chatChunk)
			fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
			if flusher != nil {
				flusher.Flush()
			}
		case "text_delta":
			text, _ := delta["text"].(string)
			chatChunk := map[string]interface{}{
				"id":      "chatcmpl-claude",
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   model,
				"choices": []map[string]interface{}{{
					"index": 0,
					"delta": map[string]interface{}{
						"content": text,
					},
					"finish_reason": nil,
				}},
			}
			chunkBytes, _ := json.Marshal(chatChunk)
			fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
			if flusher != nil {
				flusher.Flush()
			}
		case "input_json_delta":
			partial, _ := delta["partial_json"].(string)
			if partial == "" {
				return
			}
			chatChunk := map[string]interface{}{
				"id":      "chatcmpl-claude",
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   model,
				"choices": []map[string]interface{}{{
					"index": 0,
					"delta": map[string]interface{}{
						"tool_calls": []map[string]interface{}{{
							"index": *currentToolIndex,
							"function": map[string]interface{}{
								"arguments": partial,
							},
						}},
					},
					"finish_reason": nil,
				}},
			}
			chunkBytes, _ := json.Marshal(chatChunk)
			fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
			if flusher != nil {
				flusher.Flush()
			}
		}
	case "message_delta":
		// 根据 stop_reason 推导 finish_reason
		finishReason := "stop"
		if delta, ok := event["delta"].(map[string]interface{}); ok {
			if sr, _ := delta["stop_reason"].(string); sr == "tool_use" || sr == "server_tool_use" {
				finishReason = "tool_calls"
			} else if sr == "max_tokens" {
				finishReason = "length"
			}
		}
		stopChunk := map[string]interface{}{
			"id":      "chatcmpl-claude",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]interface{}{{
				"index":         0,
				"delta":         map[string]interface{}{},
				"finish_reason": finishReason,
			}},
		}
		if usage, ok := event["usage"].(map[string]interface{}); ok {
			inputTokens, _ := usage["input_tokens"].(float64)
			outputTokens, _ := usage["output_tokens"].(float64)
			*totalUsage = &types.Usage{InputTokens: int(inputTokens), OutputTokens: int(outputTokens)}
			stopChunk["usage"] = map[string]interface{}{
				"prompt_tokens":     int(inputTokens),
				"completion_tokens": int(outputTokens),
				"total_tokens":      int(inputTokens + outputTokens),
			}
		}
		chunkBytes, _ := json.Marshal(stopChunk)
		fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
		if flusher != nil {
			flusher.Flush()
		}
	case "message_start":
		if msg, ok := event["message"].(map[string]interface{}); ok {
			if usage, ok := msg["usage"].(map[string]interface{}); ok {
				inputTokens, _ := usage["input_tokens"].(float64)
				*totalUsage = &types.Usage{InputTokens: int(inputTokens), OutputTokens: 0}
			}
		}
	}
}

// chatErrorResponse 返回 OpenAI 格式的错误响应
func chatErrorResponse(c *gin.Context, statusCode int, message string, code string) {
	c.JSON(statusCode, gin.H{
		"error": gin.H{
			"message": message,
			"type":    "server_error",
			"code":    code,
		},
	})
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

	chatErrorResponse(c, 503, errMsg, "service_unavailable")
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

	chatErrorResponse(c, 503, errMsg, "service_unavailable")
}

// injectGeminiThoughtSignatures 为 Gemini 上游注入 thought_signature
// Gemini 3 模型要求 assistant message 中每个 step 的第一个 tool_call 必须包含 thought_signature，
// 否则返回 400。对于没有 thought_signature 的 tool_calls，注入 dummy 值跳过验证。
// 参考: https://ai.google.dev/gemini-api/docs/thought-signatures
func injectGeminiThoughtSignatures(body []byte) []byte {
	var reqMap map[string]interface{}
	if err := json.Unmarshal(body, &reqMap); err != nil {
		return body
	}

	messages, ok := reqMap["messages"].([]interface{})
	if !ok {
		return body
	}

	modified := false
	for _, msg := range messages {
		msgMap, ok := msg.(map[string]interface{})
		if !ok {
			continue
		}

		role, _ := msgMap["role"].(string)
		if role != "assistant" {
			continue
		}

		toolCalls, ok := msgMap["tool_calls"].([]interface{})
		if !ok || len(toolCalls) == 0 {
			continue
		}

		// 只需要为第一个 tool_call 注入（parallel FC 只有第一个需要 signature）
		firstTC, ok := toolCalls[0].(map[string]interface{})
		if !ok {
			continue
		}

		// 检查是否已有 extra_content.google.thought_signature
		if hasThoughtSignature(firstTC) {
			continue
		}

		// 注入 dummy thought_signature，保留已有的 extra_content 字段
		extraContent, ok := firstTC["extra_content"].(map[string]interface{})
		if !ok {
			extraContent = map[string]interface{}{}
		}
		google, ok := extraContent["google"].(map[string]interface{})
		if !ok {
			google = map[string]interface{}{}
		}
		google["thought_signature"] = types.DummyThoughtSignature
		extraContent["google"] = google
		firstTC["extra_content"] = extraContent
		modified = true
	}

	if !modified {
		return body
	}

	result, err := json.Marshal(reqMap)
	if err != nil {
		return body
	}
	return result
}

// hasThoughtSignature 检查 tool_call 是否已包含 thought_signature
func hasThoughtSignature(toolCall map[string]interface{}) bool {
	extraContent, ok := toolCall["extra_content"].(map[string]interface{})
	if !ok {
		return false
	}
	google, ok := extraContent["google"].(map[string]interface{})
	if !ok {
		return false
	}
	sig, ok := google["thought_signature"].(string)
	return ok && sig != ""
}
