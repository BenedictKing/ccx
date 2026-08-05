package vectors

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/ratelimit"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/gin-gonic/gin"
)

func newVectorsTestConfigManager(t *testing.T) *config.ConfigManager {
	t.Helper()
	cfgFile := t.TempDir() + "/config.json"
	if err := os.WriteFile(cfgFile, []byte(`{"upstream":[],"vectorsUpstream":[]}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfgManager, err := config.NewConfigManager(cfgFile, "")
	if err != nil {
		t.Fatalf("config manager: %v", err)
	}
	return cfgManager
}

func newVectorsTestScheduler(cfgManager *config.ConfigManager, vectorsMetrics *metrics.MetricsManager) *scheduler.ChannelScheduler {
	if vectorsMetrics == nil {
		vectorsMetrics = metrics.NewMetricsManager()
	}
	return scheduler.NewChannelScheduler(
		cfgManager,
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
		session.NewTraceAffinityManager(),
		nil,
		vectorsMetrics,
	)
}

func newVectorsTestEnvConfig() *config.EnvConfig {
	envCfg := config.NewEnvConfig()
	envCfg.ProxyAccessKey = "test-proxy-key"
	return envCfg
}

func serveVectorsEmbeddingRequest(cfgManager *config.ConfigManager, sch *scheduler.ChannelScheduler, body string) *httptest.ResponseRecorder {
	return serveVectorsEmbeddingRequestWithHeaders(cfgManager, sch, body, nil)
}

func serveVectorsEmbeddingRequestWithHeaders(cfgManager *config.ConfigManager, sch *scheduler.ChannelScheduler, body string, headers map[string]string) *httptest.ResponseRecorder {
	r := gin.New()
	r.POST("/v1/embeddings", Handler(newVectorsTestEnvConfig(), cfgManager, sch))

	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-proxy-key")
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func boolPtr(v bool) *bool {
	return &v
}

func TestBuildEmbeddingsURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{name: "root", baseURL: "https://api.openai.com", want: "https://api.openai.com/v1/embeddings"},
		{name: "versioned", baseURL: "https://api.openai.com/v1", want: "https://api.openai.com/v1/embeddings"},
		{name: "hash", baseURL: "https://api.openai.com#", want: "https://api.openai.com/embeddings"},
		{name: "slash hash", baseURL: "https://api.openai.com/#", want: "https://api.openai.com/embeddings"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildEmbeddingsURL(tt.baseURL); got != tt.want {
				t.Fatalf("buildEmbeddingsURL(%q) = %q, want %q", tt.baseURL, got, tt.want)
			}
		})
	}
}

func TestBuildProviderRequestAppliesMappingAndHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/embeddings?encoding_format=float", strings.NewReader(""))
	c.Request.Header.Set("Authorization", "Bearer client-key")
	c.Request.Header.Set("X-Forwarded-For", "127.0.0.1")

	upstream := &config.UpstreamConfig{
		ServiceType:   "openai",
		AuthHeader:    "x-api-key",
		ModelMapping:  map[string]string{"embed-public": "text-embedding-3-small"},
		CustomHeaders: map[string]string{"X-Custom": "yes"},
	}
	bodyBytes := []byte(`{"model":"embed-public","input":"hello"}`)
	req, err := buildProviderRequest(c, upstream, "https://api.example.com/v1", "sk-test", bodyBytes, "embed-public")
	if err != nil {
		t.Fatalf("buildProviderRequest() error = %v", err)
	}
	if got := req.URL.String(); got != "https://api.example.com/v1/embeddings?encoding_format=float" {
		t.Fatalf("unexpected url: %s", got)
	}
	if got := req.Header.Get("x-api-key"); got != "sk-test" {
		t.Fatalf("x-api-key = %q, want sk-test", got)
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization should be removed, got %q", got)
	}
	if got := req.Header.Get("X-Custom"); got != "yes" {
		t.Fatalf("X-Custom = %q, want yes", got)
	}
	if got := req.Header.Get("X-Forwarded-For"); got != "" {
		t.Fatalf("X-Forwarded-For should be removed, got %q", got)
	}

	requestBody, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(requestBody, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if got := payload["model"]; got != "text-embedding-3-small" {
		t.Fatalf("model = %v, want text-embedding-3-small", got)
	}
}

func TestParseEmbeddingsRequestValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		body string
		ok   bool
	}{
		{name: "valid string", body: `{"model":"text-embedding-3-small","input":"hello"}`, ok: true},
		{name: "valid array", body: `{"model":"text-embedding-3-small","input":["hello"]}`, ok: true},
		{name: "invalid number input", body: `{"model":"text-embedding-3-small","input":42}`, ok: false},
		{name: "invalid bool input", body: `{"model":"text-embedding-3-small","input":true}`, ok: false},
		{name: "invalid object input", body: `{"model":"text-embedding-3-small","input":{}}`, ok: false},
		{name: "missing model", body: `{"input":"hello"}`, ok: false},
		{name: "missing input", body: `{"model":"text-embedding-3-small"}`, ok: false},
		{name: "empty string input", body: `{"model":"text-embedding-3-small","input":""}`, ok: false},
		{name: "empty array input", body: `{"model":"text-embedding-3-small","input":[]}`, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			_, _, _, ok := parseEmbeddingsRequest(c, []byte(tt.body))
			if ok != tt.ok {
				t.Fatalf("parseEmbeddingsRequest() ok = %v, want %v", ok, tt.ok)
			}
		})
	}
}

func TestExtractEmbeddingsUsage(t *testing.T) {
	usage := extractEmbeddingsUsage([]byte(`{"usage":{"total_tokens":17}}`))
	if usage == nil {
		t.Fatal("expected usage")
	}
	if usage.InputTokens != 17 || usage.OutputTokens != 0 || usage.PromptTokens != 17 || usage.PromptTokensTotal != 17 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestHandlerFailoverAndUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	var attempts int32
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("unexpected upstream path: %s", r.URL.Path)
		}
		if r.Header.Get("X-Custom") != "yes" {
			t.Errorf("missing custom header")
		}
		if strings.Contains(r.Header.Get("Authorization"), "sk-bad") {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode upstream body: %v", err)
		}
		if got := payload["model"]; got != "text-embedding-3-small" {
			t.Errorf("upstream model = %v, want text-embedding-3-small", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[],"usage":{"prompt_tokens":12,"total_tokens":12}}`))
	}))
	defer upstreamServer.Close()

	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:          "vectors-test",
		ServiceType:   "openai",
		BaseURL:       upstreamServer.URL,
		APIKeys:       []string{"sk-bad", "sk-good"},
		ModelMapping:  map[string]string{"embed-public": "text-embedding-3-small"},
		CustomHeaders: map[string]string{"X-Custom": "yes"},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream() error = %v", err)
	}

	vectorsMetrics := metrics.NewMetricsManager()
	sch := newVectorsTestScheduler(cfgManager, vectorsMetrics)
	r := gin.New()
	r.POST("/v1/embeddings", Handler(newVectorsTestEnvConfig(), cfgManager, sch))

	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(`{"model":"embed-public","input":"hello"}`))
	req.Header.Set("Authorization", "Bearer test-proxy-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestHandlerInvalidSuccessResponseFailsOver(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{
			name:        "html body",
			contentType: "text/html",
			body:        `<html>upstream error</html>`,
		},
		{
			name:        "error json",
			contentType: "application/json",
			body:        `{"error":{"message":"upstream error","type":"server_error"}}`,
		},
		{
			name:        "missing data",
			contentType: "application/json",
			body:        `{"object":"list","usage":{"prompt_tokens":1,"total_tokens":1}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfgManager := newVectorsTestConfigManager(t)
			defer errutil.IgnoreDeferred(cfgManager.Close)

			var primaryAttempts int32
			primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&primaryAttempts, 1)
				w.Header().Set("Content-Type", tt.contentType)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer primaryServer.Close()

			var secondaryAttempts int32
			secondaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&secondaryAttempts, 1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.1],"index":0}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
			}))
			defer secondaryServer.Close()

			if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
				Name:        "secondary-vectors",
				ServiceType: "openai",
				BaseURL:     secondaryServer.URL,
				APIKeys:     []string{"sk-secondary"},
				Priority:    2,
			}); err != nil {
				t.Fatalf("AddVectorsUpstream(secondary) error = %v", err)
			}
			if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
				Name:        "primary-vectors",
				ServiceType: "openai",
				BaseURL:     primaryServer.URL,
				APIKeys:     []string{"sk-primary"},
				Priority:    1,
			}); err != nil {
				t.Fatalf("AddVectorsUpstream(primary) error = %v", err)
			}

			sch := newVectorsTestScheduler(cfgManager, metrics.NewMetricsManager())
			w := serveVectorsEmbeddingRequest(cfgManager, sch, `{"model":"text-embedding-3-small","input":"hello"}`)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), `"embedding":[0.1]`) {
				t.Fatalf("expected secondary embeddings response, got: %s", w.Body.String())
			}
			if got := atomic.LoadInt32(&primaryAttempts); got != 1 {
				t.Fatalf("primary attempts = %d, want 1", got)
			}
			if got := atomic.LoadInt32(&secondaryAttempts); got != 1 {
				t.Fatalf("secondary attempts = %d, want 1", got)
			}
		})
	}
}

func TestHandlerAppliesVectorsModelMappingToUpstreamBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	var upstreamModel string
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
			return
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode upstream body: %v", err)
			return
		}
		upstreamModel, _ = payload["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer upstreamServer.Close()

	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "jina-vectors",
		ServiceType:  "openai",
		BaseURL:      upstreamServer.URL,
		APIKeys:      []string{"sk-jina"},
		ModelMapping: map[string]string{"text-embedding-3-small": "jina-embeddings-v2-base-zh"},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream() error = %v", err)
	}

	sch := newVectorsTestScheduler(cfgManager, metrics.NewMetricsManager())
	w := serveVectorsEmbeddingRequest(cfgManager, sch, `{"model":"text-embedding-3-small","input":"hello"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	if upstreamModel != "jina-embeddings-v2-base-zh" {
		t.Fatalf("upstream model = %q, want jina-embeddings-v2-base-zh", upstreamModel)
	}
}

func TestHandlerPassesThroughVectorsModelWhenMappingMisses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	var upstreamModel string
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
			return
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode upstream body: %v", err)
			return
		}
		upstreamModel, _ = payload["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer upstreamServer.Close()

	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "openai-vectors",
		ServiceType:  "openai",
		BaseURL:      upstreamServer.URL,
		APIKeys:      []string{"sk-openai"},
		ModelMapping: map[string]string{"embed-public": "jina-embeddings-v2-base-zh"},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream() error = %v", err)
	}

	sch := newVectorsTestScheduler(cfgManager, metrics.NewMetricsManager())
	w := serveVectorsEmbeddingRequest(cfgManager, sch, `{"model":"text-embedding-3-small","input":"hello"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	if upstreamModel != "text-embedding-3-small" {
		t.Fatalf("upstream model = %q, want text-embedding-3-small", upstreamModel)
	}
}

func TestHandlerDoesNotFallbackAcrossEmbeddingSpaces(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	var primaryAttempts int32
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&primaryAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"primary failed"}}`))
	}))
	defer primaryServer.Close()

	var secondaryAttempts int32
	secondaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondaryAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.2],"index":0}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer secondaryServer.Close()

	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "secondary-vectors",
		ServiceType:  "openai",
		BaseURL:      secondaryServer.URL,
		APIKeys:      []string{"sk-secondary"},
		Priority:     2,
		ModelMapping: map[string]string{"embed-public": "embedding-model-b"},
		EmbeddingCapabilities: map[string]config.EmbeddingCapability{
			"embedding-model-b": {Dimensions: 1536, Normalized: boolPtr(true)},
		},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(secondary) error = %v", err)
	}
	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "primary-vectors",
		ServiceType:  "openai",
		BaseURL:      primaryServer.URL,
		APIKeys:      []string{"sk-primary"},
		Priority:     1,
		ModelMapping: map[string]string{"embed-public": "embedding-model-a"},
		EmbeddingCapabilities: map[string]config.EmbeddingCapability{
			"embedding-model-a": {Dimensions: 1536, Normalized: boolPtr(true)},
		},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(primary) error = %v", err)
	}

	vectorsMetrics := metrics.NewMetricsManager()
	sch := newVectorsTestScheduler(cfgManager, vectorsMetrics)
	w := serveVectorsEmbeddingRequest(cfgManager, sch, `{"model":"embed-public","input":"hello"}`)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&primaryAttempts); got != 1 {
		t.Fatalf("primary attempts = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&secondaryAttempts); got != 0 {
		t.Fatalf("secondary attempts = %d, want 0", got)
	}
	if got := vectorsMetrics.GetKeyMetrics(secondaryServer.URL, "sk-secondary", "openai"); got != nil {
		t.Fatalf("secondary metrics should not be recorded, got %+v", got)
	}
}

