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
}

func TestCalculateTokenCostUSD_CNYConversion(t *testing.T) {
	// 直接验证 CNY 单位换算：1M input @ 2 CNY + 1M output @ 8 CNY = 10 CNY ≈ 1.3889 USD
	pricing := &config.ModelPricing{
		Unit:                "per_1m_tokens_cny",
		Currency:            "CNY",
		InputCacheMissPrice: floatPtr(2),
		OutputPrice:         floatPtr(8),
	}
	cost := calcCostWithPricing(pricing, 1_000_000, 1_000_000, 0, 0)
	// 10 CNY * (1/6.8) ≈ 1.4706 USD
	if math.Abs(cost-(10.0*cnyToUSD)) > 0.0001 {
		t.Fatalf("expected %.4f USD, got %v", 10.0*cnyToUSD, cost)
	}
}

func TestCalculateTokenCostUSD_USDNoConversion(t *testing.T) {
	pricing := &config.ModelPricing{
		Unit:                "per_1m_tokens_usd",
		Currency:            "USD",
		InputCacheMissPrice: floatPtr(1),
		OutputPrice:         floatPtr(3),
	}
	cost := calcCostWithPricing(pricing, 1_000_000, 1_000_000, 0, 0)
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
	cost := calcCostWithPricing(pricing, 0, 1_000_000, 1_000_000, 1_000_000)
	// cache create 1M @ 1 + cache read 1M @ 1 (fallback) + output 1M @ 2 = 4
	if math.Abs(cost-4.0) > 0.0001 {
		t.Fatalf("expected 4 USD, got %v", cost)
	}
}
