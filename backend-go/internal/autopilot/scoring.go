package autopilot

import "fmt"

// ── 评分引擎（设计 §5.3 评分公式 + §5.5 派系偏好 + §5.6 价格偏向 + §5.7 任务域）──
// 纯函数库，不依赖调度逻辑。

// ── §5.3 权重表 ──

// ScoringWeights 是九项评分公式的权重配置。
// 设计 §5.3：Score = Σ(w_i * score_i) - penalty
type ScoringWeights struct {
	WQuality         float64 `json:"wQuality"`         // 质量档
	WStability       float64 `json:"wStability"`       // 稳定性
	WSpeed           float64 `json:"wSpeed"`           // 速度
	WCost            float64 `json:"wCost"`            // 成本档
	WSavings         float64 `json:"wSavings"`         // 省钱程度
	WTierMatch       float64 `json:"wTierMatch"`       // 策略优先标签匹配
	WFamily          float64 `json:"wFamily"`          // 模型派系偏好
	WProviderQuality float64 `json:"wProviderQuality"` // 上游供应商质量
	WDomain          float64 `json:"wDomain"`          // 任务域优势
}

// DefaultTaskWeights 返回七类 TaskClass 的默认权重表（§5.3 照抄）。
func DefaultTaskWeights() map[TaskClass]ScoringWeights {
	return map[TaskClass]ScoringWeights{
		TaskClassSupervisor: {
			WQuality: 3, WStability: 2, WSpeed: 1, WCost: 0, WSavings: 0.5,
			WTierMatch: 1, WFamily: 0.2, WProviderQuality: 1.0, WDomain: 0.5,
		},
		TaskClassWorker: {
			WQuality: 1, WStability: 1, WSpeed: 2, WCost: 2, WSavings: 3,
			WTierMatch: 1, WFamily: 0.2, WProviderQuality: 0.8, WDomain: 0.5,
		},
		TaskClassLightweight: {
			WQuality: 0, WStability: 1, WSpeed: 3, WCost: 2, WSavings: 3,
			WTierMatch: 1, WFamily: 0.1, WProviderQuality: 0.5, WDomain: 0.5,
		},
		TaskClassVision: {
			WQuality: 2, WStability: 2, WSpeed: 1, WCost: 1, WSavings: 1,
			WTierMatch: 1, WFamily: 0.2, WProviderQuality: 1.0, WDomain: 0.5,
		},
		TaskClassImageGen: {
			WQuality: 1, WStability: 2, WSpeed: 1, WCost: 2, WSavings: 2,
			WTierMatch: 1, WFamily: 0.1, WProviderQuality: 0.3, WDomain: 0,
		},
		TaskClassEmbedding: {
			WQuality: 0, WStability: 2, WSpeed: 2, WCost: 3, WSavings: 3,
			WTierMatch: 1, WFamily: 0, WProviderQuality: 0, WDomain: 0,
		},
		TaskClassLongContext: {
			WQuality: 2, WStability: 2, WSpeed: 1, WCost: 0, WSavings: 1,
			WTierMatch: 1, WFamily: 0.2, WProviderQuality: 1.0, WDomain: 0.5,
		},
	}
}

// ── §5.6 价格偏向三档预设 ──

// CostPreferenceMode 是价格偏向模式枚举。
type CostPreferenceMode string

const (
	CostPrefQualityFirst CostPreferenceMode = "quality_first" // 质量优先
	CostPrefBalanced     CostPreferenceMode = "balanced"      // 默认
	CostPrefCostFirst    CostPreferenceMode = "cost_first"    // 省钱优先
)

// costPreferencePreset 定义三档预设的乘数。
type costPreferencePreset struct {
	SavingsMultiplier         float64
	ProviderQualityMultiplier float64
}

// costPreferencePresets 是三档预设乘数表（§5.6.1）。
var costPreferencePresets = map[CostPreferenceMode]costPreferencePreset{
	CostPrefQualityFirst: {SavingsMultiplier: 0.3, ProviderQualityMultiplier: 1.5},
	CostPrefBalanced:     {SavingsMultiplier: 1.0, ProviderQualityMultiplier: 1.0},
	CostPrefCostFirst:    {SavingsMultiplier: 2.0, ProviderQualityMultiplier: 0.5},
}

