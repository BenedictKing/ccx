package handlers

import (
	"math"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/utils"
)

func TestApplyChannelLogCosts(t *testing.T) {
	discount, price := 0.5, 2.0
	upstream := &config.UpstreamConfig{
		Name: "relay", BaseURL: "https://relay.example", APIKeys: []string{"sk-discount-aaaaaaaa", "sk-standard-bbbbbbbb"},
		APIKeyConfigs: []config.APIKeyConfig{{Key: "sk-discount-aaaaaaaa", GroupMultiplier: &discount}},
		ModelCapabilities: map[string]config.UpstreamModelCapability{
			"private-model": {Pricing: &config.ModelPricing{Currency: "USD", InputCacheMissPrice: &price}},
		},
	}

	tests := []struct {
		name    string
		log     metrics.ChannelLog
		wantUSD float64
	}{
		{"按 Key 分组倍率计费", metrics.ChannelLog{Model: "private-model", KeyMask: utils.MaskAPIKey("sk-discount-aaaaaaaa"), InputTokens: 1_000_000}, 1},
		{"未配置倍率按 1 计费", metrics.ChannelLog{Model: "private-model", KeyMask: utils.MaskAPIKey("sk-standard-bbbbbbbb"), InputTokens: 1_000_000}, 2},
		{"脱敏 Key 无法反查时按 1 计费", metrics.ChannelLog{Model: "private-model", KeyMask: "sk-unknown", InputTokens: 1_000_000}, 2},
		{"无 usage 不计费", metrics.ChannelLog{Model: "private-model", KeyMask: utils.MaskAPIKey("sk-standard-bbbbbbbb")}, 0},
		{"无定价模型不计费", metrics.ChannelLog{Model: "unpriced-model-x", KeyMask: utils.MaskAPIKey("sk-standard-bbbbbbbb"), InputTokens: 1_000_000}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logEntry := tc.log
			applyChannelLogCosts([]*metrics.ChannelLog{&logEntry}, upstream, "chat", nil)
			if math.Abs(logEntry.CostUSD-tc.wantUSD) > 1e-9 {
				t.Fatalf("CostUSD = %v, want %v", logEntry.CostUSD, tc.wantUSD)
			}
		})
	}
}

func TestApplyChannelLogCostsSeparatesCollidingKeyMasks(t *testing.T) {
	discountKey, standardKey := "sk-abc-discount-xyz", "sk-abc-standard-xyz"
	if utils.MaskAPIKey(discountKey) != utils.MaskAPIKey(standardKey) {
		t.Fatal("fixture does not collide")
	}
	discount, price := 0.5, 2.0
	upstream := &config.UpstreamConfig{
		BaseURL: "https://relay.example", APIKeys: []string{discountKey, standardKey},
		APIKeyConfigs:     []config.APIKeyConfig{{Key: discountKey, GroupMultiplier: &discount}},
		ModelCapabilities: map[string]config.UpstreamModelCapability{"private-model": {Pricing: &config.ModelPricing{Currency: "USD", InputCacheMissPrice: &price}}},
	}
	for _, tc := range []struct {
		name, metricsKey string
		want             float64
	}{
		{"discount", metrics.GenerateMetricsIdentityKey(upstream.BaseURL, discountKey, "chat"), 1},
		{"standard", metrics.GenerateMetricsIdentityKey(upstream.BaseURL, standardKey, "chat"), 2},
		{"legacy-ambiguous", "", 2},
		{"unknown-identity", "unknown", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := &metrics.ChannelLog{MetricsKey: tc.metricsKey, Model: "private-model", KeyMask: utils.MaskAPIKey(standardKey), InputTokens: 1_000_000}
			if tc.metricsKey != "" {
				store := metrics.NewChannelLogStore()
				store.Record(tc.metricsKey, log)
				log = store.Get(tc.metricsKey)[0]
			}
			applyChannelLogCosts([]*metrics.ChannelLog{log}, upstream, "chat", nil)
			if math.Abs(log.CostUSD-tc.want) > 1e-9 {
				t.Fatalf("CostUSD=%v want=%v", log.CostUSD, tc.want)
			}
		})
	}
}
