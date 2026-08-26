package autopilot

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
)

// ── CapabilityFloor 能力下界 ──

// CapabilityFloor 描述请求对候选模型的能力要求。
// 上下文、推理、视觉和工具调用是硬约束；质量档是优先目标，
// 仅在没有满足目标质量的模型时允许降档兜底。
type CapabilityFloor struct {
	MinContextTokens  int         // 最小上下文窗口（0=不限）
	NeedsReasoning    bool        // 必须支持推理
	NeedsVision       bool        // 必须支持视觉
	NeedsDocument     bool        // 必须支持文档（PDF 等）
	NeedsToolCalls    bool        // 必须支持工具调用
	MinQualityTier    QualityTier // 目标质量档（无同档候选时允许降档）
	QualityBenefitCap QualityTier // 简单/常规任务超过该档后不再自动获得质量排序收益
	TaskClass         TaskClass   // 用于读取任务级 CostPreference
	TaskDomain        TaskDomain  // 用于按 (domain, effort) 取任务域强度证据
	EffortFloor       EffortLevel // 该请求的 effort 下界；空=不限
	EffortCeil        EffortLevel // 该请求的 effort 上界（场景预设引入）；空=不限
	PinnedEffort      EffortLevel // 手动意图精确锁定的 effort 档位；空=不锁定，由 Autopilot 选优
	// CostPreferenceOverride 请求级生效价格偏好（请求头 X-Cost-Preference 或场景预设默认）；
	// 空 = 沿用配置链（PerTaskClass > 全局 Mode）。
	CostPreferenceOverride string
}

// BuildCapabilityFloorFromRequestProfile 从 RequestProfile 推导能力下界。
// 复用 RequestProfile 已有的 QualityNeed/ContextNeed/VisionNeed/ToolUseNeed/ReasoningNeed，
// 零额外计算。场景预设命中时同时注入 effort 区间（显式意图，不再被 PerTaskClass 下界覆盖）。
func BuildCapabilityFloorFromRequestProfile(profile *RequestProfile) CapabilityFloor {
	if profile == nil {
		return CapabilityFloor{}
	}
	floor := CapabilityFloor{
		MinContextTokens:  profile.ContextNeed,
		NeedsReasoning:    profile.ReasoningNeed,
		NeedsVision:       profile.VisionNeed,
		NeedsDocument:     profile.DocumentNeed,
		NeedsToolCalls:    profile.ToolUseNeed,
		MinQualityTier:    requestQualityTarget(profile),
		QualityBenefitCap: requestQualityBenefitCap(profile),
		TaskClass:         profile.TaskClass,
		TaskDomain:        profile.TaskDomain,
		PinnedEffort:      resolveIntentPinnedEffort(profile),
	}
	if profile.ScenarioPreset != nil {
		floor.EffortFloor = profile.ScenarioPreset.EffortFloor
		floor.EffortCeil = profile.ScenarioPreset.EffortCeil
	}
	floor.CostPreferenceOverride = effectiveCostPreferenceForProfile(profile)
	return floor
}

// effectiveCostPreferenceForProfile 解析请求级生效价格偏好：
// 请求头 X-Cost-Preference > 场景预设默认；空 = 沿用配置链（PerTaskClass > 全局 Mode）。
func effectiveCostPreferenceForProfile(profile *RequestProfile) string {
	if profile == nil {
		return ""
	}
	if profile.CostPreferenceOverride != "" {
		return profile.CostPreferenceOverride
	}
	if profile.ScenarioPreset != nil && profile.ScenarioPreset.CostPreference != "" {
		return profile.ScenarioPreset.CostPreference
	}
	return ""
}

// resolveIntentPinnedEffort 解析手动意图对 effort 的锁定值，遵循优先级约束：
// 客户端显式声明的 off/none 是最强信号，任何手动意图都不得覆盖它重新开启思考。
// 未被客户端显式关闭时，手动意图指定的 effort 才会被采纳为锁定档位。
func resolveIntentPinnedEffort(profile *RequestProfile) EffortLevel {
	if profile == nil || profile.IntentEffortPin == nil || !profile.IntentEffortPin.Set {
		return ""
	}
	if profile.ClientEffortExplicit && profile.ClientEffort == EffortOff {
		// 客户端显式关闭思考：手动意图的 effort 覆盖必须让路。
		return ""
	}
	return profile.IntentEffortPin.Effort
}

func requestQualityTarget(profile *RequestProfile) QualityTier {
	if profile == nil {
		return ""
	}
	if profile.QualityTarget != "" {
		return profile.QualityTarget
	}
	return ResolveQualityTarget(profile)
}

// requestQualityBenefitCap 只对难度已知且不复杂的任务设置软收益上限。
// 未知或复杂任务继续按模型质量优先，保持保守路由；该上限从不淘汰候选。
// 场景预设命中时以预设帽为准（显式意图优先于复杂度推导；无帽场景不设限）。
func requestQualityBenefitCap(profile *RequestProfile) QualityTier {
	if profile == nil {
		return ""
	}
	if profile.ScenarioPreset != nil {
		if profile.ScenarioPreset.HasBenefitCap {
			return profile.ScenarioPreset.QualityBenefitCap
		}
		return ""
	}
	if profile.Complexity == TaskComplexityTrivial ||
		profile.Complexity == TaskComplexityRoutine ||
		profile.TaskClass == TaskClassLightweight {
		return requestQualityTarget(profile)
	}
	return ""
}

// ── ModelResolver 模型自动映射器 ──

// ModelResolver 实现设计 doc §5.4 的模型自动映射逻辑。
// 当请求模型在渠道 supportedModels 中不存在时，从 ModelProfileStore 中
// 找到满足 CapabilityFloor 的最佳匹配模型。
//
// 仅对 AutoManaged==true 的渠道生效；手动配置渠道通过 config.RedirectModel
// 直接短路，不经过自动映射。
type ModelResolver struct {
	profileStore *ModelProfileStore
	cfgManager   *config.ConfigManager
}

// NewModelResolver 创建 ModelResolver。
// profileStore 为 nil 时所有自动映射退化为 no-op（fail-open）。
func NewModelResolver(profileStore *ModelProfileStore, cfgManager *config.ConfigManager) *ModelResolver {
	return &ModelResolver{
		profileStore: profileStore,
		cfgManager:   cfgManager,
	}
}

