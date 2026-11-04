package providers

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// GeminiProvider Gemini 提供商
type GeminiProvider struct{}

// ConvertToProviderRequest 转换为 Gemini 请求
func (p *GeminiProvider) ConvertToProviderRequest(c *gin.Context, upstream *config.UpstreamConfig, apiKey string) (*http.Request, []byte, error) {
	// 读取和解析原始请求体
	originalBodyBytes, err := getRequestBodyBytes(c)
	if err != nil {
		return nil, nil, fmt.Errorf("读取请求体失败: %w", err)
	}

	var claudeReq types.ClaudeRequest
	if err := json.Unmarshal(originalBodyBytes, &claudeReq); err != nil {
		return nil, originalBodyBytes, fmt.Errorf("解析Claude请求体失败: %w", err)
	}

	// --- 复用旧的转换逻辑 ---
	geminiReq := p.convertToGeminiRequest(&claudeReq, upstream)
	// --- 转换逻辑结束 ---

	reqBodyBytes, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, originalBodyBytes, fmt.Errorf("序列化Gemini请求体失败: %w", err)
	}

	model := config.RedirectModel(claudeReq.Model, upstream)
	action := "generateContent"
	if claudeReq.Stream {
		action = "streamGenerateContent?alt=sse"
	}

	baseURL := strings.TrimSuffix(upstream.GetEffectiveBaseURL(), "/")
	skipVersionPrefix := strings.HasSuffix(baseURL, "#")
	if skipVersionPrefix {
		baseURL = strings.TrimSuffix(baseURL, "#")
	}
	versionPattern := regexp.MustCompile(`/v\d+[a-z]*$`)
	if !versionPattern.MatchString(baseURL) && !skipVersionPrefix {
		baseURL += "/v1beta"
	}

	url := fmt.Sprintf("%s/models/%s:%s", baseURL, model, action)

	req, err := http.NewRequestWithContext(c.Request.Context(), "POST", url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		return nil, originalBodyBytes, fmt.Errorf("创建Gemini请求失败: %w", err)
	}

	// 使用统一的头部处理逻辑（透明代理）
	// 保留客户端的大部分 headers，只移除/替换必要的认证和代理相关 headers
	req.Header = utils.PrepareUpstreamHeaders(c, req.URL.Host)
	utils.SetGeminiAuthenticationHeader(req.Header, apiKey)
	utils.ApplyCustomHeaders(req.Header, upstream.CustomHeaders)

	return req, originalBodyBytes, nil
}

// convertToGeminiRequest 转换为 Gemini 请求体
func (p *GeminiProvider) convertToGeminiRequest(claudeReq *types.ClaudeRequest, upstream *config.UpstreamConfig) map[string]interface{} {
	contents := p.convertMessages(claudeReq.Messages)

	// Claude 协议不携带 Gemini 的 thoughtSignature 字段，因此转换出的 functionCall
	// 永远缺少签名。当上游严格校验（如 vip.undyingapi.com）时会返回 400:
	// "Function call is missing a thought_signature in functionCall parts"。
	// 按渠道开关注入 dummy 签名或显式剥离字段，保持与原生 Gemini 入口一致的语义。
	if upstream != nil {
		switch {
		case upstream.StripThoughtSignature:
			// Claude→Gemini 场景下本来就不会带签名，StripThoughtSignature 在此为 no-op，
			// 但保留分支避免被下方 InjectDummyThoughtSignature 覆盖。
		case upstream.InjectDummyThoughtSignature:
			injectDummyThoughtSignatureInMapContents(contents)
		}
	}

	req := map[string]interface{}{
		"contents": contents,
	}

	// 添加系统指令
	if claudeReq.System != nil {
		systemText := extractSystemText(claudeReq.System)
		if systemText != "" {
			req["systemInstruction"] = map[string]interface{}{
				"parts": []map[string]string{
					{"text": systemText},
				},
			}
		}
	}

	// 生成配置
	genConfig := map[string]interface{}{}

	if claudeReq.MaxTokens > 0 {
		genConfig["maxOutputTokens"] = claudeReq.MaxTokens
	}

	if claudeReq.Temperature > 0 {
		genConfig["temperature"] = claudeReq.Temperature
	}

	if len(genConfig) > 0 {
		req["generationConfig"] = genConfig
	}

	// 工具
	if len(claudeReq.Tools) > 0 {
		req["tools"] = []map[string]interface{}{
			{
				"functionDeclarations": p.convertTools(claudeReq.Tools),
			},
		}
	}

	return req
}

