package metrics

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/types"
)

func TestRecordAxonHubForwardingUsageAggregatesByInboundFamilyAndMode(t *testing.T) {
	m := NewMetricsManager()
	baseURL := "https://upstream.example.com"
	apiKey := "sk-test"

	m.RecordAxonHubForwardingUsage(baseURL, apiKey, "claude", "messages", AxonHubForwardingModeSameFormatRaw, &types.Usage{
		InputTokens:  10,
		OutputTokens: 3,
	})
	m.RecordAxonHubForwardingUsage(baseURL, apiKey, "claude", "messages", AxonHubForwardingModeSameFormatRaw, nil)
	m.RecordAxonHubForwardingUsage(baseURL, apiKey, "claude", "responses", AxonHubForwardingModeCrossFormatConverted, &types.Usage{
		PromptTokensTotal:          12,
		CacheReadInputTokens:       5,
		OutputTokens:               4,
		CacheCreation5mInputTokens: 2,
	})

	resp := m.ToResponseMultiURL(0, []string{baseURL}, []string{apiKey}, "claude", 0)
	stats := resp.AxonHubForwarding
	if stats == nil {
		t.Fatal("AxonHubForwarding = nil, want stats")
	}
	if stats.RequestCount != 3 {
		t.Fatalf("requestCount = %d, want 3", stats.RequestCount)
	}
	if stats.InputTokens != 17 || stats.OutputTokens != 7 || stats.CacheCreationTokens != 2 || stats.CacheReadTokens != 5 {
		t.Fatalf("tokens = input:%d output:%d cacheCreate:%d cacheRead:%d, want 17/7/2/5",
			stats.InputTokens, stats.OutputTokens, stats.CacheCreationTokens, stats.CacheReadTokens)
	}
	if len(stats.ByRoute) != 2 {
		t.Fatalf("byRoute len = %d, want 2", len(stats.ByRoute))
	}

	messages := stats.ByRoute[0]
	if messages.InboundFamily != "messages" || messages.Mode != AxonHubForwardingModeSameFormatRaw {
		t.Fatalf("first route = %+v, want messages same_format_raw", messages)
	}
	if messages.RequestCount != 2 || messages.InputTokens != 10 || messages.OutputTokens != 3 {
		t.Fatalf("messages route = %+v, want count=2 input=10 output=3", messages)
	}

	responses := stats.ByRoute[1]
	if responses.InboundFamily != "responses" || responses.Mode != AxonHubForwardingModeCrossFormatConverted {
		t.Fatalf("second route = %+v, want responses cross_format_converted", responses)
	}
	if responses.RequestCount != 1 || responses.InputTokens != 7 || responses.CacheReadTokens != 5 {
		t.Fatalf("responses route = %+v, want count=1 input=7 cacheRead=5", responses)
	}
}

func TestRecordAxonHubForwardingUsageAggregatesHistoricalKeys(t *testing.T) {
	m := NewMetricsManager()
	baseURL := "https://upstream.example.com"

	m.RecordAxonHubForwardingUsage(baseURL, "sk-active", "claude", "messages", AxonHubForwardingModeSameFormatRaw, &types.Usage{
		InputTokens:  1,
		OutputTokens: 2,
	})
	m.RecordAxonHubForwardingUsage(baseURL, "sk-rotated", "claude", "messages", AxonHubForwardingModeSameFormatRaw, &types.Usage{
		InputTokens:  3,
		OutputTokens: 4,
	})

	resp := m.ToResponseMultiURL(0, []string{baseURL}, []string{"sk-active"}, "claude", 0, []string{"sk-rotated"})
	stats := resp.AxonHubForwarding
	if stats == nil {
		t.Fatal("AxonHubForwarding = nil, want active and historical stats")
	}
	if stats.RequestCount != 2 {
		t.Fatalf("requestCount = %d, want 2", stats.RequestCount)
	}
	if stats.InputTokens != 4 || stats.OutputTokens != 6 {
		t.Fatalf("tokens = input:%d output:%d, want 4/6", stats.InputTokens, stats.OutputTokens)
	}
	if len(stats.ByRoute) != 1 {
		t.Fatalf("byRoute len = %d, want 1", len(stats.ByRoute))
	}
	if stats.ByRoute[0].RequestCount != 2 {
		t.Fatalf("route requestCount = %d, want 2", stats.ByRoute[0].RequestCount)
	}
}
