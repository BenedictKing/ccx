package pricing

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/BenedictKing/ccx/internal/types"
)

// 帮助函数：使用 string 构造 decimal 单价（保留精度）。
func dec(s string) decimal.Decimal {
	d, _ := decimal.NewFromString(s)
	return d
}

func decPtr(s string) *decimal.Decimal {
	d := dec(s)
	return &d
}

// 构造 ModelPrice 助手：完整三种 mode + cache 写变体。
func newSampleSonnetPrice() *ModelPrice {
	return &ModelPrice{
		ModelID: "claude-3-5-sonnet",
		Items: []ModelPriceItem{
			{
				ItemCode: PriceItemCodeUsage,
				Pricing:  Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: decPtr("3.00")},
			},
			{
				ItemCode: PriceItemCodeCompletion,
				Pricing:  Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: decPtr("15.00")},
			},
			{
				ItemCode: PriceItemCodePromptCachedToken,
				Pricing:  Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: decPtr("0.30")},
			},
			{
				ItemCode: PriceItemCodeWriteCachedTokens,
				Pricing:  Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: decPtr("3.75")},
				PromptWriteCacheVariants: []PromptWriteCacheVariant{
					{VariantCode: PromptWriteCacheVariantCode5Min, Pricing: Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: decPtr("3.75")}},
					{VariantCode: PromptWriteCacheVariantCode1Hour, Pricing: Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: decPtr("6.00")}},
				},
			},
		},
	}
}

// usage_per_unit + 多模型表驱动用例。
func TestCalculate_UsagePerUnit(t *testing.T) {
	cases := []struct {
		name      string
		price     *ModelPrice
		usage     types.Usage
		wantTotal string
		wantItems int
	}{
		{
			name: "gpt-4o 简单 1k+1k",
			price: &ModelPrice{
				ModelID: "gpt-4o",
				Items: []ModelPriceItem{
					{ItemCode: PriceItemCodeUsage, Pricing: Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: decPtr("2.50")}},
					{ItemCode: PriceItemCodeCompletion, Pricing: Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: decPtr("10.00")}},
				},
			},
			usage:     types.Usage{PromptTokens: 1_000_000, CompletionTokens: 1_000_000},
			wantTotal: "12.5", // 2.5 + 10
			wantItems: 2,
		},
		{
			name: "gpt-4o-mini 0 prompt → 输入项 0 但仍输出占位 + completion=0.6",
			price: &ModelPrice{
				ModelID: "gpt-4o-mini",
				Items: []ModelPriceItem{
					{ItemCode: PriceItemCodeUsage, Pricing: Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: decPtr("0.15")}},
					{ItemCode: PriceItemCodeCompletion, Pricing: Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: decPtr("0.60")}},
				},
			},
			usage:     types.Usage{CompletionTokens: 1_000_000},
			wantTotal: "0.6",
			wantItems: 2,
		},
		{
			name: "o1 1.5M prompt + 0.5M completion",
			price: &ModelPrice{
				ModelID: "o1",
				Items: []ModelPriceItem{
					{ItemCode: PriceItemCodeUsage, Pricing: Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: decPtr("15.00")}},
					{ItemCode: PriceItemCodeCompletion, Pricing: Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: decPtr("60.00")}},
				},
			},
			usage:     types.Usage{PromptTokens: 1_500_000, CompletionTokens: 500_000},
			wantTotal: "52.5", // 1.5*15 + 0.5*60
			wantItems: 2,
		},
		{
			name: "claude-3-5-sonnet 含 cache 读：prompt=10K, cached=4K, completion=2K",
			price: newSampleSonnetPrice(),
			usage: types.Usage{
				// Anthropic 口径：InputTokens 已扣 cached
				InputTokens:          6_000,
				OutputTokens:         2_000,
				CacheReadInputTokens: 4_000,
			},
			// 输入计费 quantity = max(0, 10K - 4K - 0) = 6K → 6K/1M * 3 = 0.018
			// completion 2K * 15/1M = 0.03
			// cached 4K * 0.30/1M = 0.0012
			// total = 0.0492 (write_cached 项 quantity=0 仍占位)
			wantTotal: "0.0492",
			wantItems: 4,
		},
		{
			name: "全 0 usage：无 cache 写变体则给 0 占位",
			price: &ModelPrice{
				ModelID: "gpt-4o",
				Items: []ModelPriceItem{
					{ItemCode: PriceItemCodeUsage, Pricing: Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: decPtr("2.50")}},
					{ItemCode: PriceItemCodeCompletion, Pricing: Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: decPtr("10.00")}},
					{ItemCode: PriceItemCodeWriteCachedTokens, Pricing: Pricing{Mode: PricingModeUsagePerUnit, UsagePerUnit: decPtr("3.75")}},
				},
			},
			usage:     types.Usage{},
			wantTotal: "0",
			wantItems: 3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			total, items, err := Calculate(tc.usage, tc.price)
			require.NoError(t, err)
			require.Len(t, items, tc.wantItems)
			require.True(t, total.Equal(dec(tc.wantTotal)),
				"total mismatch: got=%s want=%s", total.String(), tc.wantTotal)
		})
	}
}

