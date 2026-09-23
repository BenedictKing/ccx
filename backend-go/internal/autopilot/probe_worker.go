package autopilot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/BenedictKing/ccx/internal/errutil"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ── 探测优先级 ──

// ProbePriority 探测队列优先级。数值越小优先级越高。
type ProbePriority int

const (
	ProbePriorityDead     ProbePriority = 0 // dead 疑似，最高优先级
	ProbePriorityDegraded ProbePriority = 1 // degraded，次优先级
	ProbePriorityUnknown  ProbePriority = 2 // unknown，再次
	ProbePriorityLow      ProbePriority = 3 // 低优先级（reserved）
)

// probePriorityFromState 从 HealthState 推导探测优先级。
func probePriorityFromState(state HealthState) ProbePriority {
	switch state {
	case HealthStateDead:
		return ProbePriorityDead
	case HealthStateDegraded:
		return ProbePriorityDegraded
	case HealthStateUnknown:
		return ProbePriorityUnknown
	default:
		return ProbePriorityLow
	}
}

// ── 探测结果 ──

// ProbeResult L2 探测结果。
type ProbeResult struct {
	EndpointUID  string
	Success      bool
	StatusCode   int
	LatencyMs    int64
	ErrorMessage string
	ProbedAt     time.Time
}

// ── API Key 解析 ──

// APIKeyResolver 根据 channelUID + keyHash（即 profile.MetricsKey）反查真实 API Key。
// ProbeWorker 本身不持有明文 key，画像里只存 sha256 摘要（KeyHashFromAPIKey），
// 无法从画像反解出可用于鉴权的明文。返回 ok=false 表示未找到匹配
// （渠道已删除/key 已轮换），调用方应跳过本次探测，不发送带假 key 的请求。
type APIKeyResolver func(channelUID, keyHash string) (apiKey string, ok bool)

// ── 探测请求 ──

// ProbeRequest 描述一次 L2 探测的 HTTP 请求。
// 直接构造原始 HTTP 请求，不经过调度器/MetricsManager/Provider 转换链路。
type ProbeRequest struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte
}

// ── 探测队列项 ──

// probeQueueItem 探测队列中的单个条目。
type probeQueueItem struct {
	EndpointUID string
	Priority    ProbePriority
	EnqueuedAt  time.Time
}

// ── 探测队列 ──

// ProbeQueue 优先级探测队列。
// 支持去重、优先级排序、出队。
// 非线程安全，调用方（ProbeWorker）负责同步。
type ProbeQueue struct {
	items []probeQueueItem
	index map[string]int // endpointUID -> items 数组 index，用于去重
}

// NewProbeQueue 创建空的探测队列。
func NewProbeQueue() *ProbeQueue {
	return &ProbeQueue{
		items: make([]probeQueueItem, 0),
		index: make(map[string]int),
	}
}

// Enqueue 入队。如果 endpointUID 已在队列中且新优先级更高（数值更小），则提升优先级。
func (q *ProbeQueue) Enqueue(item probeQueueItem) {
	if idx, exists := q.index[item.EndpointUID]; exists {
		// 已在队列中，检查是否需要提升优先级
		if item.Priority < q.items[idx].Priority {
			q.items[idx].Priority = item.Priority
		}
		return
	}
	q.index[item.EndpointUID] = len(q.items)
	q.items = append(q.items, item)
}

// DequeueBatch 按优先级出队最多 n 个条目。
// 排序规则：优先级升序（dead > degraded > unknown），同优先级按入队时间先进先出。
func (q *ProbeQueue) DequeueBatch(n int) []probeQueueItem {
	if len(q.items) == 0 || n <= 0 {
		return nil
	}

	// 按优先级+入队时间排序
	sort.SliceStable(q.items, func(i, j int) bool {
		if q.items[i].Priority != q.items[j].Priority {
			return q.items[i].Priority < q.items[j].Priority
		}
		return q.items[i].EnqueuedAt.Before(q.items[j].EnqueuedAt)
	})

	if n > len(q.items) {
		n = len(q.items)
	}

	batch := make([]probeQueueItem, n)
	copy(batch, q.items[:n])

	// 移除已出队的条目
	remaining := q.items[n:]
	q.items = make([]probeQueueItem, len(remaining))
	copy(q.items, remaining)

	// 重建 index
	q.index = make(map[string]int, len(q.items))
	for i, item := range q.items {
		q.index[item.EndpointUID] = i
	}

	return batch
}

