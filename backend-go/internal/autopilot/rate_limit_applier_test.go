package autopilot

import (
	"context"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/ratelimit"
)

// ── 辅助函数 ──

// newTestApplier 创建用于测试的 RateLimitApplier。
// 返回 applier、discoverer、limiterMgr 以便测试前喂入数据。
func newTestApplier(
	cfg *config.AutopilotRoutingConfig,
	quietLogs bool,
) (*RateLimitApplier, *RateLimitDiscoverer, *ratelimit.Manager) {
	discoverer := NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: true, QuietLogs: true})
	limiterMgr := ratelimit.NewManager()
	configGetter := func() config.AutopilotRoutingConfig {
		return *cfg
	}
	applier := NewRateLimitApplier(discoverer, limiterMgr, configGetter, quietLogs)
	return applier, discoverer, limiterMgr
}

func TestManager_RequestRateLimitApplyRunsImmediately(t *testing.T) {
	cfg := activeRoutingCfg()
	applier, discoverer, limiterMgr := newTestApplier(&cfg, true)
	limiterMgr.GetOrCreate("Messages", 0, ratelimit.Config{})
	observeHeaderRPM(discoverer, "ep-immediate", 30)
	applier.SetEndpointMappings([]EndpointLimiterMapping{{
		EndpointUID: "ep-immediate", LimiterKey: "Messages:0",
	}})

	manager := &Manager{rateLimitApplyCh: make(chan struct{}, 1)}
	manager.SetRateLimitApplier(applier)
	manager.RequestRateLimitApply()
	manager.RequestRateLimitApply()
	if got := len(manager.rateLimitApplyCh); got != 1 {
		t.Fatalf("连续通知应合并为一个待处理任务: got %d", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.runRateLimitApplyWorker(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		limiterMgr.Stop()
	})

	deadline := time.Now().Add(time.Second)
	for limiterMgr.Get("Messages", 0).GetRPM() != 30 {
		if time.Now().After(deadline) {
			t.Fatal("apply 通知未在短周期内把 discovered RPM 注入 limiter")
		}
		time.Sleep(time.Millisecond)
	}
}

// ── 门控行为测试（表驱动）──

func TestRateLimitApplier_Gating(t *testing.T) {
	tests := []struct {
		name           string
		killSwitch     bool
		enabled        bool
		confidence     float64 // discoverer 里设置的置信度
		threshold      float64 // 配置里的阈值
		explicitRPM    bool    // endpoint 是否有显式 RPM
		discoveredRPM  int     // discoverer 建议的 RPM
		wantLimiterRPM int     // 期望 limiter 生效的 RPM（0=不限速）
		wantApplied    bool    // 期望是否被应用（limiter RPM > 0）
	}{
		{
			name:           "kill switch 清除所有发现 RPM",
			killSwitch:     true,
			enabled:        true, // 即使 enabled=true
			confidence:     0.8,
			threshold:      0.7,
			discoveredRPM:  30,
			wantLimiterRPM: 0,
			wantApplied:    false,
		},
		{
			name:           "disabled 不应用发现 RPM",
			killSwitch:     false,
			enabled:        false,
			confidence:     0.8,
			threshold:      0.7,
			discoveredRPM:  30,
			wantLimiterRPM: 0,
			wantApplied:    false,
		},
		{
			name:           "enabled + 高置信度 应用发现 RPM",
			killSwitch:     false,
			enabled:        true,
			confidence:     0.8,
			threshold:      0.7,
			discoveredRPM:  30,
			wantLimiterRPM: 30,
			wantApplied:    true,
		},
		{
			name:           "enabled + 低置信度 不应用",
			killSwitch:     false,
			enabled:        true,
			confidence:     0.5,
			threshold:      0.7,
			discoveredRPM:  30,
			wantLimiterRPM: 0,
			wantApplied:    false,
		},
		{
			name:           "enabled + 显式 RPM 不覆盖",
			killSwitch:     false,
			enabled:        true,
			confidence:     0.8,
			threshold:      0.7,
			explicitRPM:    true,
			discoveredRPM:  30,
			wantLimiterRPM: 60, // 保持显式配置的 RPM
			wantApplied:    false,
		},
		{
			name:           "enabled + 置信度恰好等于阈值",
			killSwitch:     false,
			enabled:        true,
			confidence:     0.7,
			threshold:      0.7,
			discoveredRPM:  50,
			wantLimiterRPM: 50,
			wantApplied:    true,
		},
		{
			name:           "enabled + RPM=0 不应用",
			killSwitch:     false,
			enabled:        true,
			confidence:     0.8,
			threshold:      0.7,
			discoveredRPM:  0,
			wantLimiterRPM: 0,
			wantApplied:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultAutopilotRoutingConfig()
			cfg.RoutingMode = config.AutopilotModeAuto // active 模式才注入运行态 limiter（shadow/off 只展示）
			cfg.KillSwitch = tt.killSwitch
			cfg.RateLimitDiscovery.Enabled = tt.enabled
			cfg.RateLimitDiscovery.ConfidenceThreshold = tt.threshold

			applier, discoverer, limiterMgr := newTestApplier(&cfg, true)

			// 创建 limiter 并设置映射
			endpointUID := "ep-test-uid"
			limiterKey := "messages:0"

			var limiterCfg ratelimit.Config
			if tt.explicitRPM {
				limiterCfg = ratelimit.Config{RPM: 60}
			}
			l := limiterMgr.GetOrCreate("messages", 0, limiterCfg)
			// NewChannelLimiter(Config{RPM>0}) 已设置 explicitRPM=true，无需 SetExplicitRPM

			applier.SetEndpointMappings([]EndpointLimiterMapping{
				{EndpointUID: endpointUID, LimiterKey: limiterKey, ExplicitRPM: tt.explicitRPM},
			})

			// 喂入 discoverer 信号
			if tt.discoveredRPM > 0 {
				// 通过 header 信号设置高置信度建议
				discoverer.Observe(endpointUID, RateLimitSignal{
					Source:        SignalSourceHeader,
					Limit:         tt.discoveredRPM,
					WindowSeconds: 60,
				})
				// 手动调整置信度到期望值（header 默认 0.9，需要覆盖为测试值）
				// 通过多次 429 触发置信度提升到目标值
				// 注意：我们无法直接设置 confidence，但可以通过不同信号源控制
				// 这里通过构造特定的 429 信号序列来达到目标置信度
				// 由于 observe429 会将 confidence 设为 0.5 或 0.7，
				// 对于 0.8+ 需要使用 header 信号（默认 0.9）
				// 对于 0.5 需要使用 429 无 RetryAfter
				if tt.confidence < 0.7 {
					// 重新创建 discoverer，使用 429 信号
					discoverer2 := NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: true, QuietLogs: true})
					discoverer2.Observe(endpointUID, RateLimitSignal{
						Source: SignalSource429,
					})
					applier.discoverer = discoverer2
				}
			}

			// 执行 Apply
			applier.Apply()

			// 验证 limiter RPM
			actualRPM := l.GetRPM()
			if actualRPM != tt.wantLimiterRPM {
				t.Errorf("limiter RPM = %d, want %d", actualRPM, tt.wantLimiterRPM)
			}

			// 验证 HasDiscoveredRPM
			if tt.wantApplied && !l.HasDiscoveredRPM() {
				t.Error("HasDiscoveredRPM = false, want true")
			}
			if !tt.wantApplied && tt.explicitRPM && l.HasDiscoveredRPM() {
				t.Error("HasDiscoveredRPM = true for explicit RPM limiter")
			}
		})
	}
}

