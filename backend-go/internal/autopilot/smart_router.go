package autopilot

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

// ── SmartRouter（设计 §4.6 + §4.6.3 + §4.6.5 + P0.4 + P0.5）──

// RoutingPlanCandidate 是 dry-run 候选，保留评分明细并附加自动路由约束结果。
// 匿名嵌入保持既有 channelUid/score 等 JSON 字段不变。
type RoutingPlanCandidate struct {
	ScoredCandidate
	Selected      bool     `json:"selected"`
	FilterReasons []string `json:"filterReasons,omitempty"`
	MappedModel   string   `json:"mappedModel,omitempty"`
	MappingSource string   `json:"mappingSource,omitempty"`
	MappingReason string   `json:"mappingReason,omitempty"`
	ChannelName   string   `json:"channelName,omitempty"`
	KeyMask       string   `json:"keyMask,omitempty"`
}

// RoutingPlan 一次请求的路由计划（§4.6.1）。
type RoutingPlan struct {
	RequestProfile     *RequestProfile        `json:"requestProfile"`
	Candidates         []RoutingPlanCandidate `json:"candidates"`
	SelectedChannelUID string                 `json:"selectedChannelUid,omitempty"`
	SelectedModel      string                 `json:"selectedModel,omitempty"`
	FallbackUsed       bool                   `json:"fallbackUsed"`
	SortReasons        []string               `json:"sortReasons,omitempty"`
	Mode               RoutingMode            `json:"mode"`
	Weights            ScoringWeights         `json:"weights"`
}

// SmartRouter 根据请求画像 + 渠道画像生成路由计划。
// shadow 模式：只计算 + 记录 RoutingDecisionTrace，不影响真实调度。
// active 模式：返回评分排序后的候选列表，改变调度顺序。
// off / kill switch：不注入 CandidateFilter，调度链路不变。
type SmartRouter struct {
	profileStore      *ProfileStore
	intentStore       *ManualIntentStore
	traceStore        *TraceStore
	configManager     *config.ConfigManager
	advisor           *TrustedRoutingAdvisor // Phase 2: 可信路由顾问（nil = 不启用）
	decisionStore     *AdvisorDecisionStore  // Phase 2: advisor 决策记录存储
	localRuntimeStore *LocalRuntimeStore     // Phase 2: 本地运行时存储（nil = 不纳入本地候选）
	modelResolver     *ModelResolver         // dry-run 自动模型映射预览（nil = 不扩展候选）
	modelProfileStore *ModelProfileStore     // endpoint 模型质量/任务域覆盖（nil = 仅用规范基准与种子）
	now               func() time.Time

	// onCandidatesRanked Phase 4 Item 8: 候选排名回调（A/B 测试用）。
	// executeFilter 完成评分排序后调用，传入 ranked candidates。
	onCandidatesRanked func(model, channelKind string, candidates []RoutingCandidate)
}

// NewSmartRouter 创建 SmartRouter 实例。
func NewSmartRouter(
	profileStore *ProfileStore,
	intentStore *ManualIntentStore,
	traceStore *TraceStore,
	configManager *config.ConfigManager,
) *SmartRouter {
	return &SmartRouter{
		profileStore:  profileStore,
		intentStore:   intentStore,
		traceStore:    traceStore,
		configManager: configManager,
		now:           time.Now,
	}
}

// ConfigManager 返回内部 ConfigManager 引用。
func (r *SmartRouter) ConfigManager() *config.ConfigManager {
	return r.configManager
}

// IsFrontierRoutingEnabled 返回 Frontier/Ladder 是否在生产中影响选路。
func (r *SmartRouter) IsFrontierRoutingEnabled() bool {
	if r.configManager == nil {
		return false
	}
	return r.configManager.GetAutopilotRouting().IsFrontierRoutingEnabled()
}

// IsAFPCostRoutingEnabled 返回 AFP 成本适配器是否参与可比树。
func (r *SmartRouter) IsAFPCostRoutingEnabled() bool {
	if r.configManager == nil {
		return false
	}
	return r.configManager.GetAutopilotRouting().IsAFPCostRoutingEnabled()
}

// TraceStore 返回内部 TraceStore 引用。
func (r *SmartRouter) TraceStore() *TraceStore {
	return r.traceStore
}

// ProfileStore 返回内部 ProfileStore 引用。
func (r *SmartRouter) ProfileStore() *ProfileStore {
	return r.profileStore
}

// IntentStore 返回内部 ManualIntentStore 引用。
func (r *SmartRouter) IntentStore() *ManualIntentStore {
	return r.intentStore
}

// SetAdvisor 设置 TrustedRoutingAdvisor 和 AdvisorDecisionStore（由 main.go 在构造后调用）。
// nil 参数表示不启用对应功能（fail-safe：不影响调度）。
func (r *SmartRouter) SetAdvisor(advisor *TrustedRoutingAdvisor, decisionStore *AdvisorDecisionStore) {
	r.advisor = advisor
	r.decisionStore = decisionStore
}

// SetLocalRuntimeStore 设置 LocalRuntimeStore（由 main.go 在构造后调用）。
// nil 表示不纳入本地候选（fail-safe：不影响调度）。
func (r *SmartRouter) SetLocalRuntimeStore(store *LocalRuntimeStore) {
	r.localRuntimeStore = store
}

// SetModelResolver 设置请求级自动模型映射器。
// 真实路由只解析 scheduler 已提供的候选，不会额外扩展候选集合；
// dry-run 则用同一解析逻辑预览可承接请求的自动托管渠道。
func (r *SmartRouter) SetModelResolver(resolver *ModelResolver) {
	r.modelResolver = resolver
}

// SetModelProfileStore 设置 endpoint 模型画像，用于规范能力上界的渠道质量折算。
func (r *SmartRouter) SetModelProfileStore(store *ModelProfileStore) {
	r.modelProfileStore = store
}

// SetOnCandidatesRanked 设置候选排名回调（Phase 4 Item 8: A/B 测试用）。
// executeFilter 完成评分排序后调用，将排名结果传递给调用方。
func (r *SmartRouter) SetOnCandidatesRanked(fn func(model, channelKind string, candidates []RoutingCandidate)) {
	r.onCandidatesRanked = fn
}

