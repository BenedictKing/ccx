package autopilot

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/eventbus"
	"github.com/BenedictKing/ccx/internal/keypool"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

// metricsManagerAdapter 包装 *metrics.MetricsManager，实现 MetricsProvider 接口。
// MetricsManager 有 GetTimeWindowStatsForKey 和 GetKeyCircuitState / GetKeyMetrics，
// 但缺少 GetKeySnapshot；此适配器用现有方法组装 KeyCircuitSnapshot。
type metricsManagerAdapter struct {
	mgr *metrics.MetricsManager
}

// NewMetricsManagerAdapter 包装 *metrics.MetricsManager 为 MetricsProvider。
// mgr 为 nil 时返回 nil（调用方需处理）。
func NewMetricsManagerAdapter(mgr *metrics.MetricsManager) MetricsProvider {
	if mgr == nil {
		return nil
	}
	return &metricsManagerAdapter{mgr: mgr}
}

func (a *metricsManagerAdapter) GetTimeWindowStatsForKey(_ string, baseURL, apiKey, serviceType string, duration time.Duration) TimeWindowStats {
	stats := a.mgr.GetTimeWindowStatsForKey(baseURL, apiKey, serviceType, duration)
	return TimeWindowStats{
		RequestCount:          stats.RequestCount,
		SuccessCount:          stats.SuccessCount,
		FailureCount:          stats.FailureCount,
		SuccessRate:           stats.SuccessRate,
		ConnectSampleCount:    stats.ConnectSampleCount,
		P95ConnectLatencyMs:   stats.P95ConnectLatencyMs,
		FirstByteSampleCount:  stats.FirstByteSampleCount,
		P95FirstByteLatencyMs: stats.P95FirstByteLatencyMs,
	}
}

func (a *metricsManagerAdapter) GetKeySnapshot(_ string, baseURL, apiKey, serviceType string) KeyCircuitSnapshot {
	cs := a.mgr.GetKeyCircuitState(baseURL, apiKey, serviceType)
	km := a.mgr.GetKeyMetrics(baseURL, apiKey, serviceType)

	snap := KeyCircuitSnapshot{
		CircuitState: int(cs),
	}
	if km != nil {
		snap.ConsecutiveFailures = km.ConsecutiveFailures
		snap.LastSuccessAt = km.LastSuccessAt
	}
	return snap
}

// metricsAdapterManager 按入口 channelKind 路由到对应的 MetricsProvider。
// channelKind 决定指标分片，serviceType 决定分片内的 MetricsIdentity。
type metricsAdapterManager struct {
	managers map[string]MetricsProvider
}

// NewMetricsAdapterManager 创建按 channelKind 路由的 MetricsProvider。
// managers 中的 nil 条目会被安全跳过（返回零值）。
func NewMetricsAdapterManager(managers map[string]MetricsProvider) MetricsProvider {
	return &metricsAdapterManager{managers: managers}
}

func (a *metricsAdapterManager) GetTimeWindowStatsForKey(channelKind, baseURL, apiKey, serviceType string, duration time.Duration) TimeWindowStats {
	mgr, ok := a.managers[channelKind]
	if !ok || mgr == nil {
		return TimeWindowStats{SuccessRate: 100}
	}
	return mgr.GetTimeWindowStatsForKey(channelKind, baseURL, apiKey, serviceType, duration)
}

func (a *metricsAdapterManager) GetKeySnapshot(channelKind, baseURL, apiKey, serviceType string) KeyCircuitSnapshot {
	mgr, ok := a.managers[channelKind]
	if !ok || mgr == nil {
		return KeyCircuitSnapshot{}
	}
	return mgr.GetKeySnapshot(channelKind, baseURL, apiKey, serviceType)
}

// ManagerConfig Manager 可调参数。
type ManagerConfig struct {
	// WorkerInterval 后台聚合循环间隔，默认 5 分钟。
	WorkerInterval time.Duration
	// QuietLogs 是否静默常规日志（遵循 QUIET_POLLING_LOGS 惯例）。
	QuietLogs bool
}

// Manager 健康中心聚合入口。
// 持有 ProfileStore / Profiler / HealthAnalyzer / FastDecayScorer，
// 启动后台 worker 定期遍历各渠道 endpoint 进行 L1 指标采集、画像推导、健康诊断。
// 同时持有 SubscriptionStore / LocalRuntimeStore / ManualIntentStore，
// 用于驾驶舱聚合与路由注册。
type Manager struct {
	store      *ProfileStore
	profiler   *Profiler
	analyzer   *HealthAnalyzer
	scorer     *FastDecayScorer
	metrics    MetricsProvider
	cfgManager *config.ConfigManager
	cfg        ManagerConfig

	// Phase 1 新组件：限速发现、时间桶、质量趋势、分组变更
	rateLimitDiscoverer  *RateLimitDiscoverer
	timeBucketStore      *TimeBucketStore
	qualityTrendDetector *QualityTrendDetector
	groupChangeDetector  *GroupChangeDetector

	// Phase 1 新组件：订阅中心、本地 runtime、手动意图（共享 ProfileStore 的 *sql.DB）
	subscriptionStore *SubscriptionStore
	localRuntimeStore *LocalRuntimeStore
	manualIntentStore *ManualIntentStore

	// Phase 4 Item 7：本地任务模板存储
	taskTemplateStore *LocalTaskTemplateStore

	// Phase 1 新组件：advisor shadow + 决策存储 + 用量计量
	advisorStore *AdvisorDecisionStore
	advisor      *TrustedRoutingAdvisor
	usageMeter   *UsageMeter

	// Phase 2 新组件：SmartRouter + RoutingTrace
	traceStore  *TraceStore
	smartRouter *SmartRouter

	// Phase 2 第二批：endpoint 级策略 + L2 探测 + 限速应用
	probeWorker *ProbeWorker

	rateLimitApplierMu sync.RWMutex
	rateLimitApplier   *RateLimitApplier
	rateLimitApplyCh   chan struct{}

	// Phase 3A：画像变更事件（只读展示，不影响调度）
	changelogStore *ProfileChangelogStore
	eventHub       *EventHub

	// Phase B.1：跨模块状态事件（熔断 / Key 拉黑恢复等）
	eventBus        *eventbus.Bus
	stateEventStore *StateEventStore
	stateEventUnsub func()

	// Phase 3B-2：模型画像存储（用于 ModelResolver 查询）
	modelProfileStore *ModelProfileStore

	// 手动 L3：固定 canary 的 endpoint×model ProviderQuality 探测器。
	providerQualityProbe *ProviderQualityProbe

	// Phase 3B-2：模型自动映射器（用于 resolveMappedModel + ResolveModelSupport）
	modelResolver *ModelResolver

	// Phase 4 Item 6：订阅余额自动刷新 worker
	subscriptionRefreshWorker *SubscriptionRefreshWorker

	// Phase 4 Item 8：A/B 测试（低比例统计抽样双发）
	abTestStore   *ABTestStore
	abTestSampler *ABTestSampler

	// Phase 4 Item 4：渠道推荐用量画像累积（纯内存，shadow 风格，不影响调度）
	usagePatternAccumulator *UsagePatternAccumulator

	cancel func()
	wg     sync.WaitGroup
}

