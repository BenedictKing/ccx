package config

import (
	"math"
	"testing"
	"time"
)

func TestEvaluateAPIKeyMultiplierEligibility(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	one := 1.0
	two := 2.0
	zero := 0.0
	future := now.Add(time.Hour)
	past := now.Add(-time.Hour)

	tests := []struct {
		name string
		cfg  APIKeyConfig
		want MultiplierEligibility
	}{
		{name: "legacy config", cfg: APIKeyConfig{}, want: MultiplierEligibility{Eligible: true, Reason: MultiplierEligibilityReasonOK}},
		{name: "missing multiplier", cfg: APIKeyConfig{MaxGroupMultiplier: &one}, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonInvalidMultiplier}},
		{name: "missing max", cfg: APIKeyConfig{GroupMultiplier: &one}, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonInvalidMaxMultiplier}},
		{name: "nan multiplier", cfg: APIKeyConfig{GroupMultiplier: ptrFloat64(math.NaN()), MaxGroupMultiplier: &one}, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonInvalidMultiplier}},
		{name: "nan max", cfg: APIKeyConfig{GroupMultiplier: &one, MaxGroupMultiplier: ptrFloat64(math.NaN())}, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonInvalidMaxMultiplier}},
		{name: "negative max", cfg: APIKeyConfig{GroupMultiplier: &one, MaxGroupMultiplier: ptrFloat64(-1)}, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonInvalidMaxMultiplier}},
		{name: "manual source", cfg: APIKeyConfig{GroupMultiplier: &one, MaxGroupMultiplier: &two, MultiplierSource: "manual"}, want: MultiplierEligibility{Eligible: true, Reason: MultiplierEligibilityReasonOK}},
		{name: "provider source", cfg: APIKeyConfig{GroupMultiplier: &one, MaxGroupMultiplier: &two, MultiplierSource: "provider", MultiplierExpiresAt: &past}, want: MultiplierEligibility{Eligible: true, Reason: MultiplierEligibilityReasonOK}},
		{name: "over group limit", cfg: APIKeyConfig{GroupMultiplier: &two, MaxGroupMultiplier: &one, MultiplierSource: "manual"}, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonOverGroupLimit}},
		{name: "fresh new api", cfg: APIKeyConfig{GroupMultiplier: &one, MaxGroupMultiplier: &two, MultiplierSource: "new_api", MultiplierSyncStatus: "fresh", SourceSubscriptionUID: "sub", SourceRemoteTokenID: 1, MultiplierExpiresAt: &future}, want: MultiplierEligibility{Eligible: true, Reason: MultiplierEligibilityReasonOK, Status: "fresh"}},
		{name: "stale new api", cfg: APIKeyConfig{GroupMultiplier: &one, MaxGroupMultiplier: &two, MultiplierSource: "new_api", MultiplierSyncStatus: "stale", SourceSubscriptionUID: "sub", SourceRemoteTokenID: 1, MultiplierExpiresAt: &future}, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonMultiplierStale, Status: "stale"}},
		{name: "expired fresh new api", cfg: APIKeyConfig{GroupMultiplier: &one, MaxGroupMultiplier: &two, MultiplierSource: "new_api", MultiplierSyncStatus: "fresh", SourceSubscriptionUID: "sub", SourceRemoteTokenID: 1, MultiplierExpiresAt: &past}, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonMultiplierStale, Status: "fresh"}},
		{name: "missing ownership new api", cfg: APIKeyConfig{GroupMultiplier: &one, MaxGroupMultiplier: &two, MultiplierSource: "new_api", MultiplierSyncStatus: "fresh", SourceRemoteTokenID: 1, MultiplierExpiresAt: &future}, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonRelinkRequired, Status: "fresh"}},
		{name: "sync error new api", cfg: APIKeyConfig{GroupMultiplier: &one, MaxGroupMultiplier: &two, MultiplierSource: "new_api", MultiplierSyncStatus: "sync_error", SourceSubscriptionUID: "sub", SourceRemoteTokenID: 1}, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonSyncError, Status: "sync_error"}},
		{name: "relink new api", cfg: APIKeyConfig{GroupMultiplier: &one, MaxGroupMultiplier: &two, MultiplierSource: "new_api", MultiplierSyncStatus: "relink_required", SourceSubscriptionUID: "sub", SourceRemoteTokenID: 1}, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonRelinkRequired, Status: "relink_required"}},
		{name: "unknown source", cfg: APIKeyConfig{GroupMultiplier: &one, MaxGroupMultiplier: &two, MultiplierSource: "mystery"}, want: MultiplierEligibility{Reason: MultiplierEligibilityReasonUnknownSource}},
		{name: "zero multiplier opportunistic", cfg: APIKeyConfig{GroupMultiplier: &zero, MaxGroupMultiplier: &zero, MultiplierSource: "manual", ConsumptionPolicy: KeyConsumptionOpportunistic}, want: MultiplierEligibility{Eligible: true, Reason: MultiplierEligibilityReasonOK}},
		{name: "zero multiplier normal", cfg: APIKeyConfig{GroupMultiplier: &zero, MaxGroupMultiplier: &zero, MultiplierSource: "manual", ConsumptionPolicy: KeyConsumptionNormal}, want: MultiplierEligibility{Eligible: true, Reason: MultiplierEligibilityReasonOK}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateAPIKeyMultiplierEligibility(tt.cfg, now)
			if got.Eligible != tt.want.Eligible || got.Reason != tt.want.Reason || got.Status != tt.want.Status {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestGetNextAPIKeySkipsKeysByUnifiedMultiplierEligibility(t *testing.T) {
	safeRatio, unsafeRatio, limit := 1.0, 2.0, 1.0
	cm := &ConfigManager{}
	future := time.Now().Add(time.Hour)
	upstream := &UpstreamConfig{
		Name:    "newapi",
		APIKeys: []string{"unsafe", "fresh", "legacy", "stale"},
		APIKeyConfigs: []APIKeyConfig{
			{Key: "unsafe", GroupMultiplier: &unsafeRatio, MaxGroupMultiplier: &limit, MultiplierSource: "manual"},
			{Key: "fresh", GroupMultiplier: &safeRatio, MaxGroupMultiplier: &limit, MultiplierSource: "new_api", MultiplierSyncStatus: "fresh", SourceSubscriptionUID: "sub", SourceRemoteTokenID: 1, MultiplierExpiresAt: &future},
			{Key: "legacy"},
			{Key: "stale", GroupMultiplier: &safeRatio, MaxGroupMultiplier: &limit, MultiplierSource: "new_api", MultiplierSyncStatus: "stale", SourceSubscriptionUID: "sub", SourceRemoteTokenID: 2, MultiplierExpiresAt: &future},
		},
	}

	key, err := cm.GetNextAPIKey(upstream, nil, "Responses")
	if err != nil || key != "fresh" {
		t.Fatalf("GetNextAPIKey() = %q, %v; want fresh key", key, err)
	}

	key, err = cm.GetNextAPIKey(upstream, map[string]bool{"fresh": true}, "Responses")
	if err != nil || key != "legacy" {
		t.Fatalf("GetNextAPIKey() = %q, %v; want legacy key", key, err)
	}
}

func TestGetAdminAPIKeySkipsDisabledKeyByUnifiedMultiplierEligibility(t *testing.T) {
	unsafeRatio, limit := 2.0, 1.0
	cm := &ConfigManager{}
	upstream := &UpstreamConfig{
		Name: "newapi",
		DisabledAPIKeys: []DisabledKeyInfo{{
			Key: "unsafe",
			Config: &APIKeyConfig{
				Key:                "unsafe",
				GroupMultiplier:    &unsafeRatio,
				MaxGroupMultiplier: &limit,
				MultiplierSource:   "manual",
			},
		}},
	}

	if _, _, err := cm.GetAdminAPIKey(upstream, nil, "Responses"); err == nil {
		t.Fatal("GetAdminAPIKey() must not borrow an over-limit key")
	}
}

func ptrFloat64(value float64) *float64 {
	return &value
}
