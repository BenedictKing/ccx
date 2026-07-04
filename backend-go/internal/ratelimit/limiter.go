package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ChannelLimiter 是单个渠道的限速器，使用滑动窗口算法。
// 零值表示不限速（所有字段为 0 时 Acquire 立即成功）。
type ChannelLimiter struct {
	mu sync.Mutex

	// --- 滑动窗口 ---
	// window 是时间窗口大小（默认60秒=1分钟）
	// maxRequests 是窗口内允许的最大请求数（RPM）
	// timestamps 记录最近的请求时间戳
	window      time.Duration
	maxRequests int
	timestamps  []time.Time

	// --- 并发信号量 ---
	// maxConcurrent=0 表示不限并发。
	sem chan struct{}

	// --- 动态 cooldown ---
	// cooldownUntil 非零且在当前时间之后时，acquire 直接快速失败。
	cooldownUntil time.Time

	lastActivity time.Time
}

// Config 是 ChannelLimiter 的创建/更新配置。
type Config struct {
	// RPM 是每分钟请求数上限。0=不限。
	RPM int
	// WindowSeconds 是滑动窗口时长（秒）。0=默认60秒。
	WindowSeconds int
	// Burst 已废弃，保留字段仅为兼容性，不再使用。
	Burst int
	// MaxConcurrent 是最大并发上游请求数。0=不限。
	MaxConcurrent int
	// AutoFromHeaders 是否自动从上游响应头解析限流信息。默认 false。
	AutoFromHeaders bool
}

// errors
var (
	ErrInCooldown  = fmt.Errorf("rate limited: channel is in cooldown")
	ErrAcquireBusy = fmt.Errorf("rate limited: max concurrent requests reached")
	ErrWindowFull  = fmt.Errorf("rate limited: sliding window full")
)

// NewChannelLimiter 创建一个新的 ChannelLimiter。now 参数保留用于兼容性但不使用。
func NewChannelLimiter(cfg Config, now time.Time) *ChannelLimiter {
	l := &ChannelLimiter{
		timestamps: make([]time.Time, 0),
	}
	l.applyConfig(cfg)
	return l
}

// applyConfig 将配置应用到 limiter。
func (l *ChannelLimiter) applyConfig(cfg Config) {
	if cfg.RPM > 0 {
		l.maxRequests = cfg.RPM
		if cfg.WindowSeconds > 0 {
			l.window = time.Duration(cfg.WindowSeconds) * time.Second
		} else {
			l.window = 60 * time.Second // 默认1分钟
		}
	} else {
		l.maxRequests = 0
		l.window = 0
	}

	if cfg.MaxConcurrent > 0 {
		// 重新分配信号量：如果有变更需重建
		if l.sem == nil || cap(l.sem) != cfg.MaxConcurrent {
			l.sem = make(chan struct{}, cfg.MaxConcurrent)
		}
	} else {
		l.sem = nil
	}
}

// UpdateConfig 热更新配置，不丢失运行态 cooldown 和当前令牌。
// UpdateConfig 热更新配置，不丢失运行态 cooldown 和当前令牌。
// 若 cfg 与当前实际状态一致，直接返回，避免高并发 hot path 上无意义重建 sem 通道。
func (l *ChannelLimiter) UpdateConfig(cfg Config) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.configMatchesLocked(cfg) {
		return
	}
	l.applyConfig(cfg)
}

// configMatchesLocked 在持有 l.mu 时判断 cfg 是否已与 limiter 当前生效配置一致。
func (l *ChannelLimiter) configMatchesLocked(cfg Config) bool {
	// RPM / window
	if cfg.RPM > 0 {
		if l.maxRequests != cfg.RPM {
			return false
		}
		wantWindow := time.Duration(cfg.WindowSeconds) * time.Second
		if cfg.WindowSeconds <= 0 {
			wantWindow = 60 * time.Second
		}
		if l.window != wantWindow {
			return false
		}
	} else {
		if l.maxRequests != 0 || l.window != 0 {
			return false
		}
	}
	// 并发信号量
	if cfg.MaxConcurrent > 0 {
		if l.sem == nil || cap(l.sem) != cfg.MaxConcurrent {
			return false
		}
	} else {
		if l.sem != nil {
			return false
		}
	}
	return true
}

