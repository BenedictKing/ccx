package autopilot

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// ─── 请求/响应类型 ──────────────────────────────────────────────────────────────

// SubscriptionCreateRequest POST /api/subscriptions 请求体。
type SubscriptionCreateRequest struct {
	SubscriptionUID  string             `json:"subscriptionUid" binding:"required"`
	DisplayName      string             `json:"displayName" binding:"required"`
	Provider         string             `json:"provider"`
	OriginType       string             `json:"originType"`
	OriginTier       string             `json:"originTier"`
	BillingMode      string             `json:"billingMode"`
	Currency         string             `json:"currency"`
	Balance          float64            `json:"balance"`
	GroupMultipliers map[string]float64 `json:"groupMultipliers,omitempty"`
	Notes            string             `json:"notes"`
	Source           string             `json:"source"`

	// Phase 4 Item 6：余额自动刷新
	BillingAPIKey      string `json:"billingApiKey,omitempty"`
	AutoRefreshEnabled bool   `json:"autoRefreshEnabled,omitempty"`
}

// SubscriptionUpdateRequest PUT /api/subscriptions/:uid 请求体。
// 所有字段可选，仅更新非零值字段。
type SubscriptionUpdateRequest struct {
	DisplayName      *string            `json:"displayName,omitempty"`
	Provider         *string            `json:"provider,omitempty"`
	OriginType       *string            `json:"originType,omitempty"`
	OriginTier       *string            `json:"originTier,omitempty"`
	BillingMode      *string            `json:"billingMode,omitempty"`
	Currency         *string            `json:"currency,omitempty"`
	Balance          *float64           `json:"balance,omitempty"`
	GroupMultipliers map[string]float64 `json:"groupMultipliers,omitempty"`
	Notes            *string            `json:"notes,omitempty"`
	Source           *string            `json:"source,omitempty"`
	Confidence       *float64           `json:"confidence,omitempty"`

	// Phase 4 Item 6：余额自动刷新
	BillingAPIKey      *string `json:"billingApiKey,omitempty"`
	AutoRefreshEnabled *bool   `json:"autoRefreshEnabled,omitempty"`

	// ExpectedVersion 可选乐观锁；不传则与现有语义保持一致。
	ExpectedVersion *uint64 `json:"expectedVersion,omitempty"`
}

// LinkRequest POST /api/subscriptions/:uid/link 请求体。
type LinkRequest struct {
	ChannelUID string `json:"channelUid" binding:"required"`
}

// UnlinkRequest POST /api/subscriptions/:uid/unlink 请求体。
type UnlinkRequest struct {
	ChannelUID string `json:"channelUid" binding:"required"`
}

// SubscriptionItem 订阅列表/详情响应单条。
type SubscriptionItem struct {
	SubscriptionUID   string             `json:"subscriptionUid"`
	DisplayName       string             `json:"displayName"`
	Provider          string             `json:"provider,omitempty"`
	OriginType        string             `json:"originType,omitempty"`
	OriginTier        string             `json:"originTier,omitempty"`
	BillingMode       string             `json:"billingMode,omitempty"`
	Currency          string             `json:"currency,omitempty"`
	Balance           float64            `json:"balance,omitempty"`
	UsedQuota         int64              `json:"usedQuota,omitempty"`
	GroupMultipliers  map[string]float64 `json:"groupMultipliers,omitempty"`
	LinkedChannelUIDs []string           `json:"linkedChannelUids,omitempty"`
	Source            string             `json:"source,omitempty"`
	Confidence        float64            `json:"confidence,omitempty"`
	Notes             string             `json:"notes,omitempty"`
	CreatedAt         string             `json:"createdAt"`
	UpdatedAt         string             `json:"updatedAt"`
	ArchivedAt        string             `json:"archivedAt,omitempty"`

	// Phase 4 Item 6：余额自动刷新
	BillingAPIKey           string `json:"billingApiKey,omitempty"`
	AutoRefreshEnabled      bool   `json:"autoRefreshEnabled,omitempty"`
	AutoRefreshSupported    bool   `json:"autoRefreshSupported,omitempty"` // provider 是否在白名单内
	LastBalanceRefreshAt    string `json:"lastBalanceRefreshAt,omitempty"`
	LastBalanceRefreshError string `json:"lastBalanceRefreshError,omitempty"`

	// ── §8.5.1：new-api 订阅集成 ──
	// AccessToken 绝不完整出响应，只回显脱敏后的尾部片段，字段名区分以免误用。
	BaseURL             string                 `json:"baseUrl,omitempty"`
	AccessTokenMasked   string                 `json:"accessTokenMasked,omitempty"`
	UserID              string                 `json:"userId,omitempty"`
	Username            string                 `json:"username,omitempty"`
	AuthTokenMode       string                 `json:"authTokenMode,omitempty"`
	ProvisionKeyName    string                 `json:"provisionKeyName,omitempty"`
	ProvisionGroup      string                 `json:"provisionGroup,omitempty"`
	ProvisionGroupRatio *float64               `json:"provisionGroupRatio,omitempty"`
	MaxGroupMultiplier  *float64               `json:"maxGroupMultiplier,omitempty"`
	ProvisionModels     []string               `json:"provisionModels,omitempty"`
	ProvisionedTokenID  int                    `json:"provisionedTokenId,omitempty"`
	ProvisionedKeys     []NewApiProvisionedKey `json:"provisionedKeys,omitempty"`
	AvailableModels     []string               `json:"availableModels,omitempty"`
	// 站点访问代理（如地域封锁场景），非敏感配置可直接回显
	ProxyURL          string `json:"proxyUrl,omitempty"`
	ProxyPreferDirect bool   `json:"proxyPreferDirect,omitempty"`
}

