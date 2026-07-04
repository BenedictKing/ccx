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
	kind := opts.Kind
	model := opts.Model
	routePrefix := opts.RoutePrefix
	channelName := opts.ChannelName

	// 获取活跃渠道列表（含模型过滤）
	activeChannels := s.getActiveChannels(kind, model)
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
			return nil, fmt.Errorf("没有 %s 渠道支持模型 %q，请检查渠道的 supportedModels 配置", kindName, model)
		}
		return nil, fmt.Errorf("没有可用的活跃 %s 渠道", kindName)
	}

	// 按路由前缀过滤渠道
	if routePrefix != "" {
		// 有前缀：仅选择匹配的渠道
		var filtered []ChannelInfo
		for _, ch := range activeChannels {
			upstream := s.getUpstreamByIndex(ch.Index, kind)
			if upstream != nil && upstream.RoutePrefix == routePrefix {
				filtered = append(filtered, ch)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("no channels with route prefix: %s", routePrefix)
		}
		activeChannels = filtered
	} else {
		// 无前缀：排除设了路由前缀的渠道（它们只能通过前缀访问）
		var filtered []ChannelInfo
		for _, ch := range activeChannels {
			upstream := s.getUpstreamByIndex(ch.Index, kind)
			if upstream != nil && upstream.RoutePrefix == "" {
				filtered = append(filtered, ch)
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
			return nil, fmt.Errorf("没有可用于默认路由的 %s 渠道，请使用带前缀路由访问", kindName)
		}
		activeChannels = filtered
	}

	activeChannels, err := s.filterChannelsByContext(activeChannels, kind, model, opts.ContextRequirement)
	if err != nil {
		return nil, err
	}
	if opts.CandidateFilter != nil {
		activeChannels, err = opts.CandidateFilter(activeChannels, func(ch ChannelInfo) *config.UpstreamConfig {
			return s.getUpstreamByIndex(ch.Index, kind)
		}, func(ch ChannelInfo, upstream *config.UpstreamConfig) bool {
			return s.channelAvailableForCandidateFilter(ch, upstream, kind)
		})
		if err != nil {
			return nil, err
		}
		if len(activeChannels) == 0 {
			return nil, fmt.Errorf("没有可用的 %s 渠道满足候选过滤条件", kindDisplayName(kind))
		}
	}

	// 指定渠道名（X-Channel 头）：在模型、路由前缀与上下文过滤后直接定位。
	if channelName != "" {
		for _, ch := range activeChannels {
			if ch.Name == channelName {
				if failedChannels[ch.Index] {
					return nil, fmt.Errorf("指定渠道 %q 在本次请求中已失败", channelName)
				}
				upstream := s.getUpstreamByIndex(ch.Index, kind)
				if upstream == nil {
					return nil, fmt.Errorf("指定渠道 %q 配置异常", channelName)
				}
				prefix := kindSchedulerLogPrefix(kind)
				log.Printf("[%s-Pin] 通过 X-Channel 指定渠道: [%d] %s", prefix, ch.Index, ch.Name)
				return s.selectionResult(kind, upstream, ch.Index, "channel_pin"), nil
			}
		}
		return nil, fmt.Errorf("指定渠道 %q 不满足当前模型、路由前缀或上下文要求", channelName)
	}

	// 0. 检查手动序列覆盖
	if userID != "" && s.overrideManager != nil {
		if sequence, ok := s.overrideManager.GetOverrideForUserWithRole(string(kind), userID, opts.AgentRole); ok {
			prefix := kindSchedulerLogPrefix(kind)
			orderedChannels := applyManualOverrideOrder(activeChannels, sequence)
			for _, ch := range orderedChannels {
				if failedChannels[ch.Index] {
					continue
				}
				if ch.Status != "active" {
					continue
				}
				upstream := s.getUpstreamByIndex(ch.Index, kind)
				if upstream != nil && s.channelIsRuntimeAvailable(upstream, kind, ch.Index) {
					log.Printf("[%s-Override] 按手动排序选择渠道: [%d] %s (user: %s, role=%s, sequenceHead=%s)", prefix, ch.Index, ch.Name, maskUserID(userID), schedulerAgentRoleForLog(opts.AgentRole), formatOverrideSequenceHead(sequence, 3))
					// Idle 续期：对话活跃时延长 override TTL
					s.overrideManager.RefreshOverrideForUser(string(kind), userID)
					return s.selectionResult(kind, upstream, ch.Index, "manual_override"), nil
				}
			}
			log.Printf("[%s-Override] 手动排序序列中无当前可用渠道，保留排序并回退默认调度 (user: %s, role=%s, sequenceHead=%s)", prefix, maskUserID(userID), schedulerAgentRoleForLog(opts.AgentRole), formatOverrideSequenceHead(sequence, 3))
		}
	}

	// 1. 检查促销期渠道（手动覆盖之后，绕过健康检查）
	promotedChannel := s.findPromotedChannel(activeChannels, kind)
	if promotedChannel != nil && !failedChannels[promotedChannel.Index] {
		// 促销渠道存在且未失败，直接使用（不检查健康状态，让用户设置的促销渠道有机会尝试）
		upstream := s.getUpstreamByIndex(promotedChannel.Index, kind)
		if upstream != nil && len(upstream.APIKeys) > 0 && !s.channelInRuntimeCooldown(kind, promotedChannel.Index) {
			failureRate := s.channelFailureRate(upstream, kind)
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Promotion] 促销期优先选择渠道: [%d] %s (失败率: %.1f%%, 绕过健康检查)", prefix, promotedChannel.Index, upstream.Name, failureRate*100)
			return s.selectionResult(kind, upstream, promotedChannel.Index, "promotion_priority"), nil
		} else if upstream != nil {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Promotion] 警告: 促销渠道 [%d] %s 无可用密钥，跳过", prefix, promotedChannel.Index, upstream.Name)
		}
	} else if promotedChannel != nil {
		prefix := kindSchedulerLogPrefix(kind)
		log.Printf("[%s-Promotion] 警告: 促销渠道 [%d] %s 已在本次请求中失败，跳过", prefix, promotedChannel.Index, promotedChannel.Name)
	}

	// 1. 检查 Trace 亲和性（促销渠道失败时或无促销渠道时）
	if userID != "" {
		compositeKey := traceAffinityKey(kind, affinityUserID, opts.ContextRequirement)
		if preferredIdx, ok := s.traceAffinity.GetPreferredChannel(compositeKey); ok {
			bestPriority := s.findBestAvailableChannelPriority(activeChannels, failedChannels, kind, model)
			for _, ch := range activeChannels {
				if ch.Index == preferredIdx && !failedChannels[preferredIdx] {
					// 检查渠道状态：只有 active 状态才使用亲和性
					if ch.Status != "active" {
						prefix := kindSchedulerLogPrefix(kind)
						log.Printf("[%s-Affinity] 跳过亲和渠道 [%d] %s: 状态为 %s (user: %s)", prefix, preferredIdx, ch.Name, ch.Status, maskUserID(userID))
						continue
					}
					// 如果存在更高优先级且健康的候选渠道，允许优先级覆盖亲和性
					if bestPriority >= 0 && ch.Priority > bestPriority {
						prefix := kindSchedulerLogPrefix(kind)
						log.Printf("[%s-Affinity] 跳过亲和渠道 [%d] %s: 存在更高优先级可用渠道 (亲和优先级: %d, 最优优先级: %d, user: %s)", prefix, preferredIdx, ch.Name, ch.Priority, bestPriority, maskUserID(userID))
						continue
					}
					// 检查渠道是否健康且未处于运行态冷却
					upstream := s.getUpstreamByIndex(preferredIdx, kind)
					if upstream != nil && s.channelIsRuntimeAvailable(upstream, kind, preferredIdx) {
						prefix := kindSchedulerLogPrefix(kind)
						log.Printf("[%s-Affinity] Trace亲和选择渠道: [%d] %s (user: %s)", prefix, preferredIdx, upstream.Name, maskUserID(userID))
						return s.selectionResult(kind, upstream, preferredIdx, "trace_affinity"), nil
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
			continue
		}

		// 跳过非 active 状态的渠道（suspended 等）
		if ch.Status != "active" {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Channel] 跳过非活跃渠道: [%d] %s (状态: %s)", prefix, ch.Index, ch.Name, ch.Status)
			continue
		}

		upstream := s.getUpstreamByIndex(ch.Index, kind)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			continue
		}

		// 跳过失败率过高的渠道（已熔断或即将熔断）
		channelState := s.channelCircuitState(upstream, kind)
		if channelState == metrics.CircuitStateOpen || !s.channelIsHealthy(upstream, kind) {
			failureRate := s.channelFailureRate(upstream, kind)
			prefix := kindSchedulerLogPrefix(kind)
			if channelState == metrics.CircuitStateOpen {
				log.Printf("[%s-Channel] 警告: 跳过 open 渠道: [%d] %s (失败率: %.1f%%)", prefix, ch.Index, ch.Name, failureRate*100)
			} else {
				log.Printf("[%s-Channel] 警告: 跳过不健康渠道: [%d] %s (失败率: %.1f%%)", prefix, ch.Index, ch.Name, failureRate*100)
			}
			continue
		}

		// 跳过运行态 cooldown 中的渠道（如 429 Retry-After 或上游账号池临时不可用）
		if s.channelInRuntimeCooldown(kind, ch.Index) {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Channel] 跳过运行态 cooldown 中的渠道: [%d] %s", prefix, ch.Index, ch.Name)
			continue
		}

		if deferred, ratio, scope, cooldown := s.channelRateLimitSoftDeferred(upstream, kind, ch.Index, model, time.Now()); deferred {
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
			continue
		}

		if shouldReserveVisionChannelForText(kind, opts.HasImageContent, upstream, softSkipped) {
			prefix := kindSchedulerLogPrefix(kind)
			log.Printf("[%s-Vision] 文本请求保留可识图渠道: [%d] %s (待普通文本渠道水位 >= %.0f%% 后再作为溢出池)",
				prefix, ch.Index, upstream.Name, rateLimitVisionReserveWatermark*100)
			continue
		}

		prefix := kindSchedulerLogPrefix(kind)
		log.Printf("[%s-Channel] 选择渠道: [%d] %s (优先级: %d)", prefix, ch.Index, upstream.Name, ch.Priority)
		return s.selectionResult(kind, upstream, ch.Index, "priority_order"), nil
	}

	for _, skipped := range softSkipped {
		if skipped.upstream == nil || !s.channelIsRuntimeAvailable(skipped.upstream, kind, skipped.channel.Index) {
			continue
		}
		prefix := kindSchedulerLogPrefix(kind)
		log.Printf("[%s-RateLimit] 所有低水位候选不可用，回退选择高水位渠道: [%d] %s scope=%s usage=%.0f%%",
			prefix, skipped.channel.Index, skipped.upstream.Name, skipped.scope, skipped.ratio*100)
		return s.selectionResult(kind, skipped.upstream, skipped.channel.Index, "rate_limit_pressure"), nil
	}

	// 3. 所有健康渠道都失败，选择失败率最低的作为降级
	return s.selectFallbackChannel(activeChannels, failedChannels, kind)
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