func TestHandlerAllowsFallbackWithinSameEmbeddingSpace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	var primaryAttempts int32
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&primaryAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"primary failed"}}`))
	}))
	defer primaryServer.Close()

	var secondaryAttempts int32
	secondaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondaryAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.3],"index":0}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer secondaryServer.Close()

	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "secondary-vectors",
		ServiceType:  "openai",
		BaseURL:      secondaryServer.URL,
		APIKeys:      []string{"sk-secondary"},
		Priority:     2,
		ModelMapping: map[string]string{"embed-public": "embedding-model-b"},
		EmbeddingCapabilities: map[string]config.EmbeddingCapability{
			"embedding-model-b": {EmbeddingSpaceID: "shared-space", Dimensions: 1536, Normalized: boolPtr(true)},
		},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(secondary) error = %v", err)
	}
	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "primary-vectors",
		ServiceType:  "openai",
		BaseURL:      primaryServer.URL,
		APIKeys:      []string{"sk-primary"},
		Priority:     1,
		ModelMapping: map[string]string{"embed-public": "embedding-model-a"},
		EmbeddingCapabilities: map[string]config.EmbeddingCapability{
			"embedding-model-a": {EmbeddingSpaceID: "shared-space", Dimensions: 1536, Normalized: boolPtr(true)},
		},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(primary) error = %v", err)
	}

	sch := newVectorsTestScheduler(cfgManager, metrics.NewMetricsManager())
	w := serveVectorsEmbeddingRequest(cfgManager, sch, `{"model":"embed-public","input":"hello"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"embedding":[0.3]`) {
		t.Fatalf("expected secondary embeddings response, got: %s", w.Body.String())
	}
	if got := atomic.LoadInt32(&primaryAttempts); got != 1 {
		t.Fatalf("primary attempts = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&secondaryAttempts); got != 1 {
		t.Fatalf("secondary attempts = %d, want 1", got)
	}
}

func TestHandlerIgnoresSuspendedChannelAsEmbeddingCompatibilityAnchor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	var suspendedAttempts int32
	suspendedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&suspendedAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[9.9],"index":0}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer suspendedServer.Close()

	var activeAttempts int32
	activeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&activeAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.6],"index":0}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer activeServer.Close()

	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "active-compatible-vectors",
		ServiceType:  "openai",
		BaseURL:      activeServer.URL,
		APIKeys:      []string{"sk-active"},
		Priority:     2,
		ModelMapping: map[string]string{"embed-public": "embedding-model-b"},
		EmbeddingCapabilities: map[string]config.EmbeddingCapability{
			"embedding-model-b": {Dimensions: 1536, Normalized: boolPtr(true)},
		},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(active) error = %v", err)
	}
	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "suspended-incompatible-vectors",
		ServiceType:  "openai",
		BaseURL:      suspendedServer.URL,
		APIKeys:      []string{"sk-suspended"},
		Priority:     1,
		Status:       "suspended",
		ModelMapping: map[string]string{"embed-public": "embedding-model-a"},
		EmbeddingCapabilities: map[string]config.EmbeddingCapability{
			"embedding-model-a": {Dimensions: 1536, Normalized: boolPtr(true)},
		},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(suspended) error = %v", err)
	}

	sch := newVectorsTestScheduler(cfgManager, metrics.NewMetricsManager())
	w := serveVectorsEmbeddingRequest(cfgManager, sch, `{"model":"embed-public","input":"hello"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&suspendedAttempts); got != 0 {
		t.Fatalf("suspended attempts = %d, want 0", got)
	}
	if got := atomic.LoadInt32(&activeAttempts); got != 1 {
		t.Fatalf("active attempts = %d, want 1", got)
	}
}

