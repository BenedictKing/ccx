package ratelimit

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

// ── 令牌桶 ──

func TestLimiter_Acquire_ZeroConfig_NoLimit(t *testing.T) {
	l := NewChannelLimiter(Config{}, time.Now())
	for i := 0; i < 100; i++ {
		release, err := l.Acquire(context.Background(), time.Second, time.Now())
		if err != nil {
			t.Fatalf("attempt %d: unexpected error: %v", i, err)
		}
		release()
	}
}

func TestLimiter_Acquire_TokenBucket_AllowsBurstThenBlocks(t *testing.T) {
	// 滑动窗口：RPM=3 表示 60 秒内最多 3 次请求
	now := time.Now()
	l := NewChannelLimiter(Config{RPM: 3}, now)

	// 前 3 次立即成功（窗口未满）
	for i := 0; i < 3; i++ {
		release, err := l.Acquire(context.Background(), 100*time.Millisecond, now)
		if err != nil {
			t.Fatalf("request %d: unexpected error: %v", i, err)
		}
		release()
	}

	// 第 4 次应失败（窗口已满，需要等待最早的请求过期）
	_, err := l.Acquire(context.Background(), 100*time.Millisecond, now)
	if err != ErrWindowFull {
		t.Fatalf("expected ErrWindowFull, got %v", err)
	}
}

func TestLimiter_Acquire_TokenBucket_Refill(t *testing.T) {
	// 滑动窗口：RPM=2 表示 60 秒内最多 2 次请求
	base := time.Now()
	l := NewChannelLimiter(Config{RPM: 2}, base)

	// 消耗窗口配额
	for i := 0; i < 2; i++ {
		release, _ := l.Acquire(context.Background(), time.Millisecond, base)
		release()
	}

	// 60 秒后窗口滚动，应可以再次请求
	later := base.Add(61 * time.Second)
	release, err := l.Acquire(context.Background(), time.Millisecond, later)
	if err != nil {
		t.Fatalf("after window roll: unexpected error: %v", err)
	}
	release()
}

// ── 并发信号量 ──

func TestLimiter_Acquire_ConcurrentLimit(t *testing.T) {
	l := NewChannelLimiter(Config{MaxConcurrent: 2}, time.Now())

	ctx := context.Background()
	r1, _ := l.Acquire(ctx, 50*time.Millisecond, time.Now())
	r2, _ := l.Acquire(ctx, 50*time.Millisecond, time.Now())

	// 第 3 次应快速失败
	_, err := l.Acquire(ctx, 50*time.Millisecond, time.Now())
	if err != ErrAcquireBusy {
		t.Fatalf("expected ErrAcquireBusy, got %v", err)
	}

	// 释放一个后可再次获取
	r1()
	r3, err := l.Acquire(ctx, 50*time.Millisecond, time.Now())
	if err != nil {
		t.Fatalf("after release: unexpected error: %v", err)
	}
	r3()
	r2()
}

func TestLimiter_Acquire_ConcurrentCancel(t *testing.T) {
	l := NewChannelLimiter(Config{MaxConcurrent: 1}, time.Now())

	r1, _ := l.Acquire(context.Background(), time.Second, time.Now())
	defer r1()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := l.Acquire(ctx, time.Second, time.Now())
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// ── cooldown ──

func TestLimiter_InCooldown(t *testing.T) {
	now := time.Now()
	l := NewChannelLimiter(Config{RPM: 60}, now)

	// 初始无 cooldown
	in, _ := l.InCooldown(now)
	if in {
		t.Fatal("expected no cooldown initially")
	}

	// 设置 cooldown（通过 ApplyUpstreamHints 模拟 429+Retry-After）
	headers := http.Header{}
	headers.Set("Retry-After", "30")
	l.ApplyUpstreamHints(headers, http.StatusTooManyRequests, now)

	in, until := l.InCooldown(now)
	if !in {
		t.Fatal("expected in cooldown after 429")
	}
	if until.Sub(now) != 30*time.Second {
		t.Fatalf("cooldown duration = %v, want 30s", until.Sub(now))
	}

	// cooldown 期间 acquire 失败
	_, err := l.Acquire(context.Background(), time.Millisecond, now)
	if err != ErrInCooldown {
		t.Fatalf("expected ErrInCooldown, got %v", err)
	}

	// 30 秒后恢复
	later := now.Add(31 * time.Second)
	in, _ = l.InCooldown(later)
	if in {
		t.Fatal("expected cooldown expired after 31s")
	}
	release, err := l.Acquire(context.Background(), time.Millisecond, later)
	if err != nil {
		t.Fatalf("after cooldown expired: unexpected error: %v", err)
	}
	release()
}

func TestLimiter_Acquire_ConcurrentSafety(t *testing.T) {
	l := NewChannelLimiter(Config{RPM: 600, Burst: 100, MaxConcurrent: 10}, time.Now())

	var wg sync.WaitGroup
	errs := make(chan error, 200)

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := l.Acquire(context.Background(), 500*time.Millisecond, time.Now())
			if err != nil {
				errs <- err
				return
			}
			time.Sleep(time.Millisecond)
			release()
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != ErrWindowFull && err != ErrAcquireBusy {
			t.Fatalf("unexpected error type: %v", err)
		}
	}
}

