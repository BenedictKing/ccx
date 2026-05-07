package pricing

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/BenedictKing/ccx/internal/types"
)

// 每百万 tokens 的常量（用作 token 数到"百万 tokens"的归一化分母）。
var oneMillion = decimal.NewFromInt(1_000_000)

// unitsInMillionTokens 把 token 数换算为"百万 tokens"标量；非正数返回 0。
func unitsInMillionTokens(units int64) decimal.Decimal {
	if units <= 0 {
		return decimal.Zero
	}
	return decimal.NewFromInt(units).Div(oneMillion)
}

// Calculate 根据 usage 与价格表计算总成本与逐项 CostItem 列表。
//
// 输入：
//   - usage: ccx 内部 token 统计（types.Usage），包含 OpenAI/Anthropic 双口径字段；
//   - price: 模型对应的 ModelPrice。
//
// 输出：
//   - total:   合计（USD，decimal 精度），即各 CostItem.Subtotal 之和；
//   - items:   逐项明细（一个 PriceItemCode 至少一条，cache 写带变体时可能多条）；
//   - err:     校验失败或 mode 未知时返回错误。
//
// 计算逻辑要点（与 axonhub `biz.ComputeUsageCost` 行为一致）：
//  1. PriceItemCodeUsage 的 quantity = PromptTokens(归一化) - cached - writeCached，下界为 0；
//     "归一化"指 ccx 同时保留 InputTokens / PromptTokens 两口径，本函数取较大者作为 prompt 总量；
//  2. WriteCachedTokens 优先按变体计算（5min + 1hour 各一条 CostItem），只有当两者都为 0 时
//     才退回到共享 pricing 单条 CostItem；
//  3. 阶梯计费按 (前段上界, 当前段上界] 区间累加。
func Calculate(usage types.Usage, price *ModelPrice) (decimal.Decimal, []CostItem, error) {
	if price == nil {
		return decimal.Zero, nil, errors.New("pricing.Calculate: price 不能为 nil")
	}
	if err := price.Validate(); err != nil {
		return decimal.Zero, nil, err
	}

	promptTotal := promptTokensTotal(usage)
	completionTotal := completionTokensTotal(usage)
	cachedRead := int64(usage.CacheReadInputTokens)
	writeCached := int64(usage.CacheCreationInputTokens)
	writeCached5m := int64(usage.CacheCreation5mInputTokens)
	writeCached1h := int64(usage.CacheCreation1hInputTokens)

	total := decimal.Zero
	items := make([]CostItem, 0, 4)

	for idx := range price.Items {
		item := &price.Items[idx]
		switch item.ItemCode {
		case PriceItemCodeUsage:
			qty := promptTotal - cachedRead - writeCached
			if qty < 0 {
				qty = 0
			}
			ci, err := computeItemCost(item.ItemCode, "", qty, item.Pricing)
			if err != nil {
				return decimal.Zero, nil, fmt.Errorf("pricing.Calculate: %s: %w", item.ItemCode, err)
			}
			items = append(items, ci)
			total = total.Add(ci.Subtotal)

		case PriceItemCodeCompletion:
			ci, err := computeItemCost(item.ItemCode, "", completionTotal, item.Pricing)
			if err != nil {
				return decimal.Zero, nil, fmt.Errorf("pricing.Calculate: %s: %w", item.ItemCode, err)
			}
			items = append(items, ci)
			total = total.Add(ci.Subtotal)

		case PriceItemCodePromptCachedToken:
			ci, err := computeItemCost(item.ItemCode, "", cachedRead, item.Pricing)
			if err != nil {
				return decimal.Zero, nil, fmt.Errorf("pricing.Calculate: %s: %w", item.ItemCode, err)
			}
			items = append(items, ci)
			total = total.Add(ci.Subtotal)

		case PriceItemCodeWriteCachedTokens:
			// 变体模式：5min / 1hour 任一非零则只走变体，不再走共享 pricing。
			if writeCached5m > 0 || writeCached1h > 0 {
				if writeCached5m > 0 {
					p := item.FindPromptWriteCacheVariantPricing(PromptWriteCacheVariantCode5Min)
					ci, err := computeItemCost(item.ItemCode, PromptWriteCacheVariantCode5Min, writeCached5m, p)
					if err != nil {
						return decimal.Zero, nil, fmt.Errorf("pricing.Calculate: %s 5m: %w", item.ItemCode, err)
					}
					items = append(items, ci)
					total = total.Add(ci.Subtotal)
				}
				if writeCached1h > 0 {
					p := item.FindPromptWriteCacheVariantPricing(PromptWriteCacheVariantCode1Hour)
					ci, err := computeItemCost(item.ItemCode, PromptWriteCacheVariantCode1Hour, writeCached1h, p)
					if err != nil {
						return decimal.Zero, nil, fmt.Errorf("pricing.Calculate: %s 1h: %w", item.ItemCode, err)
					}
					items = append(items, ci)
					total = total.Add(ci.Subtotal)
				}
			} else {
				ci, err := computeItemCost(item.ItemCode, "", writeCached, item.Pricing)
				if err != nil {
					return decimal.Zero, nil, fmt.Errorf("pricing.Calculate: %s: %w", item.ItemCode, err)
				}
				items = append(items, ci)
				total = total.Add(ci.Subtotal)
			}
		}
	}

	return total, items, nil
}

