// Package middleware —— pipeline 通用 middleware 集合。
//
// sse_error_event.go 提供 SSEErrorEventDetector：在 RawStream 层扫描每个 SSE
// 帧，命中 `event: error` 或 data JSON `"type":"error"` 时把当前 attempt 标记为
// 失败，让 pipeline.Process 主循环走 retry / failover 分支，与
// handlers/common/stream.go::handleStreamEvents 的 stream 内 cancel 语义保持
// 一致；否则上游 HTTP 200 + 内嵌 error 帧的请求会被 pipeline 误判为成功，
// 直接把错误帧透传给客户端，并导致 raw passthrough fan-out goroutine 卡死。
package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/pipeline"
)

// SSEErrorEventDetector 实现 pipeline.Middleware.RawStream，把上游内嵌的
// `event: error` SSE 帧上抛为 pipeline.ErrUpstreamStreamError。
//
// 工作方式：
//   - RawStream 收到 stream 后立即同步预读首个 StreamEvent（与
//     pipeline.prefetchLlmStream 思路一致），若首帧即为 error，则关掉底层
//     stream 并返回 errorProbeStream（其 Probe() 返回 ErrUpstreamStreamError）。
//   - 否则返回 lazyScanningStream，把缓冲到的首帧重放给下游，并在后续
//     Next() 中持续扫描；扫描到 error 帧立即把 sentinel 注入 Err()，让
//     handler 端在消费时感知错误。
//
// 该 middleware 只负责"检测 + 上抛"，不接管上游 stream 的 cancel；底层资源
// 仍由 pipeline 主循环的 cleanupAttemptStreamResources 在 retry 路径上释放。
type SSEErrorEventDetector struct {
	pipeline.BaseMiddleware
}

// NewSSEErrorEventDetector 构造默认配置的 detector，加入 BuildPipelineOpts 默认链。
func NewSSEErrorEventDetector() *SSEErrorEventDetector { return &SSEErrorEventDetector{} }

// StreamErrorProbe 由 RawStream 包装的 stream 实现，processStream 通过类型断言
// 在把 Result 交给 handler 之前探测一次；Probe 返回非 nil 时 attempt 失败。
//
// 与 PR1 ErrEmptyResponse 路径同构（empty_response.go::HasPrefetched），
// 保持 9-hook 接口签名不变。
type StreamErrorProbe interface {
	ProbeStreamError() error
}

// RawStream 在 attempt 内预读首帧扫描 error 事件；命中即返回 sentinel 短路流。
func (m *SSEErrorEventDetector) RawStream(
	ctx context.Context,
	stream llm.Stream[*llm.StreamEvent],
) llm.Stream[*llm.StreamEvent] {
	if stream == nil {
		return stream
	}
	wrapped := newScanningStream(ctx, stream)
	return wrapped
}

// scanningStream 包装上游 StreamEvent 流，预读首帧并扫描 error 事件。
//
// 字段：
//   - inner       底层 SSE StreamEvent 流。
//   - buffered    预读到的首帧（命中 error 时为 nil 表示直接关流）。
//   - hasBuffered 是否预读到值。
//   - consumed    buffered 是否已经被 Next 吐出。
//   - probeErr    探测错误；processStream 通过 StreamErrorProbe.ProbeStreamError 读取。
//   - closed      Close 幂等标志。
type scanningStream struct {
	inner       llm.Stream[*llm.StreamEvent]
	buffered    *llm.StreamEvent
	hasBuffered bool
	consumed    bool
	probeErr    error
	closed      bool
}

func newScanningStream(_ context.Context, inner llm.Stream[*llm.StreamEvent]) *scanningStream {
	s := &scanningStream{inner: inner}
	if inner.Next() {
		ev := inner.Current()
		s.buffered = ev
		s.hasBuffered = true
		if isSSEErrorEvent(ev) {
			s.probeErr = pipeline.ErrUpstreamStreamError
			slog.Debug("[Pipeline-SSEError] detected upstream error event in first frame")
			// 不立刻 Close 底层 stream：fan-out goroutine 仍可能在等
			// 写入；pipeline.cleanupAttemptStreamResources 会在 retry 前
			// cancel ctx + 关 body + drain channel。这里只是让 ProbeStreamError
			// 上抛 sentinel，processStream 据此返回错误触发 retry。
		}
	}
	return s
}