// injectDummyThoughtSignatureInMapContents 给 map 形式的 contents 中所有 functionCall
// part 注入 part 层级的 thoughtSignature（dummy 值），用于通过 vip.undyingapi.com 等
// 第三方网关的严格校验。Google 官方 API 也接受 dummy 值。
// 仅在 part 不存在 thoughtSignature 时注入，避免覆盖客户端原始签名。
func injectDummyThoughtSignatureInMapContents(contents []map[string]interface{}) {
	for _, content := range contents {
		parts, ok := content["parts"].([]interface{})
		if !ok {
			continue
		}
		for _, p := range parts {
			partMap, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			if _, hasFunctionCall := partMap["functionCall"]; !hasFunctionCall {
				continue
			}
			if sig, exists := partMap["thoughtSignature"]; exists {
				if s, ok := sig.(string); ok && s != "" {
					continue
				}
			}
			partMap["thoughtSignature"] = types.DummyThoughtSignature
		}
	}
}

// convertMessages 转换消息
// convertMessages 转换消息
//
// Gemini 的 functionResponse.name 必须等于前面 functionCall.name（函数名），
// 否则上游无法匹配到对应的工具调用，会沉默返回空内容。Claude 协议里 tool_result
// 只携带 tool_use_id 而不带函数名，因此这里先扫一遍历史，建立 tool_use_id → name
// 的映射，转换时回查得到真函数名。
func (p *GeminiProvider) convertMessages(claudeMessages []types.ClaudeMessage) []map[string]interface{} {
	toolUseIDToName := buildToolUseIDNameMap(claudeMessages)

	messages := []map[string]interface{}{}

	for _, msg := range claudeMessages {
		geminiMsg := p.convertMessage(msg, toolUseIDToName)
		if geminiMsg != nil {
			messages = append(messages, geminiMsg)
		}
	}

	return messages
}

// buildToolUseIDNameMap 扫描 Claude 历史，收集所有 tool_use 的 id→name 映射，
// 供后续 tool_result 转换时回查函数名。
func buildToolUseIDNameMap(claudeMessages []types.ClaudeMessage) map[string]string {
	mapping := map[string]string{}
	for _, msg := range claudeMessages {
		contents, ok := msg.Content.([]interface{})
		if !ok {
			continue
		}
		for _, c := range contents {
			block, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			if blockType, _ := block["type"].(string); blockType != "tool_use" {
				continue
			}
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			if id != "" && name != "" {
				mapping[id] = name
			}
		}
	}
	return mapping
}

