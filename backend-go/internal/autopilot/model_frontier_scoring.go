package autopilot

import (
	"fmt"
	"math"
	"strconv"
)

// ────────────────────────────────────────────────────────────────
// Frontier 选型：把 rankEligibleModels 的 model × effort 候选转换为
// 能力—成本点，在 Pareto 前沿上按成本倾向车道选择。
//
// 生产入口：ModelResolver.rankEligibleModels（model_resolver.go），默认启用。
// 成本证据不可比（可比候选 < 2）时 fail-open 回退到既有字典序比较链。
//
// 设计要点（与 codexradar 等效率曲线同源）：
//   - 全局比较：候选是全部 model × effort 组合，不先锁质量档再挑档；
//   - 成本与质量并列成轴：质量增益必须值回成本溢价（balanced 溢价帽）；
//   - 噪声级质量差异不值数倍成本：置信区间重叠视为并列，并列取低成本。
// ────────────────────────────────────────────────────────────────

const (
	// frontierEvidenceVersion 标识能力—成本证据的合成规则版本。
	frontierEvidenceVersion = "modelrank.v1"

	// premiumFrontierBenchmarkMinDelta 是 quality_first 并列池内启用 premium benchmark 强比较的最小差值。
	// 仅在同为 premium 且 benchmark 差距足够大时，才让质量证据压过成本；否则仍把它们视作噪声级
	// 并列，由成本兜底，避免 1-2 分的小差异就把 compact/低成本模型完全挤掉。
	premiumFrontierBenchmarkMinDelta = 5.0

	// frontierQualityIntervalBase 是质量置信区间的基准半宽。
	frontierQualityIntervalBase = 0.05
	// frontierLowConfidenceFactor 是实测质量置信度不足时的区间加宽倍数。
	frontierLowConfidenceFactor = 2.0
	// frontierProvisionalLaneFactor 是基准证据处于 provisional 泳道时的区间加宽倍数。
	frontierProvisionalLaneFactor = 1.5

	// frontierMaxCostPremiumPerQualityBase 是 balanced 车道的成本溢价上限基础值。
	// 相对最便宜前沿簇，每 1.0 质量增益（0-1 刻度）最多接受 2.0 倍成本溢价，
	// 即每 0.01 质量增益约 2% 溢价。防止膝点落在"质量略高但成本翻倍"的簇上。
	// 实际值会按 CostPreferenceMode 与 TaskClass 权重动态放大/收紧。
	frontierMaxCostPremiumPerQualityBase = 2.0
)

// frontierEffortCostFactor 是灰度期的 effort 成本系数（2026-07-26 联合路由计划 §3.4）。
// 高档思考显著增加输出 token；等成本下高档位会支配低档位造成档位膨胀，
// 因此用保守系数把档位差异折算进成本轴。按厂商成本序 ultra>max>xhigh 递增。
// 待实测输出 token 统计积累后替换。
var frontierEffortCostFactor = map[EffortLevel]float64{
	EffortOff:     1.0,
	EffortMinimal: 1.0,
	EffortLow:     1.0,
	EffortMedium:  1.2,
	EffortHigh:    1.5,
	EffortXhigh:   1.7,
	EffortMax:     2.0,
	EffortUltra:   2.3,
}

// frontierCostFactorFor 返回候选的 effort 成本系数；未决定或未识别的档位按 1.0 处理。
// useMeasured 为 true 且该候选存在实测 cost 时，用实测值相对公开成本的归一化比例
// 替换推测系数，实现 frontier 成本轴的性价比感知。useMeasured 为 false 时强制走推测系数，
// 由 buildFrontierPoints 的全有或全无门控保证同一批候选不混用两种成本尺度。
func frontierCostFactorFor(candidate rankedModelCandidate, useMeasured bool) float64 {
	if !candidate.effortDecided || candidate.effort == "" {
		return 1.0
	}
	// 有实测 cost 且本批启用实测路径时：factor = measuredCostUSD / normalizedPublicCostUSD，fail-open 避免除零/负成本
	if useMeasured && candidate.publicCostKnown && candidate.normalizedPublicCostUSD > 0 &&
		candidate.measuredCostUSD > 0 && !math.IsNaN(candidate.measuredCostUSD) {
		factor := candidate.measuredCostUSD / candidate.normalizedPublicCostUSD
		if factor > 0 {
			return factor
		}
	}
	if factor, ok := frontierEffortCostFactor[candidate.effort]; ok {
		return factor
	}
	return 1.0
}