// flat_fee 模式。
func TestCalculate_FlatFee(t *testing.T) {
	mp := &ModelPrice{
		ModelID: "fake-flat",
		Items: []ModelPriceItem{
			{ItemCode: PriceItemCodeUsage, Pricing: Pricing{Mode: PricingModeFlatFee, FlatFee: decPtr("0.05")}},
		},
	}
	t.Run("非零 quantity 收 flat_fee", func(t *testing.T) {
		total, items, err := Calculate(types.Usage{PromptTokens: 1234567}, mp)
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.True(t, total.Equal(dec("0.05")))
	})
	t.Run("零 quantity 也收 flat_fee（与 usage 无关）", func(t *testing.T) {
		total, _, err := Calculate(types.Usage{}, mp)
		require.NoError(t, err)
		require.True(t, total.Equal(dec("0.05")))
	})
}

// usage_tiered + gemini-1.5-pro 真实跨段。
func TestCalculate_Tiered(t *testing.T) {
	mp := &ModelPrice{
		ModelID: "gemini-1.5-pro",
		Items: []ModelPriceItem{
			{
				ItemCode: PriceItemCodeUsage,
				Pricing: Pricing{
					Mode: PricingModeTiered,
					UsageTiered: &TieredPricing{
						Tiers: []PriceTier{
							{UpTo: ptr(int64(128_000)), PricePerUnit: dec("1.25")},
							{PricePerUnit: dec("2.50")},
						},
					},
				},
			},
			{
				ItemCode: PriceItemCodeCompletion,
				Pricing: Pricing{
					Mode: PricingModeTiered,
					UsageTiered: &TieredPricing{
						Tiers: []PriceTier{
							{UpTo: ptr(int64(128_000)), PricePerUnit: dec("5.00")},
							{PricePerUnit: dec("10.00")},
						},
					},
				},
			},
		},
	}
	t.Run("仅落第 1 段", func(t *testing.T) {
		total, items, err := Calculate(types.Usage{PromptTokens: 64_000}, mp)
		require.NoError(t, err)
		require.Len(t, items, 2)
		// 64K * 1.25/1M = 0.08
		require.True(t, total.Equal(dec("0.08")), "got=%s", total.String())
		require.Len(t, items[0].TierBreakdown, 1)
		require.Equal(t, int64(64_000), items[0].TierBreakdown[0].Units)
	})
	t.Run("跨段 prompt=200K", func(t *testing.T) {
		total, items, err := Calculate(types.Usage{PromptTokens: 200_000}, mp)
		require.NoError(t, err)
		// 第 1 段：128K * 1.25 / 1M = 0.16
		// 第 2 段：72K  * 2.50 / 1M = 0.18
		// total = 0.34
		require.True(t, total.Equal(dec("0.34")), "got=%s", total.String())
		require.Len(t, items[0].TierBreakdown, 2)
		require.Equal(t, int64(128_000), items[0].TierBreakdown[0].Units)
		require.Equal(t, int64(72_000), items[0].TierBreakdown[1].Units)
	})
	t.Run("zero quantity 不输出 breakdown", func(t *testing.T) {
		total, items, err := Calculate(types.Usage{}, mp)
		require.NoError(t, err)
		require.True(t, total.Equal(decimal.Zero))
		require.Len(t, items, 2)
		require.Empty(t, items[0].TierBreakdown)
	})
}

