package autopilot

import (
	"encoding/json"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// ── resolveChannelModels（(渠道, 模型) 展开）专项测试 ──

// kimiReasoningProfile / kimiNonReasoningProfile 是 multiModelAutoConfig 渠道挂载的
// 推理/非推理两个已探测模型画像。
func kimiReasoningProfile() ModelProfile {
	return ModelProfile{
		ChannelUID: "ch_kimi_auto", ChannelKind: "messages", MetricsKey: "m1",
		ModelID: "k3-256k", ModelFamily: ModelFamilyKimi, QualityTier: QualityTierHigh,
		ContextTokens: 262_144, SupportsToolCalls: true, SupportsReasoning: true, ProbeSuccess: true,
	}
}

func kimiNonReasoningProfile() ModelProfile {
	return ModelProfile{
		ChannelUID: "ch_kimi_auto", ChannelKind: "messages", MetricsKey: "m2",
		ModelID: "kimi-lite", ModelFamily: ModelFamilyKimi, QualityTier: QualityTierNormal,
		ContextTokens: 262_144, SupportsToolCalls: true, SupportsReasoning: false, ProbeSuccess: true,
	}
}

// multiModelAutoConfig 构建一个 AutoManaged 渠道与空画像 store（画像由测试自行 upsert）。
func multiModelAutoConfig() (config.Config, *ModelProfileStore) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:            "kimi-auto",
			ChannelUID:      "ch_kimi_auto",
			BaseURL:         "https://kimi.example.com",
			APIKeys:         []string{"sk-kimi"},
			Status:          "active",
			AutoManaged:     true,
			SupportedModels: []string{"*"},
		}},
		AutopilotRouting: config.AutopilotRoutingConfig{
			RoutingMode: "auto",
			ModelMapping: config.ModelMappingRoutingConfig{
				AutoResolve:            true,
				CapabilityFloorEnabled: true,
			},
		},
	}
	return cfg, &ModelProfileStore{cache: map[string]*ModelProfile{}, dirtyKeys: map[string]struct{}{}}
}

func upsertProfiles(t *testing.T, store *ModelProfileStore, profiles ...ModelProfile) {
	t.Helper()
	for i := range profiles {
		p := profiles[i]
		if err := store.Upsert(&p); err != nil {
			t.Fatalf("Upsert() error = %v", err)
		}
	}
}

// AutoManaged 渠道同时挂载推理与非推理模型：展开应产生两行，且同名承接的
// 推理行（与请求模型同名时）与非推理行都带 CandidateKey。
func TestResolveChannelModelsAutoManagedFanout(t *testing.T) {
	cfg, store := multiModelAutoConfig()
	upsertProfiles(t, store, kimiReasoningProfile(), kimiNonReasoningProfile())
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	router := NewSmartRouter(nil, nil, nil, cfgManager)
	router.SetModelResolver(NewModelResolver(store, cfgManager))

	// 请求自适应模型名（允许替代），应枚举出两个模型行。
	profile := BuildRequestProfile(RequestProfileFeatures{
		Model: "claude-opus-4-8", ChannelKind: "messages", Operation: "completion", EstTokens: 1000,
	})
	up := cfgManager.GetConfig().Upstream[0]
	resolutions := router.resolveChannelModels(&profile, &up, cfgManager.GetConfig().UpstreamModelCapabilities)
	if len(resolutions) != 2 {
		t.Fatalf("AutoManaged 展开行数 = %d, want 2: %+v", len(resolutions), resolutions)
	}
	for _, res := range resolutions {
		if !res.Supported {
			t.Errorf("候选行应 supported: %+v", res)
		}
		if res.CandidateKey == "" {
			t.Errorf("候选行应有 CandidateKey: %+v", res)
		}
	}
}

