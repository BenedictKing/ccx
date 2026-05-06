package responses

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestCompactHandler_CustomAuthorizationCannotOverrideSelectedKey(t *testing.T) {
	var gotAuthorization string
	var gotTraceID string
	var gotCookie string
	var gotUserAgent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotTraceID = r.Header.Get("X-Trace-ID")
		gotCookie = r.Header.Get("Cookie")
		gotUserAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"resp_compact_ok","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`)
	}))
	defer upstream.Close()

	router, _ := newCompactTestRouter(t, []config.UpstreamConfig{{
		Name:        "compact-auth-override",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-selected"},
		ServiceType: "responses",
		Status:      "active",
		CustomHeaders: map[string]string{
			"Authorization": "Bearer sk-custom",
			"X-Trace-ID":    "trace-1",
			"User-Agent":    "CompactCustomUA/1.0",
		},
	}})

	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(`{"model":"gpt-5","input":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "secret-key")
	req.Header.Set("Cookie", "sid=secret")
	req.Header.Set("User-Agent", "InboundUA/1.0")
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
	if gotUserAgent != "CompactCustomUA/1.0" {
		t.Fatalf("User-Agent = %q, want custom UA", gotUserAgent)
	}
}
