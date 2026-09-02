package autopilot

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BenedictKing/ccx/internal/quota"
)

// ── 默认配置 ──

const (
	DefaultRefreshInterval    = 24 * time.Hour   // 每张卡默认刷新间隔
	DefaultRefreshDailyBudget = 100              // 每日刷新调用上限
	DefaultRefreshTimeout     = 15 * time.Second // 单次刷新超时
)

// SubscriptionRefreshWorkerConfig 刷新 worker 可调参数。
type SubscriptionRefreshWorkerConfig struct {
	RefreshInterval time.Duration    // 刷新循环间隔，默认 24h
	DailyBudget     int              // 每日刷新调用上限，默认 100
	RefreshTimeout  time.Duration    // 单次刷新超时，默认 15s
	QuietLogs       bool             // 是否静默日志
	TimeFunc        func() time.Time // 自定义时钟（测试用）
}

func (c SubscriptionRefreshWorkerConfig) withDefaults() SubscriptionRefreshWorkerConfig {
	if c.RefreshInterval <= 0 {
		c.RefreshInterval = DefaultRefreshInterval
	}
	if c.DailyBudget <= 0 {
		c.DailyBudget = DefaultRefreshDailyBudget
	}
	if c.RefreshTimeout <= 0 {
		c.RefreshTimeout = DefaultRefreshTimeout
	}
	if c.TimeFunc == nil {
		c.TimeFunc = time.Now
	}
	return c
}

// ── 刷新结果 ──

// SubscriptionRefreshResult 单次余额刷新结果。
type SubscriptionRefreshResult struct {
	SubscriptionUID string
	Provider        string
	Balance         float64
	Currency        string
	Success         bool
	ErrorMessage    string
	FetchedAt       time.Time
}

// ── 每日预算 ──

// RefreshBudget 全局每日刷新预算。
// 复用 ProbeBudget 的设计模式：atomic 操作 + 跨天自动重置。
type RefreshBudget struct {
	dailyLimit int32
	used       atomic.Int32
	resetDay   atomic.Int32
	timeFunc   func() time.Time
	mu         sync.Mutex
}

// NewRefreshBudget 创建每日刷新预算。
func NewRefreshBudget(dailyLimit int, timeFunc func() time.Time) *RefreshBudget {
	if dailyLimit <= 0 {
		dailyLimit = DefaultRefreshDailyBudget
	}
	if timeFunc == nil {
		timeFunc = time.Now
	}
	b := &RefreshBudget{
		dailyLimit: int32(dailyLimit),
		timeFunc:   timeFunc,
	}
	now := b.timeFunc()
	b.resetDay.Store(int32(now.UTC().Unix() / 86400))
	return b
}

func (b *RefreshBudget) dayKey() int32 {
	return int32(b.timeFunc().UTC().Unix() / 86400)
}

func (b *RefreshBudget) maybeReset() {
	today := b.dayKey()
	if b.resetDay.Load() != today {
		b.mu.Lock()
		defer b.mu.Unlock()
		if b.resetDay.Load() != today {
			b.used.Store(0)
			b.resetDay.Store(today)
			log.Printf("[RefreshBudget-Reset] 每日刷新预算已重置 (limit=%d)", b.dailyLimit)
		}
	}
}

