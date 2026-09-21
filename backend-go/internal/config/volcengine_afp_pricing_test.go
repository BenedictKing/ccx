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

func TestResolveVolcengineAFPCost_DSV41Flash_ActivePromotion(t *testing.T) {
	// deepseek-v4.1-flash: 基础 2.5/2.5，×0.5 活动 2026-09-15 ~ 2026-09-29
	// 活动期有效系数：2.5 × 0.5 = 1.25（分段已取消，恒 ×1.0）
	// 100k 输入、10k 输出
	// AFP = ceil(100000 × 1.25 / 10000) + ceil(10000 × 1.25 / 10000)
	//     = ceil(12.5) + ceil(1.25) = 13 + 2 = 15
	at := cst(2026, 9, 20, 12, 0, 0)
	result := ResolveVolcengineAFPCost(at, "agent_plan", "deepseek-v4.1-flash", 100_000, 10_000)

	if !result.Matched {
		t.Fatal("expected matched")
	}
	if !result.PromotionApplied {
		t.Fatal("expected promotion applied")
	}
	if result.PromotionID != "volc-agent-dsv41-flash-x05-2026q3" {
		t.Fatalf("PromotionID = %q", result.PromotionID)
	}
	effIn := result.EffectiveInputCoeff.Float64()
	if effIn < 1.249 || effIn > 1.251 {
		t.Fatalf("EffectiveInputCoeff = %f, want ~1.25", effIn)
	}
	if result.TotalAFP != 15 {
		t.Fatalf("TotalAFP = %d, want 15", result.TotalAFP)
	}
}

func TestResolveVolcengineAFPCost_KimiK28Preview_ActivePromotion(t *testing.T) {
	// kimi-k2.8-preview: 基础 8/8，×0.6 活动 2026-09-17 ~ 2026-10-01
	// 活动期有效系数：8 × 0.6 = 4.8
	// 100k 输入、10k 输出
	// AFP = ceil(100000 × 4.8 / 10000) + ceil(10000 × 4.8 / 10000) = 48 + 5 = 53
	at := cst(2026, 9, 20, 12, 0, 0)
	result := ResolveVolcengineAFPCost(at, "agent_plan", "kimi-k2.8-preview", 100_000, 10_000)

	if !result.PromotionApplied {
		t.Fatal("expected promotion applied")
	}
	if result.PromotionID != "volc-agent-kimi-k28-preview-x06-2026q3" {
		t.Fatalf("PromotionID = %q", result.PromotionID)
	}
	if result.TotalAFP != 53 {
		t.Fatalf("TotalAFP = %d, want 53", result.TotalAFP)
	}
}

// ────────────────────────────────────────────────────────────────
// 输入分段系数取消（2026-09-01 CST 分界）
// 官方公告：分界后文本生成/向量化模型的输入抵扣系数不再随长度变化。
// ────────────────────────────────────────────────────────────────

func TestResolveVolcengineAFPCost_SegmentCutoff_ShortInput(t *testing.T) {
	// doubao-seed-2.1-turbo (2.5/2.5)，short 输入段 (≤32k)
	// 分界前 ×0.67：10k 输入、1k 输出
	//   AFP = ceil(10000 × 2.5 × 0.67 / 10000) + ceil(1000 × 2.5 / 10000)
	//       = ceil(1.675) + ceil(0.25) = 2 + 1 = 3
	atBefore := cst(2026, 8, 31, 23, 59, 59)
	r1 := ResolveVolcengineAFPCost(atBefore, "agent_plan", "doubao-seed-2.1-turbo", 10_000, 1_000)
	if !r1.Matched {
		t.Fatal("expected matched before cutoff")
	}
	if r1.InputSegment != InputSegmentShort {
		t.Fatalf("segment before cutoff = %v, want short", r1.InputSegment)
	}
	if got := r1.SegmentMult.Float64(); got < 0.669 || got > 0.671 {
		t.Fatalf("SegmentMult before cutoff = %f, want ~0.67", got)
	}
	if r1.TotalAFP != 3 {
		t.Fatalf("TotalAFP before cutoff = %d, want 3", r1.TotalAFP)
	}

	// 分界后统一 ×1.0：AFP = ceil(10000 × 2.5 / 10000) + ceil(1000 × 2.5 / 10000) = 3 + 1 = 4
	atAfter := cst(2026, 9, 1, 0, 0, 0)
	r2 := ResolveVolcengineAFPCost(atAfter, "agent_plan", "doubao-seed-2.1-turbo", 10_000, 1_000)
	if got := r2.SegmentMult.Float64(); got != 1.0 {
		t.Fatalf("SegmentMult after cutoff = %f, want 1.0", got)
	}
	if r2.TotalAFP != 4 {
		t.Fatalf("TotalAFP after cutoff = %d, want 4", r2.TotalAFP)
	}
}

