package scheduler

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/conversation"
	"github.com/BenedictKing/ccx/internal/keypool"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/ratelimit"
)

const (
	rateLimitLoadShedHighWatermark  = 0.50
	rateLimitLoadShedLowWatermark   = 0.30
	rateLimitVisionReserveWatermark = 0.80
	rateLimitLoadShedRecovery       = 5 * time.Minute
)

type rateLimitLoadShedState struct {
	shedding bool
	lowSince time.Time
}

type softSkippedChannel struct {
	channel  ChannelInfo
	upstream *config.UpstreamConfig
	ratio    float64
	scope    string
}

func channelRouteRef(kind ChannelKind, index int, upstream *config.UpstreamConfig) ChannelRouteRef {
	route := ChannelRouteRef{Kind: string(kind), Index: index}
	if upstream != nil {
		route.ChannelUID = upstream.ChannelUID
	}
	return route
}

func normalizedChannelRoute(ch ChannelInfo, kind ChannelKind) ChannelRouteRef {
	route := ch.Route
	if route.Kind == "" {
		route.Kind = string(kind)
	}
	if route.ChannelUID == "" && route.Index == 0 && ch.Index != 0 {
		route.Index = ch.Index
	}
	return route
}

func channelInfoFailed(ch ChannelInfo, failedChannels map[int]bool, failedRoutes map[ChannelRouteKey]bool) bool {
	if failedRoutes != nil {
		return failedRoutes[ch.Route.Key()]
	}
	return failedChannels[ch.Index]
}

