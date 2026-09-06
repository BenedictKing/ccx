package autopilot

import (
	"math"
	"strconv"
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
			expected: 0.8*(1.0/3.0) + 0.2*0.6, // balanced: 0.3867
		},
		{
			name:     "clamp 到 1.0",
			cand:     makeFrontierCandidate("m3", 3, 1.0, 100, 10),
			expected: 1.0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := frontierQualityScore(tc.cand, CostPrefBalanced); !almostEqual(got, tc.expected) {
				t.Fatalf("frontierQualityScore() = %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestFrontierQualityHalfWidth(t *testing.T) {
	cases := []struct {
		name       string
		benchKnown bool
		confidence float64
		lane       string
		expected   float64
	}{
		{"有基准分：高置信 + verified", true, 1.0, "verified", 0.05},
		// 区间加宽只适用于缺乏实测基准的候选：有校准 benchmark 分的主锚已是
		// 独立测量，再叠加渠道置信度/泳道倍率会把真实质量差距压回噪声并列。
		{"有基准分：低置信不叠加加宽", true, 0.0, "verified", 0.05},
		{"有基准分：provisional 不叠加加宽", true, 1.0, "provisional", 0.05},
		{"有基准分：低置信 + provisional 不叠加加宽", true, 0.0, "provisional", 0.05},
		{"无基准分：高置信 + verified", false, 1.0, "verified", 0.05},
		{"无基准分：低置信加宽", false, 0.0, "verified", 0.10},
		{"无基准分：provisional 加宽", false, 1.0, "provisional", 0.075},
		{"无基准分：低置信 + provisional", false, 0.0, "provisional", 0.15},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bench := 0.0
			if tc.benchKnown {
				bench = 80
			}
			cand := makeFrontierCandidate("m", 2, 0.5, bench, 10)
			cand.profile.ProviderQualityConfidence = tc.confidence
			cand.benchmarkLane = tc.lane
			if got := frontierQualityHalfWidth(cand, CostPrefBalanced, 0); !almostEqual(got, tc.expected) {
				t.Fatalf("frontierQualityHalfWidth() = %v, want %v", got, tc.expected)
			}
		})
	}
}

// ── 成本证据 ──

func TestFrontierCostFactorFor(t *testing.T) {
	cases := []struct {
		name        string
		cand        rankedModelCandidate
		useMeasured bool
		expected    float64
	}{
		{"未决定档位", rankedModelCandidate{effort: EffortHigh, effortDecided: false}, true, 1.0},
		{"空档位", rankedModelCandidate{effort: "", effortDecided: true}, true, 1.0},
		{"low", rankedModelCandidate{effort: EffortLow, effortDecided: true}, true, 1.0},
		{"medium", rankedModelCandidate{effort: EffortMedium, effortDecided: true}, true, 1.2},
		{"high", rankedModelCandidate{effort: EffortHigh, effortDecided: true}, true, 1.5},
		{"xhigh", rankedModelCandidate{effort: EffortXhigh, effortDecided: true}, true, 1.7},
		{"max", rankedModelCandidate{effort: EffortMax, effortDecided: true}, true, 2.0},
		{"ultra", rankedModelCandidate{effort: EffortUltra, effortDecided: true}, true, 2.3},
		{"未识别档位", rankedModelCandidate{effort: EffortLevel("weird"), effortDecided: true}, true, 1.0},
		{
			name: "实测 cost 替换推测系数",
			cand: rankedModelCandidate{
				effort:                  EffortMax,
				effortDecided:           true,
				publicCostKnown:         true,
				normalizedPublicCostUSD: 10,
				measuredCostUSD:         25,
			},
			useMeasured: true,
			expected:    2.5,
		},
		{
			name: "本批未启用实测路径时忽略实测 cost",
			cand: rankedModelCandidate{
				effort:                  EffortMax,
				effortDecided:           true,
				publicCostKnown:         true,
				normalizedPublicCostUSD: 10,
				measuredCostUSD:         25,
			},
			useMeasured: false,
			expected:    2.0,
		},
		{
			name: "实测 cost 缺失时回退推测系数",
			cand: rankedModelCandidate{
				effort:                  EffortMax,
				effortDecided:           true,
				publicCostKnown:         true,
				normalizedPublicCostUSD: 10,
				measuredCostUSD:         0,
			},
			useMeasured: true,
			expected:    2.0,
		},
		{
			name: "公开价未知时不使用实测 cost",
			cand: rankedModelCandidate{
				effort:                  EffortMax,
				effortDecided:           true,
				publicCostKnown:         false,
				normalizedPublicCostUSD: 0,
				measuredCostUSD:         25,
			},
			useMeasured: true,
			expected:    2.0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := frontierCostFactorFor(tc.cand, tc.useMeasured); !almostEqual(got, tc.expected) {
				t.Fatalf("frontierCostFactorFor() = %v, want %v", got, tc.expected)
			}
		})
	}
}

