// Package balanceprobe 上游余额探测调度器：周期/手动触发渠道 key 的上游余额查询，
// 结果按 check_kind="balance" 落 key_health 表（渠道内明细），
// 并聚合写入 quota.Manager（provider_api 级，参与调度评分）。
//
// 与 healthcheck 的分工：保活验证探测「渠道是否活着」；本包探测「key 还剩多少钱」。
// 探测为只读 GET，不做任何拉黑；上游 401/403 仅记录 error 状态。
package balanceprobe

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/quota"
	"github.com/BenedictKing/ccx/internal/upstreamprobe"
	"github.com/BenedictKing/ccx/internal/utils"
)

// CheckKindBalance key_health 表中的余额探测记录类型。
const CheckKindBalance = "balance"

const (
	defaultScanInterval = time.Minute
	defaultStopTimeout  = 10 * time.Second
	defaultWorkers      = 2
	taskQueueSize       = 64
)

// KeyHealthStore 余额探测结果持久化的最小接口（*metrics.SQLiteStore 已实现）。
type KeyHealthStore interface {
	UpsertKeyHealth(rec metrics.KeyHealthRecord) error
	GetKeyHealthForChannel(channelType, channelID string) ([]metrics.KeyHealthRecord, error)
	GetAllKeyHealth() ([]metrics.KeyHealthRecord, error)
}

// QuotaSink 配额真相写入/读取接口（*quota.Manager 已实现）。
type QuotaSink interface {
	UpdateChannelProviderAPI(channelUID, accountUID string, values []quota.Value, fetchErr error)
	GetChannelState(channelUID string) *quota.ChannelState
}

// Options Manager 可选参数（零值使用默认值；测试可注入时钟与间隔）。
type Options struct {
	ScanInterval time.Duration
	StopTimeout  time.Duration
	Now          func() time.Time
}

// Manager 上游余额探测调度器。
type Manager struct {
	getConfig func() config.Config
	store     KeyHealthStore
	quota     QuotaSink

	scanInterval time.Duration
	stopTimeout  time.Duration
	now          func() time.Time

	mu       sync.Mutex
	running  bool
	stopCh   chan struct{}
	tasks    chan checkTask
	wg       sync.WaitGroup
	inFlight map[string]struct{}

	// providerMemo 记录 auto 模式下各渠道上次识别成功的 provider（channelUID → provider），
	// 下一轮优先复用，避免每轮对上游重复发全序列探测请求。进程内存态，重启后首轮全序列重建。
	memoMu       sync.Mutex
	providerMemo map[string]string
}

// checkTask 探测任务：单渠道全 key 余额探测（渠道内 key 串行）。
type checkTask struct {
	channelType  string
	channelIndex int
}

// NewManager 创建余额探测调度器。getConfig 每次扫描时调用，热重载后自动读到新配置。
func NewManager(getConfig func() config.Config, store KeyHealthStore, quotaSink QuotaSink, opts Options) *Manager {
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
		getConfig:    getConfig,
		store:        store,
		quota:        quotaSink,
		scanInterval: scanInterval,
		stopTimeout:  stopTimeout,
		now:          now,
		tasks:        make(chan checkTask, taskQueueSize),
		inFlight:     make(map[string]struct{}),
		providerMemo: make(map[string]string),
	}
}

// Start 启动调度循环与 worker 池（幂等）。
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
	if g := m.getConfig().BalanceCheck; g != nil && g.MaxConcurrency > 0 {
		workers = g.MaxConcurrency
	}

	m.wg.Add(1 + workers)
	go m.loop()
	for i := 0; i < workers; i++ {
		go m.worker()
	}
	log.Printf("[BalanceProbe] 上游余额探测已启动 (扫描间隔: %s, worker: %d)", m.scanInterval, workers)
}

// Stop 停止调度循环并等待 worker 池排空（带超时，幂等）。
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
		log.Printf("[BalanceProbe] 警告: 等待 worker 池排空超时 (%s)", m.stopTimeout)
	}
}

// TriggerChannelBalanceCheck 异步触发指定渠道立即探测（管理 API 用）。
// 渠道不存在或已在队列中时返回 false；已禁用余额探测的渠道也接受手动触发（终审语义）。
func (m *Manager) TriggerChannelBalanceCheck(channelType string, channelIndex int) bool {
	cfg := m.getConfig()
	upstreams := upstreamsFor(&cfg, channelType)
	if channelIndex < 0 || channelIndex >= len(upstreams) {
		return false
	}
	return m.submit(checkTask{channelType: channelType, channelIndex: channelIndex})
}