// BuildPlan 为请求构建路由计划（§4.6.1）。
// 用于 dry-run API 和诊断，不影响真实调度。
func (r *SmartRouter) BuildPlan(profile *RequestProfile) *RoutingPlan {
	if profile == nil {
		return &RoutingPlan{Mode: RoutingModeDryRun}
	}

	// 确定分类
	input := BuildClassifierInput(profile)
	ClassifyAndFill(profile, input)

	cfg := r.configManager.GetConfig()
	autopilotCfg := cfg.AutopilotRouting

	// 获取权重
	weights := DefaultTaskWeights()[profile.TaskClass]
	weights = ApplyCostPreference(weights, CostPreferenceMode(autopilotCfg.CostPreference.Mode))

	// 覆盖权重
	for k, v := range autopilotCfg.WeightOverrides {
		switch k {
		case "wQuality":
			weights.WQuality = v
		case "wStability":
			weights.WStability = v
		case "wSpeed":
			weights.WSpeed = v
		case "wCost":
			weights.WCost = v
		case "wSavings":
			weights.WSavings = v
		case "wTierMatch":
			weights.WTierMatch = v
		case "wFamily":
			weights.WFamily = v
		case "wProviderQuality":
			weights.WProviderQuality = v
		case "wDomain":
			weights.WDomain = v
		}
	}

	familyPrefs := r.loadFamilyPrefs(autopilotCfg.ModelFamilyPreference)

	// 收集并评分候选
	entries := r.collectChannelEntries(profile)

	// P1.5：按 channel 禁用——与 executeFilter（真实路由路径）保持一致。
	// 注意：这里只过滤"禁用渠道"这个硬约束，不像 kill switch/mode==off 那样
	// 让 BuildPlan 提前返回——BuildPlan 是诊断预览接口，即使 SmartRouter
	// 处于 off/kill switch 也要能算出"如果启用会怎样"（见 P0.5 不变量测试），
	// DisabledTaskClasses 属于同一类"是否运行"的开关，因此不在此处短路；
	// DisabledChannelUIDs 则是候选集合本身的硬约束，必须和真实路径一致。
	if disabledChannelUIDs := toStringSet(autopilotCfg.DisabledChannelUIDs); len(disabledChannelUIDs) > 0 {
		filtered := entries[:0:0]
		for _, e := range entries {
			if disabledChannelUIDs[e.ChannelUID] {
				continue
			}
			filtered = append(filtered, e)
		}
		entries = filtered
	}

	if len(entries) == 0 {
		return &RoutingPlan{
			RequestProfile: profile,
			Candidates:     nil,
			Mode:           RoutingModeDryRun,
			Weights:        weights,
		}
	}

	costs := make(map[string]float64, len(entries))
	for _, e := range entries {
		costs[e.ChannelUID] = e.EstimatedCost
	}
	// AFP 路由与真实路径保持一致：开启时按渠道填充 AFP 成本并分组归一化。
	afpEnabled := r.IsAFPCostRoutingEnabled()
	if afpEnabled {
		r.applyAFPCosts(entries, profile.EstTokens)
	}
	var savingsMap map[string]float64
	if afpEnabled {
		savingsMap = normalizeSavingsScoreGrouped(entries)
	} else {
		savingsMap = NormalizeSavingsScore(costs)
	}

	ctx := ScoringContext{
		TaskClass:         profile.TaskClass,
		TaskDomain:        profile.TaskDomain,
		TargetQualityTier: requestQualityTarget(profile),
		QualityBenefitCap: requestQualityBenefitCap(profile),
		FamilyPrefs:       familyPrefs,
		Weights:           weights,
	}

	scoredEntries := make([]scoredChannelEntry, 0, len(entries))
	for _, e := range entries {
		e.ScoringCandidate.SavingsScore = savingsMap[e.ChannelUID]
		applyDomainStrength(&e, ctx.TaskDomain)
		scored := ScoreCandidate(e.ScoringCandidate, ctx)
		scoredEntries = append(scoredEntries, scoredChannelEntry{entry: e, scored: scored})
	}
	sortScoredChannelEntries(scoredEntries)

	selectedCandidates := make([]RoutingPlanCandidate, 0, len(scoredEntries))
	filteredCandidates := make([]RoutingPlanCandidate, 0, len(scoredEntries))
	for _, se := range scoredEntries {
		reasons := routingHardConstraintReasons(profile, &se.entry)
		candidate := RoutingPlanCandidate{
			ScoredCandidate: se.scored,
			Selected:        len(reasons) == 0,
			FilterReasons:   reasons,
			MappedModel:     se.entry.MappedModel,
			MappingSource:   se.entry.MappingSource,
			MappingReason:   se.entry.MappingReason,
			ChannelName:     se.entry.ChannelName,
			KeyMask:         se.entry.KeyMask,
		}
		if candidate.Selected {
			selectedCandidates = append(selectedCandidates, candidate)
		} else {
			filteredCandidates = append(filteredCandidates, candidate)
		}
	}

	fallbackUsed := len(selectedCandidates) == 0 && len(filteredCandidates) > 0
	candidates := make([]RoutingPlanCandidate, 0, len(scoredEntries))
	selectedChannelUID := ""
	selectedModel := ""
	sortReasons := []string{"smart_routing_dryrun"}
	if fallbackUsed {
		// 与 auto 一致：全部候选不满足硬约束时，不返回空计划，而是回退到原评分顺序。
		candidates = append(candidates, filteredCandidates...)
		selectedChannelUID = candidates[0].ChannelUID
		selectedModel = resolvedCandidateModel(profile.Model, candidates[0].MappedModel)
		sortReasons = append(sortReasons, "dryrun_auto_failopen_simulation")
	} else {
		// 通过硬约束的候选排在前面；过滤候选保留在尾部供诊断。
		candidates = append(candidates, selectedCandidates...)
		candidates = append(candidates, filteredCandidates...)
		if len(selectedCandidates) > 0 {
			selectedChannelUID = selectedCandidates[0].ChannelUID
			selectedModel = resolvedCandidateModel(profile.Model, selectedCandidates[0].MappedModel)
		}
		sortReasons = append(sortReasons, "dryrun_auto_filter_simulation")
	}
	for _, candidate := range candidates {
		if candidate.MappingSource == "auto_resolve_preview" {
			sortReasons = append(sortReasons, "dryrun_auto_resolve_preview")
			break
		}
	}

	plan := &RoutingPlan{
		RequestProfile:     profile,
		Candidates:         candidates,
		SelectedChannelUID: selectedChannelUID,
		SelectedModel:      selectedModel,
		FallbackUsed:       fallbackUsed,
		SortReasons:        sortReasons,
		Mode:               RoutingModeDryRun,
		Weights:            weights,
	}

	// 写入 dry-run trace（设计 §3.4：所有真实或 dry-run 路由都有 trace）
	if r.traceStore != nil {
		traceCandidates := make([]RoutingCandidate, 0, len(candidates))
		for _, c := range candidates {
			traceCandidates = append(traceCandidates, RoutingCandidate{
				ChannelUID:    c.ChannelUID,
				ChannelName:   c.ChannelName,
				KeyMask:       c.KeyMask,
				MappedModel:   c.MappedModel,
				MappingSource: c.MappingSource,
				MappingReason: c.MappingReason,
				TotalScore:    c.Score,
				Selected:      c.Selected,
				FilterReasons: c.FilterReasons,
			})
		}
		dryRunTrace := &RoutingDecisionTrace{
			SchemaVersion:      2,
			Source:             "dry_run",
			RequestKind:        profile.ChannelKind,
			TaskClass:          profile.TaskClass,
			TaskDomain:         profile.TaskDomain,
			RequestedModel:     profile.Model,
			AgentRole:          profile.AgentRole,
			Mode:               RoutingModeDryRun,
			TargetMode:         RoutingModeDryRun,
			EffectiveMode:      RoutingModeDryRun,
			Candidates:         traceCandidates,
			CandidatesBefore:   len(entries),
			CandidatesAfter:    len(selectedCandidates),
			SelectedChannelUID: selectedChannelUID,
			FallbackUsed:       fallbackUsed,
			SortReasons:        sortReasons,
		}
		r.traceStore.Record(dryRunTrace)
	}

	return plan
}

// CandidateFilterFor 为给定请求构建 scheduler.CandidateFilterFunc。
// 返回 nil 表示不注入（off / kill switch）。
// shadow 模式：计算评分 + 记录 RoutingDecisionTrace，返回原始候选列表。
// assist 模式：按评分重排渠道列表，不删除任何渠道。
// auto 模式：硬约束过滤 + 重排；过滤后为空则 fail-open 回退到只重排。
// active 模式：返回评分排序后的候选列表。
func (r *SmartRouter) CandidateFilterFor(profile *RequestProfile) scheduler.CandidateFilterFunc {
	return r.candidateFilterFor(profile, nil)
}

// CandidateFilterForWithActual 返回请求级 CandidateFilter 与真实渠道回填回调。
// 回调只更新该 filter 本次执行生成的 trace，避免并发请求串写“最近一条”记录。
func (r *SmartRouter) CandidateFilterForWithActual(
	profile *RequestProfile,
) (scheduler.CandidateFilterFunc, scheduler.CandidateSelectionObserver) {
	var traceMu sync.Mutex
	traceUID := ""

	filter := r.candidateFilterFor(profile, func(uid string) {
		traceMu.Lock()
		traceUID = uid
		traceMu.Unlock()
	})
	if filter == nil {
		return nil, nil
	}

	observer := func(actualChannelUID string) string {
		traceMu.Lock()
		uid := traceUID
		traceMu.Unlock()
		if uid != "" {
			r.UpdateActualChannel(uid, actualChannelUID)
		}
		return uid
	}
	return filter, observer
}

func (r *SmartRouter) candidateFilterFor(
	profile *RequestProfile,
	onTraceRecorded func(traceUID string),
) scheduler.CandidateFilterFunc {
	if profile == nil {
		return nil
	}

	cfg := r.configManager.GetConfig()
	autopilotCfg := cfg.AutopilotRouting
	if autopilotCfg.KillSwitch {
		return nil
	}
	routerMode := RoutingModeAuto

	// 确定分类
	input := BuildClassifierInput(profile)
	ClassifyAndFill(profile, input)

	// P1.5：按 task class 禁用——命中时 SmartRouter 对本次请求完全不介入，
	// 与 kill switch 的 "return nil" 语义完全一致，只是作用范围缩小到单个 TaskClass。
	if isTaskClassDisabled(autopilotCfg.DisabledTaskClasses, profile.TaskClass) {
		return nil
	}

	// 获取权重
	weights := DefaultTaskWeights()[profile.TaskClass]
	weights = ApplyCostPreference(weights, CostPreferenceMode(autopilotCfg.CostPreference.Mode))
	for k, v := range autopilotCfg.WeightOverrides {
		switch k {
		case "wQuality":
			weights.WQuality = v
		case "wStability":
			weights.WStability = v
		case "wSpeed":
			weights.WSpeed = v
		case "wCost":
			weights.WCost = v
		case "wSavings":
			weights.WSavings = v
		case "wTierMatch":
			weights.WTierMatch = v
		case "wFamily":
			weights.WFamily = v
		case "wProviderQuality":
			weights.WProviderQuality = v
		case "wDomain":
			weights.WDomain = v
		}
	}

	familyPrefs := r.loadFamilyPrefs(autopilotCfg.ModelFamilyPreference)

	traceStore := r.traceStore
	disabledChannelUIDs := toStringSet(autopilotCfg.DisabledChannelUIDs)

	return func(
		channels []scheduler.ChannelInfo,
		upstreamFor func(scheduler.ChannelInfo) *config.UpstreamConfig,
		candidateAvailable func(scheduler.ChannelInfo, *config.UpstreamConfig) bool,
	) ([]scheduler.ChannelInfo, error) {
		return r.executeFilter(
			channels, upstreamFor, candidateAvailable,
			profile, weights, familyPrefs, routerMode, traceStore, disabledChannelUIDs,
			cfg.UpstreamModelCapabilities,
			onTraceRecorded,
		)
	}
}

