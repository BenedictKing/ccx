package pipeline

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BenedictKing/ccx/internal/llm"
)

// fakeInbound 是测试用的 Inbound 实现，仅做最简单的请求/响应映射。
type fakeInbound struct{}

func (fakeInbound) Format() llm.Format { return llm.FormatOpenAIChat }

func (fakeInbound) TransformRequest(_ context.Context, _ *http.Request, body []byte) (*llm.Request, error) {
	return &llm.Request{
		Format:  llm.FormatOpenAIChat,
		Model:   "gpt-4o",
		RawBody: body,
	}, nil
}

func (fakeInbound) TransformResponse(_ context.Context, resp *llm.Response) ([]byte, http.Header, error) {
	return resp.Body, resp.Header, nil
}

func (fakeInbound) TransformStream(_ context.Context, in llm.Stream[*llm.Response]) llm.Stream[*llm.StreamEvent] {
	ch := make(chan *llm.StreamEvent)
	go func() {
		defer close(ch)
		for in.Next() {
			_ = in.Current()
			ch <- &llm.StreamEvent{Data: []byte("ok")}
		}
	}()
	return llm.NewChanStream(context.Background(), ch, nil)
}

// fakeOutbound 是支持 retry 双层的测试 Outbound。
//
// nextChannelFails / canRetryAfterErr 控制重试行为；attempts 计数 TransformRequest 调用次数。
type fakeOutbound struct {
	transformErr error // TransformRequest 返回的错误
	executeErr   error // Execute 返回错误（通过 fakeExecutor 注入）

	hasMore         bool
	nextChannelErr  error
	canRetryFn      func(err error) bool
	prepareRetryErr error

	attempts            int32
	channelSwitchCalls  int32
	sameChannelCalls    int32
	transformReqCalls   int32
	transformRespCalls  int32
	transformStreamCall int32
}

func (o *fakeOutbound) TransformRequest(_ context.Context, _ *llm.Request) (*http.Request, []byte, error) {
	atomic.AddInt32(&o.transformReqCalls, 1)
	atomic.AddInt32(&o.attempts, 1)
	if o.transformErr != nil {
		return nil, nil, o.transformErr
	}
	req, _ := http.NewRequest(http.MethodPost, "http://upstream.local/v1/chat/completions", strings.NewReader("{}"))
	return req, []byte("{}"), nil
}

func (o *fakeOutbound) TransformResponse(_ context.Context, resp *http.Response) (*llm.Response, error) {
	atomic.AddInt32(&o.transformRespCalls, 1)
	return &llm.Response{
		Format:     llm.FormatOpenAIChat,
		StatusCode: resp.StatusCode,
		Body:       []byte("ok"),
	}, nil
}

func (o *fakeOutbound) TransformStream(_ context.Context, _ *http.Response) llm.Stream[*llm.Response] {
	atomic.AddInt32(&o.transformStreamCall, 1)
	ch := make(chan *llm.Response)
	close(ch)
	return llm.NewChanStream(context.Background(), ch, nil)
}

// Retryable: 是否还有更多 channel
func (o *fakeOutbound) HasMoreChannels() bool { return o.hasMore }

func (o *fakeOutbound) NextChannel(_ context.Context) error {
	atomic.AddInt32(&o.channelSwitchCalls, 1)
	return o.nextChannelErr
}

// ChannelRetryable: 同 channel 重试
func (o *fakeOutbound) CanRetry(err error) bool {
	if o.canRetryFn == nil {
		return false
	}
	return o.canRetryFn(err)
}

func (o *fakeOutbound) PrepareForRetry(_ context.Context) error {
	atomic.AddInt32(&o.sameChannelCalls, 1)
	return o.prepareRetryErr
}

func newFakeExecutor(err error, status int) Executor {
	return ExecutorFunc(func(_ context.Context, _ *http.Request) (*http.Response, error) {
		if err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       http.NoBody,
		}, nil
	})
}

func TestPipeline_SuccessNonStream(t *testing.T) {
	out := &fakeOutbound{}
	p := NewFactory(newFakeExecutor(nil, 200)).Pipeline(fakeInbound{}, out)
	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	res, err := p.Process(context.Background(), req, []byte("{}"))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res == nil || res.Stream {
		t.Fatalf("expected non-stream result, got %+v", res)
	}
	if got := atomic.LoadInt32(&out.transformReqCalls); got != 1 {
		t.Fatalf("expected 1 outbound transform, got %d", got)
	}
}

