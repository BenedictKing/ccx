package messages

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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
	cm, err := config.NewConfigManager(tmpFile, "")
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
		metrics.NewMetricsManager(),
	}

	schedulerInstance := scheduler.NewChannelScheduler(
		cfgManager,
		metricsManagers[0],
		metricsManagers[1],
		metricsManagers[2],
		metricsManagers[3],
		metricsManagers[4],
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

func TestModelsHandler_AddsClaudeDesktopModelMetadata(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"claude-sonnet-4-6","object":"model"}]}`))
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

	var resp ModelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	model := findModelEntry(resp.Data, "claude-sonnet-4-6")
	if model == nil {
		t.Fatalf("缺少 Claude 模型: %#v", resp.Data)
	}
	if model.Name != "claude-sonnet-4-6" {
		t.Fatalf("name = %q, want model id", model.Name)
	}
	if model.Type != "model" {
		t.Fatalf("type = %q, want model", model.Type)
	}
	if model.DisplayName != "Claude Sonnet 4.6" {
		t.Fatalf("display_name = %q, want Claude Sonnet 4.6", model.DisplayName)
	}
	if !model.Supports1M {
		t.Fatalf("supports1m = false, want true")
	}
	if model.AnthropicFamilyTier != "sonnet" {
		t.Fatalf("anthropicFamilyTier = %q, want sonnet", model.AnthropicFamilyTier)
	}
	if !model.IsFamilyDefault {
		t.Fatalf("isFamilyDefault = false, want true")
	}
	if resp.FirstID != "claude-sonnet-4-6" || resp.LastID != "claude-sonnet-4-6" {
		t.Fatalf("first/last id = %q/%q, want claude-sonnet-4-6", resp.FirstID, resp.LastID)
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

func TestModelsHandler_MergesChatModels(t *testing.T) {
	messagesUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-messages","object":"model"},{"id":"model-shared","object":"model"}]}`))
	}))
	defer messagesUpstream.Close()

	responsesUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-responses","object":"model"},{"id":"model-shared","object":"model"}]}`))
	}))
	defer responsesUpstream.Close()

	chatUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-chat","object":"model"},{"id":"model-shared","object":"model"}]}`))
	}))
	defer chatUpstream.Close()

	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:        "messages-active",
			BaseURL:     messagesUpstream.URL,
			APIKeys:     []string{"sk-messages"},
			ServiceType: "claude",
		}},
		ResponsesUpstream: []config.UpstreamConfig{{
			Name:        "responses-active",
			BaseURL:     responsesUpstream.URL,
			APIKeys:     []string{"sk-responses"},
			ServiceType: "responses",
		}},
		ChatUpstream: []config.UpstreamConfig{{
			Name:        "chat-active",
			BaseURL:     chatUpstream.URL,
			APIKeys:     []string{"sk-chat"},
			ServiceType: "openai",
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

	var resp ModelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	ids := make([]string, 0, len(resp.Data))
	for _, model := range resp.Data {
		ids = append(ids, model.ID)
	}

	// 合并后的模型按智能规则排序：
	// model-messages、model-responses、model-chat 都不匹配特殊规则，按字母序
	// model-shared 去重后只出现一次
	want := []string{"model-chat", "model-messages", "model-responses", "model-shared"}
	if len(ids) != len(want) {
		t.Fatalf("ids len = %d, want %d, ids=%v", len(ids), len(want), ids)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids[%d] = %q, want %q, ids=%v", i, ids[i], want[i], ids)
		}
	}
}

