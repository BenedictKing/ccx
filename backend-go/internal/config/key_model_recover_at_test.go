package config

import (
	"testing"
	"time"
)

// TestDisableKeyModelWithRecoverAt 组合级限制透传上游给出的恢复时间；
// 无效/已过期的时间沿用 1 小时兜底（与 Key 级拉黑语义一致）。
func TestDisableKeyModelWithRecoverAt(t *testing.T) {
	cm := newKeyModelTestConfigManager(t)

	// 显式未来时间：原样透传
	explicit := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	if err := cm.DisableKeyModel("Messages", 0, "sk-a", "m1", "insufficient_balance", "b", explicit.Format(time.RFC3339)); err != nil {
		t.Fatalf("DisableKeyModel() error = %v", err)
	}
	got := cm.GetConfig().Upstream[0].DisabledKeyModels[0].RecoverAt
	if got != explicit.Format(time.RFC3339) {
		t.Fatalf("RecoverAt = %q, want explicit %q", got, explicit.Format(time.RFC3339))
	}

	// 已过期时间：1 小时兜底
	if err := cm.DisableKeyModel("Messages", 0, "sk-b", "m1", "insufficient_balance", "b", time.Now().Add(-time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatalf("DisableKeyModel(expired) error = %v", err)
	}
	entries := cm.GetConfig().Upstream[0].DisabledKeyModels
	var skb string
	for _, dm := range entries {
		if dm.Key == "sk-b" {
			skb = dm.RecoverAt
		}
	}
	parsed, err := time.Parse(time.RFC3339, skb)
	if err != nil {
		t.Fatalf("sk-b RecoverAt not RFC3339: %q", skb)
	}
	fallback := parsed.Sub(time.Now())
	if fallback < 55*time.Minute || fallback > 65*time.Minute {
		t.Fatalf("expired recoverAt should fall back to ~1h, got %v (%q)", fallback, skb)
	}

	// 空时间：1 小时兜底
	if err := cm.DisableKeyModel("Messages", 0, "sk-b", "m2", "insufficient_balance", "b", ""); err != nil {
		t.Fatalf("DisableKeyModel(empty) error = %v", err)
	}
	entries = cm.GetConfig().Upstream[0].DisabledKeyModels
	for _, dm := range entries {
		if dm.Key == "sk-b" && dm.Model == "m2" {
			parsed, err := time.Parse(time.RFC3339, dm.RecoverAt)
			if err != nil {
				t.Fatalf("m2 RecoverAt not RFC3339: %q", dm.RecoverAt)
			}
			if d := parsed.Sub(time.Now()); d < 55*time.Minute || d > 65*time.Minute {
				t.Fatalf("empty recoverAt should fall back to ~1h, got %v", d)
			}
		}
	}
}
