package responses

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

func newPlainResponsesUpstream() *config.UpstreamConfig {
	return &config.UpstreamConfig{
		ServiceType: "responses",
		Name:        "test-responses",
	}
}

func TestResponsesInbound_TransformRequest_ParsesModelStream(t *testing.T) {
	body := []byte(`{"model":"gpt-5","stream":true,"input":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))

	in := NewInbound()
	if got := in.Format(); got != llm.FormatOpenAIResponses {
		t.Fatalf("Format mismatch: %q", got)
	}
	llmReq, err := in.TransformRequest(context.Background(), req, body)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}
	if llmReq.Model != "gpt-5" {
		t.Fatalf("model mismatch: %q", llmReq.Model)
	}
	if !llmReq.IsStream() {
		t.Fatal("expected stream=true")
	}
	v, ok := llmReq.Metadata[MetaParsedRequest]
	if !ok {
		t.Fatal("parsed metadata missing")
	}
	parsed, ok := v.(*types.ResponsesRequest)
	if !ok || parsed.Model != "gpt-5" {
		t.Fatalf("parsed shape mismatch: %+v", v)
	}
}

func TestResponsesInbound_TransformRequest_RejectsEmptyAndMissingModel(t *testing.T) {
	in := NewInbound()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	if _, err := in.TransformRequest(context.Background(), req, nil); err == nil {
		t.Fatal("expected error for empty body")
	}
	if _, err := in.TransformRequest(context.Background(), req, []byte(`{"input":""}`)); err == nil {
		t.Fatal("expected error for missing model")
	}
	if _, err := in.TransformRequest(context.Background(), req, []byte(`{"`)); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestResponsesInbound_TransformResponse_PassthroughBody(t *testing.T) {
	in := NewInbound()
	resp := &llm.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       []byte(`{"id":"resp_1","object":"response"}`),
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

func TestResponsesInbound_TransformStream_ForwardsBytes(t *testing.T) {
	in := NewInbound()
	respCh := make(chan *llm.Response, 2)
	respCh <- &llm.Response{Body: []byte("event: response.created\ndata: {}\n\n")}
	respCh <- &llm.Response{Body: []byte("event: response.completed\ndata: {}\n\n")}
	close(respCh)
	upstream := llm.NewChanStream(context.Background(), respCh, nil)

	out := in.TransformStream(context.Background(), upstream)
	defer out.Close()

	var got []string
	for out.Next() {
		got = append(got, string(out.Current().Data))
	}
	if len(got) != 2 || !strings.Contains(got[0], "response.created") {
		t.Fatalf("unexpected events: %v", got)
	}
}

func TestResponsesOutbound_TransformRequest_BuildsViaProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(context.Background())

	body := []byte(`{"model":"gpt-5","stream":false,"input":"hi"}`)
	in := NewInbound()
	llmReq, err := in.TransformRequest(context.Background(), c.Request, body)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}
	adapters.SetGinContext(llmReq, c)
	adapters.SetUpstreamBinding(llmReq, newPlainResponsesUpstream(), "sk-test-456", "https://api.openai.example.com")

	out := NewOutbound(nil) // SessionManager nil → provider 自动初始化
	httpReq, realBody, err := out.TransformRequest(context.Background(), llmReq)
	if err != nil {
		t.Fatalf("outbound: %v", err)
	}
	if httpReq == nil {
		t.Fatal("nil http request")
	}
	if !strings.Contains(httpReq.URL.String(), "/responses") {
		t.Fatalf("expected /responses URL, got %s", httpReq.URL.String())
	}
	if got := httpReq.Header.Get("Authorization"); got != "Bearer sk-test-456" {
		t.Fatalf("expected Bearer auth, got %q", got)
	}
	if len(realBody) == 0 {
		t.Fatal("realBody empty")
	}
}

func TestResponsesOutbound_TransformRequest_RejectsMissingMetadata(t *testing.T) {
	body := []byte(`{"model":"gpt-5","input":"hi"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	in := NewInbound()
	llmReq, err := in.TransformRequest(context.Background(), req, body)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}

	out := NewOutbound(nil)
	if _, _, err := out.TransformRequest(context.Background(), llmReq); err == nil {
		t.Fatal("expected error for missing gin context")
	}
}
