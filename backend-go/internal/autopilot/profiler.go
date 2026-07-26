package autopilot

import (
	"time"

	"github.com/BenedictKing/ccx/internal/utils"
)

// ── MetricsProvider 接口 ──

// MetricsProvider 是 Profiler 对指标系统的最小依赖接口。
// 便于测试时注入 mock，不直接依赖 *metrics.MetricsManager。
type MetricsProvider interface {
	// GetTimeWindowStatsForKey 获取指定 Key 在时间窗口内的统计。
	GetTimeWindowStatsForKey(channelKind, baseURL, apiKey, serviceType string, duration time.Duration) TimeWindowStats
	// GetKeySnapshot 获取指定 Key 的快照状态（熔断器、连续失败等）。
	GetKeySnapshot(channelKind, baseURL, apiKey, serviceType string) KeyCircuitSnapshot
}

// KeyCircuitSnapshot 是单个 Key 的熔断器与活跃度快照。
// 对应 metrics.KeyMetrics 中 Profiler 需要的子集。
type KeyCircuitSnapshot struct {
	// CircuitState 当前熔断器状态（对应 metrics.CircuitState）。
	// 使用 int 编码：0=closed, 1=open, 2=half_open，与 metrics.CircuitState 枚举值对齐。
	CircuitState int
	// ConsecutiveFailures 连续可重试失败次数。
	ConsecutiveFailures int64
	// LastSuccessAt 最近一次成功的时间。
	LastSuccessAt *time.Time
	// OverloadedCount 在查询窗口内 FailureClass=overloaded（429）的请求数。
	OverloadedCount int64
}

// TimeWindowStats 是从 metrics 包再导出的轻量统计结构。
// 字段对齐 metrics.TimeWindowStats，避免 profiler 包对 metrics 产生 import 依赖。
type TimeWindowStats struct {
	RequestCount          int64
	SuccessCount          int64
	FailureCount          int64
	SuccessRate           float64
	ConnectSampleCount    int64
	P95ConnectLatencyMs   int64
	FirstByteSampleCount  int64
	P95FirstByteLatencyMs int64
}

// ── Profiler ──

// Profiler 是 L1 被动画像生成器。
// 综合模型注册表（静态规则）与运行时指标，推导 KeyEndpointProfile 各维度。
// Phase 1 为 shadow 模式：只生成画像数据，不参与调度。
type Profiler struct {
	metrics MetricsProvider
}

// NewProfiler 创建 Profiler 实例。
func NewProfiler(metrics MetricsProvider) *Profiler {
	return &Profiler{metrics: metrics}
}

// ── 主入口 ──