func TestResolveVolcengineAFPCost_SegmentCutoff_LongInput(t *testing.T) {
	// kimi-k3 (10/10)，long 输入段 (>128k)：200k 输入、10k 输出
	// 分界前 ×2.0：AFP = ceil(200000 × 10 × 2 / 10000) + ceil(10000 × 10 / 10000)
	//                  = 400 + 10 = 410
	atBefore := cst(2026, 8, 31, 23, 59, 59)
	r1 := ResolveVolcengineAFPCost(atBefore, "agent_plan", "kimi-k3", 200_000, 10_000)
	if r1.InputSegment != InputSegmentLong {
		t.Fatalf("segment before cutoff = %v, want long", r1.InputSegment)
	}
	if r1.TotalAFP != 410 {
		t.Fatalf("TotalAFP before cutoff = %d, want 410", r1.TotalAFP)
	}

	// 分界后 ×1.0：AFP = 200 + 10 = 210
	// 注意 InputSegment 仍如实记录 long，但 SegmentMult 反映实际应用的 1.0
	atAfter := cst(2026, 9, 1, 0, 0, 0)
	r2 := ResolveVolcengineAFPCost(atAfter, "agent_plan", "kimi-k3", 200_000, 10_000)
	if r2.InputSegment != InputSegmentLong {
		t.Fatalf("segment after cutoff = %v, want long (informational)", r2.InputSegment)
	}
	if got := r2.SegmentMult.Float64(); got != 1.0 {
		t.Fatalf("SegmentMult after cutoff = %f, want 1.0", got)
	}
	if r2.TotalAFP != 210 {
		t.Fatalf("TotalAFP after cutoff = %d, want 210", r2.TotalAFP)
	}
}

func TestInputSegmentMultiplierAt(t *testing.T) {
	before := cst(2026, 8, 31, 23, 59, 59)
	after := cst(2026, 9, 1, 0, 0, 0)

	tests := []struct {
		name     string
		at       time.Time
		tokens   int
		expected float64
	}{
		{"分界前 short", before, 10_000, 0.67},
		{"分界前 medium", before, 100_000, 1.0},
		{"分界前 long", before, 200_000, 2.0},
		{"分界后 short", after, 10_000, 1.0},
		{"分界后 medium", after, 100_000, 1.0},
		{"分界后 long", after, 200_000, 1.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InputSegmentMultiplierAt(tt.at, tt.tokens).Float64()
			if got < tt.expected-0.001 || got > tt.expected+0.001 {
				t.Fatalf("InputSegmentMultiplierAt(%v, %d) = %f, want %f", tt.at, tt.tokens, got, tt.expected)
			}
		})
	}
}

func TestResolveVolcengineAFPCost_DSV41Flash_AfterPromotion(t *testing.T) {
	// 活动结束后（2026-09-29 00:00 起）：基础 2.5/2.5，无活动倍率
	// 100k 输入、10k 输出
	// AFP = ceil(100000 × 2.5 / 10000) + ceil(10000 × 2.5 / 10000) = 25 + 3 = 28
	at := cst(2026, 10, 1, 0, 0, 0)
	result := ResolveVolcengineAFPCost(at, "agent_plan", "deepseek-v4.1-flash", 100_000, 10_000)

	if !result.Matched {
		t.Fatal("expected matched")
	}
	if result.PromotionApplied {
		t.Fatal("promotion should not apply after end")
	}
	if result.TotalAFP != 28 {
		t.Fatalf("TotalAFP = %d, want 28", result.TotalAFP)
	}
}

func TestResolveVolcengineAFPCost_DSV41Flash_PromotionBoundary(t *testing.T) {
	// 活动结束精确边界：2026-09-29 00:00:00 CST
	// 2026-09-28 23:59:59 仍在活动内
	atBefore := cst(2026, 9, 28, 23, 59, 59)
	r1 := ResolveVolcengineAFPCost(atBefore, "agent_plan", "deepseek-v4.1-flash", 100_000, 10_000)
	if !r1.PromotionApplied {
		t.Fatal("promotion should apply at 23:59:59 on Sep 28")
	}

	// 2026-09-29 00:00:00 已过活动边界
	atAfter := cst(2026, 9, 29, 0, 0, 0)
	r2 := ResolveVolcengineAFPCost(atAfter, "agent_plan", "deepseek-v4.1-flash", 100_000, 10_000)
	if r2.PromotionApplied {
		t.Fatal("promotion should NOT apply at 00:00:00 on Sep 29")
	}
}

