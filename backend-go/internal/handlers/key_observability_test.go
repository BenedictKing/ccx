package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/gin-gonic/gin"
)

func TestKeyObservabilityAndDiagnosticsRequireAdminAndUseStableIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key := "sk-secret-test-observability-12345"
	cfg := config.Config{ChatUpstream: []config.UpstreamConfig{
		{ChannelUID: "other", Name: "other", BaseURL: "https://other.example", APIKeys: []string{"another-key"}, ServiceType: "openai"},
		{ChannelUID: "stable", Name: "wanted", BaseURL: "https://wanted.example", APIKeys: []string{key}, ServiceType: "openai"},
	}}
	path := filepath.Join(t.TempDir(), "config.json")
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewConfigManager(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	routing, _ := metrics.NewRoutingStoreWithRetention(nil, 14)
	defer routing.Close()
	operations, _ := metrics.NewKeyOperationStore(nil, 14)
	defer operations.Close()
	manager.SetKeyOperationObserver(operations.Record)
	if err := manager.SuspendKey("Chat", 1, key); err != nil {
		t.Fatal(err)
	}
	if err := manager.ResumeKey("Chat", 1, key); err != nil {
		t.Fatal(err)
	}
	routing.StartRequest(metrics.RoutingRequest{GatewayRequestID: "retry", APIType: "chat", ReceivedAt: time.Now()})
	routing.StartAttempt("retry", metrics.RoutingAttempt{AttemptID: "failed-attempt", ChannelUID: "stable"})
	routing.SetAttemptKey("retry", "failed-attempt", metrics.RoutingKeyID(key), "masked")
	routing.FinishAttempt("retry", "failed-attempt", metrics.RoutingStatusFailed, 429, &metrics.RoutingAttemptDetails{ErrorSummary: "rate limited"})
	routing.FinishRequest("retry", metrics.RoutingStatusSuccess, "")
	env := &config.EnvConfig{EnableWebUI: true, ProxyAccessKey: "proxy", AdminAccessKey: "admin", MetricsRetentionDays: 366}
	router := gin.New()
	router.Use(middleware.WebAuthMiddleware(env, manager))
	group := router.Group("/api")
	RegisterKeyObservabilityRoutes(group, manager, routing, operations, nil)
	RegisterRoutingRoutes(group, routing, manager)
	group.GET("/diagnostics", GetDiagnostics(env, nil, routing, operations, DiagnosticVersion{Version: "test", GitCommit: "commit"}))
	get := func(path, auth string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("x-api-key", auth)
		router.ServeHTTP(w, req)
		return w
	}
	for _, endpoint := range []string{"/api/chat/keys/overview?channelUid=stable", "/api/chat/keys/operations?channelUid=stable", "/api/diagnostics"} {
		for _, auth := range []string{"", "proxy"} {
			if w := get(endpoint, auth); w.Code != 401 {
				t.Fatalf("%s: missing admin protection", endpoint)
			}
		}
		w := get(endpoint, "admin")
		if w.Code != 200 || strings.Contains(w.Body.String(), key) || strings.Contains(w.Body.String(), "https://wanted.example") {
			t.Fatalf("unsafe response: %d %s", w.Code, w.Body.String())
		}
	}
	var overview struct {
		ChannelUID string
		Keys       []keyOverview
	}
	_ = json.Unmarshal(get("/api/chat/keys/overview?channelUid=stable", "admin").Body.Bytes(), &overview)
	if overview.ChannelUID != "stable" || len(overview.Keys) != 1 || overview.Keys[0].State != "active" || overview.Keys[0].LatestFailure == nil || overview.Keys[0].LatestFailure.AttemptID != "failed-attempt" {
		t.Fatalf("wrong stable channel overview: %+v", overview)
	}
	for _, endpoint := range []string{"/api/chat/keys/overview?channelUid=missing", "/api/chat/keys/operations?channelUid=stable&keyId=plaintext", "/api/chat/keys/operations?channelUid=stable&limit=101"} {
		if w := get(endpoint, "admin"); w.Code < 400 {
			t.Fatal("invalid query accepted")
		}
	}
	w := get("/api/chat/models/routing/history?duration=retained&q=failed-attempt&keyId="+metrics.RoutingKeyID(key)+"&channelUid=stable", "admin")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "failed-attempt") {
		t.Fatalf("retained request navigation failed: %s", w.Body.String())
	}
}