func TestPipeline_SwitchChannelOnExecutorError(t *testing.T) {
	wantErr := errors.New("upstream down")

	// 第一个 attempt 失败，切 channel 后第二个成功。
	var attempt int32
	exec := ExecutorFunc(func(_ context.Context, _ *http.Request) (*http.Response, error) {
		n := atomic.AddInt32(&attempt, 1)
		if n == 1 {
			return nil, wantErr
		}
		return &http.Response{StatusCode: 200, Body: http.NoBody, Header: make(http.Header)}, nil
	})

	out := &fakeOutbound{hasMore: true}
	p := NewFactory(exec).Pipeline(fakeInbound{}, out, WithRetry(3, 0, 0))

	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	res, err := p.Process(context.Background(), req, []byte("{}"))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Stream {
		t.Fatal("expected non-stream")
	}
	if got := atomic.LoadInt32(&out.channelSwitchCalls); got != 1 {
		t.Fatalf("expected 1 channel switch, got %d", got)
	}
	if got := atomic.LoadInt32(&out.transformReqCalls); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestPipeline_SameChannelRetryPreferredOverChannelSwitch(t *testing.T) {
	wantErr := errors.New("transient")

	var attempt int32
	exec := ExecutorFunc(func(_ context.Context, _ *http.Request) (*http.Response, error) {
		n := atomic.AddInt32(&attempt, 1)
		if n < 3 {
			return nil, wantErr
		}
		return &http.Response{StatusCode: 200, Body: http.NoBody, Header: make(http.Header)}, nil
	})

	out := &fakeOutbound{
		hasMore: true,
		canRetryFn: func(err error) bool {
			return errors.Is(err, wantErr)
		},
	}
	p := NewFactory(exec).Pipeline(fakeInbound{}, out, WithRetry(0, 5, 0))

	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if _, err := p.Process(context.Background(), req, []byte("{}")); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got := atomic.LoadInt32(&out.sameChannelCalls); got != 2 {
		t.Fatalf("expected 2 same-channel retries, got %d", got)
	}
	if got := atomic.LoadInt32(&out.channelSwitchCalls); got != 0 {
		t.Fatalf("expected 0 channel switches, got %d", got)
	}
}

func TestPipeline_StopsWhenNoMoreChannels(t *testing.T) {
	wantErr := errors.New("permanent")
	exec := ExecutorFunc(func(_ context.Context, _ *http.Request) (*http.Response, error) {
		return nil, wantErr
	})

	out := &fakeOutbound{hasMore: false}
	p := NewFactory(exec).Pipeline(fakeInbound{}, out, WithRetry(3, 0, 0))

	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err := p.Process(context.Background(), req, []byte("{}"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
	// 仅尝试一次，不切 channel
	if got := atomic.LoadInt32(&out.transformReqCalls); got != 1 {
		t.Fatalf("expected 1 attempt, got %d", got)
	}
}

func TestPipeline_CtxCancelStopsRetry(t *testing.T) {
	wantErr := errors.New("transient")
	ctx, cancel := context.WithCancel(context.Background())
	exec := ExecutorFunc(func(_ context.Context, _ *http.Request) (*http.Response, error) {
		cancel()
		return nil, wantErr
	})
	out := &fakeOutbound{hasMore: true, canRetryFn: func(error) bool { return true }}
	p := NewFactory(exec).Pipeline(fakeInbound{}, out, WithRetry(5, 5, 0))

	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err := p.Process(ctx, req, []byte("{}"))
	if err == nil {
		t.Fatal("expected error after ctx cancel")
	}
}

// recordingMiddleware 记录 hook 调用顺序。
type recordingMiddleware struct {
	BaseMiddleware
	name   string
	events *[]string
}

func (m *recordingMiddleware) BeforeRequest(_ context.Context, req *llm.Request) (*llm.Request, error) {
	*m.events = append(*m.events, m.name+":before")
	return req, nil
}

func (m *recordingMiddleware) RawRequest(_ context.Context, req *http.Request) (*http.Request, error) {
	*m.events = append(*m.events, m.name+":raw_req")
	return req, nil
}

func (m *recordingMiddleware) RawResponse(_ context.Context, resp *http.Response) (*http.Response, error) {
	*m.events = append(*m.events, m.name+":raw_resp")
	return resp, nil
}

func (m *recordingMiddleware) LlmResponse(_ context.Context, resp *llm.Response) (*llm.Response, error) {
	*m.events = append(*m.events, m.name+":llm_resp")
	return resp, nil
}

func TestPipeline_MiddlewareOrder(t *testing.T) {
	var events []string
	mw1 := &recordingMiddleware{name: "mw1", events: &events}
	mw2 := &recordingMiddleware{name: "mw2", events: &events}

	out := &fakeOutbound{}
	p := NewFactory(newFakeExecutor(nil, 200)).Pipeline(
		fakeInbound{},
		out,
		WithMiddlewares(mw1, mw2),
	)

	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if _, err := p.Process(context.Background(), req, []byte("{}")); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}

	want := []string{
		"mw1:before", "mw2:before",
		"mw1:raw_req", "mw2:raw_req",
		"mw1:raw_resp", "mw2:raw_resp",
		"mw1:llm_resp", "mw2:llm_resp",
	}
	if len(events) != len(want) {
		t.Fatalf("event count mismatch: got %v, want %v", events, want)
	}
	for i, w := range want {
		if events[i] != w {
			t.Fatalf("event[%d] = %q; want %q (full: %v)", i, events[i], w, events)
		}
	}
}

func TestPipeline_BeforeRequestErrorAborts(t *testing.T) {
	wantErr := errors.New("preprocess fail")
	mw := &errorMiddleware{beforeErr: wantErr}
	out := &fakeOutbound{}
	p := NewFactory(newFakeExecutor(nil, 200)).Pipeline(fakeInbound{}, out, WithMiddlewares(mw))

	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err := p.Process(context.Background(), req, []byte("{}"))
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("expected wrap of %v, got %v", wantErr, err)
	}
	if got := atomic.LoadInt32(&out.transformReqCalls); got != 0 {
		t.Fatalf("outbound should not be called when before-request fails, got %d", got)
	}
}

type errorMiddleware struct {
	BaseMiddleware
	beforeErr error
}

func (m *errorMiddleware) BeforeRequest(_ context.Context, req *llm.Request) (*llm.Request, error) {
	if m.beforeErr != nil {
		return nil, fmt.Errorf("before: %w", m.beforeErr)
	}
	return req, nil
}
