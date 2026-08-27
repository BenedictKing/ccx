package config

import (
	"testing"
)

func TestNormalizeAPIKeyConfigsPreservesIdentityOnlyEntries(t *testing.T) {
	cfgs := normalizeAPIKeyConfigs([]string{"plain"}, []APIKeyConfig{
		{Key: "plain"},
		{KeyUID: "kid-1", CredentialUID: "cred-1"},
		{CredentialUID: "cred-2"},
	})

	if len(cfgs) != 3 {
		t.Fatalf("expected 3 configs, got %d", len(cfgs))
	}
	if cfgs[1].KeyUID != "kid-1" || cfgs[1].CredentialUID != "cred-1" || cfgs[1].Key != "" {
		t.Fatalf("expected keyUid-only skeleton to survive, got %+v", cfgs[1])
	}
	if cfgs[2].CredentialUID != "cred-2" || cfgs[2].Key != "" {
		t.Fatalf("expected credential-only skeleton to survive, got %+v", cfgs[2])
	}
}

func TestNormalizeAPIKeyConfigsForViewKeepsIdentitySkeletons(t *testing.T) {
	upstream := UpstreamConfig{
		APIKeys: []string{"plain"},
		APIKeyConfigs: []APIKeyConfig{
			{Key: "plain"},
			{KeyUID: "kid-1", CredentialUID: "cred-1"},
		},
	}

	got := NormalizeAPIKeyConfigsForView(upstream)
	if len(got) != 1 {
		t.Fatalf("expected 1 effective config, got %d", len(got))
	}
	if got[0].KeyUID != "kid-1" || got[0].CredentialUID != "cred-1" || got[0].Key != "" {
		t.Fatalf("expected identity skeleton in view, got %+v", got[0])
	}
}

func TestMergeAndNormalizeAPIKeyConfigsPreservesServerIdentityFields(t *testing.T) {
	existing := []APIKeyConfig{{Key: "old-key", KeyUID: "kid-1", CredentialUID: "cred-1", QuotaGroup: "team-a"}}
	incoming := []APIKeyConfig{{Key: "new-key", CredentialUID: "cred-1", QuotaGroup: "team-b"}}
	got := mergeAndNormalizeAPIKeyConfigs([]string{"new-key"}, existing, incoming)
	if len(got) != 1 {
		t.Fatalf("expected 1 config, got %d", len(got))
	}
	if got[0].KeyUID != "kid-1" || got[0].CredentialUID != "cred-1" {
		t.Fatalf("expected identity fields preserved, got %+v", got[0])
	}
	if got[0].QuotaGroup != "team-b" {
		t.Fatalf("expected client fields to override, got %+v", got[0])
	}
}

func TestMergeAndNormalizeAPIKeyConfigsPreservesNewApiManagedIdentity(t *testing.T) {
	existing := []APIKeyConfig{{Key: "old-key", CredentialUID: "cred-1", MultiplierSource: "new_api", SourceSubscriptionUID: "newapi-ch-1", SourceRemoteTokenID: 42}}
	incoming := []APIKeyConfig{{Key: "new-key", CredentialUID: "cred-1"}}
	got := mergeAndNormalizeAPIKeyConfigs([]string{"new-key"}, existing, incoming)
	if len(got) != 1 {
		t.Fatalf("expected 1 config, got %d", len(got))
	}
	if got[0].SourceSubscriptionUID != "newapi-ch-1" || got[0].SourceRemoteTokenID != 42 {
		t.Fatalf("expected new_api managed identity preserved, got %+v", got[0])
	}
}

func TestMergeAndNormalizeAPIKeyConfigsKeepsExplicitIdentityClearForUnmanagedKey(t *testing.T) {
	existing := []APIKeyConfig{{Key: "k", MultiplierSource: "manual", SourceSubscriptionUID: "newapi-ch-1", SourceRemoteTokenID: 42}}
	incoming := []APIKeyConfig{{Key: "k", MultiplierSource: "manual"}}
	got := mergeAndNormalizeAPIKeyConfigs([]string{"k"}, existing, incoming)
	if len(got) != 1 {
		t.Fatalf("expected 1 config, got %d", len(got))
	}
	if got[0].SourceSubscriptionUID != "" || got[0].SourceRemoteTokenID != 0 {
		t.Fatalf("expected explicit identity clear respected for unmanaged key, got %+v", got[0])
	}
}

func TestMergeAndNormalizeAPIKeyConfigsMatchesByKeyUIDBeforeCredentialOrKey(t *testing.T) {
	existing := []APIKeyConfig{
		{Key: "old-a", KeyUID: "kid-1", CredentialUID: "cred-shared", QuotaGroup: "group-a"},
		{Key: "old-b", KeyUID: "kid-2", CredentialUID: "cred-shared", QuotaGroup: "group-b"},
	}
	incoming := []APIKeyConfig{{Key: "new-b", KeyUID: "kid-2", QuotaGroup: "group-new"}}
	got := mergeAndNormalizeAPIKeyConfigs([]string{"new-b"}, existing, incoming)
	if len(got) != 1 {
		t.Fatalf("expected 1 config, got %d", len(got))
	}
	if got[0].CredentialUID != "cred-shared" || got[0].KeyUID != "kid-2" || got[0].QuotaGroup != "group-new" {
		t.Fatalf("expected keyUid match to win, got %+v", got[0])
	}
}