func TestResolveVolcengineAFPCost_DSV41Flash_PromotionStartBoundary(t *testing.T) {
	// 活动开始精确边界：2026-09-15 00:00:00 CST
	// 2026-09-14 23:59:59 活动未开始
	atBefore := cst(2026, 9, 14, 23, 59, 59)
	r1 := ResolveVolcengineAFPCost(atBefore, "agent_plan", "deepseek-v4.1-flash", 100_000, 10_000)
	if r1.PromotionApplied {
		t.Fatal("promotion should NOT apply before start")
	}

	// 2026-09-15 00:00:00 活动已开始
	atStart := cst(2026, 9, 15, 0, 0, 0)
	r2 := ResolveVolcengineAFPCost(atStart, "agent_plan", "deepseek-v4.1-flash", 100_000, 10_000)
	if !r2.PromotionApplied {
		t.Fatal("promotion should apply at start")
	}
}

func TestResolveVolcengineAFPCost_Glm53Flash_ExpiredPromotion(t *testing.T) {
	// glm-5.3-flash: 基础 0.5/0.5，×0.5 活动 2026-08-28 ~ 2026-09-12（已结束）
	// 活动内：0.5 × 0.5 = 0.25；100k/10k → 3 + 1 = 4
	atActive := cst(2026, 9, 1, 12, 0, 0)
	r1 := ResolveVolcengineAFPCost(atActive, "agent_plan", "glm-5.3-flash", 100_000, 10_000)
	if !r1.PromotionApplied {
		t.Fatal("promotion should apply during Aug 28 - Sep 11 window")
	}
	if r1.TotalAFP != 4 {
		t.Fatalf("TotalAFP in promo = %d, want 4", r1.TotalAFP)
	}

	// 活动后：基础 0.5/0.5 → 5 + 1 = 6
	atAfter := cst(2026, 9, 20, 12, 0, 0)
	r2 := ResolveVolcengineAFPCost(atAfter, "agent_plan", "glm-5.3-flash", 100_000, 10_000)
	if r2.PromotionApplied {
		t.Fatal("promotion should be expired on Sep 20")
	}
	if r2.TotalAFP != 6 {
		t.Fatalf("TotalAFP after promo = %d, want 6", r2.TotalAFP)
	}
}

func TestResolveVolcengineAFPCost_DoubaoEmbeddingVision(t *testing.T) {
	// 向量化模型，基础 0.5/0.5，无活动
	// 100k 输入、10k 输出 → ceil(5.0) + ceil(0.5) = 5 + 1 = 6
	at := cst(2026, 9, 20, 12, 0, 0)
	result := ResolveVolcengineAFPCost(at, "agent_plan", "doubao-embedding-vision", 100_000, 10_000)

	if !result.Matched {
		t.Fatal("expected matched")
	}
	if result.PromotionApplied {
		t.Fatal("embedding model should have no promotion")
	}
	if result.TotalAFP != 6 {
		t.Fatalf("TotalAFP = %d, want 6", result.TotalAFP)
	}
}

func TestResolveVolcengineAFPCost_GLM52_Delisted(t *testing.T) {
	// glm-5.2 已下线，官方抵扣表已无此模型（本次同步移除）
	at := cst(2026, 7, 24, 12, 0, 0)
	result := ResolveVolcengineAFPCost(at, "agent_plan", "glm-5.2", 100_000, 10_000)

	if result.Matched {
		t.Fatal("glm-5.2 should no longer match the AFP catalog")
	}
	if result.Confidence != AFPCostConfidenceUnknown {
		t.Fatalf("confidence = %v, want unknown", result.Confidence)
	}
}

