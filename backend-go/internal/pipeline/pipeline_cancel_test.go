package pipeline

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/llm"
)

// trackedBody 模拟一个上游 chunked body：永远阻塞在 Read 直到 Close 被调用。
// 用于验证 cleanup 是否真的关闭了 body 并解除了下游 fan-out 的阻塞。
type trackedBody struct {
	closed atomic.Bool
	closeC chan struct{}
	once   sync.Once
}

func newTrackedBody() *trackedBody {
	return &trackedBody{closeC: make(chan struct{})}
}

func (b *trackedBody) Read(p []byte) (int, error) {
	<-b.closeC
	return 0, io.EOF
}

func (b *trackedBody) Close() error {
	b.once.Do(func() {
		b.closed.Store(true)
		close(b.closeC)
	})
	return nil
}

// streamingOutbound 在第 N 次 attempt 上模拟 raw passthrough fan-out：
//   - TransformRequest 注册 attempt 级 cancel + body + chan 到 ctx 中的 AttemptState；
//   - TransformStream 启动一个 goroutine 持续从 trackedBody 读取（会阻塞到 Close）；
//   - 第一次 attempt 总是返回 ErrEmptyResponse（respCh 立即 close），强制进入 retry；
//   - 第二次 attempt 直接成功返回非空流。
type streamingOutbound struct {
	mu sync.Mutex

	attempts int
	bodies   []*trackedBody
	cancels  []context.CancelFunc
	chans    []chan *llm.StreamEvent
	wgs      []*sync.WaitGroup

	hasMore  bool
	allEmpty bool // 若为 true，所有 attempt 都返回 ErrEmptyResponse
}

func (o *streamingOutbound) Format() llm.Format { return llm.FormatOpenAIChat }

func (o *streamingOutbound) TransformRequest(_ context.Context, _ *llm.Request) (*http.Request, []byte, error) {
	req, _ := http.NewRequest(http.MethodPost, "http://upstream.local/v1/chat/completions", http.NoBody)
	return req, []byte("{}"), nil
}

func (o *streamingOutbound) TransformResponse(_ context.Context, _ *http.Response) (*llm.Response, error) {
	return &llm.Response{Format: llm.FormatOpenAIChat, StatusCode: 200}, nil
}