func TestModelsHandler_IncludesImagesModels(t *testing.T) {
	messagesUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-shared","object":"model"}]}`))
	}))
	defer messagesUpstream.Close()

	imagesUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"image-model","object":"model"},{"id":"model-shared","object":"model","input_modalities":["text","image"]}]}`))
	}))
	defer imagesUpstream.Close()

	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:        "messages-active",
			BaseURL:     messagesUpstream.URL,
			APIKeys:     []string{"sk-messages"},
			ServiceType: "claude",
		}},
		ImagesUpstream: []config.UpstreamConfig{{
			Name:        "images-active",
			BaseURL:     imagesUpstream.URL,
			APIKeys:     []string{"sk-images"},
			ServiceType: "openai",
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

	var resp ModelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if findModelEntry(resp.Data, "image-model") == nil {
		t.Fatalf("缺少 images 模型: %#v", resp.Data)
	}
	shared := findModelEntry(resp.Data, "model-shared")
	if shared == nil || !sameStrings(shared.InputModalities, []string{"text", "image"}) {
		t.Fatalf("model-shared input_modalities = %#v, want [text image]", shared)
	}
}

func TestModelsHandler_CollectsTwoSuccessfulChannelsPerProtocol(t *testing.T) {
	var calls atomic.Int32
	upstreams := make([]config.UpstreamConfig, 0, 6)
	servers := make([]*httptest.Server, 0, 6)
	for i := 0; i < 6; i++ {
		idx := i
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if idx == 0 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(fmt.Sprintf(`{"object":"list","data":[{"id":"model-%d","object":"model"}]}`, idx)))
		}))
		servers = append(servers, server)
		defer server.Close()
		upstreams = append(upstreams, config.UpstreamConfig{
			Name:        fmt.Sprintf("chat-%d", idx),
			BaseURL:     server.URL,
			APIKeys:     []string{fmt.Sprintf("sk-%d", idx)},
			ServiceType: "openai",
			Priority:    idx,
		})
	}

	cfgManager := setupModelsConfigManager(t, config.Config{ChatUpstream: upstreams})
	sch := newModelsTestScheduler(cfgManager)
	router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cfgManager, sch)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}

	var resp ModelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if findModelEntry(resp.Data, "model-1") == nil {
		t.Fatalf("缺少首个成功渠道模型 model-1: %#v", resp.Data)
	}
	if findModelEntry(resp.Data, "model-2") == nil {
		t.Fatalf("缺少第二个成功渠道模型 model-2: %#v", resp.Data)
	}
	if findModelEntry(resp.Data, "model-0") != nil {
		t.Fatalf("失败渠道模型不应出现: %#v", resp.Data)
	}
	if findModelEntry(resp.Data, "model-3") != nil {
		t.Fatalf("不应继续采集后续成功渠道模型: %#v", resp.Data)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("渠道探测次数 = %d, want 3", got)
	}
}

func TestModelsHandler_DisabledKeyFallbackStopsAtCollectTimeout(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-r.Context().Done()
	}))
	defer upstream.Close()

	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:    "messages-disabled-fallback-timeout",
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

	start := time.Now()
	router.ServeHTTP(w, req)
	elapsed := time.Since(start)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("fallback 探测次数 = %d, want 1", got)
	}
	if elapsed > 3*modelsCollectTimeout {
		t.Fatalf("fallback 耗时 = %s, want <= %s", elapsed, 3*modelsCollectTimeout)
	}
}

func TestModelsHandler_XChannelOnlyQueriesPinnedChannel(t *testing.T) {
	var pinnedCalls atomic.Int32
	var otherCalls atomic.Int32
	pinnedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pinnedCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-pinned","object":"model"}]}`))
	}))
	defer pinnedUpstream.Close()

	otherUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-other","object":"model"}]}`))
	}))
	defer otherUpstream.Close()

	cfgManager := setupModelsConfigManager(t, config.Config{
		ChatUpstream: []config.UpstreamConfig{
			{Name: "chat-pinned", BaseURL: pinnedUpstream.URL, APIKeys: []string{"sk-pinned"}, ServiceType: "openai", Priority: 0},
			{Name: "chat-other", BaseURL: otherUpstream.URL, APIKeys: []string{"sk-other"}, ServiceType: "openai", Priority: 1},
		},
	})
	sch := newModelsTestScheduler(cfgManager)
	router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cfgManager, sch)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("X-Channel", "chat-pinned")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if pinnedCalls.Load() != 1 {
		t.Fatalf("pinned calls = %d, want 1", pinnedCalls.Load())
	}
	if otherCalls.Load() != 0 {
		t.Fatalf("other channel should not be queried, calls=%d", otherCalls.Load())
	}
}

