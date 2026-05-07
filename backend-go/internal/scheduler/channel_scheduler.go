package scheduler

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/loadbalance"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/transitions"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/BenedictKing/ccx/internal/warmup"
)

// ChannelScheduler 多渠道调度器
type ChannelScheduler struct {
	mu                       sync.RWMutex
	configManager            *config.ConfigManager
	messagesMetricsManager   *metrics.MetricsManager // Messages 渠道指标
	responsesMetricsManager  *metrics.MetricsManager // Responses 渠道指标
	geminiMetricsManager     *metrics.MetricsManager // Gemini 渠道指标
	chatMetricsManager       *metrics.MetricsManager // Chat 渠道指标
	imagesMetricsManager     *metrics.MetricsManager // Images 渠道指标
	traceAffinity            *session.TraceAffinityManager
	urlManager               *warmup.URLManager       // URL 管理器（非阻塞，动态排序）
	messagesChannelLogStore  *metrics.ChannelLogStore // Messages 渠道请求日志
	responsesChannelLogStore *metrics.ChannelLogStore // Responses 渠道请求日志
	geminiChannelLogStore    *metrics.ChannelLogStore // Gemini 渠道请求日志
	chatChannelLogStore      *metrics.ChannelLogStore // Chat 渠道请求日志
	imagesChannelLogStore    *metrics.ChannelLogStore // Images 渠道请求日志

	// PR3 T4：每 kind 独立一套 LB（因 LBMetricsProvider 按 kind 隔离 channelID 命名空间）。
	loadBalancerByKind map[ChannelKind]*loadbalance.LoadBalancer
	lbProviderByKind   map[ChannelKind]*LBMetricsProvider
}

// ChannelKind 标识调度器所处理的渠道类型
// 注意：这里的 kind 与 upstream.ServiceType（openai/claude/gemini）不同，
// kind 对应的是本代理对外暴露的三类入口：messages / responses / gemini。
type ChannelKind string

const (
	ChannelKindMessages  ChannelKind = "messages"
	ChannelKindResponses ChannelKind = "responses"
	ChannelKindGemini    ChannelKind = "gemini"
	ChannelKindChat      ChannelKind = "chat"
	ChannelKindImages    ChannelKind = "images"
)

// NewChannelScheduler 创建多渠道调度器
func NewChannelScheduler(
	cfgManager *config.ConfigManager,
	messagesMetrics *metrics.MetricsManager,
	responsesMetrics *metrics.MetricsManager,
	geminiMetrics *metrics.MetricsManager,
	chatMetrics *metrics.MetricsManager,
	optional ...interface{},
) *ChannelScheduler {
	var imagesMetrics *metrics.MetricsManager
	var traceAffinity *session.TraceAffinityManager
	var urlMgr *warmup.URLManager

	if len(optional) > 0 {
		if m, ok := optional[0].(*metrics.MetricsManager); ok {
			imagesMetrics = m
			optional = optional[1:]
		}
	}
	if len(optional) > 0 {
		traceAffinity, _ = optional[0].(*session.TraceAffinityManager)
	}
	if len(optional) > 1 {
		urlMgr, _ = optional[1].(*warmup.URLManager)
	}
	if imagesMetrics == nil {
		imagesMetrics = metrics.NewMetricsManager()
	}

	return &ChannelScheduler{
		configManager:            cfgManager,
		messagesMetricsManager:   messagesMetrics,
		responsesMetricsManager:  responsesMetrics,
		geminiMetricsManager:     geminiMetrics,
		chatMetricsManager:       chatMetrics,
		imagesMetricsManager:     imagesMetrics,
		traceAffinity:            traceAffinity,
		urlManager:               urlMgr,
		messagesChannelLogStore:  metrics.NewChannelLogStore(),
		responsesChannelLogStore: metrics.NewChannelLogStore(),
		geminiChannelLogStore:    metrics.NewChannelLogStore(),
		chatChannelLogStore:      metrics.NewChannelLogStore(),
		imagesChannelLogStore:    metrics.NewChannelLogStore(),
		loadBalancerByKind:       buildLoadBalancersByKind(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, chatMetrics, imagesMetrics, traceAffinity),
		lbProviderByKind:         buildLBProvidersByKind(cfgManager, messagesMetrics, responsesMetrics, geminiMetrics, chatMetrics, imagesMetrics, traceAffinity),
	}
}

// buildLBProvidersByKind 为每个 ChannelKind 构造一个 LBMetricsProvider。
// 每个 kind 拥有独立的 channelID 命名空间（即 upstream slice 索引）。
func buildLBProvidersByKind(
	cm *config.ConfigManager,
	messagesMetrics, responsesMetrics, geminiMetrics, chatMetrics, imagesMetrics *metrics.MetricsManager,
	aff *session.TraceAffinityManager,
) map[ChannelKind]*LBMetricsProvider {
	return map[ChannelKind]*LBMetricsProvider{
		ChannelKindMessages:  NewLBMetricsProvider(messagesMetrics, cm, aff, ChannelKindMessages),
		ChannelKindResponses: NewLBMetricsProvider(responsesMetrics, cm, aff, ChannelKindResponses),
		ChannelKindGemini:    NewLBMetricsProvider(geminiMetrics, cm, aff, ChannelKindGemini),
		ChannelKindChat:      NewLBMetricsProvider(chatMetrics, cm, aff, ChannelKindChat),
		ChannelKindImages:    NewLBMetricsProvider(imagesMetrics, cm, aff, ChannelKindImages),
	}
}

