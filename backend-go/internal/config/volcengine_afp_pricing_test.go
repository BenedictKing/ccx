package config

import (
	"testing"
	"time"
)

// ────────────────────────────────────────────────────────────────
// AFPScaledCoefficient 测试
// ────────────────────────────────────────────────────────────────

func TestAFPCoefficient_Basic(t *testing.T) {
	c45 := NewAFPCoefficient(4.5)
	if c45.Float64() < 4.499 || c45.Float64() > 4.501 {
		t.Fatalf("expected ~4.5, got %f", c45.Float64())
	}

	c025 := NewAFPCoefficient(0.25)
	if c025.Float64() < 0.249 || c025.Float64() > 0.251 {
		t.Fatalf("expected ~0.25, got %f", c025.Float64())
	}
}

func TestAFPCoefficient_Mul(t *testing.T) {
	// 4.5 × 0.67 × 0.25 = 0.75375
	c45 := NewAFPCoefficient(4.5)
	c067 := NewAFPCoefficient(0.67)
	c025 := NewAFPCoefficient(0.25)

	result := c45.Mul(c067).Mul(c025)
	expected := 0.75375
	got := result.Float64()
	if got < expected-0.001 || got > expected+0.001 {
		t.Fatalf("expected ~%f, got %f (diff=%f)", expected, got, got-expected)
	}
}

func TestAFPCoefficient_MulCommutative(t *testing.T) {
	a := NewAFPCoefficient(5.5)
	b := NewAFPCoefficient(0.4)
	ab := a.Mul(b)
	ba := b.Mul(a)
	if ab != ba {
		t.Fatalf("Mul not commutative: %d != %d", ab, ba)
	}
}

// ────────────────────────────────────────────────────────────────
// 输入分段测试
// ────────────────────────────────────────────────────────────────

func TestClassifyInputSegment(t *testing.T) {
	tests := []struct {
		name     string
		tokens   int
		expected InputSegment
	}{
		{"zero", 0, InputSegmentShort},
		{"1k", 1000, InputSegmentShort},
		{"32k", 32_000, InputSegmentShort},
		{"32k+1", 32_001, InputSegmentMedium},
		{"64k", 64_000, InputSegmentMedium},
		{"128k", 128_000, InputSegmentMedium},
		{"128k+1", 128_001, InputSegmentLong},
		{"256k", 256_000, InputSegmentLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyInputSegment(tt.tokens)
			if got != tt.expected {
				t.Fatalf("ClassifyInputSegment(%d) = %v, want %v", tt.tokens, got, tt.expected)
			}
		})
	}
}

func TestInputSegmentMultiplier(t *testing.T) {
	tests := []struct {
		seg      InputSegment
		expected float64
	}{
		{InputSegmentShort, 0.67},
		{InputSegmentMedium, 1.0},
		{InputSegmentLong, 2.0},
	}
	for _, tt := range tests {
		got := InputSegmentMultiplier(tt.seg).Float64()
		if got < tt.expected-0.001 || got > tt.expected+0.001 {
			t.Fatalf("InputSegmentMultiplier(%v) = %f, want %f", tt.seg, got, tt.expected)
		}
	}
}

// ────────────────────────────────────────────────────────────────
// ResolveVolcengineAFPCost 测试
// ────────────────────────────────────────────────────────────────

// cst 返回 Asia/Shanghai 时区的 time.Date 简写。
func cst(year int, month time.Month, day, hour, min, sec int) time.Time {
	return time.Date(year, month, day, hour, min, sec, 0, time.FixedZone("CST", 8*3600))
}

func TestResolveVolcengineAFPCost_DeepSeekV4Flash(t *testing.T) {
	// deepseek-v4-flash: 基础 0.5/0.5，无活动
	// 100k 输入（medium 段 ×1.0）、10k 输出
	// AFP = ceil(100000 × 0.5 × 1.0 / 10000) + ceil(10000 × 0.5 / 10000)
	//     = ceil(5.0) + ceil(0.5) = 5 + 1 = 6
	at := cst(2026, 7, 24, 12, 0, 0)
	result := ResolveVolcengineAFPCost(at, "agent_plan", "deepseek-v4-flash", 100_000, 10_000)

	if !result.Matched {
		t.Fatal("expected matched")
	}
	if result.Confidence != AFPCostConfidenceExact {
		t.Fatalf("confidence = %v, want exact", result.Confidence)
	}
	if result.PromotionApplied {
		t.Fatal("flash should have no promotion")
	}
	if result.TotalAFP != 6 {
		t.Fatalf("TotalAFP = %d, want 6", result.TotalAFP)
	}
}

