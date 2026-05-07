// Package wire —— PR3 T8a/T8b/T8c/T8d wiring helper.
//
// 4 个 handler（messages / chat / responses / gemini）切流量到 PR1 pipeline 时
// 共享的胶水代码：
//
//   - LBOutboundAdapter：装饰 PR1 outbound，把 ChannelScheduler.SelectChannel
//     的结果通过 adapters.SetUpstreamBinding 喂给底层 outbound，并维护
//     RecordActiveConnDelta / RecordTPS / UsageStore.Append 等 LB 指标写入。
//   - BuildPipelineOpts：组装 PauseRule + KeyFailure middleware 与 retry 配置。
//
// 该 helper 单独成包是因为 pipeline/middleware 已反向依赖 internal/handlers/common
// （key failover / pause rule 复用 common.ShouldBlacklistKey 等），把 wire 放在
// common 下会形成 import cycle。新增包既保留共享语义，又避开循环。
//
// 使用范式（T8b/c/d 直接 copy）：
//
//	attemptInfo := &middleware.AttemptInfo{APIType: "Messages"}
//	ctx := middleware.WithAttemptInfo(c.Request.Context(), attemptInfo)
//	common.SetLBAPIType(c, "messages")
//	common.SetLBMetricsManager(c, mm)
//
//	in := messages.NewInbound()
//	out := messages.NewOutbound()
//	lb := wire.NewLBOutboundAdapter(out, channelScheduler, scheduler.ChannelKindMessages, startTime)
//	lb.AttemptInfo = attemptInfo
//	lb.LBMetricsMgr = mm
//	lb.UsageStore = globalUsageStore
//	lb.PriceLoader = globalPriceLoader
//	lb.UserID = userID
//	lb.Model = claudeReq.Model
//
//	p := factory.Pipeline(in, lb, wire.BuildPipelineOpts(cfgManager, lb)...)
//	result, err := p.Process(ctx, c.Request, bodyBytes)
//	defer lb.Finalize(ctx, err == nil, errCode, claudeReq.Model, llmUsage, durationMs)
package wire

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/adapters"
	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/pipeline"
	pipelinemw "github.com/BenedictKing/ccx/internal/pipeline/middleware"
	"github.com/BenedictKing/ccx/internal/pricing"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/usage"
	"github.com/shopspring/decimal"
)

// SchedulerView 抽象 ChannelScheduler 在 wire 路径上实际依赖的能力，
// 便于单测注入 fake；*scheduler.ChannelScheduler 自动满足该接口。
type SchedulerView interface {
	SelectChannel(
		ctx context.Context,
		userID string,
		failedChannels map[int]bool,
		kind scheduler.ChannelKind,
		model string,
		routePrefix string,
	) (*scheduler.SelectionResult, error)
}

// MetricsRecorder 抽象 metrics.MetricsManager 上 LBOutboundAdapter 实际依赖的写端，
// 便于单测注入 fake；*metrics.MetricsManager 自动满足该接口。
//
// 写端分两组：
//   - LB 层（channelKey 维度）：RecordActiveConnDelta / RecordTPS / RecordCost。
//   - per-key 层（baseURL + apiKey + serviceType 维度）：RecordSuccessWithUsage /
//     RecordFailure，对应 KeyMetrics 的成功/失败计数；GetKeyHistoricalStats
//     等查询依赖该路径。
type MetricsRecorder interface {
	RecordActiveConnDelta(channelKey string, delta int64)
	RecordTPS(channelKey string, totalTokens int64, durationMs int64)
	RecordCost(channelKey string, cost *decimal.Decimal, inputTokens, outputTokens, cacheReadTokens, cacheCreateTokens int64)
	RecordSuccessWithUsage(baseURL, apiKey, serviceType string, usage *types.Usage)
	RecordFailure(baseURL, apiKey, serviceType string)
}

