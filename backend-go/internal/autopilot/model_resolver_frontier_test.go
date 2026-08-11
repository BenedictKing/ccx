package autopilot

import (
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// frontierIntegrationCandidates 构造集成测试候选集（数据均来自内置注册表）：
//   - claude-opus-5   premium，benchmark 82.78，$30/1M+1M
//   - claude-sonnet-5 high，   benchmark 64.53，$12
//   - gpt-5.4         high，   benchmark 73.24，$17.5
//   - glm-5.2         high，   benchmark 62.94，$5.8
//
// 四者 benchmark 均处于 provisional 泳道且无实测置信度，质量区间（±0.15）相互重叠，
// 因此全部留在 Pareto 前沿上；opus 因质量断点独占一簇，其余三者同簇。
func frontierIntegrationCandidates() []ModelProfile {
	return []ModelProfile{
		makeModelProfile("claude-opus-5", ModelFamilyClaude, QualityTierPremium, 1000000,
			true, true, true, true, 0),
		makeModelProfile("claude-sonnet-5", ModelFamilyClaude, QualityTierHigh, 1000000,
			true, true, true, true, 0),
		makeModelProfile("gpt-5.4", ModelFamilyOpenAI, QualityTierHigh, 1000000,
			true, true, true, true, 0),
		makeModelProfile("glm-5.2", ModelFamilyGLM, QualityTierHigh, 1048576,
			true, false, true, true, 0),
	}
}

// newFrontierTestResolver 创建带指定成本倾向车道的测试 resolver。
// Frontier/Ladder 默认启用，无需开关。
func newFrontierTestResolver(t *testing.T, profiles []ModelProfile, mode string) *ModelResolver {
	t.Helper()
	return newTestResolverWithConfig(t, profiles, config.Config{
		AutopilotRouting: config.AutopilotRoutingConfig{
			CostPreference: config.CostPreferenceConfig{Mode: mode},
		},
	})
}

// 默认启用：即使无 cfgManager（balanced 默认车道），frontier 也直接参与选型——
// opus 独占的高质量簇成本溢价值不回质量增益，被拒绝升簇，同簇同族的 sonnet-5 胜出。
func TestRankEligibleModels_FrontierEnabledByDefault(t *testing.T) {
	best := rankTestModels(frontierIntegrationCandidates(), "claude-sonnet-4-6")
	if best.ModelID != "claude-sonnet-5" {
		t.Fatalf("default frontier should pick claude-sonnet-5, got %s", best.ModelID)
	}
}

// balanced 车道：opus 独占的高质量簇相对最便宜簇的成本溢价值不回质量增益
// （+0.18 质量最多接受约 +36% 成本，实际 +155%），被拒绝升簇；
// 同簇内同族优先，claude-sonnet-5 胜出。
func TestRankEligibleModels_FrontierBalancedRejectsCostPremium(t *testing.T) {
	resolver := newFrontierTestResolver(t, nil, "balanced")
	best := resolver.rankEligibleModels(frontierIntegrationCandidates(), "claude-sonnet-4-6", "ch_test", "messages", CapabilityFloor{})
	if best.profile.ModelID != "claude-sonnet-5" {
		t.Fatalf("balanced frontier should pick claude-sonnet-5, got %s", best.profile.ModelID)
	}
	if !strings.Contains(best.frontierNote, "frontier:balanced") {
		t.Fatalf("frontierNote = %q, want frontier:balanced marker", best.frontierNote)
	}
}

// cost_first 车道：无同族候选时取前沿上综合成本最低者 glm-5.2（$5.8）。
func TestRankEligibleModels_FrontierCostFirstPicksCheapest(t *testing.T) {
	resolver := newFrontierTestResolver(t, nil, "cost_first")
	best := resolver.rankEligibleModels(frontierIntegrationCandidates(), "deepseek-v4-flash", "ch_test", "messages", CapabilityFloor{})
	if best.profile.ModelID != "glm-5.2" {
		t.Fatalf("cost_first frontier should pick glm-5.2, got %s", best.profile.ModelID)
	}
	if !strings.Contains(best.frontierNote, "frontier:cost_first") {
		t.Fatalf("frontierNote = %q, want frontier:cost_first marker", best.frontierNote)
	}
}

// quality_first 车道：并列池里默认仍优先低成本；只有 premium 候选 benchmark 差距足够大时，
// 才允许更强模型压过更便宜模型。当前示例 benchmark 差值不足阈值，因此 claude-sonnet-5 仍胜出。
func TestRankEligibleModels_FrontierQualityFirstTieBreaksByCost(t *testing.T) {
	resolver := newFrontierTestResolver(t, nil, "quality_first")
	best := resolver.rankEligibleModels(frontierIntegrationCandidates(), "claude-sonnet-4-6", "ch_test", "messages", CapabilityFloor{})
	if best.profile.ModelID != "claude-sonnet-5" {
		t.Fatalf("quality_first frontier should pick tied-but-cheaper claude-sonnet-5, got %s", best.profile.ModelID)
	}
	if !strings.Contains(best.frontierNote, "tie_pool=") {
		t.Fatalf("frontierNote = %q, want tie_pool marker", best.frontierNote)
	}
}

// 成本证据不足时 fail-open 回退旧链，结果与旧链直接计算一致，并标注回退原因。
func TestRankEligibleModels_FrontierFallbackWithoutComparableCost(t *testing.T) {
	eligible := []ModelProfile{
		makeModelProfile("custom-beta", ModelFamilyClaude, QualityTierNormal, 100000,
			true, false, false, true, 0),
		makeModelProfile("custom-alpha", ModelFamilyClaude, QualityTierNormal, 100000,
			true, false, false, true, 0),
	}
	resolver := newFrontierTestResolver(t, nil, "balanced")
	best := resolver.rankEligibleModels(eligible, "claude-sonnet-5", "ch_test", "messages", CapabilityFloor{})

	legacy := rankTestModels(eligible, "claude-sonnet-5")
	if best.profile.ModelID != legacy.ModelID {
		t.Fatalf("fallback result %s differs from legacy %s", best.profile.ModelID, legacy.ModelID)
	}
	if best.frontierNote != "frontier:fallback=insufficient_comparable_cost" {
		t.Fatalf("frontierNote = %q, want fallback reason", best.frontierNote)
	}
}

func TestBuildRankedCandidates_KeepsComparableModelsAcrossLegacyTiers(t *testing.T) {
	profiles := []ModelProfile{
		makeModelProfile("gpt-5.6-sol", ModelFamilyOpenAI, QualityTierPremium, 272000,
			true, true, true, true, 0),
		makeModelProfile("gpt-5.6-terra", ModelFamilyOpenAI, QualityTierPremium, 272000,
			true, true, true, true, 0),
		makeModelProfile("gpt-5.6-luna", ModelFamilyOpenAI, QualityTierPremium, 272000,
			true, true, true, true, 0),
		makeModelProfile("gpt-5.4-mini", ModelFamilyOpenAI, QualityTierNormal, 272000,
			true, true, true, true, 0),
	}
	resolver := newFrontierTestResolver(t, profiles, "quality_first")
	ranked := resolver.buildRankedCandidates(profiles, "claude-opus-4-8", "ch_test", "responses", CapabilityFloor{
		QualityBenefitCap: QualityTierNormal,
	})
	if len(ranked) != len(profiles) {
		t.Fatalf("Frontier candidates = %d, want all %d models before dynamic clustering", len(ranked), len(profiles))
	}
	seen := make(map[string]bool, len(ranked))
	for _, candidate := range ranked {
		seen[candidate.profile.ModelID] = true
	}
	for _, profile := range profiles {
		if !seen[profile.ModelID] {
			t.Fatalf("model %q was removed by legacy QualityTier before Frontier", profile.ModelID)
		}
	}
}

func TestRankEligibleModels_QualityBenefitCapUsesDynamicCluster(t *testing.T) {
	profiles := []ModelProfile{
		makeModelProfile("gpt-5.6-sol", ModelFamilyOpenAI, QualityTierPremium, 272000,
			true, true, true, true, 0),
		makeModelProfile("gpt-5.5-openai-compact", ModelFamilyOpenAI, QualityTierPremium, 272000,
			true, true, true, true, 0),
		makeModelProfile("gpt-5.5", ModelFamilyOpenAI, QualityTierPremium, 272000,
			true, true, true, true, 0),
	}
	resolver := newFrontierTestResolver(t, profiles, "quality_first")
	best := resolver.rankEligibleModels(profiles, "claude-opus-4-8", "ch_test", "responses", CapabilityFloor{
		QualityBenefitCap: QualityTierNormal,
	})
	if best.profile.ModelID != "gpt-5.5" {
		t.Fatalf("benefit-capped dynamic cluster selected %q, want stable adequate model gpt-5.5", best.profile.ModelID)
	}
	if !strings.Contains(best.frontierNote, "benefit_cap=normal") {
		t.Fatalf("frontierNote = %q, want dynamic benefit cap marker", best.frontierNote)
	}
}