// ResolveModel 将请求模型映射到渠道实际支持的最佳模型。
//
// 返回:
//   - mappedModel: 映射后的模型名（可能与 requestModel 相同）
//   - resolved: true 表示成功映射，false 表示该渠道无满足下界的模型
//   - reason: 决策原因（用于 trace / 日志）
//
// 安全不变量:
//   - 显式 modelMapping（用户手动配置）始终优先，不经过能力下界检查
//   - 禁止链式映射：candidate 源始终是原始 GetModelProfiles 结果
//   - 仅 autoManaged 渠道走自动映射；手动渠道由 config.RedirectModel 短路
//   - 只有 ModelRoutingPolicy 白名单入口允许跨模型替代；其余请求必须精确命中模型 ID
func (r *ModelResolver) ResolveModel(
	requestModel string,
	channelUID string,
	channelKind string,
	metricsKey string,
	floor CapabilityFloor,
) (target ResolvedRouteTarget, resolved bool, reason string) {

	// Step 1: 显式 modelMapping（精确 → 模糊）始终优先。
	// 手动配置视为已知正确，不经过能力下界检查（设计 doc 安全边界）。
	if r.cfgManager != nil {
		upstream := r.findUpstream(channelUID, channelKind)
		if upstream != nil && !upstream.AutoManaged {
			redirected, matched := config.RedirectModelWithMatch(requestModel, upstream)
			if matched && redirected != requestModel {
				return ResolvedRouteTarget{Model: redirected, Reason: "manual_redirect"}, true, "manual_redirect"
			}
		}
	}

	// Step 2: 无 ModelProfileStore 时自动映射不可用，fail-open。
	if r.profileStore == nil {
		return ResolvedRouteTarget{Model: requestModel, Reason: "model_profile_store_unavailable"}, false, "model_profile_store_unavailable"
	}

	// Step 3: 查询候选模型画像。
	candidates := r.profileStore.GetModelProfiles(channelUID, channelKind, metricsKey)
	if len(candidates) == 0 {
		return ResolvedRouteTarget{Model: requestModel, Reason: "no_model_profiles"}, false, "no_model_profiles"
	}
	candidates = r.refreshAutoDiscoveryCapabilities(candidates, channelUID, channelKind)

	// Step 4: 能力过滤——上下文、推理、视觉、工具调用仍是硬约束；
	// 质量档作为首选条件，只有更高质量候选完全不存在时才允许降档，
	// 避免“没有 Opus 等价模型就整条请求不可用”。
	qualityFallback := false
	// CapabilityFloorEnabled=false 时跳过硬过滤（紧急逃生口，所有候选均可参与排序）。
	if r.cfgManager != nil {
		routingCfg := r.cfgManager.GetAutopilotRouting()
		if !routingCfg.ModelMapping.CapabilityFloorEnabled {
			// 仅过滤掉未验证的模型，不做能力下界检查。
			probeEligible := filterProbedModelProfiles(candidates)
			if len(probeEligible) == 0 {
				return ResolvedRouteTarget{Model: requestModel, Reason: "no_probed_model"}, false, "no_probed_model"
			}
			candidates = probeEligible
		} else {
			candidates, qualityFallback = filterByCapabilityFloorWithQualityFallback(candidates, floor)
		}
	} else {
		candidates, qualityFallback = filterByCapabilityFloorWithQualityFallback(candidates, floor)
	}
	if len(candidates) == 0 {
		return ResolvedRouteTarget{Model: requestModel, Reason: "no_capable_model"}, false, "no_capable_model"
	}

	// Step 5: 精确模型始终优先；非自适应入口不得跨模型替代。
	// 精确/等价命中不经过 rankEligibleModels，因此需在此单独应用 effort 决策，
	// 否则手动意图锁定的档位与自动展开都会被这条短路路径绕过。
	if exact, found := findExactModelProfile(candidates, requestModel); found {
		rsn := modelResolutionReason("found_exact_model_in_profile", qualityFallback)
		effort, decided := r.resolveSingleProfileEffort(exact, floor)
		return ResolvedRouteTarget{Model: exact.ModelID, Effort: effort, EffortDecided: decided, Reason: rsn}, true, rsn
	}
	if equivalent, found := findEquivalentModelProfile(candidates, requestModel); found {
		rsn := modelResolutionReason("found_equivalent_model_in_profile", qualityFallback)
		effort, decided := r.resolveSingleProfileEffort(equivalent, floor)
		return ResolvedRouteTarget{Model: equivalent.ModelID, Effort: effort, EffortDecided: decided, Reason: rsn}, true, rsn
	}
	intent := ClassifyModelRoutingIntent(channelKind, requestModel)
	if !intent.AllowsSubstitution() {
		return ResolvedRouteTarget{Model: requestModel, Reason: "exact_model_required"}, false, "exact_model_required"
	}

	// Step 6: 自适应入口在满足下界的候选中按模型质量、实测表现和成本选优。
	best := r.rankEligibleModels(candidates, requestModel, channelUID, channelKind, floor)
	baseReason := fmt.Sprintf("mapped %s->%s (intent:%s, %s)",
		requestModel, best.profile.ModelID, intent, best.reasonSummary())
	finalReason := modelResolutionReason(baseReason, qualityFallback)
	return ResolvedRouteTarget{
		Model:         best.profile.ModelID,
		Effort:        best.effort,
		EffortDecided: best.effortDecided,
		Reason:        finalReason,
	}, true, finalReason
}

// ResolveModelAnyEndpoint 在渠道的所有 endpoint 中判断 requestModel 是否可由自动映射支持。
// 不限定 metricsKey，适用于调度器候选筛选阶段（此时无具体 API Key）。
// 精确命中已发现模型时直接返回该模型；未命中时从该渠道所有已探测成功模型中选一个
// request-scoped 候选，避免 autoManaged 渠道在进入 EndpointAttemptPolicy 前被 active_model_filter 误剔除。
// 真正发送请求前仍会用带 metricsKey 和完整 CapabilityFloor 的 ResolveModel 再做一次 endpoint 级决策。
func (r *ModelResolver) ResolveModelAnyEndpoint(
	requestModel string,
	channelUID string,
	channelKind string,
) (target ResolvedRouteTarget, found bool, reason string) {
	return r.resolveModelAnyEndpoint(requestModel, channelUID, channelKind, CapabilityFloor{})
}

// ResolveModelAnyEndpointWithFloor 在渠道所有 endpoint 中查找满足完整能力下界的映射。
// 该方法只读且不修改配置，可供 dry-run 诊断和 scheduler 首次候选过滤复用。
func (r *ModelResolver) ResolveModelAnyEndpointWithFloor(
	requestModel string,
	channelUID string,
	channelKind string,
	floor CapabilityFloor,
) (target ResolvedRouteTarget, found bool, reason string) {
	return r.resolveModelAnyEndpoint(requestModel, channelUID, channelKind, floor)
}

// rankedModelByQuality 按 betterRankedModel 比较器对候选降序排序（模型粒度去重，
// 同模型多 effort 变体保留最优者）。用于按质量从高到低展开 (渠道, 模型) 候选行。
func (r *ModelResolver) rankedModelByQuality(
	eligible []ModelProfile,
	requestModel string,
	channelUID string,
	channelKind string,
	floor CapabilityFloor,
) []rankedModelCandidate {
	if len(eligible) == 0 {
		return nil
	}
	ranked := r.buildRankedCandidates(eligible, requestModel, channelUID, channelKind, floor)
	preferenceMode := r.modelCostPreferenceMode(floor)
	seen := make(map[string]bool, len(ranked))
	result := make([]rankedModelCandidate, 0, len(ranked))
	for _, candidate := range ranked {
		normalized := normalizeRoutingModelID(candidate.profile.ModelID)
		if normalized == "" || seen[normalized] {
			continue
		}
		seen[normalized] = true
		inserted := false
		for i := range result {
			if betterRankedModel(candidate, result[i], preferenceMode) {
				result = append(result, rankedModelCandidate{})
				copy(result[i+1:], result[i:])
				result[i] = candidate
				inserted = true
				break
			}
		}
		if !inserted {
			result = append(result, candidate)
		}
	}
	return result
}

// ResolveModelsAnyEndpointWithFloor 返回渠道内全部满足能力下界且按质量排序的模型。
// 供路由决策详情按 (渠道, 模型) 展开候选行使用；与单数版不同，这里枚举不施加
// MinQualityTier 目标质量过滤（低质量模型保留为低分行），质量排序交给评分环节。
// 能力硬约束（上下文/推理/视觉/工具调用）仍在此过滤，与路由硬约束互补。
func (r *ModelResolver) ResolveModelsAnyEndpointWithFloor(
	requestModel string,
	channelUID string,
	channelKind string,
	floor CapabilityFloor,
	maxFanout int,
) []rankedModelCandidate {
	eligible, _ := r.capabilityFilteredModelsAnyEndpoint(channelUID, channelKind, floor)
	if len(eligible) == 0 {
		return nil
	}
	ranked := r.rankedModelByQuality(eligible, requestModel, channelUID, channelKind, floor)
	if maxFanout > 0 && len(ranked) > maxFanout {
		ranked = ranked[:maxFanout]
	}
	return ranked
}

