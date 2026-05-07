package messages

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/adapters"
	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

func newPlainClaudeUpstream() *config.UpstreamConfig {
	return &config.UpstreamConfig{
		ServiceType: "claude",
		Name:        "test-claude",
	}
}

func TestMessagesInbound_TransformRequest_ParsesModelStream(t *testing.T) {
	body := []byte(`{"model":"claude-3-5-sonnet-20241022","stream":true,"messages":[{"role":"user","content":"hi"}],"max_tokens":1024}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(body)))

	in := NewInbound()
	if got := in.Format(); got != llm.FormatClaudeMessages {
		t.Fatalf("Format mismatch: %q", got)
	}
	llmReq, err := in.TransformRequest(context.Background(), req, body)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}
	if llmReq.Model != "claude-3-5-sonnet-20241022" {
		t.Fatalf("model mismatch: %q", llmReq.Model)
	}
	if !llmReq.IsStream() {
		t.Fatal("expected stream=true")
	}
	v, ok := llmReq.Metadata[MetaParsedRequest]
	if !ok {
		t.Fatal("parsed metadata missing")
	}
	parsed, ok := v.(*types.ClaudeRequest)
	if !ok || parsed.Model != "claude-3-5-sonnet-20241022" {
		t.Fatalf("parsed shape mismatch: %+v", v)
	}
}

func TestMessagesInbound_TransformRequest_RejectsEmptyAndMissingModel(t *testing.T) {
	in := NewInbound()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	if _, err := in.TransformRequest(context.Background(), req, nil); err == nil {
		t.Fatal("expected error for empty body")
	}
	if _, err := in.TransformRequest(context.Background(), req, []byte(`{"messages":[]}`)); err == nil {
		t.Fatal("expected error for missing model")
	}
	if _, err := in.TransformRequest(context.Background(), req, []byte(`{"`)); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestMessagesInbound_TransformResponse_PassthroughBody(t *testing.T) {
	in := NewInbound()
	resp := &llm.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       []byte(`{"id":"msg_1","type":"message"}`),
	}
	body, hdr, err := in.TransformResponse(context.Background(), resp)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if string(body) != string(resp.Body) {
		t.Fatalf("body mismatch")
	}
	if hdr.Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type fallback missing")
	}
}

func TestMessagesInbound_TransformStream_ForwardsBytes(t *testing.T) {
	in := NewInbound()
	respCh := make(chan *llm.Response, 2)
	respCh <- &llm.Response{Body: []byte("event: message_start\ndata: {}\n\n")}
	respCh <- &llm.Response{Body: []byte("event: message_stop\ndata: {}\n\n")}
	close(respCh)
	upstream := llm.NewChanStream(context.Background(), respCh, nil)

	out := in.TransformStream(context.Background(), upstream)
	defer out.Close()

	var got []string
	for out.Next() {
		got = append(got, string(out.Current().Data))
	}
	if len(got) != 2 || !strings.Contains(got[0], "message_start") {
		t.Fatalf("unexpected events: %v", got)
	}
}

func TestMessagesOutbound_TransformRequest_BuildsViaProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(context.Background())

	body := []byte(`{"model":"claude-3-5-sonnet-20241022","stream":false,"messages":[{"role":"user","content":"hi"}],"max_tokens":1024}`)
	in := NewInbound()
	llmReq, err := in.TransformRequest(context.Background(), c.Request, body)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}
	adapters.SetGinContext(llmReq, c)
	adapters.SetUpstreamBinding(llmReq, newPlainClaudeUpstream(), "sk-test-123", "https://api.anthropic.example.com")

	out := NewOutbound()
	httpReq, realBody, err := out.TransformRequest(context.Background(), llmReq)
	if err != nil {
		t.Fatalf("outbound: %v", err)
	}
	if httpReq == nil {
		t.Fatal("nil http request")
	}
	if !strings.HasSuffix(httpReq.URL.String(), "/v1/messages") {
		t.Fatalf("expected /v1/messages URL, got %s", httpReq.URL.String())
	}
	// ClaudeProvider 走 AuthKindStandard；utils.SetAuthenticationHeader 根据
	// key 前缀智能选 x-api-key（Anthropic 格式）或 Authorization Bearer。
	// 测试用 sk-test-123（非 sk-ant- 前缀）期望走 Bearer 路径，避免依赖具体
	// 智能匹配规则。
	if got := httpReq.Header.Get("Authorization"); got != "Bearer sk-test-123" {
		t.Fatalf("expected Bearer auth header, got %q (full headers: %v)", got, httpReq.Header)
	}
	if len(realBody) == 0 {
		t.Fatal("realBody empty")
	}
}

func TestMessagesOutbound_TransformRequest_RejectsMissingMetadata(t *testing.T) {
	body := []byte(`{"model":"claude-3-5","messages":[],"max_tokens":1}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	in := NewInbound()
	llmReq, err := in.TransformRequest(context.Background(), req, body)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}

	out := NewOutbound()
	if _, _, err := out.TransformRequest(context.Background(), llmReq); err == nil {
		t.Fatal("expected error for missing gin context")
	}
}

func TestMessagesOutbound_TransformRequest_RejectsUnknownService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(context.Background())

	body := []byte(`{"model":"claude-3-5","messages":[],"max_tokens":1}`)
	in := NewInbound()
	llmReq, _ := in.TransformRequest(context.Background(), c.Request, body)
	adapters.SetGinContext(llmReq, c)
	adapters.SetUpstreamBinding(llmReq, &config.UpstreamConfig{ServiceType: "unknown-xyz"}, "sk", "https://x.local")

	out := NewOutbound()
	if _, _, err := out.TransformRequest(context.Background(), llmReq); err == nil {
		t.Fatal("expected error for unknown service type")
	}
}
