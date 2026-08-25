// Package channels 提供 Channel Data Model v2 的 REST API（只读 + 统一写端点）。
//
// 写端点内部按 kind 路由到现有的 ConfigManager 方法，在 ChannelUID 层面统一寻址，
// 避免前端直接调用 /api/{messages,chat,...}/channels/* 六套协议路由。
// 详见 docs/specs/channel-data-model-v2.md。
package channels

import (
	"log"
	"net/http"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// Handler 渠道 v2 处理器。
type Handler struct {
	cm        *config.ConfigManager
	scheduler *scheduler.ChannelScheduler
}

// RegisterRoutes 注册渠道 v2 路由（读 + 写）。
func RegisterRoutes(apiGroup *gin.RouterGroup, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler) {
	h := &Handler{cm: cfgManager, scheduler: channelScheduler}
	apiGroup.GET("/channels", h.List)
	apiGroup.GET("/channels/:uid", h.Get)
	apiGroup.POST("/channels", h.Create)
	apiGroup.PUT("/channels/:uid", h.Update)
	apiGroup.DELETE("/channels/:uid", h.Delete)
	apiGroup.POST("/channels/:uid/keys", h.AddKey)
	apiGroup.DELETE("/channels/:uid/keys/:keyHash", h.RemoveKey)
}

// ListResponse 列表响应。
type ListResponse struct {
	SchemaVersion int                         `json:"schemaVersion"`
	Channels      []config.ChannelView        `json:"channels"`
	Capabilities  []config.EndpointCapability `json:"capabilities"`
}

// List 返回全部渠道视图与共享能力。
func (h *Handler) List(c *gin.Context) {
	views, caps := h.cm.GetChannelViews()
	c.JSON(http.StatusOK, ListResponse{
		SchemaVersion: config.ChannelSchemaVersion,
		Channels:      views,
		Capabilities:  caps,
	})
}

// GetResponse 单渠道响应，仅附带该渠道 key 引用到的能力子集。
type GetResponse struct {
	Channel      config.ChannelView          `json:"channel"`
	Capabilities []config.EndpointCapability `json:"capabilities"`
}

// Get 按聚合 ChannelUID 返回单个渠道视图及其引用的能力子集。
func (h *Handler) Get(c *gin.Context) {
	uid := c.Param("uid")
	views, caps := h.cm.GetChannelViews()
	capByUID := make(map[string]config.EndpointCapability, len(caps))
	for _, cap := range caps {
		capByUID[cap.CapabilityUID] = cap
	}
	for _, v := range views {
		if v.ChannelUID != uid {
			continue
		}
		c.JSON(http.StatusOK, GetResponse{Channel: v, Capabilities: referencedCapabilities(v, capByUID)})
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "channel not found", "uid": uid})
}

// referencedCapabilities 收集某渠道所有 key endpoint 引用到的能力（保序去重）。
func referencedCapabilities(v config.ChannelView, byUID map[string]config.EndpointCapability) []config.EndpointCapability {
	seen := make(map[string]struct{})
	out := make([]config.EndpointCapability, 0)
	for _, k := range v.Keys {
		for _, e := range k.Endpoints {
			if _, ok := seen[e.CapabilityUID]; ok {
				continue
			}
			if cap, ok := byUID[e.CapabilityUID]; ok {
				seen[e.CapabilityUID] = struct{}{}
				out = append(out, cap)
			}
		}
	}
	return out
}

// CreateRequest 统一渠道创建请求体。
type CreateRequest struct {
	Kind                    string                `json:"kind"` // messages|chat|responses|gemini|images|vectors
	Name                    string                `json:"name"`
	ServiceType             string                `json:"serviceType"`
	BaseURL                 string                `json:"baseUrl"`
	BaseURLs                []string              `json:"baseUrls"`
	APIKeys                 []string              `json:"apiKeys"`
	APIKeyConfigs           []config.APIKeyConfig `json:"apiKeyConfigs"`
	ModelMapping            map[string]string     `json:"modelMapping"`
	ReasoningMapping        map[string]string     `json:"reasoningMapping"`
	SupportedModels         []string              `json:"supportedModels"`
	CustomHeaders           map[string]string     `json:"customHeaders"`
	ProxyURL                string                `json:"proxyUrl"`
	ProxyPreferDirect       bool                  `json:"proxyPreferDirect"`
	RoutePrefix             string                `json:"routePrefix"`
	Status                  string                `json:"status"`
	Placement               string                `json:"placement"`
	AuthHeader              string                `json:"authHeader"`
	InsecureSkipVerify      bool                  `json:"insecureSkipVerify"`
	RequestTimeoutMs        int                   `json:"requestTimeoutMs"`
	ResponseHeaderTimeoutMs int                   `json:"responseHeaderTimeoutMs"`
	NoVision                bool                  `json:"noVision"`
	FastMode                bool                  `json:"fastMode"`
}

