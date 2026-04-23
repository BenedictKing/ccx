package messages

import (
	"context"
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

func setupModelsConfigManager(t *testing.T, cfg config.Config) *config.ConfigManager {
	t.Helper()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	tmpFile := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		t.Fatalf("写入配置文件失败: %v", err)
	}
	cm, err := config.NewConfigManager(tmpFile)
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })
	return cm
}

func newModelsTestScheduler(cfgManager *config.ConfigManager) *scheduler.ChannelScheduler {
	traceAffinity := session.NewTraceAffinityManager()
	metricsManagers := []*metrics.MetricsManager{
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
	}

	schedulerInstance := scheduler.NewChannelScheduler(
		cfgManager,
		metricsManagers[0],
		metricsManagers[1],
		metricsManagers[2],
		metricsManagers[3],
		traceAffinity,
		nil,
	)

	return schedulerInstance
}

func newModelsRouterForAggregate(envCfg *config.EnvConfig, cfgManager *config.ConfigManager, sch *scheduler.ChannelScheduler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/models", ModelsHandler(envCfg, cfgManager, sch))
	r.GET("/:routePrefix/v1/models", ModelsHandler(envCfg, cfgManager, sch))
	r.GET("/v1/models/:model", ModelsDetailHandler(envCfg, cfgManager, sch))
	r.GET("/:routePrefix/v1/models/:model", ModelsDetailHandler(envCfg, cfgManager, sch))
	return r
}

func TestModelsHandler_UsesActiveKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-active" {
			t.Fatalf("Authorization = %q, want active key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-active","object":"model"}]}`))
	}))
	defer upstream.Close()

	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:        "messages-active",
			BaseURL:     upstream.URL,
			APIKeys:     []string{"sk-active"},
			ServiceType: "claude",
		}},
	})
	sch := newModelsTestScheduler(cfgManager)
	router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cfgManager, sch)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); body == "" || body == "{}" {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestModelsHandler_FallbackToDisabledKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-disabled" {
			t.Fatalf("Authorization = %q, want disabled fallback key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-disabled","object":"model"}]}`))
	}))
	defer upstream.Close()

	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:    "messages-disabled-fallback",
			BaseURL: upstream.URL,
			DisabledAPIKeys: []config.DisabledKeyInfo{{
				Key:        "sk-disabled",
				Reason:     "authentication_error",
				Message:    "invalid key",
				DisabledAt: "2026-04-15T00:00:00Z",
			}},
			ServiceType: "claude",
			Status:      "active",
		}},
	})
	sch := newModelsTestScheduler(cfgManager)
	router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cfgManager, sch)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); body == "" || body == "{}" {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestModelsHandler_FallbackToDisabledKeyRespectsRoutePrefix(t *testing.T) {
	matchedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-prefix" {
			t.Fatalf("Authorization = %q, want prefixed disabled fallback key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-prefix","object":"model"}]}`))
	}))
	defer matchedUpstream.Close()

	defaultUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("default route fallback should not be used for prefixed request")
	}))
	defer defaultUpstream.Close()

	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:    "default-disabled",
				BaseURL: defaultUpstream.URL,
				DisabledAPIKeys: []config.DisabledKeyInfo{{
					Key:        "sk-default",
					Reason:     "authentication_error",
					Message:    "invalid key",
					DisabledAt: "2026-04-15T00:00:00Z",
				}},
				ServiceType: "claude",
				Status:      "active",
			},
			{
				Name:        "prefixed-disabled",
				BaseURL:     matchedUpstream.URL,
				RoutePrefix: "kimi",
				DisabledAPIKeys: []config.DisabledKeyInfo{{
					Key:        "sk-prefix",
					Reason:     "authentication_error",
					Message:    "invalid key",
					DisabledAt: "2026-04-15T00:00:00Z",
				}},
				ServiceType: "claude",
				Status:      "active",
			},
		},
	})
	sch := newModelsTestScheduler(cfgManager)
	router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cfgManager, sch)

	req := httptest.NewRequest(http.MethodGet, "/kimi/v1/models", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestModelsHandler_FallbackToDisabledKeySkipsDisabledChannels(t *testing.T) {
	disabledUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("disabled channel should not be used for fallback")
	}))
	defer disabledUpstream.Close()

	activeFallbackUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-active-disabled" {
			t.Fatalf("Authorization = %q, want active-channel disabled fallback key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-active-disabled","object":"model"}]}`))
	}))
	defer activeFallbackUpstream.Close()

	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:    "explicitly-disabled",
				BaseURL: disabledUpstream.URL,
				DisabledAPIKeys: []config.DisabledKeyInfo{{
					Key:        "sk-disabled-channel",
					Reason:     "authentication_error",
					Message:    "invalid key",
					DisabledAt: "2026-04-15T00:00:00Z",
				}},
				ServiceType: "claude",
				Status:      "disabled",
			},
			{
				Name:    "active-with-disabled-keys",
				BaseURL: activeFallbackUpstream.URL,
				DisabledAPIKeys: []config.DisabledKeyInfo{{
					Key:        "sk-active-disabled",
					Reason:     "authentication_error",
					Message:    "invalid key",
					DisabledAt: "2026-04-15T00:00:00Z",
				}},
				ServiceType: "claude",
				Status:      "active",
			},
		},
	})
	sch := newModelsTestScheduler(cfgManager)
	router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cfgManager, sch)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestModelsHandler_NoKeysStillFails(t *testing.T) {
	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:        "messages-no-keys",
			BaseURL:     "https://example.com",
			ServiceType: "claude",
		}},
	})
	sch := newModelsTestScheduler(cfgManager)
	router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cfgManager, sch)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil).WithContext(context.Background())
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestModelsHandler_ManualModelsWithoutAPIKey(t *testing.T) {
	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:               "messages-manual",
			BaseURL:            "https://example.com",
			ServiceType:        "claude",
			ModelsResponseMode: "manual",
			ManualModels:       []string{"glm-4.5", "glm-4.5", "glm-4-air"},
		}},
	})
	sch := newModelsTestScheduler(cfgManager)
	router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cfgManager, sch)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !json.Valid([]byte(body)) || !containsAll(body, []string{"glm-4.5", "glm-4-air"}) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestModelsDetailHandler_ManualModels(t *testing.T) {
	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:               "messages-manual-detail",
			BaseURL:            "https://example.com",
			ServiceType:        "claude",
			ModelsResponseMode: "manual",
			ManualModels:       []string{"glm-4.5"},
		}},
	})
	sch := newModelsTestScheduler(cfgManager)
	router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cfgManager, sch)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/glm-4.5", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !containsAll(body, []string{`"id":"glm-4.5"`, `"object":"model"`}) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func containsAll(body string, parts []string) bool {
	for _, part := range parts {
		if !strings.Contains(body, part) {
			return false
		}
	}
	return true
}
