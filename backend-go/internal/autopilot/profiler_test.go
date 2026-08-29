package autopilot

import (
	"testing"
	"time"
)

// ── Mock MetricsProvider ──

// mockMetricsProvider 实现 MetricsProvider 接口，用于测试。
type mockMetricsProvider struct {
	statsFn    func(channelKind, baseURL, apiKey, serviceType string, duration time.Duration) TimeWindowStats
	snapshotFn func(channelKind, baseURL, apiKey, serviceType string) KeyCircuitSnapshot
}

func (m *mockMetricsProvider) GetTimeWindowStatsForKey(channelKind, baseURL, apiKey, serviceType string, duration time.Duration) TimeWindowStats {
	if m.statsFn != nil {
		return m.statsFn(channelKind, baseURL, apiKey, serviceType, duration)
	}
	return TimeWindowStats{}
}

func (m *mockMetricsProvider) GetKeySnapshot(channelKind, baseURL, apiKey, serviceType string) KeyCircuitSnapshot {
	if m.snapshotFn != nil {
		return m.snapshotFn(channelKind, baseURL, apiKey, serviceType)
	}
	return KeyCircuitSnapshot{}
}

// newMockProvider 创建返回固定值的 mock provider。
func newMockProvider(stats TimeWindowStats, snapshot KeyCircuitSnapshot) *mockMetricsProvider {
	return &mockMetricsProvider{
		statsFn: func(string, string, string, string, time.Duration) TimeWindowStats {
			return stats
		},
		snapshotFn: func(string, string, string, string) KeyCircuitSnapshot {
			return snapshot
		},
	}
}

// ── DeriveStabilityTier 测试 ──