// convertMessage 转换单个消息
// convertMessage 转换单个消息
//
// toolUseIDToName 来自 convertMessages 预扫描出的 tool_use_id → 函数名映射，
// 用于把 tool_result 转换成 Gemini 的 functionResponse 时填入正确的函数名。
func (p *GeminiProvider) convertMessage(msg types.ClaudeMessage, toolUseIDToName map[string]string) map[string]interface{} {
	role := msg.Role
	if role == "assistant" {
		role = "model"
	}

	parts := []interface{}{}

	// 处理字符串内容
	if str, ok := msg.Content.(string); ok {
		parts = append(parts, map[string]string{
			"text": str,
		})
		return map[string]interface{}{
			"role":  role,
			"parts": parts,
		}
	}

	// 处理内容数组
	contents, ok := msg.Content.([]interface{})
	if !ok {
		return nil
	}

	for _, c := range contents {
		content, ok := c.(map[string]interface{})
		if !ok {
			continue
		}

		contentType, _ := content["type"].(string)

		switch contentType {
		case "text":
			// 跳过空文本块：Gemini API 严格校验 Part 的 oneof data 字段，
			// 空 {"text": ""} 会被部分上游网关判定为 "required oneof field 'data' must have one initialized field"，
			// Claude 在带 tool_use 的 assistant 消息中常会附带空 text 前缀，必须过滤。
			if text, ok := content["text"].(string); ok && text != "" {
				parts = append(parts, map[string]string{
					"text": text,
				})
			}

		case "tool_use":
			name, _ := content["name"].(string)
			input := content["input"]

			parts = append(parts, map[string]interface{}{
				"functionCall": map[string]interface{}{
					"name": name,
					"args": input,
				},
			})

		case "tool_result":
			toolUseID, _ := content["tool_use_id"].(string)
			resultContent := content["content"]

			// Gemini 用 functionResponse.name 匹配前面 functionCall.name（函数名）。
			// 优先从历史扫描得到的 id→name 映射回查；查不到时回退用 tool_use_id，
			// 避免完全丢字段（极端边界：tool_result 出现在没有对应 tool_use 的语境）。
			funcName := toolUseIDToName[toolUseID]
			if funcName == "" {
				funcName = toolUseID
			}

			var response interface{}
			if resultContent == nil {
				response = map[string]interface{}{"result": ""}
			} else if str, ok := resultContent.(string); ok {
				response = map[string]interface{}{"result": str}
			} else if obj, ok := resultContent.(map[string]interface{}); ok {
				response = obj
			} else if arr, ok := resultContent.([]interface{}); ok {
				// 提取 Content Blocks 中的文本
				var partsText []string
				for _, item := range arr {
					if block, ok := item.(map[string]interface{}); ok {
						if text, ok := block["text"].(string); ok && text != "" {
							partsText = append(partsText, text)
						}
					}
				}
				response = map[string]interface{}{"result": strings.Join(partsText, "\n")}
			} else {
				// 其他类型转为字符串包装
				bytes, _ := json.Marshal(resultContent)
				response = map[string]interface{}{"result": string(bytes)}
			}

			parts = append(parts, map[string]interface{}{
				"functionResponse": map[string]interface{}{
					"name":     funcName,
					"response": response,
				},
			})
		}
	}

	if len(parts) == 0 {
		return nil
	}

	return map[string]interface{}{
		"role":  role,
		"parts": parts,
	}
}

// convertTools 转换工具
//
// 处理链：cleanJsonSchema（剥离 OpenAI 通用元字段）
// → SanitizeGeminiToolSchema（剥离 Gemini 不支持的字段，例如 propertyNames、exclusiveMinimum、const）
// → normalizeGeminiParameters（确保有 type/properties，符合 Gemini 协议要求）
func (p *GeminiProvider) convertTools(claudeTools []types.ClaudeTool) []map[string]interface{} {
	tools := []map[string]interface{}{}

	for _, tool := range claudeTools {
		tools = append(tools, map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  normalizeGeminiParameters(types.SanitizeGeminiToolSchema(cleanJsonSchema(tool.InputSchema))),
		})
	}

	return tools
}

// normalizeGeminiParameters 确保参数 schema 符合 Gemini 要求
// Gemini 要求 functionDeclaration.parameters 必须是 type: "object" 且有 properties 字段
func normalizeGeminiParameters(schema interface{}) map[string]interface{} {
	// 默认空 schema
	defaultSchema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}

	if schema == nil {
		return defaultSchema
	}

	schemaMap, ok := schema.(map[string]interface{})
	if !ok {
		return defaultSchema
	}

	// 确保有 type 字段且为 "object"
	if _, hasType := schemaMap["type"]; !hasType {
		schemaMap["type"] = "object"
	}

	// 确保有 properties 字段
	if _, hasProps := schemaMap["properties"]; !hasProps {
		schemaMap["properties"] = map[string]interface{}{}
	}

	return schemaMap
}