// ── UpdateConfig ──

func TestLimiter_UpdateConfig_PreservesTokens(t *testing.T) {
	base := time.Now()
	l := NewChannelLimiter(Config{RPM: 60, Burst: 10}, base)

	// 消耗 5 个令牌
	for i := 0; i < 5; i++ {
		release, _ := l.Acquire(context.Background(), time.Millisecond, base)
		release()
	}

	// 更新配置但保持 RPM 不变
	l.UpdateConfig(Config{RPM: 60, Burst: 10})

	// 当前 tokens 应该约 5（从剩余 5），仍能成功 5 次
	for i := 0; i < 5; i++ {
		release, err := l.Acquire(context.Background(), time.Millisecond, base)
		if err != nil {
			t.Fatalf("attempt %d after UpdateConfig: %v", i, err)
		}
		release()
	}
}

// ── 窗口等待不占信号量 ──

func TestLimiter_Acquire_TokenWaitDoesNotBlockSemaphore(t *testing.T) {
	// 验证：当滑动窗口满时在等待，信号量槽位不被占用，
	// 新的请求仍能获取信号量（如果它们的窗口配额可用）。
	base := time.Now()
	// RPM=1, MaxConcurrent=1
	// 窗口只允许 1 个请求，信号量只有 1 个槽
	l := NewChannelLimiter(Config{RPM: 1, MaxConcurrent: 1}, base)

	// 第 1 个请求获取窗口配额 + 信号量，成功
	release1, err := l.Acquire(context.Background(), 10*time.Millisecond, base)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release1()

	// 第 2 个请求：窗口已满 → acquireToken 应快速失败（maxWait 10ms < 窗口滚动需要 60s）
	_, err = l.Acquire(context.Background(), 10*time.Millisecond, base)
	if err != ErrWindowFull {
		t.Fatalf("second acquire expected ErrWindowFull, got %v", err)
	}

	// 关键：信号量槽位只被第 1 个请求占用。
	// 如果 acquireToken 在 acquireSemaphore 之前（当前实现），
	// 第 2 个请求在窗口层就退出了，不会尝试信号量。
	// 验证方式：释放第 1 个请求后，窗口滚动后新请求应能成功（信号量未被第 2 个阻塞）
	release1()

	// 等 61 秒让窗口滚动
	later := base.Add(61 * time.Second)
	release3, err := l.Acquire(context.Background(), 10*time.Millisecond, later)
	if err != nil {
		t.Fatalf("third acquire after window roll: %v", err)
	}
	release3()
}

// ── header 解析 ──

func TestLimiter_ApplyUpstreamHints_RetryAfterSeconds(t *testing.T) {
	base := time.Now()
	l := NewChannelLimiter(Config{RPM: 60}, base)

	headers := http.Header{"Retry-After": {"45"}}
	l.ApplyUpstreamHints(headers, http.StatusTooManyRequests, base)

	in, until := l.InCooldown(base)
	if !in {
		t.Fatal("expected cooldown")
	}
	if d := until.Sub(base); d != 45*time.Second {
		t.Fatalf("cooldown = %v, want 45s", d)
	}
}

func TestLimiter_ApplyUpstreamHints_RetryAfterHTTPDate(t *testing.T) {
	base := time.Now()
	l := NewChannelLimiter(Config{RPM: 60}, base)

	future := base.Add(20 * time.Second).UTC().Format(http.TimeFormat)
	headers := http.Header{"Retry-After": {future}}
	l.ApplyUpstreamHints(headers, http.StatusTooManyRequests, base)

	in, until := l.InCooldown(base)
	if !in {
		t.Fatal("expected cooldown")
	}
	d := until.Sub(base)
	if d < 19*time.Second || d > 21*time.Second {
		t.Fatalf("cooldown = %v, want ~20s", d)
	}
}


func TestLimiter_ApplyUpstreamHints_5xxRetryAfter(t *testing.T) {
	base := time.Now()
	l := NewChannelLimiter(Config{RPM: 60}, base)

	// 503 + Retry-After 应触发 cooldown（上游临时过载的退避指示）
	headers := http.Header{"Retry-After": {"30"}}
	l.ApplyUpstreamHints(headers, http.StatusServiceUnavailable, base)

	in, until := l.InCooldown(base)
	if !in {
		t.Fatal("expected cooldown on 503 with Retry-After")
	}
	if d := until.Sub(base); d != 30*time.Second {
		t.Fatalf("cooldown = %v, want 30s", d)
	}
}

