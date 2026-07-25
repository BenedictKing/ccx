package autopilot

import "testing"

func TestAggregateChannelIncludesObservedFirstByteLatency(t *testing.T) {
	profiles := []*KeyEndpointProfile{
		{
			ChannelID:             3,
			ChannelKind:           "messages",
			HealthState:           HealthStateHealthy,
			SuccessRate15m:        0.99,
			FirstByteSampleCount:  12,
			P95FirstByteLatencyMs: 2_400,
		},
		{
			ChannelID:             3,
			ChannelKind:           "messages",
			HealthState:           HealthStateHealthy,
			SuccessRate15m:        0.95,
			FirstByteSampleCount:  8,
			P95FirstByteLatencyMs: 3_600,
		},
	}

	item := aggregateChannel("ch_test", profiles)

	if item.FirstByteSampleCount != 20 {
		t.Fatalf("FirstByteSampleCount = %d, want 20", item.FirstByteSampleCount)
	}
	if item.P95FirstByteLatencyMs != 3_600 {
		t.Fatalf("P95FirstByteLatencyMs = %d, want 3600", item.P95FirstByteLatencyMs)
	}
	if item.SpeedTier != string(SpeedTierSlow) {
		t.Fatalf("SpeedTier = %q, want %q", item.SpeedTier, SpeedTierSlow)
	}
}

func TestAggregateChannelOmitsLatencyWithoutSamples(t *testing.T) {
	item := aggregateChannel("ch_empty", []*KeyEndpointProfile{{
		ChannelID:   1,
		ChannelKind: "chat",
		HealthState: HealthStateUnknown,
	}})

	if item.FirstByteSampleCount != 0 || item.P95FirstByteLatencyMs != 0 || item.SpeedTier != "" {
		t.Fatalf("unexpected latency summary: samples=%d p95=%d tier=%q",
			item.FirstByteSampleCount, item.P95FirstByteLatencyMs, item.SpeedTier)
	}
}