// LBOutboundAdapter 装饰 PR1 outbound：在每次 NextChannel 时调度 channel/key，
// 并在 Finalize 时落 LB 指标 + UsageStore。
//
// 字段语义：
//
//   - Inner            被装饰的 PR1 outbound，pipeline 主循环只看到本 adapter。
//   - Scheduler        渠道调度器；NextChannel 通过 SelectChannel 获取下一渠道。
//   - Kind             scheduler.ChannelKind（messages / chat / responses / gemini / images）。
//   - UserID           用于 Trace 亲和性的会话 ID（可空）。
//   - Model            llm.Request.Model，用于模型兼容过滤。
//   - RoutePrefix      路由前缀，用于 SelectChannel 路由前缀过滤。
//   - AttemptInfo      指针；NextChannel 在选定 channel/key 后原地写入，供
//                      pipeline middleware 读取（T5 契约）。
//   - LBMetricsMgr     LB 指标管理器；NextChannel +1 ActiveConn，Finalize -1。
//   - UsageStore       NDJSON 落账；Finalize 调用 Append。允许 nil。
//   - PriceLoader      价格表；Finalize 通过它做成本计算。允许 nil。
//
// failedChannels / requestStart / channelKey 等是 attempt 级状态，由 NextChannel
// / Finalize 维护，调用方不应直接读写。
type LBOutboundAdapter struct {
	Inner       pipeline.Outbound
	Scheduler   SchedulerView
	Kind        scheduler.ChannelKind
	UserID      string
	Model       string
	RoutePrefix string
	Request     *llm.Request

	AttemptInfo  *pipelinemw.AttemptInfo
	LBMetricsMgr MetricsRecorder
	UsageStore   usage.UsageStore
	PriceLoader  *pricing.Loader

	requestStart    time.Time
	failedChannels  map[int]bool
	currentChannel  int
	currentKey      string
	currentBaseURL  string
	currentUpstream *config.UpstreamConfig
	// failedKeys 记录当前 channel 内已失败/已轮换过的 API key，
	// 由 PrepareForRetry 在轮换前累加；NextChannel 切到新 channel 时重置。
	failedKeys map[string]bool
	finalized  bool
}

// 编译期断言：LBOutboundAdapter 满足 pipeline.Outbound + Retryable + ChannelRetryable。
//
// ChannelRetryable 让 SSE error event / 临时上游错误优先在 *同 channel* 内
// 轮换下一个 API key 重试；当前 channel 的 key 全部耗尽后，pipeline 主循环
// 才回退到 Retryable.NextChannel 切到下一个 channel。
var (
	_ pipeline.Outbound         = (*LBOutboundAdapter)(nil)
	_ pipeline.Retryable        = (*LBOutboundAdapter)(nil)
	_ pipeline.ChannelRetryable = (*LBOutboundAdapter)(nil)
)

// NewLBOutboundAdapter 构造 adapter；inner / scheduler 不可为 nil。
//
// requestStart 用于 RecordTPS 计算耗时；建议传 handler 入口的 startTime。
func NewLBOutboundAdapter(
	inner pipeline.Outbound,
	sched SchedulerView,
	kind scheduler.ChannelKind,
	requestStart time.Time,
) *LBOutboundAdapter {
	if inner == nil {
		panic("wire.NewLBOutboundAdapter: inner outbound must not be nil")
	}
	if sched == nil {
		panic("wire.NewLBOutboundAdapter: scheduler must not be nil")
	}
	if requestStart.IsZero() {
		requestStart = time.Now()
	}
	return &LBOutboundAdapter{
		Inner:          inner,
		Scheduler:      sched,
		Kind:           kind,
		requestStart:   requestStart,
		failedChannels: make(map[int]bool),
		currentChannel: -1,
	}
}

// TransformRequest 透传到 Inner，前置确保 llm.Request.Metadata 已写入 upstream binding。
func (a *LBOutboundAdapter) TransformRequest(ctx context.Context, req *llm.Request) (*http.Request, []byte, error) {
	a.Request = req
	return a.Inner.TransformRequest(ctx, req)
}

// TransformResponse 透传到 Inner；把 *llm.Request 注入 ctx 以便 inner 的
// cross-format 路径读取 Metadata 上的 gin.Context / upstream。
func (a *LBOutboundAdapter) TransformResponse(ctx context.Context, resp *http.Response) (*llm.Response, error) {
	if a.Request != nil {
		ctx = adapters.WithRequestContext(ctx, a.Request)
	}
	return a.Inner.TransformResponse(ctx, resp)
}