// Len 返回队列长度。
func (q *ProbeQueue) Len() int {
	return len(q.items)
}

// Contains 检查 endpointUID 是否已在队列中。
func (q *ProbeQueue) Contains(endpointUID string) bool {
	_, exists := q.index[endpointUID]
	return exists
}

// Remove 从队列中移除指定 endpointUID。
func (q *ProbeQueue) Remove(endpointUID string) {
	idx, exists := q.index[endpointUID]
	if !exists {
		return
	}
	delete(q.index, endpointUID)

	// 用最后一个元素填充空位
	lastIdx := len(q.items) - 1
	if idx != lastIdx {
		q.items[idx] = q.items[lastIdx]
		q.index[q.items[idx].EndpointUID] = idx
	}
	q.items = q.items[:lastIdx]
}

// ── 每日预算 ──

// ProbeBudget 全局每日探测预算。
// 线程安全，使用 atomic 操作。
type ProbeBudget struct {
	dailyLimit int32
	used       atomic.Int32
	resetDay   atomic.Int32 // 当前预算所属日期的 unix day（UTC）
	timeFunc   func() time.Time
	mu         sync.Mutex // 仅在 reset 时使用
}

// NewProbeBudget 创建每日预算。
func NewProbeBudget(dailyLimit int) *ProbeBudget {
	if dailyLimit <= 0 {
		dailyLimit = DefaultProbeDailyBudget
	}
	b := &ProbeBudget{
		dailyLimit: int32(dailyLimit),
		timeFunc:   time.Now,
	}
	now := b.timeFunc()
	b.resetDay.Store(int32(now.UTC().Unix() / 86400))
	return b
}

// NewProbeBudgetWithTime 创建带自定义时钟的每日预算（测试用）。
func NewProbeBudgetWithTime(dailyLimit int, timeFunc func() time.Time) *ProbeBudget {
	b := &ProbeBudget{
		dailyLimit: int32(dailyLimit),
		timeFunc:   timeFunc,
	}
	now := b.timeFunc()
	b.resetDay.Store(int32(now.UTC().Unix() / 86400))
	return b
}

// dayKey 返回当前 UTC 日期的 unix day。
func (b *ProbeBudget) dayKey() int32 {
	return int32(b.timeFunc().UTC().Unix() / 86400)
}

// maybeReset 如果跨天则重置计数器。
func (b *ProbeBudget) maybeReset() {
	today := b.dayKey()
	if b.resetDay.Load() != today {
		b.mu.Lock()
		defer b.mu.Unlock()
		// double-check
		if b.resetDay.Load() != today {
			b.used.Store(0)
			b.resetDay.Store(today)
			log.Printf("[ProbeBudget-Reset] 每日探测预算已重置 (limit=%d)", b.dailyLimit)
		}
	}
}

// TryConsume 尝试消耗一次探测额度。
// 成功返回 true，预算耗尽返回 false。
func (b *ProbeBudget) TryConsume() bool {
	return b.TryConsumeN(1)
}

// TryConsumeN 原子预留 n 次探测额度。
// n<=0 视为无需消耗；剩余额度不足时不做部分扣减。
func (b *ProbeBudget) TryConsumeN(n int) bool {
	if n <= 0 {
		return true
	}
	b.maybeReset()
	for {
		cur := b.used.Load()
		if int64(cur)+int64(n) > int64(b.dailyLimit) {
			return false
		}
		if b.used.CompareAndSwap(cur, cur+int32(n)) {
			return true
		}
	}
}

