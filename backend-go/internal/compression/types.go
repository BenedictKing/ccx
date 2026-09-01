// Package compression 提供请求侧工具输出压缩（RTK 模式）。
//
// 作用域：仅压缩 messages 历史中的 tool_result 内容；
// 不碰 system、最后一条 user 消息、tool_use 参数、响应体。
//
// 设计原则：
//   - fail-open：压缩器 panic/超预算时按原文放行，绝不阻断请求
//   - 保真门是红线：JSON key 完整、数字字面量完整、diff hunk 完整、受保护 token 存活率 ≥95%
//   - 膨胀回退：压缩后体积反而变大 → 整体回退原文
//   - 可审计：每次压缩记录原始/压缩后 token 估算与回退原因
package compression

// Result 描述一次压缩的结果。
type Result struct {
	// Body 是压缩后的请求体字节；未压缩时为原始 body
	Body []byte
	// Compressed 表示是否发生了实际压缩
	Compressed bool
	// OriginalTokens 是原始 tool_result 内容的估算 token 数
	OriginalTokens int
	// CompressedTokens 是压缩后 tool_result 内容的估算 token 数
	CompressedTokens int
	// SavingsPercent 是节省比例（0-100），仅当 Compressed=true 时有意义
	SavingsPercent float64
	// Technique 是本次使用的压缩技术标识（如 "rtk_filter" / "dedup"）
	Technique string
	// FallbackReason 是回退原因（空表示未回退）
	FallbackReason string
	// FilterCount 命中的 filter 数量
	FilterCount int
	// FidelityPassed 保真门是否通过
	FidelityPassed bool
}
