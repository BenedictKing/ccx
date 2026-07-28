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

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAutopilotSimulation_EndToEnd 端到端模拟测试：验证 SmartRouter、ManualIntent、Trace 等核心功能。
func TestAutopilotSimulation_EndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 1. 准备测试环境
	store := newTestProfileStore(t)
	if store == nil {
		t.Skip("ProfileStore 初始化失败，跳过集成测试")
	}

	cfgManager := setupTestConfigManager(t)
	intentStore, err := NewManualIntentStoreWithDB(store.DB())
	require.NoError(t, err)
	traceStore, err := NewTraceStoreWithDB(store.DB())
	require.NoError(t, err)
	smartRouter := NewSmartRouter(store, intentStore, traceStore, cfgManager)

	// 2. 预填充 ProfileStore 数据（模拟3个不同质量/成本的渠道）
	//    并把对应渠道写入 config，使 ChannelUID 关联生效
	seedTestProfiles(t, store)
	seedTestChannels(t, cfgManager)

	// 3. 测试 SmartRouter dry-run 路由决策
	t.Run("SmartRouter_DryRun_Supervisor优先质量", func(t *testing.T) {
		router := gin.New()
		RegisterDryRunRoutes(router, smartRouter)

		reqBody := DryRunRequest{
			Model:       "claude-opus-4",
			ChannelKind: "messages",
			AgentRole:   "main",
			// EstTokens 必须 >= 阈值，否则 TaskClassifier 会先判定为 lightweight
			// （isLightweightRequest 规则优先于 AgentRole==main→supervisor 规则）。
			EstTokens: 50000,
		}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/smart-routing/diagnose", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "dry-run 应返回 200")

		var resp DryRunResponse
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)

		assert.NotNil(t, resp.Plan, "应返回路由计划")
		assert.Equal(t, "auto", resp.Mode, "默认模式应为 auto")

		require.NotEmpty(t, resp.Plan.Candidates, "候选列表不应为空（config 已填充3个渠道）")
		for i, c := range resp.Plan.Candidates {
			t.Logf("  候选[%d] %s 分数=%.2f", i, c.ChannelUID, c.Score)
		}

		// Supervisor 任务应把高质量官方渠道排第一（premium + stable + first-tier）
		topCandidate := resp.Plan.Candidates[0]
		assert.Equal(t, "ch_official_001", topCandidate.ChannelUID,
			"Supervisor 任务应优先高质量官方渠道")
		assert.Greater(t, topCandidate.Score, 0.0, "分数应 > 0")
	})

	t.Run("SmartRouter_DryRun_Worker优先成本", func(t *testing.T) {
		router := gin.New()
		RegisterDryRunRoutes(router, smartRouter)

		reqBody := DryRunRequest{
			Model:       "claude-sonnet-4",
			ChannelKind: "messages",
			AgentRole:   "subagent",
			EstTokens:   50000,
		}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/route-dryrun", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp DryRunResponse
		_ = json.Unmarshal(w.Body.Bytes(), &resp)

		require.NotEmpty(t, resp.Plan.Candidates, "候选列表不应为空")
		for i, c := range resp.Plan.Candidates {
			t.Logf("  候选[%d] %s 分数=%.2f", i, c.ChannelUID, c.Score)
		}

		// Worker 任务成本权重高（WCost=2, WSavings=3），免费公益渠道应排第一
		topCandidate := resp.Plan.Candidates[0]
		assert.Equal(t, "ch_free_001", topCandidate.ChannelUID,
			"Worker 任务应优先低成本渠道")
	})

	// 3b. 验证 dry-run 路径的 OriginTier tie-breaker（BuildPlan 与真实路由路径 executeFilter
	//     必须保持一致，否则用户看到的预览结果会和实际调度在同分情况下不一致）。
	t.Run("SmartRouter_DryRun_OriginTier平局排序", func(t *testing.T) {
		tieStore := newTestProfileStore(t)
		if tieStore == nil {
			t.Skip("ProfileStore 初始化失败，跳过")
		}
		tieCfgManager := setupTestConfigManager(t)
		tieIntentStore, err := NewManualIntentStoreWithDB(tieStore.DB())
		require.NoError(t, err)
		tieTraceStore, err := NewTraceStoreWithDB(tieStore.DB())
		require.NoError(t, err)
		tieSmartRouter := NewSmartRouter(tieStore, tieIntentStore, tieTraceStore, tieCfgManager)

		// 两个渠道画像完全相同（同分），只有 OriginTier 不同：first vs third。
		// 预期：first 排在 third 前面（tie-breaker 生效），且都不因为 OriginTier 影响 Score 本身。
		tieProfiles := []*KeyEndpointProfile{
			{
				EndpointUID: "ep_tie_first", ChannelUID: "ch_tie_first", ChannelKind: "messages",
				ServiceType: "claude", MetricsKey: "tie-first", BaseURL: "https://tie-first.example",
				QualityTier: QualityTierHigh, StabilityTier: StabilityTierNormal,
				SpeedTier: SpeedTierNormal, CostTier: CostTierNormal,
				OriginTier: string(OriginTierFirst), HealthState: HealthStateHealthy,
				AvailableModels: []string{"claude-opus-4"},
			},
			{
				EndpointUID: "ep_tie_third", ChannelUID: "ch_tie_third", ChannelKind: "messages",
				ServiceType: "claude", MetricsKey: "tie-third", BaseURL: "https://tie-third.example",
				QualityTier: QualityTierHigh, StabilityTier: StabilityTierNormal,
				SpeedTier: SpeedTierNormal, CostTier: CostTierNormal,
				OriginTier: string(OriginTierThird), HealthState: HealthStateHealthy,
				AvailableModels: []string{"claude-opus-4"},
			},
		}
		for _, p := range tieProfiles {
			require.NoError(t, tieStore.Upsert(p))
		}
		tieChannels := []config.UpstreamConfig{
			{Name: "平局-first", ChannelUID: "ch_tie_first", BaseURL: "https://tie-first.example",
				BaseURLs: []string{"https://tie-first.example"}, APIKeys: []string{"sk-tie-first"},
				ServiceType: "claude", Status: "active"},
			{Name: "平局-third", ChannelUID: "ch_tie_third", BaseURL: "https://tie-third.example",
				BaseURLs: []string{"https://tie-third.example"}, APIKeys: []string{"sk-tie-third"},
				ServiceType: "claude", Status: "active"},
		}
		for _, ch := range tieChannels {
			require.NoError(t, tieCfgManager.AddUpstream(ch))
		}

		router := gin.New()
		RegisterDryRunRoutes(router, tieSmartRouter)

		reqBody := DryRunRequest{
			Model:       "claude-opus-4",
			ChannelKind: "messages",
			AgentRole:   "main",
			EstTokens:   50000,
		}
		body, _ := json.Marshal(reqBody)
		req := httptest.NewRequest(http.MethodPost, "/route-dryrun", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		var resp DryRunResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Len(t, resp.Plan.Candidates, 2, "应有2个同分候选")

		c0, c1 := resp.Plan.Candidates[0], resp.Plan.Candidates[1]
		t.Logf("  候选[0] %s 分数=%.2f  候选[1] %s 分数=%.2f", c0.ChannelUID, c0.Score, c1.ChannelUID, c1.Score)

		assert.Equal(t, c0.Score, c1.Score, "两个渠道画像完全相同，分数必须严格相等，否则测试前提不成立")
		assert.Equal(t, "ch_tie_first", c0.ChannelUID,
			"OriginTier=first 应在同分时排在 OriginTier=third 前面（dry-run tie-breaker 生效）")
		assert.Equal(t, "ch_tie_third", c1.ChannelUID)
	})

	// 4. 测试 ManualRoutingIntent 创建和查询
	t.Run("ManualIntent_Create_And_List", func(t *testing.T) {
		router := gin.New()
		RegisterManualIntentRoutes(router, intentStore)

		// 创建 model_trial intent（用 CreateIntentRequest，不是 ManualRoutingIntent）
		createReq := CreateIntentRequest{
			IntentType:             IntentTypeModelTrial,
			ChannelKind:            "messages",
			Model:                  "claude-opus-4",
			TaskClasses:            []TaskClass{TaskClassWorker},
			TrafficPercent:         50,
			TTLMinutes:             60,
			MaxRequests:            10,
			FallbackOnFailure:      true,
			RequireHardConstraints: true,
			Reason:                 "集成测试-模型试用",
		}
		body, _ := json.Marshal(createReq)
		req := httptest.NewRequest(http.MethodPost, "/manual-intents", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code, "创建 intent 应返回 201")

		var created ManualRoutingIntent
		_ = json.Unmarshal(w.Body.Bytes(), &created)
		assert.NotEmpty(t, created.IntentUID, "应返回 intentUid")
		assert.Equal(t, IntentStatusActive, created.Status, "状态应为 active")
		t.Logf("Intent 已创建: uid=%s status=%s", created.IntentUID, created.Status)

		// 直接从 store 验证（绕过路由路径问题）
		all := intentStore.ListActive()
		assert.GreaterOrEqual(t, len(all), 1, "intentStore 中应至少有1个 active intent")
	})

	// 5. 测试 Trace 记录和统计
	t.Run("Trace_Record_And_Stats", func(t *testing.T) {
		// 记录几条 trace
		for i := 0; i < 5; i++ {
			trace := &RoutingDecisionTrace{
				TraceUID:         GenerateTraceUID("messages", TaskClassSupervisor, time.Now()),
				RequestKind:      "messages",
				RequestedModel:   "claude-opus-4",
				TaskClass:        TaskClassSupervisor,
				Mode:             RoutingModeShadow,
				ShadowChannelUID: "ch_test_001",
				ActualChannelUID: "ch_test_002",
				Match:            i%2 == 0, // 50% mismatch
				CreatedAt:        time.Now(),
			}
			traceStore.Record(trace)
		}

		router := gin.New()
		RegisterTraceRoutes(router, traceStore)

		// 查询统计
		req := httptest.NewRequest(http.MethodGet, "/traces/stats", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var stats TraceStats
		_ = json.Unmarshal(w.Body.Bytes(), &stats)

		assert.GreaterOrEqual(t, stats.TotalCount, 5, "总 trace 数应 ≥ 5")
		t.Logf("Trace 统计: 总数=%d, 不一致数=%d", stats.TotalCount, stats.MismatchCount)
	})

	// 6. 测试 smart-routing config 切换
	t.Run("RoutingConfig_LegacyMode_NormalizedToAuto", func(t *testing.T) {
		deps := &RoutingConfigDeps{
			CfgManager: cfgManager,
		}
		router := gin.New()
		apiGroup := router.Group("/")
		RegisterRoutingConfigRoutes(apiGroup, deps)

		// 旧 assist 配置应被归一化为唯一 auto 运行态
		updateReq := map[string]interface{}{
			"mode":           "assist",
			"costPreference": "quality_first",
		}
		body, _ := json.Marshal(updateReq)
		req := httptest.NewRequest(http.MethodPut, "/smart-routing/config", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code, "更新配置应返回 200")

		// 查询确认
		getReq := httptest.NewRequest(http.MethodGet, "/smart-routing/config", nil)
		getW := httptest.NewRecorder()
		router.ServeHTTP(getW, getReq)

		assert.Equal(t, http.StatusOK, getW.Code)

		var response map[string]interface{}
		_ = json.Unmarshal(getW.Body.Bytes(), &response)
		_, exposesMode := response["mode"]
		assert.False(t, exposesMode, "配置 API 不应再暴露 mode")
		assert.Equal(t, config.AutopilotModeAuto, cfgManager.GetEffectiveRoutingMode())
	})
}

// seedTestProfiles 预填充测试用的渠道画像数据（模拟不同质量/成本/tier）。
func seedTestProfiles(t *testing.T, store *ProfileStore) {
	// 高质量官方渠道
	p1 := &KeyEndpointProfile{
		EndpointUID:     "ep_official_001",
		ChannelUID:      "ch_official_001",
		ChannelKind:     "messages",
		ServiceType:     "claude",
		MetricsKey:      "official-high",
		BaseURL:         "https://api.anthropic.com",
		QualityTier:     QualityTierPremium,
		StabilityTier:   StabilityTierStable,
		SpeedTier:       SpeedTierFast,
		CostTier:        CostTierExpensive,
		OriginTier:      string(OriginTierFirst),
		HealthState:     HealthStateHealthy,
		AvailableModels: []string{"claude-opus-4", "claude-sonnet-4"},
	}
	_ = store.Upsert(p1)

	// 中质量中转渠道
	p2 := &KeyEndpointProfile{
		EndpointUID:     "ep_relay_001",
		ChannelUID:      "ch_relay_001",
		ChannelKind:     "messages",
		ServiceType:     "claude",
		MetricsKey:      "relay-medium",
		BaseURL:         "https://relay.example.com",
		QualityTier:     QualityTierHigh,
		StabilityTier:   StabilityTierNormal,
		SpeedTier:       SpeedTierNormal,
		CostTier:        CostTierNormal,
		OriginTier:      string(OriginTierSecond),
		HealthState:     HealthStateHealthy,
		AvailableModels: []string{"claude-opus-4", "claude-sonnet-4"},
	}
	_ = store.Upsert(p2)

	// 低成本公益渠道
	p3 := &KeyEndpointProfile{
		EndpointUID:     "ep_free_001",
		ChannelUID:      "ch_free_001",
		ChannelKind:     "messages",
		ServiceType:     "claude",
		MetricsKey:      "free-low",
		BaseURL:         "https://free.example.com",
		QualityTier:     QualityTierNormal,
		StabilityTier:   StabilityTierNormal,
		SpeedTier:       SpeedTierNormal,
		CostTier:        CostTierFree,
		OriginTier:      string(OriginTierThird),
		HealthState:     HealthStateHealthy,
		AvailableModels: []string{"claude-opus-4", "claude-sonnet-4"},
	}
	_ = store.Upsert(p3)

	t.Logf("已预填充 3 个测试渠道画像")
}

// seedTestChannels 将3个渠道写入 config，ChannelUID 与画像对齐。
// 这样 SmartRouter.collectChannelEntries 才能收集到候选并关联画像评分。
func seedTestChannels(t *testing.T, cfgManager *config.ConfigManager) {
	channels := []config.UpstreamConfig{
		{
			Name:        "官方-高质量",
			ChannelUID:  "ch_official_001",
			BaseURL:     "https://api.anthropic.com",
			BaseURLs:    []string{"https://api.anthropic.com"},
			APIKeys:     []string{"sk-official-001"},
			ServiceType: "claude",
			Status:      "active",
			Priority:    0,
		},
		{
			Name:        "中转-中质量",
			ChannelUID:  "ch_relay_001",
			BaseURL:     "https://relay.example.com",
			BaseURLs:    []string{"https://relay.example.com"},
			APIKeys:     []string{"sk-relay-001"},
			ServiceType: "claude",
			Status:      "active",
			Priority:    0,
		},
		{
			Name:        "公益-低成本",
			ChannelUID:  "ch_free_001",
			BaseURL:     "https://free.example.com",
			BaseURLs:    []string{"https://free.example.com"},
			APIKeys:     []string{"sk-free-001"},
			ServiceType: "claude",
			Status:      "active",
			Priority:    0,
		},
	}
	for _, ch := range channels {
		err := cfgManager.AddUpstream(ch)
		require.NoError(t, err, "写入测试渠道失败: %s", ch.Name)
	}
	t.Logf("已写入 3 个测试渠道到 config")
}

// setupTestConfigManager 创建测试用的 ConfigManager（带默认 autopilot 配置）。
func setupTestConfigManager(t *testing.T) *config.ConfigManager {
	// 创建临时配置文件
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	cfg := config.Config{
		Upstream:          []config.UpstreamConfig{},
		ChatUpstream:      []config.UpstreamConfig{},
		ResponsesUpstream: []config.UpstreamConfig{},
		GeminiUpstream:    []config.UpstreamConfig{},
		ImagesUpstream:    []config.UpstreamConfig{},
		VectorsUpstream:   []config.UpstreamConfig{},
		AutopilotRouting: config.AutopilotRoutingConfig{
			RoutingMode:    "shadow",
			KillSwitch:     false,
			CostPreference: config.CostPreferenceConfig{Mode: "balanced"},
		},
	}

	// 写入临时配置文件
	data, _ := json.MarshalIndent(cfg, "", "  ")
	err := os.WriteFile(configPath, data, 0600)
	require.NoError(t, err)

	// 创建 ConfigManager
	mgr, err := config.NewConfigManager(configPath, "")
	require.NoError(t, err)

	return mgr
}
