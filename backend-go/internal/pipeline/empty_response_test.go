package pipeline

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BenedictKing/ccx/internal/llm"
)

// emptyTestInbound 是一个把 *llm.Response 透传为 *llm.StreamEvent 的 inbound
// 测试桩；TransformStream 调用次数会被 transformStreamCalled 记录，便于断言
// "空响应时不进入 inbound stream 链路"。
type emptyTestInbound struct {
	transformStreamCalled int32
}

func (i *emptyTestInbound) Format() llm.Format { return llm.FormatOpenAIChat }

func (i *emptyTestInbound) TransformRequest(_ context.Context, _ *http.Request, body []byte) (*llm.Request, error) {
	stream := true
	return &llm.Request{
		Format:  llm.FormatOpenAIChat,
		Model:   "gpt-4o",
		Stream:  &stream,
		RawBody: body,
	}, nil
}

func (i *emptyTestInbound) TransformResponse(_ context.Context, resp *llm.Response) ([]byte, http.Header, error) {
	return resp.Body, resp.Header, nil
}

func (i *emptyTestInbound) TransformStream(_ context.Context, in llm.Stream[*llm.Response]) llm.Stream[*llm.StreamEvent] {
	atomic.AddInt32(&i.transformStreamCalled, 1)
	ch := make(chan *llm.StreamEvent)
	go func() {
		defer close(ch)
		for in.Next() {
			cur := in.Current()
			if cur == nil {
				continue
			}
			ch <- &llm.StreamEvent{Data: cur.Body}
		}
	}()
	return llm.NewChanStream(context.Background(), ch, nil)
}

// emptyStreamOutbound 是一个流式 outbound 测试桩，能按需注入：
//   - 一组 *llm.Response（按顺序产出）
//   - streamErr：在所有 response 产出后注入 SetErr
//
// 当 responses 为 nil 且 streamErr 为 nil 时，模拟"上游连接成功但流立即正常
// 结束、未产生任何 Response"，即典型空响应场景。
type emptyStreamOutbound struct {
	responses []*llm.Response
	streamErr error
}

func (o *emptyStreamOutbound) TransformRequest(_ context.Context, _ *llm.Request) (*http.Request, []byte, error) {
	req, _ := http.NewRequest(http.MethodPost, "http://upstream.local/v1/chat/completions", strings.NewReader("{}"))
	return req, []byte("{}"), nil
}

func (o *emptyStreamOutbound) TransformResponse(_ context.Context, _ *http.Response) (*llm.Response, error) {
	return &llm.Response{Format: llm.FormatOpenAIChat, Body: []byte("ok")}, nil
}

func (o *emptyStreamOutbound) TransformStream(_ context.Context, _ *http.Response) llm.Stream[*llm.Response] {
	ch := make(chan *llm.Response, len(o.responses))
	for _, r := range o.responses {
		ch <- r
	}
	close(ch)
	cs := llm.NewChanStream(context.Background(), ch, nil)
	if o.streamErr != nil {
		cs.SetErr(o.streamErr)
	}
	return cs
}

func newStreamExecutor(status int) Executor {
	return ExecutorFunc(func(_ context.Context, _ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       http.NoBody,
		}, nil
	})
}

// TestPipeline_EmptyResponse_DetectsAndReturnsErr 验证：当 outbound 流没有
// 产出任何 *llm.Response 即正常结束，且启用了 WithEmptyResponseDetection 时，
// pipeline 立即返回 ErrEmptyResponse；inbound.TransformStream 不应被调用。
func TestPipeline_EmptyResponse_DetectsAndReturnsErr(t *testing.T) {
	in := &emptyTestInbound{}
	out := &emptyStreamOutbound{} // 无 responses，无错误
	p := NewFactory(newStreamExecutor(200)).Pipeline(in, out, WithEmptyResponseDetection())

	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err := p.Process(context.Background(), req, []byte(`{"stream":true}`))
	if !errors.Is(err, ErrEmptyResponse) {
		t.Fatalf("expected ErrEmptyResponse, got %v", err)
	}
	if got := atomic.LoadInt32(&in.transformStreamCalled); got != 0 {
		t.Fatalf("inbound.TransformStream should not be called for empty stream, got %d", got)
	}
}

// TestPipeline_EmptyResponse_PreservesUnderlyingErr 验证：底层流报错时优先
// 返回底层错误，而不是 ErrEmptyResponse。
func TestPipeline_EmptyResponse_PreservesUnderlyingErr(t *testing.T) {
	wantErr := errors.New("network broken")
	in := &emptyTestInbound{}
	out := &emptyStreamOutbound{streamErr: wantErr}
	p := NewFactory(newStreamExecutor(200)).Pipeline(in, out, WithEmptyResponseDetection())

	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err := p.Process(context.Background(), req, []byte(`{"stream":true}`))
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wantErr=%v, got %v", wantErr, err)
	}
}

