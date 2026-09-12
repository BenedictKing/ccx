package common

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/racing"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// ── 竞速编排：首字明显慢时向跨渠道候选并行派影子请求，先交付者胜 ──
//
// 编排器包在多渠道 failover 外壳的单次渠道尝试外：
//   - 主分支照常执行；阈值定时器到期且复核通过时派 1..N 条影子分支；
//   - 全部分支共享一个提交闸门（racing.Gate），赢家 claim 即取消其余分支；
//   - 影子候选取 SmartRouter 排名并整渠道排除主渠道（同渠道影子只是同
//     provider 同队列的重复消耗），缓存为空时回退调度器按路由重选；
//   - 行为参数（影子数/触发 floor/成本过滤）由请求 CostPreference 经策略表推导，
//     用户只控制全局与渠道级开关。

const racingBranchIDKey = "ccx.racing.branch_id"

// RacingHub 竞速编排运行时依赖（main.go 初始化注入）。
type RacingHub struct {
	Registry *racing.Registry
	Sem      *racing.Semaphore
	// CandidateProvider 返回 SmartRouter 最近一次排名的候选（nil 时仅走调度器回退）。
	CandidateProvider func(model, channelKind string) []autopilot.RoutingCandidate
	// behaviorOverride 覆盖策略表（仅测试注入；nil 时用 racing.BehaviorForCostPreference）。
	behaviorOverride func(costPreference string) racing.Behavior
}

// behaviorFor 解析本请求的竞速行为。
func (h *RacingHub) behaviorFor(costPreference string) racing.Behavior {
	if h.behaviorOverride != nil {
		return h.behaviorOverride(costPreference)
	}
	return racing.BehaviorForCostPreference(costPreference)
}

// SetBehaviorOverrideForTest 注入策略表覆盖（仅供测试：跳过生产 floor 等待）。
func (h *RacingHub) SetBehaviorOverrideForTest(f func(costPreference string) racing.Behavior) {
	h.behaviorOverride = f
}

var (
	racingHubMu sync.RWMutex
	racingHub   *RacingHub
)

// SetRacingHub 注入竞速编排依赖（main.go 在初始化完成后调用）。
func SetRacingHub(hub *RacingHub) {
	racingHubMu.Lock()
	racingHub = hub
	racingHubMu.Unlock()
}

func getRacingHub() *RacingHub {
	racingHubMu.RLock()
	defer racingHubMu.RUnlock()
	return racingHub
}

// RacingAttemptInput 编排一次渠道尝试所需的请求级上下文（外壳循环已有）。
type RacingAttemptInput struct {
	Ctx              context.Context
	EnvCfg           *config.EnvConfig
	CfgManager       *config.ConfigManager
	Scheduler        *scheduler.ChannelScheduler
	Kind             scheduler.ChannelKind
	Model            string
	IsStream         bool
	HasImageContent  bool
	SelectionOptions scheduler.SelectionOptions // 主选择参数模板（回退重选时仅替换 FailedRoutes）
	Selection        *scheduler.SelectionResult
	AttemptStartedAt time.Time
	RequestStartedAt time.Time
}

// shouldArmRacing 竞速武装条件检查。false 时编排器退化为直接调用闭包。
func shouldArmRacing(
	c *gin.Context,
	hub *RacingHub,
	cfgManager *config.ConfigManager,
	kind scheduler.ChannelKind,
	selection *scheduler.SelectionResult,
	hasImageContent bool,
) bool {
	if hub == nil || hub.Registry == nil || selection == nil || selection.Upstream == nil {
		return false
	}
	switch kind {
	case scheduler.ChannelKindMessages, scheduler.ChannelKindChat, scheduler.ChannelKindResponses, scheduler.ChannelKindGemini:
	default:
		return false
	}
	// 显式渠道 pin 是用户意图，不竞速
	if c.GetHeader("X-Channel") != "" {
		return false
	}
	// 含图请求影子需整包重传图片，v1 不竞速
	if hasImageContent {
		return false
	}
	cfgSnapshot := cfgManager.GetConfig()
	return cfgSnapshot.ResolveRacingPolicy(selection.Upstream)
}

