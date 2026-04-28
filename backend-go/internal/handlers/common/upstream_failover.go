// Package common 提供 handlers 模块的公共功能
package common

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
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

const selectedAPIKeyContextKey = "selectedAPIKey"

func SelectedAPIKeyFromContext(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.GetString(selectedAPIKeyContextKey))
}

// IsClaudeSub2APIPassthroughForKey 判断是否启用 sub2api 风格透传（仅替换认证）。
// 仅在 Claude 渠道、开关开启且当前 key 为 Anthropic 官方 key 时生效。
func IsClaudeSub2APIPassthroughForKey(upstream *config.UpstreamConfig, apiKey string) bool {
	return upstream != nil &&
		strings.EqualFold(upstream.ServiceType, "claude") &&
		upstream.IsSub2APIPassthroughEnabled() &&
		utils.IsAnthropicAPIKey(apiKey)
}

// ShouldDirectClaudePassthroughForKey 判断当前请求是否应直接透传响应（SSE 与非 SSE 均适用）。
func ShouldDirectClaudePassthroughForKey(upstream *config.UpstreamConfig, apiKey string) bool {
	return upstream != nil &&
		strings.EqualFold(upstream.ServiceType, "claude") &&
		(upstream.IsStreamPassthroughEnabled() || IsClaudeSub2APIPassthroughForKey(upstream, apiKey))
}

func shouldNormalizeMetadataUserID(kind scheduler.ChannelKind, upstream *config.UpstreamConfig, apiKey string) bool {
	if upstream == nil {
		return false
	}
	if shouldSkipRequestBodyPreprocessing(kind, upstream, apiKey) {
		return false
	}
	if kind != scheduler.ChannelKindMessages && kind != scheduler.ChannelKindResponses {
		return false
	}
	return upstream.IsNormalizeMetadataUserIDEnabled()
}

func isStrictClaudePassthrough(upstream *config.UpstreamConfig) bool {
	return upstream != nil &&
		strings.EqualFold(upstream.ServiceType, "claude") &&
		upstream.IsStreamPassthroughEnabled() &&
		upstream.IsStrictRequestPassthroughEnabled()
}

func shouldSkipRequestBodyPreprocessing(kind scheduler.ChannelKind, upstream *config.UpstreamConfig, apiKey string) bool {
	if kind != scheduler.ChannelKindMessages {
		return false
	}
	return isStrictClaudePassthrough(upstream) || IsClaudeSub2APIPassthroughForKey(upstream, apiKey)
}

