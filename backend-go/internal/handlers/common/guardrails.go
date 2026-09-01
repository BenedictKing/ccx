package common

import (
	"github.com/BenedictKing/ccx/internal/guardrails"
)

// MaskForLog 是 key 掩码的统一入口：仅在写日志/落库前对文本做内容级凭据脱敏。
// 转发路径（请求体/响应体）一律不改写——掩码改写会污染对话上下文、破坏缓存与 JSON 完整性。
//
// 日志脱敏是安全底线，不受 x-ccx-disabled-guardrails 请求头影响。
// fail-open：guardrail 异常仅记日志，返回原文。
func MaskForLog(text string) string {
	if text == "" {
		return text
	}
	reg := guardrails.DefaultRegistry()
	if len(reg.List()) == 0 {
		return text
	}

	// 传空 Context——日志侧不检查豁免头
	masked, _ := reg.RunPostCall([]byte(text), &guardrails.Context{})
	return string(masked)
}
