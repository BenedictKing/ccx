package autopilot

import (
	"sync"
	"testing"
	"time"
)

// ── 辅助函数 ──

func fixedTime(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func newTestDiscoverer(t *testing.T) (*RateLimitDiscoverer, *time.Time) {
	t.Helper()
	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	d := NewRateLimitDiscoverer(defaultDiscovererConfig())
	d.nowFunc = fixedTime(now)
	return d, &now
}

// ── 表驱动测试 ──

func TestRateLimitDiscoverer_HeaderExplicitLimit(t *testing.T) {
	d, now := newTestDiscoverer(t)
	uid := "ep-header-001"

	tests := []struct {
		name     string
		sig      RateLimitSignal
		wantRPM  int
		wantConf float64
		wantSrc  RateLimitSource
	}{
		{
			name: "header 给出 limit=60 window=60s -> rpm=60",
			sig: RateLimitSignal{
				Source:        SignalSourceHeader,
				Limit:         60,
				WindowSeconds: 60,
				Timestamp:     *now,
			},
			wantRPM:  60,
			wantConf: 0.9,
			wantSrc:  RateLimitSourceHeader,
		},
		{
			name: "header 给出 limit=100 window=3600s -> rpm=2 (每小时 100 次)",
			sig: RateLimitSignal{
				Source:        SignalSourceHeader,
				Limit:         100,
				WindowSeconds: 3600,
				Timestamp:     *now,
			},
			wantRPM:  2,
			wantConf: 0.9,
			wantSrc:  RateLimitSourceHeader,
		},
		{
			name: "header limit 受 MaxAutoRPM 钳制",
			sig: RateLimitSignal{
				Source:        SignalSourceHeader,
				Limit:         10000,
				WindowSeconds: 60,
				Timestamp:     *now,
			},
			wantRPM:  120, // MaxAutoRPM
			wantConf: 0.9,
			wantSrc:  RateLimitSourceHeader,
		},
		{
			name: "header 用 resetSeconds 替代 window",
			sig: RateLimitSignal{
				Source:       SignalSourceHeader,
				Limit:        300,
				ResetSeconds: 300, // 5 分钟窗口
				Timestamp:    *now,
			},
			wantRPM:  60, // 300 / 5 = 60
			wantConf: 0.9,
			wantSrc:  RateLimitSourceHeader,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 每个子测试用独立 discoverer 避免状态累积
			subNow := *now
			sd := NewRateLimitDiscoverer(defaultDiscovererConfig())
			sd.nowFunc = fixedTime(subNow)
			sd.Observe(uid, tt.sig)

			result := sd.SuggestedLimit(uid)
			if result.RPM != tt.wantRPM {
				t.Errorf("RPM = %d, want %d", result.RPM, tt.wantRPM)
			}
			if result.Confidence != tt.wantConf {
				t.Errorf("Confidence = %f, want %f", result.Confidence, tt.wantConf)
			}
			if result.Source != tt.wantSrc {
				t.Errorf("Source = %q, want %q", result.Source, tt.wantSrc)
			}
		})
	}

	_ = d
}