// isTaskClassDisabled 判断 taskClass 是否命中禁用名单。
func isTaskClassDisabled(disabled []string, taskClass TaskClass) bool {
	for _, d := range disabled {
		if TaskClass(d) == taskClass {
			return true
		}
	}
	return false
}

// toStringSet 把字符串 slice 转成 set，便于 O(1) 查找。nil/空输入返回 nil。
func toStringSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item] = true
	}
	return set
}

// executeFilter 执行 SmartRouter 过滤逻辑。
func (r *SmartRouter) executeFilter(
	channels []scheduler.ChannelInfo,
	upstreamFor func(scheduler.ChannelInfo) *config.UpstreamConfig,
	candidateAvailable func(scheduler.ChannelInfo, *config.UpstreamConfig) bool,
	profile *RequestProfile,
	weights ScoringWeights,
	familyPrefs []ModelFamily,
	mode RoutingMode,
	traceStore *TraceStore,
	disabledChannelUIDs map[string]bool,
	upstreamModelCapabilities map[string]config.UpstreamModelCapability,
	onTraceRecorded func(traceUID string),
) ([]scheduler.ChannelInfo, error) {
	startTime := time.Now()

	// 构建 RoutingDecisionTrace
	trace := &RoutingDecisionTrace{
		SchemaVersion:       2,
		RequestKind:         profile.ChannelKind,
		TaskClass:           profile.TaskClass,
		TaskDomain:          profile.TaskDomain,
		RequestedModel:      profile.Model,
		AgentRole:           profile.AgentRole,
		Mode:                mode,
		TargetMode:          mode,
		EffectiveMode:       mode,
		CandidatesBefore:    len(channels),
		GlobalFilterReasons: make(map[string][]string),
	}

	// 构建评分上下文
	scoringCtx := ScoringContext{
		TaskClass:         profile.TaskClass,
		TaskDomain:        profile.TaskDomain,
		TargetQualityTier: requestQualityTarget(profile),
		QualityBenefitCap: requestQualityBenefitCap(profile),
		FamilyPrefs:       familyPrefs,
		Weights:           weights,
	}

	// 收集所有候选的估算成本用于归一化
	costMap := make(map[string]float64, len(channels))
	entries := make([]channelScoreEntry, 0, len(channels))
	for _, ch := range channels {
		upstream := upstreamFor(ch)
		if upstream == nil {
			continue
		}
		// P1.5：按 channel 禁用——命中的渠道对 autopilot 不存在，走和
		// "候选不可用" 完全相同的跳过路径，不影响其他非 autopilot 选路径。
		if disabledChannelUIDs[upstream.ChannelUID] {
			continue
		}
		if !candidateAvailable(ch, upstream) {
			continue
		}
		modelResolution := r.resolveChannelModel(profile, upstream, upstreamModelCapabilities)
		entry := r.buildChannelEntry(
			ch,
			upstream,
			profile.ChannelKind,
			modelResolution.ActualModel,
			upstreamModelCapabilities,
		)
		entry.MappedModel = modelResolution.MappedModel
		entry.MappingSource = modelResolution.MappingSource
		entry.MappingReason = modelResolution.MappingReason
		r.applyModelQualityTier(&entry)
		entries = append(entries, entry)
		costMap[entry.ChannelUID] = entry.EstimatedCost
	}
	// AFP 路由：为火山 Agent Plan 渠道填充 AFP 成本（含折扣），开启时用分组归一化
	// 替代扁平 USD 归一化，使 GLM-5.2 ×0.25 等折扣真正影响 SavingsScore。
	afpEnabled := r.IsAFPCostRoutingEnabled()
	if afpEnabled {
		r.applyAFPCosts(entries, profile.EstTokens)
	}
	var savingsMap map[string]float64
	if afpEnabled {
		savingsMap = normalizeSavingsScoreGrouped(entries)
	} else {
		savingsMap = NormalizeSavingsScore(costMap)
	}

	// 评分；advisor 可能追加本地候选，因此统一在其后排序。
	scoredEntries := make([]scoredChannelEntry, 0, len(entries))
	for _, e := range entries {
		e.ScoringCandidate.SavingsScore = savingsMap[e.ChannelUID]
		applyDomainStrength(&e, scoringCtx.TaskDomain)
		scored := ScoreCandidate(e.ScoringCandidate, scoringCtx)
		scoredEntries = append(scoredEntries, scoredChannelEntry{entry: e, scored: scored})
	}

	// ── Phase 2: Advisor hint + 本地候选 ──
	// 1) advisor hint 评估（shadow 模式下 Applied=false，不影响调度）
	var advisorDecisionUID string
	// advisorMinQualityTier 由下方闭包写入，供硬约束过滤阶段直接读取（类型化传值，
	// 不经过 trace.GlobalFilterReasons 的字符串往返 —— 避免格式变更导致静默失效）。
	var advisorMinQualityTier QualityTier
	if r.advisor != nil && r.decisionStore != nil {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("[SmartRouter-Advisor] panic recovered (fail-open): %v", rec)
				}
			}()

			// 从 RequestProfile 构建 AdvisorInput
			advisorInput := AdvisorInput{
				RequestKind:          profile.ChannelKind,
				Operation:            profile.Operation,
				RequestedModel:       profile.Model,
				AgentRole:            profile.AgentRole,
				InputTokenBucket:     classifyTokenBucket(profile.EstTokens),
				HasImage:             profile.HasImage,
				NeedsToolUse:         profile.ToolUseNeed,
				NeedsReasoning:       profile.ReasoningNeed,
				NeedsLongContext:     profile.ContextNeed >= 50_000,
				CandidateTaskClasses: []TaskClass{profile.TaskClass},
			}
			hint, _ := r.advisor.EvaluateShadow(advisorInput)

			// 获取 TrustedRoutingAdvisorConfig
			autopilotCfg := r.configManager.GetConfig().AutopilotRouting
			effect := ResolveAdvisorHintEffect(hint, autopilotCfg.TrustedRoutingAdvisor, profile.TaskClass)

			// 无论 Applied 与否，都记录决策（用于人工审查 promotion 依据）
			rec := &AdvisorDecisionRecord{
				AdvisorUID:        "heuristic", // 固定值：Phase 1 只使用启发式后端
				AdvisorOriginTier: "local",
				Mode:              r.advisor.State(),
				TaskClass:         profile.TaskClass,
				PromptHash:        profile.PromptHash,
				InputTokenBucket:  advisorInput.InputTokenBucket,
				Applied:           effect.Applied,
				Outcome:           "shadow", // 后续在调度结果回调中更新
				CreatedAt:         time.Now().UTC(),
			}
			if hint != nil {
				rec.Hint = *hint
			}
			if recordErr := r.decisionStore.Record(rec); recordErr != nil {
				log.Printf("[SmartRouter-Advisor] 决策记录失败: %v", recordErr)
			}
			advisorDecisionUID = rec.DecisionUID
			trace.AdvisorDecisionUID = advisorDecisionUID

			if effect.Applied {
				// MinQualityTier 转化为硬约束过滤条件
				if effect.MinQualityTier != "" {
					advisorMinQualityTier = effect.MinQualityTier
					// trace 侧仍记录可读原因，供 UI/人工审查展示（非控制流依赖）。
					trace.GlobalFilterReasons["advisor_min_quality_tier"] = []string{
						fmt.Sprintf("MinQualityTier=%s (Applied=true)", effect.MinQualityTier),
					}
				}

				// 本地候选允许标记：需结合 LocalModelRoutingConfig 再判一次
				if effect.AllowLocalCandidate && r.localRuntimeStore != nil {
					localCfg := autopilotCfg.LocalModelRouting
					localEntries := CollectLocalCandidates(r.localRuntimeStore, localCfg, profile.TaskClass)
					for _, le := range localEntries {
						localEntry := channelScoreEntry{
							ChannelUID:          le.RuntimeUID,
							ChannelKind:         profile.ChannelKind,
							OriginTier:          OriginTierLocal,
							HealthState:         HealthStateHealthy,
							EstimatedCost:       le.EstimatedCost,
							SupportsVision:      le.SupportsVision,
							SupportsToolCalls:   le.SupportsToolCalls,
							SupportsReasoning:   le.SupportsReasoning,
							ContextWindowTokens: le.ContextWindowTokens,
							ScoringCandidate: ScoringCandidate{
								ChannelUID:                le.RuntimeUID,
								QualityTier:               QualityTierNormal, // 中性默认值
								StabilityTier:             StabilityTierNormal,
								SpeedTier:                 SpeedTierNormal,
								CostTier:                  CostTierFree, // 本地运行时免费
								HealthState:               HealthStateHealthy,
								ProviderQualityScore:      0.5,
								ProviderQualityConfidence: 0.3,
								SavingsScore:              0.5,
								DomainStrengthScore:       0.5,
							},
						}
						// 本地候选纳入评分流程
						localEntry.ScoringCandidate.SavingsScore = savingsMap[le.RuntimeUID]
						localScored := ScoreCandidate(localEntry.ScoringCandidate, scoringCtx)
						scoredEntries = append(scoredEntries, scoredChannelEntry{entry: localEntry, scored: localScored})
						// 本地候选成本为0，可能影响 savingsMap 归一化；
						// 但不影响排序结果（savings 只是其中一个维度）
					}
				}

				log.Printf("[SmartRouter-Advisor] hint生效 taskClass=%s MinQualityTier=%s AllowLocal=%v",
					string(profile.TaskClass), effect.MinQualityTier, effect.AllowLocalCandidate)
			}
		}()
	}
	sortScoredChannelEntries(scoredEntries)

	// ── 人工意图匹配（设计 §4.6.4）──
	// 在评分排序后、构建结果前执行。
	var matchedIntent *IntentMatchResult
	var intentTargetUID string
	if r.intentStore != nil && len(scoredEntries) > 1 {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					log.Printf("[SmartRouter-IntentMatch] panic recovered (fail-open): %v", rec)
				}
			}()

			activeIntents := r.intentStore.ListActive()
			if len(activeIntents) == 0 {
				return
			}

			matchCtx := &IntentMatchContext{
				ChannelKind: profile.ChannelKind,
				Model:       profile.Model,
				TaskClass:   profile.TaskClass,
				AgentRole:   profile.AgentRole,
				SessionID:   profile.SessionID,
				PromptHash:  profile.PromptHash,
			}
			matchedIntent = MatchIntent(matchCtx, activeIntents)
			if matchedIntent == nil || matchedIntent.ChannelUID == "" {
				matchedIntent = nil
				return
			}
			intentTargetUID = matchedIntent.ChannelUID

			// supervisor 保护：third-party 渠道的 model_trial 不覆盖 supervisor，
			// 除非意图 TaskClasses 显式包含 supervisor。
			if profile.TaskClass == TaskClassSupervisor &&
				matchedIntent.Intent.IntentType == IntentTypeModelTrial &&
				!intentExplicitlyTargetsSupervisor(matchedIntent.Intent) {
				for _, se := range scoredEntries {
					if se.entry.ChannelUID == intentTargetUID &&
						se.entry.OriginTier == OriginTierThird {
						log.Printf("[SmartRouter-SupervisorProtect] third-party 渠道 %s 的 model_trial 不覆盖 supervisor (intent=%s)",
							intentTargetUID, matchedIntent.Intent.IntentUID)
						trace.GlobalFilterReasons["supervisor_protect"] = []string{
							fmt.Sprintf("intent=%s: third-party model_trial blocked for supervisor", matchedIntent.Intent.IntentUID),
						}
						matchedIntent = nil
						intentTargetUID = ""
						return
					}
				}
			}

			// 将目标渠道提升到 scoredEntries 首位（protected candidate）
			targetIdx := -1
			for i, se := range scoredEntries {
				if se.entry.ChannelUID == intentTargetUID {
					targetIdx = i
					break
				}
			}
			if targetIdx < 0 {
				// 目标渠道不在候选中
				trace.GlobalFilterReasons["intent_target_missing"] = []string{
					fmt.Sprintf("intent=%s: target channel %s not in candidates", matchedIntent.Intent.IntentUID, intentTargetUID),
				}
				matchedIntent = nil
				intentTargetUID = ""
				return
			}
			if targetIdx > 0 {
				promoted := scoredEntries[targetIdx]
				copy(scoredEntries[1:targetIdx+1], scoredEntries[0:targetIdx])
				scoredEntries[0] = promoted
			}

			trace.ManualIntentUID = matchedIntent.Intent.IntentUID
			trace.GlobalFilterReasons["intent_match"] = matchedIntent.Reasons

			// 意图指定了 effort 时写入共享的 IntentEffortPin，供 EndpointPolicy 的
			// CapabilityFloor.PinnedEffort 读取。意图只指定了模型未指定 effort 时
			// Pin 保持 nil/Set=false，effort 仍由 Autopilot 自行决定。
			if matchedIntent.Intent.Effort != "" && profile.IntentEffortPin != nil {
				profile.IntentEffortPin.Effort = matchedIntent.Intent.Effort
				profile.IntentEffortPin.Set = true
			}

			log.Printf("[SmartRouter-IntentMatch] uid=%s type=%s target=%s specificity=%d effort=%s",
				matchedIntent.Intent.IntentUID, string(matchedIntent.Intent.IntentType),
				intentTargetUID, matchedIntent.Specificity, string(matchedIntent.Intent.Effort))
		}()
	}

	// 从已排序的 scoredEntries 构建 trace 候选和结果列表
	result := make([]scheduler.ChannelInfo, 0, len(scoredEntries))
	candidates := make([]RoutingCandidate, 0, len(scoredEntries))
	for _, se := range scoredEntries {
		e := se.entry
		sc := se.scored
		candidate := RoutingCandidate{
			ChannelUID:     e.ChannelUID,
			ChannelName:    e.ChannelName,
			MetricsKey:     SanitizeMetricsKey(e.MetricsKey),
			KeyMask:        e.KeyMask,
			OriginTier:     string(e.OriginTier),
			ChannelKind:    e.ChannelKind,
			HealthState:    string(e.HealthState),
			MappedModel:    e.MappedModel,
			MappingSource:  e.MappingSource,
			MappingReason:  e.MappingReason,
			TotalScore:     sc.Score,
			DomainEvidence: sc.DomainEvidence,
			Scores: []CandidateScore{
				{Dimension: "quality", Score: sc.QualityScore, Weight: weights.WQuality},
				{Dimension: "stability", Score: sc.StabilityScore, Weight: weights.WStability},
				{Dimension: "speed", Score: sc.SpeedScore, Weight: weights.WSpeed},
				{Dimension: "cost", Score: sc.CostScore, Weight: weights.WCost},
				{Dimension: "savings", Score: sc.SavingsScore, Weight: weights.WSavings},
				{Dimension: "family", Score: sc.FamilyPrefScore, Weight: weights.WFamily},
				{Dimension: "provider_quality", Score: sc.ProviderQualityScore, Weight: weights.WProviderQuality},
				{Dimension: "domain", Score: sc.DomainStrengthScore, Weight: weights.WDomain},
			},
			Selected: true,
		}
		// AFP 成本信息传递到 trace
		if e.AFPCost != nil {
			candidate.AFPEstimated = e.AFPCost.Result.TotalAFP
			candidate.AFPConfidence = e.AFPCost.Result.Confidence.String()
			if e.AFPCost.Result.PromotionApplied {
				candidate.AFPPromotion = e.AFPCost.Result.PromotionID
			}
			if e.AFPCost.Evidence.ScopeID != "" {
				candidate.AFPScope = e.AFPCost.Evidence.ScopeID
			}
		} else if config.IsVolcengineProvider(upstreamFor(scheduler.ChannelInfo{Index: e.ChannelIndex})) {
			// 火山渠道但 AFP 未生效，记录原因
			candidate.AFPBypassReason = "afp_data_unavailable"
		}
		candidates = append(candidates, candidate)

		// 匹配回 ChannelInfo：优先用上游配置的 ChannelUID，回退到 ch_%d 格式
		for _, ch := range channels {
			upstream := upstreamFor(ch)
			matchUID := fmt.Sprintf("ch_%d", ch.Index)
			if upstream != nil && upstream.ChannelUID != "" {
				matchUID = upstream.ChannelUID
			}
			if matchUID == e.ChannelUID {
				result = append(result, ch)
				break
			}
		}
	}

	// ── auto 生效：硬约束过滤 + fail-open ──
	fallbackUsed := false
	{
		filteredResult := make([]scheduler.ChannelInfo, 0, len(scoredEntries))
		for i, se := range scoredEntries {
			reasons := routingHardConstraintReasons(profile, &se.entry)

			// advisor hint 的 MinQualityTier 约束（hint 真正 Applied 时才非零值）
			if advisorMinQualityTier != "" {
				if advisorMinQualityReasons := MinQualityTierReasons(se.entry.ScoringCandidate.QualityTier, advisorMinQualityTier); len(advisorMinQualityReasons) > 0 {
					reasons = append(reasons, advisorMinQualityReasons...)
				}
			}

			if len(reasons) > 0 {
				candidates[i].Selected = false
				candidates[i].FilterReasons = reasons
				trace.GlobalFilterReasons["auto_hard_constraints"] = append(
					trace.GlobalFilterReasons["auto_hard_constraints"],
					buildFilterLabel(se.entry.ChannelName, se.entry.ChannelUID, se.entry.KeyMask)+": "+joinReasons(reasons),
				)
			} else {
				// 保留未被过滤的渠道：匹配回 ChannelInfo
				for _, ch := range channels {
					upstream := upstreamFor(ch)
					matchUID := fmt.Sprintf("ch_%d", ch.Index)
					if upstream != nil && upstream.ChannelUID != "" {
						matchUID = upstream.ChannelUID
					}
					if matchUID == se.entry.ChannelUID {
						filteredResult = append(filteredResult, ch)
						break
					}
				}
			}
		}

		if len(filteredResult) > 0 {
			result = filteredResult
		} else if len(scoredEntries) > 0 {
			// fail-open：全部被过滤时回退到重排（不删除）
			fallbackUsed = true
			trace.FallbackUsed = true
			trace.GlobalFilterReasons["auto_failopen"] = []string{
				fmt.Sprintf("所有 %d 个候选均被硬约束过滤，回退到重排模式", len(scoredEntries)),
			}
			log.Printf("[SmartRouter-HardConstraintFailOpen] mode=%s taskClass=%s 全部候选被过滤，回退到重排",
				string(mode), string(profile.TaskClass))
			// result 保持重排后的完整列表
		}

		// 人工意图效果检查：目标渠道是否通过了硬约束过滤
		if matchedIntent != nil && intentTargetUID != "" {
			targetSurvived := false
			for _, ch := range result {
				upstream := upstreamFor(ch)
				matchUID := fmt.Sprintf("ch_%d", ch.Index)
				if upstream != nil && upstream.ChannelUID != "" {
					matchUID = upstream.ChannelUID
				}
				if matchUID == intentTargetUID {
					targetSurvived = true
					break
				}
			}
			if !targetSurvived {
				// 意图目标被硬约束过滤：回退到过滤后的默认排序
				result = filteredResult
				trace.FallbackUsed = true
				trace.GlobalFilterReasons["intent_fallback"] = []string{
					fmt.Sprintf("intent=%s: target %s filtered by hard constraints, fallback to score order",
						matchedIntent.Intent.IntentUID, intentTargetUID),
				}
				matchedIntent.FallbackUsed = true
				if r.intentStore != nil {
					_ = r.intentStore.RecordFallback(matchedIntent.Intent.IntentUID)
				}
				log.Printf("[SmartRouter-IntentFallback] uid=%s target=%s filtered by hard constraints",
					matchedIntent.Intent.IntentUID, intentTargetUID)
			} else if r.intentStore != nil {
				_ = r.intentStore.RecordHit(matchedIntent.Intent.IntentUID, true, 0)
			}
		}
	}

	// 记录 trace 信息
	trace.Candidates = candidates
	// result 表示 SmartRouter 模拟/生效后的候选集合：部分硬过滤时只计
	// 通过者，全部被过滤并 fail-open 时则恢复为完整候选数。
	trace.CandidatesAfter = len(result)
	trace.SortReasons = []string{"smart_routing_score"}
	if matchedIntent != nil {
		trace.SortReasons = append(trace.SortReasons, "intent_promote")
		if matchedIntent.FallbackUsed {
			trace.SortReasons = append(trace.SortReasons, "intent_fallback")
		}
	}
	if fallbackUsed {
		trace.SortReasons = append(trace.SortReasons, "auto_failopen_reorder")
	} else {
		trace.SortReasons = append(trace.SortReasons, "auto_filter_and_reorder")
	}

	selectedCandidateIndex := -1
	for i := range candidates {
		if candidates[i].Selected {
			selectedCandidateIndex = i
			break
		}
	}
	// 全部候选被硬约束过滤时，auto 会 fail-open 到原始评分首位；trace 必须反映同一结果。
	if selectedCandidateIndex < 0 && fallbackUsed && len(candidates) > 0 {
		selectedCandidateIndex = 0
	}
	if selectedCandidateIndex >= 0 {
		selected := candidates[selectedCandidateIndex]
		trace.SelectedChannelUID = selected.ChannelUID
		trace.SelectedMetricsKey = selected.MetricsKey
		trace.SelectedOriginTier = selected.OriginTier
	}

	// 计算耗时
	trace.DurationMs = time.Since(startTime).Milliseconds()
	trace.CreatedAt = time.Now().UTC()

	// shadow/dryrun 模式：记录 shadow 建议的渠道
	if mode == RoutingModeShadow && trace.SelectedChannelUID != "" {
		trace.ShadowChannelUID = trace.SelectedChannelUID
		trace.Match = true // 先假设匹配，实际填充时更新
	}

	// 持久化 trace
	if traceStore != nil {
		traceStore.Record(trace)
		if onTraceRecorded != nil {
			onTraceRecorded(trace.TraceUID)
		}
	}

	// Phase 4 Item 8: 候选排名回调（A/B 测试用）
	// 在 trace 持久化之后、返回之前调用，确保候选数据已稳定。
	if r.onCandidatesRanked != nil && len(candidates) > 0 {
		r.onCandidatesRanked(profile.Model, profile.ChannelKind, candidates)
	}

	intentUID := ""
	if matchedIntent != nil {
		intentUID = matchedIntent.Intent.IntentUID
	}
	log.Printf("[SmartRouter-Filter] taskClass=%s mode=%s candidates=%d fallback=%v intent=%s shadow=%s duration=%dms",
		string(profile.TaskClass), string(mode), len(candidates), fallbackUsed,
		intentUID, trace.ShadowChannelUID, trace.DurationMs)

	// 返回评分过滤与重排后的候选列表
	return result, nil
}

