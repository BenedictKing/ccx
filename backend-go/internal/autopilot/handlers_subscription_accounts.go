package autopilot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var errAccountMissing = errors.New("subscription account not found")

// NewApiAccountCreateRequest POST /api/subscriptions/:uid/accounts 请求体。
type NewApiAccountCreateRequest struct {
	AccessToken                string   `json:"accessToken" binding:"required"`
	UserID                     string   `json:"userId,omitempty"`
	DisplayName                string   `json:"displayName,omitempty"`
	AuthTokenMode              string   `json:"authTokenMode,omitempty"`
	ProvisionModels            []string `json:"provisionModels,omitempty"`
	MaxGroupMultiplier         *float64 `json:"maxGroupMultiplier,omitempty"`
	ProvisionAllEligibleGroups bool     `json:"provisionAllEligibleGroups,omitempty"`
	// ProxyURL 账号级代理覆盖，空=继承订阅级代理设置。
	ProxyURL string `json:"proxyUrl,omitempty"`
	// ProxyPreferDirect 直连优先，仅在代理生效时有意义。
	ProxyPreferDirect bool `json:"proxyPreferDirect,omitempty"`
}

// resolveNewApiAccountProxy 解析账号生效的代理设置：账号级优先，缺省继承调用方
// 传入的生效值（渠道级"代理通道"优先，回退订阅级）。
func resolveNewApiAccountProxy(account NewApiAccount, fallbackURL string, fallbackDirect bool) (string, bool) {
	if trimmed := strings.TrimSpace(account.ProxyURL); trimmed != "" {
		return trimmed, account.ProxyPreferDirect
	}
	return fallbackURL, fallbackDirect
}

// adapterForAccount 按账号生效代理构造适配器：账号级覆盖优先，
// 缺省继承渠道级"代理通道"，关联渠道均未配置时回退订阅级。
func adapterForAccount(deps *NewApiRouteDeps, profile *SubscriptionProfile, account NewApiAccount) *NewApiAdapter {
	fallbackURL, fallbackDirect := profile.ProxyURL, profile.ProxyPreferDirect
	if deps != nil && deps.SyncService != nil {
		fallbackURL, fallbackDirect = deps.SyncService.effectiveProxyFor(profile)
	}
	proxyURL, preferDirect := resolveNewApiAccountProxy(account, fallbackURL, fallbackDirect)
	return NewApiAdapterForProxy(proxyURL, preferDirect)
}

// NewApiCredentialsUpdateRequest PATCH /api/subscriptions/:uid/newapi-credentials 请求体。
// 指针字段用于区分“不修改”与显式提交；accessToken 只接收不回显。
type NewApiCredentialsUpdateRequest struct {
	AccessToken       *string `json:"accessToken,omitempty"`
	UserID            *string `json:"userId,omitempty"`
	AuthTokenMode     *string `json:"authTokenMode,omitempty"`
	ProxyURL          *string `json:"proxyUrl,omitempty"`
	ProxyPreferDirect *bool   `json:"proxyPreferDirect,omitempty"`
	ExpectedVersion   *uint64 `json:"expectedVersion,omitempty"`
}

// NewApiAccountItem 账号列表响应单条（脱敏）。
type NewApiAccountItem struct {
	AccountUID        string                 `json:"accountUid"`
	UserID            string                 `json:"userId,omitempty"`
	AuthTokenMode     string                 `json:"authTokenMode,omitempty"`
	DisplayName       string                 `json:"displayName,omitempty"`
	Balance           float64                `json:"balance,omitempty"`
	Status            string                 `json:"status,omitempty"`
	AccessTokenMasked string                 `json:"accessTokenMasked,omitempty"`
	ProvisionedKeys   []NewApiProvisionedKey `json:"provisionedKeys,omitempty"`
	LastSyncError     string                 `json:"lastSyncError,omitempty"`
	LastCheckedAt     time.Time              `json:"lastCheckedAt,omitempty"`
	CreatedAt         time.Time              `json:"createdAt"`
	// 账号级代理设置（非敏感配置，空表示继承订阅级）
	ProxyURL          string `json:"proxyUrl,omitempty"`
	ProxyPreferDirect bool   `json:"proxyPreferDirect,omitempty"`
}

// NewApiAccountListResponse GET /api/subscriptions/:uid/accounts 响应体。
type NewApiAccountListResponse struct {
	Accounts []NewApiAccountItem `json:"accounts"`
}

