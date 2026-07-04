// Package common 提供 handlers 模块的公共功能
package common

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/keypool"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/sjson"
)

const (
	upstreamAccountPoolCooldown = time.Minute
	upstreamOverloadedCooldown  = 15 * time.Second
	halfOpenProbeWaitTimeout    = 5 * time.Second
	halfOpenProbePollInterval   = 100 * time.Millisecond
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

	// 计算重定向后的模型（用于日志记录）
	redirectedModel := config.RedirectModel(model, upstream)
	capabilityRequestModel := model
	var originalModel string
	if redirectedModel != model {
		originalModel = model // 仅当发生重定向时记录原始模型
	}

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
				originalModel = model
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

	for urlIdx, urlResult := range urlResults {
		currentBaseURL := urlResult.URL
		originalIdx := urlResult.OriginalIdx // 原始索引用于指标记录
		failedKeys := make(map[string]bool)  // 每个 BaseURL 重置失败 Key 列表
		maxRetries := len(upstream.APIKeys)

		for attempt := 0; attempt < maxRetries; attempt++ {
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

			selection, apiKey, err := selectAttemptAPIKey(channelScheduler, kind, channelIndex, upstream, failedKeys, failedQuotaGroups, redirectedModel, nextAPIKey)
			if err != nil {
				lastError = err
				break // 当前 BaseURL 没有可用 Key，尝试下一个 BaseURL
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
				RequestLogf(c, "[%s-Key] 使用API密钥: %s (BaseURL %d/%d, 尝试 %d/%d)",
					apiType, utils.MaskAPIKey(apiKey), urlIdx+1, len(urlResults), attempt+1, maxRetries)
			}

			// 使用深拷贝避免并发修改问题
			upstreamCopy := upstream.Clone()
			upstreamCopy.BaseURL = currentBaseURL

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
			actualReasoningEffort := extractActualReasoningEffortForLog(req)

			// 记录请求开始
			channelScheduler.RecordRequestStart(currentBaseURL, apiKey, metricsServiceType, kind)

			// 计算本次尝试的 metricsKey（与统计同源的身份指纹）
			metricsKey := metrics.GenerateMetricsIdentityKey(currentBaseURL, apiKey, metricsServiceType)

			// 创建 pending 状态日志（附带代理上下文与会话标识，用于 subagent 观测）
			logRequestID := CreatePendingLog(channelLogStore, metricsKey, channelIndex, upstream.Name, redirectedModel, originalModel, originalReasoningEffort, actualReasoningEffort, apiKey, currentBaseURL, apiType, operation, metrics.RequestSourceProxy, AgentContextFromGin(c), SessionIDFromGin(c))

			// TCP 建连开始即计数：将活跃度统计提前到发起上游请求之前
			requestID := metricsManager.RecordRequestConnected(currentBaseURL, apiKey, metricsServiceType, redirectedModel)

			lifecycleTrace := &RequestLifecycleTrace{
				OnConnected: func() {
					UpdateLogStatus(channelLogStore, metricsKey, logRequestID, metrics.StatusConnecting)
				},
				OnFirstResponseByte: func() {
					UpdateLogStatus(channelLogStore, metricsKey, logRequestID, metrics.StatusFirstByte)
				},
			}
			resp, err := SendRequestWithLifecycleTrace(req, upstream, envCfg, isStream, apiType, lifecycleTrace)
			if err != nil {
				lastError = err
				// 区分客户端取消和真实渠道故障（统一口径）
				if isClientSideError(err) {
					// 客户端取消：不计入失败，不触发 failover
					metricsManager.RecordRequestFinalizeClientCancel(currentBaseURL, apiKey, metricsServiceType, requestID)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, kind)
					// 完成日志记录（客户端取消）
					CompleteLog(channelLogStore, metricsKey, logRequestID, 0, false, "client canceled", attempt > 0 || urlIdx > 0)
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
				// 记录渠道日志
				// 完成日志记录
				CompleteLog(channelLogStore, metricsKey, logRequestID, 0, false, err.Error(), attempt > 0 || urlIdx > 0)
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

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				respBodyBytes, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				respBodyBytes = utils.DecompressGzipIfNeeded(resp, respBodyBytes)

				// 记录错误响应头（用于诊断限流 header）
				LogUpstreamResponseHeaders(c, resp, envCfg, apiType)

				shouldFailover, isQuotaRelated := ShouldRetryWithNextKeyWithLogTag(resp.StatusCode, respBodyBytes, cfgManager.GetFuzzyModeEnabled(), apiType, RequestLogTag(c))

				// 检查是否应永久拉黑该 Key（认证/权限/余额错误）
				blResult := ShouldBlacklistKey(resp.StatusCode, respBodyBytes)
				if blResult.ShouldBlacklist {
					isBalanceError := blResult.Reason == "insufficient_balance"
					if !isBalanceError || upstream.IsAutoBlacklistBalanceEnabled() {
						blacklistMessage := blResult.Message
						if strings.EqualFold(apiType, "Vectors") {
							blacklistMessage = errorBodySummaryForLog(apiType, resp.StatusCode, respBodyBytes)
						}
						if err := cfgManager.BlacklistKey(apiType, channelIndex, apiKey, blResult.Reason, blacklistMessage); err != nil {
							RequestLogf(c, "[%s-Blacklist] 拉黑 Key 失败: %v", apiType, err)
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
					metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, metricsServiceType, requestID, failureClass)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, kind)
					if markURLFailure != nil {
						markURLFailure(currentBaseURL)
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
					CompleteLog(channelLogStore, metricsKey, logRequestID, resp.StatusCode, false, channelErrorInfo, attempt > 0 || urlIdx > 0)

					if isQuotaRelated {
						deprioritizeCandidates[apiKey] = true
						if selection.QuotaGroup != "" {
							failedQuotaGroups[selection.QuotaGroup] = true
						}
					}
					if IsUpstreamAccountPoolUnavailable(respBodyBytes) {
						channelScheduler.MarkChannelCooldown(kind, channelIndex, upstreamAccountPoolCooldown)
						RequestLogf(c, "[%s-Channel] 渠道 [%d] %s 上游账号池不可用，冷却 %s 并尝试下一个渠道", apiType, channelIndex, upstream.Name, upstreamAccountPoolCooldown)
						return false, "", 0, lastFailoverError, nil, lastError
					}
					if IsUpstreamTemporarilyOverloaded(respBodyBytes) {
						channelScheduler.MarkChannelCooldown(kind, channelIndex, upstreamOverloadedCooldown)
						RequestLogf(c, "[%s-Channel] 渠道 [%d] %s 上游临时过载，冷却 %s 并尝试下一个渠道", apiType, channelIndex, upstream.Name, upstreamOverloadedCooldown)
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
							channelIndex, upstream.Name, resp.StatusCode, model, redirectedModel, errorSummary)
					}
				}
				metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, metricsServiceType, requestID, metrics.FailureClassNonRetryable)
				channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, kind)
				// 记录渠道日志
				CompleteLog(channelLogStore, metricsKey, logRequestID, clientStatusCode, false, channelErrorInfo, attempt > 0 || urlIdx > 0)
				c.Data(clientStatusCode, "application/json", respBodyBytes)
				return true, "", 0, nil, nil, nil
			}

			// 成功响应：处理 quota key 降级
			if deprioritizeKey != nil && len(deprioritizeCandidates) > 0 {
				for key := range deprioritizeCandidates {
					deprioritizeKey(key)
				}
			}

			if markURLSuccess != nil {
				markURLSuccess(currentBaseURL)
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
					CompleteLog(channelLogStore, metricsKey, logRequestID, http.StatusOK, false, "client canceled", attempt > 0 || urlIdx > 0)
				} else if errors.Is(err, ErrEmptyStreamResponse) || errors.Is(err, ErrInvalidResponseBody) || errors.Is(err, ErrEmptyNonStreamResponse) || errors.Is(err, ErrStreamFirstContentTimeout) || errors.Is(err, ErrStreamStalled) {
					// 空响应（流式 / 非流式）或无效响应体（如 HTML）或流式首字超时/断流：Header 未发送，可安全 failover
					failedKeys[apiKey] = true
					cfgManager.MarkKeyAsFailed(apiKey, apiType)
					metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, metricsServiceType, requestID, metrics.FailureClassRetryable)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, kind)
					if markURLFailure != nil {
						markURLFailure(currentBaseURL)
					}
					// 记录渠道日志
					CompleteLog(channelLogStore, metricsKey, logRequestID, http.StatusOK, false, err.Error(), attempt > 0 || urlIdx > 0)
					RequestLogf(c, "[%s-InvalidResponse] 上游返回无效响应 (Key: %s): %v，尝试下一个密钥", apiType, utils.MaskAPIKey(apiKey), err)
					continue
				} else if blErr, ok := err.(*ErrBlacklistKey); ok {
					// SSE 流内检测到拉黑条件：Header 未发送，可安全 failover + 拉黑 Key
					failedKeys[apiKey] = true
					isBalanceError := blErr.Reason == "insufficient_balance"
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
					CompleteLog(channelLogStore, metricsKey, logRequestID, http.StatusOK, false, fmt.Sprintf("key blacklisted: %s - %s", blErr.Reason, blErr.Message), attempt > 0 || urlIdx > 0)
					RequestLogf(c, "[%s-Blacklist] SSE 流内错误触发拉黑 (Key: %s, 原因: %s)，尝试下一个密钥", apiType, utils.MaskAPIKey(apiKey), blErr.Reason)
					continue
				} else {
					// 真实渠道故障：计入失败指标
					cfgManager.MarkKeyAsFailed(apiKey, apiType)
					metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, metricsServiceType, requestID, metrics.FailureClassRetryable)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, kind)
					// 记录渠道日志
					CompleteLog(channelLogStore, metricsKey, logRequestID, http.StatusOK, false, err.Error(), attempt > 0 || urlIdx > 0)
					RequestLogf(c, "[%s-Key] 警告: 响应处理失败: %v", apiType, err)
				}
				return true, "", 0, nil, usage, err
			}

			metricsManager.RecordRequestFinalizeSuccess(currentBaseURL, apiKey, metricsServiceType, requestID, usage)
			channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, metricsServiceType, kind)
			if probeKey := currentBaseURL + "|" + apiKey; probeAcquired[probeKey] {
				metricsManager.ReleaseProbe(currentBaseURL, apiKey, metricsServiceType)
				delete(probeAcquired, probeKey)
			}
			// 记录渠道日志
			CompleteLog(channelLogStore, metricsKey, logRequestID, http.StatusOK, true, "", attempt > 0 || urlIdx > 0)
			return true, apiKey, originalIdx, nil, usage, nil
		}

		// 当前 BaseURL 的所有 Key 都失败，记录并尝试下一个 BaseURL
		if envCfg.ShouldLog("info") && urlIdx < len(urlResults)-1 {
			RequestLogf(c, "[%s-BaseURL] BaseURL %d/%d 所有 Key 失败，切换到下一个 BaseURL", apiType, urlIdx+1, len(urlResults))
		}
	}

	return false, "", 0, lastFailoverError, nil, lastError
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

func selectAttemptAPIKey(channelScheduler *scheduler.ChannelScheduler, kind scheduler.ChannelKind, channelIndex int, upstream *config.UpstreamConfig, failedKeys map[string]bool, failedQuotaGroups map[string]bool, model string, fallback NextAPIKeyFunc) (keypool.Selection, string, error) {
	if !keypool.HasEffectiveConfig(upstream) {
		if fallback == nil {
			return keypool.Selection{}, "", fmt.Errorf("上游 %s 没有可用的API密钥", upstream.Name)
		}
		apiKey, err := fallback(upstream, failedKeys)
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

func trackStreamingConversation(c *gin.Context, channelScheduler *scheduler.ChannelScheduler, kind scheduler.ChannelKind, model string, channelIndex int, channelName string) string {
	if c == nil || channelScheduler == nil {
		return ""
	}

	lastUserMsg, _ := c.Get("lastUserMessage")
	lastUserMsgStr, _ := lastUserMsg.(string)
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

	channelScheduler.TrackConversationWithStatus(kind, userID, model, channelIndex, channelName, "", lastUserMsgStr, userMsgCountInt, agentRole, "streaming", AgentContextFromGin(c))
	return userID
}