// Limit 返回每日探测上限。
func (b *ProbeBudget) Limit() int {
	return int(b.dailyLimit)
}

// Remaining 返回今日剩余探测额度。
func (b *ProbeBudget) Remaining() int {
	b.maybeReset()
	return int(b.dailyLimit) - int(b.used.Load())
}

// Used 返回今日已用探测额度。
func (b *ProbeBudget) Used() int {
	b.maybeReset()
	return int(b.used.Load())
}

// ── 默认配置 ──

const (
	DefaultProbeScanInterval      = 10 * time.Minute // 探测循环间隔
	DefaultProbeBatchSize         = 5                // 每批最大探测数
	DefaultProbeTimeout           = 5 * time.Second  // 单次探测超时
	DefaultProbeDailyBudget       = 200              // 每日探测上限
	DefaultProbeCooldown          = 6 * time.Hour    // 每 endpoint 冷却期
	DefaultProbeRecoveryThreshold = 2                // degraded/limited→healthy 所需连续探测成功次数
)

// ProbeWorkerConfig ProbeWorker 可调参数。
type ProbeWorkerConfig struct {
	ScanInterval           time.Duration    // 探测循环间隔，默认 10 分钟
	BatchSize              int              // 每批最大探测数，默认 5
	ProbeTimeout           time.Duration    // 单次探测超时，默认 5s
	DailyBudget            int              // 每日探测上限，默认 200
	Cooldown               time.Duration    // 每 endpoint 探测冷却期，默认 6h
	ProbeRecoveryThreshold int              // degraded/limited→healthy 所需连续探测成功次数，默认 2
	QuietLogs              bool             // 是否静默日志
	TimeFunc               func() time.Time // 自定义时钟（测试用）
}

func (c ProbeWorkerConfig) withDefaults() ProbeWorkerConfig {
	if c.ScanInterval <= 0 {
		c.ScanInterval = DefaultProbeScanInterval
	}
	if c.BatchSize <= 0 {
		c.BatchSize = DefaultProbeBatchSize
	}
	if c.ProbeTimeout <= 0 {
		c.ProbeTimeout = DefaultProbeTimeout
	}
	if c.DailyBudget <= 0 {
		c.DailyBudget = DefaultProbeDailyBudget
	}
	if c.Cooldown <= 0 {
		c.Cooldown = DefaultProbeCooldown
	}
	if c.ProbeRecoveryThreshold <= 0 {
		c.ProbeRecoveryThreshold = DefaultProbeRecoveryThreshold
	}
	if c.TimeFunc == nil {
		c.TimeFunc = time.Now
	}
	return c
}

// ── ProbeWorker ──

// ProbeWorker L2 轻量探测 worker。
// 定期扫描 ProfileStore 中 unknown/degraded/limited/dead 的 endpoint，
// 将需要探测的 endpoint 入队，按优先级出队执行轻量 HTTP 探测。
// 探测请求直接通过 http.Client 发起，不经过 MetricsManager/调度器/Provider 转换链路。
type ProbeWorker struct {
	store    *ProfileStore
	config   ProbeWorkerConfig
	budget   *ProbeBudget
	queue    *ProbeQueue
	client   *http.Client
	timeFn   func() time.Time
	resolver APIKeyResolver // 可选：由 main.go 装配注入，未设置时探测请求全部跳过（fail-open）

	cancel func()
	wg     sync.WaitGroup
}

// NewProbeWorker 创建 ProbeWorker。
func NewProbeWorker(store *ProfileStore, cfg ProbeWorkerConfig) *ProbeWorker {
	cfg = cfg.withDefaults()
	return &ProbeWorker{
		store:  store,
		config: cfg,
		budget: NewProbeBudgetWithTime(cfg.DailyBudget, cfg.TimeFunc),
		queue:  NewProbeQueue(),
		client: &http.Client{Timeout: cfg.ProbeTimeout},
		timeFn: cfg.TimeFunc,
	}
}