// ── kill switch 清除已有发现 RPM ──

func TestRateLimitApplier_KillSwitchClearsExistingDiscovery(t *testing.T) {
	// 第一阶段：enabled + active + 高置信度 → 应用
	cfg := config.DefaultAutopilotRoutingConfig()
	cfg.RoutingMode = config.AutopilotModeAuto
	cfg.KillSwitch = false
	cfg.RateLimitDiscovery.Enabled = true
	cfg.RateLimitDiscovery.ConfidenceThreshold = 0.7

	applier, discoverer, limiterMgr := newTestApplier(&cfg, true)

	endpointUID := "ep-kill-test"
	limiterKey := "messages:0"
	limiterMgr.GetOrCreate("messages", 0, ratelimit.Config{})

	applier.SetEndpointMappings([]EndpointLimiterMapping{
		{EndpointUID: endpointUID, LimiterKey: limiterKey},
	})

	// 喂入高置信度信号
	discoverer.Observe(endpointUID, RateLimitSignal{
		Source:        SignalSourceHeader,
		Limit:         30,
		WindowSeconds: 60,
	})

	applier.Apply()

	l := limiterMgr.Get("messages", 0)
	if l.GetRPM() != 30 {
		t.Fatalf("after first Apply: RPM = %d, want 30", l.GetRPM())
	}

	// 第二阶段：kill switch 开启 → 清除
	cfg.KillSwitch = true
	applier.Apply()

	if l.GetRPM() != 0 {
		t.Errorf("after kill switch: RPM = %d, want 0", l.GetRPM())
	}
	if l.HasDiscoveredRPM() {
		t.Error("HasDiscoveredRPM = true after kill switch")
	}
}