// handleAddSubscriptionAccount 为已有 new-api 订阅添加新账号，并按订阅级倍率上限为其合格分组
// 自动建代理 key 并入关联渠道，使多账号共同分担流量/额度。建 key 失败时回滚远端 key，不留下半成品账号。
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

		// 与主 provision 共用订阅 uid 锁：保证同一订阅的添加账号与 provision 串行。
		// 慢速上游 HTTP 与 store 写在锁内完成（同 uid 串行化），避免与 provision 交叉。
		var lock *sync.Mutex
		if deps != nil && deps.SyncService != nil {
			lock = deps.SyncService.LockForUID(uid)
		} else {
			lock = lockForKeyFrom(&newAPIProvisionUIDLocksMu, newAPIProvisionUIDLocks, uid)
		}
		lock.Lock()
		defer lock.Unlock()

		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		defer cancel()

		// 代理解析：请求显式携带优先，缺省继承渠道级代理通道（回退订阅级设置）
		proxyURL := strings.TrimSpace(req.ProxyURL)
		proxyPreferDirect := req.ProxyPreferDirect
		if proxyURL == "" {
			if deps != nil && deps.SyncService != nil {
				proxyURL, proxyPreferDirect = deps.SyncService.effectiveProxyFor(profile)
			} else {
				proxyURL, proxyPreferDirect = profile.ProxyURL, profile.ProxyPreferDirect
			}
		}
		adapter := NewApiAdapterForProxy(proxyURL, proxyPreferDirect)
		self, derivedUserID, err := adapter.VerifyWithFallback(ctx, profile.BaseURL, req.AccessToken, req.UserID, req.AuthTokenMode)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("账号验证失败: %v", err)})
			return
		}

		accountUID := fmt.Sprintf("acct_%d", time.Now().UnixNano())

		// 为该账号建 key：沿用订阅级倍率上限与模型白名单。建 key 失败即整个添加失败并回收已建 key。
		var accountKeys []NewApiProvisionedKey
		if deps.CfgManager != nil {
			groups, gErr := adapter.FetchGroups(ctx, profile.BaseURL, req.AccessToken, derivedUserID, req.AuthTokenMode)
			if gErr != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("无法获取该账号分组倍率，已阻止建 key: %v", gErr)})
				return
			}
			maxGroupMultiplier := req.MaxGroupMultiplier
			if maxGroupMultiplier == nil {
				maxGroupMultiplier = profile.MaxGroupMultiplier
			}
			resolved, rErr := resolveNewApiProvisionGroups(groups, "", req.ProvisionAllEligibleGroups, maxGroupMultiplier)
			if rErr != nil {
				c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "分组倍率校验失败: " + rErr.Error()})
				return
			}
			provisionModels := req.ProvisionModels
			if len(provisionModels) == 0 {
				provisionModels = profile.ProvisionModels
			}
			provisionReq := NewApiProvisionRequest{
				BaseURL:                    profile.BaseURL,
				AccessToken:                req.AccessToken,
				UserID:                     req.UserID,
				AuthTokenMode:              req.AuthTokenMode,
				ProvisionAllEligibleGroups: true,
				ProvisionModels:            provisionModels,
				MaxGroupMultiplier:         maxGroupMultiplier,
			}
			// key 名加账号前缀，避免与主账号/其他账号在同站点下的同名 key 被误复用。
			namePrefix := accountUID + "-"
			provisioned, pErr := provisionNewApiGroupKeys(ctx, adapter, provisionReq, derivedUserID, resolved, namePrefix)
			if pErr != nil {
				var conflict *newApiProvisionConflictError
				if errors.As(pErr, &conflict) {
					c.JSON(http.StatusConflict, gin.H{"error": "建 key 失败: " + conflict.Error()})
					return
				}
				var keyConflict *NewApiProvisionKeyConflictError
				if errors.As(pErr, &keyConflict) {
					c.JSON(http.StatusConflict, gin.H{"error": "建 key 失败: " + keyConflict.Error()})
					return
				}
				c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("建 key 失败: %v", pErr)})
				return
			}
			accountKeys = make([]NewApiProvisionedKey, 0, len(provisioned))
			plaintextByToken := make(map[int64]string, len(provisioned))
			for _, key := range provisioned {
				accountKeys = append(accountKeys, key.NewApiProvisionedKey)
				plaintextByToken[int64(key.TokenID)] = key.Key
			}

			account := NewApiAccount{
				AccountUID:        accountUID,
				AccessToken:       req.AccessToken,
				UserID:            derivedUserID,
				AuthTokenMode:     req.AuthTokenMode,
				ProxyURL:          strings.TrimSpace(req.ProxyURL),
				ProxyPreferDirect: req.ProxyPreferDirect,
				DisplayName:       req.DisplayName,
				Balance:           float64(self.Quota),
				Status:            "active",
				ProvisionedKeys:   accountKeys,
				LastCheckedAt:     time.Now(),
				CreatedAt:         time.Now(),
			}
			if err := deps.Store.AddAccount(uid, account); err != nil {
				cleanupNewApiProvisionedKeys(ctx, adapter, provisionReq, derivedUserID, provisioned)
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}

			// 把账号 key 明文注入关联渠道。失败时删除账号并回收远端 key。
			if deps.SyncService != nil {
				fresh := deps.Store.Get(uid)
				if rErr := deps.SyncService.ReconcileAccountProvisioned(fresh, accountUID, groups, plaintextByToken); rErr != nil {
					_ = deps.Store.RemoveAccount(uid, accountUID)
					cleanupNewApiProvisionedKeys(ctx, adapter, provisionReq, derivedUserID, provisioned)
					c.JSON(http.StatusConflict, gin.H{"error": "同步 key 到渠道失败: " + rErr.Error()})
					return
				}
			}

			if deps.Runner != nil {
				for _, chUID := range profile.LinkedChannelUIDs {
					_, _, channel, ok := findNewApiChannel(deps.CfgManager, chUID)
					if !ok {
						continue
					}
					ch := channel
					deps.Runner.TriggerDiscovery(chUID, &ch, deps.CfgManager)
				}
			}

			c.JSON(http.StatusCreated, NewApiAccountItem{
				AccountUID:        account.AccountUID,
				UserID:            account.UserID,
				AuthTokenMode:     account.AuthTokenMode,
				DisplayName:       account.DisplayName,
				Balance:           account.Balance,
				Status:            account.Status,
				AccessTokenMasked: maskAccessToken(account.AccessToken),
				ProvisionedKeys:   account.ProvisionedKeys,
				LastCheckedAt:     account.LastCheckedAt,
				CreatedAt:         account.CreatedAt,
				ProxyURL:          account.ProxyURL,
				ProxyPreferDirect: account.ProxyPreferDirect,
			})
			return
		}

		// 无 CfgManager（仅 Store 注入，如部分测试）：退化为只登记账号不建 key。
		account := NewApiAccount{
			AccountUID:    accountUID,
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
		c.JSON(http.StatusCreated, NewApiAccountItem{
			AccountUID:        account.AccountUID,
			UserID:            account.UserID,
			AuthTokenMode:     account.AuthTokenMode,
			DisplayName:       account.DisplayName,
			Balance:           account.Balance,
			Status:            account.Status,
			AccessTokenMasked: maskAccessToken(account.AccessToken),
			LastCheckedAt:     account.LastCheckedAt,
			CreatedAt:         account.CreatedAt,
			ProxyURL:          account.ProxyURL,
			ProxyPreferDirect: account.ProxyPreferDirect,
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
				AuthTokenMode:     acc.AuthTokenMode,
				DisplayName:       acc.DisplayName,
				Balance:           acc.Balance,
				Status:            acc.Status,
				AccessTokenMasked: maskAccessToken(acc.AccessToken),
				ProvisionedKeys:   acc.ProvisionedKeys,
				LastSyncError:     acc.LastSyncError,
				LastCheckedAt:     acc.LastCheckedAt,
				CreatedAt:         acc.CreatedAt,
				ProxyURL:          acc.ProxyURL,
				ProxyPreferDirect: acc.ProxyPreferDirect,
			})
		}

		c.JSON(http.StatusOK, NewApiAccountListResponse{Accounts: items})
	}
}

