package siteaccess

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/upstreamprobe"
	"github.com/gin-gonic/gin"
)

// Deps 是站点接入管理端点的依赖注入。
type Deps struct {
	Store      *AccountStore
	CfgManager *config.ConfigManager
	Adapter    *NewApiAdapter
}

// accountResponse 是账号的对外展示形态：管理令牌只给脱敏尾部，
// 密文与明文都不得出现在响应中。
type accountResponse struct {
	UID             string               `json:"uid"`
	SiteType        string               `json:"siteType"`
	BaseURL         string               `json:"baseUrl"`
	UserID          string               `json:"userId,omitempty"`
	AuthTokenMode   string               `json:"authTokenMode,omitempty"`
	CredentialMode  string               `json:"credentialMode,omitempty"`
	MaskedToken     string               `json:"maskedAccessToken,omitempty"`
	CreatedAt       time.Time            `json:"createdAt"`
	UpdatedAt       time.Time            `json:"updatedAt"`
	ProvisionedKeys []ProvisionedKeyMeta `json:"provisionedKeys,omitempty"`
	Evidence        []ChannelEvidence    `json:"evidence,omitempty"`
	LastSyncAt      *time.Time           `json:"lastSyncAt,omitempty"`
	LastSyncError   string               `json:"lastSyncError,omitempty"`
	Plans           []ProvisionRequest   `json:"plans,omitempty"`
}

func toAccountResponse(a SiteAccount) accountResponse {
	return accountResponse{
		UID:             a.UID,
		Plans:           a.Plans,
		CredentialMode:  a.CredentialMode,
		LastSyncAt:      a.LastSyncAt,
		LastSyncError:   a.LastSyncError,
		SiteType:        a.SiteType,
		BaseURL:         a.BaseURL,
		UserID:          a.UserID,
		AuthTokenMode:   a.AuthTokenMode,
		MaskedToken:     a.MaskedAccessToken(),
		CreatedAt:       a.CreatedAt,
		UpdatedAt:       a.UpdatedAt,
		ProvisionedKeys: a.ProvisionedKeys,
		Evidence:        a.Evidence,
	}
}