// TransformStream 透传到 Inner；与 TransformResponse 同理，把 *llm.Request 注入 ctx。
func (a *LBOutboundAdapter) TransformStream(ctx context.Context, resp *http.Response) llm.Stream[*llm.Response] {
	if a.Request != nil {
		ctx = adapters.WithRequestContext(ctx, a.Request)
	}
	return a.Inner.TransformStream(ctx, resp)
}

// HasMoreChannels 实现 pipeline.Retryable.HasMoreChannels。
//
// 由于 ChannelScheduler 的可用候选集合随 failedChannels 动态变化，本方法
// 不预先穷举候选；返回 true 让 pipeline.Process 调用 NextChannel，由后者
// 决定是否真的还有可选 channel。NextChannel 在无候选时返回
// pipeline.ErrChannelExhausted，主循环据此终止。
func (a *LBOutboundAdapter) HasMoreChannels() bool {
	return a.Scheduler != nil
}

// NextChannel 实现 pipeline.Retryable.NextChannel：
//
//  1. 若已选过 channel，先把当前 channel 标记为 failed（pipeline 进入下一轮意味着上一次失败）。
//  2. 调 Scheduler.SelectChannel 拿 SelectionResult；无候选时返回 pipeline.ErrChannelExhausted。
//  3. 更新 AttemptInfo（ChannelIndex / APIKey / Upstream）+ Request.Metadata（adapters.SetUpstreamBinding）。
//  4. 累加 RecordActiveConnDelta(+1)，触发 LB 指标 ActiveConn 上升。
//
// 注意：NextChannel 不直接 select API key —— SelectChannel 仅返回 channel，
// key 由 ConfigManager.GetNextAPIKeyForUser 在 outbound TransformRequest
// 之前选好；T8a 阶段 messages handler 通过 adapters.SetUpstreamBinding
// 把 (channel, key) 一次性绑定。
func (a *LBOutboundAdapter) NextChannel(ctx context.Context) error {
	if a.Scheduler == nil {
		return pipeline.ErrChannelExhausted
	}
	if a.currentChannel >= 0 {
		a.failedChannels[a.currentChannel] = true
	}

	sel, err := a.Scheduler.SelectChannel(ctx, a.UserID, a.failedChannels, a.Kind, a.Model, a.RoutePrefix)
	if err != nil || sel == nil || sel.Upstream == nil {
		if err == nil {
			err = pipeline.ErrChannelExhausted
		}
		return err
	}

	apiKey := ""
	if len(sel.Upstream.APIKeys) > 0 {
		apiKey = sel.Upstream.APIKeys[0]
	}
	baseURL := sel.Upstream.BaseURL
	if urls := sel.Upstream.GetAllBaseURLs(); len(urls) > 0 {
		baseURL = urls[0]
	}

	if a.AttemptInfo != nil {
		a.AttemptInfo.ChannelIndex = sel.ChannelIndex
		a.AttemptInfo.APIKey = apiKey
		a.AttemptInfo.Upstream = sel.Upstream
	}

	if a.Request != nil {
		adapters.SetUpstreamBinding(a.Request, sel.Upstream, apiKey, baseURL)
	}

	a.currentChannel = sel.ChannelIndex
	a.currentKey = apiKey
	a.currentBaseURL = baseURL
	a.currentUpstream = sel.Upstream
	// 切到新 channel：清空 same-channel key 黑名单，让新 channel 的 key 池
	// 从空状态开始；保留 failedChannels（channel 维度）。
	a.failedKeys = nil

	if a.LBMetricsMgr != nil && sel.Upstream.Name != "" {
		channelKey := metrics.BuildLBChannelKey(string(a.Kind), sel.Upstream.Name)
		a.LBMetricsMgr.RecordActiveConnDelta(channelKey, +1)
	}
	log.Printf("[Wire-NextChannel] kind=%s channel=[%d] %s reason=%s",
		a.Kind, sel.ChannelIndex, sel.Upstream.Name, sel.Reason)
	return nil
}