func TestModelsHandler_ProbesActiveKeysInChannelConcurrently(t *testing.T) {
	var slowCalls atomic.Int32
	var fastCalls atomic.Int32
	var fastWaitTimedOut atomic.Bool
	slowStarted := make(chan struct{})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer sk-slow":
			slowCalls.Add(1)
			close(slowStarted)
			<-r.Context().Done()
		case "Bearer sk-fast":
			fastCalls.Add(1)
			select {
			case <-slowStarted:
			case <-time.After(800 * time.Millisecond):
				fastWaitTimedOut.Store(true)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-fast","object":"model"}]}`))
		default:
			t.Fatalf("unexpected Authorization = %q", r.Header.Get("Authorization"))
		}
	}))
	defer upstream.Close()

	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:        "messages-multi-key",
			BaseURL:     upstream.URL,
			APIKeys:     []string{"sk-slow", "sk-fast"},
			ServiceType: "claude",
		}},
	})
	sch := newModelsTestScheduler(cfgManager)
	router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cfgManager, sch)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	start := time.Now()
	router.ServeHTTP(w, req)
	elapsed := time.Since(start)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if fastWaitTimedOut.Load() {
		t.Fatal("fast key did not observe slow key request, expected concurrent probing")
	}
	if slowCalls.Load() != 1 || fastCalls.Load() != 1 {
		t.Fatalf("slow/fast calls = %d/%d, want 1/1", slowCalls.Load(), fastCalls.Load())
	}
	if elapsed > modelsCollectTimeout {
		t.Fatalf("并发 key 探测耗时 = %s, want <= %s", elapsed, modelsCollectTimeout)
	}

	var resp ModelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if findModelEntry(resp.Data, "model-fast") == nil {
		t.Fatalf("缺少 fast key 返回模型: %#v", resp.Data)
	}
}

func TestModelsHandler_PrefersActiveKeysOverDisabledKeysInSameChannel(t *testing.T) {
	var activeCalls atomic.Int32
	var disabledCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer sk-active":
			activeCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-active","object":"model"}]}`))
		case "Bearer sk-disabled":
			disabledCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-disabled","object":"model"}]}`))
		default:
			t.Fatalf("unexpected Authorization = %q", r.Header.Get("Authorization"))
		}
	}))
	defer upstream.Close()

	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:        "messages-active-and-disabled",
			BaseURL:     upstream.URL,
			APIKeys:     []string{"sk-active"},
			ServiceType: "claude",
			DisabledAPIKeys: []config.DisabledKeyInfo{{
				Key:        "sk-disabled",
				Reason:     "authentication_error",
				Message:    "invalid key",
				DisabledAt: "2026-04-15T00:00:00Z",
			}},
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
	if activeCalls.Load() != 1 {
		t.Fatalf("active calls = %d, want 1", activeCalls.Load())
	}
	if disabledCalls.Load() != 0 {
		t.Fatalf("disabled key should not be used when active key exists, calls=%d", disabledCalls.Load())
	}
}

func TestModelsHandler_ReturnsRecentCacheWhenDiscoveryFails(t *testing.T) {
	var fail atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-cached","object":"model"}]}`))
	}))
	defer upstream.Close()

	cfgManager := setupModelsConfigManager(t, config.Config{
		ChatUpstream: []config.UpstreamConfig{{
			Name:        "chat-cache",
			BaseURL:     upstream.URL,
			APIKeys:     []string{"sk-cache"},
			ServiceType: "openai",
		}},
	})
	sch := newModelsTestScheduler(cfgManager)
	router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cfgManager, sch)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("initial status = %d, body=%s", w.Code, w.Body.String())
	}

	fail.Store(true)
	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("cached status = %d, body=%s", w.Code, w.Body.String())
	}
	var resp ModelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析缓存响应失败: %v", err)
	}
	if findModelEntry(resp.Data, "model-cached") == nil {
		t.Fatalf("缺少缓存模型: %#v", resp.Data)
	}
}

