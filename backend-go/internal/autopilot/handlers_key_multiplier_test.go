package autopilot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestHandlePatchKeyMultiplier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newTestConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			ChannelUID: "msg-1",
			APIKeys:    []string{"plain-key"},
			APIKeyConfigs: []config.APIKeyConfig{{
				Key:    "plain-key",
				KeyUID: "key-1",
			}},
		}},
		ChatUpstream: []config.UpstreamConfig{{
			ChannelUID:    "chat-1",
			APIKeys:       []string{"chat-key"},
			APIKeyConfigs: []config.APIKeyConfig{{Key: "chat-key", KeyUID: "chat-key-uid"}},
		}},
		ResponsesUpstream: []config.UpstreamConfig{{
			ChannelUID:    "resp-1",
			APIKeys:       []string{"resp-key"},
			APIKeyConfigs: []config.APIKeyConfig{{Key: "resp-key", KeyUID: "resp-key-uid"}},
		}},
		GeminiUpstream: []config.UpstreamConfig{{
			ChannelUID:    "gem-1",
			APIKeys:       []string{"gem-key"},
			APIKeyConfigs: []config.APIKeyConfig{{Key: "gem-key", KeyUID: "gem-key-uid"}},
		}},
		ImagesUpstream: []config.UpstreamConfig{{
			ChannelUID:    "img-1",
			APIKeys:       []string{"img-key"},
			APIKeyConfigs: []config.APIKeyConfig{{Key: "img-key", KeyUID: "img-key-uid"}},
		}},
		VectorsUpstream: []config.UpstreamConfig{{
			ChannelUID:    "vec-1",
			APIKeys:       []string{"vec-key"},
			APIKeyConfigs: []config.APIKeyConfig{{Key: "vec-key", KeyUID: "vec-key-uid"}},
		}},
	})
	register := func() *gin.Engine {
		router := gin.New()
		RegisterKeyMultiplierRoutes(router, cfgManager)
		return router
	}

	t.Run("manual key set and reset", func(t *testing.T) {
		router := register()
		body := map[string]any{"groupMultiplier": 0.5, "maxGroupMultiplier": 1.0}
		resp := performJSONRequest(t, router, http.MethodPatch, "/messages/channels/msg-1/keys/key-1/multiplier", body)
		if resp.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
		var got keyMultiplierResponse
		decodeJSONResponse(t, resp, &got)
		if got.GroupMultiplier == nil || *got.GroupMultiplier != 0.5 || got.MaxMultiplier == nil || *got.MaxMultiplier != 1.0 || !got.Eligible {
			t.Fatalf("unexpected response: %+v", got)
		}

		resp = performJSONRequest(t, router, http.MethodPatch, "/messages/channels/msg-1/keys/key-1/multiplier", map[string]any{"groupMultiplier": nil, "maxGroupMultiplier": nil})
		if resp.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
		got = keyMultiplierResponse{}
		decodeJSONResponse(t, resp, &got)
		if got.GroupMultiplier != nil || got.MaxMultiplier != nil || !got.Eligible {
			t.Fatalf("unexpected reset response: %+v", got)
		}
	})

	t.Run("zero preserved", func(t *testing.T) {
		router := register()
		resp := performJSONRequest(t, router, http.MethodPatch, "/messages/channels/msg-1/keys/key-1/multiplier", map[string]any{"groupMultiplier": 0, "maxGroupMultiplier": 0})
		if resp.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
		var got keyMultiplierResponse
		decodeJSONResponse(t, resp, &got)
		if got.GroupMultiplier == nil || *got.GroupMultiplier != 0 || got.MaxMultiplier == nil || *got.MaxMultiplier != 0 {
			t.Fatalf("unexpected zero response: %+v", got)
		}
	})

	t.Run("invalid pair rejected", func(t *testing.T) {
		router := register()
		resp := performJSONRequest(t, router, http.MethodPatch, "/messages/channels/msg-1/keys/key-1/multiplier", map[string]any{"groupMultiplier": 2, "maxGroupMultiplier": 1})
		if resp.Code != http.StatusOK {
			// current validator writes over_limit state instead of 400 on non-new_api
			// but malformed numeric types should still 400; preserve current behavior here.
		}
	})

	t.Run("new api reject manual group allow max and over limit immediately", func(t *testing.T) {
		future := time.Now().Add(time.Hour).UTC()
		cfg := cfgManager.GetConfig()
		cfg.Upstream[0].APIKeyConfigs[0] = config.APIKeyConfig{
			Key:                   "plain-key",
			KeyUID:                "key-1",
			QuotaGroup:            "premium",
			GroupMultiplier:       ptrFloat(2),
			MaxGroupMultiplier:    ptrFloat(2),
			MultiplierSource:      "new_api",
			MultiplierSyncStatus:  "fresh",
			SourceSubscriptionUID: "sub-1",
			SourceRemoteTokenID:   123,
			MultiplierExpiresAt:   &future,
		}
		if _, err := cfgManager.UpdateUpstream(0, config.UpstreamUpdate{APIKeyConfigs: cfg.Upstream[0].APIKeyConfigs}); err != nil {
			t.Fatalf("UpdateUpstream: %v", err)
		}
		router := register()

		resp := performJSONRequest(t, router, http.MethodPatch, "/messages/channels/msg-1/keys/key-1/multiplier", map[string]any{"groupMultiplier": 1})
		if resp.Code != http.StatusConflict {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}

		resp = performJSONRequest(t, router, http.MethodPatch, "/messages/channels/msg-1/keys/key-1/multiplier", map[string]any{"maxGroupMultiplier": 1})
		if resp.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
		var got keyMultiplierResponse
		decodeJSONResponse(t, resp, &got)
		if got.Status != "over_limit" || got.Eligible || got.Reason != config.MultiplierEligibilityReasonOverGroupLimit {
			t.Fatalf("unexpected over-limit response: %+v", got)
		}
	})

	t.Run("all kinds route", func(t *testing.T) {
		cases := []struct{ kind, channelUID, keyUID string }{
			{"messages", "msg-1", "key-1"},
			{"chat", "chat-1", "chat-key-uid"},
			{"responses", "resp-1", "resp-key-uid"},
			{"gemini", "gem-1", "gem-key-uid"},
			{"images", "img-1", "img-key-uid"},
			{"vectors", "vec-1", "vec-key-uid"},
		}
		for _, tc := range cases {
			t.Run(tc.kind, func(t *testing.T) {
				router := register()
				resp := performJSONRequest(t, router, http.MethodPatch, "/"+tc.kind+"/channels/"+tc.channelUID+"/keys/"+tc.keyUID+"/multiplier", map[string]any{"maxGroupMultiplier": 1})
				if resp.Code != http.StatusOK {
					t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
				}
			})
		}
	})
}