// DeriveEndpointProfile 从 L1 被动指标推导单个 endpoint 的画像。
//
// 参数：
//   - channelUID/channelID/channelKind：渠道身份
//   - baseURL/apiKey/serviceType：endpoint 身份（与 metrics 系统一致）
//   - originType/originTier：渠道来源信任分类（设计 §3.2.1），来自
//     config.UpstreamConfig.OriginType/OriginTier；传空字符串时按 "unknown" 处理，
//     不在此处做任何猜测推断。只用于隐私/治理展示和同分 tie-breaker，不参与
//     QualityTier 等质量维度推导。
//
// 返回值已填充 StabilityTier、SpeedTier、CostTier、运行时指标和诊断证据。
// QualityTier 由 DeriveQualityTier 独立推导（需要额外的 modelID/provider 输入），
// 调用方可在获取画像后按需覆写。
func (p *Profiler) DeriveEndpointProfile(
	channelUID string,
	channelID int,
	channelKind string,
	baseURL string,
	apiKey string,
	serviceType string,
	originType string,
	originTier string,
) KeyEndpointProfile {
	now := time.Now()
	keyHash := KeyHashFromAPIKey(apiKey)
	endpointUID := GenerateEndpointUID(channelUID, baseURL, keyHash)

	if originType == "" {
		originType = string(OriginUnknown)
	}
	if originTier == "" {
		originTier = string(OriginTierUnknown)
	}

	profile := KeyEndpointProfile{
		ChannelUID:      channelUID,
		ChannelID:       channelID,
		ChannelKind:     channelKind,
		EndpointUID:     endpointUID,
		OriginType:      originType,
		OriginTier:      originTier,
		ServiceType:     serviceType,
		BaseURL:         baseURL,
		IdentityBaseURL: utils.MetricsIdentityBaseURL(baseURL, serviceType),
		KeyMask:         "***" + apiKey[max(0, len(apiKey)-4):],
		KeyHash:         keyHash,
		MetricsKey:      computeMetricsIdentityKey(baseURL, apiKey, serviceType),
		UpdatedAt:       now,
		HealthState:     HealthStateUnknown,
		QualityTier:     QualityTierLow,
		StabilityTier:   StabilityTierUnstable,
		SpeedTier:       SpeedTierNormal,
		CostTier:        CostTierNormal,
		Source:          "l1_passive",
		Confidence:      0.3, // L1 被动推导的基线置信度
		SuggestedAction: ActionNone,
	}

	// 获取指标数据
	snapshot := p.metrics.GetKeySnapshot(channelKind, baseURL, apiKey, serviceType)
	stats1h := p.metrics.GetTimeWindowStatsForKey(channelKind, baseURL, apiKey, serviceType, 1*time.Hour)

	// 填充运行时指标字段
	profile.ConsecutiveFail = int(snapshot.ConsecutiveFailures)
	profile.LastSuccessAt = snapshot.LastSuccessAt
	profile.SuccessRate15m = stats1h.SuccessRate // 用 1h 窗口近似（Phase 1 精度足够）
	profile.ConnectSampleCount = stats1h.ConnectSampleCount
	profile.P95ConnectLatencyMs = stats1h.P95ConnectLatencyMs
	if stats1h.ConnectSampleCount > 0 && stats1h.P95ConnectLatencyMs > 0 {
		profile.ConnectStatsUpdatedAt = &now
	}
	profile.FirstByteSampleCount = stats1h.FirstByteSampleCount
	profile.P95FirstByteLatencyMs = stats1h.P95FirstByteLatencyMs
	if stats1h.FirstByteSampleCount > 0 && stats1h.P95FirstByteLatencyMs > 0 {
		profile.FirstByteStatsUpdatedAt = &now
	}

	// 推导各维度
	profile.StabilityTier = DeriveStabilityTier(stats1h, snapshot)
	profile.SpeedTier = DeriveSpeedTierFromConnectStats(stats1h, snapshot)
	profile.CostTier = DeriveCostTier(stats1h, snapshot)
	profile.HealthState = deriveHealthState(stats1h, snapshot)

	// 诊断证据收集
	profile.HealthEvidence = collectHealthEvidence(stats1h, snapshot)

	// 如果明确不健康，建议探测
	if profile.HealthState == HealthStateDead || profile.HealthState == HealthStateMisconfigured {
		profile.SuggestedAction = ActionProbe
	}

	return profile
}

// ── 稳定性推导 ──

// DeriveStabilityTier 根据最近 1 小时的成功率和 429 率推导 StabilityTier。
// 遵循设计 §4.3 StabilityTier 推导规则。
func DeriveStabilityTier(stats TimeWindowStats, snapshot KeyCircuitSnapshot) StabilityTier {
	// 无足够数据时保持 unstable（保守策略）
	if stats.RequestCount < 5 {
		return StabilityTierUnstable
	}

	successRate := stats.SuccessRate
	var overloadRate float64
	if stats.RequestCount > 0 {
		overloadRate = float64(snapshot.OverloadedCount) / float64(stats.RequestCount) * 100
	}

	// 基本判定
	tier := classifyStabilityByRates(successRate, overloadRate)

	// 额外降级信号（任一命中则降级）
	tier = applyStabilityDowngrades(tier, snapshot)

	return tier
}