func TestModelsHandler_CacheSeparatesPinnedChannel(t *testing.T) {
	defaultUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"model-default","object":"model"}]}`))
	}))
	defer defaultUpstream.Close()

	pinnedUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer pinnedUpstream.Close()

	cfgManager := setupModelsConfigManager(t, config.Config{
		ChatUpstream: []config.UpstreamConfig{
			{Name: "chat-default", BaseURL: defaultUpstream.URL, APIKeys: []string{"sk-default"}, ServiceType: "openai", Priority: 0},
			{Name: "chat-pinned", BaseURL: pinnedUpstream.URL, APIKeys: []string{"sk-pinned"}, ServiceType: "openai", Priority: 1},
		},
	})
	sch := newModelsTestScheduler(cfgManager)
	router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cfgManager, sch)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("default status = %d, body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("X-Channel", "chat-pinned")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("pinned status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestModelsDetailHandler_FallsBackToImages(t *testing.T) {
	messagesUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer messagesUpstream.Close()

	imagesUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models/image-model" {
			t.Fatalf("path = %q, want /v1/models/image-model", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"image-model","object":"model","owned_by":"images"}`))
	}))
	defer imagesUpstream.Close()

	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:        "messages-active",
			BaseURL:     messagesUpstream.URL,
			APIKeys:     []string{"sk-messages"},
			ServiceType: "claude",
		}},
		ImagesUpstream: []config.UpstreamConfig{{
			Name:        "images-active",
			BaseURL:     imagesUpstream.URL,
			APIKeys:     []string{"sk-images"},
			ServiceType: "openai",
		}},
	})
	sch := newModelsTestScheduler(cfgManager)
	router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cfgManager, sch)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/image-model", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != `{"id":"image-model","object":"model","owned_by":"images"}` {
		t.Fatalf("body = %s", got)
	}
}

