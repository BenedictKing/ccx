// Package healthcheck 渠道保活验证：后台调度循环 + L1（带 key 拉上游模型列表）/ L2（真实调用验活）+ 结果处置。
// 验证结果按 check_kind（l1/l2）分别落 key_health 表，到期判定只看 l1 记录。
package healthcheck

import (
	"context"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
)

// 验证级别（key_health.check_kind），L2 由后续任务实现
const (
	CheckKindL1            = "l1"
	CheckKindL2            = "l2"
	CheckKindL2ModelPrefix = "l2:"
)

// 验证结果（key_health.last_status）
const (
	StatusOK         = "ok"
	StatusAuthFailed = "auth_failed"
	StatusError      = "error"
)

// ChannelTypes 正式支持的六类渠道（与调度器 ChannelKind 取值一致）
var ChannelTypes = []string{"messages", "chat", "responses", "gemini", "images", "vectors"}

const (
	defaultScanInterval = 5 * time.Minute
	defaultStopTimeout  = 10 * time.Second
	defaultWorkers      = 4
	taskQueueSize       = 256
)

// L1Request L1 验证请求：带单个 key 探测一个 BaseURL 的模型列表
type L1Request struct {
	BaseURL            string
	APIKey             string
	ServiceType        string
	AuthHeader         string
	CustomHeaders      map[string]string
	ProxyURL           string
	InsecureSkipVerify bool
}

// L1Response 包装后的模型列表响应（由各渠道 GetChannelModels handler 适配而来）
type L1Response struct {
	StatusCode int
	Body       []byte
	// RealCallVerified 标记该次 L1 已对上游发起过真实推理调用（如火山套餐数据面探针）。
	// 调度器据此跳过同周期等价 L2，避免重复消耗套餐额度；通用 /v1/models 拉取不置位。
	RealCallVerified bool
	// Model 仅供已发起真实推理调用的 L1 探针记录实际请求模型；通用模型列表探测留空。
	Model string
}

// L1Fetcher 按渠道类型注册的 L1 模型列表拉取器（main.go 接线时注册六类）
type L1Fetcher func(ctx context.Context, req L1Request) (L1Response, error)

// KeyHealthStore 保活验证结果持久化的最小接口（*metrics.SQLiteStore 已实现）
type KeyHealthStore interface {
	UpsertKeyHealth(rec metrics.KeyHealthRecord) error
	GetKeyHealthForChannel(channelType, channelID string) ([]metrics.KeyHealthRecord, error)
	GetAllKeyHealth() ([]metrics.KeyHealthRecord, error)
}

// BlacklistFunc 鉴权失败拉黑回调（main.go 注入，内部调 ConfigManager.BlacklistKeyWithRecoverAt）
type BlacklistFunc func(channelType string, channelIndex int, apiKey, reason, message, recoverAt string)

// RecordFailureFunc 失败喂熔断回调（main.go 注入，内部调 scheduler.RecordFailure 并写渠道日志）
// serviceType 为渠道配置的原始 ServiceType（未归一化），model 仅在 L2 真实调用时有值，
// detail 为失败原因摘要（已截断）。
type RecordFailureFunc func(channelType string, channelIndex int, baseURL, apiKey, serviceType, model, detail string)

// Options Manager 可选参数（零值使用默认值；测试可注入时钟与间隔）
type Options struct {
	ScanInterval time.Duration // 调度扫描间隔（默认 5min）
	StopTimeout  time.Duration // Stop 等待 worker 池排空的超时（默认 10s）
	Now          func() time.Time
}

