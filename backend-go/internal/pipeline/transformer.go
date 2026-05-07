// Package pipeline 是 ccx 流量转发的核心抽象层。
//
// 该包参照 axonhub/llm/pipeline + axonhub/internal/server/orchestrator 的
// 设计理念，把"入站协议解析 → 候选 channel 选择 → 出站协议构造 → 流式/非流式
// 分发 → retry/failover"整条链路抽象为 Inbound / Outbound / Middleware /
// Executor 四个正交接口，并由 Pipeline.Process 主循环驱动。
//
// 关键不变量（"AxonHub-half.md" 已落地契约，本包必须保留）：
//   - retry / failover 前必须 cancel 当前 attempt、关闭 response body、等待
//     fan-out goroutine 退出（见 AttemptState.RawStreamCancel）；
//   - raw passthrough fan-out 是 attempt 级状态，不跨 retry 复用；
//   - User-Agent passthrough 独立于 body/response passthrough；
//   - custom headers 必须在 final selected auth 之前应用；
//   - sensitive inbound header 统一剥离。
//
// 本包不引入数据库、不引入 FX 依赖注入；ccx key 级 BlacklistKey /
// MarkKeyAsFailedWithDuration / MatchPauseRule 通过实现 Middleware 注入，
// 与 Retryable 接口正交。
package pipeline

import (
	"context"
	"net/http"

	"github.com/BenedictKing/ccx/internal/llm"
)

// Inbound 负责把入站 *http.Request 解析为 llm.Request，并把上游响应
// 还原为入站协议字节流。
//
// 4 个 handler（chat / messages / responses / gemini）各自实现一份 Inbound。
type Inbound interface {
	// Format 返回入站协议格式，用于 same-format 决策。
	Format() llm.Format

	// TransformRequest 解析入站请求体并构造 llm.Request。
	//
	// body 为 *http.Request 经 ReadRequestBody 读取后的原始字节；req 仅用于
	// 读取 header / context，不可作为转发对象。
	TransformRequest(ctx context.Context, req *http.Request, body []byte) (*llm.Request, error)

	// TransformResponse 把 llm.Response 序列化为入站协议字节流，返回 body 与
	// 写出时使用的 header（如 Content-Type）。
	TransformResponse(ctx context.Context, resp *llm.Response) ([]byte, http.Header, error)

	// TransformStream 把出站统一流转换为入站协议 SSE 字节流。
	//
	// 实现可保留 raw passthrough（直接转发 StreamEvent.Data），也可重新序列化。
	TransformStream(ctx context.Context, in llm.Stream[*llm.Response]) llm.Stream[*llm.StreamEvent]
}

// Outbound 负责选择上游 channel/key、构造出站 *http.Request，并把上游响应
// 还原为 llm.Response / llm.Stream。
//
// 实现需要持有 channel 候选列表与游标，并实现 Retryable 接口以支持跨 channel
// 重试；可选实现 ChannelRetryable 支持同 channel 多次尝试。
type Outbound interface {
	// TransformRequest 根据当前 channel/key 状态构造出站请求。
	//
	// 返回的 *http.Request 的 Body 已序列化、header 已注入（包括认证与
	// custom headers，custom headers 必须在认证之前应用）。
	// realBody 为最终送往上游的字节，用于日志记录与 metrics。
	TransformRequest(ctx context.Context, req *llm.Request) (out *http.Request, realBody []byte, err error)

	// TransformResponse 把上游 *http.Response 转换为 llm.Response。
	//
	// 需要：读取 body（透传场景下原样保留）、解析 usage、关闭 resp.Body。
	TransformResponse(ctx context.Context, resp *http.Response) (*llm.Response, error)

	// TransformStream 把上游 *http.Response 流式 body 转换为 llm.Stream。
	//
	// 实现需在 ctx 取消时优雅退出（关闭 body / 释放 fan-out goroutine）。
	TransformStream(ctx context.Context, resp *http.Response) llm.Stream[*llm.Response]
}