// ApplyCostPreference 按价格偏向模式调整权重（§5.6.1）。
// 生效方式：effective_w_savings = w_savings * savingsMultiplier
//
//	effective_w_provider_quality = w_provider_quality * providerQualityMultiplier
//
// 返回新权重，不修改原 weights。
func ApplyCostPreference(weights ScoringWeights, mode CostPreferenceMode) ScoringWeights {
	preset, ok := costPreferencePresets[mode]
	if !ok {
		// 未知模式不修改权重（回退到 balanced）
		preset = costPreferencePresets[CostPrefBalanced]
	}
	out := weights
	out.WSavings *= preset.SavingsMultiplier
	out.WProviderQuality *= preset.ProviderQualityMultiplier
	return out
}

// ── §5.5 派系偏好评分 ──

// CalcFamilyPreferenceScore 计算模型派系偏好分（§5.5.2）。
// 越靠前分越高，不在列表中得 0 分。
// 示例：偏好 ["claude","openai","deepseek"]（n=3）
//
//	claude 得 3，openai 得 2，deepseek 得 1，其余得 0
func CalcFamilyPreferenceScore(family ModelFamily, prefs []ModelFamily) float64 {
	n := len(prefs)
	for i, f := range prefs {
		if f == family {
			return float64(n - i)
		}
	}
	return 0
}

// ── 评分输入 ──

// ScoringCandidate 是待评分的渠道候选项输入。
// 调用方负责预计算 SavingsScore（通过 NormalizeSavingsScore）和 DomainStrengthScore（通过 BuildDomainStrengthScore）。
type ScoringCandidate struct {
	ChannelUID    string // 渠道唯一标识
	QualityTier   QualityTier
	StabilityTier StabilityTier
	SpeedTier     SpeedTier
	CostTier      CostTier
	HealthState   HealthState

	// 供应商质量（同模型在不同上游的质量差异）
	ProviderQualityScore      float64 // 0.0-1.0
	ProviderQualityConfidence float64 // 置信度

	// 模型派系
	ModelFamily ModelFamily

	// SavingsScore 是归一化后的省钱程度（0.0-1.0，越便宜越高）。
	// 调用方应使用 NormalizeSavingsScore 预计算。
	SavingsScore float64

	// DomainStrengthScore 是该模型在当前任务域的优势分（0.0-1.0，0.5=中性）。
	// 调用方应使用 BuildDomainStrengthScore 预计算。
	DomainStrengthScore float64
	DomainEvidence      *DomainStrengthEvidence

	// QualityBenchmarkKnown/QualityBenchmarkScore 是 premium 档内连续 benchmark tie-break 的输入。
	// 仅在候选最终 QualityTier 为 premium 时生效，用于区分同为 premium 但能力差异明显的模型
	// （如 gpt-5.6-sol 与 gpt-5.4），不改变跨档位的绝对排序。QualityBenchmarkScore 为
	// config.ResolveModelBenchmarkProfile 返回的 OverallScore 原始值（0-100 量级）；
	// 调用方应使用 applyPremiumBenchmarkEvidence 预计算，未知时保持零值不参与细分。
	QualityBenchmarkKnown bool
	QualityBenchmarkScore float64
}

// ScoringContext 是评分时的上下文信息（来自请求/策略）。
type ScoringContext struct {
	TaskClass         TaskClass
	TaskDomain        TaskDomain
	TargetQualityTier QualityTier   // 当前任务目标档；空值时兼容旧的绝对档位评分
	QualityBenefitCap QualityTier   // 简单/常规任务的质量收益上限；超过后不再因质量档加分
	FamilyPrefs       []ModelFamily // 用户派系偏好顺序
	Weights           ScoringWeights

	// tierMatchHint：策略偏好的标签（quality/stability/speed/cost）。
	// 用于计算 tierMatchBonus（渠道画像标签匹配策略优先标签时 +10）。
	// 空串表示不使用 tierMatchBonus。
	TierMatchHint string
}