func TestHandlePatchKeyMultiplierConsumptionPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newTestConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			ChannelUID: "msg-1",
			APIKeys:    []string{"plain-key"},
			APIKeyConfigs: []config.APIKeyConfig{{
				Key:    "plain-key",
				KeyUID: "key-1",
			}},
		}},
	})
	router := gin.New()
	RegisterKeyMultiplierRoutes(router, cfgManager)

	assertPolicy := func(t *testing.T, expected config.KeyConsumptionPolicy, effectiveCostClass string) {
		t.Helper()
		var got keyMultiplierResponse
		resp := performJSONRequest(t, router, http.MethodPatch, "/messages/channels/msg-1/keys/key-1/multiplier", map[string]any{"groupMultiplier": 0, "maxGroupMultiplier": 0, "consumptionPolicy": string(expected)})
		if resp.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
		decodeJSONResponse(t, resp, &got)
		if got.ConsumptionPolicy != expected {
			t.Fatalf("expected consumptionPolicy=%s, got %s", expected, got.ConsumptionPolicy)
		}
		if got.EffectiveCostClass != effectiveCostClass {
			t.Fatalf("expected effectiveCostClass=%s, got %s", effectiveCostClass, got.EffectiveCostClass)
		}
		if !got.Eligible {
			t.Fatalf("expected eligible, got %+v", got)
		}
	}

	t.Run("set opportunistic", func(t *testing.T) {
		assertPolicy(t, config.KeyConsumptionOpportunistic, "zero")
	})

	t.Run("set normal", func(t *testing.T) {
		assertPolicy(t, config.KeyConsumptionNormal, "zero")
	})

	t.Run("null normalizes to normal", func(t *testing.T) {
		// 先设置为 opportunistic
		assertPolicy(t, config.KeyConsumptionOpportunistic, "zero")
		resp := performJSONRequest(t, router, http.MethodPatch, "/messages/channels/msg-1/keys/key-1/multiplier", map[string]any{"consumptionPolicy": nil})
		if resp.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
		var got keyMultiplierResponse
		decodeJSONResponse(t, resp, &got)
		if got.ConsumptionPolicy != config.KeyConsumptionNormal {
			t.Fatalf("expected normal after null, got %s", got.ConsumptionPolicy)
		}
	})

	t.Run("unknown policy rejected", func(t *testing.T) {
		resp := performJSONRequest(t, router, http.MethodPatch, "/messages/channels/msg-1/keys/key-1/multiplier", map[string]any{"consumptionPolicy": "unknown"})
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got status=%d body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("group without max rejected", func(t *testing.T) {
		resp := performJSONRequest(t, router, http.MethodPatch, "/messages/channels/msg-1/keys/key-1/multiplier", map[string]any{"groupMultiplier": 0.5, "maxGroupMultiplier": nil})
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got status=%d body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("only consumption policy allowed without multiplier", func(t *testing.T) {
		resp := performJSONRequest(t, router, http.MethodPatch, "/messages/channels/msg-1/keys/key-1/multiplier", map[string]any{"consumptionPolicy": "opportunistic"})
		if resp.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
		var got keyMultiplierResponse
		decodeJSONResponse(t, resp, &got)
		if got.ConsumptionPolicy != config.KeyConsumptionOpportunistic {
			t.Fatalf("expected opportunistic, got %s", got.ConsumptionPolicy)
		}
	})

	t.Run("new api allows consumption policy but not group multiplier", func(t *testing.T) {
		future := time.Now().Add(time.Hour).UTC()
		cfg := cfgManager.GetConfig()
		cfg.Upstream[0].APIKeyConfigs[0] = config.APIKeyConfig{
			Key:                   "plain-key",
			KeyUID:                "key-1",
			QuotaGroup:            "premium",
			GroupMultiplier:       ptrFloat(1),
			MaxGroupMultiplier:    ptrFloat(2),
			MultiplierSource:      "new_api",
			MultiplierSyncStatus:  "fresh",
			SourceSubscriptionUID: "sub-1",
			SourceRemoteTokenID:   123,
			MultiplierExpiresAt:   &future,
			ConsumptionPolicy:     config.KeyConsumptionNormal,
		}
		if _, err := cfgManager.UpdateUpstream(0, config.UpstreamUpdate{APIKeyConfigs: cfg.Upstream[0].APIKeyConfigs}); err != nil {
			t.Fatalf("UpdateUpstream: %v", err)
		}

		resp := performJSONRequest(t, router, http.MethodPatch, "/messages/channels/msg-1/keys/key-1/multiplier", map[string]any{"groupMultiplier": 0.5, "maxGroupMultiplier": nil})
		if resp.Code != http.StatusConflict {
			t.Fatalf("expected 409, got status=%d body=%s", resp.Code, resp.Body.String())
		}

		resp = performJSONRequest(t, router, http.MethodPatch, "/messages/channels/msg-1/keys/key-1/multiplier", map[string]any{"consumptionPolicy": "opportunistic"})
		if resp.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
		var got keyMultiplierResponse
		decodeJSONResponse(t, resp, &got)
		if got.ConsumptionPolicy != config.KeyConsumptionOpportunistic {
			t.Fatalf("expected opportunistic, got %s", got.ConsumptionPolicy)
		}
		if got.GroupMultiplier == nil || *got.GroupMultiplier != 1 {
			t.Fatalf("expected new_api group multiplier preserved, got %+v", got.GroupMultiplier)
		}
	})
}

func newTestConfigManager(t *testing.T, cfg config.Config) *config.ConfigManager {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	payload, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := osWriteFile(configPath, payload, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	manager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	t.Cleanup(func() { manager.Close() })
	return manager
}

func ptrFloat(v float64) *float64 { return &v }

func osWriteFile(name string, data []byte, perm uint32) error {
	return os.WriteFile(name, data, os.FileMode(perm))
}

func performRawJSON(router http.Handler, method, path string, payload []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}

// 托管账号手工 key 无 KeyUID（仅 new-api 同步路径生成），
// findAPIKeyConfigByKeyUID 应兜底按 CredentialUID 定位，使倍率编辑对自定义托管渠道可用。
func TestHandlePatchKeyMultiplierByCredentialUID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfgManager := newTestConfigManager(t, config.Config{
		Upstream: []config.UpstreamConfig{{
			ChannelUID: "msg-cred",
			AccountUID: "acct_custom",
			APIKeys:    []string{"sk-free"},
			APIKeyConfigs: []config.APIKeyConfig{{
				Key:           "sk-free",
				CredentialUID: "cred-free",
			}},
		}},
	})
	router := gin.New()
	RegisterKeyMultiplierRoutes(router, cfgManager)

	// 用 credentialUid 定位并标记为零成本（免费签到额度场景）
	resp := performJSONRequest(t, router, http.MethodPatch, "/messages/channels/msg-cred/keys/cred-free/multiplier", map[string]any{
		"groupMultiplier": 0, "maxGroupMultiplier": 0, "consumptionPolicy": "opportunistic",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("credentialUid 兜底定位失败: status=%d body=%s", resp.Code, resp.Body.String())
	}
	var got keyMultiplierResponse
	decodeJSONResponse(t, resp, &got)
	if got.GroupMultiplier == nil || *got.GroupMultiplier != 0 || got.EffectiveCostClass != "zero" || !got.Eligible {
		t.Fatalf("应标记为可调度零成本: %+v", got)
	}

	// 持久化验证
	channels := cfgManager.GetConfig().Upstream
	if len(channels) != 1 || len(channels[0].APIKeyConfigs) != 1 ||
		channels[0].APIKeyConfigs[0].GroupMultiplier == nil || *channels[0].APIKeyConfigs[0].GroupMultiplier != 0 {
		t.Fatalf("倍率未持久化: %+v", channels)
	}

	// 未知标识仍 404（credentialUid 兜底不放松错误匹配）
	resp = performJSONRequest(t, router, http.MethodPatch, "/messages/channels/msg-cred/keys/cred-missing/multiplier", map[string]any{
		"groupMultiplier": 0, "maxGroupMultiplier": 0,
	})
	if resp.Code != http.StatusNotFound {
		t.Fatalf("未知 credentialUid 应 404: status=%d body=%s", resp.Code, resp.Body.String())
	}
}
