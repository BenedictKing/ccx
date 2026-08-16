package metrics

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BenedictKing/ccx/internal/eventbus"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
)

// FailureClass 表示请求失败分类，用于区分是否应影响熔断状态机。
type FailureClass string

const (
	FailureClassNone         FailureClass = ""
	FailureClassRetryable    FailureClass = "retryable"
	FailureClassOverloaded   FailureClass = "overloaded"
	FailureClassNonRetryable FailureClass = "non_retryable"
	FailureClassQuota        FailureClass = "quota"
	FailureClassClientCancel FailureClass = "client_cancel"
)

// IsBreakerRelevant 判断失败类型是否应影响 breaker 状态机。
func (fc FailureClass) IsBreakerRelevant() bool {
	return fc == FailureClassRetryable || fc == FailureClassOverloaded
}

func (fc FailureClass) OpensCircuitImmediately() bool {
	return fc == FailureClassOverloaded
}

// CircuitState 表示 Key 当前的熔断状态。
type CircuitState uint8

const (
	CircuitStateClosed CircuitState = iota
	CircuitStateOpen
	CircuitStateHalfOpen
)

func (s CircuitState) String() string {
	switch s {
	case CircuitStateOpen:
		return "open"
	case CircuitStateHalfOpen:
		return "half_open"
	default:
		return "closed"
	}
}

// ParseCircuitState 解析持久化的状态字符串。
func ParseCircuitState(text string) CircuitState {
	switch text {
	case "open":
		return CircuitStateOpen
	case "half_open":
		return CircuitStateHalfOpen
	default:
		return CircuitStateClosed
	}
}

// 默认熔断器参数常量（温和作为新默认，原温和→均衡、原均衡→激进）
const (
	defaultConsecutiveRetryableFailuresThreshold int64         = 5
	defaultHalfOpenSuccessThreshold              int           = 1
	defaultCircuitBackoffBase                    time.Duration = 30 * time.Second
	defaultCircuitBackoffMax                     time.Duration = 10 * time.Minute
	defaultBreakerHealthWindow                   time.Duration = 15 * time.Minute
	// 流式健康检测默认参数（匹配新均衡策略 = 原温和）
	defaultStreamFirstContentTimeoutMs = 90000  // HTTP 200 后首个有效内容等待超时（90秒）
	defaultStreamInactivityTimeoutMs   = 90000  // 首字后连续性确认窗口（90秒）
	defaultStreamToolCallIdleTimeoutMs = 300000 // 工具调用空闲超时（300秒）
)

// RequestRecord 带时间戳的请求记录（扩展版，支持 Token、Cache 和失败分类数据）。
type RequestRecord struct {
	ChannelUID               string // 渠道稳定标识，breakerscope 三元组之一
	RouteModel               string // 客户端原始请求模型，breakerscope 三元组之一；空为未知
	Model                    string // 实际发给上游的模型（可被 autopilot 映射改写）
	Timestamp                time.Time
	ConnectLatencyMs         int64
	FirstByteLatencyMs       int64
	Success                  bool
	FailureClass             FailureClass
	InputTokens              int64
	OutputTokens             int64
	CacheCreationInputTokens int64
	CacheReadInputTokens     int64
	ProxyKeyMask             string // 代理 Key 掩码（用于成本报表按用户分组，由 RecordRequestConnected 传入）
	KeyUID                   string
	SubscriptionUID          string
	ExchangeSnapshotVersion  uint64
	ListCostUSD              float64
	EffectiveCostUSD         float64
	EffectiveCostMultiplier  float64
	EffectiveCostAvailable   bool
	EffectiveCostReason      string
	ConsumptionPolicy        string
}

// recordRouteModel 返回 breaker 聚合使用的模型键。
// RouteModel 非空优先；否则回退 Model（兼容旧记录和同步写入入口）。
func recordRouteModel(record *RequestRecord) string {
	if record == nil {
		return ""
	}
	if record.RouteModel != "" {
		return record.RouteModel
	}
	return record.Model
}