// ConvertToClaudeResponse 转换为 Claude 响应
func (p *GeminiProvider) ConvertToClaudeResponse(providerResp *types.ProviderResponse) (*types.ClaudeResponse, error) {
	var geminiResp map[string]interface{}
	if err := json.Unmarshal(providerResp.Body, &geminiResp); err != nil {
		return nil, err
	}

	claudeResp := &types.ClaudeResponse{
		ID:      generateID(),
		Type:    "message",
		Role:    "assistant",
		Content: []types.ClaudeContent{},
	}
	if model, ok := geminiResp["modelVersion"].(string); ok {
		claudeResp.Model = model
	}

	candidates, ok := geminiResp["candidates"].([]interface{})
	if !ok || len(candidates) == 0 {
		return claudeResp, nil
	}

	candidate, ok := candidates[0].(map[string]interface{})
	if !ok {
		return claudeResp, nil
	}

	content, ok := candidate["content"].(map[string]interface{})
	if !ok {
		return claudeResp, nil
	}

	parts, ok := content["parts"].([]interface{})
	if !ok {
		return claudeResp, nil
	}

	// 处理各个部分
	for _, p := range parts {
		part, ok := p.(map[string]interface{})
		if !ok {
			continue
		}

		// 文本内容
		if text, ok := part["text"].(string); ok {
			claudeResp.Content = append(claudeResp.Content, types.ClaudeContent{
				Type: "text",
				Text: text,
			})
		}

		// 函数调用
		if fc, ok := part["functionCall"].(map[string]interface{}); ok {
			name, _ := fc["name"].(string)
			args := fc["args"]

			claudeResp.Content = append(claudeResp.Content, types.ClaudeContent{
				Type:  "tool_use",
				ID:    fmt.Sprintf("toolu_%d", len(claudeResp.Content)),
				Name:  name,
				Input: args,
			})
		}
	}

	// 设置停止原因
	finishReason, _ := candidate["finishReason"].(string)
	if strings.Contains(strings.ToLower(finishReason), "stop") {
		// 检查是否有工具调用
		hasToolCall := false
		for _, c := range claudeResp.Content {
			if c.Type == "tool_use" {
				hasToolCall = true
				break
			}
		}

		if hasToolCall {
			claudeResp.StopReason = "tool_use"
		} else {
			claudeResp.StopReason = "end_turn"
		}
	} else if strings.Contains(strings.ToLower(finishReason), "length") {
		claudeResp.StopReason = "max_tokens"
	}

	// 使用统计
	if usageMetadata, ok := geminiResp["usageMetadata"].(map[string]interface{}); ok {
		usage := &types.Usage{}
		if promptTokens, ok := usageMetadata["promptTokenCount"].(float64); ok {
			usage.InputTokens = int(promptTokens)
		}
		if candidatesTokens, ok := usageMetadata["candidatesTokenCount"].(float64); ok {
			usage.OutputTokens = int(candidatesTokens)
		}
		claudeResp.Usage = usage
	}

	return claudeResp, nil
}

