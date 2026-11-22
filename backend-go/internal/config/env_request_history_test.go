package config

import "testing"

func TestRequestHistoryRetentionDefaultsAndBounds(t *testing.T) {
	t.Setenv("METRICS_RETENTION_DAYS", "30")
	for _, item := range []struct {
		value string
		days  int
	}{{"", 14}, {"7", 7}, {"0", 1}, {"999", 366}, {"invalid", 14}} {
		t.Run(item.value, func(t *testing.T) {
			t.Setenv("REQUEST_HISTORY_RETENTION_DAYS", item.value)
			cfg := NewEnvConfig()
			if cfg.RequestHistoryRetentionDays != item.days || cfg.MetricsRetentionDays != 30 {
				t.Fatalf("retention not independent: request=%d metrics=%d", cfg.RequestHistoryRetentionDays, cfg.MetricsRetentionDays)
			}
		})
	}
}