// channelScoreEntry 渠道评分输入条目。
type channelScoreEntry struct {
	ChannelUID          string
	ChannelName         string // 渠道显示名（来自 upstream.Name）
	ChannelKind         string
	MetricsKey          string
	KeyMask             string // 掩码后的 key，如 sk-***abc
	MappedModel         string
	MappingSource       string
	MappingReason       string
	OriginTier          ChannelOriginTier
	HealthState         HealthState
	EstimatedCost       float64
	ChannelIndex        int
	ModelID             string
	DomainProfiles      []ModelProfile
	SupportsVision      bool // 渠道是否支持识图（模型注册表 + 画像聚合 + 手动配置覆盖）
	SupportsToolCalls   bool // 渠道是否支持工具调用（模型注册表 + 画像聚合）
	SupportsReasoning   bool // 渠道是否支持推理（模型注册表 + 画像聚合）
	ContextWindowTokens int  // 渠道上下文窗口大小（0 = 未知，来自模型能力注册表）
	ScoringCandidate    ScoringCandidate

	// AFP 成本信息（仅火山 Agent Plan 渠道有值）
	AFPCost *CandidateAFPCost // AFP 成本计算结果（nil = 非 AFP 渠道）

	// CompshareDeduction 优云智算套餐单次减扣倍数（0 = 非 compshare 渠道，>0 = 减扣次数）。
	// 来自 ProviderTemplate.ModelCostMultipliers；越低越省，全局可比。
	CompshareDeduction float64
}