func (m *Manager) loop() {
	defer m.wg.Done()
	ticker := time.NewTicker(m.scanInterval)
	defer ticker.Stop()

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

// channelDueKey 渠道到期判定的去重键。
func channelDueKey(channelType, channelID string) string {
	return channelType + "/" + channelID
}

// scan 扫描六类渠道，提交启用了余额探测且探测已到期的渠道。
// 到期判定取渠道下所有 balance 记录中最新的 last_check_at（渠道粒度；
// key 增删后下一轮仍会整渠道重探，保证新 key 不等满一轮间隔）。
func (m *Manager) scan() {
	cfg := m.getConfig()
	now := m.now()

	records, err := m.store.GetAllKeyHealth()
	if err != nil {
		log.Printf("[BalanceProbe] 警告: 读取 key_health 失败，本轮按全量到期处理: %v", err)
		records = nil
	}
	lastBalanceAt := make(map[string]time.Time)
	for _, r := range records {
		if r.CheckKind != CheckKindBalance {
			continue
		}
		k := channelDueKey(r.ChannelType, r.ChannelID)
		if r.LastCheckAt.After(lastBalanceAt[k]) {
			lastBalanceAt[k] = r.LastCheckAt
		}
	}

	for _, channelType := range channelTypes {
		upstreams := upstreamsFor(&cfg, channelType)
		for idx := range upstreams {
			u := &upstreams[idx]
			if channelStatus(u) != "active" {
				continue
			}
			policy := cfg.ResolveBalancePolicy(u)
			if !policy.Enabled {
				continue
			}
			if eligibleBalanceKeys(u, now) == 0 {
				continue
			}
			channelID := stableChannelID(u, idx)
			if lastAt, ok := lastBalanceAt[channelDueKey(channelType, channelID)]; ok && now.Sub(lastAt) < policy.Interval {
				continue
			}
			m.submit(checkTask{channelType: channelType, channelIndex: idx})
		}
	}
}

// channelTypes 正式支持的六类渠道（与 healthcheck.ChannelTypes 一致）。
var channelTypes = []string{"messages", "chat", "responses", "gemini", "images", "vectors"}

func upstreamsFor(cfg *config.Config, channelType string) []config.UpstreamConfig {
	switch channelType {
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
	}
	return nil
}

// stableChannelID 与 healthcheck 同口径：优先 ChannelUID，历史渠道回退数组下标。
func stableChannelID(u *config.UpstreamConfig, channelIndex int) string {
	if u != nil {
		if uid := strings.TrimSpace(u.ChannelUID); uid != "" {
			return uid
		}
	}
	return strconv.Itoa(channelIndex)
}

func channelStatus(u *config.UpstreamConfig) string {
	if u == nil || u.Status == "" {
		return "active"
	}
	return u.Status
}

// eligibleBalanceKeys 统计可参与余额探测的 key 数（跳过禁用期内与显式停用的 key）。
func eligibleBalanceKeys(u *config.UpstreamConfig, now time.Time) int {
	disabledByConfig := make(map[string]bool, len(u.APIKeyConfigs))
	for _, kc := range u.APIKeyConfigs {
		if kc.Enabled != nil && !*kc.Enabled {
			disabledByConfig[kc.Key] = true
		}
	}
	count := 0
	for _, key := range u.APIKeys {
		key = strings.TrimSpace(key)
		if key == "" || disabledByConfig[key] || u.IsKeyDisabledNow(key, now) {
			continue
		}
		count++
	}
	return count
}

// eligibleBalanceKeyList 返回可探测 key 列表（与 eligibleBalanceKeys 同过滤口径）。
func eligibleBalanceKeyList(u *config.UpstreamConfig, now time.Time) []string {
	disabledByConfig := make(map[string]bool, len(u.APIKeyConfigs))
	for _, kc := range u.APIKeyConfigs {
		if kc.Enabled != nil && !*kc.Enabled {
			disabledByConfig[kc.Key] = true
		}
	}
	out := make([]string, 0, len(u.APIKeys))
	for _, key := range u.APIKeys {
		key = strings.TrimSpace(key)
		if key == "" || disabledByConfig[key] || u.IsKeyDisabledNow(key, now) {
			continue
		}
		out = append(out, key)
	}
	return out
}

func (m *Manager) submit(t checkTask) bool {
	key := channelDueKey(t.channelType, strconv.Itoa(t.channelIndex))
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
		log.Printf("[BalanceProbe] 警告: 任务队列已满，跳过 %s", key)
		return false
	}
}

