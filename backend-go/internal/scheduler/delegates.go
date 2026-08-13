package scheduler

import (
	"context"
	"log"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/warmup"
)

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
	s.SetTraceAffinityForRequirement(userID, channelIndex, kind, nil)
}

// SetTraceAffinityForRequirement 设置带上下文桶隔离的 Trace 亲和。
func (s *ChannelScheduler) SetTraceAffinityForRequirement(userID string, channelIndex int, kind ChannelKind, requirement *ContextRequirement) {
	s.SetTraceAffinityRouteForRequirement(userID, ChannelRouteRef{Kind: string(kind), Index: channelIndex}, kind, requirement)
}

func (s *ChannelScheduler) SetTraceAffinityRouteForRequirement(userID string, route ChannelRouteRef, kind ChannelKind, requirement *ContextRequirement) {
	if userID != "" {
		compositeKey := traceAffinityKey(kind, userID, requirement)
		s.traceAffinity.SetPreferredRoute(compositeKey, route)
	}
}

// UpdateTraceAffinity 更新 Trace 亲和时间（续期，按 kind 隔离）
func (s *ChannelScheduler) UpdateTraceAffinity(userID string, kind ChannelKind) {
	if userID != "" {
		compositeKey := string(kind) + ":" + userID
		s.traceAffinity.UpdateLastUsed(compositeKey)
	}
}

// TrackConversation 追踪对话（请求成功后调用）
func (s *ChannelScheduler) TrackConversation(kind ChannelKind, userID, model string, channelIndex int, channelName, sessionID, lastUserMessage string, userMessageCount int, agentRole string, agentCtx *types.AgentContext) {
	s.TrackConversationWithMessages(kind, userID, model, channelIndex, channelName, sessionID, lastUserMessage, nil, userMessageCount, agentRole, agentCtx)
}

// TrackConversationWithMessages 追踪对话，并保存结构化用户轮次用于驾驶舱展示。
func (s *ChannelScheduler) TrackConversationWithMessages(kind ChannelKind, userID, model string, channelIndex int, channelName, sessionID, lastUserMessage string, lastUserMessages []string, userMessageCount int, agentRole string, agentCtx *types.AgentContext) {
	if s.conversationTracker != nil && userID != "" {
		s.conversationTracker.TrackWithMessages(string(kind), userID, model, channelIndex, channelName, sessionID, lastUserMessage, lastUserMessages, userMessageCount, agentRole, agentCtx)
	}
}

// TrackConversationWithStatus 追踪对话并指定状态，主要用于流式请求的进行中展示。
func (s *ChannelScheduler) TrackConversationWithStatus(kind ChannelKind, userID, model string, channelIndex int, channelName, sessionID, lastUserMessage string, userMessageCount int, agentRole, status string, agentCtx *types.AgentContext) {
	s.TrackConversationWithStatusAndMessages(kind, userID, model, channelIndex, channelName, sessionID, lastUserMessage, nil, userMessageCount, agentRole, status, agentCtx)
}

// TrackConversationWithStatusAndMessages 追踪流式对话状态，并保存结构化用户轮次。
func (s *ChannelScheduler) TrackConversationWithStatusAndMessages(kind ChannelKind, userID, model string, channelIndex int, channelName, sessionID, lastUserMessage string, lastUserMessages []string, userMessageCount int, agentRole, status string, agentCtx *types.AgentContext) {
	if s.conversationTracker != nil && userID != "" {
		s.conversationTracker.TrackWithStatusAndMessages(string(kind), userID, model, channelIndex, channelName, sessionID, lastUserMessage, lastUserMessages, userMessageCount, agentRole, status, agentCtx)
	}
}

func (s *ChannelScheduler) UpdateConversationStatus(kind ChannelKind, userID, status string) {
	if s.conversationTracker != nil && userID != "" {
		s.conversationTracker.UpdateStatus(string(kind), userID, status)
	}
}