// ── 质量分合成 ──

// frontierQualityWeights 按成本偏好车道决定质量分中 benchmark / 档先验 / 实测的权重。
var frontierQualityWeights = map[CostPreferenceMode]struct {
	Benchmark float64
	TierPrior float64
	Measured  float64
}{
	CostPrefQualityFirst: {Benchmark: 0.6, TierPrior: 0.25, Measured: 0.15},
	CostPrefBalanced:     {Benchmark: 0.5, TierPrior: 0.30, Measured: 0.20},
	CostPrefCostFirst:    {Benchmark: 0.4, TierPrior: 0.35, Measured: 0.25},
}

// frontierQualityScore 把候选的质量信号合成为 0-1 单轴分数。
// benchmark 为主锚（最接近独立能力测量），质量档为先验，实测质量为修正；
// benchmark 缺失时权重让渡给档先验与实测。measuredQualityScore 在候选构建时
// 已含 effort 加分与任务域修正（model_resolver.go），此处不重复计入。
// mode 决定 benchmark 与档先验的相对权重。
func frontierQualityScore(candidate rankedModelCandidate, mode CostPreferenceMode) float64 {
	weights, ok := frontierQualityWeights[mode]
	if !ok {
		weights = frontierQualityWeights[CostPrefBalanced]
	}
	tierPrior := float64(candidate.qualityRank) / 3.0
	if candidate.benchmarkKnown && candidate.benchmarkScore > 0 {
		return clampUnit(weights.Benchmark*(candidate.benchmarkScore/100.0) +
			weights.TierPrior*tierPrior +
			weights.Measured*candidate.measuredQualityScore)
	}
	// benchmark 缺失时：档先验占主导，measured 修正。
	measuredWeight := weights.Measured
	tierWeight := 1.0 - measuredWeight
	return clampUnit(tierWeight*tierPrior + measuredWeight*candidate.measuredQualityScore)
}

// frontierQualityHalfWidth 计算质量置信区间半宽：证据越弱区间越宽。
// 当候选接近本轮最高 benchmark 时，缩窄置信区间，让质量领先者更锐利，
// 避免显著质量优势被低成本候选的并列池吞掉。
func frontierQualityHalfWidth(candidate rankedModelCandidate, mode CostPreferenceMode, maxBenchmark float64) float64 {
	half := frontierQualityIntervalBase
	if clampUnit(candidate.profile.ProviderQualityConfidence) < 0.5 {
		half *= frontierLowConfidenceFactor
	}
	if candidate.benchmarkLane == "provisional" {
		half *= frontierProvisionalLaneFactor
	}
	// quality_first 下整体缩窄区间，让质量证据更锐利；cost_first 下保持较宽。
	switch mode {
	case CostPrefQualityFirst:
		half *= 0.85
	case CostPrefCostFirst:
		half *= 1.1
	}
	if candidate.benchmarkKnown && maxBenchmark > 0 {
		if delta := maxBenchmark - candidate.benchmarkScore; delta < premiumFrontierBenchmarkMinDelta {
			half *= 0.75
		}
	}
	return half
}

// ── 成本证据 ──

const (
	frontierCostScopeUSD         = "modelrank.usd"
	frontierCostScopeUSDProvider = "modelrank.usd_x_provider"
)

// frontierUseProviderMultiplier 决定成本轴是否乘 provider 套餐倍率。
// 仅当所有具备公开价的候选都同时具备 provider 倍率时启用，保证轴内语义一致；
// 否则整体退回公开价轴（宁可丢倍率信息，不混用两种成本语义）。
func frontierUseProviderMultiplier(ranked []rankedModelCandidate) bool {
	seenCost := false
	for i := range ranked {
		c := &ranked[i]
		if !c.publicCostKnown || c.normalizedPublicCostUSD <= 0 {
			continue
		}
		seenCost = true
		if !c.providerCostKnown || c.providerCostMultiplier <= 0 {
			return false
		}
	}
	return seenCost
}

