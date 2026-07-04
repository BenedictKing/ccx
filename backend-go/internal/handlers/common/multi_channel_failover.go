package common

import (
	"fmt"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

// MultiChannelAttemptResult 描述一次"选中渠道"的尝试结果（用于多渠道 failover 外壳复用）。
type MultiChannelAttemptResult struct {
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
			HandleAllChannelsFailed(c, false, failoverErr, lastError, apiType)
		}
	}

	failedChannels := make(map[int]bool)
	var lastError error
	var lastFailoverError *FailoverError
	hasImageContent := false
	if body := GetEffectiveRequestBody(c, nil); len(body) > 0 {
		hasImageContent = HasImageContent(c, body)
	}

	maxChannelAttempts := channelScheduler.GetActiveChannelCount(kind)

	for channelAttempt := 0; channelAttempt < maxChannelAttempts; channelAttempt++ {
		// 检查客户端是否已断开连接
		select {
		case <-c.Request.Context().Done():
			if envCfg.ShouldLog("info") {
				RequestLogf(c, "[%s-Cancel] 请求已取消，停止渠道 failover", apiType)
			}
			return
		default:
			// 继续正常流程
		}

		selection, err := channelScheduler.SelectChannelWithOptions(c.Request.Context(), scheduler.SelectionOptions{
			UserID:             userID,
			FailedChannels:     failedChannels,
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

		upstream := selection.Upstream
		channelIndex := selection.ChannelIndex

		if envCfg.ShouldLog("info") && upstream != nil {
			RequestLogf(c, "[%s-Select] 选择渠道: [%d] %s (原因: %s, 尝试 %d/%d)",
				apiType, channelIndex, upstream.Name, selection.Reason, channelAttempt+1, maxChannelAttempts)
		}

		result := trySelectedChannel(selection)
		if result.Handled {
			lastUserMsg, _ := c.Get("lastUserMessage")
			lastUserMsgStr, _ := lastUserMsg.(string)
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
					channelScheduler.SetTraceAffinityForRequirement(affinityUserID, channelIndex, kind, contextRequirement)
				}
				channelName := ""
				if upstream != nil {
					channelName = upstream.Name
				}
				channelScheduler.TrackConversation(kind, userID, model, channelIndex, channelName, "", lastUserMsgStr, userMsgCountInt, agentRole, AgentContextFromGin(c))
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

		failedChannels[channelIndex] = true

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
	}

	RequestLogf(c, "[%s-Error] 所有渠道都失败了", apiType)
	handleAllFailed(c, lastFailoverError, lastError)
}