// HandleStreamResponse 处理流式响应
func (p *GeminiProvider) HandleStreamResponse(body io.ReadCloser) (<-chan string, <-chan error, error) {
	eventChan := make(chan string, 100)
	errChan := make(chan error, 1)

	go func() {
		defer close(eventChan)
		// defer close(errChan) // 移除此行，避免竞态条件
		defer errutil.IgnoreDeferred(body.Close)

		scanner := bufio.NewScanner(body)
		// 设置更大的 buffer (1MB) 以处理大 JSON chunk，避免默认 64KB 限制
		const maxScannerBufferSize = 1024 * 1024 // 1MB
		scanner.Buffer(make([]byte, 0, 64*1024), maxScannerBufferSize)

		toolUseBlockIndex := 0
		messageStartSent := false
		stopReason := ""
		hasToolUse := false
		latestInputTokens := 0
		latestOutputTokens := 0

		// 文本块状态跟踪
		textBlockStarted := false
		textBlockIndex := 0

		for scanner.Scan() {
			line := normalizeSSEFieldLine(scanner.Text())
			line = strings.TrimSpace(line)

			if line == "" || line == "data: [DONE]" {
				continue
			}

			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			jsonStr := strings.TrimPrefix(line, "data: ")

			var chunk map[string]interface{}
			if err := json.Unmarshal([]byte(jsonStr), &chunk); err != nil {
				continue
			}

			// Gemini 可能在无 candidates 的 chunk 中返回 usageMetadata（通常出现在流末尾）
			// 这里必须先提取 usage，避免被后续 candidates 判断提前跳过。
			if usageMetadata, ok := chunk["usageMetadata"].(map[string]interface{}); ok {
				inputTokens := latestInputTokens
				outputTokens := latestOutputTokens

				if promptTokens, ok := usageMetadata["promptTokenCount"].(float64); ok {
					inputTokens = int(promptTokens)
				}
				if cachedTokens, ok := usageMetadata["cachedContentTokenCount"].(float64); ok {
					inputTokens -= int(cachedTokens)
				}
				if candidatesTokens, ok := usageMetadata["candidatesTokenCount"].(float64); ok {
					outputTokens = int(candidatesTokens)
				}

				if inputTokens < 0 {
					inputTokens = 0
				}
				if outputTokens < 0 {
					outputTokens = 0
				}

				// usageMetadata 可能多次出现：
				// - inputTokens: 直接覆盖，因为后续 chunk 可能包含 cachedContentTokenCount 使值变小
				// - outputTokens: 保持单调递增，因为输出 token 只会累加
				latestInputTokens = inputTokens
				if outputTokens > latestOutputTokens {
					latestOutputTokens = outputTokens
				}
			}

			candidates, ok := chunk["candidates"].([]interface{})
			if !ok || len(candidates) == 0 {
				continue
			}

			candidate, ok := candidates[0].(map[string]interface{})
			if !ok {
				continue
			}

			content, ok := candidate["content"].(map[string]interface{})
			if !ok {
				continue
			}

			parts, ok := content["parts"].([]interface{})
			if !ok {
				continue
			}

			for _, p := range parts {
				part, ok := p.(map[string]interface{})
				if !ok {
					continue
				}

				// 处理文本
				if text, ok := part["text"].(string); ok {
					// 在第一个 content_block 之前发送 message_start
					if !messageStartSent {
						eventChan <- buildMessageStartEvent("")
						messageStartSent = true
					}
					// 如果是第一个文本块,发送 content_block_start
					if !textBlockStarted {
						startEvent := map[string]interface{}{
							"type":  "content_block_start",
							"index": textBlockIndex,
							"content_block": map[string]string{
								"type": "text",
								"text": "",
							},
						}
						startJSON, _ := json.Marshal(startEvent)
						eventChan <- fmt.Sprintf("event: content_block_start\ndata: %s\n\n", startJSON)
						textBlockStarted = true
					}

					// 发送 content_block_delta
					deltaEvent := map[string]interface{}{
						"type":  "content_block_delta",
						"index": textBlockIndex,
						"delta": map[string]string{
							"type": "text_delta",
							"text": text,
						},
					}
					deltaJSON, _ := json.Marshal(deltaEvent)
					eventChan <- fmt.Sprintf("event: content_block_delta\ndata: %s\n\n", deltaJSON)
				}

				// 处理函数调用
				if fc, ok := part["functionCall"].(map[string]interface{}); ok {
					// 在第一个 content_block 之前发送 message_start
					if !messageStartSent {
						eventChan <- buildMessageStartEvent("")
						messageStartSent = true
					}
					// 如果有文本块正在进行,先关闭它
					if textBlockStarted {
						stopEvent := map[string]interface{}{
							"type":  "content_block_stop",
							"index": textBlockIndex,
						}
						stopJSON, _ := json.Marshal(stopEvent)
						eventChan <- fmt.Sprintf("event: content_block_stop\ndata: %s\n\n", stopJSON)
						textBlockStarted = false
						textBlockIndex++
					}

					name, _ := fc["name"].(string)
					args := fc["args"]
					id := generateGeminiToolCallID(toolUseBlockIndex)

					events := processToolUsePart(id, name, args, toolUseBlockIndex)
					for _, event := range events {
						eventChan <- event
					}
					hasToolUse = true
					toolUseBlockIndex++
				}
			}

			// 处理结束原因
			if finishReason, ok := candidate["finishReason"].(string); ok {
				// 如果有未关闭的文本块,先关闭它
				if textBlockStarted {
					stopEvent := map[string]interface{}{
						"type":  "content_block_stop",
						"index": textBlockIndex,
					}
					stopJSON, _ := json.Marshal(stopEvent)
					eventChan <- fmt.Sprintf("event: content_block_stop\ndata: %s\n\n", stopJSON)
					textBlockStarted = false
				}

				mappedStopReason := mapGeminiFinishReasonToClaudeStopReason(finishReason)
				if mappedStopReason != "" {
					stopReason = mappedStopReason
				}
			}
		}

		// 确保流结束时关闭任何未关闭的文本块
		if textBlockStarted {
			stopEvent := map[string]interface{}{
				"type":  "content_block_stop",
				"index": textBlockIndex,
			}
			stopJSON, _ := json.Marshal(stopEvent)
			eventChan <- fmt.Sprintf("event: content_block_stop\ndata: %s\n\n", stopJSON)
		}

		// 发送 message_delta（含 stop_reason）和 message_stop
		// 注意：必须先检查 scanner 错误，避免流读取异常时发送矛盾的正常结束事件
		if err := scanner.Err(); err != nil {
			errChan <- err
			return
		}

		if messageStartSent {
			if hasToolUse {
				stopReason = "tool_use"
			} else if stopReason == "" {
				stopReason = "end_turn"
			}
			deltaEvent := map[string]interface{}{
				"type": "message_delta",
				"delta": map[string]interface{}{
					"stop_reason":  stopReason,
					"stop_details": nil,
				},
				"usage": map[string]int{
					"input_tokens":  latestInputTokens,
					"output_tokens": latestOutputTokens,
				},
			}
			deltaJSON, _ := json.Marshal(deltaEvent)
			eventChan <- fmt.Sprintf("event: message_delta\ndata: %s\n\n", deltaJSON)

			msgStopEvent := map[string]interface{}{
				"type": "message_stop",
			}
			msgStopJSON, _ := json.Marshal(msgStopEvent)
			eventChan <- fmt.Sprintf("event: message_stop\ndata: %s\n\n", msgStopJSON)
		}
	}()

	return eventChan, errChan, nil
}

func mapGeminiFinishReasonToClaudeStopReason(finishReason string) string {
	reason := strings.ToLower(finishReason)

	switch {
	case strings.Contains(reason, "max_tokens"), strings.Contains(reason, "length"):
		return "max_tokens"
	case strings.Contains(reason, "stop"),
		strings.Contains(reason, "safety"),
		strings.Contains(reason, "recitation"),
		strings.Contains(reason, "other"):
		return "end_turn"
	default:
		return ""
	}
}

func generateGeminiToolCallID(index int) string {
	var randomBytes [16]byte
	if _, err := rand.Read(randomBytes[:]); err == nil {
		return "call_" + hex.EncodeToString(randomBytes[:])
	}

	return fmt.Sprintf("call_%d", index)
}