// frontierUseMeasuredCost 决定成本轴是否用实测 cost 校准。
// 仅当所有可比（公开价已知且为正）候选都带有效实测 cost（>0 且非 NaN）时启用，
// 保证实测路径在轴内语义一致；否则整体回退推测系数（frontierEffortCostFactor）。
// 与 frontierUseProviderMultiplier 同源：宁可丢实测信息，不混用两种成本尺度——
// 实测 cost（每任务约数美元）与公开价×effort 系数（2M-token 假想价）量级差数倍，
// 混在同一 Pareto 成本轴会破坏支配与聚类，反转档位膨胀防护。
func frontierUseMeasuredCost(ranked []rankedModelCandidate) bool {
	seenCost := false
	for i := range ranked {
		c := &ranked[i]
		if !c.publicCostKnown || c.normalizedPublicCostUSD <= 0 {
			continue
		}
		seenCost = true
		if c.measuredCostUSD <= 0 || math.IsNaN(c.measuredCostUSD) {
			return false
		}
	}
	return seenCost
}

// buildFrontierPoints 把候选转换为 FrontierPoint。
// 公开价未知的候选被排除（成本轴不可比，由调用方决定是否回退旧链）。
// CandidateID 编码候选在 ranked 中的下标，选择后按它映射回原候选。
func buildFrontierPoints(ranked []rankedModelCandidate, floor CapabilityFloor, mode CostPreferenceMode) []FrontierPoint {
	useProviderMultiplier := frontierUseProviderMultiplier(ranked)
	useMeasuredCost := frontierUseMeasuredCost(ranked)
	scope := frontierCostScopeUSD
	source := "registry_pricing"
	if useProviderMultiplier {
		scope = frontierCostScopeUSDProvider
		source = "registry_pricing_x_provider_multiplier"
	}

	// 先扫描本批最高 benchmark，用于缩窄显著落后的候选置信区间。
	maxBenchmark := 0.0
	for i := range ranked {
		if ranked[i].benchmarkKnown && ranked[i].benchmarkScore > maxBenchmark {
			maxBenchmark = ranked[i].benchmarkScore
		}
	}

	points := make([]FrontierPoint, 0, len(ranked))
	for i := range ranked {
		c := ranked[i]
		if !c.publicCostKnown || c.normalizedPublicCostUSD <= 0 {
			continue
		}
		costUSD := c.normalizedPublicCostUSD
		if useProviderMultiplier {
			costUSD *= c.providerCostMultiplier
		}
		costUSD *= frontierCostFactorFor(c, useMeasuredCost)

		score := frontierQualityScore(c, mode)
		half := frontierQualityHalfWidth(c, mode, maxBenchmark)
		// 成本来源标记：本批启用实测路径且该候选实测 cost 有效时提升 source 以支持可解释性
		pointSource := source
		if useMeasuredCost && c.measuredCostUSD > 0 && !math.IsNaN(c.measuredCostUSD) {
			pointSource = "registry_pricing_x_measured_effort_cost"
			if useProviderMultiplier {
				pointSource = "registry_pricing_x_provider_multiplier_x_measured_effort_cost"
			}
		}

		points = append(points, FrontierPoint{
			CandidateID:    strconv.Itoa(i),
			CanonicalModel: c.profile.ModelID,
			ModelVersion:   c.versionLineage,
			Effort:         string(c.effort),
			Domain:         floor.TaskDomain,
			QualityScore:   score,
			QualityLow:     clampUnit(score - half),
			QualityHigh:    clampUnit(score + half),
			Cost: CostEvidence{
				Unit:       CostUnitUSD,
				ScopeID:    scope,
				Estimated:  int64(math.Round(costUSD * 1e6)),
				Confidence: CostConfidenceEstimated,
				Source:     pointSource,
			},
			EvidenceVersion: frontierEvidenceVersion,
		})
	}
	return points
}