func (r *ModelResolver) resolveModelAnyEndpoint(
	requestModel string,
	channelUID string,
	channelKind string,
	floor CapabilityFloor,
) (target ResolvedRouteTarget, found bool, reason string) {
	candidates, qualityFallback, reason := r.eligibleModelsAnyEndpoint(channelUID, channelKind, floor)
	if len(candidates) == 0 {
		return ResolvedRouteTarget{Model: requestModel, Reason: reason}, false, reason
	}
	if exact, found := findExactModelProfile(candidates, requestModel); found {
		rsn := modelResolutionReason("found_exact_model_in_profile", qualityFallback)
		return ResolvedRouteTarget{Model: exact.ModelID, Reason: rsn}, true, rsn
	}
	if equivalent, found := findEquivalentModelProfile(candidates, requestModel); found {
		rsn := modelResolutionReason("found_equivalent_model_in_profile", qualityFallback)
		return ResolvedRouteTarget{Model: equivalent.ModelID, Reason: rsn}, true, rsn
	}
	intent := ClassifyModelRoutingIntent(channelKind, requestModel)
	if !intent.AllowsSubstitution() {
		return ResolvedRouteTarget{Model: requestModel, Reason: "exact_model_required"}, false, "exact_model_required"
	}

	best := r.rankEligibleModels(candidates, requestModel, channelUID, channelKind, floor)
	baseReason := fmt.Sprintf("mapped_any_endpoint %s->%s (intent:%s, %s)",
		requestModel, best.profile.ModelID, intent, best.reasonSummary())
	finalReason := modelResolutionReason(baseReason, qualityFallback)
	return ResolvedRouteTarget{
		Model:         best.profile.ModelID,
		Effort:        best.effort,
		EffortDecided: best.effortDecided,
		Reason:        finalReason,
	}, true, finalReason
}

// eligibleModelsAnyEndpoint 构建满足能力下界与目标质量档的候选模型列表（含 qualityFallback 标记与空集原因）。
// 空集 reason 为 "model_profile_store_unavailable" / "no_probed_model_profiles" / "no_capable_model"。
func (r *ModelResolver) eligibleModelsAnyEndpoint(
	channelUID string,
	channelKind string,
	floor CapabilityFloor,
) (candidates []ModelProfile, qualityFallback bool, reason string) {
	candidates, reason = r.probedModelsAnyEndpoint(channelUID, channelKind)
	if len(candidates) == 0 {
		return nil, false, reason
	}

	if r.cfgManager != nil {
		routingCfg := r.cfgManager.GetAutopilotRouting()
		if routingCfg.ModelMapping.CapabilityFloorEnabled {
			candidates, qualityFallback = filterByCapabilityFloorWithQualityFallback(candidates, floor)
		}
	} else {
		candidates, qualityFallback = filterByCapabilityFloorWithQualityFallback(candidates, floor)
	}
	if len(candidates) == 0 {
		return nil, qualityFallback, "no_capable_model"
	}
	return candidates, qualityFallback, ""
}

// capabilityFilteredModelsAnyEndpoint 仅按能力硬约束过滤（上下文/推理/视觉/工具调用），
// 不施加目标质量档过滤。供 (渠道, 模型) 展开枚举使用：保留低质量模型供评分拉开差距。
// 返回值同 eligibleModelsAnyEndpoint 的能力过滤结果（无 qualityFallback 概念）。
func (r *ModelResolver) capabilityFilteredModelsAnyEndpoint(
	channelUID string,
	channelKind string,
	floor CapabilityFloor,
) (candidates []ModelProfile, reason string) {
	candidates, reason = r.probedModelsAnyEndpoint(channelUID, channelKind)
	if len(candidates) == 0 {
		return nil, reason
	}
	// 仅按真实能力硬约束过滤，跳过质量档约束：低质量模型保留为低分行，由评分拉开差距。
	candidates = filterByCapabilityFloorWithoutQuality(candidates, floor)
	if len(candidates) == 0 {
		return nil, "no_capable_model"
	}
	return candidates, ""
}

// probedModelsAnyEndpoint 收集渠道内已探测成功且协议匹配的模型画像（含自动发现能力刷新）。
// 空集 reason 为 "model_profile_store_unavailable" / "no_probed_model_profiles"。
func (r *ModelResolver) probedModelsAnyEndpoint(channelUID, channelKind string) ([]ModelProfile, string) {
	if r.profileStore == nil {
		return nil, "model_profile_store_unavailable"
	}
	all := r.profileStore.ListActiveByChannel(channelUID)
	candidates := make([]ModelProfile, 0, len(all))
	for _, p := range all {
		if p.ChannelKind != channelKind {
			continue
		}
		if !p.ProbeSuccess {
			continue
		}
		candidates = append(candidates, p)
	}
	if len(candidates) == 0 {
		return nil, "no_probed_model_profiles"
	}
	candidates = r.refreshAutoDiscoveryCapabilities(candidates, channelUID, channelKind)
	return candidates, ""
}

// ── 过滤与排序 ──

// resolveEffortVariants 决定给定画像可用的 effort 变体列表。
// 每个返回的 EffortLevel 会生成一个独立的 rankedModelCandidate。
// resolveSingleProfileEffort 为单个已确定的画像解析 effort 档位。
// 供精确/等价模型命中路径使用：这些路径不进入候选排序，但仍需与排序路径
// 保持一致的 effort 决策语义（手动意图锁定优先，其次按配置展开取最低档）。
func (r *ModelResolver) resolveSingleProfileEffort(profile ModelProfile, floor CapabilityFloor) (EffortLevel, bool) {
	levels, decidedFlags := r.resolveEffortVariants(profile, floor)
	if len(levels) == 0 {
		return "", false
	}
	// 展开可能返回多个档位；精确命中场景没有排序阶段，按“够用即止”取最低档。
	return levels[0], decidedFlags[0]
}

func (r *ModelResolver) resolveEffortVariants(profile ModelProfile, floor CapabilityFloor) ([]EffortLevel, []bool) {
	// 手动意图锁定的 effort 优先于自动决策管线，视为已知正确（类比显式 modelMapping 的处理方式）：
	// 只要模型确实支持该档位就直接采纳，不受下方 ReasoningEffort.Enabled 全局开关影响。
	// 模型不支持该档位时 fail-open，落回下方常规展开逻辑，由 Autopilot 自行决定。
	if pinned := NormalizeEffortLevel(string(floor.PinnedEffort)); pinned != "" && profile.SupportsEffortControl {
		for _, lv := range profile.SupportedEffortLevels {
			if NormalizeEffortLevel(string(lv)) == pinned {
				return []EffortLevel{pinned}, []bool{true}
			}
		}
	}

	// 读取全局 reasoning effort 配置；cfgManager 为 nil 时 fail-open。
	var cfg *config.ReasoningEffortConfig
	if r != nil && r.cfgManager != nil {
		rc := r.cfgManager.GetAutopilotRouting().ReasoningEffort
		cfg = &rc
	}

	// 配置未启用：不展开 effort 变体，返回单个空 level（passthrough）。
	if cfg == nil || !cfg.Enabled {
		return []EffortLevel{""}, []bool{false}
	}

	// 模型不支持 effort 控制或没有声明可用档位。
	if !profile.SupportsEffortControl || len(profile.SupportedEffortLevels) == 0 {
		return []EffortLevel{""}, []bool{false}
	}

	// 计算 profile 支持的 effort 与 PerTaskClass 配置的交集。
	taskClassKey := string(floor.TaskClass)
	var configuredLevels []EffortLevel
	if cfg.PerTaskClass != nil {
		if raw, ok := cfg.PerTaskClass[taskClassKey]; ok {
			for _, s := range raw {
				if norm := NormalizeEffortLevel(s); norm != "" {
					configuredLevels = append(configuredLevels, norm)
				}
			}
		}
	}
	// 如果没有为该 TaskClass 配置特定档位，也没有全局交集，使用模型支持的全部档位。
	var intersection []EffortLevel
	if len(configuredLevels) > 0 {
		supportedSet := make(map[EffortLevel]bool, len(profile.SupportedEffortLevels))
		for _, lv := range profile.SupportedEffortLevels {
			if norm := NormalizeEffortLevel(string(lv)); norm != "" {
				supportedSet[norm] = true
			}
		}
		for _, lv := range configuredLevels {
			if supportedSet[lv] {
				intersection = append(intersection, lv)
			}
		}
	} else {
		// 没有 PerTaskClass 约束时，使用模型支持的全部档位。
		for _, lv := range profile.SupportedEffortLevels {
			if norm := NormalizeEffortLevel(string(lv)); norm != "" {
				intersection = append(intersection, norm)
			}
		}
	}

	// 交集为空时，回退到 SupportedEffortLevels 中 >= EffortFloor 的最低档。
	if len(intersection) == 0 {
		floorOrdinal := 0 // EffortFloor 为空时不设下界
		if floor.EffortFloor != "" {
			if ord, ok := effortOrdinal[floor.EffortFloor]; ok {
				floorOrdinal = ord
			}
		}
		levels := AllEffortLevels()
		for _, lv := range levels {
			ord, ok := effortOrdinal[lv]
			if !ok {
				continue
			}
			for _, supported := range profile.SupportedEffortLevels {
				if NormalizeEffortLevel(string(supported)) == lv && ord >= floorOrdinal {
					return []EffortLevel{lv}, []bool{true}
				}
			}
		}
		// 完全无法匹配时 fail-open。
		return []EffortLevel{""}, []bool{false}
	}

	// 按 ordinal 排序 intersection 以便后续选最低。
	sortEffortLevels(intersection)

	// ExpandVariants=false 时仅返回最低档。
	if !cfg.ExpandVariants {
		return []EffortLevel{intersection[0]}, []bool{true}
	}
	decided := make([]bool, len(intersection))
	for i := range decided {
		decided[i] = true
	}
	return intersection, decided
}

