package scheduler

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/conversation"
	"github.com/BenedictKing/ccx/internal/eventbus"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/quota"
	"github.com/BenedictKing/ccx/internal/ratelimit"
	"github.com/BenedictKing/ccx/internal/routingref"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/BenedictKing/ccx/internal/warmup"
)

// ChannelScheduler 多渠道调度器
// ChannelRouteRef identifies one physical configured channel route.
type ChannelRouteRef = routingref.RouteRef

// ChannelRouteKey is the stable comparable key for a physical route.
type ChannelRouteKey = routingref.Key

type ChannelScheduler struct {
	mu                        sync.RWMutex
	configManager             *config.ConfigManager
	messagesMetricsManager    *metrics.MetricsManager // Messages 渠道指标
	responsesMetricsManager   *metrics.MetricsManager // Responses 渠道指标
	geminiMetricsManager      *metrics.MetricsManager // Gemini 渠道指标
	chatMetricsManager        *metrics.MetricsManager // Chat 渠道指标
	imagesMetricsManager      *metrics.MetricsManager // Images 渠道指标
	vectorsMetricsManager     *metrics.MetricsManager // Vectors 渠道指标
	traceAffinity             *session.TraceAffinityManager
	urlManager                *warmup.URLManager       // URL 管理器（非阻塞，动态排序）
	messagesChannelLogStore   *metrics.ChannelLogStore // Messages 渠道请求日志
	responsesChannelLogStore  *metrics.ChannelLogStore // Responses 渠道请求日志
	geminiChannelLogStore     *metrics.ChannelLogStore // Gemini 渠道请求日志
	chatChannelLogStore       *metrics.ChannelLogStore // Chat 渠道请求日志
	imagesChannelLogStore     *metrics.ChannelLogStore // Images 渠道请求日志
	vectorsChannelLogStore    *metrics.ChannelLogStore // Vectors 渠道请求日志
	conversationTracker       *conversation.ConversationTracker
	overrideManager           *conversation.OverrideManager
	rateLimitManager          *ratelimit.Manager
	candidateFilterProvider   CandidateFilterProvider       // SmartRouter shadow 注入点
	modelSupportResolverFunc  ModelSupportResolverFunc      // Autopilot 模型支持解析注入点
	contextWindowResolverFunc ContextWindowResolverFunc     // 上下文有效窗口解析注入点（学习证据合成）
	overflowCandidateProvider OverflowCandidateProviderFunc // 溢出跨协议重定向候选注入点
	quotaManager              *quota.Manager                // 配额真相与余量管理器（nil = 不参与沉底排序）
	loadShedMu                sync.Mutex
	loadShedStates            map[string]rateLimitLoadShedState
	loadShedStopCh            chan struct{}
	lastSelectedMu            sync.RWMutex
	lastSelectedChannels      map[ChannelKind]int

	// eventBus 跨模块事件总线（Phase B.1，可选）。未注入时渠道状态迁移不发事件。
	eventBus atomic.Pointer[eventbus.Bus]
}

// ChannelKind 标识调度器所处理的渠道类型
// 注意：这里的 kind 与 upstream.ServiceType（openai/claude/gemini）不同，
// kind 对应的是本代理对外暴露的渠道入口：messages / responses / gemini / chat / images / vectors。
type ChannelKind string

const (
	ChannelKindMessages  ChannelKind = "messages"
	ChannelKindResponses ChannelKind = "responses"
	ChannelKindGemini    ChannelKind = "gemini"
	ChannelKindChat      ChannelKind = "chat"
	ChannelKindImages    ChannelKind = "images"
	ChannelKindVectors   ChannelKind = "vectors"
)

// NewChannelScheduler 创建多渠道调度器
func NewChannelScheduler(
	cfgManager *config.ConfigManager,
	messagesMetrics *metrics.MetricsManager,
	responsesMetrics *metrics.MetricsManager,
	geminiMetrics *metrics.MetricsManager,
	chatMetrics *metrics.MetricsManager,
	imagesMetrics *metrics.MetricsManager,
	traceAffinity *session.TraceAffinityManager,
	urlMgr *warmup.URLManager,
	vectorsMetrics ...*metrics.MetricsManager,
) *ChannelScheduler {
	vectorsManager := metrics.NewMetricsManager()
	if len(vectorsMetrics) > 0 && vectorsMetrics[0] != nil {
		vectorsManager = vectorsMetrics[0]
	}
	return &ChannelScheduler{
		configManager:            cfgManager,
		messagesMetricsManager:   messagesMetrics,
		responsesMetricsManager:  responsesMetrics,
		geminiMetricsManager:     geminiMetrics,
		chatMetricsManager:       chatMetrics,
		imagesMetricsManager:     imagesMetrics,
		vectorsMetricsManager:    vectorsManager,
		traceAffinity:            traceAffinity,
		urlManager:               urlMgr,
		messagesChannelLogStore:  metrics.NewChannelLogStore(),
		responsesChannelLogStore: metrics.NewChannelLogStore(),
		geminiChannelLogStore:    metrics.NewChannelLogStore(),
		chatChannelLogStore:      metrics.NewChannelLogStore(),
		imagesChannelLogStore:    metrics.NewChannelLogStore(),
		vectorsChannelLogStore:   metrics.NewChannelLogStore(),
		conversationTracker:      nil,
		loadShedStates:           make(map[string]rateLimitLoadShedState),
		loadShedStopCh:           make(chan struct{}),
		lastSelectedChannels:     make(map[ChannelKind]int),
	}
}

