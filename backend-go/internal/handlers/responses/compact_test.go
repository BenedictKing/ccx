package responses

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/gin-gonic/gin"
)

func newCompactTestRouter(t *testing.T, upstreams []config.UpstreamConfig) (*gin.Engine, *scheduler.ChannelScheduler) {
	router, sch, _ := newCompactTestRouterWithConfig(t, upstreams)
	return router, sch
}

func newCompactTestRouterWithConfig(t *testing.T, upstreams []config.UpstreamConfig) (*gin.Engine, *scheduler.ChannelScheduler, *config.ConfigManager) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfgManager := setupResponsesTestConfigManager(t, upstreams)
	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	traceAffinity := session.NewTraceAffinityManager()

	t.Cleanup(func() {
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		geminiMetrics.Stop()
		chatMetrics.Stop()
		imagesMetrics.Stop()
		traceAffinity.Stop()
	})

	sch := scheduler.NewChannelScheduler(
		cfgManager,
		messagesMetrics,
		responsesMetrics,
		geminiMetrics,
		chatMetrics,
		imagesMetrics,
		traceAffinity,
		nil,
	)

	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret-key",
		MaxRequestBodySize: 1024 * 1024,
	}

	r := gin.New()
	r.POST("/v1/responses/compact", CompactHandler(envCfg, cfgManager, nil, sch))
	return r, sch, cfgManager
}

func performCompactRequest(t *testing.T, router *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "secret-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestCompactHandler_SingleChannelFailureRecordsMetricsAndLogs(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"unauthorized"}`)
	}))
	defer upstream.Close()

	router, sch := newCompactTestRouter(t, []config.UpstreamConfig{{
		Name:        "single-fail",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-test"},
		ServiceType: "responses",
		Status:      "active",
	}})

	w := performCompactRequest(t, router, `{"model":"gpt-5","input":"hello"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusUnauthorized, w.Body.String())
	}

	logs := sch.GetChannelLogStore(scheduler.ChannelKindResponses).Get(0)
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}
	if logs[0].Success {
		t.Fatalf("log success = true, want false")
	}
	if logs[0].StatusCode != http.StatusUnauthorized {
		t.Fatalf("log status = %d, want %d", logs[0].StatusCode, http.StatusUnauthorized)
	}
	if logs[0].Model != "gpt-5" {
		t.Fatalf("log model = %q, want gpt-5", logs[0].Model)
	}
	if logs[0].InterfaceType != "Responses" {
		t.Fatalf("log interfaceType = %q, want Responses", logs[0].InterfaceType)
	}
	if !strings.Contains(logs[0].ErrorInfo, "unauthorized") {
		t.Fatalf("log errorInfo = %q, want contains unauthorized", logs[0].ErrorInfo)
	}

	metricsResp := sch.GetResponsesMetricsManager().ToResponseMultiURL(0, []string{upstream.URL}, []string{"sk-test"}, "responses", 0)
	if metricsResp.RequestCount != 1 {
		t.Fatalf("requestCount = %d, want 1", metricsResp.RequestCount)
	}
	if metricsResp.FailureCount != 1 {
		t.Fatalf("failureCount = %d, want 1", metricsResp.FailureCount)
	}
	if metricsResp.SuccessCount != 0 {
		t.Fatalf("successCount = %d, want 0", metricsResp.SuccessCount)
	}
}

func TestCompactHandler_MultiChannelRespectsSupportedModels(t *testing.T) {
	skippedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"resp_skipped","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`)
	}))
	defer skippedUpstream.Close()

	selectedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"resp_selected","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`)
	}))
	defer selectedUpstream.Close()

	router, sch := newCompactTestRouter(t, []config.UpstreamConfig{
		{
			Name:            "skip-image-models",
			BaseURL:         skippedUpstream.URL,
			APIKeys:         []string{"sk-skip"},
			ServiceType:     "responses",
			Status:          "active",
			SupportedModels: []string{"gpt-4*", "!*image*"},
		},
		{
			Name:            "allow-image-models",
			BaseURL:         selectedUpstream.URL,
			APIKeys:         []string{"sk-allow"},
			ServiceType:     "responses",
			Status:          "active",
			SupportedModels: []string{"gpt-4*"},
		},
	})

	w := performCompactRequest(t, router, `{"model":"gpt-4-image-preview","input":"hello"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	skippedLogs := sch.GetChannelLogStore(scheduler.ChannelKindResponses).Get(0)
	if len(skippedLogs) != 0 {
		t.Fatalf("skipped channel logs count = %d, want 0", len(skippedLogs))
	}

	selectedLogs := sch.GetChannelLogStore(scheduler.ChannelKindResponses).Get(1)
	if len(selectedLogs) != 1 {
		t.Fatalf("selected channel logs count = %d, want 1", len(selectedLogs))
	}
	if !selectedLogs[0].Success {
		t.Fatal("selected channel success = false, want true")
	}
	if selectedLogs[0].Model != "gpt-4-image-preview" {
		t.Fatalf("selected channel model = %q, want gpt-4-image-preview", selectedLogs[0].Model)
	}
}

func TestCompactHandler_ReturnsSelectionErrorWhenNoChannelSupportsModel(t *testing.T) {
	router, _ := newCompactTestRouter(t, []config.UpstreamConfig{
		{
			Name:            "exclude-image-a",
			BaseURL:         "https://example.com/v1",
			APIKeys:         []string{"sk-test-a"},
			ServiceType:     "responses",
			Status:          "active",
			SupportedModels: []string{"gpt-4*", "!*image*"},
		},
		{
			Name:            "exclude-image-b",
			BaseURL:         "https://example.net/v1",
			APIKeys:         []string{"sk-test-b"},
			ServiceType:     "responses",
			Status:          "active",
			SupportedModels: []string{"gpt-4*", "!*image*"},
		},
	})

	w := performCompactRequest(t, router, `{"model":"gpt-4-image-preview","input":"hello"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusServiceUnavailable, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "没有 Responses 渠道支持模型") {
		t.Fatalf("body = %s, want contains selection error", w.Body.String())
	}
}