// Manager 渠道保活验证调度器
// Manager 渠道保活验证调度器
type Manager struct {
	getConfig     func() config.Config
	store         KeyHealthStore
	blacklist     BlacklistFunc
	recordFailure RecordFailureFunc
	fetchers      map[string]L1Fetcher

	// modelCircuitLookup 按渠道类型返回模型级熔断追踪器；nil 时选择器退化为持久化记录+成本排序。
	modelCircuitLookup func(channelType string) *metrics.ModelCircuitTracker

	// usageResolver 读取火山套餐 AFP 余额快照，用于稀疏 L2 预算联动。
	// nil 时按静态预算处理，不放大也不缩小预算。
	usageResolver ProbeUsageResolver

	scanInterval time.Duration
	stopTimeout  time.Duration
	now          func() time.Time

	mu       sync.Mutex
	running  bool
	stopCh   chan struct{}
	tasks    chan checkTask
	wg       sync.WaitGroup
	inFlight map[string]struct{}

	// capabilityProbeLedger 记录本扫描周期内已探测的能力（按 CapabilityUID），
	// 同一扫描周期内跨账号 key 对同站点同分组协议/模型探测只执行一次，其余复用结论。
	// 周期边界与 Manager.scan 每次调度时 Reset 对齐（由 loop 的 ticker 驱动）。
	capabilityProbeLedger *config.CapabilityProbeLedger

	// l2Reuse 记录本扫描周期内 L2 成功结论（CapabilityUID -> 探测所用模型），
	// 供同站点同分组的后续 key 复用；仅成功结论可复用，失败按 key 各自探测。
	// 与 capabilityProbeLedger 同周期清空（scan 开始处）。独立小锁保护（多 worker 并发）。
	l2ReuseMu sync.Mutex
	l2Reuse   map[string]string
}

// checkTask 验证任务：单渠道全 key L1 验证（渠道内 key 串行）
type checkTask struct {
	channelType  string
	channelIndex int
}

// NewManager 创建保活验证调度器。getConfig 每次扫描时调用，热重载后自动读到新配置。
func NewManager(getConfig func() config.Config, store KeyHealthStore, blacklist BlacklistFunc, recordFailure RecordFailureFunc, opts Options) *Manager {
	scanInterval := opts.ScanInterval
	if scanInterval <= 0 {
		scanInterval = defaultScanInterval
	}
	stopTimeout := opts.StopTimeout
	if stopTimeout <= 0 {
		stopTimeout = defaultStopTimeout
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Manager{
		getConfig:             getConfig,
		store:                 store,
		blacklist:             blacklist,
		recordFailure:         recordFailure,
		fetchers:              make(map[string]L1Fetcher),
		scanInterval:          scanInterval,
		stopTimeout:           stopTimeout,
		now:                   now,
		tasks:                 make(chan checkTask, taskQueueSize),
		inFlight:              make(map[string]struct{}),
		capabilityProbeLedger: config.NewCapabilityProbeLedger(),
		l2Reuse:               make(map[string]string),
	}
}

// RegisterL1Fetcher 注册指定渠道类型的 L1 拉取器（须在 Start 前完成注册）
func (m *Manager) RegisterL1Fetcher(channelType string, fetcher L1Fetcher) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fetchers[channelType] = fetcher
}

// SetModelCircuitLookup 注入按渠道类型查询模型级熔断追踪器的函数。
// nil 表示不读取内存熔断状态，选择器仍使用持久化 key_health 信号。
func (m *Manager) SetModelCircuitLookup(lookup func(channelType string) *metrics.ModelCircuitTracker) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modelCircuitLookup = lookup
}

// SetProbeUsageResolver 注入火山套餐用量查询器（可选）。
// 注入后稀疏 L2 预算会按剩余 AFP 的一定比例进行 clamp，避免探测蚕食生产额度。
func (m *Manager) SetProbeUsageResolver(resolver ProbeUsageResolver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usageResolver = resolver
}

// Start 启动调度循环与 worker 池（幂等）
func (m *Manager) Start() {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	m.running = true
	m.stopCh = make(chan struct{})
	m.mu.Unlock()

	workers := defaultWorkers
	if g := m.getConfig().HealthCheck; g != nil && g.MaxConcurrency > 0 {
		workers = g.MaxConcurrency
	}

	m.wg.Add(1 + workers)
	go m.loop()
	for i := 0; i < workers; i++ {
		go m.worker()
	}
	log.Printf("[HealthCheck] 渠道保活验证已启动 (扫描间隔: %s, worker: %d)", m.scanInterval, workers)
}

