package config

import (
	"testing"
	"time"
)

// TestResolveHealthCheckPolicy 表驱动覆盖分档默认、覆盖优先级、clamp 与禁用逻辑
func TestResolveHealthCheckPolicy(t *testing.T) {
	tests := []struct {
		name   string
		global *GlobalHealthCheckConfig
		up     UpstreamConfig
		want   ResolvedHealthCheckPolicy
	}{
		{
			name: "分档默认-third 公益站 30min 且 L2 默认开",
			up:   UpstreamConfig{OriginTier: "third"},
			want: ResolvedHealthCheckPolicy{Enabled: true, Interval: 30 * time.Minute, VerifyRealCall: true, MaxConcurrency: 4, Timeout: 10 * time.Second, SparseL2MaxModels: 3, SparseL2MaxCostAFP: 6.0, L2ModelQuietPeriod: 30 * time.Minute},
		},
		{
			name: "分档默认-second 中转站 2h 且 L2 关",
			up:   UpstreamConfig{OriginTier: "second"},
			want: ResolvedHealthCheckPolicy{Enabled: true, Interval: 2 * time.Hour, VerifyRealCall: false, MaxConcurrency: 4, Timeout: 10 * time.Second, SparseL2MaxModels: 3, SparseL2MaxCostAFP: 6.0, L2ModelQuietPeriod: 2 * time.Hour},
		},
		{
			name: "分档默认-first 官方 6h 且 L2 关",
			up:   UpstreamConfig{OriginTier: "first"},
			want: ResolvedHealthCheckPolicy{Enabled: true, Interval: 6 * time.Hour, VerifyRealCall: false, MaxConcurrency: 4, Timeout: 10 * time.Second, SparseL2MaxModels: 3, SparseL2MaxCostAFP: 6.0, L2ModelQuietPeriod: 6 * time.Hour},
		},
		{
			name: "分档默认-local 6h 且 L2 关",
			up:   UpstreamConfig{OriginTier: "local"},
			want: ResolvedHealthCheckPolicy{Enabled: true, Interval: 6 * time.Hour, VerifyRealCall: false, MaxConcurrency: 4, Timeout: 10 * time.Second, SparseL2MaxModels: 3, SparseL2MaxCostAFP: 6.0, L2ModelQuietPeriod: 6 * time.Hour},
		},
		{
			name: "分档默认-空 OriginTier 按 6h 且 L2 关",
			up:   UpstreamConfig{},
			want: ResolvedHealthCheckPolicy{Enabled: true, Interval: 6 * time.Hour, VerifyRealCall: false, MaxConcurrency: 4, Timeout: 10 * time.Second, SparseL2MaxModels: 3, SparseL2MaxCostAFP: 6.0, L2ModelQuietPeriod: 6 * time.Hour},
		},
		{
			name: "分档默认-未知取值按 6h 且 L2 关",
			up:   UpstreamConfig{OriginTier: "unknown"},
			want: ResolvedHealthCheckPolicy{Enabled: true, Interval: 6 * time.Hour, VerifyRealCall: false, MaxConcurrency: 4, Timeout: 10 * time.Second, SparseL2MaxModels: 3, SparseL2MaxCostAFP: 6.0, L2ModelQuietPeriod: 6 * time.Hour},
		},
		{
			name:   "全局字段覆盖分档默认",
			global: &GlobalHealthCheckConfig{DefaultIntervalMinutes: 90, VerifyRealCall: boolPtr(true), MaxConcurrency: 8, TimeoutMs: 5000},
			up:     UpstreamConfig{OriginTier: "first"},
			want:   ResolvedHealthCheckPolicy{Enabled: true, Interval: 90 * time.Minute, VerifyRealCall: true, MaxConcurrency: 8, Timeout: 5 * time.Second, SparseL2MaxModels: 3, SparseL2MaxCostAFP: 6.0, L2ModelQuietPeriod: 90 * time.Minute},
		},
		{
			name:   "渠道级字段覆盖全局字段",
			global: &GlobalHealthCheckConfig{DefaultIntervalMinutes: 90, VerifyRealCall: boolPtr(false)},
			up: UpstreamConfig{OriginTier: "first", HealthCheck: &ChannelHealthCheckConfig{
				IntervalMinutes: 45,
				VerifyRealCall:  boolPtr(true),
				VerifyModel:     "claude-haiku-4-5",
			}},
			want: ResolvedHealthCheckPolicy{Enabled: true, Interval: 45 * time.Minute, VerifyRealCall: true, VerifyModel: "claude-haiku-4-5", MaxConcurrency: 4, Timeout: 10 * time.Second, SparseL2MaxModels: 3, SparseL2MaxCostAFP: 6.0, L2ModelQuietPeriod: 45 * time.Minute},
		},
		{
			name:   "Interval 小于 30min 硬下限被 clamp",
			global: &GlobalHealthCheckConfig{DefaultIntervalMinutes: 5},
			up:     UpstreamConfig{OriginTier: "first", HealthCheck: &ChannelHealthCheckConfig{IntervalMinutes: 10}},
			want:   ResolvedHealthCheckPolicy{Enabled: true, Interval: MinHealthCheckInterval, VerifyRealCall: false, MaxConcurrency: 4, Timeout: 10 * time.Second, SparseL2MaxModels: 3, SparseL2MaxCostAFP: 6.0, L2ModelQuietPeriod: MinHealthCheckInterval},
		},
		{
			name:   "全局 Enabled=false 整体禁用",
			global: &GlobalHealthCheckConfig{Enabled: boolPtr(false)},
			up:     UpstreamConfig{OriginTier: "third"},
			want:   ResolvedHealthCheckPolicy{Enabled: false, Interval: 30 * time.Minute, VerifyRealCall: true, MaxConcurrency: 4, Timeout: 10 * time.Second, SparseL2MaxModels: 3, SparseL2MaxCostAFP: 6.0, L2ModelQuietPeriod: 30 * time.Minute},
		},
		{
			name:   "渠道级 Enabled 可覆盖全局禁用单独开启",
			global: &GlobalHealthCheckConfig{Enabled: boolPtr(false)},
			up:     UpstreamConfig{OriginTier: "first", HealthCheck: &ChannelHealthCheckConfig{Enabled: boolPtr(true)}},
			want:   ResolvedHealthCheckPolicy{Enabled: true, Interval: 6 * time.Hour, VerifyRealCall: false, MaxConcurrency: 4, Timeout: 10 * time.Second, SparseL2MaxModels: 3, SparseL2MaxCostAFP: 6.0, L2ModelQuietPeriod: 6 * time.Hour},
		},
		{
			name: "渠道级 Enabled=false 单独禁用",
			up:   UpstreamConfig{OriginTier: "third", HealthCheck: &ChannelHealthCheckConfig{Enabled: boolPtr(false)}},
			want: ResolvedHealthCheckPolicy{Enabled: false, Interval: 30 * time.Minute, VerifyRealCall: true, MaxConcurrency: 4, Timeout: 10 * time.Second, SparseL2MaxModels: 3, SparseL2MaxCostAFP: 6.0, L2ModelQuietPeriod: 30 * time.Minute},
		},
		{
			name:   "MaxConcurrency/Timeout 非正值取默认值",
			global: &GlobalHealthCheckConfig{MaxConcurrency: -1, TimeoutMs: 0},
			up:     UpstreamConfig{OriginTier: "first"},
			want:   ResolvedHealthCheckPolicy{Enabled: true, Interval: 6 * time.Hour, VerifyRealCall: false, MaxConcurrency: 4, Timeout: 10 * time.Second, SparseL2MaxModels: 3, SparseL2MaxCostAFP: 6.0, L2ModelQuietPeriod: 6 * time.Hour},
		},
		{
			name:   "稀疏 L2 字段全局与渠道级覆盖",
			global: &GlobalHealthCheckConfig{SparseL2MaxModels: 5, SparseL2MaxCostAFP: 10.0, L2ModelQuietPeriodMinutes: 60},
			up: UpstreamConfig{OriginTier: "first", HealthCheck: &ChannelHealthCheckConfig{
				SparseL2MaxModels:         2,
				SparseL2MaxCostAFP:        3.0,
				L2ModelQuietPeriodMinutes: 15,
			}},
			want: ResolvedHealthCheckPolicy{Enabled: true, Interval: 6 * time.Hour, VerifyRealCall: false, MaxConcurrency: 4, Timeout: 10 * time.Second, SparseL2MaxModels: 2, SparseL2MaxCostAFP: 3.0, L2ModelQuietPeriod: 15 * time.Minute},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{HealthCheck: tt.global}
			got := cfg.ResolveHealthCheckPolicy(&tt.up)
			if got != tt.want {
				t.Errorf("ResolveHealthCheckPolicy() = %+v, 期望 %+v", got, tt.want)
			}
		})
	}
}
