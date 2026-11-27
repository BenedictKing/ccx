package scheduler

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
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

	var cfg config.Config
	if opts.ConfigSnapshot != nil {
		cfg = *opts.ConfigSnapshot
	} else {
		cfg = s.ConfigSnapshot()
	}
	if err := s.restoreSubagentContinuation(&cfg, &opts); err != nil {
		return nil, err
	}
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
	kind := opts.Kind
	model := opts.Model
	routePrefix := opts.RoutePrefix
	channelName := opts.ChannelName
	trace := newSelectionTrace(opts)
	upstreamFor := func(index int) *config.UpstreamConfig {
		return config.WithSubagentRouting(upstreamByIndex(cfg, index, kind), opts.SubagentRouting, opts.ApplySubagentRouting)
	}
	traceErr := func(err error) error {
		return newSelectionTraceError(err, trace)
	}
	finish := func(upstream *config.UpstreamConfig, channelIndex int, reason string) *SelectionResult {
		upstream = config.WithSubagentRouting(upstream, opts.SubagentRouting, opts.ApplySubagentRouting)
		upstream = config.WithAgentModel(model, upstream, opts.AgentRole)
		result := s.selectionResultWithRecord(kind, upstream, channelIndex, reason, !opts.DryRun)
		channelName := ""
		if upstream != nil {
			channelName = upstream.Name
		}
		trace.selectChannel(channelIndex, channelName, reason)
		result.Trace = trace
		return result
	}

	// 获取活跃渠道列表（含模型过滤）
	activeChannels := s.getActiveChannelsWithTrace(cfg, kind, model, opts.AgentRole, opts.SubagentRouting, opts.ApplySubagentRouting, trace)
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
			upstream := upstreamFor(ch.Index)
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
			upstream := upstreamFor(ch.Index)
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

	activeChannels, err := s.filterChannelsByContext(cfg, activeChannels, kind, model, opts.AgentRole, opts.SubagentRouting, opts.ApplySubagentRouting, opts.ContextRequirement, trace)
	if err != nil {
		trace.setStage("context_filter", 0)
		return nil, traceErr(err)
	}
	trace.setStage("context_filter", len(activeChannels))
	if opts.CandidateFilter != nil {
		beforeFilter := append([]ChannelInfo(nil), activeChannels...)
		activeChannels, err = opts.CandidateFilter(activeChannels, func(ch ChannelInfo) *config.UpstreamConfig {
			return upstreamFor(ch.Index)
		}, func(ch ChannelInfo, upstream *config.UpstreamConfig) bool {
			return s.channelAvailableForCandidateFilter(ch, upstream, kind)
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

	// 指定渠道名（X-Channel 头）：在模型、路由前缀与上下文过滤后直接定位。
	if channelName != "" {
		for _, ch := range activeChannels {
			if ch.Name == channelName {
				if failedChannels[ch.Index] {
					trace.skipChannel(ch, "channel_pin", "failed_in_request", "")
					return nil, traceErr(fmt.Errorf("指定渠道 %q 在本次请求中已失败", channelName))
				}
				upstream := upstreamFor(ch.Index)
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

	// 1. 检查促销期渠道（绕过健康检查）
	promotedChannel := s.findPromotedChannel(cfg, activeChannels, kind)
	if opts.ApplySubagentRouting {
		promotedChannel = nil
	}
	if promotedChannel != nil && !failedChannels[promotedChannel.Index] {
		// 促销渠道存在且未失败，直接使用（不检查健康状态，让用户设置的促销渠道有机会尝试）
		upstream := upstreamFor(promotedChannel.Index)
		if upstream != nil && len(upstream.APIKeys) > 0 && !s.channelInRuntimeCooldown(kind, promotedChannel.Index) {
			failureRate := s.channelFailureRate(upstream, kind)
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

	// 1. 检查 Trace 亲和性（促销渠道失败时或无促销渠道时）
	if userID != "" {
		if preferredIdx, ok := s.preferredTraceChannel(opts, affinityUserID, activeChannels); ok {
			bestPriority := s.findBestAvailableChannelPriority(cfg, activeChannels, failedChannels, kind, model, opts.AgentRole, opts.SubagentRouting, opts.ApplySubagentRouting)
			for _, ch := range activeChannels {
				if ch.Index == preferredIdx && !failedChannels[preferredIdx] {
					// 检查渠道状态：只有 active 状态才使用亲和性
					if ch.Status != "active" {
						prefix := kindSchedulerLogPrefix(kind)
						log.Printf("[%s-Affinity] 跳过亲和渠道 [%d] %s: 状态为 %s (user: %s)", prefix, preferredIdx, ch.Name, ch.Status, maskUserID(userID))
						trace.skipChannel(ch, "trace_affinity", "inactive_status", ch.Status)
						continue
					}
					// 加密续轮优先复用已成功渠道，避免跨上游重放无法解密的状态。
					if !opts.PreserveTraceAffinity && bestPriority >= 0 && ch.Priority > bestPriority {
						prefix := kindSchedulerLogPrefix(kind)
						log.Printf("[%s-Affinity] 跳过亲和渠道 [%d] %s: 存在更高优先级可用渠道 (亲和优先级: %d, 最优优先级: %d, user: %s)", prefix, preferredIdx, ch.Name, ch.Priority, bestPriority, maskUserID(userID))
						trace.skipChannel(ch, "trace_affinity", "better_priority_available", fmt.Sprintf("affinity=%d best=%d", ch.Priority, bestPriority))
						continue
					}
					// 检查渠道是否健康且未处于运行态冷却
					upstream := upstreamFor(preferredIdx)
					if upstream != nil && s.channelIsRuntimeAvailable(upstream, kind, preferredIdx) {
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
	for _, ch := range activeChannels {
		// 跳过本次请求已经失败的渠道
		if failedChannels[ch.Index] {
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

		upstream := upstreamFor(ch.Index)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			trace.skipChannel(ch, "priority_order", "missing_upstream_or_keys", "")
			continue
		}

		// 跳过失败率过高的渠道（已熔断或即将熔断）
		channelState := s.channelCircuitState(upstream, kind)
		if channelState == metrics.CircuitStateOpen || !s.channelIsHealthy(upstream, kind) {
			failureRate := s.channelFailureRate(upstream, kind)
			prefix := kindSchedulerLogPrefix(kind)
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
		if s.channelInRuntimeCooldown(kind, ch.Index) {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Channel] 跳过运行态 cooldown 中的渠道: [%d] %s", prefix, ch.Index, ch.Name)
			trace.skipChannel(ch, "priority_order", "runtime_cooldown", "")
			continue
		}

		requestModel := config.RequestModelForAgent(model, upstream, opts.AgentRole)
		if deferred, ratio, scope, cooldown := s.channelRateLimitSoftDeferred(upstream, kind, ch.Index, requestModel, time.Now()); deferred {
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
		if skipped.upstream == nil || !s.channelIsRuntimeAvailable(skipped.upstream, kind, skipped.channel.Index) {
			continue
		}
		prefix := kindSchedulerLogPrefix(kind)
		log.Printf("[%s-RateLimit] 所有低水位候选不可用，回退选择高水位渠道: [%d] %s scope=%s usage=%.0f%%",
			prefix, skipped.channel.Index, skipped.upstream.Name, skipped.scope, skipped.ratio*100)
		return finish(skipped.upstream, skipped.channel.Index, "rate_limit_pressure"), nil
	}

	// 3. 所有健康渠道都失败，选择失败率最低的作为降级
	result, err := s.selectFallbackChannelWithRecord(cfg, activeChannels, failedChannels, kind, !opts.DryRun)
	if result != nil {
		result.Upstream = config.WithSubagentRouting(result.Upstream, opts.SubagentRouting, opts.ApplySubagentRouting)
		result.Upstream = config.WithAgentModel(model, result.Upstream, opts.AgentRole)
		channelName := ""
		if result.Upstream != nil {
			channelName = result.Upstream.Name
		}
		trace.selectChannel(result.ChannelIndex, channelName, result.Reason)
		result.Trace = trace
	}
	if err != nil {
		err = traceErr(err)
	}
	return result, err
}

func (s *ChannelScheduler) channelAvailableForCandidateFilter(ch ChannelInfo, upstream *config.UpstreamConfig, kind ChannelKind) bool {
	if ch.Status != "active" || upstream == nil || len(upstream.APIKeys) == 0 {
		return false
	}
	if s.channelInRuntimeCooldown(kind, ch.Index) {
		return false
	}
	return s.channelCircuitState(upstream, kind) != metrics.CircuitStateOpen
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

func (s *ChannelScheduler) channelCircuitState(upstream *config.UpstreamConfig, kind ChannelKind) metrics.CircuitState {
	if upstream == nil || upstream.CircuitBreakerDisabled {
		return metrics.CircuitStateClosed
	}
	return s.getMetricsManager(kind).GetChannelCircuitStateMultiURL(upstream.GetAllBaseURLs(), upstream.APIKeys, NormalizedMetricsServiceType(kind, upstream.ServiceType))
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

// CredentialRateLimitLoad 合并共享上游冷却与独立 Key 的主动限速水位。
func (s *ChannelScheduler) CredentialRateLimitLoad(kind ChannelKind, channelIndex int, upstream *config.UpstreamConfig, candidate keypool.Candidate, now time.Time) (bool, float64, bool) {
	if s == nil || s.rateLimitManager == nil {
		return false, 0, false
	}
	if candidate.QuotaGroup != "" {
		if limiter := s.rateLimitManager.GetScoped(kindAPIType(kind), channelIndex, candidate.Scope); limiter != nil {
			if cooling, _ := limiter.InCooldown(now); cooling {
				return true, 1, true
			}
		}
	}
	return s.ShouldDeferForRateLimit(kind, channelIndex, keypool.LimiterScope(candidate.APIKey, ""), keypool.ConfigForCandidate(*upstream, candidate.Config), now)
}

func (s *ChannelScheduler) channelRateLimitSoftDeferred(upstream *config.UpstreamConfig, kind ChannelKind, channelIndex int, model string, now time.Time) (bool, float64, string, bool) {
	if upstream == nil || s == nil || s.rateLimitManager == nil {
		return false, 0, "", false
	}

	if deferred, ratio, cooldown := s.ShouldDeferForRateLimit(kind, channelIndex, "", ratelimit.Config{}, now); deferred {
		return true, ratio, "channel", cooldown
	}

	candidates := keypool.CandidatesForModel(upstream, nil, model)
	if len(candidates) == 0 {
		return false, 0, "", false
	}

	maxRatio := 0.0
	maxScope := ""
	maxCooldown := false
	for _, candidate := range candidates {
		deferred, ratio, cooldown := s.CredentialRateLimitLoad(kind, channelIndex, upstream, candidate, now)
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
	if upstream.CircuitBreakerDisabled {
		return true
	}
	return s.getMetricsManager(kind).IsChannelHealthyMultiURL(upstream.GetAllBaseURLs(), upstream.APIKeys, NormalizedMetricsServiceType(kind, upstream.ServiceType))
}

func (s *ChannelScheduler) channelIsRuntimeAvailable(upstream *config.UpstreamConfig, kind ChannelKind, channelIndex int) bool {
	if upstream == nil {
		return false
	}
	if s.channelCircuitState(upstream, kind) == metrics.CircuitStateOpen {
		return false
	}
	if !s.channelIsHealthy(upstream, kind) {
		return false
	}
	return !s.channelInRuntimeCooldown(kind, channelIndex)
}

func (s *ChannelScheduler) filterChannelsByContext(cfg config.Config, activeChannels []ChannelInfo, kind ChannelKind, model, agentRole string, subagentRouting *config.SubagentRoutingPolicy, applySubagentRouting bool, requirement *ContextRequirement, trace *SelectionTrace) ([]ChannelInfo, error) {
	if requirement == nil {
		return activeChannels, nil
	}
	if !cfg.ContextRouting.IsContextRoutingEnabled() {
		return activeChannels, nil
	}
	channelRequiredWindow := requirement.effectiveWindowTokens()
	if channelRequiredWindow <= 0 && !requirement.needsOutputValidation() {
		return activeChannels, nil
	}

	unknownSafeWindow := cfg.ContextRouting.EffectiveUnknownSafeWindowTokens()
	prefix := kindSchedulerLogPrefix(kind)
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
		upstream := upstreamByIndex(cfg, ch.Index, kind)
		if upstream == nil {
			trace.skipChannel(ch, "context_filter", "missing_upstream", "")
			continue
		}

		effectiveUpstream := config.WithSubagentRouting(upstream, subagentRouting, applySubagentRouting)
		effectiveUpstream = config.WithAgentModel(model, effectiveUpstream, agentRole)
		resolved := config.ResolveUpstreamCapability(model, effectiveUpstream, cfg.UpstreamModelCapabilities)
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
				reason := fmt.Sprintf("[%d]%s actual=%s input=%d>%d totalBudget=%d", ch.Index, ch.Name, resolved.ActualModel, channelRequiredWindow, capability.ContextWindowTokens, requirement.RequiredTokens)
				skipped = append(skipped, reason)
				trace.skipChannel(ch, "context_filter", "context_window_exceeded", fmt.Sprintf("actual=%s input=%d window=%d totalBudget=%d", resolved.ActualModel, channelRequiredWindow, capability.ContextWindowTokens, requirement.RequiredTokens))
				log.Printf("[%s-ContextFilter] 跳过渠道 [%d] %s: input=%d, window=%d, totalBudget=%d, output=%d, actualModel=%q, source=%s",
					prefix, ch.Index, ch.Name, channelRequiredWindow, capability.ContextWindowTokens, requirement.RequiredTokens, requirement.OutputTokens, resolved.ActualModel, resolved.Source)
				continue
			}
			appendCandidate(ch, outputOverflow)
			continue
		}

		if channelRequiredWindow <= 0 || upstream.AllowUnknownContext || channelRequiredWindow <= unknownSafeWindow {
			appendCandidate(ch, outputOverflow)
			continue
		}

		reason := fmt.Sprintf("[%d]%s actual=%s unknown input=%d totalBudget=%d", ch.Index, ch.Name, resolved.ActualModel, channelRequiredWindow, requirement.RequiredTokens)
		skipped = append(skipped, reason)
		trace.skipChannel(ch, "context_filter", "unknown_context_window", fmt.Sprintf("actual=%s input=%d safeWindow=%d totalBudget=%d", resolved.ActualModel, channelRequiredWindow, unknownSafeWindow, requirement.RequiredTokens))
		log.Printf("[%s-ContextFilter] 跳过未知上下文渠道 [%d] %s: input=%d 超过 unknownSafeWindow=%d, totalBudget=%d, output=%d, actualModel=%q",
			prefix, ch.Index, ch.Name, channelRequiredWindow, unknownSafeWindow, requirement.RequiredTokens, requirement.OutputTokens, resolved.ActualModel)
	}

	if len(filtered) == 0 && len(outputFallback) > 0 {
		log.Printf("[%s-ContextFilter] 没有完全满足显式输出上限 %d 的渠道，回退到可 clamp 的候选渠道", prefix, requirement.OutputTokens)
		return outputFallback, nil
	}
	if len(outputFallback) > 0 {
		filtered = append(filtered, outputFallback...)
	}

	if len(filtered) == 0 {
		if channelRequiredWindow <= 0 && len(skipped) > 0 {
			return nil, fmt.Errorf("没有 %s 渠道可满足当前显式输出预算 %d tokens（已过滤：%s）",
				kindDisplayName(kind), requirement.OutputTokens, strings.Join(skipped, "; "))
		}
		if maxKnownWindow > 0 {
			return nil, fmt.Errorf("没有 %s 渠道可承载当前上下文：输入估算 %d tokens，最大已知窗口 %d tokens（已过滤：%s）",
				kindDisplayName(kind), channelRequiredWindow, maxKnownWindow, strings.Join(skipped, "; "))
		}
		return nil, fmt.Errorf("没有 %s 渠道可承载当前上下文：输入估算 %d tokens，所有候选渠道上下文能力未知或不足（已过滤：%s）",
			kindDisplayName(kind), channelRequiredWindow, strings.Join(skipped, "; "))
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

// preferredTraceChannel 对加密续轮优先恢复成功渠道，普通亲和仍按上下文桶和 TTL 隔离。
func (s *ChannelScheduler) preferredTraceChannel(opts SelectionOptions, affinityUserID string, channels []ChannelInfo) (int, bool) {
	if opts.Kind == ChannelKindResponses && opts.PreserveTraceAffinity {
		if s.conversationTracker != nil {
			if channel, ok := s.conversationTracker.GetSuccessfulChannel(string(opts.Kind), opts.UserID, opts.AgentRole); ok {
				// 渠道重排后按名称恢复，避免旧索引指向另一个上游；过滤掉的来源仍走正常 failover。
				for _, candidate := range channels {
					if candidate.Name == channel.ChannelName {
						return candidate.Index, true
					}
				}
				return -1, true
			}
		}
		if preferred, ok := s.traceAffinity.GetPreferredChannel(traceAffinityKey(opts.Kind, affinityUserID, nil)); ok {
			return preferred, true
		}
	}
	return s.traceAffinity.GetPreferredChannel(traceAffinityKey(opts.Kind, affinityUserID, opts.ContextRequirement))
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
func (s *ChannelScheduler) findPromotedChannel(cfg config.Config, activeChannels []ChannelInfo, kind ChannelKind) *ChannelInfo {
	for i := range activeChannels {
		ch := &activeChannels[i]
		if ch.Status != "active" {
			continue
		}
		upstream := upstreamByIndex(cfg, ch.Index, kind)
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
	return s.selectFallbackChannelWithRecord(s.ConfigSnapshot(), activeChannels, failedChannels, kind, true)
}

func (s *ChannelScheduler) selectFallbackChannelWithRecord(
	cfg config.Config,
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
	kind ChannelKind,
	record bool,
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

		upstream := upstreamByIndex(cfg, ch.Index, kind)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			continue
		}

		channelState := s.channelCircuitState(upstream, kind)
		if channelState == metrics.CircuitStateOpen {
			continue
		}
		if s.channelInRuntimeCooldown(kind, ch.Index) {
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
		return s.selectionResultWithRecord(kind, bestUpstream, bestChannel.Index, "fallback", record), nil
	}

	return nil, fmt.Errorf("所有渠道都不可用")
}

// ChannelInfo 渠道信息（用于排序）
// Priority 约定为非负整数，数字越小优先级越高；0 表示未显式配置，将回退为渠道索引。
type ChannelInfo struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	Priority    int    `json:"priority"`
	Status      string `json:"status"`
	CircuitOpen bool   `json:"circuitOpen,omitempty"`
}

// getActiveChannels 获取活跃渠道列表（按优先级排序）
func (s *ChannelScheduler) getActiveChannels(kind ChannelKind, model string) []ChannelInfo {
	return s.getActiveChannelsWithTrace(s.ConfigSnapshot(), kind, model, "", nil, false, nil)
}

func (s *ChannelScheduler) getActiveChannelsWithTrace(cfg config.Config, kind ChannelKind, model, agentRole string, subagentRouting *config.SubagentRoutingPolicy, applySubagentRouting bool, trace *SelectionTrace) []ChannelInfo {
	upstreams := config.UpstreamsForProtocol(cfg, string(kind))
	policyByUID := map[string]config.SubagentRoutingChannel{}
	policyOrder := map[string]int{}
	if applySubagentRouting && subagentRouting != nil {
		for i, entry := range subagentRouting.Channels {
			policyByUID[entry.ChannelUID] = entry
			policyOrder[entry.ChannelUID] = i
		}
	}

	var activeChannels []ChannelInfo
	for i, upstream := range upstreams {
		status := upstream.Status
		if status == "" {
			status = "active"
		}
		priority := upstream.Priority
		if priority == 0 {
			priority = i
		}
		if applySubagentRouting && subagentRouting != nil {
			entry, ok := policyByUID[upstream.ChannelUID]
			if !ok || !entry.Enabled {
				trace.skipChannel(ChannelInfo{Index: i, Name: upstream.Name, Priority: priority, Status: status}, "subagent_policy_filter", "not_enabled", upstream.ChannelUID)
				continue
			}
			priority = policyOrder[upstream.ChannelUID]
		}
		ch := ChannelInfo{Index: i, Name: upstream.Name, Priority: priority, Status: status}
		if status == "disabled" {
			trace.skipChannel(ch, "active_model_filter", "disabled_status", status)
			continue
		}
		effectiveUpstream := config.WithSubagentRouting(&upstream, subagentRouting, applySubagentRouting)
		requestModel := config.RequestModelForAgent(model, effectiveUpstream, agentRole)
		if requestModel != "" {
			supported, reason := effectiveUpstream.ExplainModelSupport(requestModel)
			if !supported {
				prefix := kindSchedulerLogPrefix(kind)
				log.Printf("[%s-ModelFilter] 跳过渠道 [%d] %s: 模型 %q 不被 supportedModels 支持 (%s)", prefix, i, upstream.Name, requestModel, reason)
				trace.skipChannel(ch, "active_model_filter", "unsupported_model", reason)
				continue
			}
		}
		activeChannels = append(activeChannels, ch)
	}

	sort.SliceStable(activeChannels, func(i, j int) bool {
		return activeChannels[i].Priority < activeChannels[j].Priority
	})
	return activeChannels
}

// findBestAvailableChannelPriority 找到当前最佳可用渠道的优先级（用于 affinity 覆盖判断）
// 返回 -1 表示没有可用渠道
func (s *ChannelScheduler) findBestAvailableChannelPriority(
	cfg config.Config,
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
	kind ChannelKind,
	model string,
	agentRole string,
	subagentRouting *config.SubagentRoutingPolicy,
	applySubagentRouting bool,
) int {
	bestPriority := -1

	for _, ch := range activeChannels {
		if failedChannels[ch.Index] || ch.Status != "active" {
			continue
		}

		upstream := upstreamByIndex(cfg, ch.Index, kind)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			continue
		}
		if !s.channelIsRuntimeAvailable(upstream, kind, ch.Index) {
			continue
		}
		effectiveUpstream := config.WithSubagentRouting(upstream, subagentRouting, applySubagentRouting)
		requestModel := config.RequestModelForAgent(model, effectiveUpstream, agentRole)
		if deferred, _, _, _ := s.channelRateLimitSoftDeferred(upstream, kind, ch.Index, requestModel, time.Now()); deferred {
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
	return upstreamByIndex(s.ConfigSnapshot(), index, kind)
}

func upstreamByIndex(cfg config.Config, index int, kind ChannelKind) *config.UpstreamConfig {
	upstreams := config.UpstreamsForProtocol(cfg, string(kind))
	if index < 0 || index >= len(upstreams) {
		return nil
	}
	return upstreams[index].Clone()
}
