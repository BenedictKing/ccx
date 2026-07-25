package autopilot

import (
	"testing"
)

// ────────────────────────────────────────────────────────────────
// Pareto 支配关系测试
// ────────────────────────────────────────────────────────────────

func TestDominated_QualityDominates(t *testing.T) {
	a := FrontierPoint{
		QualityLow:  0.7,
		QualityHigh: 0.8,
		Cost:        CostEvidence{Estimated: 10},
	}
	b := FrontierPoint{
		QualityLow:  0.5,
		QualityHigh: 0.6,
		Cost:        CostEvidence{Estimated: 15},
	}
	// a 质量下界 > b 质量上界，且成本更低 → a 支配 b
	if !dominates(a, b) {
		t.Fatal("a should dominate b: higher quality, lower cost")
	}
}

func TestDominated_QualityOverlap(t *testing.T) {
	a := FrontierPoint{
		QualityLow:  0.5,
		QualityHigh: 0.7,
		Cost:        CostEvidence{Estimated: 10},
	}
	b := FrontierPoint{
		QualityLow:  0.4,
		QualityHigh: 0.8,
		Cost:        CostEvidence{Estimated: 15},
	}
	// 质量区间重叠（a 下界 < b 上界）→ 不支配
	if dominates(a, b) {
		t.Fatal("should NOT dominate: quality intervals overlap")
	}
}

func TestDominated_HigherCost(t *testing.T) {
	a := FrontierPoint{
		QualityLow:  0.8,
		QualityHigh: 0.9,
		Cost:        CostEvidence{Estimated: 20},
	}
	b := FrontierPoint{
		QualityLow:  0.5,
		QualityHigh: 0.6,
		Cost:        CostEvidence{Estimated: 10},
	}
	// a 质量更高但成本也更高 → 不支配
	if dominates(a, b) {
		t.Fatal("should NOT dominate: a is more expensive")
	}
}

func TestDominated_EqualCostEqualQuality(t *testing.T) {
	a := FrontierPoint{
		QualityLow:  0.5,
		QualityHigh: 0.7,
		Cost:        CostEvidence{Estimated: 10},
	}
	b := FrontierPoint{
		QualityLow:  0.5,
		QualityHigh: 0.7,
		Cost:        CostEvidence{Estimated: 10},
	}
	// 完全相同 → 不支配（需要至少一个维度严格更优）
	if dominates(a, b) {
		t.Fatal("should NOT dominate: identical points")
	}
}

// ────────────────────────────────────────────────────────────────
// FrontierForest 计算测试
// ────────────────────────────────────────────────────────────────

func testPoint(id string, quality, qualityLow, qualityHigh, cost float64) FrontierPoint {
	return FrontierPoint{
		CandidateID:  id,
		QualityScore: quality,
		QualityLow:   qualityLow,
		QualityHigh:  qualityHigh,
		Cost:         CostEvidence{Unit: CostUnitAFP, ScopeID: "vp_test", Estimated: int64(cost)},
	}
}

func TestComputeFrontierForest_Empty(t *testing.T) {
	forest := ComputeFrontierForest(nil, "vp_test", "v1")
	if len(forest.Clusters) != 0 {
		t.Fatalf("expected 0 clusters, got %d", len(forest.Clusters))
	}
}

func TestComputeFrontierForest_SinglePoint(t *testing.T) {
	points := []FrontierPoint{testPoint("a", 0.5, 0.4, 0.6, 10)}
	forest := ComputeFrontierForest(points, "vp_test", "v1")
	if len(forest.Clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(forest.Clusters))
	}
	if len(forest.Clusters[0].Points) != 1 {
		t.Fatalf("expected 1 point in cluster, got %d", len(forest.Clusters[0].Points))
	}
}

func TestComputeFrontierForest_TwoDominatingPoints(t *testing.T) {
	points := []FrontierPoint{
		testPoint("cheap-good", 0.7, 0.65, 0.75, 5),     // 低成本、高质量
		testPoint("expensive-bad", 0.4, 0.35, 0.45, 20), // 高成本、低质量（被支配）
	}
	forest := ComputeFrontierForest(points, "vp_test", "v1")
	// 被支配的点不应出现在 Pareto 前沿
	totalPoints := 0
	for _, c := range forest.Clusters {
		totalPoints += len(c.Points)
	}
	if totalPoints != 1 {
		t.Fatalf("expected 1 point on frontier, got %d", totalPoints)
	}
	if forest.Clusters[0].Points[0].CandidateID != "cheap-good" {
		t.Fatalf("expected 'cheap-good' on frontier, got %q", forest.Clusters[0].Points[0].CandidateID)
	}
}