func TestApplyAPIKeyConfigUpdateMergesIdentityAcrossProtocols(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{name: "messages", key: "m-key"},
		{name: "chat", key: "c-key"},
		{name: "responses", key: "r-key"},
		{name: "gemini", key: "g-key"},
		{name: "images", key: "i-key"},
		{name: "vectors", key: "v-key"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &UpstreamConfig{
				APIKeys:       []string{tt.key},
				APIKeyConfigs: []APIKeyConfig{{Key: "old-" + tt.key, KeyUID: "kid-" + tt.name, CredentialUID: "cred-" + tt.name, ConsumptionPolicy: KeyConsumptionOpportunistic}},
			}
			applyAPIKeyConfigUpdate(upstream, UpstreamUpdate{
				APIKeyConfigs: []APIKeyConfig{{Key: tt.key, CredentialUID: "cred-" + tt.name}},
			})
			if len(upstream.APIKeyConfigs) != 1 {
				t.Fatalf("expected 1 config, got %d", len(upstream.APIKeyConfigs))
			}
			got := upstream.APIKeyConfigs[0]
			if got.KeyUID != "kid-"+tt.name || got.CredentialUID != "cred-"+tt.name || got.Key != tt.key {
				t.Fatalf("identity merge failed: %+v", got)
			}
			if got.ConsumptionPolicy != KeyConsumptionOpportunistic {
				t.Fatalf("expected ConsumptionPolicy preserved, got %q", got.ConsumptionPolicy)
			}
		})
	}
}

func TestMergeAPIKeyConfigPreservesConsumptionPolicy(t *testing.T) {
	existing := APIKeyConfig{Key: "k1", KeyUID: "kid-1", ConsumptionPolicy: KeyConsumptionOpportunistic}
	// 上游同步未携带策略时应保留本地意图。
	merged := mergeAPIKeyConfig(&existing,
		APIKeyConfig{Key: "k1", KeyUID: "kid-1"})
	if merged.ConsumptionPolicy != KeyConsumptionOpportunistic {
		t.Fatalf("expected opportunistic preserved, got %q", merged.ConsumptionPolicy)
	}
	// 上游显式设置时应覆盖。
	merged = mergeAPIKeyConfig(
		&existing,
		APIKeyConfig{Key: "k1", KeyUID: "kid-1", ConsumptionPolicy: KeyConsumptionNormal})
	if merged.ConsumptionPolicy != KeyConsumptionNormal {
		t.Fatalf("expected normal override, got %q", merged.ConsumptionPolicy)
	}
}

func TestMarkAutopilotPresenceDetectsExplicitEmptyExchangeQuotes(t *testing.T) {
	cfg := DefaultAutopilotRoutingConfig()
	cfg.CostOptimization.ExchangeRateQuotesConfigured = false
	raw := []byte(`{"costOptimization":{"exchangeRateQuotes":[]}}`)
	markAutopilotPresence(&cfg, raw)
	if !cfg.CostOptimization.ExchangeRateQuotesConfigured {
		t.Fatal("expected explicit exchangeRateQuotes presence to be tracked")
	}
}

func TestAutopilotValidatePreservesExplicitEmptyExchangeQuotes(t *testing.T) {
	cfg := DefaultAutopilotRoutingConfig()
	cfg.CostOptimization.ExchangeRateQuotes = nil
	cfg.CostOptimization.ExchangeRateQuotesConfigured = true
	cfg.Validate()
	if cfg.CostOptimization.ExchangeRateQuotes != nil {
		t.Fatalf("expected explicit empty quotes to stay empty, got %+v", cfg.CostOptimization.ExchangeRateQuotes)
	}
}

func TestAutopilotValidateInjectsDefaultExchangeQuotesWhenMissing(t *testing.T) {
	cfg := DefaultAutopilotRoutingConfig()
	cfg.CostOptimization.ExchangeRateQuotes = nil
	cfg.CostOptimization.ExchangeRateQuotesConfigured = false
	cfg.Validate()
	quotes := cfg.CostOptimization.ExchangeRateQuotes
	if len(quotes) != 2 {
		t.Fatalf("expected 2 default quotes when missing, got %+v", quotes)
	}
	if quotes[0].SourceAmount != 1 || quotes[0].SourceUnit != "USD" || quotes[0].TargetAmount != 7 || quotes[0].TargetUnit != "CNY" {
		t.Fatalf("unexpected USD/CNY default quote: %+v", quotes[0])
	}
	if quotes[1].SourceAmount != 500 || quotes[1].SourceUnit != "LDC" || quotes[1].TargetAmount != 10 || quotes[1].TargetUnit != "CNY" {
		t.Fatalf("unexpected LDC/CNY default quote: %+v", quotes[1])
	}
}
