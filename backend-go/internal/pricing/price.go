// Package pricing 提供模型计费规则与成本计算能力。
//
// 三种计费模式（PricingMode）：
//   - flat_fee:        每次请求固定单价（不依赖 token 数）
//   - usage_per_unit:  按用量线性计费（单价单位为 USD / 1M tokens）
//   - usage_tiered:    阶梯计费（按 PriceTier 分段累加）
//
// 注意：所有单价（UsagePerUnit / PriceTier.PricePerUnit）的单位均为
// "每百万 tokens 美元数"。本包内部 Calculate 时会自动将 token 数除以 1_000_000。
//
// 该包源于 axonhub `internal/objects/price.go` + `cost.go` + `biz/cost_calc.go`，
// 改造点：
//  1. 包名 objects/biz → pricing；
//  2. 计费输入由 axonhub `llm.Usage` 调整为 ccx 的 `internal/types.Usage`；
//  3. 价格表加载方式由 ent DB 改为 embed.FS + 可选文件 override + fsnotify 热重载。
package pricing

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// PricingMode 三种计费模式枚举。
type PricingMode string

const (
	// PricingModeFlatFee 固定费用：每次请求收 FlatFee，不看 token 数。
	PricingModeFlatFee PricingMode = "flat_fee"

	// PricingModeUsagePerUnit 按用量线性计费：单价 UsagePerUnit × (token 数 / 1_000_000)。
	PricingModeUsagePerUnit PricingMode = "usage_per_unit"

	// PricingModeTiered 阶梯计费：按 PriceTier 分段累加。
	PricingModeTiered PricingMode = "usage_tiered"
)

// Pricing 单个 ItemCode 的计费规则。
//
// 三种模式互斥：通过 Mode 选择对应字段，其他字段应为 nil。
type Pricing struct {
	Mode         PricingMode      `json:"mode"`
	FlatFee      *decimal.Decimal `json:"flatFee,omitempty"`
	UsagePerUnit *decimal.Decimal `json:"usagePerUnit,omitempty"`
	UsageTiered  *TieredPricing   `json:"usageTiered,omitempty"`
}

// Validate 校验 Pricing 配置完整性。
func (p Pricing) Validate() error {
	switch p.Mode {
	case PricingModeFlatFee:
		if p.FlatFee == nil {
			return errors.New("pricing: flat_fee 模式必须设置 flatFee")
		}
	case PricingModeUsagePerUnit:
		if p.UsagePerUnit == nil {
			return errors.New("pricing: usage_per_unit 模式必须设置 usagePerUnit")
		}
	case PricingModeTiered:
		if p.UsageTiered == nil || len(p.UsageTiered.Tiers) == 0 {
			return errors.New("pricing: usage_tiered 模式必须设置至少一个 tier")
		}
		return p.UsageTiered.Validate()
	default:
		return fmt.Errorf("pricing: 未知的 mode: %q", p.Mode)
	}
	return nil
}

// TieredPricing 阶梯计费容器。
type TieredPricing struct {
	Tiers []PriceTier `json:"tiers"`
}

// Validate 校验 tiers 顺序：除最后一个外 UpTo 必须非 nil 且严格递增；
// 最后一个 tier 必须 UpTo == nil（无上界）。
func (tp TieredPricing) Validate() error {
	n := len(tp.Tiers)
	if n == 0 {
		return errors.New("pricing: tiers 不能为空")
	}
	var prev int64 = -1
	for i, t := range tp.Tiers {
		if i == n-1 {
			if t.UpTo != nil {
				return errors.New("pricing: 最后一个 tier 的 upTo 必须为 nil（无上界）")
			}
			continue
		}
		if t.UpTo == nil {
			return fmt.Errorf("pricing: 非末位 tier[%d] 的 upTo 不能为 nil", i)
		}
		if *t.UpTo <= prev {
			return fmt.Errorf("pricing: tier[%d] upTo=%d 必须严格大于前一个 tier", i, *t.UpTo)
		}
		prev = *t.UpTo
	}
	return nil
}