type scoredChannelEntry struct {
	entry  channelScoreEntry
	scored ScoredCandidate
}

// sortScoredChannelEntries 统一真实路径与 dry-run 的排序语义。
// Score 为主序，OriginTier 只在同分时作为次序；稳定排序保留完全同分候选的输入顺序。
func sortScoredChannelEntries(entries []scoredChannelEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].scored.Score != entries[j].scored.Score {
			return entries[i].scored.Score > entries[j].scored.Score
		}
		return originTierRank(entries[i].entry.OriginTier) > originTierRank(entries[j].entry.OriginTier)
	})
}

func resolvedCandidateModel(requestModel, mappedModel string) string {
	if mappedModel != "" {
		return mappedModel
	}
	return requestModel
}

type channelModelResolution struct {
	ActualModel   string
	MappedModel   string
	MappingSource string
	MappingReason string
	Supported     bool
}

// resolveChannelModel 在构建渠道能力条目前解析该渠道实际承接请求的模型。
// 真实路由与 dry-run 共用此逻辑，避免拿原始 Claude/OpenAI 模型名去判断
// GLM、Kimi 等自动映射模型的工具调用、推理和上下文能力。
func (r *SmartRouter) resolveChannelModel(
	profile *RequestProfile,
	upstream *config.UpstreamConfig,
	upstreamModelCapabilities map[string]config.UpstreamModelCapability,
) channelModelResolution {
	resolution := channelModelResolution{}
	if profile == nil || upstream == nil {
		return resolution
	}

	requestModel := profile.Model
	resolution.ActualModel = requestModel
	if requestModel == "" {
		resolution.Supported = true
		return resolution
	}

	supported, _ := upstream.ExplainModelSupport(requestModel)
	hasExplicitModelRules := len(upstream.SupportedModels) > 0
	if supported && (!upstream.AutoManaged || hasExplicitModelRules) {
		resolved := config.ResolveUpstreamCapability(requestModel, upstream, upstreamModelCapabilities)
		if resolved.ActualModel != "" {
			resolution.ActualModel = resolved.ActualModel
		}
		resolution.Supported = true
		if normalizeRoutingModelID(resolution.ActualModel) != normalizeRoutingModelID(requestModel) {
			resolution.MappedModel = resolution.ActualModel
			resolution.MappingSource = "explicit_mapping"
			resolution.MappingReason = "matched configured model mapping"
		}
		return resolution
	}

	if upstream.AutoManaged && r.modelResolver != nil {
		target, found, reason := r.modelResolver.ResolveModelAnyEndpointWithFloor(
			requestModel,
			upstream.ChannelUID,
			profile.ChannelKind,
			BuildCapabilityFloorFromRequestProfile(profile),
		)
		if found && target.Model != "" {
			resolution.ActualModel = target.Model
			resolution.Supported = true
			if normalizeRoutingModelID(target.Model) != normalizeRoutingModelID(requestModel) {
				resolution.MappedModel = target.Model
				resolution.MappingSource = "auto_resolve"
				resolution.MappingReason = reason
			}
		}
	}

	return resolution
}