// buildLoadBalancersByKind 为每个 kind 构造一个加载了完整 6 策略的 LoadBalancer。
// 策略组合与 PRD 第 23 行约定一致：Promotion + TraceAware + WeightRR + ErrorAware
// + LatencyAware + RateLimitAware。
func buildLoadBalancersByKind(
	cm *config.ConfigManager,
	messagesMetrics, responsesMetrics, geminiMetrics, chatMetrics, imagesMetrics *metrics.MetricsManager,
	aff *session.TraceAffinityManager,
) map[ChannelKind]*loadbalance.LoadBalancer {
	makeLB := func(mm *metrics.MetricsManager, kind ChannelKind) *loadbalance.LoadBalancer {
		provider := NewLBMetricsProvider(mm, cm, aff, kind)
		return loadbalance.New(provider,
			loadbalance.NewPromotionStrategy(provider, 0),
			loadbalance.NewTraceAwareStrategy(provider, 0),
			loadbalance.NewWeightRoundRobinStrategy(provider),
			loadbalance.NewErrorAwareStrategy(provider),
			loadbalance.NewLatencyAwareStrategy(provider),
			loadbalance.NewRateLimitAwareStrategy(provider),
		)
	}
	return map[ChannelKind]*loadbalance.LoadBalancer{
		ChannelKindMessages:  makeLB(messagesMetrics, ChannelKindMessages),
		ChannelKindResponses: makeLB(responsesMetrics, ChannelKindResponses),
		ChannelKindGemini:    makeLB(geminiMetrics, ChannelKindGemini),
		ChannelKindChat:      makeLB(chatMetrics, ChannelKindChat),
		ChannelKindImages:    makeLB(imagesMetrics, ChannelKindImages),
	}
}

// getMetricsManager 根据类型获取对应的指标管理器
func (s *ChannelScheduler) getMetricsManager(kind ChannelKind) *metrics.MetricsManager {
	switch kind {
	case ChannelKindResponses:
		return s.responsesMetricsManager
	case ChannelKindGemini:
		return s.geminiMetricsManager
	case ChannelKindChat:
		return s.chatMetricsManager
	case ChannelKindImages:
		return s.imagesMetricsManager
	default:
		return s.messagesMetricsManager
	}
}

func metricsLookupKeys(baseURL, apiKey, serviceType string) []string {
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

	add(metrics.GenerateMetricsIdentityKey(baseURL, apiKey, serviceType))
	for _, variant := range utils.EquivalentBaseURLVariants(baseURL, serviceType) {
		add(metrics.GenerateMetricsKey(variant, apiKey))
	}
	return keys
}

func NormalizedMetricsServiceType(kind ChannelKind, configured string) string {
	if configured != "" {
		return configured
	}
	switch kind {
	case ChannelKindGemini:
		return "gemini"
	case ChannelKindResponses:
		return "responses"
	case ChannelKindChat:
		return "openai"
	case ChannelKindImages:
		return "openai"
	default:
		return "claude"
	}
}

func (s *ChannelScheduler) setChannelStatusByKind(index int, kind ChannelKind, status string) error {
	switch kind {
	case ChannelKindResponses:
		return s.configManager.SetResponsesChannelStatus(index, status)
	case ChannelKindGemini:
		return s.configManager.SetGeminiChannelStatus(index, status)
	case ChannelKindChat:
		return s.configManager.SetChatChannelStatus(index, status)
	case ChannelKindImages:
		return s.configManager.SetImagesChannelStatus(index, status)
	default:
		return s.configManager.SetChannelStatus(index, status)
	}
}

type ScheduledRecoveryResult struct {
	Kind             ChannelKind
	ChannelIndex     int
	ChannelName      string
	RestoredKeys     []string
	ActivatedChannel bool
}

// SelectionResult 渠道选择结果
type SelectionResult struct {
	Upstream     *config.UpstreamConfig
	ChannelIndex int
	Reason       string // 选择原因（用于日志）
}

// NextScheduledRecoveryTimeUTC 返回下一个 UTC 0/8/16 点后 1 秒的恢复时刻。
func NextScheduledRecoveryTimeUTC(now time.Time) time.Time {
	now = now.UTC()
	base := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 1, 0, time.UTC)
	for _, hour := range []int{0, 8, 16} {
		candidate := time.Date(base.Year(), base.Month(), base.Day(), hour, 0, 1, 0, time.UTC)
		if now.Before(candidate) {
			return candidate
		}
	}
	return base.Add(24 * time.Hour)
}

// LastScheduledRecoveryTimeUTC 返回当前时刻之前最近一个 UTC 0/8/16 点后 1 秒的恢复时刻。
func LastScheduledRecoveryTimeUTC(now time.Time) time.Time {
	now = now.UTC()
	base := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 1, 0, time.UTC)
	for i := len([]int{0, 8, 16}) - 1; i >= 0; i-- {
		hour := []int{0, 8, 16}[i]
		candidate := time.Date(base.Year(), base.Month(), base.Day(), hour, 0, 1, 0, time.UTC)
		if !now.Before(candidate) {
			return candidate
		}
	}
	return base.Add(-8 * time.Hour)
}

// MissedScheduledRecoveryTimeUTC 返回 (lastChecked, now] 区间内最近错过的恢复槽位。
func MissedScheduledRecoveryTimeUTC(lastChecked, now time.Time) (time.Time, bool) {
	lastChecked = lastChecked.UTC()
	now = now.UTC()
	if !now.After(lastChecked) {
		return time.Time{}, false
	}
	candidate := LastScheduledRecoveryTimeUTC(now)
	if candidate.After(lastChecked) {
		return candidate, true
	}
	return time.Time{}, false
}

