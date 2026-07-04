// Package messages 提供 Claude Messages API 的处理器
package messages

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/middleware"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

// Handler Messages API 代理处理器
// 支持多渠道调度：当配置多个渠道时自动启用
func Handler(envCfg *config.EnvConfig, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler) gin.HandlerFunc {
	return gin.HandlerFunc(func(c *gin.Context) {
		// 先进行认证
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}

		startTime := time.Now()

		// 读取请求体
		bodyBytes, err := common.ReadRequestBody(c, envCfg.MaxRequestBodySize)
		if err != nil {
			return
		}

		earlyAffinityBody := common.NormalizeMetadataUserID(bodyBytes)
		earlyUserID := utils.ExtractUnifiedSessionID(c, earlyAffinityBody)
		var earlyReq types.ClaudeRequest
		if len(bodyBytes) > 0 {
			_ = json.Unmarshal(bodyBytes, &earlyReq)
		}
		common.SetRequestLogContext(c, earlyUserID, countUserMessages(earlyReq.Messages))

		// 预处理：移除空 signature 字段，预防 400 错误
		// modified 表示请求体是否被修改，详细日志由 RemoveEmptySignatures 内部记录
		bodyBytes, modified := common.RemoveEmptySignaturesWithContext(c, bodyBytes, envCfg.EnableRequestLogs, "Messages")
		_ = modified // 保留以便未来扩展（如需在 handler 层面做额外处理）

		// 预处理：清理历史 thinking 内容块/字段，预防上游参数校验 400
		bodyBytes, thinkingModified := common.SanitizeMalformedThinkingBlocksWithContext(c, bodyBytes, envCfg.EnableRequestLogs, "Messages")
		_ = thinkingModified // 保留以便未来扩展（如需在 handler 层面做额外处理）

		// 入口保留原始请求体；按渠道在发往上游前决定是否做渠道级预处理（如规范化 metadata.user_id）
		c.Set("requestBodyBytes", bodyBytes)

		// 解析请求
		var claudeReq types.ClaudeRequest
		if len(bodyBytes) > 0 {
			_ = json.Unmarshal(bodyBytes, &claudeReq)
		}

		// 提取统一会话标识用于 Trace 亲和性（保持 metadata.user_id 默认规范化后的既有路由语义）
		affinityBody := common.NormalizeMetadataUserID(bodyBytes)
		userID := utils.ExtractUnifiedSessionID(c, affinityBody)
		agentCtx := utils.ExtractAgentContext(c, bodyBytes)
		common.SetRequestLogContextWithAgent(c, userID, countUserMessages(claudeReq.Messages), agentCtx)
		c.Set("agentContext", agentCtx)

		isTitleRequest := isClaudeCodeTitleRequest(bodyBytes)
		isRecapRequest := isClaudeCodeRecapRequest(bodyBytes)
		if envCfg.ShouldLog("debug") && isTitleRequest {
			common.RequestLogf(c, "[Messages-Title-Debug] 检测到 Claude Code title 请求: user=%s, model=%s, stream=%t",
				scheduler.MaskUserIDForLog(userID), claudeReq.Model, claudeReq.Stream)
		}
		if envCfg.ShouldLog("debug") && isRecapRequest {
			common.RequestLogf(c, "[Messages-Recap-Debug] 检测到 Claude Code recap 请求: user=%s, model=%s, stream=%t",
				scheduler.MaskUserIDForLog(userID), claudeReq.Model, claudeReq.Stream)
		}

		// 提取用户最后一条消息用于对话标题 fallback
		if !isTitleRequest && !isRecapRequest {
			c.Set("lastUserMessage", extractLastUserMessage(claudeReq.Messages))
			c.Set("userMessageCount", countUserMessages(claudeReq.Messages))
		}

		// 记录原始请求信息（仅在入口处记录一次）
		common.LogOriginalRequest(c, bodyBytes, envCfg, "Messages")

		if handleClaudeDesktopConnectionTest(c, claudeReq) {
			return
		}

		// 检查是否为多渠道模式
		isMultiChannel := channelScheduler.IsMultiChannelMode(scheduler.ChannelKindMessages)

		if isMultiChannel {
			handleMultiChannel(c, envCfg, cfgManager, channelScheduler, bodyBytes, claudeReq, userID, startTime)
		} else {
			handleSingleChannel(c, envCfg, cfgManager, channelScheduler, bodyBytes, claudeReq, startTime)
		}
	})
}