func TestHandlerIgnoresChannelWithoutKeysAsEmbeddingCompatibilityAnchor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	var noKeyAttempts int32
	noKeyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&noKeyAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[9.9],"index":0}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer noKeyServer.Close()

	var activeAttempts int32
	activeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&activeAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.7],"index":0}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer activeServer.Close()

	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "active-keyed-vectors",
		ServiceType:  "openai",
		BaseURL:      activeServer.URL,
		APIKeys:      []string{"sk-active"},
		Priority:     2,
		ModelMapping: map[string]string{"embed-public": "embedding-model-b"},
		EmbeddingCapabilities: map[string]config.EmbeddingCapability{
			"embedding-model-b": {Dimensions: 1536, Normalized: boolPtr(true)},
		},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(active) error = %v", err)
	}
	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "no-key-incompatible-vectors",
		ServiceType:  "openai",
		BaseURL:      noKeyServer.URL,
		APIKeys:      nil,
		Priority:     1,
		ModelMapping: map[string]string{"embed-public": "embedding-model-a"},
		EmbeddingCapabilities: map[string]config.EmbeddingCapability{
			"embedding-model-a": {Dimensions: 1536, Normalized: boolPtr(true)},
		},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(no-key) error = %v", err)
	}

	sch := newVectorsTestScheduler(cfgManager, metrics.NewMetricsManager())
	w := serveVectorsEmbeddingRequest(cfgManager, sch, `{"model":"embed-public","input":"hello"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&noKeyAttempts); got != 0 {
		t.Fatalf("no-key attempts = %d, want 0", got)
	}
	if got := atomic.LoadInt32(&activeAttempts); got != 1 {
		t.Fatalf("active attempts = %d, want 1", got)
	}
}

func TestHandlerIgnoresCooldownChannelAsEmbeddingCompatibilityAnchor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	var cooldownAttempts int32
	cooldownServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&cooldownAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[9.9],"index":0}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer cooldownServer.Close()

	var activeAttempts int32
	activeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&activeAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.8],"index":0}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer activeServer.Close()

	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "active-cooldown-fallback-vectors",
		ServiceType:  "openai",
		BaseURL:      activeServer.URL,
		APIKeys:      []string{"sk-active"},
		Priority:     2,
		ModelMapping: map[string]string{"embed-public": "embedding-model-b"},
		EmbeddingCapabilities: map[string]config.EmbeddingCapability{
			"embedding-model-b": {Dimensions: 1536, Normalized: boolPtr(true)},
		},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(active) error = %v", err)
	}
	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "cooldown-incompatible-vectors",
		ServiceType:  "openai",
		BaseURL:      cooldownServer.URL,
		APIKeys:      []string{"sk-cooldown"},
		Priority:     1,
		ModelMapping: map[string]string{"embed-public": "embedding-model-a"},
		EmbeddingCapabilities: map[string]config.EmbeddingCapability{
			"embedding-model-a": {Dimensions: 1536, Normalized: boolPtr(true)},
		},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(cooldown) error = %v", err)
	}

	sch := newVectorsTestScheduler(cfgManager, metrics.NewMetricsManager())
	sch.SetRateLimitManager(ratelimit.NewManager())
	sch.MarkChannelCooldown(scheduler.ChannelKindVectors, 0, time.Minute)
	w := serveVectorsEmbeddingRequest(cfgManager, sch, `{"model":"embed-public","input":"hello"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&cooldownAttempts); got != 0 {
		t.Fatalf("cooldown attempts = %d, want 0", got)
	}
	if got := atomic.LoadInt32(&activeAttempts); got != 1 {
		t.Fatalf("active attempts = %d, want 1", got)
	}
}

