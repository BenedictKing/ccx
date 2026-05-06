package config

import (
	"testing"
	"time"
)

func TestMarkKeyAsFailedInitialAndAfterFixedDuration(t *testing.T) {
	cm := &ConfigManager{
		failedKeysCache:     make(map[string]*FailedKey),
		keyBackoffDurations: []time.Duration{time.Minute, 2 * time.Minute},
	}

	cm.MarkKeyAsFailed("test-key", "Messages")
	cacheKey := failedKeyCacheKey("Messages", "test-key")
	failure := cm.failedKeysCache[cacheKey]
	if failure == nil {
		t.Fatal("expected failed key to exist after first failure")
	}
	if failure.FailureCount != 1 {
		t.Fatalf("FailureCount = %d, want 1", failure.FailureCount)
	}
	if failure.FixedDuration != 0 {
		t.Fatalf("FixedDuration = %v, want 0", failure.FixedDuration)
	}

	failure.FixedDuration = 10 * time.Minute
	cm.MarkKeyAsFailed("test-key", "Messages")
	failure = cm.failedKeysCache[cacheKey]
	if failure.FailureCount != 2 {
		t.Fatalf("FailureCount = %d, want 2", failure.FailureCount)
	}
	if failure.FixedDuration != 0 {
		t.Fatalf("FixedDuration = %v, want 0 after normal failure", failure.FixedDuration)
	}
}

func TestIsLegacyClaudeDefaultFailoverRules(t *testing.T) {
	t.Run("legacy rules should match", func(t *testing.T) {
		legacy := []FailoverRule{
			{
				Action:          "cooldown",
				StatusCodes:     []int{429},
				DurationMinutes: 60,
			},
			{
				Action:      "blacklist",
				StatusCodes: []int{400, 401},
			},
		}
		if !IsLegacyClaudeDefaultFailoverRules(legacy) {
			t.Fatal("legacy failover rules should be recognized")
		}
	})

	t.Run("new default rules should not match legacy", func(t *testing.T) {
		if IsLegacyClaudeDefaultFailoverRules(DefaultClaudeFailoverRules()) {
			t.Fatal("new default rules should not be recognized as legacy")
		}
	})
}

func TestGetEffectiveFailoverRulesAutoUpgradeLegacyDefaults(t *testing.T) {
	upstream := &UpstreamConfig{
		ServiceType: "claude",
		FailoverRules: []FailoverRule{
			{
				Action:          "cooldown",
				StatusCodes:     []int{429},
				DurationMinutes: 60,
			},
			{
				Action:      "blacklist",
				StatusCodes: []int{400, 401},
			},
		},
	}

	got := upstream.GetEffectiveFailoverRules()
	if len(got) != 6 {
		t.Fatalf("len(GetEffectiveFailoverRules()) = %d, want 6", len(got))
	}

	found402 := false
	for _, rule := range got {
		if rule.Action == "blacklist" && len(rule.StatusCodes) == 1 && rule.StatusCodes[0] == 402 {
			found402 = true
			break
		}
	}
	if !found402 {
		t.Fatal("upgraded defaults should include 402 blacklist rule")
	}
}
