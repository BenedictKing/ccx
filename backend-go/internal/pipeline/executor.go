package pipeline

import (
	"context"
	"net/http"
)

// Executor 负责将 *http.Request 真正发送给上游并返回 *http.Response。
//
// 该接口把 SendRequest 这层 IO 抽象出来，便于：
//   - 单元测试通过假 executor 驱动 pipeline 主循环；
//   - 特殊上游（例如 AWS Bedrock）通过 ChannelCustomizedExecutor 注入定制 executor。
//
// 实现需要保证：
//   - 不在 Execute 内部消费 resp.Body（让 pipeline / Outbound 控制读取时机）；
//   - 透传 ctx（client cancel / deadline 必须能传递到 http.Client）；
//   - 错误返回 wrap context.Canceled / DeadlineExceeded 等典型错误以便上层判定。
type Executor interface {
	Execute(ctx context.Context, req *http.Request) (*http.Response, error)
}

// ExecutorFunc 让普通函数实现 Executor 接口，方便测试。
type ExecutorFunc func(ctx context.Context, req *http.Request) (*http.Response, error)

// Execute 实现 Executor 接口。
func (f ExecutorFunc) Execute(ctx context.Context, req *http.Request) (*http.Response, error) {
	return f(ctx, req)
}

// ChannelCustomizedExecutor 由 Outbound 可选实现，用于覆盖默认 Executor。
//
// 典型场景：AWS Bedrock 需要 SigV4 签名走自定义 client；该接口允许 Outbound
// 在 pipeline.Process 阶段为本次 attempt 切换 executor。
type ChannelCustomizedExecutor interface {
	CustomizeExecutor(base Executor) Executor
}
