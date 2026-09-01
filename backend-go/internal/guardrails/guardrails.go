// Package guardrails 提供内容级防护链（credential 掩码起步，预留 PII/注入/模态桥扩展点）。
//
// 设计原则：
//   - fail-open：任一 guardrail 异常仅记日志放行，绝不阻断流量
//   - 优先级链：按 priority 升序执行，低数值先跑
//   - 请求头豁免：x-ccx-disabled-guardrails 逗号分隔的 guardrail 名称可跳过
//   - 最小集：本期仅 credential-masker，其余为扩展预留
package guardrails

import "net/http"

// Result 描述一次 guardrail 执行结果。
// 任一 guardrail 返回 error 视为 fail-open，调用方记日志后忽略。
type Result struct {
	// Blocked 为 true 时表示请求应被拦截（credential-masker 永远为 false）。
	Blocked bool
	// Message 为拦截/改写原因说明，可审计。
	Message string
	// Modified 表示内容是否被改写（掩码后为 true）。
	Modified bool
	// Payload 存放 PreCall 改写后的请求体；未改写时为 nil。
	Payload []byte
	// Response 存放 PostCall 改写后的响应/错误体；未改写时为 nil。
	Response []byte
	// Meta 存放可审计的详细信息（命中类型、数量等）。
	Meta map[string]any
}

// Context 描述 guardrail 执行时的上下文信息。
type Context struct {
	// Model 是请求模型名，可为空。
	Model string
	// Provider 是上游服务类型（claude/openai/gemini/...），可为空。
	Provider string
	// ChannelUID 是渠道稳定标识，可为空。
	ChannelUID string
	// ChannelName 是渠道名称，可为空。
	ChannelName string
	// Headers 是入站请求头，用于解析 x-ccx-disabled-guardrails。
	Headers http.Header
}

// Guardrail 是内容防护插件的统一接口。
// 实现必须保证：方法返回 error 时调用方会 fail-open，
// 因此内部 panic 应自行 recover 后通过 error 返回。
type Guardrail interface {
	// Name 返回 guardrail 的唯一名称（kebab-case，如 credential-masker）。
	Name() string
	// Priority 返回执行优先级，数值越小越先执行。
	Priority() int
	// Enabled 返回是否启用。
	Enabled() bool
	// PreCall 在请求发往上游前执行，payload 为原始请求体字节。
	// 返回 nil Result 等价于 {Blocked:false, Modified:false}。
	// 注意：当前转发路径不挂载 PreCall（掩码改写会污染对话上下文），
	// 该契约仅作为扩展点保留。
	PreCall(payload []byte, ctx *Context) (*Result, error)
	// PostCall 处理响应体或错误体字节。当前唯一生产消费者是日志脱敏
	// 统一入口 MaskForLog（handlers/common/guardrails.go），转发路径不挂载。
	// 返回 nil Result 等价于 {Blocked:false, Modified:false}。
	PostCall(response []byte, ctx *Context) (*Result, error)
}
