// Package common 提供 handlers 模块的公共功能
package common

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/keypool"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/ratelimit"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/sjson"
)

const (
	upstreamAccountPoolCooldown      = time.Minute
	upstreamOverloadedCooldown       = 15 * time.Second
	upstreamAccountRateLimitCooldown = 15 * time.Second
	halfOpenProbeWaitTimeout         = 5 * time.Second
	halfOpenProbePollInterval        = 100 * time.Millisecond
	shortEmptyResponseRetryWindow    = 10 * time.Second
)

// isClientSideError 判断错误是否由客户端明确取消（不应计入渠道失败）
// 仅识别 context.Canceled，broken pipe/connection reset 视为连接故障需要 failover
func isClientSideError(err error) bool {
	if err == nil {
		return false
	}
	// 只有 context.Canceled 才是明确的客户端取消意图
	return errors.Is(err, context.Canceled)
}

// NextAPIKeyFunc 返回下一个可用 API key（按 failover 策略）
type NextAPIKeyFunc func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error)

// BuildRequestFunc 构建上游请求（upstreamCopy.BaseURL 已写入当前尝试的 BaseURL）
type BuildRequestFunc func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error)

// DeprioritizeKeyFunc 对 quota 相关失败的 key 做降级（实现可选择是否记录日志）
type DeprioritizeKeyFunc func(apiKey string)

// HandleSuccessFunc 处理成功响应（负责写回客户端），并返回 usage（可为 nil）
// 注意：实现方需要自行关闭 resp.Body（与现有 handlers 保持一致）。
// actualRequestBody 为本次实际转发给上游的请求体，可用于 usage 估算等后处理。
type HandleSuccessFunc func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error)

// TryUpstreamOption 为渠道内 BaseURL/Key 轮转补充可选行为。
type TryUpstreamOption func(*tryUpstreamOptions)

type tryUpstreamOptions struct {
	channelLogOptions []ChannelLogOption
	endpointPolicy    *autopilot.EndpointAttemptPolicy // endpoint 级策略（nil 时不注入）
	executionRoute    scheduler.ChannelRouteRef
	executionModel    string // 联邦 sibling 的实际执行模型（为空时沿用请求模型）
	// overflowRedirect 标记执行模型来自上下文溢出重定向注入：发送层据此
	// 剥离 responses 历史 encrypted_content 并回显 X-CCX-Model-Redirect。
	overflowRedirect bool
	// 五元组调度 pin：选中候选行的 key 身份与思考档位（锁定首选，执行层兜底）。
	executionKeyIdentity string
	executionEffort      string
}

// WithSelectionTrace 将调度器的选择摘要写入后续渠道请求日志。
// 同时同源透传五元组执行 pin（选中 key 身份与 effort 档；零值 = 不锁定），
// 六类 handler 经此统一接入；显式 WithExecutionPin 优先（条件赋值不覆盖）。
func WithSelectionTrace(selection *scheduler.SelectionResult) TryUpstreamOption {
	return func(opts *tryUpstreamOptions) {
		if opts == nil || selection == nil {
			return
		}
		opts.channelLogOptions = append(opts.channelLogOptions, WithChannelSelectionTrace(
			selection.Reason,
			scheduler.FormatSelectionTraceSummary(selection.Trace, 4),
		))
		if opts.executionKeyIdentity == "" {
			opts.executionKeyIdentity = selection.ExecutionKeyIdentity
		}
		if opts.executionEffort == "" {
			opts.executionEffort = selection.ExecutionEffort
		}
		opts.overflowRedirect = opts.overflowRedirect || selection.OverflowRedirect
	}
}

// WithEndpointAttemptPolicy 将 autopilot EndpointAttemptPolicy 注入 TryUpstreamWithAllKeys。
// nil policy 时等同于不注入（fail-open）。
// panic 防护：policy 函数 panic 时回退原列表，记日志。
func WithEndpointAttemptPolicy(policy *autopilot.EndpointAttemptPolicy) TryUpstreamOption {
	return func(opts *tryUpstreamOptions) {
		if opts == nil || policy == nil {
			return
		}
		opts.endpointPolicy = policy
	}
}

// WithExecutionRoute 指定本次尝试实际使用的物理配置路由。
// 未设置时保持兼容：默认使用 requestKind + channelIndex。
func WithExecutionRoute(route scheduler.ChannelRouteRef) TryUpstreamOption {
	return func(opts *tryUpstreamOptions) {
		if opts == nil {
			return
		}
		opts.executionRoute = route
	}
}

// WithExecutionModel 指定本次尝试实际要发送给上游的模型。
// 协议联邦候选（如 messages 请求走 chat sibling 的 kimi-k3）必须在构造上游请求
// 与应用参数约束之前完成改写，避免上游收到不存在的请求模型。
func WithExecutionModel(model string) TryUpstreamOption {
	return func(opts *tryUpstreamOptions) {
		if opts == nil {
			return
		}
		opts.executionModel = model
	}
}

// WithExecutionPin 传入 autopilot 五元组候选行的执行 pin：选中 key 身份与思考档位。
// key 身份对应的明文 key 被提到首次尝试位（失败后仍按原顺序轮转其余 key 兜底）；
// effort 档在 endpoint binding 解析未决档位时作为首选填充。
// 空值等同未设置（fail-open，走原行为）。
func WithExecutionPin(keyIdentity, effort string) TryUpstreamOption {
	return func(opts *tryUpstreamOptions) {
		if opts == nil {
			return
		}
		opts.executionKeyIdentity = keyIdentity
		opts.executionEffort = effort
	}
}

func normalizeExecutionRoute(route scheduler.ChannelRouteRef, requestKind scheduler.ChannelKind, channelIndex int, upstream *config.UpstreamConfig) scheduler.ChannelRouteRef {
	legacyDefault := route.IsZero()
	if route.Kind == "" {
		route.Kind = string(requestKind)
	}
	if legacyDefault {
		route.Index = channelIndex
	}
	if route.ChannelUID == "" && upstream != nil {
		route.ChannelUID = upstream.ChannelUID
	}
	return route
}

func ChannelAPIType(kind scheduler.ChannelKind) string {
	switch kind {
	case scheduler.ChannelKindResponses:
		return "Responses"
	case scheduler.ChannelKindGemini:
		return "Gemini"
	case scheduler.ChannelKindChat:
		return "Chat"
	case scheduler.ChannelKindImages:
		return "Images"
	case scheduler.ChannelKindVectors:
		return "Vectors"
	default:
		return "Messages"
	}
}

// ── Autopilot 包级钩子（可选注入，nil 时默认行为不变）──

// endpointPolicyProviderHook 可选的 endpoint policy 提供者。
// 由 main.go 在 autopilot 初始化后注入；handlers 通过 TryUpstreamOption 传入 policy。
// 签名：(c, model, upstream) → *EndpointAttemptPolicy（nil 表示不注入）。
var endpointPolicyProviderHook func(c *gin.Context, model string, upstream *config.UpstreamConfig) *autopilot.EndpointAttemptPolicy

// SetEndpointPolicyProviderHook 设置 endpoint policy 提供者钩子。
// 由 main.go 在 autopilot 初始化后调用；nil 表示不注入。
func SetEndpointPolicyProviderHook(hook func(c *gin.Context, model string, upstream *config.UpstreamConfig) *autopilot.EndpointAttemptPolicy) {
	endpointPolicyProviderHook = hook
}

// notifyEndpointResultHook 可选的 endpoint 请求结果通知器。
// 由 main.go 在 autopilot 初始化后注入；用于实时更新 FastDecayScorer。
// 签名：(endpointUID, success)。
var notifyEndpointResultHook func(endpointUID string, success bool)

// SetNotifyEndpointResultHook 设置 endpoint 结果通知钩子。
// 由 main.go 在 autopilot 初始化后调用；nil 表示不通知。
func SetNotifyEndpointResultHook(hook func(endpointUID string, success bool)) {
	notifyEndpointResultHook = hook
}

// PostSuccessfulProxyHook 可选的代理成功后回调。
// 由 main.go 在 autopilot A/B 测试初始化后注入。
// 在主响应已写回客户端之后、函数返回之前触发。
// 签名：(channelKind, model, channelUID string, statusCode int, latencyMs int64, bodyBytes []byte)。
// 回调函数不应阻塞（如果需要异步操作，由回调内部自行管理）。
var postSuccessfulProxyHook func(channelKind, model, channelUID string, statusCode int, latencyMs int64, bodyBytes []byte)

// SetPostSuccessfulProxyHook 设置代理成功后回调钩子。
// 由 main.go 在 autopilot ABTestSampler 初始化后调用；nil 表示不触发。
func SetPostSuccessfulProxyHook(hook func(channelKind, model, channelUID string, statusCode int, latencyMs int64, bodyBytes []byte)) {
	postSuccessfulProxyHook = hook
}

// usagePatternRecorderHook 可选的用量画像记录器（Phase 4 Item 4：渠道推荐）。
// 由 main.go 在 autopilot 初始化后注入；在主响应已写回客户端之后触发，纯观测性累积，
// 不参与任何调度/候选过滤决策。
// 签名：(proxyKeyMask, channelKind, channelUID, model string)。
var usagePatternRecorderHook func(proxyKeyMask, channelKind, channelUID, model string)

// SetUsagePatternRecorderHook 设置用量画像记录钩子。
// 由 main.go 在 autopilot 初始化后调用；nil 表示不记录。
func SetUsagePatternRecorderHook(hook func(proxyKeyMask, channelKind, channelUID, model string)) {
	usagePatternRecorderHook = hook
}

// systemHeaderFilterCache 按渠道-keyHash-模型记忆最优 system header 过滤层级。
// 仅对 Claude Messages 入口生效；其余入口的过滤由 provider 层负责。
var systemHeaderFilterCache = config.NewSystemHeaderFilterCache()

// deprecatedParamCache 按渠道-keyHash-模型记忆上游拒绝的弃用请求参数（如 temperature）。
// 首次 400 触发探测并同 Key 重试，后续同组合请求在发送前主动剥离，避免重复失败往返。
// 记忆落盘到 .config/deprecated_params.json，重启后无需重新探测；属内部状态，用户无需感知。
var deprecatedParamCache = config.NewDeprecatedParamCacheWithPersistence(config.DeprecatedParamStatePath)

// channelCompatCache 按渠道-keyHash-模型记忆上游的协议兼容性能力（如不支持 developer role）。
// 与 deprecatedParamCache 同构：首次 400 学习并同 Key 重试，后续同组合请求在构造上游请求时
// 直接应用兼容改写，用户无需手工勾选兼容性开关。
// 记忆落盘到 .config/channel_compat.json，重启后免重学。
var channelCompatCache = config.SharedChannelCompatCache()

// converterUpstreamCache 按渠道记忆"上游是 new-api/one-api 系转换层"的事实（响应头指纹学习）。
// 转换层会把 Anthropic Messages 请求转成 OpenAI 兼容格式，messages 中间的 system 角色
// 会导致上下文截断，因此命中记忆的渠道自动把 system 角色抽回顶层 system 字段。
// 记忆落盘到 .config/converter_upstreams.json，重启后免重学。
var converterUpstreamCache = config.SharedConverterUpstreamCache()

// SwapConverterUpstreamCacheForTest 临时替换包级转换层指纹缓存，返回还原函数。
//
// 仅供测试使用：全局实例带落盘，测试直接写它会在源码树里产生状态文件，
// 且上一次运行的记忆会影响下一次运行的结果。
func SwapConverterUpstreamCacheForTest(replacement *config.ConverterUpstreamCache) func() {
	original := converterUpstreamCache
	converterUpstreamCache = replacement
	return func() { converterUpstreamCache = original }
}

// converterFingerprintHeaders 是 new-api/one-api 系转换层上游在响应头中携带的指纹。
// 命中任意一个即认为该渠道是转换层上游。
var converterFingerprintHeaders = []string{
	"X-New-Api-Version",
	"X-Oneapi-Request-Id",
}

// detectConverterFingerprint 返回响应头中命中的转换层指纹头名（小写）；未命中返回空串。
func detectConverterFingerprint(header http.Header) string {
	for _, name := range converterFingerprintHeaders {
		if header.Get(name) != "" {
			return strings.ToLower(name)
		}
	}
	return ""
}

// shouldNormalizeSystemRoleForAttempt 判定当前 attempt 是否应把 messages 中的 system 角色
// 抽回顶层 system 字段。触发条件（任一即可，返回的原因用于日志）：
//  1. manual：渠道手动开关打开（强制开，保留为最终覆盖）；
//  2. converter_fingerprint：渠道曾被识别为转换层上游（响应头指纹的渠道级学习记忆）；
//  3. model_family:<family>：实际上线模型（override/mapping 后）非 claude 家族——
//     messages 协议下发非 claude 模型必然经过协议转换，inline system 角色在转换中
//     可能截断上下文。
//
// 未知家族的模型保守不触发（交由指纹学习兜底），避免对未识别端点做无谓改写。
func shouldNormalizeSystemRoleForAttempt(upstream *config.UpstreamConfig, attemptModel string) (bool, string) {
	if upstream == nil {
		return false, ""
	}
	if upstream.NormalizeSystemRoleToTopLevel {
		return true, "manual"
	}
	if converterUpstreamCache.IsConverter(upstream.ChannelUID) {
		return true, "converter_fingerprint"
	}
	family := autopilot.InferModelFamily(attemptModel, "")
	if family != autopilot.ModelFamilyClaude && family != autopilot.ModelFamilyUnknown {
		return true, "model_family:" + string(family)
	}
	return false, ""
}

func shouldNormalizeMetadataUserID(kind scheduler.ChannelKind, upstream *config.UpstreamConfig) bool {
	if upstream == nil {
		return false
	}
	if kind != scheduler.ChannelKindMessages {
		return false
	}
	return upstream.IsNormalizeMetadataUserIDEnabled()
}

func shouldStripBillingHeader(kind scheduler.ChannelKind, upstream *config.UpstreamConfig) bool {
	if upstream == nil {
		return false
	}
	if kind != scheduler.ChannelKindMessages {
		return false
	}
	return upstream.IsStripBillingHeaderEnabled()
}

func applyAdaptiveResponseHeaderTimeout(
	c *gin.Context,
	apiType string,
	policy *autopilot.EndpointAttemptPolicy,
	upstream *config.UpstreamConfig,
	upstreamCopy *config.UpstreamConfig,
	baseURL string,
	apiKey string,
	inheritedMs int,
	isStream bool,
) *config.UpstreamConfig {
	if policy == nil || policy.ResponseHeaderTimeoutForEndpoint == nil || upstream == nil || upstreamCopy == nil {
		return upstream
	}
	// 手工渠道和显式超时始终保持用户配置；自适应只管理自动托管渠道的继承值。
	if !upstream.AutoManaged || upstream.ResponseHeaderTimeoutMs > 0 || upstream.ChannelUID == "" {
		return upstream
	}

	endpointUID := autopilot.GenerateEndpointUID(upstream.ChannelUID, baseURL, autopilot.KeyHashFromAPIKey(apiKey))
	var suggestedMs int
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				RequestLogf(c, "[%s-AdaptiveTimeout] 建议器异常，保持继承超时: %v", apiType, recovered)
				suggestedMs = 0
			}
		}()
		suggestedMs = policy.ResponseHeaderTimeoutForEndpoint(endpointUID, inheritedMs, isStream)
	}()
	if suggestedMs <= 0 {
		return upstream
	}
	if policy.Mode == autopilot.RoutingModeShadow || policy.Mode == autopilot.RoutingModeDryRun {
		RequestLogf(c, "[%s-AdaptiveTimeout-Shadow] endpoint=%s 建议响应头超时 %dms，当前继承 %dms", apiType, endpointUID, suggestedMs, inheritedMs)
		return upstream
	}

	upstreamCopy.ResponseHeaderTimeoutMs = suggestedMs
	RequestLogf(c, "[%s-AdaptiveTimeout] endpoint=%s 响应头超时 %dms（继承值 %dms）", apiType, endpointUID, suggestedMs, inheritedMs)
	return upstreamCopy
}

