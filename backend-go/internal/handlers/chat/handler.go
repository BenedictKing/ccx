// Package chat 提供 Chat Completions API 的代理处理器
package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// Handler Chat Completions API 代理处理器
// 支持多渠道调度：当配置多个渠道时自动启用
func Handler(
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
) gin.HandlerFunc {
	return gin.HandlerFunc(func(c *gin.Context) {
		// Chat 代理端点统一使用代理访问密钥鉴权（x-api-key / Authorization: Bearer）
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}

		startTime := time.Now()

		// 读取原始请求体
		maxBodySize := envCfg.MaxRequestBodySize
		bodyBytes, err := common.ReadRequestBody(c, maxBodySize)
		if err != nil {
			return
		}
		c.Set("requestBodyBytes", bodyBytes)

		// 解析请求中的关键字段
		var reqMap map[string]interface{}
		if len(bodyBytes) > 0 {
			if err := json.Unmarshal(bodyBytes, &reqMap); err != nil {
				c.JSON(400, gin.H{
					"error": gin.H{
						"message": fmt.Sprintf("Invalid request body: %v", err),
						"type":    "invalid_request_error",
						"code":    "invalid_json",
					},
				})
				return
			}
		}

		// 从请求体提取 model
		model, _ := reqMap["model"].(string)
		if model == "" {
			c.JSON(400, gin.H{
				"error": gin.H{
					"message": "model is required",
					"type":    "invalid_request_error",
					"code":    "missing_parameter",
				},
			})
			return
		}

		// 从请求体提取 stream（默认 false）
		isStream, _ := reqMap["stream"].(bool)

		// 提取统一会话标识用于 Trace 亲和性
		userID := utils.ExtractUnifiedSessionID(c, bodyBytes)
		agentCtx := utils.ExtractAgentContext(c, bodyBytes)
		c.Set("agentContext", agentCtx)
		common.SetRequestLogContextWithAgent(c, userID, countChatUserMessages(reqMap), agentCtx)

		// 预处理：清理空 signature 字段，预防上游参数校验 400
		bodyBytes, modified := common.RemoveEmptySignaturesWithContext(c, bodyBytes, envCfg.EnableRequestLogs, "Chat")
		_ = modified

		// 预处理：清理历史 thinking 内容块/字段，预防上游参数校验 400
		bodyBytes, thinkingModified := common.SanitizeMalformedThinkingBlocksWithContext(c, bodyBytes, envCfg.EnableRequestLogs, "Chat")
		_ = thinkingModified

		// 记录原始请求信息
		common.LogOriginalRequest(c, bodyBytes, envCfg, "Chat")

		// 检查是否为多渠道模式
		isMultiChannel := channelScheduler.IsMultiChannelMode(scheduler.ChannelKindChat)

		if isMultiChannel {
			handleMultiChannel(c, envCfg, cfgManager, channelScheduler, bodyBytes, model, isStream, userID, startTime)
		} else {
			handleSingleChannel(c, envCfg, cfgManager, channelScheduler, bodyBytes, model, isStream, startTime)
		}
	})
}