// SetAPIKeyResolver 注入 API Key 解析回调。未注入时所有探测请求会被跳过（不消耗预算）。
func (w *ProbeWorker) SetAPIKeyResolver(resolver APIKeyResolver) {
	w.resolver = resolver
}

// Start 启动探测 worker 后台循环。
func (w *ProbeWorker) Start(ctx context.Context) {
	ctx, w.cancel = context.WithCancel(ctx)
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.run(ctx)
	}()
	if !w.config.QuietLogs {
		log.Printf("[ProbeWorker-Start] L2 探测 worker 已启动 (interval=%s, batch=%d, budget=%d/day, cooldown=%s)",
			w.config.ScanInterval, w.config.BatchSize, w.config.DailyBudget, w.config.Cooldown)
	}
}

// Stop 优雅停止探测 worker。
func (w *ProbeWorker) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
	if !w.config.QuietLogs {
		log.Printf("[ProbeWorker-Stop] L2 探测 worker 已停止 (今日已用=%d, 剩余=%d)",
			w.budget.Used(), w.budget.Remaining())
	}
}

// run 主循环。
func (w *ProbeWorker) run(ctx context.Context) {
	// 启动时立即扫描一次
	w.scanAndEnqueue()

	ticker := time.NewTicker(w.config.ScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.scanAndEnqueue()
			w.processQueue()
		}
	}
}

// scanAndEnqueue 扫描 ProfileStore，将需要探测的 endpoint 入队。
func (w *ProbeWorker) scanAndEnqueue() {
	profiles := w.store.ListActive()
	now := w.timeFn()
	var enqueued int

	for _, p := range profiles {
		// 只探测 unknown / degraded / limited / dead
		if !isProbeEligible(p.HealthState) {
			continue
		}

		// 冷却期检查
		if p.LastProbeAt != nil && now.Sub(*p.LastProbeAt) < w.config.Cooldown {
			continue
		}

		// 防止重复入队
		if w.queue.Contains(p.EndpointUID) {
			continue
		}

		w.queue.Enqueue(probeQueueItem{
			EndpointUID: p.EndpointUID,
			Priority:    probePriorityFromState(p.HealthState),
			EnqueuedAt:  now,
		})
		enqueued++
	}

	if !w.config.QuietLogs && enqueued > 0 {
		log.Printf("[ProbeWorker-Scan] 入队 %d 个 endpoint 待探测 (队列长度=%d)", enqueued, w.queue.Len())
	}
}

// processQueue 从队列出队并执行探测。
func (w *ProbeWorker) processQueue() {
	if w.queue.Len() == 0 {
		return
	}

	batch := w.queue.DequeueBatch(w.config.BatchSize)
	if len(batch) == 0 {
		return
	}

	if !w.config.QuietLogs {
		log.Printf("[ProbeWorker-Process] 出队 %d 个 endpoint 开始探测 (budget remaining=%d)",
			len(batch), w.budget.Remaining())
	}

	for i, item := range batch {
		// 从 store 获取最新画像（可能在入队后已更新）
		profile := w.store.Get(item.EndpointUID)
		if profile == nil {
			// 画像已被删除，跳过
			continue
		}

		// 二次冷却期检查（出队时再检查一次）
		now := w.timeFn()
		if profile.LastProbeAt != nil && now.Sub(*profile.LastProbeAt) < w.config.Cooldown {
			continue
		}

		// 解析真实 API Key：resolver 未注入或未命中时跳过本次探测，不消耗预算，
		// 也不会用假 key（如 KeyMask 掩码值）发送探测请求。
		apiKey, ok := w.resolveAPIKey(profile)
		if !ok {
			if !w.config.QuietLogs {
				log.Printf("[ProbeWorker-Probe] endpoint=%s 未能解析 API Key，跳过本次探测", profile.EndpointUID)
			}
			continue
		}

		// 预算检查
		if !w.budget.TryConsume() {
			log.Printf("[ProbeWorker-Budget] 每日探测预算耗尽 (limit=%d)，跳过剩余 %d 个 endpoint",
				w.config.DailyBudget, len(batch)-i)
			// 预算耗尽，将当前及后续未处理条目重新入队（明天再试）
			for _, remaining := range batch[i:] {
				w.queue.Enqueue(remaining)
			}
			return
		}

		result := w.executeProbe(profile, apiKey)
		w.applyProbeResult(result)
	}
}