func TestModelsHandler_EnrichesInputModalitiesAndVisionFallback(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"mimo-v2.5-pro","object":"model"}]}`))
	}))
	defer upstream.Close()

	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:                "mimo",
			BaseURL:             upstream.URL,
			APIKeys:             []string{"sk-mimo"},
			ServiceType:         "claude",
			ModelMapping:        map[string]string{"opus": "mimo-v2.5-pro"},
			NoVisionModels:      []string{"mimo-v2.5-pro"},
			VisionFallbackModel: "mimo-v2.5",
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

	var resp ModelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}

	modelsByID := make(map[string]ModelEntry, len(resp.Data))
	for _, model := range resp.Data {
		modelsByID[model.ID] = model
	}

	pro, ok := modelsByID["mimo-v2.5-pro"]
	if !ok {
		t.Fatalf("缺少 noVision 模型: %#v", resp.Data)
	}
	if !sameStrings(pro.InputModalities, []string{"text"}) {
		t.Fatalf("mimo-v2.5-pro input_modalities = %v, want [text]", pro.InputModalities)
	}

	opus, ok := modelsByID["opus"]
	if !ok {
		t.Fatalf("缺少请求模型别名 opus: %#v", resp.Data)
	}
	if !sameStrings(opus.InputModalities, []string{"text", "image"}) {
		t.Fatalf("opus input_modalities = %v, want [text image]", opus.InputModalities)
	}

	fallback, ok := modelsByID["mimo-v2.5"]
	if !ok {
		t.Fatalf("缺少 vision fallback 模型: %#v", resp.Data)
	}
	if !sameStrings(fallback.InputModalities, []string{"text", "image"}) {
		t.Fatalf("mimo-v2.5 input_modalities = %v, want [text image]", fallback.InputModalities)
	}
}

func TestMergeModels_PreservesVisionWhenAnyChannelSupportsImage(t *testing.T) {
	result := mergeModels(
		[]ModelEntry{{
			ID:              "model-shared",
			Object:          "model",
			InputModalities: []string{"text"},
		}},
		[]ModelEntry{{
			ID:              "model-shared",
			Object:          "model",
			InputModalities: []string{"text", "image"},
		}},
	)

	if len(result) != 1 {
		t.Fatalf("结果数量 = %d, want 1", len(result))
	}
	if !sameStrings(result[0].InputModalities, []string{"text", "image"}) {
		t.Fatalf("input_modalities = %v, want [text image]", result[0].InputModalities)
	}
}

func TestEnrichModelModalitiesForUpstream_MappedModelNeedsVisionFallback(t *testing.T) {
	upstream := &config.UpstreamConfig{
		ModelMapping:   map[string]string{"alias-pro": "mimo-v2.5-pro"},
		NoVisionModels: []string{"mimo-v2.5-pro"},
	}

	result := enrichModelModalitiesForUpstream([]ModelEntry{{ID: "alias-pro", Object: "model"}}, upstream)

	alias := findModelEntry(result, "alias-pro")
	if alias == nil {
		t.Fatalf("缺少请求模型别名: %#v", result)
	}
	if !sameStrings(alias.InputModalities, []string{"text"}) {
		t.Fatalf("alias-pro input_modalities = %v, want [text]", alias.InputModalities)
	}

	upstream.VisionFallbackModel = "mimo-v2.5"
	result = enrichModelModalitiesForUpstream([]ModelEntry{{ID: "mimo-v2.5-pro", Object: "model"}}, upstream)

	alias = findModelEntry(result, "alias-pro")
	if alias == nil {
		t.Fatalf("缺少请求模型别名: %#v", result)
	}
	if !sameStrings(alias.InputModalities, []string{"text", "image"}) {
		t.Fatalf("alias-pro input_modalities = %v, want [text image]", alias.InputModalities)
	}
}

func TestParseModelsResponseForKind_PreservesAnthropicFields(t *testing.T) {
	body := []byte(`{"object":"list","data":[{"id":"claude-opus-4-6","type":"model","display_name":"Claude Opus 4.6","created_at":"2026-01-01T00:00:00Z"}]}`)
	upstream := &config.UpstreamConfig{ServiceType: "claude"}

	result := parseModelsResponseForKind(body, upstream, nil, scheduler.ChannelKindMessages)

	model := findModelEntry(result, "claude-opus-4-6")
	if model == nil {
		t.Fatalf("缺少 Anthropic 模型: %#v", result)
	}
	if model.Type != "model" {
		t.Fatalf("type = %q, want model", model.Type)
	}
	if model.DisplayName != "Claude Opus 4.6" {
		t.Fatalf("display_name = %q, want Claude Opus 4.6", model.DisplayName)
	}
	if model.CreatedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("created_at = %q, want preserved value", model.CreatedAt)
	}
	if !model.Supports1M {
		t.Fatalf("supports1m = false, want true")
	}
	if model.AnthropicFamilyTier != "opus" {
		t.Fatalf("anthropicFamilyTier = %q, want opus", model.AnthropicFamilyTier)
	}
}

func findModelEntry(models []ModelEntry, id string) *ModelEntry {
	for i := range models {
		if models[i].ID == id {
			return &models[i]
		}
	}
	return nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestModelSortKey(t *testing.T) {
	tests := []struct {
		name     string
		models   []string
		expected []string
	}{
		{
			name:     "Claude 系列按能力排序",
			models:   []string{"claude-haiku-4-5-20251001", "claude-opus-4-8", "claude-fable-5", "claude-sonnet-4-6"},
			expected: []string{"claude-fable-5", "claude-opus-4-8", "claude-sonnet-4-6", "claude-haiku-4-5-20251001"},
		},
		{
			name:     "Kimi 系列按能力排序",
			models:   []string{"kimi-k2.6", "kimi-for-coding", "kimi-k2.7", "kimi-k2.5"},
			expected: []string{"kimi-for-coding", "kimi-k2.7", "kimi-k2.6", "kimi-k2.5"},
		},
		{
			name:     "DeepSeek 系列排序",
			models:   []string{"deepseek-v4-flash", "deepseek-v4-pro", "deepseek-v3"},
			expected: []string{"deepseek-v4-pro", "deepseek-v4-flash", "deepseek-v3"},
		},
		{
			name:     "混合模型智能排序",
			models:   []string{"gpt-4", "claude-opus-4-8", "kimi-k2.7", "claude-fable-5", "deepseek-v4-pro"},
			expected: []string{"claude-fable-5", "claude-opus-4-8", "kimi-k2.7", "deepseek-v4-pro", "gpt-4"},
		},
		{
			name:     "通用模型按字母序",
			models:   []string{"model-z", "model-a", "model-m"},
			expected: []string{"model-a", "model-m", "model-z"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entries := make([]ModelEntry, len(tt.models))
			for i, id := range tt.models {
				entries[i] = ModelEntry{ID: id, Object: "model"}
			}

			result := mergeModels(entries)

			if len(result) != len(tt.expected) {
				t.Fatalf("结果数量 = %d, want %d", len(result), len(tt.expected))
			}

			for i, expected := range tt.expected {
				if result[i].ID != expected {
					t.Errorf("result[%d] = %q, want %q", i, result[i].ID, expected)
				}
			}
		})
	}
}

func TestModelsDetailHandler_FallsBackToChat(t *testing.T) {
	messagesUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer messagesUpstream.Close()

	responsesUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer responsesUpstream.Close()

	chatUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models/model-chat" {
			t.Fatalf("path = %q, want /v1/models/model-chat", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"model-chat","object":"model","owned_by":"chat"}`))
	}))
	defer chatUpstream.Close()

	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:        "messages-active",
			BaseURL:     messagesUpstream.URL,
			APIKeys:     []string{"sk-messages"},
			ServiceType: "claude",
		}},
		ResponsesUpstream: []config.UpstreamConfig{{
			Name:        "responses-active",
			BaseURL:     responsesUpstream.URL,
			APIKeys:     []string{"sk-responses"},
			ServiceType: "responses",
		}},
		ChatUpstream: []config.UpstreamConfig{{
			Name:        "chat-active",
			BaseURL:     chatUpstream.URL,
			APIKeys:     []string{"sk-chat"},
			ServiceType: "openai",
		}},
	})
	sch := newModelsTestScheduler(cfgManager)
	router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cfgManager, sch)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/model-chat", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != `{"id":"model-chat","object":"model","owned_by":"chat"}` {
		t.Fatalf("body = %s", got)
	}
}

