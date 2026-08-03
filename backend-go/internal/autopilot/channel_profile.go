package autopilot

import (
	"sort"
	"time"
)

// ── Endpoint 能力不一致警告 ──

// EndpointInconsistency 记录同一 channel 内不同 endpoint 的能力差异。
type EndpointInconsistency struct {
	Dimension string `json:"dimension"` // "quality" | "vision" | "models" | "latency" | "health"
	Detail    string `json:"detail"`    // 例如 "endpoint-1: premium, endpoint-2: normal"
	Severity  string `json:"severity"`  // "info" | "warning"
}

// ── ChannelProfile 聚合视图 ──

// ChannelProfile 是 KeyEndpointProfile 的聚合视图，用于 UI 展示和调度粗筛。
// 不存储原始数据，由多个 KeyEndpointProfile 聚合而来。
type ChannelProfile struct {
	ChannelUID  string    `json:"channelUid"`
	ChannelID   int       `json:"channelId"` // 当前配置数组 index，仅用于展示/兼容
	ChannelKind string    `json:"channelKind"`
	OriginType  string    `json:"originType"`
	OriginTier  string    `json:"originTier"` // 信任/隐私等级，不参与质量聚合
	UpdatedAt   time.Time `json:"updatedAt"`

	// ── 聚合维度 ──
	HealthState   HealthState   `json:"healthState"`   // 混合健康态按可用性保守降级：全坏才 dead/misconfigured，夹杂健康 endpoint 时最多 degraded
	QualityTier   QualityTier   `json:"qualityTier"`   // 取最佳 endpoint 的质量
	StabilityTier StabilityTier `json:"stabilityTier"` // 取中位数
	SpeedTier     SpeedTier     `json:"speedTier"`     // 取中位数
	CostTier      CostTier      `json:"costTier"`      // 取最佳 endpoint 的成本

	// ── 能力标签（取并集）──
	SupportsVision    bool `json:"supportsVision"`
	SupportsToolCalls bool `json:"supportsToolCalls"`
	SupportsReasoning bool `json:"supportsReasoning"`
	SupportsLongCtx   bool `json:"supportsLongCtx"`

	// ── 聚合指标 ──
	TotalEndpoints   int     `json:"totalEndpoints"`
	HealthyEndpoints int     `json:"healthyEndpoints"`
	TotalModels      int     `json:"totalModels"` // 去重后的模型总数
	SuccessRate15m   float64 `json:"successRate15m"`
	P95LatencyMs     int64   `json:"p95LatencyMs"`

	// ── 能力不一致警告 ──
	EndpointInconsistencies []EndpointInconsistency `json:"endpointInconsistencies,omitempty"`

	Source     string  `json:"source"`
	Confidence float64 `json:"confidence"`
}

// ── HealthState 排序辅助 ──

// healthStateRank 返回 HealthState 的严重程度排序（数值越大越差）。
// 用于"取最差"聚合策略。
func healthStateRank(s HealthState) int {
	switch s {
	case HealthStateHealthy:
		return 0
	case HealthStateUnknown:
		return 1
	case HealthStateDegraded:
		return 2
	case HealthStateLimited:
		return 3
	case HealthStateMisconfigured:
		return 4
	case HealthStateDead:
		return 5
	default:
		return 1
	}
}

// ── tier 排序辅助 ──

// qualityTierRank 返回 QualityTier 数值排名（数值越大越好）。
func qualityTierRank(t QualityTier) int {
	switch t {
	case QualityTierLow:
		return 0
	case QualityTierNormal:
		return 1
	case QualityTierHigh:
		return 2
	case QualityTierPremium:
		return 3
	default:
		return 0
	}
}

// stabilityTierRank 返回 StabilityTier 数值排名（数值越大越好）。
func stabilityTierRank(t StabilityTier) int {
	switch t {
	case StabilityTierUnstable:
		return 0
	case StabilityTierNormal:
		return 1
	case StabilityTierStable:
		return 2
	default:
		return 0
	}
}

// speedTierRank 返回 SpeedTier 数值排名（数值越大越慢）。
func speedTierRank(t SpeedTier) int {
	switch t {
	case SpeedTierFast:
		return 0
	case SpeedTierNormal:
		return 1
	case SpeedTierSlow:
		return 2
	default:
		return 1
	}
}

// costTierRank 返回 CostTier 数值排名（数值越大越贵）。
func costTierRank(t CostTier) int {
	switch t {
	case CostTierFree:
		return 0
	case CostTierCheap:
		return 1
	case CostTierNormal:
		return 2
	case CostTierExpensive:
		return 3
	default:
		return 2
	}
}

// ── 聚合函数 ──

