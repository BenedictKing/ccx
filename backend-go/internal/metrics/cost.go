package metrics

import (
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
)

const cnyToUSD = 1.0 / 6.8

// CalculateTokenCostUSD 根据当前模型价格估算 token 成本，返回 USD。
func CalculateTokenCostUSD(model string, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64) float64 {
	return CalculateTokenCostUSDWithConfig(model, nil, nil, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens)
}

// CalculateTokenCostUSDWithConfig 优先使用渠道及全局模型价格，内置价格只作兜底。
func CalculateTokenCostUSDWithConfig(model string, upstream *config.UpstreamConfig, global map[string]config.UpstreamModelCapability, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64) float64 {
	if model == "" || model == "unknown" {
		return 0
	}
	resolved := config.ResolveUpstreamCapability(model, upstream, global)
	pricing := resolved.Capability.Pricing
	if pricing == nil && upstream != nil {
		pricing = upstream.DefaultCapability.Pricing
	}
	if pricing == nil {
		pricing = config.ResolveUpstreamCapability(resolved.ActualModel, nil, global).Capability.Pricing
	}
	if pricing == nil {
		return 0
	}
	return calcCostWithPricing(pricing, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens)
}

// CalculateTokenCostUSDForChannel 在模型成本上应用渠道分组倍率，未配置倍率按 1 计算。
func CalculateTokenCostUSDForChannel(model string, upstream *config.UpstreamConfig, global map[string]config.UpstreamModelCapability, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64) float64 {
	multiplier := upstream.BillingMultiplier()
	if multiplier == 0 {
		return 0
	}
	return multiplier * CalculateTokenCostUSDWithConfig(model, upstream, global, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens)
}

// calcCostWithPricing 根据给定价格估算 token 成本（USD）。
func calcCostWithPricing(pricing *config.ModelPricing, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64) float64 {
	if pricing == nil {
		return 0
	}

	inputPrice := valueOrZero(pricing.InputCacheMissPrice)
	cacheCreatePrice := valueOrDefault(pricing.InputCacheMissPrice, inputPrice)
	cacheReadPrice := valueOrDefault(pricing.InputCacheHitPrice, inputPrice)
	outputPrice := valueOrZero(pricing.OutputPrice)

	if len(pricing.Tiers) > 0 {
		if tier := selectPricingTier(pricing.Tiers, inputTokens+cacheCreationTokens+cacheReadTokens); tier != nil {
			inputPrice = valueOrDefault(tier.InputCacheMissPrice, inputPrice)
			cacheCreatePrice = valueOrDefault(tier.InputCacheMissPrice, cacheCreatePrice)
			cacheReadPrice = valueOrDefault(tier.InputCacheHitPrice, cacheReadPrice)
			outputPrice = valueOrDefault(tier.OutputPrice, outputPrice)
		}
	}

	cost := (float64(inputTokens)*inputPrice +
		float64(cacheCreationTokens)*cacheCreatePrice +
		float64(cacheReadTokens)*cacheReadPrice +
		float64(outputTokens)*outputPrice) / 1_000_000

	if strings.EqualFold(pricing.Currency, "CNY") || strings.Contains(strings.ToLower(pricing.Unit), "cny") {
		cost *= cnyToUSD
	}
	return cost
}

func valueOrZero(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func valueOrDefault(v *float64, fallback float64) float64 {
	if v == nil {
		return fallback
	}
	return *v
}

func selectPricingTier(tiers []config.ModelPricingTier, inputTokens int64) *config.ModelPricingTier {
	for i := range tiers {
		tier := &tiers[i]
		if tier.InputTokensAbove > 0 && inputTokens <= int64(tier.InputTokensAbove) {
			continue
		}
		if tier.InputTokensUpTo > 0 && inputTokens > int64(tier.InputTokensUpTo) {
			continue
		}
		return tier
	}
	return nil
}