// CanRetry 实现 pipeline.ChannelRetryable.CanRetry。
//
// 返回 true 表示当前 channel 内仍有未尝试的 healthy API key；pipeline 主循环
// 将调用 PrepareForRetry 完成 key 轮换，然后在同 channel 上发起下一次 attempt。
//
// 触发条件（任一）：
//   - SSE error event 帧（pipeline.ErrUpstreamStreamError）
//   - 上游 5xx / 429 等可重试 HTTP 错误（保守地接受所有非 nil 错误，
//     由 PauseRule / KeyFailure middleware 已经在前置层把不可重试错误拦走）
//
// pickNextHealthyKey 在 PrepareForRetry 中真正执行；CanRetry 仅做 fast-fail
// 的预检：upstream 为 nil 或没有可用的下一个 key 时直接返回 false，让
// pipeline 走 NextChannel 路径。
func (a *LBOutboundAdapter) CanRetry(err error) bool {
	if err == nil {
		return false
	}
	if a.AttemptInfo == nil || a.AttemptInfo.Upstream == nil {
		return false
	}
	if a.currentUpstream == nil {
		return false
	}
	return a.pickNextHealthyKey() != ""
}

// PrepareForRetry 实现 pipeline.ChannelRetryable.PrepareForRetry：
//
//  1. 把当前 key 标记到 failedKeys，防止下一轮再选到。
//  2. 在 currentUpstream.APIKeys 内挑下一个 healthy key（跳过 failedKeys）。
//  3. 同 channel 不更新 RecordActiveConnDelta（连接计数已在 NextChannel +1）；
//     仅刷新 AttemptInfo + adapters.SetUpstreamBinding 让下一次
//     TransformRequest 用新 key。
//
// 找不到候选时返回 pipeline.ErrSameChannelExhausted，让 pipeline 主循环回退
// 到 NextChannel 切 channel。
func (a *LBOutboundAdapter) PrepareForRetry(ctx context.Context) error {
	if a.AttemptInfo == nil || a.AttemptInfo.Upstream == nil || a.currentUpstream == nil {
		return pipeline.ErrSameChannelExhausted
	}

	if a.failedKeys == nil {
		a.failedKeys = make(map[string]bool)
	}
	if a.currentKey != "" {
		a.failedKeys[a.currentKey] = true
	}

	nextKey := a.pickNextHealthyKey()
	if nextKey == "" {
		return pipeline.ErrSameChannelExhausted
	}

	a.currentKey = nextKey
	a.AttemptInfo.APIKey = nextKey
	if a.Request != nil {
		adapters.SetUpstreamBinding(a.Request, a.currentUpstream, nextKey, a.currentBaseURL)
	}

	log.Printf("[Wire-NextKey] kind=%s channel=[%d] %s rotated to next key (failed=%d)",
		a.Kind, a.currentChannel, a.currentUpstream.Name, len(a.failedKeys))
	return nil
}

// pickNextHealthyKey 在 currentUpstream.APIKeys 中找下一个未被本次 attempt
// failedKeys 标失败的 key；找不到返回空字符串。
//
// 简化策略：仅按 in-memory failedKeys 过滤；不查 ConfigManager 的全局黑名单
// （那已由 KeyFailure middleware 在 attempt 完成后异步落账，对本次 same-channel
// 重试不影响 —— 即使选到一个全局黑名单 key，下次请求依然会被 ConfigManager
// 跳过）。这与 axonhub 的实现一致：retry 路径只关心当次 attempt 已失败的 key。
func (a *LBOutboundAdapter) pickNextHealthyKey() string {
	if a.currentUpstream == nil {
		return ""
	}
	for _, k := range a.currentUpstream.APIKeys {
		if k == "" {
			continue
		}
		if a.failedKeys != nil && a.failedKeys[k] {
			continue
		}
		if k == a.currentKey {
			// 当前正在用且未被加入 failedKeys：仅在主动 PrepareForRetry 标失败前
			// 出现，跳过避免重复。
			continue
		}
		return k
	}
	return ""
}

