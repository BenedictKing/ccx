// Package llm 定义 ccx 内部的统一 LLM 请求/响应中间格式。
//
// 该包参照 axonhub/llm 的设计，用于在多种 API 协议（OpenAI Chat、Claude
// Messages、OpenAI Responses、Gemini Contents）之间提供 inbound/outbound
// 双向转换的中间表示。
//
// 关键类型：
//   - Request / Response：统一请求与非流式响应。
//   - StreamEvent：上游原始 SSE 事件。
//   - Stream[T]：iterator 风格的流式数据抽象。
//   - Usage：token 统计（封装 types.Usage 并附加格式元信息）。
//
// 注意：本包刻意保持纯数据结构 + 极小依赖，方便被多个 handler 与 pipeline 共享。
package llm

import (
	"context"
)

// Stream 是流式数据的 iterator 抽象，参考 axonhub/llm/streams.Stream。
//
// 实现需满足：
//   - Next 阻塞或返回 false 表示流结束。
//   - Current 仅在最近一次 Next 返回 true 后有效。
//   - Err 在 Next 返回 false 后可读取最终错误（nil 表示正常结束）。
//   - Close 必须可重复调用且并发安全。
//
// 实现可以借助 ChanStream 等内置工具简化构造。
type Stream[T any] interface {
	Next() bool
	Current() T
	Err() error
	Close() error
}

// ChanStream 是一个基于 chan + context 的 Stream[T] 通用实现。
//
// 设计要点：
//   - context 取消时 Next 立即返回 false，Err 返回 ctx.Err()。
//   - chan 关闭即视为流结束，可通过 SetErr 在关闭前注入终止错误。
//   - Close 取消内部 ctx 并触发 closeFn（用于关闭底层资源，如 http.Response.Body）。
//   - 所有方法并发安全；多次 Close 安全幂等。
type ChanStream[T any] struct {
	ctx     context.Context
	cancel  context.CancelFunc
	ch      <-chan T
	closeFn func() error

	current T
	err     error
	closed  bool
}

// NewChanStream 用一个只读 chan + 关闭回调构造 Stream。
//
// parent 不可为 nil。closeFn 可为 nil。
func NewChanStream[T any](parent context.Context, ch <-chan T, closeFn func() error) *ChanStream[T] {
	ctx, cancel := context.WithCancel(parent)
	return &ChanStream[T]{
		ctx:     ctx,
		cancel:  cancel,
		ch:      ch,
		closeFn: closeFn,
	}
}

// SetErr 在流结束时附加终止错误。多次调用以最后一次为准。
//
// 一般由生产端在关闭 chan 之前调用，消费端通过 Err() 取出。
func (s *ChanStream[T]) SetErr(err error) {
	s.err = err
}

// Next 推进迭代器；返回 false 表示流结束或 ctx 已取消。
func (s *ChanStream[T]) Next() bool {
	if s.closed {
		return false
	}
	select {
	case <-s.ctx.Done():
		if s.err == nil {
			s.err = s.ctx.Err()
		}
		return false
	case v, ok := <-s.ch:
		if !ok {
			return false
		}
		s.current = v
		return true
	}
}

// Current 返回当前迭代值，仅在最近一次 Next 返回 true 后有效。
func (s *ChanStream[T]) Current() T {
	return s.current
}

// Err 返回流结束时的最终错误（nil 表示正常结束）。
func (s *ChanStream[T]) Err() error {
	return s.err
}

// Close 关闭流并取消其 ctx；可多次调用，幂等。
func (s *ChanStream[T]) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.cancel()
	if s.closeFn != nil {
		return s.closeFn()
	}
	return nil
}