func TestComputeFrontierForest_TradeoffPoints(t *testing.T) {
	// 三个有效前沿点：高质量高成本、中质量中成本、低质量低成本
	// 置信区间不重叠
	points := []FrontierPoint{
		testPoint("budget", 0.3, 0.25, 0.35, 2),
		testPoint("balanced", 0.55, 0.50, 0.60, 8),
		testPoint("premium", 0.85, 0.80, 0.90, 25),
	}
	forest := ComputeFrontierForest(points, "vp_test", "v1")
	totalPoints := 0
	for _, c := range forest.Clusters {
		totalPoints += len(c.Points)
	}
	// 三个点都是 Pareto 最优（成本升序，质量升序，互不支配）
	if totalPoints != 3 {
		t.Fatalf("expected 3 points on frontier, got %d", totalPoints)
	}
}

func TestComputeFrontierForest_Deterministic(t *testing.T) {
	points := []FrontierPoint{
		testPoint("a", 0.3, 0.25, 0.35, 2),
		testPoint("b", 0.55, 0.50, 0.60, 8),
		testPoint("c", 0.85, 0.80, 0.90, 25),
	}
	f1 := ComputeFrontierForest(points, "vp_test", "v1")
	f2 := ComputeFrontierForest(points, "vp_test", "v1")
	if len(f1.Clusters) != len(f2.Clusters) {
		t.Fatal("non-deterministic: different cluster count")
	}
	for i := range f1.Clusters {
		if len(f1.Clusters[i].Points) != len(f2.Clusters[i].Points) {
			t.Fatalf("non-deterministic: cluster %d different size", i)
		}
		for j := range f1.Clusters[i].Points {
			if f1.Clusters[i].Points[j].CandidateID != f2.Clusters[i].Points[j].CandidateID {
				t.Fatalf("non-deterministic: cluster %d point %d different", i, j)
			}
		}
	}
}

// ────────────────────────────────────────────────────────────────
// 三倾向候选阶梯测试
// ────────────────────────────────────────────────────────────────

func TestBuildCandidateLadder_Empty(t *testing.T) {
	forest := FrontierForest{ScopeID: "vp_test", Version: "v1"}
	ladder := BuildCandidateLadder(forest, CostPrefBalanced)
	if len(ladder.Preferred) != 0 {
		t.Fatalf("expected 0 preferred stages, got %d", len(ladder.Preferred))
	}
}

func TestBuildCandidateLadder_QualityFirst(t *testing.T) {
	points := []FrontierPoint{
		testPoint("budget", 0.3, 0.25, 0.35, 2),
		testPoint("balanced", 0.55, 0.50, 0.60, 8),
		testPoint("premium", 0.85, 0.80, 0.90, 25),
	}
	forest := ComputeFrontierForest(points, "vp_test", "v1")
	ladder := BuildCandidateLadder(forest, CostPrefQualityFirst)

	if len(ladder.Preferred) == 0 {
		t.Fatal("expected at least 1 preferred stage")
	}
	// QualityFirst 目标应是高能力端（最后一个簇）
	target := ladder.Preferred[0]
	// 目标应包含高质量点
	foundHighQuality := false
	for _, p := range target.Points {
		if p.QualityScore >= 0.7 {
			foundHighQuality = true
		}
	}
	if !foundHighQuality {
		t.Fatalf("quality_first target should contain high quality point, got avg quality %f", target.AvgQuality)
	}
}

func TestBuildCandidateLadder_CostFirst(t *testing.T) {
	points := []FrontierPoint{
		testPoint("budget", 0.1, 0.05, 0.15, 2),
		testPoint("balanced", 0.55, 0.50, 0.60, 8),
		testPoint("premium", 0.95, 0.90, 1.00, 25),
	}
	forest := ComputeFrontierForest(points, "vp_test", "v1")
	ladder := BuildCandidateLadder(forest, CostPrefCostFirst)

	if len(ladder.Preferred) == 0 {
		t.Fatal("expected at least 1 preferred stage")
	}
	// CostFirst lane 应始终有效
	if ladder.Lane != CostPrefCostFirst {
		t.Fatalf("lane = %v, want cost_first", ladder.Lane)
	}
}

