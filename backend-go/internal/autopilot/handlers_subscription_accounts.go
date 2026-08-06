package autopilot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

var errAccountMissing = errors.New("subscription account not found")

// NewApiAccountCreateRequest POST /api/subscriptions/:uid/accounts 请求体。
type NewApiAccountCreateRequest struct {
	AccessToken   string `json:"accessToken" binding:"required"`
	UserID        string `json:"userId,omitempty"`
	DisplayName   string `json:"displayName,omitempty"`
	AuthTokenMode string `json:"authTokenMode,omitempty"`
}

// NewApiCredentialsUpdateRequest PATCH /api/subscriptions/:uid/newapi-credentials 请求体。
// 指针字段用于区分“不修改”与显式提交；accessToken 只接收不回显。
type NewApiCredentialsUpdateRequest struct {
	AccessToken     *string `json:"accessToken,omitempty"`
	UserID          *string `json:"userId,omitempty"`
	AuthTokenMode   *string `json:"authTokenMode,omitempty"`
	ExpectedVersion *uint64 `json:"expectedVersion,omitempty"`
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
			AuthTokenMode: req.AuthTokenMode,
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

		if deps.SyncService != nil {
			_, _ = deps.SyncService.SyncNow(c.Request.Context(), uid)
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
		uid, accountUID := c.Param("uid"), c.Param("accountUid")
		if uid == "" || accountUID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "subscription uid 和 account uid 不能为空"})
			return
		}
		profile := deps.Store.Get(uid)
		if profile == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("subscription_uid=%s 不存在", uid)})
			return
		}
		var account NewApiAccount
		found := false
		for _, candidate := range profile.Accounts {
			if candidate.AccountUID == accountUID {
				account, found = candidate, true
				break
			}
		}
		if !found {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("account_uid=%s 不存在", accountUID)})
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		defer cancel()
		adapter := &NewApiAdapter{}
		balance, _, fetchErr := adapter.FetchBalance(ctx, profile.BaseURL, account.AccessToken, account.UserID, account.AuthTokenMode)
		now := time.Now()
		patchErr := deps.Store.Patch(uid, nil, func(current *SubscriptionProfile) error {
			for i := range current.Accounts {
				if current.Accounts[i].AccountUID != accountUID {
					continue
				}
				current.Accounts[i].LastCheckedAt = now
				if fetchErr != nil {
					current.Accounts[i].Status = "error"
				} else {
					current.Accounts[i].Balance, current.Accounts[i].Status = balance, "active"
				}
				account = current.Accounts[i]
				return nil
			}
			return errAccountMissing
		})
		if patchErr != nil {
			if errors.Is(patchErr, errAccountMissing) || strings.Contains(patchErr.Error(), "不存在") {
				c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("account_uid=%s 不存在", accountUID)})
				return
			}
			if strings.Contains(patchErr.Error(), "version 冲突") {
				c.JSON(http.StatusConflict, gin.H{"error": patchErr.Error()})
				return
			}
			if strings.Contains(patchErr.Error(), "subscription_uid") {
				c.JSON(http.StatusNotFound, gin.H{"error": patchErr.Error()})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": patchErr.Error()})
			return
		}
		if fetchErr != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("刷新余额失败: %v", fetchErr)})
			return
		}
		if deps.SyncService != nil {
			_, _ = deps.SyncService.SyncNow(c.Request.Context(), uid)
		}
		c.JSON(http.StatusOK, NewApiAccountItem{AccountUID: account.AccountUID, UserID: account.UserID, DisplayName: account.DisplayName, Balance: account.Balance, Status: account.Status, AccessTokenMasked: maskAccessToken(account.AccessToken), LastCheckedAt: account.LastCheckedAt, CreatedAt: account.CreatedAt})
	}
}

// RegisterSubscriptionAccountRoutes 注册 new-api 多账号管理路由。
// handleUpdateNewApiCredentials 更新 new-api 主账号凭证，并在落库前验证新凭证组合。
func handleUpdateNewApiCredentials(deps *NewApiRouteDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := strings.TrimSpace(c.Param("uid"))
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "仅 new_api 类型订阅支持主账号凭证更新"})
			return
		}

		var req NewApiCredentialsUpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数无效: " + err.Error()})
			return
		}
		if req.AccessToken == nil && req.UserID == nil && req.AuthTokenMode == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "至少需要提交一个凭证字段"})
			return
		}

		accessToken := profile.AccessToken
		if req.AccessToken != nil {
			accessToken = strings.TrimSpace(*req.AccessToken)
			if accessToken == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "accessToken 不能为空"})
				return
			}
		}
		userID := profile.UserID
		if req.UserID != nil {
			userID = strings.TrimSpace(*req.UserID)
		}
		authTokenMode := profile.AuthTokenMode
		if req.AuthTokenMode != nil {
			authTokenMode = strings.ToLower(strings.TrimSpace(*req.AuthTokenMode))
		}
		if authTokenMode == "" {
			authTokenMode = NewApiAuthModeBearer
		}
		if authTokenMode != NewApiAuthModeBearer && authTokenMode != NewApiAuthModeRaw && authTokenMode != NewApiAuthModeRawAuth {
			c.JSON(http.StatusBadRequest, gin.H{"error": "authTokenMode 仅支持 bearer 或 raw"})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		adapter := &NewApiAdapter{}
		self, derivedUserID, err := adapter.VerifyWithFallback(ctx, profile.BaseURL, accessToken, userID, authTokenMode)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("主账号验证失败: %v", err)})
			return
		}

		now := time.Now()
		if err := deps.Store.Patch(uid, req.ExpectedVersion, func(current *SubscriptionProfile) error {
			current.AccessToken = accessToken
			current.UserID = derivedUserID
			current.AuthTokenMode = authTokenMode
			current.Balance = float64(self.Quota)
			current.UsedQuota = self.UsedQuota
			current.LastBalanceRefreshAt = &now
			current.LastBalanceRefreshError = ""
			return nil
		}); err != nil {
			if strings.Contains(err.Error(), "version 冲突") {
				c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
				return
			}
			if strings.Contains(err.Error(), "subscription_uid") {
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// 凭证已通过校验；同步分组/模型与渠道倍率失败时保留凭证，并由订阅快照展示错误供重试。
		if deps.SyncService != nil {
			_, _ = deps.SyncService.SyncNow(c.Request.Context(), uid)
		}
		c.JSON(http.StatusOK, toSubscriptionItem(deps.Store.Get(uid)))
	}
}

func RegisterSubscriptionAccountRoutes(router gin.IRouter, deps *NewApiRouteDeps) {
	if deps == nil || deps.Store == nil {
		return
	}
	router.PATCH("/subscriptions/:uid/newapi-credentials", handleUpdateNewApiCredentials(deps))
	group := router.Group("/subscriptions/:uid/accounts")
	group.POST("", handleAddSubscriptionAccount(deps))
	group.GET("", handleListSubscriptionAccounts(deps))
	group.DELETE("/:accountUid", handleDeleteSubscriptionAccount(deps))
	group.POST("/:accountUid/refresh", handleRefreshSubscriptionAccount(deps))
}
