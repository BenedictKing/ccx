// Package responses 提供 Responses API 的处理器
package responses

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// Handler Responses API 代理处理器
// 支持多渠道调度：当配置多个渠道时自动启用
func Handler(
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	sessionManager *session.SessionManager,
	channelScheduler *scheduler.ChannelScheduler,
) gin.HandlerFunc {
	return gin.HandlerFunc(func(c *gin.Context) {
		// 先进行认证
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

		// 入口保留原始请求体；按渠道在发往上游前决定是否规范化 metadata.user_id
		c.Set("requestBodyBytes", bodyBytes)

		// 解析 Responses 请求
		var responsesReq types.ResponsesRequest
		if len(bodyBytes) > 0 {
			_ = json.Unmarshal(bodyBytes, &responsesReq)
			responsesReq.RawTools = extractRawToolsFromRequestBody(bodyBytes)
		}

		// 提取统一会话标识用于 Trace 亲和性（保持 metadata.user_id 默认规范化后的既有路由语义）
		affinityBody := common.NormalizeMetadataUserID(bodyBytes)
		userID := utils.ExtractUnifiedSessionID(c, affinityBody)
		agentCtx := utils.ExtractAgentContext(c, bodyBytes)
		c.Set("agentContext", agentCtx)

		// 统计 user 输入用于驾驶舱标题与轮数
		c.Set("lastUserMessage", extractLastResponsesUserInput(responsesReq.Input))
		c.Set("userMessageCount", countResponsesUserMessages(responsesReq.Input))
		common.SetRequestLogContextWithAgent(c, userID, countResponsesUserMessages(responsesReq.Input), agentCtx)

		// 记录原始请求信息（仅在入口处记录一次）
		common.LogOriginalRequest(c, bodyBytes, envCfg, "Responses")

		isCompactionV2 := hasCompactionTrigger(responsesReq.Input)

		// 检查是否为多渠道模式
		isMultiChannel := channelScheduler.IsMultiChannelMode(scheduler.ChannelKindResponses)

		if isMultiChannel {
			handleMultiChannel(c, envCfg, cfgManager, channelScheduler, sessionManager, bodyBytes, responsesReq, userID, startTime, isCompactionV2)
		} else {
			handleSingleChannel(c, envCfg, cfgManager, channelScheduler, sessionManager, bodyBytes, userID, responsesReq, startTime, isCompactionV2)
		}
	})
}

// handleMultiChannel 处理多渠道 Responses 请求
func extractRawToolsFromRequestBody(bodyBytes []byte) []interface{} {
	var reqMap map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &reqMap); err != nil {
		return nil
	}
	rawTools, _ := reqMap["tools"].([]interface{})
	return rawTools
}

func handleMultiChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	sessionManager *session.SessionManager,
	bodyBytes []byte,
	responsesReq types.ResponsesRequest,
	userID string,
	startTime time.Time,
	isCompactionV2 bool,
) {
	provider := &providers.ResponsesProvider{SessionManager: sessionManager}
	metricsManager := channelScheduler.GetResponsesMetricsManager()

	cfg := cfgManager.GetConfig()
	contextRequirement := common.BuildResponsesContextRequirement(bodyBytes, cfg.ContextRouting)
	common.ApplyAgentModelProfile(contextRequirement, responsesReq.Model, cfg)
	if isCompactionV2 && contextRequirement != nil {
		contextRequirement.SkipWindowValidation = true
	}
	common.LogContextEstimate(c, "Responses", contextRequirement)
	agentRole := ""
	if ac := common.AgentContextFromGin(c); ac != nil {
		agentRole = ac.AgentRole
	}
	common.HandleMultiChannelFailoverWithContextRequirement(
		c,
		envCfg,
		channelScheduler,
		scheduler.ChannelKindResponses,
		"Responses",
		userID,
		responsesReq.Model,
		contextRequirement,
		agentRole,
		func(selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
			upstream := selection.Upstream
			channelIndex := selection.ChannelIndex

			if upstream == nil {
				return common.MultiChannelAttemptResult{}
			}

			if isCompactionV2 && needsLocalCompact(upstream) {
				success, successKey, compactErr := tryLocalCompactV2WithAllKeys(c, upstream, channelIndex, responsesReq.Model, cfgManager, channelScheduler, channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses), bodyBytes, envCfg, sessionManager)
				result := common.MultiChannelAttemptResult{Attempted: true, SuccessKey: successKey}
				if success {
					result.Handled = true
					return result
				}
				if compactErr != nil {
					if compactErr.shouldFailover {
						result.FailoverError = &common.FailoverError{Status: compactErr.status, Body: compactErr.body}
						result.LastError = compactErr.err
						return result
					}
					c.Data(compactErr.status, "application/json", compactErr.body)
					result.Handled = true
					result.LastError = compactErr.err
					return result
				}
				return result
			}

			baseURLs := upstream.GetAllBaseURLs()
			sortedURLResults := channelScheduler.GetSortedURLsForChannel(scheduler.ChannelKindResponses, channelIndex, baseURLs)

			handled, successKey, successBaseURLIdx, failoverErr, usage, lastErr := common.TryUpstreamWithAllKeys(
				c,
				envCfg,
				cfgManager,
				channelScheduler,
				scheduler.ChannelKindResponses,
				"Responses",
				metricsManager,
				upstream,
				sortedURLResults,
				bodyBytes,
				contextRequirement,
				responsesReq.Stream,
				func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
					return cfgManager.GetNextResponsesAPIKey(upstream, failedKeys)
				},
				func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
					req, _, err := provider.ConvertToProviderRequest(c, upstreamCopy, apiKey)
					return req, err
				},
				func(apiKey string) {
					_ = cfgManager.DeprioritizeAPIKey(apiKey)
				},
				func(url string) {
					channelScheduler.MarkURLFailure(scheduler.ChannelKindResponses, channelIndex, url)
				},
				func(url string) {
					channelScheduler.MarkURLSuccess(scheduler.ChannelKindResponses, channelIndex, url)
				},
				func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
					// Inject codex_tool_compat_enabled for response remapping
					if responsesReq.TransformerMetadata == nil {
						responsesReq.TransformerMetadata = make(map[string]interface{})
					}
					responsesReq.TransformerMetadata["codex_tool_compat_enabled"] = upstreamCopy.IsCodexToolCompatEnabled() || upstreamCopy.CodexNativeToolPassthrough
					timeouts := common.ResolveStreamPreflightTimeouts(upstreamCopy, metricsManager.GetCircuitBreakerConfig())
					return handleSuccess(c, resp, provider, upstream.ServiceType, envCfg, sessionManager, startTime, &responsesReq, actualRequestBody, cfgManager.GetFuzzyModeEnabled(), timeouts)
				},
				responsesReq.Model,
				"",
				selection.ChannelIndex,
				channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses),
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
			common.HandleAllChannelsFailed(ctx, cfgManager.GetFuzzyModeEnabled(), failoverErr, lastError, "Responses")
		},
	)
}

