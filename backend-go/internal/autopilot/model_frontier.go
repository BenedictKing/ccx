package autopilot

import (
	"math"
	"sort"
)

// ────────────────────────────────────────────────────────────────
// Frontier 点：请求级能力—成本路由点。
// 同一 model/version/effort/taskDomain 的多个 endpoint 不重复生成质量点。
// ────────────────────────────────────────────────────────────────

// FrontierPoint 是请求级能力—成本路由点。
type FrontierPoint struct {
	CandidateID     string        // 候选唯一标识（如 channelUID + modelID）
	CanonicalModel  string        // 规范模型 ID
	ModelVersion    string        // 模型版本
	Effort          string        // effort 级别
	Domain          TaskDomain    // 任务域
	QualityScore    float64       // 归一化质量分 0.0-1.0
	QualityLow      float64       // 质量置信下界
	QualityHigh     float64       // 质量置信上界
	Cost            CostEvidence  // 成本证据
	EvidenceVersion string        // 证据版本标识
}

// EffectiveQualityRange 返回置信区间的宽度。
func (p FrontierPoint) EffectiveQualityRange() float64 {
	return p.QualityHigh - p.QualityLow
}

// ────────────────────────────────────────────────────────────────
// Pareto 非支配排序
// ────────────────────────────────────────────────────────────────

// dominates 判断点 a 是否支配点 b。
// 仅在同一成本 scope 内使用。
// a 支配 b 的条件：a 质量下界 ≥ b 质量上界 且 a 成本 ≤ b 成本，且至少一个维度严格更优。
func dominates(a, b FrontierPoint) bool {
	// 质量维度：a 的下界 >= b 的上界（高置信度支配）
	qualityDominates := a.QualityLow >= b.QualityHigh
	// 成本维度：a 的估算成本 <= b 的估算成本
	costDominates := a.Cost.Estimated <= b.Cost.Estimated
	// 至少一个维度严格更优
	strictBetter := (a.QualityLow > b.QualityHigh) || (a.Cost.Estimated < b.Cost.Estimated)

	return qualityDominates && costDominates && strictBetter
}

// FrontierCluster 是一组能力—成本近似的前沿点。
type FrontierCluster struct {
	Index     int             // 簇序号，按能力由低到高 F0...Fn
	Points    []FrontierPoint // 簇内点
	AvgQuality float64        // 簇内平均质量分
	AvgCost    float64        // 簇内平均成本
}

// FrontierForest 是一棵可比成本域的完整 Pareto 边界。
type FrontierForest struct {
	ScopeID   string              // 成本作用域
	Clusters  []FrontierCluster   // 按能力升序排列的微簇
	Version   string              // 边界版本
}

// ────────────────────────────────────────────────────────────────
// 确定性 Pareto 分层：非支配排序 + ε 去重 + 相邻差距微簇
// ────────────────────────────────────────────────────────────────

// ComputeFrontierForest 从候选点集生成完整 Pareto 边界。
// 要求所有点的 Cost.ScopeID 相同且可比较。
// 输入相同必须得到相同完整候选图（确定性）。
func ComputeFrontierForest(points []FrontierPoint, scopeID string, version string) FrontierForest {
	if len(points) == 0 {
		return FrontierForest{ScopeID: scopeID, Version: version}
	}

	// 步骤 1：非支配排序，提取 Pareto 边界（rank 0）
	ranked := paretoRank(points)
	rank0 := filterByRank(ranked, 0)

	// 步骤 2：按成本升序排列
	sort.Slice(rank0, func(i, j int) bool {
		if rank0[i].Cost.Estimated != rank0[j].Cost.Estimated {
			return rank0[i].Cost.Estimated < rank0[j].Cost.Estimated
		}
		return rank0[i].CandidateID < rank0[j].CandidateID
	})

	// 步骤 3：ε 近似去重 + 自然断点聚类
	clusters := clusterFrontierPoints(rank0)

	return FrontierForest{
		ScopeID:  scopeID,
		Clusters: clusters,
		Version:  version,
	}
}

// paretoRank 对点集进行非支配排序，返回每个点的 rank（0 = Pareto 前沿）。
func paretoRank(points []FrontierPoint) []rankedPoint {
	n := len(points)
	ranks := make([]rankedPoint, n)
	remaining := make([]int, n)
	for i := range remaining {
		remaining[i] = i
	}

	currentRank := 0
	for len(remaining) > 0 {
		// 找出当前层的非支配点
		var frontier []int
		for _, i := range remaining {
			dominatedByAny := false
			for _, j := range remaining {
				if i != j && dominates(points[j], points[i]) {
					dominatedByAny = true
					break
				}
			}
			if !dominatedByAny {
				frontier = append(frontier, i)
			}
		}

		// 分配 rank 并移除已排序的点
		for _, i := range frontier {
			ranks[i] = rankedPoint{point: points[i], rank: currentRank}
		}

		currentRank++
		nextRemaining := make([]int, 0, len(remaining)-len(frontier))
		frontierSet := make(map[int]bool, len(frontier))
		for _, i := range frontier {
			frontierSet[i] = true
		}
		for _, i := range remaining {
			if !frontierSet[i] {
				nextRemaining = append(nextRemaining, i)
			}
		}
		remaining = nextRemaining
	}

	return ranks
}

