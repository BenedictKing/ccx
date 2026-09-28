// Package common 提供 handlers 模块的公共功能
package common

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/google/uuid"
)

const streamEmptyRetryClientMessage = "Empty response from upstream; please try again."

func streamErrorClientMessage(err error) string {
	if errors.Is(err, ErrStreamPostCommitStalled) || errors.Is(err, ErrEmptyStreamResponse) {
		return streamEmptyRetryClientMessage
	}
	return fmt.Sprintf("Stream processing error: %v", err)
}

// BuildStreamErrorEvent 构建流错误 SSE 事件
func BuildStreamErrorEvent(err error) string {
	errorEvent := map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    "stream_error",
			"message": streamErrorClientMessage(err),
		},
	}
	eventJSON, _ := json.Marshal(errorEvent)
	return fmt.Sprintf("event: error\ndata: %s\n\n", eventJSON)
}

// BuildUsageEvent 构建带 usage 的 message_delta SSE 事件
func BuildUsageEvent(requestBody []byte, outputText string) string {
	inputTokens := utils.EstimateRequestTokens(requestBody)
	outputTokens := utils.EstimateTokens(outputText)

	event := map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
			"stop_details":  nil,
		},
		"usage": map[string]int{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
	}
	eventJSON, _ := json.Marshal(event)
	return fmt.Sprintf("event: message_delta\ndata: %s\n\n", eventJSON)
}

// IsMessageStartEvent 检测是否为 message_start 事件
func IsMessageStartEvent(event string) bool {
	return strings.Contains(event, "\"type\":\"message_start\"") ||
		strings.Contains(event, "\"type\": \"message_start\"")
}

// PatchMessageStartEvent 修补 message_start 事件中的 id 和 model 字段
func PatchMessageStartEvent(event string, requestModel string, rewriteModel bool, enableLog bool) string {
	return patchMessageStartEventWithLogTag(event, requestModel, rewriteModel, enableLog, "")
}

func patchMessageStartEventWithLogTag(event string, requestModel string, rewriteModel bool, enableLog bool, logTag string) string {
	if !IsMessageStartEvent(event) {
		return event
	}

	var result strings.Builder
	lines := strings.Split(event, "\n")
	patched := false

	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		jsonStr := strings.TrimPrefix(line, "data: ")
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		msg, ok := data["message"].(map[string]interface{})
		if !ok {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}

		// 补全空 id
		if id, _ := msg["id"].(string); id == "" {
			msg["id"] = fmt.Sprintf("msg_%s", uuid.New().String())
			patched = true
			if enableLog {
				logWithTag(logTag, "[Messages-Stream-Patch] 补全空 message.id: %s", msg["id"])
			}
		}

		// 检查 model 一致性（仅在配置启用时改写）
		if rewriteModel {
			if responseModel, _ := msg["model"].(string); responseModel != "" && requestModel != "" && responseModel != requestModel {
				msg["model"] = requestModel
				patched = true
				if enableLog {
					logWithTag(logTag, "[Messages-Stream-Patch] 改写 message.model: %s -> %s", responseModel, requestModel)
				}
			}
		}

		if patched {
			patchedJSON, err := json.Marshal(data)
			if err != nil {
				result.WriteString(line)
				result.WriteString("\n")
				continue
			}
			result.WriteString("data: ")
			result.Write(patchedJSON)
			result.WriteString("\n")
		} else {
			result.WriteString(line)
			result.WriteString("\n")
		}
	}

	return result.String()
}

// IsMessageStopEvent 检测是否为 message_stop 事件
func IsMessageStopEvent(event string) bool {
	if strings.Contains(event, "event: message_stop") {
		return true
	}

	for _, line := range strings.Split(event, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		jsonStr := strings.TrimPrefix(line, "data: ")

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		if data["type"] == "message_stop" {
			return true
		}
	}
	return false
}

