package autopilot

import (
	"log"
	"sort"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

// ── EndpointAttemptPolicy（设计 §4.6.2 + §4.6.2a）──
//
// 职责：在 TryUpstreamWithAllKeys 的 baseURL/key 双层循环中，
// 提供 endpoint 级别的过滤与排序策略。
//
// 本文件只做 autopilot 包内的策略类型与构建逻辑，
// 不碰 handlers/common（WithEndpointAttemptPolicy 注入在 integration 阶段统一接线）。
//
// 模式门控：
//   - dry_run → 计算评分并记录 trace，不应用健康优化；仍执行模型兼容硬约束
//   - 其余模式 → binding 级真实过滤（健康 / 衰减 / 模型能力）+ 排序，未知画像 fail-open

// ── EndpointCandidate（§4.6.2 契约）──

// EndpointCandidate 描述一个具体的 endpoint 候选（baseURL + key 组合）。
// 用于 EndpointAttemptPolicy 的过滤与排序输入/输出。
type EndpointCandidate struct {
	ChannelUID          string      `json:"channelUid"`                    // 渠道唯一标识
	ChannelName         string      `json:"channelName,omitempty"`         // 渠道显示名
	ChannelKind         string      `json:"channelKind"`                   // messages | chat | responses | ...
	MetricsKey          string      `json:"metricsKey,omitempty"`          // 画像存储键（已脱敏）
	BaseURL             string      `json:"baseUrl"`                       // 原始配置 URL
	KeyMask             string      `json:"keyMask,omitempty"`             // 掩码后的 key，如 sk-***abc
	MappedModel         string      `json:"mappedModel,omitempty"`         // 可选模型覆盖
	MappedEffort        EffortLevel `json:"mappedEffort,omitempty"`        // 自动映射的 effort 级别
	MappedEffortDecided bool        `json:"mappedEffortDecided,omitempty"` // effort 是否由 Autopilot 决定
	EndpointUID         string      `json:"endpointUid,omitempty"`         // 稳定 endpoint ID
	Score               float64     `json:"score"`                         // endpoint 级评分
	Reason              string      `json:"reason,omitempty"`              // 评分/排序原因

	// ── 评分明细（供 trace 展示）──
	HealthScore    float64 `json:"healthScore"`
	FastDecayScore float64 `json:"fastDecayScore"`
	SuccessRate    float64 `json:"successRate"`
	LatencyScore   float64 `json:"latencyScore"`
	CostScore      float64 `json:"costScore"`

	// ── public-key-routing 运行时明细 ──
	ConsumptionPolicy        string  `json:"consumptionPolicy,omitempty"`
	ConfiguredCostMultiplier float64 `json:"configuredCostMultiplier,omitempty"`
	EffectiveCostUSD         float64 `json:"effectiveCostUsd,omitempty"`
	EffectiveCostReason      string  `json:"effectiveCostReason,omitempty"`
	Opportunistic            bool    `json:"opportunistic,omitempty"`
	HardFiltered             bool    `json:"hardFiltered,omitempty"`
	FastDecayFiltered        bool    `json:"fastDecayFiltered,omitempty"`
	UtilityScore             float64 `json:"utilityScore,omitempty"`
	Weight                   int     `json:"weight,omitempty"`
	OriginalIndex            int     `json:"originalIndex,omitempty"`

	// ── 来源信任等级（用于 tie-breaker）──
	OriginTier ChannelOriginTier `json:"originTier"` // first | second | third | local | unknown
}

// ── EndpointAttemptPolicy（§4.6.2 契约）──

// EndpointAttemptPolicy 为 TryUpstreamWithAllKeys 提供 endpoint 级策略。
// 通过四个函数字段实现 §4.6.2a 的四步插入点：
//
//  1. FilterURLs — 步骤 1：保留 URL 集合，硬过滤下沉到 binding
//  2. SortURLs — 步骤 2：按 EndpointCandidate.Score 降序排列 baseURL
//  3. FilterKeys — 步骤 5：移除 FastDecay 分数过低的 key
//  4. SortKeys — 步骤 6：按 EndpointCandidate.Score 降序排列 key
//
// 任一函数为 nil 时等同于原样返回（fail-open）。
type EndpointAttemptPolicy struct {
	// FilterURLs 对 baseURL 列表做过滤。
	// 输入为原始 URL 列表，输出为过滤后的 URL 列表。
	// FailOpen=true 时：过滤后为空则回退全量输入。
	FilterURLs func(urls []string) []string

	// SortURLs 对 baseURL 列表做排序。
	// 返回排序后的 URL 列表和每个 URL 对应的评分信息。
	// shadow 模式下记录评分但返回原始顺序。
	SortURLs func(urls []string) ([]string, []EndpointCandidate)

	// FilterKeys 对指定 baseURL 的 key 列表做过滤。
	// 输入为 baseURL 和候选 key 列表，输出为过滤后的 key 列表。
	// FailOpen=true 时：过滤后为空则回退全量输入。
	FilterKeys func(baseURL string, apiKeys []string) []string

	// SortKeys 对指定 baseURL 的 key 列表做排序。
	// 返回排序后的 key 列表和每个 key 对应的评分信息。
	// shadow 模式下记录评分但返回原始顺序。
	SortKeys func(baseURL string, apiKeys []string) ([]string, []EndpointCandidate)

	// FilterKeyBindings 对当前渠道内的 endpoint binding 做硬过滤。
	// 与 FilterKeys 不同，它携带 channelUID，可精确定位 channel + baseURL + key 画像。
	// 返回空列表表示当前端点没有可用于该请求模型的 binding，调用方不得 fail-open。
	FilterKeyBindings func(channelUID, baseURL string, apiKeys []string) []string

	// SortKeyBindings 对当前渠道内的 endpoint binding 做排序。
	SortKeyBindings func(channelUID, baseURL string, apiKeys []string) ([]string, []EndpointCandidate)

	// RequestModel 请求的目标模型（用于查找 endpoint 画像的 MappedModel）。
	RequestModel string

	// Mode 当前运行模式（用于 trace 记录）。
	Mode RoutingMode

	// ResolvedModelByEndpointUID 返回 endpointUID 的自动映射模型。
	// 由 SortURLs/SortKeys 阶段在评分时顺带填充，handlers 层在构建请求前查询。
	// 返回空串表示无映射（使用原始模型）。
	// 签名：(endpointUID) → mappedModel（空串 = 无映射）
	// 向后兼容：返回 ResolvedTargetByEndpointUID 的 .Model 字段。
	ResolvedModelByEndpointUID func(endpointUID string) string

	// ResolvedTargetByEndpointUID 返回 endpointUID 的完整路由目标（model + effort 原子决定）。
	// 由 SortURLs/SortKeys 阶段在评分时顺带填充，handlers 层在构建请求前查询。
	// 返回 nil 表示无映射（使用原始模型和 effort）。
	// 签名：(endpointUID) → *ResolvedRouteTarget（nil = 无映射）
	ResolvedTargetByEndpointUID func(endpointUID string) *ResolvedRouteTarget

	// ResolvedTargetForBinding 按最终选中的 channel + URL + Key 解析完整路由目标。
	// 发送阶段优先使用此入口，避免同 URL 多 Key 或旧画像 UID 导致缓存查找落空。
	ResolvedTargetForBinding func(channelUID, baseURL, apiKey string) *ResolvedRouteTarget

	// ResponseHeaderTimeoutForEndpoint 根据 endpoint TTFB 画像返回响应头超时建议。
	// 返回 0 表示样本不足或当前请求不应缩短超时。
	ResponseHeaderTimeoutForEndpoint func(endpointUID string, inheritedMs int, isStream bool) int
}

// ── EndpointPolicyDeps 依赖注入 ──

// EndpointPolicyDeps 封装 EndpointAttemptPolicy 构建所需的依赖。
// 通过依赖注入避免与 Manager 的循环引用。
type EndpointPolicyDeps struct {
	ProfileStore  *ProfileStore                        // endpoint 画像存储
	FastDecay     *FastDecayScorer                     // 快速衰减评分器
	TraceStore    *TraceStore                          // 路由决策追踪存储
	ModelResolver *ModelResolver                       // Phase 3B-2: 自动模型映射器（nil 时不触发自动映射）
	GetRoutingCfg func() config.AutopilotRoutingConfig // Phase 3B-2: 路由配置读取（用于 AutoResolve 门控）
	APIKeyConfigs []config.APIKeyConfig                // 当前上游的 key 配置视图（按 Key 精确匹配）
}

type keyBindingDecision struct {
	Key                      string
	Candidate                EndpointCandidate
	HardEligible             bool
	FastDecayEligible        bool
	ConsumptionPolicy        config.KeyConsumptionPolicy
	ConfiguredCostMultiplier float64
	EffectiveCostUSD         float64
	EffectiveCostReason      string
	Weight                   int
	OriginalIndex            int
}

func classifyKeyBinding(deps EndpointPolicyDeps, req *RequestProfile, channelUID, baseURL, apiKey string, originalIndex int) keyBindingDecision {
	decision := keyBindingDecision{
		Key:                      apiKey,
		HardEligible:             true,
		FastDecayEligible:        true,
		ConsumptionPolicy:        config.KeyConsumptionNormal,
		ConfiguredCostMultiplier: -1,
		EffectiveCostUSD:         -1,
		EffectiveCostReason:      "unconfigured",
		OriginalIndex:            originalIndex,
	}
	cand := scoreEndpointForBinding(deps.ProfileStore, deps.FastDecay, req.Model, channelUID, baseURL, apiKey, req, &deps)
	cand.OriginalIndex = originalIndex
	decision.Candidate = cand

	profile := findProfileForBinding(deps.ProfileStore, channelUID, baseURL, apiKey)
	if profile == nil {
		decision.Candidate.Reason = "no_profile"
		decision.Candidate.UtilityScore = 0
		return decision
	}
	cfg := findAPIKeyConfig(deps.APIKeyConfigs, apiKey)
	if cfg != nil {
		decision.ConsumptionPolicy = config.NormalizeKeyConsumptionPolicy(cfg.ConsumptionPolicy)
		decision.Candidate.ConsumptionPolicy = string(decision.ConsumptionPolicy)
		decision.Candidate.Opportunistic = decision.ConsumptionPolicy == config.KeyConsumptionOpportunistic
		decision.Weight = cfg.Weight
		decision.Candidate.Weight = cfg.Weight
		if cfg.GroupMultiplier != nil {
			decision.ConfiguredCostMultiplier = *cfg.GroupMultiplier
			decision.Candidate.ConfiguredCostMultiplier = *cfg.GroupMultiplier
			decision.EffectiveCostUSD = *cfg.GroupMultiplier
			decision.EffectiveCostReason = "configured_group_multiplier"
			decision.Candidate.EffectiveCostUSD = *cfg.GroupMultiplier
			decision.Candidate.EffectiveCostReason = "configured_group_multiplier"
		}
		eligibility := config.EvaluateAPIKeyMultiplierEligibility(*cfg, time.Now())
		if !eligibility.Eligible {
			decision.HardEligible = false
			decision.Candidate.HardFiltered = true
			decision.Candidate.Reason = string(eligibility.Reason)
			decision.Candidate.UtilityScore = 0
			return decision
		}
	}
	if !profileSupportsRequestModel(profile, req.Model, req, &deps) {
		decision.HardEligible = false
		decision.Candidate.HardFiltered = true
		decision.Candidate.Reason = "model_ineligible"
		decision.Candidate.UtilityScore = 0
		return decision
	}
	if profile.HealthState == HealthStateDead || profile.HealthState == HealthStateMisconfigured {
		decision.HardEligible = false
		decision.Candidate.HardFiltered = true
		decision.Candidate.Reason = "health_ineligible"
		decision.Candidate.UtilityScore = 0
		return decision
	}
	if deps.FastDecay != nil && decision.ConsumptionPolicy == config.KeyConsumptionOpportunistic {
		if deps.FastDecay.Score(profile.EndpointUID) < fastDecayFilterThreshold {
			decision.FastDecayEligible = false
			decision.Candidate.FastDecayFiltered = true
			decision.Candidate.Reason = "opportunistic_decayed"
		}
	}
	// 效用分：healthScore × fastDecayScore × 策略倍数
	// opportunistic ×1.5 → 严重降级后会自动输给健康 normal（边界 health×decay < 0.67）
	hs := endpointHealthScore(profile.HealthState)
	fs := 1.0
	if deps.FastDecay != nil {
		fs = deps.FastDecay.Score(profile.EndpointUID)
	}
	multiplier := 1.0
	if decision.ConsumptionPolicy == config.KeyConsumptionOpportunistic {
		multiplier = opportunisticUtilityMultiplier
	}
	decision.Candidate.UtilityScore = hs * fs * multiplier
	return decision
}

// ── 构建入口 ──

// BuildEndpointPolicy 为给定请求构建 EndpointAttemptPolicy。
// 返回 nil 表示不注入（off / kill switch / nil req）。
//
// 模式门控：
//   - off / kill switch → nil
//   - shadow / dry_run → 计算评分 + 记录 trace，仅执行模型兼容硬约束
//   - assist → 真实排序，仅执行模型兼容硬约束
//   - auto → binding 级真实过滤（健康 / 衰减 / 模型能力）+ 排序，未知画像 fail-open
func findAPIKeyConfig(configs []config.APIKeyConfig, apiKey string) *config.APIKeyConfig {
	if apiKey == "" {
		return nil
	}
	for i := range configs {
		if configs[i].Key == apiKey {
			cfg := configs[i]
			return &cfg
		}
	}
	return nil
}

func BuildEndpointPolicy(deps EndpointPolicyDeps, req *RequestProfile, mode RoutingMode) *EndpointAttemptPolicy {
	if req == nil {
		return nil
	}

	if mode == RoutingModeDryRun {
		return buildShadowPolicy(deps, req)
	}
	return buildActivePolicy(deps, req, true)
}

// buildShadowPolicy 构建 shadow 模式的策略。
// 计算评分 + 记录 trace，但原样返回输入。
func buildShadowPolicy(deps EndpointPolicyDeps, req *RequestProfile) *EndpointAttemptPolicy {
	modelByUID := make(map[string]string)                // endpointUID → mappedModel（向后兼容）
	targetByUID := make(map[string]*ResolvedRouteTarget) // endpointUID → 完整路由目标

	policy := &EndpointAttemptPolicy{
		RequestModel: req.Model,
		Mode:         RoutingModeShadow,
	}

	// shadow FilterURLs：原样返回
	policy.FilterURLs = func(urls []string) []string {
		return urls
	}

	// shadow SortURLs：计算评分 + 记录 trace，返回原始顺序
	policy.SortURLs = func(urls []string) ([]string, []EndpointCandidate) {
		startTime := time.Now()
		candidates := make([]EndpointCandidate, 0, len(urls))
		for _, url := range urls {
			cand := scoreEndpointForURL(deps.ProfileStore, deps.FastDecay, req.Model, url, req, &deps)
			candidates = append(candidates, cand)
		}

		// 记录 trace
		trace := buildEndpointTrace(req, candidates, "url_shadow", startTime)
		if deps.TraceStore != nil && trace != nil {
			deps.TraceStore.Record(trace)
		}

		log.Printf("[EndpointPolicy-Shadow] model=%s urls=%d scored=%d duration=%dms",
			req.Model, len(urls), len(candidates), trace.DurationMs)

		// 填充 MappedModel 映射（shadow 模式也需要，供 handlers 层读取）
		for _, cand := range candidates {
			if cand.MappedModel != "" && cand.EndpointUID != "" {
				modelByUID[cand.EndpointUID] = cand.MappedModel
			}
			recordTargetByUID(cand, targetByUID)
		}

		// 原样返回
		return urls, candidates
	}

	// shadow FilterKeys：原样返回
	policy.FilterKeys = func(baseURL string, apiKeys []string) []string {
		return apiKeys
	}

	// shadow SortKeys：计算评分 + 记录 trace，返回原始顺序
	policy.SortKeys = func(baseURL string, apiKeys []string) ([]string, []EndpointCandidate) {
		startTime := time.Now()
		candidates := make([]EndpointCandidate, 0, len(apiKeys))
		for _, key := range apiKeys {
			cand := scoreEndpointForKey(deps.ProfileStore, deps.FastDecay, req.Model, baseURL, key, req, &deps)
			candidates = append(candidates, cand)
		}

		// 记录 trace
		trace := buildEndpointTrace(req, candidates, "key_shadow", startTime)
		if deps.TraceStore != nil && trace != nil {
			deps.TraceStore.Record(trace)
		}

		log.Printf("[EndpointPolicy-Shadow] model=%s baseURL=%s keys=%d scored=%d duration=%dms",
			req.Model, baseURL, len(apiKeys), len(candidates), trace.DurationMs)

		// 填充 MappedModel 映射（shadow 模式也需要，供 handlers 层读取）
		for _, cand := range candidates {
			if cand.MappedModel != "" && cand.EndpointUID != "" {
				modelByUID[cand.EndpointUID] = cand.MappedModel
			}
			recordTargetByUID(cand, targetByUID)
		}

		// 原样返回
		return apiKeys, candidates
	}
	policy.FilterKeyBindings = func(channelUID, baseURL string, apiKeys []string) []string {
		return filterKeyBindings(deps, req, channelUID, baseURL, apiKeys, false)
	}
	policy.SortKeyBindings = func(channelUID, baseURL string, apiKeys []string) ([]string, []EndpointCandidate) {
		sorted, cands := scoreAndSortKeyBindings(deps, req, targetByUID, channelUID, baseURL, apiKeys, false)
		for _, cand := range cands {
			if cand.MappedModel != "" && cand.EndpointUID != "" {
				modelByUID[cand.EndpointUID] = cand.MappedModel
			}
		}
		return sorted, cands
	}

	policy.ResolvedModelByEndpointUID = buildResolvedModelLookup(modelByUID)
	policy.ResolvedTargetByEndpointUID = buildResolvedTargetLookup(targetByUID, deps, req)
	policy.ResolvedTargetForBinding = buildResolvedTargetForBinding(targetByUID, deps, req)
	policy.ResponseHeaderTimeoutForEndpoint = buildResponseHeaderTimeoutLookup(deps.ProfileStore, req)
	return policy
}

// buildActivePolicy 构建 active 模式的策略。
// enableFilter=false（assist）：只排序不删减；enableFilter=true（auto）：过滤+排序。
func buildActivePolicy(deps EndpointPolicyDeps, req *RequestProfile, enableFilter bool) *EndpointAttemptPolicy {
	modelByUID := make(map[string]string)                // endpointUID → mappedModel（向后兼容）
	targetByUID := make(map[string]*ResolvedRouteTarget) // endpointUID → 完整路由目标

	policy := &EndpointAttemptPolicy{
		RequestModel: req.Model,
		Mode:         RoutingModeActive,
	}

	// URL 本身不是调度最小单元。同一 URL 下可能同时存在 healthy/dead 的 Key，
	// 因此不能根据任意一条画像删除整个 URL；硬过滤统一在 FilterKeyBindings 完成。
	policy.FilterURLs = func(urls []string) []string {
		return urls
	}

	// active SortURLs：按评分降序排列（含 panic 恢复）
	policy.SortURLs = func(urls []string) ([]string, []EndpointCandidate) {
		startTime := time.Now()

		// panic 恢复：排序异常时回退原列表
		var sortedURLs []string
		var candidates []EndpointCandidate
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[EndpointPolicy-PanicRecover] SortURLs panic recovered: %v", r)
					sortedURLs = nil
					candidates = nil
				}
			}()
			cands := make([]EndpointCandidate, 0, len(urls))
			for _, url := range urls {
				cand := scoreEndpointForURL(deps.ProfileStore, deps.FastDecay, req.Model, url, req, &deps)
				cands = append(cands, cand)
			}

			// 按评分降序排序
			sorted := make([]string, len(urls))
			copy(sorted, urls)
			sortEndpointsByScore(sorted, cands)

			sortedURLs = sorted
			candidates = cands
		}()

		// panic 回退：使用原列表
		if sortedURLs == nil {
			sortedURLs = make([]string, len(urls))
			copy(sortedURLs, urls)
			candidates = make([]EndpointCandidate, len(urls))
		}

		// 记录 trace
		trace := buildEndpointTrace(req, candidates, "url_active", startTime)
		if deps.TraceStore != nil && trace != nil {
			deps.TraceStore.Record(trace)
		}

		log.Printf("[EndpointPolicy-Active] model=%s urls=%d sorted top=%v duration=%dms",
			req.Model, len(sortedURLs), topN(sortedURLs, 3), trace.DurationMs)

		// 填充 MappedModel 映射（供 handlers 层读取）
		for _, cand := range candidates {
			if cand.MappedModel != "" && cand.EndpointUID != "" {
				modelByUID[cand.EndpointUID] = cand.MappedModel
			}
			recordTargetByUID(cand, targetByUID)
		}

		return sortedURLs, candidates
	}

	if enableFilter {
		// ── auto 模式：过滤 FastDecay 分数过低的 key ──
		policy.FilterKeys = func(baseURL string, apiKeys []string) []string {
			if len(apiKeys) == 0 {
				return apiKeys
			}
			filtered := make([]string, 0, len(apiKeys))
			for _, key := range apiKeys {
				keyHash := KeyHashFromAPIKey(key)
				endpointUID := GenerateEndpointUID("", baseURL, keyHash)
				if deps.FastDecay != nil {
					decayScore := deps.FastDecay.Score(endpointUID)
					if decayScore < fastDecayFilterThreshold {
						continue
					}
				}
				filtered = append(filtered, key)
			}
			// FailOpen：过滤后为空则回退全量
			if len(filtered) == 0 {
				return apiKeys
			}
			return filtered
		}
	} else {
		// ── assist 模式：原样返回 ──
		policy.FilterKeys = func(baseURL string, apiKeys []string) []string {
			return apiKeys
		}
	}

	// active SortKeys：按评分降序排列（含 panic 恢复）
	policy.SortKeys = func(baseURL string, apiKeys []string) ([]string, []EndpointCandidate) {
		startTime := time.Now()

		// panic 恢复：排序异常时回退原列表
		var sortedKeys []string
		var candidates []EndpointCandidate
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[EndpointPolicy-PanicRecover] SortKeys panic recovered: %v", r)
					sortedKeys = nil
					candidates = nil
				}
			}()
			cands := make([]EndpointCandidate, 0, len(apiKeys))
			for _, key := range apiKeys {
				cand := scoreEndpointForKey(deps.ProfileStore, deps.FastDecay, req.Model, baseURL, key, req, &deps)
				cands = append(cands, cand)
			}

			// 按评分降序排序
			sorted := make([]string, len(apiKeys))
			copy(sorted, apiKeys)
			sortEndpointsByScore(sorted, cands)

			sortedKeys = sorted
			candidates = cands
		}()

		// panic 回退：使用原列表
		if sortedKeys == nil {
			sortedKeys = make([]string, len(apiKeys))
			copy(sortedKeys, apiKeys)
			candidates = make([]EndpointCandidate, len(apiKeys))
		}

		// 记录 trace
		trace := buildEndpointTrace(req, candidates, "key_active", startTime)
		if deps.TraceStore != nil && trace != nil {
			deps.TraceStore.Record(trace)
		}

		log.Printf("[EndpointPolicy-Active] model=%s baseURL=%s keys=%d sorted top=%v duration=%dms",
			req.Model, baseURL, len(sortedKeys), topN(sortedKeys, 3), trace.DurationMs)

		// 填充 MappedModel 映射（供 handlers 层读取）
		for _, cand := range candidates {
			if cand.MappedModel != "" && cand.EndpointUID != "" {
				modelByUID[cand.EndpointUID] = cand.MappedModel
			}
			recordTargetByUID(cand, targetByUID)
		}

		return sortedKeys, candidates
	}

	policy.FilterKeyBindings = func(channelUID, baseURL string, apiKeys []string) []string {
		return filterKeyBindings(deps, req, channelUID, baseURL, apiKeys, enableFilter)
	}
	policy.SortKeyBindings = func(channelUID, baseURL string, apiKeys []string) ([]string, []EndpointCandidate) {
		sorted, cands := scoreAndSortKeyBindings(deps, req, targetByUID, channelUID, baseURL, apiKeys, true)
		for _, cand := range cands {
			if cand.MappedModel != "" && cand.EndpointUID != "" {
				modelByUID[cand.EndpointUID] = cand.MappedModel
			}
		}
		return sorted, cands
	}

	policy.ResolvedModelByEndpointUID = buildResolvedModelLookup(modelByUID)
	policy.ResolvedTargetByEndpointUID = buildResolvedTargetLookup(targetByUID, deps, req)
	policy.ResolvedTargetForBinding = buildResolvedTargetForBinding(targetByUID, deps, req)
	policy.ResponseHeaderTimeoutForEndpoint = buildResponseHeaderTimeoutLookup(deps.ProfileStore, req)
	return policy
}

