package common_test

import (
	"fmt"
	"sort"
	"sync"
	"errors"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/racing"
	"github.com/BenedictKing/ccx/internal/ratelimit"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// newRacingGinContext 竞速测试专用 gin context：必须带真实 ResponseRecorder——
// 分支 writer 在 claim 赢家 Commit 时向真实 writer 桥接，nil recorder 会 panic。
func newRacingGinContext() *gin.Context {
	return newTestGinContext(httptest.NewRecorder())
}

// racingTestEnv 竞速编排测试环境：三渠道 + 全局竞速开启。
func racingTestEnv(t *testing.T, mutate func(cfg *config.Config)) affinityTestEnv {
	t.Helper()
	enabled := true
	cfg := config.Config{
		Racing: &config.GlobalRacingConfig{Enabled: &enabled},
		Upstream: []config.UpstreamConfig{
			{Name: "first", ChannelUID: "ch_first", BaseURL: "https://first.example.com", APIKeys: []string{"sk-first"}, Status: "active"},
			{Name: "second", ChannelUID: "ch_second", BaseURL: "https://second.example.com", APIKeys: []string{"sk-second"}, Status: "active"},
			{Name: "third", ChannelUID: "ch_third", BaseURL: "https://third.example.com", APIKeys: []string{"sk-third"}, Status: "active"},
		},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	env := newAffinityTestEnv(t, cfg)
	t.Cleanup(func() { common.SetRacingHub(nil) })
	return env
}

// installRacingHub 注入测试 hub：小阈值 floor + 可控行为表（绕过 2s 生产下限）。
func installRacingHub(t *testing.T, provider func(model, channelKind string) []autopilot.RoutingCandidate, behavior racing.Behavior) {
	t.Helper()
	hub := &common.RacingHub{
		Registry: racing.NewRegistry(),
		Sem:      racing.NewSemaphore(12),
		CandidateProvider: func(model, channelKind string) []autopilot.RoutingCandidate {
			if provider == nil {
				return nil
			}
			return provider(model, channelKind)
		},
	}
	hub.SetBehaviorOverrideForTest(func(string) racing.Behavior { return behavior })
	common.SetRacingHub(hub)
}

// racingBranchInput 单次分支调用的观测记录。
type racingBranchInput struct {
	channelUID string
	succeeded  bool
	superseded bool
}

// racingStubBranch 构造模拟渠道分支：等待 delay（可被分支取消打断）后
// 模拟提交闸门并按 succeed 返回；record 非空时记录每次调用结果。
func racingStubBranch(delay time.Duration, succeed bool, record func(racingBranchInput)) common.TrySelectedChannelFunc {
	return func(c *gin.Context, selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-c.Request.Context().Done():
			}
		}
		if !common.RacingClaimClientCommit(c) {
			if record != nil {
				record(racingBranchInput{channelUID: selection.Route.ChannelUID, superseded: true})
			}
			return common.MultiChannelAttemptResult{Route: selection.Route, Attempted: true, LastError: common.ErrRacingSuperseded}
		}
		if !succeed {
			if record != nil {
				record(racingBranchInput{channelUID: selection.Route.ChannelUID, succeeded: false})
			}
			return common.MultiChannelAttemptResult{Route: selection.Route, Attempted: true, LastError: errors.New("boom")}
		}
		if record != nil {
			record(racingBranchInput{channelUID: selection.Route.ChannelUID, succeeded: true})
		}
		return common.MultiChannelAttemptResult{Route: selection.Route, Handled: true, Attempted: true, SuccessKey: "sk-" + selection.Route.ChannelUID}
	}
}

func racingCandidateList(channelUIDs ...string) func(model, channelKind string) []autopilot.RoutingCandidate {
	return func(model, _ string) []autopilot.RoutingCandidate {
		// 主行 Selected=true（SmartRouter 语义：通过硬约束的可行候选，主调度选中行是其中之一）；
		// 影子行同样必须 Selected=true——不可行候选不得作影子。
		cands := []autopilot.RoutingCandidate{{ChannelUID: "ch_first", ActualModel: model, Selected: true}}
		for _, uid := range channelUIDs {
			cands = append(cands, autopilot.RoutingCandidate{ChannelUID: uid, ActualModel: model, Selected: true})
		}
		return cands
	}
}