func stringListContains(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func (s *ChannelScheduler) protocolFederationEnabled(requestKind ChannelKind) bool {
	if s == nil || s.configManager == nil {
		return false
	}
	cfg := s.configManager.GetAutopilotRouting()
	federation := cfg.ProtocolFederation
	return federation.Enabled && federation.RequireSameAccount && federation.RequireAutoManaged &&
		stringListContains(federation.RequestKinds, string(requestKind))
}

func (s *ChannelScheduler) protocolFederationSiblings(accountUID string, requestKind ChannelKind) []ChannelInfo {
	if accountUID == "" || !s.protocolFederationEnabled(requestKind) {
		return nil
	}
	federation := s.configManager.GetAutopilotRouting().ProtocolFederation
	cfg := s.configManager.GetConfig()
	indexByUID := func(kind string, upstream config.UpstreamConfig) int {
		var upstreams []config.UpstreamConfig
		switch ChannelKind(kind) {
		case ChannelKindChat:
			upstreams = cfg.ChatUpstream
		case ChannelKindResponses:
			upstreams = cfg.ResponsesUpstream
		default:
			return -1
		}
		for i := range upstreams {
			if upstream.ChannelUID != "" && upstreams[i].ChannelUID == upstream.ChannelUID {
				return i
			}
		}
		return -1
	}
	seen := make(map[ChannelRouteKey]struct{})
	result := make([]ChannelInfo, 0, 2)
	for _, sibling := range s.configManager.GetAccountChannels(accountUID) {
		if !stringListContains(federation.ExecutionKinds, sibling.Kind) || !sibling.Upstream.AutoManaged {
			continue
		}
		status := sibling.Upstream.Status
		if status == "" {
			status = "active"
		}
		if status != "active" || !channelHasSelectableKey(&sibling.Upstream) || sibling.Upstream.RoutePrefix != "" {
			continue
		}
		index := indexByUID(sibling.Kind, sibling.Upstream)
		if index < 0 {
			continue
		}
		route := channelRouteRef(ChannelKind(sibling.Kind), index, &sibling.Upstream)
		if _, ok := seen[route.Key()]; ok {
			continue
		}
		seen[route.Key()] = struct{}{}
		priority := sibling.Upstream.Priority
		if priority == 0 {
			priority = index
		}
		result = append(result, ChannelInfo{
			Route:             route,
			Index:             index,
			Name:              sibling.Upstream.Name,
			Priority:          priority,
			Status:            status,
			ProtocolFidelity:  "converted",
			ConversionPenalty: federation.ConversionPenalty,
		})
	}
	return result
}

// federateDefaultCandidates 在默认 Autopilot 路径上追加同账号托管 sibling 物理路由。
// 每个 sibling 都按自身执行协议做模型解析、可用性、模型熔断与上下文校验，
// 并按 ChannelRouteRef.Key() 去重，避免同一物理渠道重复进入候选集合。
func (s *ChannelScheduler) federateDefaultCandidates(ctx context.Context, requestKind ChannelKind, channels []ChannelInfo, model string, requirement *ContextRequirement, trace *SelectionTrace) []ChannelInfo {
	if !s.protocolFederationEnabled(requestKind) {
		return channels
	}
	seen := make(map[ChannelRouteKey]struct{}, len(channels))
	accountUIDs := make(map[string]struct{})
	for i := range channels {
		channels[i].ProtocolFidelity = "native"
		seen[channels[i].Route.Key()] = struct{}{}
		if upstream := s.getUpstreamByRoute(channels[i].Route); upstream != nil && upstream.AutoManaged && upstream.AccountUID != "" {
			accountUIDs[upstream.AccountUID] = struct{}{}
		}
	}
	for accountUID := range accountUIDs {
		for _, sibling := range s.protocolFederationSiblings(accountUID, requestKind) {
			if _, ok := seen[sibling.Route.Key()]; ok {
				continue
			}
			upstream := s.getUpstreamByRoute(sibling.Route)
			kind := ChannelKind(sibling.Route.Kind)
			if upstream == nil || !s.channelAvailableForCandidateFilter(sibling, upstream, kind, "") {
				continue
			}
			actualModel := model
			if s.modelSupportResolverFunc != nil {
				supported, resolvedModel, _, _ := s.modelSupportResolverFunc(ctx, kind, upstream, model)
				if !supported || resolvedModel == "" {
					trace.skipChannel(sibling, "protocol_federation", "unsupported_model", model)
					continue
				}
				actualModel = resolvedModel
			} else if supported, reason := s.resolveModelSupport(ctx, kind, upstream, model); !supported {
				trace.skipChannel(sibling, "protocol_federation", "unsupported_model", reason)
				continue
			}
			sibling.ActualModel = actualModel
			if s.channelModelCircuitOpenByRoute(upstream, sibling.Route, actualModel) {
				trace.skipChannel(sibling, "protocol_federation", "model_circuit_open", actualModel)
				continue
			}
			if err := s.ValidateUpstreamContext(kind, actualModel, upstream, requirement); err != nil {
				trace.skipChannel(sibling, "protocol_federation", "context_window_exceeded", actualModel)
				continue
			}
			seen[sibling.Route.Key()] = struct{}{}
			channels = append(channels, sibling)
		}
	}
	return channels
}

func (s *ChannelScheduler) SelectChannel(
	ctx context.Context,
	userID string,
	failedChannels map[int]bool,
	kind ChannelKind,
	model string,
	routePrefix string,
	channelName string,
) (*SelectionResult, error) {
	return s.SelectChannelWithOptions(ctx, SelectionOptions{
		UserID:         userID,
		FailedChannels: failedChannels,
		Kind:           kind,
		Model:          model,
		RoutePrefix:    routePrefix,
		ChannelName:    channelName,
	})
}

func (s *ChannelScheduler) SelectChannelWithOptions(ctx context.Context, opts SelectionOptions) (*SelectionResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	userID := opts.UserID
	// subagent 使用隔离的亲和 key，避免被主对话亲和拉到贵渠道；
	// 这样 subagent 首次请求走 priority 排序选便宜渠道，后续命中自己的亲和复用缓存。
	affinityUserID := userID
	if opts.AgentRole == "subagent" {
		affinityUserID = userID + ":subagent"
	}
	failedChannels := opts.FailedChannels
	if failedChannels == nil {
		failedChannels = map[int]bool{}
	}
	failedRoutes := opts.FailedRoutes
	kind := opts.Kind
	model := opts.Model
	routePrefix := opts.RoutePrefix
	channelName := opts.ChannelName
	trace := newSelectionTrace(opts)
	traceErr := func(err error) error {
		return newSelectionTraceError(err, trace)
	}

	// 若 opts.SmartFilter 未显式设置但全局 provider 已注册，自动注入。
	// 这样 handler 不需要感知 SmartRouter，由 main.go 统一注册。
	var candidateSelectionObserver CandidateSelectionObserver
	if opts.SmartFilter == nil && s.candidateFilterProvider != nil {
		opts.SmartFilter, candidateSelectionObserver = s.buildSmartFilterFromProvider(ctx, kind, model)
	}

	var activeChannels []ChannelInfo
	candidateCount := 0
	finish := func(upstream *config.UpstreamConfig, channelIndex int, reason string) *SelectionResult {
		// 执行 kind/模型必须来自候选自身的物理路由，否则联邦 sibling 会被误记为请求协议。
		executionKind := kind
		executionModel := ""
		if upstream != nil {
			for _, ch := range activeChannels {
				route := normalizedChannelRoute(ch, kind)
				if route.Index != channelIndex {
					continue
				}
				if upstream.ChannelUID != "" && route.ChannelUID != "" && route.ChannelUID != upstream.ChannelUID {
					continue
				}
				executionKind = ChannelKind(route.Kind)
				executionModel = ch.ActualModel
				break
			}
		}
		result := s.selectionResultWithRecord(executionKind, upstream, channelIndex, reason, !opts.DryRun)
		result.CandidateCount = candidateCount
		result.ExecutionModel = executionModel
		if !opts.DryRun && candidateSelectionObserver != nil {
			actualChannelUID := fmt.Sprintf("ch_%d", channelIndex)
			if upstream != nil && upstream.ChannelUID != "" {
				actualChannelUID = upstream.ChannelUID
			}
			result.AutopilotTraceUID = candidateSelectionObserver(actualChannelUID)
		}
		channelName := ""
		if upstream != nil {
			channelName = upstream.Name
		}
		trace.selectChannel(result.Route, channelName, reason)
		result.Trace = trace
		return result
	}

	// 获取活跃渠道列表（含模型过滤）
	activeChannels = s.getActiveChannelsWithTrace(ctx, kind, model, trace)
	trace.setStage("active_model_filter", len(activeChannels))
	if len(activeChannels) == 0 {
		// 区分"无活跃渠道"和"无渠道支持该模型"
		kindName := "Messages"
		switch kind {
		case ChannelKindGemini:
			kindName = "Gemini"
		case ChannelKindResponses:
			kindName = "Responses"
		case ChannelKindChat:
			kindName = "Chat"
		case ChannelKindImages:
			kindName = "Images"
		case ChannelKindVectors:
			kindName = "Vectors"
		}
		if model != "" && len(s.getActiveChannels(kind, "")) > 0 {
			return nil, traceErr(fmt.Errorf("没有 %s 渠道支持模型 %q，请检查渠道的 supportedModels 配置", kindName, model))
		}
		return nil, traceErr(fmt.Errorf("没有可用的活跃 %s 渠道", kindName))
	}

	// 按路由前缀过滤渠道
	if routePrefix != "" {
		// 有前缀：仅选择匹配的渠道
		var filtered []ChannelInfo
		for _, ch := range activeChannels {
			upstream := s.getUpstreamByRoute(normalizedChannelRoute(ch, kind))
			if upstream != nil && upstream.RoutePrefix == routePrefix {
				filtered = append(filtered, ch)
			} else {
				details := ""
				if upstream != nil {
					details = upstream.RoutePrefix
				}
				trace.skipChannel(ch, "route_prefix_filter", "route_prefix_mismatch", details)
			}
		}
		if len(filtered) == 0 {
			trace.setStage("route_prefix_filter", 0)
			return nil, traceErr(fmt.Errorf("no channels with route prefix: %s", routePrefix))
		}
		activeChannels = filtered
		trace.setStage("route_prefix_filter", len(activeChannels))
	} else {
		// 无前缀：排除设了路由前缀的渠道（它们只能通过前缀访问）
		var filtered []ChannelInfo
		for _, ch := range activeChannels {
			upstream := s.getUpstreamByRoute(normalizedChannelRoute(ch, kind))
			if upstream != nil && upstream.RoutePrefix == "" {
				filtered = append(filtered, ch)
			} else {
				details := ""
				if upstream != nil {
					details = upstream.RoutePrefix
				}
				trace.skipChannel(ch, "default_route_filter", "route_prefix_only", details)
			}
		}
		if len(filtered) == 0 {
			kindName := "Messages"
			switch kind {
			case ChannelKindGemini:
				kindName = "Gemini"
			case ChannelKindResponses:
				kindName = "Responses"
			case ChannelKindChat:
				kindName = "Chat"
			case ChannelKindImages:
				kindName = "Images"
			case ChannelKindVectors:
				kindName = "Vectors"
			}
			trace.setStage("default_route_filter", 0)
			return nil, traceErr(fmt.Errorf("没有可用于默认路由的 %s 渠道，请使用带前缀路由访问", kindName))
		}
		activeChannels = filtered
		trace.setStage("default_route_filter", len(activeChannels))
	}

	activeChannels, err := s.filterChannelsByContext(activeChannels, kind, model, opts.ContextRequirement, trace)
	if err != nil {
		trace.setStage("context_filter", 0)
		return nil, traceErr(err)
	}
	trace.setStage("context_filter", len(activeChannels))

	// 在进入 SmartRouter、亲和与优先级排序前剔除没有可选 Key 的渠道。
	// 这是基础可用性约束，不属于模型重定向或自动路由决策：
	// 已持久禁用、enabled=false 或空 Key 渠道即使被选中，后续也只会立即 failover。
	activeChannels = s.filterChannelsByKeyAvailability(activeChannels, kind, trace)
	trace.setStage("key_availability_filter", len(activeChannels))
	if len(activeChannels) == 0 {
		return nil, traceErr(fmt.Errorf("没有具有可用 API Key 的 %s 渠道", kindDisplayName(kind)))
	}

	if opts.CandidateFilter != nil {
		beforeFilter := append([]ChannelInfo(nil), activeChannels...)
		activeChannels, err = opts.CandidateFilter(activeChannels, func(ch ChannelInfo) *config.UpstreamConfig {
			return s.getUpstreamByRoute(normalizedChannelRoute(ch, kind))
		}, func(ch ChannelInfo, upstream *config.UpstreamConfig) bool {
			return s.channelAvailableForCandidateFilter(ch, upstream, kind, "")
		})
		if err != nil {
			return nil, traceErr(err)
		}
		traceCandidateFilterSkips(beforeFilter, activeChannels, trace)
		if len(activeChannels) == 0 {
			trace.setStage("candidate_filter", 0)
			return nil, traceErr(fmt.Errorf("没有可用的 %s 渠道满足候选过滤条件", kindDisplayName(kind)))
		}
		trace.setStage("candidate_filter", len(activeChannels))
	}

	// 指定渠道名（X-Channel 头）：显式控制优先于 SmartFilter。
	if channelName != "" {
		for _, ch := range activeChannels {
			if ch.Name == channelName {
				if channelInfoFailed(ch, failedChannels, failedRoutes) {
					trace.skipChannel(ch, "channel_pin", "failed_in_request", "")
					return nil, traceErr(fmt.Errorf("指定渠道 %q 在本次请求中已失败", channelName))
				}
				upstream := s.getUpstreamByRoute(normalizedChannelRoute(ch, kind))
				if upstream == nil {
					trace.skipChannel(ch, "channel_pin", "missing_upstream", "")
					return nil, traceErr(fmt.Errorf("指定渠道 %q 配置异常", channelName))
				}
				prefix := kindSchedulerLogPrefix(kind)
				log.Printf("[%s-Pin] 通过 X-Channel 指定渠道: [%d] %s", prefix, ch.Index, ch.Name)
				return finish(upstream, ch.Index, "channel_pin"), nil
			}
		}
		for _, ch := range activeChannels {
			trace.skipChannel(ch, "channel_pin", "channel_name_mismatch", ch.Name)
		}
		return nil, traceErr(fmt.Errorf("指定渠道 %q 不满足当前模型、路由前缀或上下文要求", channelName))
	}

	// 0. 检查手动序列覆盖
	if userID != "" && s.overrideManager != nil {
		if sequence, ok := s.overrideManager.GetOverrideForUserWithRole(string(kind), userID, opts.AgentRole); ok {
			prefix := kindSchedulerLogPrefix(kind)
			orderedChannels := applyManualOverrideOrder(activeChannels, sequence)
			for _, ch := range orderedChannels {
				if channelInfoFailed(ch, failedChannels, failedRoutes) {
					trace.skipChannel(ch, "manual_override", "failed_in_request", "")
					continue
				}
				if ch.Status != "active" {
					trace.skipChannel(ch, "manual_override", "inactive_status", ch.Status)
					continue
				}
				upstream := s.getUpstreamByRoute(normalizedChannelRoute(ch, kind))
				if upstream != nil && s.channelIsRuntimeAvailable(upstream, kind, ch.Index, "") {
					log.Printf("[%s-Override] 按手动排序选择渠道: [%d] %s (user: %s, role=%s, sequenceHead=%s)", prefix, ch.Index, ch.Name, maskUserID(userID), schedulerAgentRoleForLog(opts.AgentRole), formatOverrideSequenceHead(sequence, 3))
					// Idle 续期：对话活跃时延长 override TTL
					if !opts.DryRun {
						s.overrideManager.RefreshOverrideForUser(string(kind), userID)
					}
					return finish(upstream, ch.Index, "manual_override"), nil
				}
				if upstream == nil {
					trace.skipChannel(ch, "manual_override", "missing_upstream", "")
				} else {
					trace.skipChannel(ch, "manual_override", "runtime_unavailable", "")
				}
			}
			log.Printf("[%s-Override] 手动排序序列中无当前可用渠道，保留排序并回退默认调度 (user: %s, role=%s, sequenceHead=%s)", prefix, maskUserID(userID), schedulerAgentRoleForLog(opts.AgentRole), formatOverrideSequenceHead(sequence, 3))
		}
	}

	// 1. 检查促销期渠道（手动覆盖之后，绕过健康检查）
	promotedChannel := s.findPromotedChannel(activeChannels, kind)
	if promotedChannel != nil && !channelInfoFailed(*promotedChannel, failedChannels, failedRoutes) {
		// 促销渠道存在且未失败，直接使用（不检查健康状态，让用户设置的促销渠道有机会尝试）
		upstream := s.getUpstreamByIndex(promotedChannel.Index, kind)
		if channelHasSelectableKey(upstream) && !s.channelInRuntimeCooldown(kind, promotedChannel.Index) {
			failureRate := s.channelFailureRate(upstream, kind, model)
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Promotion] 促销期优先选择渠道: [%d] %s (失败率: %.1f%%, 绕过健康检查)", prefix, promotedChannel.Index, upstream.Name, failureRate*100)
			return finish(upstream, promotedChannel.Index, "promotion_priority"), nil
		} else if upstream != nil {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Promotion] 警告: 促销渠道 [%d] %s 无可用密钥，跳过", prefix, promotedChannel.Index, upstream.Name)
			trace.skipChannel(*promotedChannel, "promotion", "no_available_keys_or_cooldown", "")
		}
	} else if promotedChannel != nil {
		prefix := kindSchedulerLogPrefix(kind)
		log.Printf("[%s-Promotion] 警告: 促销渠道 [%d] %s 已在本次请求中失败，跳过", prefix, promotedChannel.Index, promotedChannel.Name)
		trace.skipChannel(*promotedChannel, "promotion", "failed_in_request", "")
	}

	// 仅默认 Autopilot 路径加入同账号托管 sibling；显式 X-Channel、手动覆盖和促销已在此前返回。
	if (kind == ChannelKindMessages || kind == ChannelKindResponses) && routePrefix == "" && channelName == "" && opts.SmartFilter != nil {
		activeChannels = s.federateDefaultCandidates(ctx, kind, activeChannels, model, opts.ContextRequirement, trace)
		trace.setStage("protocol_federation", len(activeChannels))
	}

	// SmartFilter 注入点（设计 §4.6.3 / §4.6.5：显式控制之后、默认调度之前）。
	// X-Channel / ManualOverride / Promotion 均在 SmartFilter 之前执行，
	// 确保显式用户意图不受 SmartRouter 过滤影响。
	// shadow 模式：记录 RoutingDecisionTrace，返回原始列表（不影响真实调度）。
	if opts.SmartFilter != nil {
		filtered := opts.SmartFilter(ctx, activeChannels)
		if len(filtered) > 0 {
			activeChannels = filtered
		}
		// len(filtered)==0 时保留原列表，避免 SmartFilter bug 阻断全部调度
		trace.setStage("smart_filter", len(activeChannels))
	}
	// 联邦后的去重物理候选数：failover 外壳用它作为 route-aware 的尝试上限。
	candidateCount = len(activeChannels)

	// 渠道-模型级运行时熔断：剔除该模型在全部 Key 上都处于隔离期的渠道。
	// 与 SmartFilter 同属自动过滤，位置同样在显式控制（X-Channel / ManualOverride /
	// Promotion）之后——用户显式 pin 到故障渠道时应当照办并让真实错误返回，
	// 由自动调度接管时才规避。同样 fail-open：全部熔断时保留原列表。
	activeChannels = s.filterChannelsByModelCircuit(activeChannels, kind, model, trace)
	trace.setStage("model_circuit_filter", len(activeChannels))

	// 1. 检查 Trace 亲和性（促销渠道失败时或无促销渠道时）
	if userID != "" {
		compositeKey := traceAffinityKey(kind, affinityUserID, opts.ContextRequirement)
		if preferredRoute, ok := s.traceAffinity.GetPreferredRoute(compositeKey, string(kind)); ok {
			preferredIdx := preferredRoute.Index
			bestPriority := s.findBestAvailableChannelPriorityWithRoutes(activeChannels, failedChannels, failedRoutes, kind, model)
			for _, ch := range activeChannels {
				if ch.Route.Matches(preferredRoute) && !channelInfoFailed(ch, failedChannels, failedRoutes) {
					// 检查渠道状态：只有 active 状态才使用亲和性
					if ch.Status != "active" {
						prefix := kindSchedulerLogPrefix(kind)
						log.Printf("[%s-Affinity] 跳过亲和渠道 [%d] %s: 状态为 %s (user: %s)", prefix, preferredIdx, ch.Name, ch.Status, maskUserID(userID))
						trace.skipChannel(ch, "trace_affinity", "inactive_status", ch.Status)
						continue
					}
					// 如果存在更高优先级且健康的候选渠道，允许优先级覆盖亲和性
					if bestPriority >= 0 && ch.Priority > bestPriority {
						prefix := kindSchedulerLogPrefix(kind)
						log.Printf("[%s-Affinity] 跳过亲和渠道 [%d] %s: 存在更高优先级可用渠道 (亲和优先级: %d, 最优优先级: %d, user: %s)", prefix, preferredIdx, ch.Name, ch.Priority, bestPriority, maskUserID(userID))
						trace.skipChannel(ch, "trace_affinity", "better_priority_available", fmt.Sprintf("affinity=%d best=%d", ch.Priority, bestPriority))
						continue
					}
					// 检查渠道是否健康且未处于运行态冷却
					upstream := s.getUpstreamByRoute(ch.Route)
					// 模型级熔断的亲和性防线：正常情况下熔断渠道已被
					// filterChannelsByModelCircuit 从 activeChannels 剔除、走不到这里；
					// 但该过滤 fail-open，全渠道熔断时会保留原列表。此时长会话的亲和性
					// 会持续粘在故障组合上（本机制要解决的正是这种放大），故显式再判一次。
					if s.channelModelCircuitOpenByRoute(upstream, ch.Route, model) {
						prefix := kindSchedulerLogPrefix(kind)
						log.Printf("[%s-Affinity] 跳过亲和渠道 [%d] %s: 模型 %q 处于熔断隔离期 (user: %s)",
							prefix, preferredIdx, ch.Name, model, maskUserID(userID))
						trace.skipChannel(ch, "trace_affinity", "model_circuit_open", model)
						continue
					}
					if upstream != nil && s.channelIsRuntimeAvailable(upstream, kind, preferredIdx, "") {
						prefix := kindSchedulerLogPrefix(kind)
						log.Printf("[%s-Affinity] Trace亲和选择渠道: [%d] %s (user: %s)", prefix, preferredIdx, upstream.Name, maskUserID(userID))
						return finish(upstream, preferredIdx, "trace_affinity"), nil
					}
					if upstream == nil {
						trace.skipChannel(ch, "trace_affinity", "missing_upstream", "")
					} else {
						trace.skipChannel(ch, "trace_affinity", "runtime_unavailable", "")
					}
				}
			}
		}
	}

	// 2. 按优先级遍历活跃渠道
	softSkipped := make([]softSkippedChannel, 0)
	quotaSunk := make([]softSkippedChannel, 0) // 配额饱和沉底列表：饱和但不剔除，排到非饱和之后
	for _, ch := range activeChannels {
		// 跳过本次请求已经失败的渠道
		if channelInfoFailed(ch, failedChannels, failedRoutes) {
			trace.skipChannel(ch, "priority_order", "failed_in_request", "")
			continue
		}

		// 跳过非 active 状态的渠道（suspended 等）
		if ch.Status != "active" {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Channel] 跳过非活跃渠道: [%d] %s (状态: %s)", prefix, ch.Index, ch.Name, ch.Status)
			trace.skipChannel(ch, "priority_order", "inactive_status", ch.Status)
			continue
		}

		route := normalizedChannelRoute(ch, kind)
		executionKind := ChannelKind(route.Kind)
		upstream := s.getUpstreamByRoute(route)
		if !channelHasSelectableKey(upstream) {
			trace.skipChannel(ch, "priority_order", "missing_upstream_or_keys", "")
			continue
		}
		attemptModel := model
		if ch.ActualModel != "" {
			attemptModel = ch.ActualModel
		}

		// 跳过失败率过高的渠道（已熔断或即将熔断）；联邦 sibling 按其执行协议读运行态。
		channelState := s.channelCircuitState(upstream, executionKind, "")
		if channelState == metrics.CircuitStateOpen || !s.channelIsHealthy(upstream, executionKind, "") {
			failureRate := s.channelFailureRate(upstream, executionKind, "")
			prefix := kindSchedulerLogPrefix(executionKind)
			if channelState == metrics.CircuitStateOpen {
				log.Printf("[%s-Channel] 警告: 跳过 open 渠道: [%d] %s (失败率: %.1f%%)", prefix, ch.Index, ch.Name, failureRate*100)
				trace.skipChannel(ch, "priority_order", "circuit_open", fmt.Sprintf("failureRate=%.1f%%", failureRate*100))
			} else {
				log.Printf("[%s-Channel] 警告: 跳过不健康渠道: [%d] %s (失败率: %.1f%%)", prefix, ch.Index, ch.Name, failureRate*100)
				trace.skipChannel(ch, "priority_order", "unhealthy", fmt.Sprintf("failureRate=%.1f%%", failureRate*100))
			}
			continue
		}

		// 跳过运行态 cooldown 中的渠道（如 429 Retry-After 或上游账号池临时不可用）
		if s.channelInRuntimeCooldownByRoute(route) {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Channel] 跳过运行态 cooldown 中的渠道: [%d] %s", prefix, ch.Index, ch.Name)
			trace.skipChannel(ch, "priority_order", "runtime_cooldown", "")
			continue
		}

		if deferred, ratio, scope, cooldown := s.channelRateLimitSoftDeferred(upstream, executionKind, route.Index, attemptModel, time.Now()); deferred {
			prefix := kindSchedulerLogPrefix(kind)
			if cooldown {
				log.Printf("[%s-RateLimit] 软跳过高水位渠道: [%d] %s scope=%s usage=%.0f%% cooldown (high=%.0f%%, recover<%.0f%%/%s)",
					prefix, ch.Index, upstream.Name, scope, ratio*100, rateLimitLoadShedHighWatermark*100, rateLimitLoadShedLowWatermark*100, rateLimitLoadShedRecovery)
			} else {
				log.Printf("[%s-RateLimit] 软跳过高水位渠道: [%d] %s scope=%s usage=%.0f%% (high=%.0f%%, recover<%.0f%%/%s)",
					prefix, ch.Index, upstream.Name, scope, ratio*100, rateLimitLoadShedHighWatermark*100, rateLimitLoadShedLowWatermark*100, rateLimitLoadShedRecovery)
			}
			softSkipped = append(softSkipped, softSkippedChannel{
				channel:  ch,
				upstream: upstream,
				ratio:    ratio,
				scope:    scope,
			})
			reason := "rate_limit_pressure"
			if cooldown {
				reason = "rate_limit_cooldown"
			}
			trace.skipChannel(ch, "priority_order", reason, fmt.Sprintf("scope=%s usage=%.0f%%", scope, ratio*100))
			continue
		}

		// 配额饱和沉底：配额接近耗尽或已耗尽的渠道沉到非饱和渠道之后，
		// 不剔除（fail-open）。全员饱和时全体回候选。
		if s.quotaManager != nil && upstream.ChannelUID != "" {
			if s.quotaManager.IsChannelSaturated(upstream.ChannelUID, time.Now().UnixMilli()) {
				prefix := kindSchedulerLogPrefix(kind)
				truth := s.quotaManager.GetChannelTruth(upstream.ChannelUID)
				log.Printf("[%s-Quota] 配额饱和沉底: [%d] %s truth=%s (非饱和渠道耗尽后回退)",
					prefix, ch.Index, upstream.Name, truth)
				quotaSunk = append(quotaSunk, softSkippedChannel{
					channel:  ch,
					upstream: upstream,
					ratio:    1.0,
					scope:    string(truth),
				})
				trace.skipChannel(ch, "priority_order", "quota_saturated", string(truth))
				continue
			}
		}

		if shouldReserveVisionChannelForText(kind, opts.HasImageContent, upstream, softSkipped) {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Vision] 文本请求保留可识图渠道: [%d] %s (待普通文本渠道水位 >= %.0f%% 后再作为溢出池)",
				prefix, ch.Index, upstream.Name, rateLimitVisionReserveWatermark*100)
			trace.skipChannel(ch, "priority_order", "vision_reserved_for_image", "")
			continue
		}

		prefix := kindSchedulerLogPrefix(kind)
		log.Printf("[%s-Channel] 选择渠道: [%d] %s (优先级: %d)", prefix, ch.Index, upstream.Name, ch.Priority)
		return finish(upstream, ch.Index, "priority_order"), nil
	}

	for _, skipped := range softSkipped {
		if skipped.upstream == nil || !s.channelIsRuntimeAvailable(skipped.upstream, kind, skipped.channel.Index, "") {
			continue
		}
		prefix := kindSchedulerLogPrefix(kind)
		log.Printf("[%s-RateLimit] 所有低水位候选不可用，回退选择高水位渠道: [%d] %s scope=%s usage=%.0f%%",
			prefix, skipped.channel.Index, skipped.upstream.Name, skipped.scope, skipped.ratio*100)
		return finish(skipped.upstream, skipped.channel.Index, "rate_limit_pressure"), nil
	}

	// 配额饱和回退：所有非饱和渠道不可用时，尝试配额饱和的渠道（fail-open）。
	// 确保不会因为配额数据缺失或不准确而阻断全部调度。
	for _, sunk := range quotaSunk {
		if sunk.upstream == nil || !s.channelIsRuntimeAvailable(sunk.upstream, kind, sunk.channel.Index, "") {
			continue
		}
		prefix := kindSchedulerLogPrefix(kind)
		log.Printf("[%s-Quota] 所有非饱和渠道不可用，回退选择配额饱和渠道: [%d] %s truth=%s",
			prefix, sunk.channel.Index, sunk.upstream.Name, sunk.scope)
		return finish(sunk.upstream, sunk.channel.Index, "quota_saturated_fallback"), nil
	}

	// 3. 所有健康渠道都失败，选择失败率最低的作为降级
	result, err := s.selectFallbackChannelWithRouteRecord(activeChannels, failedChannels, failedRoutes, kind, !opts.DryRun)
	if result != nil {
		channelName := ""
		if result.Upstream != nil {
			channelName = result.Upstream.Name
		}
		trace.selectChannel(result.Route, channelName, result.Reason)
		result.Trace = trace
	}
	if err != nil {
		err = traceErr(err)
	}
	return result, err
}