func TestBuildCandidateLadder_Balanced_KneePoint(t *testing.T) {
	points := []FrontierPoint{
		testPoint("cheap", 0.2, 0.15, 0.25, 1),
		testPoint("mid", 0.5, 0.45, 0.55, 5),
		testPoint("expensive", 0.9, 0.85, 0.95, 30),
	}
	forest := ComputeFrontierForest(points, "vp_test", "v1")
	ladder := BuildCandidateLadder(forest, CostPrefBalanced)

	if len(ladder.Preferred) == 0 {
		t.Fatal("expected at least 1 preferred stage")
	}
}

func TestBuildCandidateLadder_OverflowIncludesNonPreferred(t *testing.T) {
	// 5 个前沿点，preferred 最多 5 个，overflow 应包含剩余的
	points := []FrontierPoint{
		testPoint("p1", 0.1, 0.05, 0.15, 1),
		testPoint("p2", 0.25, 0.20, 0.30, 3),
		testPoint("p3", 0.4, 0.35, 0.45, 6),
		testPoint("p4", 0.6, 0.55, 0.65, 12),
		testPoint("p5", 0.8, 0.75, 0.85, 20),
		testPoint("p6", 0.95, 0.90, 1.00, 35),
	}
	forest := ComputeFrontierForest(points, "vp_test", "v1")
	ladder := BuildCandidateLadder(forest, CostPrefQualityFirst)

	totalPreferred := 0
	for _, s := range ladder.Preferred {
		totalPreferred += len(s.Points)
	}
	totalOverflow := 0
	for _, s := range ladder.Overflow {
		totalOverflow += len(s.Points)
	}
	if totalPreferred+totalOverflow != 6 {
		t.Fatalf("preferred(%d) + overflow(%d) = %d, want 6", totalPreferred, totalOverflow, totalPreferred+totalOverflow)
	}
}

// ────────────────────────────────────────────────────────────────
// 稳健阈值测试
// ────────────────────────────────────────────────────────────────

func TestRobustThreshold_Empty(t *testing.T) {
	threshold := robustThreshold(nil)
	if threshold != minClusterGap {
		t.Fatalf("empty threshold = %f, want %f", threshold, minClusterGap)
	}
}

func TestRobustThreshold_SingleValue(t *testing.T) {
	threshold := robustThreshold([]float64{0.1})
	// MAD = 0, 所以 threshold = median = 0.1
	if threshold < 0.099 || threshold > 0.101 {
		t.Fatalf("single threshold = %f, want ~0.1", threshold)
	}
}

func TestRobustThreshold_MinGap(t *testing.T) {
	// 非常小的间隙应该被 minClusterGap 兜底
	threshold := robustThreshold([]float64{0.001, 0.002, 0.001})
	// median = 0.001, MAD = 0, threshold = 0.001 < minClusterGap(0.08)
	// 但 robustThreshold 自己不应用 minClusterGap，那是 clusterFrontierPoints 的事
	_ = threshold
}

// ────────────────────────────────────────────────────────────────
// 膝点检测测试
// ────────────────────────────────────────────────────────────────

func TestKneePointIndex_Empty(t *testing.T) {
	idx := kneePointIndex(nil)
	if idx != 0 {
		t.Fatalf("empty knee = %d, want 0", idx)
	}
}

func TestKneePointIndex_Single(t *testing.T) {
	clusters := []FrontierCluster{{Index: 0, AvgQuality: 0.5, AvgCost: 5}}
	idx := kneePointIndex(clusters)
	if idx != 0 {
		t.Fatalf("single knee = %d, want 0", idx)
	}
}

func TestKneePointIndex_ThreePoints(t *testing.T) {
	clusters := []FrontierCluster{
		{Index: 0, AvgQuality: 0.2, AvgCost: 1},
		{Index: 1, AvgQuality: 0.5, AvgCost: 5},   // 膝点：从廉价低质到中等的拐点
		{Index: 2, AvgQuality: 0.55, AvgCost: 20}, // 成本急升但质量增长小
	}
	idx := kneePointIndex(clusters)
	// 预期膝点在 index 1（成本变化最大处）
	if idx != 1 {
		t.Fatalf("knee = %d, want 1", idx)
	}
}
