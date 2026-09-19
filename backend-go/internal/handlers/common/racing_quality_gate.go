package common

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// 竞速质量闸门：伪工具调用标记的软校验。
//
// 背景（2026-09-12 codex 事故链）：部分中转的 responses 端点在 tool_choice=auto
// 下，模型把工具调用写成内部协议标记的纯文本（Qwen 的 <tool_call>/、DeepSeek
// 的 <｜DSML｜/<｜tool▁calls▁begin｜>），协议层不会把它转成 function_call 事件
// ——客户端拿到的是编造的"工具结果"文本。探针红线（只测强制 tool_choice）
// 天然覆盖不到 auto 行为，无法学成黑名单；竞速场景下这类分支首字快、
// 系统性抢赢真实但较慢的主分支。
//
// 定位与红线：仅在竞速闸门裁决时生效的软信号——命中伪标记的分支让出
// 提交权（按竞速败出退位，免渠道惩罚、不进学习黑名单），主分支/其他影子
// 继续服务；误杀（如用户确实让模型书写这类标记的文档）的代价只是换一个
// 分支交付。让出同时把这次已完成的伪标记观察计入白名单负反馈（让出即证据，
// tool_unsupported_signal.go notePseudoToolCallYield）——白名单 fail-open
// 窗口内劣化组合连续命中后不再获派影子，窗口自动收敛。
// 无闸门的直连路径行为完全不变；仅对本请求携带 tools 的流式分支启用。

// pseudoToolCallMarkerPatterns 伪工具调用标记的强特征（字面量子串）。
// 这些是模型内部工具调用协议的标记词，正常 assistant 正文不应包含。
var pseudoToolCallMarkerPatterns = []string{
	"<tool_call>",
	"</tool_call>", // 闭标记：实测形态常只输出闭标记段（</parameter></function></tool_call>）
	"<tool_calls>",
	"<tool_return>",        // DeepSeek 系工具返回标记（实测 modelScope V4-Pro 输出 <tool_return>/<return>）
	"<｜tool▁calls▁begin｜>", // DeepSeek 官方标记（U+2581 下划线连接）
	"<｜DSML｜",              // DeepSeek DSML 族总前缀
	"<function=",           // Qwen function 变体开头
	"</function>",          // Qwen function 闭标记
	"<parameter=",          // Qwen 参数标记（与 function/tool_call 标记同族使用）
}

// DetectPseudoToolCallMarker 检测缓冲输出是否包含伪工具调用标记。
// 大小写不敏感（文本与标记双侧归一，覆盖 DSML 全大写特例）；空串与
// 无标记文本返回 false。
func DetectPseudoToolCallMarker(text string) bool {
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	for _, marker := range pseudoToolCallMarkerPatterns {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

// maxPseudoMarkerBytes 最长伪标记的字节数（流式扫描器的尾部保留长度依据）。
var maxPseudoMarkerBytes = func() int {
	maxLen := 0
	for _, marker := range pseudoToolCallMarkerPatterns {
		if len(marker) > maxLen {
			maxLen = len(marker)
		}
	}
	return maxLen
}()

// PseudoToolCallMarkerScanner 流式文本增量的伪标记扫描器（标记可能跨 delta
// 切断，尾部保留 maxMarker-1 字节拼接判定；幂等：命中后不再扫描）。
type PseudoToolCallMarkerScanner struct {
	tail  string
	found bool
}

// Feed 送入一段新增文本，返回自本次调用起是否已检测到标记。
func (s *PseudoToolCallMarkerScanner) Feed(text string) bool {
	if s == nil || s.found {
		return s != nil && s.found
	}
	joined := s.tail + text
	if DetectPseudoToolCallMarker(joined) {
		s.found = true
		s.tail = ""
		return true
	}
	keep := maxPseudoMarkerBytes - 1
	if len(joined) > keep {
		s.tail = joined[len(joined)-keep:]
	} else {
		s.tail = joined
	}
	return false
}

// Found 返回是否已检测到标记。
func (s *PseudoToolCallMarkerScanner) Found() bool {
	return s != nil && s.found
}

// MarkPseudoToolCallMarkerIfHit 扫描一段完整文本（如 responses 预检缓冲），
// 命中则标记观察器（MarkSeverityTagIfHit 的对偶）。
func MarkPseudoToolCallMarkerIfHit(c *gin.Context, text string) {
	if DetectPseudoToolCallMarker(text) {
		MarkPseudoToolCallMarker(c)
	}
}

// RacingClaimClientCommitForStream 流式路径的竞速提交裁决（带伪标记软校验）。
// bufferedOutput 为 preflight 期间缓冲的输出文本（各协议的 text delta/原始行拼接）。
// 无闸门时直接放行（零开销）；有闸门时带工具请求且缓冲输出命中伪标记的
// 分支让出提交权并丢弃分支缓冲，调用方应以 ErrRacingSuperseded 收尾。
func RacingClaimClientCommitForStream(c *gin.Context, bufferedOutput string) bool {
	if gateFromContext(c) == nil {
		return true
	}
	if bufferedOutput != "" && DetectPseudoToolCallMarker(bufferedOutput) && streamRequestHasTools(c) {
		if bw, ok := c.Writer.(*racingBranchWriter); ok {
			bw.Discard()
		}
		RequestLogf(c, "[Racing-QualityGate] 分支首包命中伪工具调用标记（tool_choice=auto 下模型把工具调用写成文本），让出提交权")
		// 让出即证据：伪标记已在该分支自身首包缓冲实测命中（非部分流推断），
		// 与成功路径 pseudo-miss 同强度，计入白名单负反馈。
		notePseudoToolCallYield(c)
		return false
	}
	return racingClaimClientCommit(c)
}

// streamRequestHasTools 本请求是否携带工具定义（伪标记校验仅对带工具请求启用）。
func streamRequestHasTools(c *gin.Context) bool {
	body := GetEffectiveRequestBody(c, nil)
	return BodyHasTools(body)
}

// RacingShadowWithTools 判定「竞速影子分支 + 本请求携带工具定义」——
// 伪标记观察窗（preflight 延长收流）的启用条件。影子分支多观察几个
// delta 不影响客户端（主分支同时在服务）；主分支与非竞速路径不启用，
// 保持原有放行节奏。
func RacingShadowWithTools(c *gin.Context) bool {
	return gateFromContext(c) != nil && racingIsShadow(c) && streamRequestHasTools(c)
}