type rankedPoint struct {
	point FrontierPoint
	rank  int
}

func filterByRank(ranked []rankedPoint, targetRank int) []FrontierPoint {
	var result []FrontierPoint
	for _, rp := range ranked {
		if rp.rank == targetRank {
			result = append(result, rp.point)
		}
	}
	return result
}

// clusterFrontierPoints 对 Pareto 前沿点按能力—成本自然断点聚类。
//
// 算法：按成本升序扫描，当相邻点之间的能力差距超过稳健阈值
//（中位数 + MAD）时形成新簇。样本过少时使用保守最小阈值。
func clusterFrontierPoints(points []FrontierPoint) []FrontierCluster {
	if len(points) == 0 {
		return nil
	}
	if len(points) == 1 {
		p := points[0]
		return []FrontierCluster{{
			Index:      0,
			Points:     points,
			AvgQuality: p.QualityScore,
			AvgCost:    float64(p.Cost.Estimated),
		}}
	}

	// 计算相邻点间的能力差距
	gaps := make([]float64, 0, len(points)-1)
	for i := 1; i < len(points); i++ {
		gap := math.Abs(points[i].QualityScore - points[i-1].QualityScore)
		gaps = append(gaps, gap)
	}

	// 计算稳健阈值：中位数 + 1.5 * MAD
	threshold := robustThreshold(gaps)
	if threshold < minClusterGap {
		threshold = minClusterGap
	}

	// 按阈值切分簇
	clusters := []FrontierCluster{{Index: 0}}
	clusters[0].Points = append(clusters[0].Points, points[0])

	for i := 1; i < len(points); i++ {
		gap := math.Abs(points[i].QualityScore - points[i-1].QualityScore)
		if gap > threshold {
			// 新簇
			clusters = append(clusters, FrontierCluster{
				Index:  len(clusters),
				Points: []FrontierPoint{points[i]},
			})
		} else {
			// 同簇
			last := len(clusters) - 1
			clusters[last].Points = append(clusters[last].Points, points[i])
		}
	}

	// 计算每个簇的平均质量分和成本
	for i := range clusters {
		var sumQuality, sumCost float64
		for _, p := range clusters[i].Points {
			sumQuality += p.QualityScore
			sumCost += float64(p.Cost.Estimated)
		}
		n := float64(len(clusters[i].Points))
		clusters[i].AvgQuality = sumQuality / n
		clusters[i].AvgCost = sumCost / n
	}

	return clusters
}

// minClusterGap 是簇间最小能力差距（保守默认值，样本过少时使用）。
const minClusterGap = 0.08

// robustThreshold 计算中位数 + 1.5 * MAD 作为稳健阈值。
func robustThreshold(values []float64) float64 {
	if len(values) == 0 {
		return minClusterGap
	}
	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	median := percentile(sorted, 0.5)
	mad := medianAbsoluteDeviation(sorted, median)
	return median + 1.5*mad
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := p * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))
	if lower == upper {
		return sorted[lower]
	}
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}

func medianAbsoluteDeviation(sorted []float64, medianVal float64) float64 {
	devs := make([]float64, len(sorted))
	for i, v := range sorted {
		devs[i] = math.Abs(v - medianVal)
	}
	sort.Float64s(devs)
	return percentile(devs, 0.5)
}

// ────────────────────────────────────────────────────────────────
// 三倾向候选阶梯
// ────────────────────────────────────────────────────────────────



// LadderStage 是候选阶梯的一个阶段。
type LadderStage struct {
	Index       int              // 阶段序号（0 = 目标档）
	ClusterIdx  int              // 对应的 FrontierCluster 序号
	Points      []FrontierPoint  // 该阶段的候选点（按健康/延迟排序）
	AvgQuality  float64          // 阶段平均质量分
	AvgCost     float64          // 阶段平均成本
	Reason      string           // 进入该阶段的原因
}

// CandidateLadder 是请求级候选阶梯。
type CandidateLadder struct {
	FrontierVersion string
	Lane            CostPreferenceMode
	Preferred       []LadderStage // 3-5 个活动阶段
	Overflow        []LadderStage // 完整候选图剩余的有序兜底尾部
}