// Stop 停止调度循环并等待 worker 池排空（带超时，幂等）
func (m *Manager) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	m.running = false
	close(m.stopCh)
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(m.stopTimeout):
		log.Printf("[HealthCheck] 警告: 等待 worker 池排空超时 (%s)", m.stopTimeout)
	}
}

// TriggerChannelCheck 异步触发指定渠道立即验证（管理 API 用）。渠道不存在或已在队列中时返回 false。
func (m *Manager) TriggerChannelCheck(channelType string, channelIndex int) bool {
	cfg := m.getConfig()
	upstreams := UpstreamsFor(&cfg, channelType)
	if channelIndex < 0 || channelIndex >= len(upstreams) {
		return false
	}
	return m.submit(checkTask{channelType: channelType, channelIndex: channelIndex})
}

// loop 调度扫描循环（模式同 metrics.SQLiteStore.flushLoop）
func (m *Manager) loop() {
	defer m.wg.Done()
	ticker := time.NewTicker(m.scanInterval)
	defer ticker.Stop()

	// 启动即扫一次：从未验证过的渠道首轮立即到期
	m.scan()
	for {
		select {
		case <-ticker.C:
			m.scan()
		case <-m.stopCh:
			return
		}
	}
}

// worker 从任务队列取任务执行；停止信号优先，当前任务完成后退出
func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.stopCh:
			return
		default:
		}
		select {
		case <-m.stopCh:
			return
		case t := <-m.tasks:
			m.runTask(t)
		}
	}
}

// scan 扫描六类渠道的到期渠道并提交 worker 池。
// 每次 scan 开始处重置 CapabilityProbeLedger，标记新一轮探测周期；同一 CapabilityUID
// 在本周期内由首个 key 探测后，其余 key（含跨账号）复用结论，但 auth/熔断/配额仍按 key 隔离。
func (m *Manager) scan() {
	cfg := m.getConfig()
	now := m.now()

	// 新扫描周期：重置能力探测台账与 L2 成功复用缓存。
	m.resetProbeCycle()

	records, err := m.store.GetAllKeyHealth()
	if err != nil {
		log.Printf("[HealthCheck] 警告: 读取 key_health 失败，本轮按全量到期处理: %v", err)
		records = nil
	}
	l1ByChannel := groupL1Records(records)

	for _, channelType := range ChannelTypes {
		upstreams := UpstreamsFor(&cfg, channelType)
		for idx := range upstreams {
			u := &upstreams[idx]
			if channelStatus(u) != "active" {
				continue
			}
			policy := cfg.ResolveHealthCheckPolicy(u)
			if !policy.Enabled {
				continue
			}
			keys := eligibleKeys(u, now)
			if len(keys) == 0 {
				continue
			}
			channelID := strconv.Itoa(idx)
			if !channelDue(channelType, channelID, keys, l1ByChannel[channelKey(channelType, channelID)], policy.Interval, now) {
				continue
			}
			m.submit(checkTask{channelType: channelType, channelIndex: idx})
		}
	}
}

// resetProbeCycle 开启新一轮探测周期：清空能力探测台账与 L2 成功复用缓存。
// 由 scan 每次调度时调用；测试可用它模拟周期边界。
func (m *Manager) resetProbeCycle() {
	if m.capabilityProbeLedger != nil {
		m.capabilityProbeLedger.Reset()
	}
	m.l2ReuseMu.Lock()
	m.l2Reuse = make(map[string]string)
	m.l2ReuseMu.Unlock()
}