// handleDeleteSubscriptionAccount 删除指定账号：先从关联渠道剔除该账号的 key，再 best-effort 回收远端 key，
// 最后从订阅移除账号。渠道剔除失败不阻断删除，避免残留远端 key 阻塞账号移除。
func handleDeleteSubscriptionAccount(deps *NewApiRouteDeps) gin.HandlerFunc {
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

		// 1) 从关联渠道剔除该账号的 key（返回被移除的 tokenID，供远端回收）。
		var removedTokenIDs map[int64]struct{}
		if deps.SyncService != nil {
			removedTokenIDs = deps.SyncService.RemoveAccountKeysFromChannels(profile, *account)
		}

		// 2) best-effort 回收远端 key。失败仅记录日志，不阻断删除。
		if len(removedTokenIDs) > 0 {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
			defer cancel()
			adapter := adapterForAccount(deps, profile, *account)
			for tokenID := range removedTokenIDs {
				if err := adapter.DeleteToken(ctx, profile.BaseURL, account.AccessToken, account.UserID, account.AuthTokenMode, int(tokenID)); err != nil {
					log.Printf("[NewApi-Account] 回收远端 key 失败 account=%s tokenID=%d: %v", accountUID, tokenID, err)
				}
			}
		}

		// 3) 移除账号。
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
		adapter := adapterForAccount(deps, profile, account)
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
		c.JSON(http.StatusOK, NewApiAccountItem{AccountUID: account.AccountUID, UserID: account.UserID, DisplayName: account.DisplayName, Balance: account.Balance, Status: account.Status, AccessTokenMasked: maskAccessToken(account.AccessToken), ProvisionedKeys: account.ProvisionedKeys, LastSyncError: account.LastSyncError, LastCheckedAt: account.LastCheckedAt, CreatedAt: account.CreatedAt, ProxyURL: account.ProxyURL, ProxyPreferDirect: account.ProxyPreferDirect})
	}
}