// handleMultiChannel 处理多渠道代理请求
func handleMultiChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	claudeReq types.ClaudeRequest,
	userID string,
	startTime time.Time,
) {
	isTitleRequest := isClaudeCodeTitleRequest(bodyBytes)
	isRecapRequest := isClaudeCodeRecapRequest(bodyBytes)

	cfg := cfgManager.GetConfig()
	contextRequirement := common.BuildMessagesContextRequirement(bodyBytes, cfg.ContextRouting)
	common.ApplyAgentModelProfile(contextRequirement, claudeReq.Model, cfg)
	common.LogContextEstimate(c, "Messages", contextRequirement)
	agentRole := ""
	if ac := common.AgentContextFromGin(c); ac != nil {
		agentRole = ac.AgentRole
	}
	common.HandleMultiChannelFailoverWithContextRequirement(
		c,
		envCfg,
		channelScheduler,
		scheduler.ChannelKindMessages,
		"Messages",
		userID,
		claudeReq.Model,
		contextRequirement,
		agentRole,
		func(selection *scheduler.SelectionResult) common.MultiChannelAttemptResult {
			upstream := selection.Upstream
			channelIndex := selection.ChannelIndex

			if upstream == nil {
				return common.MultiChannelAttemptResult{}
			}

			provider := providers.GetProvider(upstream.ServiceType)
			if provider == nil {
				return common.MultiChannelAttemptResult{}
			}

			metricsManager := channelScheduler.GetMessagesMetricsManager()
			baseURLs := upstream.GetAllBaseURLs()
			sortedURLResults := channelScheduler.GetSortedURLsForChannel(scheduler.ChannelKindMessages, channelIndex, baseURLs)

			handled, successKey, successBaseURLIdx, failoverErr, usage, lastErr := common.TryUpstreamWithAllKeys(
				c,
				envCfg,
				cfgManager,
				channelScheduler,
				scheduler.ChannelKindMessages,
				"Messages",
				metricsManager,
				upstream,
				sortedURLResults,
				bodyBytes,
				contextRequirement,
				claudeReq.Stream,
				func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
					return cfgManager.GetNextAPIKey(upstream, failedKeys, "Messages")
				},
				func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
					req, _, err := provider.ConvertToProviderRequest(c, upstreamCopy, apiKey)
					return req, err
				},
				func(apiKey string) {
					if err := cfgManager.DeprioritizeAPIKey(apiKey); err != nil {
						common.RequestLogf(c, "[Messages-Key] 警告: 密钥降级失败: %v", err)
					}
				},
				func(url string) {
					channelScheduler.MarkURLFailure(scheduler.ChannelKindMessages, channelIndex, url)
				},
				func(url string) {
					channelScheduler.MarkURLSuccess(scheduler.ChannelKindMessages, channelIndex, url)
				},
				func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
					if claudeReq.Stream {
						timeouts := common.ResolveStreamPreflightTimeouts(upstreamCopy, metricsManager.GetCircuitBreakerConfig())
						return common.HandleStreamResponse(c, resp, provider, envCfg, startTime, upstreamCopy, actualRequestBody, claudeReq.Model, timeouts)
					}
					return handleNormalResponse(c, resp, provider, envCfg, startTime, actualRequestBody, upstreamCopy, apiKey, cfgManager.GetFuzzyModeEnabled())
				},
				claudeReq.Model,
				"",
				selection.ChannelIndex,
				channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
			)

			responseText, _ := c.Get("responseText")
			return common.MultiChannelAttemptResult{
				Handled:           handled,
				Attempted:         true,
				SuccessKey:        successKey,
				SuccessBaseURLIdx: successBaseURLIdx,
				FailoverError:     failoverErr,
				Usage:             usage,
				LastError:         lastErr,
				ResponseText:      responseTextString(responseText),
			}
		},
		func(selection *scheduler.SelectionResult, result common.MultiChannelAttemptResult) {
			if result.ResponseText == "" {
				return
			}
			if isTitleRequest {
				title := extractTitleFromResponseText(result.ResponseText)
				updated := channelScheduler.UpdateConversationTitle(scheduler.ChannelKindMessages, userID, title)
				if envCfg.ShouldLog("debug") {
					common.RequestLogf(c, "[Messages-Title-Debug] title 更新结果: user=%s, title=%q, updated=%t, responseTextLen=%d",
						scheduler.MaskUserIDForLog(userID), title, updated, len(result.ResponseText))
				}
				return
			}
			if isRecapRequest {
				recap := extractRecapFromResponseText(result.ResponseText)
				updated := channelScheduler.UpdateConversationRecap(scheduler.ChannelKindMessages, userID, recap)
				if envCfg.ShouldLog("debug") {
					common.RequestLogf(c, "[Messages-Recap-Debug] recap 更新结果: user=%s, updated=%t, responseTextLen=%d",
						scheduler.MaskUserIDForLog(userID), updated, len(result.ResponseText))
				}
			}
		},
		func(ctx *gin.Context, failoverErr *common.FailoverError, lastError error) {
			common.HandleAllChannelsFailed(ctx, cfgManager.GetFuzzyModeEnabled(), failoverErr, lastError, "Messages")
		},
	)
}