// handleSingleChannel 处理单渠道 Responses 请求
func handleSingleChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	sessionManager *session.SessionManager,
	bodyBytes []byte,
	userID string,
	responsesReq types.ResponsesRequest,
	startTime time.Time,
	isCompactionV2 bool,
) {
	upstream, channelIndex, err := cfgManager.GetCurrentResponsesUpstreamWithIndex()
	if err != nil {
		c.JSON(503, gin.H{
			"error": "未配置任何 Responses 渠道，请先在管理界面添加渠道",
			"code":  "NO_RESPONSES_UPSTREAM",
		})
		return
	}

	if len(upstream.APIKeys) == 0 {
		c.JSON(503, gin.H{
			"error": fmt.Sprintf("当前 Responses 渠道 \"%s\" 未配置API密钥", upstream.Name),
			"code":  "NO_API_KEYS",
		})
		return
	}

	cfg := cfgManager.GetConfig()
	contextRequirement := common.BuildResponsesContextRequirement(bodyBytes, cfg.ContextRouting)
	common.ApplyAgentModelProfile(contextRequirement, responsesReq.Model, cfg)
	if isCompactionV2 && contextRequirement != nil {
		contextRequirement.SkipWindowValidation = true
	}
	common.LogContextEstimate(c, "Responses", contextRequirement)
	if err := channelScheduler.ValidateUpstreamContext(scheduler.ChannelKindResponses, responsesReq.Model, upstream, contextRequirement); err != nil {
		c.JSON(400, gin.H{
			"error": err.Error(),
			"code":  "CONTEXT_WINDOW_EXCEEDED",
		})
		return
	}

	provider := &providers.ResponsesProvider{SessionManager: sessionManager}

	metricsManager := channelScheduler.GetResponsesMetricsManager()
	baseURLs := upstream.GetAllBaseURLs()

	urlResults := common.BuildDefaultURLResults(baseURLs)

	if isCompactionV2 && needsLocalCompact(upstream) {
		handled, _, compactErr := tryLocalCompactV2WithAllKeys(c, upstream, channelIndex, responsesReq.Model, cfgManager, channelScheduler, channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses), bodyBytes, envCfg, sessionManager)
		if handled {
			return
		}
		if compactErr != nil {
			if cfgManager.GetFuzzyModeEnabled() {
				c.JSON(503, gin.H{
					"type": "error",
					"error": gin.H{
						"type":    "service_unavailable",
						"message": "All upstream channels are currently unavailable",
					},
				})
				return
			}
			c.Data(compactErr.status, "application/json", compactErr.body)
			return
		}
	}

	handled, successKey, _, lastFailoverError, _, lastError := common.TryUpstreamWithAllKeys(
		c,
		envCfg,
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindResponses,
		"Responses",
		metricsManager,
		upstream,
		urlResults,
		bodyBytes,
		contextRequirement,
		responsesReq.Stream,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextResponsesAPIKey(upstream, failedKeys)
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			req, _, err := provider.ConvertToProviderRequest(c, upstreamCopy, apiKey)
			return req, err
		},
		func(apiKey string) {
			if err := cfgManager.DeprioritizeAPIKey(apiKey); err != nil {
				common.RequestLogf(c, "[Responses-Key] 警告: 密钥降级失败: %v", err)
			}
		},
		nil,
		nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			// Inject codex_tool_compat_enabled for response remapping
			if responsesReq.TransformerMetadata == nil {
				responsesReq.TransformerMetadata = make(map[string]interface{})
			}
			responsesReq.TransformerMetadata["codex_tool_compat_enabled"] = upstreamCopy.IsCodexToolCompatEnabled() || upstreamCopy.CodexNativeToolPassthrough
			timeouts := common.ResolveStreamPreflightTimeouts(upstreamCopy, metricsManager.GetCircuitBreakerConfig())
			return handleSuccess(c, resp, provider, upstream.ServiceType, envCfg, sessionManager, startTime, &responsesReq, actualRequestBody, cfgManager.GetFuzzyModeEnabled(), timeouts)
		},
		responsesReq.Model,
		"",
		channelIndex,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses),
	)

	// 追踪对话（驾驶舱显示）
	if handled && successKey != "" {
		lastUserMsg, _ := c.Get("lastUserMessage")
		lastUserMsgStr, _ := lastUserMsg.(string)
		userMsgCount, _ := c.Get("userMessageCount")
		userMsgCountInt, _ := userMsgCount.(int)

		if lastUserMsgStr != "" || userMsgCountInt > 0 {
			channelName := ""
			if upstream != nil {
				channelName = upstream.Name
			}
			agentRole := ""
			agentCtx := common.AgentContextFromGin(c)
			if agentCtx != nil {
				agentRole = agentCtx.AgentRole
			}
			channelScheduler.TrackConversation(
				scheduler.ChannelKindResponses,
				userID,
				responsesReq.Model,
				channelIndex,
				channelName,
				"",
				lastUserMsgStr,
				userMsgCountInt,
				agentRole,
				agentCtx,
			)
		}
	}

	if handled {
		return
	}

	common.RequestLogf(c, "[Responses-Error] 所有 Responses API密钥都失败了")
	common.HandleAllKeysFailed(c, cfgManager.GetFuzzyModeEnabled(), lastFailoverError, lastError, "Responses")
}