// ScoredCandidate 是评分结果。
type ScoredCandidate struct {
	ChannelUID string  `json:"channelUid"`
	Score      float64 `json:"score"` // 最终总分

	// ── 分项明细（供 trace 展示）──
	QualityScore         float64                 `json:"qualityScore"`
	StabilityScore       float64                 `json:"stabilityScore"`
	SpeedScore           float64                 `json:"speedScore"`
	CostScore            float64                 `json:"costScore"`
	SavingsScore         float64                 `json:"savingsScore"`
	TierMatchBonus       float64                 `json:"tierMatchBonus"`
	FamilyPrefScore      float64                 `json:"familyPrefScore"`
	ProviderQualityScore float64                 `json:"providerQualityScore"`
	DomainStrengthScore  float64                 `json:"domainStrengthScore"`
	DomainEvidence       *DomainStrengthEvidence `json:"domainEvidence,omitempty"`
	Penalty              float64                 `json:"penalty"`
}

// ── 评分公式 ──

// premiumBenchmarkTieBreakWeight 是 premium 档内连续 benchmark 证据对 qualityScore 的最大加成。
// 必须 < 1（qualityTierScore 相邻档位的最小间距），确保 benchmark 差异只能在同一档位内
// 重排序，永远不足以让 premium 候选反超下一档，也不会让弱 benchmark 的 premium 候选跌到 high 档以下。
const premiumBenchmarkTieBreakWeight = 0.5

// tierScoreMap 将各 tier 映射为评分公式中的分数。
// qualityScore: low=1, normal=2, high=3, premium=4
var qualityTierScore = map[QualityTier]float64{
	QualityTierLow:     1,
	QualityTierNormal:  2,
	QualityTierHigh:    3,
	QualityTierPremium: 4,
}

// stabilityScore: unstable=0, normal=1, stable=2
var stabilityTierScore = map[StabilityTier]float64{
	StabilityTierUnstable: 0,
	StabilityTierNormal:   1,
	StabilityTierStable:   2,
}

// speedScore: slow=0, normal=1, fast=2
var speedTierScore = map[SpeedTier]float64{
	SpeedTierSlow:   0,
	SpeedTierNormal: 1,
	SpeedTierFast:   2,
}

// costScore: expensive=0, normal=1, cheap=2, free=3
var costTierScore = map[CostTier]float64{
	CostTierExpensive: 0,
	CostTierNormal:    1,
	CostTierCheap:     2,
	CostTierFree:      3,
}

