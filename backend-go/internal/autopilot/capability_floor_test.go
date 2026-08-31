package autopilot

import (
	"testing"
)

// ── CapabilityFloorReasons 测试 ──

func TestCapabilityFloorReasons(t *testing.T) {
	tests := []struct {
		name     string
		caps     CandidateCapabilities
		profile  *RequestProfile
		wantLen  int // 期望 reason 数量
		wantMsgs []string
	}{
		// -- 全部通过：无需能力 --
		{
			name:    "无需求且候选无能力 -> 全部通过",
			caps:    CandidateCapabilities{},
			profile: &RequestProfile{},
			wantLen: 0,
		},
		// -- 工具调用 --
		{
			name:    "需要工具调用且候选支持 -> 通过",
			caps:    CandidateCapabilities{SupportsToolCalls: true},
			profile: &RequestProfile{ToolUseNeed: true},
			wantLen: 0,
		},
		{
			name:     "需要工具调用但候选不支持 -> 不满足",
			caps:     CandidateCapabilities{SupportsToolCalls: false},
			profile:  &RequestProfile{ToolUseNeed: true},
			wantLen:  1,
			wantMsgs: []string{"工具调用能力不满足"},
		},
		// -- 推理 --
		{
			name:    "需要推理且候选支持 -> 通过",
			caps:    CandidateCapabilities{SupportsReasoning: true},
			profile: &RequestProfile{ReasoningNeed: true},
			wantLen: 0,
		},
		{
			name:     "需要推理但候选不支持 -> 不满足",
			caps:     CandidateCapabilities{SupportsReasoning: false},
			profile:  &RequestProfile{ReasoningNeed: true},
			wantLen:  1,
			wantMsgs: []string{"推理能力不满足"},
		},
		// -- 上下文窗口 --
		{
			name:    "ContextNeed=0 -> 跳过上下文检查",
			caps:    CandidateCapabilities{ContextWindowTokens: 1000},
			profile: &RequestProfile{ContextNeed: 0},
			wantLen: 0,
		},
		{
			name:    "caps.ContextWindowTokens=0(未知) -> 跳过上下文检查",
			caps:    CandidateCapabilities{ContextWindowTokens: 0},
			profile: &RequestProfile{ContextNeed: 100000},
			wantLen: 0,
		},
		{
			name:    "上下文窗口刚好满足(相等) -> 通过",
			caps:    CandidateCapabilities{ContextWindowTokens: 100000},
			profile: &RequestProfile{ContextNeed: 100000},
			wantLen: 0,
		},
		{
			name:     "上下文窗口不足 -> 不满足",
			caps:     CandidateCapabilities{ContextWindowTokens: 50000},
			profile:  &RequestProfile{ContextNeed: 100000},
			wantLen:  1,
			wantMsgs: []string{"上下文窗口不满足"},
		},
		{
			name:    "上下文窗口充足 -> 通过",
			caps:    CandidateCapabilities{ContextWindowTokens: 200000},
			profile: &RequestProfile{ContextNeed: 100000},
			wantLen: 0,
		},
		// -- 多条同时触发 --
		{
			name: "工具+推理+上下文三项全不满足 -> 三条 reason",
			caps: CandidateCapabilities{
				SupportsToolCalls:   false,
				SupportsReasoning:   false,
				ContextWindowTokens: 32000,
			},
			profile: &RequestProfile{
				ToolUseNeed:   true,
				ReasoningNeed: true,
				ContextNeed:   128000,
			},
			wantLen:  3,
			wantMsgs: []string{"工具调用能力不满足", "推理能力不满足", "上下文窗口不满足"},
		},
		{
			name: "工具+推理两项不满足",
			caps: CandidateCapabilities{
				SupportsToolCalls:   false,
				SupportsReasoning:   false,
				ContextWindowTokens: 200000,
			},
			profile: &RequestProfile{
				ToolUseNeed:   true,
				ReasoningNeed: true,
				ContextNeed:   128000,
			},
			wantLen:  2,
			wantMsgs: []string{"工具调用能力不满足", "推理能力不满足"},
		},
		{
			name: "部分满足(推理通过,工具+上下文不满足) -> 两条 reason",
			caps: CandidateCapabilities{
				SupportsToolCalls:   false,
				SupportsReasoning:   true,
				ContextWindowTokens: 50000,
			},
			profile: &RequestProfile{
				ToolUseNeed:   true,
				ReasoningNeed: true,
				ContextNeed:   100000,
			},
			wantLen:  2,
			wantMsgs: []string{"工具调用能力不满足", "上下文窗口不满足"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CapabilityFloorReasons(tt.caps, tt.profile)
			if len(got) != tt.wantLen {
				t.Errorf("reason count = %d, want %d; got reasons = %v", len(got), tt.wantLen, got)
				return
			}
			for i, msg := range tt.wantMsgs {
				if i < len(got) && got[i] != msg {
					t.Errorf("reason[%d] = %q, want %q", i, got[i], msg)
				}
			}
		})
	}
}