// IsMessageDeltaEvent 检测是否为 message_delta 事件
func IsMessageDeltaEvent(event string) bool {
	if strings.Contains(event, "event: message_delta") {
		return true
	}
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}
		if data["type"] == "message_delta" {
			return true
		}
	}
	return false
}

// ExtractInputTokensFromEvent 从 SSE 事件中提取 input_tokens
// 支持 message_start 事件的 message.usage.input_tokens 和顶层 usage.input_tokens
func ExtractInputTokensFromEvent(event string) int {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		// 检查 message.usage.input_tokens (message_start 事件)
		if msg, ok := data["message"].(map[string]interface{}); ok {
			if usage, ok := msg["usage"].(map[string]interface{}); ok {
				if v, ok := usage["input_tokens"].(float64); ok && v > 0 {
					return int(v)
				}
			}
		}

		// 检查顶层 usage.input_tokens (message_delta 事件)
		if usage, ok := data["usage"].(map[string]interface{}); ok {
			if v, ok := usage["input_tokens"].(float64); ok && v > 0 {
				return int(v)
			}
		}
	}
	return 0
}

// ExtractTextFromEvent 从 SSE 事件中提取文本内容
func ExtractTextFromEvent(event string, buf *bytes.Buffer) {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		// Claude SSE: delta.text
		if delta, ok := data["delta"].(map[string]interface{}); ok {
			if text, ok := delta["text"].(string); ok {
				buf.WriteString(text)
			}
			if partialJSON, ok := delta["partial_json"].(string); ok {
				buf.WriteString(partialJSON)
			}
		}

		// content_block_start 中的初始文本
		if cb, ok := data["content_block"].(map[string]interface{}); ok {
			if text, ok := cb["text"].(string); ok {
				buf.WriteString(text)
			}
		}
	}
}

// ExtractThinkingFromEvent 从 SSE 事件中提取 thinking 内容
func ExtractThinkingFromEvent(event string, buf *bytes.Buffer) {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		if cb, ok := data["content_block"].(map[string]interface{}); ok {
			if cbType, _ := cb["type"].(string); cbType == "thinking" || cbType == "redacted_thinking" {
				if thinking, ok := cb["thinking"].(string); ok {
					buf.WriteString(thinking)
				}
			}
		}

		if delta, ok := data["delta"].(map[string]interface{}); ok {
			if deltaType, _ := delta["type"].(string); deltaType == "thinking_delta" || deltaType == "redacted_thinking_delta" {
				if thinking, ok := delta["thinking"].(string); ok {
					buf.WriteString(thinking)
				}
				if text, ok := delta["text"].(string); ok {
					buf.WriteString(text)
				}
			}
		}
	}
}