func racingPrimarySelection(t *testing.T, env affinityTestEnv) *scheduler.SelectionResult {
	t.Helper()
	cfgSnapshot := env.scheduler.GetConfigManager().GetConfig()
	if len(cfgSnapshot.Upstream) == 0 {
		t.Fatal("配置中无渠道")
	}
	upstream := &cfgSnapshot.Upstream[0]
	return &scheduler.SelectionResult{
		Upstream:       upstream,
		Route:          scheduler.ChannelRouteRef{Kind: "messages", Index: 0, ChannelUID: upstream.ChannelUID},
		ChannelIndex:   0,
		CandidateCount: 3,
	}
}

func racingInput(env affinityTestEnv, selection *scheduler.SelectionResult) common.RacingAttemptInput {
	return common.RacingAttemptInput{
		EnvCfg:     &config.EnvConfig{},
		CfgManager: env.scheduler.GetConfigManager(),
		Scheduler:  env.scheduler,
		Kind:       scheduler.ChannelKindMessages,
		Model:      "test-model",
		IsStream:   true,
		Selection:  selection,
	}
}

func TestRunRacingAttemptDisabledWithoutHub(t *testing.T) {
	env := racingTestEnv(t, nil)
	selection := racingPrimarySelection(t, env)
	c := newRacingGinContext()

	called := false
	sel, result := common.RunRacingAttempt(c, func(_ *gin.Context, _ *scheduler.SelectionResult) common.MultiChannelAttemptResult {
		called = true
		return common.MultiChannelAttemptResult{Handled: true, SuccessKey: "ok"}
	}, racingInput(env, selection))
	if !called || !result.Handled || sel != selection {
		t.Fatalf("无 hub 时应透传主分支: called=%v handled=%v", called, result.Handled)
	}
}

func TestRunRacingAttemptDisabledByChannelPolicy(t *testing.T) {
	env := racingTestEnv(t, func(cfg *config.Config) {
		disabled := false
		cfg.Upstream[0].Racing = &config.ChannelRacingConfig{Enabled: &disabled}
	})
	installRacingHub(t, racingCandidateList("ch_second"), racing.Behavior{MaxShadows: 1, StreamFloorMs: 20})

	var calls atomic.Int32
	branch := func(_ *gin.Context, _ *scheduler.SelectionResult) common.MultiChannelAttemptResult {
		calls.Add(1)
		time.Sleep(80 * time.Millisecond)
		return common.MultiChannelAttemptResult{Handled: true, SuccessKey: "ok"}
	}

	selection := racingPrimarySelection(t, env)
	startedAt := time.Now()
	sel, result := common.RunRacingAttempt(newRacingGinContext(), branch, racingInput(env, selection))
	elapsed := time.Since(startedAt)

	if !result.Handled || sel != selection {
		t.Fatalf("渠道关闭竞速时应透传主分支: %+v", result)
	}
	if calls.Load() != 1 {
		t.Fatalf("不应派出影子分支, 调用数 %d", calls.Load())
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("透传路径不应等待阈值, 耗时 %v", elapsed)
	}
}

