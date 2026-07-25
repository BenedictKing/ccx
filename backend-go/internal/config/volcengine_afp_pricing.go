package config

import (
	"fmt"
	"math"
	"time"
)

// ────────────────────────────────────────────────────────────────
// 固定精度类型：AFP 系数使用 1/1,000,000 精度，避免浮点累积误差。
// 4.5 × 0.67 × 0.25 必须稳定得到 0.75375。
// ────────────────────────────────────────────────────────────────

// AFPScaledCoefficient 是 AFP 系数的固定精度表示，单位为 1/1,000,000。
// 例如 4.5 存储为 4_500_000，0.67 存储为 670_000。
type AFPScaledCoefficient int64

const afpScaleFactor int64 = 1_000_000

// NewAFPCoefficient 从浮点数创建固定精度系数。
// 仅用于目录初始化；运行时计算全部使用整数运算。
func NewAFPCoefficient(v float64) AFPScaledCoefficient {
	return AFPScaledCoefficient(math.Round(v * float64(afpScaleFactor)))
}

// Float64 将固定精度系数转换为浮点数，用于展示和 trace 输出。
func (c AFPScaledCoefficient) Float64() float64 {
	return float64(c) / float64(afpScaleFactor)
}

// Mul 两个固定精度系数相乘，结果仍为固定精度。
// (a * b) / scale，使用 int64 不会溢出（最大 10 * 10 * scale < 2^63）。
func (c AFPScaledCoefficient) Mul(other AFPScaledCoefficient) AFPScaledCoefficient {
	return AFPScaledCoefficient(int64(c) * int64(other) / afpScaleFactor)
}

// ────────────────────────────────────────────────────────────────
// 输入分段：AFP 输入系数按总输入 token 长度分段。
// ≤32k → 基础系数 × 0.67，(32k, 128k] → × 1，>128k → × 2
// ────────────────────────────────────────────────────────────────

// InputSegment 表示输入 token 长度分段。
type InputSegment int

const (
	InputSegmentShort  InputSegment = iota // ≤32k tokens
	InputSegmentMedium                     // (32k, 128k]
	InputSegmentLong                       // >128k tokens
)

// InputSegmentMultiplier 返回输入分段的倍率系数。
func InputSegmentMultiplier(seg InputSegment) AFPScaledCoefficient {
	switch seg {
	case InputSegmentShort:
		return NewAFPCoefficient(0.67)
	case InputSegmentMedium:
		return NewAFPCoefficient(1.0)
	case InputSegmentLong:
		return NewAFPCoefficient(2.0)
	default:
		return NewAFPCoefficient(1.0)
	}
}

// ClassifyInputSegment 根据输入 token 数判定分段。
func ClassifyInputSegment(inputTokens int) InputSegment {
	if inputTokens <= 32_000 {
		return InputSegmentShort
	}
	if inputTokens <= 128_000 {
		return InputSegmentMedium
	}
	return InputSegmentLong
}

// InputSegmentName 返回分段的可读名称。
func InputSegmentName(seg InputSegment) string {
	switch seg {
	case InputSegmentShort:
		return "short_le32k"
	case InputSegmentMedium:
		return "medium_32k_128k"
	case InputSegmentLong:
		return "long_gt128k"
	default:
		return "unknown"
	}
}

// ────────────────────────────────────────────────────────────────
// AFP 政策规则：模型基础系数、活动窗口、套餐类型。
// ────────────────────────────────────────────────────────────────

// AFPPromotionRule 描述一个 AFP 活动倍率规则。
// 时间窗口为 [StartsAt, EndsAtExclusive)，使用 Asia/Shanghai 时区。
type AFPPromotionRule struct {
	PromotionID string               // 活动唯一标识
	StartsAt    time.Time            // 活动开始时刻（Asia/Shanghai）
	EndsAt      time.Time            // 活动结束时刻（排他，Asia/Shanghai）
	Multiplier  AFPScaledCoefficient // 活动倍率（如 0.25 存储为 250_000）
	SourceURL   string               // 官方来源 URL
	VerifiedAt  time.Time            // 最后核验日期
}

// VolcengineAFPModelRule 描述火山 Agent Plan 下单个模型的 AFP 定价规则。
type VolcengineAFPModelRule struct {
	RuleID     string               // 规则唯一标识
	Plan       string               // 套餐类型："agent_plan"
	SourceURL  string               // 官方来源 URL
	VerifiedAt time.Time            // 最后核验日期
	ModelIDs   []string             // 精确模型 ID 列表（glm-latest 作为 glm-5.2 的显式别名）
	InputBase  AFPScaledCoefficient // 基础输入系数
	OutputBase AFPScaledCoefficient // 基础输出系数
	Promotions []AFPPromotionRule   // 该模型的活动规则列表
}