// resolveAPIKey 通过注入的 APIKeyResolver 反查明文 API Key。
// resolver 未设置时始终返回 ok=false（fail-open：跳过探测而非发送假 key）。
func (w *ProbeWorker) resolveAPIKey(profile *KeyEndpointProfile) (string, bool) {
	if w.resolver == nil {
		return "", false
	}
	return w.resolver(profile.ChannelUID, profile.MetricsKey)
}

// executeProbe 对单个 endpoint 执行 L2 探测。
// 直接通过 http.Client 发起 HTTP 请求，不经过 MetricsManager/调度器。
func (w *ProbeWorker) executeProbe(profile *KeyEndpointProfile, apiKey string) ProbeResult {
	now := w.timeFn()
	result := ProbeResult{
		EndpointUID: profile.EndpointUID,
		ProbedAt:    now,
	}

	// 构造探测请求
	probeReq, err := buildProbeRequest(profile, apiKey)
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("构造探测请求失败: %v", err)
		if !w.config.QuietLogs {
			log.Printf("[ProbeWorker-Probe] endpoint=%s 构造请求失败: %v", profile.EndpointUID, err)
		}
		return result
	}

	// 构造 HTTP 请求
	httpReq, err := http.NewRequestWithContext(context.Background(), probeReq.Method, probeReq.URL, bytes.NewReader(probeReq.Body))
	if err != nil {
		result.ErrorMessage = fmt.Sprintf("创建 HTTP 请求失败: %v", err)
		return result
	}
	for k, v := range probeReq.Headers {
		httpReq.Header.Set(k, v)
	}

	// 发起探测（直接 http.Client，不经过 MetricsManager）
	start := time.Now()
	resp, err := w.client.Do(httpReq)
	latency := time.Since(start)
	result.LatencyMs = latency.Milliseconds()

	if err != nil {
		result.ErrorMessage = fmt.Sprintf("HTTP 请求失败: %v", err)
		if !w.config.QuietLogs {
			log.Printf("[ProbeWorker-Probe] endpoint=%s 探测失败: %v (latency=%dms)",
				profile.EndpointUID, err, result.LatencyMs)
		}
		return result
	}
	defer errutil.IgnoreDeferred(resp.Body.Close)
	_, _ =
		// 读取并丢弃响应体（释放连接）
		io.Copy(io.Discard, resp.Body)

	result.StatusCode = resp.StatusCode
	// 判定探测成功：2xx 或 4xx 中的认证/模型错误（说明端点存活）
	result.Success = isProbeAlive(resp.StatusCode)

	if !w.config.QuietLogs {
		log.Printf("[ProbeWorker-Probe] endpoint=%s status=%d success=%v latency=%dms",
			profile.EndpointUID, resp.StatusCode, result.Success, result.LatencyMs)
	}

	return result
}