func shouldSkipScheduledRecovery(disabledAt, recoverAt string, now time.Time) bool {
	if recoverAt != "" {
		parsed, err := time.Parse(time.RFC3339, recoverAt)
		if err == nil {
			return now.Before(parsed.UTC())
		}
	}
	if disabledAt == "" {
		return false
	}
	parsed, err := time.Parse(time.RFC3339, disabledAt)
	if err != nil {
		return false
	}
	return now.Sub(parsed.UTC()) < time.Hour
}

func kindAPIType(kind ChannelKind) string {
	switch kind {
	case ChannelKindResponses:
		return "Responses"
	case ChannelKindGemini:
		return "Gemini"
	case ChannelKindChat:
		return "Chat"
	case ChannelKindImages:
		return "Images"
	default:
		return "Messages"
	}
}

func (s *ChannelScheduler) scheduledRecoveryKinds() []ChannelKind {
	return []ChannelKind{ChannelKindMessages, ChannelKindResponses, ChannelKindGemini, ChannelKindChat, ChannelKindImages}
}

func (s *ChannelScheduler) restoreScheduledKeysForKind(kind ChannelKind, now time.Time) ([]ScheduledRecoveryResult, error) {
	cfg := s.configManager.GetConfig()
	var upstreams []config.UpstreamConfig
	switch kind {
	case ChannelKindResponses:
		upstreams = cfg.ResponsesUpstream
	case ChannelKindGemini:
		upstreams = cfg.GeminiUpstream
	case ChannelKindChat:
		upstreams = cfg.ChatUpstream
	case ChannelKindImages:
		upstreams = cfg.ImagesUpstream
	default:
		upstreams = cfg.Upstream
	}

	metricsManager := s.getMetricsManager(kind)
	apiType := kindAPIType(kind)
	results := make([]ScheduledRecoveryResult, 0)

	for idx, upstream := range upstreams {
		if upstream.Status == "disabled" || len(upstream.DisabledAPIKeys) == 0 {
			continue
		}

		keysToRestore := make([]string, 0)
		for _, dk := range upstream.DisabledAPIKeys {
			if !config.IsAutoRecoverableDisabledReason(dk.Reason) {
				continue
			}
			if shouldSkipScheduledRecovery(dk.DisabledAt, dk.RecoverAt, now) {
				continue
			}
			keysToRestore = append(keysToRestore, dk.Key)
		}
		if len(keysToRestore) == 0 {
			continue
		}

		restoreResult, err := transitions.RestoreDisabledKeysAndActivate(
			func(keys []string) ([]string, error) {
				return s.configManager.RestoreDisabledKeys(apiType, idx, keys)
			},
			func(_ string, apiKey string) {
				for _, baseURL := range upstream.GetAllBaseURLs() {
					metricsManager.MoveKeyToHalfOpen(baseURL, apiKey, NormalizedMetricsServiceType(kind, upstream.ServiceType))
				}
			},
			func(status string) error {
				return s.setChannelStatusByKind(idx, kind, status)
			},
			func() bool {
				latest := s.getUpstreamByIndex(idx, kind)
				return latest != nil && upstream.Status == "suspended" && len(upstream.APIKeys) == 0 && latest.Status == "suspended"
			},
			keysToRestore,
		)
		if err != nil {
			return nil, err
		}
		if len(restoreResult.RestoredKeys) == 0 {
			continue
		}

		updatedUpstream := s.getUpstreamByIndex(idx, kind)
		if updatedUpstream == nil {
			continue
		}

		results = append(results, ScheduledRecoveryResult{
			Kind:             kind,
			ChannelIndex:     idx,
			ChannelName:      updatedUpstream.Name,
			RestoredKeys:     restoreResult.RestoredKeys,
			ActivatedChannel: restoreResult.ActivatedChannel,
		})
	}

	return results, nil
}

// RunScheduledRecoveries 执行一次自动恢复扫描。
func (s *ChannelScheduler) RunScheduledRecoveries(now time.Time) ([]ScheduledRecoveryResult, error) {
	results := make([]ScheduledRecoveryResult, 0)
	for _, kind := range s.scheduledRecoveryKinds() {
		kindResults, err := s.restoreScheduledKeysForKind(kind, now.UTC())
		if err != nil {
			return nil, err
		}
		results = append(results, kindResults...)
	}
	return results, nil
}

