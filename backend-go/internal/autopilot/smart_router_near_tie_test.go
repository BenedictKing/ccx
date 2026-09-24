package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/scheduler"
)

func nearTieTestEntry(uid string, score float64, fidelity, model, mapped string, health HealthState) scoredChannelEntry {
	return scoredChannelEntry{
		entry: channelScoreEntry{
			ChannelUID:       uid,
			ModelID:          model,
			MappedModel:      mapped,
			ProtocolFidelity: fidelity,
			HealthState:      health,
			Route:            scheduler.ChannelRouteRef{Kind: "messages", Index: 0, ChannelUID: uid},
		},
		scored: ScoredCandidate{ChannelUID: uid, Score: score},
	}
}

func TestSortScoredChannelEntriesNearTiePrefersNativeExactModel(t *testing.T) {
	entries := []scoredChannelEntry{
		nearTieTestEntry("converted", 9.082085, "converted", "deepseek-v4-flash", "deepseek-v4-flash", HealthStateHealthy),
		nearTieTestEntry("native", 8.8585, "native", "claude-sonnet-5", "", HealthStateUnknown),
	}

	if !sortScoredChannelEntries(entries, "claude-sonnet-5") {
		t.Fatal("near-tie semantic preference should have been applied")
	}
	if got := entries[0].entry.ChannelUID; got != "native" {
		t.Fatalf("near-tie selection = %q, want native exact-model candidate", got)
	}
}

func TestSortScoredChannelEntriesFarScoreKeepsScoreOrder(t *testing.T) {
	entries := []scoredChannelEntry{
		nearTieTestEntry("converted", 10, "converted", "deepseek-v4-flash", "deepseek-v4-flash", HealthStateHealthy),
		nearTieTestEntry("native", 8, "native", "claude-sonnet-5", "", HealthStateUnknown),
	}

	if sortScoredChannelEntries(entries, "claude-sonnet-5") {
		t.Fatal("semantic preference must not apply across a material score gap")
	}
	if got := entries[0].entry.ChannelUID; got != "converted" {
		t.Fatalf("material score order = %q, want converted", got)
	}
}

func TestSortScoredChannelEntriesNearTiePrefersExactModelAfterFidelity(t *testing.T) {
	entries := []scoredChannelEntry{
		nearTieTestEntry("mapped", 8.9, "native", "provider-sonnet", "provider-sonnet", HealthStateHealthy),
		nearTieTestEntry("exact", 8.8, "native", "claude-sonnet-5", "", HealthStateUnknown),
	}

	if !sortScoredChannelEntries(entries, "claude-sonnet-5") {
		t.Fatal("near-tie exact-model preference should have been applied")
	}
	if got := entries[0].entry.ChannelUID; got != "exact" {
		t.Fatalf("near-tie exact-model selection = %q, want exact", got)
	}
}