// ── 前沿选择 ──

// maxCostPremiumPerQuality 按成本偏好车道与任务类权重动态决定溢价上限。
// quality_first / supervisor / long_context 允许更高溢价；cost_first / lightweight / embedding 更严格。
func maxCostPremiumPerQuality(mode CostPreferenceMode, taskClass TaskClass) float64 {
	base := frontierMaxCostPremiumPerQualityBase
	switch mode {
	case CostPrefQualityFirst:
		base = 6.0
	case CostPrefCostFirst:
		base = 1.0
	default:
		base = frontierMaxCostPremiumPerQualityBase
	}

	// 按任务类权重做 0.9~1.2 的微调：重质量任务稍微放宽，重成本任务稍微收紧。
	weights := DefaultTaskWeights()[taskClass]
	denom := weights.WCost
	if denom <= 0 {
		denom = 1
	}
	ratio := (weights.WQuality + 1) / (denom + 1)
	if ratio < 0.5 {
		ratio = 0.5
	}
	if ratio > 2.0 {
		ratio = 2.0
	}
	multiplier := 0.9 + 0.3*clampUnit((ratio-0.5)/1.5)
	return base * multiplier
}

// selectViaFrontier 在候选的 Pareto 前沿上按成本倾向车道选择最佳候选。
// 返回候选在 ranked 中的下标与可解释 note；ok=false 时 note 为回退原因。
func selectViaFrontier(
	ranked []rankedModelCandidate,
	floor CapabilityFloor,
	mode CostPreferenceMode,
) (idx int, note string, ok bool) {
	points := buildFrontierPoints(ranked, floor, mode)
	if len(points) < 2 {
		return -1, "insufficient_comparable_cost", false
	}
	forest := ComputeFrontierForest(points, points[0].Cost.ScopeID, frontierEvidenceVersion)
	if len(forest.Clusters) == 0 {
		return -1, "empty_frontier", false
	}

	capActive := floor.QualityBenefitCap != "" && floor.QualityBenefitCap != QualityTierPremium
	if mode == CostPrefQualityFirst && !capActive {
		idx, note = selectFrontierQualityFirst(forest, ranked)
	} else {
		target := ladderTargetCluster(forest, mode)
		if mode == CostPrefBalanced || (capActive && mode != CostPrefCostFirst) {
			target = capClusterCostPremium(forest.Clusters, target, mode, floor.TaskClass)
		}
		if capActive {
			capTarget := qualityBenefitCapClusterIndex(len(forest.Clusters), floor.QualityBenefitCap)
			if target > capTarget {
				target = capTarget
			}
			idx, note = selectBenefitCappedFrontierPoint(forest, target, ranked, mode, floor.QualityBenefitCap)
		} else {
			idx, note = selectFrontierClusterPoint(forest, target, ranked, mode)
		}
	}
	if idx < 0 {
		return -1, "no_frontier_candidate", false
	}
	return idx, note, true
}

// qualityBenefitCapClusterIndex 将兼容性的四档请求目标投影到本次请求动态生成的 F0...Fn。
// 它不改变模型的永久等级，也不限制簇数量；例如 7 个簇会映射为 F0/F2/F4/F6。
func qualityBenefitCapClusterIndex(clusterCount int, cap QualityTier) int {
	if clusterCount <= 1 {
		return 0
	}
	maxIndex := clusterCount - 1
	ratio := float64(qualityTierRank(cap)) / float64(qualityTierRank(QualityTierPremium))
	return int(math.Round(float64(maxIndex) * ratio))
}

// ladderTargetCluster 复用 BuildCandidateLadder 的车道目标簇（Preferred[0]）。
func ladderTargetCluster(forest FrontierForest, mode CostPreferenceMode) int {
	ladder := BuildCandidateLadder(forest, mode)
	if len(ladder.Preferred) == 0 {
		return 0
	}
	return ladder.Preferred[0].ClusterIdx
}