// RegisterRoutes 注册站点接入管理端点（统一在 /api/siteaccess 下）。
func RegisterRoutes(apiGroup *gin.RouterGroup, deps *Deps) {
	if deps == nil || deps.Store == nil || deps.CfgManager == nil {
		return
	}
	adapter := deps.Adapter
	if adapter == nil {
		adapter = &NewApiAdapter{}
	}

	group := apiGroup.Group("/siteaccess")
	group.GET("/accounts", func(c *gin.Context) {
		accounts, err := deps.Store.List()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		items := make([]accountResponse, 0, len(accounts))
		for _, a := range accounts {
			items = append(items, toAccountResponse(a))
		}
		c.JSON(http.StatusOK, gin.H{"accounts": items})
	})

	group.POST("/accounts", func(c *gin.Context) {
		var req struct {
			UID            string `json:"uid"`
			CredentialMode string `json:"credentialMode"`
			SiteType       string `json:"siteType"`
			BaseURL        string `json:"baseUrl"`
			UserID         string `json:"userId"`
			AuthTokenMode  string `json:"authTokenMode"`
			AccessToken    string `json:"accessToken"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
			return
		}
		candidate := SiteAccount{
			UID:            req.UID,
			CredentialMode: req.CredentialMode,
			SiteType:       req.SiteType,
			BaseURL:        req.BaseURL,
			UserID:         req.UserID,
			AuthTokenMode:  req.AuthTokenMode,
			AccessToken:    req.AccessToken,
		}
		// 新接入优先使用站点确认的用户身份，同一账号换令牌不会再创建副本。
		// 管理面不支持自检时仍允许保存，后续明确展示未验证状态。
		candidate.BaseURL = NormalizeSiteURL(candidate.BaseURL)
		if candidate.UID == "" && candidate.CredentialMode != "key" && candidate.AccessToken != "" {
			if _, derived, err := adapterForAccount(candidate, adapter).VerifyWithFallback(c.Request.Context(), candidate.BaseURL, candidate.AccessToken, candidate.UserID, candidate.AuthTokenMode); err == nil && derived != "" {
				candidate.UserID = derived
			}
		}
		uid, err := deps.Store.Upsert(candidate)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		account, _, _ := deps.Store.Get(uid)
		c.JSON(http.StatusOK, gin.H{"account": toAccountResponse(account)})
	})

	group.DELETE("/accounts/:uid", func(c *gin.Context) {
		if err := deps.Store.Delete(c.Param("uid")); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"deleted": true})
	})

	// 管理面信息验证：成功/失败只影响证据与诊断，不修改渠道状态
	group.POST("/accounts/:uid/verify", func(c *gin.Context) {
		if err := VerifyAccountInfo(c.Request.Context(), deps.Store, adapter, c.Param("uid")); err != nil {
			c.JSON(http.StatusOK, gin.H{"verified": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"verified": true})
	})

	// 协议级接入：每次只为选定协议建渠道
	group.POST("/accounts/:uid/provision", func(c *gin.Context) {
		var req struct {
			Protocol           string            `json:"protocol"`
			Agent              string            `json:"agent"`
			AllowModelFallback bool              `json:"allowModelFallback"`
			MaxAttempts        int               `json:"maxAttempts"`
			MaxWaitSeconds     int               `json:"maxWaitSeconds"`
			ExistingTokenIDs   map[string]int    `json:"existingTokenIds"`
			AutoSyncGroups     bool              `json:"autoSyncGroups"`
			Groups             []string          `json:"groups"`
			ExcludeGroups      []string          `json:"excludeGroups"`
			MaxMultiplier      *float64          `json:"maxMultiplier"`
			Models             []string          `json:"models"`
			ModelMapping       map[string]string `json:"modelMapping"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
			return
		}
		result, err := ProvisionProtocol(c.Request.Context(), deps.Store, deps.CfgManager, adapter, ProvisionRequest{
			AccountUID:         c.Param("uid"),
			Agent:              req.Agent,
			AllowModelFallback: req.AllowModelFallback,
			MaxAttempts:        req.MaxAttempts,
			MaxWaitSeconds:     req.MaxWaitSeconds,
			ExistingTokenIDs:   req.ExistingTokenIDs,
			AutoSyncGroups:     req.AutoSyncGroups,
			Protocol:           req.Protocol,
			Groups:             req.Groups,
			ExcludeGroups:      req.ExcludeGroups,
			MaxMultiplier:      req.MaxMultiplier,
			Models:             req.Models,
			ModelMapping:       req.ModelMapping,
		})
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	})

	// 手动触发对账
	group.POST("/accounts/:uid/reconcile", func(c *gin.Context) {
		actions, err := ReconcileAccount(c.Request.Context(), deps.Store, deps.CfgManager, adapter, c.Param("uid"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if actions == nil {
			actions = []ReconcileAction{}
		}
		c.JSON(http.StatusOK, gin.H{"actions": actions})
	})

	// 用户主动触发的真实推理验证（消耗一次最小真实调用，需显式模型）
	group.POST("/accounts/:uid/verify-request", func(c *gin.Context) {
		var req struct {
			Protocol string `json:"protocol"`
			Group    string `json:"group"`
			Model    string `json:"model"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
			return
		}
		if err := VerifyRealRequest(c.Request.Context(), deps.Store, deps.CfgManager, c.Param("uid"), req.Protocol, req.Group, req.Model); err != nil {
			c.JSON(http.StatusOK, gin.H{"success": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	// 已有渠道绑定/解绑账号：只更新归属与可信倍率，不创建或删除 Key
	group.PUT("/channels/:protocol/:index/binding", func(c *gin.Context) {
		var req ChannelBindingRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
			return
		}
		req.Protocol = c.Param("protocol")
		index, parseErr := strconv.Atoi(c.Param("index"))
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的渠道索引"})
			return
		}
		req.ChannelIndex = index
		var err error
		if strings.TrimSpace(req.AccountUID) == "" {
			err = UnbindExistingChannel(deps.CfgManager, req)
		} else {
			err = BindExistingChannel(c.Request.Context(), deps.Store, deps.CfgManager, adapter, req)
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	group.PUT("/channels/:protocol/:index/key-binding", func(c *gin.Context) {
		var req KeyBindingRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
			return
		}
		index, err := strconv.Atoi(c.Param("index"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的渠道索引"})
			return
		}
		req.Protocol, req.ChannelIndex = c.Param("protocol"), index
		if err := BindExistingKey(c.Request.Context(), deps.Store, deps.CfgManager, adapter, req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	group.POST("/channels/:protocol/:index/key-unbinding", func(c *gin.Context) {
		var req KeyBindingRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
			return
		}
		index, err := strconv.Atoi(c.Param("index"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的渠道索引"})
			return
		}
		req.Protocol, req.ChannelIndex = c.Param("protocol"), index
		if err := UnbindExistingKey(deps.Store, deps.CfgManager, req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	group.POST("/channels/:protocol/:index/group-copy", func(c *gin.Context) {
		var req ChannelGroupCopyRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
			return
		}
		index, err := strconv.Atoi(c.Param("index"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的渠道索引"})
			return
		}
		req.Protocol, req.ChannelIndex = c.Param("protocol"), index
		result, err := CopyChannelToGroup(c.Request.Context(), deps.Store, deps.CfgManager, adapter, req)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, result)
	})

	// 分组/模型预览：接入前展示可选分组、倍率与各组模型，不落任何渠道配置
	group.GET("/accounts/:uid/keys", func(c *gin.Context) {
		account, found, err := deps.Store.Get(c.Param("uid"))
		if err != nil || !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "账号不存在"})
			return
		}
		if account.CredentialMode == "key" {
			c.JSON(http.StatusOK, gin.H{"keys": []any{}})
			return
		}
		token, err := account.plaintextAccessToken()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		items, err := adapterForAccount(account, adapter).ListAllTokens(c.Request.Context(), account, token)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		for i := range items {
			items[i].Key = ""
		}
		c.JSON(http.StatusOK, gin.H{"keys": items})
	})

	group.GET("/accounts/:uid/groups", func(c *gin.Context) {
		account, found, err := deps.Store.Get(c.Param("uid"))
		if err != nil || !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "账号不存在"})
			return
		}
		if account.CredentialMode == "key" {
			models, err := fetchInferenceModels(c.Request.Context(), account)
			message := ""
			if err != nil {
				message = err.Error()
			}
			c.JSON(http.StatusOK, gin.H{"identified": err == nil, "groups": []gin.H{{"name": "imported", "ratio": nil, "models": models}}, "error": message})
			return
		}
		token, err := account.plaintextAccessToken()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		site := adapterForAccount(account, adapter)
		ratios, err := site.FetchGroups(c.Request.Context(), account.BaseURL, token, account.UserID, account.AuthTokenMode)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"identified": false, "error": err.Error()})
			return
		}
		type groupPreview struct {
			DisplayName   string   `json:"displayName,omitempty"`
			Name          string   `json:"name"`
			Ratio         float64  `json:"ratio"`
			Models        []string `json:"models"`
			Error         string   `json:"error,omitempty"`
			ModelsUnknown bool     `json:"modelsUnknown,omitempty"`
		}
		previews := make([]groupPreview, 0, len(ratios))
		for name, ratio := range ratios {
			preview := groupPreview{Name: name, Ratio: ratio}
			if sub, ok := site.(*sub2APIAdapter); ok {
				preview.DisplayName = sub.groupNames[name]
			}
			models, err := site.FetchGroupModels(c.Request.Context(), account.BaseURL, token, account.UserID, account.AuthTokenMode, name)
			if err != nil {
				if errors.Is(err, ErrGroupModelsUnknown) {
					preview.ModelsUnknown = true
				} else {
					preview.Error = err.Error()
				}
			} else {
				preview.Models = models
			}
			previews = append(previews, preview)
		}
		c.JSON(http.StatusOK, gin.H{"identified": true, "groups": previews})
	})

	// 余额状态展示（只读，不写配额真相、不参与调度）
	group.GET("/accounts/:uid/balance", func(c *gin.Context) {
		account, found, err := deps.Store.Get(c.Param("uid"))
		if err != nil || !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "账号不存在"})
			return
		}
		token, err := account.plaintextAccessToken()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		balance, currency, err := adapterForAccount(account, adapter).FetchBalance(c.Request.Context(), account.BaseURL, token, account.UserID, account.AuthTokenMode)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"identified": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"identified": true, "balance": balance, "currency": currency})
	})

	// 渠道 Key 级余额探测（只读展示）：auto 识别 newapi_token/sub2api/newapi_billing
	group.POST("/accounts/:uid/key-balance", func(c *gin.Context) {
		var req struct {
			Protocol string `json:"protocol"`
			Group    string `json:"group"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
			return
		}
		account, found, err := deps.Store.Get(c.Param("uid"))
		if err != nil || !found {
			c.JSON(http.StatusNotFound, gin.H{"error": "账号不存在"})
			return
		}
		apiType, _, err := provisionProtocolInfo(req.Protocol)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		fingerprint := ""
		for _, meta := range account.ProvisionedKeys {
			if meta.Protocol == strings.ToLower(req.Protocol) && meta.Group == req.Group {
				fingerprint = meta.KeyFingerprint
				break
			}
		}
		var channel *config.UpstreamConfig
		channels := protocolChannels(deps.CfgManager, apiType)
		for i := range channels {
			if matchesAccountChannel(channels[i], account, req.Group, fingerprint) {
				channel = &channels[i]
				break
			}
		}
		if channel == nil || len(channel.APIKeys) == 0 {
			c.JSON(http.StatusNotFound, gin.H{"error": "渠道或可用 Key 不存在"})
			return
		}
		result := upstreamprobe.ProbeBalance(c.Request.Context(), upstreamprobe.BalanceProbeRequest{
			BaseURL:            channel.BaseURL,
			APIKey:             channel.APIKeys[0],
			InsecureSkipVerify: channel.InsecureSkipVerify,
		}, "auto")
		c.JSON(http.StatusOK, result)
	})
}