// filterKeyBindings 将正确性约束与调度优化分开：
// 已知模型不兼容在所有模式下都必须排除；健康和衰减只在 auto 模式参与硬过滤。
func filterKeyBindings(deps EndpointPolicyDeps, req *RequestProfile, channelUID, baseURL string, apiKeys []string, filterOperationalState bool) []string {
	if len(apiKeys) == 0 {
		return apiKeys
	}
	filtered := make([]string, 0, len(apiKeys))
	for idx, key := range apiKeys {
		decision := classifyKeyBinding(deps, req, channelUID, baseURL, key, idx)
		if !decision.HardEligible {
			continue
		}
		if filterOperationalState && decision.ConsumptionPolicy == config.KeyConsumptionOpportunistic && !decision.FastDecayEligible {
			continue
		}
		filtered = append(filtered, key)
	}
	return filtered
}

func scoreAndSortKeyBindings(deps EndpointPolicyDeps, req *RequestProfile, targetByUID map[string]*ResolvedRouteTarget, channelUID, baseURL string, apiKeys []string, active bool) ([]string, []EndpointCandidate) {
	decisions := make([]keyBindingDecision, 0, len(apiKeys))
	candidates := make([]EndpointCandidate, 0, len(apiKeys))
	for idx, key := range apiKeys {
		decision := classifyKeyBinding(deps, req, channelUID, baseURL, key, idx)
		decisions = append(decisions, decision)
		candidates = append(candidates, decision.Candidate)
		recordTargetByUID(decision.Candidate, targetByUID)
	}
	sorted := append([]string(nil), apiKeys...)
	if active {
		sort.SliceStable(sorted, func(i, j int) bool {
			var left, right keyBindingDecision
			for _, d := range decisions {
				if d.Key == sorted[i] && left.Key == "" {
					left = d
				}
				if d.Key == sorted[j] && right.Key == "" {
					right = d
				}
			}
			if left.Candidate.UtilityScore != right.Candidate.UtilityScore {
				return left.Candidate.UtilityScore > right.Candidate.UtilityScore
			}
			if left.EffectiveCostUSD >= 0 && right.EffectiveCostUSD >= 0 && left.EffectiveCostUSD != right.EffectiveCostUSD {
				return left.EffectiveCostUSD < right.EffectiveCostUSD
			}
			if left.Weight != right.Weight {
				return left.Weight > right.Weight
			}
			return left.OriginalIndex < right.OriginalIndex
		})
	}
	return sorted, candidates
}

