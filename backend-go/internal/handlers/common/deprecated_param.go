package common

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// deprecatedParamPatterns 匹配上游 "X is deprecated" 类错误消息中的参数名。
// 支持反引号包裹（`temperature`）、双引号包裹（"temperature"）、裸词（temperature）。
var deprecatedParamPatterns = []*regexp.Regexp{
	// `temperature` is deprecated for this model
	regexp.MustCompile("`([a-zA-Z_][a-zA-Z0-9_]*)` +is deprecated"),
	// "temperature" is deprecated
	regexp.MustCompile(`"([a-zA-Z_][a-zA-Z0-9_]*)" +is deprecated`),
	// temperature is no longer supported
	regexp.MustCompile(`\b([a-zA-Z_][a-zA-Z0-9_]*) +is no longer supported`),
	// temperature is not supported for this model
	regexp.MustCompile(`\b([a-zA-Z_][a-zA-Z0-9_]*) +is not supported for this model`),
}

// knownDeprecatedParams 已知可安全剥离的请求参数白名单。
// 仅包含上游明确弃用后不影响核心语义的采样/生成控制参数：剥离后模型使用自身默认值，
// 请求语义（消息、工具、思考预算）不受影响。不在白名单内的参数一律不自动剥离，
// 避免误删 messages/tools 等结构性字段导致语义损坏。
var knownDeprecatedParams = map[string]bool{
	"temperature":       true,
	"top_p":             true,
	"top_k":             true,
	"frequency_penalty": true,
	"presence_penalty":  true,
}

// DeprecatedParamFromError 从上游 400 错误响应体中提取被弃用的参数名。
// 遍历顶层 message、error.message 与 error.upstream_error.message，匹配 "X is deprecated" 类模式。
// 仅返回白名单内的参数名；未匹配或不在白名单时返回空字符串。
func DeprecatedParamFromError(bodyBytes []byte) string {
	if len(bodyBytes) == 0 {
		return ""
	}
	var errResp map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &errResp); err != nil {
		return ""
	}
	for _, msg := range extractErrorMessageFields(errResp) {
		if param := findDeprecatedParamInText(msg); param != "" && isKnownDeprecatedParam(param) {
			return param
		}
	}
	return ""
}

// extractErrorMessageFields 从错误响应体中提取所有消息字段。
func extractErrorMessageFields(errResp map[string]interface{}) []string {
	var messages []string
	// 顶层 message
	if msg, ok := errResp["message"].(string); ok && msg != "" {
		messages = append(messages, msg)
	}
	// error.message / error.upstream_error.message
	if errObj, ok := errResp["error"].(map[string]interface{}); ok {
		if msg, ok := errObj["message"].(string); ok && msg != "" {
			messages = append(messages, msg)
		}
		if upstreamErr, ok := errObj["upstream_error"].(map[string]interface{}); ok {
			if msg, ok := upstreamErr["message"].(string); ok && msg != "" {
				messages = append(messages, msg)
			}
		}
	}
	return messages
}

// findDeprecatedParamInText 从文本中匹配弃用参数名。
func findDeprecatedParamInText(text string) string {
	lower := strings.ToLower(text)
	for _, pattern := range deprecatedParamPatterns {
		if match := pattern.FindStringSubmatch(lower); len(match) > 1 {
			return match[1]
		}
	}
	return ""
}

// isKnownDeprecatedParam 判断参数名是否在可安全剥离的白名单中。
func isKnownDeprecatedParam(param string) bool {
	return knownDeprecatedParams[param]
}

// deprecatedParamPaths 返回参数在各上游协议请求体中的可能位置。
// Gemini 将采样参数放在 generationConfig 下，其余协议使用顶层字段。
func deprecatedParamPaths(param string) []string {
	return []string{param, "generationConfig." + param}
}

// StripDeprecatedParams 从请求体中删除指定的弃用参数（含 Gemini 的 generationConfig 变体）。
// 返回改写后的请求体与是否发生改写；body 无效或字段均不存在时原样返回。
func StripDeprecatedParams(body []byte, params []string) ([]byte, bool) {
	if len(body) == 0 || len(params) == 0 || !gjson.ValidBytes(body) {
		return body, false
	}
	updated := body
	changed := false
	for _, param := range params {
		if param == "" {
			continue
		}
		for _, path := range deprecatedParamPaths(param) {
			if !gjson.GetBytes(updated, path).Exists() {
				continue
			}
			next, err := sjson.DeleteBytes(updated, path)
			if err != nil {
				continue
			}
			updated = next
			changed = true
		}
	}
	return updated, changed
}
