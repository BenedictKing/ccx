package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/converters"
	"github.com/BenedictKing/ccx/internal/forwarding"
	"github.com/BenedictKing/ccx/internal/passthrough"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// ResponsesProvider Responses API 提供商
type ResponsesProvider struct {
	SessionManager *session.SessionManager
}

// ConvertToProviderRequest 将请求转换为上游格式
func (p *ResponsesProvider) ConvertToProviderRequest(
	c *gin.Context,
	upstream *config.UpstreamConfig,
	apiKey string,
) (*http.Request, []byte, error) {
	bodyBytes, err := getRequestBodyBytes(c)
	if err != nil {
		return nil, nil, fmt.Errorf("读取请求体失败: %w", err)
	}

	if p.SessionManager == nil {
		p.SessionManager = newDefaultSessionManager()
	}

	providerReq, reqBodyForURL, err := p.buildProviderRequestBody(c, c.Request.URL.Path, bodyBytes, upstream)
	if err != nil {
		return nil, bodyBytes, err
	}

	reqBody, err := utils.MarshalJSONNoEscape(providerReq)
	if err != nil {
		return nil, bodyBytes, fmt.Errorf("序列化请求失败: %w", err)
	}

	targetURL, err := p.buildRequestURL(upstream, reqBodyForURL)
	if err != nil {
		return nil, bodyBytes, err
	}

	if upstream.ServiceType == "responses" && !strings.HasSuffix(c.Request.URL.Path, "/v1/messages") {
		prepared, err := forwarding.Build(c, forwarding.ForwardingRequest{
			Method:        http.MethodPost,
			URL:           targetURL,
			Body:          reqBody,
			ServiceType:   upstream.ServiceType,
			CustomHeaders: upstream.CustomHeaders,
			AuthKind:      forwarding.AuthKindStandard,
			APIKey:        apiKey,
			RawResponse:   true,
			RawStream:     isResponsesStreamRequest(reqBody),
		})
		if err != nil {
			return nil, bodyBytes, err
		}
		return prepared.Request, bodyBytes, nil
	}

	authKind := forwarding.AuthKindStandard
	platformHeaders := map[string]string(nil)
	switch upstream.ServiceType {
	case "gemini":
		authKind = forwarding.AuthKindGemini
	case "claude":
		platformHeaders = map[string]string{"anthropic-version": "2023-06-01"}
	}

	prepared, err := forwarding.Build(c, forwarding.ForwardingRequest{
		Method:          http.MethodPost,
		URL:             targetURL,
		Body:            reqBody,
		ServiceType:     upstream.ServiceType,
		PlatformHeaders: platformHeaders,
		CustomHeaders:   upstream.CustomHeaders,
		AuthKind:        authKind,
		APIKey:          apiKey,
	})
	if err != nil {
		return nil, bodyBytes, err
	}
	return prepared.Request, bodyBytes, nil
}

func isResponsesStreamRequest(bodyBytes []byte) bool {
	var req map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		return false
	}
	stream, _ := req["stream"].(bool)
	return stream
}

func (p *ResponsesProvider) buildProviderRequestBody(c *gin.Context, requestPath string, bodyBytes []byte, upstream *config.UpstreamConfig) (interface{}, []byte, error) {
	if strings.HasSuffix(requestPath, "/v1/messages") {
		responsesReq, err := p.buildResponsesRequestFromClaude(c, bodyBytes, upstream)
		if err != nil {
			return nil, nil, fmt.Errorf("解析 Claude Messages 请求失败: %w", err)
		}
		return responsesReq, bodyBytes, nil
	}

	var providerReq interface{}
	converter := converters.NewConverter(upstream.ServiceType)

	if _, ok := converter.(*converters.ResponsesPassthroughConverter); ok {
		patchedBody := passthrough.PatchPlatformFields(bodyBytes, upstream)
		var reqMap map[string]interface{}
		if err := json.Unmarshal(patchedBody, &reqMap); err != nil {
			return nil, nil, fmt.Errorf("透传模式下解析请求失败: %w", err)
		}
		normalizeResponsesInputForPassthrough(reqMap)
		providerReq = reqMap
	} else {
		var responsesReq types.ResponsesRequest
		if err := json.Unmarshal(bodyBytes, &responsesReq); err != nil {
			return nil, nil, fmt.Errorf("解析 Responses 请求失败: %w", err)
		}

		var (
			sess *session.Session
			err  error
		)
		if responsesReq.PreviousResponseID != "" {
			sess, err = p.SessionManager.GetOrCreateSession(responsesReq.PreviousResponseID)
			if err != nil {
				return nil, nil, fmt.Errorf("get session failed: %w", err)
			}
		} else {
			sess = &session.Session{}
		}

		responsesReq.Model = config.RedirectModel(responsesReq.Model, upstream)
		convertedReq, err := converter.ToProviderRequest(sess, &responsesReq)
		if err != nil {
			return nil, nil, fmt.Errorf("convert request failed: %w", err)
		}
		providerReq = convertedReq
	}

	return providerReq, bodyBytes, nil
}