// channelAvailableForCandidateFilter 判断候选在其自身执行协议下是否可用。
// 联邦 sibling 的 cooldown/熔断必须按物理路由的 kind 读取，不能按请求协议判断。
func (s *ChannelScheduler) channelAvailableForCandidateFilter(ch ChannelInfo, upstream *config.UpstreamConfig, kind ChannelKind, model string) bool {
	if ch.Status != "active" || !channelHasSelectableKey(upstream) {
		return false
	}
	route := normalizedChannelRoute(ch, kind)
	executionKind := ChannelKind(route.Kind)
	if s.channelInRuntimeCooldownByRoute(route) {
		return false
	}
	return s.channelCircuitState(upstream, executionKind, "") != metrics.CircuitStateOpen
}

// channelModelCircuitOpen 判断渠道下该模型是否已在所有 Key 上熔断。
// 供渠道级过滤与 Trace 亲和性复用，确保两条路径判定一致。
func (s *ChannelScheduler) channelModelCircuitOpen(upstream *config.UpstreamConfig, kind ChannelKind, model string) bool {
	if upstream == nil || model == "" || upstream.ChannelUID == "" {
		return false
	}
	mm := s.getMetricsManager(kind)
	if mm == nil {
		return false
	}
	tracker := mm.ModelCircuit()
	if tracker == nil {
		return false
	}
	// 只统计实际可用的 Key，不能用 upstream.APIKeys 全量：已 blacklist、
	// enabled=false 或被 per-key 白名单排除的 Key 永远不会有失败记录，把它们算进
	// "是否全部熔断"会让渠道级排除几乎永不触发——渠道有 5 把 Key、4 把已拉黑时，
	// 唯一在用的那把熔断后仍会被判为渠道健康。
	// CandidatesForModel 已封装全部可用性规则，这里传空 model 与 channelHasSelectableKey
	// 保持一致（选渠道阶段尚未应用渠道级 RedirectModel，per-key 白名单交由请求路径精确执行）。
	candidates := keypool.CandidatesForModel(upstream, nil, "")
	if len(candidates) == 0 {
		// 无可用 Key 时交由 key_availability_filter 处理，此处不重复判定。
		return false
	}
	keyHashes := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if key := strings.TrimSpace(candidate.APIKey); key != "" {
			keyHashes = append(keyHashes, metrics.ModelCircuitKeyHash(key))
		}
	}
	return tracker.ChannelModelCircuitOpen(upstream.ChannelUID, keyHashes, model)
}

