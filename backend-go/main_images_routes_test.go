package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

func TestRegisterImagesAdminRoutesIncludesMetricsEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfgManager := newTestConfigManager(t)
	messagesMetrics := newTestMetricsManager(t)
	responsesMetrics := newTestMetricsManager(t)
	geminiMetrics := newTestMetricsManager(t)
	chatMetrics := newTestMetricsManager(t)
	imagesMetrics := newTestMetricsManager(t)
	traceAffinity := session.NewTraceAffinityManager()
	t.Cleanup(traceAffinity.Stop)
	urlManager := warmup.NewURLManager(30*time.Second, 3)

	channelScheduler := scheduler.NewChannelScheduler(
		cfgManager,
		messagesMetrics,
		responsesMetrics,
		geminiMetrics,
		chatMetrics,
		imagesMetrics,
		traceAffinity,
		urlManager,
	)
	if channelScheduler.GetImagesMetricsManager() != imagesMetrics {
		t.Fatal("images scheduler metrics manager does not match images API metrics manager")
	}

	r := gin.New()
	registerImagesAdminRoutes(r.Group("/api"), cfgManager, channelScheduler, imagesMetrics)

	registered := make(map[string]bool)
	for _, route := range r.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	for _, route := range []string{
		"GET /api/images/channels/metrics",
		"GET /api/images/channels/metrics/history",
		"GET /api/images/channels/:id/keys/metrics/history",
		"GET /api/images/global/stats/history",
	} {
		if !registered[route] {
			t.Fatalf("missing route %s", route)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/images/global/stats/history?duration=6h", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want=200, body=%s", w.Code, w.Body.String())
	}
}

func newTestConfigManager(t *testing.T) *config.ConfigManager {
	t.Helper()

	cfg := config.Config{
		ImagesUpstream: []config.UpstreamConfig{{
			Name:    "images-test",
			BaseURL: "https://example.com",
			APIKeys: []string{"sk-test"},
			Status:  "active",
		}},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	configFile := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configFile, data, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile)
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	t.Cleanup(func() {
		if err := cfgManager.Close(); err != nil {
			t.Fatalf("close config manager: %v", err)
		}
	})
	return cfgManager
}

func newTestMetricsManager(t *testing.T) *metrics.MetricsManager {
	t.Helper()

	manager := metrics.NewMetricsManager()
	t.Cleanup(manager.Stop)
	return manager
}