func TestDeriveStabilityTier(t *testing.T) {
	now := time.Now()
	recentSuccess := now.Add(-1 * time.Hour)
	oldSuccess := now.Add(-10 * time.Hour)

	tests := []struct {
		name     string
		stats    TimeWindowStats
		snapshot KeyCircuitSnapshot
		want     StabilityTier
	}{
		{
			name:  "数据不足 → normal（避免健康渠道因样本少被打 0 分）",
			stats: TimeWindowStats{RequestCount: 3, SuccessRate: 100},
			want:  StabilityTierNormal,
		},
		{
			name: "高成功率低429 → stable",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessCount: 98,
				SuccessRate:  98,
			},
			snapshot: KeyCircuitSnapshot{LastSuccessAt: &recentSuccess},
			want:     StabilityTierStable,
		},
		{
			name: "成功率刚好95%且无429 → stable",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessCount: 95,
				SuccessRate:  95,
			},
			snapshot: KeyCircuitSnapshot{LastSuccessAt: &recentSuccess},
			want:     StabilityTierStable,
		},
		{
			name: "成功率95%但429率5% → normal（边界：429率不 < 5%）",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessCount: 95,
				SuccessRate:  95,
			},
			snapshot: KeyCircuitSnapshot{
				OverloadedCount: 5,
				LastSuccessAt:   &recentSuccess,
			},
			want: StabilityTierNormal,
		},
		{
			name: "成功率80%无429 → normal",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessCount: 80,
				SuccessRate:  80,
			},
			snapshot: KeyCircuitSnapshot{LastSuccessAt: &recentSuccess},
			want:     StabilityTierNormal,
		},
		{
			name: "成功率79% → unstable",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessCount: 79,
				SuccessRate:  79,
			},
			snapshot: KeyCircuitSnapshot{LastSuccessAt: &recentSuccess},
			want:     StabilityTierUnstable,
		},
		{
			name: "连续失败5次降级: stable → normal",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessCount: 99,
				SuccessRate:  99,
			},
			snapshot: KeyCircuitSnapshot{
				ConsecutiveFailures: 5,
				LastSuccessAt:       &recentSuccess,
			},
			want: StabilityTierNormal,
		},
		{
			name: "连续失败4次不降级: 保持stable",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessCount: 99,
				SuccessRate:  99,
			},
			snapshot: KeyCircuitSnapshot{
				ConsecutiveFailures: 4,
				LastSuccessAt:       &recentSuccess,
			},
			want: StabilityTierStable,
		},
		{
			name: "熔断器open → unstable",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessCount: 95,
				SuccessRate:  95,
			},
			snapshot: KeyCircuitSnapshot{
				CircuitState:  1, // open
				LastSuccessAt: &recentSuccess,
			},
			want: StabilityTierUnstable,
		},
		{
			name: "熔断器half_open不额外降级",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessCount: 95,
				SuccessRate:  95,
			},
			snapshot: KeyCircuitSnapshot{
				CircuitState:  2, // half_open
				LastSuccessAt: &recentSuccess,
			},
			want: StabilityTierStable,
		},
		{
			name: "最近成功>6小时 → unstable",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessCount: 99,
				SuccessRate:  99,
			},
			snapshot: KeyCircuitSnapshot{
				LastSuccessAt: &oldSuccess,
			},
			want: StabilityTierUnstable,
		},
		{
			name: "LastSuccessAt为nil → 不触发6小时降级",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessCount: 99,
				SuccessRate:  99,
			},
			snapshot: KeyCircuitSnapshot{
				LastSuccessAt: nil,
			},
			want: StabilityTierStable,
		},
		{
			name: "多重降级叠加: stable + 连续失败 + 熔断open → unstable",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessCount: 99,
				SuccessRate:  99,
			},
			snapshot: KeyCircuitSnapshot{
				ConsecutiveFailures: 10,
				CircuitState:        1, // open
				LastSuccessAt:       &recentSuccess,
			},
			want: StabilityTierUnstable,
		},
		{
			name: "高429率 → unstable",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessCount: 85,
				FailureCount: 15,
				SuccessRate:  85,
			},
			snapshot: KeyCircuitSnapshot{
				OverloadedCount: 25,
				LastSuccessAt:   &recentSuccess,
			},
			want: StabilityTierUnstable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveStabilityTier(tt.stats, tt.snapshot)
			if got != tt.want {
				t.Errorf("DeriveStabilityTier() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ── DeriveSpeedTier 测试 ──

func TestDeriveSpeedTier(t *testing.T) {
	// Phase 1: 无延迟数据，始终返回 normal
	tests := []struct {
		name     string
		snapshot KeyCircuitSnapshot
		want     SpeedTier
	}{
		{
			name:     "空快照 → normal",
			snapshot: KeyCircuitSnapshot{},
			want:     SpeedTierNormal,
		},
		{
			name: "有数据但Phase1无延迟 → normal",
			snapshot: KeyCircuitSnapshot{
				ConsecutiveFailures: 0,
			},
			want: SpeedTierNormal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveSpeedTier(tt.snapshot)
			if got != tt.want {
				t.Errorf("DeriveSpeedTier() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ── DeriveSpeedTierFromConnectStats 测试 ──

func TestDeriveSpeedTierFromConnectStats(t *testing.T) {
	tests := []struct {
		name  string
		stats TimeWindowStats
		want  SpeedTier
	}{
		{
			name:  "无样本 → normal（安全默认值）",
			stats: TimeWindowStats{ConnectSampleCount: 0, P95ConnectLatencyMs: 0},
			want:  SpeedTierNormal,
		},
		{
			name:  "n=7 单次偶发慢连接拉高 P95 → 样本不足，不判定为 slow",
			stats: TimeWindowStats{ConnectSampleCount: 7, P95ConnectLatencyMs: 4000},
			want:  SpeedTierNormal,
		},
		{
			name:  "n=19 仍低于门槛 → normal",
			stats: TimeWindowStats{ConnectSampleCount: 19, P95ConnectLatencyMs: 4000},
			want:  SpeedTierNormal,
		},
		{
			name:  "n=20 达到门槛且 P95 高 → slow",
			stats: TimeWindowStats{ConnectSampleCount: 20, P95ConnectLatencyMs: 4000},
			want:  SpeedTierSlow,
		},
		{
			name:  "n=25 达到门槛但 P95 快 → fast",
			stats: TimeWindowStats{ConnectSampleCount: 25, P95ConnectLatencyMs: 200},
			want:  SpeedTierFast,
		},
		{
			name:  "n=25 达到门槛，P95 处于中间档 → normal",
			stats: TimeWindowStats{ConnectSampleCount: 25, P95ConnectLatencyMs: 1000},
			want:  SpeedTierNormal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveSpeedTierFromConnectStats(tt.stats, KeyCircuitSnapshot{})
			if got != tt.want {
				t.Errorf("DeriveSpeedTierFromConnectStats() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ── DeriveCostTier 测试 ──

func TestDeriveCostTier(t *testing.T) {
	tests := []struct {
		name     string
		stats    TimeWindowStats
		snapshot KeyCircuitSnapshot
		want     CostTier
	}{
		{
			name:  "无数据 → normal",
			stats: TimeWindowStats{},
			want:  CostTierNormal,
		},
		{
			name: "少于10个请求不触发启发 → normal",
			stats: TimeWindowStats{
				RequestCount: 5,
				SuccessCount: 3,
			},
			snapshot: KeyCircuitSnapshot{
				OverloadedCount: 3,
			},
			want: CostTierNormal,
		},
		{
			name: "频繁429（>30%）→ cheap",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessCount: 60,
				FailureCount: 40,
			},
			snapshot: KeyCircuitSnapshot{
				OverloadedCount: 35,
			},
			want: CostTierCheap,
		},
		{
			name: "429率刚好30% → normal（边界：不 > 30%）",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessCount: 70,
				FailureCount: 30,
			},
			snapshot: KeyCircuitSnapshot{
				OverloadedCount: 30,
			},
			want: CostTierNormal,
		},
		{
			name: "429率31% → cheap",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessCount: 69,
				FailureCount: 31,
			},
			snapshot: KeyCircuitSnapshot{
				OverloadedCount: 31,
			},
			want: CostTierCheap,
		},
		{
			name: "无429但有普通失败 → normal",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessCount: 80,
				FailureCount: 20,
			},
			snapshot: KeyCircuitSnapshot{
				OverloadedCount: 0,
			},
			want: CostTierNormal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveCostTier(tt.stats, tt.snapshot)
			if got != tt.want {
				t.Errorf("DeriveCostTier() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ── DeriveCostTierFromProfile 测试 ──

func TestDeriveCostTierFromProfile(t *testing.T) {
	tests := []struct {
		name    string
		profile CostProfile
		want    CostTier
	}{
		{
			name:    "零成本 → free",
			profile: CostProfile{EffectiveInputCostPerMTok: 0, EffectiveOutputCostPerMTok: 0},
			want:    CostTierFree,
		},
		{
			name:    "低价 → cheap",
			profile: CostProfile{EffectiveInputCostPerMTok: 0.5, EffectiveOutputCostPerMTok: 2.0},
			want:    CostTierCheap,
		},
		{
			name:    "Input刚好$1且Output刚好$5 → normal（边界）",
			profile: CostProfile{EffectiveInputCostPerMTok: 1.0, EffectiveOutputCostPerMTok: 5.0},
			want:    CostTierNormal,
		},
		{
			name:    "中等价格 → normal",
			profile: CostProfile{EffectiveInputCostPerMTok: 5.0, EffectiveOutputCostPerMTok: 15.0},
			want:    CostTierNormal,
		},
		{
			name:    "Input刚好$10 → expensive（边界：不 < 10）",
			profile: CostProfile{EffectiveInputCostPerMTok: 10.0, EffectiveOutputCostPerMTok: 29.0},
			want:    CostTierExpensive,
		},
		{
			name:    "高价 → expensive",
			profile: CostProfile{EffectiveInputCostPerMTok: 15.0, EffectiveOutputCostPerMTok: 75.0},
			want:    CostTierExpensive,
		},
		{
			name:    "仅Input超阈值 → expensive",
			profile: CostProfile{EffectiveInputCostPerMTok: 10.0, EffectiveOutputCostPerMTok: 30.0},
			want:    CostTierExpensive,
		},
		{
			name:    "只有Output价 → 按Output判断",
			profile: CostProfile{EffectiveInputCostPerMTok: 0, EffectiveOutputCostPerMTok: 2.0},
			want:    CostTierCheap,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveCostTierFromProfile(tt.profile)
			if got != tt.want {
				t.Errorf("DeriveCostTierFromProfile() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ── DeriveQualityTier 测试 ──

func TestDeriveQualityTier(t *testing.T) {
	tests := []struct {
		name       string
		family     ModelFamily
		modelID    string
		lowQuality bool
		want       QualityTier
	}{
		{
			name:    "claude opus → premium",
			family:  ModelFamilyClaude,
			modelID: "claude-opus-4",
			want:    QualityTierPremium,
		},
		{
			name:    "claude sonnet → high",
			family:  ModelFamilyClaude,
			modelID: "claude-sonnet-4-20250514",
			want:    QualityTierHigh,
		},
		{
			name:    "claude haiku → normal",
			family:  ModelFamilyClaude,
			modelID: "claude-haiku-3.5",
			want:    QualityTierNormal,
		},
		{
			name:    "claude fable → premium",
			family:  ModelFamilyClaude,
			modelID: "claude-fable-5",
			want:    QualityTierPremium,
		},
		{
			name:    "gpt-5.6 → premium",
			family:  ModelFamilyOpenAI,
			modelID: "gpt-5.6-sol",
			want:    QualityTierPremium,
		},
		{
			name:    "gpt-5.5（常规口径 54.0）→ high",
			family:  ModelFamilyOpenAI,
			modelID: "gpt-5.5",
			want:    QualityTierHigh,
		},
		{
			name:    "gpt-5.4（单档 max 折算 31.8）→ normal",
			family:  ModelFamilyOpenAI,
			modelID: "gpt-5.4",
			want:    QualityTierNormal,
		},
		{
			name:    "gpt-5.4-mini（直测 24.3 < normalMin）→ low",
			family:  ModelFamilyOpenAI,
			modelID: "gpt-5.4-mini",
			want:    QualityTierLow,
		},
		{
			name:    "gpt-5.4-nano → normal",
			family:  ModelFamilyOpenAI,
			modelID: "gpt-5.4-nano",
			want:    QualityTierNormal,
		},
		{
			name:    "glm-5.2（常规口径 22.2）→ low",
			family:  ModelFamilyGLM,
			modelID: "glm-5.2",
			want:    QualityTierLow,
		},
		{
			name:    "glm-5.1 → high",
			family:  ModelFamilyGLM,
			modelID: "glm-5.1",
			want:    QualityTierHigh,
		},
	{
		name:    "mimo-v2.5-pro（校准 27.9 ≥ normalMin 27.0）→ normal",
		family:  ModelFamilyMiMo,
		modelID: "mimo-v2.5-pro",
		// 2026-08-29 基准刷新：deepseek-v4-flash 常规口径 26.99→28.51 填补低段断层，
		// normalMin 28.74→27.03，校准分 27.93 随之从 low 翻入 normal（边界随数据分布浮动）
		want:    QualityTierNormal,
	},
		{
			name:    "deepseek-v4-pro（常规口径 31.1）→ normal",
			family:  ModelFamilyDeepSeek,
			modelID: "deepseek-v4-pro",
			want:    QualityTierNormal,
		},
		{
			name:    "deepseek-v3 → normal",
			family:  ModelFamilyDeepSeek,
			modelID: "deepseek-v3",
			want:    QualityTierNormal,
		},
		{
			name:    "未知模型族 → low",
			family:  ModelFamilyLocal,
			modelID: "llama-3.2",
			want:    QualityTierLow,
		},
		{
			name:       "lowQuality 降级: opus premium → normal",
			family:     ModelFamilyClaude,
			modelID:    "claude-opus-4",
			lowQuality: true,
			want:       QualityTierNormal,
		},
		{
			name:       "lowQuality 对已为 normal 的不降级",
			family:     ModelFamilyClaude,
			modelID:    "claude-haiku-3.5",
			lowQuality: true,
			want:       QualityTierNormal,
		},
		{
			name:       "lowQuality 对 low 不降级",
			family:     ModelFamilyLocal,
			modelID:    "llama-3.2",
			lowQuality: true,
			want:       QualityTierLow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveQualityTier(tt.family, tt.modelID, tt.lowQuality)
			if got != tt.want {
				t.Errorf("DeriveQualityTier(%q, %q, lowQuality=%v) = %q, want %q",
					tt.family, tt.modelID, tt.lowQuality, got, tt.want)
			}
		})
	}
}

// ── Profiler 集成测试 ──

func TestProfilerDeriveEndpointProfile(t *testing.T) {
	now := time.Now()
	recentSuccess := now.Add(-30 * time.Minute)

	provider := newMockProvider(
		TimeWindowStats{
			RequestCount:          50,
			SuccessCount:          48,
			FailureCount:          2,
			SuccessRate:           96,
			ConnectSampleCount:    42,
			P95ConnectLatencyMs:   2_100,
			FirstByteSampleCount:  40,
			P95FirstByteLatencyMs: 2300,
		},
		KeyCircuitSnapshot{
			CircuitState:        0, // closed
			ConsecutiveFailures: 0,
			LastSuccessAt:       &recentSuccess,
			OverloadedCount:     1,
		},
	)

	profiler := NewProfiler(provider)
	profile := profiler.DeriveEndpointProfile(
		"test-channel", 1, "messages",
		"https://api.example.com", "sk-test1234", "claude", "", "",
	)

	// 验证身份字段
	if profile.ChannelUID != "test-channel" {
		t.Errorf("ChannelUID = %q, want %q", profile.ChannelUID, "test-channel")
	}
	if profile.ChannelID != 1 {
		t.Errorf("ChannelID = %d, want 1", profile.ChannelID)
	}
	if profile.ChannelKind != "messages" {
		t.Errorf("ChannelKind = %q, want %q", profile.ChannelKind, "messages")
	}
	if profile.EndpointUID == "" {
		t.Error("EndpointUID should not be empty")
	}
	expectedKeyHash := KeyHashFromAPIKey("sk-test1234")
	if profile.KeyHash != expectedKeyHash {
		t.Errorf("KeyHash = %q, want %q", profile.KeyHash, expectedKeyHash)
	}
	expectedMetricsKey := computeMetricsIdentityKey("https://api.example.com", "sk-test1234", "claude")
	if profile.MetricsKey != expectedMetricsKey {
		t.Errorf("MetricsKey = %q, want canonical identity %q", profile.MetricsKey, expectedMetricsKey)
	}
	if profile.IdentityBaseURL != "https://api.example.com/v1" {
		t.Errorf("IdentityBaseURL = %q, want %q", profile.IdentityBaseURL, "https://api.example.com/v1")
	}
	if profile.Source != "l1_passive" {
		t.Errorf("Source = %q, want %q", profile.Source, "l1_passive")
	}

	// 验证推导结果
	if profile.StabilityTier != StabilityTierStable {
		t.Errorf("StabilityTier = %q, want %q", profile.StabilityTier, StabilityTierStable)
	}
	if profile.HealthState != HealthStateHealthy {
		t.Errorf("HealthState = %q, want %q", profile.HealthState, HealthStateHealthy)
	}
	if profile.SpeedTier != SpeedTierSlow {
		t.Errorf("SpeedTier = %q, want %q", profile.SpeedTier, SpeedTierSlow)
	}
	if profile.CostTier != CostTierNormal {
		t.Errorf("CostTier = %q, want %q", profile.CostTier, CostTierNormal)
	}
	if profile.FirstByteSampleCount != 40 || profile.P95FirstByteLatencyMs != 2300 {
		t.Errorf("TTFB profile = samples:%d p95:%dms, want 40/2300ms",
			profile.FirstByteSampleCount, profile.P95FirstByteLatencyMs)
	}
	if profile.ConnectSampleCount != 42 || profile.P95ConnectLatencyMs != 2_100 {
		t.Errorf("connect profile = samples:%d p95:%dms, want 42/2100ms",
			profile.ConnectSampleCount, profile.P95ConnectLatencyMs)
	}
	if profile.ConnectStatsUpdatedAt == nil || profile.ConnectStatsUpdatedAt.Before(now) {
		t.Errorf("connect profile freshness timestamp = %v, want >= %v", profile.ConnectStatsUpdatedAt, now)
	}
	if profile.FirstByteStatsUpdatedAt == nil || profile.FirstByteStatsUpdatedAt.Before(now) {
		t.Errorf("TTFB profile freshness timestamp = %v, want >= %v", profile.FirstByteStatsUpdatedAt, now)
	}
	if profile.Confidence != 0.3 {
		t.Errorf("Confidence = %f, want 0.3", profile.Confidence)
	}
	if profile.SuggestedAction != ActionNone {
		t.Errorf("SuggestedAction = %q, want %q", profile.SuggestedAction, ActionNone)
	}
}

func TestProfilerDeriveEndpointProfile_DeadEndpoint(t *testing.T) {
	oldTime := time.Now().Add(-48 * time.Hour)

	provider := newMockProvider(
		TimeWindowStats{
			RequestCount: 100,
			SuccessCount: 10,
			FailureCount: 90,
			SuccessRate:  10,
		},
		KeyCircuitSnapshot{
			CircuitState:        1, // open
			ConsecutiveFailures: 15,
			LastSuccessAt:       &oldTime,
			OverloadedCount:     5,
		},
	)

	profiler := NewProfiler(provider)
	profile := profiler.DeriveEndpointProfile(
		"test-channel", 1, "messages",
		"https://api.example.com", "sk-test1234", "claude", "", "",
	)

	if profile.HealthState != HealthStateDead {
		t.Errorf("HealthState = %q, want %q", profile.HealthState, HealthStateDead)
	}
	if profile.StabilityTier != StabilityTierUnstable {
		t.Errorf("StabilityTier = %q, want %q", profile.StabilityTier, StabilityTierUnstable)
	}
	if profile.SuggestedAction != ActionProbe {
		t.Errorf("SuggestedAction = %q, want %q", profile.SuggestedAction, ActionProbe)
	}
	if len(profile.HealthEvidence) == 0 {
		t.Error("HealthEvidence should not be empty for dead endpoint")
	}
}

func TestProfilerDeriveEndpointProfile_InsufficientData(t *testing.T) {
	provider := newMockProvider(
		TimeWindowStats{RequestCount: 0, SuccessRate: 100},
		KeyCircuitSnapshot{},
	)

	profiler := NewProfiler(provider)
	profile := profiler.DeriveEndpointProfile(
		"test-channel", 1, "messages",
		"https://api.example.com", "sk-test1234", "claude", "", "",
	)

	if profile.HealthState != HealthStateUnknown {
		t.Errorf("HealthState = %q, want %q", profile.HealthState, HealthStateUnknown)
	}
	if profile.StabilityTier != StabilityTierNormal {
		t.Errorf("StabilityTier = %q, want %q", profile.StabilityTier, StabilityTierNormal)
	}
}

// ── HealthState 推导测试 ──

func TestDeriveHealthState(t *testing.T) {
	now := time.Now()
	recent := now.Add(-1 * time.Hour)
	old := now.Add(-48 * time.Hour)

	tests := []struct {
		name     string
		stats    TimeWindowStats
		snapshot KeyCircuitSnapshot
		want     HealthState
	}{
		{
			name:  "数据不足 → unknown",
			stats: TimeWindowStats{RequestCount: 3, SuccessRate: 100},
			want:  HealthStateUnknown,
		},
		{
			name: "健康 → healthy",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessRate:  98,
			},
			snapshot: KeyCircuitSnapshot{LastSuccessAt: &recent},
			want:     HealthStateHealthy,
		},
		{
			name: "成功率<20% → dead",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessRate:  15,
			},
			snapshot: KeyCircuitSnapshot{LastSuccessAt: &recent},
			want:     HealthStateDead,
		},
		{
			name: "最近成功>24小时 → dead",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessRate:  90,
			},
			snapshot: KeyCircuitSnapshot{LastSuccessAt: &old},
			want:     HealthStateDead,
		},
		{
			name: "熔断器open → limited",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessRate:  85,
			},
			snapshot: KeyCircuitSnapshot{
				CircuitState:  1,
				LastSuccessAt: &recent,
			},
			want: HealthStateLimited,
		},
		{
			name: "连续失败>=5 → degraded",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessRate:  85,
			},
			snapshot: KeyCircuitSnapshot{
				ConsecutiveFailures: 5,
				LastSuccessAt:       &recent,
			},
			want: HealthStateDegraded,
		},
		{
			name: "高429率>=20% → limited",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessRate:  85,
			},
			snapshot: KeyCircuitSnapshot{
				OverloadedCount: 20,
				LastSuccessAt:   &recent,
			},
			want: HealthStateLimited,
		},
		{
			name: "成功率<80% → degraded",
			stats: TimeWindowStats{
				RequestCount: 100,
				SuccessRate:  75,
			},
			snapshot: KeyCircuitSnapshot{LastSuccessAt: &recent},
			want:     HealthStateDegraded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveHealthState(tt.stats, tt.snapshot)
			if got != tt.want {
				t.Errorf("deriveHealthState() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ── classifyStabilityByRates 边界测试 ──

func TestClassifyStabilityByRates(t *testing.T) {
	tests := []struct {
		name         string
		successRate  float64
		overloadRate float64
		want         StabilityTier
	}{
		{"完美100%", 100, 0, StabilityTierStable},
		{"95%成功0%429", 95, 0, StabilityTierStable},
		{"96%成功5%429", 96, 5, StabilityTierNormal},
		{"94%成功0%429", 94, 0, StabilityTierNormal},
		{"80%成功0%429", 80, 0, StabilityTierNormal},
		{"80%成功19%429", 80, 19, StabilityTierNormal},
		{"79%成功0%429", 79, 0, StabilityTierUnstable},
		{"90%成功20%429", 90, 20, StabilityTierUnstable},
		{"0%成功0%429", 0, 0, StabilityTierUnstable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyStabilityByRates(tt.successRate, tt.overloadRate)
			if got != tt.want {
				t.Errorf("classifyStabilityByRates(%.1f, %.1f) = %q, want %q",
					tt.successRate, tt.overloadRate, got, tt.want)
			}
		})
	}
}

// ── collectHealthEvidence 测试 ──

func TestCollectHealthEvidence(t *testing.T) {
	t.Run("数据不足返回单一证据", func(t *testing.T) {
		evidence := collectHealthEvidence(
			TimeWindowStats{RequestCount: 2},
			KeyCircuitSnapshot{},
		)
		if len(evidence) != 1 {
			t.Errorf("expected 1 evidence, got %d", len(evidence))
		}
	})

	t.Run("健康状态返回空证据", func(t *testing.T) {
		now := time.Now()
		recent := now.Add(-1 * time.Hour)
		evidence := collectHealthEvidence(
			TimeWindowStats{RequestCount: 100, SuccessRate: 99},
			KeyCircuitSnapshot{LastSuccessAt: &recent},
		)
		if len(evidence) != 0 {
			t.Errorf("expected 0 evidence for healthy state, got %d: %v", len(evidence), evidence)
		}
	})

	t.Run("多种问题收集多条证据", func(t *testing.T) {
		old := time.Now().Add(-10 * time.Hour)
		evidence := collectHealthEvidence(
			TimeWindowStats{RequestCount: 100, SuccessRate: 50},
			KeyCircuitSnapshot{
				ConsecutiveFailures: 10,
				CircuitState:        1,
				OverloadedCount:     15,
				LastSuccessAt:       &old,
			},
		)
		if len(evidence) < 4 {
			t.Errorf("expected at least 4 evidence items, got %d: %v", len(evidence), evidence)
		}
	})
}

// ── OriginType/OriginTier 接线测试（设计 §12.2 P1.5）──

func TestProfilerDeriveEndpointProfile_PropagatesOriginFields(t *testing.T) {
	provider := newMockProvider(
		TimeWindowStats{RequestCount: 50, SuccessCount: 48, FailureCount: 2, SuccessRate: 96},
		KeyCircuitSnapshot{CircuitState: 0},
	)
	profiler := NewProfiler(provider)

	profile := profiler.DeriveEndpointProfile(
		"test-channel", 1, "messages",
		"https://api.example.com", "sk-test1234", "claude",
		"official_api", "first",
	)

	if profile.OriginType != "official_api" {
		t.Errorf("OriginType = %q, want %q", profile.OriginType, "official_api")
	}
	if profile.OriginTier != "first" {
		t.Errorf("OriginTier = %q, want %q", profile.OriginTier, "first")
	}
}

func TestProfilerDeriveEndpointProfile_EmptyOriginFallsBackToUnknown(t *testing.T) {
	provider := newMockProvider(
		TimeWindowStats{RequestCount: 50, SuccessCount: 48, FailureCount: 2, SuccessRate: 96},
		KeyCircuitSnapshot{CircuitState: 0},
	)
	profiler := NewProfiler(provider)

	profile := profiler.DeriveEndpointProfile(
		"test-channel", 1, "messages",
		"https://api.example.com", "sk-test1234", "claude",
		"", "",
	)

	if profile.OriginType != string(OriginUnknown) {
		t.Errorf("OriginType = %q, want %q (unknown fallback)", profile.OriginType, string(OriginUnknown))
	}
	if profile.OriginTier != string(OriginTierUnknown) {
		t.Errorf("OriginTier = %q, want %q (unknown fallback)", profile.OriginTier, string(OriginTierUnknown))
	}
}