// AggregateChannelProfile 从多个 KeyEndpointProfile 聚合生成 ChannelProfile。
// 聚合策略（按设计 §3.3）：
//   - HealthState: 取最差（任一 endpoint dead → 整个 channel 至少 degraded）
//   - QualityTier: 取最佳
//   - StabilityTier: 取中位数
//   - SpeedTier: 取中位数
//   - CostTier: 取最佳（便宜的 endpoint 存在就有价值）
//   - 能力标签: 取并集
//
// endpoints 为空时返回零值 ChannelProfile。
func AggregateChannelProfile(channelUID string, channelID int, channelKind string, endpoints []KeyEndpointProfile) ChannelProfile {
	now := time.Now()
	cp := ChannelProfile{
		ChannelUID:  channelUID,
		ChannelID:   channelID,
		ChannelKind: channelKind,
		UpdatedAt:   now,
	}

	if len(endpoints) == 0 {
		cp.HealthState = HealthStateUnknown
		return cp
	}

	// 从第一个 endpoint 取渠道级字段（同 channel 下相同）
	cp.OriginType = endpoints[0].OriginType
	cp.OriginTier = endpoints[0].OriginTier

	cp.TotalEndpoints = len(endpoints)

	// 收集各维度值
	stabilityTiers := make([]int, 0, len(endpoints))
	speedTiers := make([]int, 0, len(endpoints))
	seenModels := make(map[string]struct{})
	var totalSuccessRate float64
	var p95Values []int64
	var inconsistencies []EndpointInconsistency

	bestQuality := -1
	bestCost := 999
	bestHealth := healthStateRank(HealthStateDead)
	worstHealth := 0
	hasHealthyLikeEndpoint := false

	for _, ep := range endpoints {
		// 健康统计
		if ep.HealthState == HealthStateHealthy || ep.HealthState == HealthStateUnknown {
			cp.HealthyEndpoints++
			hasHealthyLikeEndpoint = true
		}

		rank := healthStateRank(ep.HealthState)
		if rank > worstHealth {
			worstHealth = rank
		}
		if rank < bestHealth {
			bestHealth = rank
		}

		// QualityTier: 取最佳
		qr := qualityTierRank(ep.QualityTier)
		if qr > bestQuality {
			bestQuality = qr
		}

		// StabilityTier: 取中位数（优先读滞后后的 EffectiveStabilityTier）
		effTier := ep.EffectiveStabilityTier
		if effTier == "" {
			effTier = ep.StabilityTier
		}
		stabilityTiers = append(stabilityTiers, stabilityTierRank(effTier))

		// SpeedTier: 取中位数
		speedTiers = append(speedTiers, speedTierRank(ep.SpeedTier))

		// CostTier: 取最佳（最便宜）
		cr := costTierRank(ep.CostTier)
		if cr < bestCost {
			bestCost = cr
		}

		// 能力标签: 取并集
		if ep.SupportsVision {
			cp.SupportsVision = true
		}
		if ep.SupportsToolCalls {
			cp.SupportsToolCalls = true
		}
		if ep.SupportsReasoning {
			cp.SupportsReasoning = true
		}
		if ep.SupportsLongCtx {
			cp.SupportsLongCtx = true
		}

		// 模型去重
		for _, m := range ep.AvailableModels {
			seenModels[m] = struct{}{}
		}

		// 指标聚合
		totalSuccessRate += ep.SuccessRate15m
		if ep.P95LatencyMs > 0 {
			p95Values = append(p95Values, ep.P95LatencyMs)
		}
	}

	// 赋值聚合结果
	cp.HealthState = rankToHealthState(worstHealth)
	if hasHealthyLikeEndpoint {
		// 只要同渠道仍存在 healthy/unknown endpoint，就不让单个 dead/misconfigured endpoint
		// 把整条渠道直接打成不可用；真正完全不可用的渠道会在 hasHealthyLikeEndpoint=false
		// 时继续保留 worstHealth 终态。mixed 场景统一降到 degraded，既保守降权，又避免 -100
		// 惩罚把仍可承接请求的渠道直接打穿。
		if worstHealth > healthStateRank(HealthStateHealthy) {
			cp.HealthState = HealthStateDegraded
		} else {
			cp.HealthState = rankToHealthState(bestHealth)
		}
	}
	cp.QualityTier = rankToQualityTier(bestQuality)
	cp.StabilityTier = rankToStabilityTier(medianInt(stabilityTiers))
	cp.SpeedTier = rankToSpeedTier(medianInt(speedTiers))
	cp.CostTier = rankToCostTier(bestCost)
	cp.TotalModels = len(seenModels)
	cp.SuccessRate15m = totalSuccessRate / float64(len(endpoints))
	cp.P95LatencyMs = medianInt64(p95Values)

	// 检测能力不一致
	inconsistencies = append(inconsistencies, detectQualityInconsistency(endpoints)...)
	inconsistencies = append(inconsistencies, detectVisionInconsistency(endpoints)...)
	cp.EndpointInconsistencies = inconsistencies

	// 置信度取最低
	minConf := 1.0
	for _, ep := range endpoints {
		if ep.Confidence < minConf {
			minConf = ep.Confidence
		}
	}
	cp.Confidence = minConf

	return cp
}