// submit 提交任务（按渠道去重、队列满时丢弃，下轮扫描重试）
func (m *Manager) submit(t checkTask) bool {
	key := channelKey(t.channelType, strconv.Itoa(t.channelIndex))
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return false
	}
	if _, dup := m.inFlight[key]; dup {
		m.mu.Unlock()
		return false
	}
	m.inFlight[key] = struct{}{}
	m.mu.Unlock()

	select {
	case m.tasks <- t:
		return true
	default:
		m.mu.Lock()
		delete(m.inFlight, key)
		m.mu.Unlock()
		log.Printf("[HealthCheck] 警告: 任务队列已满，跳过 %s", key)
		return false
	}
}

// runTask 执行单个验证任务并解除去重标记
func (m *Manager) runTask(t checkTask) {
	key := channelKey(t.channelType, strconv.Itoa(t.channelIndex))
	defer func() {
		m.mu.Lock()
		delete(m.inFlight, key)
		m.mu.Unlock()
	}()
	m.checkChannel(t.channelType, t.channelIndex)
}

// checkChannel 单渠道全 key 验证（渠道内 key 串行，避免对同一上游并发打）。
// 每个 key 先 L1；policy.VerifyRealCall 且渠道类型支持 L2 时，对 L1 成功的 key 紧接着串行做 L2。
func (m *Manager) checkChannel(channelType string, channelIndex int) {
	m.mu.Lock()
	fetcher := m.fetchers[channelType]
	m.mu.Unlock()
	if fetcher == nil {
		log.Printf("[HealthCheck] 警告: 渠道类型 %s 未注册 L1 fetcher，跳过", channelType)
		return
	}

	cfg := m.getConfig()
	upstreams := UpstreamsFor(&cfg, channelType)
	if channelIndex < 0 || channelIndex >= len(upstreams) {
		return
	}
	u := &upstreams[channelIndex]
	if channelStatus(u) != "active" {
		return
	}
	policy := cfg.ResolveHealthCheckPolicy(u)
	if !policy.Enabled {
		return
	}
	now := m.now()
	keys := eligibleKeys(u, now)
	if len(keys) == 0 {
		return
	}

	channelID := strconv.Itoa(channelIndex)
	// 上次验证记录（用于 consecutive_failures 递增/清零），按 check_kind 分开
	prevL1 := make(map[string]metrics.KeyHealthRecord)
	prevL2 := make(map[string]metrics.KeyHealthRecord)
	prevL2ByModel := make(map[string]metrics.KeyHealthRecord)
	if recs, err := m.store.GetKeyHealthForChannel(channelType, channelID); err == nil {
		for _, r := range recs {
			switch r.CheckKind {
			case CheckKindL1:
				prevL1[r.KeyMask] = r
			case CheckKindL2:
				prevL2[r.KeyMask] = r
			default:
				if model, ok := parseL2ModelCheckKind(r.CheckKind); ok {
					prevL2ByModel[model] = r
				}
			}
		}
	}

	runL2 := policy.VerifyRealCall && supportsL2(channelType)
	for _, apiKey := range keys {
		// per-key BaseURL 解析：已绑定端点的 Key 只在自己的端点上探测，
		// 不参与渠道级 BaseURL 笛卡尔积（避免混合套餐把 Agent Plan Key 误打到 Coding Plan 入口）。
		keyBaseURLs := u.BaseURLsForKey(apiKey)
		outcome := m.checkKeyL1(channelType, channelIndex, channelID, u, keyBaseURLs, apiKey, policy, prevL1, fetcher)
		if !runL2 || !outcome.ok {
			continue
		}
		if !outcome.realCallVerified {
			m.checkKeyL2(channelType, channelIndex, channelID, u, apiKey, keyBaseURLs, outcome.models, policy, prevL2)
			continue
		}
		// 火山套餐 L1 已完成一次最便宜模型的真实调用；其余模型按预算做稀疏探测。
		m.checkKeyL2Sparse(channelType, channelIndex, channelID, u, apiKey, keyBaseURLs, outcome.models, policy, prevL2ByModel)
	}
}
