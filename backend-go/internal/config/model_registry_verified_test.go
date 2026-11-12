package config

import "testing"

func TestVerifiedProviderPricingAndLimits(t *testing.T) {
	for _, tt := range []struct {
		model, currency string
		hit, miss, out  float64
	}{
		{"glm-5.3", "CNY", 2, 8, 28}, {"glm-5.3-flash", "CNY", 0.23, 0.8, 2.8},
		{"glm-5.3-flashx", "CNY", 0.57, 2, 7}, {"glm-5.3-flashx-legal", "CNY", 0.57, 2, 7},
		{"step-3.7-flash", "CNY", 0.27, 1.35, 8.1},
		{"deepseek-v4-flash", "CNY", 0.02, 1, 4}, {"deepseek-v4-pro", "CNY", 0.15, 4.5, 13.5},
		{"kimi-k3", "CNY", 2, 20, 100}, {"kimi-k3.1-pro", "CNY", 1.3, 13, 68}, {"kimi-k3.1-pro-highspeed", "CNY", 1.3, 26, 136},
	} {
		t.Run(tt.model, func(t *testing.T) {
			resolved := ResolveUpstreamCapability(tt.model, nil, nil)
			p := resolved.Capability.Pricing
			if !resolved.Known || p == nil {
				t.Fatal("missing verified pricing")
			}
			if p.Currency != tt.currency || p.Unit != "per_1m_tokens_cny" {
				t.Fatalf("currency/unit=%s/%s", p.Currency, p.Unit)
			}
			assertFloatPointerValue(t, p.InputCacheHitPrice, tt.hit, "cached")
			assertFloatPointerValue(t, p.InputCacheMissPrice, tt.miss, "uncached")
			assertFloatPointerValue(t, p.OutputPrice, tt.out, "output")
		})
	}
	for model, want := range map[string]int{"gpt-5.4": 1050000, "gpt-5.5": 1050000, "gpt-5.6-sol": 1050000, "qwen3.8-max": 1000000} {
		if got := ResolveUpstreamCapability(model, nil, nil).Capability.ContextWindowTokens; got != want {
			t.Errorf("%s context=%d,want %d", model, got, want)
		}
	}
	for _, model := range []string{"kimi-k3", "kimi-k3.1-pro", "kimi-k3.1-pro-highspeed"} {
		c := ResolveUpstreamCapability(model, nil, nil).Capability
		if c.MaxOutputTokens != 1048576 || c.DefaultOutputTokens != 131072 {
			t.Errorf("%s max/default=%d/%d", model, c.MaxOutputTokens, c.DefaultOutputTokens)
		}
	}
}