func TestRunRacingAttemptShadowWinsOverSlowPrimary(t *testing.T) {
	env := racingTestEnv(t, nil)
	installRacingHub(t, racingCandidateList("ch_second"), racing.Behavior{MaxShadows: 1, StreamFloorMs: 30})

	var superseded atomic.Bool
	primaryBranch := func(c *gin.Context, selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
		// 慢主：阻塞至影子 claim 后被取消（模拟上游读被打断）。
		select {
		case <-time.After(2 * time.Second):
		case <-c.Request.Context().Done():
		}
		if !common.RacingClaimClientCommit(c) {
			superseded.Store(true)
			return common.MultiChannelAttemptResult{Route: selection.Route, Attempted: true, LastError: common.ErrRacingSuperseded}
		}
		return common.MultiChannelAttemptResult{Route: selection.Route, Handled: true, SuccessKey: "sk-primary"}
	}
	shadowBranch := func(c *gin.Context, selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
		time.Sleep(10 * time.Millisecond)
		if !common.RacingClaimClientCommit(c) {
			return common.MultiChannelAttemptResult{Route: selection.Route, Attempted: true, LastError: common.ErrRacingSuperseded}
		}
		return common.MultiChannelAttemptResult{Route: selection.Route, Handled: true, SuccessKey: "sk-shadow"}
	}

	selection := racingPrimarySelection(t, env)
	startedAt := time.Now()
	sel, result := common.RunRacingAttempt(newRacingGinContext(), func(c *gin.Context, sel *scheduler.SelectionResult) common.MultiChannelAttemptResult {
		if sel.Route.ChannelUID == "ch_first" {
			return primaryBranch(c, sel)
		}
		return shadowBranch(c, sel)
	}, racingInput(env, selection))
	elapsed := time.Since(startedAt)

	if !superseded.Load() {
		t.Fatal("慢主分支应收到败出信号")
	}
	if sel.Route.ChannelUID != "ch_second" {
		t.Fatalf("影子应获胜, got %s", sel.Route.ChannelUID)
	}
	if result.SuccessKey != "sk-shadow" {
		t.Fatalf("应返回影子分支结果, got %+v", result)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("竞速应在影子完成后立即返回, 耗时 %v", elapsed)
	}
}

// 回归：影子 claim 获胜后主分支随即返回，编排器的止血不得取消赢家影子的
// 分支 ctx——真实流式场景 claim 只是透传开始，误杀会让客户端流中断
// （context canceled）。旧实现固定 gate.CancelExcept(0) 存在该缺陷。
func TestRunRacingAttemptShadowWinnerStreamNotCanceled(t *testing.T) {
	env := racingTestEnv(t, nil)
	installRacingHub(t, racingCandidateList("ch_second"), racing.Behavior{MaxShadows: 1, StreamFloorMs: 30})

	var winnerCtxKilled atomic.Bool
	primaryBranch := func(c *gin.Context, selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
		// 慢主：阻塞至影子 claim 后败出（与 TestRunRacingAttemptShadowWinsOverSlowPrimary 同）。
		select {
		case <-time.After(2 * time.Second):
		case <-c.Request.Context().Done():
		}
		if !common.RacingClaimClientCommit(c) {
			return common.MultiChannelAttemptResult{Route: selection.Route, Attempted: true, LastError: common.ErrRacingSuperseded}
		}
		return common.MultiChannelAttemptResult{Route: selection.Route, Handled: true, SuccessKey: "sk-primary"}
	}
	shadowBranch := func(c *gin.Context, selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
		time.Sleep(10 * time.Millisecond)
		if !common.RacingClaimClientCommit(c) {
			return common.MultiChannelAttemptResult{Route: selection.Route, Attempted: true, LastError: common.ErrRacingSuperseded}
		}
		// claim 后进入透传：主流式已败出返回，编排器此时做分支止血；
		// 赢家 ctx 在透传窗口内被 cancel 即为回归。
		select {
		case <-time.After(150 * time.Millisecond):
		case <-c.Request.Context().Done():
			winnerCtxKilled.Store(true)
		}
		return common.MultiChannelAttemptResult{Route: selection.Route, Handled: true, SuccessKey: "sk-shadow"}
	}

	selection := racingPrimarySelection(t, env)
	sel, result := common.RunRacingAttempt(newRacingGinContext(), func(c *gin.Context, sel *scheduler.SelectionResult) common.MultiChannelAttemptResult {
		if sel.Route.ChannelUID == "ch_first" {
			return primaryBranch(c, sel)
		}
		return shadowBranch(c, sel)
	}, racingInput(env, selection))

	if winnerCtxKilled.Load() {
		t.Fatal("赢家影子透传期间分支 ctx 被编排器误取消")
	}
	if sel.Route.ChannelUID != "ch_second" || result.SuccessKey != "sk-shadow" {
		t.Fatalf("应返回影子赢家结果: sel=%s result=%+v", sel.Route.ChannelUID, result)
	}
}