// PriceTier 单个阶梯。
//
//   - UpTo == nil：表示该 tier 无上界，必须是 Tiers 的最后一个；
//   - UpTo != nil：表示该 tier 覆盖区间 (前一段上界, UpTo]。
type PriceTier struct {
	// UpTo 上界（含），nil 仅允许在最后一个 tier。
	UpTo *int64 `json:"upTo,omitempty"`

	// PricePerUnit 该阶梯的"每百万 tokens"单价（USD）。
	PricePerUnit decimal.Decimal `json:"pricePerUnit"`
}

// PriceItemCode 计费项编码。
type PriceItemCode string

const (
	// PriceItemCodeUsage 输入（不含 cache 部分；prompt - cached - writeCached）。
	PriceItemCodeUsage PriceItemCode = "prompt_tokens"

	// PriceItemCodeCompletion 输出 token。
	PriceItemCodeCompletion PriceItemCode = "completion_tokens"

	// PriceItemCodePromptCachedToken cache 读 token。
	PriceItemCodePromptCachedToken PriceItemCode = "prompt_cached_tokens"

	// PriceItemCodeWriteCachedTokens cache 写 token（共享，不区分 TTL）。
	PriceItemCodeWriteCachedTokens PriceItemCode = "prompt_write_cached_tokens"
)

// PromptWriteCacheVariantCode cache 写变体（Anthropic 特有：5min / 1hour TTL）。
type PromptWriteCacheVariantCode string

const (
	PromptWriteCacheVariantCode5Min  PromptWriteCacheVariantCode = "five_min"
	PromptWriteCacheVariantCode1Hour PromptWriteCacheVariantCode = "one_hour"
)

// PromptWriteCacheVariant cache 写变体单价配置。
type PromptWriteCacheVariant struct {
	VariantCode PromptWriteCacheVariantCode `json:"variantCode"`
	Pricing     Pricing                     `json:"pricing"`
}

// ModelPriceItem 单个 ItemCode 的价格条目；可附带 cache 写变体细分。
type ModelPriceItem struct {
	ItemCode                 PriceItemCode             `json:"itemCode"`
	Pricing                  Pricing                   `json:"pricing"`
	PromptWriteCacheVariants []PromptWriteCacheVariant `json:"promptWriteCacheVariants,omitempty"`
}

// FindPromptWriteCacheVariantPricing 查找指定变体的 Pricing；找不到时回退到 item.Pricing。
func (i *ModelPriceItem) FindPromptWriteCacheVariantPricing(code PromptWriteCacheVariantCode) Pricing {
	for _, v := range i.PromptWriteCacheVariants {
		if v.VariantCode == code {
			return v.Pricing
		}
	}
	return i.Pricing
}

// ModelPrice 单个模型的完整价格表（多个 ItemCode）。
type ModelPrice struct {
	// ModelID 模型唯一标识，如 "gpt-4o"、"claude-3-5-sonnet"。
	ModelID string `json:"modelId"`

	// Items 各计费项；通常包含 prompt_tokens / completion_tokens / prompt_cached_tokens
	// / prompt_write_cached_tokens 几项。
	Items []ModelPriceItem `json:"items"`
}

// FindItem 按 ItemCode 查找条目；找不到返回 nil。
func (m *ModelPrice) FindItem(code PriceItemCode) *ModelPriceItem {
	for i := range m.Items {
		if m.Items[i].ItemCode == code {
			return &m.Items[i]
		}
	}
	return nil
}

// Validate 校验整张价格表。
func (m *ModelPrice) Validate() error {
	if m.ModelID == "" {
		return errors.New("pricing: modelId 不能为空")
	}
	for i, item := range m.Items {
		if err := item.Pricing.Validate(); err != nil {
			return fmt.Errorf("pricing: item[%d] (%s): %w", i, item.ItemCode, err)
		}
		for j, v := range item.PromptWriteCacheVariants {
			if err := v.Pricing.Validate(); err != nil {
				return fmt.Errorf("pricing: item[%d] variant[%d] (%s): %w", i, j, v.VariantCode, err)
			}
		}
	}
	return nil
}