// TransformStream 模拟 raw passthrough fan-out：把 attempt 资源挂到 AttemptState 上。
func (o *streamingOutbound) TransformStream(ctx context.Context, resp *http.Response) llm.Stream[*llm.Response] {
	o.mu.Lock()
	o.attempts++
	idx := o.attempts
	o.mu.Unlock()

	state := AttemptStateFrom(ctx)

	body := newTrackedBody()
	resp.Body = body

	attemptCtx, cancel := context.WithCancel(ctx)
	rawCh := make(chan *llm.StreamEvent)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(rawCh)
		// 模拟 fan-out goroutine：持续从 body 读取直到 ctx 取消或 body 关闭。
		buf := make([]byte, 256)
		for {
			select {
			case <-attemptCtx.Done():
				return
			default:
			}
			_, err := body.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	if state != nil {
		state.RawProviderResponse = resp
		state.RawStreamCancel = cancel
		state.RawStreamCh = rawCh
	}

	o.mu.Lock()
	o.bodies = append(o.bodies, body)
	o.cancels = append(o.cancels, cancel)
	o.chans = append(o.chans, rawCh)
	o.wgs = append(o.wgs, &wg)
	o.mu.Unlock()

	if idx == 1 || o.allEmpty {
		// attempt 1（或 allEmpty 模式下所有 attempt）：返回一个立即关闭的空流
		// → prefetch 拿不到 Response → ErrEmptyResponse。
		respCh := make(chan *llm.Response)
		close(respCh)
		return llm.NewChanStream(context.Background(), respCh, nil)
	}

	// attempt 2+：返回一个含一个 Response 的流（正常通过 prefetch）。
	respCh2 := make(chan *llm.Response, 1)
	respCh2 <- &llm.Response{Format: llm.FormatOpenAIChat, StatusCode: 200}
	close(respCh2)
	return llm.NewChanStream(context.Background(), respCh2, nil)
}

func (o *streamingOutbound) HasMoreChannels() bool          { return o.hasMore }
func (o *streamingOutbound) NextChannel(context.Context) error { return nil }

// streamingInbound 标记请求为 stream，并在 TransformRequest 中把 IsStream 打开。
type streamingInbound struct{ fakeInbound }

func (streamingInbound) TransformRequest(_ context.Context, _ *http.Request, body []byte) (*llm.Request, error) {
	t := true
	return &llm.Request{
		Format:  llm.FormatOpenAIChat,
		Model:   "gpt-4o",
		RawBody: body,
		Stream:  &t,
	}, nil
}

func newStreamingExecutor() Executor {
	return ExecutorFunc(func(_ context.Context, _ *http.Request) (*http.Response, error) {
		// 由 TransformStream 重新挂载 body；此处 body 占位即可。
		return &http.Response{
			StatusCode: 200,
			Header:     make(http.Header),
			Body:       http.NoBody,
		}, nil
	})
}

// TestPipelineProcess_StreamAttemptCancelOnRetry 验证 attempt 1 的 stream 资源
// 在 retry 之前被全部释放：cancel 触发 / body 关闭 / fan-out goroutine 退出。
func TestPipelineProcess_StreamAttemptCancelOnRetry(t *testing.T) {
	out := &streamingOutbound{hasMore: true}
	p := NewFactory(newStreamingExecutor()).Pipeline(
		streamingInbound{},
		out,
		WithRetry(3, 0, 0),
		WithEmptyResponseDetection(),
	)

	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	res, err := p.Process(context.Background(), req, []byte("{}"))
	if err != nil {
		t.Fatalf("expected retry to succeed on attempt 2, got err: %v", err)
	}
	if res == nil || !res.Stream {
		t.Fatalf("expected streaming result, got %+v", res)
	}
	// 消费/关闭 attempt 2 的流以避免 goroutine 泄漏。
	if res.EventStream != nil {
		for res.EventStream.Next() {
		}
		_ = res.EventStream.Close()
	}

	out.mu.Lock()
	defer out.mu.Unlock()

	if got := out.attempts; got < 2 {
		t.Fatalf("expected at least 2 attempts (retry triggered), got %d", got)
	}

	// 断言 a：attempt 1 的 ctx 已被 cancel —— cancel func 已挂载，Reset 后 state
	// 字段已清空，但底层 cancel 闭包已被 cleanup 调用过。我们通过 body.Close
	// 是否被调用 + chan 是否已关闭来证明 cleanup 流程已完整执行。
	body1 := out.bodies[0]
	if !body1.closed.Load() {
		t.Fatalf("attempt 1 body.Close was NOT called; cleanup did not close response body")
	}

	// 断言 b：attempt 1 的 RawStreamCh 已被 drain 并关闭（cleanup drain 到 close 后才返回）。
	ch1 := out.chans[0]
	select {
	case _, ok := <-ch1:
		if ok {
			t.Fatalf("attempt 1 raw stream channel still has events; expected closed after cleanup")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("attempt 1 raw stream channel not closed within 2s; fan-out goroutine likely leaked")
	}

	// 断言 c：attempt 1 的 fan-out goroutine 已退出。
	doneCh := make(chan struct{})
	wg1 := out.wgs[0]
	go func() {
		wg1.Wait()
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("attempt 1 fan-out goroutine did not exit within 2s")
	}
}

// TestPipelineProcess_StreamCleanupOnContextCancel 验证 ctx 取消导致主循环
// 退出时，当前 attempt 持有的 stream 资源被全部清理。
func TestPipelineProcess_StreamCleanupOnContextCancel(t *testing.T) {
	// allEmpty: 所有 attempt 都返回 ErrEmptyResponse，让 retry 持续运转，
	// 配合 retryDelay 给 ctx cancel 一个发生的时机。
	out := &streamingOutbound{hasMore: true, allEmpty: true}

	parentCtx, cancel := context.WithCancel(context.Background())
	exec := ExecutorFunc(func(_ context.Context, _ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Header:     make(http.Header),
			Body:       http.NoBody,
		}, nil
	})

	p := NewFactory(exec).Pipeline(
		streamingInbound{},
		out,
		// retryDelay = 200ms，给 cancel 一个窗口在 retry 间隙触发。
		WithRetry(10, 0, 200*time.Millisecond),
		WithEmptyResponseDetection(),
	)

	// 在 attempt 1 已经挂载好 fan-out 资源后取消 parent ctx。
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err := p.Process(parentCtx, req, []byte("{}"))
	if err == nil {
		t.Fatalf("expected error after ctx cancel, got nil")
	}

	out.mu.Lock()
	defer out.mu.Unlock()

	if len(out.bodies) == 0 {
		t.Fatalf("expected at least 1 attempt to register a body, got 0")
	}

	// 断言 a + b：body 已关闭、chan 已关闭。
	body1 := out.bodies[0]
	deadline := time.After(2 * time.Second)
	for !body1.closed.Load() {
		select {
		case <-deadline:
			t.Fatalf("attempt 1 body.Close was NOT called after ctx cancel")
		case <-time.After(20 * time.Millisecond):
		}
	}

	ch1 := out.chans[0]
	select {
	case _, ok := <-ch1:
		if ok {
			t.Fatalf("attempt 1 raw stream channel still has events after ctx cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("attempt 1 raw stream channel not closed after ctx cancel; goroutine leak")
	}

	// 断言 c：fan-out goroutine 已退出。
	doneCh := make(chan struct{})
	wg1 := out.wgs[0]
	go func() {
		wg1.Wait()
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("attempt 1 fan-out goroutine did not exit within 2s after ctx cancel")
	}
}

// TestCleanupAttemptStreamResources_Idempotent 验证 cleanup 的幂等性 + 空 state 兼容。
func TestCleanupAttemptStreamResources_Idempotent(t *testing.T) {
	// 1. nil state 不 panic。
	cleanupAttemptStreamResources(nil)

	// 2. 空 state 不 panic 且无副作用。
	cleanupAttemptStreamResources(&AttemptState{})

	// 3. 完整字段的 state，cleanup 后字段被 Reset。
	body := newTrackedBody()
	resp := &http.Response{Body: body}
	ch := make(chan *llm.StreamEvent)
	cancelCalled := atomic.Bool{}
	cancel := context.CancelFunc(func() { cancelCalled.Store(true); close(ch) })

	var errRef error
	state := &AttemptState{
		RawProviderResponse: resp,
		RawStreamCh:         ch,
		RawStreamCancel:     cancel,
		RawStreamErrRef:     &errRef,
	}

	cleanupAttemptStreamResources(state)

	if !cancelCalled.Load() {
		t.Fatal("cancel was not called")
	}
	if !body.closed.Load() {
		t.Fatal("body.Close was not called")
	}
	if state.RawProviderResponse != nil ||
		state.RawStreamCh != nil ||
		state.RawStreamCancel != nil ||
		state.RawStreamErrRef != nil {
		t.Fatalf("state was not reset: %+v", state)
	}

	// 4. 二次调用幂等。
	cleanupAttemptStreamResources(state)
}

// TestAttemptStateFrom_RoundTrip 验证 ctx 注入 / 取出。
func TestAttemptStateFrom_RoundTrip(t *testing.T) {
	if got := AttemptStateFrom(nil); got != nil {
		t.Fatalf("expected nil from nil ctx, got %+v", got)
	}
	if got := AttemptStateFrom(context.Background()); got != nil {
		t.Fatalf("expected nil from bare ctx, got %+v", got)
	}
	state := &AttemptState{OriginalModel: "x"}
	ctx := withAttemptState(context.Background(), state)
	got := AttemptStateFrom(ctx)
	if got != state {
		t.Fatalf("AttemptStateFrom did not return same pointer: got %p want %p", got, state)
	}
	// nil state 不污染 ctx。
	if ctx2 := withAttemptState(context.Background(), nil); AttemptStateFrom(ctx2) != nil {
		t.Fatal("withAttemptState(nil) should be a no-op")
	}
	// 避免未使用变量
	_ = errors.New("noop")
}