func TestResolveVolcengineAFPCost_GLM52_ActivePromotion(t *testing.T) {
	// glm-5.2: 基础 4.5/4.5，×0.25 活动至 2026-08-09
	// 活动期有效系数：4.5 × 0.25 = 1.125
	// 100k 输入（medium 段 ×1.0）、10k 输出
	// AFP = ceil(100000 × 1.125 / 10000) + ceil(10000 × 1.125 / 10000)
	//     = ceil(11.25) + ceil(1.125) = 12 + 2 = 14
	at := cst(2026, 7, 24, 12, 0, 0)
	result := ResolveVolcengineAFPCost(at, "agent_plan", "glm-5.2", 100_000, 10_000)

	if !result.Matched {
		t.Fatal("expected matched")
	}
	if !result.PromotionApplied {
		t.Fatal("expected promotion applied")
	}
	if result.PromotionID != "volc-agent-glm52-x025-2026q3" {
		t.Fatalf("PromotionID = %q", result.PromotionID)
	}
	// 有效系数 = 4.5 × 1.0(medium) × 0.25(promo) = 1.125
	effIn := result.EffectiveInputCoeff.Float64()
	if effIn < 1.124 || effIn > 1.126 {
		t.Fatalf("EffectiveInputCoeff = %f, want ~1.125", effIn)
	}
	if result.TotalAFP != 14 {
		t.Fatalf("TotalAFP = %d, want 14", result.TotalAFP)
	}
}

func TestResolveVolcengineAFPCost_GLM52_ShortInput(t *testing.T) {
	// glm-5.2 活动期，short 输入段 (≤32k): 系数 = 4.5 × 0.67 × 0.25 = 0.75375
	// 10k 输入、10k 输出
	// AFP = ceil(10000 × 0.75375 / 10000) + ceil(10000 × 1.125 / 10000)
	//     = ceil(0.75375) + ceil(1.125) = 1 + 2 = 3
	at := cst(2026, 7, 24, 12, 0, 0)
	result := ResolveVolcengineAFPCost(at, "agent_plan", "glm-5.2", 10_000, 10_000)

	if !result.PromotionApplied {
		t.Fatal("expected promotion applied")
	}
	if result.InputSegment != InputSegmentShort {
		t.Fatalf("segment = %v, want short", result.InputSegment)
	}
	if result.TotalAFP != 3 {
		t.Fatalf("TotalAFP = %d, want 3", result.TotalAFP)
	}
}

func TestResolveVolcengineAFPCost_GLM52_LongInput(t *testing.T) {
	// glm-5.2 活动期，long 输入段 (>128k): 系数 = 4.5 × 2.0 × 0.25 = 2.25
	// 200k 输入、10k 输出
	// AFP = ceil(200000 × 2.25 / 10000) + ceil(10000 × 1.125 / 10000)
	//     = ceil(45.0) + ceil(1.125) = 45 + 2 = 47
	at := cst(2026, 7, 24, 12, 0, 0)
	result := ResolveVolcengineAFPCost(at, "agent_plan", "glm-5.2", 200_000, 10_000)

	if result.InputSegment != InputSegmentLong {
		t.Fatalf("segment = %v, want long", result.InputSegment)
	}
	if result.TotalAFP != 47 {
		t.Fatalf("TotalAFP = %d, want 47", result.TotalAFP)
	}
}

func TestResolveVolcengineAFPCost_GLM52_AfterPromotion(t *testing.T) {
	// 活动结束后：基础 4.5/4.5，无活动倍率
	// 100k 输入（medium）、10k 输出
	// AFP = ceil(100000 × 4.5 / 10000) + ceil(10000 × 4.5 / 10000)
	//     = ceil(45) + ceil(4.5) = 45 + 5 = 50
	at := cst(2026, 8, 10, 0, 0, 0) // 活动已于 8/9 00:00 结束
	result := ResolveVolcengineAFPCost(at, "agent_plan", "glm-5.2", 100_000, 10_000)

	if result.PromotionApplied {
		t.Fatal("promotion should not apply after end")
	}
	if result.TotalAFP != 50 {
		t.Fatalf("TotalAFP = %d, want 50", result.TotalAFP)
	}
}