// resolveRequestCostPreference 解析请求生效价格偏好：
// 请求头 X-Cost-Preference > 场景预设默认 > 全局配置 Mode。
// 相比调度链省略 PerTaskClass 覆盖；竞速是辅助行为，轻微口径差可接受。
func resolveRequestCostPreference(c *gin.Context, cfgManager *config.ConfigManager) string {
	if v := strings.TrimSpace(c.GetHeader("X-Cost-Preference")); isValidRacingCostPreference(v) {
		return v
	}
	cfg := cfgManager.GetConfig()
	autopilotCfg := cfg.AutopilotRouting
	if preset, ok := autopilot.ResolveScenarioPreset(autopilotCfg.Scenario, c.GetHeader("X-Routing-Scenario")); ok && preset.CostPreference != "" {
		return preset.CostPreference
	}
	if mode := autopilotCfg.CostPreference.GetEffectiveCostPreferenceMode(""); isValidRacingCostPreference(mode) {
		return mode
	}
	return "balanced"
}

func isValidRacingCostPreference(v string) bool {
	switch v {
	case "quality_first", "balanced", "cost_first":
		return true
	}
	return false
}

// ErrRacingSuperseded 竞速败出（racing.ErrRacingSuperseded 的 handler 层别名）：
// 各协议非流式提交点 claim 失败时返回，失败分类链据此豁免渠道健康惩罚。
var ErrRacingSuperseded = racing.ErrRacingSuperseded

// RacingClaimClientCommit 竞速分支向客户端写出响应前的提交裁决（导出供各协议 handler 使用）。
// 无竞速闸门时直接放行（零开销路径）；失败返回 false，调用方应立即以
// ErrRacingSuperseded 返回，不得再向客户端写任何字节。
func RacingClaimClientCommit(c *gin.Context) bool { return racingClaimClientCommit(c) }

// setBranchContext 在分支 gin context 上写入竞速闸门/角色/分支编号。
func setBranchContext(c *gin.Context, gate *racing.Gate, role string, branchID int) {
	c.Set(racing.ContextKeyGate, gate)
	c.Set(racing.ContextKeyRole, role)
	c.Set(racingBranchIDKey, branchID)
}

// gateFromContext 从 gin context 读取竞速闸门（未参与竞速返回 nil）。
func gateFromContext(c *gin.Context) *racing.Gate {
	if c == nil {
		return nil
	}
	if v, ok := c.Get(racing.ContextKeyGate); ok {
		if gate, ok := v.(*racing.Gate); ok {
			return gate
		}
	}
	return nil
}

// racingClaimClientCommit 竞速分支向客户端写出响应前的提交裁决。
// 无竞速闸门时直接放行（零开销路径）；claim 成功即成为赢家并取消其余分支，
// 分支 writer 同步 Commit 桥接到真实客户端 writer；失败 Discard 分支 writer，
// 调用方应释放上游资源并以 racing.ErrRacingSuperseded 收尾。
func racingClaimClientCommit(c *gin.Context) bool {
	gate := gateFromContext(c)
	if gate == nil {
		return true
	}
	branchID := 0
	if v, ok := c.Get(racingBranchIDKey); ok {
		if id, ok := v.(int); ok {
			branchID = id
		}
	}
	if !gate.ClaimBy(branchID) {
		if bw, ok := c.Writer.(*racingBranchWriter); ok {
			bw.Discard()
		}
		return false
	}
	if bw, ok := c.Writer.(*racingBranchWriter); ok {
		bw.Commit()
	}
	SetChannelLogRacingWon(c)
	return true
}

// isRacingSuperseded 判断本分支失败是否应归因为竞速败出。
// 两种形态：显式败出错误（claim 失败返回），或"另一分支已 claim 且本分支未赢"
// 的客户端侧取消（赢家取消败者导致的 context.Canceled 系错误）。
// 真实上游错误（超时/500/拉黑）即便发生时另一分支已赢，也照常记账，不豁免。
func isRacingSuperseded(c *gin.Context, err error) bool {
	if errors.Is(err, racing.ErrRacingSuperseded) {
		return true
	}
	gate := gateFromContext(c)
	if gate == nil || err == nil || !gate.Claimed() || !isClientSideError(err) {
		return false
	}
	branchID := -1
	if v, ok := c.Get(racingBranchIDKey); ok {
		if id, ok := v.(int); ok {
			branchID = id
		}
	}
	return gate.ClaimedBy() != branchID
}

// racingSupersededOrCanceledEmptyStream 竞速分支的败出补充判定：
// 被赢家取消的上游连接有时以干净 EOF 呈现（而非读错误），分支被误判为
// "空流响应"并试图内部轮转——若此刻另一分支已 claim，一律按败出终止，
// 防止被取消的影子继续轮转并在下一轮 SendRequest 以 context.Canceled 返回
// Handled=true 被误判为赢家。
func racingSupersededOrCanceledEmptyStream(c *gin.Context, err error) bool {
	if isRacingSuperseded(c, err) {
		return true
	}
	if !errors.Is(err, ErrEmptyStreamResponse) {
		return false
	}
	return racingClaimedByOtherBranch(c)
}