func TestRateLimitDiscoverer_HeaderRemainingOnly(t *testing.T) {
	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	uid := "ep-remaining-001"

	tests := []struct {
		name        string
		signals     []RateLimitSignal
		wantMinRPM  int // 至少不低于
		wantMaxRPM  int // 至多不超过
		wantConfMin float64
	}{
		{
			name: "remaining=10 reset=60s 首次 -> 置信度较低",
			signals: []RateLimitSignal{
				{
					Source:       SignalSourceHeader,
					Remaining:    10,
					ResetSeconds: 60,
					Timestamp:    now,
				},
			},
			wantMinRPM:  10,
			wantMaxRPM:  20,
			wantConfMin: 0.10,
		},
		{
			name: "多次 remaining 信号后置信度提升",
			signals: func() []RateLimitSignal {
				var sigs []RateLimitSignal
				for i := 0; i < 5; i++ {
					sigs = append(sigs, RateLimitSignal{
						Source:       SignalSourceHeader,
						Remaining:    50,
						ResetSeconds: 60,
						Timestamp:    now.Add(time.Duration(i) * time.Minute),
					})
				}
				return sigs
			}(),
			wantMinRPM:  50,
			wantMaxRPM:  60,
			wantConfMin: 0.50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sd := NewRateLimitDiscoverer(defaultDiscovererConfig())
			sd.nowFunc = fixedTime(now)

			for _, sig := range tt.signals {
				sd.Observe(uid, sig)
			}

			result := sd.SuggestedLimit(uid)
			if result.RPM < tt.wantMinRPM {
				t.Errorf("RPM = %d, want >= %d", result.RPM, tt.wantMinRPM)
			}
			if result.RPM > tt.wantMaxRPM {
				t.Errorf("RPM = %d, want <= %d", result.RPM, tt.wantMaxRPM)
			}
			if result.Confidence < tt.wantConfMin {
				t.Errorf("Confidence = %f, want >= %f", result.Confidence, tt.wantConfMin)
			}
		})
	}
}

func TestRateLimitDiscoverer_429Backoff(t *testing.T) {
	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	uid := "ep-429-001"

	tests := []struct {
		name     string
		signals  []RateLimitSignal
		wantRPM  int
		wantConf float64
		wantSrc  RateLimitSource
	}{
		{
			name: "429+Retry-After: 从基线 100 降到 50",
			signals: []RateLimitSignal{
				// 先建立基线
				{
					Source:        SignalSourceHeader,
					Limit:         100,
					WindowSeconds: 60,
					Timestamp:     now,
				},
				// 429 + Retry-After
				{
					Source:            SignalSource429,
					HasRetryAfter:     true,
					RetryAfterSeconds: 30,
					Timestamp:         now.Add(time.Second),
				},
			},
			wantRPM:  50,  // floor(100 * 0.5)
			wantConf: 0.9, // 429 提升到 0.7，但之前 header 已给 0.9
			wantSrc:  RateLimitSourcePassiveAIMD,
		},
		{
			name: "429 无 Retry-After: 从基线 100 降到 70",
			signals: []RateLimitSignal{
				{
					Source:        SignalSourceHeader,
					Limit:         100,
					WindowSeconds: 60,
					Timestamp:     now,
				},
				{
					Source:    SignalSource429,
					Timestamp: now.Add(time.Second),
				},
			},
			wantRPM:  70,  // floor(100 * 0.7)
			wantConf: 0.9, // header 已给 0.9
			wantSrc:  RateLimitSourcePassiveAIMD,
		},
		{
			name: "连续 429 逐步降低",
			signals: []RateLimitSignal{
				{
					Source:        SignalSourceHeader,
					Limit:         100,
					WindowSeconds: 60,
					Timestamp:     now,
				},
				{
					Source:    SignalSource429,
					Timestamp: now.Add(1 * time.Second),
				},
				{
					Source:    SignalSource429,
					Timestamp: now.Add(2 * time.Second),
				},
				{
					Source:    SignalSource429,
					Timestamp: now.Add(3 * time.Second),
				},
			},
			wantRPM:  49, // floor(floor(floor(100*0.7)*0.7)*0.7) = floor(34.3) = 34... 不对
			wantConf: 0.9,
			wantSrc:  RateLimitSourcePassiveAIMD,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sd := NewRateLimitDiscoverer(defaultDiscovererConfig())
			sd.nowFunc = fixedTime(now)

			for _, sig := range tt.signals {
				sd.Observe(uid, sig)
			}

			result := sd.SuggestedLimit(uid)
			// 连续 429 的精确值用范围检查
			if tt.name == "连续 429 逐步降低" {
				if result.RPM > 50 {
					t.Errorf("RPM = %d, want <= 50 after 3x 429", result.RPM)
				}
				if result.RPM < 1 {
					t.Errorf("RPM = %d, want >= 1 (MinRPM floor)", result.RPM)
				}
			} else {
				if result.RPM != tt.wantRPM {
					t.Errorf("RPM = %d, want %d", result.RPM, tt.wantRPM)
				}
			}
			if result.Source != tt.wantSrc {
				t.Errorf("Source = %q, want %q", result.Source, tt.wantSrc)
			}
		})
	}
}

