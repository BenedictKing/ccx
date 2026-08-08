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

	// --- 滑动窗口（生效态）---
	// window 是时间窗口大小（默认60秒=1分钟）
	// maxRequests 是窗口内允许的最大请求数（effective RPM）
	// timestamps 记录最近的请求时间戳
	window      time.Duration
	maxRequests int
	timestamps  []time.Time

	// --- 动态并发控制 ---
	// 始终统计在途请求，使运行时从不限并发切换到有限并发时也能正确收敛。
	maxConcurrent     int
	activeRequests    int
	concurrencyChange chan struct{}

	// --- 动态 cooldown ---
	// cooldownUntil 非零且在当前时间之后时，acquire 直接快速失败。
	cooldownUntil time.Time

	lastActivity time.Time

	// --- 配置态（来自 config.json）---
	// configuredRPM 是用户显式配置的 RPM；0 表示未配置。
	// configuredWindow 是用户配置的窗口时长；0 表示未配置（effective 时回退 60s）。
	// explicitRPM 等价于 configuredRPM > 0，保留为可读快照。
	configuredRPM    int
	configuredWindow time.Duration
	explicitRPM      bool

	// --- 发现态（来自 autopilot AIMD）---
	// discoveredRPM 由 autopilot 运行态注入；仅在 configuredRPM==0 时生效。
	// discoveredRPMSet 标记是否已设置（区分"0=未设置"与"建议值恰为 0"）。
	discoveredRPM    int
	discoveredRPMSet bool

	// configuredMaxConcurrent/discoveredMaxConcurrent 与 RPM 使用相同优先级语义。
	configuredMaxConcurrent    int
	discoveredMaxConcurrent    int
	discoveredMaxConcurrentSet bool
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
		timestamps:        make([]time.Time, 0),
		concurrencyChange: make(chan struct{}),
	}
	l.applyConfig(cfg)
	return l
}

// applyConfig 将配置应用到 limiter。
// 仅更新配置态字段；发现态不被零值配置清除，由 recomputeLocked 重算生效值。
func (l *ChannelLimiter) applyConfig(cfg Config) {
	l.configuredRPM = cfg.RPM
	l.explicitRPM = cfg.RPM > 0
	if cfg.RPM > 0 {
		if cfg.WindowSeconds > 0 {
			l.configuredWindow = time.Duration(cfg.WindowSeconds) * time.Second
		} else {
			l.configuredWindow = 60 * time.Second
		}
	} else {
		// cfg.RPM==0：清除配置态窗口，但保留 discoveredRPM（recomputeLocked 会处理）
		l.configuredWindow = 0
	}
	if cfg.MaxConcurrent > 0 {
		l.configuredMaxConcurrent = cfg.MaxConcurrent
	} else {
		l.configuredMaxConcurrent = 0
	}
	l.recomputeLocked()
}

// recomputeLocked 根据 configuredRPM/discoveredRPM 重算 effective maxRequests/window。
// 优先级：configuredRPM > 0 > discoveredRPM（若已设置且 > 0）> 0（不限速）。
func (l *ChannelLimiter) recomputeLocked() {
	var eff int
	var win time.Duration
	if l.configuredRPM > 0 {
		eff = l.configuredRPM
		win = l.configuredWindow
		if win <= 0 {
			win = 60 * time.Second
		}
	} else if l.discoveredRPMSet && l.discoveredRPM > 0 {
		eff = l.discoveredRPM
		// 发现态生效时沿用配置窗口（若有），否则默认 60s
		win = l.configuredWindow
		if win <= 0 {
			win = 60 * time.Second
		}
	}
	l.maxRequests = eff
	l.window = win

	effectiveConcurrent := 0
	if l.configuredMaxConcurrent > 0 {
		effectiveConcurrent = l.configuredMaxConcurrent
	} else if l.discoveredMaxConcurrentSet && l.discoveredMaxConcurrent > 0 {
		effectiveConcurrent = l.discoveredMaxConcurrent
	}
	if l.maxConcurrent != effectiveConcurrent {
		l.maxConcurrent = effectiveConcurrent
		l.notifyConcurrencyChangeLocked()
	}
}