func TestResolveVolcengineAFPCost_GLMLatest_Alias(t *testing.T) {
	// glm-latest 是 glm-5.3 的别名，应匹配同一规则（无活动）
	at := cst(2026, 8, 18, 12, 0, 0)
	result := ResolveVolcengineAFPCost(at, "agent_plan", "glm-latest", 100_000, 10_000)

	if !result.Matched {
		t.Fatal("expected matched")
	}
	if !result.IsAlias {
		t.Fatal("expected IsAlias=true")
	}
	if result.AliasOf != "glm-5.3" {
		t.Fatalf("AliasOf = %q, want glm-5.3", result.AliasOf)
	}
	// 结果应与 glm-5.3 完全一致：45 + 5 = 50
	if result.TotalAFP != 50 {
		t.Fatalf("TotalAFP = %d, want 50 (same as glm-5.3)", result.TotalAFP)
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

func TestResolveVolcengineAFPCost_KimiK26_Delisted(t *testing.T) {
	// kimi-k2.6 已于 2026-08-18 下线，目录中不再收录
	at := cst(2026, 8, 18, 12, 0, 0)
	result := ResolveVolcengineAFPCost(at, "agent_plan", "kimi-k2.6", 100_000, 10_000)

	if result.Matched {
		t.Fatal("kimi-k2.6 已下线，不应匹配 AFP 目录")
	}
	if result.Confidence != AFPCostConfidenceUnknown {
		t.Fatalf("confidence = %v, want unknown", result.Confidence)
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
	result := ResolveVolcengineAFPCost(at, "coding_plan", "glm-5.3", 100_000, 10_000)

	if result.Matched {
		t.Fatal("should not match coding_plan")
	}
	if result.Confidence != AFPCostConfidenceUnknown {
		t.Fatalf("confidence = %v, want unknown", result.Confidence)
	}
}

func TestResolveVolcengineAFPCost_ExpiredPromotionsAtCurrentDate(t *testing.T) {
	// 2026-09-20：本日所有历史活动均已结束；
	// glm-5.3-flash ×0.5（8/28~9/11）与 kimi-k2.7-code / deepseek-v4-pro（7/15 止）皆已过期。
	at := cst(2026, 9, 20, 12, 0, 0)
	for _, model := range []string{
		"glm-5.3", "glm-latest", "glm-5.3-flash", "deepseek-v4-pro", "kimi-k2.7-code",
	} {
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
	result := ResolveVolcengineAFPCost(at, "agent_plan", "glm-5.3", -1, 10_000)

	if !result.Matched {
		t.Fatal("expected matched (model exists)")
	}
	if result.Confidence != AFPCostConfidenceEstimated {
		t.Fatalf("confidence = %v, want estimated", result.Confidence)
	}
}

// ────────────────────────────────────────────────────────────────
// 计划文档关键样例验证（100k 输入、10k 输出）
// ────────────────────────────────────────────────────────────────

func TestResolveVolcengineAFPCost_PlanKeyExamples(t *testing.T) {
	inputTokens := 100_000
	outputTokens := 10_000

	tests := []struct {
		model    string
		at       time.Time
		expected int64
		desc     string
	}{
		{"deepseek-v4-flash", cst(2026, 7, 24, 12, 0, 0), 6, "低成本标准模型，无活动"},
		{"kimi-k2.7-code", cst(2026, 7, 10, 12, 0, 0), 14, "×0.25 活动期内（6/10 18:00~7/15）"},
		{"deepseek-v4-pro", cst(2026, 7, 24, 12, 0, 0), 61, "×0.4 活动已结束，使用基础系数"},
		{"kimi-k3", cst(2026, 7, 24, 12, 0, 0), 110, "无活动，基础成本最高"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			result := ResolveVolcengineAFPCost(tt.at, "agent_plan", tt.model, inputTokens, outputTokens)
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
// 2026-08 目录扩充模型验证（100k 输入 medium 段、10k 输出，无活动）
// ────────────────────────────────────────────────────────────────

func TestResolveVolcengineAFPCost_202608CatalogAdditions(t *testing.T) {
	at := cst(2026, 8, 18, 12, 0, 0)
	inputTokens := 100_000
	outputTokens := 10_000

	tests := []struct {
		model    string
		expected int64
	}{
		{"doubao-seed-2.0-mini", 4},   // 0.25: ceil(2.5) + ceil(0.25) = 3 + 1
		{"doubao-seed-2.0-lite", 6},   // 0.5: 5 + 1
		{"doubao-seed-2.1-turbo", 28}, // 2.5: 25 + ceil(2.5)
		{"doubao-seed-evolving", 28},  // 2.5
		{"minimax-m3", 28},            // 2.5
		{"glm-5.3", 50},               // 4.5: 45 + 5
		{"kimi-k2.7-code", 50},        // 4.5
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			result := ResolveVolcengineAFPCost(at, "agent_plan", tt.model, inputTokens, outputTokens)
			if !result.Matched {
				t.Fatalf("expected matched for %s", tt.model)
			}
			if result.PromotionApplied {
				t.Fatalf("%s: 不应命中活动", tt.model)
			}
			if result.TotalAFP != tt.expected {
				t.Fatalf("%s: TotalAFP = %d, want %d", tt.model, result.TotalAFP, tt.expected)
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
	at := cst(2026, 9, 20, 12, 0, 0)
	in, outCoeff, applied, matched, _ := AFPModelEffectiveCoefficient(at, "deepseek-v4.1-flash")
	if !matched {
		t.Fatal("expected matched")
	}
	if !applied {
		t.Fatal("expected promotion applied")
	}
	// 2.5 × 0.5 = 1.25
	if in.Float64() < 1.249 || in.Float64() > 1.251 {
		t.Fatalf("inputCoeff = %f, want ~1.25", in.Float64())
	}
	if outCoeff.Float64() < 1.249 || outCoeff.Float64() > 1.251 {
		t.Fatalf("outputCoeff = %f, want ~1.25", outCoeff.Float64())
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