// 优先级: 促销期渠道 > Trace亲和（促销渠道失败时回退） > 渠道优先级顺序
// PR3 T4：拆分为"硬过滤 + LoadBalancer.Sort 排序"两阶段。
//   - 硬过滤负责剔除模型不兼容、route prefix 不匹配、非 active、APIKeys 空、
//     circuit Open / 不健康（除非促销期）等"不应被排序"的渠道。
//   - LoadBalancer.Sort 通过 6 个 strategy 加权打分：Promotion(+800) /
//     TraceAware(+1000) / WeightRR(10-150) / ErrorAware(0-200) / LatencyAware
//     / RateLimitAware，得分最高者胜出。
func (s *ChannelScheduler) SelectChannel(
	ctx context.Context,
	userID string,
	failedChannels map[int]bool,
	kind ChannelKind,
	model string,
	routePrefix string,
) (*SelectionResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Step 1: 硬过滤
	candidates, mapping, allActive, err := s.filterCandidates(kind, model, routePrefix, failedChannels)
	if err != nil {
		return nil, err
	}
	prefix := kindSchedulerLogPrefix(kind)
	if len(candidates) == 0 {
		log.Printf("[%s-LB] 警告: 硬过滤后无候选渠道，进入降级路径", prefix)
		return s.selectFallbackChannel(allActive, failedChannels, kind)
	}

	// Step 2: 注入 ctx（model / stream / traceID），供 strategy 读取。
	ctx = loadbalance.ContextWithRequestedModel(ctx, model)
	ctx = loadbalance.ContextWithRequestStream(ctx, false)
	if userID != "" {
		ctx = loadbalance.ContextWithTraceID(ctx, userID)
	}

	// Step 3: 调用 LoadBalancer 排序，取 top1。
	lb, ok := s.loadBalancerByKind[kind]
	if !ok || lb == nil {
		log.Printf("[%s-LB] 警告: 未找到 LoadBalancer 实例 (kind=%s)，降级", prefix, kind)
		return s.selectFallbackChannel(allActive, failedChannels, kind)
	}
	sorted := lb.Sort(ctx, candidates, model, false, 1)
	if len(sorted) == 0 {
		return s.selectFallbackChannel(allActive, failedChannels, kind)
	}
	chosenID := sorted[0].ID

	chosenInfo, ok := mapping[chosenID]
	if !ok {
		return s.selectFallbackChannel(allActive, failedChannels, kind)
	}
	upstream := s.getUpstreamByIndex(chosenInfo.Index, kind)
	if upstream == nil {
		return s.selectFallbackChannel(allActive, failedChannels, kind)
	}

	// Step 4: 推断 reason，保持现有 SelectionResult.Reason 语义兼容。
	reason := s.inferSelectionReason(userID, kind, upstream, chosenInfo.Index, failedChannels)
	log.Printf("[%s-LB] 选择渠道: [%d] %s (优先级: %d, reason: %s)", prefix, chosenInfo.Index, upstream.Name, chosenInfo.Priority, reason)

	return &SelectionResult{
		Upstream:     upstream,
		ChannelIndex: chosenInfo.Index,
		Reason:       reason,
	}, nil
}

// filterCandidates 完成 LB 排序前的"硬过滤"步骤：
//   - 状态过滤：status != "disabled"
//   - 模型兼容：通过 ExplainModelSupport 校验
//   - Route prefix 匹配（带 prefix 的渠道仅响应同名 prefix 路由）
//   - 跳过本次请求已失败的渠道
//   - 跳过 APIKeys 空 / status != "active" 的渠道
//   - 非促销渠道：跳过 circuit Open / 不健康
//   - 促销期渠道：仅要求 status=="active" + APIKeys 非空，绕过健康检查
//
// 返回 (candidates, mapping, allActive, err)：
//   - candidates: 已过滤的 *loadbalance.Channel，传给 LB.Sort
//   - mapping:    channel ID(=upstream 索引) -> ChannelInfo 反查表
//   - allActive:  路由前缀过滤后的全部活跃渠道（用于 fallback 路径，保留原行为）
//   - err:        终止性错误（无任何匹配模型/前缀的渠道）
func (s *ChannelScheduler) filterCandidates(
	kind ChannelKind,
	model string,
	routePrefix string,
	failedChannels map[int]bool,
) ([]*loadbalance.Channel, map[int]ChannelInfo, []ChannelInfo, error) {
	activeChannels := s.getActiveChannels(kind, model)
	if len(activeChannels) == 0 {
		kindName := kindAPIType(kind)
		if model != "" && len(s.getActiveChannels(kind, "")) > 0 {
			return nil, nil, nil, fmt.Errorf("没有 %s 渠道支持模型 %q，请检查渠道的 supportedModels 配置", kindName, model)
		}
		return nil, nil, nil, fmt.Errorf("没有可用的活跃 %s 渠道", kindName)
	}

	// Route prefix 过滤
	if routePrefix != "" {
		var filtered []ChannelInfo
		for _, ch := range activeChannels {
			upstream := s.getUpstreamByIndex(ch.Index, kind)
			if upstream != nil && upstream.RoutePrefix == routePrefix {
				filtered = append(filtered, ch)
			}
		}
		if len(filtered) == 0 {
			return nil, nil, nil, fmt.Errorf("no channels with route prefix: %s", routePrefix)
		}
		activeChannels = filtered
	} else {
		var filtered []ChannelInfo
		for _, ch := range activeChannels {
			upstream := s.getUpstreamByIndex(ch.Index, kind)
			if upstream != nil && upstream.RoutePrefix == "" {
				filtered = append(filtered, ch)
			}
		}
		if len(filtered) == 0 {
			kindName := kindAPIType(kind)
			return nil, nil, nil, fmt.Errorf("没有可用于默认路由的 %s 渠道，请使用带前缀路由访问", kindName)
		}
		activeChannels = filtered
	}

	candidates := make([]*loadbalance.Channel, 0, len(activeChannels))
	mapping := make(map[int]ChannelInfo, len(activeChannels))
	prefix := kindSchedulerLogPrefix(kind)

	for _, ch := range activeChannels {
		// 本次请求已失败：硬剔除
		if failedChannels[ch.Index] {
			continue
		}
		upstream := s.getUpstreamByIndex(ch.Index, kind)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			continue
		}
		// 非 active 状态（如 suspended）一律剔除
		if ch.Status != "active" {
			log.Printf("[%s-LB] 跳过非活跃渠道: [%d] %s (状态: %s)", prefix, ch.Index, ch.Name, ch.Status)
			continue
		}

		// 促销期渠道直接通过；否则需通过健康检查
		if !config.IsChannelInPromotion(upstream) {
			channelState := s.channelCircuitState(upstream, kind)
			if channelState == metrics.CircuitStateOpen {
				log.Printf("[%s-LB] 跳过 circuit Open 渠道: [%d] %s", prefix, ch.Index, ch.Name)
				continue
			}
			if !s.channelIsHealthy(upstream, kind) {
				failureRate := s.channelFailureRate(upstream, kind)
				log.Printf("[%s-LB] 跳过不健康渠道: [%d] %s (失败率: %.1f%%)", prefix, ch.Index, ch.Name, failureRate*100)
				continue
			}
		}

		// 投影 ChannelInfo → loadbalance.Channel；ID 即 upstream 索引（与 LBMetricsProvider 命名空间一致）。
		// OrderingWeight 编码 Priority（优先级越高 → OrderingWeight 越大），
		// 用作 partial sort 的次级 tiebreaker，避免 LB 等分时丢失 priority 主序。
		candidates = append(candidates, &loadbalance.Channel{
			ID:             ch.Index,
			Name:           ch.Name,
			Priority:       ch.Priority,
			OrderingWeight: priorityToOrderingWeight(ch.Priority),
		})
		mapping[ch.Index] = ch
	}

	return candidates, mapping, activeChannels, nil
}