// UpdateConfig 热更新配置，不丢失运行态 cooldown、已生效 discovered RPM 和当前令牌。
// 若 cfg 与当前配置态一致，直接返回，避免高并发 hot path 上无意义重建 sem 通道。
// cfg.RPM==0 不会清除已生效的 discoveredRPM；显式 RPM 的添加/移除通过 recomputeLocked
// 立即覆盖或恢复 discovered 值。
func (l *ChannelLimiter) UpdateConfig(cfg Config) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.configMatchesLocked(cfg) {
		return
	}
	l.applyConfig(cfg)
}

// configMatchesLocked 在持有 l.mu 时判断 cfg 是否已与 limiter 当前配置态一致。
// 只比较配置态字段（configuredRPM/configuredWindow/configuredMaxConcurrent），
// 不比较 effective maxRequests（否则 discovered 生效后会与 cfg.RPM 永远不匹配）。
func (l *ChannelLimiter) configMatchesLocked(cfg Config) bool {
	// 配置 RPM
	if l.configuredRPM != cfg.RPM {
		return false
	}
	// 配置窗口
	var wantWindow time.Duration
	if cfg.RPM > 0 {
		if cfg.WindowSeconds > 0 {
			wantWindow = time.Duration(cfg.WindowSeconds) * time.Second
		} else {
			wantWindow = 60 * time.Second
		}
	}
	if l.configuredWindow != wantWindow {
		return false
	}
	wantMaxConcurrent := cfg.MaxConcurrent
	if wantMaxConcurrent < 0 {
		wantMaxConcurrent = 0
	}
	if l.configuredMaxConcurrent != wantMaxConcurrent {
		return false
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

// ── 显式 RPM 与发现 RPM 管理 ──

// IsExplicitRPM 返回 RPM 是否由用户显式配置。
// 显式配置永远优先：SetDiscoveredRPM 在 explicitRPM=true 时直接返回 false。
func (l *ChannelLimiter) IsExplicitRPM() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.explicitRPM
}

// GetRPM 返回当前生效的 RPM 值（maxRequests / effective）。
// 优先级：configuredRPM > 0 > discoveredRPM > 0 > 0（不限速）。
func (l *ChannelLimiter) GetRPM() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.maxRequests
}

// SetDiscoveredRPM 应用 autopilot 发现的 RPM 到运行态 limiter。
// 返回 true 仅在真正改变了生效状态（首次注入、值变化或清除）时；
// 显式配置生效、非法值或 nil limiter 返回 false。
// rpm <= 0 表示清除发现覆盖（与 ClearDiscoveredRPM 等效）。
func (l *ChannelLimiter) SetDiscoveredRPM(rpm int) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	// 显式配置优先，发现值永不生效
	if l.explicitRPM {
		return false
	}

	if rpm <= 0 {
		if !l.discoveredRPMSet {
			return false
		}
		l.discoveredRPM = 0
		l.discoveredRPMSet = false
		l.recomputeLocked()
		return true
	}

	if l.discoveredRPMSet && l.discoveredRPM == rpm {
		return false
	}
	l.discoveredRPM = rpm
	l.discoveredRPMSet = true
	l.recomputeLocked()
	return true
}

// ClearDiscoveredRPM 清除发现 RPM 覆盖。
// 清除后生效值恢复为 configuredRPM（若有显式配置）或 0（不限速）。
// 即使显式 RPM 当前覆盖了发现值，也必须清除后者，避免 kill switch 或禁用
// Autopilot 后在用户移除显式配置时重新激活陈旧的 discovered RPM。
func (l *ChannelLimiter) ClearDiscoveredRPM() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	l.discoveredRPM = 0
	l.discoveredRPMSet = false
	l.recomputeLocked()
}

