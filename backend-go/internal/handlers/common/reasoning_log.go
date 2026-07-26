package common

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/tidwall/gjson"
)

func extractReasoningEffortForLog(bodyBytes []byte) string {
	if len(bytes.TrimSpace(bodyBytes)) == 0 || !gjson.ValidBytes(bodyBytes) {
		return ""
	}

	if value := strings.TrimSpace(gjson.GetBytes(bodyBytes, "thinking.type").String()); strings.EqualFold(value, "disabled") {
		return "none"
	}

	for _, path := range []string{
		"reasoning_effort",
		"reasoning.effort",
		"reasoning",
		"thinking.effort",
		"output_config.effort",
		"generationConfig.thinkingConfig.thinkingLevel",
		"thinkingConfig.thinkingLevel",
		"thinking.thinkingLevel",
	} {
		if value := stringReasoningValue(bodyBytes, path); value != "" {
			return value
		}
	}

	for _, path := range []string{
		"generationConfig.thinkingConfig.thinkingBudget",
		"thinkingConfig.thinkingBudget",
		"thinking.budget_tokens",
		"thinking.budgetTokens",
	} {
		if value := gjson.GetBytes(bodyBytes, path); value.Exists() {
			return "budget=" + formatReasoningNumber(value)
		}
	}

	for _, path := range []string{
		"generationConfig.thinkingConfig.includeThoughts",
		"thinkingConfig.includeThoughts",
	} {
		if value := gjson.GetBytes(bodyBytes, path); value.Exists() && value.Bool() {
			return "enabled"
		}
	}

	if value := strings.TrimSpace(gjson.GetBytes(bodyBytes, "thinking.type").String()); value != "" {
		return value
	}

	return ""
}

// extractActualRequestLogDetails 从最终构建的上游请求读取模型和思考等级。
// 请求体会被恢复，调用方可继续正常发送该请求。
func extractActualRequestLogDetails(req *http.Request) (model, reasoningEffort string) {
	bodyBytes := snapshotRequestBodyForLog(req)
	if len(bodyBytes) == 0 {
		return "", ""
	}
	return strings.TrimSpace(gjson.GetBytes(bodyBytes, "model").String()), extractReasoningEffortForLog(bodyBytes)
}

func snapshotRequestBodyForLog(req *http.Request) []byte {
	if req == nil || req.Body == nil {
		return nil
	}
	contentType := strings.ToLower(req.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		return nil
	}

	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil
		}
		defer errutil.IgnoreDeferred(body.Close)
		bodyBytes, err := io.ReadAll(body)
		if err != nil {
			return nil
		}
		return bodyBytes
	}

	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return nil
	}
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	return bodyBytes
}

func formatReasoningNumber(value gjson.Result) string {
	if value.Type == gjson.Number {
		number := value.Float()
		return strconv.FormatFloat(number, 'f', -1, 64)
	}
	return strings.TrimSpace(value.String())
}

func stringReasoningValue(bodyBytes []byte, path string) string {
	value := gjson.GetBytes(bodyBytes, path)
	if !value.Exists() {
		return ""
	}
	switch value.Type {
	case gjson.String, gjson.Number, gjson.True, gjson.False:
		return strings.TrimSpace(value.String())
	default:
		return ""
	}
}

// ExtractClientEffortExplicit 判断客户端是否显式设置了 effort 值。
// 返回 (raw_effort_string, true) 如果显式设置，("", false) 如果未声明。
// 区分协议：
// - Claude: thinking.type 存在（即使 "enabled" 无 effort）→ explicit；无 thinking 字段 → not explicit
// - OpenAI: reasoning_effort 存在 → explicit；absent → not explicit
// - Responses: reasoning.effort 存在 → explicit；absent → not explicit
// - Gemini: thinkingConfig 存在 → explicit；absent → not explicit
func ExtractClientEffortExplicit(bodyBytes []byte, channelKind string) (raw string, explicit bool) {
	if len(bytes.TrimSpace(bodyBytes)) == 0 || !gjson.ValidBytes(bodyBytes) {
		return "", false
	}

	// Claude Messages: thinking.type present → explicit
	if channelKind == "messages" || channelKind == "" {
		if value := gjson.GetBytes(bodyBytes, "thinking.type"); value.Exists() {
			return strings.TrimSpace(value.String()), true
		}
	}

	// OpenAI Chat: reasoning_effort present → explicit
	if channelKind == "chat" || channelKind == "" {
		if value := stringReasoningValue(bodyBytes, "reasoning_effort"); value != "" {
			return value, true
		}
	}

	// Responses: reasoning.effort present → explicit
	if channelKind == "responses" || channelKind == "" {
		if value := stringReasoningValue(bodyBytes, "reasoning.effort"); value != "" {
			return value, true
		}
	}

	// Gemini: thinkingConfig present → explicit
	if channelKind == "gemini" || channelKind == "" {
		if value := gjson.GetBytes(bodyBytes, "generationConfig.thinkingConfig"); value.Exists() {
			return "thinkingConfig", true
		}
		if value := gjson.GetBytes(bodyBytes, "thinkingConfig"); value.Exists() {
			return "thinkingConfig", true
		}
	}

	return "", false
}