func TestRunRacingAttemptPrimaryWinsFastPath(t *testing.T) {
	env := racingTestEnv(t, nil)
	installRacingHub(t, racingCandidateList("ch_second"), racing.Behavior{MaxShadows: 1, StreamFloorMs: 10_000})

	selection := racingPrimarySelection(t, env)
	sel, result := common.RunRacingAttempt(newRacingGinContext(), racingStubBranch(5*time.Millisecond, true, nil), racingInput(env, selection))
	if sel != selection || !result.Handled || result.SuccessKey != "sk-ch_first" {
		t.Fatalf("阈值未到时主分支应直接获胜: %+v", result)
	}
}

func TestRunRacingAttemptBothFailMergesShadowRoutes(t *testing.T) {
	env := racingTestEnv(t, nil)
	installRacingHub(t, racingCandidateList("ch_second"), racing.Behavior{MaxShadows: 1, StreamFloorMs: 30})

	selection := racingPrimarySelection(t, env)
	// 主 50ms 失败（晚于 30ms 阈值，保证影子已被派出），影子立即失败。
	branch := func(c *gin.Context, sel *scheduler.SelectionResult) common.MultiChannelAttemptResult {
		if sel.Route.ChannelUID == "ch_first" {
			time.Sleep(50 * time.Millisecond)
		}
		if !common.RacingClaimClientCommit(c) {
			return common.MultiChannelAttemptResult{Route: sel.Route, Attempted: true, LastError: common.ErrRacingSuperseded}
		}
		return common.MultiChannelAttemptResult{Route: sel.Route, Attempted: true, LastError: errors.New("boom")}
	}

	sel, result := common.RunRacingAttempt(newRacingGinContext(), branch, racingInput(env, selection))
	if sel != selection {
		t.Fatal("双败时应返回主 selection")
	}
	if result.Handled || result.LastError == nil {
		t.Fatalf("双败应返回失败结果: %+v", result)
	}
	found := false
	for _, key := range result.AlsoFailedRoutes {
		if key.String() == "uid:ch_second" {
			found = true
		}
	}
	if !found {
		t.Fatalf("AlsoFailedRoutes 应包含影子路由: %+v", result.AlsoFailedRoutes)
	}
}

func TestRunRacingAttemptCostFirstFiltersCandidates(t *testing.T) {
	t.Run("cheap candidate allowed", func(t *testing.T) {
		env := racingTestEnv(t, func(cfg *config.Config) {
			cheap := 0.4
			cfg.Upstream[1].CostMultiplier = &cheap
		})
		installRacingHub(t, racingCandidateList("ch_second"),
			racing.Behavior{MaxShadows: 1, StreamFloorMs: 30, CheapCandidateOnly: true})

		var shadowUID atomic.Value
		branch := func(c *gin.Context, selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
			if selection.Route.ChannelUID != "ch_first" {
				shadowUID.Store(selection.Route.ChannelUID)
				time.Sleep(5 * time.Millisecond)
			} else {
				time.Sleep(300 * time.Millisecond)
			}
			if !common.RacingClaimClientCommit(c) {
				return common.MultiChannelAttemptResult{Route: selection.Route, Attempted: true, LastError: common.ErrRacingSuperseded}
			}
			return common.MultiChannelAttemptResult{Route: selection.Route, Handled: true, SuccessKey: "ok"}
		}

		common.RunRacingAttempt(newRacingGinContext(), branch, racingInput(env, racingPrimarySelection(t, env)))
		if got, _ := shadowUID.Load().(string); got != "ch_second" {
			t.Fatalf("cost_first 应允许便宜候选作影子, got %q", got)
		}
	})

	t.Run("expensive candidate filtered", func(t *testing.T) {
		env := racingTestEnv(t, func(cfg *config.Config) {
			expensive := 0.8
			cfg.Upstream[2].CostMultiplier = &expensive
		})
		installRacingHub(t, racingCandidateList("ch_third"),
			racing.Behavior{MaxShadows: 1, StreamFloorMs: 30, CheapCandidateOnly: true})

		var shadowSeen atomic.Bool
		branch := func(c *gin.Context, selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
			if selection.Route.ChannelUID != "ch_first" {
				shadowSeen.Store(true)
			}
			time.Sleep(80 * time.Millisecond)
			if !common.RacingClaimClientCommit(c) {
				return common.MultiChannelAttemptResult{Route: selection.Route, Attempted: true, LastError: common.ErrRacingSuperseded}
			}
			return common.MultiChannelAttemptResult{Route: selection.Route, Handled: true, SuccessKey: "ok"}
		}

		_, result := common.RunRacingAttempt(newRacingGinContext(), branch, racingInput(env, racingPrimarySelection(t, env)))
		if !result.Handled {
			t.Fatalf("主分支应完成: %+v", result)
		}
		if shadowSeen.Load() {
			t.Fatal("cost_first 应回退过贵候选，不应派出影子")
		}
	})
}