// classifyStabilityByRates 按成功率和 429 率做基本分类。
func classifyStabilityByRates(successRate, overloadRate float64) StabilityTier {
	if successRate >= 95 && overloadRate < 5 {
		return StabilityTierStable
	}
	if successRate >= 80 && overloadRate < 20 {
		return StabilityTierNormal
	}
	return StabilityTierUnstable
}

// applyStabilityDowngrades 检查额外降级信号。
func applyStabilityDowngrades(current StabilityTier, snapshot KeyCircuitSnapshot) StabilityTier {
	tier := current

	// 连续失败 >= 5 次 → 最高 normal
	if snapshot.ConsecutiveFailures >= 5 {
		if tier == StabilityTierStable {
			tier = StabilityTierNormal
		}
	}

	// 熔断器 open → unstable
	// CircuitState: 0=closed, 1=open, 2=half_open
	if snapshot.CircuitState == 1 {
		tier = StabilityTierUnstable
	}

	// 最近成功 > 6 小时前 → unstable
	if snapshot.LastSuccessAt != nil {
		if time.Since(*snapshot.LastSuccessAt) > 6*time.Hour {
			tier = StabilityTierUnstable
		}
	}

	return tier
}

// ── 速度推导 ──

// DeriveSpeedTier 推导 SpeedTier。
// 无连接耗时样本时返回 SpeedTierNormal 作为安全默认值。
func DeriveSpeedTier(snapshot KeyCircuitSnapshot) SpeedTier {
	_ = snapshot
	return SpeedTierNormal
}

// connectLatencyMinSamples 是 P95 连接延迟具备统计意义所需的最小样本量。
// n < 20 时 nearestRankPercentile 计算出的 P95 会退化为近似最大值，单次异常连接即可污染判定。
const connectLatencyMinSamples int64 = 20

// DeriveSpeedTierFromConnectStats 优先按连接取得耗时推导速度档；样本不足时保留安全默认值。
func DeriveSpeedTierFromConnectStats(stats TimeWindowStats, snapshot KeyCircuitSnapshot) SpeedTier {
	if stats.ConnectSampleCount < connectLatencyMinSamples || stats.P95ConnectLatencyMs <= 0 {
		return DeriveSpeedTier(snapshot)
	}
	return classifyConnectSpeedTier(stats.P95ConnectLatencyMs)
}

func classifyConnectSpeedTier(p95LatencyMs int64) SpeedTier {
	switch {
	case p95LatencyMs < 500:
		return SpeedTierFast
	case p95LatencyMs < 2_000:
		return SpeedTierNormal
	default:
		return SpeedTierSlow
	}
}

// ── 成本推导 ──

// DeriveCostTier 根据 CostProfile 和运行时信号推导 CostTier。
// 遵循设计 §4.3 CostTier 推导规则：
//   - 优先级 1：CostProfile 中的有效成本（手动配置或模型定价 x 倍率）
//   - 优先级 2：运行时行为启发（低置信度）
func DeriveCostTier(stats TimeWindowStats, snapshot KeyCircuitSnapshot) CostTier {
	// Phase 1：无 CostProfile 数据，使用运行时启发
	// 频繁 429 且无 Retry-After → 可能免费/低配额 → cheap
	if stats.RequestCount > 10 && snapshot.OverloadedCount > 0 {
		overloadRate := float64(snapshot.OverloadedCount) / float64(stats.RequestCount)
		if overloadRate > 0.3 {
			return CostTierCheap
		}
	}

	return CostTierNormal
}