// TryConsume 尝试消耗一次刷新额度。
func (b *RefreshBudget) TryConsume() bool {
	b.maybeReset()
	for {
		cur := b.used.Load()
		if cur >= b.dailyLimit {
			return false
		}
		if b.used.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

// Remaining 返回今日剩余刷新额度。
func (b *RefreshBudget) Remaining() int {
	b.maybeReset()
	return int(b.dailyLimit) - int(b.used.Load())
}

// Used 返回今日已用刷新额度。
func (b *RefreshBudget) Used() int {
	b.maybeReset()
	return int(b.used.Load())
}

// ── SubscriptionRefreshWorker ──

// SubscriptionRefreshWorker 订阅余额自动刷新 worker。
// 定期扫描 SubscriptionStore 中满足条件的订阅，通过对应的 BalanceFetcher 查询余额并回写。
// 条件（双重门控）：
//   - 全局配置 SubscriptionAutoRefresh.Enabled = true
//   - 订阅 AutoRefreshEnabled = true 且 BillingAPIKey 非空
//   - Provider 在 supportedAutoRefreshProviders 白名单内
type SubscriptionRefreshWorker struct {
	subStore *SubscriptionStore
	fetchers *BalanceFetcherRegistry
	config   SubscriptionRefreshWorkerConfig
	budget   *RefreshBudget
	timeFn   func() time.Time
	enabled  func() bool // 从配置读取全局开关（热重载感知）
	quotaMu  sync.RWMutex
	quota    *quota.Manager // 余额结果接入配额真相体系（可选，nil = 不接入）

	cancel func()
	wg     sync.WaitGroup
}

// NewSubscriptionRefreshWorker 创建刷新 worker。
// enabled 回调用于读取全局开关（热重载感知）。
func NewSubscriptionRefreshWorker(
	subStore *SubscriptionStore,
	fetchers *BalanceFetcherRegistry,
	cfg SubscriptionRefreshWorkerConfig,
	enabled func() bool,
) *SubscriptionRefreshWorker {
	cfg = cfg.withDefaults()
	if fetchers == nil {
		fetchers = DefaultBalanceFetcherRegistry()
	}
	if enabled == nil {
		enabled = func() bool { return false }
	}
	return &SubscriptionRefreshWorker{
		subStore: subStore,
		fetchers: fetchers,
		config:   cfg,
		budget:   NewRefreshBudget(cfg.DailyBudget, cfg.TimeFunc),
		timeFn:   cfg.TimeFunc,
		enabled:  enabled,
	}
}

// Start 启动刷新 worker 后台循环。
func (w *SubscriptionRefreshWorker) Start(ctx context.Context) {
	ctx, w.cancel = context.WithCancel(ctx)
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.run(ctx)
	}()
	if !w.config.QuietLogs {
		log.Printf("[SubscriptionRefreshWorker-Start] 余额刷新 worker 已启动 (interval=%s, budget=%d/day)",
			w.config.RefreshInterval, w.config.DailyBudget)
	}
}

// SetQuotaManager 注入配额管理器（main.go 在创建后、Start 前调用）。
// 注入后余额刷新结果会同步到配额真相体系（provider_api 级），供调度评分消费。
func (w *SubscriptionRefreshWorker) SetQuotaManager(qm *quota.Manager) {
	if w == nil {
		return
	}
	w.quotaMu.Lock()
	w.quota = qm
	w.quotaMu.Unlock()
}

// Stop 优雅停止刷新 worker。
func (w *SubscriptionRefreshWorker) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
	if !w.config.QuietLogs {
		log.Printf("[SubscriptionRefreshWorker-Stop] 已停止 (今日已用=%d, 剩余=%d)",
			w.budget.Used(), w.budget.Remaining())
	}
}

// run 主循环。
func (w *SubscriptionRefreshWorker) run(ctx context.Context) {
	// 启动时立即执行一次
	w.refreshAll()

	ticker := time.NewTicker(w.config.RefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.refreshAll()
		}
	}
}

// refreshAll 扫描所有满足条件的订阅并刷新余额。
func (w *SubscriptionRefreshWorker) refreshAll() {
	// 双重门控：全局开关必须开启
	if !w.enabled() {
		return
	}

	all := w.subStore.ListAll()
	now := w.timeFn()
	var refreshed, skipped, failed int

	for _, profile := range all {
		// 门控1: 订阅级开关 + BillingAPIKey 非空
		if !profile.AutoRefreshEnabled || profile.BillingAPIKey == "" {
			skipped++
			continue
		}

		// 门控2: Provider 在白名单内
		if !IsAutoRefreshSupported(profile.Provider) {
			skipped++
			continue
		}

		// 冷却期检查
		if profile.LastBalanceRefreshAt != nil && now.Sub(*profile.LastBalanceRefreshAt) < w.config.RefreshInterval {
			skipped++
			continue
		}

		// 预算检查
		if !w.budget.TryConsume() {
			log.Printf("[SubscriptionRefreshWorker-Budget] 每日刷新预算耗尽 (limit=%d)，跳过剩余订阅",
				w.config.DailyBudget)
			break
		}

		result := w.fetchBalance(profile)
		w.applyResult(result)
		w.updateQuota(profile, result)

		if result.Success {
			refreshed++
		} else {
			failed++
		}
	}

	if !w.config.QuietLogs && (refreshed > 0 || failed > 0) {
		log.Printf("[SubscriptionRefreshWorker-Refresh] 刷新完成: 成功=%d, 失败=%d, 跳过=%d, 预算剩余=%d",
			refreshed, failed, skipped, w.budget.Remaining())
	}
}