// SubscriptionsListResponse GET /api/subscriptions 返回结构。
type SubscriptionsListResponse struct {
	Subscriptions []SubscriptionItem `json:"subscriptions"`
	Total         int                `json:"total"`
}

// ─── 路由注册 ──────────────────────────────────────────────────────────────────

// RegisterSubscriptionRoutes 注册订阅中心 CRUD + 渠道链接 API 到给定路由组。
func RegisterSubscriptionRoutes(router gin.IRouter, store *SubscriptionStore, refreshWorker *SubscriptionRefreshWorker, syncServices ...*NewApiSubscriptionSyncService) {
	var syncService *NewApiSubscriptionSyncService
	if len(syncServices) > 0 {
		syncService = syncServices[0]
	}
	group := router.Group("/subscriptions")
	{
		group.GET("", handleListSubscriptions(store))
		group.POST("", handleCreateSubscription(store))
		group.GET("/:uid", handleGetSubscription(store))
		group.PUT("/:uid", handleUpdateSubscription(store))
		group.PATCH("/:uid/billing-terms", handlePatchBillingTerms(store))
		group.DELETE("/:uid", handleDeleteSubscription(store))
		group.POST("/:uid/link", handleLinkChannel(store))
		group.POST("/:uid/unlink", handleUnlinkChannel(store))
		group.POST("/:uid/refresh", handleRefreshSubscription(store, refreshWorker, syncService))
	}
}

// ─── Handler 实现 ──────────────────────────────────────────────────────────────

// handleListSubscriptions GET /api/subscriptions
func handleListSubscriptions(store *SubscriptionStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		all := store.ListAll()
		items := make([]SubscriptionItem, 0, len(all))
		for _, p := range all {
			items = append(items, toSubscriptionItem(p))
		}

		c.JSON(http.StatusOK, SubscriptionsListResponse{
			Subscriptions: items,
			Total:         len(items),
		})
	}
}

// handleCreateSubscription POST /api/subscriptions
func handleCreateSubscription(store *SubscriptionStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req SubscriptionCreateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数无效: " + err.Error()})
			return
		}

		profile := &SubscriptionProfile{
			SubscriptionUID:  req.SubscriptionUID,
			DisplayName:      req.DisplayName,
			Provider:         req.Provider,
			OriginType:       req.OriginType,
			OriginTier:       req.OriginTier,
			BillingMode:      req.BillingMode,
			Currency:         req.Currency,
			Balance:          req.Balance,
			GroupMultipliers: req.GroupMultipliers,
			Notes:            req.Notes,
			Source:           req.Source,
			// Phase 4 Item 6
			BillingAPIKey:      req.BillingAPIKey,
			AutoRefreshEnabled: req.AutoRefreshEnabled,
		}
		if profile.Source == "" {
			profile.Source = "manual"
		}

		if err := store.Create(profile); err != nil {
			if strings.Contains(err.Error(), "已存在") {
				c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			}
			return
		}

		c.JSON(http.StatusCreated, toSubscriptionItem(profile))
	}
}

