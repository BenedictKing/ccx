package autopilot

import "testing"

func TestAggregateChannelUsesWorstObservedConnectLatencyForSpeed(t *testing.T) {
	profiles := []*KeyEndpointProfile{
		{
			ChannelID:             3,
			ChannelKind:           "messages",
			HealthState:           HealthStateHealthy,
			SuccessRate15m:        0.99,
			ConnectSampleCount:    10,
			P95ConnectLatencyMs:   300,
			FirstByteSampleCount:  12,
			P95FirstByteLatencyMs: 2_400,
		},
		{
			ChannelID:             3,
			ChannelKind:           "messages",
			HealthState:           HealthStateHealthy,
			SuccessRate15m:        0.95,
			ConnectSampleCount:    5,
			P95ConnectLatencyMs:   2_600,
			FirstByteSampleCount:  8,
			P95FirstByteLatencyMs: 3_600,
		},
	}

	item := aggregateChannel("ch_test", profiles)

	if item.ConnectSampleCount != 15 {
		t.Fatalf("ConnectSampleCount = %d, want 15", item.ConnectSampleCount)
	}
	if item.P95ConnectLatencyMs != 2_600 {
		t.Fatalf("P95ConnectLatencyMs = %d, want 2600", item.P95ConnectLatencyMs)
	}
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

	if item.ConnectSampleCount != 0 || item.P95ConnectLatencyMs != 0 ||
		item.FirstByteSampleCount != 0 || item.P95FirstByteLatencyMs != 0 || item.SpeedTier != "" {
		t.Fatalf("unexpected latency summary: connect=%d/%d firstByte=%d/%d tier=%q",
			item.ConnectSampleCount, item.P95ConnectLatencyMs,
			item.FirstByteSampleCount, item.P95FirstByteLatencyMs, item.SpeedTier)
	}
}