func (m *Manager) runTask(t checkTask) {
	key := channelDueKey(t.channelType, strconv.Itoa(t.channelIndex))
	defer func() {
		m.mu.Lock()
		delete(m.inFlight, key)
		m.mu.Unlock()
	}()
	m.probeChannel(t.channelType, t.channelIndex)
}

// keyBalanceDetail key_health.detail 的结构化载荷（管理端读取时解析回显）。
type keyBalanceDetail struct {
	Provider     string   `json:"provider,omitempty"`
	Scope        string   `json:"scope,omitempty"`
	Remaining    *float64 `json:"remaining,omitempty"`
	Used         *float64 `json:"used,omitempty"`
	Limit        *float64 `json:"limit,omitempty"`
	OriginalUnit string   `json:"originalUnit,omitempty"`
	// USD 是 credits 单位换算后的参考值（QuotaPerUnit）；USD 单位时与 Remaining 同值。
	USD         *float64                          `json:"usd,omitempty"`
	Unlimited   bool                              `json:"unlimited,omitempty"`
	RateWindows []upstreamprobe.BalanceRateWindow `json:"rateWindows,omitempty"`
	Error       string                            `json:"error,omitempty"`
}

// probeChannel 单渠道全 key 余额探测（渠道内 key 串行），聚合写 quota。
func (m *Manager) probeChannel(channelType string, channelIndex int) {
	cfg := m.getConfig()
	upstreams := upstreamsFor(&cfg, channelType)
	if channelIndex < 0 || channelIndex >= len(upstreams) {
		return
	}
	u := &upstreams[channelIndex]
	policy := cfg.ResolveBalancePolicy(u)
	now := m.now()
	keys := eligibleBalanceKeyList(u, now)
	if len(keys) == 0 {
		return
	}
	channelID := stableChannelID(u, channelIndex)
	channelUID := u.ChannelUID

	// 上次记录（consecutive_failures 递增/清零）
	prevByMask := make(map[string]metrics.KeyHealthRecord)
	if recs, err := m.store.GetKeyHealthForChannel(channelType, channelID); err == nil {
		for _, r := range recs {
			if r.CheckKind == CheckKindBalance {
				prevByMask[r.KeyMask] = r
			}
		}
	}

	memoKey := channelUID
	if memoKey == "" {
		memoKey = channelID
	}
	preferred := m.rememberedProvider(memoKey)

	var agg balanceAggregate
	var firstErr error
	for _, apiKey := range keys {
		keyMask := utils.MaskAPIKey(apiKey)
		probeReq := upstreamprobe.BalanceProbeRequest{
			BaseURL:            u.BaseURL,
			APIKey:             apiKey,
			CustomHeaders:      u.CustomHeaders,
			ProxyURL:           u.ProxyURL,
			ProxyPreferDirect:  u.ProxyPreferDirect,
			InsecureSkipVerify: u.InsecureSkipVerify,
			Timeout:            policy.Timeout,
		}
		res := upstreamprobe.ProbeBalance(context.Background(), probeReq, policy.Provider, preferred)
		if res.Identified && res.Provider != config.BalanceProviderAuto {
			m.rememberProvider(memoKey, res.Provider)
		}
		m.recordKeyResult(channelType, channelID, keyMask, prevByMask[keyMask], res, policy.QuotaPerUnit, now)
		agg.add(res, policy.QuotaPerUnit)
		if res.Err != nil && firstErr == nil {
			firstErr = res.Err
		}
	}

	// 全部 key 失败才向上层报错；有任一有效读数时数据本身可用。
	var fetchErr error
	if agg.errCount == len(keys) && firstErr != nil {
		fetchErr = firstErr
	}
	if m.quota != nil && channelUID != "" {
		m.quota.UpdateChannelProviderAPI(channelUID, "probe:"+channelUID, agg.values(), fetchErr)
	}
}

// rememberedProvider 读取 auto 模式记忆的上次成功 provider（空串表示无记忆）。
func (m *Manager) rememberedProvider(channelUID string) string {
	m.memoMu.Lock()
	defer m.memoMu.Unlock()
	return m.providerMemo[channelUID]
}

func (m *Manager) rememberProvider(channelUID, provider string) {
	m.memoMu.Lock()
	defer m.memoMu.Unlock()
	m.providerMemo[channelUID] = provider
}