// KeyMetrics 单个 Key 的指标（绑定到 BaseURL + Key 组合）
type KeyMetrics struct {
	MetricsKey          string       `json:"metricsKey"`          // hash(baseURL + apiKey)
	BaseURL             string       `json:"baseUrl"`             // 用于显示
	KeyMask             string       `json:"keyMask"`             // 脱敏的 key（用于显示）
	RequestCount        int64        `json:"requestCount"`        // 总请求数
	SuccessCount        int64        `json:"successCount"`        // 成功数
	FailureCount        int64        `json:"failureCount"`        // 失败数
	ConsecutiveFailures int64        `json:"consecutiveFailures"` // 连续可重试失败数
	ActiveRequests      int64        `json:"activeRequests"`      // 进行中的请求数
	LastSuccessAt       *time.Time   `json:"lastSuccessAt,omitempty"`
	LastFailureAt       *time.Time   `json:"lastFailureAt,omitempty"`
	CircuitBrokenAt     *time.Time   `json:"circuitBrokenAt,omitempty"` // breaker 进入 open 的时间（兼容旧字段）
	CircuitState        CircuitState `json:"-"`
	HalfOpenAt          *time.Time   `json:"halfOpenAt,omitempty"`
	NextRetryAt         *time.Time   `json:"nextRetryAt,omitempty"`
	BackoffLevel        int          `json:"backoffLevel"`
	HalfOpenSuccesses   int          `json:"halfOpenSuccesses"`
	ProbeInFlight       bool         `json:"-"`
	// 滑动窗口记录（最近 N 次请求的结果，用于展示综合成功率）
	recentResults []bool // true=success, false=failure
	// breaker 滑动窗口（仅记录成功和可重试失败）
	breakerResults []bool
	// breaker 窗口内失败涉及的模型集合（用于模型多样性门槛：
	// 仅当失败跨多个不同模型时才允许熔断整个 Key，避免单一模型故障拖垮 Key）
	breakerFailureModels map[string]struct{}
	// 带时间戳的请求记录（用于分时段统计，保留24小时）
	requestHistory []RequestRecord
	// 进行中请求在 requestHistory 中的索引（用于“连接即计数”，结束后回写成功/失败与 token）
	pendingHistoryIdx map[uint64]int
}

// ChannelMetrics 渠道聚合指标（用于 API 返回，兼容旧结构）
type ChannelMetrics struct {
	ChannelIndex        int          `json:"channelIndex"`
	RequestCount        int64        `json:"requestCount"`
	SuccessCount        int64        `json:"successCount"`
	FailureCount        int64        `json:"failureCount"`
	ConsecutiveFailures int64        `json:"consecutiveFailures"`
	LastSuccessAt       *time.Time   `json:"lastSuccessAt,omitempty"`
	LastFailureAt       *time.Time   `json:"lastFailureAt,omitempty"`
	CircuitBrokenAt     *time.Time   `json:"circuitBrokenAt,omitempty"`
	CircuitState        CircuitState `json:"-"`
	NextRetryAt         *time.Time   `json:"nextRetryAt,omitempty"`
	HalfOpenSuccesses   int          `json:"halfOpenSuccesses"`
	// 滑动窗口记录（兼容旧代码）
	recentResults  []bool
	breakerResults []bool
	// 带时间戳的请求记录
	requestHistory []RequestRecord
}

// TimeWindowStats 分时段统计
// 使用 omitempty 减少 JSON 体积，0 值字段不输出
// 注意：successRate 不使用 omitempty，因为 0% 是有意义的值（全失败）
type TimeWindowStats struct {
	RequestCount int64   `json:"requestCount,omitempty"`
	SuccessCount int64   `json:"successCount,omitempty"`
	FailureCount int64   `json:"failureCount,omitempty"`
	SuccessRate  float64 `json:"successRate"`
	// 连接取得耗时从发起上游请求到 httptrace.GotConn；仅统计最终成功请求。
	ConnectSampleCount  int64 `json:"connectSampleCount,omitempty"`
	P95ConnectLatencyMs int64 `json:"p95ConnectLatencyMs,omitempty"`
	// 首响应字节（TTFB）仅统计成功请求；快速错误、超时和取消不污染样本。
	FirstByteSampleCount  int64 `json:"firstByteSampleCount,omitempty"`
	P95FirstByteLatencyMs int64 `json:"p95FirstByteLatencyMs,omitempty"`
	// Token 统计（按时间窗口聚合）
	InputTokens         int64 `json:"inputTokens,omitempty"`
	OutputTokens        int64 `json:"outputTokens,omitempty"`
	CacheCreationTokens int64 `json:"cacheCreationTokens,omitempty"`
	CacheReadTokens     int64 `json:"cacheReadTokens,omitempty"`
	// CacheHitRate 缓存命中率（Token口径），范围 0-100
	// 定义：cacheReadTokens / (cacheReadTokens + inputTokens) * 100
	CacheHitRate float64 `json:"cacheHitRate,omitempty"`
}