// MarkFailed 显式把指定 channel 标记为本次请求已失败。
//
// 主要用于测试或外部失败注入；pipeline.Process 自身在 NextChannel 二次调用
// 时会自动把上一轮 channel 累加到 failedChannels（见 NextChannel 实现），
// 故正常路径下调用方无需显式调用本方法。
func (a *LBOutboundAdapter) MarkFailed(channelIndex int) {
	if a.failedChannels == nil {
		a.failedChannels = make(map[int]bool)
	}
	a.failedChannels[channelIndex] = true
}

// FailedChannels 返回当前 attempt 已被标记 failed 的 channel 索引集合（拷贝），
// 仅供测试 / 观测使用。
func (a *LBOutboundAdapter) FailedChannels() map[int]bool {
	out := make(map[int]bool, len(a.failedChannels))
	for k, v := range a.failedChannels {
		out[k] = v
	}
	return out
}

// CurrentChannelIndex 返回当前已选 channel 索引；尚未选过返回 -1。
func (a *LBOutboundAdapter) CurrentChannelIndex() int { return a.currentChannel }

// Finalize 在 handler 决定收尾本次请求（成功或彻底失败）时调用。
//
// 行为：
//   - RecordActiveConnDelta(-1) 释放当前 channel 的活跃计数；
//   - success && tokens>0 时调 RecordTPS（按 PR3 T1 口径，仅成功路径写入）；
//   - PriceLoader.GetPrice + pricing.Calculate 计算总成本与 cost items；
//   - 拼装 UsageRecord 调 UsageStore.Append（snake_case JSON 由 record.go 处理）。
//
// 任意组件 nil 时跳过对应步骤，不返回错误。多次调用幂等（仅首次生效）。
func (a *LBOutboundAdapter) Finalize(
	ctx context.Context,
	success bool,
	errCode string,
	model string,
	llmUsage llm.Usage,
	durationMs int64,
) {
	if a == nil || a.finalized {
		return
	}
	a.finalized = true

	channelKey := ""
	if a.currentUpstream != nil && a.currentUpstream.Name != "" {
		channelKey = metrics.BuildLBChannelKey(string(a.Kind), a.currentUpstream.Name)
	}

	if a.LBMetricsMgr != nil && channelKey != "" {
		a.LBMetricsMgr.RecordActiveConnDelta(channelKey, -1)
	}

	totalTokens := int64(llmUsage.Inner.InputTokens) +
		int64(llmUsage.Inner.OutputTokens) +
		int64(llmUsage.Inner.CacheReadInputTokens) +
		int64(llmUsage.Inner.CacheCreationInputTokens)
	if totalTokens <= 0 {
		// fallback：取 OpenAI 口径 PromptTokens + CompletionTokens
		totalTokens = int64(llmUsage.Inner.PromptTokens) + int64(llmUsage.Inner.CompletionTokens)
	}

	if success && totalTokens > 0 && durationMs > 0 && a.LBMetricsMgr != nil && channelKey != "" {
		a.LBMetricsMgr.RecordTPS(channelKey, totalTokens, durationMs)
	}

	// per-key KeyMetrics 写入：使用 (baseURL, apiKey, serviceType) 三元组定位，
	// 对应 metrics.MetricsManager.GetKeyHistoricalStats 等查询路径。任一字段为空
	// 时跳过（避免落到匿名 bucket）。
	if a.LBMetricsMgr != nil && a.currentUpstream != nil && a.currentKey != "" {
		baseURL := a.currentUpstream.BaseURL
		if a.currentBaseURL != "" {
			baseURL = a.currentBaseURL
		}
		serviceType := a.currentUpstream.ServiceType
		if baseURL != "" {
			if success {
				a.LBMetricsMgr.RecordSuccessWithUsage(baseURL, a.currentKey, serviceType, &llmUsage.Inner)
			} else {
				a.LBMetricsMgr.RecordFailure(baseURL, a.currentKey, serviceType)
			}
		}
	}

	if a.UsageStore == nil {
		return
	}

	rec := usage.UsageRecord{
		Timestamp:                  time.Now(),
		ChannelID:                  a.currentChannel,
		ModelID:                    model,
		Format:                     string(llmUsage.Format),
		InputTokens:                int64(llmUsage.Inner.InputTokens),
		OutputTokens:               int64(llmUsage.Inner.OutputTokens),
		CacheReadInputTokens:       int64(llmUsage.Inner.CacheReadInputTokens),
		CacheCreationInputTokens:   int64(llmUsage.Inner.CacheCreationInputTokens),
		CacheCreationInputTokens5m: int64(llmUsage.Inner.CacheCreation5mInputTokens),
		CacheCreationInputTokens1h: int64(llmUsage.Inner.CacheCreation1hInputTokens),
		TotalTokens:                totalTokens,
		DurationMs:                 durationMs,
		Success:                    success,
		ErrorCode:                  errCode,
	}
	if a.currentUpstream != nil {
		rec.ChannelName = a.currentUpstream.Name
	}
	if a.currentKey != "" {
		rec.APIKeyMasked = usage.MaskAPIKey(a.currentKey)
	}

	if a.PriceLoader != nil && model != "" {
		if price, ok := a.PriceLoader.GetPrice(model); ok {
			total, items, calcErr := pricing.Calculate(llmUsage.Inner, price)
			if calcErr == nil {
				rec.TotalCost = &total
				rec.CostItems = items
				rec.PriceVersion = a.PriceLoader.Version()
				// PR3 T9: 推送 cost / token 增量到 dashboard 聚合面。
				// nil-safe：LBMetricsMgr 可能为 nil，channelKey 也可能为空。
				if a.LBMetricsMgr != nil && channelKey != "" {
					a.LBMetricsMgr.RecordCost(
						channelKey,
						&total,
						int64(llmUsage.Inner.InputTokens),
						int64(llmUsage.Inner.OutputTokens),
						int64(llmUsage.Inner.CacheReadInputTokens),
						int64(llmUsage.Inner.CacheCreationInputTokens),
					)
				}
			} else {
				log.Printf("[Wire-Finalize] 价格计算失败: model=%s err=%v", model, calcErr)
			}
		}
	}

	if err := a.UsageStore.Append(ctx, rec); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("[Wire-Finalize] usage 落账失败: model=%s err=%v", model, err)
	}
}