// racingClaimedByOtherBranch 闸门已被其他分支 claim 且本分支未赢。
func racingClaimedByOtherBranch(c *gin.Context) bool {
	gate := gateFromContext(c)
	if gate == nil || !gate.Claimed() {
		return false
	}
	branchID := -1
	if v, ok := c.Get(racingBranchIDKey); ok {
		if id, ok := v.(int); ok {
			branchID = id
		}
	}
	return gate.ClaimedBy() != branchID
}

// racingIsShadow 判断该分支是否为影子分支。
func racingIsShadow(c *gin.Context) bool {
	if v, ok := c.Get(racing.ContextKeyRole); ok {
		s, _ := v.(string)
		return s == racing.RoleShadow
	}
	return false
}

// racingStreamCeilingMs 流式触发上限：与渠道首字超时同源（等过它请求已失败）。
func racingStreamCeilingMs(in *RacingAttemptInput, upstream *config.UpstreamConfig) int {
	global := in.Scheduler.GetMetricsManagerForRoute(in.Selection.Route).GetCircuitBreakerConfig()
	return ResolveStreamFirstContentTimeout(upstream.StreamFirstContentTimeoutMs, global.StreamFirstContentTimeoutMs)
}

// racingNonStreamCeilingMs 非流式触发上限：与等待上游响应头超时同源。
func racingNonStreamCeilingMs(in *RacingAttemptInput, upstream *config.UpstreamConfig) int {
	globalMs := config.GetRuntimeResponseHeaderTimeoutMs(in.EnvCfg.ResponseHeaderTimeout * 1000)
	return upstream.GetEffectiveResponseHeaderTimeoutMs(globalMs)
}

// racingEffectiveCostMultiplier 综合成本倍率 = 渠道 CostMultiplier × 命中 key 的 GroupMultiplier。
func racingEffectiveCostMultiplier(upstream *config.UpstreamConfig, keyIdentity string) float64 {
	m := 1.0
	if upstream == nil {
		return m
	}
	if upstream.CostMultiplier != nil && *upstream.CostMultiplier > 0 {
		m = *upstream.CostMultiplier
	}
	keyIdentity = strings.TrimSpace(keyIdentity)
	if keyIdentity == "" {
		return m
	}
	for _, cfg := range config.NormalizeAPIKeyConfigsForView(*upstream) {
		uid := strings.TrimSpace(cfg.KeyUID)
		matched := uid != "" && uid == keyIdentity
		if !matched && uid == "" && cfg.Key != "" {
			matched = "kh_"+autopilot.KeyHashFromAPIKey(cfg.Key) == keyIdentity
		}
		if matched {
			if cfg.GroupMultiplier != nil && *cfg.GroupMultiplier > 0 {
				m *= *cfg.GroupMultiplier
			}
			break
		}
	}
	return m
}

// findRacingChannelByUID 按 kind 反查渠道配置与索引。
func findRacingChannelByUID(cfg *config.Config, channelUID string, kind scheduler.ChannelKind) (*config.UpstreamConfig, int) {
	var list []config.UpstreamConfig
	switch kind {
	case scheduler.ChannelKindResponses:
		list = cfg.ResponsesUpstream
	case scheduler.ChannelKindGemini:
		list = cfg.GeminiUpstream
	case scheduler.ChannelKindChat:
		list = cfg.ChatUpstream
	default:
		list = cfg.Upstream
	}
	for i := range list {
		if channelUID != "" && list[i].ChannelUID == channelUID {
			return &list[i], i
		}
	}
	return nil, 0
}

// ── 编排执行 ──

type racingShadowRun struct {
	branchID  int
	selection *scheduler.SelectionResult
	ginCtx    *gin.Context
	cancel    context.CancelFunc
	done      chan MultiChannelAttemptResult
	startedAt time.Time
}

type racingRuns struct {
	mu                 sync.Mutex
	gate               *racing.Gate
	hub                *RacingHub
	in                 *RacingAttemptInput
	trySelectedChannel TrySelectedChannelFunc
	behavior           racing.Behavior
	thresholdMs        int
	// needsToolWhitelist 本请求带工具定义（spawnShadows 时判定一次）：
	// true 时影子候选受工具调用白名单渠道间排他约束。
	needsToolWhitelist bool
	// clientWriter 真实客户端 writer（主分支包装前的原值），影子分支 writer 的桥接目标。
	clientWriter    gin.ResponseWriter
	runs            []*racingShadowRun
	results         map[int]MultiChannelAttemptResult
	nextBranchID    int
	usedIdentities  map[string]bool // 已占用候选身份（主 + 已派影子）
	usedChannels    map[string]bool // 已占用渠道身份（主 + 已派影子）：影子按渠道粒度去重
	usedRouteKeys   map[scheduler.ChannelRouteKey]bool
	failedRouteKeys []scheduler.ChannelRouteKey
	spawned         bool
}