// sortEffortLevels 按 effortOrdinal 升序排列。
func sortEffortLevels(levels []EffortLevel) {
	for i := 1; i < len(levels); i++ {
		for j := i; j > 0; j-- {
			prevOrd, _ := effortOrdinal[levels[j-1]]
			currOrd, _ := effortOrdinal[levels[j]]
			if prevOrd > currOrd {
				levels[j-1], levels[j] = levels[j], levels[j-1]
			}
		}
	}
}

// filterEffortFloor 过滤掉 effort 低于 EffortFloor 的已决定候选。
// 仅在 EffortFloor 非空且至少一个候选存活时生效；全部被过滤时 fail-open。
// resolveEffortFloor 按任务类从 ReasoningEffortConfig.PerTaskClass 推导 effort 下界。
// 取该任务类允许档位中的最低档作为下界，使 supervisor 类请求不会被选到 off 档。
// 配置缺失、未启用或任务类无配置时返回空串（不设下界）。
func (r *ModelResolver) resolveEffortFloor(taskClass TaskClass) EffortLevel {
	if r == nil || r.cfgManager == nil || taskClass == "" {
		return ""
	}
	cfg := r.cfgManager.GetAutopilotRouting().ReasoningEffort
	if !cfg.Enabled || len(cfg.PerTaskClass) == 0 {
		return ""
	}
	allowed, ok := cfg.PerTaskClass[string(taskClass)]
	if !ok || len(allowed) == 0 {
		return ""
	}
	lowest := EffortLevel("")
	lowestOrdinal := -1
	for _, raw := range allowed {
		level := NormalizeEffortLevel(raw)
		if level == "" {
			continue
		}
		ord, exists := effortOrdinal[level]
		if !exists {
			continue
		}
		if lowestOrdinal < 0 || ord < lowestOrdinal {
			lowest = level
			lowestOrdinal = ord
		}
	}
	return lowest
}

func filterEffortFloor(candidates []rankedModelCandidate, floor CapabilityFloor) []rankedModelCandidate {
	if floor.EffortFloor == "" && floor.EffortCeil == "" {
		return candidates
	}
	var floorOrdinal, ceilOrdinal int
	hasFloor := false
	if floor.EffortFloor != "" {
		if ord, ok := effortOrdinal[floor.EffortFloor]; ok {
			floorOrdinal = ord
			hasFloor = true
		}
	}
	hasCeil := false
	if floor.EffortCeil != "" {
		if ord, ok := effortOrdinal[floor.EffortCeil]; ok {
			ceilOrdinal = ord
			hasCeil = true
		}
	}
	if !hasFloor && !hasCeil {
		return candidates
	}
	var filtered []rankedModelCandidate
	for _, c := range candidates {
		if !c.effortDecided {
			// 未决定 effort 的候选不受 effort 区间约束。
			filtered = append(filtered, c)
			continue
		}
		candOrdinal, cok := effortOrdinal[c.effort]
		if !cok || (!hasFloor || candOrdinal >= floorOrdinal) && (!hasCeil || candOrdinal <= ceilOrdinal) {
			filtered = append(filtered, c)
		}
	}
	// fail-open：如果全部被过滤则保留原始列表。
	if len(filtered) == 0 {
		return candidates
	}
	return filtered
}

// 与 capability_floor.go 的 CapabilityFloorReasons 逻辑一致，
// 但作用于 ModelProfile（而非 CandidateCapabilities），并额外检查 QualityTier。
func filterByCapabilityFloor(profiles []ModelProfile, floor CapabilityFloor) []ModelProfile {
	return filterByCapabilityFloorInternal(profiles, floor, true)
}

// filterByCapabilityFloorWithoutQuality 保留所有真实能力约束，仅跳过质量档约束。
// 用于“高档候选不存在时”的用户体验兜底；不会放行上下文或工具能力不足的模型。
func filterByCapabilityFloorWithoutQuality(profiles []ModelProfile, floor CapabilityFloor) []ModelProfile {
	return filterByCapabilityFloorInternal(profiles, floor, false)
}

// filterByCapabilityFloorWithQualityFallback 先按完整能力目标筛选；若仅质量档
// 导致无候选，则保留所有真实能力硬约束并允许质量降档。
func filterByCapabilityFloorWithQualityFallback(profiles []ModelProfile, floor CapabilityFloor) ([]ModelProfile, bool) {
	eligible := filterByCapabilityFloor(profiles, floor)
	if len(eligible) > 0 || floor.MinQualityTier == "" {
		return eligible, false
	}
	fallback := filterByCapabilityFloorWithoutQuality(profiles, floor)
	return fallback, len(fallback) > 0
}

func filterProbedModelProfiles(profiles []ModelProfile) []ModelProfile {
	eligible := make([]ModelProfile, 0, len(profiles))
	for _, profile := range profiles {
		if profile.ProbeSuccess {
			eligible = append(eligible, profile)
		}
	}
	return eligible
}

func filterByCapabilityFloorInternal(profiles []ModelProfile, floor CapabilityFloor, enforceQuality bool) []ModelProfile {
	var eligible []ModelProfile
	for _, p := range profiles {
		// 未验证通过的模型不参与自动映射
		if !p.ProbeSuccess {
			continue
		}
		if p.ContextTokens < floor.MinContextTokens {
			continue
		}
		if floor.NeedsReasoning && !p.SupportsReasoning {
			continue
		}
		if floor.NeedsVision && !p.SupportsVision {
			continue
		}
		if floor.NeedsDocument && !p.SupportsDocument {
			continue
		}
		if floor.NeedsToolCalls && !p.SupportsToolCalls {
			continue
		}
		if enforceQuality && qualityTierRank(p.QualityTier) < qualityTierRank(floor.MinQualityTier) &&
			!effortLevelQualityAdmission(p.ModelID, floor.MinQualityTier) {
			continue
		}
		eligible = append(eligible, p)
	}
	return eligible
}

// effortLevelQualityAdmission 判断模型是否凭任一已测 effort 档的实测分通过质量下限。
// 背景：QualityTier 是模型级常规口径分（medium 对齐），压平了 effort 维度；部分模型
// effort 档间差异极大（低常规档模型拉满 effort 后实测可达高档能力），仅按模型级档位
// 过滤会把这类 (model, effort) 组合整体排除。口径：取 coding 域 deepswe/codexradar
// pass@1 直测证据（与 regularEffortBaselineScore/档位边界同源同量纲，default 档按
// medium 归一），任一档实测分（百分制）达到下限对应边界分即放行。放行后该模型在
// 排序层仍按各 effort 档实际分数竞争，不会被虚抬。
func effortLevelQualityAdmission(modelID string, min QualityTier) bool {
	if min == "" || min == QualityTierLow {
		return true
	}
	benchmark := config.ResolveModelBenchmarkProfile(modelID)
	if !benchmark.Known {
		return false
	}
	premiumMin, highMin, normalMin := computeQualityTierBoundaries()
	var cutoff float64
	switch min {
	case QualityTierPremium:
		cutoff = premiumMin
	case QualityTierHigh:
		cutoff = highMin
	default:
		cutoff = normalMin
	}
	if cutoff <= 0 {
		return false
	}
	for _, ev := range benchmark.Profile.BenchmarkEvidence {
		if ev.Domain != "coding" || ev.Metric != "pass_at_1" {
			continue
		}
		if ev.Benchmark != "deepswe" && ev.Benchmark != "codexradar" {
			continue
		}
		effort := NormalizeEffortLevel(ev.Effort)
		if effort == "" || effort == EffortOff {
			continue
		}
		// 只认非常规档（序数 > medium）的实测：default/medium 是常规口径本身，
		// 已由模型级档位表达；豁免它会让 singleEffortOnly 封顶等保守判定失效。
		if EffortLevelOrdinal(effort) <= EffortLevelOrdinal(EffortMedium) {
			continue
		}
		if score := ev.RawValue * 100; score >= cutoff {
			return true
		}
	}
	return false
}