// 限流热渠道不派影子（2026-09-09 生产观测：seekai 429 风暴中 shadow 把单请求
// 放大成多条真实上游请求）。排名缓存路径绕过调度器冷却过滤，候选过滤必须兜底。
func TestRunRacingAttemptSkipsRateLimitHotCandidate(t *testing.T) {
	env := racingTestEnv(t, nil)
	installRacingHub(t, racingCandidateList("ch_second", "ch_third"), racing.Behavior{MaxShadows: 1, StreamFloorMs: 30})

	// ch_second（索引 1）渠道级冷却：影子应跳过它落到 ch_third
	env.scheduler.SetRateLimitManager(ratelimit.NewManager())
	env.scheduler.MarkChannelCooldown(scheduler.ChannelKindMessages, 1, time.Minute)

	var shadowUID atomic.Value
	branch := func(c *gin.Context, selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
		if selection.Route.ChannelUID != "ch_first" {
			shadowUID.Store(selection.Route.ChannelUID)
			time.Sleep(10 * time.Millisecond)
		} else {
			// 慢主：阻塞至影子 claim 后被取消
			select {
			case <-time.After(2 * time.Second):
			case <-c.Request.Context().Done():
			}
		}
		if !common.RacingClaimClientCommit(c) {
			return common.MultiChannelAttemptResult{Route: selection.Route, Attempted: true, LastError: common.ErrRacingSuperseded}
		}
		return common.MultiChannelAttemptResult{Route: selection.Route, Handled: true, SuccessKey: "sk-" + selection.Route.ChannelUID}
	}

	_, result := common.RunRacingAttempt(newRacingGinContext(), branch, racingInput(env, racingPrimarySelection(t, env)))
	if result.SuccessKey != "sk-ch_third" {
		t.Fatalf("影子应落到未冷却的 ch_third 并获胜: %+v", result)
	}
	if got, _ := shadowUID.Load().(string); got != "ch_third" {
		t.Fatalf("限流热渠道应被跳过, got %q", got)
	}
}

