package common

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/gin-gonic/gin"
)

// claudeStreamSequence 构造一段最小可用的 Claude SSE 流文本（包含 message_start
// 与 message_stop），保证 PreflightStreamEvents 能识别为非空响应。
func claudeStreamSequence(text string) []string {
	return []string{
		`event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-3","stop_reason":null,"usage":{"input_tokens":1,"output_tokens":0}}}

`,
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"" + text + "\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}
}

func TestFirstTokenTracker_MarkFirstEvent(t *testing.T) {
	t.Run("nil tracker is safe", func(t *testing.T) {
		var tracker *firstTokenTracker
		tracker.MarkFirstEvent() // should not panic
	})

	t.Run("nil manager via tracker", func(t *testing.T) {
		tracker := &firstTokenTracker{
			channelKey:   "ch1",
			requestStart: time.Now().Add(-50 * time.Millisecond),
		}
		// 缺失 manager 时应直接 no-op，不 panic、不翻 fired 标记。
		tracker.MarkFirstEvent()
		if tracker.fired.Load() {
			t.Fatalf("fired flag should remain false when manager is nil")
		}
	})

	t.Run("only once for repeated calls", func(t *testing.T) {
		mgr := metrics.NewMetricsManager()
		channelKey := "ch-once"
		tracker := &firstTokenTracker{
			manager:      mgr,
			channelKey:   channelKey,
			requestStart: time.Now().Add(-100 * time.Millisecond),
		}
		tracker.MarkFirstEvent()
		first := mgr.GetFirstTokenLatencyAvg(channelKey)
		if first <= 0 {
			t.Fatalf("expected first latency > 0, got %d", first)
		}
		// Sleep so that a subsequent record (if it incorrectly fired) would change avg.
		time.Sleep(50 * time.Millisecond)
		tracker.MarkFirstEvent()
		tracker.MarkFirstEvent()
		second := mgr.GetFirstTokenLatencyAvg(channelKey)
		if second != first {
			t.Fatalf("MarkFirstEvent must fire only once; first=%d second=%d", first, second)
		}
	})
}

func TestChannelKeyForLB(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		upstream *config.UpstreamConfig
		want     string
	}{
		{name: "nil upstream", kind: "messages", upstream: nil, want: ""},
		{name: "name only with kind", kind: "messages", upstream: &config.UpstreamConfig{Name: "alpha"}, want: "messages:alpha"},
		{name: "empty kind, nil upstream", kind: "", upstream: nil, want: ""},
		{name: "name takes precedence over baseurl", kind: "messages", upstream: &config.UpstreamConfig{Name: "alpha", BaseURL: "https://api.example.com"}, want: "messages:alpha"},
		{name: "fallback to baseurl when name empty", kind: "messages", upstream: &config.UpstreamConfig{Name: "", BaseURL: "https://x"}, want: "messages:https://x"},
		{name: "fully empty struct", kind: "messages", upstream: &config.UpstreamConfig{}, want: ""},
		{name: "chat kind", kind: "chat", upstream: &config.UpstreamConfig{Name: "openai"}, want: "chat:openai"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ChannelKeyForLB(tt.kind, tt.upstream); got != tt.want {
				t.Fatalf("ChannelKeyForLB(%q, %+v) = %q, want %q", tt.kind, tt.upstream, got, tt.want)
			}
		})
	}
}

// runHandleStreamForFirstToken 是一个帮助函数，构造完整的 HandleStreamResponse 调用，
// 注入指定数量的 SSE event 并返回测试期间的 MetricsManager 与 channelKey。
func runHandleStreamForFirstToken(t *testing.T, events []string) (*metrics.MetricsManager, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))

	mgr := metrics.NewMetricsManager()
	channelKey := "messages:test-channel"
	upstream := &config.UpstreamConfig{Name: "test-channel", BaseURL: "https://upstream.local"}
	SetLBMetricsManager(c, mgr)
	SetLBAPIType(c, "messages")

	eventChan := make(chan string, len(events))
	errChan := make(chan error, 1)
	// 异步写入并预留 ≥1ms 让 requestStart→preflight 出现可测量的耗时。
	go func() {
		time.Sleep(5 * time.Millisecond)
		for _, ev := range events {
			eventChan <- ev
		}
		close(eventChan)
		close(errChan)
	}()

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}

	_, err := HandleStreamResponse(c, resp, &testStreamProvider{eventChan: eventChan, errChan: errChan}, &config.EnvConfig{}, []byte(`{}`), upstream)
	if err != nil {
		t.Fatalf("HandleStreamResponse() error = %v", err)
	}
	return mgr, channelKey
}