// toolWhitelistAllows 带工具请求的影子候选路由排他判定：
// 本次请求的执行协议上存在任一「运行期 auto 实测真实工具调用」路由
// （TraitVerifiedToolCalls runtime 来源）时，非白名单路由不放行；该协议无任何
// 白名单路由 fail-open。不带工具的请求恒放行。兜底重选路径不经 SmartRouter
// 行构建，须在此挡。白名单按稳定路由身份（逻辑渠道×协议）比对，渠道重建
// 重铸物理 UID 不影响判定。
func (r *racingRuns) toolWhitelistAllows(upstream *config.UpstreamConfig) bool {
	if !r.needsToolWhitelist || upstream == nil {
		return true
	}
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return true
	}
	kind := string(r.in.Kind)
	routes := cache.VerifiedToolCallRoutes(kind, true)
	if len(routes) == 0 {
		return true
	}
	return routes[config.ToolRouteIdentity(upstream, kind)]
}

// RunRacingAttempt 包装一次渠道尝试：竞速未武装时行为与直接调用闭包完全一致；
// 武装后主分支与影子分支竞速，返回实际服务请求的 selection 与结果。
// 结果的 AlsoFailedRoutes 由调用方并入 failedRoutes。
func RunRacingAttempt(
	c *gin.Context,
	trySelectedChannel TrySelectedChannelFunc,
	in RacingAttemptInput,
) (*scheduler.SelectionResult, MultiChannelAttemptResult) {
	hub := getRacingHub()
	if !shouldArmRacing(c, hub, in.CfgManager, in.Kind, in.Selection, in.HasImageContent) {
		return in.Selection, trySelectedChannel(c, in.Selection)
	}
	if in.Ctx == nil {
		in.Ctx = c.Request.Context()
	}

	gate := racing.NewGate()
	setBranchContext(c, gate, racing.RolePrimary, 0)

	behavior := hub.behaviorFor(resolveRequestCostPreference(c, in.CfgManager))
	family := racing.FamilyForModel(in.Model)
	stage := racing.StageStreamFirstContent
	floorMs := behavior.StreamFloorMs
	if !in.IsStream {
		stage = racing.StageNonStreamComplete
		floorMs = racing.NonStreamFloorMs
	}
	ceilingMs := racingStreamCeilingMs(&in, in.Selection.Upstream)
	if !in.IsStream {
		ceilingMs = racingNonStreamCeilingMs(&in, in.Selection.Upstream)
	}
	thresholdMs := hub.Registry.ThresholdMs(family, stage, floorMs, ceilingMs)

	primaryCost := racingEffectiveCostMultiplier(in.Selection.Upstream, in.Selection.ExecutionKeyIdentity)
	// 主分支同样经分支 writer 写出：真实客户端 writer 只被 claim 赢家触碰
	// （影子分支见 startShadow），分支间不再共享响应头 map。
	origWriter := c.Writer
	c.Writer = newRacingBranchWriter(origWriter)
	runs := &racingRuns{
		gate:               gate,
		hub:                hub,
		in:                 &in,
		trySelectedChannel: trySelectedChannel,
		behavior:           behavior,
		thresholdMs:        thresholdMs,
		needsToolWhitelist: BodyHasTools(GetEffectiveRequestBody(c, nil)),
		clientWriter:       origWriter,
		results:            make(map[int]MultiChannelAttemptResult),
		nextBranchID:       1,
		usedIdentities: map[string]bool{
			racingCandidateIdentity(in.Selection.Upstream.ChannelUID, in.Selection.ExecutionKeyIdentity, in.Selection.ExecutionModel): true,
		},
		usedChannels:  map[string]bool{racingChannelIdentity(in.Selection.Upstream, in.Selection.Route): true},
		usedRouteKeys: map[scheduler.ChannelRouteKey]bool{in.Selection.Route.Key(): true},
	}

	clientCtx := c.Request.Context()
	primaryBranchCtx, primaryCancel := context.WithCancel(clientCtx)
	gate.RegisterCancel(0, primaryCancel)

	// 主分支上游请求换绑分支 context（providers 从 c.Request.Context() 取消信号）。
	origRequest := c.Request
	c.Request = origRequest.WithContext(primaryBranchCtx)
	defer func() {
		c.Request = origRequest
		c.Writer = origWriter
		primaryCancel()
	}()

	// 阈值定时器：到期复核（客户端断开/已结算/主已出首字）后派影子。
	timer := time.AfterFunc(time.Duration(thresholdMs)*time.Millisecond, func() {
		select {
		case <-clientCtx.Done():
			return
		default:
		}
		if gate.Claimed() {
			return
		}
		if in.IsStream {
			if observer := GetStreamTimeoutObserver(c); observer.HasFirstContent() {
				return
			}
		}
		runs.spawnShadows(c, primaryCost)
	})

	// 主分支执行。
	primaryStartedAt := time.Now()
	result := trySelectedChannel(c, in.Selection)
	timer.Stop()
	// 止血只针对非赢家分支：影子已 claim 时固定 CancelExcept(0) 会误杀正在
	// 透传的赢家（客户端流被切断 context canceled）；无 claim（主分支真实失败）
	// 时保留在飞影子，由下方结算路径兜底接管。
	if claimedBy := gate.ClaimedBy(); claimedBy >= 0 {
		gate.CancelExcept(claimedBy)
	}

	// 主分支赢（或 headers 已发后完成的唯一分支）：等影子收尾，记录样本后返回。
	if result.Handled {
		runs.waitAllAndRecordStreamSamples(family)
		if !in.IsStream {
			recordRacingNonStreamSample(family, primaryStartedAt)
		}
		return in.Selection, result
	}

	// 主分支被影子抢走提交权：等影子完成，回拷分支 keys 后返回赢家结果。
	// 影子 claim 后又失败（无赢家）时并入其真实失败路由，推进外层 failover。
	if gate.Claimed() && gate.ClaimedBy() != 0 {
		runs.waitAllAndRecordStreamSamples(family)
		if win, winResult := runs.pickWinner(); win != nil {
			notifyPrimarySuperseded(c, in, result)
			copyBranchKeysBack(c, win.ginCtx)
			if !in.IsStream {
				recordRacingNonStreamSample(family, win.startedAt)
			}
			return win.selection, winResult
		}
		result.AlsoFailedRoutes = runs.failedRouteList()
		return in.Selection, result
	}

	// 主分支真实失败：等在飞影子结算，任一赢家接管；全败合并失败推进 failover。
	if runs.hasAny() {
		runs.waitAllAndRecordStreamSamples(family)
		if win, winResult := runs.pickWinner(); win != nil {
			copyBranchKeysBack(c, win.ginCtx)
			if !in.IsStream {
				recordRacingNonStreamSample(family, win.startedAt)
			}
			return win.selection, winResult
		}
		result.AlsoFailedRoutes = runs.failedRouteList()
		return in.Selection, result
	}
	if !in.IsStream {
		recordRacingNonStreamSample(family, primaryStartedAt)
	} else {
		recordRacingStreamSample(c, family)
	}
	return in.Selection, result
}