// recordTargetByUID 统一 targetByUID 的写入条件：只要候选携带非空 MappedModel 就记录，
// MappedEffort / MappedEffortDecided 原样带过，避免不同调用路径出现"写入条件"漂移。
// 门槛用 MappedModel 而非 MappedEffortDecided，因为无映射模型的候选不应产生任何改写，
// 而"有模型映射但 effort 未决定"是合法的"只改模型"场景，也需要落入 targetByUID。
func recordTargetByUID(cand EndpointCandidate, targetByUID map[string]*ResolvedRouteTarget) {
	if cand.MappedModel == "" || cand.EndpointUID == "" {
		return
	}
	targetByUID[cand.EndpointUID] = &ResolvedRouteTarget{
		Model:         cand.MappedModel,
		Effort:        cand.MappedEffort,
		EffortDecided: cand.MappedEffortDecided,
		Reason:        "endpoint_policy",
	}
}

// ── endpoint 级评分函数 ──

// endpointScoreWeights 定义 endpoint 级评分的权重。
// 设计 §4.6.2a：健康 > FastDecay 衰减 > 成功率 > 延迟 > 成本。
var endpointScoreWeights = struct {
	Health    float64 // 健康状态权重（最高优先级）
	FastDecay float64 // FastDecay 衰减权重
	Success   float64 // 成功率权重
	Latency   float64 // 延迟权重
	Cost      float64 // 成本权重（tie-breaker）
}{
	Health:    40.0,
	FastDecay: 25.0,
	Success:   20.0,
	Latency:   10.0,
	Cost:      5.0,
}