// filterChannelsByModelCircuit 排除该模型已在全部 Key 上熔断的渠道。
//
// 与 active_model_filter 的区别：后者读静态 supportedModels 配置，这里读运行时健康度。
// 只有渠道下所有 Key 对该模型都处于隔离期才排除；任一 Key 可用时保留渠道，
// 让 Key 级过滤（keypool.CandidatesForModelFiltered）去命中那把健康的 Key。
//
// fail-open：过滤后为空时返回原列表。全渠道同时熔断多半意味着上游或网络整体异常，
// 此时应让请求带着真实的上游错误返回，而不是退化成"没有可用渠道"掩盖真正的故障原因。
func (s *ChannelScheduler) filterChannelsByModelCircuit(channels []ChannelInfo, kind ChannelKind, model string, trace *SelectionTrace) []ChannelInfo {
	if model == "" || len(channels) == 0 {
		return channels
	}

	filtered := make([]ChannelInfo, 0, len(channels))
	skipped := make([]ChannelInfo, 0)
	for _, ch := range channels {
		route := normalizedChannelRoute(ch, kind)
		upstream := s.getUpstreamByRoute(route)
		executionKind := ChannelKind(route.Kind)
		actualModel := model
		if ch.ActualModel != "" {
			actualModel = ch.ActualModel
		}
		if s.channelModelCircuitOpen(upstream, executionKind, actualModel) {
			skipped = append(skipped, ch)
			continue
		}
		filtered = append(filtered, ch)
	}

	if len(filtered) == 0 {
		return channels
	}
	for _, ch := range skipped {
		prefix := kindSchedulerLogPrefix(kind)
		log.Printf("[%s-ModelCircuit] 跳过渠道 [%d] %s: 模型 %q 在全部 Key 上处于熔断隔离期",
			prefix, ch.Index, ch.Name, model)
		trace.skipChannel(ch, "model_circuit_filter", "model_circuit_open", model)
	}
	return filtered
}