// TryUpstreamWithAllKeys 尝试一个 upstream 的所有 BaseURL + Key（纯 failover）
// 返回:
//   - handled: 是否已向客户端写回响应（成功或非 failover 错误）
//   - successKey: 成功的 key（仅 handled=true 且成功时有值）
//   - successBaseURLIdx: 成功 BaseURL 的原始索引（用于指标记录）
//   - failoverErr: 最后一次可故障转移的上游错误（用于多渠道聚合错误）
//   - usage: usage 统计（可能为 nil）
//
// buildRequestCostContext 在请求开始时解析并固化 list/effective 成本输入。
// 渠道级「充值币种/金额 + 渠道币种/到账金额」四字段齐备时，用全局汇率图
// 经 ResolveEffectiveCostUSD 计算有效成本并固化（覆盖订阅快照缺失场景）。
func buildRequestCostContext(cfgManager *config.ConfigManager, upstream *config.UpstreamConfig, selection keypool.Selection, model string, consumptionPolicy string) metrics.RequestCostContext {
	ctx := metrics.RequestCostContext{
		KeyUID:              selection.KeyUID,
		SubscriptionUID:     strings.TrimSpace(selection.Config.SourceSubscriptionUID),
		EffectiveCostReason: "subscription payment/credit snapshot unavailable",
		ConsumptionPolicy:   consumptionPolicy,
	}

	// 列表成本（标价）：渠道计价币种为 CNY 等非 USD 时按全局硬编码汇率折 USD，作为基准。
	resolved := config.ResolveUpstreamCapability(model, upstream, nil)
	ctx.ListCostUSD = metrics.CalculateTokenCostUSDWithPricing(resolved.Capability.Pricing, 0, 0, 0, 0)

	// 构建全局汇率图（用于充值/渠道币种到 USD 的折算）。
	var graph *config.ExchangeRateGraph
	var snapshotVersion uint64
	if cfgManager != nil {
		costConfig := cfgManager.GetAutopilotRouting().CostOptimization
		if costConfig.ExchangeRateSnapshot != nil {
			snapshotVersion = costConfig.ExchangeRateSnapshot.Version
			ctx.ExchangeSnapshotVersion = snapshotVersion
		}
		if len(costConfig.ExchangeRateQuotes) > 0 {
			if snapshotVersion == 0 {
				snapshotVersion = 1
			}
			if g, err := config.NewExchangeRateGraph(costConfig.ExchangeRateQuotes, snapshotVersion, time.Now()); err == nil {
				graph = g
			}
		}
	}

	// 渠道级充值→到账换算：四字段齐备且金额为正时启用有效成本计算。
	// EffectiveMultiplier = (PaymentAmount × 充值币价) / (CreditAmount × 渠道币价)，
	// EffectiveCostUSD = ListCostUSD(折渠道币种) × EffectiveMultiplier（乘法语义）。
	if graph != nil &&
		upstream.ChannelPaymentAmount != nil && *upstream.ChannelPaymentAmount > 0 &&
		upstream.ChannelCreditAmount != nil && *upstream.ChannelCreditAmount > 0 &&
		strings.TrimSpace(upstream.ChannelPaymentCurrency) != "" &&
		strings.TrimSpace(upstream.ChannelCreditCurrency) != "" {
		res := autopilot.ResolveEffectiveCostUSD(autopilot.EffectiveCostInput{
			Graph:           graph,
			ListCostUSD:     ctx.ListCostUSD,
			GroupMultiplier: 1.0,
			TimeMultiplier:  1.0,
			PaymentAmount:   *upstream.ChannelPaymentAmount,
			PaymentUnit:     strings.TrimSpace(upstream.ChannelPaymentCurrency),
			CreditAmount:    *upstream.ChannelCreditAmount,
			CreditUnit:      strings.TrimSpace(upstream.ChannelCreditCurrency),
			KeyUID:          selection.KeyUID,
			SubscriptionUID: ctx.SubscriptionUID,
		})
		if res.Available && res.EffectiveMultiplier > 0 {
			ctx.EffectiveCostMultiplier = res.EffectiveMultiplier
			ctx.EffectiveCostAvailable = true
			ctx.EffectiveCostReason = "channel payment/credit conversion"
		} else if res.Reason != "" {
			ctx.EffectiveCostReason = "channel conversion unavailable: " + res.Reason
		}
	} else if upstream.CostMultiplier != nil && *upstream.CostMultiplier > 0 {
		// 简化路径：仅配充值倍率（乘法）时直接生效，无需汇率图。
		ctx.EffectiveCostMultiplier = *upstream.CostMultiplier
		ctx.EffectiveCostAvailable = true
		ctx.EffectiveCostReason = "channel cost multiplier"
	}
	// 实际 token 数在 finalize 时才可得；标价成本在那里按固化模型定价计算。
	return ctx
}