// MetricsManager 指标管理器
type MetricsManager struct {
	mu                           sync.RWMutex
	keyMetrics                   map[string]*KeyMetrics // key: hash(baseURL + apiKey)
	windowSize                   int                    // 滑动窗口大小
	failureThreshold             float64                // 失败率阈值
	consecutiveFailuresThreshold int64                  // 连续可重试失败触发阈值
	circuitRecoveryTime          time.Duration          // 兼容旧统计字段，表示基础探测冷却时间
	circuitBackoffBase           time.Duration
	circuitBackoffMax            time.Duration
	halfOpenSuccessTarget        int
	stopCh                       chan struct{} // 用于停止清理 goroutine
	nextRequestID                uint64        // 单进程递增请求ID（用于 pendingHistoryIdx）

	// 流式健康检测参数
	streamFirstContentTimeoutMs int // HTTP 200 后首个有效内容等待超时（ms，0=禁用）
	streamInactivityTimeoutMs   int // 首字后连续性确认窗口（ms，0=禁用）
	streamToolCallIdleTimeoutMs int // 工具调用空闲超时（ms）

	// 持久化存储（可选）
	store   PersistenceStore
	apiType string // "messages"、"responses"、"gemini" 或 "chat"

	// 渠道-模型级熔断（内存态，自带独立锁，见 model_circuit.go）
	modelCircuit *ModelCircuitTracker

	// 跨模块事件总线（Phase B.1，可选）。未注入时熔断迁移不发事件。
	eventBus atomic.Pointer[eventbus.Bus]
}

// ModelCircuit 返回渠道-模型级熔断追踪器。
func (m *MetricsManager) ModelCircuit() *ModelCircuitTracker {
	if m == nil {
		return nil
	}
	return m.modelCircuit
}

// GetPersistenceStore 获取持久化存储（可能为 nil）
func (m *MetricsManager) GetPersistenceStore() PersistenceStore {
	return m.store
}

// GetAPIType 获取 API 类型
func (m *MetricsManager) GetAPIType() string {
	return m.apiType
}

// SetEventBus 注入跨模块事件总线（Phase B.1）。可选依赖：未注入时熔断迁移不发事件，
// 现有调度与查询行为不变。并发安全。
func (m *MetricsManager) SetEventBus(bus *eventbus.Bus) {
	if m == nil {
		return
	}
	m.eventBus.Store(bus)
}

// NewMetricsManager 创建指标管理器
// NewMetricsManager 创建指标管理器
func NewMetricsManager() *MetricsManager {
	m := &MetricsManager{
		keyMetrics:                   make(map[string]*KeyMetrics),
		windowSize:                   20,  // 默认基于最近 20 次请求计算失败率（均衡策略 = 原温和）
		failureThreshold:             0.7, // 默认 70% 失败率阈值（均衡策略 = 原温和）
		consecutiveFailuresThreshold: defaultConsecutiveRetryableFailuresThreshold,
		circuitRecoveryTime:          defaultCircuitBackoffBase,
		circuitBackoffBase:           defaultCircuitBackoffBase,
		circuitBackoffMax:            defaultCircuitBackoffMax,
		halfOpenSuccessTarget:        defaultHalfOpenSuccessThreshold,
		streamFirstContentTimeoutMs:  defaultStreamFirstContentTimeoutMs,
		streamInactivityTimeoutMs:    defaultStreamInactivityTimeoutMs,
		streamToolCallIdleTimeoutMs:  defaultStreamToolCallIdleTimeoutMs,
		stopCh:                       make(chan struct{}),
		modelCircuit:                 NewModelCircuitTracker(""),
	}
	// 启动后台熔断恢复任务
	go m.cleanupCircuitBreakers()
	return m
}

