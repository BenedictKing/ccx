package forwarding

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestBuildConstructsRequestAndStripsSensitiveInboundHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c := newTestContext("POST", "/v1/messages", map[string]string{
		"Authorization":       "Bearer inbound",
		"x-api-key":           "inbound-api-key",
		"x-goog-api-key":      "inbound-goog-key",
		"Cookie":              "sid=secret",
		"Set-Cookie":          "sid=secret",
		"Proxy-Authorization": "Basic secret",
		"Connection":          "Keep-Alive, X-Connection-Scoped",
		"X-Connection-Scoped": "strip-me",
		"Keep-Alive":          "timeout=5",
		"Accept-Encoding":     "gzip",
		"X-Forwarded-For":     "127.0.0.1",
		"User-Agent":          "Client/1.0",
		"Accept":              "application/json",
		"X-Trace-ID":          "trace-1",
	})

	prepared, err := Build(c, ForwardingRequest{
		Method:      "PATCH",
		URL:         "https://upstream.example/v1/messages?debug=1",
		Body:        []byte(`{"model":"claude"}`),
		ContentType: "application/custom+json",
		ServiceType: "openai",
		APIKey:      "sk-selected",
		RawResponse: true,
		RawStream:   true,
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	req := prepared.Request
	if req.Method != "PATCH" {
		t.Fatalf("method = %q, want PATCH", req.Method)
	}
	if req.URL.String() != "https://upstream.example/v1/messages?debug=1" {
		t.Fatalf("URL = %q", req.URL.String())
	}
	if req.Header.Get("Host") != "upstream.example" {
		t.Fatalf("Host = %q, want upstream.example", req.Header.Get("Host"))
	}
	if req.Header.Get("Content-Type") != "application/custom+json" {
		t.Fatalf("Content-Type = %q, want custom content type", req.Header.Get("Content-Type"))
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != `{"model":"claude"}` {
		t.Fatalf("body = %q", string(body))
	}
	if string(prepared.Body) != `{"model":"claude"}` {
		t.Fatalf("prepared body = %q", string(prepared.Body))
	}
	if !prepared.RawResponse || !prepared.RawStream {
		t.Fatalf("raw strategy = (%v, %v), want both true", prepared.RawResponse, prepared.RawStream)
	}

	for _, header := range []string{
		"x-api-key",
		"x-goog-api-key",
		"Cookie",
		"Set-Cookie",
		"Proxy-Authorization",
		"Connection",
		"X-Connection-Scoped",
		"Keep-Alive",
		"Accept-Encoding",
		"X-Forwarded-For",
	} {
		if got := req.Header.Get(header); got != "" {
			t.Fatalf("%s = %q, want stripped", header, got)
		}
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sk-selected" {
		t.Fatalf("Authorization = %q, want selected key", got)
	}
	if got := req.Header.Get("User-Agent"); got != "Client/1.0" {
		t.Fatalf("User-Agent = %q, want inbound passthrough", got)
	}
	if got := req.Header.Get("Accept"); got != "application/json" {
		t.Fatalf("Accept = %q, want passthrough", got)
	}
	if got := req.Header.Get("X-Trace-ID"); got != "trace-1" {
		t.Fatalf("X-Trace-ID = %q, want passthrough", got)
	}
}

func TestBuildCustomAuthLikeHeadersCannotOverrideSelectedStandardKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c := newTestContext("POST", "/v1/chat/completions", nil)
	prepared, err := Build(c, ForwardingRequest{
		URL:         "https://upstream.example/v1/chat/completions",
		Body:        []byte(`{}`),
		ServiceType: "openai",
		CustomHeaders: map[string]string{
			"Authorization":  "Bearer custom",
			"x-api-key":      "custom-api-key",
			"x-goog-api-key": "custom-goog-key",
		},
		AuthKind: AuthKindStandard,
		APIKey:   "sk-selected",
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	headers := prepared.Request.Header
	if got := headers.Get("Authorization"); got != "Bearer sk-selected" {
		t.Fatalf("Authorization = %q, want selected key", got)
	}
	if got := headers.Get("x-api-key"); got != "" {
		t.Fatalf("x-api-key = %q, want cleared by final auth", got)
	}
	if got := headers.Get("x-goog-api-key"); got != "" {
		t.Fatalf("x-goog-api-key = %q, want cleared by final auth", got)
	}
}

func TestBuildCustomAuthLikeHeadersCannotOverrideSelectedAnthropicKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c := newTestContext("POST", "/v1/messages", nil)
	prepared, err := Build(c, ForwardingRequest{
		URL:         "https://upstream.example/v1/messages",
		Body:        []byte(`{}`),
		ServiceType: "claude",
		CustomHeaders: map[string]string{
			"Authorization": "Bearer custom",
			"x-api-key":     "custom-api-key",
		},
		AuthKind: AuthKindStandard,
		APIKey:   "sk-ant-api03-selected",
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	headers := prepared.Request.Header
	if got := headers.Get("x-api-key"); got != "sk-ant-api03-selected" {
		t.Fatalf("x-api-key = %q, want selected Anthropic key", got)
	}
	if got := headers.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want cleared by final auth", got)
	}
}

func TestBuildCustomAuthLikeHeadersCannotOverrideSelectedGeminiKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c := newTestContext("POST", "/v1beta/models/gemini:generateContent", nil)
	prepared, err := Build(c, ForwardingRequest{
		URL:         "https://generativelanguage.googleapis.com/v1beta/models/gemini:generateContent",
		Body:        []byte(`{}`),
		ServiceType: "gemini",
		CustomHeaders: map[string]string{
			"Authorization":  "Bearer custom",
			"x-api-key":      "custom-api-key",
			"x-goog-api-key": "custom-goog-key",
		},
		AuthKind: AuthKindGemini,
		APIKey:   "AIza-selected",
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	headers := prepared.Request.Header
	if got := headers.Get("x-goog-api-key"); got != "AIza-selected" {
		t.Fatalf("x-goog-api-key = %q, want selected Gemini key", got)
	}
	if got := headers.Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want cleared by final auth", got)
	}
	if got := headers.Get("x-api-key"); got != "" {
		t.Fatalf("x-api-key = %q, want cleared by final auth", got)
	}
}

func TestBuildUserAgentPassthroughCustomOverrideAndClaudeFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		inboundUA     string
		serviceType   string
		customHeaders map[string]string
		wantUA        string
	}{
		{
			name:        "inbound user agent passes through",
			inboundUA:   "Client/1.0",
			serviceType: "claude",
			wantUA:      "Client/1.0",
		},
		{
			name:        "claude fallback applies when user agent is absent",
			serviceType: "claude",
			wantUA:      "claude-cli/2.0.34 (external, cli)",
		},
		{
			name:        "custom user agent wins over inbound and fallback",
			inboundUA:   "Client/1.0",
			serviceType: "claude",
			customHeaders: map[string]string{
				"User-Agent": "AdminConfigured/2.0",
			},
			wantUA: "AdminConfigured/2.0",
		},
		{
			name:        "non claude target has no fallback",
			serviceType: "openai",
			wantUA:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := map[string]string{}
			if tt.inboundUA != "" {
				headers["User-Agent"] = tt.inboundUA
			}
			c := newTestContext("POST", "/test", headers)

			prepared, err := Build(c, ForwardingRequest{
				URL:           "https://upstream.example/v1/test",
				Body:          []byte(`{}`),
				ServiceType:   tt.serviceType,
				CustomHeaders: tt.customHeaders,
				APIKey:        "sk-selected",
			})
			if err != nil {
				t.Fatalf("Build returned error: %v", err)
			}

			if got := prepared.Request.Header.Get("User-Agent"); got != tt.wantUA {
				t.Fatalf("User-Agent = %q, want %q", got, tt.wantUA)
			}
		})
	}
}

func newTestContext(method, target string, headers map[string]string) *gin.Context {
	req := httptest.NewRequest(method, target, nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	return c
}
