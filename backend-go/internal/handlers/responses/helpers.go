package responses

import (
	"encoding/json"
	"strings"

	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/types"
)

func parseInputToItems(input interface{}) ([]types.ResponsesItem, error) {
	return types.ParseResponsesInput(input)
}

func countResponsesUserMessages(input interface{}) int {
	return len(extractResponsesUserInputMessages(input))
}

func extractLastResponsesUserInput(input interface{}) string {
	return strings.Join(extractRecentResponsesUserInputs(input), " / ")
}

func extractRecentResponsesUserInputs(input interface{}) []string {
	const maxLen = 80
	messages := extractResponsesUserInputMessages(input)
	if len(messages) == 0 {
		return nil
	}

	var parts []string
	totalLen := 0
	for i := len(messages) - 1; i >= 0; i-- {
		parts = append(parts, messages[i])
		totalLen += len([]rune(messages[i]))
		if totalLen >= maxLen {
			break
		}
	}
	for left, right := 0, len(parts)-1; left < right; left, right = left+1, right-1 {
		parts[left], parts[right] = parts[right], parts[left]
	}
	return parts
}

func extractResponsesUserInputMessages(input interface{}) []string {
	switch v := input.(type) {
	case string:
		if cleaned := cleanResponsesUserText(v); cleaned != "" {
			return []string{cleaned}
		}
		return nil
	case []interface{}:
		var texts []string
		for _, item := range v {
			m, ok := item.(map[string]interface{})
			if !ok || m["role"] != "user" {
				continue
			}
			contentTexts := extractResponsesContentTexts(m["content"])
			if len(contentTexts) == 0 {
				continue
			}
			if text := strings.TrimSpace(strings.Join(contentTexts, "\n")); text != "" {
				texts = append(texts, text)
			}
		}
		return texts
	}
	return nil
}

func extractResponsesContentTexts(content interface{}) []string {
	switch v := content.(type) {
	case string:
		if cleaned := cleanResponsesUserText(v); cleaned != "" {
			return []string{cleaned}
		}
	case []interface{}:
		var texts []string
		for _, block := range v {
			m, ok := block.(map[string]interface{})
			if !ok || m["type"] != "input_text" {
				continue
			}
			if text, ok := m["text"].(string); ok {
				if cleaned := cleanResponsesUserText(text); cleaned != "" {
					texts = append(texts, cleaned)
				}
			}
		}
		return texts
	}
	return nil
}

func cleanResponsesUserText(text string) string {
	text = removeResponsesTaggedBlocks(text, "system-reminder")
	text = removeResponsesTaggedBlocks(text, "local-command-caveat")
	text = removeResponsesTaggedBlocks(text, "command-name")
	text = removeResponsesTaggedBlocks(text, "command-message")
	text = removeResponsesTaggedBlocks(text, "command-args")
	text = removeResponsesTaggedBlocks(text, "local-command-stdout")
	text = removeResponsesTaggedBlocks(text, "local-command-stderr")
	text = strings.TrimSpace(text)
	if common.IsClaudeNoVisibleOutputRetryPrompt(text) {
		return ""
	}
	if isInjectedContextTitleText(text) {
		return ""
	}
	if strings.HasPrefix(text, "<") && strings.Contains(text, ">") {
		return ""
	}
	return text
}

func isInjectedContextTitleText(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	injectedPrefixes := []string{
		"# agents.md instructions",
		"# claude.md instructions",
		"# codebase and user instructions",
		"<instructions>",
	}
	for _, prefix := range injectedPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return strings.Contains(lower, "project-doc") && strings.Contains(lower, "agents.md")
}

func removeResponsesTaggedBlocks(text, tag string) string {
	for {
		start := strings.Index(text, "<"+tag+">")
		if start < 0 {
			return text
		}
		endTag := "</" + tag + ">"
		end := strings.Index(text[start:], endTag)
		if end < 0 {
			return strings.TrimSpace(text[:start])
		}
		end += start + len(endTag)
		text = text[:start] + text[end:]
	}
}

