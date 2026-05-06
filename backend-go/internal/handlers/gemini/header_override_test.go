package gemini

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

func TestBuildProviderRequest_CustomGeminiKeyCannotOverrideSelectedKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := &config.UpstreamConfig{
		BaseURL:     "https://generativelanguage.googleapis.com",
		ServiceType: "gemini",
		CustomHeaders: map[string]string{
			"x-goog-api-key": "custom-gemini-key",
			"X-Trace-ID":     "trace-1",
		},
	}
	geminiReq := &types.GeminiRequest{
		Contents: []types.GeminiContent{
			{
				Parts: []types.GeminiPart{{Text: "hi"}},
			},
		},
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", nil)

	req, err := buildProviderRequest(c, upstream, upstream.BaseURL, "selected-gemini-key", geminiReq, "gemini-2.0-flash", false)
	if err != nil {
		t.Fatalf("buildProviderRequest failed: %v", err)
	}

	if got := req.Header.Get("x-goog-api-key"); got != "selected-gemini-key" {
		t.Fatalf("x-goog-api-key = %q, want selected key", got)
	}
	if got := req.Header.Get("X-Trace-ID"); got != "trace-1" {
		t.Fatalf("X-Trace-ID = %q, want custom metadata header", got)
	}
}

func TestHandler_CustomGeminiKeyCannotOverrideSelectedKey(t *testing.T) {
	var gotGeminiKey string
	var gotTraceID string
	var gotCookie string
	var gotUserAgent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotGeminiKey = r.Header.Get("x-goog-api-key")
		gotTraceID = r.Header.Get("X-Trace-ID")
		gotCookie = r.Header.Get("Cookie")
		gotUserAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`))
	}))
	defer upstream.Close()

	router := newGeminiTestRouter(t, config.UpstreamConfig{
		Name:        "gemini-auth-override",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"selected-gemini-key"},
		ServiceType: "gemini",
		Status:      "active",
		CustomHeaders: map[string]string{
			"x-goog-api-key": "custom-gemini-key",
			"X-Trace-ID":     "trace-1",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", strings.NewReader(`{"contents":[{"parts":[{"text":"hello"}]}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "secret-key")
	req.Header.Set("Cookie", "sid=secret")
	req.Header.Set("User-Agent", "GeminiClient/1.0")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	if gotGeminiKey != "selected-gemini-key" {
		t.Fatalf("x-goog-api-key = %q, want selected key", gotGeminiKey)
	}
	if gotTraceID != "trace-1" {
		t.Fatalf("X-Trace-ID = %q, want custom metadata header", gotTraceID)
	}
	if gotCookie != "" {
		t.Fatalf("Cookie = %q, want stripped", gotCookie)
	}
	if gotUserAgent != "GeminiClient/1.0" {
		t.Fatalf("User-Agent = %q, want inbound UA", gotUserAgent)
	}
}
