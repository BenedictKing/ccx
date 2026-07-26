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
//   - Claude: thinking.type 存在（即使 "enabled" 无 effort）→ explicit；无 thinking 字段 → not explicit
//   - OpenAI: reasoning_effort 存在 → explicit；absent → not explicit
//   - Responses: reasoning.effort 存在 → explicit；absent → not explicit
//   - Gemini: thinkingConfig.thinkingLevel 存在 → explicit，raw 为实际 level；
//     thinkingConfig 存在但无 thinkingLevel 时，仅当 thinkingBudget=0（关闭思考）才视为
//     explicit 且 raw="none"，否则视为未钳定 effort 级别 → not explicit；
//     thinkingConfig 整体缺失 → not explicit
func ExtractClientEffortExplicit(bodyBytes []byte, channelKind string) (raw string, explicit bool) {
	if len(bytes.TrimSpace(bodyBytes)) == 0 || !gjson.ValidBytes(bodyBytes) {
		return "", false
	}

	// Claude Messages: thinking.type present → explicit
	// type=enabled 时真实档位在 thinking.effort，优先取它；缺失才回退到 type 值本身
	// （type=disabled 即代表显式关闭思考）。
	if channelKind == "messages" || channelKind == "" {
		if value := gjson.GetBytes(bodyBytes, "thinking.type"); value.Exists() {
			if effort := stringReasoningValue(bodyBytes, "thinking.effort"); effort != "" {
				return effort, true
			}
			return strings.TrimSpace(value.String()), true
		}
		if effort := stringReasoningValue(bodyBytes, "thinking.effort"); effort != "" {
			return effort, true
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

	// Gemini: thinkingConfig.thinkingLevel present → explicit（取真实 effort 值，
	// 与 extractReasoningEffortForLog 的 Gemini 路径保持一致，不返回容器名占位）
	if channelKind == "gemini" || channelKind == "" {
		if raw, explicit := extractGeminiEffortExplicit(bodyBytes); explicit {
			return raw, true
		}
	}

	return "", false
}

// extractGeminiEffortExplicit 从 Gemini 请求体解析真实 effort 级别，而不是返回容器字段名占位。
// 路径顺序与 extractReasoningEffortForLog 的 Gemini 分支一致：
// generationConfig.thinkingConfig.thinkingLevel 优先，裸 thinkingConfig.thinkingLevel 作为兼容回退。
// - thinkingLevel 存在 → 返回该原始值，explicit=true
// - thinkingConfig 存在但无 thinkingLevel：
//   - thinkingBudget=0（关闭思考）→ 返回 "none"，explicit=true
//   - 否则视为未钳定任何 effort 级别 → 返回 ""，explicit=false
//
// - thinkingConfig 整体不存在 → 返回 ""，explicit=false
func extractGeminiEffortExplicit(bodyBytes []byte) (raw string, explicit bool) {
	for _, path := range []string{
		"generationConfig.thinkingConfig.thinkingLevel",
		"thinkingConfig.thinkingLevel",
	} {
		if value := stringReasoningValue(bodyBytes, path); value != "" {
			return value, true
		}
	}

	containerExists := gjson.GetBytes(bodyBytes, "generationConfig.thinkingConfig").Exists() ||
		gjson.GetBytes(bodyBytes, "thinkingConfig").Exists()
	if !containerExists {
		return "", false
	}

	for _, path := range []string{
		"generationConfig.thinkingConfig.thinkingBudget",
		"thinkingConfig.thinkingBudget",
	} {
		if value := gjson.GetBytes(bodyBytes, path); value.Exists() && value.Type == gjson.Number && value.Float() == 0 {
			return "none", true
		}
	}

	return "", false
}