// ScoreCandidate 对单个候选项执行九项评分公式（§5.3）。
// 返回 ScoredCandidate，包含总分和各分项明细。
func ScoreCandidate(candidate ScoringCandidate, ctx ScoringContext) ScoredCandidate {
	w := ctx.Weights

	// 1. qualityScore：目标质量是下限，不对更高质量档做距离惩罚。
	// 简单/常规任务可通过 QualityBenefitCap 让质量收益在足够档位饱和，
	// 再由实测表现、成本等软证据决定；旧调用方未提供目标时保持绝对档位语义。
	qs := qualityTierScore[candidate.QualityTier]
	if ctx.TargetQualityTier != "" && candidate.QualityTier != "" {
		qualityRank := qualityTierRank(candidate.QualityTier)
		if ctx.QualityBenefitCap != "" && qualityRank > qualityTierRank(ctx.QualityBenefitCap) {
			qualityRank = qualityTierRank(ctx.QualityBenefitCap)
		}
		qs = float64(qualityRank + 1)
	}
	// premium 同档连续 benchmark tie-break：qualityTierScore/绝对档位语义把同为 premium 的
	// 候选压成同一个整数分（如 gpt-5.6-sol 与 gpt-5.4 都是 4）。benchmarkBonus 只在候选最终
	// 判定为 premium 且有已知 benchmark 证据时生效，幅度 < 1 保证不会让 premium 候选越级
	// 压过更高绝对档位（premium 本就是最高档，不存在越级问题），也不会打乱非 premium 候选
	// 之间原有的整数档位排序。
	benchmarkBenefitAllowed := ctx.QualityBenefitCap == "" ||
		qualityTierRank(candidate.QualityTier) <= qualityTierRank(ctx.QualityBenefitCap)
	if candidate.QualityTier == QualityTierPremium && candidate.QualityBenchmarkKnown && benchmarkBenefitAllowed {
		qs += clampF(candidate.QualityBenchmarkScore/100.0, 0, 1) * premiumBenchmarkTieBreakWeight
	}

	// 2. stabilityScore: unstable=0, normal=1, stable=2
	ss := stabilityTierScore[candidate.StabilityTier]

	// 3. speedScore: slow=0, normal=1, fast=2
	sp := speedTierScore[candidate.SpeedTier]

	// 4. costScore: expensive=0, normal=1, cheap=2, free=3
	cs := costTierScore[candidate.CostTier]

	// 5. savingsScore：调用方预计算的归一化省钱分（0.0-1.0）
	sav := candidate.SavingsScore

	// 6. tierMatchBonus：渠道画像标签匹配策略优先标签时 +10
	tmb := calcTierMatchBonus(candidate, ctx.TierMatchHint)

	// 7. familyPreferenceScore
	fps := CalcFamilyPreferenceScore(candidate.ModelFamily, ctx.FamilyPrefs)

	// 8. providerQualityScore（置信度 < 0.5 时视为 0，§5.3 约束）
	pqs := candidate.ProviderQualityScore
	if candidate.ProviderQualityConfidence < 0.5 {
		pqs = 0
	}

	// 9. domainStrengthScore：调用方通过 BuildDomainStrengthScore 预计算
	dss := candidate.DomainStrengthScore

	// 10. penalty：healthState=degraded 时 -5, limited 时 -20
	penalty := calcPenalty(candidate.HealthState)

	// 九项求和
	total := w.WQuality*qs +
		w.WStability*ss +
		w.WSpeed*sp +
		w.WCost*cs +
		w.WSavings*sav +
		w.WTierMatch*tmb +
		w.WFamily*fps +
		w.WProviderQuality*pqs +
		w.WDomain*dss -
		penalty

	return ScoredCandidate{
		ChannelUID:           candidate.ChannelUID,
		Score:                total,
		QualityScore:         qs,
		StabilityScore:       ss,
		SpeedScore:           sp,
		CostScore:            cs,
		SavingsScore:         sav,
		TierMatchBonus:       tmb,
		FamilyPrefScore:      fps,
		ProviderQualityScore: pqs,
		DomainStrengthScore:  dss,
		DomainEvidence:       candidate.DomainEvidence,
		Penalty:              penalty,
	}
}

// calcTierMatchBonus 计算策略优先标签匹配加分。
// 设计 §5.3：渠道画像标签匹配策略优先标签时 +10。
func calcTierMatchBonus(c ScoringCandidate, hint string) float64 {
	switch hint {
	case "quality":
		if c.QualityTier == QualityTierPremium {
			return 10
		}
	case "stability":
		if c.StabilityTier == StabilityTierStable {
			return 10
		}
	case "speed":
		if c.SpeedTier == SpeedTierFast {
			return 10
		}
	case "cost":
		if c.CostTier == CostTierFree || c.CostTier == CostTierCheap {
			return 10
		}
	}
	return 0
}

// calcPenalty 计算健康状态惩罚分（§5.3）。
// assist 模式必须保留全部候选，因此用终态惩罚确保 dead/misconfigured
// 不会因成本或输入顺序排在仍可尝试的渠道之前。
func calcPenalty(hs HealthState) float64 {
	switch hs {
	case HealthStateDegraded:
		return 5
	case HealthStateLimited:
		return 20
	case HealthStateDead, HealthStateMisconfigured:
		return 100
	default:
		return 0
	}
}

// ── §5.6.2 稳定性不可突破边界校验 ──