func TestHandlerFiltersEmbeddingChannelsByRequestedDimensions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	var unsupportedAttempts int32
	unsupportedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&unsupportedAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer unsupportedServer.Close()

	var supportedAttempts int32
	supportedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&supportedAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.4],"index":0}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer supportedServer.Close()

	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "supported-dimensions",
		ServiceType:  "openai",
		BaseURL:      supportedServer.URL,
		APIKeys:      []string{"sk-supported"},
		Priority:     2,
		ModelMapping: map[string]string{"embed-public": "embedding-model-b"},
		EmbeddingCapabilities: map[string]config.EmbeddingCapability{
			"embedding-model-b": {
				EmbeddingSpaceID:    "shared-space",
				Dimensions:          1536,
				SupportedDimensions: []int{1024, 1536},
				Normalized:          boolPtr(true),
			},
		},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(supported) error = %v", err)
	}
	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "unsupported-dimensions",
		ServiceType:  "openai",
		BaseURL:      unsupportedServer.URL,
		APIKeys:      []string{"sk-unsupported"},
		Priority:     1,
		ModelMapping: map[string]string{"embed-public": "embedding-model-a"},
		EmbeddingCapabilities: map[string]config.EmbeddingCapability{
			"embedding-model-a": {
				EmbeddingSpaceID:    "shared-space",
				Dimensions:          1536,
				SupportedDimensions: []int{1536},
				Normalized:          boolPtr(true),
			},
		},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(unsupported) error = %v", err)
	}

	sch := newVectorsTestScheduler(cfgManager, metrics.NewMetricsManager())
	w := serveVectorsEmbeddingRequest(cfgManager, sch, `{"model":"embed-public","input":"hello","dimensions":1024}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&unsupportedAttempts); got != 0 {
		t.Fatalf("unsupported attempts = %d, want 0", got)
	}
	if got := atomic.LoadInt32(&supportedAttempts); got != 1 {
		t.Fatalf("supported attempts = %d, want 1", got)
	}
}

func TestHandlerRejectsInvalidEmbeddingDimensions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	sch := newVectorsTestScheduler(cfgManager, metrics.NewMetricsManager())
	for _, body := range []string{
		`{"model":"embed-public","input":"hello","dimensions":0}`,
		`{"model":"embed-public","input":"hello","dimensions":-1}`,
		`{"model":"embed-public","input":"hello","dimensions":1.5}`,
		`{"model":"embed-public","input":"hello","dimensions":"1024"}`,
	} {
		w := serveVectorsEmbeddingRequest(cfgManager, sch, body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body %s: expected 400, got %d, response=%s", body, w.Code, w.Body.String())
		}
	}
}

func TestHandlerEmbeddingCompatibilityCannotBeBypassedByPinPromotionOrTraceAffinity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		configure  func(*testing.T, *config.ConfigManager, *scheduler.ChannelScheduler)
		headers    map[string]string
		body       string
		wantStatus int
		wantGood   int32
		wantBad    int32
	}{
		{
			name: "x channel pin",
			headers: map[string]string{
				"X-Channel": "incompatible-vectors",
			},
			body:       `{"model":"embed-public","input":"hello"}`,
			wantStatus: http.StatusServiceUnavailable,
			wantGood:   0,
			wantBad:    0,
		},
		{
			name: "promotion",
			configure: func(t *testing.T, cfgManager *config.ConfigManager, _ *scheduler.ChannelScheduler) {
				t.Helper()
				if err := cfgManager.SetVectorsChannelPromotion(1, 5*time.Minute); err != nil {
					t.Fatalf("SetVectorsChannelPromotion() error = %v", err)
				}
			},
			body:       `{"model":"embed-public","input":"hello"}`,
			wantStatus: http.StatusOK,
			wantGood:   1,
			wantBad:    0,
		},
		{
			name: "trace affinity",
			configure: func(t *testing.T, _ *config.ConfigManager, sch *scheduler.ChannelScheduler) {
				t.Helper()
				sch.SetTraceAffinity("compat-user", 1, scheduler.ChannelKindVectors)
			},
			body:       `{"model":"embed-public","input":"hello","user":"compat-user"}`,
			wantStatus: http.StatusOK,
			wantGood:   1,
			wantBad:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfgManager := newVectorsTestConfigManager(t)
			defer errutil.IgnoreDeferred(cfgManager.Close)

			var badAttempts int32
			badServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&badAttempts, 1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[9.9],"index":0}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
			}))
			defer badServer.Close()

			var goodAttempts int32
			goodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&goodAttempts, 1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.5],"index":0}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
			}))
			defer goodServer.Close()

			if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
				Name:         "incompatible-vectors",
				ServiceType:  "openai",
				BaseURL:      badServer.URL,
				APIKeys:      []string{"sk-bad-space"},
				Priority:     2,
				ModelMapping: map[string]string{"embed-public": "embedding-model-b"},
				EmbeddingCapabilities: map[string]config.EmbeddingCapability{
					"embedding-model-b": {Dimensions: 1536, Normalized: boolPtr(true)},
				},
			}); err != nil {
				t.Fatalf("AddVectorsUpstream(incompatible) error = %v", err)
			}
			if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
				Name:         "compatible-vectors",
				ServiceType:  "openai",
				BaseURL:      goodServer.URL,
				APIKeys:      []string{"sk-good-space"},
				Priority:     1,
				ModelMapping: map[string]string{"embed-public": "embedding-model-a"},
				EmbeddingCapabilities: map[string]config.EmbeddingCapability{
					"embedding-model-a": {Dimensions: 1536, Normalized: boolPtr(true)},
				},
			}); err != nil {
				t.Fatalf("AddVectorsUpstream(compatible) error = %v", err)
			}

			sch := newVectorsTestScheduler(cfgManager, metrics.NewMetricsManager())
			if tt.configure != nil {
				tt.configure(t, cfgManager, sch)
			}

			w := serveVectorsEmbeddingRequestWithHeaders(cfgManager, sch, tt.body, tt.headers)
			if w.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d, body=%s", tt.wantStatus, w.Code, w.Body.String())
			}
			if got := atomic.LoadInt32(&goodAttempts); got != tt.wantGood {
				t.Fatalf("compatible attempts = %d, want %d", got, tt.wantGood)
			}
			if got := atomic.LoadInt32(&badAttempts); got != tt.wantBad {
				t.Fatalf("incompatible attempts = %d, want %d", got, tt.wantBad)
			}
		})
	}
}

func TestHandlerVectors422DoesNotFailoverOrAffectBreaker(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	var primaryAttempts int32
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&primaryAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"message":"Validation error: bad embedding model","type":"invalid_request_error","code":"invalid_request"}}`))
	}))
	defer primaryServer.Close()

	var secondaryAttempts int32
	secondaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondaryAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer secondaryServer.Close()

	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:        "secondary-vectors",
		ServiceType: "openai",
		BaseURL:     secondaryServer.URL,
		APIKeys:     []string{"sk-secondary"},
		Priority:    2,
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(secondary) error = %v", err)
	}
	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:        "primary-vectors",
		ServiceType: "openai",
		BaseURL:     primaryServer.URL,
		APIKeys:     []string{"sk-primary"},
		Priority:    1,
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(primary) error = %v", err)
	}

	vectorsMetrics := metrics.NewMetricsManager()
	sch := newVectorsTestScheduler(cfgManager, vectorsMetrics)
	w := serveVectorsEmbeddingRequest(cfgManager, sch, `{"model":"text-embedding-3-small","input":"hello"}`)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&primaryAttempts); got != 1 {
		t.Fatalf("primary attempts = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&secondaryAttempts); got != 0 {
		t.Fatalf("secondary attempts = %d, want 0", got)
	}

	keyMetrics := vectorsMetrics.GetKeyMetrics(primaryServer.URL, "sk-primary", "openai")
	if keyMetrics == nil {
		t.Fatal("expected primary key metrics")
	}
	if keyMetrics.RequestCount != 1 || keyMetrics.FailureCount != 1 {
		t.Fatalf("metrics counts = requests:%d failures:%d, want 1/1", keyMetrics.RequestCount, keyMetrics.FailureCount)
	}
	if keyMetrics.ConsecutiveFailures != 0 {
		t.Fatalf("consecutive breaker failures = %d, want 0", keyMetrics.ConsecutiveFailures)
	}
	if got := vectorsMetrics.CalculateKeyFailureRate(primaryServer.URL, "sk-primary", "openai"); got != 0 {
		t.Fatalf("breaker failure rate = %v, want 0", got)
	}
	if state := vectorsMetrics.GetKeyCircuitState(primaryServer.URL, "sk-primary", "openai"); state != metrics.CircuitStateClosed {
		t.Fatalf("circuit state = %s, want closed", state)
	}
}

