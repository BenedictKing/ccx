package common

import (
	"context"
	"fmt"
	"time"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

// MultiChannelAttemptResult 描述一次"选中渠道"的尝试结果（用于多渠道 failover 外壳复用）。
type MultiChannelAttemptResult struct {
	Route             scheduler.ChannelRouteRef
	Handled           bool
	Attempted         bool
	SuccessKey        string
	SuccessBaseURLIdx int
	FailoverError     *FailoverError
	Usage             *types.Usage
	LastError         error
	ResponseText      string
}

// TrySelectedChannelFunc 尝试一次选中的渠道，返回该渠道的尝试结果。
type TrySelectedChannelFunc func(selection *scheduler.SelectionResult) MultiChannelAttemptResult

// OnMultiChannelHandledFunc 在请求被"处理完成"时回调（成功或非 failover 错误都会触发）。
type OnMultiChannelHandledFunc func(selection *scheduler.SelectionResult, result MultiChannelAttemptResult)

type pendingRoutingAttempt struct {
	route           scheduler.ChannelRouteRef
	selection       *scheduler.SelectionResult
	result          MultiChannelAttemptResult
	duration        time.Duration
	channelFallback bool
}

// HandleAllFailedFunc 处理"所有渠道都失败"的返回逻辑（不同入口可能有不同错误格式）。
type HandleAllFailedFunc func(c *gin.Context, failoverErr *FailoverError, lastError error)

// HandleMultiChannelFailover 处理多渠道 failover 外壳逻辑（选渠道 + 聚合错误 + Trace 亲和）。
// 具体"渠道内 Key/BaseURL 轮转"由 trySelectedChannel 实现（通常调用 TryUpstreamWithAllKeys）。
func HandleMultiChannelFailover(
	c *gin.Context,
	envCfg *config.EnvConfig,
	channelScheduler *scheduler.ChannelScheduler,
	kind scheduler.ChannelKind,
	apiType string,
	userID string,
	model string,
	agentRole string,
	trySelectedChannel TrySelectedChannelFunc,
	onHandled OnMultiChannelHandledFunc,
	handleAllFailed HandleAllFailedFunc,
) {
	HandleMultiChannelFailoverWithContextRequirement(c, envCfg, channelScheduler, kind, apiType, userID, model, nil, agentRole, trySelectedChannel, onHandled, handleAllFailed)
}

// HandleMultiChannelFailoverWithContextRequirement 处理带上下文需求的多渠道 failover。
// agentRole 用于角色感知 override 查找与 trace affinity 隔离（"" | "main" | "subagent"）。
func HandleMultiChannelFailoverWithContextRequirement(
	c *gin.Context,
	envCfg *config.EnvConfig,
	channelScheduler *scheduler.ChannelScheduler,
	kind scheduler.ChannelKind,
	apiType string,
	userID string,
	model string,
	contextRequirement *scheduler.ContextRequirement,
	agentRole string,
	trySelectedChannel TrySelectedChannelFunc,
	onHandled OnMultiChannelHandledFunc,
	handleAllFailed HandleAllFailedFunc,
) {
	HandleMultiChannelFailoverWithSelectionFilter(
		c,
		envCfg,
		channelScheduler,
		kind,
		apiType,
		userID,
		model,
		contextRequirement,
		agentRole,
		nil,
		trySelectedChannel,
		onHandled,
		handleAllFailed,
	)
}

func HandleMultiChannelFailoverWithSelectionFilter(
	c *gin.Context,
	envCfg *config.EnvConfig,
	channelScheduler *scheduler.ChannelScheduler,
	kind scheduler.ChannelKind,
	apiType string,
	userID string,
	model string,
	contextRequirement *scheduler.ContextRequirement,
	agentRole string,
	candidateFilter scheduler.CandidateFilterFunc,
	trySelectedChannel TrySelectedChannelFunc,
	onHandled OnMultiChannelHandledFunc,
	handleAllFailed HandleAllFailedFunc,
) {
	if c == nil || envCfg == nil || channelScheduler == nil || trySelectedChannel == nil {
		return
	}
	if handleAllFailed == nil {
		handleAllFailed = func(c *gin.Context, failoverErr *FailoverError, lastError error) {
			HandleAllChannelsFailed(c, failoverErr, lastError, apiType)
		}
	}
	requestStartedAt := time.Now()

	// 生成服务端逻辑请求关联 ID（贯穿所有渠道尝试和 trace）
	correlationID := autopilot.GenerateRequestCorrelationID()
	c.Set("ccx.request_correlation_id", correlationID)

	// 该外壳是 route-aware 调用方：失败集合只按物理路由记录，避免不同 kind
	// 的相同 index 相互污染。旧 SelectChannel/SelectionOptions 调用方仍可显式传 FailedChannels。
	failedRoutes := make(map[scheduler.ChannelRouteKey]bool)
	var lastError error
	var lastFailoverError *FailoverError
	hasImageContent := false
	requestBody := GetEffectiveRequestBody(c, nil)
	if len(requestBody) > 0 {
		hasImageContent = HasImageContent(c, requestBody)
	}
	EnsureAutopilotRequestProfile(c, kind, model, "", userID, requestBody)

	// Same-kind candidates are already unique by physical route today. Keeping the bound
	// separate from failedRoutes makes Phase 4 able to replace it with a deduplicated
	// cross-kind candidate count without changing the attempt loop contract.
	maxChannelAttempts := channelScheduler.GetActiveChannelCount(kind)
	if (kind == scheduler.ChannelKindMessages || kind == scheduler.ChannelKindResponses) && c.Param("routePrefix") == "" && c.GetHeader("X-Channel") == "" {
		maxChannelAttempts = channelScheduler.GetFederatedCandidateCount(kind)
	}
	var pendingOutcome *pendingRoutingAttempt

	for channelAttempt := 0; channelAttempt < maxChannelAttempts; channelAttempt++ {
		// 检查客户端是否已断开连接
		select {
		case <-c.Request.Context().Done():
			if pendingOutcome != nil {
				pendingOutcome.result.LastError = context.Canceled
				notifyRoutingOutcome(pendingOutcome.selection, buildRoutingOutcome(
					c, pendingOutcome.selection, pendingOutcome.result, true,
					pendingOutcome.channelFallback, requestStartedAt, time.Since(requestStartedAt),
				))
			}
			if envCfg.ShouldLog("info") {
				RequestLogf(c, "[%s-Cancel] 请求已取消，停止渠道 failover", apiType)
			}
			return
		default:
			// 继续正常流程
		}

		selection, err := channelScheduler.SelectChannelWithOptions(c.Request.Context(), scheduler.SelectionOptions{
			UserID:             userID,
			FailedRoutes:       failedRoutes,
			Kind:               kind,
			Model:              model,
			RoutePrefix:        c.Param("routePrefix"),
			ChannelName:        c.GetHeader("X-Channel"),
			ContextRequirement: contextRequirement,
			HasImageContent:    hasImageContent,
			AgentRole:          agentRole,
			CandidateFilter:    candidateFilter,
		})
		if err != nil {
			lastError = err
			break
		}
		if channelAttempt == 0 && selection.CandidateCount > 0 {
			maxChannelAttempts = selection.CandidateCount
		}

		// 新渠道已经成功选出，上一渠道失败由“请求终态”降为“中间尝试失败”。
		if pendingOutcome != nil {
			notifyRoutingOutcome(pendingOutcome.selection, buildRoutingOutcome(
				c, pendingOutcome.selection, pendingOutcome.result, false,
				false, requestStartedAt, pendingOutcome.duration,
			))
			pendingOutcome = nil
		}

		upstream := selection.Upstream
		channelIndex := selection.ChannelIndex

		// 将 Autopilot trace UID 写入 gin context，供 ChannelLog 关联
		if selection.AutopilotTraceUID != "" {
			c.Set("ccx.autopilot_trace_uid", selection.AutopilotTraceUID)
		}

		if envCfg.ShouldLog("info") && upstream != nil {
			RequestLogf(c, "[%s-Select] 选择渠道: [%d] %s (原因: %s, 尝试 %d/%d)",
				apiType, channelIndex, upstream.Name, selection.Reason, channelAttempt+1, maxChannelAttempts)
		}
		if envCfg.ShouldLog("debug") {
			if summary := scheduler.FormatSelectionTraceSummary(selection.Trace, 4); summary != "" {
				RequestLogf(c, "[%s-Select-Trace] %s", apiType, summary)
			}
		}

		resetAutopilotAttemptTelemetry(c)
		attemptStartedAt := time.Now()
		result := trySelectedChannel(selection)
		attemptDuration := time.Since(attemptStartedAt)
		if result.Handled {
			notifyRoutingOutcome(selection, buildRoutingOutcome(
				c, selection, result, true, channelAttempt > 0,
				requestStartedAt, time.Since(requestStartedAt),
			))
			lastUserMsg, _ := c.Get("lastUserMessage")
			lastUserMsgStr, _ := lastUserMsg.(string)
			lastUserMsgs, _ := c.Get("lastUserMessages")
			lastUserMessages, _ := lastUserMsgs.([]string)
			userMsgCount, _ := c.Get("userMessageCount")
			userMsgCountInt, _ := userMsgCount.(int)

			// 只有真正成功的普通请求才设置 Trace 亲和并追踪对话；title 请求没有用户消息，不污染卡片状态。
			if result.SuccessKey != "" && (lastUserMsgStr != "" || userMsgCountInt > 0) {
				// 含图请求成功不写普通 Trace 亲和，避免一次视觉请求覆盖文本亲和
				if kind == scheduler.ChannelKindImages || !HasImageContentCached(c) {
					// subagent 使用隔离的亲和 key，避免覆盖主对话亲和
					affinityUserID := userID
					if agentRole == "subagent" {
						affinityUserID = userID + ":subagent"
					}
					channelScheduler.SetTraceAffinityRouteForRequirement(affinityUserID, selection.Route, kind, contextRequirement)
				}
				channelName := ""
				if upstream != nil {
					channelName = upstream.Name
				}
				channelScheduler.TrackConversationWithMessages(kind, userID, model, channelIndex, channelName, "", lastUserMsgStr, lastUserMessages, userMsgCountInt, agentRole, AgentContextFromGin(c))
				if envCfg.ShouldLog("debug") {
					RequestLogf(c, "[%s-Conversation-Debug] 已追踪对话: kind=%s, user=%s, model=%s, channel=%d, userMessages=%d, hasFallbackTitle=%t",
						apiType, kind, scheduler.MaskUserIDForLog(userID), model, channelIndex, userMsgCountInt, lastUserMsgStr != "")
				}
			}
			if onHandled != nil {
				onHandled(selection, result)
			}
			return
		}

		failedRoutes[selection.Route.Key()] = true

		if result.FailoverError != nil {
			lastFailoverError = result.FailoverError
			if upstream != nil {
				lastError = fmt.Errorf("渠道 [%d] %s 失败", channelIndex, upstream.Name)
			} else {
				lastError = fmt.Errorf("渠道 [%d] 失败", channelIndex)
			}
		} else if result.LastError != nil {
			lastError = result.LastError
		}

		if result.Attempted && upstream != nil {
			RequestLogf(c, "[%s-Failover] 警告: 渠道 [%d] %s 所有密钥都失败，尝试下一个渠道", apiType, channelIndex, upstream.Name)
		}
		pendingOutcome = &pendingRoutingAttempt{
			route:           selection.Route,
			selection:       selection,
			result:          result,
			duration:        attemptDuration,
			channelFallback: channelAttempt > 0,
		}
	}

	RequestLogf(c, "[%s-Error] 所有渠道都失败了", apiType)
	handleAllFailed(c, lastFailoverError, lastError)
	if pendingOutcome != nil {
		notifyRoutingOutcome(pendingOutcome.selection, buildRoutingOutcome(
			c, pendingOutcome.selection, pendingOutcome.result, true,
			pendingOutcome.channelFallback, requestStartedAt, time.Since(requestStartedAt),
		))
	}
}