// ValidateWeightInvariants 校验权重不变量（§5.3 约束验证）。
// 校验项（对应设计文档的三个权重约束验证）：
//  1. w_stability * 2.0 > w_family * maxFamilyPref（stable 的非偏好派系始终胜过 unstable 的偏好派系）
//  2. w_stability * 2.0 > w_provider_quality * 1.0（stable 低质量实现仍优于 unstable 高质量实现）
//  3. w_stability * 2.0 > w_domain * 1.0（任务域优势不影响稳定性排序）
//
// 注意：w_savings 不在此校验范围内，因为 §5.6.2 稳定性压倒省钱是在 cost_first 乘数生效后
// 的有效权重层面需要保证的，基础权重表允许 worker/lightweight 的 w_savings > w_stability*2
// （cost_first 下 savings 放大 2.0 倍是设计意图，稳定性保障需在 ScoreCandidate 的调用层面处理）。
//
// maxFamilyPref 是派系偏好列表的最大可能长度（用于计算 w_family 的最大贡献）。
// 违反任一不变量返回 error，配置热重载时应调用此函数校验。
func ValidateWeightInvariants(weights ScoringWeights, maxFamilyPref int) error {
	if maxFamilyPref < 1 {
		maxFamilyPref = 1
	}

	stabilityDelta := weights.WStability * 2.0 // stable(2) - unstable(0)

	// 不变量 1：w_stability * 2.0 > w_family * maxFamilyPref
	familyMax := weights.WFamily * float64(maxFamilyPref)
	if stabilityDelta <= familyMax {
		return fmt.Errorf("weight invariant violated: w_stability(%v)*2.0=%v <= w_family(%v)*maxPref(%v)=%v; "+
			"stable must always beat unstable+preferred_family",
			weights.WStability, stabilityDelta, weights.WFamily, maxFamilyPref, familyMax)
	}

	// 不变量 2：w_stability * 2.0 > w_provider_quality * 1.0
	pqMax := weights.WProviderQuality * 1.0
	if stabilityDelta <= pqMax {
		return fmt.Errorf("weight invariant violated: w_stability(%v)*2.0=%v <= w_provider_quality(%v)*1.0=%v; "+
			"stable low-quality must beat unstable high-quality",
			weights.WStability, stabilityDelta, weights.WProviderQuality, pqMax)
	}

	// 不变量 3：w_stability * 2.0 > w_domain * 1.0
	domainMax := weights.WDomain * 1.0
	if stabilityDelta <= domainMax {
		return fmt.Errorf("weight invariant violated: w_stability(%v)*2.0=%v <= w_domain(%v)*1.0=%v; "+
			"domain advantage must not override stability",
			weights.WStability, stabilityDelta, weights.WDomain, domainMax)
	}

	return nil
}

// ValidateDefaultWeights 校验所有 TaskClass 的默认权重不变量。
// 用于启动时一次性校验，确保出厂配置满足设计约束。
func ValidateDefaultWeights(maxFamilyPref int) error {
	for tc, w := range DefaultTaskWeights() {
		if err := ValidateWeightInvariants(w, maxFamilyPref); err != nil {
			return fmt.Errorf("task class %q: %w", tc, err)
		}
	}
	return nil
}

// ── 辅助函数 ──

// clampF 将 f 钳制在 [lo, hi] 范围内。
func clampF(f, lo, hi float64) float64 {
	if f < lo {
		return lo
	}
	if f > hi {
		return hi
	}
	return f
}

// NormalizeSavingsScore 将估算成本归一化为 savings 分（0.0-1.0）。
// costs 是所有候选渠道的估算成本列表，最小成本得 1.0，最大得 0.0。
// 仅当成本有差异时才归一化；所有成本相同时所有候选得 0.5。
func NormalizeSavingsScore(costs map[string]float64) map[string]float64 {
	if len(costs) == 0 {
		return nil
	}

	minCost, maxCost := -1.0, -1.0
	for _, c := range costs {
		if c < 0 {
			continue // 忽略无效成本
		}
		if minCost < 0 || c < minCost {
			minCost = c
		}
		if maxCost < 0 || c > maxCost {
			maxCost = c
		}
	}

	result := make(map[string]float64, len(costs))
	if maxCost <= minCost {
		// 所有成本相同，全部给 0.5 中性分
		for uid := range costs {
			result[uid] = 0.5
		}
		return result
	}

	diff := maxCost - minCost
	for uid, c := range costs {
		if c < 0 {
			result[uid] = 0.5
			continue
		}
		// 越便宜分越高：1.0 - (cost - min) / (max - min)
		result[uid] = 1.0 - (c-minCost)/diff
	}
	return result
}

// BuildDomainStrengthScore 从 ModelProfile 和 TaskDomain 获取域优势分。
// 复用已有的 DomainStrength 函数，profile 为 nil 时返回 0.5 中性值。
func BuildDomainStrengthScore(profile *ModelProfile, domain TaskDomain) float64 {
	if profile == nil {
		return 0.5
	}
	return DomainStrength(profile, domain)
}
