package common

import (
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// maxOutputTokenFields 返回各入口请求体中表示"最大输出 token"的字段路径（按优先级）。
// 与 context_requirement.go 的 extractFirstInt 字段保持一致，确保估算与 clamp 取同一字段。
func maxOutputTokenFields(kind scheduler.ChannelKind) []string {
	switch kind {
	case scheduler.ChannelKindMessages:
		return []string{"max_tokens"}
	case scheduler.ChannelKindChat:
		return []string{"max_completion_tokens", "max_tokens"}
	case scheduler.ChannelKindResponses:
		return []string{"max_output_tokens", "max_tokens"}
	case scheduler.ChannelKindGemini:
		// Gemini 支持顶层与 generationConfig 两种位置
		return []string{"maxOutputTokens", "generationConfig.maxOutputTokens"}
	default:
		// images 等入口没有输出 token 概念
		return nil
	}
}

// maxOutputTokensInBody 返回请求体中实际声明的最大输出 token。
// 与 clampMaxTokensInBody 使用同一组字段与优先级，保证"读到多少就钳多少"；
// 字段缺失或非正数值时返回 0。
func maxOutputTokensInBody(bodyBytes []byte, kind scheduler.ChannelKind) int {
	if len(bodyBytes) == 0 || !gjson.ValidBytes(bodyBytes) {
		return 0
	}
	for _, field := range maxOutputTokenFields(kind) {
		value := gjson.GetBytes(bodyBytes, field)
		if value.Exists() && value.Type == gjson.Number && value.Int() > 0 {
			return int(value.Int())
		}
	}
	return 0
}

// clampMaxTokensInBody 在发往上游前，将请求体中的最大输出 token 字段下调到 maxOutput。
// 仅当字段存在且取值大于 maxOutput 时才改写，其余情况原样返回 bodyBytes（不新增字段）。
// 返回改写后的请求体与是否发生改写。
func clampMaxTokensInBody(bodyBytes []byte, kind scheduler.ChannelKind, maxOutput int) ([]byte, bool) {
	if maxOutput <= 0 || len(bodyBytes) == 0 || !gjson.ValidBytes(bodyBytes) {
		return bodyBytes, false
	}
	updated := bodyBytes
	changed := false
	for _, field := range maxOutputTokenFields(kind) {
		value := gjson.GetBytes(updated, field)
		if !value.Exists() || value.Type != gjson.Number {
			continue
		}
		if int(value.Int()) <= maxOutput {
			continue
		}
		next, err := sjson.SetBytes(updated, field, maxOutput)
		if err != nil {
			continue
		}
		updated = next
		changed = true
	}
	return updated, changed
}