// priorityToOrderingWeight 把 ccx 的 Priority（小为优）映射为 LB 的 OrderingWeight
// （大为优）。
//
// 仅作为 partial_sort.go 的 tiebreaker（TotalScore 相同时使用），不参与 WRR 打分
// （WRR 已通过 provider.OrderingWeight 取值，当前 LBMetricsProvider 返回 0，
// strategy_weight_rr 内部回退到 channel.OrderingWeight；为避免污染 WRR 分数，
// 这里取 1_000_000 - Priority 量级，既能在 tiebreaker 中保持稳定主序，又能在
// WRR 中等价于"超高权重"（耐受请求数远高于实际请求数），不会颠覆 priority 顺序。
func priorityToOrderingWeight(priority int) int {
	const offset = 1_000_000
	return offset - priority
}

// inferSelectionReason 在 LB 选出 channel 后还原 SelectionResult.Reason，
// 保持与原 SelectChannel 三种 reason 字段一致：
//   - "promotion_priority": 选中渠道处于促销期且未失败
//   - "trace_affinity":     选中渠道是 trace affinity 偏好渠道
//   - "priority_order":     其余情况（按 LB 总分胜出）
//
// 触发顺序：promotion 优于 trace_affinity（与 LB 总分比重 800 vs 1000 含义不同；
// 这里的 reason 仅是"展示标签"，实际选择仍以 LB 总分为准）。
func (s *ChannelScheduler) inferSelectionReason(
	userID string,
	kind ChannelKind,
	upstream *config.UpstreamConfig,
	chosenIdx int,
	failedChannels map[int]bool,
) string {
	if upstream != nil && config.IsChannelInPromotion(upstream) && !failedChannels[chosenIdx] {
		return "promotion_priority"
	}
	if userID != "" && s.traceAffinity != nil {
		compositeKey := string(kind) + ":" + userID
		if pref, ok := s.traceAffinity.GetPreferredChannel(compositeKey); ok && pref == chosenIdx {
			return "trace_affinity"
		}
	}
	return "priority_order"
}

func (s *ChannelScheduler) channelCircuitState(upstream *config.UpstreamConfig, kind ChannelKind) metrics.CircuitState {
	if upstream == nil {
		return metrics.CircuitStateClosed
	}
	return s.getMetricsManager(kind).GetChannelCircuitStateMultiURL(upstream.GetAllBaseURLs(), upstream.APIKeys, NormalizedMetricsServiceType(kind, upstream.ServiceType))
}

func (s *ChannelScheduler) channelFailureRate(upstream *config.UpstreamConfig, kind ChannelKind) float64 {
	if upstream == nil {
		return 0
	}
	return s.getMetricsManager(kind).CalculateChannelFailureRateMultiURL(upstream.GetAllBaseURLs(), upstream.APIKeys, NormalizedMetricsServiceType(kind, upstream.ServiceType))
}

func (s *ChannelScheduler) channelIsHealthy(upstream *config.UpstreamConfig, kind ChannelKind) bool {
	if upstream == nil {
		return false
	}
	return s.getMetricsManager(kind).IsChannelHealthyMultiURL(upstream.GetAllBaseURLs(), upstream.APIKeys, NormalizedMetricsServiceType(kind, upstream.ServiceType))
}

// selectFallbackChannel 选择降级渠道（失败率最低的）
func (s *ChannelScheduler) selectFallbackChannel(
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
	kind ChannelKind,
) (*SelectionResult, error) {
	var bestChannel *ChannelInfo
	var bestUpstream *config.UpstreamConfig
	bestFailureRate := float64(2) // 初始化为不可能的值

	for i := range activeChannels {
		ch := &activeChannels[i]
		if failedChannels[ch.Index] {
			continue
		}
		// 跳过非 active 状态的渠道
		if ch.Status != "active" {
			continue
		}

		upstream := s.getUpstreamByIndex(ch.Index, kind)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			continue
		}

		channelState := s.channelCircuitState(upstream, kind)
		if channelState == metrics.CircuitStateOpen {
			continue
		}

		failureRate := s.channelFailureRate(upstream, kind)
		if failureRate < bestFailureRate {
			bestFailureRate = failureRate
			bestChannel = ch
			bestUpstream = upstream
		}
	}

	if bestChannel != nil && bestUpstream != nil {
		prefix := kindSchedulerLogPrefix(kind)
		log.Printf("[%s-Fallback] 警告: 降级选择渠道: [%d] %s (失败率: %.1f%%)",
			prefix, bestChannel.Index, bestUpstream.Name, bestFailureRate*100)
		return &SelectionResult{
			Upstream:     bestUpstream,
			ChannelIndex: bestChannel.Index,
			Reason:       "fallback",
		}, nil
	}

	return nil, fmt.Errorf("所有渠道都不可用")
}

