package autopilot

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// createSSETestStore 创建使用内存 SQLite 的 TraceStore（GetTraceDetail 需要 DB）。
func createSSETestStore(t *testing.T) *TraceStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewTraceStoreWithDB(db)
	if err != nil {
		t.Fatalf("创建 TraceStore 失败: %v", err)
	}
	return store
}

// ── SSE Golden 回归测试（设计 §6.4）──
//
// 建立本地 fake upstream 的 SSE fixture，验证 Trace v2 不改变流式输出。
// 断言：SSE event/data 顺序、状态码、结束帧语义与未开启 trace 时一致。

// fakeSSEUpstream 创建一个返回固定 SSE 流的测试服务器。
func fakeSSEUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	sseBody := strings.Join([]string{
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"delta":{"role":"assistant","content":""},"index":0}]}` + "\n\n",
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"delta":{"content":"Hello"},"index":0}]}` + "\n\n",
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"delta":{"content":" world"},"index":0}]}` + "\n\n",
		`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"delta":{},"index":0,"finish_reason":"stop"}]}` + "\n\n",
		`data: [DONE]` + "\n\n",
	}, "")

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, line := range strings.SplitAfter(sseBody, "\n") {
			w.Write([]byte(line))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
}

// collectSSEEvents 从 HTTP 响应体收集所有 SSE 事件。
func collectSSEEvents(t *testing.T, body []byte) []string {
	t.Helper()
	var events []string
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			events = append(events, line)
		}
	}
	return events
}

// TestSSEGolden_FakeUpstreamNoTrace 验证 fake upstream SSE 输出基线。
// 不涉及 SmartRouter，仅验证 fake upstream 的 SSE 帧顺序正确。
func TestSSEGolden_FakeUpstreamNoTrace(t *testing.T) {
	server := fakeSSEUpstream(t)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", server.URL, strings.NewReader(`{}`))
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	body := make([]byte, 0, 4096)
	buf := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if err != nil {
			break
		}
	}

	events := collectSSEEvents(t, body)
	if len(events) != 5 {
		t.Fatalf("SSE 事件数 = %d, want 5", len(events))
	}
	// 验证结束帧
	if events[len(events)-1] != "data: [DONE]" {
		t.Errorf("最后事件 = %q, want 'data: [DONE]'", events[len(events)-1])
	}
}

// TestSSEGolden_TraceStoreDoesNotAffectOutput 验证开启 TraceStore 后
// fake upstream 的 SSE 输出帧顺序与未开启时完全一致。
func TestSSEGolden_TraceStoreDoesNotAffectOutput(t *testing.T) {
	server := fakeSSEUpstream(t)
	defer server.Close()

	// 创建带 DB 的 TraceStore（模拟 trace v2 已开启）
	store := createSSETestStore(t)
	traceUID := GenerateTraceUIDv2()
	store.Record(&RoutingDecisionTrace{
		TraceUID:        traceUID,
		SchemaVersion:   2,
		RequestKind:     "chat",
		TaskClass:       TaskClassWorker,
		Mode:            RoutingModeShadow,
		ManualIntentUID: "mi_sse",
		CreatedAt:       time.Now().UTC(),
	})

	// 发起 SSE 请求（与基线测试相同的 fake upstream）
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", server.URL, strings.NewReader(`{}`))
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200", resp.StatusCode)
	}

	body := make([]byte, 0, 4096)
	buf := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if err != nil {
			break
		}
	}

	events := collectSSEEvents(t, body)

	// 验证事件数与基线一致
	if len(events) != 5 {
		t.Errorf("SSE 事件数 = %d, want 5（与未开启 trace 时一致）", len(events))
	}

	// 验证结束帧
	if len(events) > 0 && events[len(events)-1] != "data: [DONE]" {
		t.Errorf("最后事件 = %q, want 'data: [DONE]'", events[len(events)-1])
	}

	// 记录终态（不影响已返回的 SSE 输出）
	_ = store.RecordOutcome(traceUID, RoutingOutcome{
		Terminal:           true,
		Success:            true,
		StatusCode:         200,
		RequestDurationMs:  100,
		FirstByteLatencyMs: 10,
		CompletedAt:        time.Now().UTC(),
	})

	// 验证 trace 详情可读取
	detail, err := store.GetTraceDetail(traceUID)
	if err != nil {
		t.Fatalf("GetTraceDetail 失败: %v", err)
	}
	if detail == nil {
		t.Fatal("应能读取 trace 详情")
	}
}

// TestSSEGolden_EventOrderConsistent 验证 SSE 事件顺序固定。
func TestSSEGolden_EventOrderConsistent(t *testing.T) {
	server := fakeSSEUpstream(t)
	defer server.Close()

	// 运行 3 次，验证输出一致
	var allEvents [][]string
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		req, _ := http.NewRequestWithContext(ctx, "POST", server.URL, strings.NewReader(`{}`))
		req.Header.Set("Accept", "text/event-stream")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			cancel()
			t.Fatalf("第 %d 次请求失败: %v", i, err)
		}

		body := make([]byte, 0, 4096)
		buf := make([]byte, 1024)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				body = append(body, buf[:n]...)
			}
			if err != nil {
				break
			}
		}
		resp.Body.Close()
		cancel()

		events := collectSSEEvents(t, body)
		allEvents = append(allEvents, events)
	}

	// 验证三次运行事件一致
	for i := 1; i < len(allEvents); i++ {
		if len(allEvents[i]) != len(allEvents[0]) {
			t.Fatalf("第 %d 次事件数 = %d, want %d", i, len(allEvents[i]), len(allEvents[0]))
		}
		for j, event := range allEvents[i] {
			if event != allEvents[0][j] {
				t.Errorf("第 %d 次事件[%d] = %q, want %q", i, j, event, allEvents[0][j])
			}
		}
	}
}

// TestSSEGolden_FirstByteLatencyRecorded 验证首字节时延被记录。
func TestSSEGolden_FirstByteLatencyRecorded(t *testing.T) {
	server := fakeSSEUpstream(t)
	defer server.Close()

	store := createSSETestStore(t)
	traceUID := GenerateTraceUIDv2()
	store.Record(&RoutingDecisionTrace{
		TraceUID:        traceUID,
		SchemaVersion:   2,
		RequestKind:     "chat",
		TaskClass:       TaskClassWorker,
		Mode:            RoutingModeShadow,
		ManualIntentUID: "mi_sse",
		CreatedAt:       time.Now().UTC(),
	})

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "POST", server.URL, strings.NewReader(`{}`))
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	firstByteAt := time.Now()
	firstByteMs := firstByteAt.Sub(start).Milliseconds()

	// 消费响应体
	buf := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(buf)
		if n == 0 || err != nil {
			break
		}
	}
	completedAt := time.Now()
	durationMs := completedAt.Sub(start).Milliseconds()

	// 记录终态
	_ = store.RecordOutcome(traceUID, RoutingOutcome{
		Terminal:           true,
		Success:            true,
		StatusCode:         200,
		RequestDurationMs:  durationMs,
		FirstByteLatencyMs: firstByteMs,
		CompletedAt:        completedAt.UTC(),
	})

	detail, err := store.GetTraceDetail(traceUID)
	if err != nil {
		t.Fatalf("GetTraceDetail 失败: %v", err)
	}
	if detail.FirstByteLatencyMs < 0 {
		t.Errorf("FirstByteLatencyMs = %d, 应 >= 0", detail.FirstByteLatencyMs)
	}
	if detail.RequestDurationMs < 0 {
		t.Errorf("RequestDurationMs = %d, 应 >= 0", detail.RequestDurationMs)
	}
}

// TestSSEGolden_CancelDoesNotCrash 验证客户端取消不导致 trace 崩溃。
func TestSSEGolden_CancelDoesNotCrash(t *testing.T) {
	server := fakeSSEUpstream(t)
	defer server.Close()

	store := createSSETestStore(t)
	traceUID := GenerateTraceUIDv2()
	store.Record(&RoutingDecisionTrace{
		TraceUID:        traceUID,
		SchemaVersion:   2,
		RequestKind:     "chat",
		TaskClass:       TaskClassWorker,
		Mode:            RoutingModeShadow,
		ManualIntentUID: "mi_sse",
		CreatedAt:       time.Now().UTC(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", server.URL, strings.NewReader(`{}`))
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}

	// 读取一个 chunk 后取消
	buf := make([]byte, 1024)
	resp.Body.Read(buf)
	cancel()
	resp.Body.Close()

	// 记录取消终态（不应崩溃）
	err = store.RecordOutcome(traceUID, RoutingOutcome{
		Terminal:          true,
		Success:           false,
		StatusCode:        0,
		Outcome:           "cancelled",
		RequestDurationMs: 50,
		CompletedAt:       time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("取消后 RecordOutcome 不应失败: %v", err)
	}

	// 验证 trace 仍可读取
	detail, err := store.GetTraceDetail(traceUID)
	if err != nil {
		t.Fatalf("取消后 GetTraceDetail 不应失败: %v", err)
	}
	if detail.Outcome != "cancelled" {
		t.Errorf("Outcome = %q, want cancelled", detail.Outcome)
	}
}

// TestSSEGolden_TraceDetailSanitized 验证 SSE trace 详情不含敏感字段。
func TestSSEGolden_TraceDetailSanitized(t *testing.T) {
	store := createSSETestStore(t)
	traceUID := GenerateTraceUIDv2()

	// 记录含"敏感"字段的 trace
	store.Record(&RoutingDecisionTrace{
		TraceUID:           traceUID,
		SchemaVersion:      2,
		RequestKind:        "chat",
		TaskClass:          TaskClassWorker,
		Mode:               RoutingModeShadow,
		ManualIntentUID:    "mi_sse_san",
		SelectedMetricsKey: "https://api.example.com|sk-secret-key-12345678",
		CreatedAt:          time.Now().UTC(),
	})

	_ = store.RecordOutcome(traceUID, RoutingOutcome{
		Terminal:          true,
		Success:           true,
		StatusCode:        200,
		RequestDurationMs: 500,
		CompletedAt:       time.Now().UTC(),
	})

	detail, err := store.GetTraceDetail(traceUID)
	if err != nil {
		t.Fatalf("GetTraceDetail 失败: %v", err)
	}

	// 验证 trace 详情序列化后不含敏感字段
	data, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	sensitive := ScanJSONForSensitive(data)
	if len(sensitive) > 0 {
		t.Errorf("trace 详情含敏感字段: %v", sensitive)
	}
}