func (p *ResponsesProvider) buildResponsesRequestFromClaude(c *gin.Context, bodyBytes []byte, upstream *config.UpstreamConfig) (map[string]interface{}, error) {
	var claudeReq types.ClaudeRequest
	if err := json.Unmarshal(bodyBytes, &claudeReq); err != nil {
		return nil, err
	}

	input := make([]map[string]interface{}, 0, len(claudeReq.Messages))
	for _, msg := range claudeReq.Messages {
		role := normalizeRole(msg.Role)
		contentBlocks := make([]map[string]interface{}, 0)
		flushMessage := func() {
			if len(contentBlocks) == 0 {
				return
			}
			input = append(input, map[string]interface{}{
				"type":    "message",
				"role":    role,
				"content": contentBlocks,
			})
			contentBlocks = make([]map[string]interface{}, 0)
		}
		switch content := msg.Content.(type) {
		case string:
			if content != "" {
				contentBlocks = append(contentBlocks, map[string]interface{}{
					"type": responsesTextContentType(role),
					"text": content,
				})
			}
		case []interface{}:
			for _, rawBlock := range content {
				block, ok := rawBlock.(map[string]interface{})
				if !ok {
					continue
				}
				switch block["type"] {
				case "text":
					if text, ok := block["text"].(string); ok && text != "" {
						contentBlocks = append(contentBlocks, map[string]interface{}{
							"type": responsesTextContentType(role),
							"text": text,
						})
					}
				case "tool_use":
					flushMessage()
					arguments, _ := utils.MarshalJSONNoEscape(block["input"])
					input = append(input, map[string]interface{}{
						"type":      "function_call",
						"call_id":   block["id"],
						"name":      block["name"],
						"arguments": string(arguments),
					})
				case "tool_result":
					flushMessage()
					resultText := extractClaudeToolResult(block["content"])
					input = append(input, map[string]interface{}{
						"type":    "function_call_output",
						"call_id": block["tool_use_id"],
						"output":  resultText,
					})
				}
			}
		}

		flushMessage()
	}

	responsesReq := map[string]interface{}{
		"model":  config.RedirectModel(claudeReq.Model, upstream),
		"input":  input,
		"stream": claudeReq.Stream,
	}
	if effort := config.ResolveReasoningEffort(claudeReq.Model, upstream); effort != "" {
		responsesReq["reasoning"] = map[string]interface{}{"effort": effort}
	}
	if instructions := extractResponsesInstructions(claudeReq.System); instructions != "" {
		responsesReq["instructions"] = instructions
	}
	if claudeReq.MaxTokens > 0 {
		responsesReq["max_output_tokens"] = claudeReq.MaxTokens
	}
	if claudeReq.Temperature > 0 {
		responsesReq["temperature"] = claudeReq.Temperature
	}
	if claudeReq.TopP > 0 {
		responsesReq["top_p"] = claudeReq.TopP
	}
	if claudeReq.ToolChoice != nil {
		responsesReq["tool_choice"] = claudeReq.ToolChoice
	}
	if len(claudeReq.Tools) > 0 {
		tools := make([]map[string]interface{}, 0, len(claudeReq.Tools))
		for _, tool := range claudeReq.Tools {
			item := map[string]interface{}{
				"type":       "function",
				"name":       tool.Name,
				"parameters": tool.InputSchema,
			}
			if tool.Description != "" {
				item["description"] = tool.Description
			}
			tools = append(tools, item)
		}
		responsesReq["tools"] = tools
		if claudeReq.ParallelToolCalls != nil {
			responsesReq["parallel_tool_calls"] = *claudeReq.ParallelToolCalls
		} else {
			responsesReq["parallel_tool_calls"] = true
		}
	}
	if claudeReq.Metadata != nil {
		if userID, ok := claudeReq.Metadata["user_id"].(string); ok && userID != "" {
			responsesReq["user"] = userID
		}
	}
	if _, exists := responsesReq["user"]; !exists {
		if sessionID := utils.ExtractUnifiedSessionID(c, bodyBytes); sessionID != "" {
			responsesReq["user"] = sessionID
		}
	}
	if cacheKey := utils.ExtractUnifiedSessionID(c, bodyBytes); cacheKey != "" {
		responsesReq["prompt_cache_key"] = cacheKey
	}
	return responsesReq, nil
}