// TestPipeline_EmptyResponse_PassesThroughWhenContent 验证：当流产出任意一
// 个有内容的 Response 时，pipeline 正常返回 stream，并且预读到的首个
// Response 仍然能被下游消费。
func TestPipeline_EmptyResponse_PassesThroughWhenContent(t *testing.T) {
	in := &emptyTestInbound{}
	out := &emptyStreamOutbound{
		responses: []*llm.Response{
			{Format: llm.FormatOpenAIChat, Body: []byte(`{"delta":"hello"}`)},
			{Format: llm.FormatOpenAIChat, Body: []byte(`{"delta":"world"}`)},
		},
	}
	p := NewFactory(newStreamExecutor(200)).Pipeline(in, out, WithEmptyResponseDetection())

	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	res, err := p.Process(context.Background(), req, []byte(`{"stream":true}`))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res == nil || !res.Stream || res.EventStream == nil {
		t.Fatalf("expected stream result, got %+v", res)
	}

	// 完整消费一次 EventStream，断言两条 event 都流出（验证预读未丢失第一条）。
	var got []string
	for res.EventStream.Next() {
		got = append(got, string(res.EventStream.Current().Data))
	}
	if err := res.EventStream.Err(); err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if len(got) != 2 || got[0] != `{"delta":"hello"}` || got[1] != `{"delta":"world"}` {
		t.Fatalf("expected 2 events with deltas, got %v", got)
	}
}

// TestPipeline_EmptyResponse_DisabledByDefault 验证：未启用空流检测时，即便
// outbound 流产出 0 条 Response，pipeline 也会正常返回 stream（保持原行为）。
func TestPipeline_EmptyResponse_DisabledByDefault(t *testing.T) {
	in := &emptyTestInbound{}
	out := &emptyStreamOutbound{}
	p := NewFactory(newStreamExecutor(200)).Pipeline(in, out) // 不带 WithEmptyResponseDetection

	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	res, err := p.Process(context.Background(), req, []byte(`{"stream":true}`))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res == nil || !res.Stream {
		t.Fatalf("expected stream result, got %+v", res)
	}
	// 这里 inbound.TransformStream 应该被调用（即便底层流为空，也走完整链路）。
	if got := atomic.LoadInt32(&in.transformStreamCalled); got != 1 {
		t.Fatalf("expected inbound.TransformStream to be called once, got %d", got)
	}
}

// TestPipeline_EmptyResponse_RetriesOnEmpty 验证：空流检测错误进入 retry 分支，
// 切到下一个 channel 后能成功返回。
func TestPipeline_EmptyResponse_RetriesOnEmpty(t *testing.T) {
	in := &emptyTestInbound{}

	// retryingOutbound 第一次返回空流，第二次返回有内容流。
	out := &retryingEmptyOutbound{
		hasMore: true,
		responses: [][]*llm.Response{
			nil, // 第 1 次：空
			{{Format: llm.FormatOpenAIChat, Body: []byte(`{"delta":"ok"}`)}}, // 第 2 次：有
		},
	}

	p := NewFactory(newStreamExecutor(200)).Pipeline(in, out,
		WithRetry(3, 0, 0),
		WithEmptyResponseDetection(),
	)

	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	res, err := p.Process(context.Background(), req, []byte(`{"stream":true}`))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res == nil || !res.Stream {
		t.Fatalf("expected stream result, got %+v", res)
	}
	if got := atomic.LoadInt32(&out.channelSwitched); got != 1 {
		t.Fatalf("expected 1 channel switch, got %d", got)
	}
}

// retryingEmptyOutbound 模拟"第 N 次 attempt 返回 responses[N]"的 outbound。
type retryingEmptyOutbound struct {
	hasMore   bool
	responses [][]*llm.Response

	attempt         int32
	channelSwitched int32
}

func (o *retryingEmptyOutbound) TransformRequest(_ context.Context, _ *llm.Request) (*http.Request, []byte, error) {
	req, _ := http.NewRequest(http.MethodPost, "http://upstream.local/v1/chat/completions", strings.NewReader("{}"))
	return req, []byte("{}"), nil
}

func (o *retryingEmptyOutbound) TransformResponse(_ context.Context, _ *http.Response) (*llm.Response, error) {
	return &llm.Response{Format: llm.FormatOpenAIChat, Body: []byte("ok")}, nil
}

func (o *retryingEmptyOutbound) TransformStream(_ context.Context, _ *http.Response) llm.Stream[*llm.Response] {
	idx := int(atomic.AddInt32(&o.attempt, 1) - 1)
	var rs []*llm.Response
	if idx < len(o.responses) {
		rs = o.responses[idx]
	}
	ch := make(chan *llm.Response, len(rs)+1)
	for _, r := range rs {
		ch <- r
	}
	close(ch)
	return llm.NewChanStream(context.Background(), ch, nil)
}

func (o *retryingEmptyOutbound) HasMoreChannels() bool { return o.hasMore }

func (o *retryingEmptyOutbound) NextChannel(_ context.Context) error {
	atomic.AddInt32(&o.channelSwitched, 1)
	return nil
}