// buildChannelEntry 从 ChannelInfo + UpstreamConfig 构建评分输入。
// 无画像时使用中性默认值（不惩罚）。
func (r *SmartRouter) buildChannelEntry(
	ch scheduler.ChannelInfo,
	upstream *config.UpstreamConfig,
	channelKind string,
	model string,
	upstreamModelCapabilities map[string]config.UpstreamModelCapability,
) channelScoreEntry {
	channelUID := upstream.ChannelUID
	if channelUID == "" {
		channelUID = fmt.Sprintf("ch_%d", ch.Index)
	}
	if channelKind == "" {
		channelKind = string(scheduler.ChannelKindMessages)
	}
	entry := channelScoreEntry{
		ChannelUID:    channelUID,
		ChannelName:   upstream.Name,
		ChannelKind:   channelKind,
		ChannelIndex:  ch.Index,
		HealthState:   HealthStateUnknown,
		OriginTier:    OriginTierUnknown, // 无画像时默认 unknown
		EstimatedCost: -1,                // 负数表示未知，避免被误判为免费渠道
	}
	actualModel := model
	modelProvider := ""
	var modelPricing *config.ModelPricing
	if model != "" {
		// model 已由 resolveChannelModel 收敛为实际发送模型；这里仍需保留渠道显式
		// ModelMapping 的兼容路径，但动态映射场景不得回退匹配原请求模型能力。
		resolved := config.ResolveMappedUpstreamCapability(model, config.RedirectModel(model, upstream), upstream, upstreamModelCapabilities)
		actualModel = resolved.ActualModel
		if resolved.Known {
			capability := resolved.Capability
			modelProvider = capability.Provider
			modelPricing = capability.Pricing
			entry.ContextWindowTokens = capability.ContextWindowTokens
			entry.SupportsVision = capability.Capabilities["vision"]
			entry.SupportsToolCalls = capability.Capabilities["toolCalls"]
			entry.SupportsReasoning = capability.ThinkingMode != "" || len(capability.ReasoningEfforts) > 0
		}
	}
	entry.ModelID = actualModel
	if modelPricing != nil {
		multiplier := 1.0
		pricingProviderID := strings.TrimSpace(upstream.ProviderID)
		if pricingProviderID == "" {
			pricingProviderID, _ = config.InferProviderIDFromBaseURL(upstream.BaseURL)
		}
		if pricingProviderID == "" {
			for _, baseURL := range upstream.BaseURLs {
				if inferred, ok := config.InferProviderIDFromBaseURL(baseURL); ok {
					pricingProviderID = inferred
					break
				}
			}
		}
		if r.configManager != nil && pricingProviderID != "" {
			multiplier = r.configManager.GetAutopilotRouting().CostOptimization.ProviderTimePricingMultiplier(pricingProviderID, r.currentTime())
		}
		// 使用各类 token 每百万的参考成本做候选间归一化，时段倍率统一作用于全部计费项。
		entry.EstimatedCost = metrics.CalculateTokenCostUSDWithPricing(modelPricing, 1_000_000, 1_000_000, 1_000_000, 1_000_000) * multiplier
	}
	modelFamily := InferModelFamily(actualModel, modelProvider)
	visionDisabled := upstream.NoVision || containsString(upstream.NoVisionModels, actualModel)
	if visionDisabled {
		entry.SupportsVision = false
	}

	// 从 ProfileStore 读取画像
	if r.profileStore != nil {
		profiles := r.profileStore.ListActiveByChannel(channelUID)
		matchingProfiles := make([]*KeyEndpointProfile, 0, len(profiles))
		for _, profile := range profiles {
			if profile != nil && profile.ChannelKind == entry.ChannelKind {
				matchingProfiles = append(matchingProfiles, profile)
			}
		}
		if len(matchingProfiles) > 0 {
			// 解引用指针切片
			profileValues := make([]KeyEndpointProfile, len(matchingProfiles))
			for i, p := range matchingProfiles {
				profileValues[i] = *p
			}
			agg := AggregateChannelProfile(channelUID, ch.Index, entry.ChannelKind, profileValues)
			entry.HealthState = agg.HealthState
			entry.OriginTier = ChannelOriginTier(agg.OriginTier)
			entry.MetricsKey = matchingProfiles[0].MetricsKey
			entry.KeyMask = matchingProfiles[0].KeyMask
			// 注册表与画像都是正向能力证据；手动禁用视觉始终优先。
			if !visionDisabled {
				entry.SupportsVision = entry.SupportsVision || agg.SupportsVision
			}
			entry.SupportsToolCalls = entry.SupportsToolCalls || agg.SupportsToolCalls
			entry.SupportsReasoning = entry.SupportsReasoning || agg.SupportsReasoning

			entry.ScoringCandidate = ScoringCandidate{
				ChannelUID:                channelUID,
				QualityTier:               agg.QualityTier,
				StabilityTier:             agg.StabilityTier,
				SpeedTier:                 agg.SpeedTier,
				CostTier:                  agg.CostTier,
				HealthState:               agg.HealthState,
				ProviderQualityScore:      0.5,
				ProviderQualityConfidence: 0.3,
				ModelFamily:               modelFamily,
				SavingsScore:              0.5,
				DomainStrengthScore:       0.5,
			}
			r.applyModelQualityTier(&entry)
			r.attachDomainProfiles(&entry, modelProvider)
			return entry
		}
	}

	// 无画像：中性默认值，不惩罚
	entry.ScoringCandidate = ScoringCandidate{
		ChannelUID:                channelUID,
		QualityTier:               QualityTierNormal,
		StabilityTier:             StabilityTierNormal,
		SpeedTier:                 SpeedTierNormal,
		CostTier:                  CostTierNormal,
		HealthState:               HealthStateUnknown,
		ProviderQualityScore:      0.5,
		ProviderQualityConfidence: 0.3,
		ModelFamily:               modelFamily,
		SavingsScore:              0.5,
		DomainStrengthScore:       0.5,
	}
	r.applyModelQualityTier(&entry)
	r.attachDomainProfiles(&entry, modelProvider)
	return entry
}

