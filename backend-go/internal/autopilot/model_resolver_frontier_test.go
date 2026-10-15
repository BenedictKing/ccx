package autopilot

import (
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/presetstore"
)

// ── 前沿选型集成用例的固定注册表 fixture ──
//
// 这些用例早期直接借用内置注册表的真实模型数据，但注册表每次快照刷新都会
// 微调 overallScore/价格，而 balanced 溢价帽在真实数据上恰好贴着帽线
//（opus 簇成本约为便宜簇的 2.5 倍，与帽线只差百分之几），2026-10-08 快照
// 刷新即把用例掀翻。fixture 改用 -fx 后缀的独立条目，数值刻意远离两条
// 决策边界，数据刷新不再影响单测：
//   - 聚类断点：便宜簇内 bench 差 1-2 分（gap 0.01-0.02 << minClusterGap=0.08）；
//     opus 断点含档位差折算后 gap ≈ 0.31 > maxClusterGap=0.12，任何阈值下都独占一簇；
//   - 溢价帽：质量增益 gain ≈ 0.162，balanced 有效系数 9~12
//     （frontierMaxCostPremiumPerQualityBase=10 × 任务类 0.9~1.2）下帽线
//     最多 1+0.162×12 ≈ 2.94，实际成本比 30/10 = 3.0，全范围内都被拒绝。

// frontierFixtureSpec 定义 fixture 模型的基准分与公开价（per 1M tokens, USD）。
type frontierFixtureSpec struct {
	bench     float64
	inputUSD  float64
	outputUSD float64
}

var frontierFixtureRegistry = map[string]frontierFixtureSpec{
	"glm-5.2-fx":         {bench: 67, inputUSD: 1.5, outputUSD: 2.5}, // 1M+1M = $4
	"claude-sonnet-5-fx": {bench: 69, inputUSD: 4, outputUSD: 6},     // $10
	"gpt-5.4-fx":         {bench: 70, inputUSD: 6, outputUSD: 10},    // $16
	"claude-opus-5-fx":   {bench: 81, inputUSD: 12, outputUSD: 18},   // $30
}

// withFrontierFixtureRegistry 把 -fx fixture 条目追加进内置注册表快照
// （后缀条目不参与真实模型解析），测试结束恢复原 store。
func withFrontierFixtureRegistry(t *testing.T) {
	t.Helper()
	previous := presetstore.Default()
	bundle := previous.Get()
	if bundle.ModelRegistry == nil {
		t.Fatalf("内置注册表快照缺少 modelRegistry，无法安装 fixture")
	}
	for modelID, spec := range frontierFixtureRegistry {
		bundle.ModelRegistry.BenchmarkProfiles = append(bundle.ModelRegistry.BenchmarkProfiles,
			presetstore.ModelBenchmarkProfilePreset{
				Patterns:             []string{modelID},
				CanonicalModel:       modelID,
				OverallScore:         spec.bench,
				CategoryScores:       map[string]float64{"general": spec.bench},
				Sources:              []string{"frontier-test-fixture"},
				VerifiedAt:           "2026-10-15",
				Lane:                 "provisional",
				SharedResults:        1,
				ComparableCategories: 1,
				TotalCategories:      1,
			})
		input, output := spec.inputUSD, spec.outputUSD
		bundle.ModelRegistry.UpstreamCapabilities = append(bundle.ModelRegistry.UpstreamCapabilities,
			presetstore.ModelRegistryCapabilityPreset{
				Patterns: []string{modelID},
				Pricing: &presetstore.ModelPricingPreset{
					Unit:                "per_1m_tokens_usd",
					InputCacheMissPrice: &input,
					OutputPrice:         &output,
				},
			})
	}
	presetstore.SetDefault(presetstore.NewPresetStore(bundle))
	t.Cleanup(func() {
		presetstore.SetDefault(previous)
	})
}