// NewMetricsManagerWithConfig 创建带配置的指标管理器
// NewMetricsManagerWithConfig 创建带配置的指标管理器
func NewMetricsManagerWithConfig(windowSize int, failureThreshold float64) *MetricsManager {
	if windowSize < 3 {
		windowSize = 3 // 最小 3
	}
	if failureThreshold <= 0 || failureThreshold > 1 {
		failureThreshold = 0.5
	}
	m := &MetricsManager{
		keyMetrics:                   make(map[string]*KeyMetrics),
		windowSize:                   windowSize,
		failureThreshold:             failureThreshold,
		consecutiveFailuresThreshold: defaultConsecutiveRetryableFailuresThreshold,
		circuitRecoveryTime:          defaultCircuitBackoffBase,
		circuitBackoffBase:           defaultCircuitBackoffBase,
		circuitBackoffMax:            defaultCircuitBackoffMax,
		halfOpenSuccessTarget:        defaultHalfOpenSuccessThreshold,
		streamFirstContentTimeoutMs:  defaultStreamFirstContentTimeoutMs,
		streamInactivityTimeoutMs:    defaultStreamInactivityTimeoutMs,
		streamToolCallIdleTimeoutMs:  defaultStreamToolCallIdleTimeoutMs,
		stopCh:                       make(chan struct{}),
		modelCircuit:                 NewModelCircuitTracker(""),
	}
	// 启动后台熔断恢复任务
	go m.cleanupCircuitBreakers()
	return m
}

// NewMetricsManagerWithPersistence 创建带持久化的指标管理器
// NewMetricsManagerWithPersistence 创建带持久化的指标管理器
func NewMetricsManagerWithPersistence(windowSize int, failureThreshold float64, store PersistenceStore, apiType string) *MetricsManager {
	if windowSize < 3 {
		windowSize = 3
	}
	if failureThreshold <= 0 || failureThreshold > 1 {
		failureThreshold = 0.5
	}
	m := &MetricsManager{
		keyMetrics:                   make(map[string]*KeyMetrics),
		windowSize:                   windowSize,
		failureThreshold:             failureThreshold,
		consecutiveFailuresThreshold: defaultConsecutiveRetryableFailuresThreshold,
		circuitRecoveryTime:          defaultCircuitBackoffBase,
		circuitBackoffBase:           defaultCircuitBackoffBase,
		circuitBackoffMax:            defaultCircuitBackoffMax,
		halfOpenSuccessTarget:        defaultHalfOpenSuccessThreshold,
		streamFirstContentTimeoutMs:  defaultStreamFirstContentTimeoutMs,
		streamInactivityTimeoutMs:    defaultStreamInactivityTimeoutMs,
		streamToolCallIdleTimeoutMs:  defaultStreamToolCallIdleTimeoutMs,
		stopCh:                       make(chan struct{}),
		store:                        store,
		apiType:                      apiType,
		modelCircuit:                 NewModelCircuitTracker(apiType),
	}

	// 从持久化存储加载历史数据
	if store != nil {
		if err := m.loadFromStore(); err != nil {
			log.Printf("[Metrics-Load] 警告: [%s] 加载历史指标数据失败: %v", apiType, err)
		}
	}

	// 启动后台熔断恢复任务
	go m.cleanupCircuitBreakers()
	return m
}

