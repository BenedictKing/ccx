package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// TestNormalizeSavingsScoreGrouped_AFPScopeGroup verifies that AFP candidates
// within the same scope normalize independently by TotalAFP: cheapest gets 1.0,
// most expensive gets 0.0. This is the core lever making GLM-5.2 (×0.25 promo)
// outrank DeepSeek-V4-Pro (no promo) on the volcengine Agent Plan.
func TestNormalizeSavingsScoreGrouped_AFPScopeGroup(t *testing.T) {
	scope := "vp_shared"
	entries := []channelScoreEntry{
		{
			ChannelUID:    "ch_glm",
			EstimatedCost: 7.34, // USD list price, ignored because AFPCost set
			AFPCost: &CandidateAFPCost{
				Result: config.AFPCostResult{TotalAFP: 14},
				Evidence: CostEvidence{
					Unit:      CostUnitAFP,
					ScopeID:   scope,
					Estimated: 14,
				},
			},
		},
		{
			ChannelUID:    "ch_dsv4",
			EstimatedCost: 1.77,
			AFPCost: &CandidateAFPCost{
				Result: config.AFPCostResult{TotalAFP: 61},
				Evidence: CostEvidence{
					Unit:      CostUnitAFP,
					ScopeID:   scope,
					Estimated: 61,
				},
			},
		},
	}

	savings := normalizeSavingsScoreGrouped(entries)
	if got := savings["ch_glm"]; got != 1.0 {
		t.Fatalf("glm (cheapest AFP) SavingsScore = %v, want 1.0", got)
	}
	if got := savings["ch_dsv4"]; got != 0.0 {
		t.Fatalf("deepseek (most expensive AFP) SavingsScore = %v, want 0.0", got)
	}
}

// TestNormalizeSavingsScoreGrouped_USDAndAFPIndependent verifies AFP and USD
// candidates form separate comparability groups: the cheapest in each group
// gets 1.0, so a USD channel is not penalized for being more expensive than an
// AFP channel it cannot be compared against.
func TestNormalizeSavingsScoreGrouped_USDAndAFPIndependent(t *testing.T) {
	entries := []channelScoreEntry{
		{
			ChannelUID:    "ch_afp_cheap",
			EstimatedCost: 100, // ignored, AFPCost present
			AFPCost: &CandidateAFPCost{
				Result:   config.AFPCostResult{TotalAFP: 10},
				Evidence: CostEvidence{Unit: CostUnitAFP, ScopeID: "vp_a", Estimated: 10},
			},
		},
		{
			ChannelUID:    "ch_usd_cheap",
			EstimatedCost: 2.0,
		},
		{
			ChannelUID:    "ch_usd_expensive",
			EstimatedCost: 8.0,
		},
	}

	savings := normalizeSavingsScoreGrouped(entries)
	// Sole AFP candidate has no peers in its scope → neutral 0.5 (matches
	// NormalizeSavingsScore single/all-equal semantics). The point of this test
	// is that USD candidates normalize independently of the AFP candidate's cost.
	if got := savings["ch_afp_cheap"]; got != 0.5 {
		t.Fatalf("sole AFP candidate SavingsScore = %v, want 0.5 (no peers)", got)
	}
	if got := savings["ch_usd_cheap"]; got != 1.0 {
		t.Fatalf("cheapest USD SavingsScore = %v, want 1.0", got)
	}
	if got := savings["ch_usd_expensive"]; got != 0.0 {
		t.Fatalf("most expensive USD SavingsScore = %v, want 0.0", got)
	}
}

// TestNormalizeSavingsScoreGrouped_NoCostNeutral verifies candidates without
// any cost evidence (unknown pricing, EstimatedCost<0) get the neutral 0.5.
func TestNormalizeSavingsScoreGrouped_NoCostNeutral(t *testing.T) {
	entries := []channelScoreEntry{
		{ChannelUID: "ch_unknown", EstimatedCost: -1},
		{ChannelUID: "ch_known", EstimatedCost: 3.0},
	}
	savings := normalizeSavingsScoreGrouped(entries)
	if got := savings["ch_unknown"]; got != 0.5 {
		t.Fatalf("unknown-cost candidate SavingsScore = %v, want 0.5", got)
	}
	if got := savings["ch_known"]; got != 0.5 {
		t.Fatalf("sole known USD candidate SavingsScore = %v, want 0.5 (no peers)", got)
	}
}

// TestNormalizeSavingsScoreGrouped_SameAFPSameCost verifies that when all AFP
// candidates in a scope share the same TotalAFP, they all get the neutral 0.5
// (no cost differentiation), matching NormalizeSavingsScore semantics.
func TestNormalizeSavingsScoreGrouped_SameAFPSameCost(t *testing.T) {
	entries := []channelScoreEntry{
		{
			ChannelUID: "ch_a",
			AFPCost: &CandidateAFPCost{
				Result:   config.AFPCostResult{TotalAFP: 20},
				Evidence: CostEvidence{Unit: CostUnitAFP, ScopeID: "vp_x", Estimated: 20},
			},
		},
		{
			ChannelUID: "ch_b",
			AFPCost: &CandidateAFPCost{
				Result:   config.AFPCostResult{TotalAFP: 20},
				Evidence: CostEvidence{Unit: CostUnitAFP, ScopeID: "vp_x", Estimated: 20},
			},
		},
	}
	savings := normalizeSavingsScoreGrouped(entries)
	if got := savings["ch_a"]; got != 0.5 {
		t.Fatalf("equal-cost AFP candidate a SavingsScore = %v, want 0.5", got)
	}
	if got := savings["ch_b"]; got != 0.5 {
		t.Fatalf("equal-cost AFP candidate b SavingsScore = %v, want 0.5", got)
	}
}

// TestNormalizeSavingsScoreGrouped_DifferentScopes verifies AFP candidates in
// different scopes normalize within their own scope, never across scopes.
func TestNormalizeSavingsScoreGrouped_DifferentScopes(t *testing.T) {
	entries := []channelScoreEntry{
		{
			ChannelUID: "ch_scope1_cheap",
			AFPCost: &CandidateAFPCost{
				Result:   config.AFPCostResult{TotalAFP: 5},
				Evidence: CostEvidence{Unit: CostUnitAFP, ScopeID: "vp_1", Estimated: 5},
			},
		},
		{
			ChannelUID: "ch_scope2_cheap",
			AFPCost: &CandidateAFPCost{
				Result:   config.AFPCostResult{TotalAFP: 50},
				Evidence: CostEvidence{Unit: CostUnitAFP, ScopeID: "vp_2", Estimated: 50},
			},
		},
		{
			ChannelUID: "ch_scope2_expensive",
			AFPCost: &CandidateAFPCost{
				Result:   config.AFPCostResult{TotalAFP: 100},
				Evidence: CostEvidence{Unit: CostUnitAFP, ScopeID: "vp_2", Estimated: 100},
			},
		},
	}
	savings := normalizeSavingsScoreGrouped(entries)
	if got := savings["ch_scope1_cheap"]; got != 0.5 {
		t.Fatalf("sole scope1 candidate SavingsScore = %v, want 0.5", got)
	}
	if got := savings["ch_scope2_cheap"]; got != 1.0 {
		t.Fatalf("cheapest scope2 SavingsScore = %v, want 1.0", got)
	}
	if got := savings["ch_scope2_expensive"]; got != 0.0 {
		t.Fatalf("most expensive scope2 SavingsScore = %v, want 0.0", got)
	}
}