// handleSingleChannel 处理单渠道代理请求
func handleSingleChannel(
	c *gin.Context,
	envCfg *config.EnvConfig,
	cfgManager *config.ConfigManager,
	channelScheduler *scheduler.ChannelScheduler,
	bodyBytes []byte,
	claudeReq types.ClaudeRequest,
	startTime time.Time,
) {
	upstream, channelIndex, err := cfgManager.GetCurrentUpstreamWithIndex()
	if err != nil {
		c.JSON(503, gin.H{
			"error": "未配置任何渠道，请先在管理界面添加渠道",
			"code":  "NO_UPSTREAM",
		})
		return
	}

	if len(upstream.APIKeys) == 0 {
		c.JSON(503, gin.H{
			"error": fmt.Sprintf("当前渠道 \"%s\" 未配置API密钥", upstream.Name),
			"code":  "NO_API_KEYS",
		})
		return
	}

	cfg := cfgManager.GetConfig()
	contextRequirement := common.BuildMessagesContextRequirement(bodyBytes, cfg.ContextRouting)
	common.ApplyAgentModelProfile(contextRequirement, claudeReq.Model, cfg)
	common.LogContextEstimate(c, "Messages", contextRequirement)
	if err := channelScheduler.ValidateUpstreamContext(scheduler.ChannelKindMessages, claudeReq.Model, upstream, contextRequirement); err != nil {
		c.JSON(400, gin.H{
			"error": err.Error(),
			"code":  "CONTEXT_WINDOW_EXCEEDED",
		})
		return
	}

	provider := providers.GetProvider(upstream.ServiceType)
	if provider == nil {
		c.JSON(400, gin.H{"error": "Unsupported service type"})
		return
	}

	metricsManager := channelScheduler.GetMessagesMetricsManager()
	baseURLs := upstream.GetAllBaseURLs()

	urlResults := common.BuildDefaultURLResults(baseURLs)

	handled, _, _, lastFailoverError, _, lastError := common.TryUpstreamWithAllKeys(
		c,
		envCfg,
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindMessages,
		"Messages",
		metricsManager,
		upstream,
		urlResults,
		bodyBytes,
		contextRequirement,
		claudeReq.Stream,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, "Messages")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			req, _, err := provider.ConvertToProviderRequest(c, upstreamCopy, apiKey)
			return req, err
		},
		func(apiKey string) {
			if err := cfgManager.DeprioritizeAPIKey(apiKey); err != nil {
				common.RequestLogf(c, "[Messages-Key] 警告: 密钥降级失败: %v", err)
			}
		},
		nil,
		nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			if claudeReq.Stream {
				timeouts := common.ResolveStreamPreflightTimeouts(upstreamCopy, metricsManager.GetCircuitBreakerConfig())
				return common.HandleStreamResponse(c, resp, provider, envCfg, startTime, upstreamCopy, actualRequestBody, claudeReq.Model, timeouts)
			}
			return handleNormalResponse(c, resp, provider, envCfg, startTime, actualRequestBody, upstreamCopy, apiKey, cfgManager.GetFuzzyModeEnabled())
		},
		claudeReq.Model,
		"",
		channelIndex,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
	)
	if handled {
		userID := utils.ExtractUnifiedSessionID(c, common.NormalizeMetadataUserID(bodyBytes))
		isTitleRequest := isClaudeCodeTitleRequest(bodyBytes)
		isRecapRequest := isClaudeCodeRecapRequest(bodyBytes)
		if !isTitleRequest && !isRecapRequest {
			lastUserMessage := extractLastUserMessage(claudeReq.Messages)
			userMessageCount := countUserMessages(claudeReq.Messages)
			agentRole := ""
			affinityUserID := userID
			agentCtx := common.AgentContextFromGin(c)
			if agentCtx != nil {
				agentRole = agentCtx.AgentRole
				if agentRole == "subagent" {
					affinityUserID = userID + ":subagent"
				}
			}
			channelScheduler.SetTraceAffinityForRequirement(affinityUserID, channelIndex, scheduler.ChannelKindMessages, contextRequirement)
			channelScheduler.TrackConversation(scheduler.ChannelKindMessages, userID, claudeReq.Model, channelIndex, upstream.Name, "", lastUserMessage, userMessageCount, agentRole, agentCtx)
			if envCfg.ShouldLog("debug") {
				common.RequestLogf(c, "[Messages-Conversation-Debug] 已追踪单渠道对话: user=%s, model=%s, channel=%d, userMessages=%d, hasFallbackTitle=%t",
					scheduler.MaskUserIDForLog(userID), claudeReq.Model, channelIndex, userMessageCount, lastUserMessage != "")
			}
		} else if isTitleRequest {
			responseText, _ := c.Get("responseText")
			title := extractTitleFromResponseText(responseTextString(responseText))
			updated := channelScheduler.UpdateConversationTitle(scheduler.ChannelKindMessages, userID, title)
			if envCfg.ShouldLog("debug") {
				common.RequestLogf(c, "[Messages-Title-Debug] 单渠道 title 更新结果: user=%s, title=%q, updated=%t, responseTextLen=%d",
					scheduler.MaskUserIDForLog(userID), title, updated, len(responseTextString(responseText)))
			}
		} else if isRecapRequest {
			responseText, _ := c.Get("responseText")
			recap := extractRecapFromResponseText(responseTextString(responseText))
			updated := channelScheduler.UpdateConversationRecap(scheduler.ChannelKindMessages, userID, recap)
			if envCfg.ShouldLog("debug") {
				common.RequestLogf(c, "[Messages-Recap-Debug] 单渠道 recap 更新结果: user=%s, updated=%t, responseTextLen=%d",
					scheduler.MaskUserIDForLog(userID), updated, len(responseTextString(responseText)))
			}
		}
		return
	}

	common.RequestLogf(c, "[Messages-Error] 所有API密钥都失败了")
	common.HandleAllKeysFailed(c, cfgManager.GetFuzzyModeEnabled(), lastFailoverError, lastError, "Messages")
}