// ────────────────────────────────────────────────────────────────
// AFP 计算结果
// ────────────────────────────────────────────────────────────────

// AFPCostConfidence 表示 AFP 计算结果的置信度。
type AFPCostConfidence int

const (
	AFPCostConfidenceExact     AFPCostConfidence = iota // 完全匹配：plan + model + tokens 已知
	AFPCostConfidenceEstimated                          // 部分匹配：缺少输入或输出 token，使用估算
	AFPCostConfidenceUnknown                            // 无法计算：未知 plan、模型或缺失关键数据
)

// String 返回置信度的可读名称。
func (c AFPCostConfidence) String() string {
	switch c {
	case AFPCostConfidenceExact:
		return "exact"
	case AFPCostConfidenceEstimated:
		return "estimated"
	case AFPCostConfidenceUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// AFPCostResult 是 ResolveVolcengineAFPCost 的返回值。
type AFPCostResult struct {
	// 是否命中已知规则
	Matched bool

	// 规则信息
	RuleID  string // 匹配的模型规则 ID
	ModelID string // 实际匹配的模型 ID（可能是别名解析后的）
	IsAlias bool   // 输入 modelID 是否为别名
	AliasOf string // 别名指向的规范模型 ID
	Plan    string // 套餐类型

	// 系数
	InputBaseCoeff  AFPScaledCoefficient // 基础输入系数
	OutputBaseCoeff AFPScaledCoefficient // 基础输出系数
	InputSegment    InputSegment         // 输入分段
	SegmentMult     AFPScaledCoefficient // 输入分段倍率

	// 活动
	PromotionApplied bool                 // 是否命中活动
	PromotionID      string               // 活动 ID
	PromotionMult    AFPScaledCoefficient // 活动倍率

	// 有效系数 = 基础 × 分段 × 活动
	EffectiveInputCoeff  AFPScaledCoefficient
	EffectiveOutputCoeff AFPScaledCoefficient

	// AFP 计算结果
	InputAFP  int64 // 输入 AFP = ceil(inputTokens × effectiveInput / 10000)
	OutputAFP int64 // 输出 AFP = ceil(outputTokens × effectiveOutput / 10000)
	TotalAFP  int64 // 总 AFP = inputAFP + outputAFP

	// 置信度与不可计算原因
	Confidence AFPCostConfidence
	Reason     string // 不可计算时的原因说明
}

// ────────────────────────────────────────────────────────────────
// 内置火山 Agent Plan AFP 政策目录
// ────────────────────────────────────────────────────────────────

// agentPlanAFPRules 是编译期内置的火山 Agent Plan AFP 模型规则。
// 来源：https://www.volcengine.com/docs/82379/2533565
// 核验日期：2026-07-24
var agentPlanAFPRules = []VolcengineAFPModelRule{
	{
		RuleID:     "volc-agent-dsv4-flash",
		Plan:       "agent_plan",
		SourceURL:  "https://www.volcengine.com/docs/82379/2533565",
		VerifiedAt: time.Date(2026, 7, 24, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
		ModelIDs:   []string{"deepseek-v4-flash"},
		InputBase:  NewAFPCoefficient(0.5),
		OutputBase: NewAFPCoefficient(0.5),
		Promotions: nil, // 无本轮活动
	},
	{
		RuleID:     "volc-agent-glm52",
		Plan:       "agent_plan",
		SourceURL:  "https://www.volcengine.com/docs/82379/2533565",
		VerifiedAt: time.Date(2026, 7, 24, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
		ModelIDs:   []string{"glm-5.2", "glm-latest"}, // glm-latest 是 glm-5.2 的显式别名
		InputBase:  NewAFPCoefficient(4.5),
		OutputBase: NewAFPCoefficient(4.5),
		Promotions: []AFPPromotionRule{
			{
				PromotionID: "volc-agent-glm52-x025-2026q3",
				// 官方页面展示 2026-08-08 23:59:59，规范化为排他边界 2026-08-09 00:00:00
				StartsAt:   time.Date(2026, 7, 1, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
				EndsAt:     time.Date(2026, 8, 9, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
				Multiplier: NewAFPCoefficient(0.25),
				SourceURL:  "https://www.volcengine.com/docs/82379/2533565",
				VerifiedAt: time.Date(2026, 7, 24, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
			},
		},
	},
	{
		RuleID:     "volc-agent-dsv4-pro",
		Plan:       "agent_plan",
		SourceURL:  "https://www.volcengine.com/docs/82379/2533565",
		VerifiedAt: time.Date(2026, 7, 24, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
		ModelIDs:   []string{"deepseek-v4-pro"},
		InputBase:  NewAFPCoefficient(5.5),
		OutputBase: NewAFPCoefficient(5.5),
		Promotions: []AFPPromotionRule{
			{
				PromotionID: "volc-agent-dsv4-pro-x04-2026q3",
				// ×0.4 活动已于 2026-07-15 00:00:00 结束
				StartsAt:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
				EndsAt:     time.Date(2026, 7, 15, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
				Multiplier: NewAFPCoefficient(0.4),
				SourceURL:  "https://www.volcengine.com/docs/82379/2533565",
				VerifiedAt: time.Date(2026, 7, 24, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
			},
		},
	},
	{
		RuleID:     "volc-agent-kimi-k26",
		Plan:       "agent_plan",
		SourceURL:  "https://www.volcengine.com/docs/82379/2533565",
		VerifiedAt: time.Date(2026, 7, 24, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
		ModelIDs:   []string{"kimi-k2.6"},
		InputBase:  NewAFPCoefficient(4.5),
		OutputBase: NewAFPCoefficient(4.5),
		Promotions: []AFPPromotionRule{
			{
				PromotionID: "volc-agent-kimi-k26-x04-2026q3",
				// ×0.4 活动已结束
				StartsAt:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
				EndsAt:     time.Date(2026, 7, 15, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
				Multiplier: NewAFPCoefficient(0.4),
				SourceURL:  "https://www.volcengine.com/docs/82379/2533565",
				VerifiedAt: time.Date(2026, 7, 24, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
			},
		},
	},
	{
		RuleID:     "volc-agent-kimi-k27-code",
		Plan:       "agent_plan",
		SourceURL:  "https://www.volcengine.com/docs/82379/2533565",
		VerifiedAt: time.Date(2026, 7, 24, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
		ModelIDs:   []string{"kimi-k2.7-code"},
		InputBase:  NewAFPCoefficient(4.5),
		OutputBase: NewAFPCoefficient(4.5),
		Promotions: []AFPPromotionRule{
			{
				PromotionID: "volc-agent-kimi-k27-code-x025-2026q3",
				// ×0.25 活动已结束
				StartsAt:   time.Date(2026, 6, 1, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
				EndsAt:     time.Date(2026, 7, 1, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
				Multiplier: NewAFPCoefficient(0.25),
				SourceURL:  "https://www.volcengine.com/docs/82379/2533565",
				VerifiedAt: time.Date(2026, 7, 24, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
			},
		},
	},
	{
		RuleID:     "volc-agent-kimi-k3",
		Plan:       "agent_plan",
		SourceURL:  "https://www.volcengine.com/docs/82379/2533565",
		VerifiedAt: time.Date(2026, 7, 24, 0, 0, 0, 0, time.FixedZone("CST", 8*3600)),
		ModelIDs:   []string{"kimi-k3"},
		InputBase:  NewAFPCoefficient(10.0),
		OutputBase: NewAFPCoefficient(10.0),
		Promotions: nil, // 无本轮活动
	},
}

// ResolveVolcengineAFPCost 根据请求时刻、套餐类型、模型 ID、输入/输出 token 数计算 AFP 成本。
//
// 匹配顺序：精确模型 ID/别名 → 套餐类型 → 输入分段 → 活动倍率。
// 多个活动重叠是配置错误，取第一个在时间窗口内的活动（目录按时间排序）。
// 返回完整的系数链和 AFP 计算结果，供 trace 和诊断使用。
func ResolveVolcengineAFPCost(
	at time.Time,
	plan string,
	modelID string,
	inputTokens int,
	outputTokens int,
) AFPCostResult {
	result := AFPCostResult{
		Plan:       plan,
		Confidence: AFPCostConfidenceUnknown,
	}

	// 只支持 agent_plan
	if plan != "agent_plan" {
		result.Reason = fmt.Sprintf("unsupported plan: %q (only agent_plan supported)", plan)
		return result
	}

	// 匹配模型规则
	rule, matchedID, isAlias, aliasOf := matchAFPModelRule(modelID)
	if rule == nil {
		result.Reason = fmt.Sprintf("unknown model: %q (no AFP rule found)", modelID)
		return result
	}

	result.Matched = true
	result.RuleID = rule.RuleID
	result.ModelID = matchedID
	result.IsAlias = isAlias
	result.AliasOf = aliasOf
	result.InputBaseCoeff = rule.InputBase
	result.OutputBaseCoeff = rule.OutputBase

	// 输入分段
	seg := ClassifyInputSegment(inputTokens)
	segMult := InputSegmentMultiplier(seg)
	result.InputSegment = seg
	result.SegmentMult = segMult

	// 匹配活动规则（取第一个在时间窗口内的）
	for _, promo := range rule.Promotions {
		if !at.Before(promo.StartsAt) && at.Before(promo.EndsAt) {
			result.PromotionApplied = true
			result.PromotionID = promo.PromotionID
			result.PromotionMult = promo.Multiplier
			break
		}
	}
	if !result.PromotionApplied {
		result.PromotionMult = NewAFPCoefficient(1.0)
	}

	// 有效系数 = 基础 × 分段倍率 × 活动倍率
	result.EffectiveInputCoeff = rule.InputBase.Mul(segMult).Mul(result.PromotionMult)
	result.EffectiveOutputCoeff = rule.OutputBase.Mul(result.PromotionMult)

	// 计算 AFP：AFP = (inputTokens × effectiveInput + outputTokens × effectiveOutput) / 10_000
	// 使用向上取整，与火山计费一致
	if inputTokens >= 0 && outputTokens >= 0 {
		inputAFP := computeAFPComponent(inputTokens, result.EffectiveInputCoeff)
		outputAFP := computeAFPComponent(outputTokens, result.EffectiveOutputCoeff)
		result.InputAFP = inputAFP
		result.OutputAFP = outputAFP
		result.TotalAFP = inputAFP + outputAFP
		result.Confidence = AFPCostConfidenceExact
	} else {
		// tokens 未知时保留系数但降低置信度
		result.Confidence = AFPCostConfidenceEstimated
		result.Reason = "input or output tokens unknown; coefficients available but AFP not computed"
	}

	return result
}

// computeAFPComponent 计算单个方向的 AFP 分量。
// AFP = ceil(tokens × effectiveCoefficient / 10_000)
// 使用固定精度整数运算，避免浮点误差。
func computeAFPComponent(tokens int, coeff AFPScaledCoefficient) int64 {
	if tokens <= 0 {
		return 0
	}
	// tokens × coeff / scaleFactor / 10_000，向上取整
	// 先算 tokens × coeff（可能会很大，但 int64 足够）
	numerator := int64(tokens) * int64(coeff)
	denominator := afpScaleFactor * 10_000
	// 向上取整：(numerator + denominator - 1) / denominator
	return (numerator + denominator - 1) / denominator
}

// matchAFPModelRule 根据模型 ID 匹配内置 AFP 规则。
// 返回匹配的规则、实际匹配的模型 ID、是否为别名、别名指向。
func matchAFPModelRule(modelID string) (rule *VolcengineAFPModelRule, matchedID string, isAlias bool, aliasOf string) {
	normalizedID := normalizeAFPModelID(modelID)
	for i := range agentPlanAFPRules {
		r := &agentPlanAFPRules[i]
		for _, mid := range r.ModelIDs {
			if normalizeAFPModelID(mid) == normalizedID {
				// 判断是否为别名（规则内第一个 ModelID 是规范 ID）
				canonical := r.ModelIDs[0]
				if mid != canonical {
					return r, mid, true, canonical
				}
				return r, mid, false, ""
			}
		}
	}
	return nil, "", false, ""
}

// normalizeAFPModelID 规范化模型 ID 用于比较。
func normalizeAFPModelID(id string) string {
	// 小写 + 去空格
	result := make([]byte, 0, len(id))
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c == ' ' || c == '\t' {
			continue
		}
		if c >= 'A' && c <= 'Z' {
			c = c + ('a' - 'A')
		}
		result = append(result, c)
	}
	return string(result)
}

// AgentPlanAFPRules 返回内置 AFP 规则的只读副本，供诊断和 dry-run 使用。
func AgentPlanAFPRules() []VolcengineAFPModelRule {
	out := make([]VolcengineAFPModelRule, len(agentPlanAFPRules))
	copy(out, agentPlanAFPRules)
	return out
}

// AFPModelEffectiveCoefficient 返回指定模型在给定时刻的有效输入/输出系数。
// 供 SmartRouter 在构建 channelScoreEntry 时获取 AFP 成本信息。
// 返回值：(inputCoeff, outputCoeff, promotionApplied, matched, reason)
func AFPModelEffectiveCoefficient(at time.Time, modelID string) (
	inputCoeff, outputCoeff AFPScaledCoefficient,
	promotionApplied bool, matched bool, reason string,
) {
	rule, _, _, _ := matchAFPModelRule(modelID)
	if rule == nil {
		return 0, 0, false, false, fmt.Sprintf("no AFP rule for model %q", modelID)
	}

	promoMult := AFPScaledCoefficient(NewAFPCoefficient(1.0))
	applied := false
	for _, promo := range rule.Promotions {
		if !at.Before(promo.StartsAt) && at.Before(promo.EndsAt) {
			promoMult = promo.Multiplier
			applied = true
			break
		}
	}

	inputCoeff = rule.InputBase.Mul(promoMult)
	outputCoeff = rule.OutputBase.Mul(promoMult)
	return inputCoeff, outputCoeff, applied, true, ""
}