// notifyPrimarySuperseded 主尝试被影子取代时补记其 trace 终态（防悬空）。
func notifyPrimarySuperseded(c *gin.Context, in RacingAttemptInput, primaryResult MultiChannelAttemptResult) {
	notifyRoutingOutcome(in.Selection, buildRoutingOutcome(
		c, in.Selection, primaryResult, false, false,
		in.RequestStartedAt, time.Since(in.RequestStartedAt),
	))
}

// spawnShadows 派出影子分支（阈值定时器触发；幂等）。
func (r *racingRuns) spawnShadows(c *gin.Context, primaryCost float64) {
	r.mu.Lock()
	if r.spawned {
		r.mu.Unlock()
		return
	}
	r.spawned = true
	r.mu.Unlock()

	for len(r.snapshotRuns()) < r.behavior.MaxShadows {
		select {
		case <-r.in.Ctx.Done():
			return
		default:
		}
		if r.gate.Claimed() {
			return
		}
		if !r.hub.Sem.TryAcquire() {
			return
		}
		sel := r.nextShadowSelection(primaryCost)
		if sel == nil {
			r.hub.Sem.Release()
			RequestLogf(c, "[Racing] 阈值已到但无可用影子候选（缓存无跨渠道可行候选且路由重选无果），本次放弃竞速")
			return
		}
		r.startShadow(c, sel)
	}
}