func (s *ChannelScheduler) filterChannelsByKeyAvailability(channels []ChannelInfo, kind ChannelKind, trace *SelectionTrace) []ChannelInfo {
	filtered := make([]ChannelInfo, 0, len(channels))
	for _, ch := range channels {
		upstream := s.getUpstreamByRoute(normalizedChannelRoute(ch, kind))
		if !channelHasSelectableKey(upstream) {
			configured, disabled := 0, 0
			if upstream != nil {
				configured = len(upstream.APIKeys)
				disabled = len(upstream.DisabledAPIKeys)
			}
			trace.skipChannel(ch, "key_availability_filter", "no_selectable_keys",
				fmt.Sprintf("configured=%d disabled=%d", configured, disabled))
			continue
		}
		filtered = append(filtered, ch)
	}
	return filtered
}

// channelHasSelectableKey 只判断渠道的基础 Key 可用性。
// model 置空，避免在 scheduler 尚未确定自动映射实际模型时误用 per-key model 白名单；
// 模型级限制仍由请求路径基于 redirectedModel 精确执行。
func channelHasSelectableKey(upstream *config.UpstreamConfig) bool {
	return upstream != nil && len(keypool.CandidatesForModel(upstream, nil, "")) > 0
}

func traceCandidateFilterSkips(before, after []ChannelInfo, trace *SelectionTrace) {
	if trace == nil || len(before) == 0 {
		return
	}
	kept := make(map[int]struct{}, len(after))
	for _, ch := range after {
		kept[ch.Index] = struct{}{}
	}
	for _, ch := range before {
		if _, ok := kept[ch.Index]; ok {
			continue
		}
		trace.skipChannel(ch, "candidate_filter", "filtered_out", "")
	}
}

func (s *ChannelScheduler) channelCircuitState(upstream *config.UpstreamConfig, kind ChannelKind, model string) metrics.CircuitState {
	if upstream == nil {
		return metrics.CircuitStateClosed
	}
	return s.getMetricsManager(kind).GetChannelCircuitStateMultiURL(upstream.GetAllBaseURLs(), upstream.APIKeys, NormalizedMetricsServiceType(kind, upstream.ServiceType), model)
}

// channelInRuntimeCooldown 判断渠道是否处于运行态 cooldown。
func (s *ChannelScheduler) channelInRuntimeCooldown(kind ChannelKind, channelIndex int) bool {
	if s.rateLimitManager == nil {
		return false
	}
	limiter := s.rateLimitManager.Get(kindAPIType(kind), channelIndex)
	if limiter == nil {
		return false
	}
	inCooldown, _ := limiter.InCooldown(time.Now())
	return inCooldown
}

// ShouldDeferForRateLimit 判断指定渠道或 key/quota scope 是否应因高水位暂缓新请求。
// 第三个返回值 inCooldown 标识此次软跳是否由 cooldown 触发。
func (s *ChannelScheduler) ShouldDeferForRateLimit(kind ChannelKind, channelIndex int, scope string, cfg ratelimit.Config, now time.Time) (bool, float64, bool) {
	if s == nil || s.rateLimitManager == nil {
		return false, 0, false
	}
	if now.IsZero() {
		now = time.Now()
	}

	apiType := kindAPIType(kind)
	limiter := s.rateLimitManager.Get(apiType, channelIndex)
	if scope != "" {
		limiter = s.rateLimitManager.GetOrCreateScoped(apiType, channelIndex, scope, cfg)
	}
	if limiter == nil {
		return false, 0, false
	}

	status := limiter.Status(now)

	// cooldown 优先检查：仅靠上游 Retry-After 学到的 cooldown 也需要软跳过，
	// 且不写入 loadShed 状态，cooldown 到期后立即可用。
	// 返回 utilization=1.0 表示饱和/不可用，防止调用方误判为低水位。
	if status.InCooldown {
		return true, 1.0, true
	}

	if status.MaxRequests <= 0 && status.MaxConcurrent <= 0 {
		s.clearRateLimitLoadShed(apiType, channelIndex, scope)
		return false, 0, false
	}

	ratio := status.Utilization()
	key := rateLimitLoadShedKey(apiType, channelIndex, scope)

	s.loadShedMu.Lock()
	defer s.loadShedMu.Unlock()
	if s.loadShedStates == nil {
		s.loadShedStates = make(map[string]rateLimitLoadShedState)
	}

	state := s.loadShedStates[key]

	if ratio >= rateLimitLoadShedHighWatermark {
		state.shedding = true
		state.lowSince = time.Time{}
		s.loadShedStates[key] = state
		return true, ratio, false
	}

	if !state.shedding {
		delete(s.loadShedStates, key)
		return false, ratio, false
	}

	if ratio < rateLimitLoadShedLowWatermark {
		if state.lowSince.IsZero() {
			state.lowSince = now
			s.loadShedStates[key] = state
			return true, ratio, false
		}
		if now.Sub(state.lowSince) >= rateLimitLoadShedRecovery {
			delete(s.loadShedStates, key)
			return false, ratio, false
		}
		s.loadShedStates[key] = state
		return true, ratio, false
	}

	// 30% ≤ ratio < 50%：维持 lowSince 不变，继续等待恢复
	s.loadShedStates[key] = state
	return true, ratio, false
}

func (s *ChannelScheduler) clearRateLimitLoadShed(apiType string, channelIndex int, scope string) {
	if s == nil {
		return
	}
	key := rateLimitLoadShedKey(apiType, channelIndex, scope)
	s.loadShedMu.Lock()
	defer s.loadShedMu.Unlock()
	delete(s.loadShedStates, key)
}

// Start 启动后台 reaper，定期推进到期的 loadShed 状态。
func (s *ChannelScheduler) Start() {
	go s.recoverExpiredLoadShedStates()
}

// Stop 停止后台 reaper。
func (s *ChannelScheduler) Stop() {
	close(s.loadShedStopCh)
}

func (s *ChannelScheduler) recoverExpiredLoadShedStates() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.recoverLoadShedStates()
		case <-s.loadShedStopCh:
			return
		}
	}
}