// handleNormalResponse 处理非流式响应
func handleNormalResponse(
	c *gin.Context,
	resp *http.Response,
	provider providers.Provider,
	envCfg *config.EnvConfig,
	startTime time.Time,
	requestBody []byte,
	upstream *config.UpstreamConfig,
	apiKey string,
	fuzzyMode bool,
) (*types.Usage, error) {
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to read response"})
		return nil, err
	}

	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		common.RequestLogf(c, "[Messages-Timing] 响应完成: %dms, 状态: %d", responseTime, resp.StatusCode)
		common.LogUpstreamResponse(c, resp, bodyBytes, envCfg, "Messages")
	}
	logNormalProtocolDebug(c, resp, bodyBytes, envCfg)

	providerResp := &types.ProviderResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		Body:       bodyBytes,
		Stream:     false,
	}

	claudeResp, err := provider.ConvertToClaudeResponse(providerResp)
	if err != nil {
		// JSON 解析失败（如上游返回 HTML 错误页面）：不写 Header，返回可 failover 的错误
		common.RequestLogf(c, "[Messages-InvalidBody] 响应体解析失败: %v, body_len=%d, json_valid=%t, looks_sse=%t, body前100字节=%q, body后300字节=%q",
			err, len(bodyBytes), json.Valid(bodyBytes), looksLikeSSEPayload(bodyBytes), previewPrefix(bodyBytes, 100), previewSuffix(bodyBytes, 300))
		return nil, fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)
	}

	// 空响应拦截（仅 Fuzzy 模式）：上游 200 但 content 语义为空，
	// Header 未发送，可安全 failover 到下一个 Key/BaseURL/渠道
	if fuzzyMode && common.IsClaudeResponseEmpty(claudeResp) {
		common.RequestLogf(c, "[Messages-EmptyResponse] 上游返回空响应（非流式，Key: %s），触发 failover", utils.MaskAPIKey(apiKey))
		return nil, common.ErrEmptyNonStreamResponse
	}
	c.Set("responseText", extractClaudeResponseText(claudeResp.Content))

	// Token 补全逻辑
	if claudeResp.Usage == nil {
		estimatedInput := utils.EstimateRequestTokens(requestBody)
		estimatedOutput := utils.EstimateResponseTokens(claudeResp.Content)
		claudeResp.Usage = &types.Usage{
			InputTokens:  estimatedInput,
			OutputTokens: estimatedOutput,
		}
		if envCfg.EnableResponseLogs {
			common.RequestLogf(c, "[Messages-Token] 上游无Usage, 本地估算: input=%d, output=%d", estimatedInput, estimatedOutput)
		}
	} else {
		originalInput := claudeResp.Usage.InputTokens
		originalOutput := claudeResp.Usage.OutputTokens
		patched := false

		hasCacheTokens := claudeResp.Usage.CacheCreationInputTokens > 0 || claudeResp.Usage.CacheReadInputTokens > 0

		if claudeResp.Usage.InputTokens <= 1 && !hasCacheTokens {
			claudeResp.Usage.InputTokens = utils.EstimateRequestTokens(requestBody)
			patched = true
		}
		if claudeResp.Usage.OutputTokens <= 1 {
			claudeResp.Usage.OutputTokens = utils.EstimateResponseTokens(claudeResp.Content)
			patched = true
		}
		if envCfg.EnableResponseLogs {
			if patched {
				common.RequestLogf(c, "[Messages-Token] 虚假值补全: InputTokens=%d->%d, OutputTokens=%d->%d",
					originalInput, claudeResp.Usage.InputTokens, originalOutput, claudeResp.Usage.OutputTokens)
			}
			common.RequestLogf(c, "[Messages-Token] InputTokens=%d, OutputTokens=%d, CacheCreationInputTokens=%d, CacheReadInputTokens=%d, CacheCreation5m=%d, CacheCreation1h=%d, CacheTTL=%s",
				claudeResp.Usage.InputTokens, claudeResp.Usage.OutputTokens,
				claudeResp.Usage.CacheCreationInputTokens, claudeResp.Usage.CacheReadInputTokens,
				claudeResp.Usage.CacheCreation5mInputTokens, claudeResp.Usage.CacheCreation1hInputTokens,
				claudeResp.Usage.CacheTTL)
		}
	}

	// 监听客户端断开连接
	ctx := c.Request.Context()
	go func() {
		<-ctx.Done()
		if !c.Writer.Written() {
			if envCfg.EnableResponseLogs {
				responseTime := time.Since(startTime).Milliseconds()
				common.RequestLogf(c, "[Messages-Timing] 响应中断: %dms, 状态: %d", responseTime, resp.StatusCode)
			}
		}
	}()

	// 转发上游响应头
	utils.ForwardResponseHeaders(resp.Header, c.Writer)
	if normalProtocolDebugEnabled(envCfg) {
		common.RequestLogf(c, "[Messages-Protocol-Debug] 写回前响应头: client_content_type=%q", c.Writer.Header().Get("Content-Type"))
	}

	c.JSON(200, claudeResp)
	if normalProtocolDebugEnabled(envCfg) {
		common.RequestLogf(c, "[Messages-Protocol-Debug] 写回后响应头: client_content_type=%q, written=%t", c.Writer.Header().Get("Content-Type"), c.Writer.Written())
	}

	if envCfg.EnableResponseLogs {
		responseTime := time.Since(startTime).Milliseconds()
		common.RequestLogf(c, "[Messages-Timing] 响应发送完成: %dms, 状态: %d", responseTime, resp.StatusCode)
	}

	return claudeResp.Usage, nil
}

