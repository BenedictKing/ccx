package handlers

import (
	"net/http"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/conversation"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

type ConversationHandlerDeps struct {
	Tracker          *conversation.ConversationTracker
	OverrideManager  *conversation.OverrideManager
	ChannelScheduler *scheduler.ChannelScheduler
	ConfigManager    *config.ConfigManager
}

func GetConversations(deps *ConversationHandlerDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		kindFilter := c.Query("kind")

		conversations := deps.Tracker.GetActiveConversations(kindFilter)

		// 同步清理孤儿 override（会话已过期但 override 仍存在的情况）
		if kindFilter == "" {
			activeIDs := make(map[string]bool, len(conversations))
			for _, conv := range conversations {
				activeIDs[conv.ID] = true
			}
			deps.OverrideManager.PurgeOrphans(activeIDs)
		}

		overrides := deps.OverrideManager.GetAllOverrides()

		overridesResponse := make(map[string]interface{})
		for id, override := range overrides {
			overridesResponse[id] = gin.H{
				"sequence":         override.Sequence,
				"hasMainSequence":  override.HasMainSequence,
				"subagentSequence": override.SubagentSequence,
				"setAt":            override.SetAt,
				"expiresAt":        override.ExpiresAt,
				"isPerpetual":      override.IsPerpetual,
			}
		}

		channelsByKind := gin.H{}
		for _, kind := range []scheduler.ChannelKind{
			scheduler.ChannelKindMessages,
			scheduler.ChannelKindChat,
			scheduler.ChannelKindImages,
			scheduler.ChannelKindResponses,
			scheduler.ChannelKindGemini,
		} {
			channelsByKind[string(kind)] = deps.ChannelScheduler.GetConversationChannelsByKind(kind)
		}

		c.JSON(http.StatusOK, gin.H{
			"conversations":  conversations,
			"total":          len(conversations),
			"overrides":      overridesResponse,
			"channelsByKind": channelsByKind,
		})
	}
}

type SetOverrideRequest struct {
	Sequence              []conversation.ChannelEntry `json:"sequence"`
	SubagentSequence      []conversation.ChannelEntry `json:"subagentSequence,omitempty"`      // subagent 专用序列（可选）
	ClearSubagentSequence bool                        `json:"clearSubagentSequence,omitempty"` // 仅清除 subagent 专用序列
	Duration              *int                        `json:"duration,omitempty"`              // 秒；nil=系统默认；-1=永不恢复
}

func SetConversationOverride(deps *ConversationHandlerDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")
		if convID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "conversation id is required"})
			return
		}

		var req SetOverrideRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
			return
		}

		conv, ok := deps.Tracker.GetConversation(convID)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
			return
		}
		if deps.ConfigManager != nil {
			if _, activated := config.SubagentRoutingForConfig(deps.ConfigManager.GetConfig(), conv.Kind); activated {
				c.JSON(http.StatusConflict, gin.H{"error": "该协议已启用独立子代理配置，请在渠道列表的子代理标签页调整全局策略", "code": "SUBAGENT_ROUTING_ACTIVE"})
				return
			}
		}

		// 解析 duration：nil=系统默认，-1=永不恢复，>0=自定义秒数
		var overrideDuration time.Duration
		if req.Duration != nil {
			if *req.Duration == -1 {
				overrideDuration = -1
			} else if *req.Duration > 0 {
				overrideDuration = time.Duration(*req.Duration) * time.Second
			}
		}

		if len(req.Sequence) == 0 && len(req.SubagentSequence) == 0 && !req.ClearSubagentSequence {
			c.JSON(http.StatusBadRequest, gin.H{"error": "sequence or subagentSequence is required"})
			return
		}

		if len(req.Sequence) > 0 {
			err := deps.OverrideManager.SetOverride(convID, conv.Kind, conv.RawUserID, req.Sequence, overrideDuration)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}

		if len(req.SubagentSequence) > 0 {
			if saErr := deps.OverrideManager.SetSubagentOverride(convID, conv.Kind, conv.RawUserID, req.SubagentSequence, req.Sequence, overrideDuration); saErr != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": saErr.Error()})
				return
			}
		}

		if req.ClearSubagentSequence {
			deps.OverrideManager.ClearSubagentOverride(convID)
		}

		c.JSON(http.StatusOK, gin.H{
			"message":               "override set successfully",
			"conversationId":        convID,
			"sequence":              req.Sequence,
			"subagentSequence":      req.SubagentSequence,
			"clearSubagentSequence": req.ClearSubagentSequence,
		})
	}
}

func RemoveConversationOverride(deps *ConversationHandlerDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		convID := c.Param("id")
		if convID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "conversation id is required"})
			return
		}

		removed := deps.OverrideManager.RemoveOverride(convID)
		if !removed {
			c.JSON(http.StatusNotFound, gin.H{"error": "no override found for this conversation"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"message":        "override removed",
			"conversationId": convID,
		})
	}
}

// GetConversationSettings 获取驾驶舱设置
func GetConversationSettings(deps *ConversationHandlerDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := deps.ConfigManager.GetConfig()

		// 获取有效的 override TTL（优先使用配置文件中的值，否则返回 0 表示使用环境变量默认值）
		overrideTTLMinutes := cfg.OverrideTTLMinutes

		c.JSON(http.StatusOK, gin.H{
			"overrideTtlMinutes": overrideTTLMinutes,
		})
	}
}

type UpdateConversationSettingsRequest struct {
	OverrideTTLMinutes *int `json:"overrideTtlMinutes"` // 1-1440；nil 表示不修改
}

// UpdateConversationSettings 更新驾驶舱设置
func UpdateConversationSettings(deps *ConversationHandlerDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req UpdateConversationSettingsRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
			return
		}

		if req.OverrideTTLMinutes != nil {
			ttl := *req.OverrideTTLMinutes

			// 更新配置文件（内部会标准化为有效选项，不合适的值使用默认 30 分钟）
			if err := deps.ConfigManager.SetOverrideTTLMinutes(ttl); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update config: " + err.Error()})
				return
			}

			// 获取标准化后的值
			cfg := deps.ConfigManager.GetConfig()
			normalizedTTL := cfg.OverrideTTLMinutes

			// 动态更新 OverrideManager 的默认 TTL
			if normalizedTTL == -1 {
				// -1 表示永不过期
				deps.OverrideManager.SetDefaultTTL(-1)
			} else {
				deps.OverrideManager.SetDefaultTTL(time.Duration(normalizedTTL) * time.Minute)
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"message": "settings updated successfully",
		})
	}
}