// recoverLoadShedStates 为 high-watermark shedding 状态启动恢复计时器。
// 实际恢复（删除状态）由 ShouldDeferForRateLimit 基于 limiter 实际利用率确认，
// 避免 reaper 在 limiter 仍有活跃请求时误删状态。
func (s *ChannelScheduler) recoverLoadShedStates() {
	s.loadShedMu.Lock()
	defer s.loadShedMu.Unlock()
	now := time.Now()
	for key, state := range s.loadShedStates {
		if !state.shedding {
			delete(s.loadShedStates, key)
			continue
		}
		if state.lowSince.IsZero() {
			// high-watermark shedding：启动恢复计时器，
			// 使空闲状态在 rateLimitLoadShedRecovery 后由 ShouldDeferForRateLimit 清理。
			state.lowSince = now
			s.loadShedStates[key] = state
		}
	}
}

func rateLimitLoadShedKey(apiType string, channelIndex int, scope string) string {
	if scope == "" {
		scope = "channel"
	}
	return fmt.Sprintf("%s:%d:%s", apiType, channelIndex, scope)
}

func (s *ChannelScheduler) channelRateLimitSoftDeferred(upstream *config.UpstreamConfig, kind ChannelKind, channelIndex int, model string, now time.Time) (bool, float64, string, bool) {
	if upstream == nil || s == nil || s.rateLimitManager == nil {
		return false, 0, "", false
	}

	if deferred, ratio, cooldown := s.ShouldDeferForRateLimit(kind, channelIndex, "", ratelimit.Config{}, now); deferred {
		return true, ratio, "channel", cooldown
	}

	if !keypool.HasEffectiveConfig(upstream) {
		return false, 0, "", false
	}

	candidates := keypool.CandidatesForModel(upstream, nil, model)
	if len(candidates) == 0 {
		return false, 0, "", false
	}

	maxRatio := 0.0
	maxScope := ""
	maxCooldown := false
	for _, candidate := range candidates {
		cfg := keypool.ConfigForCandidate(*upstream, candidate.Config)
		deferred, ratio, cooldown := s.ShouldDeferForRateLimit(kind, channelIndex, candidate.Scope, cfg, now)
		if ratio > maxRatio {
			maxRatio = ratio
			maxScope = candidate.Scope
		}
		if cooldown {
			maxCooldown = true
		}
		if !deferred {
			return false, ratio, candidate.Scope, false
		}
	}
	if maxScope == "" {
		maxScope = "key"
	}
	return true, maxRatio, maxScope, maxCooldown
}

func shouldReserveVisionChannelForText(kind ChannelKind, hasImageContent bool, upstream *config.UpstreamConfig, softSkipped []softSkippedChannel) bool {
	if kind == ChannelKindImages || hasImageContent || upstream == nil || upstream.NoVision {
		return false
	}
	for _, skipped := range softSkipped {
		if skipped.upstream == nil || !skipped.upstream.NoVision {
			continue
		}
		if skipped.ratio < rateLimitVisionReserveWatermark {
			return true
		}
	}
	return false
}

// MarkChannelCooldown 将渠道置入短期冷却，后续调度会暂时跳过该渠道。
func (s *ChannelScheduler) MarkChannelCooldown(kind ChannelKind, channelIndex int, duration time.Duration) {
	if s == nil || s.rateLimitManager == nil || duration <= 0 {
		return
	}
	s.rateLimitManager.SetCooldown(kindAPIType(kind), channelIndex, duration, time.Now())
}

// MarkLimiterScopeCooldown 对当前 key/quota scope 施加短期冷却，避免因单个
// 账号 429 冻结整个渠道的其他独立账号。scope 非空时只冷却该 scope 的
// limiter；scope 为空时回退到 MarkChannelCooldown。
// limiter Manager 封装在 scheduler 内，handler 不直接操作内部 manager。
func (s *ChannelScheduler) MarkLimiterScopeCooldown(kind ChannelKind, channelIndex int, scope string, duration time.Duration) {
	if s == nil || s.rateLimitManager == nil || duration <= 0 {
		return
	}
	if scope == "" {
		s.MarkChannelCooldown(kind, channelIndex, duration)
		return
	}
	s.rateLimitManager.SetCooldownScoped(kindAPIType(kind), channelIndex, scope, duration, time.Now())
}

func (s *ChannelScheduler) channelFailureRate(upstream *config.UpstreamConfig, kind ChannelKind, model string) float64 {
	if upstream == nil {
		return 0
	}
	return s.getMetricsManager(kind).CalculateChannelFailureRateMultiURL(upstream.GetAllBaseURLs(), upstream.APIKeys, NormalizedMetricsServiceType(kind, upstream.ServiceType), model)
}

func (s *ChannelScheduler) channelIsHealthy(upstream *config.UpstreamConfig, kind ChannelKind, model string) bool {
	if upstream == nil {
		return false
	}
	return s.getMetricsManager(kind).IsChannelHealthyMultiURL(upstream.GetAllBaseURLs(), upstream.APIKeys, NormalizedMetricsServiceType(kind, upstream.ServiceType), model)
}

func (s *ChannelScheduler) channelIsRuntimeAvailable(upstream *config.UpstreamConfig, kind ChannelKind, channelIndex int, model string) bool {
	if !channelHasSelectableKey(upstream) {
		return false
	}
	if s.channelCircuitState(upstream, kind, model) == metrics.CircuitStateOpen {
		return false
	}
	if !s.channelIsHealthy(upstream, kind, model) {
		return false
	}
	return !s.channelInRuntimeCooldown(kind, channelIndex)
}

func (s *ChannelScheduler) filterChannelsByContext(activeChannels []ChannelInfo, kind ChannelKind, model string, requirement *ContextRequirement, trace *SelectionTrace) ([]ChannelInfo, error) {
	if requirement == nil {
		return activeChannels, nil
	}
	cfg := s.configManager.GetConfig()
	if !cfg.ContextRouting.IsContextRoutingEnabled() {
		return activeChannels, nil
	}
	channelRequiredWindow := requirement.effectiveWindowTokens()
	if channelRequiredWindow <= 0 && !requirement.needsOutputValidation() {
		return activeChannels, nil
	}

	unknownSafeWindow := cfg.ContextRouting.EffectiveUnknownSafeWindowTokens()
	filtered := make([]ChannelInfo, 0, len(activeChannels))
	outputFallback := make([]ChannelInfo, 0)
	skipped := make([]string, 0)
	maxKnownWindow := 0
	appendCandidate := func(ch ChannelInfo, outputOverflow bool) {
		if outputOverflow {
			outputFallback = append(outputFallback, ch)
			return
		}
		filtered = append(filtered, ch)
	}

	for _, ch := range activeChannels {
		route := normalizedChannelRoute(ch, kind)
		executionKind := ChannelKind(route.Kind)
		upstream := s.getUpstreamByRoute(route)
		prefix := kindSchedulerLogPrefix(executionKind)
		if upstream == nil {
			trace.skipChannel(ch, "context_filter", "missing_upstream", "")
			continue
		}
		actualModel := model
		if ch.ActualModel != "" {
			actualModel = ch.ActualModel
		}
		resolved := config.ResolveUpstreamCapability(actualModel, upstream, cfg.UpstreamModelCapabilities)
		capability := resolved.Capability
		if capability.ContextWindowTokens > maxKnownWindow {
			maxKnownWindow = capability.ContextWindowTokens
		}
		outputOverflow := requirement.ExplicitOutputMax && capability.MaxOutputTokens > 0 && requirement.OutputTokens > capability.MaxOutputTokens
		if outputOverflow {
			log.Printf("[%s-ContextFilter] 渠道 [%d] %s: 显式输出上限 %d 超过实际模型 %q 最大输出 %d，将作为可 clamp 的低优先级候选",
				prefix, ch.Index, ch.Name, requirement.OutputTokens, resolved.ActualModel, capability.MaxOutputTokens)
		}
		if requirement.SkipWindowValidation {
			appendCandidate(ch, outputOverflow)
			continue
		}
		if capability.ContextWindowTokens > 0 {
			if channelRequiredWindow > 0 && channelRequiredWindow > capability.ContextWindowTokens {
				reason := fmt.Sprintf("[%s:%d]%s actual=%s input=%d>%d totalBudget=%d", route.Kind, ch.Index, ch.Name, resolved.ActualModel, channelRequiredWindow, capability.ContextWindowTokens, requirement.RequiredTokens)
				skipped = append(skipped, reason)
				trace.skipChannel(ch, "context_filter", "context_window_exceeded", fmt.Sprintf("actual=%s input=%d window=%d totalBudget=%d", resolved.ActualModel, channelRequiredWindow, capability.ContextWindowTokens, requirement.RequiredTokens))
				continue
			}
			appendCandidate(ch, outputOverflow)
			continue
		}
		if channelRequiredWindow <= 0 || upstream.AllowUnknownContext || channelRequiredWindow <= unknownSafeWindow {
			appendCandidate(ch, outputOverflow)
			continue
		}
		skipped = append(skipped, fmt.Sprintf("[%s:%d]%s actual=%s unknown input=%d totalBudget=%d", route.Kind, ch.Index, ch.Name, resolved.ActualModel, channelRequiredWindow, requirement.RequiredTokens))
		trace.skipChannel(ch, "context_filter", "unknown_context_window", fmt.Sprintf("actual=%s input=%d safeWindow=%d totalBudget=%d", resolved.ActualModel, channelRequiredWindow, unknownSafeWindow, requirement.RequiredTokens))
	}
	if len(filtered) == 0 && len(outputFallback) > 0 {
		return outputFallback, nil
	}
	filtered = append(filtered, outputFallback...)
	if len(filtered) == 0 {
		if maxKnownWindow > 0 {
			return nil, fmt.Errorf("没有候选物理路由可承载当前上下文：输入估算 %d tokens，最大已知窗口 %d tokens（已过滤：%s）", channelRequiredWindow, maxKnownWindow, strings.Join(skipped, "; "))
		}
		return nil, fmt.Errorf("没有候选物理路由可承载当前上下文：输入估算 %d tokens（已过滤：%s）", channelRequiredWindow, strings.Join(skipped, "; "))
	}
	return filtered, nil
}

