package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func TestBuildProviderRequest_InjectsReasoningBeforeModelRedirect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(context.Background())

	bodyBytes := []byte(`{"model":"gpt-5.1-codex","messages":[{"role":"user","content":"hi"}]}`)
	upstream := &config.UpstreamConfig{
		ServiceType: "openai",
		ModelMapping: map[string]string{
			"gpt-5.1-codex": "gpt-5.2-codex",
		},
		ReasoningMapping: map[string]string{
			"gpt-5.1-codex": "xhigh",
		},
		TextVerbosity: "low",
		FastMode:      true,
	}

	req, err := buildProviderRequest(c, upstream, "https://api.example.com", "sk-test", bodyBytes, "gpt-5.1-codex", false)
	if err != nil {
		t.Fatalf("buildProviderRequest() err = %v", err)
	}

	var got map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
		t.Fatalf("decode request body: %v", err)
	}

	if got["model"] != "gpt-5.2-codex" {
		t.Fatalf("model = %v, want gpt-5.2-codex", got["model"])
	}

	reasoning, ok := got["reasoning"].(map[string]interface{})
	if !ok || reasoning["effort"] != "xhigh" {
		t.Fatalf("reasoning = %#v, want effort=xhigh", got["reasoning"])
	}

	text, ok := got["text"].(map[string]interface{})
	if !ok || text["verbosity"] != "low" {
		t.Fatalf("text = %#v, want verbosity=low", got["text"])
	}

	if got["service_tier"] != "priority" {
		t.Fatalf("service_tier = %v, want priority", got["service_tier"])
	}
}

func TestBuildProviderRequest_OpenAISameFormatPreservesBodyWhenNoPlatformPatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(context.Background())

	bodyBytes := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}],"unknown":{"nested":1},"metadata":{"trace":"abc"}}`)
	upstream := &config.UpstreamConfig{
		ServiceType: "openai",
	}

	req, err := buildProviderRequest(c, upstream, "https://api.example.com", "sk-test", bodyBytes, "gpt-5", false)
	if err != nil {
		t.Fatalf("buildProviderRequest() err = %v", err)
	}

	got, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	if string(got) != string(bodyBytes) {
		t.Fatalf("request body = %s, want exact original body %s", got, bodyBytes)
	}
}

func TestBuildProviderRequest_CustomAuthorizationCannotOverrideSelectedKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(context.Background())

	bodyBytes := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`)
	upstream := &config.UpstreamConfig{
		ServiceType: "openai",
		CustomHeaders: map[string]string{
			"Authorization": "Bearer sk-custom",
			"X-Trace-ID":    "trace-1",
		},
	}

	req, err := buildProviderRequest(c, upstream, "https://api.example.com", "sk-selected", bodyBytes, "gpt-5", false)
	if err != nil {
		t.Fatalf("buildProviderRequest() err = %v", err)
	}

	if got := req.Header.Get("Authorization"); got != "Bearer sk-selected" {
		t.Fatalf("Authorization = %q, want selected key", got)
	}
	if got := req.Header.Get("X-Trace-ID"); got != "trace-1" {
		t.Fatalf("X-Trace-ID = %q, want custom metadata header", got)
	}
}

func TestBuildProviderRequest_ClaudeTargetUserAgentFallbackAndCustomOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)

	bodyBytes := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`)

	t.Run("missing user agent gets fallback", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(context.Background())

		req, err := buildProviderRequest(c, &config.UpstreamConfig{ServiceType: "claude"}, "https://api.example.com", "sk-selected", bodyBytes, "gpt-5", false)
		if err != nil {
			t.Fatalf("buildProviderRequest() err = %v", err)
		}
		if got := req.Header.Get("User-Agent"); got != "claude-cli/2.0.34 (external, cli)" {
			t.Fatalf("User-Agent = %q, want Claude fallback", got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer sk-selected" {
			t.Fatalf("Authorization = %q, want selected key", got)
		}
	})

	t.Run("custom user agent wins", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(context.Background())
		c.Request.Header.Set("User-Agent", "InboundUA/1.0")

		upstream := &config.UpstreamConfig{
			ServiceType: "claude",
			CustomHeaders: map[string]string{
				"User-Agent":    "ChatCustomUA/1.0",
				"Authorization": "Bearer custom",
			},
		}
		req, err := buildProviderRequest(c, upstream, "https://api.example.com", "sk-selected", bodyBytes, "gpt-5", false)
		if err != nil {
			t.Fatalf("buildProviderRequest() err = %v", err)
		}
		if got := req.Header.Get("User-Agent"); got != "ChatCustomUA/1.0" {
			t.Fatalf("User-Agent = %q, want custom UA", got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer sk-selected" {
			t.Fatalf("Authorization = %q, want selected key", got)
		}
	})
}