// modelResolutionReason 标记发生了质量降档，但不改变现有调用方的映射结果。
func modelResolutionReason(reason string, qualityFallback bool) string {
	if !qualityFallback {
		return reason
	}
	return "quality_fallback: " + reason
}

// rankedModelCandidate 保存模型选优所需的软证据。
// 上下文窗口不在这里评分；它只在 CapabilityFloor 阶段作为硬下限使用。
type rankedModelCandidate struct {
	profile                        ModelProfile
	effort                         EffortLevel // 该候选代表的 effort 档位；空=该模型未展开 effort 变体
	effortDecided                  bool        // true 表示 effort 由 autopilot 决定（非 passthrough）
	qualityRank                    int
	qualityBenefitCap              QualityTier
	providerModelQualityKnown      bool
	providerModelQualityComparable bool
	providerModelQualityPriority   int
	providerModelQualitySource     string
	measuredQualityScore           float64
	latencyKnown                   bool
	latencyMs                      int64
	providerCostKnown              bool
	providerCostMultiplier         float64
	providerCostSource             string
	publicCostKnown                bool
	normalizedPublicCostUSD        float64
	benchmarkKnown                 bool
	benchmarkScore                 float64
	benchmarkModel                 string
	benchmarkLane                  string // benchmark 证据泳道（provisional/verified），frontier 置信区间加宽用
	measuredCostUSD                float64
	versionLineage                 string
	versionNumbers                 []int
	sameFamily                     bool
	normalizedCandidateID          string
	frontierNote                   string // frontier 选型命中或回退的可解释标记，非空时追加到 reasonSummary
}

func (candidate rankedModelCandidate) reasonSummary() string {
	quality := string(candidate.profile.QualityTier)
	if candidate.qualityBenefitCap != "" {
		quality = fmt.Sprintf("%s(benefit_cap:%s)", candidate.profile.QualityTier, candidate.qualityBenefitCap)
	}
	providerQuality := "unknown"
	if candidate.providerModelQualityKnown {
		status := candidate.providerModelQualitySource
		if !candidate.providerModelQualityComparable {
			status += ",inactive_incomplete_tier"
		}
		providerQuality = fmt.Sprintf("%d(%s)", candidate.providerModelQualityPriority, status)
	}
	measuredQuality := "unknown"
	if candidate.profile.ProviderQualityConfidence >= 0.5 {
		measuredQuality = fmt.Sprintf("%.3f", candidate.measuredQualityScore)
	}
	latency := "unknown"
	if candidate.latencyKnown {
		latency = fmt.Sprintf("%dms", candidate.latencyMs)
	}
	providerCost := "unknown"
	if candidate.providerCostKnown {
		providerCost = fmt.Sprintf("%.6f(%s)", candidate.providerCostMultiplier, candidate.providerCostSource)
	}
	publicCost := "unknown"
	if candidate.publicCostKnown {
		publicCost = fmt.Sprintf("%.6f", candidate.normalizedPublicCostUSD)
	}
	benchmark := "unknown"
	if candidate.benchmarkKnown {
		benchmark = fmt.Sprintf("%.2f(%s)", candidate.benchmarkScore, candidate.benchmarkModel)
	}
	version := "unknown"
	if candidate.versionLineage != "" {
		version = fmt.Sprintf("%s:%v", candidate.versionLineage, candidate.versionNumbers)
	}
	summary := fmt.Sprintf("family:%s, quality:%s, provider_quality_priority:%s, measured_quality:%s, benchmark:%s, version:%s, latency:%s, provider_cost_multiplier:%s, normalized_public_cost_usd:%s",
		candidate.profile.ModelFamily, quality, providerQuality, measuredQuality, benchmark, version, latency, providerCost, publicCost)
	if candidate.frontierNote != "" {
		summary += ", " + candidate.frontierNote
	}
	return summary
}

// rankEligibleModels 在已经满足能力下界的候选中选择最佳模型。
//
// 排序优先级（高→低）：
//  1. 模型质量档越高越优先；简单/常规任务在 QualityBenefitCap 处收益饱和
//  2. 优先保持请求模型族，避免同档降级时无故跨协议语义族
//  3. 同档候选均有 provider 已确认的能力顺序时，优先选择更强模型
//  4. 结合 CostPreference 使用实测质量、规范基准、版本、延迟与成本证据
//  5. model ID 仅作为最终稳定兜底，不承载推荐顺序
func (r *ModelResolver) rankEligibleModels(
	eligible []ModelProfile,
	requestModel string,
	channelUID string,
	channelKind string,
	floor CapabilityFloor,
) rankedModelCandidate {
	ranked := r.buildRankedCandidates(eligible, requestModel, channelUID, channelKind, floor)
	preferenceMode := r.modelCostPreferenceMode(floor)

	// Frontier 选型：在全部 model × effort 候选的 Pareto 前沿上按车道选择，
	// 成本与质量并列成轴（替代下方 qualityRank 绝对主导的字典序链）。
	// QualityBenefitCap 在 Frontier 内投影到动态 F0...Fn；成本证据不足时
	// 才回退固定 QualityTier 分带与旧字典序链。
	frontierFallback := ""
	idx, note, frontierOK := selectViaFrontier(ranked, floor, preferenceMode)
	if frontierOK {
		best := ranked[idx]
		best.frontierNote = note
		return best
	}
	frontierFallback = note
	ranked = selectQualityBenefitBand(ranked, floor.QualityBenefitCap)

	best := ranked[0]
	for i := 1; i < len(ranked); i++ {
		if betterRankedModel(ranked[i], best, preferenceMode) {
			best = ranked[i]
		}
	}
	if frontierFallback != "" {
		best.frontierNote = "frontier:fallback=" + frontierFallback
	}
	return best
}

// effortAwareBenchmarkScore 根据候选 effort 档位返回该档位对应的 benchmark 分数。
// 当 BenchmarkEvidence 中存有该 effort 的 overall 实测数据时，用实测 rawValue 相对
// default effort 的比值缩放 OverallScore；否则返回原始 OverallScore（不惩罚缺数据）。
// effort 为空时视为 default，直接返回 OverallScore。
func effortAwareBenchmarkScore(bp config.ModelBenchmarkProfile, effort EffortLevel) (float64, bool) {
	if bp.OverallScore <= 0 {
		return 0, false
	}
	if effort == "" || effort == "default" {
		return bp.OverallScore, true
	}

	// 收集 default effort 的 overall rawValue 作为基准。
	var defaultRaw float64
	var effortRaw float64
	for _, ev := range bp.BenchmarkEvidence {
		if ev.Domain != "overall" || ev.RawValue <= 0 {
			continue
		}
		norm := NormalizeEffortLevel(ev.Effort)
		if norm == "" || norm == EffortOff {
			defaultRaw = ev.RawValue
		}
		if norm == effort {
			effortRaw = ev.RawValue
		}
	}
	if defaultRaw <= 0 {
		// 无 default 基准，直接返回 OverallScore。
		return bp.OverallScore, true
	}
	if effortRaw > 0 {
		// 按实测 raw 比值缩放 OverallScore，反映 effort 间真实智商差异。
		ratio := effortRaw / defaultRaw
		return bp.OverallScore * ratio, true
	}
	return bp.OverallScore, true
}