func prepareRequestBodyForUpstream(
	requestBody []byte,
	kind scheduler.ChannelKind,
	apiType string,
	upstream *config.UpstreamConfig,
	cfgManager *config.ConfigManager,
	envCfg *config.EnvConfig,
	apiKey string,
) []byte {
	attemptBody := requestBody
	enableRequestLogs := envCfg != nil && envCfg.EnableRequestLogs

	if shouldSkipRequestBodyPreprocessing(kind, upstream, apiKey) {
		return attemptBody
	}

	if kind == scheduler.ChannelKindMessages {
		var modified bool
		attemptBody, modified = RemoveEmptySignatures(attemptBody, enableRequestLogs, apiType)
		_ = modified
		attemptBody, modified = SanitizeMalformedThinkingBlocks(attemptBody, enableRequestLogs, apiType)
		_ = modified
		if cfgManager != nil && cfgManager.GetStripBillingHeader() {
			attemptBody, _ = RemoveBillingHeaders(attemptBody, enableRequestLogs, apiType)
		}
	}

	if shouldNormalizeMetadataUserID(kind, upstream, apiKey) {
		attemptBody = NormalizeMetadataUserID(attemptBody)
	}

	return attemptBody
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
	isStream bool,
	nextAPIKey NextAPIKeyFunc,
	buildRequest BuildRequestFunc,
	deprioritizeKey DeprioritizeKeyFunc,
	markURLFailure func(url string),
	markURLSuccess func(url string),
	handleSuccess HandleSuccessFunc,
	model string,
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

	var lastFailoverError *FailoverError
	deprioritizeCandidates := make(map[string]bool)
	probeAcquired := make(map[string]bool)
	defer func() {
		for key := range probeAcquired {
			parts := strings.SplitN(key, "|", 2)
			if len(parts) == 2 {
				metricsManager.ReleaseProbe(parts[0], parts[1])
			}
		}
	}()

	// 计算重定向后的模型（用于日志记录）
	redirectedModel := config.RedirectModel(model, upstream)
	var originalModel string
	if redirectedModel != model {
		originalModel = model // 仅当发生重定向时记录原始模型
	}

	for urlIdx, urlResult := range urlResults {
		currentBaseURL := urlResult.URL
		originalIdx := urlResult.OriginalIdx // 原始索引用于指标记录
		failedKeys := make(map[string]bool)  // 每个 BaseURL 重置失败 Key 列表
		maxRetries := len(upstream.APIKeys)

		for attempt := 0; attempt < maxRetries; attempt++ {
			apiKey, err := nextAPIKey(upstream, failedKeys)
			if err != nil {
				lastError = err
				break // 当前 BaseURL 没有可用 Key，尝试下一个 BaseURL
			}

			// 检查熔断状态
			circuitState := metricsManager.GetKeyCircuitState(currentBaseURL, apiKey)
			if circuitState == metrics.CircuitStateOpen {
				failedKeys[apiKey] = true
				log.Printf("[%s-Circuit] 跳过 open 状态中的 Key: %s", apiType, utils.MaskAPIKey(apiKey))
				continue
			}
			if circuitState == metrics.CircuitStateHalfOpen {
				probeKey := currentBaseURL + "|" + apiKey
				if !metricsManager.TryAcquireProbe(currentBaseURL, apiKey) {
					failedKeys[apiKey] = true
					log.Printf("[%s-Circuit] 跳过 half-open 探针已占用的 Key: %s", apiType, utils.MaskAPIKey(apiKey))
					continue
				}
				probeAcquired[probeKey] = true
				log.Printf("[%s-Circuit] 使用 half-open 探针 Key: %s", apiType, utils.MaskAPIKey(apiKey))
			}

			if envCfg.ShouldLog("info") {
				log.Printf("[%s-Key] 使用API密钥: %s (BaseURL %d/%d, 尝试 %d/%d)",
					apiType, utils.MaskAPIKey(apiKey), urlIdx+1, len(urlResults), attempt+1, maxRetries)
			}

			// 使用深拷贝避免并发修改问题
			attemptBody := prepareRequestBodyForUpstream(requestBody, kind, apiType, upstream, cfgManager, envCfg, apiKey)
			RestoreRequestBody(c, attemptBody)
			c.Set("requestBodyBytes", attemptBody)
			c.Set(selectedAPIKeyContextKey, apiKey)
			upstreamCopy := upstream.Clone()
			upstreamCopy.BaseURL = currentBaseURL

			req, err := buildRequest(c, upstreamCopy, apiKey)
			if err != nil {
				// buildRequest 失败通常是客户端参数问题或本地构建错误
				// 不应污染熔断统计，直接返回错误
				log.Printf("[%s-BuildRequest] 请求构建失败: %v", apiType, err)
				return false, "", 0, nil, nil, fmt.Errorf("request build failed: %w", err)
			}

			// 记录请求开始
			channelScheduler.RecordRequestStart(currentBaseURL, apiKey, kind)

			// TCP 建连开始即计数：将活跃度统计提前到发起上游请求之前
			requestID := metricsManager.RecordRequestConnected(currentBaseURL, apiKey, redirectedModel)

			attemptStart := time.Now()
			resp, err := SendRequest(req, upstream, envCfg, isStream, apiType)
			if err != nil {
				lastError = err
				// 区分客户端取消和真实渠道故障（统一口径）
				if isClientSideError(err) {
					// 客户端取消：不计入失败，不触发 failover
					metricsManager.RecordRequestFinalizeClientCancel(currentBaseURL, apiKey, requestID)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, kind)
					log.Printf("[%s-Cancel] 请求已取消（SendRequest 阶段）", apiType)
					return true, "", 0, nil, nil, err
				}
				// 真实渠道故障：计入失败，继续 failover
				failedKeys[apiKey] = true
				cfgManager.MarkKeyAsFailed(apiKey, apiType)
				metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, requestID, metrics.FailureClassRetryable)
				channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, kind)
				if markURLFailure != nil {
					markURLFailure(currentBaseURL)
				}
				// 记录渠道日志
				RecordChannelLog(channelLogStore, channelIndex, redirectedModel, originalModel, 0, time.Since(attemptStart).Milliseconds(), false, apiKey, currentBaseURL, err.Error(), apiType, attempt > 0 || urlIdx > 0)
				log.Printf("[%s-Key] 警告: API密钥失败: %v", apiType, err)
				continue
			}

			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				respBodyBytes, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				respBodyBytes = utils.DecompressGzipIfNeeded(resp, respBodyBytes)

				// 优先检查全局暂停规则（状态码 + 关键词匹配 → 自定义暂停时间）
				if pauseRule := cfgManager.MatchPauseRule(resp.StatusCode, respBodyBytes); pauseRule != nil {
					duration := time.Duration(pauseRule.DurationMinutes) * time.Minute
					failedKeys[apiKey] = true
					cfgManager.MarkKeyAsFailedWithDuration(apiKey, apiType, duration)
					metricsManager.RecordRequestFinalizeFailure(currentBaseURL, apiKey, requestID)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, kind)
					if markURLFailure != nil {
						markURLFailure(currentBaseURL)
					}
					lastError = fmt.Errorf("暂停规则命中: %d - %s", resp.StatusCode, pauseRule.Description)
					if channelLogStore != nil {
						errInfo := string(respBodyBytes)
						if len(errInfo) > 200 {
							errInfo = errInfo[:200]
						}
						channelLogStore.Record(channelIndex, &metrics.ChannelLog{
							Timestamp:     time.Now(),
							Model:         redirectedModel,
							OriginalModel: originalModel,
							StatusCode:    resp.StatusCode,
							DurationMs:    time.Since(attemptStart).Milliseconds(),
							Success:       false,
							KeyMask:       utils.MaskAPIKey(apiKey),
							BaseURL:       currentBaseURL,
							ErrorInfo:     fmt.Sprintf("pause_rule: %s (%d min)", pauseRule.Description, pauseRule.DurationMinutes),
							IsRetry:       attempt > 0 || urlIdx > 0,
							InterfaceType: apiType,
						})
					}
					log.Printf("[%s-PauseRule] 命中暂停规则 (Key: %s, 状态码: %d, 暂停: %d分钟, 规则: %s)", apiType, utils.MaskAPIKey(apiKey), resp.StatusCode, pauseRule.DurationMinutes, pauseRule.Description)
					continue
				}

				ruleDecision := matchChannelFailoverRule(upstream, resp.StatusCode, respBodyBytes, "", "", "")
				var shouldFailover bool
				var isQuotaRelated bool
				blResult := BlacklistResult{}
				forceBlacklistByRule := false
				skipCooldownAndBreaker := false
				skipDefaultCooldownForRuleDrivenClaude := false

				if ruleDecision.Matched {
					shouldFailover = true
					isQuotaRelated = ruleDecision.IsQuotaRelated
					if ruleDecision.Action == failoverActionBlacklist {
						forceBlacklistByRule = true
						blResult = BlacklistResult{
							ShouldBlacklist: true,
							Reason:          ruleDecision.Reason,
							Message:         ruleDecision.Message,
						}
					}
					log.Printf("[%s-Failover-Rule] 命中渠道规则: channel=[%d] %s, action=%s, status=%d, key=%s, desc=%s",
						apiType, channelIndex, upstream.Name, ruleDecision.Action, resp.StatusCode, utils.MaskAPIKey(apiKey), ruleDecision.Description)
				} else if isModelRouteUnavailableError(respBodyBytes) {
					shouldFailover = true
					skipCooldownAndBreaker = true
					log.Printf("[%s-Failover-Model] route group has no matching model, skip current attempt: channel=[%d] %s, key=%s, status=%d",
						apiType, channelIndex, upstream.Name, utils.MaskAPIKey(apiKey), resp.StatusCode)
				} else {
					shouldFailover, isQuotaRelated = ShouldRetryWithNextKey(resp.StatusCode, respBodyBytes, cfgManager.GetFuzzyModeEnabled(), apiType)
					// 检查是否应永久拉黑该 Key（认证/权限/余额错误）
					blResult = ShouldBlacklistKey(resp.StatusCode, respBodyBytes)
					if strings.EqualFold(upstream.ServiceType, "claude") {
						skipDefaultCooldownForRuleDrivenClaude = true
					}
				}

				if blResult.ShouldBlacklist {
					shouldApplyBlacklist := true
					isBalanceError := blResult.Reason == "insufficient_balance"
					if !forceBlacklistByRule && isBalanceError && !upstream.IsAutoBlacklistBalanceEnabled() {
						shouldApplyBlacklist = false
					}
					if shouldApplyBlacklist {
						if err := cfgManager.BlacklistKey(apiType, channelIndex, apiKey, blResult.Reason, blResult.Message); err != nil {
							log.Printf("[%s-Blacklist] 拉黑 Key 失败: %v", apiType, err)
						}
					}
				}

				if shouldFailover {
					lastError = fmt.Errorf("上游错误: %d", resp.StatusCode)
					failedKeys[apiKey] = true
					// 已拉黑的 key 不需要再冷却
					if !blResult.ShouldBlacklist {
						if skipCooldownAndBreaker {
							log.Printf("[%s-Failover-Model] skip cooldown and breaker for model routing miss: channel=[%d] %s, key=%s",
								apiType, channelIndex, upstream.Name, utils.MaskAPIKey(apiKey))
						} else if ruleDecision.Matched && ruleDecision.Action == failoverActionCooldown {
							cfgManager.MarkKeyAsFailedWithDuration(apiKey, apiType, ruleDecision.Duration)
							log.Printf("[%s-Failover-Rule] 规则冷却生效: channel=[%d] %s, key=%s, duration=%s",
								apiType, channelIndex, upstream.Name, utils.MaskAPIKey(apiKey), ruleDecision.Duration)
						} else if skipDefaultCooldownForRuleDrivenClaude {
							log.Printf("[%s-Failover-Rule] Claude 未命中规则，仅本次跳过该 key，不写入默认冷却: channel=[%d] %s, key=%s, status=%d",
								apiType, channelIndex, upstream.Name, utils.MaskAPIKey(apiKey), resp.StatusCode)
						} else {
							cfgManager.MarkKeyAsFailed(apiKey, apiType)
						}
					}
					failureClass := metrics.FailureClassRetryable
					if skipCooldownAndBreaker {
						failureClass = metrics.FailureClassNonRetryable
					} else if isQuotaRelated || blResult.Reason == "insufficient_balance" {
						failureClass = metrics.FailureClassQuota
					}
					metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, requestID, failureClass)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, kind)
					if markURLFailure != nil {
						markURLFailure(currentBaseURL)
					}
					errorSummary := truncateMessage(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(string(respBodyBytes)), "\n", " "), "\r", " "))
					if errorSummary != "" {
						log.Printf("[%s-Key] 上游错误详情摘要: channel=[%d] %s, key=%s, summary=%s", apiType, channelIndex, upstream.Name, utils.MaskAPIKey(apiKey), errorSummary)
					}
					log.Printf("[%s-Key] 警告: API密钥失败 (状态: %d)，尝试下一个密钥", apiType, resp.StatusCode)

					lastFailoverError = &FailoverError{
						Status: resp.StatusCode,
						Body:   respBodyBytes,
					}

					// 记录渠道日志
					RecordChannelLog(channelLogStore, channelIndex, redirectedModel, originalModel, resp.StatusCode, time.Since(attemptStart).Milliseconds(), false, apiKey, currentBaseURL, string(respBodyBytes), apiType, attempt > 0 || urlIdx > 0)

					if isQuotaRelated {
						deprioritizeCandidates[apiKey] = true
					}
					continue
				}

				// 非 failover 错误，记录失败指标后返回（请求已处理）
				metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, requestID, metrics.FailureClassNonRetryable)
				channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, kind)
				// 记录渠道日志
				RecordChannelLog(channelLogStore, channelIndex, redirectedModel, originalModel, resp.StatusCode, time.Since(attemptStart).Milliseconds(), false, apiKey, currentBaseURL, string(respBodyBytes), apiType, attempt > 0 || urlIdx > 0)
				c.Data(resp.StatusCode, "application/json", respBodyBytes)
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

			usage, err = handleSuccess(c, resp, upstreamCopy, apiKey, attemptBody)
			if err != nil {
				lastError = err
				// 区分客户端错误和渠道故障
				if isClientSideError(err) {
					// 客户端取消/断开：计入总请求数但不计入失败
					metricsManager.RecordRequestFinalizeClientCancel(currentBaseURL, apiKey, requestID)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, kind)
					log.Printf("[%s-Cancel] 请求已取消，停止渠道 failover", apiType)
				} else if errors.Is(err, ErrEmptyStreamResponse) || errors.Is(err, ErrInvalidResponseBody) {
					// 空响应或无效响应体（如 HTML）：Header 未发送，可安全 failover
					failedKeys[apiKey] = true
					cfgManager.MarkKeyAsFailed(apiKey, apiType)
					metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, requestID, metrics.FailureClassRetryable)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, kind)
					if markURLFailure != nil {
						markURLFailure(currentBaseURL)
					}
					// 记录渠道日志
					RecordChannelLog(channelLogStore, channelIndex, redirectedModel, originalModel, http.StatusOK, time.Since(attemptStart).Milliseconds(), false, apiKey, currentBaseURL, err.Error(), apiType, attempt > 0 || urlIdx > 0)
					log.Printf("[%s-InvalidResponse] 上游返回无效响应 (Key: %s): %v，尝试下一个密钥", apiType, utils.MaskAPIKey(apiKey), err)
					continue
				} else if blErr, ok := err.(*ErrBlacklistKey); ok {
					// SSE 流内检测到拉黑条件：Header 未发送，可安全 failover + 拉黑 Key
					failedKeys[apiKey] = true
					isBalanceError := blErr.Reason == "insufficient_balance"
					isRateLimit := blErr.Reason == "rate_limit"
					// 速率限制只冷却不拉黑，其他错误按原逻辑拉黑
					if isRateLimit {
						cfgManager.MarkKeyAsFailed(apiKey, apiType)
					} else {
						if !isBalanceError || upstream.IsAutoBlacklistBalanceEnabled() {
							if blacklistErr := cfgManager.BlacklistKey(apiType, channelIndex, apiKey, blErr.Reason, blErr.Message); blacklistErr != nil {
								log.Printf("[%s-Blacklist] 拉黑 Key 失败: %v", apiType, blacklistErr)
							}
						}
					}
					failureClass := metrics.FailureClassRetryable
					if isBalanceError {
						failureClass = metrics.FailureClassQuota
					}
					metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, requestID, failureClass)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, kind)
					if markURLFailure != nil {
						markURLFailure(currentBaseURL)
					}
					logAction := "blacklisted"
					if isRateLimit {
						logAction = "rate_limited"
					}
					RecordChannelLog(channelLogStore, channelIndex, redirectedModel, originalModel, http.StatusOK, time.Since(attemptStart).Milliseconds(), false, apiKey, currentBaseURL, fmt.Sprintf("key %s: %s - %s", logAction, blErr.Reason, blErr.Message), apiType, attempt > 0 || urlIdx > 0)
					log.Printf("[%s-Blacklist] SSE 流内错误触发%s (Key: %s, 原因: %s)，尝试下一个密钥", apiType, logAction, utils.MaskAPIKey(apiKey), blErr.Reason)
					continue
				} else {
					// 真实渠道故障：计入失败指标
					cfgManager.MarkKeyAsFailed(apiKey, apiType)
					metricsManager.RecordRequestFinalizeFailureWithClass(currentBaseURL, apiKey, requestID, metrics.FailureClassRetryable)
					channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, kind)
					// 记录渠道日志
					RecordChannelLog(channelLogStore, channelIndex, redirectedModel, originalModel, http.StatusOK, time.Since(attemptStart).Milliseconds(), false, apiKey, currentBaseURL, err.Error(), apiType, attempt > 0 || urlIdx > 0)
					log.Printf("[%s-Key] 警告: 响应处理失败: %v", apiType, err)
				}
				return true, "", 0, nil, usage, err
			}

			metricsManager.RecordRequestFinalizeSuccess(currentBaseURL, apiKey, requestID, usage)
			channelScheduler.RecordRequestEnd(currentBaseURL, apiKey, kind)
			if probeKey := currentBaseURL + "|" + apiKey; probeAcquired[probeKey] {
				metricsManager.ReleaseProbe(currentBaseURL, apiKey)
				delete(probeAcquired, probeKey)
			}
			// 记录渠道日志
			RecordChannelLog(channelLogStore, channelIndex, redirectedModel, originalModel, http.StatusOK, time.Since(attemptStart).Milliseconds(), true, apiKey, currentBaseURL, "", apiType, attempt > 0 || urlIdx > 0)
			return true, apiKey, originalIdx, nil, usage, nil
		}

		// 当前 BaseURL 的所有 Key 都失败，记录并尝试下一个 BaseURL
		if envCfg.ShouldLog("info") && urlIdx < len(urlResults)-1 {
			log.Printf("[%s-BaseURL] BaseURL %d/%d 所有 Key 失败，切换到下一个 BaseURL", apiType, urlIdx+1, len(urlResults))
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