func extractResponsesInstructions(system interface{}) string {
	arr, ok := system.([]interface{})
	if !ok || len(arr) == 0 {
		return extractSystemText(system)
	}

	first, ok := arr[0].(map[string]interface{})
	if !ok || first["type"] != "text" {
		return extractSystemText(system)
	}

	text, ok := first["text"].(string)
	if !ok || !strings.HasPrefix(text, "x-anthropic-billing-header:") {
		return extractSystemText(system)
	}

	return extractSystemTextBlocks(system, 1)
}

func responsesTextContentType(role string) string {
	if role == "assistant" {
		return "output_text"
	}
	return "input_text"
}

func extractClaudeToolResult(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			block, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if text, ok := block["text"].(string); ok && text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		bytes, _ := utils.MarshalJSONNoEscape(v)
		return string(bytes)
	}
}

func (p *ResponsesProvider) buildRequestURL(upstream *config.UpstreamConfig, bodyBytes []byte) (string, error) {
	if upstream.ServiceType == "gemini" {
		var responsesReq types.ResponsesRequest
		if err := json.Unmarshal(bodyBytes, &responsesReq); err != nil {
			return "", fmt.Errorf("解析 Responses 请求失败: %w", err)
		}
		model := config.RedirectModel(responsesReq.Model, upstream)
		return forwarding.BuildGeminiNativeURL(upstream.GetEffectiveBaseURL(), model, responsesReq.Stream), nil
	}
	return p.buildTargetURL(upstream), nil
}

// buildTargetURL 根据上游类型构建目标 URL
func (p *ResponsesProvider) buildTargetURL(upstream *config.UpstreamConfig) string {
	var endpoint string
	switch upstream.ServiceType {
	case "responses":
		endpoint = "/responses"
	case "claude":
		endpoint = "/messages"
	case "gemini":
		endpoint = ""
	default:
		endpoint = "/chat/completions"
	}

	return forwarding.BuildEndpointURL(upstream.GetEffectiveBaseURL(), "/v1", endpoint)
}