// loadFromStore 从持久化存储加载数据
func (m *MetricsManager) loadFromStore() error {
	if m.store == nil {
		return nil
	}

	// 加载最近 24 小时的数据
	since := time.Now().Add(-24 * time.Hour)
	records, err := m.store.LoadRecords(since, m.apiType)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(records) == 0 {
		log.Printf("[Metrics-Load] [%s] 无历史指标数据需要加载", m.apiType)
	} else {
		for _, r := range records {
			metrics := m.getOrCreateKeyLocked(r.BaseURL, r.MetricsKey, r.KeyMask)

			metrics.requestHistory = append(metrics.requestHistory, RequestRecord{
				Model:                    r.Model,
				Timestamp:                r.Timestamp,
				Success:                  r.Success,
				FailureClass:             normalizeFailureClass(r.Success, r.FailureClass),
				InputTokens:              r.InputTokens,
				OutputTokens:             r.OutputTokens,
				CacheCreationInputTokens: r.CacheCreationTokens,
				CacheReadInputTokens:     r.CacheReadTokens,
			})

			metrics.RequestCount++
			if r.Success {
				metrics.SuccessCount++
				if metrics.LastSuccessAt == nil || r.Timestamp.After(*metrics.LastSuccessAt) {
					t := r.Timestamp
					metrics.LastSuccessAt = &t
				}
			} else {
				metrics.FailureCount++
				if metrics.LastFailureAt == nil || r.Timestamp.After(*metrics.LastFailureAt) {
					t := r.Timestamp
					metrics.LastFailureAt = &t
				}
				if normalizeFailureClass(r.Success, r.FailureClass).IsBreakerRelevant() {
					metrics.ConsecutiveFailures++
				}
			}
		}

		now := time.Now()
		for _, metrics := range m.keyMetrics {
			m.refreshBreakerWindowsLocked(metrics, now)
		}
	}

	m.loadHistoricalTimestamps()

	states, err := m.store.LoadCircuitStates(m.apiType)
	if err != nil {
		return err
	}
	for metricsKey, state := range states {
		metrics, ok := m.keyMetrics[metricsKey]
		if !ok {
			metrics = m.getOrCreateKeyLocked(state.BaseURL, state.MetricsKey, state.KeyMask)
		}
		metrics.CircuitState = ParseCircuitState(state.CircuitState)
		metrics.CircuitBrokenAt = state.CircuitOpenedAt
		metrics.HalfOpenAt = state.HalfOpenAt
		metrics.NextRetryAt = state.NextRetryAt
		metrics.BackoffLevel = state.BackoffLevel
		metrics.HalfOpenSuccesses = state.HalfOpenSuccesses
		metrics.ConsecutiveFailures = state.ConsecutiveFailures
		metrics.ProbeInFlight = false
		m.refreshBreakerWindowsLocked(metrics, time.Now())
	}

	log.Printf("[Metrics-Load] [%s] 已从持久化存储加载 %d 条历史记录、%d 条熔断状态，重建 %d 个 Key 指标",
		m.apiType, len(records), len(states), len(m.keyMetrics))
	return nil
}

// loadHistoricalTimestamps 加载全量历史时间戳，补全超出 24h 窗口的 LastSuccessAt/LastFailureAt。
// 调用前必须已持有 m.mu.Lock()。
func (m *MetricsManager) loadHistoricalTimestamps() {
	timestamps, err := m.store.LoadLatestTimestamps(m.apiType)
	if err != nil {
		log.Printf("[Metrics-Load] 警告: [%s] 加载历史时间戳失败: %v", m.apiType, err)
		return
	}
	for metricsKey, kt := range timestamps {
		existing, ok := m.keyMetrics[metricsKey]
		if !ok {
			// 24h 内无记录但历史有请求：创建空壳，只携带时间戳
			existing = m.getOrCreateKeyLocked(kt.BaseURL, metricsKey, kt.KeyMask)
		}
		// 只在持久化值更新时覆盖（防回退）
		if kt.LastSuccessAt != nil && (existing.LastSuccessAt == nil || kt.LastSuccessAt.After(*existing.LastSuccessAt)) {
			existing.LastSuccessAt = kt.LastSuccessAt
		}
		if kt.LastFailureAt != nil && (existing.LastFailureAt == nil || kt.LastFailureAt.After(*existing.LastFailureAt)) {
			existing.LastFailureAt = kt.LastFailureAt
		}
	}
}

// getOrCreateKeyLocked 获取或创建 Key 指标（用于加载时，已知 metricsKey 和 keyMask）
func (m *MetricsManager) getOrCreateKeyLocked(baseURL, metricsKey, keyMask string) *KeyMetrics {
	if metrics, exists := m.keyMetrics[metricsKey]; exists {
		return metrics
	}
	metrics := &KeyMetrics{
		MetricsKey:        metricsKey,
		BaseURL:           baseURL,
		KeyMask:           keyMask,
		CircuitState:      CircuitStateClosed,
		recentResults:     make([]bool, 0, m.windowSize),
		breakerResults:    make([]bool, 0, m.windowSize),
		pendingHistoryIdx: make(map[uint64]int),
	}
	m.keyMetrics[metricsKey] = metrics
	return metrics
}

// generateMetricsKey 生成指标键 hash(baseURL + apiKey)（内部使用）
func generateMetricsKey(baseURL, apiKey string) string {
	h := sha256.New()
	h.Write([]byte(baseURL + "|" + apiKey))
	return hex.EncodeToString(h.Sum(nil))[:16] // 取前16位作为键
}

// GenerateMetricsKey 生成指标键 hash(baseURL + apiKey)（导出供外部使用）
func GenerateMetricsKey(baseURL, apiKey string) string {
	return generateMetricsKey(baseURL, apiKey)
}