func TryUpstreamWithAllKeys(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	kind scheduler.ChannelKind,
	apiType string,
	metricsManager *metrics.MetricsManager,
	upstream *config.UpstreamConfig,
	urlResults []warmup.URLLatencyResult,
	requestBody []byte,
	contextRequirement *scheduler.ContextRequirement,
	isStream bool,
	nextAPIKey NextAPIKeyFunc,
	buildRequest BuildRequestFunc,
	deprioritizeKey DeprioritizeKeyFunc,
	markURLFailure func(url string),
	markURLSuccess func(url string),
	handleSuccess HandleSuccessFunc,
	model string,
	operation string,
	channelIndex int,
	channelLogStore *metrics.ChannelLogStore,
	opts ...TryUpstreamOption,
) (handled bool, successKey string, successBaseURLIdx int, failoverErr *FailoverError, usage *types.Usage, lastError error) {
	if upstream == nil || len(upstream.APIKeys) == 0 {
		return false, "", 0, nil, nil, nil
	}
	if metricsManager == nil {
		return false, "", 0, nil, nil, nil
	}
	if nextAPIKey == nil || buildRequest == nil || handleSuccess == nil {
		return false, "", 0, nil, nil, nil
	}
	if len(urlResults) == 0 {
		return false, "", 0, nil, nil, nil
	}
	upstream = config.RuntimeUpstreamForAutoManagedProvider(upstream)

	// 请求侧压缩：仅对 messages 类入口的 tool_result 历史做 RTK 模式压缩。
	// fail-open：异常/保真门不通过/膨胀 均回退原文，不阻断请求。
	if len(requestBody) > 0 {
		scenarioKey := c.GetHeader("X-Routing-Scenario")
		compressedBody := ApplyRequestCompression(
			c, requestBody, kind, scenarioKey,
			false, // 全局默认关（最小集阶段，仅场景预设触发）
			false, // 渠道级暂未暴露配置
		)
		if len(compressedBody) != len(requestBody) {
			requestBody = compressedBody
			RestoreRequestBody(c, requestBody)
			c.Set("requestBodyBytes", requestBody)
		}
	}

	tryOpts := tryUpstreamOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&tryOpts)
		}
	}
	executionRoute := normalizeExecutionRoute(tryOpts.executionRoute, kind, channelIndex, upstream)
	executionKind := scheduler.ChannelKind(executionRoute.Kind)
	executionIndex := executionRoute.Index
	executionAPIType := ChannelAPIType(executionKind)
	if routedMetrics := channelScheduler.GetMetricsManagerForRoute(executionRoute); routedMetrics != nil {
		metricsManager = routedMetrics
	}
	if routedLogStore := channelScheduler.GetChannelLogStoreForRoute(executionRoute); routedLogStore != nil {
		channelLogStore = routedLogStore
	}
	modelCircuit := modelCircuitChecker(metricsManager, model)
	keyAutoWeight := keyAutoWeightFactor(metricsManager)

	metricsServiceType := scheduler.NormalizedMetricsServiceType(executionKind, upstream.ServiceType)

	var lastFailoverError *FailoverError
	deprioritizeCandidates := make(map[string]bool)
	failedQuotaGroups := make(map[string]bool)
	probeAcquired := make(map[string]bool)
	// 当前持有的限速并发信号量释放函数（兜底：函数任意路径返回时释放，避免泄漏）
	var activeRateLimitRelease func()
	defer func() {
		if activeRateLimitRelease != nil {
			activeRateLimitRelease()
		}
		for key := range probeAcquired {
			parts := strings.SplitN(key, "|", 2)
			if len(parts) == 2 {
				metricsManager.ReleaseProbe(parts[0], parts[1], metricsServiceType)
			}
		}
	}()

	// 协议联邦：调度器已按 sibling 执行协议解析出真实模型，先落到请求体，
	// 后续的能力判断、参数约束与 provider 请求构造都以该模型为准。
	if tryOpts.executionModel != "" && tryOpts.executionModel != model {
		if rewritten, err := sjson.SetBytes(requestBody, "model", tryOpts.executionModel); err == nil {
			requestBody = rewritten
			RestoreRequestBody(c, requestBody)
			c.Set("requestBodyBytes", requestBody)
			RequestLogf(c, "[%s-Federation] 请求协议 %s 走执行协议 %s，模型改写: %s -> %s",
				apiType, kind, executionKind, model, tryOpts.executionModel)
		}
		model = tryOpts.executionModel
	}

	// 溢出跨协议/跨模型重定向（调度器注入的 OverflowRedirect 候选）：
	// 执行模型已在联邦改写中落到请求体，这里补剥离与回显。responses 直连下
	// 跨模型无法解密历史加密推理，剥离 encrypted_content 保留 summary
	//（与 chat 转换器口径对齐）；跨协议组合本来就要走转换器，无需剥离。
	if tryOpts.overflowRedirect && tryOpts.executionModel != "" && tryOpts.executionModel != model {
		if executionKind == scheduler.ChannelKindResponses {
			if stripped := stripResponsesEncryptedContent(requestBody); string(stripped) != string(requestBody) {
				requestBody = stripped
				RestoreRequestBody(c, requestBody)
				c.Set("requestBodyBytes", requestBody)
			}
		}
		c.Header("X-CCX-Model-Redirect", model+" -> "+tryOpts.executionModel)
		RequestLogf(c, "[%s-Overflow] 上下文溢出重定向生效: %s -> %s（执行协议 %s）",
			apiType, model, tryOpts.executionModel, executionKind)
	}

	// 五元组调度 pin 的明文 key：身份反查一次（key 已被移除/轮换时为空 = 不锁定）。
	pinnedAPIKey := autopilot.ResolvePinnedAPIKey(upstream, tryOpts.executionKeyIdentity)

	// 先应用用户配置的模型映射。endpoint 级自动映射会在选定 Key 后再覆盖本次尝试的模型。
	redirectedModel := config.RedirectModel(model, upstream)
	capabilityRequestModel := model

	// 历史图片轮次限制：替换历史图片为占位符，避免不必要的 vision 回退
	if kind != scheduler.ChannelKindImages {
		effectiveLimit := resolveHistoricalImageTurnLimit(upstream)
		if effectiveLimit > 0 {
			if replaced, modified := StripHistoricalImagesWithContext(c, requestBody, effectiveLimit, envCfg.EnableRequestLogs, apiType); modified {
				requestBody = replaced
			}
		}
	}

	// Vision 能力检查：含图请求跳过不支持 vision 的渠道/模型
	if kind != scheduler.ChannelKindImages && HasImageContent(c, requestBody) {
		if upstream.NoVision {
			RequestLogf(c, "[%s-Vision] 跳过不支持视觉的渠道 [%d] %s", apiType, executionIndex, upstream.Name)
			return false, "", 0, nil, nil, fmt.Errorf("channel %s does not support vision", upstream.Name)
		}
		if isNoVisionModel(upstream, redirectedModel) {
			if upstream.VisionFallbackModel != "" {
				fallback := upstream.VisionFallbackModel
				RequestLogf(c, "[%s-Vision] 模型 %s 不支持视觉，使用 fallback: %s (渠道 [%d] %s)", apiType, redirectedModel, fallback, executionIndex, upstream.Name)
				if replaced, err := sjson.SetBytes(requestBody, "model", fallback); err == nil {
					requestBody = replaced
				}
				redirectedModel = fallback
				capabilityRequestModel = fallback
				if err := channelScheduler.ValidateUpstreamContext(executionKind, redirectedModel, upstream, contextRequirement); err != nil {
					RequestLogf(c, "[%s-Vision] fallback 模型 %s 不满足上下文需求，跳过渠道 [%d] %s: %v", apiType, redirectedModel, executionIndex, upstream.Name, err)
					return false, "", 0, nil, nil, err
				}
			} else {
				RequestLogf(c, "[%s-Vision] 模型 %s 不支持视觉且无 fallback，跳过渠道 [%d] %s", apiType, redirectedModel, executionIndex, upstream.Name)
				return false, "", 0, nil, nil, fmt.Errorf("model %s does not support vision", redirectedModel)
			}
		}
	}

	// ── EndpointAttemptPolicy: 步骤 1+2（FilterURLs + SortURLs）──
	// 对 urlResults 应用 policy 过滤/排序（设计 §4.6.2a）。
	// nil policy / hook 未设置 / panic 时均 fail-open，不影响现有逻辑。
	endpointPolicy := tryOpts.endpointPolicy
	if endpointPolicy == nil && endpointPolicyProviderHook != nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[Autopilot-EndpointPolicy] endpointPolicyProviderHook panic: %v", r)
				}
			}()
			endpointPolicy = endpointPolicyProviderHook(c, model, upstream)
		}()
	}
	urlResults = applyPolicyToURLs(endpointPolicy, urlResults, apiType, c)

	for urlIdx, urlResult := range urlResults {
		currentBaseURL := urlResult.URL
		originalIdx := urlResult.OriginalIdx // 原始索引用于指标记录
		failedKeys := make(map[string]bool)  // 每个 BaseURL 重置失败 Key 列表
		maxRetries := len(upstream.APIKeys)
		shortEmptyRetried := make(map[string]bool)
		var retrySelection keypool.Selection
		var retryAPIKey string

		for keyAttempts, attemptOrdinal := 0, 0; keyAttempts < maxRetries || retryAPIKey != ""; attemptOrdinal++ {
			isRetryAttempt := attemptOrdinal > 0 || urlIdx > 0
			// 每次 attempt 开始时清除上一轮的 effort 可观测性标记，
			// 防止 failover 到未产生 resolved target 的 endpoint 时残留旧值。
			c.Set("effortDecisionSource", "passthrough")
			c.Set("effortClampedByClient", false)
			// 自动映射未命中原因同样按 attempt 重置：映射成功的尝试不携带上一轮的失败原因。
			c.Set("mappingFailReason", "")
			// 释放上一轮 attempt 的并发信号量（首次为空操作）
			if activeRateLimitRelease != nil {
				activeRateLimitRelease()
				activeRateLimitRelease = nil
			}
			attemptBody := requestBody
			if shouldStripBillingHeader(kind, upstream) {
				attemptBody, _ = RemoveBillingHeadersWithContext(c, attemptBody, envCfg.EnableRequestLogs, apiType)
			}
			if shouldNormalizeMetadataUserID(kind, upstream) {
				attemptBody = NormalizeMetadataUserID(attemptBody)
			}
			// 发往上游前按实际模型能力 clamp 最大输出 token：
			// 客户端/上游 subagent 可能发送超过模型上限的 max_tokens（如 Claude Code 默认 64000），
			// 而部分平台（火山方舟 kimi 系列硬限 32768）会直接 400。此处静默下调到模型上限，
			// 使请求成功而非被调度过滤为"无可用渠道"。
			if cap := config.ResolveUpstreamCapability(capabilityRequestModel, upstream, cfgManager.GetConfig().UpstreamModelCapabilities); cap.Capability.MaxOutputTokens > 0 {
				if clamped, changed := clampMaxTokensInBody(attemptBody, kind, cap.Capability.MaxOutputTokens); changed {
					attemptBody = clamped
					RequestLogf(c, "[%s-Clamp] max_tokens 超过模型 %q 上限 %d，已下调", apiType, cap.ActualModel, cap.Capability.MaxOutputTokens)
				}
			}
			RestoreRequestBody(c, attemptBody)
			c.Set("requestBodyBytes", attemptBody)

			var selection keypool.Selection
			var apiKey string
			internalRetry := retryAPIKey != ""
			if retryAPIKey != "" {
				selection = retrySelection
				apiKey = retryAPIKey
				retrySelection = keypool.Selection{}
				retryAPIKey = ""
			} else {
				keyAttempts++
				var err error
				// 步骤 5+6: EndpointAttemptPolicy FilterKeys + SortKeys
				// selectAttemptAPIKeyFiltered 在 keypool.CandidatesForModel 之后应用 policy 过滤/排序。
				// nil policy 时回退到 selectAttemptAPIKey（行为不变）。
				selection, apiKey, err = selectAttemptAPIKeyFiltered(channelScheduler, executionKind, executionIndex, upstream, currentBaseURL, failedKeys, failedQuotaGroups, redirectedModel, nextAPIKey, endpointPolicy, apiType, c, modelCircuit, keyAutoWeight, pinnedAPIKey)
				if err != nil {
					lastError = err
					break // 当前 BaseURL 没有可用 Key，尝试下一个 BaseURL
				}
			}

			// per-key baseURL 绑定过滤（provider 模板化添加）：
			// 若该 Key 通过 APIKeyConfigs 绑定了特定端点，且与当前 BaseURL 不符，则跳过，
			// 避免不同 plan 的 Key 与多 BaseURL 产生无效笛卡尔积（如 MiMo sk-/tp- 交叉尝试必失败）。
			// 未绑定端点的 Key（历史手填渠道）保持原有笛卡尔积行为。
			if bound := upstream.BoundBaseURLForKey(apiKey); bound != "" && bound != currentBaseURL {
				failedKeys[apiKey] = true
				RequestLogf(c, "[%s-Key] 跳过绑定其他端点的 Key: %s (绑定 %s ≠ 当前 %s)", apiType, utils.MaskAPIKey(apiKey), bound, currentBaseURL)
				continue
			}

			// Phase 2: 分层 system header 过滤。
			// 仅对 Claude Messages 入口生效；缓存 key 为 channelUID:keyHash:model。
			if kind == scheduler.ChannelKindMessages && upstream.ChannelUID != "" {
				keyHash := autopilot.KeyHashFromAPIKey(apiKey)
				if entry := systemHeaderFilterCache.Get(upstream.ChannelUID, keyHash, redirectedModel); entry != nil && entry.Level > 0 {
					if filtered, modified := applySystemHeaderFilterToBody(attemptBody, providers.SystemHeaderFilterLevel(entry.Level)); modified {
						attemptBody = filtered
						RestoreRequestBody(c, attemptBody)
						c.Set("requestBodyBytes", attemptBody)
						RequestLogf(c, "[%s-SystemFilter] 渠道 %s 应用过滤层级 %d", apiType, upstream.Name, entry.Level)
					}
				}
			}

			// 检查熔断状态
			circuitState := metricsManager.GetKeyCircuitState(currentBaseURL, apiKey, metricsServiceType)
			if circuitState == metrics.CircuitStateOpen {
				failedKeys[apiKey] = true
				RequestLogf(c, "[%s-Circuit] 跳过 open 状态中的 Key: %s", apiType, utils.MaskAPIKey(apiKey))
				continue
			}
			if circuitState == metrics.CircuitStateHalfOpen {
				probeKey := currentBaseURL + "|" + apiKey
				if !metricsManager.TryAcquireProbe(currentBaseURL, apiKey, metricsServiceType) {
					RequestLogf(c, "[%s-Circuit] half-open 探针已占用，等待 Key 恢复: %s", apiType, utils.MaskAPIKey(apiKey))
					acquired, state := waitForHalfOpenProbe(c.Request.Context(), metricsManager, currentBaseURL, apiKey, metricsServiceType)
					if acquired {
						probeAcquired[probeKey] = true
						RequestLogf(c, "[%s-Circuit] 等待后取得 half-open 探针 Key: %s", apiType, utils.MaskAPIKey(apiKey))
					} else if state == metrics.CircuitStateClosed {
						RequestLogf(c, "[%s-Circuit] half-open 探针已由其他请求恢复，继续使用 Key: %s", apiType, utils.MaskAPIKey(apiKey))
					} else {
						failedKeys[apiKey] = true
						RequestLogf(c, "[%s-Circuit] half-open 探针等待超时或已熔断，暂时跳过 Key: %s", apiType, utils.MaskAPIKey(apiKey))
						continue
					}
				} else {
					probeAcquired[probeKey] = true
					RequestLogf(c, "[%s-Circuit] 使用 half-open 探针 Key: %s", apiType, utils.MaskAPIKey(apiKey))
				}
			}

			if envCfg.ShouldLog("info") {
				displayMax := maxRetries
				if internalRetry || len(shortEmptyRetried) > 0 {
					displayMax++
				}
				RequestLogf(c, "[%s-Key] 使用API密钥: %s (BaseURL %d/%d, 尝试 %d/%d)",
					apiType, utils.MaskAPIKey(apiKey), urlIdx+1, len(urlResults), attemptOrdinal+1, displayMax)
			}

			// 使用深拷贝避免并发修改问题
			upstreamCopy := upstream.Clone()
			upstreamCopy.BaseURL = currentBaseURL

			// Phase 3B-2: 应用 EndpointAttemptPolicy 的自动模型映射（含 effort 原子改写）。
			// ResolvedRouteTarget 来自 ModelResolver（AutoManaged 渠道，三条件门控通过），
			// 优先级低于 RedirectModel（手动配置短路后 target 恒为 nil，不会双重映射）。
			attemptModel := redirectedModel
			var appliedMappedModel string
			if endpointPolicy != nil {
				keyHash := autopilot.KeyHashFromAPIKey(apiKey)
				euid := autopilot.GenerateEndpointUID(upstream.ChannelUID, currentBaseURL, keyHash)

				// 优先使用新 API（原子 model+effort），回退到旧 API（仅 model）。
				// lookup 同时带出解析链路的未命中原因（no_capable_model / no_model_profiles /
				// exact_model_required / no_profile 等），供 fail-open 透传时落可观测性字段，
				// 避免"为什么没映射"完全静默。
				var target *autopilot.ResolvedRouteTarget
				mappingFailReason := ""
				if endpointPolicy.ResolvedTargetForBinding != nil {
					target, mappingFailReason = endpointPolicy.ResolvedTargetForBinding(upstream.ChannelUID, currentBaseURL, apiKey)
				}
				if target == nil && endpointPolicy.ResolvedTargetByEndpointUID != nil {
					if t, reason := endpointPolicy.ResolvedTargetByEndpointUID(euid); t != nil {
						target = t
						mappingFailReason = ""
					} else if mappingFailReason == "" {
						mappingFailReason = reason
					}
				}
				if target == nil && endpointPolicy.ResolvedModelByEndpointUID != nil {
					if mm := endpointPolicy.ResolvedModelByEndpointUID(euid); mm != "" {
						target = &autopilot.ResolvedRouteTarget{Model: mm}
						mappingFailReason = ""
					}
				}
				if target != nil && target.Model != "" {
					// 五元组调度 pin：binding 解析未决档（passthrough）时用调度选中档填充。
					// 拷贝填充，勿改 policy 缓存中的共享 target；模型以 per-key 解析为准
					//（与调度同源 resolver，冲突时信任执行近实时结论）。
					if target.Effort == "" && tryOpts.executionEffort != "" {
						pinnedTarget := *target
						pinnedTarget.Effort = autopilot.EffortLevel(tryOpts.executionEffort)
						pinnedTarget.EffortDecided = true
						target = &pinnedTarget
					}
					// Atomic rewrite: model + effort together
					// 注意必须赋回外层 attemptBody：此前此处用 := 声明了块内影子变量，
					// 模型改写只短暂进入 gin context，随后的 system 归一化/参数约束等步骤
					// 基于外层旧 body 再次 Set，会把改写静默覆盖（火山渠道 auto_resolve 后
					// 仍以原模型发出、上游 400 的根因）。
					if rewritten, rewriteOk := atomicModelEffortRewrite(attemptBody, target, upstreamCopy, executionKind); rewriteOk {
						attemptBody = rewritten
						attemptModel = target.Model
						appliedMappedModel = target.Model
						RestoreRequestBody(c, attemptBody)
						c.Set("requestBodyBytes", attemptBody)
						RequestLogf(c, "[%s-AutoModel] endpoint=%s model override: %s -> %s (effort=%s, decided=%v)",
							apiType, euid, model, target.Model, target.Effort, target.EffortDecided)
					}

					// 记录 effort 决策来源与钳位状态，供 ChannelLog 可观测性字段使用
					if target.EffortDecided {
						c.Set("effortDecisionSource", "autopilot")
						// 注意：ExtractClientEffortExplicit 按 scheduler.ChannelKind（小写 messages/chat/...）
						// 分支判断协议字段，而非 apiType 显示名（Messages/Chat/...），此处须传入 kind。
						clientRaw, clientExplicit := ExtractClientEffortExplicit(requestBody, string(kind))
						if isEffortClampedByClient(clientRaw, clientExplicit, target.Effort) {
							c.Set("effortClampedByClient", true)
						}
					} else {
						c.Set("effortDecisionSource", "passthrough")
					}
				} else if mappingFailReason != "" {
					// 自动映射链路运行过但未命中：fail-open 透传原始模型，
					// 记录原因供事后排查（ChannelLog.mappingFailReason + 请求日志）。
					c.Set("mappingFailReason", mappingFailReason)
					RequestLogf(c, "[%s-AutoModel] endpoint=%s 自动映射未命中，按原始模型透传: model=%s reason=%s",
						apiType, euid, model, mappingFailReason)
				}
			}

			// Claude Messages 入口：将 messages 中的 system 角色抽回顶层 system 字段。
			// 统一判定点放在 attemptModel 确定之后：手动开关为强制开；自动触发覆盖两类场景——
			// (a) 上线模型经 override/mapping 后非 claude 家族（messages 协议发非 claude 模型
			// 必然经过协议转换）；(b) 上游响应头曾命中 new-api/one-api 转换层指纹。
			// 兼容 Opus 4.8 / Fable 5 等新客户端将 system 作为消息 role 发送、而转换层上游
			// 遇到 inline system 会截断上下文的情况。归一化幂等，与 SystemHeaderFilter、
			// 模型改写、参数约束等步骤正交。
			if kind == scheduler.ChannelKindMessages {
				if should, normReason := shouldNormalizeSystemRoleForAttempt(upstreamCopy, attemptModel); should {
					if normalizedBody, normChanged := providers.NormalizeSystemRoleToTopLevelWithChanged(attemptBody); normChanged {
						attemptBody = normalizedBody
						RestoreRequestBody(c, attemptBody)
						c.Set("requestBodyBytes", attemptBody)
						RequestLogf(c, "[%s-Preprocess] 已将 messages 中的 system 角色抽回顶层 system 字段（原因: %s）", apiType, normReason)
					}
				}
			}

			// harness 重复注入的 total_tokens 提醒块对任何上游都是纯垃圾，无条件去重。
			// 必须放在 system 角色归一化之后：新客户端把该块作为 messages 里的 system
			// 角色发送，归一化抽到顶层之前去重扫不到任何块。
			if deduped, changed := DeduplicateTotalTokensSystemBlocks(c, attemptBody, envCfg.EnableRequestLogs, apiType); changed {
				attemptBody = deduped
				RestoreRequestBody(c, attemptBody)
				c.Set("requestBodyBytes", attemptBody)
			}

			// 已知厂商参数约束（主动侧）：无需先失败一次，按 model-registry 里的文档约束直接规避。
			// 例如 Kimi K3/K2.7-code/K2.6 的 temperature/top_p/n 等为固定值，传入即 400。
			// 约束数据随 model-registry 走 presetstore 刷新链路，运营者更新 JSON 即可生效，
			// 不需要重新编译发版。
			if paramCap := config.ResolveUpstreamCapability(attemptModel, upstream, cfgManager.GetConfig().UpstreamModelCapabilities); paramCap.Capability.ParamConstraints != nil {
				if stripped, applied := ApplyKnownParamConstraints(attemptBody, paramCap.Capability.ParamConstraints); len(applied) > 0 {
					attemptBody = stripped
					RestoreRequestBody(c, attemptBody)
					c.Set("requestBodyBytes", attemptBody)
					RequestLogf(c, "[%s-ParamConstraint] 模型 %s 应用已知参数约束: %s",
						apiType, attemptModel, strings.Join(applied, ","))
				}
			}

			// 输出上限自学习（主动侧）：命中记忆时把最大输出 token 字段下调到该 渠道-Key-模型
			// 组合的实测上限。注册表登记的是模型公开上限（如 Kimi K2.6 官方 262144），同一模型
			// 在个别部署上更低（火山方舟 coding 端点 32768），只能由真实请求被拒后学到。
			// 实测值只收紧不放宽：高于注册表上限时 clamp 不会改动任何字段。
			if upstream.ChannelUID != "" {
				keyHash := autopilot.KeyHashFromAPIKey(apiKey)
				if state, ok := channelCompatCache.OutputLimit(upstream.ChannelUID, keyHash, attemptModel); ok && state.MaxOutputTokens > 0 {
					if clamped, changed := clampMaxTokensInBody(attemptBody, kind, state.MaxOutputTokens); changed {
						attemptBody = clamped
						RestoreRequestBody(c, attemptBody)
						c.Set("requestBodyBytes", attemptBody)
						RequestLogf(c, "[%s-OutputLimit] 渠道 %s 模型 %s 按实测上限 %d 下调最大输出 token（来源 %s）",
							apiType, upstream.Name, attemptModel, state.MaxOutputTokens, state.Source)
					}
				}
			}

			// 内容启发式兼容项（无上游报错信号）：首次遇到该组合时异步探测一次并记忆，
			// 不阻塞当前请求，结论对后续请求生效。替代原先要用户手工点诊断按钮的做法。
			maybeTriggerCompatProbe(upstream, apiKey, currentBaseURL, attemptModel)

			// 渠道兼容性自学习（主动侧）：命中记忆时把学习结论注入本次请求所用的上游副本
			// （LearnedCompatTraits，仅当次请求生效，不落盘）。实际改写由各 provider 在
			// 构造上游请求时执行（那里才知道协议形态），此处只传递结论。
			//
			// 注意 TraitStripCodexClientTools 不在此处注入：它没有独立的运行时字段，
			// CodexToolCompat 是保留给用户手工控制的既有开关（无可靠错误信号，见
			// compat_signal.go 说明），自学习信号不应该复用/覆盖这个手工语义的字段。
			if upstream.ChannelUID != "" {
				keyHash := autopilot.KeyHashFromAPIKey(apiKey)
				learnedTraits := []config.CompatTrait{
					config.TraitDowngradeDeveloperRole,
					config.TraitStripImageGenTool,
					config.TraitPassbackThinkingBlocks,
					config.TraitPassbackReasoningContent,
					config.TraitStripEmptyTextBlocks,
					config.TraitNormalizeNonstandardChatRoles,
					config.TraitCodexNativeToolPassthrough,
					config.TraitUnsupportedBetaHeader,
				}
				for _, trait := range learnedTraits {
					if state, ok := channelCompatCache.Trait(upstream.ChannelUID, keyHash, attemptModel, trait); ok {
						upstreamCopy.SetLearnedCompatTrait(trait, state.Enabled)
						channelCompatCache.MarkApplied(upstream.ChannelUID, keyHash, attemptModel, trait)
						// TraitUnsupportedBetaHeader 附带被拒 token 名列表，provider 按 token 粒度剥离
						if trait == config.TraitUnsupportedBetaHeader && state.Enabled {
							if tokens := ExtractRejectedBetaTokens(state.Evidence); len(tokens) > 0 {
								upstreamCopy.SetLearnedRejectedBetaTokens(tokens)
							}
						}
					}
				}
			}

			// 弃用参数自适应（主动侧）：命中记忆时在发送前剥离上游已拒绝的参数。
			// 记忆键为 channelUID:keyHash:实际请求模型，不影响同渠道的其他模型/Key。
			if upstream.ChannelUID != "" {
				keyHash := autopilot.KeyHashFromAPIKey(apiKey)
				if params := deprecatedParamCache.Params(upstream.ChannelUID, keyHash, attemptModel); len(params) > 0 {
					if stripped, modified := StripDeprecatedParams(attemptBody, params); modified {
						attemptBody = stripped
						RestoreRequestBody(c, attemptBody)
						c.Set("requestBodyBytes", attemptBody)
						deprecatedParamCache.MarkStripped(upstream.ChannelUID, keyHash, attemptModel)
						RequestLogf(c, "[%s-DeprecatedParam] 渠道 %s 模型 %s 预剥离已知弃用参数: %s",
							apiType, upstream.Name, attemptModel, strings.Join(params, ","))
					}
				}
			}

			// 主动限速：在构建/发送请求前获取许可（渠道级 + Key/Quota scope）
			if rateLimitMgr := channelScheduler.GetRateLimitManager(); rateLimitMgr != nil {
				const maxRateLimitWait = 10 * time.Second
				releases := make([]func(), 0, 2)
				if limiter := rateLimitMgr.Get(executionAPIType, executionIndex); limiter != nil {
					release, rlErr := limiter.Acquire(c.Request.Context(), maxRateLimitWait, time.Now())
					if rlErr != nil {
						lastError = rlErr
						RequestLogf(c, "[%s-RateLimit] 渠道限速器拦截: %v，尝试下一个 Key/渠道", apiType, rlErr)
						failedKeys[apiKey] = true
						continue
					}
					releases = append(releases, release)
				}
				if selection.LimiterScope != "" {
					keyLimiter := rateLimitMgr.GetOrCreateScoped(executionAPIType, executionIndex, selection.LimiterScope, keypool.ConfigForCandidate(*upstream, selection.Config))
					release, rlErr := keyLimiter.Acquire(c.Request.Context(), maxRateLimitWait, time.Now())
					if rlErr != nil {
						lastError = rlErr
						RequestLogf(c, "[%s-RateLimit] Key/Quota 限速器拦截: scope=%s, err=%v，尝试下一个 Key/渠道", apiType, selection.LimiterScope, rlErr)
						failedKeys[apiKey] = true
						if selection.QuotaGroup != "" {
							failedQuotaGroups[selection.QuotaGroup] = true
						}
						for i := len(releases) - 1; i >= 0; i-- {
							releases[i]()
						}
						continue
					}
					releases = append(releases, release)
				}
				activeRateLimitRelease = func() {
					for i := len(releases) - 1; i >= 0; i-- {
						releases[i]()
					}
				}
			}

			req, err := buildRequest(c, upstreamCopy, apiKey)
			if err != nil {
				// buildRequest 失败通常是客户端参数问题或本地构建错误
				// 不应污染熔断统计，直接返回错误
				RequestLogf(c, "[%s-BuildRequest] 请求构建失败: %v", apiType, err)
				return false, "", 0, nil, nil, fmt.Errorf("request build failed: %w", err)
			}
			req = WithRequestLogContext(req, c)
			originalReasoningEffort := extractReasoningEffortForLog(requestBody)
			actualAttemptModel, actualReasoningEffort := extractActualRequestLogDetails(req)
			if actualAttemptModel == "" {
				actualAttemptModel = attemptModel
			}
			c.Set(autopilotActualModelKey, actualAttemptModel)
			c.Set(autopilotActualEffortKey, actualReasoningEffort)

			// (Key,模型) 持久化限制复查：autopilot 映射在选 Key 之后才发生，
			// 选 Key 阶段的 IsKeyModelDisabledNow 只能按映射前模型检查，
			// 这里按实际发往上游的模型补查，保证 model_not_found 学习到的限制
			// 在自动映射渠道上真正生效。命中即跳过本 Key，走正常 failover，
			// 不重复计熔断、不写新黑名单（这是已知限制，不是新故障信号）。
			if actualAttemptModel != "" && upstream.IsKeyModelDisabledNow(apiKey, actualAttemptModel, time.Now()) {
				RequestLogf(c, "[%s-KeyModel] 跳过 (Key %s, 模型 %s)：处于限制期，尝试下一个 Key/渠道",
					apiType, utils.MaskAPIKey(apiKey), actualAttemptModel)
				failedKeys[apiKey] = true
				if probeKey := currentBaseURL + "|" + apiKey; probeAcquired[probeKey] {
					metricsManager.ReleaseProbe(currentBaseURL, apiKey, metricsServiceType)
					delete(probeAcquired, probeKey)
				}
				continue
			}

			actualOriginalModel := ""
			if actualAttemptModel != model {
				actualOriginalModel = model
			}

			// 记录请求开始
			channelScheduler.RecordRequestStart(currentBaseURL, apiKey, metricsServiceType, executionKind)

			// 计算本次尝试的 metricsKey（与统计同源的身份指纹）
			metricsKey := metrics.GenerateMetricsIdentityKey(currentBaseURL, apiKey, metricsServiceType)

			// 提取代理 Key 掩码（ProxyAuthMiddleware 写入 gin context），用于成本报表按用户维度分组
			proxyKeyMask := middleware.GetProxyKeyMask(c)
			logOpts := tryOpts.channelLogOptions
			if proxyKeyMask != "" {
				logOpts = append(logOpts, WithProxyKeyMask(proxyKeyMask))
			}

			// 提取请求关联 ID（multi_channel_failover 生成，写入 gin context）
			if correlationID, ok := c.Get("ccx.request_correlation_id"); ok {
				if cid, ok := correlationID.(string); ok && cid != "" {
					logOpts = append(logOpts, WithRequestCorrelationID(cid))
				}
			}

			// 提取 Autopilot trace UID（SmartRouter 生成，写入 gin context）
			if traceUID, ok := c.Get("ccx.autopilot_trace_uid"); ok {
				if uid, ok := traceUID.(string); ok && uid != "" {
					logOpts = append(logOpts, WithAutopilotTraceUID(uid))
				}
			}

			// 提取 effort 决策来源与钳位状态（endpoint policy 写入 gin context）
			if source, ok := c.Get("effortDecisionSource"); ok {
				if s, ok := source.(string); ok && s != "" {
					logOpts = append(logOpts, WithEffortDecisionSource(s))
				}
			}
			if clamped, ok := c.Get("effortClampedByClient"); ok {
				if c, ok := clamped.(bool); ok && c {
					logOpts = append(logOpts, WithEffortClampedByClient(true))
				}
			}

			// 提取自动映射未命中原因（endpoint policy 写入 gin context）。
			// 非空表示本次尝试 fail-open 透传了原始模型，原因进 ChannelLog 供事后排查。
			if reason := c.GetString("mappingFailReason"); reason != "" {
				logOpts = append(logOpts, WithMappingFailReason(reason))
			}

			// 创建 pending 状态日志（附带代理上下文与会话标识，用于 subagent 观测）
			// interfaceType 记录实际执行协议：联邦 sibling 必须归属到 chat/responses，
			// 否则日志会把 chat 上的尝试错误地展示成 messages 渠道的尝试。
			logRequestID := CreatePendingLog(channelLogStore, metricsKey, executionIndex, upstream.Name, actualAttemptModel, actualOriginalModel, originalReasoningEffort, actualReasoningEffort, apiKey, currentBaseURL, executionAPIType, operation, metrics.RequestSourceProxy, AgentContextFromGin(c), SessionIDFromGin(c), logOpts...)

			// 计算当前 Key 的 ConsumptionPolicy，供 metrics 与 trace 共同使用。
			consumptionPolicy := ""
			if endpointPolicy != nil {
				if _, cands := callPolicySortKeyBindings(endpointPolicy, upstream.ChannelUID, currentBaseURL, []string{apiKey}, apiType, c); len(cands) > 0 {
					consumptionPolicy = cands[0].ConsumptionPolicy
				}
			}
			c.Set("ccx.autopilot_consumption_policy", consumptionPolicy)

			// 向 Autopilot trace 追加一条 "started" endpoint 尝试摘要（fail-open）
			attemptTraceUID, _ := c.Get("ccx.autopilot_trace_uid")
			if uid, ok := attemptTraceUID.(string); ok && uid != "" {
				configuredCostMultiplier := -1.0
				if endpointPolicy != nil {
					if _, cands := callPolicySortKeyBindings(endpointPolicy, upstream.ChannelUID, currentBaseURL, []string{apiKey}, apiType, c); len(cands) > 0 {
						configuredCostMultiplier = cands[0].ConfiguredCostMultiplier
					}
				}
				c.Set("ccx.autopilot_configured_cost_multiplier", configuredCostMultiplier)
				recordEndpointAttempt(uid, autopilot.EndpointAttemptSummary{
					AttemptUID:               logRequestID,
					Status:                   "started",
					ChannelUID:               upstream.ChannelUID,
					EndpointLabel:            autopilot.DeriveEndpointLabel(upstream.ChannelUID, 0),
					Result:                   "attempt_failed",
					ConsumptionPolicy:        consumptionPolicy,
					ConfiguredCostMultiplier: configuredCostMultiplier,
				})
			}

			// TCP 建连开始即计数：将活跃度统计提前到发起上游请求之前；同时关联 proxyKeyMask 用于成本报表持久化
			costContext := buildRequestCostContext(cfgManager, upstream, selection, actualAttemptModel, consumptionPolicy)
			requestID := metricsManager.RecordRequestConnectedWithCostContext(currentBaseURL, apiKey, metricsServiceType, upstream.ChannelUID, actualAttemptModel, model, proxyKeyMask, costContext)
			// 压缩遥测随 pending 记录传播：压缩发生在进入 attempt 循环之前，
			// 每个 attempt 的记录都要补挂，否则 SQLite 压缩列长期为零（成本报表统计失真）。
			if compCtx := GetCompressionContext(c); compCtx != nil {
				metricsManager.RecordRequestCompression(currentBaseURL, apiKey, metricsServiceType, requestID, metrics.CompressionStats{
					Compressed:       compCtx.Compressed,
					OriginalTokens:   int64(compCtx.OriginalTokens),
					CompressedTokens: int64(compCtx.CompressedTokens),
					SavingsPercent:   compCtx.SavingsPercent,
					Technique:        compCtx.Technique,
					FallbackReason:   compCtx.FallbackReason,
				})
			}

			attemptStartedAt := time.Now()
			var connectedOnce sync.Once
			lifecycleTrace := &RequestLifecycleTrace{
				OnConnected: func() {
					connectedOnce.Do(func() {
						connectedAt := time.Now()
						UpdateLogStatus(channelLogStore, metricsKey, logRequestID, metrics.StatusConnecting)
						metricsManager.RecordRequestConnectionLatency(
							currentBaseURL, apiKey, metricsServiceType, requestID, connectedAt.Sub(attemptStartedAt),
						)
					})
				},
				OnFirstResponseByte: func() {
					firstByteAt := time.Now()
					UpdateLogStatus(channelLogStore, metricsKey, logRequestID, metrics.StatusFirstByte)
					metricsManager.RecordRequestFirstByte(currentBaseURL, apiKey, metricsServiceType, requestID, firstByteAt.Sub(attemptStartedAt))
					recordAutopilotFirstByte(c, firstByteAt)
				},
			}
			globalResponseHeaderTimeout := config.GetRuntimeResponseHeaderTimeoutMs(envCfg.ResponseHeaderTimeout * 1000)
			requestUpstream := applyAdaptiveResponseHeaderTimeout(
				c, apiType, endpointPolicy, upstream, upstreamCopy, currentBaseURL, apiKey, globalResponseHeaderTimeout, isStream,
			)
			// 出站尝试摘要：一行说清"这条请求实际发到哪、用什么模型、是否被映射"。
			// 动机：完整出站体日志（[X-Request-Body] 实际请求体）控制台侧被截断到
			// consoleJSONTextLimit 字符，而 model 字段位于 JSON 尾部——控制台视角
			// "看不到实际发送的模型"，排查自动映射问题时只能翻原始日志文件。
			if envCfg.EnableRequestLogs {
				mappingNote := "passthrough"
				switch {
				case appliedMappedModel != "":
					mappingNote = "auto_resolve"
				case c.GetString("mappingFailReason") != "":
					mappingNote = "fail_open:" + c.GetString("mappingFailReason")
				case attemptModel != model:
					mappingNote = "manual_redirect"
				}
				RequestLogf(c, "[%s-UpstreamAttempt] 渠道=[%d] %s (%s) url=%s 模型 %q -> %q 映射=%s key=%s",
					apiType, executionIndex, upstream.Name, executionAPIType, currentBaseURL,
					model, actualAttemptModel, mappingNote, utils.MaskAPIKey(apiKey))
			}
			resp, err := SendRequestWithLifecycleTrace(req, requestUpstream, envCfg, isStream, apiType, lifecycleTrace)
			if err != nil {
				lastError = err
				// 区分客户端取消和真实渠道故障（统一口径）
				if isClientSideError(err) {
					// 客户端取消：不计入失败，不触发 failover
					metricsManager.RecordRequestFinalizeClientCancel(currentBaseURL, apiKey, metricsServiceType, requestID)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, executionKind)
					// 完成日志记录（客户端取消）
					CompleteLog(channelLogStore, metricsKey, logRequestID, 0, false, "client canceled", isRetryAttempt)
					RequestLogf(c, "[%s-Cancel] 请求已取消（SendRequest 阶段）", apiType)
					return true, "", 0, nil, nil, err
				}
				// 真实渠道故障：计入失败，继续 failover
				failedKeys[apiKey] = true
				cfgManager.MarkKeyAsFailed(apiKey, executionAPIType)
				metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, metricsServiceType, requestID, metrics.FailureClassRetryable)
				channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, executionKind)
				recordModelCircuitFailure(c, metricsManager, upstream, apiKey, model, err.Error(), apiType)
				if markURLFailure != nil {
					markURLFailure(currentBaseURL)
				}
				// 步骤 10: 通知 autopilot FastDecayScorer 请求失败（连接错误）
				if notifyEndpointResultHook != nil {
					keyHash := autopilot.KeyHashFromAPIKey(apiKey)
					endpointUID := autopilot.GenerateEndpointUID(upstream.ChannelUID, currentBaseURL, keyHash)
					notifyEndpointResultHook(endpointUID, false)
				}
				// Phase 2: 记录 system header 过滤失败，触发渐进升级。
				// 仅对 system header 相关错误生效，避免无关故障误判为 header 不兼容。
				if kind == scheduler.ChannelKindMessages && upstream.ChannelUID != "" && isSystemHeaderError(err.Error()) {
					keyHash := autopilot.KeyHashFromAPIKey(apiKey)
					systemHeaderFilterCache.RecordFailure(upstream.ChannelUID, keyHash, redirectedModel, err.Error())
					tryUpgradeFilterLevel(upstream.ChannelUID, keyHash, redirectedModel)
				}
				// 记录渠道日志
				// 完成日志记录
				CompleteLog(channelLogStore, metricsKey, logRequestID, 0, false, err.Error(), isRetryAttempt)
				recordAttemptCompleted(c, logRequestID, upstream.ChannelUID, "upstream_error", 0, time.Since(attemptStartedAt).Milliseconds())
				RequestLogf(c, "[%s-Key] 警告: API密钥失败: %v", apiType, err)
				continue
			}

			// 学习上游限流头：动态调整限速器状态（cooldown 等）
			if rateLimitMgr := channelScheduler.GetRateLimitManager(); rateLimitMgr != nil {
				now := time.Now()
				if limiter := rateLimitMgr.Get(executionAPIType, executionIndex); limiter != nil {
					limiter.ApplyUpstreamHints(resp.Header, resp.StatusCode, now)
				}
				if selection.LimiterScope != "" {
					if limiter := rateLimitMgr.GetScoped(executionAPIType, executionIndex, selection.LimiterScope); limiter != nil {
						limiter.ApplyUpstreamHints(resp.Header, resp.StatusCode, now)
					}
				}
			}

			// 转换层指纹学习：new-api/one-api 系中转在任何响应（含错误响应）头中携带指纹，
			// 识别到即按渠道记忆，后续请求自动把 messages 中的 system 角色抽回顶层
			// （见 attempt 内的统一归一化判定）。注意这是学习式机制，本次请求不追溯生效。
			if kind == scheduler.ChannelKindMessages && upstream.ChannelUID != "" {
				if fingerprint := detectConverterFingerprint(resp.Header); fingerprint != "" {
					if converterUpstreamCache.Mark(upstream.ChannelUID, fingerprint) {
						RequestLogf(c, "[%s-Preprocess] 渠道 %s 识别到转换层指纹(%s)，后续请求将自动归一化 system 角色", apiType, upstream.Name, fingerprint)
					}
				}
			}

			// 通知 Autopilot 限速发现器（Phase 1 shadow，不修改调度链路）
			// endpointUID 与 profiler 同源：GenerateEndpointUID(channelUID, baseURL, keyHashFromAPIKey)
			// 复用已有的 metricsKey（与统计同源的身份指纹）
			// 非 2xx 响应：在读完 body 并分类后通知（带 reason），确保同一次 429
			// 只通知一次 Discoverer，避免先记普通 429、再记精确账号限流导致连续降速。
			keyHash := autopilot.KeyHashFromAPIKey(apiKey)
			signalEndpointUID := autopilot.GenerateEndpointUID(upstream.ChannelUID, currentBaseURL, keyHash)

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				respBodyBytes, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				respBodyBytes = utils.DecompressGzipIfNeeded(resp, respBodyBytes)

				// 记录错误响应头（用于诊断限流 header）
				LogUpstreamResponseHeaders(c, resp, envCfg, apiType)

				shouldFailover, isQuotaRelated := ShouldRetryWithNextKeyWithLogTag(resp.StatusCode, respBodyBytes, apiType, RequestLogTag(c))
				isTemporarilyOverloaded := IsUpstreamTemporarilyOverloaded(respBodyBytes)
				isAccountPoolUnavailable := IsUpstreamAccountPoolUnavailable(respBodyBytes)
				// 火山账号级 429 (AccountRateLimitExceeded)：精确识别，仅冷却当前 scope
				isAccountRateLimited := IsUpstreamAccountRateLimited(resp.StatusCode, respBodyBytes)

				// 非 2xx 响应：读完 body 分类后通知 Discoverer 一次（带 reason），
				// 避免先记普通 429、再记精确账号限流导致同一次响应连续降速两次。
				signalReason := ""
				if isAccountRateLimited {
					signalReason = string(autopilot.RateLimitReasonAccountRateLimitExceeded)
				}
				ratelimit.NotifySignal(
					upstream.ChannelUID, signalEndpointUID, metricsKey, executionAPIType, upstream.Name, isStream,
					time.Since(attemptStartedAt).Milliseconds(),
					resp.Header, resp.StatusCode, signalReason,
				)

				// 检查是否应永久拉黑该 Key（认证/权限/余额错误）
				blResult := ShouldBlacklistKey(resp.StatusCode, respBodyBytes)
				if blResult.ShouldBlacklist {
					isBalanceError := IsBalanceOrQuotaBlacklistReason(blResult.Reason)
					if !isBalanceError || upstream.IsAutoBlacklistBalanceEnabled() {
						blacklistMessage := blResult.Message
						if strings.EqualFold(apiType, "Vectors") {
							blacklistMessage = errorBodySummaryForLog(apiType, resp.StatusCode, respBodyBytes)
						}
						if err := cfgManager.BlacklistKeyWithRecoverAt(executionAPIType, executionIndex, apiKey, blResult.Reason, blacklistMessage, blResult.RecoverAt); err != nil {
							RequestLogf(c, "[%s-Blacklist] 拉黑 Key 失败: %v", apiType, err)
						}
						// 认证/权限/余额错误写入 global 桶：整把 Key 不可用，所有模型都受影响。
						recordModelCircuitGlobal(c, metricsManager, upstream, apiKey, blResult.Reason, apiType)
					}
				} else if restrictionReason := keyModelRestrictionReason(respBodyBytes); actualAttemptModel != "" && restrictionReason != "" &&
					(restrictionReason != "image_generation_not_enabled" || !upstream.IsStripImageGenerationToolEnabled()) {
					// 图片生成受限且用户未显式配置剥离：先学习"该组合需剥离图片工具"，
					// 由下一轮主动注入后重试修复请求，而不是直接惩罚这个 Key。
					// 只有学习已记录过（说明剥离后仍失败）才回落到限制 (Key,模型)。
					learnedImageStrip := false
					// 外层条件已保证 restrictionReason=="image_generation_not_enabled" 时
					// IsStripImageGenerationToolEnabled() 为 false（尚未学到/未过期种子判定为需要剥离），
					// 无需再判断字段是否被"用户显式设置"——该字段已不存在，六个兼容性开关完全交由学习决定。
					if restrictionReason == "image_generation_not_enabled" && upstream.ChannelUID != "" {
						keyHash := autopilot.KeyHashFromAPIKey(apiKey)
						summary := errorBodySummaryForLog(apiType, resp.StatusCode, respBodyBytes)
						// 记忆键用 attemptModel（而非 actualAttemptModel）：主动注入侧按 attemptModel 查询，
						// 两者在 provider 改写模型时会不一致，键不统一会导致学到的结论永远查不到。
						learnedImageStrip = channelCompatCache.Record(upstream.ChannelUID, keyHash, attemptModel,
							config.TraitStripImageGenTool, true, config.CompatSourceErrorSignal, summary)
						if learnedImageStrip {
							RequestLogf(c, "[%s-ChannelCompat] 渠道 %s 模型 %s 未开通图片生成，已记忆并将在重试时剥离图片工具",
								apiType, upstream.Name, attemptModel)
						}
					}
					if !learnedImageStrip {
						// 上游明确声明该模型或其 Codex 图片工具不受支持：限制该 Key 对这个实际模型的路由。
						// 仅限制 (Key, 模型) 组合（持久化+定时恢复），保留 failover 换渠道，不连累该 Key 其他模型。
						summary := errorBodySummaryForLog(apiType, resp.StatusCode, respBodyBytes)
						if err := cfgManager.DisableKeyModel(executionAPIType, executionIndex, apiKey, actualAttemptModel, restrictionReason, summary); err != nil {
							RequestLogf(c, "[%s-KeyModel] 限制 (Key,模型) 组合失败: %v", apiType, err)
						}
					}
				}

				// 上下文上限自学习（被动侧）：上游以 400/422 明确表示输入超出其上下文窗口时，
				// 记忆该 渠道-Key-模型 组合的实测上限，供后续路由硬约束提前排除该组合。
				//
				// 与其他兼容项不同，这里不做同 Key 重试：请求内容本身没有可自动改写之处，
				// 换 Key 也不会变短。学到结论后按正常流程 failover 到其他渠道，
				// 下一次同类请求由 SmartRouter 在选渠道阶段就避开这个组合。
				//
				// 位置在 shouldFailover 判断之前：上下文超限报错常含 "exceeded" 等词而被归为
				// 配额类可 failover 错误并提前 continue，放在后面会永远学不到。
				if (resp.StatusCode == 400 || resp.StatusCode == 422) && upstream.ChannelUID != "" {
					estimatedInput := estimatedInputTokensForContextLimit(c, attemptBody)
					if signal := ContextLimitFromError(resp.StatusCode, respBodyBytes, estimatedInput); signal != nil {
						if channelCompatCache.RecordContextLimit(upstream.ChannelUID, keyHash, attemptModel,
							signal.MaxInputTokens, signal.Source, signal.Evidence, estimatedInput) {
							RequestLogf(c, "[%s-ContextLimit] 渠道 %s 模型 %s 实测上下文上限 %d tokens（来源 %s，本次请求约 %d tokens），已记忆并将在后续路由中规避",
								apiType, upstream.Name, attemptModel, signal.MaxInputTokens, signal.Source, estimatedInput)
						}
					}
				}

				// 输出上限自学习（被动侧）：上游 400/422 明确指出 max_tokens/max_output_tokens
				// 超过其上限时，记忆该 渠道-Key-模型 组合的实测上限，并用同一 Key 以钳制后的值
				// 立即重试。与上下文上限不同，这里做同 Key 重试：下调输出 token 是可自动改写的
				// 方向（与模型注册表 clamp 的静默下调一致），请求内容本身没有被拒绝。
				// 仅当首次学到更严格结论且请求体字段确实需要下调时重试，避免死循环。
				if (resp.StatusCode == 400 || resp.StatusCode == 422) && upstream.ChannelUID != "" && !c.Writer.Written() {
					if requested := maxOutputTokensInBody(attemptBody, kind); requested > 0 {
						if signal := OutputLimitFromError(resp.StatusCode, respBodyBytes, requested); signal != nil {
							if channelCompatCache.RecordOutputLimit(upstream.ChannelUID, keyHash, attemptModel,
								signal.MaxOutputTokens, config.CompatSourceUpstreamDeclared, signal.Evidence, requested) {
								if _, changed := clampMaxTokensInBody(attemptBody, kind, signal.MaxOutputTokens); changed {
									retrySelection = selection
									retryAPIKey = apiKey
									metricsManager.RecordRequestFinalizeIgnored(currentBaseURL, apiKey, metricsServiceType, requestID)
									channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, executionKind)
									if probeKey := currentBaseURL + "|" + apiKey; probeAcquired[probeKey] {
										metricsManager.ReleaseProbe(currentBaseURL, apiKey, metricsServiceType)
										delete(probeAcquired, probeKey)
									}
									CompleteLog(channelLogStore, metricsKey, logRequestID, resp.StatusCode, false, string(respBodyBytes), isRetryAttempt)
									RequestLogf(c, "[%s-OutputLimit] 渠道 %s 模型 %s 拒绝输出 token %d（上限 %d），已记忆并下调后同 Key 重试",
										apiType, upstream.Name, attemptModel, requested, signal.MaxOutputTokens)
									continue
								}
							}
						}
					}
				}

				// 弃用参数自适应（被动侧）：上游以 400 明确拒绝某个采样参数时，
				// 记忆该 渠道-Key-模型 组合并用同一 Key 立即重试（剥离在下一轮循环开头生效）。
				// 仅当参数首次记录且请求体中确实存在该字段时重试，避免记忆已生效后死循环。
				if resp.StatusCode == 400 && upstream.ChannelUID != "" && !c.Writer.Written() {
					if param := DeprecatedParamFromError(respBodyBytes); param != "" {
						keyHash := autopilot.KeyHashFromAPIKey(apiKey)
						if _, present := StripDeprecatedParams(attemptBody, []string{param}); present &&
							deprecatedParamCache.Record(upstream.ChannelUID, keyHash, attemptModel, param) {
							retrySelection = selection
							retryAPIKey = apiKey
							metricsManager.RecordRequestFinalizeIgnored(currentBaseURL, apiKey, metricsServiceType, requestID)
							channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, executionKind)
							if probeKey := currentBaseURL + "|" + apiKey; probeAcquired[probeKey] {
								metricsManager.ReleaseProbe(currentBaseURL, apiKey, metricsServiceType)
								delete(probeAcquired, probeKey)
							}
							CompleteLog(channelLogStore, metricsKey, logRequestID, resp.StatusCode, false, string(respBodyBytes), isRetryAttempt)
							RequestLogf(c, "[%s-DeprecatedParam] 渠道 %s 模型 %s 拒绝参数 %q，已记忆并剥离后同 Key 重试",
								apiType, upstream.Name, attemptModel, param)
							continue
						}
					}
				}

				// 渠道兼容性自学习（被动侧）：上游以 400/422 明确表示缺少某项协议能力时，
				// 记忆该 渠道-Key-模型 组合并用同一 Key 立即重试（改写在下一轮循环开头注入）。
				// 仅当结论首次记录时重试，避免记忆已生效后死循环。
				//
				// 位置在 shouldFailover 判断之前：本包的错误分类以"大多数非 2xx 都 failover"为
				// 默认策略，未命中已知不可重试关键词的 400/422 会被判定为 shouldFailover=true
				// 并在下面 continue，若放在其后，developer role 降级等结论将永远学不到。
				// 与上面的弃用参数块是兄弟关系而非嵌套：弃用参数识别只针对 400，
				// 兼容性学习同时覆盖 400 与 422（部分上游用 422 表达结构不受支持）。
				if (resp.StatusCode == 400 || resp.StatusCode == 422) && upstream.ChannelUID != "" && !c.Writer.Written() {
					signalCtx := CompatSignalContext{
						HasDeveloperRole:       BodyHasDeveloperRole(attemptBody),
						HasCodexClientTools:    kind == scheduler.ChannelKindResponses,
						HasHistoricalThinking:  BodyHasHistoricalThinking(attemptBody),
						HasAnthropicBetaHeader: HeaderHasAnthropicBeta(c),
					}
					if signal := CompatTraitFromError(resp.StatusCode, respBodyBytes, signalCtx); signal != nil {
						keyHash := autopilot.KeyHashFromAPIKey(apiKey)
						if channelCompatCache.Record(upstream.ChannelUID, keyHash, attemptModel,
							signal.Trait, signal.Enabled, config.CompatSourceErrorSignal, signal.Evidence) {
							retrySelection = selection
							retryAPIKey = apiKey
							metricsManager.RecordRequestFinalizeIgnored(currentBaseURL, apiKey, metricsServiceType, requestID)
							channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, executionKind)
							if probeKey := currentBaseURL + "|" + apiKey; probeAcquired[probeKey] {
								metricsManager.ReleaseProbe(currentBaseURL, apiKey, metricsServiceType)
								delete(probeAcquired, probeKey)
							}
							CompleteLog(channelLogStore, metricsKey, logRequestID, resp.StatusCode, false, string(respBodyBytes), isRetryAttempt)
							RequestLogf(c, "[%s-ChannelCompat] 渠道 %s 模型 %s 缺少能力 %s，已记忆并应用兼容改写后同 Key 重试",
								apiType, upstream.Name, attemptModel, signal.Trait)
							continue
						}
					}
				}

				// document 能力自学习（被动侧）：请求携带 document 块且上游以 400/422 拒绝时，
				// 记忆该 渠道-Key-模型 组合不支持 document，供 SmartRouter 在选渠道阶段规避。
				// 与上下文上限同策略：只记录不做同 Key 重试（请求体没有可自动改写之处，
				// 剥掉 document 等于改变用户意图）；位置在 shouldFailover 之前，
				// 保证 invalid_request 类不可重试错误也能学到。
				// 放在弃用参数/compat-signal 块之后：具体原因（参数、developer role 等）优先被
				// 专属学习块截获，document 弱信号只兜住"通用 invalid_request"。
				if (resp.StatusCode == 400 || resp.StatusCode == 422) && upstream.ChannelUID != "" {
					if signal := DocumentUnsupportedFromError(resp.StatusCode, respBodyBytes, detectDocumentInBody(attemptBody)); signal != nil {
						keyHash := autopilot.KeyHashFromAPIKey(apiKey)
						summary := errorBodySummaryForLog(apiType, resp.StatusCode, respBodyBytes)
						if channelCompatCache.Record(upstream.ChannelUID, keyHash, attemptModel,
							config.TraitNoDocumentSupport, true, config.CompatSourceErrorSignal, summary) {
							strength := "弱信号"
							if signal.Strong {
								strength = "强信号"
							}
							RequestLogf(c, "[%s-DocumentCompat] 渠道 %s 模型 %s 拒绝 document 块（%s），已记忆并将在后续路由中规避",
								apiType, upstream.Name, attemptModel, strength)
						}
					}
				}

				// 工具调用能力自学习（被动侧·错误路径）：请求携带 tools 且上游以 400/422
				// 点名拒绝工具能力时，记忆该 渠道-Key-模型 组合不支持工具调用，供 SmartRouter
				// 在选渠道阶段规避。与 document 同策略：只记录不做同 Key 重试（剥掉 tools
				// 等于改变用户意图）；只认点名强信号，不做通用 invalid_request 弱信号归因
				// ——带 tools 的请求在 agent 流量中占比极高，弱信号误杀风险远大于 document。
				if (resp.StatusCode == 400 || resp.StatusCode == 422) && upstream.ChannelUID != "" {
					if signal := ToolUnsupportedFromError(resp.StatusCode, respBodyBytes, BodyHasTools(attemptBody)); signal != nil {
						keyHash := autopilot.KeyHashFromAPIKey(apiKey)
						if channelCompatCache.Record(upstream.ChannelUID, keyHash, attemptModel,
							config.TraitNoToolCallSupport, true, config.CompatSourceErrorSignal, signal.Evidence) {
							RequestLogf(c, "[%s-ToolCallCompat] 渠道 %s 模型 %s 拒绝工具调用（%s），已记忆并将在后续路由中规避",
								apiType, upstream.Name, attemptModel, signal.Evidence)
						}
					}
				}

				if shouldFailover {
					lastError = fmt.Errorf("上游错误: %d", resp.StatusCode)
					failedKeys[apiKey] = true
					cfgManager.MarkKeyAsFailed(apiKey, executionAPIType)
					failureClass := metrics.FailureClassRetryable
					if isQuotaRelated {
						failureClass = metrics.FailureClassQuota
					}
					if isTemporarilyOverloaded || isAccountPoolUnavailable || isAccountRateLimited {
						failureClass = metrics.FailureClassOverloaded
					}
					metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, metricsServiceType, requestID, failureClass)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, executionKind)
					// 仅 Retryable 计入模型级熔断：Quota 与 Overloaded（含账号级限流）
					// 反映的是额度或瞬时负载，已由 cooldown / scope 冷却处理，
					// 记到模型头上会让限流误伤该模型的可用性。
					if failureClass == metrics.FailureClassRetryable && !blResult.ShouldBlacklist {
						recordModelCircuitFailure(c, metricsManager, upstream, apiKey, model,
							errorBodySummaryForLog(apiType, resp.StatusCode, respBodyBytes), apiType)
					}
					if markURLFailure != nil {
						markURLFailure(currentBaseURL)
					}
					// 步骤 10: 通知 autopilot FastDecayScorer 请求失败（HTTP 错误）
					if notifyEndpointResultHook != nil {
						keyHash := autopilot.KeyHashFromAPIKey(apiKey)
						endpointUID := autopilot.GenerateEndpointUID(upstream.ChannelUID, currentBaseURL, keyHash)
						notifyEndpointResultHook(endpointUID, false)
					}
					// Phase 2: 记录 system header 过滤失败，触发渐进升级。
					// 仅对 system header 相关错误生效，避免无关故障误判为 header 不兼容。
					if kind == scheduler.ChannelKindMessages && upstream.ChannelUID != "" {
						keyHash := autopilot.KeyHashFromAPIKey(apiKey)
						errorSummary := errorBodySummaryForLog(apiType, resp.StatusCode, respBodyBytes)
						if isSystemHeaderError(errorSummary) {
							systemHeaderFilterCache.RecordFailure(upstream.ChannelUID, keyHash, redirectedModel, errorSummary)
							tryUpgradeFilterLevel(upstream.ChannelUID, keyHash, redirectedModel)
						}
					}
					errorSummary := errorBodySummaryForLog(apiType, resp.StatusCode, respBodyBytes)
					if errorSummary != "" {
						RequestLogf(c, "[%s-Key] 上游错误详情摘要: channel=[%d] %s, key=%s, summary=%s", apiType, executionIndex, upstream.Name, utils.MaskAPIKey(apiKey), errorSummary)
					}
					RequestLogf(c, "[%s-Key] 警告: API密钥失败 (状态: %d)，尝试下一个密钥", apiType, resp.StatusCode)

					lastFailoverError = &FailoverError{
						Status: resp.StatusCode,
						Body:   respBodyBytes,
					}

					// 记录渠道日志
					channelErrorInfo := string(respBodyBytes)
					if strings.EqualFold(apiType, "Vectors") {
						channelErrorInfo = errorBodySummaryForLog(apiType, resp.StatusCode, respBodyBytes)
					}
					CompleteLog(channelLogStore, metricsKey, logRequestID, resp.StatusCode, false, channelErrorInfo, isRetryAttempt)

					if isQuotaRelated {
						deprioritizeCandidates[apiKey] = true
						if selection.QuotaGroup != "" {
							failedQuotaGroups[selection.QuotaGroup] = true
						}
					}
					// 火山账号级 429：只冷却当前 key/quota scope，同渠道其他独立账号继续 failover。
					// scope 非空 → scoped cooldown + continue（尝试同渠道下一个独立 key）。
					// scope 为空（无 keypool 配置）→ 回退 channel cooldown + 切下一渠道。
					// 账号级 429 属可恢复临时限流，不永久 blacklist。
					if isAccountRateLimited {
						if selection.LimiterScope != "" {
							channelScheduler.MarkLimiterScopeCooldown(executionKind, executionIndex, selection.LimiterScope, upstreamAccountRateLimitCooldown)
							RequestLogf(c, "[%s-AccountRateLimit] 渠道 [%d] %s key=%s 账号级限流，冷却 scope=%s %s，尝试同渠道下一个独立 key",
								apiType, executionIndex, upstream.Name, utils.MaskAPIKey(apiKey), selection.LimiterScope, upstreamAccountRateLimitCooldown)
							continue
						}
						channelScheduler.MarkChannelCooldown(executionKind, executionIndex, upstreamAccountRateLimitCooldown)
						RequestLogf(c, "[%s-AccountRateLimit] 渠道 [%d] %s 账号级限流且无 scope，冷却渠道 %s 并尝试下一个渠道",
							apiType, executionIndex, upstream.Name, upstreamAccountRateLimitCooldown)
						return false, "", 0, lastFailoverError, nil, lastError
					}
					if isAccountPoolUnavailable {
						channelScheduler.MarkChannelCooldown(executionKind, executionIndex, upstreamAccountPoolCooldown)
						RequestLogf(c, "[%s-Channel] 渠道 [%d] %s 上游账号池不可用，冷却 %s 并尝试下一个渠道", apiType, executionIndex, upstream.Name, upstreamAccountPoolCooldown)
						return false, "", 0, lastFailoverError, nil, lastError
					}
					if isTemporarilyOverloaded {
						channelScheduler.MarkChannelCooldown(executionKind, executionIndex, upstreamOverloadedCooldown)
						RequestLogf(c, "[%s-Channel] 渠道 [%d] %s 上游临时过载，冷却 %s 并尝试下一个渠道", apiType, executionIndex, upstream.Name, upstreamOverloadedCooldown)
						return false, "", 0, lastFailoverError, nil, lastError
					}
					continue
				}

				// 非 failover 错误，记录失败指标后返回（请求已处理）
				clientStatusCode := normalizeUpstreamErrorStatus(resp.StatusCode, respBodyBytes)
				channelErrorInfo := string(respBodyBytes)
				if strings.EqualFold(apiType, "Vectors") {
					errorSummary := errorBodySummaryForLog(apiType, resp.StatusCode, respBodyBytes)
					channelErrorInfo = errorSummary
					if errorSummary != "" {
						RequestLogf(c, "[Vectors-UpstreamError] channel=[%d] %s status=%d original_model=%q mapped_model=%q summary=%s",
							executionIndex, upstream.Name, resp.StatusCode, model, actualAttemptModel, errorSummary)
					}
				}
				metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, metricsServiceType, requestID, metrics.FailureClassNonRetryable)
				channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, executionKind)
				// 不计入模型级熔断：本分支是 shouldFailover=false 的场景
				// （invalid_request / bad_request / 内容审核），问题出在请求本身而非
				// 渠道-模型健康度。换渠道换模型都不会让同一个畸形请求成功，
				// 记账只会让反复发送此类请求的客户端把健康渠道打成熔断。
				// 记录渠道日志
				CompleteLog(channelLogStore, metricsKey, logRequestID, clientStatusCode, false, channelErrorInfo, isRetryAttempt)
				c.Data(clientStatusCode, "application/json", respBodyBytes)
				return true, "", 0, nil, nil, nil
			}

			// 成功响应（2xx）：通知 Discoverer（header/success 路径），reason 为空
			ratelimit.NotifySignal(
				upstream.ChannelUID, signalEndpointUID, metricsKey, executionAPIType, upstream.Name, isStream,
				time.Since(attemptStartedAt).Milliseconds(),
				resp.Header, resp.StatusCode, "",
			)

			// 成功响应：处理 quota key 降级
			if deprioritizeKey != nil && len(deprioritizeCandidates) > 0 {
				for key := range deprioritizeCandidates {
					deprioritizeKey(key)
				}
			}

			streamingUserID := ""
			if isStream {
				streamingUserID = trackStreamingConversation(c, channelScheduler, kind, model, executionIndex, upstream.Name)
				StartStreamTimeoutObservation(c, channelLogStore, metricsKey, logRequestID, time.Now())
			}
			// Phase 3B-2: 回显自动模型映射信息（受 EchoMappedModel 配置门控）。
			// 必须在 handleSuccess 写出响应体之前设置：流式首包/非流式 JSON 一旦写出，
			// 再补 header 就会静默丢失（旧实现挂在完成后即有此 bug）。
			// 每次 attempt 覆盖/清除，防止映射失败的 attempt failover 后残留旧值。
			{
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
			usage, err = handleSuccess(c, resp, upstreamCopy, apiKey, attemptBody)
			// 上下文窗口自学习（放宽侧）：2xx 完成即实证该渠道×协议×模型可承载本次输入，
			// 棘轮只升不降。失败/取消/空响应不学习（err 非 nil 时内部直接返回）。
			MaybeRecordContextWindowProven(c, apiType, upstreamCopy, executionKind, attemptModel, usage, err)
			if isStream {
				FinishStreamTimeoutObservation(c)
				// 工具调用能力自学习（被动侧·成功路径）：强制 tool_choice 的请求 2xx
				// 完成但流式全程零工具调用块，说明上游不会执行工具（假模型/剥离 tools）。
				// 仅 messages/responses：只有这两条流式路径接了工具活动标记，
				// 其他协议 sawToolCall=false 无法区分"没调用"与"没观测"，不得学习。
				if executionKind == scheduler.ChannelKindMessages || executionKind == scheduler.ChannelKindResponses {
					MaybeLearnForcedToolChoiceMiss(c, upstream, apiKey, attemptModel, attemptBody,
						GetStreamTimeoutObserver(c).SawToolCall())
					// 安全分类能力自学习（被动侧·成功路径）：分类形状请求 2xx 完成但
					// 输出无 <severity> 标记，说明该渠道×模型不遵循格式约束。
					// 同样仅 messages/responses（只有这两条流式路径接了标记扫描）。
					MaybeLearnSeverityClassOutcome(c, upstream, apiKey, attemptModel, attemptBody,
						GetStreamTimeoutObserver(c).SawSeverityTag(), err)
				}
			} else if scanned, found := NonStreamSeverityOutcome(c); scanned {
				// 安全分类能力自学习（被动侧·成功路径，非流式）：CC 安全分类器子请求
				// 是非流式的（stream=false），只挂流式会漏掉全部此类请求。扫描结论由
				// messages/responses 的非流式成功处理写入（MarkNonStreamSeverityScan）；
				// 未接线路径不置位、不学习。
				MaybeLearnSeverityClassOutcome(c, upstream, apiKey, attemptModel, attemptBody, found, err)
			}
			if err != nil {
				if isStream && streamingUserID != "" {
					channelScheduler.UpdateConversationStatus(kind, streamingUserID, "active")
				}
				lastError = err
				// 区分客户端错误和渠道故障
				if isClientSideError(err) {
					// 客户端取消/断开：计入总请求数但不计入失败
					metricsManager.RecordRequestFinalizeClientCancel(currentBaseURL, apiKey, metricsServiceType, requestID)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, executionKind)
					RequestLogf(c, "[%s-Cancel] 请求已取消，停止渠道 failover", apiType)
					// 完成日志记录（客户端取消）
					CompleteLog(channelLogStore, metricsKey, logRequestID, http.StatusOK, false, "client canceled", isRetryAttempt)
				} else if errors.Is(err, ErrEmptyStreamResponse) || errors.Is(err, ErrInvalidResponseBody) || errors.Is(err, ErrEmptyNonStreamResponse) || errors.Is(err, ErrStreamFirstContentTimeout) || errors.Is(err, ErrStreamStalled) {
					// 空响应（流式 / 非流式）或无效响应体（如 HTML）或流式首字超时/断流：Header 未发送，可安全 failover
					retryKey := currentBaseURL + "|" + apiKey
					elapsed := time.Since(attemptStartedAt)
					if shouldRetryShortEmptyResponse(kind, err) && !shortEmptyRetried[retryKey] && elapsed <= shortEmptyResponseRetryWindow && !c.Writer.Written() {
						shortEmptyRetried[retryKey] = true
						retrySelection = selection
						retryAPIKey = apiKey
						metricsManager.RecordRequestFinalizeIgnored(currentBaseURL, apiKey, metricsServiceType, requestID)
						channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, executionKind)
						if probeAcquired[retryKey] {
							metricsManager.ReleaseProbe(currentBaseURL, apiKey, metricsServiceType)
							delete(probeAcquired, retryKey)
						}
						CompleteLog(channelLogStore, metricsKey, logRequestID, http.StatusOK, false, err.Error(), isRetryAttempt)
						RequestLogf(c, "[%s-EmptyResponse-Retry] 上游短空响应 (Key: %s, 耗时: %dms)，同渠道同 Key 重试一次",
							apiType, utils.MaskAPIKey(apiKey), elapsed.Milliseconds())
						continue
					}
					failedKeys[apiKey] = true
					cfgManager.MarkKeyAsFailed(apiKey, executionAPIType)
					metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, metricsServiceType, requestID, metrics.FailureClassRetryable)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, executionKind)
					// 空响应 / 无效响应体 / 首字超时 / 断流都是该渠道-模型确实不可用的表现。
					recordModelCircuitFailure(c, metricsManager, upstream, apiKey, model, err.Error(), apiType)
					if markURLFailure != nil {
						markURLFailure(currentBaseURL)
					}
					// 记录渠道日志
					CompleteLog(channelLogStore, metricsKey, logRequestID, http.StatusOK, false, err.Error(), isRetryAttempt)
					RequestLogf(c, "[%s-InvalidResponse] 上游返回无效响应 (Key: %s): %v，尝试下一个密钥", apiType, utils.MaskAPIKey(apiKey), err)
					continue
				} else if blErr, ok := err.(*ErrBlacklistKey); ok {
					// SSE 流内检测到拉黑条件：Header 未发送，可安全 failover + 拉黑 Key
					failedKeys[apiKey] = true
					isBalanceError := IsBalanceOrQuotaBlacklistReason(blErr.Reason)
					if !isBalanceError || upstream.IsAutoBlacklistBalanceEnabled() {
						if blacklistErr := cfgManager.BlacklistKey(executionAPIType, executionIndex, apiKey, blErr.Reason, blErr.Message); blacklistErr != nil {
							RequestLogf(c, "[%s-Blacklist] 拉黑 Key 失败: %v", apiType, blacklistErr)
						}
					}
					cfgManager.MarkKeyAsFailed(apiKey, executionAPIType)
					metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, metricsServiceType, requestID, metrics.FailureClassRetryable)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, executionKind)
					if markURLFailure != nil {
						markURLFailure(currentBaseURL)
					}
					CompleteLog(channelLogStore, metricsKey, logRequestID, http.StatusOK, false, fmt.Sprintf("key blacklisted: %s - %s", blErr.Reason, blErr.Message), isRetryAttempt)
					RequestLogf(c, "[%s-Blacklist] SSE 流内错误触发拉黑 (Key: %s, 原因: %s)，尝试下一个密钥", apiType, utils.MaskAPIKey(apiKey), blErr.Reason)
					continue
				} else {
					// 真实渠道故障：计入失败指标
					cfgManager.MarkKeyAsFailed(apiKey, executionAPIType)
					metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, metricsServiceType, requestID, metrics.FailureClassRetryable)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, executionKind)
					recordModelCircuitFailure(c, metricsManager, upstream, apiKey, model, err.Error(), apiType)
					// 记录渠道日志
					CompleteLog(channelLogStore, metricsKey, logRequestID, http.StatusOK, false, err.Error(), isRetryAttempt)
					RequestLogf(c, "[%s-Key] 警告: 响应处理失败: %v", apiType, err)
				}
				return true, "", 0, nil, usage, err
			}

			if markURLSuccess != nil {
				markURLSuccess(currentBaseURL)
			}
			// 步骤 9: 通知 autopilot FastDecayScorer 请求成功（§4.6.2a）
			if notifyEndpointResultHook != nil {
				keyHash := autopilot.KeyHashFromAPIKey(apiKey)
				endpointUID := autopilot.GenerateEndpointUID(upstream.ChannelUID, currentBaseURL, keyHash)
				notifyEndpointResultHook(endpointUID, true)
			}
			metricsManager.RecordRequestFinalizeSuccess(currentBaseURL, apiKey, metricsServiceType, requestID, usage)
			channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, executionKind)
			// 成功即解除该渠道-模型的失败累积；若这是熔断到期后放行的探针，
			// 同时重置退避级别并回收状态条目。
			recordModelCircuitSuccess(metricsManager, upstream, apiKey, model)

			// Phase 2: 记录 system header 过滤成功，巩固当前过滤层级。
			if kind == scheduler.ChannelKindMessages && upstream.ChannelUID != "" {
				keyHash := autopilot.KeyHashFromAPIKey(apiKey)
				systemHeaderFilterCache.Set(upstream.ChannelUID, keyHash, redirectedModel, getCurrentFilterLevel(upstream.ChannelUID, keyHash, redirectedModel))
			}

			if probeKey := currentBaseURL + "|" + apiKey; probeAcquired[probeKey] {
				metricsManager.ReleaseProbe(currentBaseURL, apiKey, metricsServiceType)
				delete(probeAcquired, probeKey)
			}
			// 记录渠道日志
			CompleteLog(channelLogStore, metricsKey, logRequestID, http.StatusOK, true, "", isRetryAttempt)
			recordAttemptCompleted(c, logRequestID, upstream.ChannelUID, "success", http.StatusOK, time.Since(attemptStartedAt).Milliseconds())

			// Phase 4 Item 8: 代理成功后回调（A/B 测试用）。
			// 在主响应已写回客户端之后触发，不影响主请求路径。
			if postSuccessfulProxyHook != nil {
				latencyMs := time.Since(attemptStartedAt).Milliseconds()
				// 复制 bodyBytes 防止异步使用时被原始切片回收
				bodyCopy := make([]byte, len(requestBody))
				copy(bodyCopy, requestBody)
				postSuccessfulProxyHook(string(executionKind), model, upstream.ChannelUID, http.StatusOK, latencyMs, bodyCopy)
			}

			// Phase 4 Item 4: 用量画像记录（渠道推荐用）。
			// 与上面的 A/B 测试回调同一时机（主响应已返回），纯观测性累积，不影响主请求路径。
			if usagePatternRecorderHook != nil {
				if proxyKeyMask := middleware.GetProxyKeyMask(c); proxyKeyMask != "" {
					usagePatternRecorderHook(proxyKeyMask, string(executionKind), upstream.ChannelUID, model)
				}
			}

			return true, apiKey, originalIdx, nil, usage, nil
		}

		// 当前 BaseURL 的所有 Key 都失败，记录并尝试下一个 BaseURL
		if envCfg.ShouldLog("info") && urlIdx < len(urlResults)-1 {
			RequestLogf(c, "[%s-BaseURL] BaseURL %d/%d 所有 Key 失败，切换到下一个 BaseURL", apiType, urlIdx+1, len(urlResults))
		}
	}

	return false, "", 0, lastFailoverError, nil, lastError
}