// ── MinQualityTierReasons 测试 ──

func TestMinQualityTierReasons(t *testing.T) {
	tests := []struct {
		name      string
		candidate QualityTier
		required  QualityTier
		wantLen   int
	}{
		// -- 边界相等（刚好满足） --
		{
			name:      "premium == premium -> 满足",
			candidate: QualityTierPremium,
			required:  QualityTierPremium,
			wantLen:   0,
		},
		{
			name:      "high == high -> 满足",
			candidate: QualityTierHigh,
			required:  QualityTierHigh,
			wantLen:   0,
		},
		{
			name:      "normal == normal -> 满足",
			candidate: QualityTierNormal,
			required:  QualityTierNormal,
			wantLen:   0,
		},
		{
			name:      "low == low -> 满足",
			candidate: QualityTierLow,
			required:  QualityTierLow,
			wantLen:   0,
		},
		// -- 候选高于要求 --
		{
			name:      "premium >= high -> 满足",
			candidate: QualityTierPremium,
			required:  QualityTierHigh,
			wantLen:   0,
		},
		{
			name:      "premium >= normal -> 满足",
			candidate: QualityTierPremium,
			required:  QualityTierNormal,
			wantLen:   0,
		},
		{
			name:      "high >= normal -> 满足",
			candidate: QualityTierHigh,
			required:  QualityTierNormal,
			wantLen:   0,
		},
		{
			name:      "high >= low -> 满足",
			candidate: QualityTierHigh,
			required:  QualityTierLow,
			wantLen:   0,
		},
		// -- 候选低于要求 --
		{
			name:      "low < premium -> 不满足",
			candidate: QualityTierLow,
			required:  QualityTierPremium,
			wantLen:   1,
		},
		{
			name:      "normal < premium -> 不满足",
			candidate: QualityTierNormal,
			required:  QualityTierPremium,
			wantLen:   1,
		},
		{
			name:      "low < high -> 不满足",
			candidate: QualityTierLow,
			required:  QualityTierHigh,
			wantLen:   1,
		},
		{
			name:      "normal < high -> 不满足",
			candidate: QualityTierNormal,
			required:  QualityTierHigh,
			wantLen:   1,
		},
		{
			name:      "low < normal -> 不满足",
			candidate: QualityTierLow,
			required:  QualityTierNormal,
			wantLen:   1,
		},
		// -- 未知/空值边界（qualityTierRank: unknown=0, low=0, normal=1, high=2, premium=3） --
		{
			name:      "未知候选 < premium -> 不满足(rank 0 < 3)",
			candidate: QualityTier(""),
			required:  QualityTierPremium,
			wantLen:   1,
		},
		{
			name:      "要求为空(未知) -> 候选总是满足(rank X >= 0)",
			candidate: QualityTierLow,
			required:  QualityTier(""),
			wantLen:   0,
		},
		{
			name:      "双方都为空 -> 满足(rank均为0,相等)",
			candidate: QualityTier(""),
			required:  QualityTier(""),
			wantLen:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MinQualityTierReasons(tt.candidate, tt.required)
			if len(got) != tt.wantLen {
				t.Errorf("reason count = %d, want %d; got reasons = %v", len(got), tt.wantLen, got)
			}
		})
	}
}

// ── 安全分类硬约束 ──

func TestCapabilityFloorReasonsSeverityClass(t *testing.T) {
	// 分类请求 + 候选支持 -> 通过
	reasons := CapabilityFloorReasons(CandidateCapabilities{SupportsSeverityClass: true}, &RequestProfile{SeverityClassNeed: true})
	if len(reasons) != 0 {
		t.Fatalf("支持时应通过，got %v", reasons)
	}
	// 分类请求 + 候选被实测收紧为不支持 -> 不满足
	reasons = CapabilityFloorReasons(CandidateCapabilities{SupportsSeverityClass: false}, &RequestProfile{SeverityClassNeed: true})
	if len(reasons) != 1 || reasons[0] != "安全分类格式能力不满足" {
		t.Fatalf("不支持时应返回唯一原因，got %v", reasons)
	}
	// 非分类请求 + 候选不支持 -> 不影响（零记忆零影响）
	reasons = CapabilityFloorReasons(CandidateCapabilities{SupportsSeverityClass: false}, &RequestProfile{})
	if len(reasons) != 0 {
		t.Fatalf("非分类请求不应受该约束影响，got %v", reasons)
	}
}