// ChannelInfo 渠道信息（用于排序）
// Priority 约定为非负整数，数字越小优先级越高；0 表示未显式配置，将回退为渠道索引。
type ChannelInfo struct {
	Index    int
	Name     string
	Priority int
	Status   string
}

// getActiveChannels 获取活跃渠道列表（按优先级排序）
func (s *ChannelScheduler) getActiveChannels(kind ChannelKind, model string) []ChannelInfo {
	cfg := s.configManager.GetConfig()

	var upstreams []config.UpstreamConfig
	switch kind {
	case ChannelKindResponses:
		upstreams = cfg.ResponsesUpstream
	case ChannelKindGemini:
		upstreams = cfg.GeminiUpstream
	case ChannelKindChat:
		upstreams = cfg.ChatUpstream
	case ChannelKindImages:
		upstreams = cfg.ImagesUpstream
	default:
		upstreams = cfg.Upstream
	}

	// 筛选活跃渠道
	var activeChannels []ChannelInfo
	for i, upstream := range upstreams {
		status := upstream.Status
		if status == "" {
			status = "active" // 默认为活跃
		}

		// 只选择 active 状态的渠道（suspended 也算在活跃序列中，但会被健康检查过滤）
		if status != "disabled" {
			// 过滤不支持当前模型的渠道
			if model != "" {
				supported, reason := upstream.ExplainModelSupport(model)
				if !supported {
					prefix := kindSchedulerLogPrefix(kind)
					log.Printf("[%s-ModelFilter] 跳过渠道 [%d] %s: 模型 %q 不被 supportedModels 支持 (%s)", prefix, i, upstream.Name, model, reason)
					continue
				}
			}

			priority := upstream.Priority
			if priority == 0 {
				priority = i // 默认优先级为索引
			}

			activeChannels = append(activeChannels, ChannelInfo{
				Index:    i,
				Name:     upstream.Name,
				Priority: priority,
				Status:   status,
			})
		}
	}

	// 按优先级排序（数字越小优先级越高）
	sort.Slice(activeChannels, func(i, j int) bool {
		return activeChannels[i].Priority < activeChannels[j].Priority
	})

	return activeChannels
}

// getUpstreamByIndex 根据索引获取上游配置
// 注意：返回的是副本，避免指向 slice 元素的指针在 slice 重分配后失效
func (s *ChannelScheduler) getUpstreamByIndex(index int, kind ChannelKind) *config.UpstreamConfig {
	cfg := s.configManager.GetConfig()

	var upstreams []config.UpstreamConfig
	switch kind {
	case ChannelKindResponses:
		upstreams = cfg.ResponsesUpstream
	case ChannelKindGemini:
		upstreams = cfg.GeminiUpstream
	case ChannelKindChat:
		upstreams = cfg.ChatUpstream
	case ChannelKindImages:
		upstreams = cfg.ImagesUpstream
	default:
		upstreams = cfg.Upstream
	}

	if index >= 0 && index < len(upstreams) {
		// 返回副本，避免返回指向 slice 元素的指针
		upstream := upstreams[index]
		return &upstream
	}
	return nil
}

// RecordSuccess 记录渠道成功（使用 baseURL + apiKey）
func (s *ChannelScheduler) RecordSuccess(baseURL, apiKey, serviceType string, kind ChannelKind) {
	s.getMetricsManager(kind).RecordSuccess(baseURL, apiKey, serviceType)
}

// RecordSuccessWithUsage 记录渠道成功（带 Usage 数据）
func (s *ChannelScheduler) RecordSuccessWithUsage(baseURL, apiKey, serviceType string, usage *types.Usage, kind ChannelKind) {
	s.getMetricsManager(kind).RecordSuccessWithUsage(baseURL, apiKey, serviceType, usage)
}

// RecordFailure 记录渠道失败（使用 baseURL + apiKey）
func (s *ChannelScheduler) RecordFailure(baseURL, apiKey, serviceType string, kind ChannelKind) {
	s.getMetricsManager(kind).RecordFailure(baseURL, apiKey, serviceType)
}

// RecordRequestStart 记录请求开始
func (s *ChannelScheduler) RecordRequestStart(baseURL, apiKey, serviceType string, kind ChannelKind) {
	s.getMetricsManager(kind).RecordRequestStart(baseURL, apiKey, serviceType)
}

// RecordRequestEnd 记录请求结束
func (s *ChannelScheduler) RecordRequestEnd(baseURL, apiKey, serviceType string, kind ChannelKind) {
	s.getMetricsManager(kind).RecordRequestEnd(baseURL, apiKey, serviceType)
}

// SetTraceAffinity 设置 Trace 亲和（按 kind 隔离）
func (s *ChannelScheduler) SetTraceAffinity(userID string, channelIndex int, kind ChannelKind) {
	if userID != "" {
		compositeKey := string(kind) + ":" + userID
		s.traceAffinity.SetPreferredChannel(compositeKey, channelIndex)
	}
}

// UpdateTraceAffinity 更新 Trace 亲和时间（续期，按 kind 隔离）
func (s *ChannelScheduler) UpdateTraceAffinity(userID string, kind ChannelKind) {
	if userID != "" {
		compositeKey := string(kind) + ":" + userID
		s.traceAffinity.UpdateLastUsed(compositeKey)
	}
}

// GetMessagesMetricsManager 获取 Messages 渠道指标管理器
func (s *ChannelScheduler) GetMessagesMetricsManager() *metrics.MetricsManager {
	return s.messagesMetricsManager
}

// GetResponsesMetricsManager 获取 Responses 渠道指标管理器
func (s *ChannelScheduler) GetResponsesMetricsManager() *metrics.MetricsManager {
	return s.responsesMetricsManager
}