func TestResolveVolcengineAFPCost_GLM52_PromotionBoundary(t *testing.T) {
	// 活动结束精确边界：2026-08-09 00:00:00 CST
	// 2026-08-08 23:59:59 仍在活动内
	atBefore := cst(2026, 8, 8, 23, 59, 59)
	r1 := ResolveVolcengineAFPCost(atBefore, "agent_plan", "glm-5.2", 100_000, 10_000)
	if !r1.PromotionApplied {
		t.Fatal("promotion should apply at 23:59:59 on Aug 8")
	}

	// 2026-08-09 00:00:00 已过活动边界
	atAfter := cst(2026, 8, 9, 0, 0, 0)
	r2 := ResolveVolcengineAFPCost(atAfter, "agent_plan", "glm-5.2", 100_000, 10_000)
	if r2.PromotionApplied {
		t.Fatal("promotion should NOT apply at 00:00:00 on Aug 9")
	}
}

func TestResolveVolcengineAFPCost_GLM52_PromotionStartBoundary(t *testing.T) {
	// 活动开始精确边界：2026-07-01 00:00:00 CST
	// 2026-06-30 23:59:59 活动未开始
	atBefore := cst(2026, 6, 30, 23, 59, 59)
	r1 := ResolveVolcengineAFPCost(atBefore, "agent_plan", "glm-5.2", 100_000, 10_000)
	if r1.PromotionApplied {
		t.Fatal("promotion should NOT apply before start")
	}

	// 2026-07-01 00:00:00 活动已开始
	atStart := cst(2026, 7, 1, 0, 0, 0)
	r2 := ResolveVolcengineAFPCost(atStart, "agent_plan", "glm-5.2", 100_000, 10_000)
	if !r2.PromotionApplied {
		t.Fatal("promotion should apply at start")
	}
}

func TestResolveVolcengineAFPCost_GLMLatest_Alias(t *testing.T) {
	// glm-latest 是 glm-5.2 的别名，应匹配同一规则
	at := cst(2026, 7, 24, 12, 0, 0)
	result := ResolveVolcengineAFPCost(at, "agent_plan", "glm-latest", 100_000, 10_000)

	if !result.Matched {
		t.Fatal("expected matched")
	}
	if !result.IsAlias {
		t.Fatal("expected IsAlias=true")
	}
	if result.AliasOf != "glm-5.2" {
		t.Fatalf("AliasOf = %q, want glm-5.2", result.AliasOf)
	}
	// 结果应与 glm-5.2 完全一致
	if result.TotalAFP != 14 {
		t.Fatalf("TotalAFP = %d, want 14 (same as glm-5.2)", result.TotalAFP)
	}
}

func TestResolveVolcengineAFPCost_KimiK3(t *testing.T) {
	// kimi-k3: 基础 10/10，无活动
	// 100k 输入（medium）、10k 输出
	// AFP = ceil(100000 × 10.0 / 10000) + ceil(10000 × 10.0 / 10000)
	//     = 100 + 10 = 110
	at := cst(2026, 7, 24, 12, 0, 0)
	result := ResolveVolcengineAFPCost(at, "agent_plan", "kimi-k3", 100_000, 10_000)

	if !result.Matched {
		t.Fatal("expected matched")
	}
	if result.PromotionApplied {
		t.Fatal("kimi-k3 should have no promotion")
	}
	if result.TotalAFP != 110 {
		t.Fatalf("TotalAFP = %d, want 110", result.TotalAFP)
	}
}

func TestResolveVolcengineAFPCost_DeepSeekV4Pro_ExpiredPromotion(t *testing.T) {
	// deepseek-v4-pro: 基础 5.5/5.5，×0.4 活动已于 2026-07-15 结束
	// 活动结束后使用基础系数
	at := cst(2026, 7, 24, 12, 0, 0)
	result := ResolveVolcengineAFPCost(at, "agent_plan", "deepseek-v4-pro", 100_000, 10_000)

	if !result.Matched {
		t.Fatal("expected matched")
	}
	if result.PromotionApplied {
		t.Fatal("pro promotion should be expired")
	}
	// 基础系数：5.5/5.5，100k medium ×1.0
	// AFP = ceil(100000 × 5.5 / 10000) + ceil(10000 × 5.5 / 10000)
	//     = 55 + 6 = 61
	if result.TotalAFP != 61 {
		t.Fatalf("TotalAFP = %d, want 61", result.TotalAFP)
	}
}