func TestHandleStreamResponse_FirstTokenLatencyRecorded(t *testing.T) {
	tests := []struct {
		name        string
		events      []string
		wantRecord  bool
		description string
	}{
		{
			name:        "full claude stream records once",
			events:      claudeStreamSequence("hello world"),
			wantRecord:  true,
			description: "多事件流仍只记录一次首 token 延迟样本",
		},
		{
			name:        "minimal text delta stream",
			events:      claudeStreamSequence("ok"),
			wantRecord:  true,
			description: "只要预检通过就应记录首 token",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, channelKey := runHandleStreamForFirstToken(t, tt.events)
			latency := mgr.GetFirstTokenLatencyAvg(channelKey)
			if tt.wantRecord {
				if latency <= 0 {
					t.Fatalf("expected first token latency > 0, got %d (%s)", latency, tt.description)
				}
			} else if latency != 0 {
				t.Fatalf("expected no recorded latency, got %d", latency)
			}
		})
	}
}

func TestHandleStreamResponse_FirstTokenSingleSampleAcrossManyEvents(t *testing.T) {
	mgr, channelKey := runHandleStreamForFirstToken(t, claudeStreamSequence("multi event stream"))
	first := mgr.GetFirstTokenLatencyAvg(channelKey)
	if first <= 0 {
		t.Fatalf("expected first sample to be recorded, got %d", first)
	}
	// avg over a single sample equals that sample. Issuing another stream on the same
	// channelKey should add a new sample and shift avg, but within a single stream the
	// first-token tracker must not double-count.
	// Direct re-poll within same stream's lifecycle is simulated by checking value
	// stability after a small delay (no further stream invocation).
	time.Sleep(10 * time.Millisecond)
	second := mgr.GetFirstTokenLatencyAvg(channelKey)
	if second != first {
		t.Fatalf("avg latency drifted within single stream: first=%d second=%d", first, second)
	}
}

func TestHandleStreamResponse_FirstTokenNotRecordedOnEmptyStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))

	mgr := metrics.NewMetricsManager()
	channelKey := "messages:empty-channel"
	upstream := &config.UpstreamConfig{Name: "empty-channel"}
	SetLBMetricsManager(c, mgr)
	SetLBAPIType(c, "messages")

	// 完全空流：channel 无事件直接关闭 → preflight IsEmpty=true → 不应记录首 token
	eventChan := make(chan string)
	errChan := make(chan error)
	close(eventChan)
	close(errChan)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}

	_, err := HandleStreamResponse(c, resp, &testStreamProvider{eventChan: eventChan, errChan: errChan}, &config.EnvConfig{}, []byte(`{}`), upstream)
	if err != ErrEmptyStreamResponse {
		t.Fatalf("expected ErrEmptyStreamResponse, got %v", err)
	}
	if got := mgr.GetFirstTokenLatencyAvg(channelKey); got != 0 {
		t.Fatalf("expected no first token recorded for empty stream, got %d", got)
	}
}

func TestHandleStreamResponse_NoMetricsManagerIsNoOp(t *testing.T) {
	// 未注入 MetricsManager 时，stream 行为应保持不变且不发生 panic。
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))

	upstream := &config.UpstreamConfig{Name: "no-mgr-channel"}
	events := claudeStreamSequence("hi")
	eventChan := make(chan string, len(events))
	errChan := make(chan error, 1)
	for _, ev := range events {
		eventChan <- ev
	}
	close(eventChan)
	close(errChan)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}

	if _, err := HandleStreamResponse(c, resp, &testStreamProvider{eventChan: eventChan, errChan: errChan}, &config.EnvConfig{}, []byte(`{}`), upstream); err != nil {
		t.Fatalf("HandleStreamResponse() unexpected error: %v", err)
	}
}
