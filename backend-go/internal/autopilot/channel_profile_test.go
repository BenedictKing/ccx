package autopilot

import "testing"

func TestAggregateHealthState_MixedCapsAtDegraded(t *testing.T) {
	if got := AggregateHealthState([]DiagnosisResult{{State: HealthStateHealthy}, {State: HealthStateDead}}); got != HealthStateDegraded {
		t.Fatalf("mixed health aggregate = %s, want degraded", got)
	}
}

func TestAggregateChannelProfile_UsesEffectiveStabilityTierWhenSet(t *testing.T) {
	ep1 := KeyEndpointProfile{
		EndpointUID:            "ep-1",
		HealthState:            HealthStateHealthy,
		QualityTier:            QualityTierNormal,
		StabilityTier:          StabilityTierUnstable,
		EffectiveStabilityTier: StabilityTierStable,
		SpeedTier:              SpeedTierNormal,
		CostTier:               CostTierNormal,
		Confidence:             0.5,
	}
	ep2 := KeyEndpointProfile{
		EndpointUID:            "ep-2",
		HealthState:            HealthStateHealthy,
		QualityTier:            QualityTierNormal,
		StabilityTier:          StabilityTierNormal,
		EffectiveStabilityTier: StabilityTierNormal,
		SpeedTier:              SpeedTierNormal,
		CostTier:               CostTierNormal,
		Confidence:             0.5,
	}

	cp := AggregateChannelProfile("ch-1", 0, "messages", []KeyEndpointProfile{ep1, ep2})
	if cp.StabilityTier != StabilityTierStable {
		t.Errorf("聚合 StabilityTier 应使用 EffectiveStabilityTier: got %s, want stable", cp.StabilityTier)
	}
}

func TestAggregateChannelProfile_FallsBackToStabilityTierWhenEffectiveEmpty(t *testing.T) {
	ep1 := KeyEndpointProfile{
		EndpointUID:            "ep-1",
		HealthState:            HealthStateHealthy,
		QualityTier:            QualityTierNormal,
		StabilityTier:          StabilityTierStable,
		EffectiveStabilityTier: "",
		SpeedTier:              SpeedTierNormal,
		CostTier:               CostTierNormal,
		Confidence:             0.5,
	}
	ep2 := KeyEndpointProfile{
		EndpointUID:            "ep-2",
		HealthState:            HealthStateHealthy,
		QualityTier:            QualityTierNormal,
		StabilityTier:          StabilityTierNormal,
		EffectiveStabilityTier: StabilityTierNormal,
		SpeedTier:              SpeedTierNormal,
		CostTier:               CostTierNormal,
		Confidence:             0.5,
	}

	cp := AggregateChannelProfile("ch-1", 0, "messages", []KeyEndpointProfile{ep1, ep2})
	if cp.StabilityTier != StabilityTierStable {
		t.Errorf("EffectiveStabilityTier 为空时应回退到 StabilityTier: got %s, want stable", cp.StabilityTier)
	}
}

func TestAggregateChannelProfile_MixedEffectiveAndFallback(t *testing.T) {
	ep1 := KeyEndpointProfile{
		EndpointUID:            "ep-1",
		HealthState:            HealthStateHealthy,
		QualityTier:            QualityTierNormal,
		StabilityTier:          StabilityTierStable,
		EffectiveStabilityTier: StabilityTierStable,
		SpeedTier:              SpeedTierNormal,
		CostTier:               CostTierNormal,
		Confidence:             0.5,
	}
	ep2 := KeyEndpointProfile{
		EndpointUID:   "ep-2",
		HealthState:   HealthStateHealthy,
		QualityTier:   QualityTierNormal,
		StabilityTier: StabilityTierUnstable,
		SpeedTier:     SpeedTierNormal,
		CostTier:      CostTierNormal,
		Confidence:    0.5,
	}

	cp := AggregateChannelProfile("ch-1", 0, "messages", []KeyEndpointProfile{ep1, ep2})
	if cp.StabilityTier != StabilityTierStable {
		t.Errorf("混合场景聚合结果错误: got %s, want stable", cp.StabilityTier)
	}
}

func TestAggregateChannelProfile_MixedHealthCapsAtDegraded(t *testing.T) {
	ep1 := KeyEndpointProfile{
		EndpointUID:   "ep-1",
		HealthState:   HealthStateHealthy,
		QualityTier:   QualityTierPremium,
		StabilityTier: StabilityTierStable,
		SpeedTier:     SpeedTierFast,
		CostTier:      CostTierNormal,
		Confidence:    0.85,
	}
	ep2 := KeyEndpointProfile{
		EndpointUID:   "ep-2",
		HealthState:   HealthStateDead,
		QualityTier:   QualityTierLow,
		StabilityTier: StabilityTierUnstable,
		SpeedTier:     SpeedTierSlow,
		CostTier:      CostTierNormal,
		Confidence:    0.85,
	}

	cp := AggregateChannelProfile("ch-1", 0, "messages", []KeyEndpointProfile{ep1, ep2})
	if cp.HealthState != HealthStateDegraded {
		t.Fatalf("mixed health should cap at degraded, got %s", cp.HealthState)
	}
	if cp.QualityTier != QualityTierPremium {
		t.Fatalf("quality should still take best endpoint, got %s", cp.QualityTier)
	}
}
