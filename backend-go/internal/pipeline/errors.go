package pipeline

import "errors"

// 这些错误是 pipeline 内部约定的语义错误。
//
// 它们与 ccx 已有的 common.ErrEmptyStreamResponse / ErrInvalidResponseBody
// 语义对齐，但放在 pipeline 包内避免 internal/handlers/common 反向依赖。
var (
	// ErrEmptyResponse 表示上游流已结束但未产生任何有效内容；
	// 触发后 pipeline 通常按"可重试"处理（与 Retryable.HasMoreChannels 协作）。
	ErrEmptyResponse = errors.New("pipeline: empty response stream")

	// ErrChannelExhausted 表示 Retryable.HasMoreChannels 已返回 false，
	// 整条 channel 候选链表已耗尽。
	ErrChannelExhausted = errors.New("pipeline: all channels exhausted")

	// ErrSameChannelExhausted 表示 ChannelRetryable.CanRetry 拒绝继续在
	// 当前 channel 重试（与 maxSameChannelRetries 共同决定）。
	ErrSameChannelExhausted = errors.New("pipeline: same channel retries exhausted")

	// ErrInvalidResponseBody 表示上游响应体格式不合法（如 HTML 错误页）。
	// pipeline 应触发 failover 并由 Middleware 决定是否冷却 / 拉黑当前 key。
	ErrInvalidResponseBody = errors.New("pipeline: invalid response body")
)
