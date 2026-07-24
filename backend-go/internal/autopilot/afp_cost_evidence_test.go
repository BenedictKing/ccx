package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestCostEvidence_IsComparableWith_SameUnitScope(t *testing.T) {
	a := CostEvidence{Unit: CostUnitAFP, ScopeID: "vp_abc123", Estimated: 10}
	b := CostEvidence{Unit: CostUnitAFP, ScopeID: "vp_abc123", Estimated: 20}
	if !a.IsComparableWith(b) {
		t.Fatal("same unit + scope should be comparable")
	}
}

func TestCostEvidence_IsComparableWith_DifferentScope(t *testing.T) {
	a := CostEvidence{Unit: CostUnitAFP, ScopeID: "vp_abc123", Estimated: 10}
	b := CostEvidence{Unit: CostUnitAFP, ScopeID: "vp_def456", Estimated: 20}
	if a.IsComparableWith(b) {
		t.Fatal("different AFP scope should NOT be comparable")
	}
}

func TestCostEvidence_IsComparableWith_MixedUnit(t *testing.T) {
	a := CostEvidence{Unit: CostUnitAFP, ScopeID: "vp_abc123", Estimated: 10}
	b := CostEvidence{Unit: CostUnitUSD, ScopeID: "", Estimated: 20}
	if a.IsComparableWith(b) {
		t.Fatal("AFP and USD should NOT be comparable")
	}
}

func TestCostEvidence_IsComparableWith_USD(t *testing.T) {
	a := CostEvidence{Unit: CostUnitUSD, Estimated: 10}
	b := CostEvidence{Unit: CostUnitUSD, Estimated: 20}
	if !a.IsComparableWith(b) {
		t.Fatal("same USD unit should be comparable")
	}
}

func TestCostEvidence_IsComparableWith_Unknown(t *testing.T) {
	a := CostEvidence{Unit: CostUnitUnknown, Estimated: 10}
	b := CostEvidence{Unit: CostUnitUSD, Estimated: 20}
	if a.IsComparableWith(b) {
		t.Fatal("unknown unit should NOT be comparable")
	}
}

func TestBuildAFPRequestProfile_Basic(t *testing.T) {
	profile := &RequestProfile{
		Model:       "claude-sonnet-4-20250514",
		ChannelKind: "messages",
		EstTokens:   50000,
		ContextNeed: 50000,
	}

	afp := BuildAFPRequestProfile(profile, 8192, nil)

	if afp == nil {
		t.Fatal("expected non-nil AFPRequestProfile")
	}
	if afp.EstOutputTokens != 8192 {
		t.Fatalf("EstOutputTokens = %d, want 8192", afp.EstOutputTokens)
	}
	if afp.PricingSnapshot.InputEstimate.Tokens != 50000 {
		t.Fatalf("InputEstimate.Tokens = %d, want 50000", afp.PricingSnapshot.InputEstimate.Tokens)
	}
	if afp.PricingSnapshot.OutputBudget.Tokens != 8192 {
		t.Fatalf("OutputBudget.Tokens = %d, want 8192", afp.PricingSnapshot.OutputBudget.Tokens)
	}
	if afp.PricingSnapshot.OutputBudget.Source != TokenEstimateSourceClient {
		t.Fatalf("OutputBudget.Source = %v, want client", afp.PricingSnapshot.OutputBudget.Source)
	}
	if afp.PricingSnapshot.PolicyVersion != agentPlanAFPPolicyVersion {
		t.Fatalf("PolicyVersion = %q, want %q", afp.PricingSnapshot.PolicyVersion, agentPlanAFPPolicyVersion)
	}
}

func TestBuildAFPRequestProfile_UnknownOutputBudget(t *testing.T) {
	profile := &RequestProfile{
		Model:     "deepseek-v4-flash",
		EstTokens: 10000,
	}

	afp := BuildAFPRequestProfile(profile, 0, nil)

	if afp.PricingSnapshot.OutputBudget.Tokens != 0 {
		t.Fatalf("OutputBudget.Tokens = %d, want 0 (unknown)", afp.PricingSnapshot.OutputBudget.Tokens)
	}
	if afp.PricingSnapshot.OutputBudget.Source != TokenEstimateSourceUnknown {
		t.Fatalf("OutputBudget.Source = %v, want unknown", afp.PricingSnapshot.OutputBudget.Source)
	}
}

func TestBuildAFPRequestProfile_WithContextNeed(t *testing.T) {
	// ContextNeed > EstTokens 时应使用更大的值
	profile := &RequestProfile{
		Model:       "kimi-k3",
		EstTokens:   10000,
		ContextNeed: 100000,
	}

	afp := BuildAFPRequestProfile(profile, 4096, nil)

	if afp.PricingSnapshot.InputEstimate.Tokens != 100000 {
		t.Fatalf("InputEstimate.Tokens = %d, want 100000 (from ContextNeed)", afp.PricingSnapshot.InputEstimate.Tokens)
	}
}