// applyModelQualityTier 用实际映射模型的质量档覆盖渠道聚合档位。
// 一个渠道可能同时挂载 K3 和 kimi-for-coding；只使用渠道最佳 endpoint
// 的聚合档位会把前者的 Premium 错投给后者，导致轻量/worker 请求持续选择 K3。
// 优先使用精确模型画像；自动发现的旧画像若模型族尚未补齐，则回退到当前
// 模型注册表推导；只有发生实际映射时才用注册表结果覆盖聚合档位。
func (r *SmartRouter) applyModelQualityTier(entry *channelScoreEntry) {
	if entry == nil || entry.ModelID == "" {
		return
	}

	quality := QualityTier("")
	if r.modelProfileStore != nil && entry.ChannelUID != "" {
		for _, profile := range r.modelProfileStore.ListActiveByChannel(entry.ChannelUID) {
			if profile.ChannelKind != entry.ChannelKind ||
				!strings.EqualFold(profile.ModelID, entry.ModelID) || profile.QualityTier == "" {
				continue
			}
			// auto_discovery 旧版本可能把 K3 写成 unknown/low；注册表是这类
			// 模型的能力事实源，避免陈旧画像继续把 K3 当成低档模型。
			if profile.Source == "auto_discovery" {
				family := InferModelFamily(profile.ModelID, "")
				if family != ModelFamilyUnknown {
					profile.QualityTier = ModelProfileQualityTierFromFamily(family, profile.ModelID)
				}
			}
			if quality == "" || qualityTierRank(profile.QualityTier) > qualityTierRank(quality) {
				quality = profile.QualityTier
			}
		}
	}
	if quality == "" && entry.MappedModel != "" {
		modelFamily := entry.ScoringCandidate.ModelFamily
		if modelFamily != ModelFamilyUnknown && modelFamily != "" {
			quality = ModelProfileQualityTierFromFamily(modelFamily, entry.ModelID)
		}
	}
	if quality != "" {
		entry.ScoringCandidate.QualityTier = quality
	}
}

func (r *SmartRouter) currentTime() time.Time {
	if r != nil && r.now != nil {
		return r.now()
	}
	return time.Now()
}

// resolveChannelAFPScope 解析单个火山渠道的套餐作用域。
// 仅 volcengine/volc-ark 渠道解析，其余返回 nil。用渠道首个可用 Key 作代表凭证：
// 同 channel 多 Key 通常共享账号/套餐，首 Key 即可定位 scope；不同账号的边界以首 Key 近似。
// ConfigManager 缺失或凭证未托管时 scope.AFPComparable=false，调用方自然回退 USD。
func (r *SmartRouter) resolveChannelAFPScope(upstream *config.UpstreamConfig) *config.VolcenginePlanScope {
	if r == nil || r.configManager == nil || upstream == nil {
		return nil
	}
	if !config.IsVolcengineProvider(upstream) {
		return nil
	}
	if len(upstream.APIKeys) == 0 {
		return nil
	}
	scope := config.ResolveVolcenginePlanScopeFromUpstream(r.configManager, upstream, upstream.APIKeys[0])
	return &scope
}

// resolveCompshareDeduction 返回优云智算渠道指定模型的单次减扣次数。
// 非 compshare 或模板未收录该模型时返回 0；模板来自内置 ProviderTemplate.ModelCostMultipliers。
func (r *SmartRouter) resolveCompshareDeduction(upstream *config.UpstreamConfig, modelID string) float64 {
	if r == nil || upstream == nil || modelID == "" {
		return 0
	}
	if upstream.ProviderID != "compshare" {
		return 0
	}
	tmpl, ok := config.GetProviderTemplate("compshare")
	if !ok {
		return 0
	}
	mult, ok := tmpl.ModelCostMultiplierForModel(modelID)
	if !ok {
		return 0
	}
	return mult
}

// applyAFPCosts 在评分前为火山 Agent Plan 渠道填充 AFP 成本证据，为优云智算渠道填充减扣次数。
// 仅当 AFP 路由开启时执行；非火山/优云智算或非可比 scope 的渠道保持原状，回退 USD EstimatedCost。
// inputTokens 取请求估算输入；outputTokens 暂用 0（confidence=Estimated），
// 同 scope 内相对排序由 promotion 倍率/减扣次数决定，与输出 token 绝对值无关。
func (r *SmartRouter) applyAFPCosts(entries []channelScoreEntry, inputTokens int) {
	if r == nil || !r.IsAFPCostRoutingEnabled() {
		return
	}
	at := r.currentTime().Unix()
	for i := range entries {
		e := &entries[i]
		if e.ModelID == "" {
			continue
		}
		upstream := r.upstreamByChannelUID(e.ChannelUID, e.ChannelKind)
		if upstream == nil {
			continue
		}
		// 火山 Agent Plan AFP
		if scope := r.resolveChannelAFPScope(upstream); scope != nil {
			e.AFPCost = ComputeCandidateAFPCostWithScope(at, scope, e.ModelID, inputTokens, 0)
		}
		// 优云智算 compshare 单次减扣
		if ded := r.resolveCompshareDeduction(upstream, e.ModelID); ded > 0 {
			e.CompshareDeduction = ded
		}
	}
}

// upstreamByChannelUID 通过 ChannelUID 反查 UpstreamConfig。
// buildChannelEntry 只在执行期持有了 upstream，但 applyAFPCosts 在 entries 构建后统一执行，
// 需要重新定位 upstream 以解析火山 scope。
func (r *SmartRouter) upstreamByChannelUID(channelUID, channelKind string) *config.UpstreamConfig {
	if r == nil || r.configManager == nil || channelUID == "" {
		return nil
	}
	cfg := r.configManager.GetConfig()
	kind := scheduler.ChannelKindMessages
	if channelKind != "" {
		kind = scheduler.ChannelKind(channelKind)
	}
	var upstreams []config.UpstreamConfig
	switch kind {
	case scheduler.ChannelKindResponses:
		upstreams = cfg.ResponsesUpstream
	case scheduler.ChannelKindGemini:
		upstreams = cfg.GeminiUpstream
	case scheduler.ChannelKindChat:
		upstreams = cfg.ChatUpstream
	case scheduler.ChannelKindImages:
		upstreams = cfg.ImagesUpstream
	case scheduler.ChannelKindVectors:
		upstreams = cfg.VectorsUpstream
	default:
		upstreams = cfg.Upstream
	}
	for i := range upstreams {
		u := &upstreams[i]
		uid := u.ChannelUID
		if uid == "" {
			uid = fmt.Sprintf("ch_%d", i)
		}
		if uid == channelUID {
			return u
		}
	}
	return nil
}

// normalizeSavingsScoreGrouped 按成本可比性分组归一化省钱分（§5.6 AFP/Compshare 适配）。
// AFP 候选按 ScopeID 分组组内归一化（用 TotalAFP）；compshare 候选按减扣次数组内归一化；
// USD 候选（有有效 EstimatedCost>=0 且无折扣成本）组内归一化；无成本候选得 0.5。
// 各成本类型不可比，各自独立归一化，使各自组内最便宜者各得 1.0。
func normalizeSavingsScoreGrouped(entries []channelScoreEntry) map[string]float64 {
	result := make(map[string]float64, len(entries))
	if len(entries) == 0 {
		return result
	}

	// AFP 分组：scopeID -> (uid -> TotalAFP)
	afpGroups := make(map[string]map[string]float64)
	// compshare 组：uid -> 减扣次数
	compshareCosts := make(map[string]float64)
	// USD 组：uid -> EstimatedCost
	usdCosts := make(map[string]float64)

	for _, e := range entries {
		if e.CompshareDeduction > 0 {
			compshareCosts[e.ChannelUID] = e.CompshareDeduction
		} else if e.AFPCost != nil && e.AFPCost.Evidence.Unit == CostUnitAFP && e.AFPCost.Evidence.ScopeID != "" {
			scopeID := e.AFPCost.Evidence.ScopeID
			if afpGroups[scopeID] == nil {
				afpGroups[scopeID] = make(map[string]float64)
			}
			afpGroups[scopeID][e.ChannelUID] = float64(e.AFPCost.Evidence.Estimated)
		} else if e.EstimatedCost >= 0 {
			usdCosts[e.ChannelUID] = e.EstimatedCost
		} else {
			// 无成本证据：中性
			result[e.ChannelUID] = 0.5
		}
	}

	// 每个 AFP scope 组内归一化
	for _, costs := range afpGroups {
		savings := normalizeCostGroup(costs)
		for uid, s := range savings {
			result[uid] = s
		}
	}
	// compshare 组归一化
	if len(compshareCosts) > 0 {
		savings := normalizeCostGroup(compshareCosts)
		for uid, s := range savings {
			result[uid] = s
		}
	}
	// USD 组归一化
	if len(usdCosts) > 0 {
		savings := normalizeCostGroup(usdCosts)
		for uid, s := range savings {
			result[uid] = s
		}
	}
	return result
}

// normalizeCostGroup 在单一可比组内做 min/max 归一化：最便宜得 1.0，最贵得 0.0，
// 全部相同得 0.5。与 NormalizeSavingsScore 语义一致，但作用于已分组的子集。
func normalizeCostGroup(costs map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(costs))
	if len(costs) == 0 {
		return result
	}
	minCost, maxCost := -1.0, -1.0
	for _, c := range costs {
		if c < 0 {
			continue
		}
		if minCost < 0 || c < minCost {
			minCost = c
		}
		if maxCost < 0 || c > maxCost {
			maxCost = c
		}
	}
	if maxCost <= minCost {
		for uid := range costs {
			result[uid] = 0.5
		}
		return result
	}
	diff := maxCost - minCost
	for uid, c := range costs {
		if c < 0 {
			result[uid] = 0.5
			continue
		}
		result[uid] = 1.0 - (c-minCost)/diff
	}
	return result
}