const (
	// fastDecayFilterThreshold FastDecay 过滤阈值。
	// 低于此值的 key 在 active 模式下被过滤。
	fastDecayFilterThreshold = 0.15

	// opportunisticUtilityMultiplier 机会性 key 在效用分中的偏好倍数。
	// 1.5 表示机会性 key 获得 50% 的排序优势，但严重降级（health × decay < 0.67）时仍会输给健康 normal key。
	opportunisticUtilityMultiplier = 1.5

	// neutralEndpointScore 中性 endpoint 评分（无画像时的默认值）。
	neutralEndpointScore = 50.0
)

// scoreEndpoint 计算单个 endpoint 的综合评分。
// 按设计 §4.6.2a 的优先级链路：健康 > FastDecay > 成功率 > 延迟 > 成本。
// 无画像时返回中性分（不惩罚）。
func scoreEndpoint(profile *KeyEndpointProfile, fastDecayScore float64) float64 {
	if profile == nil {
		return neutralEndpointScore
	}

	// 1. 健康状态分（0-100，dead=0，healthy=100）
	healthScore := endpointHealthScore(profile.HealthState)

	// 2. FastDecay 分（0-1.0 → 0-100）
	decayScore := clampF(fastDecayScore, 0, 1) * 100

	// 3. 成功率分（0-1.0 → 0-100）
	successRate := clampF(profile.SuccessRate15m, 0, 1) * 100

	// 4. 延迟分（越低越好，以 5000ms 为基线归一化）
	latencyScore := latencyToScore(profile.P95LatencyMs)

	// 5. 成本分（costTier → 分数，越便宜越高）
	costScore := endpointCostScore(profile.CostTier)

	// 加权求和
	total := endpointScoreWeights.Health*healthScore +
		endpointScoreWeights.FastDecay*decayScore +
		endpointScoreWeights.Success*successRate +
		endpointScoreWeights.Latency*latencyScore +
		endpointScoreWeights.Cost*costScore

	// 归一化到 0-100
	maxPossible := endpointScoreWeights.Health*100 +
		endpointScoreWeights.FastDecay*100 +
		endpointScoreWeights.Success*100 +
		endpointScoreWeights.Latency*100 +
		endpointScoreWeights.Cost*100

	base := total / maxPossible * 100

	// 健康状态惩罚乘数：dead/limited 的 endpoint 即使其他维度全满分也应被严重降权
	multiplier := healthMultiplier(profile.HealthState)
	return base * multiplier
}