// SetConversationComponents 设置对话追踪和覆盖管理组件
func (s *ChannelScheduler) SetConversationComponents(tracker *conversation.ConversationTracker, overrideMgr *conversation.OverrideManager) {
	s.conversationTracker = tracker
	s.overrideManager = overrideMgr
}

// GetConversationTracker 获取对话追踪器
func (s *ChannelScheduler) GetConversationTracker() *conversation.ConversationTracker {
	return s.conversationTracker
}

// GetOverrideManager 获取覆盖管理器
func (s *ChannelScheduler) GetOverrideManager() *conversation.OverrideManager {
	return s.overrideManager
}

// SetRateLimitManager 设置主动限速管理器
func (s *ChannelScheduler) SetRateLimitManager(m *ratelimit.Manager) {
	s.rateLimitManager = m
}

// SetEventBus 注入跨模块事件总线（Phase B.1）。可选依赖：未注入时 scheduler 不发渠道状态事件。
// 并发安全；会把同一总线透传给已注入的各 MetricsManager（如果它们尚未设置）。
func (s *ChannelScheduler) SetEventBus(bus *eventbus.Bus) {
	if s == nil {
		return
	}
	s.eventBus.Store(bus)
	if s.messagesMetricsManager != nil {
		s.messagesMetricsManager.SetEventBus(bus)
	}
	if s.responsesMetricsManager != nil {
		s.responsesMetricsManager.SetEventBus(bus)
	}
	if s.geminiMetricsManager != nil {
		s.geminiMetricsManager.SetEventBus(bus)
	}
	if s.chatMetricsManager != nil {
		s.chatMetricsManager.SetEventBus(bus)
	}
	if s.imagesMetricsManager != nil {
		s.imagesMetricsManager.SetEventBus(bus)
	}
	if s.vectorsMetricsManager != nil {
		s.vectorsMetricsManager.SetEventBus(bus)
	}
}

// publishChannelStatusEvent 发布渠道 administrative 状态变更事件。
// bus 未注入、channelUID 为空或状态未变化时为空操作；发布非阻塞。
func (s *ChannelScheduler) publishChannelStatusEvent(channelUID, channelName, kind, oldStatus, newStatus, reason string) {
	if s == nil || channelUID == "" || oldStatus == newStatus {
		return
	}
	bus := s.eventBus.Load()
	if bus == nil {
		return
	}
	if oldStatus == "" {
		oldStatus = "active"
	}
	if newStatus == "" {
		newStatus = "active"
	}
	now := time.Now().UTC()
	bus.Publish(eventbus.Event{
		UID:         "",
		Type:        eventbus.TypeChannelStatusChanged,
		Scope:       eventbus.ScopeConfig,
		Subject:     channelUID,
		ChannelKind: kind,
		From:        oldStatus,
		To:          newStatus,
		Cause:       reason,
		Payload: map[string]any{
			"channelUID":  channelUID,
			"channelName": channelName,
			"kind":        kind,
			"oldStatus":   oldStatus,
			"newStatus":   newStatus,
			"reason":      reason,
			"timestamp":   now.Unix(),
		},
		CreatedAt: now,
	})
}

// CandidateSelectionObserver 在本次 CandidateFilter 对应的真实渠道选定后回调。
// actualChannelUID 已按与 SmartRouter 相同的规则补齐（缺失时为 ch_<index>）。
// 返回本次请求级决策 trace UID；为空表示没有可回填的 trace。
type CandidateSelectionObserver func(actualChannelUID string) string

// CandidateFilterProvider 根据请求 context、渠道类型和模型返回对应的 CandidateFilter
// 及其请求级选择回调。
// 用于 SmartRouter shadow 注入：main.go 注册后，SelectChannelWithOptions 自动调用。
type CandidateFilterProvider func(ctx context.Context, kind ChannelKind, model string) (CandidateFilterFunc, CandidateSelectionObserver)

// SetCandidateFilterProvider 设置全局候选过滤提供器。
// 由 main.go 在 autopilot SmartRouter 初始化后注册。
// provider 为 nil 时清除（恢复默认行为）。
// 注入点在 SelectionOptions.CandidateFilter 之后、X-Channel/ManualOverride/Promotion 之前。
func (s *ChannelScheduler) SetCandidateFilterProvider(provider CandidateFilterProvider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.candidateFilterProvider = provider
}