func (s *ChannelScheduler) UpdateConversationStatusByID(conversationID, status string) bool {
	if s.conversationTracker == nil || conversationID == "" {
		return false
	}
	return s.conversationTracker.UpdateStatusByID(conversationID, status)
}

func (s *ChannelScheduler) UpdateConversationTitle(kind ChannelKind, userID, title string) bool {
	if s.conversationTracker == nil || userID == "" || title == "" {
		return false
	}
	return s.conversationTracker.UpdateTitle(string(kind), userID, title)
}

func (s *ChannelScheduler) UpdateConversationRecap(kind ChannelKind, userID, recap string) bool {
	if s.conversationTracker == nil || userID == "" || recap == "" {
		return false
	}
	return s.conversationTracker.UpdateRecap(string(kind), userID, recap)
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

// GetVectorsMetricsManager 获取 Vectors 指标管理器
func (s *ChannelScheduler) GetVectorsMetricsManager() *metrics.MetricsManager {
	return s.vectorsMetricsManager
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
	case ChannelKindVectors:
		return s.vectorsChannelLogStore
	default:
		return s.messagesChannelLogStore
	}
}

func (s *ChannelScheduler) GetMetricsManagerForRoute(route ChannelRouteRef) *metrics.MetricsManager {
	return s.getMetricsManager(ChannelKind(route.Kind))
}