// healthMultiplier 根据 HealthState 返回惩罚乘数。
// dead/limited 的 endpoint 即使其他维度全满分，最终分也会被严重压制。
func healthMultiplier(hs HealthState) float64 {
	switch hs {
	case HealthStateDead:
		return 0.05 // dead 几乎归零
	case HealthStateLimited:
		return 0.30 // limited 严重降权
	case HealthStateMisconfigured:
		return 0.40
	case HealthStateDegraded:
		return 0.70 // degraded 轻度降权
	default:
		return 1.0 // healthy/unknown 不惩罚
	}
}

// endpointHealthScore 将 HealthState 映射为 0-100 分。
func endpointHealthScore(hs HealthState) float64 {
	switch hs {
	case HealthStateHealthy:
		return 100
	case HealthStateUnknown:
		return 70 // 新渠道给中性偏高分
	case HealthStateDegraded:
		return 40
	case HealthStateLimited:
		return 20
	case HealthStateMisconfigured:
		return 10
	case HealthStateDead:
		return 0
	default:
		return 70
	}
}

// latencyToScore 将 P95 延迟（ms）转换为 0-100 分。
// 以 5000ms 为基线，延迟越低分越高。
func latencyToScore(p95Ms int64) float64 {
	if p95Ms <= 0 {
		return 70 // 无延迟数据时给中性分
	}
	// 线性衰减：0ms=100, 5000ms=0
	score := 100.0 - float64(p95Ms)/50.0
	return clampF(score, 0, 100)
}

// endpointCostScore 将 CostTier 映射为 0-100 分（越便宜越高）。
func endpointCostScore(ct CostTier) float64 {
	switch ct {
	case CostTierFree:
		return 100
	case CostTierCheap:
		return 75
	case CostTierNormal:
		return 50
	case CostTierExpensive:
		return 25
	default:
		return 50
	}
}

// effortLevelToScore 将 EffortLevel 映射为 0-100 数值分，供 trace 维度展示。
// 按厂商投入/成本序递增：xhigh=88, max=95, ultra=100。
func effortLevelToScore(e EffortLevel) float64 {
	switch e {
	case EffortOff:
		return 0
	case EffortMinimal:
		return 15
	case EffortLow:
		return 30
	case EffortMedium:
		return 50
	case EffortHigh:
		return 75
	case EffortXhigh:
		return 88
	case EffortMax:
		return 95
	case EffortUltra:
		return 100
	default:
		return 50
	}
}

// ── 评分辅助函数 ──