func TestHandlerVectorsNonRetryableErrorDoesNotLogEmbeddingInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	const sensitiveInput = "secret customer embedding text"
	errorBody := `{"error":{"message":"embedding input ` + sensitiveInput + ` was rejected","type":"invalid_request_error","code":"invalid_request","param":"input"},"input":"` + sensitiveInput + `"}`
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(errorBody))
	}))
	defer upstreamServer.Close()

	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:        "safe-log-vectors",
		ServiceType: "openai",
		BaseURL:     upstreamServer.URL,
		APIKeys:     []string{"sk-primary"},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream() error = %v", err)
	}

	var logs bytes.Buffer
	origWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() {
		log.SetOutput(origWriter)
	})

	sch := newVectorsTestScheduler(cfgManager, metrics.NewMetricsManager())
	w := serveVectorsEmbeddingRequest(cfgManager, sch, `{"model":"text-embedding-3-small","input":"`+sensitiveInput+`"}`)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), sensitiveInput) {
		t.Fatalf("client response should still pass through upstream body, got: %s", w.Body.String())
	}
	if strings.Contains(logs.String(), sensitiveInput) {
		t.Fatalf("server log leaked embedding input: %s", logs.String())
	}

	metricsKey := metrics.GenerateMetricsIdentityKey(upstreamServer.URL, "sk-primary", "openai")
	channelLogs := sch.GetChannelLogStore(scheduler.ChannelKindVectors).Get(metricsKey)
	if len(channelLogs) != 1 {
		t.Fatalf("channel logs count = %d, want 1", len(channelLogs))
	}
	if strings.Contains(channelLogs[0].ErrorInfo, sensitiveInput) {
		t.Fatalf("channel log leaked embedding input: %s", channelLogs[0].ErrorInfo)
	}
	if !strings.Contains(channelLogs[0].ErrorInfo, "status=422") || !strings.Contains(channelLogs[0].ErrorInfo, "param=input") {
		t.Fatalf("unexpected channel log error info: %s", channelLogs[0].ErrorInfo)
	}
}

