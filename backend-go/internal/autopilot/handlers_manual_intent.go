package autopilot

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// ── 请求/响应类型 ──

// CreateIntentRequest POST /api/manual-intents 请求体。
type CreateIntentRequest struct {
	Name                   string      `json:"name,omitempty"`
	IntentType             IntentType  `json:"intentType"`
	ChannelKind            string      `json:"channelKind"`
	ChannelUID             string      `json:"channelUid,omitempty"`
	MetricsKey             string      `json:"metricsKey,omitempty"`
	Model                  string      `json:"model,omitempty"`
	MappedModel            string      `json:"mappedModel,omitempty"`
	Effort                 EffortLevel `json:"effort,omitempty"`
	AgentRoles             []string    `json:"agentRoles,omitempty"`
	TaskClasses            []TaskClass `json:"taskClasses,omitempty"`
	SessionID              string      `json:"sessionId,omitempty"`
	TrafficPercent         int         `json:"trafficPercent,omitempty"`
	ExpiresAt              string      `json:"expiresAt"`            // RFC3339 格式
	TTLMinutes             int         `json:"ttlMinutes,omitempty"` // 替代 expiresAt，分钟数
	MaxRequests            int         `json:"maxRequests,omitempty"`
	MaxEstimatedCost       float64     `json:"maxEstimatedCost,omitempty"`
	FallbackOnFailure      bool        `json:"fallbackOnFailure,omitempty"`
	RequireHardConstraints bool        `json:"requireHardConstraints"`
	CreatedBy              string      `json:"createdBy,omitempty"`
	Reason                 string      `json:"reason,omitempty"`
}

// IntentResponse 单条意图的 API 响应。
type IntentResponse struct {
	IntentUID string `json:"intentUid"`
	*ManualRoutingIntent
}

// IntentListResponse 意图列表的 API 响应。
type IntentListResponse struct {
	Intents []*ManualRoutingIntent `json:"intents"`
	Total   int                    `json:"total"`
}

// ── 路由注册 ──

// RegisterManualIntentRoutes 注册人工路由意图 API 到给定路由组。
// 路由前缀由调用方控制（例如 router.Group("/manual-intents")）。
func RegisterManualIntentRoutes(router gin.IRouter, store *ManualIntentStore) {
	group := router.Group("/manual-intents")
	{
		group.POST("", handleCreateIntent(store))
		group.GET("", handleListIntents(store))
		group.GET("/:uid", handleGetIntent(store))
		group.DELETE("/:uid", handleDeleteIntent(store))
	}
}

// ── Handler 实现 ──

// handleCreateIntent POST /api/manual-intents
// 创建一条人工路由意图。
func handleCreateIntent(store *ManualIntentStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req CreateIntentRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败: " + err.Error()})
			return
		}

		// 解析 expiresAt
		var expiresAt time.Time
		if req.ExpiresAt != "" {
			var err error
			expiresAt, err = time.Parse(time.RFC3339, req.ExpiresAt)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "expiresAt 格式无效，需 RFC3339: " + err.Error()})
				return
			}
		} else if req.TTLMinutes > 0 {
			expiresAt = time.Now().UTC().Add(time.Duration(req.TTLMinutes) * time.Minute)
		} else {
			// 默认 TTL 24 小时
			expiresAt = time.Now().UTC().Add(24 * time.Hour)
		}

		intent := &ManualRoutingIntent{
			Name:                   req.Name,
			IntentType:             req.IntentType,
			ChannelKind:            req.ChannelKind,
			ChannelUID:             req.ChannelUID,
			MetricsKey:             req.MetricsKey,
			Model:                  req.Model,
			MappedModel:            req.MappedModel,
			Effort:                 req.Effort,
			AgentRoles:             req.AgentRoles,
			TaskClasses:            req.TaskClasses,
			SessionID:              req.SessionID,
			TrafficPercent:         req.TrafficPercent,
			ExpiresAt:              expiresAt,
			MaxRequests:            req.MaxRequests,
			MaxEstimatedCost:       req.MaxEstimatedCost,
			FallbackOnFailure:      req.FallbackOnFailure,
			RequireHardConstraints: req.RequireHardConstraints,
			CreatedBy:              req.CreatedBy,
			Reason:                 req.Reason,
		}

		if err := store.Create(intent); err != nil {
			if validationErr, ok := err.(*IntentValidationError); ok {
				c.JSON(http.StatusBadRequest, gin.H{"error": validationErr.Error()})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "创建意图失败: " + err.Error()})
			return
		}

		c.JSON(http.StatusCreated, intent)
	}
}

// handleListIntents GET /api/manual-intents
// 查询参数：
//   - all=true  返回全部意图（含 expired/exhausted/disabled），否则只返回 active
func handleListIntents(store *ManualIntentStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		showAll := c.Query("all") == "true"

		var intents []*ManualRoutingIntent
		if showAll {
			intents = store.ListAll()
		} else {
			intents = store.ListActive()
		}

		c.JSON(http.StatusOK, IntentListResponse{
			Intents: intents,
			Total:   len(intents),
		})
	}
}

// handleGetIntent GET /api/manual-intents/:uid
func handleGetIntent(store *ManualIntentStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := c.Param("uid")
		if uid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "uid 不能为空"})
			return
		}

		intent := store.Get(uid)
		if intent == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "意图不存在"})
			return
		}

		c.JSON(http.StatusOK, intent)
	}
}

// handleDeleteIntent DELETE /api/manual-intents/:uid
func handleDeleteIntent(store *ManualIntentStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := c.Param("uid")
		if uid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "uid 不能为空"})
			return
		}

		if err := store.Delete(uid); err != nil {
			if err == ErrIntentNotFound {
				c.JSON(http.StatusNotFound, gin.H{"error": "意图不存在"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败: " + err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"message": "已删除", "intentUid": uid})
	}
}
