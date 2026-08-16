package config

import "testing"

// 渠道级计费覆盖字段的部分更新与清零语义
func TestApplyUpstreamUpdateFields_CostFields(t *testing.T) {
	floatPtr := func(v float64) *float64 { return &v }

	t.Run("set multiplier and rate", func(t *testing.T) {
		u := &UpstreamConfig{}
		_, err := applyUpstreamUpdateFields(u, UpstreamUpdate{
			CostMultiplier: floatPtr(0.5),
			ExchangeRate:   floatPtr(6.8),
		})
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if u.CostMultiplier == nil || *u.CostMultiplier != 0.5 {
			t.Fatalf("CostMultiplier = %v, want 0.5", u.CostMultiplier)
		}
		if u.ExchangeRate == nil || *u.ExchangeRate != 6.8 {
			t.Fatalf("ExchangeRate = %v, want 6.8", u.ExchangeRate)
		}
	})

	t.Run("nil keeps existing", func(t *testing.T) {
		u := &UpstreamConfig{CostMultiplier: floatPtr(2), ExchangeRate: floatPtr(7)}
		_, err := applyUpstreamUpdateFields(u, UpstreamUpdate{})
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if u.CostMultiplier == nil || *u.CostMultiplier != 2 {
			t.Fatalf("CostMultiplier should be preserved, got %v", u.CostMultiplier)
		}
		if u.ExchangeRate == nil || *u.ExchangeRate != 7 {
			t.Fatalf("ExchangeRate should be preserved, got %v", u.ExchangeRate)
		}
	})

	t.Run("zero clears to nil", func(t *testing.T) {
		u := &UpstreamConfig{CostMultiplier: floatPtr(2), ExchangeRate: floatPtr(7)}
		_, err := applyUpstreamUpdateFields(u, UpstreamUpdate{
			CostMultiplier: floatPtr(0),
			ExchangeRate:   floatPtr(0),
		})
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if u.CostMultiplier != nil {
			t.Fatalf("CostMultiplier should be cleared to nil, got %v", *u.CostMultiplier)
		}
		if u.ExchangeRate != nil {
			t.Fatalf("ExchangeRate should be cleared to nil, got %v", *u.ExchangeRate)
		}
	})

	t.Run("negative rejected", func(t *testing.T) {
		u := &UpstreamConfig{}
		if _, err := applyUpstreamUpdateFields(u, UpstreamUpdate{CostMultiplier: floatPtr(-1)}); err == nil {
			t.Fatal("negative costMultiplier should be rejected")
		}
		if _, err := applyUpstreamUpdateFields(u, UpstreamUpdate{ExchangeRate: floatPtr(-6.8)}); err == nil {
			t.Fatal("negative exchangeRate should be rejected")
		}
	})
}
