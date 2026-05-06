package images

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/gin-gonic/gin"
)

func TestBuildProviderRequest_CustomAuthorizationCannotOverrideSelectedKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

	upstream := &config.UpstreamConfig{
		ServiceType: "openai",
		CustomHeaders: map[string]string{
			"Authorization": "Bearer sk-custom",
			"X-Trace-ID":    "trace-1",
		},
	}
	bodyBytes := []byte(`{"model":"gpt-image-1","prompt":"hello"}`)

	req, err := buildProviderRequest(c, upstream, "https://api.example.com", "sk-selected", bodyBytes, "gpt-image-1")
	if err != nil {
		t.Fatalf("buildProviderRequest() error = %v", err)
	}

	if got := req.Header.Get("Authorization"); got != "Bearer sk-selected" {
		t.Fatalf("Authorization = %q, want selected key", got)
	}
	if got := req.Header.Get("X-Trace-ID"); got != "trace-1" {
		t.Fatalf("X-Trace-ID = %q, want custom metadata header", got)
	}
}

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
		_, _ = w.Write([]byte(`{"created":1,"data":[{"url":"https://example.com/image.png"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer upstream.Close()

	router := newImagesHeaderOverrideRouter(t, config.UpstreamConfig{
		Name:        "images-auth-override",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-selected"},
		ServiceType: "openai",
		Status:      "active",
		CustomHeaders: map[string]string{
			"Authorization": "Bearer sk-custom",
			"X-Trace-ID":    "trace-1",
			"User-Agent":    "ImagesCustomUA/1.0",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"gpt-image-1","prompt":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-key")
	req.Header.Set("Cookie", "sid=secret")
	req.Header.Set("Proxy-Authorization", "Basic secret")
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
	if gotProxyAuthorization != "" {
		t.Fatalf("Proxy-Authorization = %q, want stripped", gotProxyAuthorization)
	}
	if gotUserAgent != "ImagesCustomUA/1.0" {
		t.Fatalf("User-Agent = %q, want custom UA", gotUserAgent)
	}
}

func newImagesHeaderOverrideRouter(t *testing.T, upstream config.UpstreamConfig) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := config.Config{ImagesUpstream: []config.UpstreamConfig{upstream}}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("serialize config: %v", err)
	}
	tmpFile := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	cfgManager, err := config.NewConfigManager(tmpFile)
	if err != nil {
		t.Fatalf("NewConfigManager() err = %v", err)
	}
	t.Cleanup(func() { _ = cfgManager.Close() })

	traceAffinity := session.NewTraceAffinityManager()
	t.Cleanup(traceAffinity.Stop)
	channelScheduler := scheduler.NewChannelScheduler(
		cfgManager,
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
		traceAffinity,
		nil,
	)
	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret-key",
		MaxRequestBodySize: 1024 * 1024,
	}

	router := gin.New()
	router.POST("/v1/images/generations", Handler(envCfg, cfgManager, channelScheduler))
	return router
}
