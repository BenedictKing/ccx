package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/pipeline"
)

// makeStream 把若干 StreamEvent 通过 ChanStream 包装成 llm.Stream，便于驱动 detector。
func makeStream(events []*llm.StreamEvent) llm.Stream[*llm.StreamEvent] {
	ch := make(chan *llm.StreamEvent, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)
	return llm.NewChanStream(context.Background(), ch, nil)
}

// drain 把 stream 全部消费掉，返回收集到的事件 + 终止 Err。
func drain(s llm.Stream[*llm.StreamEvent]) ([]*llm.StreamEvent, error) {
	out := []*llm.StreamEvent{}
	for s.Next() {
		out = append(out, s.Current())
	}
	return out, s.Err()
}

func TestSSEErrorEventDetector_FirstFrameError_ProbeReturnsSentinel(t *testing.T) {
	upstream := makeStream([]*llm.StreamEvent{
		{Data: []byte("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\",\"message\":\"slow\"}}\n\n")},
	})
	mw := NewSSEErrorEventDetector()
	wrapped := mw.RawStream(context.Background(), upstream)

	probe, ok := wrapped.(StreamErrorProbe)
	if !ok {
		t.Fatalf("wrapped stream does not implement StreamErrorProbe")
	}
	if err := probe.ProbeStreamError(); !errors.Is(err, pipeline.ErrUpstreamStreamError) {
		t.Fatalf("ProbeStreamError = %v, want ErrUpstreamStreamError", err)
	}
}

func TestSSEErrorEventDetector_HappyStream_PassThrough(t *testing.T) {
	upstream := makeStream([]*llm.StreamEvent{
		{Data: []byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n")},
		{Data: []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")},
		{Data: []byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")},
	})
	mw := NewSSEErrorEventDetector()
	wrapped := mw.RawStream(context.Background(), upstream)

	if probe, ok := wrapped.(StreamErrorProbe); ok {
		if err := probe.ProbeStreamError(); err != nil {
			t.Fatalf("happy stream probed err = %v, want nil", err)
		}
	}

	events, err := drain(wrapped)
	if err != nil {
		t.Fatalf("drain err = %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("event count = %d, want 3", len(events))
	}
}

func TestSSEErrorEventDetector_MidStreamError_LateMarkErr(t *testing.T) {
	upstream := makeStream([]*llm.StreamEvent{
		{Data: []byte("event: message_start\ndata: {\"type\":\"message_start\"}\n\n")},
		{Data: []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")},
		{Data: []byte("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"server_error\",\"message\":\"oops\"}}\n\n")},
		{Data: []byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")},
	})
	mw := NewSSEErrorEventDetector()
	wrapped := mw.RawStream(context.Background(), upstream)

	// 首帧不是 error，所以 Probe 应在预读阶段返回 nil。
	if probe, ok := wrapped.(StreamErrorProbe); ok {
		if err := probe.ProbeStreamError(); err != nil {
			t.Fatalf("first-frame probe = %v, want nil for mid-stream error", err)
		}
	}

	got, drainErr := drain(wrapped)
	// 期望前 3 帧（含 error 帧本身）被吐出，第 4 帧之后停下。
	if len(got) != 3 {
		t.Fatalf("forwarded events = %d, want 3 (up to and including error frame)", len(got))
	}
	if !errors.Is(drainErr, pipeline.ErrUpstreamStreamError) {
		t.Fatalf("Err() after error frame = %v, want ErrUpstreamStreamError", drainErr)
	}

	// Probe 在扫描后也应返回 sentinel。
	if probe, ok := wrapped.(StreamErrorProbe); ok {
		if err := probe.ProbeStreamError(); !errors.Is(err, pipeline.ErrUpstreamStreamError) {
			t.Fatalf("ProbeStreamError after scan = %v, want ErrUpstreamStreamError", err)
		}
	}
}

func TestSSEErrorEventDetector_EmptyStream_PassThrough(t *testing.T) {
	upstream := makeStream(nil)
	mw := NewSSEErrorEventDetector()
	wrapped := mw.RawStream(context.Background(), upstream)

	if probe, ok := wrapped.(StreamErrorProbe); ok {
		if err := probe.ProbeStreamError(); err != nil {
			t.Fatalf("empty stream probe = %v, want nil", err)
		}
	}
	events, err := drain(wrapped)
	if err != nil {
		t.Fatalf("drain empty err = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("event count = %d, want 0", len(events))
	}
}

func TestSSEErrorEventDetector_DataJSONErrorTypeWithoutEventLine(t *testing.T) {
	// 仅 data JSON 的 type=error，没有 event 行。
	upstream := makeStream([]*llm.StreamEvent{
		{Data: []byte("data: {\"type\":\"error\",\"error\":{\"message\":\"boom\"}}\n\n")},
	})
	mw := NewSSEErrorEventDetector()
	wrapped := mw.RawStream(context.Background(), upstream)

	probe, _ := wrapped.(StreamErrorProbe)
	if probe == nil || !errors.Is(probe.ProbeStreamError(), pipeline.ErrUpstreamStreamError) {
		t.Fatalf("data-only error not detected, probe=%v", probe)
	}
}

func TestSSEErrorEventDetector_StreamEventTypeFieldIsError(t *testing.T) {
	// 上游已经把 event.Type 解析成 "error"，Data 不需要再含 event: 行。
	upstream := makeStream([]*llm.StreamEvent{
		{Type: "error", Data: []byte("data: {\"error\":{\"message\":\"boom\"}}\n\n")},
	})
	mw := NewSSEErrorEventDetector()
	wrapped := mw.RawStream(context.Background(), upstream)

	probe, _ := wrapped.(StreamErrorProbe)
	if probe == nil || !errors.Is(probe.ProbeStreamError(), pipeline.ErrUpstreamStreamError) {
		t.Fatalf("Type=error not detected via probe: %v", probe)
	}
}

func TestSSEErrorEventDetector_NilStream_PassThroughNoPanic(t *testing.T) {
	mw := NewSSEErrorEventDetector()
	got := mw.RawStream(context.Background(), nil)
	if got != nil {
		t.Fatalf("nil stream wrapping should pass through, got %v", got)
	}
}