// Create 创建渠道（统一入口，按 kind 路由到对应数组）。
func (h *Handler) Create(c *gin.Context) {
	var req CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}

	k, err := config.ChannelKindByName(req.Kind)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	up := config.UpstreamConfig{
		Name:                    req.Name,
		ServiceType:             req.ServiceType,
		BaseURL:                 req.BaseURL,
		BaseURLs:                req.BaseURLs,
		APIKeys:                 req.APIKeys,
		APIKeyConfigs:           req.APIKeyConfigs,
		ModelMapping:            req.ModelMapping,
		ReasoningMapping:        req.ReasoningMapping,
		SupportedModels:         req.SupportedModels,
		CustomHeaders:           req.CustomHeaders,
		ProxyURL:                req.ProxyURL,
		ProxyPreferDirect:       req.ProxyPreferDirect,
		RoutePrefix:             req.RoutePrefix,
		Status:                  req.Status,
		AuthHeader:              req.AuthHeader,
		InsecureSkipVerify:      req.InsecureSkipVerify,
		RequestTimeoutMs:        req.RequestTimeoutMs,
		ResponseHeaderTimeoutMs: req.ResponseHeaderTimeoutMs,
		NoVision:                req.NoVision,
		FastMode:                req.FastMode,
	}

	if err := config.AddUpstreamByKind(h.cm, k, up, req.Placement); err != nil {
		log.Printf("[Channels-Create] 创建渠道失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create channel: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "渠道已创建"})
}

// UpdateRequest 统一渠道更新请求体。
type UpdateRequest struct {
	Kind                     *string                                   `json:"kind"`
	Name                     *string                                   `json:"name"`
	ServiceType              *string                                   `json:"serviceType"`
	BaseURL                  *string                                   `json:"baseUrl"`
	BaseURLs                 []string                                  `json:"baseUrls"`
	APIKeys                  []string                                  `json:"apiKeys"`
	APIKeyConfigs            []config.APIKeyConfig                     `json:"apiKeyConfigs"`
	ModelMapping             map[string]string                         `json:"modelMapping"`
	ModelCapabilities        map[string]config.UpstreamModelCapability `json:"modelCapabilities"`
	ReasoningMapping         map[string]string                         `json:"reasoningMapping"`
	SupportedModels          []string                                  `json:"supportedModels"`
	CustomHeaders            map[string]string                         `json:"customHeaders"`
	ProxyURL                 *string                                   `json:"proxyUrl"`
	ProxyPreferDirect        *bool                                     `json:"proxyPreferDirect"`
	RoutePrefix              *string                                   `json:"routePrefix"`
	Status                   *string                                   `json:"status"`
	Priority                 *int                                      `json:"priority"`
	AuthHeader               *string                                   `json:"authHeader"`
	InsecureSkipVerify       *bool                                     `json:"insecureSkipVerify"`
	RequestTimeoutMs         *int                                      `json:"requestTimeoutMs"`
	ResponseHeaderTimeoutMs  *int                                      `json:"responseHeaderTimeoutMs"`
	NoVision                 *bool                                     `json:"noVision"`
	FastMode                 *bool                                     `json:"fastMode"`
	Remark                   *string                                   `json:"remark"`
	Description              *string                                   `json:"description"`
	Website                  *string                                   `json:"website"`
	Tags                     []string                                  `json:"tags"`
	NoVisionModels           []string                                  `json:"noVisionModels"`
	VisionFallbackModel      *string                                   `json:"visionFallbackModel"`
	RateLimitRPM             *int                                      `json:"rateLimitRpm"`
	RateLimitBurst           *int                                      `json:"rateLimitBurst"`
	RateLimitMaxConcurrent   *int                                      `json:"rateLimitMaxConcurrent"`
	ConvertImageURLToB64JSON *bool                                     `json:"convertImageUrlToB64Json"`
	StripCodexClientTools    *bool                                     `json:"stripCodexClientTools"`
	CodexToolCompat          *bool                                     `json:"codexToolCompat"`
}

// Update 更新渠道（按 ChannelUID 寻址，内部按 kind 路由）。
func (h *Handler) Update(c *gin.Context) {
	uid := c.Param("uid")
	loc, _, found := h.cm.FindUpstreamByUID(uid)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "channel not found", "uid": uid})
		return
	}

	var req UpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}

	updates := config.UpstreamUpdate{
		Name:                     req.Name,
		ServiceType:              req.ServiceType,
		BaseURL:                  req.BaseURL,
		BaseURLs:                 req.BaseURLs,
		APIKeys:                  req.APIKeys,
		APIKeyConfigs:            req.APIKeyConfigs,
		ModelMapping:             req.ModelMapping,
		ModelCapabilities:        req.ModelCapabilities,
		ReasoningMapping:         req.ReasoningMapping,
		SupportedModels:          req.SupportedModels,
		CustomHeaders:            req.CustomHeaders,
		ProxyURL:                 req.ProxyURL,
		ProxyPreferDirect:        req.ProxyPreferDirect,
		RoutePrefix:              req.RoutePrefix,
		Status:                   req.Status,
		Priority:                 req.Priority,
		AuthHeader:               req.AuthHeader,
		InsecureSkipVerify:       req.InsecureSkipVerify,
		RequestTimeoutMs:         req.RequestTimeoutMs,
		ResponseHeaderTimeoutMs:  req.ResponseHeaderTimeoutMs,
		NoVision:                 req.NoVision,
		FastMode:                 req.FastMode,
		Remark:                   req.Remark,
		Description:              req.Description,
		Website:                  req.Website,
		Tags:                     req.Tags,
		NoVisionModels:           req.NoVisionModels,
		VisionFallbackModel:      req.VisionFallbackModel,
		RateLimitRPM:             req.RateLimitRPM,
		RateLimitBurst:           req.RateLimitBurst,
		RateLimitMaxConcurrent:   req.RateLimitMaxConcurrent,
		ConvertImageURLToB64JSON: req.ConvertImageURLToB64JSON,
		StripCodexClientTools:    req.StripCodexClientTools,
		CodexToolCompat:          req.CodexToolCompat,
	}

	shouldResetMetrics, err := config.UpdateUpstreamByKind(h.cm, loc, updates)
	if err != nil {
		log.Printf("[Channels-Update] 更新渠道失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update channel: " + err.Error()})
		return
	}

	if shouldResetMetrics && h.scheduler != nil {
		kind := schedulerChannelKind(loc.Kind)
		h.scheduler.ResetChannelMetrics(loc.Index, kind)
	}

	c.JSON(http.StatusOK, gin.H{"message": "渠道已更新"})
}