// scoreEndpointForURL 为指定 baseURL 评分。
// 从 ProfileStore 查找画像，无画像时返回中性分。
func scoreEndpointForURL(store *ProfileStore, fastDecay *FastDecayScorer, model, baseURL string, req *RequestProfile, deps *EndpointPolicyDeps) EndpointCandidate {
	cand := EndpointCandidate{
		BaseURL: baseURL,
		Score:   neutralEndpointScore,
		Reason:  "no_profile",
	}

	if store == nil {
		return cand
	}

	profile := findProfileByBaseURL(store, baseURL)
	if profile == nil {
		return cand
	}

	cand.ChannelUID = profile.ChannelUID
	cand.ChannelKind = profile.ChannelKind
	cand.OriginTier = ChannelOriginTier(profile.OriginTier)
	cand.MetricsKey = profile.MetricsKey
	cand.KeyMask = profile.KeyMask
	cand.EndpointUID = profile.EndpointUID
	if target := resolveMappedModel(profile, model, req, deps); target != nil {
		cand.MappedModel = target.Model
		cand.MappedEffort = target.Effort
		cand.MappedEffortDecided = target.EffortDecided
	}

	// 计算 FastDecay 分
	fastDecayScore := 1.0
	if fastDecay != nil {
		fastDecayScore = fastDecay.Score(profile.EndpointUID)
	}

	cand.HealthScore = endpointHealthScore(profile.HealthState)
	cand.FastDecayScore = fastDecayScore * 100
	cand.SuccessRate = profile.SuccessRate15m
	cand.LatencyScore = latencyToScore(profile.P95LatencyMs)
	cand.CostScore = endpointCostScore(profile.CostTier)
	cand.Score = scoreEndpoint(profile, fastDecayScore)
	cand.Reason = "profile_scored"

	return cand
}

// scoreEndpointForKey 为指定 baseURL + apiKey 组合评分。
// 从 ProfileStore 查找画像，无画像时返回中性分。
func scoreEndpointForKey(store *ProfileStore, fastDecay *FastDecayScorer, model, baseURL, apiKey string, req *RequestProfile, deps *EndpointPolicyDeps) EndpointCandidate {
	keyHash := KeyHashFromAPIKey(apiKey)
	endpointUID := GenerateEndpointUID("", baseURL, keyHash)

	cand := EndpointCandidate{
		BaseURL:     baseURL,
		KeyMask:     maskKeyForDisplay(apiKey),
		EndpointUID: endpointUID,
		Score:       neutralEndpointScore,
		Reason:      "no_profile",
	}

	if store == nil {
		return cand
	}

	profile := store.Get(endpointUID)
	if profile == nil {
		// 尝试通过 baseURL 模糊匹配
		profile = findProfileByBaseURL(store, baseURL)
	}
	if profile == nil {
		return cand
	}

	cand.ChannelUID = profile.ChannelUID
	cand.ChannelKind = profile.ChannelKind
	cand.OriginTier = ChannelOriginTier(profile.OriginTier)
	cand.MetricsKey = profile.MetricsKey
	// 命中画像后改用 profile.EndpointUID（含真实 channelUID），
	// 与 handlers 层 upstream_failover.go 的 GenerateEndpointUID(upstream.ChannelUID, ...) 保持一致，
	// 否则 modelByUID 的 key 与 handlers 层查询的 key 永不相等，MappedModel 永远查不到（见 Phase 3B-2 复核发现）。
	if profile.EndpointUID != "" {
		cand.EndpointUID = profile.EndpointUID
	}
	if target := resolveMappedModel(profile, model, req, deps); target != nil {
		cand.MappedModel = target.Model
		cand.MappedEffort = target.Effort
		cand.MappedEffortDecided = target.EffortDecided
	}

	// 计算 FastDecay 分（沿用本函数原有的 baseURL+keyHash 派生 UID，不改变既有 FastDecay 查找行为）
	fastDecayScore := 1.0
	if fastDecay != nil {
		fastDecayScore = fastDecay.Score(endpointUID)
	}

	cand.HealthScore = endpointHealthScore(profile.HealthState)
	cand.FastDecayScore = fastDecayScore * 100
	cand.SuccessRate = profile.SuccessRate15m
	cand.LatencyScore = latencyToScore(profile.P95LatencyMs)
	cand.CostScore = endpointCostScore(profile.CostTier)
	cand.Score = scoreEndpoint(profile, fastDecayScore)
	cand.Reason = "profile_scored"

	return cand
}

func scoreEndpointForBinding(store *ProfileStore, fastDecay *FastDecayScorer, model, channelUID, baseURL, apiKey string, req *RequestProfile, deps *EndpointPolicyDeps) EndpointCandidate {
	endpointUID := GenerateEndpointUID(channelUID, baseURL, KeyHashFromAPIKey(apiKey))
	cand := EndpointCandidate{
		ChannelUID:  channelUID,
		BaseURL:     baseURL,
		KeyMask:     maskKeyForDisplay(apiKey),
		EndpointUID: endpointUID,
		Score:       neutralEndpointScore,
		Reason:      "no_profile",
	}
	if store == nil {
		return cand
	}
	profile := findProfileForBinding(store, channelUID, baseURL, apiKey)
	if profile == nil {
		return cand
	}
	return scoreEndpointProfile(cand, profile, fastDecay, model, req, deps)
}

func scoreEndpointProfile(cand EndpointCandidate, profile *KeyEndpointProfile, fastDecay *FastDecayScorer, model string, req *RequestProfile, deps *EndpointPolicyDeps) EndpointCandidate {
	cand.ChannelUID = profile.ChannelUID
	cand.ChannelKind = profile.ChannelKind
	cand.OriginTier = ChannelOriginTier(profile.OriginTier)
	cand.MetricsKey = profile.MetricsKey
	cand.KeyMask = profile.KeyMask
	cand.EndpointUID = profile.EndpointUID
	if target := resolveMappedModel(profile, model, req, deps); target != nil {
		cand.MappedModel = target.Model
		cand.MappedEffort = target.Effort
		cand.MappedEffortDecided = target.EffortDecided
	}
	fastDecayScore := 1.0
	if fastDecay != nil {
		fastDecayScore = fastDecay.Score(profile.EndpointUID)
	}
	cand.HealthScore = endpointHealthScore(profile.HealthState)
	cand.FastDecayScore = fastDecayScore * 100
	cand.SuccessRate = profile.SuccessRate15m
	cand.LatencyScore = latencyToScore(profile.P95LatencyMs)
	cand.CostScore = endpointCostScore(profile.CostTier)
	cand.Score = scoreEndpoint(profile, fastDecayScore)
	cand.Reason = "profile_scored"
	return cand
}

