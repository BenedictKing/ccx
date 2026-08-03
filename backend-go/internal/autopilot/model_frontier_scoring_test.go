package autopilot

import (
	"math"
	"strings"
	"testing"
)

// makeFrontierCandidate 构造 frontier 单测用的合成候选。
// benchmarkScore <= 0 表示 benchmark 未知；publicCostUSD <= 0 表示公开价未知。
func makeFrontierCandidate(modelID string, qualityRank int, measured, benchmarkScore, publicCostUSD float64) rankedModelCandidate {
	return rankedModelCandidate{
		profile: ModelProfile{
			ModelID:                   modelID,
			ModelFamily:               ModelFamilyClaude,
			ProviderQualityConfidence: 1.0,
		},
		qualityRank:             qualityRank,
		measuredQualityScore:    measured,
		benchmarkKnown:          benchmarkScore > 0,
		benchmarkScore:          benchmarkScore,
		benchmarkLane:           "verified",
		publicCostKnown:         publicCostUSD > 0,
		normalizedPublicCostUSD: publicCostUSD,
		normalizedCandidateID:   strings.ToLower(modelID),
	}
}

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// ── 质量分合成 ──

func TestFrontierQualityScore(t *testing.T) {
	cases := []struct {
		name     string
		cand     rankedModelCandidate
		expected float64
	}{
		{
			name:     "benchmark 主锚",
			cand:     makeFrontierCandidate("m1", 3, 0.5, 80, 10),
			expected: 0.5*0.8 + 0.3*1.0 + 0.2*0.5, // 0.8
		},
		{
			name:     "benchmark 缺失时权重让渡",
			cand:     makeFrontierCandidate("m2", 1, 0.6, 0, 10),
			expected: 0.6*(1.0/3.0) + 0.4*0.6, // 0.44
		},
		{
			name:     "clamp 到 1.0",
			cand:     makeFrontierCandidate("m3", 3, 1.0, 100, 10),
			expected: 1.0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := frontierQualityScore(tc.cand); !almostEqual(got, tc.expected) {
				t.Fatalf("frontierQualityScore() = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestFrontierQualityHalfWidth(t *testing.T) {
	cases := []struct {
		name       string
		confidence float64
		lane       string
		expected   float64
	}{
		{"高置信 + verified", 1.0, "verified", 0.05},
		{"低置信加宽", 0.0, "verified", 0.10},
		{"provisional 加宽", 1.0, "provisional", 0.075},
		{"低置信 + provisional", 0.0, "provisional", 0.15},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cand := makeFrontierCandidate("m", 2, 0.5, 80, 10)
			cand.profile.ProviderQualityConfidence = tc.confidence
			cand.benchmarkLane = tc.lane
			if got := frontierQualityHalfWidth(cand); !almostEqual(got, tc.expected) {
				t.Fatalf("frontierQualityHalfWidth() = %v, want %v", got, tc.expected)
			}
		})
	}
}

// ── 成本证据 ──

func TestFrontierCostFactorFor(t *testing.T) {
	cases := []struct {
		name     string
		effort   EffortLevel
		decided  bool
		expected float64
	}{
		{"未决定档位", EffortHigh, false, 1.0},
		{"空档位", "", true, 1.0},
		{"low", EffortLow, true, 1.0},
		{"medium", EffortMedium, true, 1.2},
		{"high", EffortHigh, true, 1.5},
		{"max", EffortMax, true, 2.0},
		{"未识别档位", EffortLevel("weird"), true, 1.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cand := rankedModelCandidate{effort: tc.effort, effortDecided: tc.decided}
			if got := frontierCostFactorFor(cand); !almostEqual(got, tc.expected) {
				t.Fatalf("frontierCostFactorFor() = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestFrontierUseProviderMultiplier(t *testing.T) {
	withProvider := func(c rankedModelCandidate) rankedModelCandidate {
		c.providerCostKnown = true
		c.providerCostMultiplier = 2
		return c
	}
	t.Run("全部候选具备倍率时启用", func(t *testing.T) {
		ranked := []rankedModelCandidate{
			withProvider(makeFrontierCandidate("a", 2, 0.5, 80, 10)),
			withProvider(makeFrontierCandidate("b", 2, 0.5, 70, 20)),
		}
		if !frontierUseProviderMultiplier(ranked) {
			t.Fatal("expected provider multiplier axis")
		}
	})
	t.Run("任一候选缺倍率时退回公开价轴", func(t *testing.T) {
		ranked := []rankedModelCandidate{
			withProvider(makeFrontierCandidate("a", 2, 0.5, 80, 10)),
			makeFrontierCandidate("b", 2, 0.5, 70, 20),
		}
		if frontierUseProviderMultiplier(ranked) {
			t.Fatal("expected public USD axis")
		}
	})
	t.Run("无成本证据时不启用", func(t *testing.T) {
		ranked := []rankedModelCandidate{makeFrontierCandidate("a", 2, 0.5, 80, 0)}
		if frontierUseProviderMultiplier(ranked) {
			t.Fatal("expected false without cost evidence")
		}
	})
}

func TestBuildFrontierPoints(t *testing.T) {
	t.Run("成本未知候选被排除", func(t *testing.T) {
		ranked := []rankedModelCandidate{
			makeFrontierCandidate("known", 2, 0.5, 80, 10),
			makeFrontierCandidate("unknown", 2, 0.5, 80, 0),
		}
		points := buildFrontierPoints(ranked, CapabilityFloor{})
		if len(points) != 1 || points[0].CanonicalModel != "known" {
			t.Fatalf("buildFrontierPoints() = %+v, want only the cost-known candidate", points)
		}
	})
	t.Run("effort 成本系数折算进成本轴", func(t *testing.T) {
		cand := makeFrontierCandidate("m", 2, 0.5, 80, 10)
		cand.effort = EffortMax
		cand.effortDecided = true
		points := buildFrontierPoints([]rankedModelCandidate{cand}, CapabilityFloor{})
		if len(points) != 1 || points[0].Cost.Estimated != 20_000_000 {
			t.Fatalf("Estimated = %+v, want 20e6 micro-USD (10 USD x max factor 2.0)", points)
		}
	})
	t.Run("provider 倍率在轴内一致时乘算", func(t *testing.T) {
		cand := makeFrontierCandidate("m", 2, 0.5, 80, 10)
		cand.providerCostKnown = true
		cand.providerCostMultiplier = 3
		points := buildFrontierPoints([]rankedModelCandidate{cand}, CapabilityFloor{})
		if len(points) != 1 || points[0].Cost.ScopeID != frontierCostScopeUSDProvider ||
			points[0].Cost.Estimated != 30_000_000 {
			t.Fatalf("point = %+v, want provider scope with 30e6 micro-USD", points)
		}
	})
	t.Run("候选下标编码进 CandidateID", func(t *testing.T) {
		ranked := []rankedModelCandidate{
			makeFrontierCandidate("skip", 2, 0.5, 80, 0),
			makeFrontierCandidate("keep", 2, 0.5, 80, 10),
		}
		points := buildFrontierPoints(ranked, CapabilityFloor{})
		if len(points) != 1 || points[0].CandidateID != "1" {
			t.Fatalf("CandidateID = %+v, want \"1\"", points)
		}
	})
}

// ── 前沿选择 ──

func TestSelectViaFrontier_InsufficientComparableCost(t *testing.T) {
	ranked := []rankedModelCandidate{
		makeFrontierCandidate("known", 2, 0.5, 80, 10),
		makeFrontierCandidate("unknown", 2, 0.5, 80, 0),
	}
	if _, note, ok := selectViaFrontier(ranked, CapabilityFloor{}, CostPrefBalanced); ok || note != "insufficient_comparable_cost" {
		t.Fatalf("selectViaFrontier() = (_, %q, %v), want insufficient_comparable_cost fallback", note, ok)
	}
}

func TestSelectViaFrontier_EffortInflationGuard(t *testing.T) {
	// 同模型三个已决定档位，质量信号完全一致：等质量下低成本档位必须支配高档位，
	// 任何车道都不得选出 max（档位膨胀防护）。
	efforts := []EffortLevel{EffortLow, EffortMedium, EffortMax}
	ranked := make([]rankedModelCandidate, 0, len(efforts))
	for _, e := range efforts {
		cand := makeFrontierCandidate("same-model", 2, 0.5, 80, 10)
		cand.effort = e
		cand.effortDecided = true
		ranked = append(ranked, cand)
	}
	for _, mode := range []CostPreferenceMode{CostPrefBalanced, CostPrefCostFirst, CostPrefQualityFirst} {
		idx, _, ok := selectViaFrontier(ranked, CapabilityFloor{}, mode)
		if !ok || ranked[idx].effort != EffortLow {
			t.Fatalf("mode %s selected effort %q (ok=%v), want low", mode, ranked[idx].effort, ok)
		}
	}
}

func TestCapClusterCostPremium(t *testing.T) {
	t.Run("质量增益不值回溢价时回退", func(t *testing.T) {
		clusters := []FrontierCluster{
			{Index: 0, AvgQuality: 0.50, AvgCost: 10},
			{Index: 1, AvgQuality: 0.52, AvgCost: 35}, // +0.02 质量，3.5 倍成本
		}
		if got := capClusterCostPremium(clusters, 1); got != 0 {
			t.Fatalf("capClusterCostPremium() = %d, want 0", got)
		}
	})
	t.Run("溢价值回质量增益时保持", func(t *testing.T) {
		clusters := []FrontierCluster{
			{Index: 0, AvgQuality: 0.50, AvgCost: 10},
			{Index: 1, AvgQuality: 0.60, AvgCost: 11}, // +0.10 质量，仅 +10% 成本
		}
		if got := capClusterCostPremium(clusters, 1); got != 1 {
			t.Fatalf("capClusterCostPremium() = %d, want 1", got)
		}
	})
	t.Run("目标已是最便宜簇", func(t *testing.T) {
		clusters := []FrontierCluster{{Index: 0, AvgQuality: 0.5, AvgCost: 10}}
		if got := capClusterCostPremium(clusters, 0); got != 0 {
			t.Fatalf("capClusterCostPremium() = %d, want 0", got)
		}
	})
}

func TestSelectFrontierQualityFirst_PrefersHigherBenchmarkBeforeCheapest(t *testing.T) {
	// A 与 B 同为 premium，benchmark 差距足够大（>=5），quality_first 应先兑现质量证据，
	// 允许更强模型压过更便宜模型。
	a := makeFrontierCandidate("gpt-5.6-sol", 3, 0.55, 81.36, 20)
	a.profile.ModelFamily = ModelFamilyOpenAI
	b := makeFrontierCandidate("gpt-5.4-openai-compact", 3, 0.55, 73.24, 10)
	b.profile.ModelFamily = ModelFamilyOpenAI
	ranked := []rankedModelCandidate{a, b}
	forest := FrontierForest{
		ScopeID: frontierCostScopeUSD,
		Version: frontierEvidenceVersion,
		Clusters: []FrontierCluster{{
			Index: 0,
			Points: []FrontierPoint{
				{CandidateID: "0", QualityScore: 0.84, QualityLow: 0.76, QualityHigh: 0.92, Cost: CostEvidence{Estimated: 20_000_000}},
				{CandidateID: "1", QualityScore: 0.82, QualityLow: 0.74, QualityHigh: 0.90, Cost: CostEvidence{Estimated: 10_000_000}},
			},
		}},
	}
	idx, note := selectFrontierQualityFirst(forest, ranked)
	if idx != 0 {
		t.Fatalf("selectFrontierQualityFirst() = (%d, %q), want idx 0 (higher benchmark point)", idx, note)
	}
	if !strings.Contains(note, "tie_pool=2") {
		t.Fatalf("note = %q, want tie_pool=2", note)
	}
}

func TestSelectFrontierQualityFirst_TiePoolPrefersCheapest(t *testing.T) {
	// A 仅比 B 高 0.02 质量（区间重叠），成本却是 8 倍：并列容差规则必须选 B；
	// C 区间与 A 不重叠，不进并列池。
	ranked := []rankedModelCandidate{
		makeFrontierCandidate("a", 3, 0.5, 80, 80),
		makeFrontierCandidate("b", 3, 0.5, 78, 10),
		makeFrontierCandidate("c", 1, 0.5, 50, 1),
	}
	forest := FrontierForest{
		ScopeID: frontierCostScopeUSD,
		Version: frontierEvidenceVersion,
		Clusters: []FrontierCluster{{
			Index: 0,
			Points: []FrontierPoint{
				{CandidateID: "0", QualityScore: 0.80, QualityLow: 0.75, QualityHigh: 0.85, Cost: CostEvidence{Estimated: 80_000_000}},
				{CandidateID: "1", QualityScore: 0.78, QualityLow: 0.73, QualityHigh: 0.83, Cost: CostEvidence{Estimated: 10_000_000}},
				{CandidateID: "2", QualityScore: 0.50, QualityLow: 0.45, QualityHigh: 0.55, Cost: CostEvidence{Estimated: 1_000_000}},
			},
		}},
	}
	idx, note := selectFrontierQualityFirst(forest, ranked)
	if idx != 1 {
		t.Fatalf("selectFrontierQualityFirst() = (%d, %q), want idx 1 (cheapest tied point)", idx, note)
	}
	if !strings.Contains(note, "tie_pool=2") {
		t.Fatalf("note = %q, want tie_pool=2", note)
	}
}

func TestPickFrontierPoint_SameFamilyPreferred(t *testing.T) {
	cheap := makeFrontierCandidate("cheap", 2, 0.5, 80, 5)
	cheap.sameFamily = false
	family := makeFrontierCandidate("family", 2, 0.5, 80, 10)
	family.sameFamily = true
	ranked := []rankedModelCandidate{cheap, family}
	points := []FrontierPoint{
		{CandidateID: "0", Cost: CostEvidence{Estimated: 5_000_000}},
		{CandidateID: "1", Cost: CostEvidence{Estimated: 10_000_000}},
	}
	if got := pickFrontierPoint(points, ranked); got != 1 {
		t.Fatalf("pickFrontierPoint() = %d, want 1 (same family over lower cost)", got)
	}
}
