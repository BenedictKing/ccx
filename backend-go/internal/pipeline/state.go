package pipeline

import (
	"context"
	"net/http"

	"github.com/BenedictKing/ccx/internal/llm"
)

// AttemptState 持有单次 attempt 期间需要在 middleware / transformer 之间共享
// 的临时状态。
//
// 该结构对应 axonhub/internal/server/orchestrator/PersistenceState，但去除
// 所有 ent / 数据库 / 多租户字段（参见 PRD Q1=A）。
//
// 字段语义：
//   - OriginalModel：API key profile mapping 之后、上游 redirect 之前的模型名。
//   - LlmRequest：当前 attempt 使用的 llm.Request 句柄（中间格式）。
//   - RawProviderRequest：本次 attempt 实际发往上游的 *http.Request（finalize 后）。
//   - RawProviderResponse：本次 attempt 上游返回的 *http.Response（透传时使用）。
//   - StreamCompleted：流式响应是否已完整结束（区分客户端中断 vs 流内出错）。
//   - RawStreamCh：raw passthrough 时 fan-out goroutine 推送 StreamEvent 的 chan。
//   - RawStreamErrRef：fan-out goroutine 写入终止错误的指针；retry 前必须重置。
//   - RawStreamCancel：取消 fan-out goroutine 的 cancel 函数；retry/failover 前必须调用。
//
// AxonHub-half.md 第 138-142 行已锁定的契约：
//   - raw passthrough fan-out 是 attempt 级状态，不跨 retry 复用；
//   - retry / failover 前必须 cancel 当前 attempt、关闭 response body、
//     等待 fan-out goroutine 退出。
type AttemptState struct {
	OriginalModel string

	LlmRequest *llm.Request

	RawProviderRequest  *http.Request
	RawProviderResponse *http.Response

	StreamCompleted bool

	RawStreamCh     chan *llm.StreamEvent
	RawStreamErrRef *error
	RawStreamCancel context.CancelFunc
}

// Reset 在进入下一个 attempt 之前清空 attempt 级字段。
//
// 调用方必须确保已：1) 调用 RawStreamCancel；2) 关闭 RawProviderResponse.Body；
// 3) 等待 fan-out goroutine 退出（通过 Cancel 触发的 ctx.Done() 信号）。
func (s *AttemptState) Reset() {
	if s == nil {
		return
	}
	s.RawProviderRequest = nil
	s.RawProviderResponse = nil
	s.StreamCompleted = false
	s.RawStreamCh = nil
	s.RawStreamErrRef = nil
	s.RawStreamCancel = nil
}