func logNormalProtocolDebug(c *gin.Context, resp *http.Response, bodyBytes []byte, envCfg *config.EnvConfig) {
	if !normalProtocolDebugEnabled(envCfg) || resp == nil {
		return
	}

	upstreamAccept := ""
	if resp.Request != nil {
		upstreamAccept = resp.Request.Header.Get("Accept")
	}

	common.RequestLogf(c, "[Messages-Protocol-Debug] stream=false, client_accept=%q, upstream_accept=%q, upstream_content_type=%q, status=%d, body_len=%d, json_valid=%t, looks_sse=%t, body前300字节=%q, body后300字节=%q",
		c.GetHeader("Accept"),
		upstreamAccept,
		resp.Header.Get("Content-Type"),
		resp.StatusCode,
		len(bodyBytes),
		json.Valid(bodyBytes),
		looksLikeSSEPayload(bodyBytes),
		previewPrefix(bodyBytes, 300),
		previewSuffix(bodyBytes, 300),
	)
}

func normalProtocolDebugEnabled(envCfg *config.EnvConfig) bool {
	return envCfg != nil && envCfg.EnableResponseLogs && envCfg.IsDevelopment() && envCfg.ShouldLog("debug")
}

func looksLikeSSEPayload(bodyBytes []byte) bool {
	trimmed := bytes.TrimSpace(bodyBytes)
	return bytes.HasPrefix(trimmed, []byte("data:")) ||
		bytes.HasPrefix(trimmed, []byte("event:")) ||
		bytes.Contains(trimmed, []byte("\ndata:")) ||
		bytes.Contains(trimmed, []byte("\nevent:")) ||
		bytes.Contains(trimmed, []byte("[DONE]"))
}