func GenerateMetricsIdentityKey(baseURL, apiKey, serviceType string) string {
	return generateMetricsKey(utils.MetricsIdentityBaseURL(baseURL, serviceType), apiKey)
}

// GenerateMetricsLookupKeys 生成用于查询同一指标身份的所有兼容键。
// 第一个键是当前规范身份键；后续键覆盖历史 baseURL 归一化规则产生的兼容键。
func GenerateMetricsLookupKeys(baseURL, apiKey, serviceType string) []string {
	seen := make(map[string]struct{}, 4)
	keys := make([]string, 0, 4)
	add := func(metricsKey string) {
		if metricsKey == "" {
			return
		}
		if _, exists := seen[metricsKey]; exists {
			return
		}
		seen[metricsKey] = struct{}{}
		keys = append(keys, metricsKey)
	}

	add(GenerateMetricsIdentityKey(baseURL, apiKey, serviceType))
	for _, variant := range utils.EquivalentBaseURLVariants(baseURL, serviceType) {
		add(generateMetricsKey(variant, apiKey))
	}
	return keys
}

func (m *MetricsManager) metricsLookupKeys(baseURL, apiKey, serviceType string) []string {
	return GenerateMetricsLookupKeys(baseURL, apiKey, serviceType)
}

func (m *MetricsManager) getIdentityMetricsLocked(baseURL, apiKey, serviceType string) *KeyMetrics {
	for _, metricsKey := range m.metricsLookupKeys(baseURL, apiKey, serviceType) {
		if metrics, exists := m.keyMetrics[metricsKey]; exists {
			return metrics
		}
	}
	return nil
}

func (m *MetricsManager) getMetricsVariantsLocked(baseURL, apiKey, serviceType string) []*KeyMetrics {
	lookupKeys := m.metricsLookupKeys(baseURL, apiKey, serviceType)
	seen := make(map[*KeyMetrics]struct{}, len(lookupKeys))
	variants := make([]*KeyMetrics, 0, len(lookupKeys))
	for _, metricsKey := range lookupKeys {
		metrics, exists := m.keyMetrics[metricsKey]
		if !exists {
			continue
		}
		if _, duplicated := seen[metrics]; duplicated {
			continue
		}
		seen[metrics] = struct{}{}
		variants = append(variants, metrics)
	}
	return variants
}

func (m *MetricsManager) getFirstMatchingMetricsLocked(baseURL, apiKey, serviceType string) *KeyMetrics {
	return m.getIdentityMetricsLocked(baseURL, apiKey, serviceType)
}

func (m *MetricsManager) metricsIdentityKey(baseURL, apiKey, serviceType string) string {
	return generateMetricsKey(utils.MetricsIdentityBaseURL(baseURL, serviceType), apiKey)
}

func (m *MetricsManager) circuitStateSeverity(state CircuitState) int {
	switch state {
	case CircuitStateOpen:
		return 3
	case CircuitStateHalfOpen:
		return 2
	default:
		return 1
	}
}

