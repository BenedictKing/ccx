package images

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func newImagesTestConfigManager(t *testing.T) *config.ConfigManager {
	t.Helper()
	cfgFile := t.TempDir() + "/config.json"
	if err := os.WriteFile(cfgFile, []byte(`{"upstream":[],"imagesUpstream":[]}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfgManager, err := config.NewConfigManager(cfgFile)
	if err != nil {
		t.Fatalf("config manager: %v", err)
	}
	return cfgManager
}

func newImagesTestEnvConfig() *config.EnvConfig {
	envCfg := config.NewEnvConfig()
	envCfg.ProxyAccessKey = "test-key"
	return envCfg
}

func TestBuildProviderRequest_URLVariants(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"image-default","prompt":"hello"}`))

	upstream := &config.UpstreamConfig{ServiceType: "openai"}
	req, err := buildProviderRequest(c, upstream, "https://api.openai.com", "sk-test", []byte(`{"model":"image-default","prompt":"hello"}`), "image-default")
	if err != nil {
		t.Fatalf("buildProviderRequest() error = %v", err)
	}
	if req.URL.String() != "https://api.openai.com/v1/images/generations" {
		t.Fatalf("unexpected url: %s", req.URL.String())
	}

	req, err = buildProviderRequest(c, upstream, "https://api.openai.com#", "sk-test", []byte(`{"model":"image-default","prompt":"hello"}`), "image-default")
	if err != nil {
		t.Fatalf("buildProviderRequest() error = %v", err)
	}
	if req.URL.String() != "https://api.openai.com/images/generations" {
		t.Fatalf("unexpected # url: %s", req.URL.String())
	}

	req, err = buildProviderRequest(c, upstream, "https://api.openai.com/#", "sk-test", []byte(`{"model":"image-default","prompt":"hello"}`), "image-default")
	if err != nil {
		t.Fatalf("buildProviderRequest() error = %v", err)
	}
	if req.URL.String() != "https://api.openai.com/images/generations" {
		t.Fatalf("unexpected /# url: %s", req.URL.String())
	}
}

func TestBuildProviderRequest_RejectsUnsupportedServiceType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"image-default","prompt":"hello"}`))

	upstream := &config.UpstreamConfig{ServiceType: "gemini"}
	_, err := buildProviderRequest(c, upstream, "https://api.openai.com", "sk-test", []byte(`{"model":"image-default","prompt":"hello"}`), "image-default")
	if err == nil {
		t.Fatal("expected error for unsupported serviceType")
	}
	if !strings.Contains(err.Error(), "openai serviceType") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAddUpstream_RejectsUnsupportedServiceType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgFile := t.TempDir() + "/config.json"
	if err := os.WriteFile(cfgFile, []byte(`{"upstream":[],"imagesUpstream":[]}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfgManager, err := config.NewConfigManager(cfgFile)
	if err != nil {
		t.Fatalf("config manager: %v", err)
	}
	defer func() { _ = cfgManager.Close() }()

	r := gin.New()
	r.POST("/api/images/channels", AddUpstream(cfgManager))

	body := strings.NewReader(`{"name":"images-gemini","serviceType":"gemini","baseUrl":"https://example.com","apiKeys":["test-key"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/images/channels", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "openai serviceType") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestHandler_MissingModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgFile := t.TempDir() + "/config.json"
	if err := os.WriteFile(cfgFile, []byte(`{"upstream":[]}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfgManager, err := config.NewConfigManager(cfgFile)
	if err != nil {
		t.Fatalf("config manager: %v", err)
	}
	defer func() { _ = cfgManager.Close() }()

	envCfg := config.NewEnvConfig()
	envCfg.ProxyAccessKey = "test-key"

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"prompt":"hello"}`))
	c.Request.Header.Set("Authorization", "Bearer test-key")
	Handler(envCfg, cfgManager, nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_MissingPrompt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newImagesTestConfigManager(t)
	defer func() { _ = cfgManager.Close() }()

	envCfg := newImagesTestEnvConfig()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"gpt-image-1"}`))
	c.Request.Header.Set("Authorization", "Bearer test-key")
	Handler(envCfg, cfgManager, nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestHandler_InvalidMultipartEditsReturnsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newImagesTestConfigManager(t)
	defer func() { _ = cfgManager.Close() }()

	envCfg := newImagesTestEnvConfig()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader("broken"))
	c.Request.Header.Set("Authorization", "Bearer test-key")
	c.Request.Header.Set("Content-Type", "multipart/form-data")
	Handler(envCfg, cfgManager, nil)(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid_multipart") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestLogImagesOriginalRequestOmitsMultipartBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader(""))

	envCfg := newImagesTestEnvConfig()
	envCfg.EnableRequestLogs = true
	envCfg.Env = "development"
	envCfg.RawLogOutput = true

	var logs bytes.Buffer
	originalOutput := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(originalOutput)

	body := []byte("multipart-boundary\r\nfile-bytes-that-must-not-be-logged")
	logImagesOriginalRequest(c, body, "multipart/form-data; boundary=multipart-boundary", envCfg)

	if strings.Contains(logs.String(), "file-bytes-that-must-not-be-logged") {
		t.Fatalf("multipart body was logged: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "multipart request body omitted from logs") {
		t.Fatalf("missing omission marker in logs: %s", logs.String())
	}
}

func TestGetChannelModels_CustomAuthorizationCannotOverrideSelectedKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var gotAuthorization string
	var gotTraceID string
	var gotUserAgent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotTraceID = r.Header.Get("X-Trace-ID")
		gotUserAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-image-1"}]}`))
	}))
	defer upstream.Close()

	cfgFile := t.TempDir() + "/config.json"
	cfgJSON := `{"imagesUpstream":[{"name":"images-admin","serviceType":"openai","baseUrl":"` + upstream.URL + `","apiKeys":["sk-selected"],"status":"active"}]}`
	if err := os.WriteFile(cfgFile, []byte(cfgJSON), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfgManager, err := config.NewConfigManager(cfgFile)
	if err != nil {
		t.Fatalf("config manager: %v", err)
	}
	defer func() { _ = cfgManager.Close() }()

	router := gin.New()
	router.POST("/api/images/channels/:id/models", GetChannelModels(cfgManager))

	req := httptest.NewRequest(http.MethodPost, "/api/images/channels/0/models", strings.NewReader(`{"key":"sk-selected","customHeaders":{"Authorization":"Bearer sk-custom","X-Trace-ID":"trace-1","User-Agent":"AdminUA/1.0"}}`))
	req.Header.Set("Content-Type", "application/json")
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
	if gotUserAgent != "AdminUA/1.0" {
		t.Fatalf("User-Agent = %q, want custom UA", gotUserAgent)
	}
}