func TestResolveVolcengineAFPCost_KimiK26_ExpiredPromotion(t *testing.T) {
	// kimi-k2.6: 基础 4.5/4.5，×0.4 活动已结束
	at := cst(2026, 7, 24, 12, 0, 0)
	result := ResolveVolcengineAFPCost(at, "agent_plan", "kimi-k2.6", 100_000, 10_000)

	if !result.Matched {
		t.Fatal("expected matched")
	}
	if result.PromotionApplied {
		t.Fatal("k2.6 promotion should be expired")
	}
	// 基础系数：4.5/4.5
	// AFP = ceil(100000 × 4.5 / 10000) + ceil(10000 × 4.5 / 10000)
	//     = 45 + 5 = 50
	if result.TotalAFP != 50 {
		t.Fatalf("TotalAFP = %d, want 50", result.TotalAFP)
	}
}

func TestResolveVolcengineAFPCost_ZeroTokens(t *testing.T) {
	at := cst(2026, 7, 24, 12, 0, 0)
	result := ResolveVolcengineAFPCost(at, "agent_plan", "deepseek-v4-flash", 0, 0)

	if !result.Matched {
		t.Fatal("expected matched")
	}
	if result.TotalAFP != 0 {
		t.Fatalf("TotalAFP = %d, want 0", result.TotalAFP)
	}
}

func TestResolveVolcengineAFPCost_UnknownModel(t *testing.T) {
	at := cst(2026, 7, 24, 12, 0, 0)
	result := ResolveVolcengineAFPCost(at, "agent_plan", "gpt-5", 100_000, 10_000)

	if result.Matched {
		t.Fatal("should not match unknown model")
	}
	if result.Confidence != AFPCostConfidenceUnknown {
		t.Fatalf("confidence = %v, want unknown", result.Confidence)
	}
	if result.Reason == "" {
		t.Fatal("expected reason for unknown model")
	}
}

func TestResolveVolcengineAFPCost_AutoIsUpstreamRoutingMode(t *testing.T) {
	result := ResolveVolcengineAFPCost(cst(2026, 8, 15, 12, 0, 0), "agent_plan", "auto", 100_000, 10_000)

	if result.Matched {
		t.Fatal("auto is an upstream routing mode, not a statically priced model")
	}
	if result.Confidence != AFPCostConfidenceUnknown {
		t.Fatalf("confidence = %v, want unknown", result.Confidence)
	}
}

func TestResolveVolcengineAFPCost_UnsupportedPlan(t *testing.T) {
	at := cst(2026, 7, 24, 12, 0, 0)
	result := ResolveVolcengineAFPCost(at, "coding_plan", "glm-5.2", 100_000, 10_000)

	if result.Matched {
		t.Fatal("should not match coding_plan")
	}
	if result.Confidence != AFPCostConfidenceUnknown {
		t.Fatalf("confidence = %v, want unknown", result.Confidence)
	}
}

func TestResolveVolcengineAFPCost_ExpiredPromotionsAtCurrentDate(t *testing.T) {
	at := cst(2026, 8, 15, 12, 0, 0)
	for _, model := range []string{"glm-5.2", "glm-latest", "deepseek-v4-pro", "kimi-k2.6", "kimi-k2.7-code"} {
		t.Run(model, func(t *testing.T) {
			result := ResolveVolcengineAFPCost(at, "agent_plan", model, 100_000, 10_000)
			if !result.Matched {
				t.Fatalf("expected %s to match AFP catalog: %+v", model, result)
			}
			if result.PromotionApplied {
				t.Fatalf("promotion should be expired at %v: %+v", at, result)
			}
		})
	}
}

func TestResolveVolcengineAFPCost_NegativeTokens(t *testing.T) {
	at := cst(2026, 7, 24, 12, 0, 0)
	result := ResolveVolcengineAFPCost(at, "agent_plan", "glm-5.2", -1, 10_000)

	if !result.Matched {
		t.Fatal("expected matched (model exists)")
	}
	if result.Confidence != AFPCostConfidenceEstimated {
		t.Fatalf("confidence = %v, want estimated", result.Confidence)
	}
}

// ────────────────────────────────────────────────────────────────
// 计划文档关键样例验证（100k 输入、10k 输出，活动期 2026-07-24）
// ────────────────────────────────────────────────────────────────