func (s *ChannelScheduler) channelCircuitState(upstream *config.UpstreamConfig, kind ChannelKind) metrics.CircuitState {
	if upstream == nil {
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

func (s *ChannelScheduler) filterChannelsByContext(activeChannels []ChannelInfo, kind ChannelKind, model string, requirement *ContextRequirement) ([]ChannelInfo, error) {
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
		upstream := s.getUpstreamByIndex(ch.Index, kind)
		if upstream == nil {
			continue
		}

		resolved := config.ResolveUpstreamCapability(model, upstream, cfg.UpstreamModelCapabilities)
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
		upstream := s.getUpstreamByIndex(ch.Index, kind)
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
		return s.selectionResult(kind, bestUpstream, bestChannel.Index, "fallback"), nil
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

// findBestAvailableChannelPriority 找到当前最佳可用渠道的优先级（用于 affinity 覆盖判断）
// 返回 -1 表示没有可用渠道
func (s *ChannelScheduler) findBestAvailableChannelPriority(
	activeChannels []ChannelInfo,
	failedChannels map[int]bool,
	kind ChannelKind,
	model string,
) int {
	bestPriority := -1

	for _, ch := range activeChannels {
		if failedChannels[ch.Index] || ch.Status != "active" {
			continue
		}

		upstream := s.getUpstreamByIndex(ch.Index, kind)
		if upstream == nil || len(upstream.APIKeys) == 0 {
			continue
		}
		if !s.channelIsRuntimeAvailable(upstream, kind, ch.Index) {
			continue
		}
		if deferred, _, _, _ := s.channelRateLimitSoftDeferred(upstream, kind, ch.Index, model, time.Now()); deferred {
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

	if index >= 0 && index < len(upstreams) {
		// 返回副本，避免返回指向 slice 元素的指针
		upstream := upstreams[index]
		return &upstream
	}
	return nil
}