func shouldRetryShortEmptyResponse(kind scheduler.ChannelKind, err error) bool {
	if kind != scheduler.ChannelKindMessages {
		return false
	}
	return errors.Is(err, ErrEmptyStreamResponse) || errors.Is(err, ErrEmptyNonStreamResponse)
}

// BuildDefaultURLResults 将 URLs 转为按原始顺序的结果列表（无动态排序）
func BuildDefaultURLResults(urls []string) []warmup.URLLatencyResult {
	results := make([]warmup.URLLatencyResult, len(urls))
	for i, url := range urls {
		results[i] = warmup.URLLatencyResult{
			URL:         url,
			OriginalIdx: i,
			Success:     true,
		}
	}
	return results
}

func waitForHalfOpenProbe(ctx context.Context, metricsManager *metrics.MetricsManager, baseURL, apiKey, serviceType string) (bool, metrics.CircuitState) {
	if metricsManager == nil {
		return false, metrics.CircuitStateOpen
	}

	timer := time.NewTimer(halfOpenProbeWaitTimeout)
	defer timer.Stop()
	ticker := time.NewTicker(halfOpenProbePollInterval)
	defer ticker.Stop()

	lastState := metrics.CircuitStateHalfOpen
	for {
		select {
		case <-ctx.Done():
			return false, lastState
		case <-timer.C:
			return false, lastState
		case <-ticker.C:
			lastState = metricsManager.GetKeyCircuitState(baseURL, apiKey, serviceType)
			if lastState != metrics.CircuitStateHalfOpen {
				return false, lastState
			}
			if metricsManager.TryAcquireProbe(baseURL, apiKey, serviceType) {
				return true, metrics.CircuitStateHalfOpen
			}
		}
	}
}