func (m *MetricsManager) mergeKeyMetricsLocked(dst, src *KeyMetrics) {
	if dst == nil || src == nil || dst == src {
		return
	}

	dst.RequestCount += src.RequestCount
	dst.SuccessCount += src.SuccessCount
	dst.FailureCount += src.FailureCount
	dst.ActiveRequests += src.ActiveRequests
	if src.ConsecutiveFailures > dst.ConsecutiveFailures {
		dst.ConsecutiveFailures = src.ConsecutiveFailures
	}
	if dst.LastSuccessAt == nil || (src.LastSuccessAt != nil && src.LastSuccessAt.After(*dst.LastSuccessAt)) {
		dst.LastSuccessAt = src.LastSuccessAt
	}
	if dst.LastFailureAt == nil || (src.LastFailureAt != nil && src.LastFailureAt.After(*dst.LastFailureAt)) {
		dst.LastFailureAt = src.LastFailureAt
	}
	if len(src.recentResults) > 0 {
		dst.recentResults = append(dst.recentResults, src.recentResults...)
		if len(dst.recentResults) > m.windowSize {
			dst.recentResults = dst.recentResults[len(dst.recentResults)-m.windowSize:]
		}
	}
	if len(src.breakerResults) > 0 {
		dst.breakerResults = append(dst.breakerResults, src.breakerResults...)
		if len(dst.breakerResults) > m.windowSize {
			dst.breakerResults = dst.breakerResults[len(dst.breakerResults)-m.windowSize:]
		}
	}
	if len(src.breakerFailureModels) > 0 {
		if dst.breakerFailureModels == nil {
			dst.breakerFailureModels = make(map[string]struct{}, len(src.breakerFailureModels))
		}
		for model := range src.breakerFailureModels {
			dst.breakerFailureModels[model] = struct{}{}
		}
	}
	if len(src.requestHistory) > 0 {
		offset := len(dst.requestHistory)
		dst.requestHistory = append(dst.requestHistory, src.requestHistory...)
		if dst.pendingHistoryIdx == nil {
			dst.pendingHistoryIdx = make(map[uint64]int, len(src.pendingHistoryIdx))
		}
		for requestID, idx := range src.pendingHistoryIdx {
			dst.pendingHistoryIdx[requestID] = offset + idx
		}
	}
	if src.ProbeInFlight {
		dst.ProbeInFlight = true
	}
	if src.BackoffLevel > dst.BackoffLevel {
		dst.BackoffLevel = src.BackoffLevel
	}
	if src.HalfOpenSuccesses > dst.HalfOpenSuccesses {
		dst.HalfOpenSuccesses = src.HalfOpenSuccesses
	}
	if dst.CircuitBrokenAt == nil || (src.CircuitBrokenAt != nil && src.CircuitBrokenAt.After(*dst.CircuitBrokenAt)) {
		dst.CircuitBrokenAt = src.CircuitBrokenAt
	}
	if dst.HalfOpenAt == nil || (src.HalfOpenAt != nil && src.HalfOpenAt.After(*dst.HalfOpenAt)) {
		dst.HalfOpenAt = src.HalfOpenAt
	}
	if dst.NextRetryAt == nil || (src.NextRetryAt != nil && src.NextRetryAt.After(*dst.NextRetryAt)) {
		dst.NextRetryAt = src.NextRetryAt
	}
	if m.circuitStateSeverity(src.CircuitState) > m.circuitStateSeverity(dst.CircuitState) {
		dst.CircuitState = src.CircuitState
	}
}

func (m *MetricsManager) getWritableMetricsLocked(baseURL, apiKey, serviceType string) *KeyMetrics {
	if metrics := m.getIdentityMetricsLocked(baseURL, apiKey, serviceType); metrics != nil {
		if metrics.MetricsKey != m.metricsIdentityKey(baseURL, apiKey, serviceType) {
			return m.getOrCreateKey(baseURL, apiKey, serviceType)
		}
		return metrics
	}
	return m.getOrCreateKey(baseURL, apiKey, serviceType)
}

func (m *MetricsManager) getIdentityMetricsByMultiURLLocked(baseURLs []string, apiKey, serviceType string) []*KeyMetrics {
	seen := make(map[*KeyMetrics]struct{}, len(baseURLs))
	result := make([]*KeyMetrics, 0, len(baseURLs))
	for _, baseURL := range baseURLs {
		for _, metrics := range m.getMetricsVariantsLocked(baseURL, apiKey, serviceType) {
			if _, exists := seen[metrics]; exists {
				continue
			}
			seen[metrics] = struct{}{}
			result = append(result, metrics)
		}
	}
	return result
}

func (m *MetricsManager) getIdentityMetricsByMultiURLAndKeysLocked(baseURLs, apiKeys []string, serviceType string) []*KeyMetrics {
	seen := make(map[string]struct{}, len(baseURLs)*max(1, len(apiKeys)))
	result := make([]*KeyMetrics, 0, len(baseURLs)*max(1, len(apiKeys)))
	for _, apiKey := range apiKeys {
		for _, metrics := range m.getIdentityMetricsByMultiURLLocked(baseURLs, apiKey, serviceType) {
			if _, exists := seen[metrics.MetricsKey]; exists {
				continue
			}
			seen[metrics.MetricsKey] = struct{}{}
			result = append(result, metrics)
		}
	}
	return result
}