// HasDiscoveredRPM 返回当前是否有活跃的发现 RPM 覆盖。
func (l *ChannelLimiter) HasDiscoveredRPM() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.discoveredRPMSet
}

// ── 显式并发与发现并发管理 ──

// IsExplicitMaxConcurrent 返回最大并发是否由用户显式配置。
func (l *ChannelLimiter) IsExplicitMaxConcurrent() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.configuredMaxConcurrent > 0
}

// GetMaxConcurrent 返回当前生效的最大并发；0 表示不限并发。
func (l *ChannelLimiter) GetMaxConcurrent() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.maxConcurrent
}

// SetDiscoveredMaxConcurrent 应用 autopilot 发现的最大并发。
// 显式配置优先；maxConcurrent<=0 表示清除发现值。
func (l *ChannelLimiter) SetDiscoveredMaxConcurrent(maxConcurrent int) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.configuredMaxConcurrent > 0 {
		return false
	}
	if maxConcurrent <= 0 {
		if !l.discoveredMaxConcurrentSet {
			return false
		}
		l.discoveredMaxConcurrent = 0
		l.discoveredMaxConcurrentSet = false
		l.recomputeLocked()
		return true
	}
	if l.discoveredMaxConcurrentSet && l.discoveredMaxConcurrent == maxConcurrent {
		return false
	}
	l.discoveredMaxConcurrent = maxConcurrent
	l.discoveredMaxConcurrentSet = true
	l.recomputeLocked()
	return true
}

// ClearDiscoveredMaxConcurrent 清除发现并发，即使当前被显式配置遮蔽也会移除。
func (l *ChannelLimiter) ClearDiscoveredMaxConcurrent() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.discoveredMaxConcurrent = 0
	l.discoveredMaxConcurrentSet = false
	l.recomputeLocked()
}

// HasDiscoveredMaxConcurrent 返回是否保存了发现并发值。
func (l *ChannelLimiter) HasDiscoveredMaxConcurrent() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.discoveredMaxConcurrentSet
}

func (l *ChannelLimiter) notifyConcurrencyChangeLocked() {
	if l.concurrencyChange == nil {
		l.concurrencyChange = make(chan struct{})
		return
	}
	close(l.concurrencyChange)
	l.concurrencyChange = make(chan struct{})
}

// Acquire 尝试获取一个请求许可。返回 release 函数（必须在请求完成后调用以释放并发占用）。
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

	// 2. 获取令牌（等待期间不占用并发额度，避免排队请求挤占并发槽位）
	if err := l.acquireToken(ctx, maxWait, now); err != nil {
		return func() {}, err
	}

	// 3. 获取并发额度（拿到令牌后才占槽，确保计数反映真实在途请求数）
	release, err = l.acquireConcurrency(ctx, maxWait)
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

// acquireConcurrency 获取动态并发额度，支持运行中升降上限、ctx 取消和超时。
func (l *ChannelLimiter) acquireConcurrency(ctx context.Context, maxWait time.Duration) (func(), error) {
	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	for {
		l.mu.Lock()
		if l.maxConcurrent <= 0 || l.activeRequests < l.maxConcurrent {
			l.activeRequests++
			l.lastActivity = time.Now()
			l.mu.Unlock()
			return l.makeConcurrencyRelease(), nil
		}
		changed := l.concurrencyChange
		l.mu.Unlock()

		select {
		case <-ctx.Done():
			return func() {}, ctx.Err()
		case <-timer.C:
			return func() {}, ErrAcquireBusy
		case <-changed:
		}
	}
}

func (l *ChannelLimiter) makeConcurrencyRelease() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			if l.activeRequests > 0 {
				l.activeRequests--
			}
			l.lastActivity = time.Now()
			l.notifyConcurrencyChangeLocked()
			l.mu.Unlock()
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
	l.lastActivity = now
	return LimiterStatus{
		WindowSize:      len(l.timestamps),
		MaxRequests:     l.maxRequests,
		MaxConcurrent:   l.maxConcurrent,
		ActiveRequests:  l.activeRequests,
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
