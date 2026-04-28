package config

import (
	"testing"
	"time"
)

func TestMarkKeyAsFailedClearsFixedDuration(t *testing.T) {
	cm := &ConfigManager{
		failedKeysCache:     make(map[string]*FailedKey),
		keyBackoffDurations: []time.Duration{time.Minute, 2 * time.Minute},
	}

	cm.MarkKeyAsFailedWithDuration("test-key", "Messages", 10*time.Minute)
	failure := cm.failedKeysCache[failedKeyCacheKey("Messages", "test-key")]
	if failure == nil {
		t.Fatal("expected failed key to be recorded")
	}
	if failure.FixedDuration != 10*time.Minute {
		t.Fatalf("expected fixed duration to be set, got %v", failure.FixedDuration)
	}

	cm.MarkKeyAsFailed("test-key", "Messages")
	failure = cm.failedKeysCache[failedKeyCacheKey("Messages", "test-key")]
	if failure.FixedDuration != 0 {
		t.Fatalf("expected fixed duration to be cleared after normal failure, got %v", failure.FixedDuration)
	}
	if failure.FailureCount != 2 {
		t.Fatalf("expected failure count to increment to 2, got %d", failure.FailureCount)
	}
}
