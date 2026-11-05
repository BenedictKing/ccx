package siteaccess

import (
	"net/http"
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
	MaskedToken     string               `json:"maskedAccessToken,omitempty"`
	CreatedAt       time.Time            `json:"createdAt"`
	UpdatedAt       time.Time            `json:"updatedAt"`
	ProvisionedKeys []ProvisionedKeyMeta `json:"provisionedKeys,omitempty"`
	Evidence        []ChannelEvidence    `json:"evidence,omitempty"`
}

func toAccountResponse(a SiteAccount) accountResponse {
	return accountResponse{
		UID:             a.UID,
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
			SiteType      string `json:"siteType"`
			BaseURL       string `json:"baseUrl"`
			UserID        string `json:"userId"`
			AuthTokenMode string `json:"authTokenMode"`
			AccessToken   string `json:"accessToken"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
			return
		}
		uid, err := deps.Store.Upsert(SiteAccount{
			SiteType:      req.SiteType,
			BaseURL:       req.BaseURL,
			UserID:        req.UserID,
			AuthTokenMode: req.AuthTokenMode,
			AccessToken:   req.AccessToken,
		})
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
			Protocol      string            `json:"protocol"`
			Groups        []string          `json:"groups"`
			ExcludeGroups []string          `json:"excludeGroups"`
			MaxMultiplier *float64          `json:"maxMultiplier"`
			Models        []string          `json:"models"`
			ModelMapping  map[string]string `json:"modelMapping"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体解析失败"})
			return
		}
		result, err := ProvisionProtocol(c.Request.Context(), deps.Store, deps.CfgManager, adapter, ProvisionRequest{
			AccountUID:    c.Param("uid"),
			Protocol:      req.Protocol,
			Groups:        req.Groups,
			ExcludeGroups: req.ExcludeGroups,
			MaxMultiplier: req.MaxMultiplier,
			Models:        req.Models,
			ModelMapping:  req.ModelMapping,
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
		balance, currency, err := adapter.FetchBalance(c.Request.Context(), account.BaseURL, token, account.UserID, account.AuthTokenMode)
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
		channelName := ProvisionedChannelName(account.BaseURL, strings.ToLower(req.Protocol), req.Group)
		var channel *config.UpstreamConfig
		channels := protocolChannels(deps.CfgManager, apiType)
		for i := range channels {
			if channels[i].Name == channelName {
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