// startShadow 启动一条影子分支（调用方已占用信号量额度）。
func (r *racingRuns) startShadow(c *gin.Context, sel *scheduler.SelectionResult) {
	r.mu.Lock()
	branchID := r.nextBranchID
	r.nextBranchID++
	r.mu.Unlock()

	shadowCtx, cancel := context.WithCancel(r.in.Ctx)
	r.gate.RegisterCancel(branchID, cancel)

	shadowC := c.Copy()
	shadowC.Request = c.Request.WithContext(shadowCtx)
	// gin 的 Copy() 不复制 Writer（副本 Writer 为 nil）。影子挂独立分支 writer：
	// pre-commit 写私有缓冲，claim 赢家 Commit 时桥接到真实客户端 writer，
	// 败者 Discard 后写出静默丢弃——真实 writer 全程只被赢家触碰。
	shadowC.Writer = newRacingBranchWriter(r.clientWriter)
	setBranchContext(shadowC, r.gate, racing.RoleShadow, branchID)

	run := &racingShadowRun{
		branchID:  branchID,
		selection: sel,
		ginCtx:    shadowC,
		cancel:    cancel,
		done:      make(chan MultiChannelAttemptResult, 1),
		startedAt: time.Now(),
	}
	r.mu.Lock()
	r.runs = append(r.runs, run)
	r.mu.Unlock()

	RequestLogf(c, "[Racing] 首字等待超阈值(%dms)，向候选 %s 派出影子分支 #%d", r.thresholdMs, sel.Route.Key().String(), branchID)
	// 慢证据信号二：竞速触发本身（首字超同家族 p90 阈值才触发），主组合记一次。
	// 主分支 key 在 attempt 内部轮转，此处从 selection 的 pin 身份反查当前 key。
	recordPrimaryRacingTriggerEvidence(c, r.in.Selection, r.in.Model)
	go func() {
		defer r.hub.Sem.Release()
		defer cancel()
		res := r.trySelectedChannel(shadowC, sel)
		run.done <- res
	}()
}

// nextShadowSelection 选取下一个影子候选：排名缓存优先（整渠道排除主渠道），
// 缓存不可用时回退调度器按已用路由重选。nil 表示无可用候选。
// 影子按渠道粒度去重：同渠道影子是同 provider 同队列的重复消耗（key 级对冲
// 已由 attempt 内部轮转覆盖），对延迟/可用性无实质改善，只放大流量与 429 风险。
func (r *racingRuns) nextShadowSelection(primaryCost float64) *scheduler.SelectionResult {
	r.mu.Lock()
	usedIdentities := make(map[string]bool, len(r.usedIdentities))
	for k := range r.usedIdentities {
		usedIdentities[k] = true
	}
	usedChannels := make(map[string]bool, len(r.usedChannels))
	for k := range r.usedChannels {
		usedChannels[k] = true
	}
	usedRouteKeys := make(map[scheduler.ChannelRouteKey]bool, len(r.usedRouteKeys))
	for k := range r.usedRouteKeys {
		usedRouteKeys[k] = true
	}
	r.mu.Unlock()

	// 路径一：SmartRouter 排名缓存（同渠道行由候选函数整渠道排除主渠道，
	// 影子间渠道去重在此处的 usedChannels 检查落地）。
	if r.hub.CandidateProvider != nil {
		cands := r.hub.CandidateProvider(r.in.Model, string(r.in.Kind))
		if len(cands) > 0 {
			picked := autopilot.RacingShadowCandidates(
				cands,
				r.in.Selection.Upstream.ChannelUID,
				r.in.Selection.ExecutionKeyIdentity,
				len(cands),
			)
			for _, cand := range picked {
				if usedChannels[cand.ChannelUID] {
					continue
				}
				identity := racingCandidateIdentity(cand.ChannelUID, cand.KeyIdentity, cand.ActualModel)
				if usedIdentities[identity] {
					continue
				}
				sel := r.buildSelectionFromCandidate(cand, primaryCost)
				if sel == nil {
					continue
				}
				r.mu.Lock()
				r.usedIdentities[identity] = true
				r.usedChannels[cand.ChannelUID] = true
				r.mu.Unlock()
				return sel
			}
			// 缓存非空但无可行候选（全部被硬约束过滤/身份重复/成本过滤）：
			// 落回路径二按路由重选，不静默放弃。
		}
	}

	// 路径二：调度器按已用路由重选（路由粒度回退）。循环跳过已用渠道与
	// 不可行路由，直到找到未用渠道或无可选；调度器回退到已排除路由时放弃
	// （防死循环）。
	failedRoutes := usedRouteKeys
	for {
		sel, err := r.in.Scheduler.SelectChannelWithOptions(r.in.Ctx, func() scheduler.SelectionOptions {
			opts := r.in.SelectionOptions
			opts.FailedRoutes = failedRoutes
			return opts
		}())
		if err != nil || sel == nil || sel.Upstream == nil {
			return nil
		}
		routeKey := sel.Route.Key()
		if failedRoutes[routeKey] {
			return nil
		}
		failedRoutes[routeKey] = true
		if usedChannels[racingChannelIdentity(sel.Upstream, sel.Route)] {
			continue
		}
		cfgSnapshot := r.in.CfgManager.GetConfig()
		if !cfgSnapshot.ResolveRacingPolicy(sel.Upstream) {
			continue
		}
		// 同路径一：调度器兜底重选可能返回软延迟/冷却渠道（last-resort），热渠道不派影子。
		if r.in.Scheduler.IsChannelRateLimitHot(r.in.Kind, sel.ChannelIndex, sel.Upstream, r.in.Model) {
			continue
		}
		// cost_first 的倍率过滤在回退路径同样生效：调度器按路由重选拿不到五元组，
		// 用渠道 CostMultiplier 近似（key 分组倍率未 pin 时不可知）。
		if r.behavior.CheapCandidateOnly {
			if racingEffectiveCostMultiplier(sel.Upstream, sel.ExecutionKeyIdentity) > primaryCost*0.5 {
				continue
			}
		}
		// 工具调用白名单路由排他（带工具请求）：兜底重选不经影子构建出口，
		// 须单独挡（语义同 buildSelectionFromCandidate 内的排他）。
		if !r.toolWhitelistAllows(sel.Upstream) {
			continue
		}
		r.mu.Lock()
		r.usedRouteKeys[routeKey] = true
		r.usedChannels[racingChannelIdentity(sel.Upstream, sel.Route)] = true
		r.mu.Unlock()
		return sel
	}
}