// InCooldown 返回当前是否处于动态 cooldown 期。
func (l *ChannelLimiter) InCooldown(now time.Time) (bool, time.Time) {
	if l == nil {
		return false, time.Time{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cooldownUntil.IsZero() || !now.Before(l.cooldownUntil) {
		return false, time.Time{}
	}
	return true, l.cooldownUntil
}

// SetCooldown 将渠道置入短期冷却；如果已有更晚的冷却截止时间，则保持原值。
func (l *ChannelLimiter) SetCooldown(until time.Time) {
	if l == nil || until.IsZero() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if until.After(l.cooldownUntil) {
		l.cooldownUntil = until
	}
	l.lastActivity = time.Now()
}

// LastActivity 返回最后活动时间（用于 reaper 判断是否可清理）。
func (l *ChannelLimiter) LastActivity() time.Time {
	if l == nil {
		return time.Time{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastActivity
}

// Acquire 尝试获取一个请求许可。返回 release 函数（必须在请求完成后调用以释放并发信号量）。
// maxWait 是最长排队等待时间；ctx 支持客户端断开取消。
// 返回的 error 可能是 ErrInCooldown / ErrAcquireBusy / ErrWindowFull / context.Canceled。
func (l *ChannelLimiter) Acquire(ctx context.Context, maxWait time.Duration, now time.Time) (release func(), err error) {
	if l == nil {
		return func() {}, nil
	}

	// 1. 检查 cooldown
	if released, ok := l.tryCooldown(now); !ok {
		return released, ErrInCooldown
	}

	// 2. 获取令牌（等待期间不占用并发信号量，避免排队请求挤占并发槽位）
	if err := l.acquireToken(ctx, maxWait, now); err != nil {
		return func() {}, err
	}

	// 3. 获取并发信号量（拿到令牌后才占槽，确保信号量反映真实在途请求数）
	release, err = l.acquireSemaphore(ctx, maxWait)
	if err != nil {
		return func() {}, err
	}

	return release, nil
}

// tryCooldown 检查并返回是否可继续。不可继续时返回一个空 release + false。
func (l *ChannelLimiter) tryCooldown(now time.Time) (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.cooldownUntil.IsZero() || !now.Before(l.cooldownUntil) {
		return func() {}, true
	}
	return func() {}, false
}

// acquireSemaphore 获取并发信号量，支持 ctx 取消和 maxWait 超时。
func (l *ChannelLimiter) acquireSemaphore(ctx context.Context, maxWait time.Duration) (func(), error) {
	if l.sem == nil {
		return func() {}, nil
	}

	// 快速尝试（非阻塞）
	select {
	case l.sem <- struct{}{}:
		return l.makeSemaphoreRelease(), nil
	default:
	}

	// 需要等待
	deadline := time.After(maxWait)
	for {
		select {
		case <-ctx.Done():
			return func() {}, ctx.Err()
		case <-deadline:
			return func() {}, ErrAcquireBusy
		case l.sem <- struct{}{}:
			return l.makeSemaphoreRelease(), nil
		}
	}
}

func (l *ChannelLimiter) makeSemaphoreRelease() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			<-l.sem
		})
	}
}

// acquireToken 使用滑动窗口检查是否允许请求。需要等待时循环 sleep，支持 ctx/timeout 取消。
func (l *ChannelLimiter) acquireToken(ctx context.Context, maxWait time.Duration, now time.Time) error {
	l.mu.Lock()

	if l.maxRequests <= 0 {
		// 不限速
		l.mu.Unlock()
		return nil
	}

	// 清理过期的时间戳（窗口外）
	l.cleanOldTimestampsLocked(now)

	if len(l.timestamps) < l.maxRequests {
		// 窗口未满，允许请求
		l.timestamps = append(l.timestamps, now)
		l.lastActivity = now
		l.mu.Unlock()
		return nil
	}

	// 窗口已满，计算最早的请求何时过期
	oldest := l.timestamps[0]
	waitDuration := l.window - now.Sub(oldest)
	l.mu.Unlock()

	if waitDuration > maxWait {
		return ErrWindowFull
	}

	// 等待窗口滚动
	deadline := time.After(maxWait)
	ticker := time.NewTicker(waitDuration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return ErrWindowFull
		case <-ticker.C:
			l.mu.Lock()
			currentNow := time.Now()
			l.cleanOldTimestampsLocked(currentNow)
			if len(l.timestamps) < l.maxRequests {
				l.timestamps = append(l.timestamps, currentNow)
				l.mu.Unlock()
				return nil
			}
			// 还是满的，缩小等待间隔
			if len(l.timestamps) > 0 {
				oldest := l.timestamps[0]
				nextWait := l.window - currentNow.Sub(oldest)
				if nextWait < 10*time.Millisecond {
					nextWait = 10 * time.Millisecond
				}
				ticker.Reset(nextWait)
			}
			l.mu.Unlock()
		}
	}
}

// cleanOldTimestampsLocked 清理窗口外的旧时间戳（需持有锁）。
func (l *ChannelLimiter) cleanOldTimestampsLocked(now time.Time) {
	cutoff := now.Add(-l.window)
	validIdx := 0
	for validIdx < len(l.timestamps) && l.timestamps[validIdx].Before(cutoff) {
		validIdx++
	}
	if validIdx > 0 {
		l.timestamps = l.timestamps[validIdx:]
	}
}

// Status 返回当前 limiter 状态快照（用于调试/日志）。
func (l *ChannelLimiter) Status(now time.Time) LimiterStatus {
	if l == nil {
		return LimiterStatus{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cleanOldTimestampsLocked(now)

	inCooldown := !l.cooldownUntil.IsZero() && now.Before(l.cooldownUntil)
	semUsed := 0
	if l.sem != nil {
		semUsed = len(l.sem)
	}
	l.lastActivity = now
	return LimiterStatus{
		WindowSize:      len(l.timestamps),
		MaxRequests:     l.maxRequests,
		MaxConcurrent:   cap(l.sem),
		ActiveRequests:  semUsed,
		InCooldown:      inCooldown,
		CooldownUntil:   l.cooldownUntil,
		AutoFromHeaders: false, // 由 Manager 层设置
	}
}

// LimiterStatus 是 limiter 的只读快照。
type LimiterStatus struct {
	WindowSize     int // 当前窗口内请求数
	MaxRequests    int // 窗口最大请求数（RPM）
	MaxConcurrent  int
	ActiveRequests int
	InCooldown     bool
	CooldownUntil  time.Time

	AutoFromHeaders bool
}

// Utilization 返回当前限速器的最大使用率：请求窗口使用率与并发使用率取较大值。
func (s LimiterStatus) Utilization() float64 {
	utilization := 0.0
	if s.MaxRequests > 0 {
		utilization = float64(s.WindowSize) / float64(s.MaxRequests)
	}
	if s.MaxConcurrent > 0 {
		concurrentUtilization := float64(s.ActiveRequests) / float64(s.MaxConcurrent)
		if concurrentUtilization > utilization {
			utilization = concurrentUtilization
		}
	}
	return utilization
}