// 优先精确匹配候选 effort 档位；当 evidence 报告的档位超出注册表 SupportedEffortLevels
// （例如 evidence 测了 ultra，但该模型只声明到 max）导致精确键缺失时，回退取该模型
// 已测档位的最小成本，作为该模型成本下界参与 frontier 校准。这样既不伪造注册表未声明的
// 档位，也避免 measuredCostUSD 恒 0 让校准静默失效。无任何实测成本时返回 0。
func measuredCostForEffort(effortCostUSD map[EffortLevel]float64, effort EffortLevel) float64 {
	if cost, ok := effortCostUSD[effort]; ok && cost > 0 {
		return cost
	}
	minCost := 0.0
	for _, cost := range effortCostUSD {
		if cost > 0 && (minCost == 0 || cost < minCost) {
			minCost = cost
		}
	}
	return minCost
}

// buildRankedCandidates 把 eligible 画像展开为 model × effort 排序候选，
// 并补齐证据字段（质量/成本/基准/版本）与 EffortFloor 过滤。
// 候选顺序保持输入顺序，排序决策由调用方完成。
func (r *ModelResolver) buildRankedCandidates(
	eligible []ModelProfile,
	requestModel string,
	channelUID string,
	channelKind string,
	floor CapabilityFloor,
) []rankedModelCandidate {
	reqFamily := InferModelFamily(requestModel, "")
	upstream, global := r.modelRankingCapabilityContext(channelUID, channelKind)
	// EffortFloor 由 ReasoningEffortConfig.PerTaskClass 按任务类推导。
	// 调用方构造 CapabilityFloor 时拿不到 autopilot 配置，因此在此补齐，
	// 使下界过滤与档位回退真正生效而非停留在结构体字段上。
	if floor.EffortFloor == "" {
		floor.EffortFloor = r.resolveEffortFloor(floor.TaskClass)
	}

	ranked := make([]rankedModelCandidate, 0, len(eligible))
	for _, profile := range eligible {
		qualityPriority, qualitySource, qualityKnown := providerModelQualityPriority(profile.ModelID, upstream)
		providerMultiplier, providerSource, providerKnown := providerModelCostMultiplier(profile.ModelID, upstream)
		publicCostUSD, publicCostKnown := normalizedModelCostUSD(profile.ModelID, upstream, global)
		benchmark := config.ResolveModelBenchmarkProfile(profile.ModelID)
		// 收集该模型各 effort 的实测 cost，供 frontier 成本轴校准
		effortCostUSD := make(map[EffortLevel]float64)
		for _, ev := range benchmark.Profile.BenchmarkEvidence {
			normalized := NormalizeEffortLevel(ev.Effort)
			if normalized == "" || ev.CostUSD == nil || math.IsNaN(*ev.CostUSD) || *ev.CostUSD <= 0 {
				continue
			}
			// 同一 effort 多条证据时取最小成本（保守估计）
			if existing, ok := effortCostUSD[normalized]; !ok || *ev.CostUSD < existing {
				effortCostUSD[normalized] = *ev.CostUSD
			}
		}
		baseScore := measuredProviderQualityScore(profile)

		effortLevels, effortDecided := r.resolveEffortVariants(profile, floor)
		for i, effort := range effortLevels {
			decided := effortDecided[i]
			bonus := 0.0
			if decided && effort != "" {
				bonus = EffortQualityBonus(effort) * 0.1
			}
			// 按该候选实际代表的 effort 档位取任务域证据，使 (domain, effort)
			// 精确匹配与跨档位置信度折算真正参与排序，而非恒走 domain-only 回退。
			domainScore := 0.5
			if floor.TaskDomain != "" {
				domainScore = ResolveDomainStrengthForEffort(&profile, floor.TaskDomain, effort).Score
			}
			// 实测 cost 按候选 effort 精确取；未命中时回退该模型已测档位成本下界，
			// 保证 frontier 校准在"evidence 档位超注册表档位"场景仍生效。
			measuredCost := measuredCostForEffort(effortCostUSD, effort)
			// benchmark 分按 effort 特定实测数据缩放，反映不同思考等级的真实智商差异。
			effBenchScore, effBenchKnown := effortAwareBenchmarkScore(benchmark.Profile, effort)
			ranked = append(ranked, rankedModelCandidate{
				profile:                      profile,
				effort:                       effort,
				effortDecided:                decided,
				qualityRank:                  qualityTierRank(profile.QualityTier),
				qualityBenefitCap:            floor.QualityBenefitCap,
				providerModelQualityKnown:    qualityKnown,
				providerModelQualityPriority: qualityPriority,
				providerModelQualitySource:   qualitySource,
				measuredQualityScore:         baseScore + bonus + (domainScore-0.5)*0.1,
				latencyKnown:                 profile.ProbeLatencyMs > 0,
				latencyMs:                    profile.ProbeLatencyMs,
				providerCostKnown:            providerKnown,
				providerCostMultiplier:       providerMultiplier,
				providerCostSource:           providerSource,
				publicCostKnown:              publicCostKnown,
				normalizedPublicCostUSD:      publicCostUSD,
				benchmarkKnown:               effBenchKnown,
				benchmarkScore:               effBenchScore,
				benchmarkModel:               benchmark.Profile.CanonicalModel,
				benchmarkLane:                benchmark.Profile.Lane,
				measuredCostUSD:              measuredCost,
				versionLineage:               modelVersionLineage(profile.ModelFamily, profile.ModelID),
				versionNumbers:               modelVersionNumbers(profile.ModelFamily, profile.ModelID),
				sameFamily:                   profile.ModelFamily == reqFamily,
				normalizedCandidateID:        strings.ToLower(profile.ModelID),
			})
		}
	}

	// EffortFloor 过滤：移除低于下界的已决定候选（fail-open）。
	ranked = filterEffortFloor(ranked, floor)
	qualityPriorityComplete := make(map[int]bool)
	qualityRankSeen := make(map[int]bool)
	for i := range ranked {
		rank := ranked[i].qualityRank
		if !qualityRankSeen[rank] {
			qualityRankSeen[rank] = true
			qualityPriorityComplete[rank] = true
		}
		if !ranked[i].providerModelQualityKnown {
			qualityPriorityComplete[rank] = false
		}
	}
	for i := range ranked {
		ranked[i].providerModelQualityComparable = qualityPriorityComplete[ranked[i].qualityRank]
	}
	return ranked
}

func (r *ModelResolver) modelCostPreferenceMode(floor CapabilityFloor) CostPreferenceMode {
	if override := floor.CostPreferenceOverride; override != "" {
		switch CostPreferenceMode(override) {
		case CostPrefQualityFirst, CostPrefBalanced, CostPrefCostFirst:
			return CostPreferenceMode(override)
		}
	}
	if r == nil || r.cfgManager == nil {
		return CostPrefBalanced
	}
	mode := CostPreferenceMode(r.cfgManager.GetAutopilotRouting().CostPreference.GetEffectiveCostPreferenceMode(string(floor.TaskClass)))
	switch mode {
	case CostPrefQualityFirst, CostPrefBalanced, CostPrefCostFirst:
		return mode
	default:
		return CostPrefBalanced
	}
}

