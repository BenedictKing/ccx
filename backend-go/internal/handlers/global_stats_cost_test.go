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