// hasResponsesFunctionCall 检查 Responses 事件中是否包含工具调用
func hasResponsesFunctionCall(event string) bool {
	lines := strings.Split(event, "\n")
	for _, line := range lines {
		var jsonStr string
		if strings.HasPrefix(line, "data:") {
			jsonStr = strings.TrimPrefix(line, "data:")
			jsonStr = strings.TrimPrefix(jsonStr, " ")
		} else {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		// 检查 response.output 中是否有 function_call 类型
		if response, ok := data["response"].(map[string]interface{}); ok {
			if output, ok := response["output"].([]interface{}); ok {
				for _, item := range output {
					if itemMap, ok := item.(map[string]interface{}); ok {
						if itemType, ok := itemMap["type"].(string); ok && itemType == "function_call" {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func buildResponsesPreflightDiagnostic(seenEvent, seenCompleted, seenUsageOnly, seenUnknown bool, unknownEventType, text string) string {
	switch {
	case !seenEvent:
		return "未收到任何转换后的 Responses 事件"
	case seenUsageOnly && common.IsEffectivelyEmptyStreamText(text):
		return "仅收到 usage/计数类 Responses 事件，没有文本或语义内容"
	case seenUnknown && common.IsEffectivelyEmptyStreamText(text):
		if unknownEventType != "" {
			return "收到了未识别的 Responses 事件类型=" + unknownEventType + "，但没有文本或语义内容"
		}
		return "收到了未识别的 Responses 事件类型，但没有文本或语义内容"
	case seenCompleted && common.IsEffectivelyEmptyStreamText(text):
		return "流正常结束(response.completed)，但未检测到文本或语义内容"
	default:
		return "检测到空的 Responses 流，但未匹配到明确类别"
	}
}

func isResponsesUsageOnlyEvent(event string) bool {
	lines := strings.Split(event, "\n")
	for _, line := range lines {
		var jsonStr string
		if strings.HasPrefix(line, "data:") {
			jsonStr = strings.TrimPrefix(line, "data:")
			jsonStr = strings.TrimPrefix(jsonStr, " ")
		} else {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}
		if data["type"] == "response.completed" {
			if response, ok := data["response"].(map[string]interface{}); ok {
				if usage, ok := response["usage"].(map[string]interface{}); ok && len(usage) > 0 {
					if output, ok := response["output"].([]interface{}); !ok || len(output) == 0 {
						return true
					}
				}
			}
		}
	}
	return false
}

func isCompactionV2UsageOnlyStream(isCompactionV2, seenCompleted, seenUsageOnly bool) bool {
	return isCompactionV2 && seenCompleted && seenUsageOnly
}

func firstUnknownResponsesEventType(event string) (string, bool) {
	knownTypes := map[string]struct{}{
		"response.created": {}, "response.in_progress": {}, "response.incomplete": {},
		"response.output_text.delta": {}, "response.function_call_arguments.delta": {}, "response.function_call_arguments.done": {},
		"response.custom_tool_call_input.delta": {}, "response.custom_tool_call_input.done": {},
		"response.reasoning_summary_text.delta": {}, "response.reasoning_summary_text.done": {}, "response.reasoning_summary_part.added": {}, "response.reasoning_summary_part.done": {},
		"response.output_json.delta": {}, "response.content_part.added": {}, "response.content_part.delta": {}, "response.content_part.done": {}, "response.audio.delta": {}, "response.audio_transcript.delta": {},
		"response.output_item.added": {}, "response.output_item.done": {}, "response.completed": {},
		"response.error": {}, "response.failed": {}, "error": {}, "keepalive": {},
	}
	lines := strings.Split(event, "\n")
	for _, line := range lines {
		var jsonStr string
		if strings.HasPrefix(line, "data:") {
			jsonStr = strings.TrimPrefix(line, "data:")
			jsonStr = strings.TrimPrefix(jsonStr, " ")
		} else {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}
		if t, _ := data["type"].(string); t != "" {
			if _, ok := knownTypes[t]; !ok {
				return t, true
			}
		}
	}
	return "", false
}

type responsesStreamErrorInfo struct {
	Type    string
	Code    string
	Message string
}

func detectResponsesStreamError(event, sseEventName string) (responsesStreamErrorInfo, bool) {
	for _, line := range strings.Split(event, "\n") {
		var jsonStr string
		if strings.HasPrefix(line, "data:") {
			jsonStr = strings.TrimPrefix(line, "data:")
			jsonStr = strings.TrimPrefix(jsonStr, " ")
		} else {
			continue
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		eventType, _ := data["type"].(string)
		if eventType == "" {
			eventType = sseEventName
		}
		if !isResponsesErrorEventType(eventType) && !isResponsesErrorEventType(sseEventName) {
			continue
		}

		info := extractResponsesErrorInfo(data)
		if info.Message == "" {
			info.Message = "upstream returned a Responses error event"
		}
		return info, true
	}
	return responsesStreamErrorInfo{}, false
}

func isResponsesErrorEventType(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "error", "response.error", "response.failed":
		return true
	default:
		return false
	}
}

func extractResponsesErrorInfo(data map[string]interface{}) responsesStreamErrorInfo {
	if errObj, ok := data["error"].(map[string]interface{}); ok {
		return responsesErrorInfoFromMap(errObj)
	}
	if response, ok := data["response"].(map[string]interface{}); ok {
		if errObj, ok := response["error"].(map[string]interface{}); ok {
			return responsesErrorInfoFromMap(errObj)
		}
	}
	if errMsg, ok := data["error"].(string); ok {
		return responsesStreamErrorInfo{Message: errMsg}
	}
	if msg, ok := data["message"].(string); ok {
		return responsesStreamErrorInfo{Message: msg}
	}
	return responsesStreamErrorInfo{}
}

func responsesErrorInfoFromMap(errObj map[string]interface{}) responsesStreamErrorInfo {
	errType, _ := errObj["type"].(string)
	errCode, _ := errObj["code"].(string)
	errMsg, _ := errObj["message"].(string)
	return responsesStreamErrorInfo{
		Type:    errType,
		Code:    errCode,
		Message: errMsg,
	}
}

func detectResponsesErrorBlacklist(info responsesStreamErrorInfo) (reason, message string) {
	errType := info.Type
	if errType == "" {
		errType = info.Code
	}
	errObj := map[string]interface{}{
		"type":    errType,
		"code":    info.Code,
		"message": info.Message,
	}
	payload, err := json.Marshal(map[string]interface{}{
		"type":  "error",
		"error": errObj,
	})
	if err != nil {
		return "", ""
	}
	return common.DetectStreamBlacklistError("event: error\ndata: " + string(payload) + "\n\n")
}

func isRetryableResponsesError(info responsesStreamErrorInfo) bool {
	code := strings.ToLower(strings.TrimSpace(info.Code))
	errType := strings.ToLower(strings.TrimSpace(info.Type))
	message := strings.ToLower(strings.TrimSpace(info.Message))

	switch code {
	case "server_is_overloaded", "slow_down", "rate_limit_exceeded", "rate_limit", "temporarily_unavailable",
		"service_unavailable", "server_error", "internal_error", "timeout":
		return true
	}
	switch errType {
	case "service_unavailable_error", "server_error", "rate_limit_error", "timeout_error":
		return true
	}
	return strings.Contains(message, "server") && strings.Contains(message, "overload")
}

func formatResponsesErrorDiagnostic(info responsesStreamErrorInfo) string {
	parts := make([]string, 0, 2)
	if info.Code != "" {
		parts = append(parts, info.Code)
	}
	if info.Type != "" && info.Type != info.Code {
		parts = append(parts, info.Type)
	}
	if info.Message == "" {
		if len(parts) == 0 {
			return "上游返回 Responses 错误事件"
		}
		return "上游返回 Responses 错误: " + strings.Join(parts, "/")
	}
	if len(parts) == 0 {
		return "上游返回 Responses 错误: " + info.Message
	}
	return "上游返回 Responses 错误: " + info.Message + " (" + strings.Join(parts, "/") + ")"
}