// ValidateUpstreamContext 校验单个渠道是否满足当前上下文需求。
func (s *ChannelScheduler) ValidateUpstreamContext(kind ChannelKind, model string, upstream *config.UpstreamConfig, requirement *ContextRequirement) error {
	if upstream == nil || requirement == nil {
		return nil
	}
	cfg := s.configManager.GetConfig()
	if !cfg.ContextRouting.IsContextRoutingEnabled() {
		return nil
	}

	resolved := config.ResolveUpstreamCapability(model, upstream, cfg.UpstreamModelCapabilities)
	capability := resolved.Capability
	if requirement.ExplicitOutputMax && capability.MaxOutputTokens > 0 && requirement.OutputTokens > capability.MaxOutputTokens {
		log.Printf("[%s-ContextFilter] 渠道 %q 的实际模型 %q 最大输出为 %d tokens，低于请求的 %d tokens，后续发送前将下调到模型上限",
			kindSchedulerLogPrefix(kind), upstream.Name, resolved.ActualModel, capability.MaxOutputTokens, requirement.OutputTokens)
	}
	if requirement.SkipWindowValidation {
		return nil
	}
	channelRequiredWindow := requirement.effectiveWindowTokens()
	if channelRequiredWindow <= 0 {
		return nil
	}
	if capability.ContextWindowTokens > 0 {
		if channelRequiredWindow > capability.ContextWindowTokens {
			return fmt.Errorf("渠道 %q 的实际模型 %q 上下文窗口为 %d tokens，低于当前请求输入估算 %d tokens",
				upstream.Name, resolved.ActualModel, capability.ContextWindowTokens, channelRequiredWindow)
		}
		return nil
	}
	if upstream.AllowUnknownContext || channelRequiredWindow <= cfg.ContextRouting.EffectiveUnknownSafeWindowTokens() {
		return nil
	}
	return fmt.Errorf("渠道 %q 的实际模型 %q 上下文能力未知，当前请求输入估算 %d tokens 超过未知安全窗口 %d tokens",
		upstream.Name, resolved.ActualModel, channelRequiredWindow, cfg.ContextRouting.EffectiveUnknownSafeWindowTokens())
}

func applyManualOverrideOrder(activeChannels []ChannelInfo, sequence []conversation.ChannelEntry) []ChannelInfo {
	if len(activeChannels) == 0 || len(sequence) == 0 {
		return activeChannels
	}
	byIndex := make(map[int]ChannelInfo, len(activeChannels))
	for _, ch := range activeChannels {
		byIndex[ch.Index] = ch
	}

	ordered := make([]ChannelInfo, 0, len(activeChannels))
	used := make(map[int]bool, len(activeChannels))
	for _, entry := range sequence {
		ch, ok := byIndex[entry.ChannelIndex]
		if !ok || used[ch.Index] {
			continue
		}
		ordered = append(ordered, ch)
		used[ch.Index] = true
	}
	for _, ch := range activeChannels {
		if !used[ch.Index] {
			ordered = append(ordered, ch)
		}
	}
	return ordered
}

func schedulerAgentRoleForLog(role string) string {
	if strings.TrimSpace(role) == "" {
		return "unknown"
	}
	return role
}