// ── rank ↔ 枚举转换（表驱动）──
var healthStateByRank = [6]HealthState{HealthStateHealthy, HealthStateUnknown, HealthStateDegraded, HealthStateLimited, HealthStateMisconfigured, HealthStateDead}
var qualityTierByRank = [4]QualityTier{QualityTierLow, QualityTierNormal, QualityTierHigh, QualityTierPremium}
var stabilityTierByRank = [3]StabilityTier{StabilityTierUnstable, StabilityTierNormal, StabilityTierStable}
var speedTierByRank = [3]SpeedTier{SpeedTierFast, SpeedTierNormal, SpeedTierSlow}
var costTierByRank = [4]CostTier{CostTierFree, CostTierCheap, CostTierNormal, CostTierExpensive}

func rankToHealthState(rank int) HealthState {
	return lookupRank(rank, healthStateByRank[:], HealthStateUnknown)
}
func rankToQualityTier(rank int) QualityTier {
	return lookupRank(rank, qualityTierByRank[:], QualityTierLow)
}
func rankToStabilityTier(rank int) StabilityTier {
	return lookupRank(rank, stabilityTierByRank[:], StabilityTierUnstable)
}
func rankToSpeedTier(rank int) SpeedTier {
	return lookupRank(rank, speedTierByRank[:], SpeedTierNormal)
}
func rankToCostTier(rank int) CostTier { return lookupRank(rank, costTierByRank[:], CostTierNormal) }

// lookupRank 从切片中按 rank 取值，越界返回 defaultVal。
func lookupRank[T ~string](rank int, table []T, defaultVal T) T {
	if rank >= 0 && rank < len(table) {
		return table[rank]
	}
	return defaultVal
}

// ── 不一致检测 ──

func detectQualityInconsistency(endpoints []KeyEndpointProfile) []EndpointInconsistency {
	if len(endpoints) <= 1 {
		return nil
	}
	minQ, maxQ := qualityTierRank(endpoints[0].QualityTier), qualityTierRank(endpoints[0].QualityTier)
	minName, maxName := "", ""
	for _, ep := range endpoints {
		r := qualityTierRank(ep.QualityTier)
		if r < minQ {
			minQ = r
			minName = ep.EndpointUID
		}
		if r > maxQ {
			maxQ = r
			maxName = ep.EndpointUID
		}
	}
	if maxQ-minQ >= 2 {
		return []EndpointInconsistency{{
			Dimension: "quality",
			Detail:    minName + ": " + string(rankToQualityTier(minQ)) + ", " + maxName + ": " + string(rankToQualityTier(maxQ)),
			Severity:  "warning",
		}}
	}
	if maxQ-minQ == 1 {
		return []EndpointInconsistency{{
			Dimension: "quality",
			Detail:    minName + ": " + string(rankToQualityTier(minQ)) + ", " + maxName + ": " + string(rankToQualityTier(maxQ)),
			Severity:  "info",
		}}
	}
	return nil
}

func detectVisionInconsistency(endpoints []KeyEndpointProfile) []EndpointInconsistency {
	if len(endpoints) <= 1 {
		return nil
	}
	hasVision, hasNoVision := false, false
	var visionName, noVisionName string
	for _, ep := range endpoints {
		if ep.SupportsVision {
			hasVision = true
			if visionName == "" {
				visionName = ep.EndpointUID
			}
		} else {
			hasNoVision = true
			if noVisionName == "" {
				noVisionName = ep.EndpointUID
			}
		}
	}
	if hasVision && hasNoVision {
		return []EndpointInconsistency{{
			Dimension: "vision",
			Detail:    visionName + ": supports vision, " + noVisionName + ": no vision",
			Severity:  "warning",
		}}
	}
	return nil
}

// ── 统计辅助 ──

// medianInt 对已排序输入取中位数。输入会就地排序。
func medianInt(vals []int) int {
	if len(vals) == 0 {
		return 0
	}
	sort.Ints(vals)
	return vals[len(vals)/2]
}

// medianInt64 对已排序输入取中位数。输入会就地排序。
func medianInt64(vals []int64) int64 {
	if len(vals) == 0 {
		return 0
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	return vals[len(vals)/2]
}