func TestLimiter_ApplyUpstreamHints_5xxNoRetryAfter(t *testing.T) {
	base := time.Now()
	l := NewChannelLimiter(Config{RPM: 60}, base)

	// 503 无 Retry-After 不触发 cooldown（由 failover 层 body 识别处理）
	headers := http.Header{}
	l.ApplyUpstreamHints(headers, http.StatusServiceUnavailable, base)

	in, _ := l.InCooldown(base)
	if in {
		t.Fatal("should not cooldown on 503 without Retry-After")
	}
}

func TestLimiter_ApplyUpstreamHints_AnthropicRemainingLow(t *testing.T) {
	base := time.Now()
	l := NewChannelLimiter(Config{RPM: 60}, base)

	resetTime := base.Add(10 * time.Second).Format(time.RFC3339)
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-requests-remaining", "0")
	headers.Set("anthropic-ratelimit-requests-reset", resetTime)
	l.ApplyUpstreamHints(headers, http.StatusOK, base) // 200 也解析

	in, until := l.InCooldown(base)
	if !in {
		t.Fatal("expected cooldown from Anthropic remaining=0")
	}
	d := until.Sub(base)
	if d < 9*time.Second || d > 11*time.Second {
		t.Fatalf("cooldown = %v, want ~10s", d)
	}
}

func TestLimiter_ApplyUpstreamHints_AnthropicRemainingOK(t *testing.T) {
	base := time.Now()
	l := NewChannelLimiter(Config{RPM: 60}, base)

	resetTime := base.Add(10 * time.Second).Format(time.RFC3339)
	headers := http.Header{}
	headers.Set("anthropic-ratelimit-requests-remaining", "50")
	headers.Set("anthropic-ratelimit-requests-reset", resetTime)
	l.ApplyUpstreamHints(headers, http.StatusOK, base)

	in, _ := l.InCooldown(base)
	if in {
		t.Fatal("should not cooldown when remaining=50")
	}
}

func TestLimiter_ApplyUpstreamHints_OpenAIRemainingLow(t *testing.T) {
	base := time.Now()
	l := NewChannelLimiter(Config{RPM: 60}, base)

	headers := http.Header{}
	headers.Set("x-ratelimit-remaining-requests", "0")
	headers.Set("x-ratelimit-reset-requests", "5s")
	l.ApplyUpstreamHints(headers, http.StatusOK, base)

	in, until := l.InCooldown(base)
	if !in {
		t.Fatal("expected cooldown from OpenAI remaining=0")
	}
	d := until.Sub(base)
	if d != 5*time.Second {
		t.Fatalf("cooldown = %v, want 5s", d)
	}
}

func TestLimiter_ApplyUpstreamHints_IgnoresOnNon429WithoutRemaining(t *testing.T) {
	base := time.Now()
	l := NewChannelLimiter(Config{RPM: 60}, base)

	// 200 + 无 remaining header → 不触发 cooldown
	headers := http.Header{"Retry-After": {"10"}}
	l.ApplyUpstreamHints(headers, http.StatusOK, base)

	in, _ := l.InCooldown(base)
	if in {
		t.Fatal("should not cooldown on 200 with only Retry-After (no remaining)")
	}
}

func TestLimiter_SetCooldown(t *testing.T) {
	base := time.Now()
	l := NewChannelLimiter(Config{}, base)

	l.SetCooldown(base.Add(30 * time.Second))
	in, until := l.InCooldown(base)
	if !in {
		t.Fatal("expected cooldown")
	}
	if d := until.Sub(base); d != 30*time.Second {
		t.Fatalf("cooldown = %v, want 30s", d)
	}

	l.SetCooldown(base.Add(10 * time.Second))
	_, until = l.InCooldown(base)
	if d := until.Sub(base); d != 30*time.Second {
		t.Fatalf("shorter cooldown should not replace existing, got %v", d)
	}
}

// ── nil limiter safety ──

func TestLimiter_NilSafety(t *testing.T) {
	var l *ChannelLimiter
	release, err := l.Acquire(context.Background(), time.Second, time.Now())
	if err != nil {
		t.Fatalf("nil limiter Acquire: %v", err)
	}
	release()

	in, _ := l.InCooldown(time.Now())
	if in {
		t.Fatal("nil limiter should not be in cooldown")
	}
}

// ── Status ──

func TestLimiter_Status(t *testing.T) {
	now := time.Now()
	l := NewChannelLimiter(Config{RPM: 120, MaxConcurrent: 3}, now)

	s := l.Status(now)
	if s.MaxRequests != 120 {
		t.Fatalf("maxRequests = %v, want 120", s.MaxRequests)
	}
	if s.WindowSize != 0 {
		t.Fatalf("windowSize = %v, want 0", s.WindowSize)
	}
	if s.MaxConcurrent != 3 {
		t.Fatalf("maxConcurrent = %v, want 3", s.MaxConcurrent)
	}
	if s.InCooldown {
		t.Fatal("should not be in cooldown")
	}
}
