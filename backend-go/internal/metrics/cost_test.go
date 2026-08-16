package metrics

import (
	"math"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func floatPtr(v float64) *float64 { return &v }

func TestCalculateTokenCostUSD_NoPricing(t *testing.T) {
	if cost := CalculateTokenCostUSD("", 1_000_000, 1_000_000, 0, 0); cost != 0 {
		t.Fatalf("expected 0 for empty model, got %v", cost)
	}
	if cost := CalculateTokenCostUSD("unknown", 1_000_000, 1_000_000, 0, 0); cost != 0 {
		t.Fatalf("expected 0 for unknown model, got %v", cost)
	}
	if _, pricingComplete := CalculateTokenCostUSDWithStatus("unknown", 1_000_000, 1_000_000, 0, 0); pricingComplete {
		t.Fatal("unknown model must not be reported as fully priced")
	}
}

func TestCalculateTokenCostUSDWithStatus_UsesRuntimePresetPricing(t *testing.T) {
	cost, pricingComplete := CalculateTokenCostUSDWithStatus("claude-opus-4-8", 1_000_000, 1_000_000, 0, 0)
	if !pricingComplete {
		t.Fatal("claude-opus-4-8 pricing from the runtime preset must be complete")
	}
	if math.Abs(cost-30) > 0.0001 {
		t.Fatalf("expected 30 USD from runtime preset, got %v", cost)
	}
}

func TestPricingCoversTokenUsage(t *testing.T) {
	pricing := &config.ModelPricing{OutputPrice: floatPtr(3)}
	if pricingCoversTokenUsage(pricing, 1_000_000, 1_000_000, 0, 0) {
		t.Fatal("missing input price must make mixed token usage incomplete")
	}
	if !pricingCoversTokenUsage(pricing, 0, 1_000_000, 0, 0) {
		t.Fatal("output-only usage should be complete when output price is known")
	}
}

func TestCalculateTokenCostUSD_CNYConversion(t *testing.T) {
	// 直接验证 CNY 单位换算：1M input @ 2 CNY + 1M output @ 8 CNY = 10 CNY
	// cnyToUSD = 1/6.8 → 10 CNY ≈ 1.4706 USD
	pricing := &config.ModelPricing{
		Unit:                "per_1m_tokens_cny",
		Currency:            "CNY",
		InputCacheMissPrice: floatPtr(2),
		OutputPrice:         floatPtr(8),
	}
	cost := calcCostWithPricing(pricing, 1_000_000, 1_000_000, 0, 0, 0)
	if math.Abs(cost-10.0*cnyToUSD) > 0.0001 {
		t.Fatalf("expected %.4f USD, got %v", 10.0*cnyToUSD, cost)
	}
	// 显式锚定：7 CNY 必须等于 1 USD
	if math.Abs(7.0*cnyToUSD-1.0) > 0.05 {
		t.Fatalf("cnyToUSD=%v must map 7 CNY to 1 USD", cnyToUSD)
	}
}

func TestCalculateTokenCostUSD_USDNoConversion(t *testing.T) {
	pricing := &config.ModelPricing{
		Unit:                "per_1m_tokens_usd",
		Currency:            "USD",
		InputCacheMissPrice: floatPtr(1),
		OutputPrice:         floatPtr(3),
	}
	cost := CalculateTokenCostUSDWithPricing(pricing, 1_000_000, 1_000_000, 0, 0)
	if math.Abs(cost-4.0) > 0.0001 {
		t.Fatalf("expected 4 USD, got %v", cost)
	}
}

func TestCalculateTokenCostUSD_CachePricingFallback(t *testing.T) {
	// 缺失 cache hit 价格时，回退到 input miss 价格
	pricing := &config.ModelPricing{
		Unit:                "per_1m_tokens_usd",
		Currency:            "USD",
		InputCacheMissPrice: floatPtr(1),
		OutputPrice:         floatPtr(2),
	}
	cost := calcCostWithPricing(pricing, 0, 1_000_000, 1_000_000, 1_000_000, 0)
	// cache create 1M @ 1 + cache read 1M @ 1 (fallback) + output 1M @ 2 = 4
	if math.Abs(cost-4.0) > 0.0001 {
		t.Fatalf("expected 4 USD, got %v", cost)
	}
}

func TestApplyEffectiveCostMultiplier(t *testing.T) {
	tests := []struct {
		name       string
		listCost   float64
		multiplier float64
		want       float64
	}{
		{name: "no multiplier (0)", listCost: 10.0, multiplier: 0, want: 10.0},
		{name: "no multiplier (negative)", listCost: 10.0, multiplier: -1, want: 10.0},
		{name: "identity multiplier (1.0)", listCost: 10.0, multiplier: 1.0, want: 10.0},
		{name: "discount (0.8)", listCost: 10.0, multiplier: 0.8, want: 8.0},
		{name: "surcharge (1.5)", listCost: 10.0, multiplier: 1.5, want: 15.0},
		{name: "zero cost", listCost: 0, multiplier: 1.5, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyEffectiveCostMultiplier(tt.listCost, tt.multiplier)
			if math.Abs(got-tt.want) > 0.0001 {
				t.Fatalf("ApplyEffectiveCostMultiplier(%v, %v) = %v, want %v", tt.listCost, tt.multiplier, got, tt.want)
			}
		})
	}
}

func TestCalculateTokenCostUSD_ChannelExchangeRateOverride(t *testing.T) {
	// 渠道级汇率覆盖：CNY 标价，1M input @ 2 CNY + 1M output @ 8 CNY = 10 CNY。
	// 默认 cnyToUSD=1/6.8 → ≈1.4706 USD；注入渠道汇率 7.0（7 CNY=1 USD）→ 10/7 ≈ 1.4286 USD。
	pricing := &config.ModelPricing{
		Unit:                "per_1m_tokens_cny",
		Currency:            "CNY",
		InputCacheMissPrice: floatPtr(2),
		OutputPrice:         floatPtr(8),
	}
	defaultCost := CalculateTokenCostUSDWithPricingAndRate(pricing, 1_000_000, 1_000_000, 0, 0, 0)
	if math.Abs(defaultCost-10.0*cnyToUSD) > 0.0001 {
		t.Fatalf("default rate: expected %.4f, got %v", 10.0*cnyToUSD, defaultCost)
	}
	overrideCost := CalculateTokenCostUSDWithPricingAndRate(pricing, 1_000_000, 1_000_000, 0, 0, 7.0)
	if math.Abs(overrideCost-10.0/7.0) > 0.0001 {
		t.Fatalf("override rate 7.0: expected %.4f, got %v", 10.0/7.0, overrideCost)
	}
	// USD 标价不受汇率影响
	usd := &config.ModelPricing{Unit: "per_1m_tokens_usd", Currency: "USD", InputCacheMissPrice: floatPtr(1), OutputPrice: floatPtr(3)}
	if got := CalculateTokenCostUSDWithPricingAndRate(usd, 1_000_000, 1_000_000, 0, 0, 7.0); math.Abs(got-4.0) > 0.0001 {
		t.Fatalf("USD pricing must ignore exchangeRate, got %v", got)
	}
}