// DetectStreamBlacklistError 检测 SSE error 事件中是否包含应拉黑 Key 的错误
// 返回 (reason, message)，reason 非空表示应拉黑
func DetectStreamBlacklistError(event string) (reason string, message string) {
	// 检查是否为 error 事件
	isErrorEvent := false
	for _, line := range strings.Split(event, "\n") {
		if strings.HasPrefix(line, "event: ") {
			if strings.TrimPrefix(line, "event: ") == "error" {
				isErrorEvent = true
			}
			break
		}
	}

	// 即使不是显式的 event: error，也检查 data 中的 type == "error"
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		// Claude 格式: {"type":"error","error":{"type":"authentication_error","message":"..."}}
		if dataType, _ := data["type"].(string); dataType == "error" || isErrorEvent {
			if errObj, ok := data["error"].(map[string]interface{}); ok {
				errType, _ := errObj["type"].(string)
				errMsg, _ := errObj["message"].(string)
				errCode, _ := errObj["code"].(string)

				typeLower := strings.ToLower(errType)

				// 认证错误
				if typeLower == "authentication_error" || typeLower == "invalid_api_key" {
					return "authentication_error", truncateMsg(errMsg)
				}
				if isAuthenticationMessage(errMsg) {
					return "authentication_error", truncateMsg(errMsg)
				}
				// 权限错误
				if typeLower == "permission_error" || typeLower == "permission_denied" {
					return "permission_error", truncateMsg(errMsg)
				}
				if isPermissionMessage(errMsg) {
					return "permission_error", truncateMsg(errMsg)
				}
				// 余额不足（明确的错误类型或错误码）
				if typeLower == "insufficient_balance" || typeLower == "insufficient_quota" || typeLower == "billing_error" {
					return "insufficient_balance", truncateMsg(errMsg)
				}
				// 已知的余额不足错误码（如 Kimi 的 1113）
				if isInsufficientBalanceCode(errCode) || isInsufficientBalanceMessage(errMsg) {
					return "insufficient_balance", truncateMsg(errMsg)
				}
			}
			if errStr, ok := data["error"].(string); ok {
				if isAuthenticationMessage(errStr) {
					return "authentication_error", truncateMsg(errStr)
				}
				if isPermissionMessage(errStr) {
					return "permission_error", truncateMsg(errStr)
				}
				if isInsufficientBalanceMessage(errStr) {
					return "insufficient_balance", truncateMsg(errStr)
				}
			}
			if msg, ok := data["message"].(string); ok {
				if isAuthenticationMessage(msg) {
					return "authentication_error", truncateMsg(msg)
				}
				if isPermissionMessage(msg) {
					return "permission_error", truncateMsg(msg)
				}
				if isInsufficientBalanceMessage(msg) {
					return "insufficient_balance", truncateMsg(msg)
				}
			}
		}
	}
	return "", ""
}

// isInsufficientBalanceCode 检查错误码是否为已知的余额不足代码
func isInsufficientBalanceCode(code string) bool {
	knownCodes := []string{
		"1113",                 // Kimi: 余额不足或无可用资源包
		"INSUFFICIENT_BALANCE", // 通用余额不足
		"INSUFFICIENT_QUOTA",   // 通用额度不足
		"INSUFFICIENT_USER_QUOTA",
		"API_KEY_QUOTA_EXHAUSTED",
		"USAGE_LIMIT_EXCEEDED",   // 当日/周期额度耗尽
		"DAILY_LIMIT_EXCEEDED",   // 当日额度耗尽
		"SUBSCRIPTION_NOT_FOUND", // 订阅不存在/未激活
		"SUBSCRIPTION_INVALID",
		"PRE_CONSUME_TOKEN_QUOTA_FAILED",
		"PRE_CONSUME_QUOTA_FAILED",
	}
	for _, c := range knownCodes {
		if strings.EqualFold(code, c) {
			return true
		}
	}
	return false
}

