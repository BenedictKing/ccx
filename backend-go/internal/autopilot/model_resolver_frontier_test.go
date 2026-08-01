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

// newFrontierTestResolver 创建开启 frontierRoutingEnabled 的测试 resolver。
func newFrontierTestResolver(t *testing.T, profiles []ModelProfile, mode string) *ModelResolver {
	t.Helper()
	return newTestResolverWithConfig(t, profiles, config.Config{
		AutopilotRouting: config.AutopilotRoutingConfig{
			FrontierRoutingEnabled: true,
			CostPreference:         config.CostPreferenceConfig{Mode: mode},
		},
	})
}

// 开关关闭（默认）时保持旧链行为：qualityRank 绝对主导，premium 的 opus-5 胜出，
// 哪怕它的成本是 sonnet-5 的 2.5 倍、glm-5.2 的 5 倍。
func TestRankEligibleModels_FrontierDisabledKeepsLegacyPick(t *testing.T) {
	best := rankTestModels(frontierIntegrationCandidates(), "claude-sonnet-4-6")
	if best.ModelID != "claude-opus-5" {
		t.Fatalf("legacy chain should pick claude-opus-5, got %s", best.ModelID)
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

// quality_first 车道：opus-5 质量最高但与其区间重叠的候选均为质量并列，
// 并列池中同族最低成本是 claude-sonnet-5——不为噪声级质量差异付 2.5 倍成本。
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

// 成本证据不足时 fail-open 回退旧链，结果与开关关闭完全一致，并标注回退原因。
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