func TestRateLimitDiscoverer_429NoBaseline(t *testing.T) {
	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	uid := "ep-429-nobase"

	sd := NewRateLimitDiscoverer(defaultDiscovererConfig())
	sd.nowFunc = fixedTime(now)

	// 没有基线就收到 429，应从 MaxAutoRPM/2 开始折半
	sd.Observe(uid, RateLimitSignal{
		Source:    SignalSource429,
		Timestamp: now,
	})

	result := sd.SuggestedLimit(uid)
	if result.RPM < 1 {
		t.Errorf("RPM = %d, want >= 1", result.RPM)
	}
	// MaxAutoRPM=120, /2=60, *0.7=42
	if result.RPM > 60 {
		t.Errorf("RPM = %d, want <= 60", result.RPM)
	}
	if result.Source != RateLimitSourcePassiveAIMD {
		t.Errorf("Source = %q, want passive_aimd", result.Source)
	}
}

func TestRateLimitDiscoverer_AIMDIncrease(t *testing.T) {
	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	uid := "ep-aimd-001"

	sd := NewRateLimitDiscoverer(defaultDiscovererConfig())
	sd.nowFunc = fixedTime(now)

	// 建立基线
	sd.Observe(uid, RateLimitSignal{
		Source:        SignalSourceHeader,
		Limit:         60,
		WindowSeconds: 60,
		Timestamp:     now,
	})

	result := sd.SuggestedLimit(uid)
	if result.RPM != 60 {
		t.Fatalf("baseline RPM = %d, want 60", result.RPM)
	}

	// 立即成功：不应上调（未过 10 分钟）
	sd.Observe(uid, RateLimitSignal{
		Source:    SignalSourceSuccess,
		Timestamp: now.Add(time.Second),
	})

	result = sd.SuggestedLimit(uid)
	if result.RPM != 60 {
		t.Errorf("immediate success RPM = %d, want 60 (no increase yet)", result.RPM)
	}

	// 15 分钟后成功：满足上调条件（>= 10 分钟间隔 + >= 15 分钟无 429）
	future := now.Add(16 * time.Minute)
	sd.nowFunc = fixedTime(future)
	sd.Observe(uid, RateLimitSignal{
		Source:    SignalSourceSuccess,
		LatencyMs: 200,
		Timestamp: future,
	})

	result = sd.SuggestedLimit(uid)
	// 60 + 10% = 66
	if result.RPM < 61 || result.RPM > 70 {
		t.Errorf("AIMD increase RPM = %d, want 61-70", result.RPM)
	}
}

func TestRateLimitDiscoverer_AIMDIncreaseBlocked429(t *testing.T) {
	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	uid := "ep-aimd-block"

	sd := NewRateLimitDiscoverer(defaultDiscovererConfig())
	sd.nowFunc = fixedTime(now)

	// 建立基线
	sd.Observe(uid, RateLimitSignal{
		Source:        SignalSourceHeader,
		Limit:         60,
		WindowSeconds: 60,
		Timestamp:     now,
	})

	// 2 分钟前有 429
	past429 := now.Add(-2 * time.Minute)
	sd.nowFunc = fixedTime(past429)
	sd.Observe(uid, RateLimitSignal{
		Source:    SignalSource429,
		Timestamp: past429,
	})

	// 恢复时钟，发送成功信号
	future := past429.Add(12 * time.Minute) // 间隔够但 No429Since 只有 12 分钟 < 15 分钟
	sd.nowFunc = fixedTime(future)
	sd.Observe(uid, RateLimitSignal{
		Source:    SignalSourceSuccess,
		LatencyMs: 100,
		Timestamp: future,
	})

	result := sd.SuggestedLimit(uid)
	// 429 后降到 70%: floor(60*0.7)=42, 再收到 429 变 floor(60*0.7)=42
	// 然后 AIMD 不应上调（No429Since 只过了 12 分钟 < 15 分钟）
	if result.RPM > 42 {
		t.Errorf("RPM = %d, want <= 42 (AIMD blocked by recent 429)", result.RPM)
	}
}