func TestModelsDetailHandler_PrefersMessagesOverChat(t *testing.T) {
	messagesUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"model-shared","object":"model","owned_by":"messages"}`))
	}))
	defer messagesUpstream.Close()

	responsesUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"model-shared","object":"model","owned_by":"responses"}`))
	}))
	defer responsesUpstream.Close()

	chatUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"model-shared","object":"model","owned_by":"chat"}`))
	}))
	defer chatUpstream.Close()

	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:        "messages-active",
			BaseURL:     messagesUpstream.URL,
			APIKeys:     []string{"sk-messages"},
			ServiceType: "claude",
		}},
		ResponsesUpstream: []config.UpstreamConfig{{
			Name:        "responses-active",
			BaseURL:     responsesUpstream.URL,
			APIKeys:     []string{"sk-responses"},
			ServiceType: "responses",
		}},
		ChatUpstream: []config.UpstreamConfig{{
			Name:        "chat-active",
			BaseURL:     chatUpstream.URL,
			APIKeys:     []string{"sk-chat"},
			ServiceType: "openai",
		}},
	})
	sch := newModelsTestScheduler(cfgManager)
	router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cfgManager, sch)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/model-shared", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); got != `{"id":"model-shared","object":"model","owned_by":"messages"}` {
		t.Fatalf("body = %s", got)
	}
}

func TestModelsDetailHandler_ChatFallbackRespectsRoutePrefix(t *testing.T) {
	defaultChatUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("default route chat fallback should not be used for prefixed request")
	}))
	defer defaultChatUpstream.Close()

	prefixedChatUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-prefix-chat" {
			t.Fatalf("Authorization = %q, want prefixed chat disabled fallback key", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"model-prefix","object":"model","owned_by":"chat"}`))
	}))
	defer prefixedChatUpstream.Close()

	cfgManager := setupModelsConfigManager(t, config.Config{
		ChatUpstream: []config.UpstreamConfig{
			{
				Name:    "default-chat-disabled",
				BaseURL: defaultChatUpstream.URL,
				DisabledAPIKeys: []config.DisabledKeyInfo{{
					Key:        "sk-default-chat",
					Reason:     "authentication_error",
					Message:    "invalid key",
					DisabledAt: "2026-04-15T00:00:00Z",
				}},
				ServiceType: "openai",
				Status:      "active",
			},
			{
				Name:        "prefixed-chat-disabled",
				BaseURL:     prefixedChatUpstream.URL,
				RoutePrefix: "kimi",
				DisabledAPIKeys: []config.DisabledKeyInfo{{
					Key:        "sk-prefix-chat",
					Reason:     "authentication_error",
					Message:    "invalid key",
					DisabledAt: "2026-04-15T00:00:00Z",
				}},
				ServiceType: "openai",
				Status:      "active",
			},
		},
	})
	sch := newModelsTestScheduler(cfgManager)
	router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cfgManager, sch)

	req := httptest.NewRequest(http.MethodGet, "/kimi/v1/models/model-prefix", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
}

