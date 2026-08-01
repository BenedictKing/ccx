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
// 生产入口：ModelResolver.rankEligibleModels（model_resolver.go）。
// 仅在 frontierRoutingEnabled 开启且成本证据可比时生效；
// 其余情况回退到既有字典序比较链（fail-open）。
//
// 设计要点（与 codexradar 等效率曲线同源）：
//   - 全局比较：候选是全部 model × effort 组合，不先锁质量档再挑档；
//   - 成本与质量并列成轴：质量增益必须值回成本溢价（balanced 溢价帽）；
//   - 噪声级质量差异不值数倍成本：置信区间重叠视为并列，并列取低成本。
// ────────────────────────────────────────────────────────────────

const (
	// frontierEvidenceVersion 标识能力—成本证据的合成规则版本。
	frontierEvidenceVersion = "modelrank.v1"

	// frontierQualityIntervalBase 是质量置信区间的基准半宽。
	frontierQualityIntervalBase = 0.05
	// frontierLowConfidenceFactor 是实测质量置信度不足时的区间加宽倍数。
	frontierLowConfidenceFactor = 2.0
	// frontierProvisionalLaneFactor 是基准证据处于 provisional 泳道时的区间加宽倍数。
	frontierProvisionalLaneFactor = 1.5

	// frontierMaxCostPremiumPerQuality 是 balanced 车道的成本溢价上限：
	// 相对最便宜前沿簇，每 1.0 质量增益（0-1 刻度）最多接受 2.0 倍成本溢价，
	// 即每 0.01 质量增益约 2% 溢价。防止膝点落在"质量略高但成本翻倍"的簇上。
	frontierMaxCostPremiumPerQuality = 2.0
)

// frontierEffortCostFactor 是灰度期的 effort 成本系数（2026-07-26 联合路由计划 §3.4）。
// 高档思考显著增加输出 token；等成本下高档位会支配低档位造成档位膨胀，
// 因此用保守系数把档位差异折算进成本轴。待实测输出 token 统计积累后替换。
var frontierEffortCostFactor = map[EffortLevel]float64{
	EffortOff:     1.0,
	EffortMinimal: 1.0,
	EffortLow:     1.0,
	EffortMedium:  1.2,
	EffortHigh:    1.5,
	EffortMax:     2.0,
}

// frontierCostFactorFor 返回候选的 effort 成本系数；未决定或未识别的档位按 1.0 处理。
func frontierCostFactorFor(candidate rankedModelCandidate) float64 {
	if !candidate.effortDecided || candidate.effort == "" {
		return 1.0
	}
	if factor, ok := frontierEffortCostFactor[candidate.effort]; ok {
		return factor
	}
	return 1.0
}

// ── 质量分合成 ──

// frontierQualityScore 把候选的质量信号合成为 0-1 单轴分数。
// benchmark 为主锚（最接近独立能力测量），质量档为先验，实测质量为修正；
// benchmark 缺失时权重让渡给档先验与实测。measuredQualityScore 在候选构建时
// 已含 effort 加分与任务域修正（model_resolver.go），此处不重复计入。
func frontierQualityScore(candidate rankedModelCandidate) float64 {
	tierPrior := float64(candidate.qualityRank) / 3.0
	if candidate.benchmarkKnown && candidate.benchmarkScore > 0 {
		return clampUnit(0.5*(candidate.benchmarkScore/100.0) + 0.3*tierPrior + 0.2*candidate.measuredQualityScore)
	}
	return clampUnit(0.6*tierPrior + 0.4*candidate.measuredQualityScore)
}

// frontierQualityHalfWidth 计算质量置信区间半宽：证据越弱区间越宽。
// 区间重叠的候选互不支配（噪声级质量差异不压制），由 dominates 的区间规则消费。
func frontierQualityHalfWidth(candidate rankedModelCandidate) float64 {
	half := frontierQualityIntervalBase
	if clampUnit(candidate.profile.ProviderQualityConfidence) < 0.5 {
		half *= frontierLowConfidenceFactor
	}
	if candidate.benchmarkLane == "provisional" {
		half *= frontierProvisionalLaneFactor
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

// buildFrontierPoints 把候选转换为 FrontierPoint。
// 公开价未知的候选被排除（成本轴不可比，由调用方决定是否回退旧链）。
// CandidateID 编码候选在 ranked 中的下标，选择后按它映射回原候选。
func buildFrontierPoints(ranked []rankedModelCandidate, floor CapabilityFloor) []FrontierPoint {
	useProviderMultiplier := frontierUseProviderMultiplier(ranked)
	scope := frontierCostScopeUSD
	source := "registry_pricing"
	if useProviderMultiplier {
		scope = frontierCostScopeUSDProvider
		source = "registry_pricing_x_provider_multiplier"
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
		costUSD *= frontierCostFactorFor(c)

		score := frontierQualityScore(c)
		half := frontierQualityHalfWidth(c)
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
				Source:     source,
			},
			EvidenceVersion: frontierEvidenceVersion,
		})
	}
	return points
}

// ── 前沿选择 ──

// selectViaFrontier 在候选的 Pareto 前沿上按成本倾向车道选择最佳候选。
// 返回候选在 ranked 中的下标与可解释 note；ok=false 时 note 为回退原因。
func selectViaFrontier(
	ranked []rankedModelCandidate,
	floor CapabilityFloor,
	mode CostPreferenceMode,
) (idx int, note string, ok bool) {
	points := buildFrontierPoints(ranked, floor)
	if len(points) < 2 {
		return -1, "insufficient_comparable_cost", false
	}
	forest := ComputeFrontierForest(points, points[0].Cost.ScopeID, frontierEvidenceVersion)
	if len(forest.Clusters) == 0 {
		return -1, "empty_frontier", false
	}

	if mode == CostPrefQualityFirst {
		idx, note = selectFrontierQualityFirst(forest, ranked)
	} else {
		target := ladderTargetCluster(forest, mode)
		if mode == CostPrefBalanced {
			target = capClusterCostPremium(forest.Clusters, target)
		}
		idx, note = selectFrontierClusterPoint(forest, target, ranked, mode)
	}
	if idx < 0 {
		return -1, "no_frontier_candidate", false
	}
	return idx, note, true
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
func capClusterCostPremium(clusters []FrontierCluster, target int) int {
	for target > 0 {
		gain := clusters[target].AvgQuality - clusters[0].AvgQuality
		if gain > 0 && clusters[target].AvgCost <= clusters[0].AvgCost*(1+gain*frontierMaxCostPremiumPerQuality) {
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
	best := pickFrontierPoint(tied, ranked)
	if best < 0 {
		return -1, ""
	}
	return best, fmt.Sprintf("frontier:%s/tie_pool=%d/v=%s", CostPrefQualityFirst, len(tied), forest.Version)
}

// pickFrontierPoint 在一组前沿点中按 同族 > 成本最低 > 下标最小 选点（确定性）。
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
		if idx < bestIdx {
			bestIdx, bestPoint = idx, p
		}
	}
	return bestIdx
}
