package pipeline

import (
	"bytes"
	"context"

	"github.com/BenedictKing/ccx/internal/llm"
)

// prefetchLlmStream 是 llm.Stream[*llm.Response] 的"预读 + 重放"适配器。
//
// 用途：支撑 WithEmptyResponseDetection —— 在 Inbound.TransformStream 被调用
// 之前，向下游流预读首个 *llm.Response。若底层流未产生任何"有内容"的
// Response 即结束，则视为空响应，外层 processStream 会返回 ErrEmptyResponse
// 触发 retry；反之，Prefetched() 为 true，适配器的 Next() 在第一次调用时先
// 吐出预读到的值，之后透明转发。
//
// 字段：
//   - inner：底层 llm.Response 流。
//   - buffered：预读缓冲的首个 *llm.Response。
//   - bufferedErr：首个 Next() 结束时底层流的错误（预读阶段即可观测）。
//   - hasBuffered：是否预读到了值。
//   - consumed：buffered 是否已被消费（让 Next 之后的调用走底层）。
//   - sawContent：在整个流周期中是否至少观察到一个"有内容"的 Response。
//   - closed：Close 幂等标志。
//
// 设计参考：axonhub/llm/pipeline/empty_response.go.hasResponseContent 与 ccx
// 简化版 hasMeaningfulResponse（见下）。由于 ccx 的 *llm.Response 主要以
// Body/Header/Usage/Metadata 形式承载信息，这里采用与 axonhub 等价的"是否
// 为空对象"语义。
type prefetchLlmStream struct {
	inner llm.Stream[*llm.Response]

	buffered    *llm.Response
	bufferedErr error
	hasBuffered bool
	consumed    bool
	sawContent  bool
	closed      bool
}

// newPrefetchLlmStream 创建适配器并立刻触发一次底层 Next，以便外层判断流是否
// 为空；若 ctx 已取消或底层立即返回 false，hasBuffered 为 false。
//
// parent ctx 用于 select 取消：若 parent 在预读途中取消，底层 Stream 的
// ChanStream 实现会感知 ctx.Done()。这里不额外消费 ctx，仅作为 Close 时的
// 释放通道。
func newPrefetchLlmStream(_ context.Context, inner llm.Stream[*llm.Response]) *prefetchLlmStream {
	ps := &prefetchLlmStream{inner: inner}
	if inner.Next() {
		ps.buffered = inner.Current()
		ps.hasBuffered = true
		if hasMeaningfulResponse(ps.buffered) {
			ps.sawContent = true
		}
	} else {
		ps.bufferedErr = inner.Err()
	}
	return ps
}

// HasPrefetched 是否在预读阶段拿到了一个值。
//
// processStream 用此方法判断是否需要返回 ErrEmptyResponse：
//   - HasPrefetched == false 且底层 Err() == nil ⇒ 视为空响应。
//   - HasPrefetched == false 且底层 Err() != nil ⇒ 直接返回底层错误。
func (s *prefetchLlmStream) HasPrefetched() bool { return s.hasBuffered }

// PrefetchErr 返回预读阶段底层流的 Err（若 hasBuffered=false 时有效）。
func (s *prefetchLlmStream) PrefetchErr() error { return s.bufferedErr }

// Next 先吐出预读到的值，再透明委托底层流。
func (s *prefetchLlmStream) Next() bool {
	if s.closed {
		return false
	}
	if s.hasBuffered && !s.consumed {
		s.consumed = true
		return true
	}
	if !s.inner.Next() {
		return false
	}
	if hasMeaningfulResponse(s.inner.Current()) {
		s.sawContent = true
	}
	return true
}

// Current 返回最近一次 Next 对应的值。
func (s *prefetchLlmStream) Current() *llm.Response {
	if s.hasBuffered && s.consumed && s.inner.Current() == nil {
		// 刚吐出 buffered，尚未推进底层：返回 buffered。
		return s.buffered
	}
	if s.hasBuffered && !s.consumed {
		return s.buffered
	}
	return s.inner.Current()
}

// Err 返回底层错误；若启用了空流检测但整个生命周期都未见 sawContent，
// 由外层在流消费完毕后通过 SawContent() 判定是否追加 ErrEmptyResponse。
func (s *prefetchLlmStream) Err() error { return s.inner.Err() }

// SawContent 在流被完整消费后可读：true 表示至少观察到一个"有内容" Response。
func (s *prefetchLlmStream) SawContent() bool { return s.sawContent }

// Close 幂等关闭底层流。
func (s *prefetchLlmStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.inner.Close()
}

// hasMeaningfulResponse 判断单个 *llm.Response 是否携带非空内容。
//
// 当前规则（粗粒度，与 ccx llm.Response 字段形态相适配）：
//   - resp 不为 nil；
//   - Body 去首尾空白后非空，且不是 SSE [DONE] sentinel，或
//   - Usage 不为空（某些协议仅在最后一个 event 携带 usage）。
//
// 该规则保守：只要看到任意"看起来像数据"的字节就认为有内容，避免误判正常
// 响应为"空"。参考 axonhub/llm/pipeline/empty_response.go 的 hasResponseContent。
func hasMeaningfulResponse(resp *llm.Response) bool {
	if resp == nil {
		return false
	}
	if resp.Usage != nil && !resp.Usage.IsZero() {
		return true
	}
	trimmed := bytes.TrimSpace(resp.Body)
	if len(trimmed) == 0 {
		return false
	}
	if bytes.Equal(trimmed, []byte("[DONE]")) {
		return false
	}
	return true
}