// ── EndpointAttemptPolicy 辅助函数 ──
//
// 设计 §4.6.2a 十步执行顺序的 policy 注入点：
//  步骤 1: applyPolicyToURLs — FilterURLs + SortURLs（URL 循环前）
//  步骤 5-6: selectAttemptAPIKeyFiltered — FilterKeys + SortKeys（每个 baseURL 内 key 候选阶段）
//  步骤 9-10: notifyEndpointResult — 请求成功/失败后更新 FastDecay（通过 hook）

// applyPolicyToURLs 对 urlResults 应用 EndpointAttemptPolicy 的 FilterURLs 和 SortURLs。
// 步骤 1 + 步骤 2：过滤 + 排序 baseURL 列表。
// policy 为 nil 时原样返回。
// 任一 policy 函数 panic 时回退原列表（fail-open）。
func applyPolicyToURLs(policy *autopilot.EndpointAttemptPolicy, urlResults []warmup.URLLatencyResult, apiType string, c *gin.Context) (ret []warmup.URLLatencyResult) {
	if policy == nil || len(urlResults) == 0 {
		return urlResults
	}
	ret = urlResults // 默认回退：panic recover 时返回原列表

	// 整体 recovery：任一 policy 函数 panic 时回退原列表（fail-open）
	defer func() {
		if r := recover(); r != nil {
			RequestLogf(c, "[%s-Autopilot-EndpointPolicy] applyPolicyToURLs panic: %v，回退原列表", apiType, r)
			ret = urlResults
		}
	}()

	// 步骤 1: FilterURLs
	urls := make([]string, len(urlResults))
	for i, r := range urlResults {
		urls[i] = r.URL
	}
	filtered := callPolicyFilterURLs(policy, urls, apiType, c)

	// 步骤 2: SortURLs
	sorted, _ := callPolicySortURLs(policy, filtered, apiType, c)

	// 按排序后的 URL 重建 urlResults（保留 OriginalIdx）
	urlToResult := make(map[string]warmup.URLLatencyResult, len(urlResults))
	for _, r := range urlResults {
		urlToResult[r.URL] = r
	}
	result := make([]warmup.URLLatencyResult, 0, len(sorted))
	for _, url := range sorted {
		if r, ok := urlToResult[url]; ok {
			result = append(result, r)
		}
	}
	return result
}