func betterRankedModel(candidate, current rankedModelCandidate, preferenceMode CostPreferenceMode) bool {
	if candidate.qualityRank != current.qualityRank {
		return candidate.qualityRank > current.qualityRank
	}
	if candidate.sameFamily != current.sameFamily {
		return candidate.sameFamily
	}
	if candidate.providerModelQualityComparable && current.providerModelQualityComparable &&
		candidate.providerModelQualityPriority != current.providerModelQualityPriority {
		return candidate.providerModelQualityPriority > current.providerModelQualityPriority
	}

	if preferenceMode == CostPrefCostFirst {
		if better, decided := compareRankedModelCost(candidate, current); decided {
			return better
		}
	}
	// benchmark 是独立能力测量，优先于版本号兜底；显著差异时直接决定。
	if better, decided := compareModelBenchmark(candidate, current); decided {
		return better
	}
	if better, decided := compareModelVersion(candidate, current); decided {
		return better
	}
	if candidate.measuredQualityScore != current.measuredQualityScore {
		return candidate.measuredQualityScore > current.measuredQualityScore
	}
	if preferenceMode == CostPrefBalanced {
		if better, decided := compareRankedModelCost(candidate, current); decided {
			return better
		}
	}
	if candidate.latencyKnown != current.latencyKnown {
		return candidate.latencyKnown
	}
	if candidate.latencyKnown && candidate.latencyMs != current.latencyMs {
		return candidate.latencyMs < current.latencyMs
	}
	if preferenceMode == CostPrefQualityFirst {
		if better, decided := compareRankedModelCost(candidate, current); decided {
			return better
		}
	}
	// anti-effort-inflation：同模型同等质量时，优先选低 effort（够用即止）。
	if candidate.effortDecided && current.effortDecided &&
		candidate.normalizedCandidateID == current.normalizedCandidateID {
		candOrd, cok := effortOrdinal[candidate.effort]
		currOrd, uok := effortOrdinal[current.effort]
		if cok && uok && candOrd != currOrd {
			return candOrd < currOrd
		}
	}
	return candidate.normalizedCandidateID < current.normalizedCandidateID
}

func compareModelBenchmark(candidate, current rankedModelCandidate) (better bool, decided bool) {
	if candidate.benchmarkKnown != current.benchmarkKnown {
		return candidate.benchmarkKnown, true
	}
	if candidate.benchmarkKnown && candidate.benchmarkScore != current.benchmarkScore {
		return candidate.benchmarkScore > current.benchmarkScore, true
	}
	return false, false
}

// modelVersionPattern 提取模型 ID 中最接近厂商/系列标记的版本串。
// 例如 kimi-k2.6 → lineage=k2, numbers=[2,6]；
// kimi-k2.7-code → lineage=k2, numbers=[2,7]。
// 版本号只作为同一 ModelFamily、同一 lineage 且没有更可靠质量证据时的兜底，
// 不跨厂商比较，也不覆盖供应商声明或实测质量。
var modelVersionPattern = regexp.MustCompile(`(?:^|[-/])([a-z]+)-?(\d+(?:[.-]\d+)*)`)

// modelDatedSuffixPattern 匹配模型 ID 末尾的日期快照后缀（如 -0731、-20250929），
// 与注册表 deepseek-v4-flash(?:-\d{4,8})? 的口径一致；快照日期追加为最低位版本号，
// 使同 lineage 内带快照的更新 checkpoint（如 deepseek-v4-flash-0731）优先于基础版。
var modelDatedSuffixPattern = regexp.MustCompile(`-\d{4,8}$`)

func modelVersionLineage(family ModelFamily, modelID string) string {
	prefix, numbers, ok := parseModelVersion(modelID)
	if !ok || len(numbers) == 0 {
		return ""
	}
	if family == ModelFamilyClaude {
		return prefix
	}
	return fmt.Sprintf("%s%d", prefix, numbers[0])
}

func modelVersionNumbers(family ModelFamily, modelID string) []int {
	_, numbers, ok := parseModelVersion(modelID)
	if !ok {
		return nil
	}
	return numbers
}

func parseModelVersion(modelID string) (string, []int, bool) {
	normalized := strings.ToLower(strings.TrimSpace(modelID))
	match := modelVersionPattern.FindStringSubmatch(normalized)
	if len(match) != 3 {
		return "", nil, false
	}
	parts := strings.FieldsFunc(match[2], func(r rune) bool { return r == '.' || r == '-' })
	numbers := make([]int, 0, len(parts)+1)
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			return "", nil, false
		}
		numbers = append(numbers, value)
	}
	if len(numbers) == 0 {
		return "", nil, false
	}
	// 版本串未覆盖 ID 末尾时，把日期快照后缀追加为最低位版本号
	if loc := modelVersionPattern.FindStringIndex(normalized); loc != nil && loc[1] < len(normalized) {
		if suffix := modelDatedSuffixPattern.FindString(normalized); suffix != "" {
			if value, err := strconv.Atoi(suffix[1:]); err == nil {
				numbers = append(numbers, value)
			}
		}
	}
	return match[1], numbers, true
}

func compareModelVersion(candidate, current rankedModelCandidate) (better bool, decided bool) {
	// benchmark 存在显著差异时，版本号兜底不应覆盖独立能力测量。
	if candidate.benchmarkKnown && current.benchmarkKnown {
		if math.Abs(candidate.benchmarkScore-current.benchmarkScore) >= premiumFrontierBenchmarkMinDelta {
			return false, false
		}
	}
	if candidate.profile.ModelFamily == "" || candidate.profile.ModelFamily != current.profile.ModelFamily ||
		candidate.versionLineage == "" || candidate.versionLineage != current.versionLineage ||
		len(candidate.versionNumbers) == 0 || len(current.versionNumbers) == 0 {
		return false, false
	}
	limit := len(candidate.versionNumbers)
	if len(current.versionNumbers) < limit {
		limit = len(current.versionNumbers)
	}
	for i := 0; i < limit; i++ {
		if candidate.versionNumbers[i] == current.versionNumbers[i] {
			continue
		}
		return candidate.versionNumbers[i] > current.versionNumbers[i], true
	}
	if len(candidate.versionNumbers) != len(current.versionNumbers) {
		return len(candidate.versionNumbers) > len(current.versionNumbers), true
	}
	return false, false
}

// selectQualityBenefitBand 为难度已知的简单/常规任务选择最低的足够质量档。
// 若目标档不存在则使用紧邻的更高档；质量降档兜底时选择可用的最高档。
// 更高档模型只有在同渠道成本不高于目标档全部候选时才继续参与，保留“更强且更省”的选择。
// 它只缩小软排序集合，不改变上下文、工具等硬能力过滤，也不影响精确模型命中。
func selectQualityBenefitBand(eligible []rankedModelCandidate, cap QualityTier) []rankedModelCandidate {
	if cap == "" {
		return eligible
	}

	capRank := qualityTierRank(cap)
	selectedRank := -1
	for _, candidate := range eligible {
		rank := candidate.qualityRank
		if rank >= capRank && (selectedRank < capRank || rank < selectedRank) {
			selectedRank = rank
		}
	}
	if selectedRank < 0 {
		for _, candidate := range eligible {
			if rank := candidate.qualityRank; rank > selectedRank {
				selectedRank = rank
			}
		}
	}

	selected := make([]rankedModelCandidate, 0, len(eligible))
	for _, candidate := range eligible {
		if candidate.qualityRank == selectedRank {
			selected = append(selected, candidate)
		}
	}
	if selectedRank < capRank {
		return selected
	}
	for _, candidate := range eligible {
		if candidate.qualityRank > selectedRank && modelCostNoHigherThanBand(candidate, selected) {
			selected = append(selected, candidate)
		}
	}
	return selected
}

func modelCostNoHigherThanBand(candidate rankedModelCandidate, baseline []rankedModelCandidate) bool {
	providerCostPresent := candidate.providerCostKnown
	for _, current := range baseline {
		providerCostPresent = providerCostPresent || current.providerCostKnown
	}
	if providerCostPresent {
		if !candidate.providerCostKnown {
			return false
		}
		for _, current := range baseline {
			if !current.providerCostKnown || candidate.providerCostSource != current.providerCostSource ||
				candidate.providerCostMultiplier > current.providerCostMultiplier {
				return false
			}
		}
		return len(baseline) > 0
	}

	if !candidate.publicCostKnown {
		return false
	}
	for _, current := range baseline {
		if !current.publicCostKnown || candidate.normalizedPublicCostUSD > current.normalizedPublicCostUSD {
			return false
		}
	}
	return len(baseline) > 0
}