// handleMultiChannel 处理多渠道 Chat 请求
func handleMultiChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	model string,
	isStream bool,
	userID string,
	startTime time.Time,
) {
	metricsManager := channelScheduler.GetChatMetricsManager()
	cfg := cfgManager.GetConfig()
	contextRequirement := common.BuildChatContextRequirement(bodyBytes, cfg.ContextRouting)
	common.ApplyAgentModelProfile(contextRequirement, model, cfg)
	common.LogContextEstimate(c, "Chat", contextRequirement)
	agentRole := ""
	if ac := common.AgentContextFromGin(c); ac != nil {
		agentRole = ac.AgentRole
	}
	common.HandleMultiChannelFailoverWithContextRequirement(
		c,
		envCfg,
		channelScheduler,
		scheduler.ChannelKindChat,
		"Chat",
		userID,
		model,
		contextRequirement,
		agentRole,
		func(selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
			upstream := selection.Upstream
			channelIndex := selection.ChannelIndex

			if upstream == nil {
				return common.MultiChannelAttemptResult{}
			}

			baseURLs := upstream.GetAllBaseURLs()
			sortedURLResults := channelScheduler.GetSortedURLsForChannel(scheduler.ChannelKindChat, channelIndex, baseURLs)

			handled, successKey, successBaseURLIdx, failoverErr, usage, lastErr := common.TryUpstreamWithAllKeys(
				c,
				envCfg,
				cfgManager,
				channelScheduler,
				scheduler.ChannelKindChat,
				"Chat",
				metricsManager,
				upstream,
				sortedURLResults,
				bodyBytes,
				contextRequirement,
				isStream,
				func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
					return cfgManager.GetNextChatAPIKey(upstream, failedKeys)
				},
				func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
					// 使用 context 中的最新请求体（已经过 failover 内的 metadata 规范化、
					// 历史图片轮次限制替换等处理），而非闭包捕获的原始 bodyBytes。
					return buildProviderRequest(c, upstreamCopy, upstreamCopy.BaseURL, apiKey, common.GetEffectiveRequestBody(c, bodyBytes), model, isStream)
				},
				func(apiKey string) {
					_ = cfgManager.DeprioritizeAPIKey(apiKey)
				},
				func(url string) {
					channelScheduler.MarkURLFailure(scheduler.ChannelKindChat, channelIndex, url)
				},
				func(url string) {
					channelScheduler.MarkURLSuccess(scheduler.ChannelKindChat, channelIndex, url)
				},
				func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
					timeouts := common.ResolveStreamPreflightTimeouts(upstreamCopy, metricsManager.GetCircuitBreakerConfig())
					return handleSuccess(c, resp, upstreamCopy.ServiceType, envCfg, startTime, model, isStream, cfgManager.GetFuzzyModeEnabled(), timeouts)
				},
				model,
				"",
				selection.ChannelIndex,
				channelScheduler.GetChannelLogStore(scheduler.ChannelKindChat),
			)

			return common.MultiChannelAttemptResult{
				Handled:           handled,
				Attempted:         true,
				SuccessKey:        successKey,
				SuccessBaseURLIdx: successBaseURLIdx,
				FailoverError:     failoverErr,
				Usage:             usage,
				LastError:         lastErr,
			}
		},
		nil,
		func(ctx *gin.Context, failoverErr *common.FailoverError, lastError error) {
			handleAllChannelsFailed(ctx, failoverErr, lastError)
		},
	)
}

// handleSingleChannel 处理单渠道 Chat 请求
func handleSingleChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	model string,
	isStream bool,
	startTime time.Time,
) {
	upstream, channelIndex, err := cfgManager.GetCurrentChatUpstreamWithIndex()
	if err != nil {
		chatErrorResponse(c, 503, "No Chat upstream configured", "service_unavailable")
		return
	}

	if len(upstream.APIKeys) == 0 {
		chatErrorResponse(c, 503, fmt.Sprintf("No API keys configured for upstream \"%s\"", upstream.Name), "service_unavailable")
		return
	}

	cfg := cfgManager.GetConfig()
	contextRequirement := common.BuildChatContextRequirement(bodyBytes, cfg.ContextRouting)
	common.ApplyAgentModelProfile(contextRequirement, model, cfg)
	common.LogContextEstimate(c, "Chat", contextRequirement)
	if err := channelScheduler.ValidateUpstreamContext(scheduler.ChannelKindChat, model, upstream, contextRequirement); err != nil {
		chatErrorResponse(c, 400, err.Error(), "context_window_exceeded")
		return
	}

	metricsManager := channelScheduler.GetChatMetricsManager()
	baseURLs := upstream.GetAllBaseURLs()
	urlResults := common.BuildDefaultURLResults(baseURLs)

	handled, _, _, lastFailoverError, _, lastError := common.TryUpstreamWithAllKeys(
		c,
		envCfg,
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindChat,
		"Chat",
		metricsManager,
		upstream,
		urlResults,
		bodyBytes,
		contextRequirement,
		isStream,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextChatAPIKey(upstream, failedKeys)
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return buildProviderRequest(c, upstreamCopy, upstreamCopy.BaseURL, apiKey, common.GetEffectiveRequestBody(c, bodyBytes), model, isStream)
		},
		func(apiKey string) {
			_ = cfgManager.DeprioritizeAPIKey(apiKey)
		},
		nil,
		nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			timeouts := common.ResolveStreamPreflightTimeouts(upstreamCopy, metricsManager.GetCircuitBreakerConfig())
			return handleSuccess(c, resp, upstreamCopy.ServiceType, envCfg, startTime, model, isStream, cfgManager.GetFuzzyModeEnabled(), timeouts)
		},
		model,
		"",
		channelIndex,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindChat),
	)
	if handled {
		return
	}

	common.RequestLogf(c, "[Chat-Error] 所有 API密钥都失败了")
	handleAllKeysFailed(c, lastFailoverError, lastError)
}