// callPolicyFilterURLs 安全调用 policy.FilterURLs，panic 时回退原列表。
func callPolicyFilterURLs(policy *autopilot.EndpointAttemptPolicy, urls []string, apiType string, c *gin.Context) []string {
	if policy == nil || policy.FilterURLs == nil {
		return urls
	}
	result := policy.FilterURLs(urls)
	return result
}

// callPolicySortURLs 安全调用 policy.SortURLs，panic 时回退原列表。
// 使用 result-capture 模式确保 panic 时返回原始输入。
func callPolicySortURLs(policy *autopilot.EndpointAttemptPolicy, urls []string, apiType string, c *gin.Context) ([]string, []autopilot.EndpointCandidate) {
	if policy == nil || policy.SortURLs == nil {
		return urls, nil
	}
	var result []string
	var candidates []autopilot.EndpointCandidate
	func() {
		defer func() {
			if r := recover(); r != nil {
				RequestLogf(c, "[%s-Autopilot-EndpointPolicy] SortURLs panic: %v，回退原列表", apiType, r)
			}
		}()
		result, candidates = policy.SortURLs(urls)
	}()
	if len(result) == 0 {
		return urls, nil
	}
	return result, candidates
}

// selectAttemptAPIKeyFiltered 对 policy 过滤/排序后的 key 列表选择下一个可用 API key。
// 步骤 5 (FilterKeys) + 步骤 6 (SortKeys)：在 keypool.CandidatesForModel 之后应用 endpoint 级策略。
// 与 selectAttemptAPIKey 逻辑一致，但使用 policy 过滤/排序后的候选列表。
// policy 过滤/排序失败时回退到 selectAttemptAPIKey（fail-open）。
func selectAttemptAPIKeyFiltered(
	channelScheduler *scheduler.ChannelScheduler,
	kind scheduler.ChannelKind,
	channelIndex int,
	upstream *config.UpstreamConfig,
	baseURL string,
	failedKeys map[string]bool,
	failedQuotaGroups map[string]bool,
	model string,
	fallback NextAPIKeyFunc,
	policy *autopilot.EndpointAttemptPolicy,
	apiType string,
	c *gin.Context,
	circuitOpen keypool.ModelCircuitChecker,
	autoWeight keypool.AutoWeightFactor,
	pinAPIKey string,
) (keypool.Selection, string, error) {
	if policy == nil {
		return selectAttemptAPIKey(channelScheduler, kind, channelIndex, upstream, failedKeys, failedQuotaGroups, model, fallback, circuitOpen, autoWeight)
	}
	if baseURL == "" {
		baseURL = upstream.BaseURL
	}

	if !keypool.HasEffectiveConfig(upstream) {
		// 无 keypool 配置时：对 raw APIKeys 应用 policy filter/sort
		effectiveFailedKeys := failedKeysWithRestrictions(upstream, failedKeys, model, circuitOpen)
		apiKeys := make([]string, 0, len(upstream.APIKeys))
		for _, key := range upstream.APIKeys {
			if key != "" && !effectiveFailedKeys[key] {
				apiKeys = append(apiKeys, key)
			}
		}
		if len(apiKeys) == 0 {
			return keypool.Selection{}, "", fmt.Errorf("上游 %s 没有可用的API密钥", upstream.Name)
		}

		filtered := callPolicyFilterKeyBindings(policy, upstream.ChannelUID, baseURL, apiKeys, apiType, c)
		if len(filtered) == 0 {
			return keypool.Selection{}, "", fmt.Errorf("上游 %s 没有支持模型 %s 的 endpoint binding", upstream.Name, model)
		}
		sorted, _ := callPolicySortKeyBindings(policy, upstream.ChannelUID, baseURL, filtered, apiType, c)
		if fallback != nil {
			key, err := fallback(upstream, effectiveFailedKeys)
			if err != nil {
				key = ""
			}
			// 验证 key 在过滤后的列表中
			filteredSet := make(map[string]bool, len(sorted))
			for _, k := range sorted {
				filteredSet[k] = true
			}
			if key != "" && filteredSet[key] && !effectiveFailedKeys[key] {
				return keypool.Selection{APIKey: key}, key, nil
			}
		}
		// 五元组调度 pin：选中 key 在 policy 过滤集内时优先返回；
		// pin key 失败进入 failedKeys 后自然落到下方循环（兜底轮转）。
		if pinAPIKey != "" && !effectiveFailedKeys[pinAPIKey] {
			for _, key := range sorted {
				if key == pinAPIKey {
					return keypool.Selection{APIKey: key}, key, nil
				}
			}
		}
		for _, key := range sorted {
			if key != "" && !effectiveFailedKeys[key] {
				return keypool.Selection{APIKey: key}, key, nil
			}
		}
		return keypool.Selection{}, "", fmt.Errorf("上游 %s 没有可用的 endpoint binding", upstream.Name)
	}

	// keypool 路径：获取候选 → FilterKeys → SortKeys → 选择
	candidates := keypool.CandidatesForModelWeighted(upstream, failedKeys, model, circuitOpen, autoWeight)
	if len(candidates) == 0 {
		// 候选为空必须能区分"没配 Key"与"Key 全被运行时限制（禁用/持久限制/熔断）"，
		// 否则排障时只能看到笼统的"没有可用的API密钥"。
		RequestLogf(c, "[%s-KeySelection] 上游 %s 模型 %q keypool 候选为空 (configured=%d disabledKeys=%d failedKeys=%d disabledKeyModels=%d)",
			apiType, upstream.Name, model, len(upstream.APIKeys), len(upstream.DisabledAPIKeys), len(failedKeys), len(upstream.DisabledKeyModels))
		return keypool.Selection{}, "", fmt.Errorf("上游 %s 没有可用的API密钥", upstream.Name)
	}

	// 步骤 5: FilterKeys
	candidateKeys := make([]string, len(candidates))
	for i, cand := range candidates {
		candidateKeys[i] = cand.APIKey
	}
	filteredKeys := callPolicyFilterKeyBindings(policy, upstream.ChannelUID, baseURL, candidateKeys, apiType, c)
	if len(filteredKeys) == 0 {
		return keypool.Selection{}, "", fmt.Errorf("上游 %s 没有支持模型 %s 的 endpoint binding", upstream.Name, model)
	}

	// 步骤 6: SortKeys
	sortedKeys, _ := callPolicySortKeyBindings(policy, upstream.ChannelUID, baseURL, filteredKeys, apiType, c)

	// public-key-routing shadow 对比：同时计算忽略 opportunistic 的基线排序，
	// 如果基线首选与实际首选不同，记录差异供后续 trace/metrics 分析（不影响本次调度）。
	if policy != nil && policy.Mode == autopilot.RoutingModeAuto {
		baselineKeys := callPolicySortKeyBindingsBaseline(policy, upstream.ChannelUID, baseURL, filteredKeys, apiType, c)
		if len(baselineKeys) > 0 && len(sortedKeys) > 0 && baselineKeys[0] != sortedKeys[0] {
			c.Set("ccx.public_key_routing_shadow_diff", map[string]string{
				"actual":   sortedKeys[0],
				"baseline": baselineKeys[0],
			})
			RequestLogf(c, "[%s-PublicKeyRouting-ShadowDiff] 实际首选 %s 与基线首选 %s 不同",
				apiType, autopilot.MaskKey(sortedKeys[0]), autopilot.MaskKey(baselineKeys[0]))
		}
	}

	// 构建 candidate 查找表
	candidateMap := make(map[string]keypool.Candidate, len(candidates))
	for _, cand := range candidates {
		candidateMap[cand.APIKey] = cand
	}

	// 按 policy 排序后的顺序选择第一个可用 key。
	// 五元组调度 pin：选中 key 在过滤集内时提到选择序首位（swap 不破坏集合语义），
	// 失败/quota 组失败/限流 defer 由下方循环体统一处理，自然轮转后续 key。
	if pinAPIKey != "" {
		for i, k := range sortedKeys {
			if k == pinAPIKey && i > 0 {
				sortedKeys[0], sortedKeys[i] = sortedKeys[i], sortedKeys[0]
				break
			}
		}
	}
	var deferred []keypool.Selection
	for _, apiKey := range sortedKeys {
		if failedKeys[apiKey] {
			continue
		}
		cand, ok := candidateMap[apiKey]
		if !ok {
			continue
		}
		if cand.QuotaGroup != "" && failedQuotaGroups[cand.QuotaGroup] {
			continue
		}
		selection := keypool.Selection{
			APIKey:         cand.APIKey,
			CredentialID:   cand.Scope,
			CredentialName: cand.Config.Name,
			QuotaGroup:     cand.QuotaGroup,
			LimiterScope:   cand.Scope,
			Config:         cand.Config,
		}
		if channelScheduler != nil && selection.LimiterScope != "" {
			cfg := keypool.ConfigForCandidate(*upstream, selection.Config)
			deferForLoad, _, _ := channelScheduler.ShouldDeferForRateLimit(kind, channelIndex, selection.LimiterScope, cfg, time.Now())
			if deferForLoad {
				deferred = append(deferred, selection)
				continue
			}
		}
		return selection, cand.APIKey, nil
	}

	if len(deferred) > 0 {
		selection := deferred[0]
		return selection, selection.APIKey, nil
	}

	return keypool.Selection{}, "", fmt.Errorf("上游 %s 没有可用的API密钥", upstream.Name)
}

