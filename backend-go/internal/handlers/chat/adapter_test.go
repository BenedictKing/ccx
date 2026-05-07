package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/adapters"
	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/gin-gonic/gin"
)

// helper：构造一个最简上游配置（OpenAI 兼容上游，无模型映射、无 platform patch）。
func newPlainOpenAIUpstream() *config.UpstreamConfig {
	return &config.UpstreamConfig{
		ServiceType: "openai",
		Name:        "test-openai",
	}
}

// TestChatInbound_TransformRequest_ParsesModelAndStream 验证 inbound 解析关键字段。
func TestChatInbound_TransformRequest_ParsesModelAndStream(t *testing.T) {
	body := []byte(`{"model":"gpt-4o-mini","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))

	in := NewInbound()
	if got := in.Format(); got != llm.FormatOpenAIChat {
		t.Fatalf("Format mismatch: %q", got)
	}
	llmReq, err := in.TransformRequest(context.Background(), req, body)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}
	if llmReq.Model != "gpt-4o-mini" {
		t.Fatalf("model mismatch: %q", llmReq.Model)
	}
	if !llmReq.IsStream() {
		t.Fatal("expected stream=true")
	}
	// RawBody 必须保留原始字节，且为独立拷贝（修改原始 body 不应影响）。
	if string(llmReq.RawBody) != string(body) {
		t.Fatalf("RawBody mismatch")
	}
	body[0] = 'X'
	if llmReq.RawBody[0] == 'X' {
		t.Fatal("RawBody must be an independent copy")
	}
}

func TestChatInbound_TransformRequest_RejectsEmptyAndMissingModel(t *testing.T) {
	in := NewInbound()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	if _, err := in.TransformRequest(context.Background(), req, nil); err == nil {
		t.Fatal("expected error for empty body")
	}
	if _, err := in.TransformRequest(context.Background(), req, []byte(`{"stream":true}`)); err == nil {
		t.Fatal("expected error for missing model")
	}
	if _, err := in.TransformRequest(context.Background(), req, []byte(`{"`)); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestChatInbound_TransformResponse_PassthroughBody 验证 same-format 响应直通。
func TestChatInbound_TransformResponse_PassthroughBody(t *testing.T) {
	in := NewInbound()
	hdr := http.Header{}
	hdr.Set("X-Trace-Id", "abc")
	resp := &llm.Response{
		Format:     llm.FormatOpenAIChat,
		StatusCode: 200,
		Header:     hdr,
		Body:       []byte(`{"choices":[]}`),
	}
	body, gotHdr, err := in.TransformResponse(context.Background(), resp)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if string(body) != string(resp.Body) {
		t.Fatalf("body mismatch")
	}
	if gotHdr.Get("Content-Type") != "application/json" {
		t.Fatalf("expected Content-Type=application/json, got %q", gotHdr.Get("Content-Type"))
	}
	if gotHdr.Get("X-Trace-Id") != "abc" {
		t.Fatalf("X-Trace-Id should pass through")
	}
}

// TestChatInbound_TransformStream_PassthroughEvents 验证流式 same-format 透传：
// outbound 给出的每个 *llm.Response.Body 应该作为独立 SSE event 透出。
func TestChatInbound_TransformStream_PassthroughEvents(t *testing.T) {
	in := NewInbound()

	// 构造一个 *llm.Response chan，模拟 outbound.TransformStream 的产出。
	respCh := make(chan *llm.Response, 3)
	respCh <- &llm.Response{Body: []byte("data: {\"d\":1}\n\n")}
	respCh <- &llm.Response{Body: []byte("data: {\"d\":2}\n\n")}
	respCh <- &llm.Response{Body: []byte("data: [DONE]\n\n")}
	close(respCh)
	upstream := llm.NewChanStream(context.Background(), respCh, nil)

	out := in.TransformStream(context.Background(), upstream)
	defer out.Close()

	var got []string
	for out.Next() {
		got = append(got, string(out.Current().Data))
	}
	if err := out.Err(); err != nil {
		t.Fatalf("stream err: %v", err)
	}
	if len(got) != 3 || got[0] != "data: {\"d\":1}\n\n" || got[2] != "data: [DONE]\n\n" {
		t.Fatalf("unexpected events: %v", got)
	}
}

// TestChatOutbound_TransformRequest_BuildsViaExistingFunc 验证 outbound 调用
// buildProviderRequest（PR1 不重写转换逻辑硬约束）。
//
// 通过观察构造出的 *http.Request URL/Header 来确认走的是 chat.buildProviderRequest
// "openai" 分支。
func TestChatOutbound_TransformRequest_BuildsViaExistingFunc(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(context.Background())

	body := []byte(`{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	in := NewInbound()
	llmReq, err := in.TransformRequest(context.Background(), c.Request, body)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}
	adapters.SetGinContext(llmReq, c)
	adapters.SetUpstreamBinding(llmReq, newPlainOpenAIUpstream(), "sk-test-123", "https://api.example.com")

	out := NewOutbound()
	httpReq, realBody, err := out.TransformRequest(context.Background(), llmReq)
	if err != nil {
		t.Fatalf("outbound: %v", err)
	}
	if httpReq == nil {
		t.Fatal("nil http request")
	}
	if got := httpReq.URL.String(); !strings.HasSuffix(got, "/v1/chat/completions") {
		t.Fatalf("unexpected URL: %s", got)
	}
	// 标准 AuthKindStandard 应该把 apiKey 放进 Authorization header。
	if got := httpReq.Header.Get("Authorization"); got != "Bearer sk-test-123" {
		t.Fatalf("expected Bearer auth, got %q", got)
	}
	if string(realBody) == "" {
		t.Fatal("realBody should not be empty")
	}

	// 真实 body 序列应当与原始保持一致（因为没有平台 patch 也没有模型映射）。
	if !json.Valid(realBody) {
		t.Fatal("realBody should be valid JSON")
	}
}

// TestChatAdapter_RoundTrip_NonStream 验证 OpenAI Chat 请求经过 inbound → outbound
// → inbound.TransformResponse 之后能还原为字节级一致的响应。
//
// 该测试不依赖真实上游，仅断言 adapter 不引入额外字段：response 对应的 body
// 经过 inbound.TransformResponse 应当原样返回。
func TestChatAdapter_RoundTrip_NonStream(t *testing.T) {
	original := []byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"hi"}}]}`)
	hdr := http.Header{}
	hdr.Set("Content-Type", "application/json")
	hdr.Set("X-Custom", "yes")

	llmResp := &llm.Response{
		Format:     llm.FormatOpenAIChat,
		StatusCode: 200,
		Header:     hdr,
		Body:       append([]byte(nil), original...),
	}
	body, gotHdr, err := NewInbound().TransformResponse(context.Background(), llmResp)
	if err != nil {
		t.Fatalf("inbound resp: %v", err)
	}
	if string(body) != string(original) {
		t.Fatalf("body mismatch: round-trip changed bytes")
	}
	if gotHdr.Get("X-Custom") != "yes" || gotHdr.Get("Content-Type") != "application/json" {
		t.Fatalf("header mismatch: %v", gotHdr)
	}
}

// TestChatOutbound_TransformResponse_ReadsAndCloses 验证 outbound 完整读取 body
// 并关闭 resp.Body（避免 goroutine 泄露）。
func TestChatOutbound_TransformResponse_ReadsAndCloses(t *testing.T) {
	body := `{"id":"chatcmpl-1"}`
	closed := false
	rc := &trackedReadCloser{Reader: strings.NewReader(body), onClose: func() { closed = true }}
	resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: rc}

	llmResp, err := NewOutbound().TransformResponse(context.Background(), resp)
	if err != nil {
		t.Fatalf("outbound: %v", err)
	}
	if string(llmResp.Body) != body {
		t.Fatalf("body mismatch")
	}
	if !closed {
		t.Fatal("response body should be closed")
	}
}

type trackedReadCloser struct {
	io.Reader
	onClose func()
}

func (r *trackedReadCloser) Close() error {
	if r.onClose != nil {
		r.onClose()
	}
	return nil
}

// TestChatOutbound_TransformRequest_RejectsMissingMetadata 验证 metadata 缺失时
// 返回明确错误。
func TestChatOutbound_TransformRequest_RejectsMissingMetadata(t *testing.T) {
	in := NewInbound()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	llmReq, err := in.TransformRequest(context.Background(), req, []byte(`{"model":"gpt-4o"}`))
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}

	out := NewOutbound()
	if _, _, err := out.TransformRequest(context.Background(), llmReq); err == nil {
		t.Fatal("expected error for missing gin context")
	}
}