// cache 写变体：5min + 1hour 都非零时输出 2 条。
func TestCalculate_CacheWriteVariants(t *testing.T) {
	mp := newSampleSonnetPrice()
	usage := types.Usage{
		PromptTokens:               20_000,
		CompletionTokens:           1_000,
		CacheReadInputTokens:       1_000,
		CacheCreationInputTokens:   16_000, // 总写 cache（OpenAI 兼容字段，会被变体覆盖）
		CacheCreation5mInputTokens: 10_000,
		CacheCreation1hInputTokens: 6_000,
	}
	total, items, err := Calculate(usage, mp)
	require.NoError(t, err)
	// 期望 4 条：prompt + completion + cached_read + 2 个 write 变体 = 5 条
	require.Len(t, items, 5)
	// total:
	//   prompt qty = 20K - 1K - 16K = 3K → 3K * 3 / 1M = 0.009
	//   completion = 1K * 15 / 1M = 0.015
	//   cached_read = 1K * 0.30 / 1M = 0.0003
	//   write 5m = 10K * 3.75 / 1M = 0.0375
	//   write 1h = 6K * 6 / 1M = 0.036
	// sum = 0.0978
	require.True(t, total.Equal(dec("0.0978")), "got=%s", total.String())

	var have5m, have1h bool
	for _, it := range items {
		if it.ItemCode == PriceItemCodeWriteCachedTokens {
			switch it.PromptWriteCacheVariantCode {
			case PromptWriteCacheVariantCode5Min:
				have5m = true
				require.Equal(t, int64(10_000), it.Quantity)
			case PromptWriteCacheVariantCode1Hour:
				have1h = true
				require.Equal(t, int64(6_000), it.Quantity)
			}
		}
	}
	require.True(t, have5m && have1h, "write cache 5m/1h variants 应同时存在")
}

// 当变体 token 都为 0 时回退到共享 pricing。
func TestCalculate_CacheWriteFallbackShared(t *testing.T) {
	mp := newSampleSonnetPrice()
	usage := types.Usage{
		PromptTokens:             10_000,
		CacheCreationInputTokens: 4_000,
	}
	total, items, err := Calculate(usage, mp)
	require.NoError(t, err)
	// prompt qty = 10K - 0 - 4K = 6K * 3 / 1M = 0.018
	// write_cached 4K * 3.75 / 1M = 0.015
	// total = 0.033
	require.True(t, total.Equal(dec("0.033")), "got=%s", total.String())
	// 应当只有 4 条：prompt + completion(=0) + cached_read(=0) + write_shared
	require.Len(t, items, 4)
	// 找到 write 项确认无 variant code
	for _, it := range items {
		if it.ItemCode == PriceItemCodeWriteCachedTokens {
			require.Equal(t, PromptWriteCacheVariantCode(""), it.PromptWriteCacheVariantCode)
			require.Equal(t, int64(4_000), it.Quantity)
		}
	}
}

func TestCalculate_NilPriceErrors(t *testing.T) {
	_, _, err := Calculate(types.Usage{}, nil)
	require.Error(t, err)
}
