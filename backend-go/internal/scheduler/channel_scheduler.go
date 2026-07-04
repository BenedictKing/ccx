package scheduler

import (
	"sync"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/conversation"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/ratelimit"
	"github.com/BenedictKing/ccx/internal/session"
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
	vectorsMetricsManager    *metrics.MetricsManager // Vectors 渠道指标
	traceAffinity            *session.TraceAffinityManager
	urlManager               *warmup.URLManager       // URL 管理器（非阻塞，动态排序）
	messagesChannelLogStore  *metrics.ChannelLogStore // Messages 渠道请求日志
	responsesChannelLogStore *metrics.ChannelLogStore // Responses 渠道请求日志
	geminiChannelLogStore    *metrics.ChannelLogStore // Gemini 渠道请求日志
	chatChannelLogStore      *metrics.ChannelLogStore // Chat 渠道请求日志
	imagesChannelLogStore    *metrics.ChannelLogStore // Images 渠道请求日志
	vectorsChannelLogStore   *metrics.ChannelLogStore // Vectors 渠道请求日志
	conversationTracker      *conversation.ConversationTracker
	overrideManager          *conversation.OverrideManager
	rateLimitManager         *ratelimit.Manager
	loadShedMu               sync.Mutex
	loadShedStates           map[string]rateLimitLoadShedState
	loadShedStopCh           chan struct{}
	lastSelectedMu           sync.RWMutex
	lastSelectedChannels     map[ChannelKind]int
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
	case ChannelKindVectors:
		return s.configManager.SetVectorsChannelStatus(index, status)
	default:
		return s.configManager.SetChannelStatus(index, status)
	}
}
