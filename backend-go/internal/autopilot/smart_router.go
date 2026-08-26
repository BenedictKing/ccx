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
	"github.com/BenedictKing/ccx/internal/routingref"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

// ── SmartRouter（设计 §4.6 + §4.6.3 + §4.6.5 + P0.4 + P0.5）──

// RoutingPlanCandidate 是 dry-run 候选，保留评分明细并附加自动路由约束结果。
// 匿名嵌入保持既有 channelUid/score 等 JSON 字段不变。
type RoutingPlanCandidate struct {
	ScoredCandidate
	Selected           bool     `json:"selected"`
	FilterReasons      []string `json:"filterReasons,omitempty"`
	MappedModel        string   `json:"mappedModel,omitempty"`
	MappingSource      string   `json:"mappingSource,omitempty"`
	MappingReason      string   `json:"mappingReason,omitempty"`
	CandidateKey       string   `json:"candidateKey,omitempty"` // (渠道, 模型) 粒度标识：channelUID|model
	ChannelName        string   `json:"channelName,omitempty"`
	KeyMask            string   `json:"keyMask,omitempty"`
	LogicalChannelUID  string   `json:"logicalChannelUid,omitempty"`
	LogicalChannelName string   `json:"logicalChannelName,omitempty"`
}

// RoutingPlanLogicalGroup 是 dry-run 候选在 LogicalChannel 维度的聚合视图（Phase A.3）。
// 仅用于诊断展示，不参与真实调度；组内成员沿用扁平候选列表的分数降序。
type RoutingPlanLogicalGroup struct {
	LogicalChannelUID  string   `json:"logicalChannelUid,omitempty"`
	LogicalChannelName string   `json:"logicalChannelName,omitempty"`
	ChannelUIDs        []string `json:"channelUids"`    // 组内物理候选 UID（分数降序）
	BestChannelUID     string   `json:"bestChannelUid"` // 组内首个（最高分）候选
	BestScore          float64  `json:"bestScore"`
	SelectedCount      int      `json:"selectedCount"` // 组内通过硬约束的候选数
	TotalCount         int      `json:"totalCount"`
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
	// LogicalGroups 是候选按 LogicalChannel 聚合的诊断视图（Phase A.3）。
	// 仅在 LogicalChannelIdentityEnabled 开启且候选带 logical 身份时填充。
	LogicalGroups []RoutingPlanLogicalGroup `json:"logicalGroups,omitempty"`
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
	subscriptionStore *SubscriptionStore     // 订阅账务快照（nil = effective cost 不可用）
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

// isLogicalChannelIdentityEnabled 返回是否应在候选/trace/dry-run 中透传 LogicalChannel 身份。
func (r *SmartRouter) isLogicalChannelIdentityEnabled() bool {
	if r == nil || r.configManager == nil {
		return true
	}
	return r.configManager.GetAutopilotRouting().IsLogicalChannelIdentityEnabled()
}

// SetSubscriptionStore 注入订阅账务快照，用于统一 effective USD 成本解析。
func (r *SmartRouter) SetSubscriptionStore(store *SubscriptionStore) {
	if r != nil {
		r.subscriptionStore = store
	}
}

// ConfigManager 返回内部 ConfigManager 引用。
func (r *SmartRouter) ConfigManager() *config.ConfigManager {
	return r.configManager
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

	// 获取权重，解析生效的成本偏好模式（与 ModelResolver 一致）：
	// 请求头/场景预设 > PerTaskClass > 全局 Mode。
	effectiveMode := effectiveCostPreferenceForProfile(profile)
	if effectiveMode == "" {
		effectiveMode = autopilotCfg.CostPreference.GetEffectiveCostPreferenceMode(string(profile.TaskClass))
	}
	weights := DefaultTaskWeights()[profile.TaskClass]
	weights = ApplyCostPreference(weights, CostPreferenceMode(effectiveMode))

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
		plan := &RoutingPlan{
			RequestProfile: profile,
			Candidates:     nil,
			Mode:           RoutingModeDryRun,
			Weights:        weights,
		}
		r.recordDryRunTrace(plan, 0, 0)
		return plan
	}

	costs := make(map[string]float64, len(entries))
	for _, e := range entries {
		savingsKey := e.CandidateKey
		if savingsKey == "" {
			savingsKey = e.ChannelUID
		}
		costs[savingsKey] = e.EstimatedCost
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
		savingsKey := e.CandidateKey
		if savingsKey == "" {
			savingsKey = e.ChannelUID
		}
		e.ScoringCandidate.SavingsScore = savingsMap[savingsKey]
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
			ScoredCandidate:    se.scored,
			Selected:           len(reasons) == 0,
			FilterReasons:      reasons,
			MappedModel:        se.entry.MappedModel,
			MappingSource:      se.entry.MappingSource,
			MappingReason:      se.entry.MappingReason,
			CandidateKey:       se.entry.CandidateKey,
			ChannelName:        se.entry.ChannelName,
			KeyMask:            se.entry.KeyMask,
			LogicalChannelUID:  se.entry.LogicalChannelUID,
			LogicalChannelName: se.entry.LogicalChannelName,
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
	if r.isLogicalChannelIdentityEnabled() {
		plan.LogicalGroups = groupCandidatesByLogical(candidates)
	}

	r.recordDryRunTrace(plan, len(entries), len(selectedCandidates))

	return plan
}

// groupCandidatesByLogical 把 dry-run 扁平候选按 LogicalChannel 聚合为分组视图（Phase A.3）。
// 分组键：LogicalChannelUID 非空则用它，否则回退到 ChannelUID（独立物理渠道成单例组）。
// 组按首次出现顺序排列（= 输入候选的分数降序），确定性稳定。
// 当没有任何候选带 LogicalChannelUID 时返回 nil（分组退化为纯单例，无展示价值）。
func groupCandidatesByLogical(candidates []RoutingPlanCandidate) []RoutingPlanLogicalGroup {
	if len(candidates) == 0 {
		return nil
	}
	hasLogical := false
	groups := make([]RoutingPlanLogicalGroup, 0, len(candidates))
	indexByKey := make(map[string]int, len(candidates))
	for _, c := range candidates {
		key := c.LogicalChannelUID
		if key == "" {
			key = c.ChannelUID
		} else {
			hasLogical = true
		}
		if idx, ok := indexByKey[key]; ok {
			g := &groups[idx]
			g.ChannelUIDs = append(g.ChannelUIDs, c.ChannelUID)
			g.TotalCount++
			if c.Selected {
				g.SelectedCount++
			}
			if g.LogicalChannelName == "" && c.LogicalChannelName != "" {
				g.LogicalChannelName = c.LogicalChannelName
			}
			continue
		}
		indexByKey[key] = len(groups)
		selected := 0
		if c.Selected {
			selected = 1
		}
		groups = append(groups, RoutingPlanLogicalGroup{
			LogicalChannelUID:  c.LogicalChannelUID,
			LogicalChannelName: c.LogicalChannelName,
			ChannelUIDs:        []string{c.ChannelUID},
			BestChannelUID:     c.ChannelUID,
			BestScore:          c.Score,
			SelectedCount:      selected,
			TotalCount:         1,
		})
	}
	if !hasLogical {
		return nil
	}
	return groups
}

// recordDryRunTrace 将 BuildPlan 结果持久化为 schema v2 dry-run trace。
func (r *SmartRouter) recordDryRunTrace(plan *RoutingPlan, candidatesBefore, candidatesAfter int) {
	if r.traceStore == nil || plan == nil || plan.RequestProfile == nil {
		return
	}

	traceCandidates := make([]RoutingCandidate, 0, len(plan.Candidates))
	for _, candidate := range plan.Candidates {
		traceCandidates = append(traceCandidates, RoutingCandidate{
			ChannelUID:         candidate.ChannelUID,
			ChannelName:        candidate.ChannelName,
			KeyMask:            candidate.KeyMask,
			MappedModel:        candidate.MappedModel,
			MappingSource:      candidate.MappingSource,
			MappingReason:      candidate.MappingReason,
			TotalScore:         candidate.Score,
			Selected:           candidate.Selected,
			FilterReasons:      candidate.FilterReasons,
			LogicalChannelUID:  candidate.LogicalChannelUID,
			LogicalChannelName: candidate.LogicalChannelName,
		})
	}

	profile := plan.RequestProfile
	r.traceStore.Record(&RoutingDecisionTrace{
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
		CandidatesBefore:   candidatesBefore,
		CandidatesAfter:    candidatesAfter,
		SelectedChannelUID: plan.SelectedChannelUID,
		FallbackUsed:       plan.FallbackUsed,
		SortReasons:        plan.SortReasons,
	})
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

	// 获取权重，解析生效的成本偏好模式（与 ModelResolver 一致）：
	// 请求头/场景预设 > PerTaskClass > 全局 Mode。
	effectiveMode := effectiveCostPreferenceForProfile(profile)
	if effectiveMode == "" {
		effectiveMode = autopilotCfg.CostPreference.GetEffectiveCostPreferenceMode(string(profile.TaskClass))
	}
	weights := DefaultTaskWeights()[profile.TaskClass]
	weights = ApplyCostPreference(weights, CostPreferenceMode(effectiveMode))
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
			trace.GlobalFilterReasons["candidate_pre_filter"] = append(
				trace.GlobalFilterReasons["candidate_pre_filter"],
				ch.Name+": missing_upstream",
			)
			continue
		}
		// P1.5：按 channel 禁用——命中的渠道对 autopilot 不存在，走和
		// "候选不可用" 完全相同的跳过路径，不影响其他非 autopilot 选路径。
		if disabledChannelUIDs[upstream.ChannelUID] {
			trace.GlobalFilterReasons["candidate_pre_filter"] = append(
				trace.GlobalFilterReasons["candidate_pre_filter"],
				ch.Name+": disabled_channel",
			)
			continue
		}
		if !candidateAvailable(ch, upstream) {
			trace.GlobalFilterReasons["candidate_pre_filter"] = append(
				trace.GlobalFilterReasons["candidate_pre_filter"],
				ch.Name+": candidate_unavailable",
			)
			continue
		}
		route := federatedRoute(ch, profile.ChannelKind)
		executionKind := route.Kind
		executionProfile := *profile
		executionProfile.ChannelKind = executionKind
		channelUID := upstream.ChannelUID
		if channelUID == "" {
			channelUID = fmt.Sprintf("ch_%d", ch.Index)
		}
		modelResolutions := r.resolveChannelModels(&executionProfile, upstream, upstreamModelCapabilities)
		// 协议联邦覆盖：ActualModel 非空时短路为单行，避免破坏 sibling execution protocol 路径。
		if ch.ActualModel != "" {
			modelResolutions = []channelModelResolution{{
				ActualModel:   ch.ActualModel,
				Supported:     true,
				CandidateKey:  routingCandidateKey(channelUID, ch.ActualModel),
				MappedModel:   ch.ActualModel,
				MappingSource: "protocol_federation",
				MappingReason: "resolved for sibling execution protocol",
			}}
			if normalizeRoutingModelID(ch.ActualModel) == normalizeRoutingModelID(profile.Model) {
				modelResolutions[0].MappedModel = ""
				modelResolutions[0].MappingSource = ""
				modelResolutions[0].MappingReason = ""
			}
		}
		for _, modelResolution := range modelResolutions {
			entry := r.buildChannelEntry(
				ch,
				upstream,
				executionKind,
				modelResolution.ActualModel,
				upstreamModelCapabilities,
			)
			entry.Route = route
			entry.CandidateKey = modelResolution.CandidateKey
			if entry.CandidateKey == "" {
				entry.CandidateKey = routingCandidateKey(channelUID, modelResolution.ActualModel)
			}
			// 同名承接（MappingSource 为空）时 MappedModel 保持空：映射质量档折算
			// (applyModelQualityTier) 依赖 MappedModel 判空，填入请求模型名会把同名承接
			// 误判为映射模型并抬高其质量档。模型名展示由 CandidateKey + 前端回退负责。
			entry.MappedModel = modelResolution.MappedModel
			entry.MappingSource = modelResolution.MappingSource
			entry.MappingReason = modelResolution.MappingReason
			entry.ProtocolFidelity = ch.ProtocolFidelity
			entry.ConversionPenalty = ch.ConversionPenalty
			r.applyModelQualityTier(&entry)
			entries = append(entries, entry)
			costMap[entry.CandidateKey] = entry.EstimatedCost
		}
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
		// 按 (渠道, 模型) 粒度取 savings 分；CandidateKey 为空（本地候选等）回退渠道级。
		savingsKey := e.CandidateKey
		if savingsKey == "" {
			savingsKey = e.ChannelUID
		}
		e.ScoringCandidate.SavingsScore = savingsMap[savingsKey]
		applyDomainStrength(&e, scoringCtx.TaskDomain)
		scored := ScoreCandidate(e.ScoringCandidate, scoringCtx)
		if e.ProtocolFidelity == "converted" && e.ConversionPenalty > 0 {
			scored.Score -= e.ConversionPenalty
			scored.Penalty += e.ConversionPenalty
		}
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
	// 同一渠道可展开多个 (渠道, 模型) 行；result 是渠道级 failover 排序，须按路由键去重。
	seenRouteKeys := make(map[routingref.Key]bool, len(scoredEntries))
	for _, se := range scoredEntries {
		e := se.entry
		sc := se.scored
		candidate := RoutingCandidate{
			ChannelUID:         e.ChannelUID,
			ChannelName:        e.ChannelName,
			CandidateKey:       e.CandidateKey,
			ExecutionKind:      e.ChannelKind,
			ProtocolFidelity:   e.ProtocolFidelity,
			ConversionPenalty:  e.ConversionPenalty,
			MetricsKey:         SanitizeMetricsKey(e.MetricsKey),
			KeyMask:            e.KeyMask,
			OriginTier:         string(e.OriginTier),
			HealthState:        string(e.HealthState),
			MappedModel:        e.MappedModel,
			MappingSource:      e.MappingSource,
			MappingReason:      e.MappingReason,
			LogicalChannelUID:  e.LogicalChannelUID,
			LogicalChannelName: e.LogicalChannelName,
			TotalScore:         sc.Score,
			DomainEvidence:     sc.DomainEvidence,
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

		// 匹配回 ChannelInfo：优先用上游配置的 ChannelUID，回退到 ch_%d 格式。
		// 按路由键去重：同渠道的多个模型行只贡献一个 failover 槽位（首个=最高分模型行）。
		if !seenRouteKeys[e.Route.Key()] {
			for _, ch := range channels {
				if federatedRoute(ch, profile.ChannelKind).Key() == e.Route.Key() {
					result = append(result, ch)
					seenRouteKeys[e.Route.Key()] = true
					break
				}
			}
		}
	}

	// ── auto 生效：硬约束过滤 + fail-open ──
	fallbackUsed := false
	{
		filteredResult := make([]scheduler.ChannelInfo, 0, len(scoredEntries))
		seenFilteredRouteKeys := make(map[routingref.Key]bool, len(scoredEntries))
		for i, se := range scoredEntries {
			reasons := routingHardConstraintReasons(profile, &se.entry)

			// advisor hint 的 MinQualityTier 约束（hint 真正 Applied 时才非零值）；
			// 行粒度为 (渠道, 模型)，与模型级过滤同样允许 effort 级实测分豁免
			if advisorMinQualityTier != "" {
				if advisorMinQualityReasons := MinQualityTierReasons(se.entry.ScoringCandidate.QualityTier, advisorMinQualityTier); len(advisorMinQualityReasons) > 0 &&
					!effortLevelQualityAdmission(se.entry.ModelID, advisorMinQualityTier) {
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
				// 保留未被过滤的渠道：匹配回 ChannelInfo。按路由键去重，
				// 同渠道一个模型行通过即保留该渠道（取首个通过行=该渠道最高分存活模型）。
				if !seenFilteredRouteKeys[se.entry.Route.Key()] {
					for _, ch := range channels {
						if federatedRoute(ch, profile.ChannelKind).Key() == se.entry.Route.Key() {
							filteredResult = append(filteredResult, ch)
							seenFilteredRouteKeys[se.entry.Route.Key()] = true
							break
						}
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
				matchUID := fmt.Sprintf("%s:ch_%d", ch.Route.Kind, ch.Index)
				if upstream != nil && upstream.ChannelUID != "" {
					matchUID = ch.Route.Kind + ":" + upstream.ChannelUID
				}
				if matchUID == intentTargetUID || (upstream != nil && upstream.ChannelUID == intentTargetUID) {
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

// federatedRoute 归一化候选的物理路由标识。
// 旧调用方只填 Index（无 Route），此时按请求协议补齐 kind 与 index，
// 使同一候选在评分、结果映射和硬约束阶段得到稳定且互不冲突的身份。
func federatedRoute(ch scheduler.ChannelInfo, requestKind string) scheduler.ChannelRouteRef {
	route := ch.Route
	if route.Kind == "" {
		route.Kind = requestKind
		if route.Kind == "" {
			route.Kind = string(scheduler.ChannelKindMessages)
		}
	}
	if route.ChannelUID == "" && route.Index == 0 && ch.Index != 0 {
		route.Index = ch.Index
	}
	return route
}

// channelScoreEntry 渠道评分输入条目。
type channelScoreEntry struct {
	ChannelUID          string
	ChannelName         string // 渠道显示名（来自 upstream.Name）
	ChannelKind         string
	Route               scheduler.ChannelRouteRef
	ProtocolFidelity    string
	ConversionPenalty   float64
	MetricsKey          string
	KeyMask             string // 掩码后的 key，如 sk-***abc
	MappedModel         string
	MappingSource       string
	MappingReason       string
	CandidateKey        string // (渠道, 模型) 粒度标识：channelUID|model
	OriginTier          ChannelOriginTier
	HealthState         HealthState
	EstimatedCost       float64
	ChannelIndex        int
	ModelID             string
	BenchmarkScore      float64
	BenchmarkKnown      bool
	DomainProfiles      []ModelProfile
	SupportsVision      bool // 渠道是否支持识图（模型注册表 + 画像聚合 + 手动配置覆盖）
	SupportsDocument    bool // 渠道是否支持文档（PDF 等，模型注册表来源，无画像聚合来源）
	SupportsToolCalls   bool // 渠道是否支持工具调用（模型注册表 + 画像聚合）
	SupportsReasoning   bool // 渠道是否支持推理（模型注册表 + 画像聚合）
	ContextWindowTokens int  // 渠道上下文窗口大小（0 = 未知，来自模型能力注册表）
	ScoringCandidate    ScoringCandidate

	// AFP 成本信息（仅火山 Agent Plan 渠道有值）
	AFPCost *CandidateAFPCost // AFP 成本计算结果（nil = 非 AFP 渠道）

	// CompshareDeduction 优云智算套餐单次减扣倍数（0 = 非 compshare 渠道，>0 = 减扣次数）。
	// 来自 ProviderTemplate.ModelCostMultipliers；越低越省，全局可比。
	CompshareDeduction float64

	// LogicalChannelUID / LogicalChannelName 是所属逻辑渠道的稳定身份与显示名。
	// 当前 Phase A.1 仅透传，不参与评分；旧配置或独立物理渠道为空。
	LogicalChannelUID  string
	LogicalChannelName string
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
	CandidateKey  string // (渠道, 模型) 粒度标识：channelUID|model
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

	upstream = config.RuntimeUpstreamForAutoManagedProvider(upstream)
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

// routingCandidateFanoutLimit 单渠道按 (渠道, 模型) 展开候选行的上限，防 candidates_json 膨胀。
const routingCandidateFanoutLimit = 8

// routingCandidateKey 计算 (渠道, 模型) 粒度候选行的稳定标识。
func routingCandidateKey(channelUID, model string) string {
	return channelUID + "|" + normalizeRoutingModelID(model)
}

// resolveChannelModels 按 (渠道, 模型) 粒度枚举该渠道能服务请求模型的所有模型，
// 每行独立评分 + 独立硬约束判定。分支镜像单数版 resolveChannelModel，但返回切片。
// 同名承接渠道也补显示模型名（MappedModel 为空时用 ActualModel=请求模型名填充），
// 保证每行模型名非空。枚举为空但渠道 supported 时回退到单数解析结果（fail-open）。
func (r *SmartRouter) resolveChannelModels(
	profile *RequestProfile,
	upstream *config.UpstreamConfig,
	upstreamModelCapabilities map[string]config.UpstreamModelCapability,
) []channelModelResolution {
	if profile == nil || upstream == nil {
		return nil
	}
	channelUID := upstream.ChannelUID
	requestModel := profile.Model
	upstream = config.RuntimeUpstreamForAutoManagedProvider(upstream)
	floor := BuildCapabilityFloorFromRequestProfile(profile)

	// ── AutoManaged 渠道：枚举全部满足能力下界的已探测模型，按质量排序后截断 top N。
	if upstream.AutoManaged && r.modelResolver != nil {
		ranked := r.modelResolver.ResolveModelsAnyEndpointWithFloor(
			requestModel, channelUID, profile.ChannelKind, floor, routingCandidateFanoutLimit,
		)
		// 精确/等价命中优先：请求模型或其等价模型在画像中时只产该行（与单数版短路一致）。
		exactModel := ""
		for _, candidate := range ranked {
			if normalizeRoutingModelID(candidate.profile.ModelID) == normalizeRoutingModelID(requestModel) {
				exactModel = candidate.profile.ModelID
				break
			}
		}
		// 非自适应入口禁止跨模型替代：无精确/等价命中且意图要求精确时，
		// 交由单数版返回 Supported=false（该渠道不产生候选行）。
		if exactModel == "" && !ClassifyModelRoutingIntent(profile.ChannelKind, requestModel).AllowsSubstitution() {
			return nil
		}
		resolutions := make([]channelModelResolution, 0, len(ranked))
		seen := make(map[string]bool, len(ranked))
		for _, candidate := range ranked {
			model := candidate.profile.ModelID
			if exactModel != "" && model != exactModel {
				// 有精确/等价命中时只保留该行。
				continue
			}
			normalized := normalizeRoutingModelID(model)
			if normalized == "" || seen[normalized] {
				continue
			}
			seen[normalized] = true
			res := channelModelResolution{
				ActualModel:  model,
				Supported:    true,
				CandidateKey: routingCandidateKey(channelUID, model),
			}
			if normalized != normalizeRoutingModelID(requestModel) {
				res.MappedModel = model
				res.MappingSource = "auto_resolve"
				res.MappingReason = candidate.reasonSummary()
			}
			// 同名承接（normalized == 请求模型）：MappedModel 留空，由调用方用请求模型名补显示。
			resolutions = append(resolutions, res)
		}
		if len(resolutions) > 0 {
			return resolutions
		}
	}

	// ── 显式/白名单渠道：从 ModelMapping 值 + 请求模型 redirect 目标出发，
	// 逐个过 ExplainModelSupport 应用 SupportedModels 增删。
	seen := make(map[string]bool)
	resolutions := make([]channelModelResolution, 0, 2)
	addResolution := func(actualModel, source, reason string) {
		if actualModel == "" {
			return
		}
		if supported, _ := upstream.ExplainModelSupport(actualModel); !supported {
			return
		}
		normalized := normalizeRoutingModelID(actualModel)
		if normalized == "" || seen[normalized] {
			return
		}
		seen[normalized] = true
		res := channelModelResolution{
			ActualModel:  actualModel,
			Supported:    true,
			CandidateKey: routingCandidateKey(channelUID, actualModel),
		}
		if normalized != normalizeRoutingModelID(requestModel) {
			res.MappedModel = actualModel
			res.MappingSource = source
			res.MappingReason = reason
		}
		resolutions = append(resolutions, res)
	}

	if len(upstream.ModelMapping) > 0 {
		for _, target := range upstream.ModelMapping {
			addResolution(target, "explicit_mapping", "matched configured model mapping")
		}
	}
	// 请求模型的 redirect 目标（无映射时为请求模型本身）。
	if redirected, matched := config.RedirectModelWithMatch(requestModel, upstream); matched {
		addResolution(redirected, "explicit_mapping", "matched configured model mapping")
	} else if redirected != "" {
		addResolution(redirected, "", "")
	}

	// fail-open：枚举为空但单数解析认为渠道 supported 时，回退到单模型行。
	if len(resolutions) == 0 {
		if single := r.resolveChannelModel(profile, upstream, upstreamModelCapabilities); single.Supported {
			single.CandidateKey = routingCandidateKey(channelUID, single.ActualModel)
			return []channelModelResolution{single}
		}
		return nil
	}
	return resolutions
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
		Route:         ch.Route,
		ChannelIndex:  ch.Index,
		HealthState:   HealthStateUnknown,
		OriginTier:    OriginTierUnknown,
		EstimatedCost: -1,
	}
	if r.isLogicalChannelIdentityEnabled() {
		entry.LogicalChannelUID = strings.TrimSpace(upstream.LogicalChannelUID)
		entry.LogicalChannelName = strings.TrimSpace(upstream.LogicalName)
	}
	actualModel := model
	modelProvider := ""
	var modelPricing *config.ModelPricing
	if model != "" {
		resolved := config.ResolveMappedUpstreamCapability(model, config.RedirectModel(model, upstream), upstream, upstreamModelCapabilities)
		actualModel = resolved.ActualModel
		if resolved.Known {
			capability := resolved.Capability
			modelProvider = capability.Provider
			modelPricing = capability.Pricing
			entry.ContextWindowTokens = capability.ContextWindowTokens
			entry.SupportsVision = capability.Capabilities["vision"]
			entry.SupportsDocument = capability.Capabilities["document"]
			entry.SupportsToolCalls = capability.Capabilities["toolCalls"]
			entry.SupportsReasoning = upstreamCapabilitySupportsReasoning(capability)
		}
	}
	entry.ModelID = actualModel
	benchmark := config.ResolveModelBenchmarkProfile(actualModel)
	entry.BenchmarkKnown = benchmark.Known && benchmark.Profile.OverallScore > 0
	entry.BenchmarkScore = benchmark.Profile.OverallScore
	if learned, ok := learnedContextLimit(channelUID, actualModel); ok {
		if entry.ContextWindowTokens == 0 || learned < entry.ContextWindowTokens {
			entry.ContextWindowTokens = learned
		}
	}
	// 实测结论只收紧不放松：注册表说不支持保持不支持，注册表说支持但实测拒绝也收紧为不支持。
	// 学到的规避经既有 document_unsupported 硬约束呈现，routingHardConstraintReasons 无需改动。
	if learnedDocumentUnsupported(channelUID, actualModel) {
		entry.SupportsDocument = false
	}
	if modelPricing != nil {
		listCost := metrics.CalculateTokenCostUSDWithPricing(modelPricing, 1_000_000, 1_000_000, 1_000_000, 1_000_000)
		entry.EstimatedCost = listCost
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
		timeMultiplier := 1.0
		var graph *config.ExchangeRateGraph
		if r.configManager != nil {
			costConfig := r.configManager.GetAutopilotRouting().CostOptimization
			if pricingProviderID != "" {
				timeMultiplier = costConfig.ProviderTimePricingMultiplier(pricingProviderID, r.currentTime())
			}
			if len(costConfig.ExchangeRateQuotes) > 0 {
				version := uint64(1)
				if costConfig.ExchangeRateSnapshot != nil && costConfig.ExchangeRateSnapshot.Version > 0 {
					version = costConfig.ExchangeRateSnapshot.Version
				}
				var graphErr error
				graph, graphErr = config.NewExchangeRateGraph(costConfig.ExchangeRateQuotes, version, r.currentTime())
				if graphErr != nil {
					r.logExchangeRateBuildErrorOnce(version, graphErr)
				}
			}
		}
		// 默认成本：请求可用 key 中的最小有效成本（保留 0 成本）。
		entry.EstimatedCost = -1
		for _, cfg := range config.NormalizeAPIKeyConfigsForView(*upstream) {
			eligibility := config.EvaluateAPIKeyMultiplierEligibility(cfg, r.currentTime())
			if !eligibility.Eligible {
				continue
			}
			groupMultiplier := 1.0
			if cfg.GroupMultiplier != nil {
				groupMultiplier = *cfg.GroupMultiplier
			}
			candidateCost := listCost * timeMultiplier * groupMultiplier
			if graph != nil && r.subscriptionStore != nil {
				subscriptionUID := strings.TrimSpace(cfg.SourceSubscriptionUID)
				if subscriptionUID != "" {
					subscription := r.subscriptionStore.Get(subscriptionUID)
					if subscription != nil && subscription.PaymentAmount != nil && subscription.CreditAmount != nil {
						resolvedCost := ResolveEffectiveCostUSD(EffectiveCostInput{
							Graph: graph, ListCostAmount: listCost, ListCostUnit: "USD",
							GroupMultiplier: groupMultiplier, TimeMultiplier: timeMultiplier,
							PaymentAmount: *subscription.PaymentAmount, PaymentUnit: subscription.PaymentUnit,
							CreditAmount: *subscription.CreditAmount, CreditUnit: subscription.CreditUnit,
							KeyUID: cfg.KeyUID, SubscriptionUID: subscriptionUID,
						})
						if resolvedCost.Available {
							candidateCost = resolvedCost.EffectiveCostUSD
						} else {
							r.logEffectiveCostMissingOnce(subscriptionUID, resolvedCost.Reason)
						}
					}
				}
			}
			if entry.EstimatedCost < 0 || candidateCost < entry.EstimatedCost {
				entry.EstimatedCost = candidateCost
			}
		}
		// 无可用 key 时保持默认倍率 1.0，避免把无配置渠道错误排除
		if entry.EstimatedCost < 0 {
			entry.EstimatedCost = listCost * timeMultiplier
		}
	}
	modelFamily := InferModelFamily(actualModel, modelProvider)
	visionDisabled := upstream.NoVision || containsString(upstream.NoVisionModels, actualModel)
	if visionDisabled {
		entry.SupportsVision = false
	}

	if r.profileStore != nil {
		profiles := r.profileStore.ListActiveByChannel(channelUID)
		matchingProfiles := make([]*KeyEndpointProfile, 0, len(profiles))
		for _, profile := range profiles {
			if profile != nil && profile.ChannelKind == entry.ChannelKind {
				matchingProfiles = append(matchingProfiles, profile)
			}
		}
		if len(matchingProfiles) > 0 {
			profileValues := make([]KeyEndpointProfile, len(matchingProfiles))
			for i, p := range matchingProfiles {
				profileValues[i] = *p
			}
			agg := AggregateChannelProfile(channelUID, ch.Index, entry.ChannelKind, profileValues)
			entry.HealthState = agg.HealthState
			entry.OriginTier = ChannelOriginTier(agg.OriginTier)
			entry.MetricsKey = matchingProfiles[0].MetricsKey
			entry.KeyMask = matchingProfiles[0].KeyMask
			if !visionDisabled {
				entry.SupportsVision = entry.SupportsVision || agg.SupportsVision
			}
			entry.SupportsToolCalls = entry.SupportsToolCalls || agg.SupportsToolCalls
			entry.SupportsReasoning = entry.SupportsReasoning || agg.SupportsReasoning
			entry.ScoringCandidate = ScoringCandidate{
				ChannelUID: channelUID, QualityTier: agg.QualityTier, StabilityTier: agg.StabilityTier,
				SpeedTier: agg.SpeedTier, CostTier: agg.CostTier, HealthState: agg.HealthState,
				ProviderQualityScore: 0.5, ProviderQualityConfidence: 0.3,
				ModelFamily: modelFamily, SavingsScore: 0.5, DomainStrengthScore: 0.5,
			}
			r.applyModelQualityTier(&entry)
			r.attachDomainProfiles(&entry, modelProvider)
			return entry
		}

		// Phase A.2 fallback：本物理渠道无画像时，尝试聚合同一 LogicalChannel 下
		// 兄弟物理渠道的画像。仅在开关开启且能找到兄弟画像时生效；物理画像存在的
		// 分支已在上方 return，绝不会覆盖真实物理画像。
		if agg, ok := r.aggregateSiblingChannelProfile(channelUID); ok {
			entry.HealthState = agg.HealthState
			entry.OriginTier = ChannelOriginTier(agg.OriginTier)
			if !visionDisabled {
				entry.SupportsVision = entry.SupportsVision || agg.SupportsVision
			}
			entry.SupportsToolCalls = entry.SupportsToolCalls || agg.SupportsToolCalls
			entry.SupportsReasoning = entry.SupportsReasoning || agg.SupportsReasoning
			entry.ScoringCandidate = ScoringCandidate{
				ChannelUID: channelUID, QualityTier: agg.QualityTier, StabilityTier: agg.StabilityTier,
				SpeedTier: agg.SpeedTier, CostTier: agg.CostTier, HealthState: agg.HealthState,
				ProviderQualityScore: 0.5, ProviderQualityConfidence: 0.3,
				ModelFamily: modelFamily, SavingsScore: 0.5, DomainStrengthScore: 0.5,
			}
			r.applyModelQualityTier(&entry)
			r.attachDomainProfiles(&entry, modelProvider)
			return entry
		}
	}
	entry.ScoringCandidate = ScoringCandidate{
		ChannelUID: channelUID, QualityTier: QualityTierNormal, StabilityTier: StabilityTierNormal,
		SpeedTier: SpeedTierNormal, CostTier: CostTierNormal, HealthState: HealthStateUnknown,
		ProviderQualityScore: 0.5, ProviderQualityConfidence: 0.3,
		ModelFamily: modelFamily, SavingsScore: 0.5, DomainStrengthScore: 0.5,
	}
	r.applyModelQualityTier(&entry)
	r.attachDomainProfiles(&entry, modelProvider)
	return entry
}

// aggregateSiblingChannelProfile 聚合同一 LogicalChannel 下兄弟物理渠道（跨协议）的画像，
// 作为本物理渠道无画像时的评分 fallback（Phase A.2）。
// 仅在 LogicalChannelScoringEnabled 开启、能定位逻辑渠道、且存在至少一个有画像的兄弟渠道时返回 ok=true。
// 排除 channelUID 自身（其无画像才会走到这里）。
func (r *SmartRouter) aggregateSiblingChannelProfile(channelUID string) (ChannelProfile, bool) {
	if r == nil || r.profileStore == nil || r.configManager == nil {
		return ChannelProfile{}, false
	}
	if !r.configManager.GetAutopilotRouting().IsLogicalChannelScoringEnabled() {
		return ChannelProfile{}, false
	}
	siblings := r.configManager.LogicalSiblingChannelUIDs(channelUID)
	if len(siblings) == 0 {
		return ChannelProfile{}, false
	}
	endpoints := make([]KeyEndpointProfile, 0, len(siblings))
	for _, siblingUID := range siblings {
		if siblingUID == channelUID {
			continue
		}
		for _, profile := range r.profileStore.ListActiveByChannel(siblingUID) {
			if profile != nil {
				endpoints = append(endpoints, *profile)
			}
		}
	}
	if len(endpoints) == 0 {
		return ChannelProfile{}, false
	}
	// channelKind 用兄弟画像自身的 kind 聚合；这里只关心健康/质量/成本/能力维度，
	// 与请求 kind 无关，因此传空字符串占位。
	return AggregateChannelProfile(channelUID, 0, "", endpoints), true
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
					profile.QualityTier = ModelProfileQualityTier(profile.ModelID, family)
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
			quality = ModelProfileQualityTier(entry.ModelID, modelFamily)
		}
	}
	if quality != "" {
		entry.ScoringCandidate.QualityTier = quality
	}
	applyPremiumBenchmarkEvidence(entry)
}

// applyPremiumBenchmarkEvidence 为最终判定为 premium 档的候选补充连续 benchmark 证据，
// 供 ScoreCandidate 在同档内做 tie-break（如 gpt-5.6-sol 相对 gpt-5.4 的能力差异）。
// 只在 QualityTier 已经是 premium 时生效，不跨档位比较、不影响 premium 之外的排序；
// benchmark 未知时保持零值，评分侧回退到原有中性行为。
func applyPremiumBenchmarkEvidence(entry *channelScoreEntry) {
	if entry == nil || entry.ScoringCandidate.QualityTier != QualityTierPremium {
		return
	}
	if !entry.BenchmarkKnown {
		return
	}
	entry.ScoringCandidate.QualityBenchmarkKnown = true
	entry.ScoringCandidate.QualityBenchmarkScore = entry.BenchmarkScore
}

// exchangeRateBuildErrorTracker 记录已报告的汇率图构建失败版本，避免热路径刷屏。
type exchangeRateBuildErrorTracker struct {
	mu      sync.Mutex
	version uint64
}

var sharedExchangeRateBuildErrorTracker exchangeRateBuildErrorTracker

// logExchangeRateBuildErrorOnce 按 ExchangeRateSnapshot.Version 只记录一次汇率图构建失败。
func (r *SmartRouter) logExchangeRateBuildErrorOnce(version uint64, err error) {
	sharedExchangeRateBuildErrorTracker.mu.Lock()
	defer sharedExchangeRateBuildErrorTracker.mu.Unlock()
	if sharedExchangeRateBuildErrorTracker.version == version {
		return
	}
	sharedExchangeRateBuildErrorTracker.version = version
	log.Printf("[SmartRouter-ExchangeRate] 汇率图构建失败，回退标价: %v", err)
}

// effectiveCostMissingTracker 按 subscriptionUID+Reason 采样记录 effective cost 不可用原因。
type effectiveCostMissingTracker struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

var sharedEffectiveCostMissingTracker effectiveCostMissingTracker

// logEffectiveCostMissingOnce 在 effective cost 不可用时按 uid+reason 只记一次日志。
func (r *SmartRouter) logEffectiveCostMissingOnce(subscriptionUID, reason string) {
	if reason == "" {
		return
	}
	key := subscriptionUID + "|" + reason
	sharedEffectiveCostMissingTracker.mu.Lock()
	defer sharedEffectiveCostMissingTracker.mu.Unlock()
	if sharedEffectiveCostMissingTracker.seen == nil {
		sharedEffectiveCostMissingTracker.seen = make(map[string]struct{})
	}
	if _, ok := sharedEffectiveCostMissingTracker.seen[key]; ok {
		return
	}
	sharedEffectiveCostMissingTracker.seen[key] = struct{}{}
	log.Printf("[SmartRouter-EffectiveCost] subscription=%s effective cost 不可用: %s", subscriptionUID, reason)
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

	// AFP 分组：scopeID -> (候选键 -> TotalAFP)
	afpGroups := make(map[string]map[string]float64)
	// compshare 组：候选键 -> 减扣次数
	compshareCosts := make(map[string]float64)
	// USD 组：候选键 -> EstimatedCost
	usdCosts := make(map[string]float64)

	// 评分键：优先 (渠道, 模型) 粒度 CandidateKey，空（本地候选等）回退渠道级 ChannelUID。
	key := func(e channelScoreEntry) string {
		if e.CandidateKey != "" {
			return e.CandidateKey
		}
		return e.ChannelUID
	}

	for _, e := range entries {
		if e.CompshareDeduction > 0 {
			compshareCosts[key(e)] = e.CompshareDeduction
		} else if e.AFPCost != nil && e.AFPCost.Evidence.Unit == CostUnitAFP && e.AFPCost.Evidence.ScopeID != "" {
			scopeID := e.AFPCost.Evidence.ScopeID
			if afpGroups[scopeID] == nil {
				afpGroups[scopeID] = make(map[string]float64)
			}
			afpGroups[scopeID][key(e)] = float64(e.AFPCost.Evidence.Estimated)
		} else if e.EstimatedCost >= 0 {
			usdCosts[key(e)] = e.EstimatedCost
		} else {
			// 无成本证据：中性
			result[key(e)] = 0.5
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
//   - document 请求但渠道不支持文档（PDF 等）
//   - CapabilityFloor：请求需要推理但渠道不支持（画像数据可用时）
//   - CapabilityFloor：请求需要工具调用但渠道不支持
//   - CapabilityFloor：上下文窗口需求大于渠道容量
func routingHardConstraintReasons(profile *RequestProfile, entry *channelScoreEntry) []string {
	var reasons []string

	// 识图硬约束
	if profile.VisionNeed && !entry.SupportsVision {
		reasons = append(reasons, "vision_unsupported")
	}

	// 文档硬约束
	if profile.DocumentNeed && !entry.SupportsDocument {
		reasons = append(reasons, "document_unsupported")
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
		modelResolutions := r.resolveChannelModels(profile, &upstream, cfg.UpstreamModelCapabilities)
		if model != "" {
			// 过滤掉不支持的行；全部不支持则跳过该渠道。
			supported := modelResolutions[:0]
			for _, mr := range modelResolutions {
				if mr.Supported {
					supported = append(supported, mr)
				}
			}
			modelResolutions = supported
			if len(modelResolutions) == 0 {
				continue
			}
		}
		ch := scheduler.ChannelInfo{
			Index:    i,
			Name:     upstream.Name,
			Priority: upstream.Priority,
			Status:   status,
		}
		for _, modelResolution := range modelResolutions {
			entry := r.buildChannelEntry(ch, &upstream, channelKind, modelResolution.ActualModel, cfg.UpstreamModelCapabilities)
			entry.CandidateKey = modelResolution.CandidateKey
			if entry.CandidateKey == "" {
				entry.CandidateKey = routingCandidateKey(upstream.ChannelUID, modelResolution.ActualModel)
			}
			// 同名承接（MappingSource 为空）时 MappedModel 保持空，避免映射质量档折算误判；见 executeFilter。
			entry.MappedModel = modelResolution.MappedModel
			entry.MappingSource = modelResolution.MappingSource
			if entry.MappingSource == "auto_resolve" {
				entry.MappingSource = "auto_resolve_preview"
			}
			entry.MappingReason = modelResolution.MappingReason
			r.applyModelQualityTier(&entry)
			entries = append(entries, entry)
		}
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