// buildSelectionFromCandidate 把五元组候选落为可执行的 SelectionResult。
// 渠道不存在/不参与竞速/成本过滤不通过返回 nil。
func (r *racingRuns) buildSelectionFromCandidate(cand autopilot.RoutingCandidate, primaryCost float64) *scheduler.SelectionResult {
	cfgSnapshot := r.in.CfgManager.GetConfig()
	upstream, index := findRacingChannelByUID(&cfgSnapshot, cand.ChannelUID, r.in.Kind)
	if upstream == nil {
		return nil
	}
	// 工具调用白名单路由排他（带工具请求）：排名缓存行由本请求以外的
	// 历史/并发排名产生，其 Selected 语义不含本请求的工具白名单约束，
	// 须在影子构建出口统一挡——本次执行协议上存在任一运行期验证路由时，
	// 影子不从非白名单路由派（伪工具标记方言的根治约束；该协议无白名单 fail-open）。
	if !r.toolWhitelistAllows(upstream) {
		return nil
	}
	if !cfgSnapshot.ResolveRacingPolicy(upstream) {
		return nil
	}
	// 限流热渠道不派影子：影子是真实上游请求，429 风暴中只会放大消耗
	// （排名缓存路径绕过调度器冷却过滤，必须在此补齐）。
	if r.in.Scheduler.IsChannelRateLimitHot(r.in.Kind, index, upstream, cand.ActualModel) {
		return nil
	}
	if r.behavior.CheapCandidateOnly {
		if racingEffectiveCostMultiplier(upstream, cand.KeyIdentity) > primaryCost*0.5 {
			return nil
		}
	}
	return &scheduler.SelectionResult{
		Upstream:             upstream,
		Route:                scheduler.ChannelRouteRef{Kind: string(r.in.Kind), Index: index, ChannelUID: cand.ChannelUID},
		ChannelIndex:         index,
		CandidateCount:       r.in.Selection.CandidateCount,
		ExecutionModel:       cand.ActualModel,
		ExecutionKeyIdentity: cand.KeyIdentity,
		// effort 跟随主请求实际档位（selection.ExecutionEffort）；候选行的 effort
		// 维来自排名缓存（可能是别的请求的），沿用会把 xhigh 主请求降成 medium 影子。
		ExecutionEffort: r.in.Selection.ExecutionEffort,
		Reason:          "racing_shadow",
	}
}

func racingCandidateIdentity(channelUID, keyIdentity, actualModel string) string {
	return channelUID + "|" + strings.TrimSpace(keyIdentity) + "|" + actualModel
}

// racingChannelIdentity 渠道粒度身份：优先稳定 ChannelUID，空 UID 回退路由键。
func racingChannelIdentity(upstream *config.UpstreamConfig, route scheduler.ChannelRouteRef) string {
	if upstream != nil && upstream.ChannelUID != "" {
		return upstream.ChannelUID
	}
	return route.Key().String()
}

// snapshotRuns 当前分支列表（副本）。
func (r *racingRuns) snapshotRuns() []*racingShadowRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	runs := make([]*racingShadowRun, len(r.runs))
	copy(runs, r.runs)
	return runs
}