func TestBuildClaudeCompatibleModelsURLs(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		expected []string
	}{
		{
			name:    "纯域名不产生额外候选",
			baseURL: "https://api.anthropic.com",
			expected: []string{
				"https://api.anthropic.com/v1/models",
			},
		},
		{
			name:    "带 /anthropic 尾段产生两个候选（剔除后即纯域名）",
			baseURL: "https://api.deepseek.com/anthropic",
			expected: []string{
				"https://api.deepseek.com/anthropic/v1/models",
				"https://api.deepseek.com/v1/models",
			},
		},
		{
			name:    "带 /anthropic/v1 尾段产生两个候选",
			baseURL: "https://api.deepseek.com/anthropic/v1",
			expected: []string{
				"https://api.deepseek.com/anthropic/v1/models",
				"https://api.deepseek.com/v1/models",
			},
		},
		{
			name:    "带 /proxy/anthropic 产生三个候选",
			baseURL: "https://api.vendor.com/proxy/anthropic",
			expected: []string{
				"https://api.vendor.com/proxy/anthropic/v1/models",
				"https://api.vendor.com/proxy/v1/models",
				"https://api.vendor.com/v1/models",
			},
		},
		{
			name:    "带 /proxy/claude/v1 产生三个候选",
			baseURL: "https://api.vendor.com/proxy/claude/v1",
			expected: []string{
				"https://api.vendor.com/proxy/claude/v1/models",
				"https://api.vendor.com/proxy/v1/models",
				"https://api.vendor.com/v1/models",
			},
		},
		{
			name:    "带 /messages 尾段产生两个候选",
			baseURL: "https://api.vendor.com/messages",
			expected: []string{
				"https://api.vendor.com/messages/v1/models",
				"https://api.vendor.com/v1/models",
			},
		},
		{
			name:    "非协议尾段不产生额外候选",
			baseURL: "https://api.vendor.com/openai",
			expected: []string{
				"https://api.vendor.com/openai/v1/models",
			},
		},
		{
			name:    "# 标记保持兼容",
			baseURL: "https://api.vendor.com/anthropic#",
			expected: []string{
				"https://api.vendor.com/anthropic/models",
				"https://api.vendor.com/v1/models",
			},
		},
		{
			name:    "带端口的域名",
			baseURL: "https://localhost:8080/anthropic",
			expected: []string{
				"https://localhost:8080/anthropic/v1/models",
				"https://localhost:8080/v1/models",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildClaudeCompatibleModelsURLs(tt.baseURL)
			if len(got) != len(tt.expected) {
				t.Fatalf("候选数量不匹配: got %v, want %v", got, tt.expected)
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("候选[%d] = %q, want %q", i, got[i], tt.expected[i])
				}
			}
		})
	}
}

func TestTryModelsRequest_ClaudeCompatFallback(t *testing.T) {
	// 模拟上游：第一个 URL 返回 404，第二个返回 200
	callCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.URL.Path == "/anthropic/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"deepseek-chat","object":"model","owned_by":"deepseek"}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()

	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:    "deepseek-compat",
				BaseURL: upstream.URL + "/anthropic",
				APIKeys: []string{"sk-test"},
				Status:  "active",
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
	if callCount < 2 {
		t.Errorf("期望至少 2 次请求（第一次 404 后 fallback），实际 %d 次", callCount)
	}

	var resp ModelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(resp.Data) == 0 {
		t.Fatal("期望返回模型列表，但为空")
	}
	if resp.Data[0].ID != "deepseek-chat" {
		t.Errorf("模型 ID = %q, want %q", resp.Data[0].ID, "deepseek-chat")
	}
}

