package llm

import "github.com/BenedictKing/ccx/internal/types"

// Format 标识请求/响应所属的 API 协议格式。
//
// 与 internal/passthrough.APIFormat 含义一致，但 llm 包独立于 passthrough，
// 避免环依赖。Adapter 层负责在两个常量集合之间转换。
type Format string

const (
	FormatUnknown         Format = ""
	FormatOpenAIChat      Format = "openai_chat"
	FormatOpenAIResponses Format = "openai_responses"
	FormatClaudeMessages  Format = "claude_messages"
	FormatGeminiContents  Format = "gemini_contents"
)

// Usage 是 ccx 内部统一的 token 统计结构，封装 types.Usage 并附加格式元信息。
//
// 字段说明见 types.Usage 注释；本结构仅作为 pipeline 流转用，metrics
// 持久化仍以 types.Usage 为准。
type Usage struct {
	// Inner 是 ccx 现有的 token 字段集合（包含 InputTokens / OutputTokens /
	// CacheRead / CacheCreation / PromptTokensTotal 等）。
	Inner types.Usage

	// Format 标识本次 usage 来源的入站格式，便于 metrics 层按格式归类。
	Format Format
}

// IsZero 判断当前 usage 是否为空（所有 token 字段为 0）。
//
// 用于 pipeline / metrics 区分"无 usage 数据"与"usage 全 0"两种语义；
// 当前简单基于 Inner 的关键字段判断，未来如需更严格可扩展。
func (u Usage) IsZero() bool {
	in := u.Inner
	return in.InputTokens == 0 &&
		in.OutputTokens == 0 &&
		in.CacheCreationInputTokens == 0 &&
		in.CacheReadInputTokens == 0 &&
		in.PromptTokensTotal == 0 &&
		in.PromptTokens == 0 &&
		in.CompletionTokens == 0
}