func findProfileForBinding(store *ProfileStore, channelUID, baseURL, apiKey string) *KeyEndpointProfile {
	if store == nil || channelUID == "" || baseURL == "" || apiKey == "" {
		return nil
	}
	profile := store.Get(GenerateEndpointUID(channelUID, baseURL, KeyHashFromAPIKey(apiKey)))
	if profile == nil {
		return nil
	}

	// L1 profiler 的旧版本曾把 keyHash 写入 MetricsKey。按当前 binding 重建
	// 运行时身份，使旧画像无需等待后台刷新即可命中同一 endpoint 的模型画像。
	metricsKey := computeMetricsIdentityKey(baseURL, apiKey, profile.ServiceType)
	if profile.MetricsKey == metricsKey {
		return profile
	}
	snapshot := *profile
	snapshot.KeyHash = KeyHashFromAPIKey(apiKey)
	snapshot.MetricsKey = metricsKey
	return &snapshot
}

func profileSupportsRequestModel(profile *KeyEndpointProfile, model string, req *RequestProfile, deps *EndpointPolicyDeps) bool {
	if profile == nil || model == "" || len(profile.AvailableModels) == 0 {
		return true
	}
	for _, available := range profile.AvailableModels {
		if strings.EqualFold(strings.TrimSpace(available), strings.TrimSpace(model)) {
			return true
		}
	}
	if target := resolveMappedModel(profile, model, req, deps); target != nil && target.Model != "" {
		return true
	}
	return false
}

// findProfileByBaseURL 从 ProfileStore 查找匹配 baseURL 的画像。
// 返回第一个匹配的画像副本；未找到返回 nil。
func findProfileByBaseURL(store *ProfileStore, baseURL string) *KeyEndpointProfile {
	if store == nil || baseURL == "" {
		return nil
	}
	all := store.ListActive()
	for _, p := range all {
		if p.BaseURL == baseURL {
			return p
		}
	}
	return nil
}

// resolveMappedModel 解析 endpoint 的模型映射（含 effort 原子决定）。
// 优先级：profile.ModelMapping（显式 per-endpoint 映射）> ModelResolver 自动映射。
// ModelResolver 仅在 AutoResolve 门控通过时调用；返回 nil 表示无映射。
// 显式 ModelMapping 返回 EffortDecided=false（不注入 effort）；
// ModelResolver 自动映射返回完整 target（含 effort 和 EffortDecided）。
func resolveMappedModel(profile *KeyEndpointProfile, model string, req *RequestProfile, deps *EndpointPolicyDeps) *ResolvedRouteTarget {
	if profile == nil || model == "" {
		return nil
	}

	routingCfg := getRoutingCfgFromDeps(deps)
	preferAutoOverManual := routingCfg != nil && routingCfg.ModelMapping.PreferAutoOverManual

	if preferAutoOverManual {
		// 反转顺序（退役期灰度）：ModelResolver 自动决策优先；
		// 自动决策 fail-open（未命中）时才回退到显式手动映射。
		if target := resolveAutoModel(profile, model, req, deps, routingCfg); target != nil {
			return target
		}
		return resolveManualMapping(profile, model, "manual_mapping_fallback")
	}

	// 默认顺序（历史行为不变）：显式 modelMapping 优先，ModelResolver 自动映射兜底。
	if target := resolveManualMapping(profile, model, "manual_mapping"); target != nil {
		return target
	}
	return resolveAutoModel(profile, model, req, deps, routingCfg)
}

// resolveManualMapping 解析 profile 上显式配置的 modelMapping（用户手动配置，视为已知正确；不覆盖 effort）。
// reason 由调用方指定，用于区分默认优先级路径（manual_mapping）与自动决策 fail-open 后的兜底路径（manual_mapping_fallback）。
func resolveManualMapping(profile *KeyEndpointProfile, model string, reason string) *ResolvedRouteTarget {
	if len(profile.ModelMapping) == 0 {
		return nil
	}
	mapped, ok := profile.ModelMapping[model]
	if !ok {
		return nil
	}
	return &ResolvedRouteTarget{Model: mapped, Effort: "", EffortDecided: false, Reason: reason}
}

// resolveAutoModel 解析 ModelResolver 自动映射（Phase 3B-2）。
// 三条件门控：AutoResolve + RoutingMode in {assist, auto} + no KillSwitch；不满足任一条件时返回 nil（fail-open）。
func resolveAutoModel(profile *KeyEndpointProfile, model string, req *RequestProfile, deps *EndpointPolicyDeps, routingCfg *config.AutopilotRoutingConfig) *ResolvedRouteTarget {
	resolver := getModelResolverFromDeps(deps)
	if resolver == nil || req == nil {
		return nil
	}

	if routingCfg == nil || !routingCfg.ModelMapping.AutoResolve {
		return nil
	}
	floor := BuildCapabilityFloorFromRequestProfile(req)
	target, resolved, _ := resolver.ResolveModel(
		model,
		profile.ChannelUID,
		profile.ChannelKind,
		profile.MetricsKey,
		floor,
	)
	if resolved {
		return &target
	}
	return nil
}

// maskKeyForDisplay 对 API Key 做掩码（用于 EndpointCandidate.KeyMask）。
func maskKeyForDisplay(key string) string {
	return MaskKey(key)
}

// getModelResolverFromDeps 安全获取 deps 中的 ModelResolver。
// deps 或 ModelResolver 为 nil 时返回 nil（调用方回退到原有逻辑）。
func getModelResolverFromDeps(deps *EndpointPolicyDeps) *ModelResolver {
	if deps == nil {
		return nil
	}
	return deps.ModelResolver
}

// getRoutingCfgFromDeps 安全获取路由配置。
// deps 或 GetRoutingCfg 为 nil 时返回 nil（调用方回退到原有逻辑）。
func getRoutingCfgFromDeps(deps *EndpointPolicyDeps) *config.AutopilotRoutingConfig {
	if deps == nil || deps.GetRoutingCfg == nil {
		return nil
	}
	cfg := deps.GetRoutingCfg()
	return &cfg
}

// ── 排序辅助 ──

// sortEndpointsByScore 按 EndpointCandidate.Score 降序排序。
// items 和 candidates 必须等长，items[i] 对应 candidates[i]。
// 同分时按成本分降序，信任等级高的作为 tie-breaker。
func sortEndpointsByScore(items []string, candidates []EndpointCandidate) {
	if len(items) != len(candidates) || len(items) <= 1 {
		return
	}
	sort.Stable(&endpointSorter{items: items, candidates: candidates})
}

// endpointSorter 实现 sort.Interface，按 Score 降序排序。
type endpointSorter struct {
	items      []string
	candidates []EndpointCandidate
}

func (s *endpointSorter) Len() int { return len(s.items) }

func (s *endpointSorter) Swap(i, j int) {
	s.items[i], s.items[j] = s.items[j], s.items[i]
	s.candidates[i], s.candidates[j] = s.candidates[j], s.candidates[i]
}

func (s *endpointSorter) Less(i, j int) bool {
	ci, cj := s.candidates[i], s.candidates[j]
	// 主排序：Score 降序
	if ci.Score != cj.Score {
		return ci.Score > cj.Score
	}
	// 次排序：成本分降序（便宜优先）
	if ci.CostScore != cj.CostScore {
		return ci.CostScore > cj.CostScore
	}
	// 三级排序：信任等级降序（first > second > {third,local} > unknown）
	return originTierRank(ci.OriginTier) > originTierRank(cj.OriginTier)
}