func (r *SmartRouter) attachDomainProfiles(entry *channelScoreEntry, provider string) {
	if entry == nil {
		return
	}
	if r.modelProfileStore != nil && entry.ChannelUID != "" && entry.ModelID != "" {
		for _, profile := range r.modelProfileStore.ListActiveByChannel(entry.ChannelUID) {
			if profile.ChannelKind != entry.ChannelKind || !strings.EqualFold(profile.ModelID, entry.ModelID) {
				continue
			}
			if profile.ModelFamily == ModelFamilyUnknown || profile.ModelFamily == "" {
				profile.ModelFamily = InferModelFamily(profile.ModelID, provider)
			}
			entry.DomainProfiles = append(entry.DomainProfiles, profile)
		}
		sort.SliceStable(entry.DomainProfiles, func(i, j int) bool {
			if entry.DomainProfiles[i].MetricsKey != entry.DomainProfiles[j].MetricsKey {
				return entry.DomainProfiles[i].MetricsKey < entry.DomainProfiles[j].MetricsKey
			}
			return entry.DomainProfiles[i].UpdatedAt.Before(entry.DomainProfiles[j].UpdatedAt)
		})
	}
	if len(entry.DomainProfiles) == 0 {
		entry.DomainProfiles = []ModelProfile{{
			ChannelUID:  entry.ChannelUID,
			ChannelKind: entry.ChannelKind,
			ModelID:     entry.ModelID,
			ModelFamily: InferModelFamily(entry.ModelID, provider),
		}}
	}
}

func applyDomainStrength(entry *channelScoreEntry, domain TaskDomain) {
	if entry == nil {
		return
	}
	profiles := entry.DomainProfiles
	if len(profiles) == 0 {
		profiles = []ModelProfile{{ModelID: entry.ModelID, ModelFamily: entry.ScoringCandidate.ModelFamily}}
	}

	selected := profiles[0]
	best := ResolveDomainStrength(&selected, domain)
	for i := 1; i < len(profiles); i++ {
		candidate := ResolveDomainStrength(&profiles[i], domain)
		if candidate.Score > best.Score ||
			(candidate.Score == best.Score && candidate.EvidenceConfidence > best.EvidenceConfidence) {
			selected = profiles[i]
			best = candidate
		}
	}

	entry.ScoringCandidate.DomainStrengthScore = best.Score
	evidence := best
	entry.ScoringCandidate.DomainEvidence = &evidence
	if selected.ModelFamily != "" && selected.ModelFamily != ModelFamilyUnknown {
		entry.ScoringCandidate.ModelFamily = selected.ModelFamily
	}
	if selected.ProviderQualityConfidence > 0 {
		entry.ScoringCandidate.ProviderQualityScore = selected.ProviderQualityScore
		entry.ScoringCandidate.ProviderQualityConfidence = selected.ProviderQualityConfidence
	}
}

// routingHardConstraintReasons 检查自动路由硬约束，返回不满足的原因列表。
// auto 模式据此过滤真实候选；shadow/dry-run 模式仅据此生成模拟 trace。
// 空列表表示该渠道满足所有硬约束。
// 当前硬约束（逐批扩展）：
//   - vision 请求但渠道不支持识图
//   - CapabilityFloor：请求需要推理但渠道不支持（画像数据可用时）
//   - CapabilityFloor：请求需要工具调用但渠道不支持
//   - CapabilityFloor：上下文窗口需求大于渠道容量
func routingHardConstraintReasons(profile *RequestProfile, entry *channelScoreEntry) []string {
	var reasons []string

	// 识图硬约束
	if profile.VisionNeed && !entry.SupportsVision {
		reasons = append(reasons, "vision_unsupported")
	}

	// CapabilityFloor 三项硬约束（工具调用、推理、上下文窗口）
	reasons = append(reasons, CapabilityFloorReasons(CandidateCapabilities{
		SupportsToolCalls:   entry.SupportsToolCalls,
		SupportsReasoning:   entry.SupportsReasoning,
		ContextWindowTokens: entry.ContextWindowTokens,
	}, profile)...)

	return reasons
}

// buildFilterLabel 构造过滤原因的渠道标签，优先使用渠道名，回退到 ChannelUID。
// 格式：渠道名 (key掩码) 或 ch_xxx
func buildFilterLabel(name, uid, keyMask string) string {
	if name == "" {
		name = uid
	}
	if keyMask != "" {
		return name + " (" + keyMask + ")"
	}
	return name
}

// joinReasons 将原因列表拼接为逗号分隔字符串。
func joinReasons(reasons []string) string {
	if len(reasons) == 0 {
		return ""
	}
	result := reasons[0]
	for _, r := range reasons[1:] {
		result += "," + r
	}
	return result
}

// classifyTokenBucket 将估算 token 数映射到 AdvisorInput 所需的分桶字符串。
// 遵循 AdvisorInput 白名单：<1k | 1-10k | 10-50k | 50k+
func classifyTokenBucket(estTokens int) string {
	if estTokens >= 50000 {
		return "50k+"
	}
	if estTokens >= 10000 {
		return "10-50k"
	}
	if estTokens >= 1000 {
		return "1-10k"
	}
	return "<1k"
}

// collectChannelEntries 收集指定请求的 dry-run 渠道条目。
// 对不直接支持请求模型的 autoManaged 渠道，可通过 ModelResolver 增加只读预览候选；
// 该函数不参与真实 scheduler，因而不会改变 shadow 的实际候选集合。
func (r *SmartRouter) collectChannelEntries(profile *RequestProfile) []channelScoreEntry {
	if profile == nil {
		return nil
	}
	channelKind := profile.ChannelKind
	model := profile.Model
	cfg := r.configManager.GetConfig()
	var upstreams []config.UpstreamConfig
	switch channelKind {
	case "responses":
		upstreams = cfg.ResponsesUpstream
	case "gemini":
		upstreams = cfg.GeminiUpstream
	case "chat":
		upstreams = cfg.ChatUpstream
	case "images":
		upstreams = cfg.ImagesUpstream
	case "vectors":
		upstreams = cfg.VectorsUpstream
	default:
		upstreams = cfg.Upstream
	}

	entries := make([]channelScoreEntry, 0, len(upstreams))
	for i, upstream := range upstreams {
		status := upstream.Status
		if status == "" {
			status = "active"
		}
		// 与真实 CandidateFilter 的配置层候选条件一致；运行时 cooldown/熔断
		// 由 scheduler 诊断接口负责，BuildPlan 不持有对应运行态。
		if status != "active" || len(upstream.APIKeys) == 0 {
			continue
		}
		modelResolution := r.resolveChannelModel(profile, &upstream, cfg.UpstreamModelCapabilities)
		if model != "" && !modelResolution.Supported {
			continue
		}
		ch := scheduler.ChannelInfo{
			Index:    i,
			Name:     upstream.Name,
			Priority: upstream.Priority,
			Status:   status,
		}
		entry := r.buildChannelEntry(ch, &upstream, channelKind, modelResolution.ActualModel, cfg.UpstreamModelCapabilities)
		entry.MappedModel = modelResolution.MappedModel
		entry.MappingSource = modelResolution.MappingSource
		if entry.MappingSource == "auto_resolve" {
			entry.MappingSource = "auto_resolve_preview"
		}
		entry.MappingReason = modelResolution.MappingReason
		r.applyModelQualityTier(&entry)
		entries = append(entries, entry)
	}
	return entries
}

// loadFamilyPrefs 从配置加载派系偏好。
func (r *SmartRouter) loadFamilyPrefs(cfg config.ModelFamilyPreferenceConfig) []ModelFamily {
	if !cfg.Enabled || len(cfg.GlobalOrder) == 0 {
		return nil
	}
	prefs := make([]ModelFamily, 0, len(cfg.GlobalOrder))
	for _, f := range cfg.GlobalOrder {
		prefs = append(prefs, ModelFamily(f))
	}
	return prefs
}

// UpdateActualChannel 供调度完成后按 TraceUID 回填真实尝试渠道。
// shadow trace 同时计算推荐与实际是否一致。
func (r *SmartRouter) UpdateActualChannel(traceUID, actualChannelUID string) {
	if r.traceStore == nil || traceUID == "" || actualChannelUID == "" {
		return
	}
	if err := r.traceStore.UpdateActualChannel(traceUID, actualChannelUID); err != nil {
		log.Printf("[SmartRouter-Update] 警告: trace=%s 真实渠道回填失败: %v", traceUID, err)
	}
}