// ProbeStreamError 实现 StreamErrorProbe。
func (s *scanningStream) ProbeStreamError() error { return s.probeErr }

// Next 先吐出预读到的值，再继续扫描后续帧；命中 error 帧时记录 sentinel
// 并返回 false（让消费侧停下来，与底层 Err() 语义对齐）。
func (s *scanningStream) Next() bool {
	if s.closed {
		return false
	}
	if s.probeErr != nil {
		// 首帧命中 error：buffered 已经填好；让消费侧把 buffered 取走一次后停下，
		// 避免 handler 端收到空 chan 立即关闭。
		if s.hasBuffered && !s.consumed {
			s.consumed = true
			return true
		}
		return false
	}
	if s.hasBuffered && !s.consumed {
		s.consumed = true
		return true
	}
	if !s.inner.Next() {
		return false
	}
	if isSSEErrorEvent(s.inner.Current()) {
		s.probeErr = pipeline.ErrUpstreamStreamError
		slog.Debug("[Pipeline-SSEError] detected upstream error event in mid-stream frame")
		// 让消费侧拿到这一帧（保证 BuildStreamErrorEvent 不丢上游错误信息），
		// 下次 Next 返回 false。
		return true
	}
	return true
}

// Current 返回最近一次 Next 对应的事件。
func (s *scanningStream) Current() *llm.StreamEvent {
	if s.hasBuffered && s.consumed && s.inner.Current() == nil {
		return s.buffered
	}
	if s.hasBuffered && !s.consumed {
		return s.buffered
	}
	return s.inner.Current()
}

// Err 返回探测错误（优先）或底层流错误。
func (s *scanningStream) Err() error {
	if s.probeErr != nil {
		return s.probeErr
	}
	return s.inner.Err()
}

// Close 幂等关闭底层流。
func (s *scanningStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.inner.Close()
}

// isSSEErrorEvent 判断单个 StreamEvent 是否表达上游错误。
//
// 规则（与 handlers/common/stream.go 检测逻辑保持一致）：
//   - StreamEvent.Type == "error"，或
//   - Data 中存在 `event: error` 行，或
//   - Data 中 data JSON 含 `"type":"error"`。
//
// 该判定故意保守：仅在显式 error 信号下命中，避免误伤正常事件。
func isSSEErrorEvent(ev *llm.StreamEvent) bool {
	if ev == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(ev.Type), "error") {
		return true
	}
	if len(ev.Data) == 0 {
		return false
	}
	// 行扫描：兼容 "event: error" 与 "event:error"，以及 data JSON 内嵌 type=error。
	for _, raw := range bytes.Split(ev.Data, []byte("\n")) {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		lower := bytes.ToLower(line)
		if bytes.HasPrefix(lower, []byte("event:")) {
			val := bytes.TrimSpace(lower[len("event:"):])
			if bytes.Equal(val, []byte("error")) {
				return true
			}
		}
		if bytes.HasPrefix(lower, []byte("data:")) {
			payload := bytes.TrimSpace(line[len("data:"):])
			if dataLooksLikeError(payload) {
				return true
			}
		}
	}
	return false
}

// dataLooksLikeError 解析 SSE data 行 payload，判定是否携带 type=error 标记。
func dataLooksLikeError(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	// 仅在看似 JSON 时尝试解析；其它形态（如 [DONE]）一律不视为错误。
	if payload[0] != '{' {
		return false
	}
	var obj map[string]any
	if err := json.Unmarshal(payload, &obj); err != nil {
		return false
	}
	if t, ok := obj["type"].(string); ok && strings.EqualFold(t, "error") {
		return true
	}
	// 兼容 bigmodel 风格：顶层无 type 但含 error 对象。
	if _, ok := obj["error"]; ok {
		// 仅当顶层不存在普通业务字段（content/message_delta 等）时才视为错误。
		// 这里只要 error 对象/字符串存在就当错误，与 handlers/common/stream.go 行为一致。
		return true
	}
	return false
}
