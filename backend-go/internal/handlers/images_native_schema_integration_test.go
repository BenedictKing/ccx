package handlers_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/handlers/images"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/gin-gonic/gin"
)

// 通义千问原生 Schema 的 generations 请求必须真正到达上游，且请求与响应字节都不被改写。
func TestImagesGenerationsNativeInputMessagesPassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const requestBody = `{"model":"qwen-image-plus","input":{"messages":[{"role":"user","content":[{"text":"一只猫"}]}]},"parameters":{"size":"1024*1024","n":1}}`
	const upstreamResponse = `{"output":{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":[{"image":"https://example.com/cat.png"}]}}]},"usage":{"image_count":1},"request_id":"req-1"}`

	var upstreamPath, upstreamBody, upstreamContentType string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		upstreamPath = r.URL.Path
		upstreamBody = string(body)
		upstreamContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(upstreamResponse))
	}))
	defer upstream.Close()

	cfg := config.Config{ImagesUpstream: []config.UpstreamConfig{{
		Name:                   "qwen",
		BaseURL:                upstream.URL,
		ServiceType:            "openai",
		Status:                 "active",
		CircuitBreakerDisabled: true,
		APIKeys:                []string{"good-key"},
	}}}
	path := filepath.Join(t.TempDir(), "config.json")
	payload, _ := json.Marshal(cfg)
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	cm, err := config.NewConfigManager(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()
	manager := metrics.NewMetricsManager()
	defer manager.Stop()
	sch := scheduler.NewChannelScheduler(cm, manager, manager, manager, manager, manager, session.NewTraceAffinityManager(), nil, manager)
	env := config.NewEnvConfig()
	env.ProxyAccessKey = "proxy-test"
	env.EnableRequestLogs = false
	store, _ := metrics.NewRoutingStore(nil)
	defer store.Close()

	router := gin.New()
	router.Use(common.RoutingStoreMiddleware(store))
	router.POST("/v1/images/generations", images.Handler(env, cm, sch))
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(requestBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer proxy-test")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("proxy=%d %s", recorder.Code, recorder.Body.String())
	}
	if upstreamPath != "/v1/images/generations" {
		t.Fatalf("upstream path=%q", upstreamPath)
	}
	if upstreamBody != requestBody {
		t.Fatalf("upstream body changed: %s", upstreamBody)
	}
	if upstreamContentType != "application/json" {
		t.Fatalf("upstream content type=%q", upstreamContentType)
	}
	if recorder.Body.String() != upstreamResponse {
		t.Fatalf("client response changed: %s", recorder.Body.String())
	}
}
