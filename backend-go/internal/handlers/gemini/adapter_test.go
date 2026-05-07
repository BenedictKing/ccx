package gemini

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

func newPlainGeminiUpstream() *config.UpstreamConfig {
	return &config.UpstreamConfig{
		ServiceType: "gemini",
		Name:        "test-gemini",
	}
}

func TestGeminiInbound_TransformRequest_ParsesPathAndBody(t *testing.T) {
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	req := httptest.NewRequest(http.MethodPost,
		"/v1beta/models/gemini-2.0-flash:generateContent",
		strings.NewReader(string(body)),
	)

	in := NewInbound()
	if got := in.Format(); got != llm.FormatGeminiContents {
		t.Fatalf("Format mismatch: %q", got)
	}
	llmReq, err := in.TransformRequest(context.Background(), req, body)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}
	if llmReq.Model != "gemini-2.0-flash" {
		t.Fatalf("model mismatch: %q", llmReq.Model)
	}
	if llmReq.IsStream() {
		t.Fatal("expected non-stream for generateContent")
	}
	parsed, err := loadParsedRequest(llmReq)
	if err != nil {
		t.Fatalf("parsed metadata missing: %v", err)
	}
	if parsed == nil || len(parsed.Contents) != 1 {
		t.Fatalf("parsed body shape unexpected: %+v", parsed)
	}
}

func TestGeminiInbound_StreamGenerateContent_FlagsStream(t *testing.T) {
	body := []byte(`{"contents":[]}`)
	req := httptest.NewRequest(http.MethodPost,
		"/v1beta/models/gemini-1.5-pro:streamGenerateContent",
		strings.NewReader(string(body)),
	)
	in := NewInbound()
	llmReq, err := in.TransformRequest(context.Background(), req, body)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}
	if !llmReq.IsStream() {
		t.Fatal("expected stream=true for streamGenerateContent")
	}
	if llmReq.Model != "gemini-1.5-pro" {
		t.Fatalf("model mismatch: %q", llmReq.Model)
	}
}

func TestGeminiInbound_RejectsInvalidPaths(t *testing.T) {
	in := NewInbound()
	cases := []string{
		"/no-models-prefix",
		"/v1beta/models/:generateContent",
	}
	for _, p := range cases {
		req := httptest.NewRequest(http.MethodPost, p, nil)
		if _, err := in.TransformRequest(context.Background(), req, []byte(`{}`)); err == nil {
			t.Fatalf("expected error for path %q", p)
		}
	}
}

func TestGeminiOutbound_TransformRequest_BuildsURLAndAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	path := "/v1beta/models/gemini-2.0-flash:generateContent"
	c.Request = httptest.NewRequest(http.MethodPost, path, nil).WithContext(context.Background())

	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	in := NewInbound()
	llmReq, err := in.TransformRequest(context.Background(), c.Request, body)
	if err != nil {
		t.Fatalf("inbound: %v", err)
	}
	adapters.SetGinContext(llmReq, c)
	adapters.SetUpstreamBinding(llmReq, newPlainGeminiUpstream(), "AIza-test", "https://generativelanguage.example.com")

	out := NewOutbound()
	httpReq, _, err := out.TransformRequest(context.Background(), llmReq)
	if err != nil {
		t.Fatalf("outbound: %v", err)
	}
	url := httpReq.URL.String()
	if !strings.Contains(url, "/models/gemini-2.0-flash") {
		t.Fatalf("URL should contain mapped model path, got: %s", url)
	}
	// AuthKindGemini 通过 query string 或 x-goog-api-key 传 key；不强 assert 具体形态，
	// 只确认 apiKey 被注入到请求中。
	hasKeyInURL := strings.Contains(url, "AIza-test")
	hasKeyInHeader := httpReq.Header.Get("x-goog-api-key") == "AIza-test" ||
		httpReq.Header.Get("X-Goog-Api-Key") == "AIza-test"
	if !hasKeyInURL && !hasKeyInHeader {
		t.Fatalf("API key not injected: url=%s headers=%v", url, httpReq.Header)
	}
}

func TestGeminiOutbound_TransformRequest_RejectsMissingMetadata(t *testing.T) {
	body := []byte(`{"contents":[]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-pro:generateContent", nil)
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

func TestGeminiOutbound_LoadParsedRequest_RejectsTypeMismatch(t *testing.T) {
	req := &llm.Request{Metadata: map[string]any{MetaParsedRequest: "not-a-gemini-request"}}
	if _, err := loadParsedRequest(req); err == nil {
		t.Fatal("expected error for type mismatch")
	}
	req2 := &llm.Request{Metadata: map[string]any{MetaParsedRequest: (*types.GeminiRequest)(nil)}}
	if _, err := loadParsedRequest(req2); err == nil {
		t.Fatal("expected error for nil parsed request")
	}
}

func TestGeminiInbound_TransformResponse_AddsContentTypeIfMissing(t *testing.T) {
	in := NewInbound()
	resp := &llm.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       []byte(`{"candidates":[]}`),
	}
	body, hdr, err := in.TransformResponse(context.Background(), resp)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if string(body) != string(resp.Body) {
		t.Fatalf("body mismatch")
	}
	if hdr.Get("Content-Type") != "application/json" {
		t.Fatalf("missing Content-Type fallback")
	}
}

func TestGeminiInbound_TransformStream_ForwardsBytes(t *testing.T) {
	in := NewInbound()
	respCh := make(chan *llm.Response, 2)
	respCh <- &llm.Response{Body: []byte("data: {\"text\":\"a\"}\n\n")}
	respCh <- &llm.Response{Body: []byte("data: {\"text\":\"b\"}\n\n")}
	close(respCh)
	upstream := llm.NewChanStream(context.Background(), respCh, nil)

	out := in.TransformStream(context.Background(), upstream)
	defer out.Close()

	var got []string
	for out.Next() {
		got = append(got, string(out.Current().Data))
	}
	if len(got) != 2 || got[0] != "data: {\"text\":\"a\"}\n\n" {
		t.Fatalf("unexpected events: %v", got)
	}
}
