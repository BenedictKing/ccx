package autopilot

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// NewApiAccountCreateRequest POST /api/subscriptions/:uid/accounts 请求体。
type NewApiAccountCreateRequest struct {
	AccessToken   string `json:"accessToken" binding:"required"`
	UserID        string `json:"userId,omitempty"`
	DisplayName   string `json:"displayName,omitempty"`
	AuthTokenMode string `json:"authTokenMode,omitempty"`
}

// NewApiAccountItem 账号列表响应单条（脱敏）。
type NewApiAccountItem struct {
	AccountUID        string    `json:"accountUid"`
	UserID            string    `json:"userId,omitempty"`
	DisplayName       string    `json:"displayName,omitempty"`
	Balance           float64   `json:"balance,omitempty"`
	Status            string    `json:"status,omitempty"`
	AccessTokenMasked string    `json:"accessTokenMasked,omitempty"`
	LastCheckedAt     time.Time `json:"lastCheckedAt,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
}

// NewApiAccountListResponse GET /api/subscriptions/:uid/accounts 响应体。
type NewApiAccountListResponse struct {
	Accounts []NewApiAccountItem `json:"accounts"`
}

// handleAddSubscriptionAccount 为已有 new-api 订阅添加新账号。
func handleAddSubscriptionAccount(deps *NewApiRouteDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := c.Param("uid")
		if uid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "subscription uid 不能为空"})
			return
		}

		profile := deps.Store.Get(uid)
		if profile == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("subscription_uid=%s 不存在", uid)})
			return
		}
		if profile.Provider != "new_api" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "仅 new_api 类型订阅支持多账号"})
			return
		}

		var req NewApiAccountCreateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数无效: " + err.Error()})
			return
		}
		if req.AccessToken == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "accessToken 必填"})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()

		adapter := &NewApiAdapter{}
		self, derivedUserID, err := adapter.VerifyWithFallback(ctx, profile.BaseURL, req.AccessToken, req.UserID, req.AuthTokenMode)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("账号验证失败: %v", err)})
			return
		}

		account := NewApiAccount{
			AccountUID:    fmt.Sprintf("acct_%d", time.Now().UnixNano()),
			AccessToken:   req.AccessToken,
			UserID:        derivedUserID,
			DisplayName:   req.DisplayName,
			Balance:       float64(self.Quota),
			Status:        "active",
			LastCheckedAt: time.Now(),
			CreatedAt:     time.Now(),
		}

		if err := deps.Store.AddAccount(uid, account); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusCreated, NewApiAccountItem{
			AccountUID:        account.AccountUID,
			UserID:            account.UserID,
			DisplayName:       account.DisplayName,
			Balance:           account.Balance,
			Status:            account.Status,
			AccessTokenMasked: maskAccessToken(account.AccessToken),
			LastCheckedAt:     account.LastCheckedAt,
			CreatedAt:         account.CreatedAt,
		})
	}
}

// handleListSubscriptionAccounts 获取订阅下的账号列表（脱敏）。
func handleListSubscriptionAccounts(deps *NewApiRouteDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := c.Param("uid")
		if uid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "subscription uid 不能为空"})
			return
		}

		profile := deps.Store.Get(uid)
		if profile == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("subscription_uid=%s 不存在", uid)})
			return
		}

		items := make([]NewApiAccountItem, 0, len(profile.Accounts))
		for _, acc := range profile.Accounts {
			items = append(items, NewApiAccountItem{
				AccountUID:        acc.AccountUID,
				UserID:            acc.UserID,
				DisplayName:       acc.DisplayName,
				Balance:           acc.Balance,
				Status:            acc.Status,
				AccessTokenMasked: maskAccessToken(acc.AccessToken),
				LastCheckedAt:     acc.LastCheckedAt,
				CreatedAt:         acc.CreatedAt,
			})
		}

		c.JSON(http.StatusOK, NewApiAccountListResponse{Accounts: items})
	}
}

// handleDeleteSubscriptionAccount 删除指定账号。
func handleDeleteSubscriptionAccount(deps *NewApiRouteDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := c.Param("uid")
		accountUID := c.Param("accountUid")
		if uid == "" || accountUID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "subscription uid 和 account uid 不能为空"})
			return
		}

		if err := deps.Store.RemoveAccount(uid, accountUID); err != nil {
			if strings.Contains(err.Error(), "不存在") {
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{"success": true})
	}
}

// handleRefreshSubscriptionAccount 刷新单个账号余额。
func handleRefreshSubscriptionAccount(deps *NewApiRouteDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := c.Param("uid")
		accountUID := c.Param("accountUid")
		if uid == "" || accountUID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "subscription uid 和 account uid 不能为空"})
			return
		}

		profile := deps.Store.Get(uid)
		if profile == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("subscription_uid=%s 不存在", uid)})
			return
		}

		var account *NewApiAccount
		for i := range profile.Accounts {
			if profile.Accounts[i].AccountUID == accountUID {
				account = &profile.Accounts[i]
				break
			}
		}
		if account == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("account_uid=%s 不存在", accountUID)})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()

		adapter := &NewApiAdapter{}
		balance, _, err := adapter.FetchBalance(ctx, profile.BaseURL, account.AccessToken, account.UserID, account.UserID)
		if err != nil {
			account.Status = "error"
			account.LastCheckedAt = time.Now()
			_ = deps.Store.Update(profile)
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("刷新余额失败: %v", err)})
			return
		}

		account.Balance = balance
		account.Status = "active"
		account.LastCheckedAt = time.Now()

		if err := deps.Store.Update(profile); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, NewApiAccountItem{
			AccountUID:        account.AccountUID,
			UserID:            account.UserID,
			DisplayName:       account.DisplayName,
			Balance:           account.Balance,
			Status:            account.Status,
			AccessTokenMasked: maskAccessToken(account.AccessToken),
			LastCheckedAt:     account.LastCheckedAt,
			CreatedAt:         account.CreatedAt,
		})
	}
}

// RegisterSubscriptionAccountRoutes 注册 new-api 多账号管理路由。
func RegisterSubscriptionAccountRoutes(router gin.IRouter, deps *NewApiRouteDeps) {
	if deps == nil || deps.Store == nil {
		return
	}
	group := router.Group("/subscriptions/:uid/accounts")
	group.POST("", handleAddSubscriptionAccount(deps))
	group.GET("", handleListSubscriptionAccounts(deps))
	group.DELETE("/:accountUid", handleDeleteSubscriptionAccount(deps))
	group.POST("/:accountUid/refresh", handleRefreshSubscriptionAccount(deps))
}
