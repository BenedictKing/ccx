package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

type testContextKey string

func newGinContext(method, url string, body []byte, ctx context.Context) *gin.Context {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	req := httptest.NewRequest(method, url, bytes.NewReader(body))
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	c.Request = req
	return c
}

func TestClaudeProvider_ConvertToProviderRequest_PassbackConvertsRealThinking(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{
		"model": "mimo-v2.5-pro",
		"messages": [
			{"role": "assistant", "content": [
				{"type": "thinking", "thinking": "real reasoning"},
				{"type": "text", "text": "answer"}
			]}
		]
	}`)
	c := newGinContext(http.MethodPost, "/v1/messages", body, context.Background())
	upstream := &config.UpstreamConfig{
		BaseURL:                  "https://api.example.com",
		ServiceType:              "claude",
		PassbackReasoningContent: true,
	}

	p := &ClaudeProvider{}
	_, reqBody, err := p.ConvertToProviderRequest(c, upstream, "sk-ant-test")
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}
	if !bytes.Contains(reqBody, []byte(`"reasoning_content":"real reasoning"`)) {
		t.Fatalf("request body missing reasoning_content: %s", string(reqBody))
	}
	if !bytes.Contains(reqBody, []byte(`"type":"thinking"`)) {
		t.Fatalf("request body should keep real thinking block for compatibility: %s", string(reqBody))
	}
}

func TestClaudeProvider_ConvertToProviderRequest_ReasoningStyleAutoPassback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{
		"model": "kimi-k2.6",
		"thinking": {"type": "enabled"},
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "read"}]},
			{"role": "assistant", "content": [
				{"type": "tool_use", "id": "toolu_1", "name": "Read", "input": {"file_path": "/tmp/a"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "toolu_1", "content": "ok"}
			]}
		]
	}`)
	c := newGinContext(http.MethodPost, "/v1/messages", body, context.Background())
	upstream := &config.UpstreamConfig{
		BaseURL:             "https://api.kimi.com/coding",
		ServiceType:         "claude",
		ReasoningParamStyle: "reasoning",
	}

	p := &ClaudeProvider{}
	_, reqBody, err := p.ConvertToProviderRequest(c, upstream, "sk-kimi-test")
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}
	if !bytes.Contains(reqBody, []byte(`"reasoning_content":"`)) {
		t.Fatalf("request body missing auto reasoning_content: %s", string(reqBody))
	}
	if !bytes.Contains(reqBody, []byte(`"type":"tool_use"`)) {
		t.Fatalf("request body should keep tool_use block: %s", string(reqBody))
	}
}

func TestConvertToProviderRequest_PropagatesContext(t *testing.T) {
	gin.SetMode(gin.TestMode)

	key := testContextKey("test-key")
	ctx := context.WithValue(context.Background(), key, "ok")

	t.Run("claude", func(t *testing.T) {
		c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{"model":"claude-3","messages":[]}`), ctx)
		upstream := &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "claude"}

		p := &ClaudeProvider{}
		req, _, err := p.ConvertToProviderRequest(c, upstream, "sk-ant-test")
		if err != nil {
			t.Fatalf("ConvertToProviderRequest() err = %v", err)
		}
		if got := req.Context().Value(key); got != "ok" {
			t.Fatalf("req.Context().Value(key) = %v, want %v", got, "ok")
		}
	})

	t.Run("openai", func(t *testing.T) {
		c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`), ctx)
		upstream := &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "openai"}

		p := &OpenAIProvider{}
		req, _, err := p.ConvertToProviderRequest(c, upstream, "sk-test")
		if err != nil {
			t.Fatalf("ConvertToProviderRequest() err = %v", err)
		}
		if got := req.Context().Value(key); got != "ok" {
			t.Fatalf("req.Context().Value(key) = %v, want %v", got, "ok")
		}
	})

	t.Run("gemini", func(t *testing.T) {
		c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{"model":"gemini-2.0-flash","messages":[{"role":"user","content":"hi"}]}`), ctx)
		upstream := &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "gemini"}

		p := &GeminiProvider{}
		req, _, err := p.ConvertToProviderRequest(c, upstream, "AIza-test")
		if err != nil {
			t.Fatalf("ConvertToProviderRequest() err = %v", err)
		}
		if got := req.Context().Value(key); got != "ok" {
			t.Fatalf("req.Context().Value(key) = %v, want %v", got, "ok")
		}
	})

	t.Run("responses", func(t *testing.T) {
		c := newGinContext(http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-4o","input":"hi"}`), ctx)
		upstream := &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "responses"}

		p := &ResponsesProvider{}
		req, _, err := p.ConvertToProviderRequest(c, upstream, "sk-test")
		if err != nil {
			t.Fatalf("ConvertToProviderRequest() err = %v", err)
		}
		if got := req.Context().Value(key); got != "ok" {
			t.Fatalf("req.Context().Value(key) = %v, want %v", got, "ok")
		}
	})
}

func TestConvertToProviderRequest_UsesUpdatedRequestBodyBytesContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"claude-3","messages":[],"metadata":{"user_id":"{\"device_id\":\"abc\"}"}}`)
	normalized := []byte(`{"model":"claude-3","messages":[],"metadata":{"user_id":"user_abc"}}`)
	c := newGinContext(http.MethodPost, "/v1/messages", body, context.Background())
	c.Set("requestBodyBytes", normalized)
	upstream := &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "claude"}

	p := &ClaudeProvider{}
	_, reqBody, err := p.ConvertToProviderRequest(c, upstream, "sk-ant-test")
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}
	if string(reqBody) != string(normalized) {
		t.Fatalf("request body = %s, want %s", string(reqBody), string(normalized))
	}
}

func TestOpenAIProvider_ConvertToProviderRequest_MapsMetadataUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"gpt-4o","metadata":{"user_id":"deepseek_user_123"},"messages":[{"role":"user","content":"hi"}]}`)
	c := newGinContext(http.MethodPost, "/v1/messages", body, context.Background())
	upstream := &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "openai"}

	p := &OpenAIProvider{}
	req, _, err := p.ConvertToProviderRequest(c, upstream, "sk-test")
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}

	var got map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if got["user_id"] != "deepseek_user_123" {
		t.Fatalf("user_id = %v, want deepseek_user_123", got["user_id"])
	}
}