func previewPrefix(bodyBytes []byte, limit int) string {
	if limit <= 0 || len(bodyBytes) <= limit {
		return string(bodyBytes)
	}
	return string(bodyBytes[:limit])
}

func previewSuffix(bodyBytes []byte, limit int) string {
	if limit <= 0 || len(bodyBytes) <= limit {
		return string(bodyBytes)
	}
	return string(bodyBytes[len(bodyBytes)-limit:])
}

func isClaudeCodeTitleRequest(bodyBytes []byte) bool {
	var req struct {
		OutputConfig struct {
			Format struct {
				Schema struct {
					Required []string `json:"required"`
				} `json:"schema"`
			} `json:"format"`
		} `json:"output_config"`
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		return false
	}

	requiresTitle := false
	for _, field := range req.OutputConfig.Format.Schema.Required {
		if field == "title" {
			requiresTitle = true
			break
		}
	}
	if !requiresTitle {
		return false
	}

	for _, block := range req.System {
		if strings.Contains(block.Text, "Generate a concise") && strings.Contains(block.Text, "title") {
			return true
		}
	}
	return false
}

func extractTitleFromResponseText(responseText string) string {
	responseText = strings.TrimSpace(responseText)
	if responseText == "" {
		return ""
	}

	var payload struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(responseText), &payload); err == nil {
		return strings.TrimSpace(payload.Title)
	}

	return strings.Trim(strings.TrimSpace(responseText), `"`)
}

