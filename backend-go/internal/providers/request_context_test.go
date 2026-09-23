package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/thinkingcache"
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
		BaseURL:     "https://api.example.com",
		ServiceType: "claude",
	}
	upstream.SetLearnedCompatTrait(config.TraitPassbackReasoningContent, true)

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

func TestClaudeProvider_ConvertToProviderRequest_InjectsCachedThinkingForDeepSeek(t *testing.T) {
	gin.SetMode(gin.TestMode)
	thinkingcache.ResetForTest()

	content := []interface{}{map[string]interface{}{
		"type":  "tool_use",
		"id":    "toolu_123",
		"name":  "Bash",
		"input": map[string]interface{}{"command": "ls"},
	}}
	if !thinkingcache.StoreClaudeThinkingForContent("session-1", content, "cached reasoning") {
		t.Fatal("expected thinking cache store to succeed")
	}

	body := []byte(`{
		"model": "deepseek-v4-pro",
		"thinking": {"type": "adaptive"},
		"messages": [
			{"role": "assistant", "content": [
				{"type": "tool_use", "id": "toolu_123", "name": "Bash", "input": {"command": "ls"}}
			]}
		]
	}`)
	c := newGinContext(http.MethodPost, "/v1/messages", body, context.Background())
	c.Request.Header.Set("X-Claude-Code-Session-Id", "session-1")
	upstream := &config.UpstreamConfig{
		BaseURL:     "https://api.deepseek.com",
		ServiceType: "claude",
	}

	p := &ClaudeProvider{}
	_, reqBody, err := p.ConvertToProviderRequest(c, upstream, "sk-ant-test")
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}
	if !bytes.Contains(reqBody, []byte(`"type":"thinking"`)) || !bytes.Contains(reqBody, []byte(`"thinking":"cached reasoning"`)) {
		t.Fatalf("request body missing cached thinking block: %s", string(reqBody))
	}
}

func TestClaudeProvider_ConvertToProviderRequest_StripsDeepSeekClearThinkingContextManagement(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{
		"model": "deepseek-v4-pro",
		"thinking": {"type": "adaptive"},
		"context_management": {"edits": [{"keep": "all", "type": "clear_thinking_20251015"}]},
		"messages": [{"role": "user", "content": [{"type": "text", "text": "hello"}]}]
	}`)
	c := newGinContext(http.MethodPost, "/v1/messages", body, context.Background())
	c.Request.Header.Set("Anthropic-Beta", "claude-code-20250219,context-management-2025-06-27,effort-2025-11-24")
	upstream := &config.UpstreamConfig{
		BaseURL:     "https://api.deepseek.com/anthropic",
		ServiceType: "claude",
	}

	p := &ClaudeProvider{}
	req, reqBody, err := p.ConvertToProviderRequest(c, upstream, "sk-ant-test")
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}
	if bytes.Contains(reqBody, []byte(`clear_thinking_20251015`)) {
		t.Fatalf("request body should strip unsupported clear_thinking edit: %s", string(reqBody))
	}
	if bytes.Contains(reqBody, []byte(`context_management`)) {
		t.Fatalf("empty context_management should be removed: %s", string(reqBody))
	}
	if !bytes.Contains(reqBody, []byte(`"thinking":{"type":"adaptive"}`)) {
		t.Fatalf("request body should keep thinking request: %s", string(reqBody))
	}
	if got := req.Header.Get("Anthropic-Beta"); strings.Contains(got, "context-management-2025-06-27") {
		t.Fatalf("Anthropic-Beta should strip unsupported context-management token, got %q", got)
	}
	if got := req.Header.Get("Anthropic-Beta"); !strings.Contains(got, "claude-code-20250219") || !strings.Contains(got, "effort-2025-11-24") {
		t.Fatalf("Anthropic-Beta should keep other tokens, got %q", got)
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