func formatOverrideSequenceHead(sequence []conversation.ChannelEntry, limit int) string {
	if len(sequence) == 0 {
		return "[]"
	}
	if limit <= 0 || limit > len(sequence) {
		limit = len(sequence)
	}
	parts := make([]string, 0, limit+1)
	for _, entry := range sequence[:limit] {
		name := entry.ChannelName
		if name == "" {
			name = "unknown"
		}
		parts = append(parts, fmt.Sprintf("%d:%s", entry.ChannelIndex, name))
	}
	if len(sequence) > limit {
		parts = append(parts, fmt.Sprintf("+%d", len(sequence)-limit))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func traceAffinityKey(kind ChannelKind, userID string, requirement *ContextRequirement) string {
	channelRequiredWindow := requirement.effectiveWindowTokens()
	if channelRequiredWindow <= 0 {
		return string(kind) + ":" + userID
	}
	return string(kind) + ":" + userID + ":" + contextBucket(channelRequiredWindow)
}

func contextBucket(tokens int) string {
	switch {
	case tokens <= 200000:
		return "ctx-200k"
	case tokens <= 272000:
		return "ctx-272k"
	case tokens <= 400000:
		return "ctx-400k"
	case tokens <= 1000000:
		return "ctx-1m"
	default:
		return "ctx-over-1m"
	}
}

func kindDisplayName(kind ChannelKind) string {
	switch kind {
	case ChannelKindGemini:
		return "Gemini"
	case ChannelKindResponses:
		return "Responses"
	case ChannelKindChat:
		return "Chat"
	case ChannelKindImages:
		return "Images"
	case ChannelKindVectors:
		return "Vectors"
	default:
		return "Messages"
	}
}

// findPromotedChannel 查找处于促销期的渠道
func (s *ChannelScheduler) findPromotedChannel(activeChannels []ChannelInfo, kind ChannelKind) *ChannelInfo {
	for i := range activeChannels {
		ch := &activeChannels[i]
		if ch.Status != "active" {
			continue
		}
		upstream := s.getUpstreamByRoute(normalizedChannelRoute(*ch, kind))
		if upstream != nil {
			if config.IsChannelInPromotion(upstream) {
				prefix := kindSchedulerLogPrefix(kind)
				log.Printf("[%s-Promotion] 找到促销渠道: [%d] %s (promotionUntil: %v)", prefix, ch.Index, upstream.Name, upstream.PromotionUntil)
				return ch
			}
		}
	}
	return nil
}

// selectFallbackChannel 选择降级渠道（失败率最低的）
func (s *ChannelScheduler) selectFallbackChannel(
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
	kind ChannelKind,
) (*SelectionResult, error) {
	return s.selectFallbackChannelWithRecord(activeChannels, failedChannels, kind, true)
}

func (s *ChannelScheduler) selectFallbackChannelWithRecord(
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
	kind ChannelKind,
	record bool,
) (*SelectionResult, error) {
	return s.selectFallbackChannelWithRouteRecord(activeChannels, failedChannels, nil, kind, record)
}

func (s *ChannelScheduler) selectFallbackChannelWithRouteRecord(
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
	failedRoutes map[ChannelRouteKey]bool,
	kind ChannelKind,
	record bool,
) (*SelectionResult, error) {
	var bestChannel *ChannelInfo
	var bestUpstream *config.UpstreamConfig
	bestFailureRate := float64(2)

	for i := range activeChannels {
		ch := &activeChannels[i]
		if channelInfoFailed(*ch, failedChannels, failedRoutes) || ch.Status != "active" {
			continue
		}

		route := normalizedChannelRoute(*ch, kind)
		upstream := s.getUpstreamByRoute(route)
		if !channelHasSelectableKey(upstream) {
			continue
		}
		if s.channelCircuitState(upstream, ChannelKind(route.Kind), "") == metrics.CircuitStateOpen || s.channelInRuntimeCooldownByRoute(route) {
			continue
		}

		failureRate := s.channelFailureRate(upstream, ChannelKind(route.Kind), "")
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
		return s.selectionResultWithRecord(ChannelKind(bestChannel.Route.Kind), bestUpstream, bestChannel.Index, "fallback", record), nil
	}

	return nil, fmt.Errorf("所有渠道都不可用")
}

// ChannelInfo 渠道信息（用于排序）
// Priority 约定为非负整数，数字越小优先级越高；0 表示未显式配置，将回退为渠道索引。
type ChannelInfo struct {
	Route             ChannelRouteRef `json:"route"`
	Index             int             `json:"index"`
	Name              string          `json:"name"`
	Priority          int             `json:"priority"`
	Status            string          `json:"status"`
	CircuitOpen       bool            `json:"circuitOpen,omitempty"`
	ActualModel       string          `json:"actualModel,omitempty"`
	ProtocolFidelity  string          `json:"protocolFidelity,omitempty"`
	ConversionPenalty float64         `json:"conversionPenalty,omitempty"`
}

// getActiveChannels 获取活跃渠道列表（按优先级排序）
func (s *ChannelScheduler) getActiveChannels(kind ChannelKind, model string) []ChannelInfo {
	return s.getActiveChannelsWithTrace(context.Background(), kind, model, nil)
}

func (s *ChannelScheduler) getActiveChannelsWithTrace(ctx context.Context, kind ChannelKind, model string, trace *SelectionTrace) []ChannelInfo {
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

	// 筛选活跃渠道
	var activeChannels []ChannelInfo
	for i, upstream := range upstreams {
		status := upstream.Status
		if status == "" {
			status = "active" // 默认为活跃
		}
		priority := upstream.Priority
		if priority == 0 {
			priority = i // 默认优先级为索引
		}
		ch := ChannelInfo{
			Route:    channelRouteRef(kind, i, &upstream),
			Index:    i,
			Name:     upstream.Name,
			Priority: priority,
			Status:   status,
		}

		// 只选择 active 状态的渠道（suspended 也算在活跃序列中，但会被健康检查过滤）
		if status != "disabled" {
			// 过滤不支持当前模型的渠道
			if model != "" {
				supported, reason := s.resolveModelSupport(ctx, kind, &upstream, model)
				if !supported {
					prefix := kindSchedulerLogPrefix(kind)
					log.Printf("[%s-ModelFilter] 跳过渠道 [%d] %s: 模型 %q 不被 supportedModels 支持 (%s)", prefix, i, upstream.Name, model, reason)
					trace.skipChannel(ch, "active_model_filter", "unsupported_model", reason)
					continue
				}
			}

			activeChannels = append(activeChannels, ch)
		} else {
			trace.skipChannel(ch, "active_model_filter", "disabled_status", status)
		}
	}

	// 按优先级排序（数字越小优先级越高）
	sort.Slice(activeChannels, func(i, j int) bool {
		return activeChannels[i].Priority < activeChannels[j].Priority
	})

	return activeChannels
}

// resolveModelSupport 判断渠道是否支持指定模型。
// 优先调用 modelSupportResolverFunc（autopilot 注入）。普通未命中仍回退到
// UpstreamConfig.ExplainModelSupport；权威拒绝则直接过滤，避免空 SupportedModels
// 把画像已确认不支持的模型重新放回候选。
func (s *ChannelScheduler) resolveModelSupport(ctx context.Context, kind ChannelKind, upstream *config.UpstreamConfig, model string) (bool, string) {
	s.mu.RLock()
	resolver := s.modelSupportResolverFunc
	s.mu.RUnlock()

	if resolver != nil {
		supported, _, source, reason := resolver(ctx, kind, upstream, model)
		if supported {
			return true, ""
		}
		if source == ModelSupportSourceAuthoritativeDeny {
			return false, reason
		}
		// resolver 未命中 → 回退到原有 ExplainModelSupport
	}

	return upstream.ExplainModelSupport(model)
}

// findBestAvailableChannelPriority 找到当前最佳可用渠道的优先级（用于 affinity 覆盖判断）
// 返回 -1 表示没有可用渠道
func (s *ChannelScheduler) findBestAvailableChannelPriority(
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
	kind ChannelKind,
	model string,
) int {
	return s.findBestAvailableChannelPriorityWithRoutes(activeChannels, failedChannels, nil, kind, model)
}

func (s *ChannelScheduler) findBestAvailableChannelPriorityWithRoutes(
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
	failedRoutes map[ChannelRouteKey]bool,
	kind ChannelKind,
	model string,
) int {
	bestPriority := -1

	for _, ch := range activeChannels {
		if channelInfoFailed(ch, failedChannels, failedRoutes) || ch.Status != "active" {
			continue
		}

		route := normalizedChannelRoute(ch, kind)
		routeKind := ChannelKind(route.Kind)
		upstream := s.getUpstreamByRoute(route)
		if !channelHasSelectableKey(upstream) || !s.channelIsRuntimeAvailable(upstream, routeKind, ch.Index, "") {
			continue
		}
		if deferred, _, _, _ := s.channelRateLimitSoftDeferred(upstream, routeKind, ch.Index, model, time.Now()); deferred {
			continue
		}

		if bestPriority == -1 || ch.Priority < bestPriority {
			bestPriority = ch.Priority
		}
	}

	return bestPriority
}

// getUpstreamByIndex 根据索引获取上游配置
// 注意：返回的是副本，避免指向 slice 元素的指针在 slice 重分配后失效
func (s *ChannelScheduler) getUpstreamByIndex(index int, kind ChannelKind) *config.UpstreamConfig {
	if s == nil || s.configManager == nil {
		return nil
	}
	return s.configManager.GetUpstreamByIndex(kindAPIType(kind), index)
}

func (s *ChannelScheduler) getUpstreamByRoute(route ChannelRouteRef) *config.UpstreamConfig {
	return s.getUpstreamByIndex(route.Index, ChannelKind(route.Kind))
}

func (s *ChannelScheduler) channelInRuntimeCooldownByRoute(route ChannelRouteRef) bool {
	return s.channelInRuntimeCooldown(ChannelKind(route.Kind), route.Index)
}

func (s *ChannelScheduler) channelModelCircuitOpenByRoute(upstream *config.UpstreamConfig, route ChannelRouteRef, model string) bool {
	return s.channelModelCircuitOpen(upstream, ChannelKind(route.Kind), model)
}

// buildSmartFilterFromProvider 从全局 CandidateFilterProvider 构建 SmartFilter。
// 返回的 SmartFilter 包装了 SmartRouter 的 CandidateFilterFunc，
// 并通过 availableFn 过滤掉不满足基础条件的候选（非 active、无 key、熔断中）。
// 返回 nil 表示 provider 返回了 nil filter（off / kill switch），不注入。
func (s *ChannelScheduler) buildSmartFilterFromProvider(
	ctx context.Context,
	kind ChannelKind,
	model string,
) (func(context.Context, []ChannelInfo) []ChannelInfo, CandidateSelectionObserver) {
	if s.candidateFilterProvider == nil {
		return nil, nil
	}

	filter, observer := s.candidateFilterProvider(ctx, kind, model)
	if filter == nil {
		return nil, nil // off / kill switch
	}

	return func(ctx context.Context, channels []ChannelInfo) []ChannelInfo {
		// SmartRouter 在评分、结果映射和硬约束阶段会多次读取同一渠道。
		// 请求级缓存避免每次读取都通过 GetConfig 深拷贝整份配置。
		// 缓存键必须使用物理路由身份（Route.Key）：协议联邦下 messages[0]
		// 与 chat[0] 同索引但属不同物理渠道，按 ch.Index 缓存会返回错误上游，
		// 导致评分、可用性、disabled 检查和 trace UID 归因错配。
		upstreamCache := make(map[ChannelRouteKey]*config.UpstreamConfig, len(channels))
		upstreamFor := func(ch ChannelInfo) *config.UpstreamConfig {
			routeKey := normalizedChannelRoute(ch, kind).Key()
			if upstream, ok := upstreamCache[routeKey]; ok {
				return upstream
			}
			upstream := s.getUpstreamByRoute(normalizedChannelRoute(ch, kind))
			upstreamCache[routeKey] = upstream
			return upstream
		}
		result, err := filter(channels, upstreamFor, func(ch ChannelInfo, upstream *config.UpstreamConfig) bool {
			return s.channelAvailableForCandidateFilter(ch, upstream, kind, "")
		})
		if err != nil {
			// SmartFilter 出错时返回空列表，触发 fallback 保留原列表
			return nil
		}
		return result
	}, observer
}
