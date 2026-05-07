package pricing

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// TestModelPriceJSONRoundTrip 验证 ModelPrice 与三种 mode 的 JSON 序列化/反序列化对称性。
func TestModelPriceJSONRoundTrip(t *testing.T) {
	flat := decimal.NewFromFloat(0.05)
	per := decimal.NewFromFloat(2.50)
	src := ModelPrice{
		ModelID: "gpt-4o",
		Items: []ModelPriceItem{
			{
				ItemCode: PriceItemCodeUsage,
				Pricing: Pricing{
					Mode:         PricingModeUsagePerUnit,
					UsagePerUnit: &per,
				},
			},
			{
				ItemCode: PriceItemCodeCompletion,
				Pricing: Pricing{
					Mode:    PricingModeFlatFee,
					FlatFee: &flat,
				},
			},
			{
				ItemCode: PriceItemCodePromptCachedToken,
				Pricing: Pricing{
					Mode: PricingModeTiered,
					UsageTiered: &TieredPricing{
						Tiers: []PriceTier{
							{UpTo: ptr(int64(128_000)), PricePerUnit: decimal.NewFromFloat(1.25)},
							{PricePerUnit: decimal.NewFromFloat(2.50)},
						},
					},
				},
			},
		},
	}

	require.NoError(t, src.Validate())

	raw, err := json.Marshal(src)
	require.NoError(t, err)

	var got ModelPrice
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, src.ModelID, got.ModelID)
	require.Len(t, got.Items, 3)

	// usage_per_unit 反序列化保留精度
	require.Equal(t, PricingModeUsagePerUnit, got.Items[0].Pricing.Mode)
	require.NotNil(t, got.Items[0].Pricing.UsagePerUnit)
	require.True(t, got.Items[0].Pricing.UsagePerUnit.Equal(per))

	// flat_fee
	require.Equal(t, PricingModeFlatFee, got.Items[1].Pricing.Mode)
	require.True(t, got.Items[1].Pricing.FlatFee.Equal(flat))

	// usage_tiered
	require.Equal(t, PricingModeTiered, got.Items[2].Pricing.Mode)
	require.NotNil(t, got.Items[2].Pricing.UsageTiered)
	require.Len(t, got.Items[2].Pricing.UsageTiered.Tiers, 2)
	require.NotNil(t, got.Items[2].Pricing.UsageTiered.Tiers[0].UpTo)
	require.Equal(t, int64(128_000), *got.Items[2].Pricing.UsageTiered.Tiers[0].UpTo)
	require.Nil(t, got.Items[2].Pricing.UsageTiered.Tiers[1].UpTo)
}

// TestPricingValidate 错误模式 + 阶梯顺序校验。
func TestPricingValidate(t *testing.T) {
	t.Run("flat_fee 缺 flatFee", func(t *testing.T) {
		require.Error(t, Pricing{Mode: PricingModeFlatFee}.Validate())
	})
	t.Run("usage_per_unit 缺 usagePerUnit", func(t *testing.T) {
		require.Error(t, Pricing{Mode: PricingModeUsagePerUnit}.Validate())
	})
	t.Run("usage_tiered 缺 tiers", func(t *testing.T) {
		require.Error(t, Pricing{Mode: PricingModeTiered, UsageTiered: &TieredPricing{}}.Validate())
	})
	t.Run("末位 tier upTo 必须 nil", func(t *testing.T) {
		p := Pricing{
			Mode: PricingModeTiered,
			UsageTiered: &TieredPricing{
				Tiers: []PriceTier{
					{UpTo: ptr(int64(1_000_000)), PricePerUnit: decimal.NewFromFloat(1)},
					{UpTo: ptr(int64(2_000_000)), PricePerUnit: decimal.NewFromFloat(2)},
				},
			},
		}
		require.Error(t, p.Validate())
	})
	t.Run("非末位 tier upTo 不能 nil", func(t *testing.T) {
		p := Pricing{
			Mode: PricingModeTiered,
			UsageTiered: &TieredPricing{
				Tiers: []PriceTier{
					{PricePerUnit: decimal.NewFromFloat(1)},
					{PricePerUnit: decimal.NewFromFloat(2)},
				},
			},
		}
		require.Error(t, p.Validate())
	})
	t.Run("未知 mode", func(t *testing.T) {
		require.Error(t, Pricing{Mode: PricingMode("nope")}.Validate())
	})
	t.Run("ModelPrice modelId 必填", func(t *testing.T) {
		flat := decimal.NewFromFloat(1)
		mp := ModelPrice{Items: []ModelPriceItem{
			{ItemCode: PriceItemCodeUsage, Pricing: Pricing{Mode: PricingModeFlatFee, FlatFee: &flat}},
		}}
		require.Error(t, mp.Validate())
	})
}

// TestFindPromptWriteCacheVariantPricing 找不到时回退到 item.Pricing。
func TestFindPromptWriteCacheVariantPricing(t *testing.T) {
	base := decimal.NewFromFloat(3.75)
	hour := decimal.NewFromFloat(6.00)
	item := ModelPriceItem{
		ItemCode: PriceItemCodeWriteCachedTokens,
		Pricing: Pricing{
			Mode:         PricingModeUsagePerUnit,
			UsagePerUnit: &base,
		},
		PromptWriteCacheVariants: []PromptWriteCacheVariant{
			{
				VariantCode: PromptWriteCacheVariantCode1Hour,
				Pricing: Pricing{
					Mode:         PricingModeUsagePerUnit,
					UsagePerUnit: &hour,
				},
			},
		},
	}
	got1h := item.FindPromptWriteCacheVariantPricing(PromptWriteCacheVariantCode1Hour)
	require.True(t, got1h.UsagePerUnit.Equal(hour))
	got5m := item.FindPromptWriteCacheVariantPricing(PromptWriteCacheVariantCode5Min)
	require.True(t, got5m.UsagePerUnit.Equal(base), "5m 缺失时应回退到 item.Pricing")
}

func ptr[T any](v T) *T { return &v }