// TestModelsHandler_CopilotResolvesTokenAndHitsModelsEndpoint 验证 copilot 渠道在 /v1/models
// 代理时：1) 走 Copilot token exchange 拿到 runtime token；2) 请求 {baseURL}/models（不含 /v1 前缀）；
// 3) 用 runtime token 而非原始 GitHub OAuth token 认证；4) 注入 Copilot 识别头。
// 回归 issue #245：修复前 requestModelsFromSelection 未处理 copilot，导致 /v1/models 返回 404。
func TestModelsHandler_CopilotResolvesTokenAndHitsModelsEndpoint(t *testing.T) {
	var sawPath, sawAuth, sawIntegrationID string
	var copilotHits int32
	copilotAPISrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&copilotHits, 1)
		sawPath = r.URL.Path
		sawAuth = r.Header.Get("Authorization")
		sawIntegrationID = r.Header.Get("Copilot-Integration-Id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"gpt-4o","object":"model"}]}`))
	}))
	defer copilotAPISrv.Close()

	// 注入 mock token resolver：返回 runtime token + Copilot API mock server 地址
	oldResolver := copilotTokenResolver
	t.Cleanup(func() { copilotTokenResolver = oldResolver })
	copilotTokenResolver = func(ctx context.Context, githubToken, proxyURL string) (string, string, error) {
		if githubToken != "gho_test_oauth" {
			t.Errorf("token exchange 收到 GitHub token = %q, want gho_test_oauth", githubToken)
		}
		return "copilot-runtime-token", copilotAPISrv.URL, nil
	}

	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:        "copilot-channel",
			BaseURL:     "https://api.githubcopilot.com",
			APIKeys:     []string{"gho_test_oauth"},
			ServiceType: "copilot",
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
	if atomic.LoadInt32(&copilotHits) != 1 {
		t.Fatalf("期望 Copilot API 被命中 1 次，实际 %d 次", atomic.LoadInt32(&copilotHits))
	}
	if sawPath != "/models" {
		t.Errorf("Copilot API 请求路径 = %q, want /models（不应包含 /v1 前缀）", sawPath)
	}
	if sawAuth != "Bearer copilot-runtime-token" {
		t.Errorf("Copilot API Authorization = %q, want Bearer copilot-runtime-token（应为 runtime token 而非原始 GitHub OAuth token）", sawAuth)
	}
	if sawIntegrationID != "vscode-chat" {
		t.Errorf("Copilot-Integration-Id = %q, want vscode-chat", sawIntegrationID)
	}

	var resp ModelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if len(resp.Data) == 0 || resp.Data[0].ID != "gpt-4o" {
		t.Fatalf("期望返回 gpt-4o 模型，实际 %+v", resp.Data)
	}
}

// TestModelsHandler_CopilotTokenExchangeFailure 验证 copilot token exchange 失败时
// /v1/models 代理不命中 Copilot API 并最终返回 404。
func TestModelsHandler_CopilotTokenExchangeFailure(t *testing.T) {
	var copilotHits int32
	copilotAPISrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&copilotHits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer copilotAPISrv.Close()

	oldResolver := copilotTokenResolver
	t.Cleanup(func() { copilotTokenResolver = oldResolver })
	copilotTokenResolver = func(ctx context.Context, githubToken, proxyURL string) (string, string, error) {
		return "", "", fmt.Errorf("mock token exchange failure")
	}

	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:        "copilot-channel",
			BaseURL:     "https://api.githubcopilot.com",
			APIKeys:     []string{"gho_test_oauth"},
			ServiceType: "copilot",
		}},
	})
	sch := newModelsTestScheduler(cfgManager)
	router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cfgManager, sch)

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if atomic.LoadInt32(&copilotHits) != 0 {
		t.Fatalf("token exchange 失败时不应命中 Copilot API，实际命中 %d 次", atomic.LoadInt32(&copilotHits))
	}
	if w.Code != http.StatusNotFound {
		t.Errorf("期望 token exchange 失败后返回 404，实际 %d", w.Code)
	}
}

// TestModelsDetailHandler_Copilot 验证 /v1/models/:model 代理对 copilot 渠道也走 token exchange
// 并请求 {baseURL}/models/{model}（而非 /v1/models/{model}）。
func TestModelsDetailHandler_Copilot(t *testing.T) {
	var sawPath, sawAuth string
	copilotAPISrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"gpt-4o","object":"model"}`))
	}))
	defer copilotAPISrv.Close()

	oldResolver := copilotTokenResolver
	t.Cleanup(func() { copilotTokenResolver = oldResolver })
	copilotTokenResolver = func(ctx context.Context, githubToken, proxyURL string) (string, string, error) {
		return "copilot-runtime-token", copilotAPISrv.URL, nil
	}

	cfgManager := setupModelsConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:        "copilot-channel",
			BaseURL:     "https://api.githubcopilot.com",
			APIKeys:     []string{"gho_test_oauth"},
			ServiceType: "copilot",
		}},
	})
	sch := newModelsTestScheduler(cfgManager)
	router := newModelsRouterForAggregate(&config.EnvConfig{ProxyAccessKey: "test-key"}, cfgManager, sch)

	req := httptest.NewRequest(http.MethodGet, "/v1/models/gpt-4o", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if sawPath != "/models/gpt-4o" {
		t.Errorf("Copilot API detail 请求路径 = %q, want /models/gpt-4o", sawPath)
	}
	if sawAuth != "Bearer copilot-runtime-token" {
		t.Errorf("Authorization = %q, want Bearer copilot-runtime-token", sawAuth)
	}
}