// promptTokensTotal 归一化 prompt 总量：取 PromptTokens（OpenAI 口径，已含 cache）
// 与 InputTokens（Anthropic 口径，未含 cache）较大者，避免上游字段缺失时算 0。
//
// ccx PR1/PR2 在写入 Usage 时会同步两个字段，但仍兜底处理只有一边的情况。
func promptTokensTotal(u types.Usage) int64 {
	prompt := int64(u.PromptTokens)
	if u.PromptTokensTotal > 0 && int64(u.PromptTokensTotal) > prompt {
		prompt = int64(u.PromptTokensTotal)
	}
	// Anthropic 口径 InputTokens 通常不含 cache，需要补回 cache 读 / 写以与 OpenAI 口径对齐。
	anthropic := int64(u.InputTokens) + int64(u.CacheReadInputTokens) + int64(u.CacheCreationInputTokens)
	if anthropic > prompt {
		prompt = anthropic
	}
	return prompt
}

// completionTokensTotal 归一化 completion 总量：取 OutputTokens / CompletionTokens 较大者。
func completionTokensTotal(u types.Usage) int64 {
	out := int64(u.CompletionTokens)
	if int64(u.OutputTokens) > out {
		out = int64(u.OutputTokens)
	}
	return out
}

// computeItemCost 计算单个 CostItem。
func computeItemCost(code PriceItemCode, variant PromptWriteCacheVariantCode, quantity int64, p Pricing) (CostItem, error) {
	ci := CostItem{
		ItemCode:                    code,
		PromptWriteCacheVariantCode: variant,
		Quantity:                    quantity,
		Subtotal:                    decimal.Zero,
	}

	switch p.Mode {
	case PricingModeFlatFee:
		// 固定费用：不依赖 quantity（即便 quantity=0 也收 FlatFee）。
		if p.FlatFee == nil {
			return ci, errors.New("flat_fee 缺失 flatFee")
		}
		ci.Subtotal = *p.FlatFee
		return ci, nil

	case PricingModeUsagePerUnit:
		if p.UsagePerUnit == nil {
			return ci, errors.New("usage_per_unit 缺失 usagePerUnit")
		}
		if quantity <= 0 {
			return ci, nil
		}
		ci.Subtotal = unitsInMillionTokens(quantity).Mul(*p.UsagePerUnit)
		return ci, nil

	case PricingModeTiered:
		if p.UsageTiered == nil || len(p.UsageTiered.Tiers) == 0 {
			return ci, errors.New("usage_tiered 缺失 tiers")
		}
		if quantity <= 0 {
			return ci, nil
		}
		ci.TierBreakdown = make([]TierCost, 0, len(p.UsageTiered.Tiers))
		var lower int64 // 当前段下界（不含）
		remaining := quantity
		for _, tier := range p.UsageTiered.Tiers {
			if remaining <= 0 {
				break
			}
			var segment int64
			if tier.UpTo == nil {
				segment = remaining
			} else {
				capUnits := *tier.UpTo - lower
				if capUnits <= 0 {
					continue
				}
				if remaining < capUnits {
					segment = remaining
				} else {
					segment = capUnits
				}
				lower = *tier.UpTo
			}
			subtotal := unitsInMillionTokens(segment).Mul(tier.PricePerUnit)
			tc := TierCost{UpTo: tier.UpTo, Units: segment, Subtotal: subtotal}
			ci.TierBreakdown = append(ci.TierBreakdown, tc)
			ci.Subtotal = ci.Subtotal.Add(subtotal)
			remaining -= segment
		}
		return ci, nil

	default:
		return ci, fmt.Errorf("未知 pricing.mode: %q", p.Mode)
	}
}
