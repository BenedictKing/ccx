package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

func chatStreamHasDataActivity(lines []string) bool {
	for _, line := range lines {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		jsonData := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if jsonData != "" && jsonData != "[DONE]" {
			return true
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
	timeouts common.StreamPreflightTimeouts,
	progress *common.StreamProgressLogger,
) (*types.Usage, error) {
	var totalUsage *types.Usage
	buf := make([]byte, 32*1024)
	var remainder string
	pending := prefetched
	inactivityTimeout := time.Duration(timeouts.InactivityTimeoutMs) * time.Millisecond
	lastActivity := time.Now()

	for {
		var chunk []byte
		var readErr error
		if len(pending) > 0 {
			chunk = pending
			pending = nil
		} else {
			remaining := inactivityTimeout - time.Since(lastActivity)
			if remaining <= 0 {
				progress.Finish("stalled")
				return nil, common.ErrStreamPostCommitStalled
			}
			n, err, timedOut := readChunkWithTimeout(resp, buf, remaining)
			if timedOut {
				progress.Finish("stalled")
				return nil, common.ErrStreamPostCommitStalled
			}
			readErr = err
			if n > 0 {
				chunk = buf[:n]
				lastActivity = time.Now()
			}
		}

		if len(chunk) > 0 {
			common.MarkStreamActivity(c)
			progress.AddBytes(len(chunk))
			progress.Tick()
			if loggingEnabled {
				_, _ = logBuffer.Write(chunk)
			}
			// 使用行缓冲机制避免跨 chunk 截断
			data := remainder + string(chunk)
			lines := strings.Split(data, "\n")
			remainder = lines[len(lines)-1]
			completeLines := lines[:len(lines)-1]

			// 尝试从完整行中提取 usage
			for _, line := range completeLines {
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				jsonData := strings.TrimPrefix(line, "data: ")
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
			_, _ = c.Writer.Write(chunk)
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if remainder != "" {
				flushCompletePassthroughRemainder(c, flusher, remainder)
			}
			break
		}
	}

	progress.Finish("completed")
	return totalUsage, nil
}

// flushCompletePassthroughRemainder 在上游流以未终结的完整 data 行收尾时补齐 SSE 终止符。
// 尾行正文已随原始 chunk 原样透传给客户端，这里只补 "\n\n"；若重发正文，
// 客户端 SSE 解析器会把两份拼到同一行，产生尾部重复输出/JSON 损坏。
func flushCompletePassthroughRemainder(c *gin.Context, flusher http.Flusher, remainder string) {
	trimmed := strings.TrimSpace(remainder)
	if !strings.HasPrefix(trimmed, "data: ") {
		return
	}
	jsonData := strings.TrimPrefix(trimmed, "data: ")
	if jsonData != "[DONE]" && !json.Valid([]byte(jsonData)) {
		return
	}
	_, _ = fmt.Fprintf(c.Writer, "\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func readChunkWithTimeout(resp *http.Response, buf []byte, timeout time.Duration) (int, error, bool) {
	type timeoutReader interface {
		ReadWithTimeout(p []byte, timeout time.Duration) (int, error, bool)
	}
	if tr, ok := resp.Body.(timeoutReader); ok {
		return tr.ReadWithTimeout(buf, timeout)
	}
	n, err := resp.Body.Read(buf)
	return n, err, false
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
	timeouts common.StreamPreflightTimeouts,
	progress *common.StreamProgressLogger,
) (*types.Usage, error) {
	var totalUsage *types.Usage
	var doneSent bool
	buf := make([]byte, 32*1024)
	var remainder string
	pending := prefetched
	inactivityTimeout := time.Duration(timeouts.InactivityTimeoutMs) * time.Millisecond
	lastActivity := time.Now()

	for {
		var chunk []byte
		var readErr error
		if len(pending) > 0 {
			chunk = pending
			pending = nil
		} else {
			remaining := inactivityTimeout - time.Since(lastActivity)
			if remaining <= 0 {
				progress.Finish("stalled")
				return nil, common.ErrStreamPostCommitStalled
			}
			n, err, timedOut := readChunkWithTimeout(resp, buf, remaining)
			if timedOut {
				progress.Finish("stalled")
				return nil, common.ErrStreamPostCommitStalled
			}
			readErr = err
			if n > 0 {
				chunk = buf[:n]
				lastActivity = time.Now()
			}
		}

		if len(chunk) > 0 {
			common.MarkStreamActivity(c)
			progress.AddBytes(len(chunk))
			progress.Tick()
			if loggingEnabled {
				_, _ = logBuffer.Write(chunk)
			}
			data := remainder + string(chunk)
			lines := strings.Split(data, "\n")
			remainder = lines[len(lines)-1]
			lines = lines[:len(lines)-1]
			for _, line := range lines {
				processClaudeChatStreamLine(c, flusher, model, line, &totalUsage, &doneSent)
			}
		}

		if readErr != nil {
			if remainder != "" {
				processClaudeChatStreamLine(c, flusher, model, remainder, &totalUsage, &doneSent)
			}
			break
		}
	}

	if !doneSent {
		_, _ = fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}

	progress.Finish("completed")
	return totalUsage, nil
}

// flushResponsesSSEEvent 处理缓存的 Responses SSE data 行，生成对应的 Chat chunk。

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
	timeouts common.StreamPreflightTimeouts,
	progress *common.StreamProgressLogger,
) (*types.Usage, error) {
	var totalUsage *types.Usage
	var doneSent bool
	var roleSent bool
	buf := make([]byte, 32*1024)
	var remainder string
	pending := prefetched
	inactivityTimeout := time.Duration(timeouts.InactivityTimeoutMs) * time.Millisecond
	lastActivity := time.Now()
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
			remaining := inactivityTimeout - time.Since(lastActivity)
			if remaining <= 0 {
				progress.Finish("stalled")
				return nil, common.ErrStreamPostCommitStalled
			}
			n, err, timedOut := readChunkWithTimeout(resp, buf, remaining)
			if timedOut {
				progress.Finish("stalled")
				return nil, common.ErrStreamPostCommitStalled
			}
			readErr = err
			if n > 0 {
				chunk = buf[:n]
				lastActivity = time.Now()
			}
		}

		if len(chunk) > 0 {
			common.MarkStreamActivity(c)
			progress.AddBytes(len(chunk))
			progress.Tick()
			if loggingEnabled {
				_, _ = logBuffer.Write(chunk)
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
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				jsonData := strings.TrimPrefix(line, "data: ")
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
					_, _ = fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
					if flusher != nil {
						flusher.Flush()
					}
					doneSent = true
				}
			}
		}

		if readErr != nil {
			break
		}
	}

	if !doneSent {
		_, _ = fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}

	progress.Finish("completed")
	return totalUsage, nil
}

// writeChatSSEChunk 将 Chat chunk 写为 SSE 格式并 flush。
func writeChatSSEChunk(c *gin.Context, flusher http.Flusher, chunk map[string]interface{}) {
	chunkBytes, _ := json.Marshal(chunk)
	_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
	if flusher != nil {
		flusher.Flush()
	}
}

func processClaudeChatStreamLine(c *gin.Context, flusher http.Flusher, model string, line string, totalUsage **types.Usage, doneSent *bool) {
	if !strings.HasPrefix(line, "data: ") {
		return
	}
	jsonData := strings.TrimPrefix(line, "data: ")
	if jsonData == "[DONE]" {
		_, _ = fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
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
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
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
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
			if flusher != nil {
				flusher.Flush()
			}
		}
	case "message_delta":
		stopChunk := map[string]interface{}{
			"id":      "chatcmpl-claude",
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   model,
			"choices": []map[string]interface{}{{
				"index":         0,
				"delta":         map[string]interface{}{},
				"finish_reason": "stop",
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
		_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunkBytes))
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