// RegisterSubscriptionAccountRoutes 注册 new-api 多账号管理路由。
// NewApiAccountCredentialsUpdateRequest PATCH /accounts/:accountUid/credentials 请求体。
// 代理不在账号级维护（渠道"代理通道"是唯一事实源），故无 proxy 字段。
type NewApiAccountCredentialsUpdateRequest struct {
	AccessToken   *string `json:"accessToken,omitempty"`
	UserID        *string `json:"userId,omitempty"`
	AuthTokenMode *string `json:"authTokenMode,omitempty"`
}

// handleUpdateSubscriptionAccountCredentials 更新子账号凭证，落库前先用新凭证组合
// 调站点验证（对齐主账号 handleUpdateNewApiCredentials 语义），通过后回填余额。
func handleUpdateSubscriptionAccountCredentials(deps *NewApiRouteDeps) gin.HandlerFunc {
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

		var req NewApiAccountCredentialsUpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数无效: " + err.Error()})
			return
		}
		if req.AccessToken == nil && req.UserID == nil && req.AuthTokenMode == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "至少需要提交一个凭证字段"})
			return
		}

		accessToken := account.AccessToken
		if req.AccessToken != nil {
			accessToken = strings.TrimSpace(*req.AccessToken)
			if accessToken == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "accessToken 不能为空"})
				return
			}
		}
		userID := account.UserID
		if req.UserID != nil {
			userID = strings.TrimSpace(*req.UserID)
		}
		authTokenMode := account.AuthTokenMode
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

		merged := *account
		merged.AccessToken, merged.UserID, merged.AuthTokenMode = accessToken, userID, authTokenMode
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		adapter := adapterForAccount(deps, profile, merged)
		self, derivedUserID, err := adapter.VerifyWithFallback(ctx, profile.BaseURL, accessToken, userID, authTokenMode)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("账号验证失败: %v", err)})
			return
		}

		now := time.Now()
		updated := merged
		patchErr := deps.Store.Patch(uid, nil, func(current *SubscriptionProfile) error {
			for i := range current.Accounts {
				if current.Accounts[i].AccountUID != accountUID {
					continue
				}
				current.Accounts[i].AccessToken = accessToken
				current.Accounts[i].UserID = derivedUserID
				current.Accounts[i].AuthTokenMode = authTokenMode
				current.Accounts[i].Balance = float64(self.Quota)
				current.Accounts[i].Status = "active"
				current.Accounts[i].LastCheckedAt = now
				updated = current.Accounts[i]
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

		// 凭证已通过校验；分组/倍率同步失败不回滚凭证，由账号快照 lastSyncError 展示。
		if deps.SyncService != nil {
			_, _ = deps.SyncService.SyncNow(c.Request.Context(), uid)
		}
		c.JSON(http.StatusOK, NewApiAccountItem{
			AccountUID:        updated.AccountUID,
			UserID:            updated.UserID,
			AuthTokenMode:     updated.AuthTokenMode,
			DisplayName:       updated.DisplayName,
			Balance:           updated.Balance,
			Status:            updated.Status,
			AccessTokenMasked: maskAccessToken(updated.AccessToken),
			ProvisionedKeys:   updated.ProvisionedKeys,
			LastSyncError:     updated.LastSyncError,
			LastCheckedAt:     updated.LastCheckedAt,
			CreatedAt:         updated.CreatedAt,
			ProxyURL:          updated.ProxyURL,
			ProxyPreferDirect: updated.ProxyPreferDirect,
		})
	}
}

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
		// 代理按生效值验证：请求显式提交优先，否则沿用订阅现有设置
		proxyURL := profile.ProxyURL
		if req.ProxyURL != nil {
			proxyURL = strings.TrimSpace(*req.ProxyURL)
		}
		proxyPreferDirect := profile.ProxyPreferDirect
		if req.ProxyPreferDirect != nil {
			proxyPreferDirect = *req.ProxyPreferDirect
		}
		adapter := NewApiAdapterForProxy(proxyURL, proxyPreferDirect)
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
			if req.ProxyURL != nil {
				current.ProxyURL = strings.TrimSpace(*req.ProxyURL)
			}
			if req.ProxyPreferDirect != nil {
				current.ProxyPreferDirect = *req.ProxyPreferDirect
			}
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
	group.PATCH("/:accountUid/credentials", handleUpdateSubscriptionAccountCredentials(deps))
}