// handleGetSubscription GET /api/subscriptions/:uid
func handleGetSubscription(store *SubscriptionStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := c.Param("uid")
		if uid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "uid 不能为空"})
			return
		}

		p := store.Get(uid)
		if p == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "订阅不存在: " + uid})
			return
		}

		c.JSON(http.StatusOK, toSubscriptionItem(p))
	}
}

// handleUpdateSubscription PUT /api/subscriptions/:uid
func handleUpdateSubscription(store *SubscriptionStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := c.Param("uid")
		if uid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "uid 不能为空"})
			return
		}

		existing := store.Get(uid)
		if existing == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "订阅不存在: " + uid})
			return
		}

		var req SubscriptionUpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数无效: " + err.Error()})
			return
		}

		err := store.Patch(uid, req.ExpectedVersion, func(profile *SubscriptionProfile) error {
			if req.DisplayName != nil {
				profile.DisplayName = *req.DisplayName
			}
			if req.Provider != nil {
				profile.Provider = *req.Provider
			}
			if req.OriginType != nil {
				profile.OriginType = *req.OriginType
			}
			if req.OriginTier != nil {
				profile.OriginTier = *req.OriginTier
			}
			if req.BillingMode != nil {
				profile.BillingMode = *req.BillingMode
			}
			if req.Currency != nil {
				profile.Currency = *req.Currency
			}
			if req.Balance != nil {
				profile.Balance = *req.Balance
			}
			if req.GroupMultipliers != nil {
				profile.GroupMultipliers = req.GroupMultipliers
			}
			if req.Notes != nil {
				profile.Notes = *req.Notes
			}
			if req.Source != nil {
				profile.Source = *req.Source
			}
			if req.Confidence != nil {
				profile.Confidence = *req.Confidence
			}
			// Phase 4 Item 6：余额自动刷新字段
			if req.BillingAPIKey != nil {
				profile.BillingAPIKey = *req.BillingAPIKey
			}
			if req.AutoRefreshEnabled != nil {
				profile.AutoRefreshEnabled = *req.AutoRefreshEnabled
			}
			return nil
		})
		if err != nil {
			if isSubscriptionVersionConflict(err) {
				c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
				return
			}
			if isSubscriptionNotFound(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		existing = store.Get(uid)

		c.JSON(http.StatusOK, toSubscriptionItem(existing))
	}
}

// handleDeleteSubscription DELETE /api/subscriptions/:uid
func handleDeleteSubscription(store *SubscriptionStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := c.Param("uid")
		if uid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "uid 不能为空"})
			return
		}

		if err := store.Delete(uid); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.Status(http.StatusNoContent)
	}
}

// handleLinkChannel POST /api/subscriptions/:uid/link
func handleLinkChannel(store *SubscriptionStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := c.Param("uid")
		if uid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "uid 不能为空"})
			return
		}

		var req LinkRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数无效: " + err.Error()})
			return
		}

		if err := store.LinkChannel(uid, req.ChannelUID); err != nil {
			if strings.Contains(err.Error(), "不存在") {
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			}
			return
		}

		p := store.Get(uid)
		c.JSON(http.StatusOK, toSubscriptionItem(p))
	}
}

// handleUnlinkChannel POST /api/subscriptions/:uid/unlink
func handleUnlinkChannel(store *SubscriptionStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := c.Param("uid")
		if uid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "uid 不能为空"})
			return
		}

		var req UnlinkRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数无效: " + err.Error()})
			return
		}

		if err := store.UnlinkChannel(uid, req.ChannelUID); err != nil {
			if strings.Contains(err.Error(), "不存在") {
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			}
			return
		}

		p := store.Get(uid)
		c.JSON(http.StatusOK, toSubscriptionItem(p))
	}
}