// BuildPipelineOpts 返回 4 个 handler 通用的 pipeline.Option 列表：
//   - WithMiddlewares(pauseMW, keyMW, sseErrMW)：PauseRule 必须先于 KeyFailure（T5 契约）。
//   - WithEmptyResponseDetection()：上游空流时触发 retry。
//   - WithRetry(maxChannelRetries=4, maxSameChannelRetries=8, retryDelay=0)：
//     same-channel 上限设为 8，让 LBOutboundAdapter 的 ChannelRetryable
//     能在当前 channel 内最多轮换 8 个 API key（覆盖典型多 key 配置）；
//     pipeline.PrepareForRetry 返回 ErrSameChannelExhausted 时仍会自动回退到
//     NextChannel 切下一个 channel。
//
// cfgManager 不可为 nil（CCXKey/Pause middleware 内部 panic on nil）。
func BuildPipelineOpts(cfgManager *config.ConfigManager, lb *LBOutboundAdapter) []pipeline.Option {
	if cfgManager == nil {
		panic("wire.BuildPipelineOpts: cfgManager must not be nil")
	}
	keyMW := pipelinemw.NewCCXKeyFailureMiddleware(cfgManager)
	pauseMW := pipelinemw.NewCCXPauseRuleMiddleware(cfgManager)
	sseErrMW := pipelinemw.NewSSEErrorEventDetector()
	opts := []pipeline.Option{
		pipeline.WithMiddlewares(pauseMW, keyMW, sseErrMW),
		pipeline.WithEmptyResponseDetection(),
		pipeline.WithRetry(4, 8, 0),
	}
	_ = lb // 保留 lb 参数为未来按 lb 状态调整 retry 上限 / 注入 ChannelRetryable 留出口子
	return opts
}