func TestBuildAFPRequestProfile_NilProfile(t *testing.T) {
	afp := BuildAFPRequestProfile(nil, 0, nil)
	if afp != nil {
		t.Fatal("nil profile should return nil")
	}
}

func TestBuildAFPRequestProfile_WithScope(t *testing.T) {
	scope := &config.VolcenginePlanScope{
		ScopeID:       "vp_test123",
		Plan:          "agent_plan",
		AFPComparable: true,
	}
	profile := &RequestProfile{
		Model:     "glm-5.2",
		EstTokens: 50000,
	}

	afp := BuildAFPRequestProfile(profile, 8192, scope)

	if afp.AgentPlanScope == nil {
		t.Fatal("expected AgentPlanScope to be set")
	}
	if afp.AgentPlanScope.ScopeID != "vp_test123" {
		t.Fatalf("ScopeID = %q, want vp_test123", afp.AgentPlanScope.ScopeID)
	}
}

func TestComputeCandidateAFPCost_AgentPlan(t *testing.T) {
	scope := &config.VolcenginePlanScope{
		ScopeID:       "vp_test123",
		Plan:          "agent_plan",
		AFPComparable: true,
	}
	profile := &RequestProfile{
		Model:     "glm-5.2",
		EstTokens: 100000,
	}
	afpProfile := BuildAFPRequestProfile(profile, 10000, scope)
	// 设置评估时间为活动期内（2026-07-25 00:00:00 Asia/Shanghai）
	afpProfile.PricingSnapshot.EvaluatedAt = 1784908800

	cost := ComputeCandidateAFPCost(
		afpProfile.PricingSnapshot.EvaluatedAt,
		afpProfile,
		"glm-5.2",
		100000,
		10000,
	)

	if cost == nil {
		t.Fatal("expected non-nil cost for agent_plan")
	}
	if cost.Evidence.Unit != CostUnitAFP {
		t.Fatalf("Unit = %v, want AFP", cost.Evidence.Unit)
	}
	if cost.Evidence.ScopeID != "vp_test123" {
		t.Fatalf("ScopeID = %q, want vp_test123", cost.Evidence.ScopeID)
	}
	if cost.Result.TotalAFP <= 0 {
		t.Fatalf("TotalAFP = %d, want > 0", cost.Result.TotalAFP)
	}
	if !cost.Result.PromotionApplied {
		t.Fatal("expected promotion applied for glm-5.2 at 2026-07-25")
	}
}

func TestComputeCandidateAFPCost_NonComparable(t *testing.T) {
	scope := &config.VolcenginePlanScope{
		ScopeID:       "vp_test123",
		Plan:          "agent_plan",
		AFPComparable: false,
	}
	profile := &RequestProfile{Model: "glm-5.2", EstTokens: 100000}
	afpProfile := BuildAFPRequestProfile(profile, 10000, scope)

	cost := ComputeCandidateAFPCost(1753420800, afpProfile, "glm-5.2", 100000, 10000)
	if cost != nil {
		t.Fatal("expected nil cost for non-comparable scope")
	}
}

func TestComputeCandidateAFPCost_NilProfile(t *testing.T) {
	cost := ComputeCandidateAFPCost(0, nil, "glm-5.2", 0, 0)
	if cost != nil {
		t.Fatal("expected nil cost for nil profile")
	}
}

func TestComputeCandidateAFPCost_UnknownModel(t *testing.T) {
	scope := &config.VolcenginePlanScope{
		ScopeID:       "vp_test123",
		Plan:          "agent_plan",
		AFPComparable: true,
	}
	profile := &RequestProfile{Model: "gpt-5", EstTokens: 100000}
	afpProfile := BuildAFPRequestProfile(profile, 10000, scope)

	cost := ComputeCandidateAFPCost(1753420800, afpProfile, "gpt-5", 100000, 10000)
	if cost != nil {
		t.Fatal("expected nil cost for unknown model")
	}
}

func TestCostConfidenceFromAFP(t *testing.T) {
	tests := []struct {
		input    config.AFPCostConfidence
		expected CostConfidence
	}{
		{config.AFPCostConfidenceExact, CostConfidenceExact},
		{config.AFPCostConfidenceEstimated, CostConfidenceEstimated},
		{config.AFPCostConfidenceUnknown, CostConfidenceUnknown},
	}
	for _, tt := range tests {
		got := costConfidenceFromAFP(tt.input)
		if got != tt.expected {
			t.Fatalf("costConfidenceFromAFP(%v) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}
