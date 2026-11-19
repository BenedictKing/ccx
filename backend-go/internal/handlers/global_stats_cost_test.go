package handlers

import (
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

func TestGlobalCostKeepsSameModelChannelPricesSeparate(t *testing.T) {
	price := func(v float64) *float64 { return &v }
	capability := func(v float64) map[string]config.UpstreamModelCapability {
		return map[string]config.UpstreamModelCapability{
			"private-model": {Pricing: &config.ModelPricing{Currency: "USD", InputCacheMissPrice: price(v)}},
		}
	}
	cfg := config.Config{ChatUpstream: []config.UpstreamConfig{
		{BaseURL: "https://first.example", APIKeys: []string{"sk-first"}, ServiceType: "openai", ModelCapabilities: capability(1)},
		{BaseURL: "https://second.example", APIKeys: []string{"sk-second"}, ServiceType: "openai", ModelCapabilities: capability(3)},
	}}
	calculate := globalCostCalculator(cfg, scheduler.ChannelKindChat)
	first := metrics.GenerateMetricsIdentityKey("https://first.example", "sk-first", "openai")
	second := metrics.GenerateMetricsIdentityKey("https://second.example", "sk-second", "openai")
	if got := calculate(first, "private-model", 1_000_000, 0, 0, 0); got != 1 {
		t.Fatalf("first channel cost = %v, want 1", got)
	}
	if got := calculate(second, "private-model", 1_000_000, 0, 0, 0); got != 3 {
		t.Fatalf("second channel cost = %v, want 3", got)
	}
	ts := time.Now().Truncate(5 * time.Minute)
	buckets := []metrics.ModelAggregatedBucket{
		{MetricsKey: first, Model: "private-model", Timestamp: ts, TotalRequests: 1, SuccessCount: 1, InputTokens: 1_000_000},
		{MetricsKey: second, Model: "private-model", Timestamp: ts, TotalRequests: 1, SuccessCount: 1, InputTokens: 1_000_000},
	}
	points := buildModelDataPointsFromAggregatedBuckets(buckets, map[int64]int{ts.Unix(): 0}, []metrics.GlobalHistoryDataPoint{{Timestamp: ts}}, calculate)
	if got := points["private-model"][0]; got.RequestCount != 2 || got.InputTokens != 2_000_000 || got.CostUSD != 4 {
		t.Fatalf("merged model stats = %+v, want 2 requests and USD 4", got)
	}
}

func TestGlobalCostAppliesKeyGroupMultipliers(t *testing.T) {
	zero, discount, price := 0.0, 0.5, 2.0
	upstream := config.UpstreamConfig{
		BaseURL: "https://relay.example", APIKeys: []string{"free", "discount", "standard"},
		APIKeyConfigs:   []config.APIKeyConfig{{Key: "free", GroupMultiplier: &zero}, {Key: "discount", GroupMultiplier: &discount}},
		DisabledAPIKeys: []config.DisabledKeyInfo{{Key: "disabled-free", Config: &config.APIKeyConfig{Key: "disabled-free", GroupMultiplier: &zero}}},
		ModelCapabilities: map[string]config.UpstreamModelCapability{
			"private-model": {Pricing: &config.ModelPricing{Currency: "USD", InputCacheMissPrice: &price}},
		},
	}
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{upstream}, ChatUpstream: []config.UpstreamConfig{upstream},
		ResponsesUpstream: []config.UpstreamConfig{upstream}, GeminiUpstream: []config.UpstreamConfig{upstream},
		ImagesUpstream: []config.UpstreamConfig{upstream}, VectorsUpstream: []config.UpstreamConfig{upstream},
	}
	for _, kind := range []scheduler.ChannelKind{
		scheduler.ChannelKindMessages, scheduler.ChannelKindChat, scheduler.ChannelKindResponses,
		scheduler.ChannelKindGemini, scheduler.ChannelKindImages, scheduler.ChannelKindVectors,
	} {
		t.Run(string(kind), func(t *testing.T) {
			calculate := globalCostCalculator(cfg, kind)
			serviceType := scheduler.NormalizedMetricsServiceType(kind, upstream.ServiceType)
			for _, tc := range []struct {
				key  string
				want float64
			}{{"free", 0}, {"discount", 1}, {"standard", 2}, {"disabled-free", 0}} {
				for _, identity := range metrics.GenerateMetricsLookupKeys(upstream.BaseURL, tc.key, serviceType) {
					if got := calculate(identity, "private-model", 1_000_000, 0, 0, 0); got != tc.want {
						t.Errorf("key %s cost = %v, want %v", tc.key, got, tc.want)
					}
				}
			}
		})
	}
}

func TestGlobalCostKeepsConsistentMultiplierForSharedKeys(t *testing.T) {
	zero, discount, globalPrice := 0.0, 0.5, 3.0
	for _, tc := range []struct {
		name        string
		multipliers []*float64
		want        float64
	}{
		{"free", []*float64{&zero, &zero, &zero}, 0},
		{"discount", []*float64{&discount, &discount}, 1.5},
		{"unset", []*float64{nil, nil}, 3},
		{"conflict", []*float64{&zero, &discount, &zero}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{UpstreamModelCapabilities: map[string]config.UpstreamModelCapability{
				"private-model": {Pricing: &config.ModelPricing{Currency: "USD", InputCacheMissPrice: &globalPrice}},
			}}
			for _, multiplier := range tc.multipliers {
				cfg.ChatUpstream = append(cfg.ChatUpstream, config.UpstreamConfig{
					BaseURL: "https://shared.example", APIKeys: []string{"sk-shared"}, ServiceType: "openai",
					APIKeyConfigs: []config.APIKeyConfig{{Key: "sk-shared", GroupMultiplier: multiplier}},
				})
			}
			calculate := globalCostCalculator(cfg, scheduler.ChannelKindChat)
			for _, identity := range metrics.GenerateMetricsLookupKeys("https://shared.example", "sk-shared", "openai") {
				if got := calculate(identity, "private-model", 1_000_000, 0, 0, 0); got != tc.want {
					t.Errorf("shared cost = %v, want %v", got, tc.want)
				}
			}
			if got := calculate("deleted-key", "private-model", 1_000_000, 0, 0, 0); got != 3 {
				t.Errorf("unattributed cost = %v, want 3", got)
			}
		})
	}
}
