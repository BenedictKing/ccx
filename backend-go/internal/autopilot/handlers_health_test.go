package autopilot

import "testing"

func TestAggregateChannelUsesWorstReliableConnectLatencyForSpeed(t *testing.T) {
	profiles := []*KeyEndpointProfile{
		{
			ChannelID:             3,
			ChannelKind:           "messages",
			HealthState:           HealthStateHealthy,
			SuccessRate15m:        0.99,
			ConnectSampleCount:    24,
			P95ConnectLatencyMs:   300,
			SpeedTier:             SpeedTierFast,
			FirstByteSampleCount:  12,
			P95FirstByteLatencyMs: 2_400,
		},
		{
			ChannelID:             3,
			ChannelKind:           "messages",
			HealthState:           HealthStateHealthy,
			SuccessRate15m:        0.95,
			ConnectSampleCount:    20,
			P95ConnectLatencyMs:   2_600,
			SpeedTier:             SpeedTierSlow,
			FirstByteSampleCount:  8,
			P95FirstByteLatencyMs: 3_600,
		},
	}

	item := aggregateChannel("ch_test", profiles)

	if item.ConnectSampleCount != 44 {
		t.Fatalf("ConnectSampleCount = %d, want 44", item.ConnectSampleCount)
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

func TestAggregateChannel_LowSampleOutlierNotFlaggedSlow(t *testing.T) {
	// 单 endpoint，n=7（< 20 门槛），其中一次 4s 慢连接把 P95 拉到最大值，
	// 但样本量不足以支撑“持续慢速”的判定，渠道级 SpeedTier 不应为 slow。
	profiles := []*KeyEndpointProfile{
		{
			ChannelID:           1,
			ChannelKind:         "messages",
			HealthState:         HealthStateHealthy,
			SuccessRate15m:      1,
			ConnectSampleCount:  7,
			P95ConnectLatencyMs: 4_000,
			SpeedTier:           SpeedTierNormal, // DeriveSpeedTierFromConnectStats 在 n<20 时回退为 normal
		},
	}

	item := aggregateChannel("ch_low_sample", profiles)

	if item.SpeedTier == string(SpeedTierSlow) {
		t.Fatalf("SpeedTier = %q, want not slow (n=7 样本不足)", item.SpeedTier)
	}
	// 展示层仍应给出可读的 P95 数值，供 tooltip 参考。
	if item.P95ConnectLatencyMs != 4_000 {
		t.Fatalf("P95ConnectLatencyMs = %d, want 4000 (仍展示原始值)", item.P95ConnectLatencyMs)
	}
	if item.ConnectSampleCount != 7 {
		t.Fatalf("ConnectSampleCount = %d, want 7", item.ConnectSampleCount)
	}
}

func TestAggregateChannel_HighSampleSlowEndpointFlagged(t *testing.T) {
	// 单 endpoint，n=25，P95 持续处于慢速区间，样本量已达门槛，应正确标红。
	profiles := []*KeyEndpointProfile{
		{
			ChannelID:           2,
			ChannelKind:         "messages",
			HealthState:         HealthStateHealthy,
			SuccessRate15m:      0.98,
			ConnectSampleCount:  25,
			P95ConnectLatencyMs: 3_000,
			SpeedTier:           SpeedTierSlow,
		},
	}

	item := aggregateChannel("ch_high_sample_slow", profiles)

	if item.SpeedTier != string(SpeedTierSlow) {
		t.Fatalf("SpeedTier = %q, want %q", item.SpeedTier, SpeedTierSlow)
	}
	if item.P95ConnectLatencyMs != 3_000 {
		t.Fatalf("P95ConnectLatencyMs = %d, want 3000", item.P95ConnectLatencyMs)
	}
}

func TestAggregateChannel_MultiEndpointLowSampleNotAggregatedIntoSlow(t *testing.T) {
	// 两个 endpoint 各自样本量都低于门槛（n=8, n=9），即使各自 P95 都很高，
	// 累加样本量（17）依然低于门槛，渠道级不应被判定为 slow。
	profiles := []*KeyEndpointProfile{
		{
			ChannelID:           4,
			ChannelKind:         "chat",
			HealthState:         HealthStateHealthy,
			ConnectSampleCount:  8,
			P95ConnectLatencyMs: 3_500,
			SpeedTier:           SpeedTierNormal,
		},
		{
			ChannelID:           4,
			ChannelKind:         "chat",
			HealthState:         HealthStateHealthy,
			ConnectSampleCount:  9,
			P95ConnectLatencyMs: 5_000,
			SpeedTier:           SpeedTierNormal,
		},
	}

	item := aggregateChannel("ch_multi_low_sample", profiles)

	if item.SpeedTier == string(SpeedTierSlow) {
		t.Fatalf("SpeedTier = %q, want not slow (各 endpoint 样本量均不足)", item.SpeedTier)
	}
	if item.ConnectSampleCount != 17 {
		t.Fatalf("ConnectSampleCount = %d, want 17", item.ConnectSampleCount)
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
