package metrics

import (
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
)

const cnyToUSD = 1.0 / 6.8

// CalculateTokenCostUSD 根据当前模型价格估算 token 成本，返回 USD。
func CalculateTokenCostUSD(model string, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64) float64 {
	cost, _ := CalculateTokenCostUSDWithStatus(model, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens)
	return cost
}

// CalculateTokenCostUSDWithStatus 根据当前模型价格估算 token 成本，并标识该次用量是否有完整定价。
// 当定价缺失时仍保留已知部分成本，调用方可将结果作为成本下界展示。
func CalculateTokenCostUSDWithStatus(model string, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64) (float64, bool) {
	if model == "" || model == "unknown" {
		return 0, false
	}
	resolved := config.ResolveUpstreamCapability(model, nil, nil)
	pricing := resolved.Capability.Pricing
	return calcCostWithPricing(pricing, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens), pricingCoversTokenUsage(pricing, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens)
}

// CalculateTokenCostUSDWithPricing 使用已解析的模型价格估算成本。
// 调用方可在渠道级模型注册表覆盖生效时避免再次按全局模型名解析。
func CalculateTokenCostUSDWithPricing(pricing *config.ModelPricing, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64) float64 {
	return calcCostWithPricing(pricing, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens)
}

func pricingCoversTokenUsage(pricing *config.ModelPricing, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64) bool {
	if pricing == nil || !hasAnyTokenPrice(pricing) {
		return false
	}

	inputPrice := pricing.InputCacheMissPrice
	cacheCreationPrice := pricing.InputCacheMissPrice
	cacheReadPrice := priceOrFallback(pricing.InputCacheHitPrice, inputPrice)
	outputPrice := pricing.OutputPrice

	if tier := selectPricingTier(pricing.Tiers, inputTokens+cacheCreationTokens+cacheReadTokens); tier != nil {
		inputPrice = priceOrFallback(tier.InputCacheMissPrice, inputPrice)
		cacheCreationPrice = priceOrFallback(tier.InputCacheMissPrice, cacheCreationPrice)
		cacheReadPrice = priceOrFallback(tier.InputCacheHitPrice, cacheReadPrice)
		outputPrice = priceOrFallback(tier.OutputPrice, outputPrice)
	}

	return (inputTokens == 0 || inputPrice != nil) &&
		(cacheCreationTokens == 0 || cacheCreationPrice != nil) &&
		(cacheReadTokens == 0 || cacheReadPrice != nil) &&
		(outputTokens == 0 || outputPrice != nil)
}

func hasAnyTokenPrice(pricing *config.ModelPricing) bool {
	if pricing.InputCacheHitPrice != nil || pricing.InputCacheMissPrice != nil || pricing.OutputPrice != nil {
		return true
	}
	for _, tier := range pricing.Tiers {
		if tier.InputCacheHitPrice != nil || tier.InputCacheMissPrice != nil || tier.OutputPrice != nil {
			return true
		}
	}
	return false
}

func priceOrFallback(price, fallback *float64) *float64 {
	if price != nil {
		return price
	}
	return fallback
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

// ApplyEffectiveCostMultiplier 将官方价（list price）按实际成本倍率换算为 EffectiveCostUSD。
// multiplier 来自 autopilot.KeyEndpointProfile.CostProfile.EffectiveCostMultiplier，由调用方传入，
// metrics 包本身不反向依赖 autopilot（保持现有分层约束）。
// multiplier <= 0 或 == 1.0 时返回原始 listCostUSD（无折扣/无加价）。
func ApplyEffectiveCostMultiplier(listCostUSD float64, multiplier float64) float64 {
	if multiplier <= 0 || multiplier == 1.0 {
		return listCostUSD
	}
	return listCostUSD * multiplier
}
