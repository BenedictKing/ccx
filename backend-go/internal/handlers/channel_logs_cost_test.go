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
		GroupMultiplier: &discount,
		ModelCapabilities: map[string]config.UpstreamModelCapability{
			"private-model": {Pricing: &config.ModelPricing{Currency: "USD", InputCacheMissPrice: &price}},
		},
	}

	tests := []struct {
		name    string
		log     metrics.ChannelLog
		wantUSD float64
	}{
		{"按渠道分组倍率计费", metrics.ChannelLog{Model: "private-model", KeyMask: utils.MaskAPIKey("sk-discount-aaaaaaaa"), InputTokens: 1_000_000}, 1},
		{"其他 Key 使用同一倍率", metrics.ChannelLog{Model: "private-model", KeyMask: utils.MaskAPIKey("sk-standard-bbbbbbbb"), InputTokens: 1_000_000}, 1},
		{"历史 Key 无法反查时仍按渠道倍率", metrics.ChannelLog{Model: "private-model", KeyMask: "sk-unknown", InputTokens: 1_000_000}, 1},
		{"无 usage 不计费", metrics.ChannelLog{Model: "private-model", KeyMask: utils.MaskAPIKey("sk-standard-bbbbbbbb")}, 0},
		{"无定价模型不计费", metrics.ChannelLog{Model: "unpriced-model-x", KeyMask: utils.MaskAPIKey("sk-standard-bbbbbbbb"), InputTokens: 1_000_000}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logEntry := tc.log
			applyChannelLogCosts([]*metrics.ChannelLog{&logEntry}, upstream, nil)
			if math.Abs(logEntry.CostUSD-tc.wantUSD) > 1e-9 {
				t.Fatalf("CostUSD = %v, want %v", logEntry.CostUSD, tc.wantUSD)
			}
		})
	}
}