// recordKeyResult 单 key 结果落 key_health 表。
func (m *Manager) recordKeyResult(channelType, channelID, keyMask string, prev metrics.KeyHealthRecord, res upstreamprobe.BalanceProbeResult, quotaPerUnit float64, now time.Time) {
	detail := keyBalanceDetail{
		Provider:     res.Provider,
		Scope:        res.Scope,
		Remaining:    res.Remaining,
		Used:         res.Used,
		Limit:        res.Limit,
		OriginalUnit: res.OriginalUnit,
		Unlimited:    res.Unlimited,
		RateWindows:  res.RateWindows,
	}
	switch {
	case res.Err != nil:
		detail.Error = truncateDetail(res.Err.Error())
	case res.OriginalUnit == "credits" && res.Remaining != nil:
		usd := *res.Remaining / quotaPerUnit
		detail.USD = &usd
	case res.OriginalUnit == "USD" && res.Remaining != nil:
		detail.USD = res.Remaining
	}
	detailJSON, _ := json.Marshal(detail)

	status := "ok"
	var failures int64
	if res.Err != nil {
		status = "error"
		failures = prev.ConsecutiveFailures + 1
	}

	rec := metrics.KeyHealthRecord{
		ChannelType:         channelType,
		ChannelID:           channelID,
		KeyMask:             keyMask,
		CheckKind:           CheckKindBalance,
		LastCheckAt:         now,
		LastStatus:          status,
		ConsecutiveFailures: failures,
		LatencyMs:           0,
		Detail:              string(detailJSON),
	}
	if err := m.store.UpsertKeyHealth(rec); err != nil {
		log.Printf("[BalanceProbe] 警告: 写入 key_health 失败 (%s/%s/%s): %v", channelType, channelID, keyMask, err)
	}
}

func truncateDetail(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

// ── 渠道级聚合 ──

// balanceAggregate 多 key 探测结果的求和聚合。
// 单 key 耗尽不误杀整渠道：Σremaining 反映渠道总余额，耗尽判定只在全部 key 耗尽时成立。
type balanceAggregate struct {
	unlimitedCount int
	valuedCount    int
	errCount       int

	creditsLimit, creditsUsed, creditsRemain    float64
	currencyLimit, currencyUsed, currencyRemain float64
}

func (a *balanceAggregate) add(res upstreamprobe.BalanceProbeResult, quotaPerUnit float64) {
	if res.Err != nil || !res.Identified {
		a.errCount++
		return
	}
	if res.Unlimited {
		a.unlimitedCount++
		return
	}
	if res.Remaining == nil {
		// 识别成功但无数值（如 sub2api 订阅模式缺 remaining）：计入有效识别，不进数值聚合。
		a.valuedCount++
		return
	}
	a.valuedCount++
	if res.OriginalUnit == "credits" {
		if res.Limit != nil {
			a.creditsLimit += *res.Limit
		}
		if res.Used != nil {
			a.creditsUsed += *res.Used
		}
		a.creditsRemain += *res.Remaining
		a.currencyRemain += *res.Remaining / quotaPerUnit
		if res.Limit != nil {
			a.currencyLimit += *res.Limit / quotaPerUnit
		}
		if res.Used != nil {
			a.currencyUsed += *res.Used / quotaPerUnit
		}
		return
	}
	// USD 等货币单位直接聚合
	if res.Limit != nil {
		a.currencyLimit += *res.Limit
	}
	if res.Used != nil {
		a.currencyUsed += *res.Used
	}
	a.currencyRemain += *res.Remaining
}

// values 生成写入 quota.Manager 的维度值。全 unlimited 时不写数值维度（中性分 fail-open），
// 但仍返回空切片让调用方刷新 FetchedAtMs/Supported 标记。
func (a *balanceAggregate) values() []quota.Value {
	if a.valuedCount == 0 {
		return nil
	}
	values := make([]quota.Value, 0, 2)
	values = append(values, quota.Value{
		Dimension: quota.DimCurrency,
		Limit:     floatPtr(a.currencyLimit),
		Used:      floatPtr(a.currencyUsed),
		Remaining: floatPtr(a.currencyRemain),
		Unit:      "USD",
	})
	if a.creditsRemain != 0 || a.creditsLimit != 0 || a.creditsUsed != 0 {
		values = append(values, quota.Value{
			Dimension: quota.DimCredits,
			Limit:     floatPtr(a.creditsLimit),
			Used:      floatPtr(a.creditsUsed),
			Remaining: floatPtr(a.creditsRemain),
			Unit:      "credits",
		})
	}
	return values
}

func floatPtr(v float64) *float64 { return &v }