// GetGeminiMetricsManager 获取 Gemini 渠道指标管理器
func (s *ChannelScheduler) GetGeminiMetricsManager() *metrics.MetricsManager {
	return s.geminiMetricsManager
}

// GetChatMetricsManager 获取 Chat 指标管理器
func (s *ChannelScheduler) GetChatMetricsManager() *metrics.MetricsManager {
	return s.chatMetricsManager
}

// GetImagesMetricsManager 获取 Images 指标管理器
func (s *ChannelScheduler) GetImagesMetricsManager() *metrics.MetricsManager {
	return s.imagesMetricsManager
}

// GetTraceAffinityManager 获取 Trace 亲和性管理器
func (s *ChannelScheduler) GetTraceAffinityManager() *session.TraceAffinityManager {
	return s.traceAffinity
}

// GetChannelLogStore 根据渠道类型获取对应的日志存储
func (s *ChannelScheduler) GetChannelLogStore(kind ChannelKind) *metrics.ChannelLogStore {
	switch kind {
	case ChannelKindResponses:
		return s.responsesChannelLogStore
	case ChannelKindGemini:
		return s.geminiChannelLogStore
	case ChannelKindChat:
		return s.chatChannelLogStore
	case ChannelKindImages:
		return s.imagesChannelLogStore
	default:
		return s.messagesChannelLogStore
	}
}

// ResetChannelMetrics 重置渠道所有 Key 的熔断/失败状态（保留历史统计）
// 用于：1) 手动恢复熔断 2) 更换 API Key 后重置熔断状态
func (s *ChannelScheduler) ResetChannelMetrics(channelIndex int, kind ChannelKind) {
	upstream := s.getUpstreamByIndex(channelIndex, kind)
	if upstream == nil {
		return
	}
	metricsManager := s.getMetricsManager(kind)
	for _, baseURL := range upstream.GetAllBaseURLs() {
		for _, apiKey := range upstream.APIKeys {
			metricsManager.ResetKeyFailureState(baseURL, apiKey, NormalizedMetricsServiceType(kind, upstream.ServiceType))
		}
	}
	prefix := kindSchedulerLogPrefix(kind)
	log.Printf("[%s-Reset] 渠道 [%d] %s 的熔断状态已重置（保留历史统计）", prefix, channelIndex, upstream.Name)
}

// ResetKeyMetrics 重置单个 Key 的指标
func (s *ChannelScheduler) ResetKeyMetrics(baseURL, apiKey, serviceType string, kind ChannelKind) {
	s.getMetricsManager(kind).ResetKey(baseURL, apiKey, serviceType)
}

// DeleteChannelMetrics 删除渠道的所有指标数据（内存 + 持久化）
// 用于删除渠道时清理相关的统计数据
// 注意：如果其他渠道使用相同的 (BaseURL, APIKey) 组合，则保留对应的 MetricsKey
// 前置条件：调用此方法前，被删除的渠道应已从 config 中移除
func (s *ChannelScheduler) DeleteChannelMetrics(upstream *config.UpstreamConfig, kind ChannelKind) {
	if upstream == nil {
		return
	}

	prefix := kindSchedulerLogPrefix(kind)

	// 前置条件守卫：检查被删除渠道是否仍在配置中
	// 如果仍在配置中，说明调用时机不对，记录警告并继续执行（但结果可能不正确）
	if s.isUpstreamInConfig(upstream, kind) {
		log.Printf("[%s-Delete] 警告: 渠道 %s 仍在配置中，删除指标可能不完整（应先从配置中移除）", prefix, upstream.Name)
	}

	// 获取被删除渠道的所有 (BaseURL, APIKey) 组合
	deletedBaseURLs := upstream.GetAllBaseURLs()
	deletedKeys := append([]string{}, upstream.APIKeys...)
	deletedKeys = append(deletedKeys, upstream.HistoricalAPIKeys...)

	// 收集当前配置中所有渠道使用的 (BaseURL, APIKey) 组合
	// 注意：此时被删除渠道应已从 config 中移除
	usedMetricsKeys := s.collectUsedMetricsKeys(kind)

	// 收集只被删除渠道独占的 metricsKey 列表（使用 map 去重）
	exclusiveKeysSet := make(map[string]struct{})
	serviceType := NormalizedMetricsServiceType(kind, upstream.ServiceType)

	for _, baseURL := range deletedBaseURLs {
		for _, apiKey := range deletedKeys {
			for _, metricsKey := range metricsLookupKeys(baseURL, apiKey, serviceType) {
				if !usedMetricsKeys[metricsKey] {
					exclusiveKeysSet[metricsKey] = struct{}{}
				}
			}
		}
	}

	// 转换为切片
	exclusiveMetricsKeys := make([]string, 0, len(exclusiveKeysSet))
	for key := range exclusiveKeysSet {
		exclusiveMetricsKeys = append(exclusiveMetricsKeys, key)
	}

	metricsManager := s.getMetricsManager(kind)

	// 只删除独占的 MetricsKey
	if len(exclusiveMetricsKeys) > 0 {
		metricsManager.DeleteByMetricsKeys(exclusiveMetricsKeys)
		log.Printf("[%s-Delete] 渠道 %s 的 %d 个独占指标数据已清理", prefix, upstream.Name, len(exclusiveMetricsKeys))
	} else {
		log.Printf("[%s-Delete] 渠道 %s 的指标数据被其他渠道共享，已保留", prefix, upstream.Name)
	}
}