// applyProbeResult 将探测结果回写到画像。
func (w *ProbeWorker) applyProbeResult(result ProbeResult) {
	profile := w.store.Get(result.EndpointUID)
	if profile == nil {
		return
	}

	now := result.ProbedAt
	profile.LastProbeAt = &now
	profile.ProbeSuccess = result.Success
	profile.ProbeLatencyMs = result.LatencyMs
	profile.ProbeStatusCode = result.StatusCode
	profile.UpdatedAt = now

	if result.Success {
		profile.ProbeConfidence = 0.8
		profile.ConsecutiveProbeSuccess++
		// 探测成功：如果是 dead/degraded/limited，提升健康状态
		switch profile.HealthState {
		case HealthStateDead:
			// dead 但探测成功 → 可能恢复，标记 degraded 让 L1 继续验证
			profile.HealthState = HealthStateDegraded
			profile.HealthEvidence = append(profile.HealthEvidence,
				fmt.Sprintf("[L2-Probe] 探测成功（status=%d），dead→degraded 待 L1 验证", result.StatusCode))
		case HealthStateUnknown:
			profile.HealthState = HealthStateHealthy
			profile.HealthEvidence = append(profile.HealthEvidence,
				fmt.Sprintf("[L2-Probe] 探测成功（status=%d），unknown→healthy", result.StatusCode))
		case HealthStateDegraded, HealthStateLimited:
			// degraded/limited → healthy 需要连续多次探测成功，避免单次探测噪声导致 flapping
			if profile.ConsecutiveProbeSuccess >= w.config.ProbeRecoveryThreshold {
				previous := profile.HealthState
				profile.HealthState = HealthStateHealthy
				profile.HealthEvidence = append(profile.HealthEvidence,
					fmt.Sprintf("[L2-Probe] 连续探测成功 %d 次（status=%d），%s→healthy",
						profile.ConsecutiveProbeSuccess, result.StatusCode, previous))
			} else {
				profile.HealthEvidence = append(profile.HealthEvidence,
					fmt.Sprintf("[L2-Probe] 探测成功（status=%d），连续成功 %d/%d 次，尚未达到恢复阈值",
						result.StatusCode, profile.ConsecutiveProbeSuccess, w.config.ProbeRecoveryThreshold))
			}
		}
	} else {
		profile.ProbeConfidence = 0.3
		profile.ConsecutiveProbeSuccess = 0
		profile.HealthEvidence = append(profile.HealthEvidence,
			fmt.Sprintf("[L2-Probe] 探测失败: %s (status=%d)", result.ErrorMessage, result.StatusCode))
	}

	// 限制 evidence 数量，保留最近 20 条
	if len(profile.HealthEvidence) > 20 {
		profile.HealthEvidence = profile.HealthEvidence[len(profile.HealthEvidence)-20:]
	}

	if err := w.store.Upsert(profile); err != nil {
		log.Printf("[ProbeWorker-Apply] 回写画像失败 endpoint=%s: %v", result.EndpointUID, err)
	}
}

// QueueLen 返回当前队列长度（供外部观测）。
func (w *ProbeWorker) QueueLen() int {
	return w.queue.Len()
}

// BudgetRemaining 返回今日剩余探测额度。
func (w *ProbeWorker) BudgetRemaining() int {
	return w.budget.Remaining()
}

// ── 探测请求构造 ──

// buildProbeRequest 根据 endpoint 的 serviceType 和配置构造最小探测请求。
// L2 探测目标：验证连通性和认证，不验证模型能力。
func buildProbeRequest(profile *KeyEndpointProfile, apiKey string) (*ProbeRequest, error) {
	switch profile.ServiceType {
	case "claude":
		return buildClaudeProbeRequest(profile, apiKey), nil
	case "openai":
		return buildChatProbeRequest(profile, apiKey), nil
	case "responses":
		return buildResponsesProbeRequest(profile, apiKey), nil
	case "gemini":
		return buildGeminiProbeRequest(profile, apiKey), nil
	default:
		return nil, fmt.Errorf("不支持的 serviceType: %s", profile.ServiceType)
	}
}

// buildClaudeProbeRequest 构造 Claude Messages 探测请求。
// 优先使用 count_tokens（最轻量），回退到最小 messages 请求。
func buildClaudeProbeRequest(profile *KeyEndpointProfile, apiKey string) *ProbeRequest {
	baseURL := strings.TrimRight(profile.BaseURL, "/")
	probeModel := pickProbeModel(profile, "claude-3-5-haiku-20241022")

	// 尝试 count_tokens（最轻量的探测方式）
	countTokensBody, _ := json.Marshal(map[string]interface{}{
		"model":    probeModel,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})

	headers := map[string]string{
		"Content-Type":      "application/json",
		"x-api-key":         apiKey,
		"anthropic-version": "2023-06-01",
	}

	return &ProbeRequest{
		Method:  http.MethodPost,
		URL:     baseURL + "/v1/messages/count_tokens",
		Headers: headers,
		Body:    countTokensBody,
	}
}