// DeriveCostTierFromProfile 根据已有 CostProfile 推导 CostTier。
// 用于调用方已有手动/注册表定价数据的场景。
func DeriveCostTierFromProfile(profile CostProfile) CostTier {
	if profile.EffectiveInputCostPerMTok == 0 && profile.EffectiveOutputCostPerMTok == 0 {
		return CostTierFree
	}
	if profile.EffectiveInputCostPerMTok < 1.0 && profile.EffectiveOutputCostPerMTok < 5.0 {
		return CostTierCheap
	}
	if profile.EffectiveInputCostPerMTok < 10.0 && profile.EffectiveOutputCostPerMTok < 30.0 {
		return CostTierNormal
	}
	return CostTierExpensive
}

// ── 质量推导 ──

// DeriveQualityTier 综合推导 QualityTier。
// 遵循设计 §4.3 QualityTier 推导优先级：
//   - 优先级 1：模型注册表中的模型族（ModelProfileQualityTierFromFamily）
//   - 优先级 2：渠道级 LowQuality 标记（lowQuality=true → 最高 normal）
//   - 优先级 3：可选 capability-test 探测质量（Phase 1 不实现）
func DeriveQualityTier(family ModelFamily, modelID string, lowQuality bool) QualityTier {
	// 优先级 1：从模型族推导
	tier := ModelProfileQualityTierFromFamily(family, modelID)

	// 优先级 2：LowQuality 降级
	if lowQuality && tier > QualityTierNormal {
		return QualityTierNormal
	}

	return tier
}

// ── 健康状态推导（内部）──

// deriveHealthState 根据稳定性指标推导 HealthState。
// 这是 Profiler 的内部逻辑，不直接暴露给调用方。
func deriveHealthState(stats TimeWindowStats, snapshot KeyCircuitSnapshot) HealthState {
	if stats.RequestCount < 5 {
		return HealthStateUnknown
	}

	successRate := stats.SuccessRate

	// 死亡：成功率极低或长时间无成功
	if successRate < 20 {
		return HealthStateDead
	}
	if snapshot.LastSuccessAt != nil && time.Since(*snapshot.LastSuccessAt) > 24*time.Hour {
		return HealthStateDead
	}

	// 熔断器 open → limited
	if snapshot.CircuitState == 1 {
		return HealthStateLimited
	}

	// 连续失败 >= 5 → degraded
	if snapshot.ConsecutiveFailures >= 5 {
		return HealthStateDegraded
	}

	// 高 429 率 → limited
	if stats.RequestCount > 0 {
		overloadRate := float64(snapshot.OverloadedCount) / float64(stats.RequestCount) * 100
		if overloadRate >= 20 {
			return HealthStateLimited
		}
	}

	// 成功率偏低 → degraded
	if successRate < 80 {
		return HealthStateDegraded
	}

	return HealthStateHealthy
}

// ── 诊断证据收集 ──

// collectHealthEvidence 从指标数据中收集诊断证据字符串。
func collectHealthEvidence(stats TimeWindowStats, snapshot KeyCircuitSnapshot) []string {
	var evidence []string

	if stats.RequestCount < 5 {
		evidence = append(evidence, "insufficient_data: fewer than 5 requests in window")
		return evidence
	}

	if stats.SuccessRate < 80 {
		evidence = append(evidence, "low_success_rate: "+formatPercent(stats.SuccessRate/100))
	}

	if snapshot.ConsecutiveFailures >= 5 {
		evidence = append(evidence, "consecutive_failures: "+itoa(int(snapshot.ConsecutiveFailures)))
	}

	if snapshot.CircuitState == 1 {
		evidence = append(evidence, "circuit_breaker_open")
	}

	if stats.RequestCount > 0 {
		overloadRate := float64(snapshot.OverloadedCount) / float64(stats.RequestCount)
		if overloadRate >= 0.05 {
			evidence = append(evidence, "high_429_rate: "+formatPercent(overloadRate))
		}
	}

	if snapshot.LastSuccessAt != nil {
		elapsed := time.Since(*snapshot.LastSuccessAt)
		if elapsed > 6*time.Hour {
			evidence = append(evidence, "last_success_ago: "+elapsed.Truncate(time.Minute).String())
		}
	}

	return evidence
}