func TestAddUpstreamRejectsUnsupportedServiceType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	r := gin.New()
	r.POST("/api/vectors/channels", AddUpstream(cfgManager))

	req := httptest.NewRequest(http.MethodPost, "/api/vectors/channels", strings.NewReader(`{"name":"bad","serviceType":"gemini","baseUrl":"https://example.com","apiKeys":["sk-test"]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Vectors") {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}
}

func TestVectorsConfigRejectsInvalidEmbeddingCapabilities(t *testing.T) {
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	invalid := map[string]config.EmbeddingCapability{
		"text-embedding-3-small": {Dimensions: -1},
	}
	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:                  "invalid-add",
		ServiceType:           "openai",
		BaseURL:               "https://example.com",
		APIKeys:               []string{"sk-invalid"},
		EmbeddingCapabilities: invalid,
	}); !errors.Is(err, config.ErrInvalidEmbeddingCapability) {
		t.Fatalf("AddVectorsUpstream() error = %v, want ErrInvalidEmbeddingCapability", err)
	}

	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:        "valid-vectors",
		ServiceType: "openai",
		BaseURL:     "https://example.com",
		APIKeys:     []string{"sk-valid"},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream() error = %v", err)
	}

	_, err := cfgManager.UpdateVectorsUpstream(0, config.UpstreamUpdate{EmbeddingCapabilities: invalid})
	if !errors.Is(err, config.ErrInvalidEmbeddingCapability) {
		t.Fatalf("UpdateVectorsUpstream() error = %v, want ErrInvalidEmbeddingCapability", err)
	}
}

func TestGetChannelModelsSSRFLogDoesNotLeakRequestSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	var logs bytes.Buffer
	origWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() {
		log.SetOutput(origWriter)
	})

	r := gin.New()
	r.POST("/api/vectors/channels/:id/models", GetChannelModels(cfgManager))

	const sensitiveBaseURL = "http://user:sk-secret-in-url@169.254.169.254/latest/meta-data?api_key=sk-secret-in-query"
	body, err := json.Marshal(GetModelsRequest{
		Key:     "sk-request-key-do-not-log",
		BaseURL: sensitiveBaseURL,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/vectors/channels/0/models", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-auth-header-do-not-log")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body=%s", w.Code, w.Body.String())
	}

	logText := logs.String()
	if !strings.Contains(logText, "SSRF guard blocked") {
		t.Fatalf("expected SSRF log, got: %s", logText)
	}
	if !strings.Contains(logText, "caller=") {
		t.Fatalf("expected caller in SSRF log, got: %s", logText)
	}
	for _, leaked := range []string{
		sensitiveBaseURL,
		"sk-secret-in-url",
		"sk-secret-in-query",
		"sk-request-key-do-not-log",
		"sk-auth-header-do-not-log",
	} {
		if strings.Contains(logText, leaked) {
			t.Fatalf("SSRF log leaked %q: %s", leaked, logText)
		}
	}
}

func TestGetChannelModelsFailureLogDoesNotLeakTemporaryBaseURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	closedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	sensitiveBaseURL := strings.Replace(closedServer.URL, "http://", "http://user:sk-secret-in-url@", 1) + "?api_key=sk-secret-in-query"
	closedServer.Close()

	var logs bytes.Buffer
	origWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() {
		log.SetOutput(origWriter)
	})

	r := gin.New()
	r.POST("/api/vectors/channels/:id/models", GetChannelModels(cfgManager))

	body, err := json.Marshal(GetModelsRequest{
		Key:     "sk-request-key-do-not-log",
		BaseURL: sensitiveBaseURL,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/vectors/channels/0/models", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d, body=%s", w.Code, w.Body.String())
	}

	logText := logs.String()
	if !strings.Contains(logText, "using temporary baseUrl") || !strings.Contains(logText, "request failed") {
		t.Fatalf("expected model probe logs, got: %s", logText)
	}
	for _, leaked := range []string{
		sensitiveBaseURL,
		"sk-secret-in-url",
		"sk-secret-in-query",
		"sk-request-key-do-not-log",
	} {
		if strings.Contains(logText, leaked) {
			t.Fatalf("model probe log leaked %q: %s", leaked, logText)
		}
	}
}

func TestBuildEmbeddingsRequestBodyStripsClientStreamField(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStream bool
	}{
		{name: "stream true", body: `{"model":"text-embedding-3-small","input":"hello","stream":true}`, wantStream: false},
		{name: "stream false", body: `{"model":"text-embedding-3-small","input":"hello","stream":false}`, wantStream: false},
		{name: "no stream field", body: `{"model":"text-embedding-3-small","input":"hello"}`, wantStream: false},
		{name: "stream string", body: `{"model":"text-embedding-3-small","input":"hello","stream":"true"}`, wantStream: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := buildEmbeddingsRequestBody([]byte(tt.body), "text-embedding-3-small", "text-embedding-3-small")
			if err != nil {
				t.Fatalf("buildEmbeddingsRequestBody() error = %v", err)
			}
			var payload map[string]interface{}
			if err := json.Unmarshal(out, &payload); err != nil {
				t.Fatalf("unmarshal output: %v", err)
			}
			if _, exists := payload["stream"]; exists != tt.wantStream {
				t.Fatalf("stream field exists = %v, want %v, body=%s", exists, tt.wantStream, string(out))
			}
			if got := payload["model"]; got != "text-embedding-3-small" {
				t.Fatalf("model = %v, want text-embedding-3-small", got)
			}
			if got := payload["input"]; got != "hello" {
				t.Fatalf("input = %v, want hello", got)
			}
		})
	}
}

func TestHandlerStripsStreamFieldBeforeForwardingToUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	var upstreamBody []byte
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
			return
		}
		upstreamBody = body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.1],"index":0}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer upstreamServer.Close()

	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:        "openai-vectors",
		ServiceType: "openai",
		BaseURL:     upstreamServer.URL,
		APIKeys:     []string{"sk-openai"},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream() error = %v", err)
	}

	sch := newVectorsTestScheduler(cfgManager, metrics.NewMetricsManager())
	w := serveVectorsEmbeddingRequest(cfgManager, sch, `{"model":"text-embedding-3-small","input":"hello","stream":true}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	var forwarded map[string]interface{}
	if err := json.Unmarshal(upstreamBody, &forwarded); err != nil {
		t.Fatalf("unmarshal forwarded body: %v", err)
	}
	if _, exists := forwarded["stream"]; exists {
		t.Fatalf("upstream body should not contain stream field, got: %s", string(upstreamBody))
	}
	if got := forwarded["model"]; got != "text-embedding-3-small" {
		t.Fatalf("upstream model = %v, want text-embedding-3-small", got)
	}
	if got := forwarded["input"]; got != "hello" {
		t.Fatalf("upstream input = %v, want hello", got)
	}
}

func TestEmbeddingNormalizedStateThreeValues(t *testing.T) {
	tests := []struct {
		name  string
		input *bool
		want  int
	}{
		{name: "nil returns -1", input: nil, want: -1},
		{name: "false returns 0", input: boolPtr(false), want: 0},
		{name: "true returns 1", input: boolPtr(true), want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := embeddingNormalizedState(tt.input)
			if got != tt.want {
				t.Fatalf("embeddingNormalizedState(%v) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestEmbeddingCompatibilityKeyForRejectsInvalidSupportedDimensions(t *testing.T) {
	tests := []struct {
		name       string
		capability config.EmbeddingCapability
		reqDims    int
		wantValid  bool
	}{
		{
			name:       "negative default dimensions",
			capability: config.EmbeddingCapability{Dimensions: -1},
			reqDims:    0,
			wantValid:  false,
		},
		{
			name:       "zero in supported dimensions",
			capability: config.EmbeddingCapability{Dimensions: 1536, SupportedDimensions: []int{512, 0, 1536}},
			reqDims:    0,
			wantValid:  false,
		},
		{
			name:       "negative in supported dimensions",
			capability: config.EmbeddingCapability{Dimensions: 1536, SupportedDimensions: []int{-1, 1536}},
			reqDims:    0,
			wantValid:  false,
		},
		{
			name:       "unsupported request dimensions",
			capability: config.EmbeddingCapability{Dimensions: 1536, SupportedDimensions: []int{1024, 1536}},
			reqDims:    512,
			wantValid:  false,
		},
		{
			name:       "valid default dimensions",
			capability: config.EmbeddingCapability{Dimensions: 1536, SupportedDimensions: []int{1024, 1536}},
			reqDims:    0,
			wantValid:  true,
		},
		{
			name:       "valid request dimensions in supported list",
			capability: config.EmbeddingCapability{Dimensions: 1536, SupportedDimensions: []int{512, 1024, 1536}},
			reqDims:    512,
			wantValid:  true,
		},
		{
			name:       "valid request dimensions matches default",
			capability: config.EmbeddingCapability{Dimensions: 1536, SupportedDimensions: []int{1024, 1536}},
			reqDims:    1536,
			wantValid:  true,
		},
		{
			name:       "missing space id falls back to actual model",
			capability: config.EmbeddingCapability{Dimensions: 1536},
			reqDims:    0,
			wantValid:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved := config.ResolvedEmbeddingCapability{
				Capability:  tt.capability,
				ActualModel: "test-model",
				Known:       true,
			}
			_, ok, _ := embeddingCompatibilityKeyFor(resolved, tt.reqDims)
			if ok != tt.wantValid {
				t.Fatalf("embeddingCompatibilityKeyFor() valid = %v, want %v", ok, tt.wantValid)
			}
		})
	}
}

func TestHandlerNormalizedInconsistencyBlocksFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	var primaryAttempts int32
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&primaryAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"primary failed"}}`))
	}))
	defer primaryServer.Close()

	var secondaryAttempts int32
	secondaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondaryAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.9],"index":0}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer secondaryServer.Close()

	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "secondary-normalized-false",
		ServiceType:  "openai",
		BaseURL:      secondaryServer.URL,
		APIKeys:      []string{"sk-secondary"},
		Priority:     2,
		ModelMapping: map[string]string{"embed-public": "embedding-model-b"},
		EmbeddingCapabilities: map[string]config.EmbeddingCapability{
			"embedding-model-b": {EmbeddingSpaceID: "shared-space", Dimensions: 1536, Normalized: boolPtr(false)},
		},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(secondary) error = %v", err)
	}
	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "primary-normalized-true",
		ServiceType:  "openai",
		BaseURL:      primaryServer.URL,
		APIKeys:      []string{"sk-primary"},
		Priority:     1,
		ModelMapping: map[string]string{"embed-public": "embedding-model-a"},
		EmbeddingCapabilities: map[string]config.EmbeddingCapability{
			"embedding-model-a": {EmbeddingSpaceID: "shared-space", Dimensions: 1536, Normalized: boolPtr(true)},
		},
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(primary) error = %v", err)
	}

	vectorsMetrics := metrics.NewMetricsManager()
	sch := newVectorsTestScheduler(cfgManager, vectorsMetrics)
	w := serveVectorsEmbeddingRequest(cfgManager, sch, `{"model":"embed-public","input":"hello"}`)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d, body=%s", w.Code, w.Body.String())
	}
	if got := atomic.LoadInt32(&primaryAttempts); got != 1 {
		t.Fatalf("primary attempts = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&secondaryAttempts); got != 0 {
		t.Fatalf("secondary attempts = %d, want 0 (normalized mismatch should block fallback)", got)
	}
}

func TestHandlerModelNotFoundTriggersFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	var primaryAttempts int32
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&primaryAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"model_not_found","message":"No available channel for model text-embedding-3-small under group vectors","type":"new_api_error"}}`))
	}))
	defer primaryServer.Close()

	var secondaryAttempts int32
	secondaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondaryAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.42],"index":0}],"usage":{"prompt_tokens":5,"total_tokens":5}}`))
	}))
	defer secondaryServer.Close()

	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:        "secondary-vectors",
		ServiceType: "openai",
		BaseURL:     secondaryServer.URL,
		APIKeys:     []string{"sk-secondary"},
		Priority:    2,
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(secondary) error = %v", err)
	}
	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:        "primary-vectors",
		ServiceType: "openai",
		BaseURL:     primaryServer.URL,
		APIKeys:     []string{"sk-primary"},
		Priority:    1,
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(primary) error = %v", err)
	}

	vectorsMetrics := metrics.NewMetricsManager()
	sch := newVectorsTestScheduler(cfgManager, vectorsMetrics)
	w := serveVectorsEmbeddingRequest(cfgManager, sch, `{"model":"text-embedding-3-small","input":"hello"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"embedding":[0.42]`) {
		t.Fatalf("expected secondary embeddings response, got: %s", w.Body.String())
	}
	if got := atomic.LoadInt32(&primaryAttempts); got != 1 {
		t.Fatalf("primary attempts = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&secondaryAttempts); got != 1 {
		t.Fatalf("secondary attempts = %d, want 1", got)
	}

	primaryMetrics := vectorsMetrics.GetKeyMetrics(primaryServer.URL, "sk-primary", "openai")
	if primaryMetrics == nil {
		t.Fatal("expected primary key metrics to be recorded")
	}
	if primaryMetrics.RequestCount != 1 {
		t.Fatalf("primary request count = %d, want 1", primaryMetrics.RequestCount)
	}
	if primaryMetrics.FailureCount != 1 {
		t.Fatalf("primary failure count = %d, want 1", primaryMetrics.FailureCount)
	}
}

func TestHandlerEmptyResponseBodyTriggersFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newVectorsTestConfigManager(t)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	var primaryAttempts int32
	primaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&primaryAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte{})
	}))
	defer primaryServer.Close()

	var secondaryAttempts int32
	secondaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&secondaryAttempts, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.77],"index":0}],"usage":{"prompt_tokens":3,"total_tokens":3}}`))
	}))
	defer secondaryServer.Close()

	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:        "secondary-vectors",
		ServiceType: "openai",
		BaseURL:     secondaryServer.URL,
		APIKeys:     []string{"sk-secondary"},
		Priority:    2,
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(secondary) error = %v", err)
	}
	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:        "primary-vectors",
		ServiceType: "openai",
		BaseURL:     primaryServer.URL,
		APIKeys:     []string{"sk-primary"},
		Priority:    1,
	}); err != nil {
		t.Fatalf("AddVectorsUpstream(primary) error = %v", err)
	}

	vectorsMetrics := metrics.NewMetricsManager()
	sch := newVectorsTestScheduler(cfgManager, vectorsMetrics)
	w := serveVectorsEmbeddingRequest(cfgManager, sch, `{"model":"text-embedding-3-small","input":"hello"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"embedding":[0.77]`) {
		t.Fatalf("expected secondary embeddings response, got: %s", w.Body.String())
	}
	if got := atomic.LoadInt32(&primaryAttempts); got != 1 {
		t.Fatalf("primary attempts = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&secondaryAttempts); got != 1 {
		t.Fatalf("secondary attempts = %d, want 1", got)
	}

	primaryMetrics := vectorsMetrics.GetKeyMetrics(primaryServer.URL, "sk-primary", "openai")
	if primaryMetrics == nil {
		t.Fatal("expected primary key metrics to be recorded")
	}
	if primaryMetrics.RequestCount != 1 || primaryMetrics.FailureCount != 1 {
		t.Fatalf("primary metrics counts = requests:%d failures:%d, want 1/1", primaryMetrics.RequestCount, primaryMetrics.FailureCount)
	}
}