func callPolicyFilterKeyBindings(policy *autopilot.EndpointAttemptPolicy, channelUID, baseURL string, keys []string, apiType string, c *gin.Context) []string {
	if policy == nil || policy.FilterKeyBindings == nil {
		return callPolicyFilterKeys(policy, baseURL, keys, apiType, c)
	}
	result := keys
	func() {
		defer func() {
			if r := recover(); r != nil {
				RequestLogf(c, "[%s-Autopilot-EndpointPolicy] FilterKeyBindings panic: %v，回退原列表", apiType, r)
				result = keys
			}
		}()
		result = policy.FilterKeyBindings(channelUID, baseURL, keys)
	}()
	// 全灭 fail-open：binding 画像（健康/模型兼容）是推测性负信号，可能过期或误判。
	// 全灭时放行让真实上游裁决——真不支持则由 model_not_found 学习闭环持久化限制，
	// 误判则请求成功并带动画像恢复；避免"本地判死→本地拒绝→永远拿不到真实信号"的死锁。
	// 与 callPolicyFilterKeys 的空结果回退语义一致；局部过滤（剩部分 Key）不受影响。
	if len(result) == 0 && len(keys) > 0 {
		RequestLogf(c, "[%s-Autopilot-EndpointPolicy] FilterKeyBindings 渠道 %s %s 全部 %d 个 Key 被硬过滤，fail-open 回退原列表以暴露真实上游信号",
			apiType, channelUID, baseURL, len(keys))
		return keys
	}
	return result
}

func callPolicySortKeyBindings(policy *autopilot.EndpointAttemptPolicy, channelUID, baseURL string, keys []string, apiType string, c *gin.Context) ([]string, []autopilot.EndpointCandidate) {
	if policy == nil || policy.SortKeyBindings == nil {
		return callPolicySortKeys(policy, baseURL, keys, apiType, c)
	}
	result := keys
	var candidates []autopilot.EndpointCandidate
	func() {
		defer func() {
			if r := recover(); r != nil {
				RequestLogf(c, "[%s-Autopilot-EndpointPolicy] SortKeyBindings panic: %v，回退原列表", apiType, r)
				result = keys
				candidates = nil
			}
		}()
		result, candidates = policy.SortKeyBindings(channelUID, baseURL, keys)
	}()
	if len(result) == 0 {
		return keys, nil
	}
	return result, candidates
}

// callPolicySortKeyBindingsBaseline 安全调用 policy.SortKeyBindingsBaseline，panic 时回退原列表。
func callPolicySortKeyBindingsBaseline(policy *autopilot.EndpointAttemptPolicy, channelUID, baseURL string, keys []string, apiType string, c *gin.Context) []string {
	if policy == nil || policy.SortKeyBindingsBaseline == nil {
		return keys
	}
	result := keys
	func() {
		defer func() {
			if r := recover(); r != nil {
				RequestLogf(c, "[%s-Autopilot-EndpointPolicy] SortKeyBindingsBaseline panic: %v，回退原列表", apiType, r)
				result = keys
			}
		}()
		result, _ = policy.SortKeyBindingsBaseline(channelUID, baseURL, keys)
	}()
	if len(result) == 0 {
		return keys
	}
	return result
}

// callPolicyFilterKeys 安全调用 policy.FilterKeys，panic 时回退原列表。
func callPolicyFilterKeys(policy *autopilot.EndpointAttemptPolicy, baseURL string, keys []string, apiType string, c *gin.Context) []string {
	if policy == nil || policy.FilterKeys == nil {
		return keys
	}
	var result []string
	func() {
		defer func() {
			if r := recover(); r != nil {
				RequestLogf(c, "[%s-Autopilot-EndpointPolicy] FilterKeys panic: %v，回退原列表", apiType, r)
			}
		}()
		result = policy.FilterKeys(baseURL, keys)
	}()
	if len(result) == 0 {
		return keys
	}
	return result
}

// callPolicySortKeys 安全调用 policy.SortKeys，panic 时回退原列表。
// 使用 result-capture 模式确保 panic 时返回原始输入。
func callPolicySortKeys(policy *autopilot.EndpointAttemptPolicy, baseURL string, keys []string, apiType string, c *gin.Context) ([]string, []autopilot.EndpointCandidate) {
	if policy == nil || policy.SortKeys == nil {
		return keys, nil
	}
	var result []string
	var candidates []autopilot.EndpointCandidate
	func() {
		defer func() {
			if r := recover(); r != nil {
				RequestLogf(c, "[%s-Autopilot-EndpointPolicy] SortKeys panic: %v，回退原列表", apiType, r)
			}
		}()
		result, candidates = policy.SortKeys(baseURL, keys)
	}()
	if len(result) == 0 {
		return keys, nil
	}
	return result, candidates
}