// ConvertToClaudeResponse 将上游响应转换为 Claude 响应
func (p *ResponsesProvider) ConvertToClaudeResponse(providerResp *types.ProviderResponse) (*types.ClaudeResponse, error) {
	var responsesResp map[string]interface{}
	if err := json.Unmarshal(providerResp.Body, &responsesResp); err != nil {
		return nil, err
	}

	claudeResp := &types.ClaudeResponse{
		ID:      generateID(),
		Type:    "message",
		Role:    "assistant",
		Content: []types.ClaudeContent{},
	}

	if id, ok := responsesResp["id"].(string); ok && id != "" {
		claudeResp.ID = id
	}

	if output, ok := responsesResp["output"].([]interface{}); ok {
		for _, rawItem := range output {
			item, ok := rawItem.(map[string]interface{})
			if !ok {
				continue
			}
			switch item["type"] {
			case "message":
				if content, ok := item["content"].([]interface{}); ok {
					for _, rawBlock := range content {
						block, ok := rawBlock.(map[string]interface{})
						if !ok {
							continue
						}
						if text, ok := block["text"].(string); ok && text != "" {
							claudeResp.Content = append(claudeResp.Content, types.ClaudeContent{Type: "text", Text: text})
						}
					}
				}
			case "function_call":
				var input interface{}
				if args, ok := item["arguments"].(string); ok && args != "" {
					_ = json.Unmarshal([]byte(args), &input)
				}
				input = sanitizeClaudeToolInput(toString(item["name"]), input)
				claudeResp.Content = append(claudeResp.Content, types.ClaudeContent{
					Type:  "tool_use",
					ID:    toString(item["call_id"]),
					Name:  toString(item["name"]),
					Input: input,
				})
			}
		}
	}

	if usageRaw, ok := responsesResp["usage"].(map[string]interface{}); ok {
		claudeResp.Usage = &types.Usage{}
		if v, ok := usageRaw["input_tokens"].(float64); ok {
			claudeResp.Usage.InputTokens = int(v)
			claudeResp.Usage.PromptTokensTotal = int(v)
		}
		if v, ok := usageRaw["output_tokens"].(float64); ok {
			claudeResp.Usage.OutputTokens = int(v)
		}
		if cacheCreation, ok := usageRaw["cache_creation_input_tokens"].(float64); ok {
			claudeResp.Usage.CacheCreationInputTokens = int(cacheCreation)
		}
		if cacheRead, ok := usageRaw["cache_read_input_tokens"].(float64); ok {
			claudeResp.Usage.CacheReadInputTokens = int(cacheRead)
		} else {
			claudeResp.Usage.CacheReadInputTokens = extractResponsesCacheReadTokens(usageRaw)
		}
		if cacheCreation5m, ok := usageRaw["cache_creation_5m_input_tokens"].(float64); ok {
			claudeResp.Usage.CacheCreation5mInputTokens = int(cacheCreation5m)
		}
		if cacheCreation1h, ok := usageRaw["cache_creation_1h_input_tokens"].(float64); ok {
			claudeResp.Usage.CacheCreation1hInputTokens = int(cacheCreation1h)
		}
		if cacheTTL, ok := usageRaw["cache_ttl"].(string); ok {
			claudeResp.Usage.CacheTTL = cacheTTL
		}
	}

	hasToolUse := false
	for _, block := range claudeResp.Content {
		if block.Type == "tool_use" {
			hasToolUse = true
			break
		}
	}
	if hasToolUse {
		claudeResp.StopReason = "tool_use"
	} else if status, _ := responsesResp["status"].(string); status == "incomplete" {
		claudeResp.StopReason = "max_tokens"
	} else {
		claudeResp.StopReason = "end_turn"
	}

	return claudeResp, nil
}

// ConvertToResponsesResponse 将上游响应转换为 Responses 格式
func (p *ResponsesProvider) ConvertToResponsesResponse(
	providerResp *types.ProviderResponse,
	upstreamType string,
	sessionID string,
) (*types.ResponsesResponse, error) {
	respMap, err := converters.JSONToMap(providerResp.Body)
	if err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	converter := converters.NewConverter(upstreamType)
	return converter.FromProviderResponse(respMap, sessionID)
}

