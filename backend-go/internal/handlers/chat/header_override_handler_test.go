package chat

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestHandler_CustomAuthorizationCannotOverrideSelectedKey(t *testing.T) {
	var gotAuthorization string
	var gotTraceID string
	var gotCookie string
	var gotProxyAuthorization string
	var gotUserAgent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotTraceID = r.Header.Get("X-Trace-ID")
		gotCookie = r.Header.Get("Cookie")
		gotProxyAuthorization = r.Header.Get("Proxy-Authorization")
		gotUserAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl_auth","object":"chat.completion","model":"gpt-5","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	router := newChatTestRouter(t, config.UpstreamConfig{
		Name:        "chat-auth-override",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-selected"},
		ServiceType: "openai",
		Status:      "active",
		CustomHeaders: map[string]string{
			"Authorization": "Bearer sk-custom",
			"X-Trace-ID":    "trace-1",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5","messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-key")
	req.Header.Set("Cookie", "sid=secret")
	req.Header.Set("Proxy-Authorization", "Basic secret")
	req.Header.Set("User-Agent", "Client/1.0")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	if gotAuthorization != "Bearer sk-selected" {
		t.Fatalf("Authorization = %q, want selected key", gotAuthorization)
	}
	if gotTraceID != "trace-1" {
		t.Fatalf("X-Trace-ID = %q, want custom metadata header", gotTraceID)
	}
	if gotCookie != "" {
		t.Fatalf("Cookie = %q, want stripped", gotCookie)
	}
	if gotProxyAuthorization != "" {
		t.Fatalf("Proxy-Authorization = %q, want stripped", gotProxyAuthorization)
	}
	if gotUserAgent != "Client/1.0" {
		t.Fatalf("User-Agent = %q, want inbound UA", gotUserAgent)
	}
}