// capClusterCostPremium 是 balanced 车道的溢价保险：从膝点向低成本方向回退，
// 直到目标簇相对最便宜簇的成本溢价值回质量增益。
func capClusterCostPremium(clusters []FrontierCluster, target int, mode CostPreferenceMode, taskClass TaskClass) int {
	maxPremium := maxCostPremiumPerQuality(mode, taskClass)
	for target > 0 {
		gain := clusters[target].AvgQuality - clusters[0].AvgQuality
		if gain > 0 && clusters[target].AvgCost <= clusters[0].AvgCost*(1+gain*maxPremium) {
			break
		}
		target--
	}
	return target
}

// selectFrontierClusterPoint 在目标簇内选点（同族 > 成本 > 下标）。
func selectFrontierClusterPoint(
	forest FrontierForest,
	clusterIdx int,
	ranked []rankedModelCandidate,
	mode CostPreferenceMode,
) (int, string) {
	if clusterIdx < 0 || clusterIdx >= len(forest.Clusters) {
		return -1, ""
	}
	best := pickFrontierPoint(forest.Clusters[clusterIdx].Points, ranked)
	if best < 0 {
		return -1, ""
	}
	return best, fmt.Sprintf("frontier:%s/cluster=%d/v=%s", mode, clusterIdx, forest.Version)
}

// selectBenefitCappedFrontierPoint 在达到请求收益上限后按同族、成本和稳定 ID 选点。
// 同簇质量差异已被视为当前任务不再需要的额外收益，等成本时不重新升级到更强模型。
func selectBenefitCappedFrontierPoint(
	forest FrontierForest,
	clusterIdx int,
	ranked []rankedModelCandidate,
	mode CostPreferenceMode,
	cap QualityTier,
) (int, string) {
	if clusterIdx < 0 || clusterIdx >= len(forest.Clusters) {
		return -1, ""
	}
	best := pickBenefitCappedFrontierPoint(forest.Clusters[clusterIdx].Points, ranked)
	if best < 0 {
		return -1, ""
	}
	return best, fmt.Sprintf("frontier:%s/cluster=%d/benefit_cap=%s/v=%s", mode, clusterIdx, cap, forest.Version)
}

// selectFrontierQualityFirst 实现 quality_first 的并列容差规则：
// 与最高质量点置信区间重叠的前沿点视为质量并列，并列中取成本最低者，
// 避免为噪声级质量差异付出数倍成本。
func selectFrontierQualityFirst(forest FrontierForest, ranked []rankedModelCandidate) (int, string) {
	var all []FrontierPoint
	for _, cluster := range forest.Clusters {
		all = append(all, cluster.Points...)
	}
	if len(all) == 0 {
		return -1, ""
	}
	top := all[0]
	for _, p := range all[1:] {
		if p.QualityScore > top.QualityScore ||
			(p.QualityScore == top.QualityScore && p.Cost.Estimated < top.Cost.Estimated) {
			top = p
		}
	}
	tied := make([]FrontierPoint, 0, len(all))
	for _, p := range all {
		if p.QualityHigh >= top.QualityLow {
			tied = append(tied, p)
		}
	}
	best := pickFrontierQualityFirstPoint(tied, ranked)
	if best < 0 {
		return -1, ""
	}
	return best, fmt.Sprintf("frontier:%s/tie_pool=%d/v=%s", CostPrefQualityFirst, len(tied), forest.Version)
}

