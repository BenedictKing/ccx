package common

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
)

// 安全分类请求能力的运行期自学习（docs/specs/severity-class-capability.md）。
//
// 与 tool_unsupported_signal.go 同构：安全分类的失败同样是"假成功"——上游 2xx
// 正常完成，但输出不含客户端要求的 <severity> 格式标记（闲聊文本甚至幻觉工具
// 调用），错误路径学习对此无能为力，只能从成功响应的行为观测学。
//
// 防误杀红线（与工具调用能力一致，只认无歧义信号）：
//   - 只对分类形状请求学习（出站请求体含 </severity> 停止序列，经
//     autopilot.SeverityClassRequestShape 判定，协议转换保留该标记）；
//   - 只在流式正常完成（handleSuccess 无错误）后学习：中途出错/客户端取消/
//     空响应一律不学，避免把传输故障记成能力缺陷；
//   - 输出文本含 <severity 标记 = 能力确认（清除负结论）；不含 = 负结论。
//     请求方明确要求"仅输出 <severity>N</severity>"，任何合规输出都必然含标记，
//     这是无歧义的。

// severityTagMarker 流式文本中检测的格式标记（开标签即可，客户端可能在
// stop_sequence 处截断，闭标签未必出现在输出里）。
const severityTagMarker = "<severity"

// SeverityTagScanner 跨增量安全地检测流式文本中的 <severity 标记。
// 标记可能被 SSE 分片切成两半（"<sev" + "erity>"），用 N-1 长度的尾部拼接兜底。
type SeverityTagScanner struct {
	tail  string
	found bool
}

// Feed 送入一段新增文本，返回自本次调用起是否已检测到标记（幂等：命中后不再扫描）。
func (s *SeverityTagScanner) Feed(text string) bool {
	if s == nil || s.found {
		return s != nil && s.found
	}
	joined := s.tail + text
	if strings.Contains(joined, severityTagMarker) {
		s.found = true
		s.tail = ""
		return true
	}
	// 只需保留 marker 长度-1 的尾部，避免长输出无限累积。
	keep := len(severityTagMarker) - 1
	if len(joined) > keep {
		s.tail = joined[len(joined)-keep:]
	} else {
		s.tail = joined
	}
	return false
}

// Found 返回是否已检测到标记。
func (s *SeverityTagScanner) Found() bool {
	return s != nil && s.found
}

// MarkSeverityTagIfHit 扫描一段完整文本（如 responses 预检缓冲），命中则标记观察器。
// 完整文本无分片风险，直接 Contains。
func MarkSeverityTagIfHit(c *gin.Context, text string) {
	if observer := GetStreamTimeoutObserver(c); observer != nil && strings.Contains(text, severityTagMarker) {
		observer.MarkSeverityTag()
	}
}

// MaybeLearnSeverityClassOutcome 运行期学习：分类形状请求流式完成后的能力判定。
//
// 调用方约束（与 MaybeLearnForcedToolChoiceMiss 同一挂载点）：
//   - 仅 messages/responses 流式路径调用（只有这两条路径接了 <severity 文本扫描）；
//   - streamErr 必须传 handleSuccess 的返回错误，非 nil 时不学习。
//
// 学习口径：
//   - sawSeverityTag=true → 记录"支持"（enabled=false，翻转既有负结论）；
//   - sawSeverityTag=false → 记录"不支持"（enabled=true），后续路由自动规避。
func MaybeLearnSeverityClassOutcome(c *gin.Context, upstream *config.UpstreamConfig, apiKey, model string, attemptBody []byte, sawSeverityTag bool, streamErr error) {
	if streamErr != nil {
		return
	}
	if c == nil || upstream == nil || upstream.ChannelUID == "" || model == "" {
		return
	}
	if !autopilot.SeverityClassRequestShape(attemptBody) {
		return
	}
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return
	}
	keyHash := autopilot.KeyHashFromAPIKey(apiKey)
	if sawSeverityTag {
		// 能力确认：清除负结论。Record 的翻转语义（enabled 变化）保证只在
		// 结论真正变化时落盘，反复确认不产生 IO。
		if cache.Record(upstream.ChannelUID, keyHash, model, config.TraitNoSeverityClass, false, config.CompatSourceRuntimeSignal, "分类请求输出含 <severity> 标记，能力确认") {
			RequestLogf(c, "[SeverityClassCompat] 渠道 %s 模型 %s 安全分类格式能力确认，已清除规避记忆", upstream.Name, model)
		}
		return
	}
	if cache.Record(upstream.ChannelUID, keyHash, model, config.TraitNoSeverityClass, true, config.CompatSourceRuntimeSignal, "分类请求 2xx 完成但输出无 <severity> 标记") {
		RequestLogf(c, "[SeverityClassCompat] 渠道 %s 模型 %s 无法完成安全分类格式输出（流式全程无 <severity> 标记），已记忆并将在后续路由中规避",
			upstream.Name, model)
	}
}