// DetectStreamDataError 检测单条 SSE data JSON 是否为错误事件，并复用拉黑分类词汇表。
// 覆盖 chat/gemini 客户端路径的原始上游错误形态（这两个协议的预检测发生在
// 协议转换之前，只能按原始 data 识别）：
//   - OpenAI/Gemini 风格：顶层 "error" 键（对象或字符串）
//   - Claude 风格：顶层 type=="error"（详情在 error 对象或退化到 message 键）
//   - Responses 风格：type=="response.failed"/"response.error"（详情在 response.error）
//
// jsonData 为去掉 "data:" 前缀后的 JSON 文本；[DONE] 与普通内容 chunk 返回 isErr=false。
// isErr=true 且 reason 非空表示应拉黑 Key（recoverAt 为上游文案中的恢复时间，可为空）；
// isErr=true 且 reason 为空表示普通上游错误（Header 未写，可安全 failover）。
func DetectStreamDataError(jsonData string) (reason string, message string, recoverAt string, isErr bool) {
	jsonData = strings.TrimSpace(jsonData)
	if jsonData == "" || jsonData == "[DONE]" {
		return "", "", "", false
	}
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		return "", "", "", false
	}

	eventType, _ := data["type"].(string)
	var errType, errCode, errMsg string
	hasError := false

	if errObj, ok := data["error"].(map[string]interface{}); ok {
		errType, _ = errObj["type"].(string)
		errMsg, _ = errObj["message"].(string)
		if code, ok := errObj["code"].(string); ok {
			errCode = code
		} else if codeNum, ok := errObj["code"].(float64); ok {
			errCode = fmt.Sprintf("%d", int(codeNum))
		}
		hasError = true
	} else if errStr, ok := data["error"].(string); ok {
		errMsg = errStr
		hasError = true
	} else if eventType == "error" {
		// Claude 退化形态：type=="error" 且错误详情在顶层 message
		if msg, ok := data["message"].(string); ok && strings.TrimSpace(msg) != "" {
			errMsg = msg
			hasError = true
		}
	} else if eventType == "response.failed" || eventType == "response.error" {
		if response, ok := data["response"].(map[string]interface{}); ok {
			if errObj, ok := response["error"].(map[string]interface{}); ok {
				errType, _ = errObj["type"].(string)
				errMsg, _ = errObj["message"].(string)
				errCode, _ = errObj["code"].(string)
			}
		}
		hasError = true
	}

	if !hasError {
		return "", "", "", false
	}
	if strings.TrimSpace(errMsg) == "" {
		errMsg = "upstream returned an error event"
	}

	// 拉黑分类复用 DetectStreamBlacklistError 的词汇表，保持单一事实源。
	// type 缺失时用 code 回填（与 responses 预检的 detectResponsesErrorBlacklist
	// 及 ShouldBlacklistKey 的 code 兜底语义一致）。
	payloadType := errType
	if strings.TrimSpace(payloadType) == "" {
		payloadType = errCode
	}
	payload, err := json.Marshal(map[string]interface{}{
		"type": "error",
		"error": map[string]interface{}{
			"type":    payloadType,
			"code":    errCode,
			"message": errMsg,
		},
	})
	if err == nil {
		if r, m := DetectStreamBlacklistError("event: error\ndata: " + string(payload) + "\n\n"); r != "" {
			return r, m, utils.ExtractQuotaRecoverAt(m), true
		}
	}
	return "", truncateMsg(errMsg), "", true
}

// ClassifyPreflightStreamError 将预检测阶段的流错误统一映射为 failover 家族错误。
// 预检测完成前 Header 未写，任何非客户端、非哨兵错误（provider 裸包装的上游错误
// 事件、传输层错误）都可以安全换 Key/渠道重试；统一归入 ErrEmptyStreamResponse
// 语义，避免落入 failover 执行器的"真实渠道故障"分支直接终结整个 failover 链。
func ClassifyPreflightStreamError(err error) error {
	if err == nil {
		return nil
	}
	if isClientSideError(err) {
		return err
	}
	var blErr *ErrBlacklistKey
	switch {
	case errors.Is(err, ErrStreamFirstContentTimeout),
		errors.Is(err, ErrStreamStalled),
		errors.Is(err, ErrEmptyStreamResponse),
		errors.Is(err, ErrInvalidResponseBody),
		errors.Is(err, ErrEmptyNonStreamResponse),
		errors.Is(err, ErrRacingSuperseded),
		errors.As(err, &blErr):
		return err
	}
	return fmt.Errorf("%w: %v", ErrEmptyStreamResponse, err)
}

// truncateMsg 截断消息（最多200字符）
func truncateMsg(msg string) string {
	if len(msg) > 200 {
		return msg[:200]
	}
	return msg
}

// extractSSEEventInfo 从 SSE 事件中提取事件类型、block 索引和 block 类型
func extractSSEEventInfo(event string) (eventType string, blockIndex int, blockType string) {
	for _, line := range strings.Split(event, "\n") {
		jsonStr, ok := extractSSEJSONLine(line)
		if !ok {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		eventType, _ = data["type"].(string)
		if idx, ok := data["index"].(float64); ok {
			blockIndex = int(idx)
		}

		// 从 content_block 中提取类型
		if cb, ok := data["content_block"].(map[string]interface{}); ok {
			blockType, _ = cb["type"].(string)
		}

		return
	}
	return
}

// truncateForLog 截断字符串用于日志输出
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