// Delete 删除渠道（按 ChannelUID 寻址）。
func (h *Handler) Delete(c *gin.Context) {
	uid := c.Param("uid")
	loc, _, found := h.cm.FindUpstreamByUID(uid)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "channel not found", "uid": uid})
		return
	}

	removed, err := config.RemoveUpstreamByKind(h.cm, loc)
	if err != nil {
		log.Printf("[Channels-Delete] 删除渠道失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete channel: " + err.Error()})
		return
	}

	if h.scheduler != nil {
		kind := schedulerChannelKind(loc.Kind)
		h.scheduler.DeleteChannelLogs(removed, kind)
		h.scheduler.DeleteChannelMetrics(removed, kind)
	}

	c.JSON(http.StatusOK, gin.H{"message": "渠道已删除", "removed": removed})
}

// AddKeyRequest 添加 key 请求体。
type AddKeyRequest struct {
	APIKey string `json:"apiKey"`
}

// AddKey 添加 API Key（按 ChannelUID 寻址）。
func (h *Handler) AddKey(c *gin.Context) {
	uid := c.Param("uid")
	loc, _, found := h.cm.FindUpstreamByUID(uid)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "channel not found", "uid": uid})
		return
	}

	var req AddKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.APIKey) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "apiKey is required"})
		return
	}

	if err := config.AddChannelKeyByKind(h.cm, loc, req.APIKey); err != nil {
		if strings.Contains(err.Error(), "API密钥已存在") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "API密钥已存在"})
		} else {
			log.Printf("[Channels-Key] 添加 key 失败: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add key: " + err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "API密钥已添加", "success": true})
}

// RemoveKey 删除 API Key（按 ChannelUID + keyHash 寻址，keyHash 是 key 的前 16 位 hex）。
func (h *Handler) RemoveKey(c *gin.Context) {
	uid := c.Param("uid")
	keyHash := c.Param("keyHash")
	loc, upstream, found := h.cm.FindUpstreamByUID(uid)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "channel not found", "uid": uid})
		return
	}

	// keyHash 是 key 的 SHA256 前 16 位 hex，查找匹配的明文 key
	var matchedKey string
	for _, k := range upstream.APIKeys {
		if config.ChannelKeyHash(k) == keyHash {
			matchedKey = k
			break
		}
	}
	if matchedKey == "" {
		// 也检查历史 key
		for _, k := range upstream.HistoricalAPIKeys {
			if config.ChannelKeyHash(k) == keyHash {
				matchedKey = k
				break
			}
		}
	}
	if matchedKey == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "key not found", "keyHash": keyHash})
		return
	}

	if err := config.RemoveChannelKeyByKind(h.cm, loc, matchedKey); err != nil {
		if strings.Contains(err.Error(), "API密钥不存在") {
			c.JSON(http.StatusNotFound, gin.H{"error": "API key not found"})
		} else {
			log.Printf("[Channels-Key] 删除 key 失败: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove key: " + err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "API密钥已删除"})
}

// schedulerChannelKind 将 config.channelKind 转为 scheduler.ChannelKind。
func schedulerChannelKind(k config.ChannelKind) scheduler.ChannelKind {
	switch k {
	case config.ChannelKindMessages:
		return scheduler.ChannelKindMessages
	case config.ChannelKindChat:
		return scheduler.ChannelKindChat
	case config.ChannelKindResponses:
		return scheduler.ChannelKindResponses
	case config.ChannelKindGemini:
		return scheduler.ChannelKindGemini
	case config.ChannelKindImages:
		return scheduler.ChannelKindImages
	case config.ChannelKindVectors:
		return scheduler.ChannelKindVectors
	default:
		return scheduler.ChannelKindMessages
	}
}