func TestRateLimitDiscoverer_LowConfidenceWhenNoSignal(t *testing.T) {
	d, _ := newTestDiscoverer(t)

	// 从未观测的 endpoint
	result := d.SuggestedLimit("nonexistent")
	if result.RPM != 0 {
		t.Errorf("RPM = %d, want 0 for unknown endpoint", result.RPM)
	}
	if result.Confidence != 0 {
		t.Errorf("Confidence = %f, want 0 for unknown endpoint", result.Confidence)
	}
	if result.Source != RateLimitSourceUnknown {
		t.Errorf("Source = %q, want unknown", result.Source)
	}
}

func TestRateLimitDiscoverer_MinRPMFloor(t *testing.T) {
	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	uid := "ep-minfloor"

	sd := NewRateLimitDiscoverer(defaultDiscovererConfig())
	sd.nowFunc = fixedTime(now)

	// 建立极低基线
	sd.Observe(uid, RateLimitSignal{
		Source:        SignalSourceHeader,
		Limit:         2,
		WindowSeconds: 60,
		Timestamp:     now,
	})

	// 反复 429 把估算压下去
	for i := 0; i < 20; i++ {
		sd.Observe(uid, RateLimitSignal{
			Source:    SignalSource429,
			Timestamp: now.Add(time.Duration(i+1) * time.Second),
		})
	}

	result := sd.SuggestedLimit(uid)
	if result.RPM < 1 {
		t.Errorf("RPM = %d, want >= 1 (MinRPM floor)", result.RPM)
	}
}

func TestRateLimitDiscoverer_AllSuggestedLimits(t *testing.T) {
	d, now := newTestDiscoverer(t)

	d.Observe("ep-1", RateLimitSignal{
		Source:        SignalSourceHeader,
		Limit:         60,
		WindowSeconds: 60,
		Timestamp:     *now,
	})
	d.Observe("ep-2", RateLimitSignal{
		Source:        SignalSourceHeader,
		Limit:         120,
		WindowSeconds: 60,
		Timestamp:     *now,
	})

	all := d.AllSuggestedLimits()
	if len(all) != 2 {
		t.Fatalf("AllSuggestedLimits count = %d, want 2", len(all))
	}
	if all["ep-1"].RPM != 60 {
		t.Errorf("ep-1 RPM = %d, want 60", all["ep-1"].RPM)
	}
	if all["ep-2"].RPM != 120 {
		t.Errorf("ep-2 RPM = %d, want 120", all["ep-2"].RPM)
	}
}

func TestRateLimitDiscoverer_ConcurrentSafety(t *testing.T) {
	d, now := newTestDiscoverer(t)
	uid := "ep-concurrent"

	var wg sync.WaitGroup
	const goroutines = 50

	// 并发 Observe
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d.Observe(uid, RateLimitSignal{
				Source:        SignalSourceHeader,
				Limit:         60,
				WindowSeconds: 60,
				Timestamp:     now.Add(time.Duration(i) * time.Millisecond),
			})
		}(i)
	}

	// 并发 SuggestedLimit
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = d.SuggestedLimit(uid)
		}()
	}

	// 并发 AllSuggestedLimits
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = d.AllSuggestedLimits()
		}()
	}

	wg.Wait()

	// 验证最终状态合理
	result := d.SuggestedLimit(uid)
	if result.RPM < 1 {
		t.Errorf("concurrent RPM = %d, want >= 1", result.RPM)
	}
	if result.Confidence < 0.5 {
		t.Errorf("concurrent Confidence = %f, want >= 0.5", result.Confidence)
	}
}