// collectUsedMetricsKeys 收集当前配置中所有渠道仍在使用的 identity metricsKey。
// 注意：调用此方法前，被删除的渠道应已从 config 中移除。
func (s *ChannelScheduler) collectUsedMetricsKeys(kind ChannelKind) map[string]bool {
	cfg := s.configManager.GetConfig()

	var upstreams []config.UpstreamConfig
	switch kind {
	case ChannelKindResponses:
		upstreams = cfg.ResponsesUpstream
	case ChannelKindGemini:
		upstreams = cfg.GeminiUpstream
	case ChannelKindChat:
		upstreams = cfg.ChatUpstream
	case ChannelKindImages:
		upstreams = cfg.ImagesUpstream
	default:
		upstreams = cfg.Upstream
	}

	usedMetricsKeys := make(map[string]bool)
	for _, upstream := range upstreams {
		baseURLs := upstream.GetAllBaseURLs()
		allKeys := append([]string{}, upstream.APIKeys...)
		allKeys = append(allKeys, upstream.HistoricalAPIKeys...)
		serviceType := NormalizedMetricsServiceType(kind, upstream.ServiceType)

		for _, baseURL := range baseURLs {
			for _, apiKey := range allKeys {
				for _, metricsKey := range metricsLookupKeys(baseURL, apiKey, serviceType) {
					usedMetricsKeys[metricsKey] = true
				}
			}
		}
	}

	return usedMetricsKeys
}

// isUpstreamInConfig 检查指定的 upstream 是否仍在当前配置中
// 通过比较 Name 字段判断（Name 在同类型渠道中应唯一）
func (s *ChannelScheduler) isUpstreamInConfig(upstream *config.UpstreamConfig, kind ChannelKind) bool {
	cfg := s.configManager.GetConfig()

	var upstreams []config.UpstreamConfig
	switch kind {
	case ChannelKindResponses:
		upstreams = cfg.ResponsesUpstream
	case ChannelKindGemini:
		upstreams = cfg.GeminiUpstream
	case ChannelKindChat:
		upstreams = cfg.ChatUpstream
	case ChannelKindImages:
		upstreams = cfg.ImagesUpstream
	default:
		upstreams = cfg.Upstream
	}

	for _, u := range upstreams {
		if u.Name == upstream.Name {
			return true
		}
	}
	return false
}

// GetActiveChannelCount 获取活跃渠道数量
func (s *ChannelScheduler) GetActiveChannelCount(kind ChannelKind) int {
	return len(s.getActiveChannels(kind, ""))
}

// IsMultiChannelMode 判断是否为多渠道模式
func (s *ChannelScheduler) IsMultiChannelMode(kind ChannelKind) bool {
	return s.GetActiveChannelCount(kind) > 1
}

// maskUserID 掩码 user_id（保护隐私）
func maskUserID(userID string) string {
	if len(userID) <= 16 {
		return "***"
	}
	return userID[:8] + "***" + userID[len(userID)-4:]
}

// GetSortedURLsForChannel 获取渠道排序后的 URL 列表（非阻塞，立即返回）
// 返回按动态排序的 URL 结果列表，包含原始索引用于指标记录
func (s *ChannelScheduler) GetSortedURLsForChannel(
	kind ChannelKind,
	channelIndex int,
	urls []string,
) []warmup.URLLatencyResult {
	if s.urlManager == nil || len(urls) <= 1 {
		// 无 URL 管理器或单 URL，返回默认结果
		results := make([]warmup.URLLatencyResult, len(urls))
		for i, url := range urls {
			results[i] = warmup.URLLatencyResult{
				URL:         url,
				OriginalIdx: i,
				Success:     true,
			}
		}
		return results
	}
	return s.urlManager.GetSortedURLs(urlManagerChannelKey(kind, channelIndex), urls)
}

// MarkURLSuccess 标记 URL 成功
func (s *ChannelScheduler) MarkURLSuccess(kind ChannelKind, channelIndex int, url string) {
	if s.urlManager != nil {
		s.urlManager.MarkSuccess(urlManagerChannelKey(kind, channelIndex), url)
	}
}

// MarkURLFailure 标记 URL 失败，触发动态排序
func (s *ChannelScheduler) MarkURLFailure(kind ChannelKind, channelIndex int, url string) {
	if s.urlManager != nil {
		s.urlManager.MarkFailure(urlManagerChannelKey(kind, channelIndex), url)
	}
}

// InvalidateURLCache 使渠道 URL 状态失效
func (s *ChannelScheduler) InvalidateURLCache(kind ChannelKind, channelIndex int) {
	if s.urlManager != nil {
		s.urlManager.InvalidateChannel(urlManagerChannelKey(kind, channelIndex))
	}
}

// GetURLManagerStats 获取 URL 管理器统计
func (s *ChannelScheduler) GetURLManagerStats() map[string]interface{} {
	if s.urlManager != nil {
		return s.urlManager.GetStats()
	}
	return nil
}

func kindSchedulerLogPrefix(kind ChannelKind) string {
	switch kind {
	case ChannelKindResponses:
		return "Scheduler-Responses"
	case ChannelKindGemini:
		return "Scheduler-Gemini"
	case ChannelKindChat:
		return "Scheduler-Chat"
	case ChannelKindImages:
		return "Scheduler-Images"
	default:
		return "Scheduler"
	}
}

func urlManagerChannelKey(kind ChannelKind, channelIndex int) int {
	const stride = 1_000_000
	return urlManagerChannelKeyOrdinal(kind)*stride + channelIndex
}

func urlManagerChannelKeyOrdinal(kind ChannelKind) int {
	switch kind {
	case ChannelKindResponses:
		return 1
	case ChannelKindGemini:
		return 2
	case ChannelKindChat:
		return 3
	case ChannelKindImages:
		return 4
	default:
		return 0
	}
}