// 推理硬约束分两层生效（架构语义）：
//   - ReasoningNeed=true 时，AutoManaged 枚举在 resolver 层按能力过滤，非推理模型根本不进候选；
//   - 仅当推理模型也进了候选（不强制推理时），硬约束层才按行独立判定。
//
// 这里验证 ReasoningNeed=true 时枚举层剔除 kimi-lite、仅留 k3-256k 且被选中。
func TestResolveChannelModelsReasoningHardConstraint(t *testing.T) {
	cfg, store := multiModelAutoConfig()
	upsertProfiles(t, store, kimiReasoningProfile(), kimiNonReasoningProfile())
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	router := NewSmartRouter(nil, nil, nil, cfgManager)
	router.SetModelResolver(NewModelResolver(store, cfgManager))

	profile := BuildRequestProfile(RequestProfileFeatures{
		Model: "claude-opus-4-8", ChannelKind: "messages", Operation: "completion",
		EstTokens: 1000, ReasoningNeed: true,
	})
	plan := router.BuildPlan(&profile)

	var reasoningSelected bool
	for _, c := range plan.Candidates {
		if c.ChannelUID != "ch_kimi_auto" {
			continue
		}
		if c.MappedModel == "kimi-lite" {
			t.Errorf("ReasoningNeed=true 时非推理模型 kimi-lite 应在枚举层被剔除: %+v", c)
		}
		if c.MappedModel == "k3-256k" && c.Selected {
			reasoningSelected = true
		}
	}
	if !reasoningSelected {
		t.Errorf("推理模型行 k3-256k 应通过并被选中: %+v", plan.Candidates)
	}
}

// 不强制推理时，推理与非推理模型都进候选，各自独立评分；硬约束层不再按推理过滤，
// 两行均 Selected，靠质量档/分数拉开差距。
func TestResolveChannelModelsNoReasoningNeedBothScored(t *testing.T) {
	cfg, store := multiModelAutoConfig()
	upsertProfiles(t, store, kimiReasoningProfile(), kimiNonReasoningProfile())
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	router := NewSmartRouter(nil, nil, nil, cfgManager)
	router.SetModelResolver(NewModelResolver(store, cfgManager))

	profile := BuildRequestProfile(RequestProfileFeatures{
		Model: "claude-opus-4-8", ChannelKind: "messages", Operation: "completion", EstTokens: 1000,
	})
	plan := router.BuildPlan(&profile)

	seen := map[string]bool{}
	for _, c := range plan.Candidates {
		if c.ChannelUID == "ch_kimi_auto" {
			seen[c.MappedModel] = true
		}
	}
	if !seen["k3-256k"] || !seen["kimi-lite"] {
		t.Errorf("不强制推理时应展开两个模型行, got: %+v", plan.Candidates)
	}
}

// 显式映射渠道（单映射目标）展开应恰好一行，无 fan-out 回归。
func TestResolveChannelModelsExplicitMappingSingleRow(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:            "manual",
			ChannelUID:      "ch_manual",
			BaseURL:         "https://manual.example.com",
			APIKeys:         []string{"sk-manual"},
			Status:          "active",
			SupportedModels: []string{"*"},
			ModelMapping:    map[string]string{"claude-opus-4-8": "real-opus"},
		}},
		AutopilotRouting: config.AutopilotRoutingConfig{RoutingMode: "auto"},
	}
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	router := NewSmartRouter(nil, nil, nil, cfgManager)
	profile := BuildRequestProfile(RequestProfileFeatures{
		Model: "claude-opus-4-8", ChannelKind: "messages", Operation: "completion", EstTokens: 1000,
	})
	up := cfgManager.GetConfig().Upstream[0]
	resolutions := router.resolveChannelModels(&profile, &up, cfgManager.GetConfig().UpstreamModelCapabilities)
	if len(resolutions) != 1 {
		t.Fatalf("显式映射渠道展开行数 = %d, want 1: %+v", len(resolutions), resolutions)
	}
	if resolutions[0].MappedModel != "real-opus" || resolutions[0].MappingSource != "explicit_mapping" {
		t.Errorf("显式映射行 = %+v, want real-opus/explicit_mapping", resolutions[0])
	}
}

// 同名承接渠道：解析结果的 ActualModel == 请求模型，CandidateKey 携带请求模型名。
// MappedModel 保持空（避免映射质量档折算误判），展示模型名由 CandidateKey 回退。
func TestResolveChannelModelsSameNameCarryover(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:            "direct",
			ChannelUID:      "ch_direct",
			BaseURL:         "https://direct.example.com",
			APIKeys:         []string{"sk-direct"},
			Status:          "active",
			SupportedModels: []string{"claude-opus-4-8"},
		}},
		AutopilotRouting: config.AutopilotRoutingConfig{RoutingMode: "auto"},
	}
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	router := NewSmartRouter(nil, nil, nil, cfgManager)
	profile := BuildRequestProfile(RequestProfileFeatures{
		Model: "claude-opus-4-8", ChannelKind: "messages", Operation: "completion", EstTokens: 1000,
	})
	up := cfgManager.GetConfig().Upstream[0]
	resolutions := router.resolveChannelModels(&profile, &up, cfgManager.GetConfig().UpstreamModelCapabilities)
	if len(resolutions) != 1 {
		t.Fatalf("同名承接展开行数 = %d, want 1: %+v", len(resolutions), resolutions)
	}
	res := resolutions[0]
	if res.ActualModel != "claude-opus-4-8" {
		t.Errorf("同名承接 ActualModel = %q, want claude-opus-4-8", res.ActualModel)
	}
	if res.MappedModel != "" {
		t.Errorf("同名承接 MappedModel 应为空（避免质量档误判）, got %q", res.MappedModel)
	}
	if res.CandidateKey != "ch_direct|claude-opus-4-8" {
		t.Errorf("同名承接 CandidateKey = %q, want ch_direct|claude-opus-4-8", res.CandidateKey)
	}
}