func extractRecapFromResponseText(responseText string) string {
	responseText = strings.TrimSpace(responseText)
	if responseText == "" {
		return ""
	}

	var payload struct {
		Recap   string `json:"recap"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(responseText), &payload); err == nil {
		if recap := strings.TrimSpace(payload.Recap); recap != "" {
			return recap
		}
		if summary := strings.TrimSpace(payload.Summary); summary != "" {
			return summary
		}
	}

	var text string
	if err := json.Unmarshal([]byte(responseText), &text); err == nil {
		return strings.TrimSpace(text)
	}
	return strings.Trim(strings.TrimSpace(responseText), `"`)
}

func extractClaudeResponseText(contents []types.ClaudeContent) string {
	parts := make([]string, 0, len(contents))
	for _, content := range contents {
		if content.Type != "text" {
			continue
		}
		if text := strings.TrimSpace(content.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func responseTextString(value interface{}) string {
	text, _ := value.(string)
	return text
}

const claudeDesktopConnectionTestText = "I'm ready to help. What would you like to work on?"

var claudeDesktopConnectionTestDeltas = []string{
	"I",
	"'m",
	" ready to help",
	".",
	" What",
	" would you like to work",
	" on?",
}

func handleClaudeDesktopConnectionTest(c *gin.Context, req types.ClaudeRequest) bool {
	if !isClaudeDesktopConnectionTestRequest(req) {
		return false
	}

	common.RequestLogf(c, "[Messages-ConnectionTest] 命中 Claude Desktop 连接测试内置响应: model=%s, stream=%t", req.Model, req.Stream)
	if req.Stream {
		writeClaudeDesktopConnectionTestStream(c, req.Model)
		return true
	}

	c.JSON(http.StatusOK, gin.H{
		"id":            "msg_ccx_connection_test",
		"type":          "message",
		"role":          "assistant",
		"model":         req.Model,
		"content":       []gin.H{{"type": "text", "text": claudeDesktopConnectionTestText}},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage": gin.H{
			"input_tokens":                15,
			"output_tokens":               14,
			"cache_creation_input_tokens": 0,
			"cache_read_input_tokens":     0,
		},
	})
	return true
}

func isClaudeDesktopConnectionTestRequest(req types.ClaudeRequest) bool {
	if strings.TrimSpace(req.Model) != "haiku" || req.MaxTokens != 1 || len(req.Messages) != 1 || len(req.Tools) > 0 || req.ToolChoice != nil {
		return false
	}

	msg := req.Messages[0]
	if msg.Role != "user" {
		return false
	}
	texts := extractRawUserTextBlocks(msg)
	return len(texts) == 1 && strings.TrimSpace(texts[0]) == "."
}

func writeClaudeDesktopConnectionTestStream(c *gin.Context, model string) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	writeClaudeSSEEvent(c, "message_start", gin.H{
		"type": "message_start",
		"message": gin.H{
			"id":      "msg_ccx_connection_test",
			"type":    "message",
			"role":    "assistant",
			"model":   model,
			"content": []interface{}{},
			"usage": gin.H{
				"input_tokens":                15,
				"output_tokens":               0,
				"cache_creation_input_tokens": 0,
				"cache_read_input_tokens":     0,
			},
		},
	})
	writeClaudeSSEEvent(c, "content_block_start", gin.H{
		"type":          "content_block_start",
		"index":         0,
		"content_block": gin.H{"type": "text", "text": ""},
	})
	for _, delta := range claudeDesktopConnectionTestDeltas {
		writeClaudeSSEEvent(c, "content_block_delta", gin.H{
			"type":  "content_block_delta",
			"index": 0,
			"delta": gin.H{"type": "text_delta", "text": delta},
		})
	}
	writeClaudeSSEEvent(c, "content_block_stop", gin.H{
		"type":  "content_block_stop",
		"index": 0,
	})
	writeClaudeSSEEvent(c, "message_delta", gin.H{
		"type": "message_delta",
		"delta": gin.H{
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
			"stop_details":  nil,
		},
		"usage": gin.H{
			"input_tokens":                15,
			"output_tokens":               14,
			"cache_creation_input_tokens": 0,
			"cache_read_input_tokens":     0,
		},
	})
	writeClaudeSSEEvent(c, "message_stop", gin.H{
		"type": "message_stop",
	})
}

func writeClaudeSSEEvent(c *gin.Context, event string, data gin.H) {
	payload, _ := json.Marshal(data)
	_, _ = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, payload)
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
}

func countUserMessages(messages []types.ClaudeMessage) int {
	count := 0
	for _, msg := range messages {
		if msg.Role == "user" && len(extractUserTextBlocks(msg)) > 0 {
			count++
		}
	}
	return count
}

func extractLastUserMessage(messages []types.ClaudeMessage) string {
	const maxLen = 80
	var parts []string
	totalLen := 0

	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != "user" {
			continue
		}
		texts := extractUserTextBlocks(messages[i])
		for j := len(texts) - 1; j >= 0; j-- {
			parts = append(parts, texts[j])
			totalLen += len([]rune(texts[j]))
			if totalLen >= maxLen {
				break
			}
		}
		if totalLen >= maxLen {
			break
		}
	}

	if len(parts) == 0 {
		return ""
	}

	for left, right := 0, len(parts)-1; left < right; left, right = left+1, right-1 {
		parts[left], parts[right] = parts[right], parts[left]
	}
	return strings.Join(parts, " / ")
}

func isClaudeCodeRecapRequest(bodyBytes []byte) bool {
	var req struct {
		ContextManagement *struct {
			Edits []struct {
				Type string `json:"type"`
			} `json:"edits"`
		} `json:"context_management"`
		Messages []types.ClaudeMessage `json:"messages"`
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		return false
	}
	if req.ContextManagement == nil || len(req.ContextManagement.Edits) == 0 {
		return false
	}

	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role != "user" {
			continue
		}
		for _, text := range extractRawUserTextBlocks(req.Messages[i]) {
			if isClaudeCodeRecapPrompt(text) {
				return true
			}
		}
		return false
	}
	return false
}