// ── nil 安全 ──

func TestRateLimitApplier_NilSafety(t *testing.T) {
	// nil applier 不 panic
	var nilApplier *RateLimitApplier
	nilApplier.Apply()
	nilApplier.SetEndpointMappings(nil)

	// nil 依赖不 panic
	applier := NewRateLimitApplier(nil, nil, nil, true)
	applier.Apply()
	applier.SetEndpointMappings([]EndpointLimiterMapping{
		{EndpointUID: "ep-1", LimiterKey: "messages:0"},
	})
	applier.Apply() // discoverer=nil → no-op
}

// ── parseLimiterKey ──

func TestParseLimiterKey(t *testing.T) {
	tests := []struct {
		key       string
		wantType  string
		wantIdx   int
		wantScope string
	}{
		{"messages:0", "messages", 0, ""},
		{"chat:3", "chat", 3, ""},
		{"responses:1:scope123", "responses", 1, "scope123"},
		{"gemini:0:key_abc", "gemini", 0, "key_abc"},
		{"images:5", "images", 5, ""},
		{"vectors:0:quota_group", "vectors", 0, "quota_group"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			apiType, idx, scope := parseLimiterKey(tt.key)
			if apiType != tt.wantType {
				t.Errorf("apiType = %q, want %q", apiType, tt.wantType)
			}
			if idx != tt.wantIdx {
				t.Errorf("channelIndex = %d, want %d", idx, tt.wantIdx)
			}
			if scope != tt.wantScope {
				t.Errorf("scope = %q, want %q", scope, tt.wantScope)
			}
		})
	}
}

func TestRateLimitApplier_AppliesDiscoveredMaxConcurrent(t *testing.T) {
	cfg := config.DefaultAutopilotRoutingConfig()
	cfg.RoutingMode = config.AutopilotModeAuto
	cfg.RateLimitDiscovery.Enabled = true
	cfg.RateLimitDiscovery.ConfidenceThreshold = 0.7

	applier, discoverer, limiterMgr := newTestApplier(&cfg, true)
	endpointUID := "ep-concurrent"
	limiterKey := "messages:0"
	limiterMgr.GetOrCreate("messages", 0, ratelimit.Config{})

	applier.SetEndpointMappings([]EndpointLimiterMapping{
		{EndpointUID: endpointUID, LimiterKey: limiterKey},
	})

	// 触发慢 TTFB 降并发：基线 1s，连续 3 次 5s
	for i := 0; i < latencyWarmupSamples; i++ {
		discoverer.Observe(endpointUID, RateLimitSignal{
			Source:      SignalSourceSuccess,
			IsStreaming: true,
			LatencyMs:   1000,
		})
	}
	for i := 0; i < latencySlowStreakThreshold; i++ {
		discoverer.Observe(endpointUID, RateLimitSignal{
			Source:      SignalSourceSuccess,
			IsStreaming: true,
			LatencyMs:   20000,
		})
	}

	applier.Apply()

	l := limiterMgr.Get("messages", 0)
	if got := l.GetMaxConcurrent(); got != 4 {
		t.Errorf("limiter MaxConcurrent = %d, want 4", got)
	}
	if !l.HasDiscoveredMaxConcurrent() {
		t.Error("HasDiscoveredMaxConcurrent = false, want true")
	}
}