// ModelSupportResolverFunc 自动解析模型是否被某渠道支持。
// ctx 携带请求级画像，允许解析器在首次选渠前应用与 endpoint policy 一致的能力下界。
// 返回值:
//   - supported: 渠道是否支持该模型（由解析器裁决）
//   - actualModel: 解析后的实际模型名（为空时调用方使用原始 model）
//   - source: 解析来源标识（如 "manual_redirect" / "auto_resolve"）
//   - reason: 不支持时的原因（仅日志/诊断用）
//
// 由 main.go 在 autopilot ModelResolver 初始化后注册。
// 为 nil 时回退到 UpstreamConfig.ExplainModelSupport 原有路径（fail-open）。
type ModelSupportResolverFunc func(ctx context.Context, kind ChannelKind, upstream *config.UpstreamConfig, model string) (supported bool, actualModel string, source string, reason string)

// ModelSupportSourceAuthoritativeDeny 表示 resolver 已掌握该渠道的模型画像，
// supported=false 是权威拒绝，调度器不得再回退到空 SupportedModels 的“支持全部”语义。
const ModelSupportSourceAuthoritativeDeny = "resolver_authoritative_deny"

// SetModelSupportResolverProvider 设置模型支持解析提供器。
// 由 main.go 在 autopilot ModelResolver 初始化后注册。
// provider 为 nil 时清除（恢复默认行为：直接调用 UpstreamConfig.ExplainModelSupport）。
func (s *ChannelScheduler) SetModelSupportResolverProvider(fn ModelSupportResolverFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modelSupportResolverFunc = fn
}

// ContextWindowResolverFunc 把注册表窗口解析为该渠道×协议×模型的有效输入窗口。
// registryWindow 是注册表/渠道配置声明的窗口；返回 effective 应合成学习证据：
// 成功实证（放宽棘轮）、/v1/models 自报（声明）与实测 400 收紧上限。
// declared 返回实测收紧上限（0 = 无收紧证据）：溢出试探候选必须排除 declared 已
// 判定装不下的组合——那种试探只会在已知会 400 的渠道上浪费一次往返。
// effective 返回 0 或负数时调用方沿用 registryWindow（fail-open）。
type ContextWindowResolverFunc func(channelUID string, kind ChannelKind, actualModel string, registryWindow int) (effective int, declared int)

// OverflowCandidateProviderFunc 返回可承载 inputTokens 的跨协议重定向候选。
// 候选必须带 Route（指向目标协议渠道数组）与 ActualModel（执行模型），
// 可用性/优先级遍历由调度管线后续阶段兜底。返回空 = 无重定向（沿用容量错误）。
type OverflowCandidateProviderFunc func(ctx context.Context, kind ChannelKind, model string, inputTokens int) []ChannelInfo

// SetOverflowCandidateProvider 设置溢出重定向候选提供器。
// 由 main.go 在 autopilot Manager 初始化后注册；nil 时清除。
// 消费点：SelectChannelWithOptions 的上下文过滤全灭分支（试探候选也耗尽之后）。
func (s *ChannelScheduler) SetOverflowCandidateProvider(fn OverflowCandidateProviderFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overflowCandidateProvider = fn
}

// SetContextWindowResolverProvider 设置上下文有效窗口解析提供器。
// 由 main.go 注册（闭包读 config.SharedChannelCompatCache）。
// nil 时清除（恢复默认行为：只信注册表声明）。
// 消费点：filterChannelsByContext 与 ValidateUpstreamContext。
func (s *ChannelScheduler) SetContextWindowResolverProvider(fn ContextWindowResolverFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contextWindowResolverFunc = fn
}

// SetQuotaManager 注入配额管理器，用于配额饱和沉底排序。
// nil 时不参与排序（fail-open，不影响现有调度顺序）。
func (s *ChannelScheduler) SetQuotaManager(qm *quota.Manager) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.quotaManager = qm
}

// GetRateLimitManager 获取主动限速管理器
func (s *ChannelScheduler) GetRateLimitManager() *ratelimit.Manager {
	return s.rateLimitManager
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
	case ChannelKindVectors:
		return s.vectorsMetricsManager
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
	case ChannelKindVectors:
		return "openai"
	default:
		return "claude"
	}
}

func (s *ChannelScheduler) setChannelStatusByKind(index int, kind ChannelKind, status, reason string) error {
	upstream := s.getUpstreamByIndex(index, kind)
	oldStatus := ""
	channelUID := ""
	channelName := ""
	if upstream != nil {
		oldStatus = config.GetChannelStatus(upstream)
		channelUID = upstream.ChannelUID
		channelName = upstream.Name
	}

	var err error
	switch kind {
	case ChannelKindResponses:
		err = s.configManager.SetResponsesChannelStatus(index, status)
	case ChannelKindGemini:
		err = s.configManager.SetGeminiChannelStatus(index, status)
	case ChannelKindChat:
		err = s.configManager.SetChatChannelStatus(index, status)
	case ChannelKindImages:
		err = s.configManager.SetImagesChannelStatus(index, status)
	case ChannelKindVectors:
		err = s.configManager.SetVectorsChannelStatus(index, status)
	default:
		err = s.configManager.SetChannelStatus(index, status)
	}
	if err != nil {
		return err
	}

	if channelUID != "" {
		s.publishChannelStatusEvent(channelUID, channelName, string(kind), oldStatus, status, reason)
	}
	return nil
}
