package config

import (
	"testing"
	"time"
)

func TestParseBalanceProvider(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", BalanceProviderAuto},
		{"auto", BalanceProviderAuto},
		{"AUTO", BalanceProviderAuto},
		{" newapi_token ", BalanceProviderNewapiToken},
		{"NewAPI_Token", BalanceProviderNewapiToken},
		{"sub2api", BalanceProviderSub2API},
		{"newapi_billing", BalanceProviderNewapiBilling},
		{"unknown-provider", BalanceProviderAuto},
	}
	for _, tt := range tests {
		if got := ParseBalanceProvider(tt.in); got != tt.want {
			t.Errorf("ParseBalanceProvider(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestResolveBalancePolicy(t *testing.T) {
	tests := []struct {
		name         string
		global       *GlobalBalanceCheckConfig
		channel      *ChannelBalanceCheckConfig
		managedKind  string
		wantEnabled  bool
		wantProvider string
		wantInterval time.Duration
		wantQuotaPU  float64
		wantTimeout  time.Duration
		wantMaxConc  int
	}{
		{
			name:         "非托管渠道无配置默认关闭",
			channel:      nil,
			managedKind:  "",
			wantEnabled:  false,
			wantProvider: BalanceProviderAuto,
			wantInterval: 6 * time.Hour,
			wantQuotaPU:  DefaultBalanceQuotaPerUnit,
			wantTimeout:  15 * time.Second,
			wantMaxConc:  2,
		},
		{
			name:         "new_api 托管渠道默认开启 auto",
			channel:      nil,
			managedKind:  "new_api",
			wantEnabled:  true,
			wantProvider: BalanceProviderAuto,
			wantInterval: 6 * time.Hour,
			wantQuotaPU:  DefaultBalanceQuotaPerUnit,
			wantTimeout:  15 * time.Second,
			wantMaxConc:  2,
		},
		{
			name:         "全局关闭覆盖托管默认",
			global:       &GlobalBalanceCheckConfig{Enabled: boolPtr(false)},
			channel:      nil,
			managedKind:  "new_api",
			wantEnabled:  false,
			wantProvider: BalanceProviderAuto,
			wantInterval: 6 * time.Hour,
			wantQuotaPU:  DefaultBalanceQuotaPerUnit,
			wantTimeout:  15 * time.Second,
			wantMaxConc:  2,
		},
		{
			name:         "全局开启覆盖非托管默认",
			global:       &GlobalBalanceCheckConfig{Enabled: boolPtr(true), DefaultIntervalMinutes: 120, MaxConcurrency: 5, TimeoutMs: 8000},
			channel:      nil,
			managedKind:  "",
			wantEnabled:  true,
			wantProvider: BalanceProviderAuto,
			wantInterval: 120 * time.Minute,
			wantQuotaPU:  DefaultBalanceQuotaPerUnit,
			wantTimeout:  8 * time.Second,
			wantMaxConc:  5,
		},
		{
			name:         "渠道级覆盖全局",
			global:       &GlobalBalanceCheckConfig{Enabled: boolPtr(true)},
			channel:      &ChannelBalanceCheckConfig{Enabled: boolPtr(false), Provider: "sub2api", IntervalMinutes: 60, QuotaPerUnit: 250000},
			managedKind:  "new_api",
			wantEnabled:  false,
			wantProvider: BalanceProviderSub2API,
			wantInterval: 60 * time.Minute,
			wantQuotaPU:  250000,
			wantTimeout:  15 * time.Second,
			wantMaxConc:  2,
		},
		{
			name:         "间隔硬下限 clamp 到 30 分钟",
			channel:      &ChannelBalanceCheckConfig{Provider: "newapi_token", IntervalMinutes: 5},
			managedKind:  "new_api",
			wantEnabled:  true,
			wantProvider: BalanceProviderNewapiToken,
			wantInterval: MinBalanceCheckInterval,
			wantQuotaPU:  DefaultBalanceQuotaPerUnit,
			wantTimeout:  15 * time.Second,
			wantMaxConc:  2,
		},
		{
			name:         "未知 provider 归一化为 auto",
			channel:      &ChannelBalanceCheckConfig{Provider: "deepseek"},
			managedKind:  "new_api",
			wantEnabled:  true,
			wantProvider: BalanceProviderAuto,
			wantInterval: 6 * time.Hour,
			wantQuotaPU:  DefaultBalanceQuotaPerUnit,
			wantTimeout:  15 * time.Second,
			wantMaxConc:  2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{BalanceCheck: tt.global}
			u := &UpstreamConfig{AutoManagedKind: tt.managedKind, BalanceCheck: tt.channel}
			p := cfg.ResolveBalancePolicy(u)
			if p.Enabled != tt.wantEnabled {
				t.Errorf("Enabled = %v, want %v", p.Enabled, tt.wantEnabled)
			}
			if p.Provider != tt.wantProvider {
				t.Errorf("Provider = %q, want %q", p.Provider, tt.wantProvider)
			}
			if p.Interval != tt.wantInterval {
				t.Errorf("Interval = %v, want %v", p.Interval, tt.wantInterval)
			}
			if p.QuotaPerUnit != tt.wantQuotaPU {
				t.Errorf("QuotaPerUnit = %v, want %v", p.QuotaPerUnit, tt.wantQuotaPU)
			}
			if p.Timeout != tt.wantTimeout {
				t.Errorf("Timeout = %v, want %v", p.Timeout, tt.wantTimeout)
			}
			if p.MaxConcurrency != tt.wantMaxConc {
				t.Errorf("MaxConcurrency = %v, want %v", p.MaxConcurrency, tt.wantMaxConc)
			}
		})
	}
}

func TestCloneChannelBalanceCheckConfig(t *testing.T) {
	var nilCfg *ChannelBalanceCheckConfig
	if got := cloneChannelBalanceCheckConfig(nilCfg); got != nil {
		t.Errorf("clone nil = %v, want nil", got)
	}
	enabled := true
	src := &ChannelBalanceCheckConfig{Enabled: &enabled, Provider: "sub2api"}
	cp := cloneChannelBalanceCheckConfig(src)
	if cp == src || cp.Enabled == src.Enabled {
		t.Fatalf("clone 应独立分配指针字段")
	}
	*cp.Enabled = false
	if *src.Enabled != true {
		t.Errorf("修改克隆不应影响原值")
	}

	// Clone() 全渠道深拷贝覆盖 BalanceCheck
	u := &UpstreamConfig{BalanceCheck: src}
	cloned := u.Clone()
	if cloned.BalanceCheck == u.BalanceCheck || cloned.BalanceCheck.Enabled == u.BalanceCheck.Enabled {
		t.Fatalf("UpstreamConfig.Clone 应深拷 BalanceCheck 指针字段")
	}
}
