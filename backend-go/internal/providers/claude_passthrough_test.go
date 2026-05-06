package providers

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func TestClaudeProvider_CustomAuthHeadersCannotOverrideSelectedKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	requestBody := []byte(`{"model":"claude-3-5-sonnet-latest","messages":[{"role":"user","content":"hi"}]}`)
	c := newGinContext(http.MethodPost, "/v1/messages", requestBody, context.Background())

	upstream := &config.UpstreamConfig{
		BaseURL:     "https://api.anthropic.com",
		ServiceType: "claude",
		CustomHeaders: map[string]string{
			"Authorization": "Bearer custom-token",
			"x-api-key":     "custom-api-key",
		},
	}

	p := &ClaudeProvider{}
	req, _, err := p.ConvertToProviderRequest(c, upstream, "sk-selected")
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}

	if got := req.Header.Get("Authorization"); got != "Bearer sk-selected" {
		t.Fatalf("Authorization = %q, want selected key bearer", got)
	}
	if got := req.Header.Get("x-api-key"); got != "" {
		t.Fatalf("x-api-key should be cleared for non-Anthropic selected key, got %q", got)
	}
}

func TestProviderCustomAuthHeadersCannotOverrideSelectedKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	claudeBody := []byte(`{"model":"claude-client","messages":[{"role":"user","content":"hi"}]}`)
	responsesBody := []byte(`{"model":"gpt-client","input":"hi"}`)

	tests := []struct {
		name       string
		body       []byte
		path       string
		upstream   *config.UpstreamConfig
		apiKey     string
		build      func(*gin.Context, *config.UpstreamConfig, string) (*http.Request, []byte, error)
		wantAuth   string
		wantAPIKey string
		wantGoog   string
	}{
		{
			name: "openai bearer wins",
			body: claudeBody,
			path: "/v1/messages",
			upstream: &config.UpstreamConfig{
				BaseURL:     "https://api.openai.com",
				ServiceType: "openai",
				CustomHeaders: map[string]string{
					"Authorization":  "Bearer custom-token",
					"x-api-key":      "custom-api-key",
					"x-goog-api-key": "custom-goog-key",
				},
			},
			apiKey:   "sk-selected",
			build:    (&OpenAIProvider{}).ConvertToProviderRequest,
			wantAuth: "Bearer sk-selected",
		},
		{
			name: "gemini google key wins",
			body: claudeBody,
			path: "/v1/messages",
			upstream: &config.UpstreamConfig{
				BaseURL:     "https://generativelanguage.googleapis.com",
				ServiceType: "gemini",
				CustomHeaders: map[string]string{
					"Authorization":  "Bearer custom-token",
					"x-api-key":      "custom-api-key",
					"x-goog-api-key": "custom-goog-key",
				},
			},
			apiKey:   "gemini-selected",
			build:    (&GeminiProvider{}).ConvertToProviderRequest,
			wantGoog: "gemini-selected",
		},
		{
			name: "responses bearer wins",
			body: responsesBody,
			path: "/v1/responses",
			upstream: &config.UpstreamConfig{
				BaseURL:     "https://api.openai.com",
				ServiceType: "responses",
				CustomHeaders: map[string]string{
					"Authorization":  "Bearer custom-token",
					"x-api-key":      "custom-api-key",
					"x-goog-api-key": "custom-goog-key",
				},
			},
			apiKey:   "sk-selected",
			build:    (&ResponsesProvider{}).ConvertToProviderRequest,
			wantAuth: "Bearer sk-selected",
		},
		{
			name: "responses to gemini google key wins",
			body: responsesBody,
			path: "/v1/responses",
			upstream: &config.UpstreamConfig{
				BaseURL:     "https://generativelanguage.googleapis.com",
				ServiceType: "gemini",
				CustomHeaders: map[string]string{
					"Authorization":  "Bearer custom-token",
					"x-api-key":      "custom-api-key",
					"x-goog-api-key": "custom-goog-key",
				},
			},
			apiKey:   "gemini-selected",
			build:    (&ResponsesProvider{}).ConvertToProviderRequest,
			wantGoog: "gemini-selected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newGinContext(http.MethodPost, tt.path, tt.body, context.Background())
			req, _, err := tt.build(c, tt.upstream, tt.apiKey)
			if err != nil {
				t.Fatalf("ConvertToProviderRequest() err = %v", err)
			}
			if got := req.Header.Get("Authorization"); got != tt.wantAuth {
				t.Fatalf("Authorization = %q, want %q", got, tt.wantAuth)
			}
			if got := req.Header.Get("x-api-key"); got != tt.wantAPIKey {
				t.Fatalf("x-api-key = %q, want %q", got, tt.wantAPIKey)
			}
			if got := req.Header.Get("x-goog-api-key"); got != tt.wantGoog {
				t.Fatalf("x-goog-api-key = %q, want %q", got, tt.wantGoog)
			}
		})
	}
}

