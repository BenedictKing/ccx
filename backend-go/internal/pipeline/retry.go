package pipeline

import "context"

// Retryable 由 Outbound 可选实现，允许 pipeline 在当前 attempt 失败后
// 切换到下一个 channel 候选。
//
// 语义与 axonhub/llm/pipeline.Retryable 一致：
//   - HasMoreChannels 在 pipeline 判定是否需要尝试下一个 channel 时被调用；
//     仅当 attempt 次数 < maxChannelRetries 时才会被调用。
//   - NextChannel 在 HasMoreChannels 返回 true 之后被调用；实现负责切换
//     channel/baseURL/key 等内部状态，为下一次 TransformRequest 做准备。
type Retryable interface {
	HasMoreChannels() bool
	NextChannel(ctx context.Context) error
}

// ChannelRetryable 由 Outbound 可选实现，允许 pipeline 在当前 channel 上
// 多次重试（例如临时网络错误）。
//
// 语义与 axonhub/llm/pipeline.ChannelRetryable 一致：
//   - CanRetry 根据当前错误判断是否可在同 channel 重试；仅当
//     sameChannelRetries 尚未耗尽时才会被调用。
//   - PrepareForRetry 在同 channel 重试前重置临时状态（如 AttemptState.Reset）。
//
// 当 CanRetry 返回 false 或 PrepareForRetry 返回错误时，pipeline 会回退到
// Retryable.NextChannel 尝试换 channel。
type ChannelRetryable interface {
	CanRetry(err error) bool
	PrepareForRetry(ctx context.Context) error
}