// handleRefreshSubscription POST /api/subscriptions/:uid/refresh
// 手动触发指定订阅的余额刷新（不消耗全局每日预算）。
// 需要 BillingAPIKey 非空且 Provider 在白名单内。
func handleRefreshSubscription(store *SubscriptionStore, refreshWorker *SubscriptionRefreshWorker, syncServices ...*NewApiSubscriptionSyncService) gin.HandlerFunc {
	var syncService *NewApiSubscriptionSyncService
	if len(syncServices) > 0 {
		syncService = syncServices[0]
	}
	return func(c *gin.Context) {
		uid := c.Param("uid")
		if uid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "uid 不能为空"})
			return
		}

		profile := store.Get(uid)
		if profile == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "订阅不存在: " + uid})
			return
		}

		if profile.Provider == "new_api" {
			if syncService == nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "new-api 同步服务未就绪"})
				return
			}
			result, err := syncService.SyncNow(c.Request.Context(), uid)
			if err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "refreshResult": result})
				return
			}
			c.JSON(http.StatusOK, gin.H{"subscription": toSubscriptionItem(store.Get(uid)), "refreshResult": result})
			return
		}

		if profile.BillingAPIKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "未配置 BillingAPIKey，无法刷新余额"})
			return
		}

		if !IsAutoRefreshSupported(profile.Provider) {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("provider=%s 不支持自动余额刷新", profile.Provider)})
			return
		}

		// 使用 worker 的 fetcher 直接查询（不消耗预算）
		if refreshWorker == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "余额刷新服务未就绪"})
			return
		}

		// 直接调用 worker 的内部方法——构造临时 fetch 并回写
		profilePtr := store.Get(uid) // 重新获取最新副本
		if profilePtr == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "订阅不存在: " + uid})
			return
		}
		result := refreshWorker.fetchBalance(profilePtr)
		refreshWorker.applyResult(result)

		// 返回更新后的订阅
		updated := store.Get(uid)
		if updated == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "刷新后无法读取订阅"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"subscription": toSubscriptionItem(updated),
			"refreshResult": gin.H{
				"success":      result.Success,
				"balance":      result.Balance,
				"currency":     result.Currency,
				"errorMessage": result.ErrorMessage,
			},
		})
	}
}

// ─── 内部辅助 ──────────────────────────────────────────────────────────────────

// toSubscriptionItem 将 SubscriptionProfile 转为 API 响应结构。
func toSubscriptionItem(p *SubscriptionProfile) SubscriptionItem {
	item := SubscriptionItem{
		SubscriptionUID:   p.SubscriptionUID,
		DisplayName:       p.DisplayName,
		Provider:          p.Provider,
		OriginType:        p.OriginType,
		OriginTier:        p.OriginTier,
		BillingMode:       p.BillingMode,
		Currency:          p.Currency,
		Balance:           p.Balance,
		UsedQuota:         p.UsedQuota,
		GroupMultipliers:  p.GroupMultipliers,
		LinkedChannelUIDs: p.LinkedChannelUIDs,
		Source:            p.Source,
		Confidence:        p.Confidence,
		Notes:             p.Notes,
		CreatedAt:         p.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:         p.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		// Phase 4 Item 6：余额自动刷新
		BillingAPIKey:           p.BillingAPIKey,
		AutoRefreshEnabled:      p.AutoRefreshEnabled,
		AutoRefreshSupported:    IsAutoRefreshSupported(p.Provider),
		LastBalanceRefreshError: p.LastBalanceRefreshError,

		// §8.5.1：new-api 订阅集成——AccessToken 绝不完整出响应，仅脱敏展示
		BaseURL:             p.BaseURL,
		AccessTokenMasked:   maskAccessToken(p.AccessToken),
		UserID:              p.UserID,
		Username:            p.Username,
		AuthTokenMode:       p.AuthTokenMode,
		ProvisionKeyName:    p.ProvisionKeyName,
		ProvisionGroup:      p.ProvisionGroup,
		ProvisionGroupRatio: p.ProvisionGroupRatio,
		MaxGroupMultiplier:  p.MaxGroupMultiplier,
		ProvisionModels:     p.ProvisionModels,
		ProvisionedTokenID:  p.ProvisionedTokenID,
		ProvisionedKeys:     append([]NewApiProvisionedKey(nil), p.ProvisionedKeys...),
		AvailableModels:     p.AvailableModels,
		ProxyURL:            p.ProxyURL,
		ProxyPreferDirect:   p.ProxyPreferDirect,
	}
	if p.ArchivedAt != nil {
		item.ArchivedAt = p.ArchivedAt.Format("2006-01-02T15:04:05Z07:00")
	}
	if p.LastBalanceRefreshAt != nil {
		item.LastBalanceRefreshAt = p.LastBalanceRefreshAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return item
}

// maskAccessToken 对 new-api 访问令牌脱敏：只显示尾部 4 位，其余用 "****" 替代。
// 空令牌返回空字符串（不出现在响应里，字段有 omitempty）。
func maskAccessToken(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 4 {
		return "****"
	}
	return "****" + token[len(token)-4:]
}