// 影子按渠道粒度去重（2026-09-13 生产观测：白名单排他下 3 个影子全打同一 ark
// 渠道同 key 同模型——同 provider 同队列的重复消耗，无延迟/可用性多样性收益，
// 只放大流量与 429 风险）。
func TestRunRacingAttemptShadowsChannelDedup(t *testing.T) {
	recordBranch := func(shadowCalls *[]string, mu *sync.Mutex) common.TrySelectedChannelFunc {
		return func(c *gin.Context, selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
			uid := selection.Route.ChannelUID
			if uid == "ch_first" {
				// 慢主：阻塞至影子 claim 后被取消
				select {
				case <-time.After(2 * time.Second):
				case <-c.Request.Context().Done():
				}
			} else {
				mu.Lock()
				*shadowCalls = append(*shadowCalls, uid)
				mu.Unlock()
				// 影子延迟须明显长于派出循环（瞬时连发），避免首个影子
				// 提前 claim 终止后续派出，干扰去重计数。
				select {
				case <-time.After(200 * time.Millisecond):
				case <-c.Request.Context().Done():
				}
			}
			if !common.RacingClaimClientCommit(c) {
				return common.MultiChannelAttemptResult{Route: selection.Route, Attempted: true, LastError: common.ErrRacingSuperseded}
			}
			return common.MultiChannelAttemptResult{Route: selection.Route, Handled: true, SuccessKey: "sk-" + uid}
		}
	}

	t.Run("同渠道多key行只派一个影子", func(t *testing.T) {
		env := racingTestEnv(t, nil)
		installRacingHub(t, func(model, _ string) []autopilot.RoutingCandidate {
			return []autopilot.RoutingCandidate{
				{ChannelUID: "ch_first", ActualModel: model, Selected: true},
				{ChannelUID: "ch_second", KeyIdentity: "k1", ActualModel: model, Selected: true},
				{ChannelUID: "ch_second", KeyIdentity: "k2", ActualModel: model, Selected: true},
				{ChannelUID: "ch_third", KeyIdentity: "k3", ActualModel: model, Selected: true},
			}
		}, racing.Behavior{MaxShadows: 3, StreamFloorMs: 30})

		var mu sync.Mutex
		var shadowCalls []string
		_, result := common.RunRacingAttempt(newRacingGinContext(), recordBranch(&shadowCalls, &mu), racingInput(env, racingPrimarySelection(t, env)))
		if !result.Handled {
			t.Fatalf("应有赢家: %+v", result)
		}
		sort.Strings(shadowCalls)
		if fmt.Sprint(shadowCalls) != "[ch_second ch_third]" {
			t.Fatalf("影子应按渠道去重为 [ch_second ch_third], got %v", shadowCalls)
		}
	})

	t.Run("无跨渠道候选时不重复打同一渠道", func(t *testing.T) {
		// ch_third 禁用：缓存与调度器重选的可行集都只剩 ch_second 一个跨渠道候选
		env := racingTestEnv(t, func(cfg *config.Config) {
			cfg.Upstream[2].Status = "disabled"
		})
		installRacingHub(t, func(model, _ string) []autopilot.RoutingCandidate {
			return []autopilot.RoutingCandidate{
				{ChannelUID: "ch_first", ActualModel: model, Selected: true},
				{ChannelUID: "ch_second", KeyIdentity: "k1", ActualModel: model, Selected: true},
				{ChannelUID: "ch_second", KeyIdentity: "k2", ActualModel: model, Selected: true},
			}
		}, racing.Behavior{MaxShadows: 3, StreamFloorMs: 30})

		var mu sync.Mutex
		var shadowCalls []string
		_, result := common.RunRacingAttempt(newRacingGinContext(), recordBranch(&shadowCalls, &mu), racingInput(env, racingPrimarySelection(t, env)))
		if !result.Handled {
			t.Fatalf("应有赢家: %+v", result)
		}
		if fmt.Sprint(shadowCalls) != "[ch_second]" {
			t.Fatalf("仅一个跨渠道候选时只应派一个影子, got %v", shadowCalls)
		}
	})

	t.Run("兜底重选路径同样按渠道去重", func(t *testing.T) {
		env := racingTestEnv(t, nil)
		// 排名缓存为空 → 全部走路径二调度器重选；调度器按 priority 依次返回
		// ch_second、ch_third，第三个影子无未用渠道可派而放弃。
		installRacingHub(t, nil, racing.Behavior{MaxShadows: 3, StreamFloorMs: 30})

		var mu sync.Mutex
		var shadowCalls []string
		_, result := common.RunRacingAttempt(newRacingGinContext(), recordBranch(&shadowCalls, &mu), racingInput(env, racingPrimarySelection(t, env)))
		if !result.Handled {
			t.Fatalf("应有赢家: %+v", result)
		}
		sort.Strings(shadowCalls)
		if fmt.Sprint(shadowCalls) != "[ch_second ch_third]" {
			t.Fatalf("路径二影子应分散到不同渠道 [ch_second ch_third], got %v", shadowCalls)
		}
	})
}
