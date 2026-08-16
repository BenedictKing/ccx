package config

import "testing"

// 渠道级计费覆盖字段的部分更新与清零语义
func TestApplyUpstreamUpdateFields_CostFields(t *testing.T) {
	floatPtr := func(v float64) *float64 { return &v }
	strPtr := func(s string) *string { return &s }

	t.Run("set multiplier and payment/credit", func(t *testing.T) {
		u := &UpstreamConfig{}
		_, err := applyUpstreamUpdateFields(u, UpstreamUpdate{
			CostMultiplier:         floatPtr(0.5),
			ChannelPaymentCurrency: strPtr("LDC"),
			ChannelPaymentAmount:   floatPtr(20),
			ChannelCreditCurrency:  strPtr("USD"),
			ChannelCreditAmount:    floatPtr(1),
		})
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if u.CostMultiplier == nil || *u.CostMultiplier != 0.5 {
			t.Fatalf("CostMultiplier = %v, want 0.5", u.CostMultiplier)
		}
		if u.ChannelPaymentCurrency != "LDC" || u.ChannelCreditCurrency != "USD" {
			t.Fatalf("currencies = %q/%q, want LDC/USD", u.ChannelPaymentCurrency, u.ChannelCreditCurrency)
		}
		if u.ChannelPaymentAmount == nil || *u.ChannelPaymentAmount != 20 {
			t.Fatalf("ChannelPaymentAmount = %v, want 20", u.ChannelPaymentAmount)
		}
		if u.ChannelCreditAmount == nil || *u.ChannelCreditAmount != 1 {
			t.Fatalf("ChannelCreditAmount = %v, want 1", u.ChannelCreditAmount)
		}
	})

	t.Run("nil keeps existing", func(t *testing.T) {
		u := &UpstreamConfig{
			CostMultiplier:         floatPtr(2),
			ChannelPaymentCurrency: "CNY",
			ChannelPaymentAmount:   floatPtr(7),
			ChannelCreditCurrency:  "USD",
			ChannelCreditAmount:    floatPtr(1),
		}
		_, err := applyUpstreamUpdateFields(u, UpstreamUpdate{})
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if u.CostMultiplier == nil || *u.CostMultiplier != 2 {
			t.Fatalf("CostMultiplier should be preserved, got %v", u.CostMultiplier)
		}
		if u.ChannelPaymentAmount == nil || *u.ChannelPaymentAmount != 7 {
			t.Fatalf("ChannelPaymentAmount should be preserved, got %v", u.ChannelPaymentAmount)
		}
	})

	t.Run("zero amount clears to nil", func(t *testing.T) {
		u := &UpstreamConfig{
			CostMultiplier:       floatPtr(2),
			ChannelPaymentAmount: floatPtr(20),
			ChannelCreditAmount:  floatPtr(1),
		}
		_, err := applyUpstreamUpdateFields(u, UpstreamUpdate{
			CostMultiplier:       floatPtr(0),
			ChannelPaymentAmount: floatPtr(0),
			ChannelCreditAmount:  floatPtr(0),
		})
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if u.CostMultiplier != nil {
			t.Fatalf("CostMultiplier should be cleared to nil, got %v", *u.CostMultiplier)
		}
		if u.ChannelPaymentAmount != nil {
			t.Fatalf("ChannelPaymentAmount should be cleared to nil, got %v", *u.ChannelPaymentAmount)
		}
		if u.ChannelCreditAmount != nil {
			t.Fatalf("ChannelCreditAmount should be cleared to nil, got %v", *u.ChannelCreditAmount)
		}
	})

	t.Run("currency trimmed / cleared", func(t *testing.T) {
		u := &UpstreamConfig{ChannelPaymentCurrency: "LDC", ChannelCreditCurrency: "USD"}
		_, err := applyUpstreamUpdateFields(u, UpstreamUpdate{
			ChannelPaymentCurrency: strPtr("  cny  "),
			ChannelCreditCurrency:  strPtr(""),
		})
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if u.ChannelPaymentCurrency != "cny" {
			t.Fatalf("ChannelPaymentCurrency = %q, want trimmed cny", u.ChannelPaymentCurrency)
		}
		if u.ChannelCreditCurrency != "" {
			t.Fatalf("ChannelCreditCurrency = %q, want cleared", u.ChannelCreditCurrency)
		}
	})

	t.Run("negative rejected", func(t *testing.T) {
		u := &UpstreamConfig{}
		if _, err := applyUpstreamUpdateFields(u, UpstreamUpdate{CostMultiplier: floatPtr(-1)}); err == nil {
			t.Fatal("negative costMultiplier should be rejected")
		}
		if _, err := applyUpstreamUpdateFields(u, UpstreamUpdate{ChannelPaymentAmount: floatPtr(-20)}); err == nil {
			t.Fatal("negative channelPaymentAmount should be rejected")
		}
		if _, err := applyUpstreamUpdateFields(u, UpstreamUpdate{ChannelCreditAmount: floatPtr(-1)}); err == nil {
			t.Fatal("negative channelCreditAmount should be rejected")
		}
	})
}