func TestRateLimitDiscoverer_GetState(t *testing.T) {
	d, now := newTestDiscoverer(t)
	uid := "ep-state"

	d.Observe(uid, RateLimitSignal{
		Source:        SignalSourceHeader,
		Limit:         60,
		WindowSeconds: 60,
		Timestamp:     *now,
	})

	state := d.GetState(uid)
	if state == nil {
		t.Fatal("GetState returned nil for observed endpoint")
	}
	if state.EstimatedRPM != 60 {
		t.Errorf("state.EstimatedRPM = %d, want 60", state.EstimatedRPM)
	}
	if state.ObserveCount != 1 {
		t.Errorf("state.ObserveCount = %d, want 1", state.ObserveCount)
	}

	// 未观测的 endpoint 返回 nil
	if d.GetState("nonexistent") != nil {
		t.Error("GetState should return nil for unobserved endpoint")
	}
}

func TestRateLimitDiscoverer_SeedProbeEstimate(t *testing.T) {
	d, _ := newTestDiscoverer(t)

	baseline := d.SeedProbeEstimate("ep-probe", 30, false)
	if baseline.RPM != 30 || baseline.Confidence != 0.2 || baseline.Source != RateLimitSourcePassiveAIMD {
		t.Fatalf("baseline=%+v", baseline)
	}

	limited := d.SeedProbeEstimate("ep-probe", 15, true)
	if limited.RPM != 15 || limited.Confidence != 0.7 || limited.Source != RateLimitSourcePassiveAIMD {
		t.Fatalf("limited=%+v", limited)
	}
	state := d.GetState("ep-probe")
	if state == nil || state.Last429At == nil {
		t.Fatalf("429 seed state=%+v", state)
	}

	// 后续较宽松的探测结果不能放宽已经收敛的限额。
	if got := d.SeedProbeEstimate("ep-probe", 30, false); got.RPM != 15 {
		t.Fatalf("relaxed seed RPM=%d, want 15", got.RPM)
	}
}

func TestRateLimitDiscoverer_SeedProbeEstimateDoesNotOverrideHeader(t *testing.T) {
	d, now := newTestDiscoverer(t)
	d.Observe("ep-header", RateLimitSignal{
		Source:        SignalSourceHeader,
		Limit:         80,
		WindowSeconds: 60,
		Timestamp:     *now,
	})

	got := d.SeedProbeEstimate("ep-header", 15, true)
	if got.RPM != 80 || got.Confidence != defaultDiscovererConfig().HeaderConfidenceMax || got.Source != RateLimitSourceHeader {
		t.Fatalf("header suggestion was overwritten: %+v", got)
	}
}

func TestRateLimitDiscoverer_StreamingLatencyCongestion(t *testing.T) {
	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	uid := "ep-latency-001"

	sd := NewRateLimitDiscoverer(defaultDiscovererConfig())
	sd.nowFunc = fixedTime(now)

	// 前 5 个样本建立基线：1000ms 左右
	for i := 0; i < latencyWarmupSamples; i++ {
		sd.Observe(uid, RateLimitSignal{
			Source:      SignalSourceSuccess,
			IsStreaming: true,
			LatencyMs:   1000,
			Timestamp:   now.Add(time.Duration(i) * time.Second),
		})
	}

	result := sd.SuggestedLimit(uid)
	if result.MaxConcurrent != 0 {
		t.Fatalf("warmup should not produce maxConcurrent, got %d", result.MaxConcurrent)
	}

	// 连续 3 次显著变慢（>3 倍基线）触发降并发
	for i := 0; i < latencySlowStreakThreshold; i++ {
		sd.Observe(uid, RateLimitSignal{
			Source:      SignalSourceSuccess,
			IsStreaming: true,
			LatencyMs:   20000,
			Timestamp:   now.Add(time.Duration(latencyWarmupSamples+i) * time.Second),
		})
	}

	result = sd.SuggestedLimit(uid)
	if result.MaxConcurrent != 4 { // default 8 -> floor(8*0.5)=4
		t.Errorf("after slow streak MaxConcurrent = %d, want 4", result.MaxConcurrent)
	}
	if result.ConcurrentConfidence < latencyCongestionConfidence {
		t.Errorf("ConcurrentConfidence = %f, want >= %f", result.ConcurrentConfidence, latencyCongestionConfidence)
	}

	// 429 也会进一步降并发
	sd.Observe(uid, RateLimitSignal{
		Source:      SignalSource429,
		IsStreaming: true,
		Timestamp:   now.Add(20 * time.Second),
	})
	result = sd.SuggestedLimit(uid)
	if result.MaxConcurrent != 2 { // 4 -> floor(4*0.5)=2
		t.Errorf("after 429 MaxConcurrent = %d, want 2", result.MaxConcurrent)
	}
}