func selectAttemptAPIKey(channelScheduler *scheduler.ChannelScheduler, kind scheduler.ChannelKind, channelIndex int, upstream *config.UpstreamConfig, failedKeys map[string]bool, failedQuotaGroups map[string]bool, model string, fallback NextAPIKeyFunc, circuitOpen keypool.ModelCircuitChecker, autoWeight keypool.AutoWeightFactor) (keypool.Selection, string, error) {
	if !keypool.HasEffectiveConfig(upstream) {
		if fallback == nil {
			return keypool.Selection{}, "", fmt.Errorf("上游 %s 没有可用的API密钥", upstream.Name)
		}
		apiKey, err := fallback(upstream, failedKeysWithRestrictions(upstream, failedKeys, model, circuitOpen))
		if err != nil {
			return keypool.Selection{}, "", err
		}
		return keypool.Selection{APIKey: apiKey}, apiKey, nil
	}

	var deferred []keypool.Selection
	for _, candidate := range keypool.CandidatesForModelWeighted(upstream, failedKeys, model, circuitOpen, autoWeight) {
		if candidate.QuotaGroup != "" && failedQuotaGroups[candidate.QuotaGroup] {
			continue
		}
		selection := keypool.Selection{
			APIKey:         candidate.APIKey,
			KeyUID:         candidate.KeyUID,
			CredentialID:   candidate.Scope,
			CredentialName: candidate.Config.Name,
			QuotaGroup:     candidate.QuotaGroup,
			LimiterScope:   candidate.Scope,
			Config:         candidate.Config,
		}
		if channelScheduler != nil && selection.LimiterScope != "" {
			cfg := keypool.ConfigForCandidate(*upstream, selection.Config)
			deferForLoad, _, _ := channelScheduler.ShouldDeferForRateLimit(kind, channelIndex, selection.LimiterScope, cfg, time.Now())
			if deferForLoad {
				deferred = append(deferred, selection)
				continue
			}
		}
		return selection, candidate.APIKey, nil
	}

	if len(deferred) > 0 {
		selection := deferred[0]
		return selection, selection.APIKey, nil
	}

	return keypool.Selection{}, "", fmt.Errorf("上游 %s 没有可用的API密钥", upstream.Name)
}

// recordModelCircuitGlobal 将整把 Key 都不可用的失败记入 global 桶。
func recordModelCircuitGlobal(c *gin.Context, metricsManager *metrics.MetricsManager,
	upstream *config.UpstreamConfig, apiKey, errSummary, apiType string) {
	if metricsManager == nil || upstream == nil || upstream.ChannelUID == "" {
		return
	}
	tracker := metricsManager.ModelCircuit()
	if tracker == nil {
		return
	}
	if opened, until := tracker.RecordModelFailure(
		upstream.ChannelUID, metrics.ModelCircuitKeyHash(apiKey), "", errSummary); opened {
		RequestLogf(c, "[%s-ModelCircuit] 渠道 %s Key 持续失败，暂停全模型调度至 %s",
			apiType, upstream.Name, until.Format(time.RFC3339))
	}
	// 整把 Key 不可用的失败同时计入 per-key 自动权重（软降权）。
	if aw := metricsManager.KeyAutoWeight(); aw != nil {
		aw.RecordFailure(upstream.ChannelUID, metrics.ModelCircuitKeyHash(apiKey), time.Now())
	}
}

// recordModelCircuitFailure 记录一次渠道-模型级失败。
//
// 只在失败确实反映该渠道-模型健康度时调用。以下场景由调用方负责排除：
// 配额/账号限流（已有 cooldown 与 scope 冷却机制）、内容审核（换渠道不改变请求内容）、
// 客户端取消、以及 RecordRequestFinalizeIgnored 的中间态重试。
//
// model 必须传客户端请求的原始模型，不要改用 attemptModel / redirectedModel：
// 读侧（scheduler 渠道级过滤、keypool Key 级过滤）都以原始模型为键，
// 写读不一致会让熔断在 AutoManaged 渠道上完全失效。详见 modelCircuitChecker 的说明。
func recordModelCircuitFailure(c *gin.Context, metricsManager *metrics.MetricsManager,
	upstream *config.UpstreamConfig, apiKey, model, errSummary, apiType string) {
	if metricsManager == nil || upstream == nil || upstream.ChannelUID == "" || model == "" {
		return
	}
	tracker := metricsManager.ModelCircuit()
	if tracker == nil {
		return
	}
	if opened, until := tracker.RecordModelFailure(
		upstream.ChannelUID, metrics.ModelCircuitKeyHash(apiKey), model, errSummary); opened {
		RequestLogf(c, "[%s-ModelCircuit] 渠道 %s 模型 %s 持续失败，暂停调度至 %s",
			apiType, upstream.Name, model, until.Format(time.RFC3339))
	}
}

// recordModelCircuitSuccess 记录一次成功，解除该组合的失败累积。
func recordModelCircuitSuccess(metricsManager *metrics.MetricsManager,
	upstream *config.UpstreamConfig, apiKey, model string) {
	if metricsManager == nil || upstream == nil || upstream.ChannelUID == "" || model == "" {
		return
	}
	if tracker := metricsManager.ModelCircuit(); tracker != nil {
		tracker.RecordModelSuccess(upstream.ChannelUID, metrics.ModelCircuitKeyHash(apiKey), model)
	}
	if aw := metricsManager.KeyAutoWeight(); aw != nil {
		aw.RecordSuccess(upstream.ChannelUID, metrics.ModelCircuitKeyHash(apiKey), time.Now())
	}
}

// keyAutoWeightFactor 构造 keypool 的 per-key 自动权重系数闭包。
//
// 读取 metrics 滑窗统计并完成 apiKey → keyHash 换算，让 keypool 不感知 metrics。
// 配置关闭（config scheduler.keyAutoWeight=false，main.go 热更新运行态）或
// metricsManager 缺失时返回 nil，keypool 排序退回纯手控 weight。
func keyAutoWeightFactor(metricsManager *metrics.MetricsManager) keypool.AutoWeightFactor {
	if metricsManager == nil || !config.RuntimeKeyAutoWeightEnabled() {
		return nil
	}
	tracker := metricsManager.KeyAutoWeight()
	if tracker == nil {
		return nil
	}
	return func(channelUID, apiKey string) float64 {
		return tracker.WeightFactor(channelUID, metrics.ModelCircuitKeyHash(apiKey), time.Now())
	}
}

// modelCircuitChecker 构造 keypool 的渠道-模型级熔断查询闭包。
//
// 把 apiKey → keyHash 的换算放在这里，让 keypool 无需感知哈希算法。
// metricsManager 为 nil 时返回 nil（fail-open，不做该项过滤）。
//
// circuitModel 是熔断表的模型键，固定取客户端请求的原始模型，**不使用** keypool
// 传入的模型参数。原因：熔断的读侧散布在三个层次，各自能拿到的最精确模型名不同——
// scheduler 选渠道时只有原始 model（渠道级 RedirectModel 尚未应用），keypool 选 Key
// 时有 redirectedModel，而记账发生在 autopilot 映射之后（attemptModel）。三者取值
// 不一致会让写入的键永远查不到，AutoManaged 渠道上熔断将完全失效。原始 model 是唯一
// 在三层都可得的标识，也贴合用户视角："这个渠道处理不了我请求的模型"。
//
// keypool 侧传入的 model 仍用于 per-key 白名单与 IsKeyModelDisabledNow 判定，
// 那两个机制按重定向后的模型比较才正确，因此签名保留该参数。
// 注意 IsKeyModelDisabledNow 在此只能覆盖手动 RedirectModel 的场景；autopilot
// 映射目标由发送前的复查兜底（见请求构建后的 KeyModel 复查块），两层合起来
// 才保证持久化限制对自动映射渠道生效。
func modelCircuitChecker(metricsManager *metrics.MetricsManager, circuitModel string) keypool.ModelCircuitChecker {
	if metricsManager == nil || circuitModel == "" {
		return nil
	}
	tracker := metricsManager.ModelCircuit()
	if tracker == nil {
		return nil
	}
	return func(channelUID, apiKey, _ string) bool {
		return tracker.IsModelCircuitOpen(channelUID, metrics.ModelCircuitKeyHash(apiKey), circuitModel)
	}
}

// failedKeysWithRestrictions 在持久化限制之外一并计入渠道-模型级运行时熔断。
//
// 无 keypool 配置的渠道走 raw APIKeys 路径，不经过 CandidatesForModelFiltered，
// 熔断过滤必须在这里补上，否则手工填写多 Key 的老渠道拿不到该保护。
func failedKeysWithRestrictions(upstream *config.UpstreamConfig, failedKeys map[string]bool, model string, circuitOpen keypool.ModelCircuitChecker) map[string]bool {
	effective := failedKeysWithPersistentRestrictions(upstream, failedKeys, model)
	if upstream == nil || model == "" || circuitOpen == nil || upstream.ChannelUID == "" {
		return effective
	}

	var merged map[string]bool
	for _, key := range upstream.APIKeys {
		if key == "" || effective[key] {
			continue
		}
		if !circuitOpen(upstream.ChannelUID, key, model) {
			continue
		}
		if merged == nil {
			merged = make(map[string]bool, len(effective)+1)
			for k, v := range effective {
				merged[k] = v
			}
		}
		merged[key] = true
	}
	if merged == nil {
		return effective
	}
	return merged
}

func failedKeysWithPersistentRestrictions(upstream *config.UpstreamConfig, failedKeys map[string]bool, model string) map[string]bool {
	if upstream == nil || (len(upstream.DisabledAPIKeys) == 0 && (model == "" || len(upstream.DisabledKeyModels) == 0)) {
		return failedKeys
	}

	var effective map[string]bool
	now := time.Now()
	for _, key := range upstream.APIKeys {
		keyDisabled := upstream.IsKeyDisabledNow(key, now)
		modelDisabled := model != "" && upstream.IsKeyModelDisabledNow(key, model, now)
		if !keyDisabled && !modelDisabled {
			continue
		}
		if effective == nil {
			effective = make(map[string]bool, len(failedKeys)+1)
			for failedKey, failed := range failedKeys {
				effective[failedKey] = failed
			}
		}
		effective[key] = true
	}
	if effective == nil {
		return failedKeys
	}
	return effective
}

func trackStreamingConversation(c *gin.Context, channelScheduler *scheduler.ChannelScheduler, kind scheduler.ChannelKind, model string, channelIndex int, channelName string) string {
	if c == nil || channelScheduler == nil {
		return ""
	}

	lastUserMsg, _ := c.Get("lastUserMessage")
	lastUserMsgStr, _ := lastUserMsg.(string)
	lastUserMsgs, _ := c.Get("lastUserMessages")
	lastUserMessages, _ := lastUserMsgs.([]string)
	userMsgCount, _ := c.Get("userMessageCount")
	userMsgCountInt, _ := userMsgCount.(int)
	if lastUserMsgStr == "" && userMsgCountInt == 0 {
		return ""
	}

	userID, _, agentRole, ok := RequestConversationContextFromGin(c)
	if !ok || userID == "" {
		return ""
	}
	if agentCtx := AgentContextFromGin(c); agentCtx != nil && agentRole == "" {
		agentRole = agentCtx.AgentRole
	}

	channelScheduler.TrackConversationWithStatusAndMessages(kind, userID, model, channelIndex, channelName, "", lastUserMsgStr, lastUserMessages, userMsgCountInt, agentRole, "streaming", AgentContextFromGin(c))
	return userID
}

// applySystemHeaderFilterToBody 对请求体的 system 字段应用分层过滤。
// 返回过滤后的请求体与是否发生修改。
func applySystemHeaderFilterToBody(body []byte, level providers.SystemHeaderFilterLevel) ([]byte, bool) {
	if level <= providers.LevelNoFilter {
		return body, false
	}

	var reqMap map[string]interface{}
	if err := json.Unmarshal(body, &reqMap); err != nil {
		return body, false
	}

	systemRaw, ok := reqMap["system"]
	if !ok || systemRaw == nil {
		return body, false
	}

	filtered, modified := providers.FilterSystemHeader(systemRaw, level)
	if !modified {
		return body, false
	}

	reqMap["system"] = filtered
	newBody, err := json.Marshal(reqMap)
	if err != nil {
		return body, false
	}
	return newBody, true
}

// getCurrentFilterLevel 获取当前缓存的过滤层级；无记录时返回 0（不过滤）。
func getCurrentFilterLevel(channelUID, keyHash, model string) int {
	if entry := systemHeaderFilterCache.Get(channelUID, keyHash, model); entry != nil {
		return entry.Level
	}
	return 0
}

// tryUpgradeFilterLevel 在失败次数达到阈值时升级到下一过滤层级。
// 层级上限为 3（LevelFirstBlock）。
// 只有 system header 相关错误才会触发升级。
func tryUpgradeFilterLevel(channelUID, keyHash, model string) {
	entry := systemHeaderFilterCache.Get(channelUID, keyHash, model)
	if entry == nil {
		return
	}
	const failureUpgradeThreshold = 3
	if entry.FailureCount >= failureUpgradeThreshold && entry.Level < int(providers.LevelFirstBlock) {
		systemHeaderFilterCache.Set(channelUID, keyHash, model, entry.Level+1)
	}
}

// isSystemHeaderError 判断错误是否与 system header 相关。
// 与 providers.isSystemHeaderError 保持一致的 keyword 检测，用于 failover 失败升级。
func isSystemHeaderError(errStr string) bool {
	errStr = strings.ToLower(errStr)
	keywords := []string{
		"system",
		"billing",
		"cch",
		"header",
		"instruction",
		"prompt",
	}
	for _, keyword := range keywords {
		if strings.Contains(errStr, keyword) {
			return true
		}
	}
	return false
}

// atomicModelEffortRewrite 原子地改写请求体中的 model 和 effort。
// 保证：如果 effort 改写失败，model 也保持不变（原子性）。
// 返回 (newBody, true) 表示成功，(originalBody, false) 表示失败或无需改写。
// isEffortClampedByClient 判断客户端显式声明的 effort 是否被 autopilot 的选择钳位。
// 仅当客户端 effort 的序数值严格低于 autopilot 选择的 effort 时返回 true。
// 客户端未显式声明 effort 时返回 false。
func isEffortClampedByClient(clientRaw string, clientExplicit bool, targetEffort autopilot.EffortLevel) bool {
	if !clientExplicit {
		return false
	}
	clientOrd := autopilot.EffortLevelOrdinal(autopilot.NormalizeEffortLevel(clientRaw))
	targetOrd := autopilot.EffortLevelOrdinal(autopilot.NormalizeEffortLevel(string(targetEffort)))
	return clientOrd >= 0 && targetOrd >= 0 && clientOrd < targetOrd
}

func atomicModelEffortRewrite(body []byte, target *autopilot.ResolvedRouteTarget, upstream *config.UpstreamConfig, kind scheduler.ChannelKind) ([]byte, bool) {
	if target == nil || target.Model == "" {
		return body, false
	}

	// Step 1: 改写 model
	modelBody, err := sjson.SetBytes(body, "model", target.Model)
	if err != nil {
		return body, false
	}

	// Step 2: 如果 effort 由 Autopilot 决定，注入 reasoning params
	if target.EffortDecided && target.Effort != "" {
		// adaptive_only guard: ThinkingMode=adaptive_only 的模型不注入 thinking params
		// （模型自身决定思考深度，Autopilot 只负责模型选择）
		cap := config.ResolveUpstreamCapability(target.Model, upstream, nil)
		if cap.Known && cap.Capability.ThinkingMode == "adaptive_only" {
			// 只改写 model，不注入 effort
			return modelBody, true
		}

		style := effortInjectionStyle(kind, upstream)
		if style == "" {
			// 该渠道类型不接受思考参数（images/vectors），只改写 model。
			return modelBody, true
		}
		var reqMap map[string]interface{}
		if err := json.Unmarshal(modelBody, &reqMap); err != nil {
			return body, false
		}
		config.ApplyReasoningParamStyle(reqMap, style, string(target.Effort))
		effortBody, err := json.Marshal(reqMap)
		if err != nil {
			return body, false
		}
		return effortBody, true
	}

	return modelBody, true
}

// effortInjectionStyle 决定该渠道的 effort 注入形态。
//
// 判定依据是渠道种类与 ServiceType，而不是模型名匹配：
//   - gemini 渠道（或 ServiceType=gemini 的上游）走 Gemini 原生 thinkingConfig 形态；
//   - images / vectors 渠道不接受思考参数，返回空串表示"不注入"；
//   - 其余渠道沿用渠道配置的 ReasoningParamStyle，缺省为 Responses 的 reasoning 对象形态。
func effortInjectionStyle(kind scheduler.ChannelKind, upstream *config.UpstreamConfig) string {
	// 不支持思考参数的物理路由始终禁用注入；Gemini 物理路由始终使用原生形态。
	switch kind {
	case scheduler.ChannelKindImages, scheduler.ChannelKindVectors:
		return ""
	case scheduler.ChannelKindGemini:
		return config.ReasoningParamStyleGemini
	}
	serviceType := ""
	if upstream != nil {
		serviceType = strings.ToLower(strings.TrimSpace(upstream.ServiceType))
	}
	switch serviceType {
	case "gemini":
		return config.ReasoningParamStyleGemini
	case "openai":
		return "reasoning_effort"
	case "responses", "copilot":
		return "reasoning"
	case "claude":
		return "thinking"
	}
	if upstream != nil && upstream.ReasoningParamStyle != "" {
		return upstream.ReasoningParamStyle
	}
	// ServiceType 缺失时保留旧调用方的 kind 回退；route-aware 调用优先使用上游物理服务类型。
	switch kind {
	case scheduler.ChannelKindMessages:
		return "thinking"
	case scheduler.ChannelKindChat:
		return "reasoning_effort"
	default:
		return "reasoning"
	}
}