// HandleStreamResponse 处理流式响应
func (p *ResponsesProvider) HandleStreamResponse(ctx context.Context, body io.ReadCloser) (<-chan string, <-chan error, error) {
	ctx = normalizeStreamContext(ctx)
	eventChan := make(chan string, 100)
	errChan := make(chan error, 1)

	go func() {
		defer close(eventChan)
		defer close(errChan)
		defer closeStreamBodyOnCancel(ctx, body)()
		defer body.Close()

		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		pendingEventType := ""
		messageStartSent := false
		textBlockStarted := false
		textBlockIndex := 0
		toolBlockIndex := 1
		currentTool := map[string]string{}
		var currentToolArgs strings.Builder
		latestInputTokens := 0
		latestOutputTokens := 0
		latestCacheCreationTokens := 0
		latestCacheReadTokens := 0
		latestCacheCreation5mTokens := 0
		latestCacheCreation1hTokens := 0
		latestCacheTTL := ""
		stopReason := "end_turn"

		emit := func(event string) bool {
			return sendStreamEvent(ctx, eventChan, event)
		}
		emitJSON := func(eventName string, payload map[string]interface{}) bool {
			payload["type"] = eventName
			b, _ := json.Marshal(payload)
			return emit(fmt.Sprintf("event: %s\ndata: %s\n\n", eventName, string(b)))
		}

		for scanner.Scan() {
			if isStreamContextCanceled(ctx) {
				return
			}
			line := strings.TrimSpace(normalizeSSEFieldLine(scanner.Text()))
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "event: ") {
				pendingEventType = strings.TrimPrefix(line, "event: ")
				continue
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			var data map[string]interface{}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data); err != nil {
				continue
			}

			eventType := pendingEventType
			if eventType == "" {
				eventType = toString(data["type"])
			}
			pendingEventType = ""

			switch eventType {
			case "response.output_text.delta":
				if !messageStartSent {
					if !emit(buildMessageStartEvent("responses")) {
						return
					}
					messageStartSent = true
				}
				if !textBlockStarted {
					if !emitJSON("content_block_start", map[string]interface{}{
						"index":         textBlockIndex,
						"content_block": map[string]interface{}{"type": "text", "text": ""},
					}) {
						return
					}
					textBlockStarted = true
				}
				delta := toString(data["delta"])
				if delta != "" {
					if !emitJSON("content_block_delta", map[string]interface{}{
						"index": textBlockIndex,
						"delta": map[string]interface{}{"type": "text_delta", "text": delta},
					}) {
						return
					}
				}
			case "response.output_item.added":
				item, _ := data["item"].(map[string]interface{})
				if toString(item["type"]) != "function_call" {
					continue
				}
				if !messageStartSent {
					if !emit(buildMessageStartEvent("responses")) {
						return
					}
					messageStartSent = true
				}
				if textBlockStarted {
					if !emitJSON("content_block_stop", map[string]interface{}{"index": textBlockIndex}) {
						return
					}
					textBlockStarted = false
				}
				currentTool = map[string]string{
					"id":   toString(item["call_id"]),
					"name": toString(item["name"]),
				}
				currentToolArgs.Reset()
				if currentTool["id"] == "" {
					currentTool["id"] = currentTool["name"]
				}
				if !emitJSON("content_block_start", map[string]interface{}{
					"index": toolBlockIndex,
					"content_block": map[string]interface{}{
						"type": "tool_use",
						"id":   currentTool["id"],
						"name": currentTool["name"],
					},
				}) {
					return
				}
			case "response.function_call_arguments.delta":
				if currentTool["id"] == "" {
					continue
				}
				// 先聚合完整 arguments，再一次性发给下游（便于做 JSON 级别清洗）。
				currentToolArgs.WriteString(toString(data["delta"]))
			case "response.output_item.done":
				item, _ := data["item"].(map[string]interface{})
				if toString(item["type"]) == "function_call" && currentTool["id"] != "" {
					argsJSON := currentToolArgs.String()
					if strings.TrimSpace(argsJSON) == "" {
						argsJSON = toString(item["arguments"])
					}
					if strings.TrimSpace(argsJSON) == "" {
						argsJSON = "{}"
					}
					argsJSON = sanitizeClaudeToolArgsJSON(currentTool["name"], argsJSON)

					if !emitJSON("content_block_delta", map[string]interface{}{
						"index": toolBlockIndex,
						"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": argsJSON},
					}) {
						return
					}
					if !emitJSON("content_block_stop", map[string]interface{}{"index": toolBlockIndex}) {
						return
					}
					toolBlockIndex++
					stopReason = "tool_use"
					currentTool = map[string]string{}
					currentToolArgs.Reset()
				}
			case "response.completed":
				response, _ := data["response"].(map[string]interface{})
				usage, _ := response["usage"].(map[string]interface{})
				if v, ok := usage["input_tokens"].(float64); ok {
					latestInputTokens = int(v)
				}
				if v, ok := usage["output_tokens"].(float64); ok {
					latestOutputTokens = int(v)
				}
				if v, ok := usage["cache_creation_input_tokens"].(float64); ok {
					latestCacheCreationTokens = int(v)
				}
				if v, ok := usage["cache_read_input_tokens"].(float64); ok {
					latestCacheReadTokens = int(v)
				} else {
					latestCacheReadTokens = extractResponsesCacheReadTokens(usage)
				}
				if v, ok := usage["cache_creation_5m_input_tokens"].(float64); ok {
					latestCacheCreation5mTokens = int(v)
				}
				if v, ok := usage["cache_creation_1h_input_tokens"].(float64); ok {
					latestCacheCreation1hTokens = int(v)
				}
				if v, ok := usage["cache_ttl"].(string); ok {
					latestCacheTTL = v
				}
				status := toString(response["status"])
				if status == "incomplete" {
					stopReason = "max_tokens"
				}
				if textBlockStarted {
					if !emitJSON("content_block_stop", map[string]interface{}{"index": textBlockIndex}) {
						return
					}
					textBlockStarted = false
				}
				if !messageStartSent {
					if !emit(buildMessageStartEvent("responses")) {
						return
					}
					messageStartSent = true
				}
				usagePayload := map[string]interface{}{
					"input_tokens":  latestInputTokens,
					"output_tokens": latestOutputTokens,
				}
				if latestCacheCreationTokens > 0 {
					usagePayload["cache_creation_input_tokens"] = latestCacheCreationTokens
				}
				if latestCacheReadTokens > 0 {
					usagePayload["cache_read_input_tokens"] = latestCacheReadTokens
				}
				if latestCacheCreation5mTokens > 0 {
					usagePayload["cache_creation_5m_input_tokens"] = latestCacheCreation5mTokens
				}
				if latestCacheCreation1hTokens > 0 {
					usagePayload["cache_creation_1h_input_tokens"] = latestCacheCreation1hTokens
				}
				if latestCacheTTL != "" {
					usagePayload["cache_ttl"] = latestCacheTTL
				}
				if !emitJSON("message_delta", map[string]interface{}{
					"delta": map[string]interface{}{"stop_reason": stopReason, "stop_sequence": nil},
					"usage": usagePayload,
				}) {
					return
				}
				emitJSON("message_stop", map[string]interface{}{})
			}
		}

		if err := scanner.Err(); err != nil {
			if isStreamContextCanceled(ctx) {
				return
			}
			sendStreamError(ctx, errChan, err)
		}
	}()

	return eventChan, errChan, nil
}

func extractResponsesCacheReadTokens(usage map[string]interface{}) int {
	if cacheRead, ok := usage["cache_read_input_tokens"].(float64); ok {
		return int(cacheRead)
	}
	inputDetails, ok := usage["input_tokens_details"].(map[string]interface{})
	if !ok {
		return 0
	}
	if cachedTokens, ok := inputDetails["cached_tokens"].(float64); ok {
		return int(cachedTokens)
	}
	return 0
}

func toString(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func normalizeResponsesInputForPassthrough(reqMap map[string]interface{}) {
	input, ok := reqMap["input"].([]interface{})
	if !ok {
		return
	}

	for _, rawItem := range input {
		item, ok := rawItem.(map[string]interface{})
		if !ok || toString(item["type"]) != "message" {
			continue
		}

		role := normalizeRole(toString(item["role"]))
		targetTextType := responsesTextContentType(role)
		content, ok := item["content"].([]interface{})
		if !ok {
			continue
		}

		for _, rawBlock := range content {
			block, ok := rawBlock.(map[string]interface{})
			if !ok {
				continue
			}
			blockType := toString(block["type"])
			if blockType == "input_text" || blockType == "output_text" {
				block["type"] = targetTextType
			}
		}
	}
}