// pickFrontierQualityFirstPoint 在 quality_first 的并列池里优先兑现 benchmark/quality 证据，
// 避免高 benchmark 模型仅因成本更高就被低成本模型压掉；成本只在前述质量证据
// 仍无法区分时才作为次级 tie-break。这样 quality_first 才真正体现"质量优先"。
func pickFrontierQualityFirstPoint(points []FrontierPoint, ranked []rankedModelCandidate) int {
	bestIdx := -1
	var bestPoint FrontierPoint
	for _, p := range points {
		idx, err := strconv.Atoi(p.CandidateID)
		if err != nil || idx < 0 || idx >= len(ranked) {
			continue
		}
		if bestIdx < 0 {
			bestIdx, bestPoint = idx, p
			continue
		}
		cand, best := ranked[idx], ranked[bestIdx]
		if cand.sameFamily != best.sameFamily {
			if cand.sameFamily {
				bestIdx, bestPoint = idx, p
			}
			continue
		}
		// benchmark 是独立能力测量；差距足够大时直接兑现，不论是否同档。
		if cand.benchmarkKnown && best.benchmarkKnown {
			delta := cand.benchmarkScore - best.benchmarkScore
			if math.Abs(delta) >= premiumFrontierBenchmarkMinDelta {
				if delta > 0 {
					bestIdx, bestPoint = idx, p
				}
				continue
			}
		}
		// 同档内 benchmark 差不足阈值时，回到 qualityRank、成本、实测质量、qualityScore 的兜底链。
		if cand.qualityRank != best.qualityRank {
			if cand.qualityRank > best.qualityRank {
				bestIdx, bestPoint = idx, p
			}
			continue
		}
		if p.Cost.Estimated != bestPoint.Cost.Estimated {
			if p.Cost.Estimated < bestPoint.Cost.Estimated {
				bestIdx, bestPoint = idx, p
			}
			continue
		}
		if cand.measuredQualityScore != best.measuredQualityScore {
			if cand.measuredQualityScore > best.measuredQualityScore {
				bestIdx, bestPoint = idx, p
			}
			continue
		}
		if p.QualityScore != bestPoint.QualityScore {
			if p.QualityScore > bestPoint.QualityScore {
				bestIdx, bestPoint = idx, p
			}
			continue
		}
		if idx < bestIdx {
			bestIdx, bestPoint = idx, p
		}
	}
	return bestIdx
}

// pickFrontierPoint 在一组前沿点中按 同族 > 成本最低 > 质量最高 > 下标最小 选点（确定性）。
// 成本相同时取质量更高者——等价的免费质量不应放弃，同时保证结果与输入顺序无关。
func pickFrontierPoint(points []FrontierPoint, ranked []rankedModelCandidate) int {
	bestIdx := -1
	var bestPoint FrontierPoint
	for _, p := range points {
		idx, err := strconv.Atoi(p.CandidateID)
		if err != nil || idx < 0 || idx >= len(ranked) {
			continue
		}
		if bestIdx < 0 {
			bestIdx, bestPoint = idx, p
			continue
		}
		if candSame, bestSame := ranked[idx].sameFamily, ranked[bestIdx].sameFamily; candSame != bestSame {
			if candSame {
				bestIdx, bestPoint = idx, p
			}
			continue
		}
		if p.Cost.Estimated != bestPoint.Cost.Estimated {
			if p.Cost.Estimated < bestPoint.Cost.Estimated {
				bestIdx, bestPoint = idx, p
			}
			continue
		}
		if p.QualityScore != bestPoint.QualityScore {
			if p.QualityScore > bestPoint.QualityScore {
				bestIdx, bestPoint = idx, p
			}
			continue
		}
		if idx < bestIdx {
			bestIdx, bestPoint = idx, p
		}
	}
	return bestIdx
}

func pickBenefitCappedFrontierPoint(points []FrontierPoint, ranked []rankedModelCandidate) int {
	bestIdx := -1
	var bestPoint FrontierPoint
	for _, p := range points {
		idx, err := strconv.Atoi(p.CandidateID)
		if err != nil || idx < 0 || idx >= len(ranked) {
			continue
		}
		if bestIdx < 0 {
			bestIdx, bestPoint = idx, p
			continue
		}
		if candSame, bestSame := ranked[idx].sameFamily, ranked[bestIdx].sameFamily; candSame != bestSame {
			if candSame {
				bestIdx, bestPoint = idx, p
			}
			continue
		}
		if p.Cost.Estimated != bestPoint.Cost.Estimated {
			if p.Cost.Estimated < bestPoint.Cost.Estimated {
				bestIdx, bestPoint = idx, p
			}
			continue
		}
		candidateID := ranked[idx].normalizedCandidateID
		bestID := ranked[bestIdx].normalizedCandidateID
		if candidateID < bestID || (candidateID == bestID && idx < bestIdx) {
			bestIdx, bestPoint = idx, p
		}
	}
	return bestIdx
}