// originTierRank 返回 OriginTier 的信任等级排名（数值越大越可信）。
// 用于 tie-breaker：first=3, second=2, {third,local}=1, unknown=0。
// 设计原则：不把信任等级当质量，local 与 third 同级（本地可信但低速/低资源）。
func originTierRank(tier ChannelOriginTier) int {
	switch tier {
	case OriginTierFirst:
		return 3
	case OriginTierSecond:
		return 2
	case OriginTierThird, OriginTierLocal:
		return 1
	default:
		return 0
	}
}

// ── Trace 辅助 ──

// buildEndpointTrace 构建 endpoint 级别的 RoutingDecisionTrace。
func buildEndpointTrace(req *RequestProfile, candidates []EndpointCandidate, stage string, startTime time.Time) *RoutingDecisionTrace {
	if req == nil {
		return nil
	}

	now := time.Now()
	trace := &RoutingDecisionTrace{
		RequestKind:    req.ChannelKind,
		TaskClass:      req.TaskClass,
		TaskDomain:     req.TaskDomain,
		RequestedModel: req.Model,
		AgentRole:      req.AgentRole,
		Mode:           RoutingModeShadow, // endpoint trace 始终为 shadow
		SortReasons:    []string{"endpoint_policy_" + stage},
		DurationMs:     now.Sub(startTime).Milliseconds(),
		CreatedAt:      now,
	}

	// 将 EndpointCandidate 转换为 RoutingCandidate（用于 trace 展示）
	trace.Candidates = make([]RoutingCandidate, 0, len(candidates))
	for _, cand := range candidates {
		rc := RoutingCandidate{
			ChannelUID:  cand.ChannelUID,
			ChannelName: cand.ChannelName,
			MetricsKey:  SanitizeMetricsKey(cand.MetricsKey),
			KeyMask:     cand.KeyMask,
			ChannelKind: cand.ChannelKind,
			MappedModel: cand.MappedModel,
			TotalScore:  cand.Score,
			Selected:    true,
		}
		scores := []CandidateScore{
			{Dimension: "endpoint_health", Score: cand.HealthScore, Weight: endpointScoreWeights.Health},
			{Dimension: "endpoint_fast_decay", Score: cand.FastDecayScore, Weight: endpointScoreWeights.FastDecay},
			{Dimension: "endpoint_success_rate", Score: cand.SuccessRate * 100, Weight: endpointScoreWeights.Success},
			{Dimension: "endpoint_latency", Score: cand.LatencyScore, Weight: endpointScoreWeights.Latency},
			{Dimension: "endpoint_cost", Score: cand.CostScore, Weight: endpointScoreWeights.Cost},
		}
		if cand.MappedEffortDecided {
			scores = append(scores, CandidateScore{
				Dimension: "effort",
				Score:     effortLevelToScore(cand.MappedEffort),
				Weight:    0, // informational, not weighted in total
			})
		}
		rc.Scores = scores
		trace.Candidates = append(trace.Candidates, rc)
	}

	trace.CandidatesBefore = len(candidates)
	trace.CandidatesAfter = len(candidates)

	return trace
}

// ── 调试辅助 ──

// topN 返回切片的前 N 个元素（用于日志）。
func topN(items []string, n int) []string {
	if n <= 0 || len(items) == 0 {
		return nil
	}
	if n > len(items) {
		n = len(items)
	}
	result := make([]string, n)
	copy(result, items[:n])
	return result
}

// GetEndpointCandidates 获取指定 baseURL 列表的 endpoint 评分信息。
// 仅用于诊断/dry-run，不影响调度。

// buildResolvedModelLookup 从 endpointUID → mappedModel 映射构建闭包。
// 空 map 时返回总是返回空串的闭包（避免 nil 检查）。
func buildResolvedModelLookup(m map[string]string) func(string) string {
	return func(endpointUID string) string {
		return m[endpointUID]
	}
}

// buildResolvedTargetLookup 从 endpointUID → *ResolvedRouteTarget 映射构建闭包。
// 排序阶段填充的 map 仅作为缓存；缓存未命中时按最终选中的 endpoint 画像现场解析。
// 同一 BaseURL 下可能有多个 Key，URL 级评分只会观察其中一个画像，不能让模型改写
// 依赖排序副作用，否则最终选中另一 Key 时会把原始模型名直接发给上游。
func buildResolvedTargetLookup(m map[string]*ResolvedRouteTarget, deps EndpointPolicyDeps, req *RequestProfile) func(string) *ResolvedRouteTarget {
	return func(endpointUID string) *ResolvedRouteTarget {
		if target := m[endpointUID]; target != nil {
			return target
		}
		if deps.ProfileStore == nil || req == nil || endpointUID == "" {
			return nil
		}
		profile := deps.ProfileStore.Get(endpointUID)
		if profile == nil {
			return nil
		}
		target := resolveMappedModel(profile, req.Model, req, &deps)
		if target != nil && target.Model != "" {
			m[endpointUID] = target
			return target
		}
		return nil
	}
}

func buildResolvedTargetForBinding(m map[string]*ResolvedRouteTarget, deps EndpointPolicyDeps, req *RequestProfile) func(string, string, string) *ResolvedRouteTarget {
	return func(channelUID, baseURL, apiKey string) *ResolvedRouteTarget {
		if deps.ProfileStore == nil || req == nil {
			return nil
		}
		profile := findProfileForBinding(deps.ProfileStore, channelUID, baseURL, apiKey)
		if profile == nil {
			return nil
		}
		if target := m[profile.EndpointUID]; target != nil {
			return target
		}
		target := resolveMappedModel(profile, req.Model, req, &deps)
		if target != nil && target.Model != "" {
			m[profile.EndpointUID] = target
			return target
		}
		return nil
	}
}
func GetEndpointCandidates(store *ProfileStore, fastDecay *FastDecayScorer, model string, urls []string) []EndpointCandidate {
	candidates := make([]EndpointCandidate, 0, len(urls))
	for _, url := range urls {
		cand := scoreEndpointForURL(store, fastDecay, model, url, nil, nil)
		candidates = append(candidates, cand)
	}
	return candidates
}

// GetKeyCandidates 获取指定 baseURL 的 key 列表的 endpoint 评分信息。
// 仅用于诊断/dry-run，不影响调度。
func GetKeyCandidates(store *ProfileStore, fastDecay *FastDecayScorer, model, baseURL string, apiKeys []string) []EndpointCandidate {
	candidates := make([]EndpointCandidate, 0, len(apiKeys))
	for _, key := range apiKeys {
		cand := scoreEndpointForKey(store, fastDecay, model, baseURL, key, nil, nil)
		candidates = append(candidates, cand)
	}
	return candidates
}