func TestCompactHandler_MultiChannelFailoverRecordsFailedChannelLogs(t *testing.T) {
	failedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"unauthorized"}`)
	}))
	defer failedUpstream.Close()

	successUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"resp_compact_ok","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`)
	}))
	defer successUpstream.Close()

	router, sch := newCompactTestRouter(t, []config.UpstreamConfig{
		{
			Name:        "fail-first",
			BaseURL:     failedUpstream.URL,
			APIKeys:     []string{"sk-fail"},
			ServiceType: "responses",
			Status:      "active",
		},
		{
			Name:        "succeed-second",
			BaseURL:     successUpstream.URL,
			APIKeys:     []string{"sk-ok"},
			ServiceType: "responses",
			Status:      "active",
		},
	})

	w := performCompactRequest(t, router, `{"model":"gpt-5","input":"hello"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	failedLogs := sch.GetChannelLogStore(scheduler.ChannelKindResponses).Get(0)
	if len(failedLogs) != 1 {
		t.Fatalf("failed channel logs count = %d, want 1", len(failedLogs))
	}
	if failedLogs[0].Success {
		t.Fatalf("failed channel log success = true, want false")
	}
	if failedLogs[0].StatusCode != http.StatusUnauthorized {
		t.Fatalf("failed channel status = %d, want %d", failedLogs[0].StatusCode, http.StatusUnauthorized)
	}

	successLogs := sch.GetChannelLogStore(scheduler.ChannelKindResponses).Get(1)
	if len(successLogs) != 1 {
		t.Fatalf("success channel logs count = %d, want 1", len(successLogs))
	}
	if !successLogs[0].Success {
		t.Fatalf("success channel log success = false, want true")
	}
	if successLogs[0].StatusCode != http.StatusOK {
		t.Fatalf("success channel status = %d, want %d", successLogs[0].StatusCode, http.StatusOK)
	}

	failedMetrics := sch.GetResponsesMetricsManager().ToResponseMultiURL(0, []string{failedUpstream.URL}, []string{"sk-fail"}, "responses", 0)
	if failedMetrics.RequestCount != 1 || failedMetrics.FailureCount != 1 {
		t.Fatalf("failed channel metrics = %+v, want requestCount=1 failureCount=1", failedMetrics)
	}

	successMetrics := sch.GetResponsesMetricsManager().ToResponseMultiURL(1, []string{successUpstream.URL}, []string{"sk-ok"}, "responses", 0)
	if successMetrics.RequestCount != 1 || successMetrics.SuccessCount != 1 {
		t.Fatalf("success channel metrics = %+v, want requestCount=1 successCount=1", successMetrics)
	}
}

func TestCompactHandler_ModelRouteUnavailableSkipsCooldownAndBreaker(t *testing.T) {
	var attempts int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		if attempts == 1 {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"model_not_found","message":"No available channel for model gpt-5 under group default"}}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"resp_compact_ok","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`)
	}))
	defer upstream.Close()

	router, sch, cfgManager := newCompactTestRouterWithConfig(t, []config.UpstreamConfig{{
		Name:        "route-miss-then-ok",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-route-miss", "sk-ok"},
		ServiceType: "responses",
		Status:      "active",
	}})

	w := performCompactRequest(t, router, `{"model":"gpt-5","input":"hello"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if cfgManager.IsKeyFailed("sk-route-miss", "Responses") {
		t.Fatal("route-miss key was marked failed, want no cooldown")
	}

	metricsResp := sch.GetResponsesMetricsManager().ToResponseMultiURL(0, []string{upstream.URL}, []string{"sk-route-miss", "sk-ok"}, "responses", 0)
	if metricsResp.FailureCount != 0 {
		t.Fatalf("failureCount = %d, want 0", metricsResp.FailureCount)
	}
	if metricsResp.SuccessCount != 1 {
		t.Fatalf("successCount = %d, want 1", metricsResp.SuccessCount)
	}

	logs := sch.GetChannelLogStore(scheduler.ChannelKindResponses).Get(0)
	if len(logs) != 2 {
		t.Fatalf("logs count = %d, want 2", len(logs))
	}
	var foundRouteMiss, foundSuccess bool
	for _, entry := range logs {
		if !entry.Success && entry.StatusCode == http.StatusNotFound {
			foundRouteMiss = true
		}
		if entry.Success && entry.StatusCode == http.StatusOK {
			foundSuccess = true
		}
	}
	if !foundRouteMiss {
		t.Fatalf("logs = %+v, want failed 404 route miss log", logs)
	}
	if !foundSuccess {
		t.Fatalf("logs = %+v, want success 200 log", logs)
	}
}

func TestCompactHandler_MultiChannelModelRouteUnavailableSkipsCooldownAndBreaker(t *testing.T) {
	var attempts int
	firstChannel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		if attempts == 1 {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"model_not_found","message":"No available channel for model gpt-5 under group default"}}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"resp_compact_ok","status":"completed","output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}`)
	}))
	defer firstChannel.Close()

	secondChannel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("unexpected request to second channel")
	}))
	defer secondChannel.Close()

	router, sch, cfgManager := newCompactTestRouterWithConfig(t, []config.UpstreamConfig{
		{
			Name:        "route-miss-then-ok",
			BaseURL:     firstChannel.URL,
			APIKeys:     []string{"sk-route-miss", "sk-ok"},
			ServiceType: "responses",
			Status:      "active",
		},
		{
			Name:        "unused-channel",
			BaseURL:     secondChannel.URL,
			APIKeys:     []string{"sk-unused"},
			ServiceType: "responses",
			Status:      "active",
		},
	})

	w := performCompactRequest(t, router, `{"model":"gpt-5","input":"hello"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if cfgManager.IsKeyFailed("sk-route-miss", "Responses") {
		t.Fatal("route-miss key was marked failed, want no cooldown")
	}

	metricsResp := sch.GetResponsesMetricsManager().ToResponseMultiURL(0, []string{firstChannel.URL}, []string{"sk-route-miss", "sk-ok"}, "responses", 0)
	if metricsResp.FailureCount != 0 {
		t.Fatalf("failureCount = %d, want 0", metricsResp.FailureCount)
	}
	if metricsResp.SuccessCount != 1 {
		t.Fatalf("successCount = %d, want 1", metricsResp.SuccessCount)
	}
}