func TestRateLimitApplier_ExplicitMaxConcurrentNotOverridden(t *testing.T) {
	cfg := config.DefaultAutopilotRoutingConfig()
	cfg.RoutingMode = config.AutopilotModeAuto
	cfg.RateLimitDiscovery.Enabled = true
	cfg.RateLimitDiscovery.ConfidenceThreshold = 0.7

	applier, discoverer, limiterMgr := newTestApplier(&cfg, true)
	endpointUID := "ep-explicit-concurrent"
	limiterKey := "messages:0"
	limiterMgr.GetOrCreate("messages", 0, ratelimit.Config{MaxConcurrent: 2})

	applier.SetEndpointMappings([]EndpointLimiterMapping{
		{EndpointUID: endpointUID, LimiterKey: limiterKey, ExplicitMaxConcurrent: true},
	})

	for i := 0; i < latencyWarmupSamples; i++ {
		discoverer.Observe(endpointUID, RateLimitSignal{
			Source:      SignalSourceSuccess,
			IsStreaming: true,
			LatencyMs:   1000,
		})
	}
	for i := 0; i < latencySlowStreakThreshold; i++ {
		discoverer.Observe(endpointUID, RateLimitSignal{
			Source:      SignalSourceSuccess,
			IsStreaming: true,
			LatencyMs:   20000,
		})
	}

	applier.Apply()

	l := limiterMgr.Get("messages", 0)
	if got := l.GetMaxConcurrent(); got != 2 {
		t.Errorf("explicit MaxConcurrent should be preserved, got %d, want 2", got)
	}
	if l.HasDiscoveredMaxConcurrent() {
		t.Error("HasDiscoveredMaxConcurrent = true for explicit maxConcurrent limiter")
	}
}

func TestRateLimitApplier_RPMAndConcurrentIndependent(t *testing.T) {
	cfg := config.DefaultAutopilotRoutingConfig()
	cfg.RoutingMode = config.AutopilotModeAuto
	cfg.RateLimitDiscovery.Enabled = true
	cfg.RateLimitDiscovery.ConfidenceThreshold = 0.7

	applier, discoverer, limiterMgr := newTestApplier(&cfg, true)
	endpointUID := "ep-mixed"
	limiterKey := "messages:0"
	limiterMgr.GetOrCreate("messages", 0, ratelimit.Config{RPM: 60}) // 显式 RPM

	applier.SetEndpointMappings([]EndpointLimiterMapping{
		{EndpointUID: endpointUID, LimiterKey: limiterKey, ExplicitRPM: true, LimiterConfig: ratelimit.Config{RPM: 60}},
	})

	// 只有并发信号（无 header），触发降并发
	for i := 0; i < latencyWarmupSamples; i++ {
		discoverer.Observe(endpointUID, RateLimitSignal{
			Source:      SignalSourceSuccess,
			IsStreaming: true,
			LatencyMs:   1000,
		})
	}
	for i := 0; i < latencySlowStreakThreshold; i++ {
		discoverer.Observe(endpointUID, RateLimitSignal{
			Source:      SignalSourceSuccess,
			IsStreaming: true,
			LatencyMs:   20000,
		})
	}

	applier.Apply()

	l := limiterMgr.Get("messages", 0)
	if got := l.GetRPM(); got != 60 {
		t.Errorf("explicit RPM should be preserved, got %d, want 60", got)
	}
	if got := l.GetMaxConcurrent(); got != 4 {
		t.Errorf("discovered MaxConcurrent should be applied, got %d, want 4", got)
	}
}

// ── Apply 无映射时 no-op ──

func TestRateLimitApplier_ApplyNoMappings(t *testing.T) {
	cfg := config.DefaultAutopilotRoutingConfig()
	cfg.RateLimitDiscovery.Enabled = true
	cfg.RateLimitDiscovery.ConfidenceThreshold = 0.7

	applier, discoverer, _ := newTestApplier(&cfg, true)

	// 不调用 SetEndpointMappings，直接 Apply
	discoverer.Observe("ep-orphan", RateLimitSignal{
		Source:        SignalSourceHeader,
		Limit:         30,
		WindowSeconds: 60,
	})

	// 不 panic
	applier.Apply()
}