// TestBuildFrontierPoints_MeasuredCostCalibration 验证实测 cost 校准成本轴且来源标记正确。
func TestBuildFrontierPoints_MeasuredCostCalibration(t *testing.T) {
	cand := makeFrontierCandidate("m", 2, 0.5, 80, 10)
	cand.effort = EffortHigh
	cand.effortDecided = true
	cand.measuredCostUSD = 30
	points := buildFrontierPoints([]rankedModelCandidate{cand}, CapabilityFloor{}, CostPrefBalanced)
	if len(points) != 1 {
		t.Fatalf("points = %d, want 1", len(points))
	}
	// public cost 10 * measured factor 3.0 = 30
	if points[0].Cost.Estimated != 30_000_000 {
		t.Fatalf("Estimated = %v, want 30e6 micro-USD", points[0].Cost.Estimated)
	}
	if points[0].Cost.Source != "registry_pricing_x_measured_effort_cost" {
		t.Fatalf("Source = %q, want registry_pricing_x_measured_effort_cost", points[0].Cost.Source)
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

// TestFrontierUseMeasuredCost 验证实测 cost 校准的全有或全无门控。
func TestFrontierUseMeasuredCost(t *testing.T) {
	withMeasured := func(c rankedModelCandidate, cost float64) rankedModelCandidate {
		c.measuredCostUSD = cost
		return c
	}
	t.Run("全部候选带实测 cost 时启用", func(t *testing.T) {
		ranked := []rankedModelCandidate{
			withMeasured(makeFrontierCandidate("a", 2, 0.5, 80, 10), 2),
			withMeasured(makeFrontierCandidate("b", 2, 0.5, 70, 20), 4),
		}
		if !frontierUseMeasuredCost(ranked) {
			t.Fatal("expected measured cost axis")
		}
	})
	t.Run("任一候选缺实测 cost 时整体回退推测系数", func(t *testing.T) {
		ranked := []rankedModelCandidate{
			withMeasured(makeFrontierCandidate("a", 2, 0.5, 80, 10), 2),
			makeFrontierCandidate("b", 2, 0.5, 70, 20),
		}
		if frontierUseMeasuredCost(ranked) {
			t.Fatal("expected fallback to inferred effort factor")
		}
	})
	t.Run("实测 cost 非法时回退", func(t *testing.T) {
		ranked := []rankedModelCandidate{
			withMeasured(makeFrontierCandidate("a", 2, 0.5, 80, 10), math.NaN()),
			withMeasured(makeFrontierCandidate("b", 2, 0.5, 70, 20), 4),
		}
		if frontierUseMeasuredCost(ranked) {
			t.Fatal("expected fallback for NaN measured cost")
		}
	})
	t.Run("公开价未知候选不参与门控", func(t *testing.T) {
		ranked := []rankedModelCandidate{
			withMeasured(makeFrontierCandidate("a", 2, 0.5, 80, 10), 2),
			makeFrontierCandidate("no-public-cost", 2, 0.5, 70, 0),
		}
		if !frontierUseMeasuredCost(ranked) {
			t.Fatal("expected measured cost axis ignoring cost-unknown candidate")
		}
	})
}

// TestBuildFrontierPoints_MeasuredCostAxisConsistency 验证同一批候选绝不混用两种成本尺度。
func TestBuildFrontierPoints_MeasuredCostAxisConsistency(t *testing.T) {
	// 全部候选带实测 cost：成本轴用实测值（measured/public），source 标记为 measured。
	t.Run("全部实测时用实测校准", func(t *testing.T) {
		a := makeFrontierCandidate("a", 2, 0.5, 80, 10)
		a.effort, a.effortDecided = EffortHigh, true
		a.measuredCostUSD = 30
		b := makeFrontierCandidate("b", 2, 0.5, 70, 20)
		b.effort, b.effortDecided = EffortHigh, true
		b.measuredCostUSD = 50
		points := buildFrontierPoints([]rankedModelCandidate{a, b}, CapabilityFloor{}, CostPrefBalanced)
		if len(points) != 2 {
			t.Fatalf("points = %d, want 2", len(points))
		}
		// a: 10 * (30/10)=30; b: 20 * (50/20)=50
		if points[0].Cost.Estimated != 30_000_000 || points[1].Cost.Estimated != 50_000_000 {
			t.Fatalf("Estimated = (%v, %v), want (30e6, 50e6)", points[0].Cost.Estimated, points[1].Cost.Estimated)
		}
		for _, p := range points {
			if p.Cost.Source != "registry_pricing_x_measured_effort_cost" {
				t.Fatalf("Source = %q, want measured_effort_cost", p.Cost.Source)
			}
		}
	})
	// 部分候选缺实测 cost：整批回退推测系数，实测值不得渗入成本轴，source 不带 measured 标记。
	t.Run("部分实测时整体回退推测系数", func(t *testing.T) {
		a := makeFrontierCandidate("a", 2, 0.5, 80, 10)
		a.effort, a.effortDecided = EffortHigh, true
		a.measuredCostUSD = 30 // 有实测，但因 b 缺失而整批回退
		b := makeFrontierCandidate("b", 2, 0.5, 70, 20)
		b.effort, b.effortDecided = EffortHigh, true
		points := buildFrontierPoints([]rankedModelCandidate{a, b}, CapabilityFloor{}, CostPrefBalanced)
		if len(points) != 2 {
			t.Fatalf("points = %d, want 2", len(points))
		}
		// 两候选均按 high 推测系数 1.5：a=15, b=30；不得出现 a 的实测 30。
		if points[0].Cost.Estimated != 15_000_000 || points[1].Cost.Estimated != 30_000_000 {
			t.Fatalf("Estimated = (%v, %v), want (15e6, 30e6) inferred factors", points[0].Cost.Estimated, points[1].Cost.Estimated)
		}
		for _, p := range points {
			if p.Cost.Source == "registry_pricing_x_measured_effort_cost" {
				t.Fatalf("Source = %q, must not mark measured when axis fell back", p.Cost.Source)
			}
		}
	})
}

func TestBuildFrontierPoints(t *testing.T) {
	t.Run("成本未知候选被排除", func(t *testing.T) {
		ranked := []rankedModelCandidate{
			makeFrontierCandidate("known", 2, 0.5, 80, 10),
			makeFrontierCandidate("unknown", 2, 0.5, 80, 0),
		}
		points := buildFrontierPoints(ranked, CapabilityFloor{}, CostPrefBalanced)
		if len(points) != 1 || points[0].CanonicalModel != "known" {
			t.Fatalf("buildFrontierPoints() = %+v, want only the cost-known candidate", points)
		}
	})
	t.Run("effort 成本系数折算进成本轴", func(t *testing.T) {
		cand := makeFrontierCandidate("m", 2, 0.5, 80, 10)
		cand.effort = EffortMax
		cand.effortDecided = true
		points := buildFrontierPoints([]rankedModelCandidate{cand}, CapabilityFloor{}, CostPrefBalanced)
		if len(points) != 1 || points[0].Cost.Estimated != 20_000_000 {
			t.Fatalf("Estimated = %+v, want 20e6 micro-USD (10 USD x max factor 2.0)", points)
		}
	})
	t.Run("provider 倍率在轴内一致时乘算", func(t *testing.T) {
		cand := makeFrontierCandidate("m", 2, 0.5, 80, 10)
		cand.providerCostKnown = true
		cand.providerCostMultiplier = 3
		points := buildFrontierPoints([]rankedModelCandidate{cand}, CapabilityFloor{}, CostPrefBalanced)
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
		points := buildFrontierPoints(ranked, CapabilityFloor{}, CostPrefBalanced)
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

func TestPickBenefitCappedFrontierPoint_DoesNotUpgradeOnEqualCost(t *testing.T) {
	stronger := makeFrontierCandidate("gpt-5.6-sol", 3, 0.8, 81.48, 35)
	stronger.sameFamily = true
	adequate := makeFrontierCandidate("gpt-5.5", 3, 0.7, 72.31, 35)
	adequate.sameFamily = true
	ranked := []rankedModelCandidate{stronger, adequate}
	points := []FrontierPoint{
		{CandidateID: "0", QualityScore: 0.82, Cost: CostEvidence{Estimated: 35_000_000}},
		{CandidateID: "1", QualityScore: 0.75, Cost: CostEvidence{Estimated: 35_000_000}},
	}
	if got := pickBenefitCappedFrontierPoint(points, ranked); got != 1 {
		t.Fatalf("pickBenefitCappedFrontierPoint() = %d, want stable adequate model idx 1", got)
	}
}

func TestCapClusterCostPremium(t *testing.T) {
	t.Run("质量增益不值回溢价时回退", func(t *testing.T) {
		clusters := []FrontierCluster{
			{Index: 0, AvgQuality: 0.50, AvgCost: 10},
			{Index: 1, AvgQuality: 0.52, AvgCost: 35}, // +0.02 质量，3.5 倍成本
		}
		if got := capClusterCostPremium(clusters, 1, CostPrefBalanced, TaskClassWorker); got != 0 {
			t.Fatalf("capClusterCostPremium() = %d, want 0", got)
		}
	})
	t.Run("溢价值回质量增益时保持", func(t *testing.T) {
		clusters := []FrontierCluster{
			{Index: 0, AvgQuality: 0.50, AvgCost: 10},
			{Index: 1, AvgQuality: 0.60, AvgCost: 11}, // +0.10 质量，仅 +10% 成本
		}
		if got := capClusterCostPremium(clusters, 1, CostPrefBalanced, TaskClassWorker); got != 1 {
			t.Fatalf("capClusterCostPremium() = %d, want 1", got)
		}
	})
	t.Run("目标已是最便宜簇", func(t *testing.T) {
		clusters := []FrontierCluster{{Index: 0, AvgQuality: 0.5, AvgCost: 10}}
		if got := capClusterCostPremium(clusters, 0, CostPrefBalanced, TaskClassWorker); got != 0 {
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

// TestPickFrontierQualityFirstPoint 系列验证 quality_first 并列决胜链：
// 质量证据（benchmark/档位）优先，同族粘性只在有限溢价内生效，其余由成本兜底。
func TestPickFrontierQualityFirstPoint(t *testing.T) {
	// 构造一个跨族候选（与 makeFrontierCandidate 默认 claude 族相对）。
	crossFamily := func(c rankedModelCandidate) rankedModelCandidate {
		c.profile.ModelFamily = ModelFamilyOpenAI
		return c
	}
	cases := []struct {
		name string
		// ranked 与 points 按下标一一对应
		ranked  []rankedModelCandidate
		costs   []int64
		wantIdx int
	}{
		{
			name: "同族溢价超容忍上限时被更便宜跨族候选替代",
			// 同族 30、跨族 17.77：溢价 69% > 25%，噪声级 bench 差（2.57 < 5）不值回溢价
			ranked: []rankedModelCandidate{
				func() rankedModelCandidate {
					c := makeFrontierCandidate("claude-opus-5", 3, 0.5, 83.07, 30)
					c.sameFamily = true
					return c
				}(),
				func() rankedModelCandidate {
					c := crossFamily(makeFrontierCandidate("kimi-k3", 3, 0.5, 80.50, 17.77))
					c.sameFamily = false
					return c
				}(),
			},
			costs:   []int64{45_000_000, 26_660_000},
			wantIdx: 1,
		},
		{
			name: "同族溢价在容忍上限内保持族粘性",
			ranked: []rankedModelCandidate{
				func() rankedModelCandidate {
					c := makeFrontierCandidate("claude-opus-5", 3, 0.5, 82, 11)
					c.sameFamily = true
					return c
				}(),
				func() rankedModelCandidate {
					c := crossFamily(makeFrontierCandidate("kimi-k3", 3, 0.5, 80, 10))
					c.sameFamily = false
					return c
				}(),
			},
			costs:   []int64{11_000_000, 10_000_000},
			wantIdx: 0,
		},
		{
			name: "benchmark 差距足够大时压过族粘性",
			ranked: []rankedModelCandidate{
				func() rankedModelCandidate {
					c := makeFrontierCandidate("claude-opus-5", 3, 0.5, 73, 20)
					c.sameFamily = true
					return c
				}(),
				func() rankedModelCandidate {
					c := crossFamily(makeFrontierCandidate("kimi-k3", 3, 0.5, 80.50, 10))
					c.sameFamily = false
					return c
				}(),
			},
			costs:   []int64{20_000_000, 10_000_000},
			wantIdx: 1,
		},
		{
			name: "质量档先验压过族粘性（bench 均缺失）",
			ranked: []rankedModelCandidate{
				func() rankedModelCandidate {
					c := crossFamily(makeFrontierCandidate("cross-premium", 3, 0.5, 0, 10))
					c.sameFamily = false
					return c
				}(),
				func() rankedModelCandidate {
					c := makeFrontierCandidate("same-high", 2, 0.5, 0, 10)
					c.sameFamily = true
					return c
				}(),
			},
			costs:   []int64{10_000_000, 10_000_000},
			wantIdx: 0,
		},
		{
			name: "质量证据优先：同档时 known 压过更便宜的 unknown",
			ranked: []rankedModelCandidate{
				crossFamily(makeFrontierCandidate("deepseek-v4-flash", 3, 0.5, 0, 1)),
				crossFamily(makeFrontierCandidate("kimi-k3", 3, 0.5, 80, 18)),
			},
			costs:   []int64{1_000_000, 18_000_000},
			wantIdx: 1,
		},
		{
			name: "质量证据优先：known 在前时保持，不被便宜 unknown 反超",
			ranked: []rankedModelCandidate{
				crossFamily(makeFrontierCandidate("kimi-k3", 3, 0.5, 80, 18)),
				crossFamily(makeFrontierCandidate("deepseek-v4-flash", 3, 0.5, 0, 1)),
			},
			costs:   []int64{18_000_000, 1_000_000},
			wantIdx: 0,
		},
		{
			name: "同成本同证据时取更低下标（确定性）",
			ranked: []rankedModelCandidate{
				crossFamily(makeFrontierCandidate("a", 3, 0.5, 80, 10)),
				crossFamily(makeFrontierCandidate("b", 3, 0.5, 80, 10)),
			},
			costs:   []int64{10_000_000, 10_000_000},
			wantIdx: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			points := make([]FrontierPoint, 0, len(tc.ranked))
			for i, cost := range tc.costs {
				points = append(points, FrontierPoint{
					CandidateID: strconv.Itoa(i),
					Cost:        CostEvidence{Estimated: cost},
				})
			}
			if got := pickFrontierQualityFirstPoint(points, tc.ranked); got != tc.wantIdx {
				t.Fatalf("pickFrontierQualityFirstPoint() = %d, want %d", got, tc.wantIdx)
			}
		})
	}
}

// TestSelectFrontierQualityFirst_TightTiePool 验证并列池收紧规则：
// 无 benchmark 证据的候选不与实测 top 并列；上界触及 top 点估计才可入池。
func TestSelectFrontierQualityFirst_TightTiePool(t *testing.T) {
	t.Run("无 bench 证据的宽区间候选不进并列池", func(t *testing.T) {
		// a 为实测 top（bench 83）；b 无 bench 证据但区间宽，旧规则下上界可蹭到 a 的下界。
		a := makeFrontierCandidate("top", 3, 0.5, 83, 30)
		a.sameFamily = true
		b := makeFrontierCandidate("no-bench", 2, 0.5, 0, 1)
		b.sameFamily = false
		ranked := []rankedModelCandidate{a, b}
		forest := FrontierForest{
			ScopeID: frontierCostScopeUSD,
			Version: frontierEvidenceVersion,
			Clusters: []FrontierCluster{{
				Index: 0,
				Points: []FrontierPoint{
					{CandidateID: "0", QualityScore: 0.84, QualityLow: 0.75, QualityHigh: 0.93, Cost: CostEvidence{Estimated: 45_000_000}},
					{CandidateID: "1", QualityScore: 0.66, QualityLow: 0.53, QualityHigh: 0.79, Cost: CostEvidence{Estimated: 1_320_000}},
				},
			}},
		}
		idx, note := selectFrontierQualityFirst(forest, ranked)
		if idx != 0 {
			t.Fatalf("selectFrontierQualityFirst() = (%d, %q), want idx 0 (measured top)", idx, note)
		}
		if !strings.Contains(note, "tie_pool=1") {
			t.Fatalf("note = %q, want tie_pool=1 excluding no-bench candidate", note)
		}
	})
	t.Run("上界未触及 top 点估计的候选不进并列池", func(t *testing.T) {
		// b 有 bench 证据，但上界 0.83 < top 点估计 0.84，不得入池。
		a := makeFrontierCandidate("top", 3, 0.5, 83, 30)
		b := makeFrontierCandidate("near", 3, 0.5, 78, 10)
		ranked := []rankedModelCandidate{a, b}
		forest := FrontierForest{
			ScopeID: frontierCostScopeUSD,
			Version: frontierEvidenceVersion,
			Clusters: []FrontierCluster{{
				Index: 0,
				Points: []FrontierPoint{
					{CandidateID: "0", QualityScore: 0.84, QualityLow: 0.75, QualityHigh: 0.93, Cost: CostEvidence{Estimated: 80_000_000}},
					{CandidateID: "1", QualityScore: 0.78, QualityLow: 0.73, QualityHigh: 0.83, Cost: CostEvidence{Estimated: 10_000_000}},
				},
			}},
		}
		idx, note := selectFrontierQualityFirst(forest, ranked)
		if idx != 0 || !strings.Contains(note, "tie_pool=1") {
			t.Fatalf("selectFrontierQualityFirst() = (%d, %q), want idx 0 with tie_pool=1", idx, note)
		}
	})
	t.Run("上界触及 top 点估计的 bench 候选正常入池", func(t *testing.T) {
		a := makeFrontierCandidate("top", 3, 0.5, 83, 30)
		b := makeFrontierCandidate("near", 3, 0.5, 80, 10)
		ranked := []rankedModelCandidate{a, b}
		forest := FrontierForest{
			ScopeID: frontierCostScopeUSD,
			Version: frontierEvidenceVersion,
			Clusters: []FrontierCluster{{
				Index: 0,
				Points: []FrontierPoint{
					{CandidateID: "0", QualityScore: 0.84, QualityLow: 0.75, QualityHigh: 0.93, Cost: CostEvidence{Estimated: 80_000_000}},
					{CandidateID: "1", QualityScore: 0.82, QualityLow: 0.73, QualityHigh: 0.91, Cost: CostEvidence{Estimated: 10_000_000}},
				},
			}},
		}
		idx, note := selectFrontierQualityFirst(forest, ranked)
		if idx != 1 || !strings.Contains(note, "tie_pool=2") {
			t.Fatalf("selectFrontierQualityFirst() = (%d, %q), want idx 1 with tie_pool=2", idx, note)
		}
	})
}