// NewManager 创建 Manager 实例。
// store 由调用方构造并传入；metrics 应为 metricsAdapterManager（按 serviceType 路由）。
// 内部从 store.DB() 复用同一 *sql.DB 创建 SubscriptionStore / LocalRuntimeStore / ManualIntentStore。
func NewManager(
	store *ProfileStore,
	metrics MetricsProvider,
	cfgManager *config.ConfigManager,
	cfg ManagerConfig,
) (*Manager, error) {
	if cfg.WorkerInterval <= 0 {
		cfg.WorkerInterval = 5 * time.Minute
	}

	db := store.DB()
	if db == nil {
		return nil, fmt.Errorf("[Autopilot-NewManager] ProfileStore.DB() 为 nil，无法初始化子 store")
	}

	subStore, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		return nil, fmt.Errorf("[Autopilot-NewManager] 初始化 SubscriptionStore 失败: %w", err)
	}
	lrStore, err := NewLocalRuntimeStoreWithDB(db)
	if err != nil {
		return nil, fmt.Errorf("[Autopilot-NewManager] 初始化 LocalRuntimeStore 失败: %w", err)
	}
	miStore, err := NewManualIntentStoreWithDB(db)
	if err != nil {
		return nil, fmt.Errorf("[Autopilot-NewManager] 初始化 ManualIntentStore 失败: %w", err)
	}

	ttStore, ttErr := NewLocalTaskTemplateStoreWithDB(db)
	if ttErr != nil {
		return nil, fmt.Errorf("[Autopilot-NewManager] 初始化 LocalTaskTemplateStore 失败: %w", ttErr)
	}

	advisorStore, asErr := NewAdvisorDecisionStoreWithDB(db)
	if asErr != nil {
		return nil, fmt.Errorf("[Autopilot-NewManager] 初始化 AdvisorDecisionStore 失败: %w", asErr)
	}

	changelogStore, clErr := NewProfileChangelogStoreWithDB(db)
	if clErr != nil {
		return nil, fmt.Errorf("[Autopilot-NewManager] 初始化 ProfileChangelogStore 失败: %w", clErr)
	}

	modelProfileStore, mpErr := NewModelProfileStoreWithDB(db)
	if mpErr != nil {
		return nil, fmt.Errorf("[Autopilot-NewManager] 初始化 ModelProfileStore 失败: %w", mpErr)
	}

	// 初始化 Phase 1 新组件（shadow/read-only，不修改调度链路）
	timeBucketStore := NewTimeBucketStore()
	manager := &Manager{
		store:      store,
		profiler:   NewProfiler(metrics),
		analyzer:   NewHealthAnalyzer(),
		scorer:     NewFastDecayScorer(),
		metrics:    metrics,
		cfgManager: cfgManager,
		cfg:        cfg,

		rateLimitDiscoverer:  NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: true, QuietLogs: cfg.QuietLogs}),
		timeBucketStore:      timeBucketStore,
		qualityTrendDetector: NewQualityTrendDetector(timeBucketStore),
		groupChangeDetector:  NewGroupChangeDetector(store),

		subscriptionStore: subStore,
		localRuntimeStore: lrStore,
		manualIntentStore: miStore,
		taskTemplateStore: ttStore,

		advisorStore: advisorStore,
		advisor:      NewTrustedRoutingAdvisor(),
		usageMeter:   NewUsageMeter(UsageMeterConfig{QuietLogs: cfg.QuietLogs}, timeBucketStore),

		changelogStore: changelogStore,
		eventHub:       NewEventHub(),

		eventBus:        eventbus.NewBus(),
		stateEventStore: nil, // 由 main.go 注入或 NewManagerWithStateStore 显式设置

		modelProfileStore: modelProfileStore,
		modelResolver:     NewModelResolver(modelProfileStore, cfgManager),

		usagePatternAccumulator: NewUsagePatternAccumulator(defaultUsagePatternRetentionDays),
		rateLimitApplyCh:        make(chan struct{}, 1),
	}
	manager.providerQualityProbe = NewProviderQualityProbe(
		store,
		modelProfileStore,
		cfgManager,
		manager.ResolveAPIKey,
		ProviderQualityProbeConfig{},
	)
	// 在启动任何后台 worker 前同步建立当前配置的有效画像清单，
	// 避免 L2 探测启动扫描短暂读到全部历史画像。
	manager.refreshEndpointInventory()
	return manager, nil
}

// RateLimitDiscoverer 返回内部限速发现器引用（供 main.go 注册信号回调）。
func (m *Manager) RateLimitDiscoverer() *RateLimitDiscoverer {
	return m.rateLimitDiscoverer
}

// TimeBucketStore 返回内部时间桶存储引用。
func (m *Manager) TimeBucketStore() *TimeBucketStore {
	return m.timeBucketStore
}

// ObserveRateLimitSignal 供信号回调调用：喂限速发现器和时间桶。
// 并发安全，不修改调度链路。
// channelName 为渠道可读名（upstream.Name），仅用于日志展示，可能为空。
// reason 携带 429 细分原因（非 429 传空串），由 upstream_failover.go
// 读完 body 分类后传入，确保同一次非 2xx 响应只通知一次 Discoverer。
func (m *Manager) ObserveRateLimitSignal(
	endpointUID string,
	channelID int,
	metricsKey string,
	serviceType string,
	channelName string,
	isStream bool,
	latencyMs int64,
	headers http.Header,
	statusCode int,
	reason string,
) bool {
	if endpointUID == "" {
		return false
	}
	now := time.Now()

	// 记录请求到 UsageMeter（本地计量兜底，Unit=requests）
	// token 数量在代理层难以精确获取，暂按请求数计量（tokens 恒传 0）
	if m.usageMeter != nil {
		m.usageMeter.RecordRequest(endpointUID, 0, RequestRecordMeta{
			ChannelName: channelName,
			ServiceType: serviceType,
			StatusCode:  statusCode,
			IsStream:    isStream,
			Reason:      reason,
		})
	}

	// 构造限速信号喂给 Discoverer
	signal := RateLimitSignal{
		IsStreaming: isStream,
		LatencyMs:   latencyMs,
		Timestamp:   now,
		Reason:      RateLimitSignalReason(reason),
	}
	if signal.Reason == "" {
		signal.Reason = RateLimitReasonUnknown
	}

	// 429 信号
	if statusCode == http.StatusTooManyRequests {
		signal.Source = SignalSource429
		// 解析 Retry-After
		ra := headers.Get("Retry-After")
		if ra != "" {
			if secs, err := strconv.ParseFloat(ra, 64); err == nil && secs > 0 {
				signal.HasRetryAfter = true
				signal.RetryAfterSeconds = secs
			}
		}
		changed := m.rateLimitDiscoverer.Observe(endpointUID, signal)

		// 时间桶：记录为失败 + 429
		m.timeBucketStore.Record(endpointUID, channelID, metricsKey, false, latencyMs)
		return changed
	}

	// 成功响应（2xx）：解析 header 中的限速信息
	if statusCode >= 200 && statusCode < 300 {
		signal.Source = SignalSourceSuccess

		// Anthropic limit header
		if limitStr := headers.Get("anthropic-ratelimit-requests-limit"); limitStr != "" {
			if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 {
				signal.Limit = limit
				signal.Source = SignalSourceHeader
			}
		}
		// Anthropic remaining header
		if remStr := headers.Get("anthropic-ratelimit-requests-remaining"); remStr != "" {
			if rem, err := strconv.Atoi(remStr); err == nil {
				signal.Remaining = rem
			}
		}
		// Anthropic reset header（RFC3339 → 秒数差）
		if resetStr := headers.Get("anthropic-ratelimit-requests-reset"); resetStr != "" {
			if resetTime, err := time.Parse(time.RFC3339, resetStr); err == nil {
				secs := resetTime.Sub(now).Seconds()
				if secs > 0 {
					signal.ResetSeconds = secs
				}
			}
		}

		// OpenAI limit header
		if limitStr := headers.Get("x-ratelimit-limit-requests"); limitStr != "" {
			if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 {
				signal.Limit = limit
				signal.Source = SignalSourceHeader
			}
		}
		// OpenAI remaining header
		if remStr := headers.Get("x-ratelimit-remaining-requests"); remStr != "" {
			if rem, err := strconv.Atoi(remStr); err == nil {
				signal.Remaining = rem
			}
		}
		// OpenAI reset header（duration 格式）
		if resetStr := headers.Get("x-ratelimit-reset-requests"); resetStr != "" {
			if d, err := time.ParseDuration(resetStr); err == nil && d > 0 {
				signal.ResetSeconds = d.Seconds()
			}
		}

		changed := m.rateLimitDiscoverer.Observe(endpointUID, signal)

		// 时间桶：记录为成功
		m.timeBucketStore.Record(endpointUID, channelID, metricsKey, true, latencyMs)
		return changed
	}

	// 非 429 非 2xx：记录为失败但不喂限速信号（由健康诊断器处理）
	m.timeBucketStore.Record(endpointUID, channelID, metricsKey, false, latencyMs)
	return false
}