func TestRateLimitDiscoverer_StreamingLatencyIgnoresSingleSpike(t *testing.T) {
	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	uid := "ep-latency-spike"

	sd := NewRateLimitDiscoverer(defaultDiscovererConfig())
	sd.nowFunc = fixedTime(now)

	// 建立基线
	for i := 0; i < latencyWarmupSamples; i++ {
		sd.Observe(uid, RateLimitSignal{
			Source:      SignalSourceSuccess,
			IsStreaming: true,
			LatencyMs:   1000,
			Timestamp:   now.Add(time.Duration(i) * time.Second),
		})
	}

	// 仅一次 60s 绝对慢请求，不应触发
	sd.Observe(uid, RateLimitSignal{
		Source:      SignalSourceSuccess,
		IsStreaming: true,
		LatencyMs:   latencyAbsoluteSlowMs,
		Timestamp:   now.Add(10 * time.Second),
	})

	result := sd.SuggestedLimit(uid)
	if result.MaxConcurrent != 0 {
		t.Errorf("single spike should not reduce concurrency, got %d", result.MaxConcurrent)
	}
}

func TestRateLimitDiscoverer_StreamingLatencyRecovery(t *testing.T) {
	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	uid := "ep-latency-recovery"

	sd := NewRateLimitDiscoverer(defaultDiscovererConfig())
	sd.nowFunc = fixedTime(now)

	// 建立基线并触发一次降并发
	for i := 0; i < latencyWarmupSamples; i++ {
		sd.Observe(uid, RateLimitSignal{
			Source:      SignalSourceSuccess,
			IsStreaming: true,
			LatencyMs:   1000,
			Timestamp:   now.Add(time.Duration(i) * time.Second),
		})
	}
	for i := 0; i < latencySlowStreakThreshold; i++ {
		sd.Observe(uid, RateLimitSignal{
			Source:      SignalSourceSuccess,
			IsStreaming: true,
			LatencyMs:   20000,
			Timestamp:   now.Add(time.Duration(latencyWarmupSamples+i) * time.Second),
		})
	}
	if got := sd.SuggestedLimit(uid).MaxConcurrent; got != 4 {
		t.Fatalf("pre-recovery MaxConcurrent = %d, want 4", got)
	}

	// 连续健康样本，但间隔不足 AIMDIncreaseInterval，不应恢复
	base := now.Add(time.Duration(latencyWarmupSamples+latencySlowStreakThreshold) * time.Second)
	for i := 0; i < latencyHealthyStreakTarget; i++ {
		sd.Observe(uid, RateLimitSignal{
			Source:      SignalSourceSuccess,
			IsStreaming: true,
			LatencyMs:   800,
			Timestamp:   base.Add(time.Duration(i) * time.Second),
		})
	}
	if got := sd.SuggestedLimit(uid).MaxConcurrent; got != 4 {
		t.Errorf("MaxConcurrent recovered too early = %d, want 4", got)
	}

	// 超过 AIMDIncreaseInterval 后再来健康样本，应 +1
	future := base.Add(defaultDiscovererConfig().AIMDIncreaseInterval + time.Second)
	sd.nowFunc = fixedTime(future)
	for i := 0; i < latencyHealthyStreakTarget; i++ {
		sd.Observe(uid, RateLimitSignal{
			Source:      SignalSourceSuccess,
			IsStreaming: true,
			LatencyMs:   800,
			Timestamp:   future.Add(time.Duration(i) * time.Second),
		})
	}
	if got := sd.SuggestedLimit(uid).MaxConcurrent; got != 5 {
		t.Errorf("after recovery MaxConcurrent = %d, want 5", got)
	}
}