func TestResolveVolcengineAFPCost_PlanKeyExamples(t *testing.T) {
	at := cst(2026, 7, 24, 12, 0, 0)
	inputTokens := 100_000
	outputTokens := 10_000

	tests := []struct {
		model    string
		expected int64
		desc     string
	}{
		{"deepseek-v4-flash", 6, "低成本标准模型，无活动"},
		{"glm-5.2", 14, "×0.25 活动期高性价比"},
		{"deepseek-v4-pro", 61, "×0.4 活动已结束，使用基础系数"},
		{"kimi-k3", 110, "无活动，基础成本最高"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			result := ResolveVolcengineAFPCost(at, "agent_plan", tt.model, inputTokens, outputTokens)
			if !result.Matched {
				t.Fatalf("expected matched for %s", tt.model)
			}
			if result.TotalAFP != tt.expected {
				t.Fatalf("%s: TotalAFP = %d, want %d (%s)", tt.model, result.TotalAFP, tt.expected, tt.desc)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────
// 整数稳定性测试：大 token 数不溢出
// ────────────────────────────────────────────────────────────────

func TestResolveVolcengineAFPCost_LargeTokens(t *testing.T) {
	at := cst(2026, 7, 24, 12, 0, 0)
	// 1M 输入 (>128k → long 段 ×2.0)、100k 输出，kimi-k3 (10/10)
	// AFP = ceil(1000000 × 10 × 2.0 / 10000) + ceil(100000 × 10 / 10000)
	//     = ceil(2000) + ceil(100) = 2000 + 100 = 2100
	result := ResolveVolcengineAFPCost(at, "agent_plan", "kimi-k3", 1_000_000, 100_000)
	if result.TotalAFP != 2100 {
		t.Fatalf("TotalAFP = %d, want 2100", result.TotalAFP)
	}
}

// ────────────────────────────────────────────────────────────────
// AgentPlanAFPRules 只读副本测试
// ────────────────────────────────────────────────────────────────

func TestAgentPlanAFPRules_ReturnsCopy(t *testing.T) {
	rules1 := AgentPlanAFPRules()
	rules2 := AgentPlanAFPRules()
	if len(rules1) != len(rules2) {
		t.Fatalf("length mismatch: %d vs %d", len(rules1), len(rules2))
	}
	// 修改副本不影响原始
	if len(rules1) > 0 {
		rules1[0].RuleID = "modified"
		rules3 := AgentPlanAFPRules()
		if rules3[0].RuleID == "modified" {
			t.Fatal("modifying returned slice affected internal state")
		}
	}
}

// ────────────────────────────────────────────────────────────────
// AFPModelEffectiveCoefficient 测试
// ────────────────────────────────────────────────────────────────

func TestAFPModelEffectiveCoefficient_ActivePromo(t *testing.T) {
	at := cst(2026, 7, 24, 12, 0, 0)
	in, outCoeff, applied, matched, _ := AFPModelEffectiveCoefficient(at, "glm-5.2")
	if !matched {
		t.Fatal("expected matched")
	}
	if !applied {
		t.Fatal("expected promotion applied")
	}
	// 4.5 × 0.25 = 1.125
	if in.Float64() < 1.124 || in.Float64() > 1.126 {
		t.Fatalf("inputCoeff = %f, want ~1.125", in.Float64())
	}
	if outCoeff.Float64() < 1.124 || outCoeff.Float64() > 1.126 {
		t.Fatalf("outputCoeff = %f, want ~1.125", outCoeff.Float64())
	}
}

func TestAFPModelEffectiveCoefficient_NoPromo(t *testing.T) {
	at := cst(2026, 7, 24, 12, 0, 0)
	in, outCoeff, applied, matched, _ := AFPModelEffectiveCoefficient(at, "kimi-k3")
	if !matched {
		t.Fatal("expected matched")
	}
	if applied {
		t.Fatal("kimi-k3 should have no promotion")
	}
	if in.Float64() < 9.999 || in.Float64() > 10.001 {
		t.Fatalf("inputCoeff = %f, want ~10.0", in.Float64())
	}
	if outCoeff.Float64() < 9.999 || outCoeff.Float64() > 10.001 {
		t.Fatalf("outputCoeff = %f, want ~10.0", outCoeff.Float64())
	}
}

func TestAFPModelEffectiveCoefficient_Unknown(t *testing.T) {
	at := cst(2026, 7, 24, 12, 0, 0)
	_, _, _, matched, reason := AFPModelEffectiveCoefficient(at, "unknown-model")
	if matched {
		t.Fatal("should not match unknown model")
	}
	if reason == "" {
		t.Fatal("expected reason")
	}
}