func BenchmarkEmbeddingCompatibilityFilter(b *testing.B) {
	b.Run("all_available_same_space_64", func(b *testing.B) {
		benchmarkEmbeddingCompatibilityFilter(b, 64, false, false)
	})

	b.Run("all_available_same_space_256", func(b *testing.B) {
		benchmarkEmbeddingCompatibilityFilter(b, 256, false, false)
	})

	b.Run("mixed_spaces_256", func(b *testing.B) {
		benchmarkEmbeddingCompatibilityFilter(b, 256, true, false)
	})

	b.Run("mixed_spaces_with_unavailable_256", func(b *testing.B) {
		benchmarkEmbeddingCompatibilityFilter(b, 256, true, true)
	})
}

func benchmarkEmbeddingCompatibilityFilter(b *testing.B, channelCount int, mixedSpaces bool, withUnavailable bool) {
	b.Helper()
	channels, upstreamFor, available := makeEmbeddingCompatibilityBenchmarkInputs(channelCount, mixedSpaces, withUnavailable)
	filter := newEmbeddingCompatibilityFilter(nil, "embed-public", 1536)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		candidates, err := filter(channels, upstreamFor, available)
		if err != nil {
			b.Fatalf("newEmbeddingCompatibilityFilter() unexpected err = %v", err)
		}
		if len(candidates) == 0 {
			b.Fatalf("expected at least one candidate")
		}
	}
}