func TestClaudeProvider_StrictPassthrough_PreservesUnknownFieldsAndPatchesModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	requestBody := []byte(`{"model":"claude-client","messages":[{"role":"user","content":"hi"}],"unknown":{"nested":1},"temperature":0.7}`)
	c := newGinContext(http.MethodPost, "/v1/messages", requestBody, context.Background())
	upstream := &config.UpstreamConfig{
		BaseURL:      "https://api.anthropic.com",
		ServiceType:  "claude",
		ModelMapping: map[string]string{"claude-client": "claude-target"},
	}

	p := &ClaudeProvider{}
	req, forwardedBody, err := p.ConvertToProviderRequest(c, upstream, "sk-ant-test")
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read req body: %v", err)
	}

	for _, body := range [][]byte{forwardedBody, bodyBytes} {
		if got := gjson.GetBytes(body, "model").String(); got != "claude-target" {
			t.Fatalf("model = %q, want claude-target; body=%s", got, body)
		}
		if got := gjson.GetBytes(body, "unknown.nested").Int(); got != 1 {
			t.Fatalf("unknown.nested = %d, want 1; body=%s", got, body)
		}
		if got := gjson.GetBytes(body, "temperature").Float(); got != 0.7 {
			t.Fatalf("temperature = %v, want 0.7; body=%s", got, body)
		}
	}
}

func TestProviderRequestsStripSensitiveInboundHeadersAndPreserveUserAgent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{"model":"claude-client","messages":[{"role":"user","content":"hi"}]}`), context.Background())
	c.Request.Header.Set("Authorization", "Bearer inbound")
	c.Request.Header.Set("x-api-key", "inbound-api")
	c.Request.Header.Set("x-goog-api-key", "inbound-goog")
	c.Request.Header.Set("Cookie", "sid=secret")
	c.Request.Header.Set("Proxy-Authorization", "Basic secret")
	c.Request.Header.Set("User-Agent", "Client/1.0")
	c.Request.Header.Set("X-Trace-ID", "inbound-trace")

	upstream := &config.UpstreamConfig{
		BaseURL:     "https://api.openai.com",
		ServiceType: "openai",
	}

	req, _, err := (&OpenAIProvider{}).ConvertToProviderRequest(c, upstream, "sk-selected")
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}

	if got := req.Header.Get("Authorization"); got != "Bearer sk-selected" {
		t.Fatalf("Authorization = %q, want selected key", got)
	}
	for _, header := range []string{"x-api-key", "x-goog-api-key", "Cookie", "Proxy-Authorization"} {
		if got := req.Header.Get(header); got != "" {
			t.Fatalf("%s = %q, want stripped", header, got)
		}
	}
	if got := req.Header.Get("User-Agent"); got != "Client/1.0" {
		t.Fatalf("User-Agent = %q, want inbound UA", got)
	}
	if got := req.Header.Get("X-Trace-ID"); got != "inbound-trace" {
		t.Fatalf("X-Trace-ID = %q, want preserved metadata", got)
	}
}

func TestClaudeProviderUserAgentFallbackAndCustomOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"claude-client","messages":[{"role":"user","content":"hi"}]}`)

	t.Run("missing user agent gets claude fallback", func(t *testing.T) {
		c := newGinContext(http.MethodPost, "/v1/messages", body, context.Background())
		upstream := &config.UpstreamConfig{
			BaseURL:     "https://api.anthropic.com",
			ServiceType: "claude",
		}

		req, _, err := (&ClaudeProvider{}).ConvertToProviderRequest(c, upstream, "sk-selected")
		if err != nil {
			t.Fatalf("ConvertToProviderRequest() err = %v", err)
		}
		if got := req.Header.Get("User-Agent"); got != "claude-cli/2.0.34 (external, cli)" {
			t.Fatalf("User-Agent = %q, want Claude fallback", got)
		}
	})

	t.Run("custom user agent overrides inbound user agent", func(t *testing.T) {
		c := newGinContext(http.MethodPost, "/v1/messages", body, context.Background())
		c.Request.Header.Set("User-Agent", "Inbound/1.0")
		upstream := &config.UpstreamConfig{
			BaseURL:     "https://api.anthropic.com",
			ServiceType: "claude",
			CustomHeaders: map[string]string{
				"User-Agent": "CustomUA/2.0",
			},
		}

		req, _, err := (&ClaudeProvider{}).ConvertToProviderRequest(c, upstream, "sk-selected")
		if err != nil {
			t.Fatalf("ConvertToProviderRequest() err = %v", err)
		}
		if got := req.Header.Get("User-Agent"); got != "CustomUA/2.0" {
			t.Fatalf("User-Agent = %q, want custom UA", got)
		}
	})
}