// fetchBalance 对单个订阅执行余额查询。
func (w *SubscriptionRefreshWorker) fetchBalance(profile *SubscriptionProfile) SubscriptionRefreshResult {
	now := w.timeFn()
	result := SubscriptionRefreshResult{
		SubscriptionUID: profile.SubscriptionUID,
		Provider:        profile.Provider,
		FetchedAt:       now,
	}

	fetcher := w.fetchers.Get(profile.Provider)
	if fetcher == nil {
		result.ErrorMessage = fmt.Sprintf("未找到 provider=%s 的余额查询器", profile.Provider)
		return result
	}

	ctx, cancel := context.WithTimeout(context.Background(), w.config.RefreshTimeout)
	defer cancel()

	balance, currency, err := fetcher.FetchBalance(ctx, profile.BillingAPIKey)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("查询余额失败: %v", err)
		return result
	}

	result.Balance = balance
	result.Currency = currency
	result.Success = true
	return result
}

// applyResult 将查询结果回写到订阅画像。
func (w *SubscriptionRefreshWorker) applyResult(result SubscriptionRefreshResult) {
	err := w.subStore.Patch(result.SubscriptionUID, nil, func(profile *SubscriptionProfile) error {
		now := result.FetchedAt
		profile.LastBalanceRefreshAt = &now
		if result.Success {
			profile.LastBalanceRefreshError = ""
			if result.Balance >= 0 {
				profile.Balance = result.Balance
			}
			if result.Currency != "" {
				profile.Currency = result.Currency
			}
		} else {
			profile.LastBalanceRefreshError = result.ErrorMessage
			if !w.config.QuietLogs {
				log.Printf("[SubscriptionRefreshWorker-Apply] 订阅=%s 刷新失败: %s", result.SubscriptionUID, result.ErrorMessage)
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("[SubscriptionRefreshWorker-Apply] 回写订阅画像失败 uid=%s: %v", result.SubscriptionUID, err)
	}
}

// updateQuota 将余额刷新结果接入配额管理器（provider_api 级，最高可信度来源）。
// 订阅经 LinkedChannelUIDs 关联渠道；accountUID 用 SubscriptionUID 做饱和桶聚合键。
// fail-open：未注入管理器或订阅未关联渠道时静默跳过。
func (w *SubscriptionRefreshWorker) updateQuota(profile *SubscriptionProfile, result SubscriptionRefreshResult) {
	if w == nil || profile == nil || len(profile.LinkedChannelUIDs) == 0 {
		return
	}
	w.quotaMu.RLock()
	qm := w.quota
	w.quotaMu.RUnlock()
	if qm == nil {
		return
	}

	var values []quota.Value
	var fetchErr error
	if result.Success {
		if result.Balance >= 0 {
			balance := result.Balance
			values = []quota.Value{{
				Dimension: quota.DimCurrency,
				Remaining: &balance,
				Unit:      result.Currency,
			}}
		}
	} else {
		fetchErr = fmt.Errorf("%s", result.ErrorMessage)
	}
	for _, channelUID := range profile.LinkedChannelUIDs {
		qm.UpdateChannelProviderAPI(channelUID, profile.SubscriptionUID, values, fetchErr)
	}
}

// BudgetRemaining 返回今日剩余刷新额度。
func (w *SubscriptionRefreshWorker) BudgetRemaining() int {
	return w.budget.Remaining()
}

// BudgetUsed 返回今日已用刷新额度。
func (w *SubscriptionRefreshWorker) BudgetUsed() int {
	return w.budget.Used()
}