// StartWorker 启动后台聚合 worker。ctx 取消时退出循环。
// 若 ProbeWorker 已设置，同步启动 L2 探测 worker。
// 若 SubscriptionRefreshWorker 已设置，同步启动余额刷新 worker。
func (m *Manager) StartWorker(ctx context.Context) {
	ctx, m.cancel = context.WithCancel(ctx)
	m.wg.Add(2)
	go func() {
		defer m.wg.Done()
		m.runWorker(ctx)
	}()
	go func() {
		defer m.wg.Done()
		m.runRateLimitApplyWorker(ctx)
	}()
	// L2 探测 worker（config 门控，由 main.go 按配置决定是否 SetProbeWorker）
	if m.probeWorker != nil {
		m.probeWorker.Start(ctx)
	}
	// 订阅余额刷新 worker（config 门控，由 main.go 按配置决定是否 SetSubscriptionRefreshWorker）
	if m.subscriptionRefreshWorker != nil {
		m.subscriptionRefreshWorker.Start(ctx)
	}
}

// Stop 优雅停止后台 worker（等待当前循环完成）。
// 若 ProbeWorker / SubscriptionRefreshWorker 已设置，同步停止。
func (m *Manager) Stop() {
	if m.subscriptionRefreshWorker != nil {
		m.subscriptionRefreshWorker.Stop()
	}
	if m.probeWorker != nil {
		m.probeWorker.Stop()
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
}

// Close 停止 worker 并关闭所有 Store。
func (m *Manager) Close() error {
	m.Stop()
	var errs []error
	if err := m.store.Close(); err != nil {
		errs = append(errs, err)
	}
	if m.subscriptionStore != nil {
		if err := m.subscriptionStore.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if m.localRuntimeStore != nil {
		if err := m.localRuntimeStore.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if m.manualIntentStore != nil {
		if err := m.manualIntentStore.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if m.advisorStore != nil {
		if err := m.advisorStore.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if m.traceStore != nil {
		if err := m.traceStore.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("[Autopilot-Close] 关闭 store 出错: %v", errs)
	}
	return nil
}

// ProfileStore 返回内部 ProfileStore 引用（供 handler 读取画像数据）。
func (m *Manager) ProfileStore() *ProfileStore {
	return m.store
}

// SubscriptionStore 返回内部 SubscriptionStore 引用。
func (m *Manager) SubscriptionStore() *SubscriptionStore {
	return m.subscriptionStore
}

// LocalRuntimeStore 返回内部 LocalRuntimeStore 引用。
func (m *Manager) LocalRuntimeStore() *LocalRuntimeStore {
	return m.localRuntimeStore
}

// ManualIntentStore 返回内部 ManualIntentStore 引用。
func (m *Manager) ManualIntentStore() *ManualIntentStore {
	return m.manualIntentStore
}

// TaskTemplateStore 返回内部 LocalTaskTemplateStore 引用。
func (m *Manager) TaskTemplateStore() *LocalTaskTemplateStore {
	return m.taskTemplateStore
}

// AdvisorDecisionStore 返回内部 AdvisorDecisionStore 引用。
func (m *Manager) AdvisorDecisionStore() *AdvisorDecisionStore {
	return m.advisorStore
}

// Advisor 返回内部 TrustedRoutingAdvisor 引用。
func (m *Manager) Advisor() *TrustedRoutingAdvisor {
	return m.advisor
}

// UsageMeter 返回内部 UsageMeter 引用。
func (m *Manager) UsageMeter() *UsageMeter {
	return m.usageMeter
}

// TraceStore 返回内部 TraceStore 引用。
func (m *Manager) TraceStore() *TraceStore {
	return m.traceStore
}

// SetTraceStore 设置 TraceStore（由 main.go 在 NewManager 后调用）。
func (m *Manager) SetTraceStore(ts *TraceStore) {
	m.traceStore = ts
}

// SmartRouter 返回内部 SmartRouter 引用。
func (m *Manager) SmartRouter() *SmartRouter {
	return m.smartRouter
}

// SetSmartRouter 设置 SmartRouter（由 main.go 在 NewManager 后调用）。
func (m *Manager) SetSmartRouter(sr *SmartRouter) {
	m.smartRouter = sr
}

// WireSmartRouter 将 advisor、localRuntimeStore、模型画像和 dry-run ModelResolver 注入 SmartRouter。
// 在 main.go 构造 SmartRouter 后调用，确保 Phase 2 组件全部连接。
// nil 参数表示不启用对应功能（fail-safe）。
func (m *Manager) WireSmartRouter() {
	if m.smartRouter == nil {
		return
	}
	m.smartRouter.SetAdvisor(m.advisor, m.advisorStore)
	m.smartRouter.SetLocalRuntimeStore(m.localRuntimeStore)
	m.smartRouter.SetModelResolver(m.modelResolver)
	m.smartRouter.SetModelProfileStore(m.modelProfileStore)
	m.smartRouter.SetSubscriptionStore(m.subscriptionStore)
}

// FastDecayScorer 返回内部 FastDecayScorer 引用（供 handler 层通知请求结果）。
func (m *Manager) FastDecayScorer() *FastDecayScorer {
	return m.scorer
}

// ProbeWorker 返回内部 ProbeWorker 引用。
func (m *Manager) ProbeWorker() *ProbeWorker {
	return m.probeWorker
}

// SetProbeWorker 设置 ProbeWorker（由 main.go 在 NewManager 后调用）。
func (m *Manager) SetProbeWorker(pw *ProbeWorker) {
	m.probeWorker = pw
}

// SubscriptionRefreshWorker 返回内部 SubscriptionRefreshWorker 引用。
func (m *Manager) SubscriptionRefreshWorker() *SubscriptionRefreshWorker {
	return m.subscriptionRefreshWorker
}

// SetSubscriptionRefreshWorker 设置 SubscriptionRefreshWorker（由 main.go 在 NewManager 后调用）。
func (m *Manager) SetSubscriptionRefreshWorker(rw *SubscriptionRefreshWorker) {
	m.subscriptionRefreshWorker = rw
}

// ABTestSampler 返回内部 ABTestSampler 引用。
func (m *Manager) ABTestSampler() *ABTestSampler {
	return m.abTestSampler
}

// SetABTestSampler 设置 ABTestSampler（由 main.go 在 NewManager 后调用）。
func (m *Manager) SetABTestSampler(s *ABTestSampler) {
	m.abTestSampler = s
}

// ABTestStore 返回内部 ABTestStore 引用。
func (m *Manager) ABTestStore() *ABTestStore {
	return m.abTestStore
}

// SetABTestStore 设置 ABTestStore（由 main.go 在 NewManager 后调用）。
func (m *Manager) SetABTestStore(s *ABTestStore) {
	m.abTestStore = s
}

// UsagePatternAccumulator 返回内部用量画像累积器引用（Phase 4 Item 4：渠道推荐）。
func (m *Manager) UsagePatternAccumulator() *UsagePatternAccumulator {
	return m.usagePatternAccumulator
}

// RecordUsagePattern 记录一次请求的用量画像：proxyKeyMask 落到 channelUID。
// 用于 Phase 4 Item 4 渠道推荐的用量画像积累。
// 由 handlers/common 的 usagePatternRecorderHook 在请求成功完成后调用（主响应已返回客户端之后），
// 纯观测性累积，不参与任何调度/候选过滤决策。
// proxyKeyMask 或 channelUID 为空时静默跳过。
//
// channelKind/model 目前未参与域推导（InferTaskDomain 依赖 system prompt/工具集/文件扩展名等
// DomainHints，这些信号在代理层的请求完成钩子上不可得，是现有代码库的已知缺口，非本项新增回归）；
// 保留参数是为了未来在有 DomainHints 数据源时无需再改调用方签名。
func (m *Manager) RecordUsagePattern(proxyKeyMask, channelKind, channelUID, model string) {
	if m.usagePatternAccumulator == nil || proxyKeyMask == "" || channelUID == "" {
		return
	}
	_ = channelKind
	_ = model
	domain := InferTaskDomain(DomainHints{})
	m.usagePatternAccumulator.RecordUsage(proxyKeyMask, domain, channelUID)
}

// ResolveAPIKey 根据 channelUID + keyHash 反查明文 API Key，供 ProbeWorker.APIKeyResolver 使用。
// KeyEndpointProfile 只存 sha256(apiKey) 摘要（MetricsKey），无法直接还原明文，
// 需要遍历当前配置里该渠道的 APIKeys，逐个计算摘要与 keyHash 比对。
// 返回 ok=false 表示渠道已删除或 key 已轮换，调用方（ProbeWorker）应跳过本次探测。
func (m *Manager) ResolveAPIKey(channelUID, keyHash string) (string, bool) {
	if channelUID == "" || keyHash == "" {
		return "", false
	}
	for _, entry := range m.gatherChannelEntries() {
		if entry.ChannelUID != channelUID {
			continue
		}
		for _, key := range entry.APIKeys {
			if KeyHashFromAPIKey(key) == keyHash {
				return key, true
			}
		}
	}
	return "", false
}

// RateLimitApplier 返回内部 RateLimitApplier 引用。
func (m *Manager) RateLimitApplier() *RateLimitApplier {
	return m.rateLimitApplier
}

// ChangelogStore 返回内部 ProfileChangelogStore 引用（供 API handler 读取历史）。
func (m *Manager) ChangelogStore() *ProfileChangelogStore {
	return m.changelogStore
}

// EventHub 返回内部 EventHub 引用（供 WebSocket handler 订阅、AutoDiscoveryRunner 发布）。
func (m *Manager) EventHub() *EventHub {
	return m.eventHub
}

// EventBus 返回跨模块 eventbus.Bus（Phase B.1），可由 main.go 注入或 WireEventBus 创建。
func (m *Manager) EventBus() *eventbus.Bus {
	return m.eventBus
}

// StateEventStore 返回内部 StateEventStore 引用（供 API handler 读取历史）。
func (m *Manager) StateEventStore() *StateEventStore {
	return m.stateEventStore
}

// WireEventBus 注入 eventbus.Bus 并订阅 StateEventStore。
// 应在 main.go 启动阶段调用（config + metrics 已注入之后）。
func (m *Manager) WireEventBus(bus *eventbus.Bus, store *StateEventStore) {
	if m == nil {
		return
	}
	if bus != nil {
		m.eventBus = bus
	}
	if store != nil {
		m.stateEventStore = store
		// 订阅全类型，持久化到 SQLite 供前端 /api/health-center/state-events 拉取。
		ch, unsub := bus.Subscribe()
		go func() {
			for ev := range ch {
				store.Record(ev)
			}
		}()
		m.stateEventUnsub = unsub
	}
}

// ModelProfileStore 返回内部 ModelProfileStore 引用（供 main.go 传入 AutoDiscoveryRunner）。
func (m *Manager) ModelProfileStore() *ModelProfileStore {
	return m.modelProfileStore
}

// ProviderQualityProbe 返回手动 L3 ProviderQuality 探测器。
func (m *Manager) ProviderQualityProbe() *ProviderQualityProbe {
	return m.providerQualityProbe
}

// ModelResolver 返回内部 ModelResolver 引用（供 EndpointPolicyDeps 和 main.go 接线）。
func (m *Manager) ModelResolver() *ModelResolver {
	return m.modelResolver
}

// ResolveModelSupport 判断 AutoManaged 渠道是否支持指定模型。
// 签名匹配 scheduler.ModelSupportResolverFunc，供 main.go 通过
// scheduler.SetModelSupportResolverProvider(mgr.ResolveModelSupport) 注册。
//
// 实现策略：
//   - 非 AutoManaged 渠道：直接委托 ExplainModelSupport（零额外成本）
//   - AutoManaged 渠道 + 显式 SupportedModels 命中：直接返回
//   - AutoManaged 渠道 + 三条件门控通过：其余情况调用 ModelResolver；
//     空 SupportedModels 下画像未命中是权威拒绝，禁止回退到“支持全部”
//   - 门控不满足（AutoResolve=false 或 mode=off/shadow 或 KillSwitch）：回退 ExplainModelSupport
//
// 安全不变量：
//   - Resolver nil 时行为与原有路径字节级一致（fail-open）
//   - 非 AutoManaged 渠道的 ExplainModelSupport/RedirectModel 路径完全不变
func (m *Manager) ResolveModelSupport(kind string, upstream *config.UpstreamConfig, model string) (supported bool, actualModel string, source string, reason string) {
	return m.ResolveModelSupportWithFloor(kind, upstream, model, CapabilityFloor{})
}

// ResolveModelSupportWithFloor 使用请求级能力下界判断 AutoManaged 渠道能否承接模型。
// scheduler 首次选渠与 endpoint policy 复用同一份 RequestProfile，避免先选中已知不兼容渠道后再 failover。
func (m *Manager) ResolveModelSupportWithFloor(
	kind string,
	upstream *config.UpstreamConfig,
	model string,
	floor CapabilityFloor,
) (supported bool, actualModel string, source string, reason string) {
	if upstream == nil || model == "" {
		return false, "", "invalid_input", "upstream or model is nil/empty"
	}

	// 非 AutoManaged 渠道：直接走原有路径
	if !upstream.AutoManaged {
		sup, rsn := upstream.ExplainModelSupport(model)
		return sup, "", "explain", rsn
	}

	// AutoManaged 渠道：显式 SupportedModels 命中时走 fast path。
	// 空列表的传统语义是“支持全部”，但自动托管渠道的实际模型以 endpoint profile 为准，
	// 不能在这里短路，否则 ModelResolver 永远没有机会运行。
	sup, rsn := upstream.ExplainModelSupport(model)
	if len(upstream.SupportedModels) > 0 && sup {
		return true, "", "explain", ""
	}

	// ExplainModelSupport 拒绝：检查三条件门控
	routingCfg := m.cfgManager.GetAutopilotRouting()
	if !routingCfg.ModelMapping.AutoResolve {
		return sup, "", "explain", rsn
	}
	// 门控通过：调用 ModelResolver（调度器候选筛选阶段，无具体 API Key）
	if m.modelResolver == nil {
		return sup, "", "explain", rsn
	}

	// 使用完整能力下界判断渠道所有 endpoint 是否能通过自动映射承接该模型。
	// 真正请求发送前，resolveMappedModel 会用完整 metricsKey 和 CapabilityFloor 再做 endpoint 级映射。
	target, found, resolverReason := m.modelResolver.ResolveModelAnyEndpointWithFloor(
		model,
		upstream.ChannelUID,
		kind,
		floor,
	)
	if found {
		source := "auto_resolve"
		switch resolverReason {
		case "found_exact_model_in_profile":
			source = "model_profile_exact"
		case "found_equivalent_model_in_profile":
			source = "model_profile_equivalent"
		}
		return true, target.Model, source, resolverReason
	}

	// 自动托管渠道没有显式 SupportedModels 时，画像是模型支持事实源。
	// 此处若继续 fail-open，空列表会把 exact-only 策略拒绝的模型重新放回候选。
	if len(upstream.SupportedModels) == 0 {
		return false, "", scheduler.ModelSupportSourceAuthoritativeDeny, resolverReason
	}

	// 显式 SupportedModels 已经能给出否定结果，保留原有回退语义。
	return false, "", "explain", rsn
}

// SetRateLimitApplier 设置 RateLimitApplier（由 main.go 在 StartWorker 前调用）。
func (m *Manager) SetRateLimitApplier(rla *RateLimitApplier) {
	if m == nil {
		return
	}
	m.rateLimitApplierMu.Lock()
	defer m.rateLimitApplierMu.Unlock()
	m.rateLimitApplier = rla
}

func (m *Manager) getRateLimitApplier() *RateLimitApplier {
	if m == nil {
		return nil
	}
	m.rateLimitApplierMu.RLock()
	defer m.rateLimitApplierMu.RUnlock()
	return m.rateLimitApplier
}

// RefreshRateLimitMappings 依据当前配置刷新 endpoint 到 limiter 的完整映射。
// 空 inventory 同样会替换为 nil，后续 Apply 可据此清理被删除渠道或轮换 Key 的旧状态。
func (m *Manager) RefreshRateLimitMappings() {
	applier := m.getRateLimitApplier()
	if applier == nil {
		return
	}
	inventory := m.refreshEndpointInventory()
	applier.SetEndpointMappings(m.buildEndpointLimiterMappings(inventory))
}

// RequestRateLimitApply 向容量为 1 的 apply worker 发送合并通知。
// 请求路径绝不等待全量遍历；连续 429 会自然折叠为一次最新状态的 Apply。
func (m *Manager) RequestRateLimitApply() {
	if m == nil || m.getRateLimitApplier() == nil {
		return
	}
	select {
	case m.rateLimitApplyCh <- struct{}{}:
	default:
	}
}

// runWorker 后台循环主逻辑。
func (m *Manager) runWorker(ctx context.Context) {
	if !m.cfg.QuietLogs {
		log.Printf("[Autopilot-Worker] 后台聚合 worker 已启动 (间隔: %s)", m.cfg.WorkerInterval)
	}

	// 启动时立即执行一次
	m.collectAll()

	ticker := time.NewTicker(m.cfg.WorkerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if !m.cfg.QuietLogs {
				log.Printf("[Autopilot-Worker] 后台聚合 worker 正在停止")
			}
			// 退出前 flush 脏数据
			if err := m.store.Flush(); err != nil {
				log.Printf("[Autopilot-Worker] 警告: 退出前 flush 失败: %v", err)
			}
			return
		case <-ticker.C:
			m.collectAll()
		}
	}
}

// runRateLimitApplyWorker 串行执行运行态 limiter 的短周期协调。
// 精确 429 和配置变更通过 rateLimitApplyCh 立即唤醒；30 秒 ticker 用于
// 处理通知丢失以及仅含 header/success 信号时的最终收敛。
func (m *Manager) runRateLimitApplyWorker(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.rateLimitApplyCh:
		case <-ticker.C:
		}
		if applier := m.getRateLimitApplier(); applier != nil {
			applier.Apply()
		}
	}
}

// collectAll 遍历所有渠道的 endpoint，执行 L1 画像推导 + 健康诊断。
func (m *Manager) collectAll() {
	start := time.Now()
	inventory := m.refreshEndpointInventory()
	entries := inventory.Entries

	// 限速 Applier 接线（设计 §6/§7）：刷新 endpoint→limiter mapping 并应用/清理 discovered RPM。
	// 即使 inventory 为空也要调用，确保渠道删除/Key 轮换/kill switch 时旧 limiter 状态被清理，
	// 不在 collectAll 开头直接跳过清理。
	if applier := m.getRateLimitApplier(); applier != nil {
		mappings := m.buildEndpointLimiterMappings(inventory)
		applier.SetEndpointMappings(mappings)
		applier.Apply()
	}

	if len(entries) == 0 {
		if !m.cfg.QuietLogs {
			log.Printf("[Autopilot-Worker] 无可遍历渠道，跳过本轮")
		}
		return
	}

	var profiled, diagnosed int
	var allProfiles []*KeyEndpointProfile
	// 读取 StabilityTier 滞后窗口配置（单次读取，避免循环内反复加锁）
	routingCfg := m.cfgManager.GetAutopilotRouting()
	stabilityHysteresisWindows := routingCfg.HealthCheck.StabilityHysteresisWindows
	// Phase 4 Item 3: SLO regression 自动回滚配置
	sloRollbackCfg := routingCfg.SLORollback
	channelDegrading := make(map[string]bool) // channelUID -> 是否存在 degrading endpoint
	for _, entry := range entries {
		for _, apiKey := range entry.APIKeys {
			profile := m.profiler.DeriveEndpointProfile(
				entry.ChannelUID,
				entry.ChannelID,
				entry.ChannelKind,
				entry.BaseURL,
				apiKey,
				entry.ServiceType,
				entry.OriginType,
				entry.OriginTier,
			)
			oldProfile := m.store.Get(profile.EndpointUID)
			carryForwardDiscoveryFields(oldProfile, &profile)
			carryForwardConnectStats(oldProfile, &profile, time.Now())
			carryForwardFirstByteStats(oldProfile, &profile, time.Now())
			profiled++

			// 收集被动信号并诊断
			signals := m.collectSignals(entry.ChannelKind, entry.BaseURL, apiKey, entry.ServiceType)
			diagnosis := m.analyzer.Diagnose(signals)

			profile.HealthState = diagnosis.State
			profile.HealthConfidence = diagnosis.Confidence
			if diagnosis.Reason != "" {
				profile.HealthEvidence = []string{diagnosis.Reason}
			}
			profile.UpdatedAt = time.Now()

			// ── Phase 1 新组件接入 ──

			// 限速发现器：映射建议到画像字段
			if m.rateLimitDiscoverer != nil {
				suggested := m.rateLimitDiscoverer.SuggestedLimit(profile.EndpointUID)
				if suggested.RPM > 0 {
					profile.DiscoveredRPM = suggested.RPM
					profile.RateLimitConfidence = suggested.Confidence
					profile.RateLimitSource = string(suggested.Source)
					profile.SuggestedRPMSource = string(suggested.Source)
					profile.SuggestedRPMTPM = suggested.TPM
					profile.SuggestedRPMRPD = suggested.RPD
				}
				if suggested.MaxConcurrent > 0 {
					profile.DiscoveredMaxConcurrent = suggested.MaxConcurrent
					if suggested.ConcurrentConfidence > profile.RateLimitConfidence {
						profile.RateLimitConfidence = suggested.ConcurrentConfidence
					}
					if profile.RateLimitSource == "" {
						profile.RateLimitSource = string(suggested.Source)
					}
				}
			}

			// 质量趋势检测
			if m.qualityTrendDetector != nil {
				trend := m.qualityTrendDetector.DetectTrend(
					profile.EndpointUID,
					profile.MetricsKey,
					time.Now(),
				)
				// 仅在有数据时附加趋势（避免空桶浪费 JSON 空间）
				if trend.Direction != TrendStable || len(trend.HourlyPattern) > 0 {
					profile.QualityTrend = &trend
				}

				// Phase 4 Item 3: 渠道级 degrading 聚合（任一 endpoint degrading 即计为渠道 degrading）
				if trend.Direction == TrendDegrading {
					channelDegrading[entry.ChannelUID] = true
				}
			}

			// 分组变更检测
			groupChanged := false
			if m.groupChangeDetector != nil {
				changed, changeResult := m.groupChangeDetector.CheckGroupChange(
					entry.ChannelUID,
					entry.ChannelKind,
					profile.MetricsKey,
					profile.AvailableModels,
				)
				groupChanged = changed
				if changed {
					now := time.Now()
					profile.GroupChangedAt = &now
					profile.ModelListHash = changeResult.NewHash
					profile.LastGroupChange = &changeResult
				} else {
					// 维持快照哈希（即使无变更，也同步当前哈希供前端展示）
					snap := m.groupChangeDetector.GetSnapshot(
						buildGroupSnapshotKey(entry.ChannelUID, profile.MetricsKey),
					)
					if snap != nil {
						profile.ModelListHash = snap.ListHash
					}
				}
			}

			// 用量窗口：从 UsageMeter 计算并写入画像
			if m.usageMeter != nil {
				profile.UsageWindows = m.usageMeter.ComputeWindows(profile.EndpointUID)
			}

			// L2 探测字段 carry-forward：DeriveEndpointProfile 每轮都构造全新 struct，
			// Probe* 字段是零值。若不从旧画像搬运过来，ProbeWorker 刚写入的
			// LastProbeAt/ConsecutiveProbeSuccess 等会被本轮 Upsert 整行覆盖清零，
			// 导致 scanAndEnqueue 的探测冷却期形同虚设。HealthState/HealthConfidence
			// 等 L1 诊断字段不受影响，继续以本轮真实流量信号为准。
			carryForwardProbeFields(oldProfile, &profile)

			// StabilityTier 晋降级滞后：连续 N 轮（N = stabilityHysteresisWindows）
			// rawTier 一致才采纳，防止单轮噪声导致 EffectiveStabilityTier 抖动。
			applyStabilityHysteresis(oldProfile, &profile, profile.StabilityTier, stabilityHysteresisWindows)

			// ── Phase 3A：画像变更事件检测（只读展示，不影响调度）──
			// 必须在 Upsert 覆盖缓存之前读取旧值，否则 diff 永远为空。
			m.emitProfileChangeEvents(profile.EndpointUID, &profile, groupChanged)

			if err := m.store.Upsert(&profile); err != nil {
				log.Printf("[Autopilot-Worker] 警告: 写入画像失败 endpoint=%s: %v", profile.EndpointUID, err)
			}
			diagnosed++

			// 收集指针用于订阅级能力推导
			p := profile
			allProfiles = append(allProfiles, &p)
		}

		// ── Phase 4 Item 3: SLO regression 自动回滚检查 ──
		// 双门控：SLO Rollback 开关必须开启 + advisor 当前状态必须为 active。
		if sloRollbackCfg.Enabled && m.advisor != nil {
			isDegrading := channelDegrading[entry.ChannelUID]
			rollbackChannelUID := m.advisor.CheckAndApplySLORollback(
				entry.ChannelUID,
				isDegrading,
				sloRollbackCfg.ConsecutiveWindows,
			)
			if rollbackChannelUID != "" {
				// 记录回滚决策
				if m.advisorStore != nil {
					rec := &AdvisorDecisionRecord{
						AdvisorUID:        rollbackChannelUID,
						AdvisorOriginTier: "slo_regression",
						Mode:              AdvisorStateRolledBack,
						TaskClass:         "slo_regression",
						Applied:           true,
						Outcome:           "rolled_back",
						Reason:            "slo_regression",
					}
					if err := m.advisorStore.Record(rec); err != nil {
						log.Printf("[Autopilot-Worker] 警告: SLO 回滚决策记录失败: %v", err)
					}
				}
			}
		}
	}

	// 批量落盘
	if err := m.store.Flush(); err != nil {
		log.Printf("[Autopilot-Worker] 警告: flush 失败: %v", err)
	}

	// ── 订阅级能力推导 + drift 检测（shadow，不修改调度）──
	m.updateSubscriptionCapabilities(allProfiles)

	elapsed := time.Since(start)
	if !m.cfg.QuietLogs {
		log.Printf("[Autopilot-Worker] 本轮完成: 渠道=%d, 画像=%d, 诊断=%d, 耗时=%s",
			len(entries), profiled, diagnosed, elapsed.Truncate(time.Millisecond))
	}
}

// buildEndpointLimiterMappings 从当前 inventory + config 构造 endpoint→limiter 映射。
// 同一 limiter 可被多 endpoint 映射，Apply 阶段会取确定性最小 RPM。
// ExplicitRPM 根据 key 配置及 channel 继承链计算；显式配置的 endpoint 不被注入发现 RPM。
// 未知 channelKind 返回空 apiType 并跳过，不默认映射到 Messages。
func (m *Manager) buildEndpointLimiterMappings(inv endpointInventory) []EndpointLimiterMapping {
	if m == nil || m.cfgManager == nil {
		return nil
	}
	cfg := m.cfgManager.GetConfig()
	var mappings []EndpointLimiterMapping
	for _, entry := range inv.Entries {
		apiType := channelKindToAPIType(entry.ChannelKind)
		if apiType == "" {
			continue
		}
		upstreams := upstreamsByKind(cfg, entry.ChannelKind)
		if entry.ChannelID < 0 || entry.ChannelID >= len(upstreams) {
			continue
		}
		base := upstreams[entry.ChannelID]
		// Auto-managed 渠道使用 runtime 上游（与 failover 一致）
		upstream := base
		if ru := config.RuntimeUpstreamForAutoManagedProvider(&base); ru != nil {
			upstream = *ru
		}
		configs := config.NormalizeAPIKeyConfigsForView(upstream)
		byKey := make(map[string]config.APIKeyConfig, len(configs))
		for _, c := range configs {
			byKey[c.Key] = c
		}
		useScopedLimiter := keypool.HasEffectiveConfig(&upstream)
		for _, key := range entry.APIKeys {
			kcfg := byKey[key]
			if kcfg.Key == "" {
				kcfg.Key = key
			}
			scope := ""
			if useScopedLimiter {
				scope = keypool.LimiterScopeFor(key, kcfg)
			}
			explicitRPM := kcfg.RateLimitRPM > 0 || upstream.RateLimitRPM > 0
			explicitMaxConcurrent := kcfg.RateLimitMaxConcurrent > 0 || upstream.RateLimitMaxConcurrent > 0
			limiterConfig := keypool.ConfigForCandidate(upstream, kcfg)
			limiterKey := fmt.Sprintf("%s:%d", apiType, entry.ChannelID)
			if scope != "" {
				limiterKey += ":" + scope
			}
			euid := GenerateEndpointUID(entry.ChannelUID, entry.BaseURL, KeyHashFromAPIKey(key))
			mappings = append(mappings, EndpointLimiterMapping{
				EndpointUID:           euid,
				LimiterKey:            limiterKey,
				LimiterConfig:         limiterConfig,
				ExplicitRPM:           explicitRPM,
				ExplicitMaxConcurrent: explicitMaxConcurrent,
			})
		}
	}
	return mappings
}

// upstreamsByKind 返回渠道类型对应的 upstream 列表（原始 config 索引）。
func upstreamsByKind(cfg config.Config, kind string) []config.UpstreamConfig {
	switch kind {
	case "messages":
		return cfg.Upstream
	case "responses":
		return cfg.ResponsesUpstream
	case "gemini":
		return cfg.GeminiUpstream
	case "chat":
		return cfg.ChatUpstream
	case "images":
		return cfg.ImagesUpstream
	case "vectors":
		return cfg.VectorsUpstream
	default:
		return nil
	}
}

// carryForwardDiscoveryFields 保留由自动发现写入、L1 流量画像无法重新推导的字段。
func carryForwardDiscoveryFields(old *KeyEndpointProfile, current *KeyEndpointProfile) {
	if old == nil || current == nil {
		return
	}
	current.AccountUID = old.AccountUID
	current.CredentialUID = old.CredentialUID
	current.AvailableModels = append([]string(nil), old.AvailableModels...)
	current.ModelListHash = old.ModelListHash
	current.ModelDiscoverySource = old.ModelDiscoverySource
	current.ModelDiscoveryMessage = old.ModelDiscoveryMessage
	current.ProtocolModels = cloneProtocolModels(old.ProtocolModels)
	current.ProtocolModelsHash = cloneStringMap(old.ProtocolModelsHash)
	current.ProtocolDiscoveredAt = cloneTimeMap(old.ProtocolDiscoveredAt)
	current.ProtocolDiscoverySource = cloneStringMap(old.ProtocolDiscoverySource)
	current.ProtocolDiscoveryMessage = cloneStringMap(old.ProtocolDiscoveryMessage)
	current.ProtocolDiscoveryError = cloneStringMap(old.ProtocolDiscoveryError)
	if old.ModelsDiscoveredAt != nil {
		discoveredAt := old.ModelsDiscoveredAt.UTC()
		current.ModelsDiscoveredAt = &discoveredAt
	}
	current.MiniMaxTokenPlanUsage = cloneMiniMaxTokenPlanUsage(old.MiniMaxTokenPlanUsage)
	current.MiniMaxTokenPlanUsageError = old.MiniMaxTokenPlanUsageError
	// 限速发现字段 carry-forward：DeriveEndpointProfile 每轮构造全新 struct，
	// 限速画像字段是 Discoverer 写入、L1 流量画像无法重新推导的。不搬运的话
	// Discoverer 暂无内存建议（如重启后）时 collectAll 不会从画像恢复，导致
	// ProfileStore 已有画像被本轮 Upsert 清零。当前轮产生的新建议仍优先覆盖旧值
	// （由下方 suggested.RPM>0 分支处理）。
	current.DiscoveredRPM = old.DiscoveredRPM
	current.DiscoveredMaxConcurrent = old.DiscoveredMaxConcurrent
	current.RateLimitSource = old.RateLimitSource
	current.RateLimitConfidence = old.RateLimitConfidence
	current.SuggestedRPMSource = old.SuggestedRPMSource
	current.SuggestedRPMTPM = old.SuggestedRPMTPM
	current.SuggestedRPMRPD = old.SuggestedRPMRPD
	if old.ModelMapping != nil {
		current.ModelMapping = make(map[string]string, len(old.ModelMapping))
		for source, target := range old.ModelMapping {
			current.ModelMapping[source] = target
		}
	}
}

// carryForwardProbeFields 将旧画像的 L2 探测字段搬运到本轮新构造的画像上。
// DeriveEndpointProfile 每轮都是全新 struct，Probe* 字段零值；不搬运的话
// ProbeWorker.applyProbeResult 写入的探测状态会在下一轮 L1 循环里被无声清零，
// 破坏 scanAndEnqueue 的冷却期判定和 ConsecutiveProbeSuccess 的连续计数语义。
// old 为 nil（首次画像）时是 no-op。
func carryForwardProbeFields(old *KeyEndpointProfile, current *KeyEndpointProfile) {
	if old == nil || current == nil {
		return
	}
	current.LastProbeAt = old.LastProbeAt
	current.ProbeSuccess = old.ProbeSuccess
	current.ProbeLatencyMs = old.ProbeLatencyMs
	current.ProbeConfidence = old.ProbeConfidence
	current.ProbeStatusCode = old.ProbeStatusCode
	current.ConsecutiveProbeSuccess = old.ConsecutiveProbeSuccess
}

// carryForwardFirstByteStats 在指标 SQLite 尚未持久化 TTFB 原始样本时，
// 通过已落盘的 endpoint 画像跨重启保留最近一小时聚合值；时间戳保持不变，
// 因此反复重启不会无限续期陈旧样本。
func carryForwardFirstByteStats(old *KeyEndpointProfile, current *KeyEndpointProfile, now time.Time) {
	if old == nil || current == nil || current.FirstByteSampleCount > 0 || current.P95FirstByteLatencyMs > 0 {
		return
	}
	if old.FirstByteSampleCount <= 0 || old.P95FirstByteLatencyMs <= 0 {
		return
	}
	if !isFirstByteStatsFresh(old.FirstByteStatsUpdatedAt, now) {
		return
	}
	current.FirstByteSampleCount = old.FirstByteSampleCount
	current.P95FirstByteLatencyMs = old.P95FirstByteLatencyMs
	updatedAt := *old.FirstByteStatsUpdatedAt
	current.FirstByteStatsUpdatedAt = &updatedAt
}

// carryForwardConnectStats 在连接耗时原始样本仅驻留内存时，跨重启保留仍新鲜的最近聚合值。
func carryForwardConnectStats(old *KeyEndpointProfile, current *KeyEndpointProfile, now time.Time) {
	if old == nil || current == nil || current.ConnectSampleCount > 0 || current.P95ConnectLatencyMs > 0 {
		return
	}
	if old.ConnectSampleCount <= 0 || old.P95ConnectLatencyMs <= 0 {
		return
	}
	if !isFirstByteStatsFresh(old.ConnectStatsUpdatedAt, now) {
		return
	}
	current.ConnectSampleCount = old.ConnectSampleCount
	current.P95ConnectLatencyMs = old.P95ConnectLatencyMs
	updatedAt := *old.ConnectStatsUpdatedAt
	current.ConnectStatsUpdatedAt = &updatedAt
}

// emitProfileChangeEvents 对比 endpointUID 的旧画像与本轮新画像，
// 将检测到的变更写入 changelog 并广播给事件订阅者。
// 必须在调用方执行 m.store.Upsert 之前调用，否则 store.Get 拿到的就是新值，diff 恒为空。
// nil-safe：changelogStore/eventHub 未初始化时静默跳过，不影响 collectAll 主流程。
func (m *Manager) emitProfileChangeEvents(endpointUID string, current *KeyEndpointProfile, groupChanged bool) {
	if m.changelogStore == nil && m.eventHub == nil {
		return
	}
	old := m.store.Get(endpointUID)
	events := DetectProfileChanges(old, current, groupChanged, time.Now())
	for _, ev := range events {
		if m.changelogStore != nil {
			m.changelogStore.Record(ev)
		}
		if m.eventHub != nil {
			m.eventHub.Publish(ev)
		}
	}
}

func (m *Manager) refreshEndpointInventory() endpointInventory {
	inventory := buildEndpointInventory(m.cfgManager.GetConfig())
	m.store.ReplaceActiveEndpointUIDs(inventory.EndpointUIDs)
	if m.modelProfileStore != nil {
		m.modelProfileStore.ReplaceActiveBindings(inventory.ModelProfileBindings)
	}
	return inventory
}

// gatherChannelEntries 从当前配置中提取所有渠道及其 API Key。
func (m *Manager) gatherChannelEntries() []channelEntry {
	return buildEndpointInventory(m.cfgManager.GetConfig()).Entries
}

// collectSignals 从 MetricsManager 收集 endpoint 级被动信号。
func (m *Manager) collectSignals(channelKind, baseURL, apiKey, serviceType string) EndpointSignals {
	now := time.Now()
	signals := EndpointSignals{Now: now}

	// 1 小时窗口
	stats1h := m.metrics.GetTimeWindowStatsForKey(channelKind, baseURL, apiKey, serviceType, 1*time.Hour)
	signals.TotalRequests1h = int(stats1h.RequestCount)
	signals.SuccessCount1h = int(stats1h.SuccessCount)
	signals.FailureCount1h = int(stats1h.FailureCount)
	signals.SuccessRate1h = stats1h.SuccessRate

	// 24 小时窗口
	stats24h := m.metrics.GetTimeWindowStatsForKey(channelKind, baseURL, apiKey, serviceType, 24*time.Hour)
	signals.TotalRequests24h = int(stats24h.RequestCount)
	signals.SuccessCount24h = int(stats24h.SuccessCount)
	signals.FailureCount24h = int(stats24h.FailureCount)

	// 15 分钟窗口
	stats15m := m.metrics.GetTimeWindowStatsForKey(channelKind, baseURL, apiKey, serviceType, 15*time.Minute)
	signals.TotalRequests15m = int(stats15m.RequestCount)
	if stats15m.RequestCount > 0 {
		signals.SuccessRate15m = stats15m.SuccessRate
	}

	// 熔断器快照
	snapshot := m.metrics.GetKeySnapshot(channelKind, baseURL, apiKey, serviceType)
	signals.ConsecutiveFail = int(snapshot.ConsecutiveFailures)
	signals.LastSuccessAt = snapshot.LastSuccessAt
	signals.CircuitBreakerOpen = snapshot.CircuitState == 1 // 1 = open

	// Phase 1: 细粒度错误分类暂不逐条统计，以总失败数代替
	signals.AuthFailureCount = 0
	signals.DNSFailureCount = 0
	signals.QuotaFailureCount = 0
	signals.OverloadedCount = int(snapshot.OverloadedCount)
	signals.RetryAfterCount = 0
	signals.NotFoundCount = 0
	signals.ProtocolErrorCount = 0
	signals.StreamBreakCount = 0
	signals.EmptyResponseCount = 0

	// Key 统计
	signals.TotalKeys = 1
	signals.DisabledKeys = 0

	return signals
}

// updateSubscriptionCapabilities 按 SubscriptionUID 分组调 BuildSharedCapability + DetectCapabilityDrift。
// drift 写入 endpoint 画像的不一致诊断（复用 EndpointInconsistency 结构）。
// Phase 1 shadow：不修改调度链路，仅更新画像字段供 UI 展示。
func (m *Manager) updateSubscriptionCapabilities(profiles []*KeyEndpointProfile) {
	if m.subscriptionStore == nil || len(profiles) == 0 {
		return
	}

	// 构建 channelUID → 订阅 UID 映射
	allSubs := m.subscriptionStore.ListAll()
	channelToSub := make(map[string]string) // channelUID → subscriptionUID
	for _, sub := range allSubs {
		for _, chUID := range sub.LinkedChannelUIDs {
			channelToSub[chUID] = sub.SubscriptionUID
		}
	}

	// 按 subscriptionUID 分组 endpoints
	subEndpoints := make(map[string][]*KeyEndpointProfile) // subscriptionUID → endpoints
	for _, ep := range profiles {
		subUID, ok := channelToSub[ep.ChannelUID]
		if !ok {
			continue
		}
		subEndpoints[subUID] = append(subEndpoints[subUID], ep)
	}

	driftWarnings := 0
	for subUID, eps := range subEndpoints {
		shared := BuildSharedCapability(eps)
		if shared == nil {
			continue
		}

		// 写入订阅画像
		if err := m.subscriptionStore.Patch(subUID, nil, func(profile *SubscriptionProfile) error {
			profile.SharedCapability = shared
			return nil
		}); err != nil {
			log.Printf("[Autopilot-Worker] 警告: 更新订阅共享能力失败 uid=%s: %v", subUID, err)
		}

		// 检测 drift 并写入 endpoint 画像
		for _, ep := range eps {
			ep.InheritedFromSubscription = true
			diffs := DetectCapabilityDrift(shared, ep)
			if len(diffs) > 0 {
				for _, d := range diffs {
					ep.EndpointInconsistencies = append(ep.EndpointInconsistencies, EndpointInconsistency{
						Dimension: "subscription_drift",
						Detail:    d,
						Severity:  "warning",
					})
					driftWarnings++
				}
			}
			if err := m.store.Upsert(ep); err != nil {
				log.Printf("[Autopilot-Worker] 警告: 写入 drift 画像失败 endpoint=%s: %v", ep.EndpointUID, err)
			}
		}
	}

	// 再次落盘（drift 写入的画像变更）
	if driftWarnings > 0 {
		if err := m.store.Flush(); err != nil {
			log.Printf("[Autopilot-Worker] 警告: drift 画像 flush 失败: %v", err)
		}
		if !m.cfg.QuietLogs {
			log.Printf("[Autopilot-Worker] 订阅能力推导: 订阅=%d, drift 警告=%d", len(subEndpoints), driftWarnings)
		}
	}
}