// SupportedModels include/exclude 收窄可服务集。
func TestResolveChannelModelsSupportedModelsFilter(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:            "filtered",
			ChannelUID:      "ch_filtered",
			BaseURL:         "https://filtered.example.com",
			APIKeys:         []string{"sk-filtered"},
			Status:          "active",
			SupportedModels: []string{"good-*", "!good-blocked"},
			ModelMapping: map[string]string{
				"claude-opus-4-8": "good-model",
				"other":           "good-blocked",
				"third":           "bad-model",
			},
		}},
		AutopilotRouting: config.AutopilotRoutingConfig{RoutingMode: "auto"},
	}
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	router := NewSmartRouter(nil, nil, nil, cfgManager)
	profile := BuildRequestProfile(RequestProfileFeatures{
		Model: "claude-opus-4-8", ChannelKind: "messages", Operation: "completion", EstTokens: 1000,
	})
	up := cfgManager.GetConfig().Upstream[0]
	resolutions := router.resolveChannelModels(&profile, &up, cfgManager.GetConfig().UpstreamModelCapabilities)
	// good-model 命中 include；good-blocked 命中 exclude；bad-model 不命中 include。
	if len(resolutions) != 1 || resolutions[0].ActualModel != "good-model" {
		t.Fatalf("SupportedModels 收窄后 = %+v, want 仅 good-model", resolutions)
	}
}

// fail-open：无可用画像且 supported 时回退单模型行，保证 FallbackUsed 语义。
func TestResolveChannelModelsFailOpenFallback(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:            "fallback",
			ChannelUID:      "ch_fallback",
			BaseURL:         "https://fallback.example.com",
			APIKeys:         []string{"sk-fallback"},
			Status:          "active",
			AutoManaged:     true,
			SupportedModels: []string{"claude-opus-4-8"},
		}},
		AutopilotRouting: config.AutopilotRoutingConfig{RoutingMode: "auto"},
	}
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	router := NewSmartRouter(nil, nil, nil, cfgManager)
	// modelResolver 为 nil：AutoManaged 枚举退化为空，走 fail-open 单数解析。
	profile := BuildRequestProfile(RequestProfileFeatures{
		Model: "claude-opus-4-8", ChannelKind: "messages", Operation: "completion", EstTokens: 1000,
	})
	up := cfgManager.GetConfig().Upstream[0]
	resolutions := router.resolveChannelModels(&profile, &up, cfgManager.GetConfig().UpstreamModelCapabilities)
	if len(resolutions) != 1 || !resolutions[0].Supported {
		t.Fatalf("fail-open 回退 = %+v, want 单行 supported", resolutions)
	}
}

// 旧单模型形态 trace 经序列化往返后 CandidateKey 为空（向后兼容）。
func TestRoutingCandidateLegacyRoundTripEmptyCandidateKey(t *testing.T) {
	legacy := RoutingCandidate{
		ChannelUID:  "ch_legacy",
		ChannelName: "legacy",
		MappedModel: "some-model",
		TotalScore:  5.0,
		Selected:    true,
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal error = %v", err)
	}
	var back RoutingCandidate
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal error = %v", err)
	}
	if back.CandidateKey != "" {
		t.Errorf("旧 trace 反序列化 CandidateKey 应为空, got %q", back.CandidateKey)
	}
	// 序列化不含 candidateKey 字段（omitempty）。
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw error = %v", err)
	}
	if _, present := raw["candidateKey"]; present {
		t.Errorf("空 CandidateKey 不应序列化 candidateKey 字段: %v", raw)
	}
}
