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
}

// WithSelectionTrace 将调度器的选择摘要写入后续渠道请求日志。
func WithSelectionTrace(selection *scheduler.SelectionResult) TryUpstreamOption {
	return func(opts *tryUpstreamOptions) {
		if opts == nil || selection == nil {
			return
		}
		opts.channelLogOptions = append(opts.channelLogOptions, WithChannelSelectionTrace(
			selection.Reason,
			scheduler.FormatSelectionTraceSummary(selection.Trace, 4),
		))
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
var channelCompatCache = config.NewChannelCompatCacheWithPersistence(config.ChannelCompatStatePath)

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

	tryOpts := tryUpstreamOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&tryOpts)
		}
	}

	metricsServiceType := scheduler.NormalizedMetricsServiceType(kind, upstream.ServiceType)

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
			RequestLogf(c, "[%s-Vision] 跳过不支持视觉的渠道 [%d] %s", apiType, channelIndex, upstream.Name)
			return false, "", 0, nil, nil, fmt.Errorf("channel %s does not support vision", upstream.Name)
		}
		if isNoVisionModel(upstream, redirectedModel) {
			if upstream.VisionFallbackModel != "" {
				fallback := upstream.VisionFallbackModel
				RequestLogf(c, "[%s-Vision] 模型 %s 不支持视觉，使用 fallback: %s (渠道 [%d] %s)", apiType, redirectedModel, fallback, channelIndex, upstream.Name)
				if replaced, err := sjson.SetBytes(requestBody, "model", fallback); err == nil {
					requestBody = replaced
				}
				redirectedModel = fallback
				capabilityRequestModel = fallback
				if err := channelScheduler.ValidateUpstreamContext(kind, redirectedModel, upstream, contextRequirement); err != nil {
					RequestLogf(c, "[%s-Vision] fallback 模型 %s 不满足上下文需求，跳过渠道 [%d] %s: %v", apiType, redirectedModel, channelIndex, upstream.Name, err)
					return false, "", 0, nil, nil, err
				}
			} else {
				RequestLogf(c, "[%s-Vision] 模型 %s 不支持视觉且无 fallback，跳过渠道 [%d] %s", apiType, redirectedModel, channelIndex, upstream.Name)
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
			// Claude Messages 入口：将 messages 中的 system 角色抽回顶层 system 字段。
			// 在 provider 分发前统一处理，使所有上游类型（claude/openai/gemini/responses）均生效，
			// 兼容 Opus 4.8 / Fable 5 等将 system 作为消息 role 发送、而旧上游仅支持 user/assistant 的情况。
			if kind == scheduler.ChannelKindMessages && upstream.NormalizeSystemRoleToTopLevel {
				attemptBody = providers.NormalizeSystemRoleToTopLevel(attemptBody)
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
				selection, apiKey, err = selectAttemptAPIKeyFiltered(channelScheduler, kind, channelIndex, upstream, currentBaseURL, failedKeys, failedQuotaGroups, redirectedModel, nextAPIKey, endpointPolicy, apiType, c)
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

				// 优先使用新 API（原子 model+effort），回退到旧 API（仅 model）
				var target *autopilot.ResolvedRouteTarget
				if endpointPolicy.ResolvedTargetForBinding != nil {
					target = endpointPolicy.ResolvedTargetForBinding(upstream.ChannelUID, currentBaseURL, apiKey)
				}
				if target == nil && endpointPolicy.ResolvedTargetByEndpointUID != nil {
					target = endpointPolicy.ResolvedTargetByEndpointUID(euid)
				}
				if target == nil && endpointPolicy.ResolvedModelByEndpointUID != nil {
					if mm := endpointPolicy.ResolvedModelByEndpointUID(euid); mm != "" {
						target = &autopilot.ResolvedRouteTarget{Model: mm}
					}
				}
				if target != nil && target.Model != "" {
					// Atomic rewrite: model + effort together
					attemptBody, rewriteOk := atomicModelEffortRewrite(attemptBody, target, upstreamCopy, kind)
					if rewriteOk {
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
				}
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

			// 内容启发式兼容项（无上游报错信号）：首次遇到该组合时异步探测一次并记忆，
			// 不阻塞当前请求，结论对后续请求生效。替代原先要用户手工点诊断按钮的做法。
			maybeTriggerCompatProbe(upstream, apiKey, currentBaseURL, attemptModel)

			// 渠道兼容性自学习（主动侧）：命中记忆时把学习结论注入本次请求所用的上游副本。
			// 实际改写由各 provider 在构造上游请求时执行（那里才知道协议形态），此处只传递结论。
			if upstream.ChannelUID != "" {
				keyHash := autopilot.KeyHashFromAPIKey(apiKey)
				if state, ok := channelCompatCache.Trait(upstream.ChannelUID, keyHash, attemptModel, config.TraitDowngradeDeveloperRole); ok && state.Enabled {
					upstreamCopy.LearnedDowngradeDeveloperRole = true
					channelCompatCache.MarkApplied(upstream.ChannelUID, keyHash, attemptModel, config.TraitDowngradeDeveloperRole)
				}
				if state, ok := channelCompatCache.Trait(upstream.ChannelUID, keyHash, attemptModel, config.TraitStripImageGenTool); ok && state.Enabled {
					if upstreamCopy.StripImageGenerationTool == nil {
						upstreamCopy.StripImageGenerationTool = config.BoolPtr(true)
						channelCompatCache.MarkApplied(upstream.ChannelUID, keyHash, attemptModel, config.TraitStripImageGenTool)
					}
				}
				if state, ok := channelCompatCache.Trait(upstream.ChannelUID, keyHash, attemptModel, config.TraitStripCodexClientTools); ok && state.Enabled {
					if upstreamCopy.CodexToolCompat == nil {
						upstreamCopy.CodexToolCompat = config.BoolPtr(true)
						channelCompatCache.MarkApplied(upstream.ChannelUID, keyHash, attemptModel, config.TraitStripCodexClientTools)
					}
				}
				if state, ok := channelCompatCache.Trait(upstream.ChannelUID, keyHash, attemptModel, config.TraitPassbackThinkingBlocks); ok && state.Enabled {
					if upstreamCopy.PassbackThinkingBlocks == nil {
						upstreamCopy.PassbackThinkingBlocks = config.BoolPtr(true)
						channelCompatCache.MarkApplied(upstream.ChannelUID, keyHash, attemptModel, config.TraitPassbackThinkingBlocks)
					}
				}
				// 探针学习的内容启发式兼容项：结论可能为 false（探测确认不需要），
				// 因此按 state.Enabled 原值注入而非只在 true 时注入。
				if state, ok := channelCompatCache.Trait(upstream.ChannelUID, keyHash, attemptModel, config.TraitPassbackReasoningContent); ok {
					if upstreamCopy.PassbackReasoningContent == nil {
						upstreamCopy.PassbackReasoningContent = config.BoolPtr(state.Enabled)
						channelCompatCache.MarkApplied(upstream.ChannelUID, keyHash, attemptModel, config.TraitPassbackReasoningContent)
					}
				}
				if state, ok := channelCompatCache.Trait(upstream.ChannelUID, keyHash, attemptModel, config.TraitStripEmptyTextBlocks); ok {
					if upstreamCopy.StripEmptyTextBlocks == nil {
						upstreamCopy.StripEmptyTextBlocks = config.BoolPtr(state.Enabled)
						channelCompatCache.MarkApplied(upstream.ChannelUID, keyHash, attemptModel, config.TraitStripEmptyTextBlocks)
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
				if limiter := rateLimitMgr.Get(apiType, channelIndex); limiter != nil {
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
					keyLimiter := rateLimitMgr.GetOrCreateScoped(apiType, channelIndex, selection.LimiterScope, keypool.ConfigForCandidate(*upstream, selection.Config))
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
			actualOriginalModel := ""
			if actualAttemptModel != model {
				actualOriginalModel = model
			}

			// 记录请求开始
			channelScheduler.RecordRequestStart(currentBaseURL, apiKey, metricsServiceType, kind)

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

			// 创建 pending 状态日志（附带代理上下文与会话标识，用于 subagent 观测）
			logRequestID := CreatePendingLog(channelLogStore, metricsKey, channelIndex, upstream.Name, actualAttemptModel, actualOriginalModel, originalReasoningEffort, actualReasoningEffort, apiKey, currentBaseURL, apiType, operation, metrics.RequestSourceProxy, AgentContextFromGin(c), SessionIDFromGin(c), logOpts...)

			// 向 Autopilot trace 追加一条 "started" endpoint 尝试摘要（fail-open）
			attemptTraceUID, _ := c.Get("ccx.autopilot_trace_uid")
			if uid, ok := attemptTraceUID.(string); ok && uid != "" {
				recordEndpointAttempt(uid, autopilot.EndpointAttemptSummary{
					AttemptUID:    logRequestID,
					Status:        "started",
					ChannelUID:    upstream.ChannelUID,
					EndpointLabel: autopilot.DeriveEndpointLabel(upstream.ChannelUID, 0),
					Result:        "attempt_failed",
				})
			}

			// TCP 建连开始即计数：将活跃度统计提前到发起上游请求之前；同时关联 proxyKeyMask 用于成本报表持久化
			requestID := metricsManager.RecordRequestConnectedWithProxyKeyMask(currentBaseURL, apiKey, metricsServiceType, actualAttemptModel, proxyKeyMask)

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
			resp, err := SendRequestWithLifecycleTrace(req, requestUpstream, envCfg, isStream, apiType, lifecycleTrace)
			if err != nil {
				lastError = err
				// 区分客户端取消和真实渠道故障（统一口径）
				if isClientSideError(err) {
					// 客户端取消：不计入失败，不触发 failover
					metricsManager.RecordRequestFinalizeClientCancel(currentBaseURL, apiKey, metricsServiceType, requestID)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, kind)
					// 完成日志记录（客户端取消）
					CompleteLog(channelLogStore, metricsKey, logRequestID, 0, false, "client canceled", isRetryAttempt)
					RequestLogf(c, "[%s-Cancel] 请求已取消（SendRequest 阶段）", apiType)
					return true, "", 0, nil, nil, err
				}
				// 真实渠道故障：计入失败，继续 failover
				failedKeys[apiKey] = true
				cfgManager.MarkKeyAsFailed(apiKey, apiType)
				metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, metricsServiceType, requestID, metrics.FailureClassRetryable)
				channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, kind)
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
				if limiter := rateLimitMgr.Get(apiType, channelIndex); limiter != nil {
					limiter.ApplyUpstreamHints(resp.Header, resp.StatusCode, now)
				}
				if selection.LimiterScope != "" {
					if limiter := rateLimitMgr.GetScoped(apiType, channelIndex, selection.LimiterScope); limiter != nil {
						limiter.ApplyUpstreamHints(resp.Header, resp.StatusCode, now)
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

				shouldFailover, isQuotaRelated := ShouldRetryWithNextKeyWithLogTag(resp.StatusCode, respBodyBytes, cfgManager.GetFuzzyModeEnabled(), apiType, RequestLogTag(c))
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
					signalEndpointUID, metricsKey, apiType, upstream.Name, isStream,
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
						if err := cfgManager.BlacklistKeyWithRecoverAt(apiType, channelIndex, apiKey, blResult.Reason, blacklistMessage, blResult.RecoverAt); err != nil {
							RequestLogf(c, "[%s-Blacklist] 拉黑 Key 失败: %v", apiType, err)
						}
					}
				} else if restrictionReason := keyModelRestrictionReason(respBodyBytes); actualAttemptModel != "" && restrictionReason != "" &&
					(restrictionReason != "image_generation_not_enabled" || !upstream.IsStripImageGenerationToolEnabled()) {
					// 图片生成受限且用户未显式配置剥离：先学习"该组合需剥离图片工具"，
					// 由下一轮主动注入后重试修复请求，而不是直接惩罚这个 Key。
					// 只有学习已记录过（说明剥离后仍失败）才回落到限制 (Key,模型)。
					learnedImageStrip := false
					if restrictionReason == "image_generation_not_enabled" && upstream.ChannelUID != "" &&
						upstream.StripImageGenerationTool == nil {
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
						if err := cfgManager.DisableKeyModel(apiType, channelIndex, apiKey, actualAttemptModel, restrictionReason, summary); err != nil {
							RequestLogf(c, "[%s-KeyModel] 限制 (Key,模型) 组合失败: %v", apiType, err)
						}
					}
				}

				if shouldFailover {
					lastError = fmt.Errorf("上游错误: %d", resp.StatusCode)
					failedKeys[apiKey] = true
					cfgManager.MarkKeyAsFailed(apiKey, apiType)
					failureClass := metrics.FailureClassRetryable
					if isQuotaRelated {
						failureClass = metrics.FailureClassQuota
					}
					if isTemporarilyOverloaded || isAccountPoolUnavailable || isAccountRateLimited {
						failureClass = metrics.FailureClassOverloaded
					}
					metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, metricsServiceType, requestID, failureClass)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, kind)
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
						RequestLogf(c, "[%s-Key] 上游错误详情摘要: channel=[%d] %s, key=%s, summary=%s", apiType, channelIndex, upstream.Name, utils.MaskAPIKey(apiKey), errorSummary)
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
							channelScheduler.MarkLimiterScopeCooldown(kind, channelIndex, selection.LimiterScope, upstreamAccountRateLimitCooldown)
							RequestLogf(c, "[%s-AccountRateLimit] 渠道 [%d] %s key=%s 账号级限流，冷却 scope=%s %s，尝试同渠道下一个独立 key",
								apiType, channelIndex, upstream.Name, utils.MaskAPIKey(apiKey), selection.LimiterScope, upstreamAccountRateLimitCooldown)
							continue
						}
						channelScheduler.MarkChannelCooldown(kind, channelIndex, upstreamAccountRateLimitCooldown)
						RequestLogf(c, "[%s-AccountRateLimit] 渠道 [%d] %s 账号级限流且无 scope，冷却渠道 %s 并尝试下一个渠道",
							apiType, channelIndex, upstream.Name, upstreamAccountRateLimitCooldown)
						return false, "", 0, lastFailoverError, nil, lastError
					}
					if isAccountPoolUnavailable {
						channelScheduler.MarkChannelCooldown(kind, channelIndex, upstreamAccountPoolCooldown)
						RequestLogf(c, "[%s-Channel] 渠道 [%d] %s 上游账号池不可用，冷却 %s 并尝试下一个渠道", apiType, channelIndex, upstream.Name, upstreamAccountPoolCooldown)
						return false, "", 0, lastFailoverError, nil, lastError
					}
					if isTemporarilyOverloaded {
						channelScheduler.MarkChannelCooldown(kind, channelIndex, upstreamOverloadedCooldown)
						RequestLogf(c, "[%s-Channel] 渠道 [%d] %s 上游临时过载，冷却 %s 并尝试下一个渠道", apiType, channelIndex, upstream.Name, upstreamOverloadedCooldown)
						return false, "", 0, lastFailoverError, nil, lastError
					}
					continue
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
							channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, kind)
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

					// 渠道兼容性自学习（被动侧）：上游以 400/422 明确表示缺少某项协议能力时，
					// 记忆该 渠道-Key-模型 组合并用同一 Key 立即重试（改写在下一轮循环开头注入）。
					// 仅当结论首次记录时重试，避免记忆已生效后死循环。
					if (resp.StatusCode == 400 || resp.StatusCode == 422) && upstream.ChannelUID != "" && !c.Writer.Written() {
						signalCtx := CompatSignalContext{
							HasDeveloperRole:      BodyHasDeveloperRole(attemptBody),
							HasCodexClientTools:   kind == scheduler.ChannelKindResponses,
							HasHistoricalThinking: BodyHasHistoricalThinking(attemptBody),
						}
						if signal := CompatTraitFromError(resp.StatusCode, respBodyBytes, signalCtx); signal != nil {
							keyHash := autopilot.KeyHashFromAPIKey(apiKey)
							if channelCompatCache.Record(upstream.ChannelUID, keyHash, attemptModel,
								signal.Trait, signal.Enabled, config.CompatSourceErrorSignal, signal.Evidence) {
								retrySelection = selection
								retryAPIKey = apiKey
								metricsManager.RecordRequestFinalizeIgnored(currentBaseURL, apiKey, metricsServiceType, requestID)
								channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, kind)
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
				}

				// 非 failover 错误，记录失败指标后返回（请求已处理）
				clientStatusCode := normalizeUpstreamErrorStatus(resp.StatusCode, respBodyBytes)
				channelErrorInfo := string(respBodyBytes)
				if strings.EqualFold(apiType, "Vectors") {
					errorSummary := errorBodySummaryForLog(apiType, resp.StatusCode, respBodyBytes)
					channelErrorInfo = errorSummary
					if errorSummary != "" {
						RequestLogf(c, "[Vectors-UpstreamError] channel=[%d] %s status=%d original_model=%q mapped_model=%q summary=%s",
							channelIndex, upstream.Name, resp.StatusCode, model, actualAttemptModel, errorSummary)
					}
				}
				metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, metricsServiceType, requestID, metrics.FailureClassNonRetryable)
				channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, kind)
				// 记录渠道日志
				CompleteLog(channelLogStore, metricsKey, logRequestID, clientStatusCode, false, channelErrorInfo, isRetryAttempt)
				c.Data(clientStatusCode, "application/json", respBodyBytes)
				return true, "", 0, nil, nil, nil
			}

			// 成功响应（2xx）：通知 Discoverer（header/success 路径），reason 为空
			ratelimit.NotifySignal(
				signalEndpointUID, metricsKey, apiType, upstream.Name, isStream,
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
				streamingUserID = trackStreamingConversation(c, channelScheduler, kind, model, channelIndex, upstream.Name)
				StartStreamTimeoutObservation(c, channelLogStore, metricsKey, logRequestID, time.Now())
			}
			usage, err = handleSuccess(c, resp, upstreamCopy, apiKey, attemptBody)
			if isStream {
				FinishStreamTimeoutObservation(c)
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
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, kind)
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
						channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, kind)
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
					cfgManager.MarkKeyAsFailed(apiKey, apiType)
					metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, metricsServiceType, requestID, metrics.FailureClassRetryable)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, kind)
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
						if blacklistErr := cfgManager.BlacklistKey(apiType, channelIndex, apiKey, blErr.Reason, blErr.Message); blacklistErr != nil {
							RequestLogf(c, "[%s-Blacklist] 拉黑 Key 失败: %v", apiType, blacklistErr)
						}
					}
					cfgManager.MarkKeyAsFailed(apiKey, apiType)
					metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, metricsServiceType, requestID, metrics.FailureClassRetryable)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, kind)
					if markURLFailure != nil {
						markURLFailure(currentBaseURL)
					}
					CompleteLog(channelLogStore, metricsKey, logRequestID, http.StatusOK, false, fmt.Sprintf("key blacklisted: %s - %s", blErr.Reason, blErr.Message), isRetryAttempt)
					RequestLogf(c, "[%s-Blacklist] SSE 流内错误触发拉黑 (Key: %s, 原因: %s)，尝试下一个密钥", apiType, utils.MaskAPIKey(apiKey), blErr.Reason)
					continue
				} else {
					// 真实渠道故障：计入失败指标
					cfgManager.MarkKeyAsFailed(apiKey, apiType)
					metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, metricsServiceType, requestID, metrics.FailureClassRetryable)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, kind)
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
			channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, kind)

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

			// Phase 3B-2: 回显自动模型映射信息（受 EchoMappedModel 配置门控）。
			if appliedMappedModel != "" {
				routingCfg := cfgManager.GetAutopilotRouting()
				if routingCfg.ModelMapping.EchoMappedModel {
					c.Header("X-CCX-Mapped-Model", actualAttemptModel)
					c.Header("X-CCX-Original-Model", model)
					c.Header("X-CCX-Mapping-Source", "auto_resolve")
				}
			}

			// Phase 4 Item 8: 代理成功后回调（A/B 测试用）。
			// 在主响应已写回客户端之后触发，不影响主请求路径。
			if postSuccessfulProxyHook != nil {
				latencyMs := time.Since(attemptStartedAt).Milliseconds()
				// 复制 bodyBytes 防止异步使用时被原始切片回收
				bodyCopy := make([]byte, len(requestBody))
				copy(bodyCopy, requestBody)
				postSuccessfulProxyHook(string(kind), model, upstream.ChannelUID, http.StatusOK, latencyMs, bodyCopy)
			}

			// Phase 4 Item 4: 用量画像记录（渠道推荐用）。
			// 与上面的 A/B 测试回调同一时机（主响应已返回），纯观测性累积，不影响主请求路径。
			if usagePatternRecorderHook != nil {
				if proxyKeyMask := middleware.GetProxyKeyMask(c); proxyKeyMask != "" {
					usagePatternRecorderHook(proxyKeyMask, string(kind), upstream.ChannelUID, model)
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
) (keypool.Selection, string, error) {
	if policy == nil {
		return selectAttemptAPIKey(channelScheduler, kind, channelIndex, upstream, failedKeys, failedQuotaGroups, model, fallback)
	}
	if baseURL == "" {
		baseURL = upstream.BaseURL
	}

	if !keypool.HasEffectiveConfig(upstream) {
		// 无 keypool 配置时：对 raw APIKeys 应用 policy filter/sort
		effectiveFailedKeys := failedKeysWithPersistentRestrictions(upstream, failedKeys, model)
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
		for _, key := range sorted {
			if key != "" && !effectiveFailedKeys[key] {
				return keypool.Selection{APIKey: key}, key, nil
			}
		}
		return keypool.Selection{}, "", fmt.Errorf("上游 %s 没有可用的 endpoint binding", upstream.Name)
	}

	// keypool 路径：获取候选 → FilterKeys → SortKeys → 选择
	candidates := keypool.CandidatesForModel(upstream, failedKeys, model)
	if len(candidates) == 0 {
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

	// 构建 candidate 查找表
	candidateMap := make(map[string]keypool.Candidate, len(candidates))
	for _, cand := range candidates {
		candidateMap[cand.APIKey] = cand
	}

	// 按 policy 排序后的顺序选择第一个可用 key
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

func selectAttemptAPIKey(channelScheduler *scheduler.ChannelScheduler, kind scheduler.ChannelKind, channelIndex int, upstream *config.UpstreamConfig, failedKeys map[string]bool, failedQuotaGroups map[string]bool, model string, fallback NextAPIKeyFunc) (keypool.Selection, string, error) {
	if !keypool.HasEffectiveConfig(upstream) {
		if fallback == nil {
			return keypool.Selection{}, "", fmt.Errorf("上游 %s 没有可用的API密钥", upstream.Name)
		}
		apiKey, err := fallback(upstream, failedKeysWithPersistentRestrictions(upstream, failedKeys, model))
		if err != nil {
			return keypool.Selection{}, "", err
		}
		return keypool.Selection{APIKey: apiKey}, apiKey, nil
	}

	var deferred []keypool.Selection
	for _, candidate := range keypool.CandidatesForModel(upstream, failedKeys, model) {
		if candidate.QuotaGroup != "" && failedQuotaGroups[candidate.QuotaGroup] {
			continue
		}
		selection := keypool.Selection{
			APIKey:         candidate.APIKey,
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
	switch kind {
	case scheduler.ChannelKindImages, scheduler.ChannelKindVectors:
		return ""
	case scheduler.ChannelKindGemini:
		return config.ReasoningParamStyleGemini
	}
	if upstream != nil && strings.EqualFold(strings.TrimSpace(upstream.ServiceType), "gemini") {
		return config.ReasoningParamStyleGemini
	}
	if upstream != nil && upstream.ReasoningParamStyle != "" {
		return upstream.ReasoningParamStyle
	}
	// ReasoningParamStyle 为空时（如自动托管渠道被 RuntimeUpstreamForAutoManagedProvider 清空），
	// 按渠道类型推导原生形态，不能一律回退到 responses 的 reasoning 对象形式，
	// 否则 Claude/Chat 上游收到不识别的字段会静默丢弃 effort。
	switch kind {
	case scheduler.ChannelKindMessages:
		return "thinking"
	case scheduler.ChannelKindChat:
		return "reasoning_effort"
	}
	return "reasoning"
}