func (m *MetricsManager) hasAvailableIdentityCandidateLocked(baseURLs, apiKeys []string, serviceType string) bool {
	seen := make(map[string]struct{}, len(baseURLs)*max(1, len(apiKeys)))
	for _, apiKey := range apiKeys {
		for _, baseURL := range baseURLs {
			identityKey := m.metricsIdentityKey(baseURL, apiKey, serviceType)
			if _, exists := seen[identityKey]; exists {
				continue
			}
			seen[identityKey] = struct{}{}
			metrics, exists := m.keyMetrics[identityKey]
			if !exists {
				for _, lookupKey := range m.metricsLookupKeys(baseURL, apiKey, serviceType) {
					if lookupKey == identityKey {
						continue
					}
					if metrics, exists = m.keyMetrics[lookupKey]; exists {
						break
					}
				}
			}
			if !exists || metrics.CircuitState != CircuitStateOpen {
				return true
			}
		}
	}
	return false
}

func (m *MetricsManager) findPendingRequestMetricsLocked(baseURL, apiKey, serviceType string, requestID uint64) *KeyMetrics {
	for _, metrics := range m.getMetricsVariantsLocked(baseURL, apiKey, serviceType) {
		if metrics == nil || metrics.pendingHistoryIdx == nil {
			continue
		}
		if _, ok := metrics.pendingHistoryIdx[requestID]; ok {
			return metrics
		}
	}
	return nil
}

// getOrCreateKey 获取或创建 Key 指标
func (m *MetricsManager) getOrCreateKey(baseURL, apiKey, serviceType string) *KeyMetrics {
	identityBaseURL := utils.MetricsIdentityBaseURL(baseURL, serviceType)
	metricsKey := generateMetricsKey(identityBaseURL, apiKey)
	if metrics, exists := m.keyMetrics[metricsKey]; exists {
		return metrics
	}

	var primary *KeyMetrics
	for _, lookupKey := range m.metricsLookupKeys(baseURL, apiKey, serviceType) {
		if lookupKey == metricsKey {
			continue
		}
		metrics, exists := m.keyMetrics[lookupKey]
		if !exists {
			continue
		}
		if primary == nil {
			primary = metrics
			continue
		}
		m.mergeKeyMetricsLocked(primary, metrics)
		delete(m.keyMetrics, lookupKey)
	}
	if primary != nil {
		primary.MetricsKey = metricsKey
		primary.BaseURL = identityBaseURL
		m.keyMetrics[metricsKey] = primary
		for _, lookupKey := range m.metricsLookupKeys(baseURL, apiKey, serviceType) {
			if lookupKey == metricsKey {
				continue
			}
			if current, exists := m.keyMetrics[lookupKey]; exists && current == primary {
				delete(m.keyMetrics, lookupKey)
			}
		}
		return primary
	}

	metrics := &KeyMetrics{
		MetricsKey:        metricsKey,
		BaseURL:           identityBaseURL,
		KeyMask:           utils.MaskAPIKey(apiKey),
		CircuitState:      CircuitStateClosed,
		recentResults:     make([]bool, 0, m.windowSize),
		breakerResults:    make([]bool, 0, m.windowSize),
		pendingHistoryIdx: make(map[uint64]int),
	}
	m.keyMetrics[metricsKey] = metrics
	return metrics
}

func normalizeFailureClass(success bool, failureClass FailureClass) FailureClass {
	if success {
		return FailureClassNone
	}
	if failureClass == FailureClassNone {
		return FailureClassRetryable
	}
	return failureClass
}

func isBreakerRelevantFailure(success bool, failureClass FailureClass) bool {
	if success {
		return true
	}
	return normalizeFailureClass(success, failureClass).IsBreakerRelevant()
}

func extractUsageTokens(usage *types.Usage) (int64, int64, int64, int64) {
	if usage == nil {
		return 0, 0, 0, 0
	}
	inputTokens := int64(usage.InputTokens)
	cacheReadTokens := int64(usage.CacheReadInputTokens)
	if usage.PromptTokensTotal > 0 && cacheReadTokens > 0 {
		normalizedInput := usage.PromptTokensTotal - usage.CacheReadInputTokens
		if normalizedInput < 0 {
			normalizedInput = 0
		}
		inputTokens = int64(normalizedInput)
	}
	outputTokens := int64(usage.OutputTokens)
	cacheCreationTokens := int64(usage.CacheCreationInputTokens)
	if cacheCreationTokens <= 0 {
		cacheCreationTokens = int64(usage.CacheCreation5mInputTokens + usage.CacheCreation1hInputTokens)
	}
	return inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens
}