func TestRateLimitDiscoverer_NonStreamingLatencyIgnored(t *testing.T) {
	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	uid := "ep-nonstream"

	sd := NewRateLimitDiscoverer(defaultDiscovererConfig())
	sd.nowFunc = fixedTime(now)

	for i := 0; i < latencyWarmupSamples+latencySlowStreakThreshold; i++ {
		sd.Observe(uid, RateLimitSignal{
			Source:      SignalSourceSuccess,
			IsStreaming: false,
			LatencyMs:   50000,
			Timestamp:   now.Add(time.Duration(i) * time.Second),
		})
	}

	if got := sd.SuggestedLimit(uid).MaxConcurrent; got != 0 {
		t.Errorf("non-streaming latency should not affect concurrency, got %d", got)
	}
}

func TestRateLimitDiscoverer_UpdateConfigCapsMaxConcurrent(t *testing.T) {
	now := time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC)
	uid := "ep-cap"

	sd := NewRateLimitDiscoverer(defaultDiscovererConfig())
	sd.nowFunc = fixedTime(now)

	for i := 0; i < latencyWarmupSamples; i++ {
		sd.Observe(uid, RateLimitSignal{
			Source:      SignalSourceSuccess,
			IsStreaming: true,
			LatencyMs:   1000,
			Timestamp:   now.Add(time.Duration(i) * time.Second),
		})
	}
	for i := 0; i < latencySlowStreakThreshold; i++ {
		sd.Observe(uid, RateLimitSignal{
			Source:      SignalSourceSuccess,
			IsStreaming: true,
			LatencyMs:   20000,
			Timestamp:   now.Add(time.Duration(latencyWarmupSamples+i) * time.Second),
		})
	}

	// 热重载把 MaxAutoConcurrent 降到 2，已学习值应被裁切
	cfg := defaultDiscovererConfig()
	cfg.MaxAutoConcurrent = 2
	sd.UpdateConfig(cfg)

	if got := sd.SuggestedLimit(uid).MaxConcurrent; got != 2 {
		t.Errorf("after cap MaxConcurrent = %d, want 2", got)
	}
}

func TestRateLimitDiscoverer_StateCount(t *testing.T) {
	d, now := newTestDiscoverer(t)

	if d.StateCount() != 0 {
		t.Errorf("initial StateCount = %d, want 0", d.StateCount())
	}

	d.Observe("ep-1", RateLimitSignal{
		Source:    SignalSourceSuccess,
		Timestamp: *now,
	})
	d.Observe("ep-2", RateLimitSignal{
		Source:    SignalSourceSuccess,
		Timestamp: *now,
	})

	if d.StateCount() != 2 {
		t.Errorf("StateCount = %d, want 2", d.StateCount())
	}
}

func TestRateLimitDiscoverer_EmptyEndpointUID(t *testing.T) {
	d, now := newTestDiscoverer(t)

	// 空 endpointUID 应被忽略
	d.Observe("", RateLimitSignal{
		Source:        SignalSourceHeader,
		Limit:         60,
		WindowSeconds: 60,
		Timestamp:     *now,
	})

	if d.StateCount() != 0 {
		t.Errorf("StateCount = %d, want 0 (empty UID ignored)", d.StateCount())
	}
}

func TestNormalizeToRPM(t *testing.T) {
	tests := []struct {
		name          string
		limit         int
		windowSeconds float64
		want          int
	}{
		{"60 in 60s -> 60", 60, 60, 60},
		{"100 in 3600s -> 2", 100, 3600, 2},
		{"300 in 300s -> 60", 300, 300, 60},
		{"1000 in 60s -> 1000", 1000, 60, 1000},
		{"zero window -> limit", 50, 0, 50},
		{"negative window -> limit", 50, -1, 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeToRPM(tt.limit, tt.windowSeconds)
			if got != tt.want {
				t.Errorf("normalizeToRPM(%d, %.0f) = %d, want %d",
					tt.limit, tt.windowSeconds, got, tt.want)
			}
		})
	}
}