func extractUserTextBlocks(message types.ClaudeMessage) []string {
	texts := []string{}
	for _, text := range extractRawUserTextBlocks(message) {
		if cleaned := cleanUserTitleText(text); cleaned != "" {
			texts = append(texts, cleaned)
		}
	}
	return texts
}

func extractRawUserTextBlocks(message types.ClaudeMessage) []string {
	texts := []string{}

	switch content := message.Content.(type) {
	case string:
		texts = append(texts, content)
	case []interface{}:
		for _, block := range content {
			m, ok := block.(map[string]interface{})
			if !ok || m["type"] != "text" {
				continue
			}
			if text, ok := m["text"].(string); ok {
				texts = append(texts, text)
			}
		}
	}
	return texts
}

func cleanUserTitleText(text string) string {
	text = removeTaggedBlocks(text, "system-reminder")
	text = removeTaggedBlocks(text, "local-command-caveat")
	text = removeTaggedBlocks(text, "command-name")
	text = removeTaggedBlocks(text, "command-message")
	text = removeTaggedBlocks(text, "command-args")
	text = removeTaggedBlocks(text, "local-command-stdout")
	text = removeTaggedBlocks(text, "local-command-stderr")
	text = strings.TrimSpace(text)
	if common.IsClaudeNoVisibleOutputRetryPrompt(text) {
		return ""
	}
	if isClaudeCodeRecapPrompt(text) {
		return ""
	}
	if isInjectedContextTitleText(text) {
		return ""
	}
	if strings.HasPrefix(text, "<") && strings.Contains(text, ">") {
		return ""
	}
	return text
}

func isClaudeCodeRecapPrompt(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "The user stepped away and is coming back. Recap in under 40 words")
}

func isInjectedContextTitleText(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	injectedPrefixes := []string{
		"# agents.md instructions",
		"# claude.md instructions",
		"# claude command:",
		"# codebase and user instructions",
		"<instructions>",
	}
	for _, prefix := range injectedPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return strings.Contains(lower, "project-doc") && strings.Contains(lower, "agents.md")
}

func removeTaggedBlocks(text, tag string) string {
	for {
		start := strings.Index(text, "<"+tag+">")
		if start < 0 {
			return text
		}
		endTag := "</" + tag + ">"
		end := strings.Index(text[start:], endTag)
		if end < 0 {
			return strings.TrimSpace(text[:start])
		}
		end += start + len(endTag)
		text = text[:start] + text[end:]
	}
}

// CountTokensHandler 处理 /v1/messages/count_tokens 请求
func CountTokensHandler(envCfg *config.EnvConfig, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		middleware.ProxyAuthMiddleware(envCfg)(c)
		if c.IsAborted() {
			return
		}

		// 使用统一的请求体读取函数，应用大小限制
		bodyBytes, err := common.ReadRequestBody(c, envCfg.MaxRequestBodySize)
		if err != nil {
			// ReadRequestBody 已经返回了错误响应
			return
		}
		c.Set("requestBodyBytes", bodyBytes)

		var req struct {
			Model    string      `json:"model"`
			System   interface{} `json:"system"`
			Messages interface{} `json:"messages"`
			Tools    interface{} `json:"tools"`
		}
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			c.JSON(400, gin.H{"error": "Invalid JSON"})
			return
		}
		var claudeReq types.ClaudeRequest
		_ = json.Unmarshal(bodyBytes, &claudeReq)
		userID := utils.ExtractUnifiedSessionID(c, common.NormalizeMetadataUserID(bodyBytes))
		agentCtx := utils.ExtractAgentContext(c, bodyBytes)
		common.SetRequestLogContextWithAgent(c, userID, countUserMessages(claudeReq.Messages), agentCtx)
		c.Set("agentContext", agentCtx)

		inputTokens := utils.EstimateRequestTokens(bodyBytes)

		c.JSON(200, gin.H{
			"input_tokens": inputTokens,
		})

		if envCfg.EnableResponseLogs {
			common.RequestLogf(c, "[Messages-Token] CountTokens本地估算: model=%s, input_tokens=%d", req.Model, inputTokens)
		}
	}
}