func makeEmbeddingCompatibilityBenchmarkInputs(
	channelCount int,
	mixedSpaces bool,
	withUnavailable bool,
) ([]scheduler.ChannelInfo, func(scheduler.ChannelInfo) *config.UpstreamConfig, func(scheduler.ChannelInfo, *config.UpstreamConfig) bool) {
	channels := make([]scheduler.ChannelInfo, 0, channelCount)
	upstreamByIndex := make(map[int]*config.UpstreamConfig, channelCount)
	for i := 0; i < channelCount; i++ {
		status := "active"
		if withUnavailable && i%7 == 0 {
			status = "suspended"
		}

		actualModel := "embedding-model-shared"
		spaceID := "shared-space"
		if mixedSpaces {
			actualModel = "embedding-model-" + strconv.Itoa(i%16)
			spaceID = "space-" + strconv.Itoa(i%32)
			if i%3 == 0 {
				spaceID = "shared-space"
			}
		}

		upstream := &config.UpstreamConfig{
			Name:         "vectors-benchmark-" + strconv.Itoa(i),
			ServiceType:  "openai",
			BaseURL:      "https://example.com/v" + strconv.Itoa(i),
			APIKeys:      []string{"sk-bench-" + strconv.Itoa(i)},
			Priority:     i + 1,
			Status:       status,
			ModelMapping: map[string]string{"embed-public": actualModel},
			EmbeddingCapabilities: map[string]config.EmbeddingCapability{
				actualModel: {
					EmbeddingSpaceID: spaceID,
					Dimensions:       1536,
					Normalized:       boolPtr(true),
				},
			},
		}
		upstreamByIndex[i] = upstream
		channels = append(channels, scheduler.ChannelInfo{
			Index:    i,
			Name:     upstream.Name,
			Status:   status,
			Priority: i,
		})
	}

	return channels,
		func(ch scheduler.ChannelInfo) *config.UpstreamConfig {
			return upstreamByIndex[ch.Index]
		},
		func(ch scheduler.ChannelInfo, upstream *config.UpstreamConfig) bool {
			return ch.Status == "active" && upstream != nil && len(upstream.APIKeys) > 0
		}
}

func BenchmarkHandlerVectorsEmbeddingPipeline(b *testing.B) {
	b.Run("single_channel_success", func(b *testing.B) {
		benchmarkHandlerVectorsEmbeddingPipeline(b, false)
	})

	b.Run("four_channel_strict_compatibility", func(b *testing.B) {
		benchmarkHandlerVectorsEmbeddingPipeline(b, true)
	})
}

func benchmarkHandlerVectorsEmbeddingPipeline(b *testing.B, withFallback bool) {
	b.Helper()
	gin.SetMode(gin.TestMode)

	cfgManager := newVectorsTestConfigManagerFromBench(b)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	activeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if withFallback {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"primary temporary failure"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.4],"index":0}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer activeServer.Close()

	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"object":"embedding","embedding":[0.4],"index":0}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer fallbackServer.Close()

	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "primary",
		ServiceType:  "openai",
		BaseURL:      activeServer.URL,
		APIKeys:      []string{"sk-bench-primary"},
		Priority:     1,
		ModelMapping: map[string]string{"embed-public": "embedding-model-a"},
		EmbeddingCapabilities: map[string]config.EmbeddingCapability{
			"embedding-model-a": {EmbeddingSpaceID: "shared-space", Dimensions: 1536, Normalized: boolPtr(true)},
		},
	}); err != nil {
		b.Fatalf("AddVectorsUpstream(primary) error = %v", err)
	}
	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "secondary",
		ServiceType:  "openai",
		BaseURL:      fallbackServer.URL,
		APIKeys:      []string{"sk-bench-secondary"},
		Priority:     2,
		ModelMapping: map[string]string{"embed-public": "embedding-model-a"},
		EmbeddingCapabilities: map[string]config.EmbeddingCapability{
			"embedding-model-a": {EmbeddingSpaceID: "shared-space", Dimensions: 1536, Normalized: boolPtr(true)},
		},
	}); err != nil {
		b.Fatalf("AddVectorsUpstream(secondary) error = %v", err)
	}
	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "tertiary",
		ServiceType:  "openai",
		BaseURL:      fallbackServer.URL,
		APIKeys:      []string{"sk-bench-tertiary"},
		Priority:     3,
		ModelMapping: map[string]string{"embed-public": "embedding-model-a"},
		EmbeddingCapabilities: map[string]config.EmbeddingCapability{
			"embedding-model-a": {EmbeddingSpaceID: "shared-space", Dimensions: 1536, Normalized: boolPtr(true)},
		},
	}); err != nil {
		b.Fatalf("AddVectorsUpstream(tertiary) error = %v", err)
	}
	if err := cfgManager.AddVectorsUpstream(config.UpstreamConfig{
		Name:         "fallback-extra",
		ServiceType:  "openai",
		BaseURL:      fallbackServer.URL,
		APIKeys:      []string{"sk-bench-extra"},
		Priority:     4,
		ModelMapping: map[string]string{"embed-public": "embedding-model-a"},
		EmbeddingCapabilities: map[string]config.EmbeddingCapability{
			"embedding-model-a": {EmbeddingSpaceID: "shared-space", Dimensions: 1536, Normalized: boolPtr(true)},
		},
	}); err != nil {
		b.Fatalf("AddVectorsUpstream(fallback-extra) error = %v", err)
	}

	r := gin.New()
	r.POST("/v1/embeddings", Handler(newVectorsTestEnvConfig(), cfgManager, newVectorsTestScheduler(cfgManager, metrics.NewMetricsManager())))

	body := []byte(`{"model":"embed-public","input":"hello benchmark embedding payload","dimensions":1536}`)

	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-proxy-key")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			b.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
		}
	}
}

func newVectorsTestConfigManagerFromBench(b *testing.B) *config.ConfigManager {
	b.Helper()
	cfgFile := filepath.Join(b.TempDir(), "config.json")
	if err := os.WriteFile(cfgFile, []byte(`{"upstream":[],"vectorsUpstream":[]}`), 0o600); err != nil {
		b.Fatalf("write config: %v", err)
	}
	cfgManager, err := config.NewConfigManager(cfgFile, "")
	if err != nil {
		b.Fatalf("config manager: %v", err)
	}
	return cfgManager
}