// hasAny 是否派过影子。
func (r *racingRuns) hasAny() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.runs) > 0
}

// waitAllAndRecordStreamSamples 等待全部分支结束并收割结果；
// 流式分支出过首字即记入阈值窗口（含败者，消赢家偏差），非流式样本由赢家路径记。
func (r *racingRuns) waitAllAndRecordStreamSamples(family racing.Family) {
	for _, run := range r.snapshotRuns() {
		r.mu.Lock()
		_, alreadyDone := r.results[run.branchID]
		r.mu.Unlock()
		if alreadyDone {
			continue
		}
		res := <-run.done
		r.mu.Lock()
		r.results[run.branchID] = res
		r.mu.Unlock()
		// 仅真实渠道失败才计入 failedRoutes；竞速败出与被取消不污染路由排除。
		if !res.Handled && isRealBranchFailure(res.LastError) {
			r.mu.Lock()
			r.failedRouteKeys = append(r.failedRouteKeys, run.selection.Route.Key())
			r.mu.Unlock()
		}
		recordRacingStreamSample(run.ginCtx, family)
	}
}

// isRealBranchFailure 分支失败是否为真实渠道故障（区别于竞速败出/被取消）。
func isRealBranchFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, racing.ErrRacingSuperseded) || errors.Is(err, context.Canceled) {
		return false
	}
	return true
}

// pickWinner 返回已结算分支中第一个真正交付了响应的赢家（Handled 且无错误；
// 被取消的影子可能在内部轮转后以 Handled=true+err 收尾，不得入选）。
func (r *racingRuns) pickWinner() (*racingShadowRun, MultiChannelAttemptResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, run := range r.runs {
		if res, ok := r.results[run.branchID]; ok && res.Handled && res.LastError == nil {
			return run, res
		}
	}
	return nil, MultiChannelAttemptResult{}
}

// failedRouteList 全败场景下需要并入外层 failedRoutes 的影子路由。
func (r *racingRuns) failedRouteList() []scheduler.ChannelRouteKey {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]scheduler.ChannelRouteKey(nil), r.failedRouteKeys...)
}

// copyBranchKeysBack 影子赢家结算后把分支 gin keys 合并回主 context，
// 保证 responseText/lastUserMessage 等后处理读到赢家数据。
func copyBranchKeysBack(dst, src *gin.Context) {
	if dst == nil || src == nil {
		return
	}
	for key, value := range src.Keys {
		keyStr, ok := key.(string)
		if !ok || strings.HasPrefix(keyStr, "ccx.racing.") {
			continue
		}
		dst.Set(keyStr, value)
	}
}

// recordRacingStreamSample 流式样本：出过首字（首个有效内容）即记，无论胜负。
func recordRacingStreamSample(c *gin.Context, family racing.Family) {
	hub := getRacingHub()
	if hub == nil || hub.Registry == nil {
		return
	}
	if ms := GetStreamTimeoutObserver(c).FirstContentMs(); ms > 0 {
		hub.Registry.Record(family, racing.StageStreamFirstContent, ms)
	}
}

// recordRacingNonStreamSample 非流式样本：仅赢家完成时记分支总耗时。
func recordRacingNonStreamSample(family racing.Family, branchStartedAt time.Time) {
	hub := getRacingHub()
	if hub == nil || hub.Registry == nil {
		return
	}
	if ms := time.Since(branchStartedAt).Milliseconds(); ms > 0 {
		hub.Registry.Record(family, racing.StageNonStreamComplete, ms)
	}
}

// writeEchoMappingHeaders 写出/清除自动模型映射回显头。
// 竞速场景分支响应头经分支 writer 隔离（racingBranchWriter），无并发写竞争，
// 只有 claim 赢家的头会在 Commit 时并入真实客户端 writer。
func writeEchoMappingHeaders(c *gin.Context, cfgManager *config.ConfigManager, appliedMappedModel, actualAttemptModel, model string) {
	echoMapping := appliedMappedModel != "" && cfgManager.GetAutopilotRouting().ModelMapping.EchoMappedModel
	if echoMapping {
		c.Header("X-CCX-Mapped-Model", actualAttemptModel)
		c.Header("X-CCX-Original-Model", model)
		c.Header("X-CCX-Mapping-Source", "auto_resolve")
	} else {
		c.Writer.Header().Del("X-CCX-Mapped-Model")
		c.Writer.Header().Del("X-CCX-Original-Model")
		c.Writer.Header().Del("X-CCX-Mapping-Source")
	}
}