// frontierIntegrationCandidates 构造集成测试候选集（数据由 withFrontierFixtureRegistry
// 钉住的 -fx fixture 条目提供，与内置注册表的真实数据解耦）：
//   - claude-opus-5-fx   premium，bench 81，$30/1M+1M
//   - claude-sonnet-5-fx high，   bench 69，$10
//   - gpt-5.4-fx         high，   bench 70，$16
//   - glm-5.2-fx         high，   bench 67，$4
//
// 四者 benchmark 均处于 provisional 泳道且无实测置信度，质量区间（±0.15）相互重叠，
// 因此全部留在 Pareto 前沿上；opus 因质量断点独占一簇，其余三者同簇。
func frontierIntegrationCandidates() []ModelProfile {
	return []ModelProfile{
		makeModelProfile("claude-opus-5-fx", ModelFamilyClaude, QualityTierPremium, 1000000,
			true, true, true, true, 0),
		makeModelProfile("claude-sonnet-5-fx", ModelFamilyClaude, QualityTierHigh, 1000000,
			true, true, true, true, 0),
		makeModelProfile("gpt-5.4-fx", ModelFamilyOpenAI, QualityTierHigh, 1000000,
			true, true, true, true, 0),
		makeModelProfile("glm-5.2-fx", ModelFamilyGLM, QualityTierHigh, 1048576,
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
// opus 独占的高质量簇成本溢价值不回质量增益，被拒绝升簇，同簇同族的 sonnet-5-fx 胜出。
func TestRankEligibleModels_FrontierEnabledByDefault(t *testing.T) {
	withFrontierFixtureRegistry(t)
	best := rankTestModels(frontierIntegrationCandidates(), "claude-sonnet-4-6")
	if best.ModelID != "claude-sonnet-5-fx" {
		t.Fatalf("default frontier should pick claude-sonnet-5-fx, got %s", best.ModelID)
	}
}

// balanced 车道：opus 独占的高质量簇相对最便宜簇的成本溢价值不回质量增益
// （质量增益 ≈0.162，帽线 1+gain×9~12 ≈ 2.5~2.9，实际成本比 3.0），被拒绝升簇；
// 同簇内同族优先，claude-sonnet-5-fx 胜出。
func TestRankEligibleModels_FrontierBalancedRejectsCostPremium(t *testing.T) {
	withFrontierFixtureRegistry(t)
	resolver := newFrontierTestResolver(t, nil, "balanced")
	best := resolver.rankEligibleModels(frontierIntegrationCandidates(), "claude-sonnet-4-6", "ch_test", "messages", CapabilityFloor{})
	if best.profile.ModelID != "claude-sonnet-5-fx" {
		t.Fatalf("balanced frontier should pick claude-sonnet-5-fx, got %s", best.profile.ModelID)
	}
	if !strings.Contains(best.frontierNote, "frontier:balanced") {
		t.Fatalf("frontierNote = %q, want frontier:balanced marker", best.frontierNote)
	}
}

// cost_first 车道：无同族候选时取前沿上综合成本最低者 glm-5.2-fx（$4）。
func TestRankEligibleModels_FrontierCostFirstPicksCheapest(t *testing.T) {
	withFrontierFixtureRegistry(t)
	resolver := newFrontierTestResolver(t, nil, "cost_first")
	best := resolver.rankEligibleModels(frontierIntegrationCandidates(), "deepseek-v4-flash", "ch_test", "messages", CapabilityFloor{})
	if best.profile.ModelID != "glm-5.2-fx" {
		t.Fatalf("cost_first frontier should pick glm-5.2-fx, got %s", best.profile.ModelID)
	}
	if !strings.Contains(best.frontierNote, "frontier:cost_first") {
		t.Fatalf("frontierNote = %q, want frontier:cost_first marker", best.frontierNote)
	}
}

// quality_first 车道：显著 benchmark 优势（≥premiumFrontierBenchmarkMinDelta）直接兑现，
// 不被低成本并列池吞掉。当前示例中 claude-opus-5-fx(81) 显著高于
// claude-sonnet-5-fx(69)，差距 12 分超过 5 分阈值，因此 quality_first 应选择更强的
// opus-5-fx；只有差距不足阈值时才按成本 tie-break。
func TestRankEligibleModels_FrontierQualityFirstRespectsBenchmarkDelta(t *testing.T) {
	withFrontierFixtureRegistry(t)
	resolver := newFrontierTestResolver(t, nil, "quality_first")
	best := resolver.rankEligibleModels(frontierIntegrationCandidates(), "claude-sonnet-4-6", "ch_test", "messages", CapabilityFloor{})
	if best.profile.ModelID != "claude-opus-5-fx" {
		t.Fatalf("quality_first frontier should pick significantly stronger claude-opus-5-fx, got %s", best.profile.ModelID)
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
	// 注册表快照演进后 sol($4/$20)比 gpt-5.5($5/$30)便宜且 bench 更高，
	// cap 簇内按"便宜优先"应选 sol——cap 语义是不为超档质量付溢价，而非禁选高档
	if best.profile.ModelID != "gpt-5.6-sol" {
		t.Fatalf("benefit-capped dynamic cluster selected %q, want cheapest adequate model gpt-5.6-sol", best.profile.ModelID)
	}
	if !strings.Contains(best.frontierNote, "benefit_cap=normal") {
		t.Fatalf("frontierNote = %q, want dynamic benefit cap marker", best.frontierNote)
	}
}