// buildChatProbeRequest 构造 OpenAI Chat 探测请求。
// 发送最小 chat completion 请求。
func buildChatProbeRequest(profile *KeyEndpointProfile, apiKey string) *ProbeRequest {
	baseURL := strings.TrimRight(profile.BaseURL, "/")
	probeModel := pickProbeModel(profile, "gpt-4o-mini")

	body, _ := json.Marshal(map[string]interface{}{
		"model": probeModel,
		"messages": []map[string]string{
			{"role": "user", "content": "hi"},
		},
		"max_tokens": 16,
	})

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + apiKey,
	}

	return &ProbeRequest{
		Method:  http.MethodPost,
		URL:     baseURL + "/v1/chat/completions",
		Headers: headers,
		Body:    body,
	}
}

// buildResponsesProbeRequest 构造 Codex Responses 探测请求。
func buildResponsesProbeRequest(profile *KeyEndpointProfile, apiKey string) *ProbeRequest {
	baseURL := strings.TrimRight(profile.BaseURL, "/")
	probeModel := pickProbeModel(profile, "codex-mini")

	body, _ := json.Marshal(map[string]interface{}{
		"model": probeModel,
		"input": []map[string]interface{}{
			{
				"role":    "user",
				"content": []map[string]string{{"type": "input_text", "text": "hi"}},
			},
		},
		"max_output_tokens": 16,
	})

	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + apiKey,
	}

	return &ProbeRequest{
		Method:  http.MethodPost,
		URL:     baseURL + "/v1/responses",
		Headers: headers,
		Body:    body,
	}
}

// buildGeminiProbeRequest 构造 Gemini 探测请求。
func buildGeminiProbeRequest(profile *KeyEndpointProfile, apiKey string) *ProbeRequest {
	baseURL := strings.TrimRight(profile.BaseURL, "/")
	probeModel := pickProbeModel(profile, "gemini-2.0-flash")

	body, _ := json.Marshal(map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"role":  "user",
				"parts": []map[string]string{{"text": "hi"}},
			},
		},
		"generationConfig": map[string]interface{}{
			"maxOutputTokens": 16,
		},
	})

	headers := map[string]string{
		"Content-Type":   "application/json",
		"x-goog-api-key": apiKey,
	}

	return &ProbeRequest{
		Method:  http.MethodPost,
		URL:     fmt.Sprintf("%s/v1beta/models/%s:generateContent", baseURL, probeModel),
		Headers: headers,
		Body:    body,
	}
}

// ── 辅助函数 ──

// isProbeEligible 判断 HealthState 是否应该入队探测。
func isProbeEligible(state HealthState) bool {
	switch state {
	case HealthStateUnknown, HealthStateDegraded, HealthStateLimited, HealthStateDead:
		return true
	default:
		return false
	}
}

// isProbeAlive 根据 HTTP 状态码判断 endpoint 是否存活。
// 2xx = 完全成功；4xx（非 404/405/408）= 端点存活但请求有误（认证/参数问题）。
func isProbeAlive(statusCode int) bool {
	if statusCode >= 200 && statusCode < 300 {
		return true
	}
	// 401/403 = 认证问题但端点存活
	// 429 = 限流但端点存活
	// 400/422 = 参数问题但端点存活
	if statusCode == 401 || statusCode == 403 || statusCode == 429 ||
		statusCode == 400 || statusCode == 422 {
		return true
	}
	// 404/405/408/5xx = 端点可能不可用
	return false
}

// pickProbeModel 从画像的 AvailableModels 中选择探测用模型。
// 如果画像有模型列表，选第一个；否则使用默认值。
func pickProbeModel(profile *KeyEndpointProfile, defaultModel string) string {
	if len(profile.AvailableModels) > 0 {
		return profile.AvailableModels[0]
	}
	return defaultModel
}