// compareRankedModelCost 比较同一渠道内的模型成本证据。
// provider 套餐倍率与公开 USD 价格单位不同，因此先独立比较倍率；
// 仅当双方倍率相同或双方都缺失时，公开价格才作为下一层证据。
func compareRankedModelCost(candidate, current rankedModelCandidate) (better bool, decided bool) {
	if candidate.providerCostKnown != current.providerCostKnown {
		return candidate.providerCostKnown, true
	}
	if candidate.providerCostKnown && candidate.providerCostMultiplier != current.providerCostMultiplier {
		return candidate.providerCostMultiplier < current.providerCostMultiplier, true
	}
	if candidate.publicCostKnown != current.publicCostKnown {
		return candidate.publicCostKnown, true
	}
	if candidate.publicCostKnown && candidate.normalizedPublicCostUSD != current.normalizedPublicCostUSD {
		return candidate.normalizedPublicCostUSD < current.normalizedPublicCostUSD, true
	}
	return false, false
}

// measuredProviderQualityScore 将供应商实测质量按置信度向 0.5 中性值收缩。
// 无可信观测时保持中性，避免零值被误判为最差质量。
func measuredProviderQualityScore(profile ModelProfile) float64 {
	confidence := clampUnit(profile.ProviderQualityConfidence)
	if confidence < 0.5 {
		return 0.5
	}
	quality := clampUnit(profile.ProviderQualityScore)
	return 0.5 + (quality-0.5)*confidence
}

// providerModelQualityPriority 返回 provider 已确认的同档模型能力顺序。
// 映射允许不完整；调用方只有在同档候选全部命中时才使用，避免把“未知”误判为低质量。
func providerModelQualityPriority(
	modelID string,
	upstream *config.UpstreamConfig,
) (int, string, bool) {
	tmpl, ok := providerTemplateForUpstream(upstream)
	if !ok {
		return 0, "", false
	}
	priority, ok := tmpl.ModelQualityPriorityForModel(modelID)
	if !ok {
		return 0, "", false
	}
	return priority, "provider_template:" + tmpl.ProviderID, true
}

// providerModelCostMultiplier 返回当前渠道/provider 套餐内模型的相对消耗倍率。
// ProviderID 是首选事实源；旧配置未保存 ProviderID 时，仅按已知模板 URL 做保守识别。
func providerModelCostMultiplier(
	modelID string,
	upstream *config.UpstreamConfig,
) (float64, string, bool) {
	tmpl, ok := providerTemplateForUpstream(upstream)
	if !ok {
		return 0, "", false
	}
	multiplier, ok := tmpl.ModelCostMultiplierForModel(modelID)
	if !ok {
		return 0, "", false
	}
	return multiplier, "provider_template:" + tmpl.ProviderID, true
}

func providerTemplateForUpstream(upstream *config.UpstreamConfig) (*config.ProviderTemplate, bool) {
	if upstream == nil {
		return nil, false
	}
	providerID := strings.TrimSpace(upstream.ProviderID)
	if providerID == "" {
		providerID, _ = config.InferProviderIDFromBaseURL(upstream.GetEffectiveBaseURL())
	}
	return config.GetProviderTemplate(providerID)
}

// normalizedModelCostUSD 使用统一的 100 万输入 + 100 万输出作为公开价格比较基准。
// provider 套餐倍率缺失或相同时使用；实际 endpoint 折扣仍由 endpoint 调度处理。
func normalizedModelCostUSD(
	modelID string,
	upstream *config.UpstreamConfig,
	global map[string]config.UpstreamModelCapability,
) (float64, bool) {
	resolved := config.ResolveUpstreamCapability(modelID, upstream, global)
	if !resolved.Known || !hasKnownModelPricing(resolved.Capability.Pricing) {
		return 0, false
	}
	cost := metrics.CalculateTokenCostUSDWithPricing(
		resolved.Capability.Pricing,
		1_000_000,
		1_000_000,
		0,
		0,
	)
	return cost, true
}

func hasKnownModelPricing(pricing *config.ModelPricing) bool {
	if pricing == nil {
		return false
	}
	if pricing.InputCacheHitPrice != nil || pricing.InputCacheMissPrice != nil || pricing.OutputPrice != nil {
		return true
	}
	for _, tier := range pricing.Tiers {
		if tier.InputCacheHitPrice != nil || tier.InputCacheMissPrice != nil || tier.OutputPrice != nil {
			return true
		}
	}
	return false
}

// modelRankingCapabilityContext 返回模型选优使用的能力与价格上下文。
// 自动解析候选已经是 endpoint 的实际模型名，因此清空 ModelMapping，避免价格查询形成链式重定向。
func (r *ModelResolver) modelRankingCapabilityContext(
	channelUID string,
	channelKind string,
) (*config.UpstreamConfig, map[string]config.UpstreamModelCapability) {
	if r.cfgManager == nil {
		return nil, nil
	}
	cfg := r.cfgManager.GetConfig()
	upstream := r.findUpstream(channelUID, channelKind)
	if upstream == nil {
		return nil, cfg.UpstreamModelCapabilities
	}
	upstreamCopy := *upstream
	upstreamCopy.ModelMapping = nil
	return &upstreamCopy, cfg.UpstreamModelCapabilities
}

// refreshAutoDiscoveryCapabilities 兼容由旧版本写入的自动发现画像。
// 旧实现误用了下游 AgentModelProfile，可能把 GLM-5.2 等上游模型写成错误窗口和能力；
// 运行时以当前上游能力注册表重新派生，后续自动发现会把同样结果持久化。
func (r *ModelResolver) refreshAutoDiscoveryCapabilities(
	candidates []ModelProfile,
	channelUID string,
	channelKind string,
) []ModelProfile {
	if len(candidates) == 0 {
		return candidates
	}

	var upstream *config.UpstreamConfig
	var global map[string]config.UpstreamModelCapability
	if r.cfgManager != nil {
		cfg := r.cfgManager.GetConfig()
		global = cfg.UpstreamModelCapabilities
		upstream = r.findUpstream(channelUID, channelKind)
	}

	refreshed := append([]ModelProfile(nil), candidates...)
	for i := range refreshed {
		profile := &refreshed[i]
		if profile.Source != "auto_discovery" {
			continue
		}
		oldFamily := profile.ModelFamily
		oldQuality := profile.QualityTier
		oldContext := profile.ContextTokens
		oldVision := profile.SupportsVision
		oldDocument := profile.SupportsDocument
		oldTools := profile.SupportsToolCalls
		oldReasoning := profile.SupportsReasoning
		profile.ModelFamily = InferModelFamily(profile.ModelID, "")
		profile.QualityTier = ModelProfileQualityTier(profile.ModelID, profile.ModelFamily)
		if resolved := config.ResolveUpstreamCapability(profile.ModelID, upstream, global); resolved.Known {
			applyUpstreamModelCapability(profile, resolved.Capability)
		}
		if oldFamily != profile.ModelFamily || oldQuality != profile.QualityTier ||
			oldContext != profile.ContextTokens || oldVision != profile.SupportsVision ||
			oldDocument != profile.SupportsDocument ||
			oldTools != profile.SupportsToolCalls || oldReasoning != profile.SupportsReasoning {
			profile.UpdatedAt = time.Now()
			_ = r.profileStore.Upsert(profile)
		}
	}
	return refreshed
}

// ── 辅助 ──

// findUpstream 根据 channelUID 和 channelKind 从 ConfigManager 查找对应的 UpstreamConfig。
// 遍历所有渠道类型列表，匹配 ChannelUID。
// 返回 nil 表示未找到（渠道已删除或 UID 不匹配）。
func (r *ModelResolver) findUpstream(channelUID, channelKind string) *config.UpstreamConfig {
	if r.cfgManager == nil || channelUID == "" {
		return nil
	}
	cfg := r.cfgManager.GetConfig()

	type upstreamList struct {
		channels []config.UpstreamConfig
		kind     string
	}
	lists := []upstreamList{
		{cfg.Upstream, "messages"},
		{cfg.ResponsesUpstream, "responses"},
		{cfg.GeminiUpstream, "gemini"},
		{cfg.ChatUpstream, "chat"},
		{cfg.ImagesUpstream, "images"},
		{cfg.VectorsUpstream, "vectors"},
	}

	for _, ul := range lists {
		if ul.kind != channelKind {
			continue
		}
		for i := range ul.channels {
			if ul.channels[i].ChannelUID == channelUID {
				return &ul.channels[i]
			}
		}
	}
	return nil
}