// BuildCandidateLadder 从 FrontierForest 物化三倾向候选阶梯。
// 每种倾向基于完整候选图物化 3-5 个活动阶段。
func BuildCandidateLadder(forest FrontierForest, lane CostPreferenceMode) CandidateLadder {
	ladder := CandidateLadder{
		FrontierVersion: forest.Version,
		Lane:            lane,
	}

	if len(forest.Clusters) == 0 {
		return ladder
	}

	// 按倾向选择目标簇和扩展方向
	targetIdx, preferred := selectLadderStages(forest.Clusters, lane)

	// 构建 Preferred 阶段（最多 5 个）
	ladder.Preferred = make([]LadderStage, 0, len(preferred))
	for i, ci := range preferred {
		if ci < 0 || ci >= len(forest.Clusters) {
			continue
		}
		cluster := forest.Clusters[ci]
		stage := LadderStage{
			Index:      i,
			ClusterIdx: ci,
			Points:     cluster.Points,
			AvgQuality: cluster.AvgQuality,
			AvgCost:    cluster.AvgCost,
			Reason:     stageReason(lane, i, ci == targetIdx),
		}
		ladder.Preferred = append(ladder.Preferred, stage)
	}

	// Overflow 是不在 Preferred 中的簇
	preferredSet := make(map[int]bool, len(preferred))
	for _, ci := range preferred {
		preferredSet[ci] = true
	}
	ladder.Overflow = make([]LadderStage, 0)
	for ci, cluster := range forest.Clusters {
		if preferredSet[ci] {
			continue
		}
		ladder.Overflow = append(ladder.Overflow, LadderStage{
			Index:      len(ladder.Overflow),
			ClusterIdx: ci,
			Points:     cluster.Points,
			AvgQuality: cluster.AvgQuality,
			AvgCost:    cluster.AvgCost,
			Reason:     "overflow",
		})
	}

	return ladder
}

// selectLadderStages 为指定倾向选择目标簇和相邻回退簇。
// 返回目标簇索引和所有选中的簇索引（按优先级排序）。
func selectLadderStages(clusters []FrontierCluster, lane CostPreferenceMode) (targetIdx int, selected []int) {
	n := len(clusters)
	if n == 0 {
		return -1, nil
	}

	switch lane {
	case CostPrefQualityFirst:
		// 目标：高能力端（最后一个簇）
		targetIdx = n - 1
		// 向低能力方向扩展
		selected = make([]int, 0, minInt(n, 5))
		for i := n - 1; i >= 0 && len(selected) < 5; i-- {
			selected = append(selected, i)
		}

	case CostPrefCostFirst:
		// 目标：满足最低能力的低成本端（第一个簇）
		targetIdx = 0
		// 向高能力方向扩展
		selected = make([]int, 0, minInt(n, 5))
		for i := 0; i < n && len(selected) < 5; i++ {
			selected = append(selected, i)
		}

	case CostPrefBalanced:
		// 目标：Pareto 膝点附近（中间簇）
		targetIdx = kneePointIndex(clusters)
		// 从膝点向两侧展开
		selected = make([]int, 0, minInt(n, 5))
		selected = append(selected, targetIdx)
		// 交替向两侧扩展
		lo, hi := targetIdx-1, targetIdx+1
		for len(selected) < 5 && (lo >= 0 || hi < n) {
			if hi < n && len(selected) < 5 {
				selected = append(selected, hi)
				hi++
			}
			if lo >= 0 && len(selected) < 5 {
				selected = append(selected, lo)
				lo--
			}
		}
		sort.Ints(selected)

	default:
		targetIdx = 0
		selected = []int{0}
	}

	return targetIdx, selected
}

// kneePointIndex 找到 Pareto 膝点（能力/成本边际变化最大的拐点）。
func kneePointIndex(clusters []FrontierCluster) int {
	n := len(clusters)
	if n <= 1 {
		return 0
	}
	if n == 2 {
		return 1 // 偏好高能力
	}

	// 使用最大曲率法：在归一化能力-成本平面上找最大转弯
	maxCurvature := -1.0
	knee := n / 2

	for i := 1; i < n-1; i++ {
		// 三点之间的角度变化
		dx1 := clusters[i].AvgCost - clusters[i-1].AvgCost
		dy1 := clusters[i].AvgQuality - clusters[i-1].AvgQuality
		dx2 := clusters[i+1].AvgCost - clusters[i].AvgCost
		dy2 := clusters[i+1].AvgQuality - clusters[i].AvgQuality

		// 用叉积近似曲率
		cross := math.Abs(dx1*dy2 - dy1*dx2)
		if cross > maxCurvature {
			maxCurvature = cross
			knee = i
		}
	}
	return knee
}

func stageReason(lane CostPreferenceMode, stageIdx int, isTarget bool) string {
	if isTarget {
		switch lane {
		case CostPrefQualityFirst:
			return "target: highest quality"
		case CostPrefBalanced:
			return "target: knee point"
		case CostPrefCostFirst:
			return "target: lowest cost above floor"
		}
	}
	if stageIdx == 0 {
		return "target"
	}
	return "fallback"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