func (s *ChannelScheduler) GetChannelLogStoreForRoute(route ChannelRouteRef) *metrics.ChannelLogStore {
	return s.GetChannelLogStore(ChannelKind(route.Kind))
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

// DeleteChannelLogs 删除渠道的独占日志数据（内存态）。
// 与 DeleteChannelMetrics 口径一致：仅删除不再被其他存活渠道引用的 metricsKey 对应的日志桶。
// 前置条件：调用此方法前，被删除的渠道应已从 config 中移除。
func (s *ChannelScheduler) DeleteChannelLogs(upstream *config.UpstreamConfig, kind ChannelKind) {
	if upstream == nil {
		return
	}

	prefix := kindSchedulerLogPrefix(kind)

	if s.isUpstreamInConfig(upstream, kind) {
		log.Printf("[%s-Delete] 警告: 渠道 %s 仍在配置中，删除日志可能不完整（应先从配置中移除）", prefix, upstream.Name)
	}

	deletedBaseURLs := upstream.GetAllBaseURLs()
	deletedKeys := append([]string{}, upstream.APIKeys...)
	deletedKeys = append(deletedKeys, upstream.HistoricalAPIKeys...)

	usedMetricsKeys := s.collectUsedMetricsKeys(kind)

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

	exclusiveMetricsKeys := make([]string, 0, len(exclusiveKeysSet))
	for key := range exclusiveKeysSet {
		exclusiveMetricsKeys = append(exclusiveMetricsKeys, key)
	}

	channelLogStore := s.GetChannelLogStore(kind)
	if channelLogStore != nil && len(exclusiveMetricsKeys) > 0 {
		channelLogStore.Remove(exclusiveMetricsKeys)
		log.Printf("[%s-Delete] 渠道 %s 的 %d 个独占日志桶已清理", prefix, upstream.Name, len(exclusiveMetricsKeys))
	} else {
		log.Printf("[%s-Delete] 渠道 %s 的日志数据被其他渠道共享，已保留", prefix, upstream.Name)
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
	case ChannelKindVectors:
		upstreams = cfg.VectorsUpstream
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
	case ChannelKindVectors:
		upstreams = cfg.VectorsUpstream
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

// GetFederatedCandidateCount 返回默认 Autopilot 路径可见的去重物理候选数。
// 显式 X-Channel/单渠道调用方不会使用该计数。
func (s *ChannelScheduler) GetFederatedCandidateCount(kind ChannelKind) int {
	seen := make(map[ChannelRouteKey]struct{})
	for _, ch := range s.getActiveChannelsWithTrace(context.Background(), kind, "", newSelectionTrace(SelectionOptions{Kind: kind})) {
		seen[ch.Route.Key()] = struct{}{}
	}
	if !s.protocolFederationEnabled(kind) {
		return len(seen)
	}
	cfg := s.configManager.GetConfig()
	var nativeUpstreams []config.UpstreamConfig
	switch kind {
	case ChannelKindResponses:
		nativeUpstreams = cfg.ResponsesUpstream
	default:
		nativeUpstreams = cfg.Upstream
	}
	for _, upstream := range nativeUpstreams {
		if upstream.AccountUID == "" || !upstream.AutoManaged {
			continue
		}
		for _, sibling := range s.protocolFederationSiblings(upstream.AccountUID, kind) {
			seen[sibling.Route.Key()] = struct{}{}
		}
	}
	return len(seen)
}

// GetFirstActiveChannelIndex 返回当前类型下第一个活跃渠道索引；无活跃渠道时回退到 0；无渠道时返回 -1。
func (s *ChannelScheduler) GetFirstActiveChannelIndex(kind ChannelKind) int {
	channels := s.getActiveChannels(kind, "")
	if len(channels) == 0 {
		return -1
	}
	return channels[0].Index
}

func (s *ChannelScheduler) recordLastSelectedChannel(kind ChannelKind, channelIndex int) {
	s.lastSelectedMu.Lock()
	defer s.lastSelectedMu.Unlock()
	s.lastSelectedChannels[kind] = channelIndex
}

// GetCurrentChannelIndex 返回最近一次成功选中的渠道；若尚无请求记录，则回退到首个活跃渠道。
func (s *ChannelScheduler) GetCurrentChannelIndex(kind ChannelKind) int {
	s.lastSelectedMu.RLock()
	channelIndex, ok := s.lastSelectedChannels[kind]
	s.lastSelectedMu.RUnlock()
	if ok {
		return channelIndex
	}
	return s.GetFirstActiveChannelIndex(kind)
}

// IsMultiChannelMode 判断是否为多渠道模式
func (s *ChannelScheduler) IsMultiChannelMode(kind ChannelKind) bool {
	return s.GetActiveChannelCount(kind) > 1
}

func (s *ChannelScheduler) GetConversationChannelsByKind(kind ChannelKind) []ChannelInfo {
	channels := s.getActiveChannels(kind, "")
	for i := range channels {
		upstream := s.getUpstreamByIndex(channels[i].Index, kind)
		if upstream != nil {
			channels[i].CircuitOpen = s.channelCircuitState(upstream, kind, "") == metrics.CircuitStateOpen
		}
	}
	return channels
}

// MaskUserIDForLog 掩码 user_id 供跨包日志使用。
func MaskUserIDForLog(userID string) string {
	if userID == "" {
		return ""
	}
	return maskUserID(userID)
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

func (s *ChannelScheduler) GetSortedURLsForRoute(route ChannelRouteRef, urls []string) []warmup.URLLatencyResult {
	return s.GetSortedURLsForChannel(ChannelKind(route.Kind), route.Index, urls)
}

// MarkURLSuccess 标记 URL 成功
func (s *ChannelScheduler) MarkURLSuccess(kind ChannelKind, channelIndex int, url string) {
	if s.urlManager != nil {
		s.urlManager.MarkSuccess(urlManagerChannelKey(kind, channelIndex), url)
	}
}

func (s *ChannelScheduler) MarkURLSuccessForRoute(route ChannelRouteRef, url string) {
	s.MarkURLSuccess(ChannelKind(route.Kind), route.Index, url)
}

// MarkURLFailure 标记 URL 失败，触发动态排序
func (s *ChannelScheduler) MarkURLFailure(kind ChannelKind, channelIndex int, url string) {
	if s.urlManager != nil {
		s.urlManager.MarkFailure(urlManagerChannelKey(kind, channelIndex), url)
	}
}

func (s *ChannelScheduler) MarkURLFailureForRoute(route ChannelRouteRef, url string) {
	s.MarkURLFailure(ChannelKind(route.Kind), route.Index, url)
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
	case ChannelKindVectors:
		return "Scheduler-Vectors"
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
	case ChannelKindVectors:
		return 5
	default:
		return 0
	}
}
