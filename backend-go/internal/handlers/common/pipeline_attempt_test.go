package common

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/pipeline"
)

// helper：构造一个带可控 body 的 http.Response。
func buildResp(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// helper：等待一个 chan 关闭，超时则 t.Fatal。
func waitChanClosed[T any](t *testing.T, ch <-chan T, timeout time.Duration, name string) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline.C:
			t.Fatalf("timeout waiting for %s to close", name)
		}
	}
}

func TestBindRawStreamFanout_FullStreamCompletes(t *testing.T) {
	// 标准 SSE：两个 event，正常 EOF 结束。
	body := "data: {\"a\":1}\n\ndata: {\"a\":2}\n\n"
	resp := buildResp(body)

	state := &pipeline.AttemptState{}
	cleanup, err := BindRawStreamFanout(context.Background(), resp, state)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	defer cleanup()

	if state.RawStreamCh == nil || state.RawStreamCancel == nil || state.RawStreamErrRef == nil {
		t.Fatalf("attempt state fields not bound: %+v", state)
	}

	// 消费两条 event，然后 ch 关闭。
	var got [][]byte
	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-state.RawStreamCh:
			if !ok {
				break loop
			}
			got = append(got, ev.Data)
		case <-timeout:
			t.Fatal("timeout waiting for events")
		}
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d (%v)", len(got), got)
	}
	if string(got[0]) != "data: {\"a\":1}\n\n" || string(got[1]) != "data: {\"a\":2}\n\n" {
		t.Fatalf("event content mismatch: %q / %q", got[0], got[1])
	}

	if err := CollectFanoutErr(state); err != nil {
		t.Fatalf("expected no fan-out err, got %v", err)
	}
}

func TestBindRawStreamFanout_CleanupCancelsAndResets(t *testing.T) {
	// 用一个无限读取的 body：读完后 EOF，不会主动关闭，模拟还在流中。
	// 这里直接用空字符串，body 立刻 EOF；fan-out goroutine 应该自然退出。
	resp := buildResp("")
	state := &pipeline.AttemptState{}

	cleanup, err := BindRawStreamFanout(context.Background(), resp, state)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}

	// 等到 ch 关闭再 cleanup（验证正常退出路径）。
	waitChanClosed(t, state.RawStreamCh, 2*time.Second, "RawStreamCh")

	cleanup()
	// cleanup 之后 state 应被 Reset：所有 attempt 字段为 nil/zero。
	if state.RawStreamCh != nil || state.RawStreamCancel != nil || state.RawStreamErrRef != nil ||
		state.RawProviderResponse != nil || state.RawProviderRequest != nil || state.StreamCompleted {
		t.Fatalf("state not reset after cleanup: %+v", state)
	}

	// cleanup 应幂等。
	cleanup()
}

func TestBindRawStreamFanout_RebindAfterCleanup(t *testing.T) {
	// 第一次 attempt：绑定、消费、cleanup。
	resp1 := buildResp("data: ev1\n\n")
	state := &pipeline.AttemptState{}
	cleanup1, err := BindRawStreamFanout(context.Background(), resp1, state)
	if err != nil {
		t.Fatalf("first bind: %v", err)
	}
	waitChanClosed(t, state.RawStreamCh, 2*time.Second, "first RawStreamCh")
	cleanup1()

	// 第二次 attempt：相同 state 上再绑定（验证 retry 场景下的 fan-out 复用）。
	resp2 := buildResp("data: ev2\n\n")
	cleanup2, err := BindRawStreamFanout(context.Background(), resp2, state)
	if err != nil {
		t.Fatalf("second bind: %v", err)
	}
	defer cleanup2()

	var got []string
	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-state.RawStreamCh:
			if !ok {
				break loop
			}
			got = append(got, string(ev.Data))
		case <-timeout:
			t.Fatal("timeout")
		}
	}
	if len(got) != 1 || got[0] != "data: ev2\n\n" {
		t.Fatalf("second attempt events mismatch: %v", got)
	}
}

func TestBindRawStreamFanout_RebindWithoutCleanupRejected(t *testing.T) {
	resp1 := buildResp("data: ev1\n\n")
	state := &pipeline.AttemptState{}
	_, err := BindRawStreamFanout(context.Background(), resp1, state)
	if err != nil {
		t.Fatalf("first bind: %v", err)
	}

	// 没 cleanup 直接再 bind：必须报错（防止 fan-out goroutine 泄露）。
	resp2 := buildResp("data: ev2\n\n")
	if _, err := BindRawStreamFanout(context.Background(), resp2, state); err == nil {
		t.Fatal("expected error when re-binding without cleanup, got nil")
	}
}

func TestBindRawStreamFanout_NilArgsRejected(t *testing.T) {
	if _, err := BindRawStreamFanout(context.Background(), nil, &pipeline.AttemptState{}); err == nil {
		t.Fatal("expected error for nil response, got nil")
	}
	if _, err := BindRawStreamFanout(context.Background(), buildResp(""), nil); err == nil {
		t.Fatal("expected error for nil state, got nil")
	}
	if _, err := BindRawStreamFanout(context.Background(), &http.Response{StatusCode: 200, Body: nil}, &pipeline.AttemptState{}); err == nil {
		t.Fatal("expected error for nil body, got nil")
	}
}

// TestBindRawStreamFanout_ParentCtxCancelStopsFanout 验证父 ctx 取消时 fan-out
// goroutine 也会退出。
func TestBindRawStreamFanout_ParentCtxCancelStopsFanout(t *testing.T) {
	// 构造一个会"永久 block 在 read"的 body：用 pipe 写端不关。
	pr, pw := io.Pipe()
	defer pw.Close()
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: pr}

	parentCtx, parentCancel := context.WithCancel(context.Background())
	defer parentCancel()

	state := &pipeline.AttemptState{}
	cleanup, err := BindRawStreamFanout(parentCtx, resp, state)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}

	// 父 ctx 取消 → fan-out 应该退出 → ch 应该关闭。
	parentCancel()
	waitChanClosed(t, state.RawStreamCh, 5*time.Second, "RawStreamCh after parent cancel")

	cleanup()
}
