// Package logicalchannels 提供逻辑渠道的 REST API。
// 事务在 config.ConfigManager 内部完成；本包负责 HTTP 入参/出参与错误翻译。
package logicalchannels

import (
	"net/http"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// RegisterRoutes 注册逻辑渠道相关路由到 apiGroup。
func RegisterRoutes(apiGroup *gin.RouterGroup, cfgManager *config.ConfigManager, channelScheduler *scheduler.ChannelScheduler) {
	h := &Handler{cm: cfgManager, scheduler: channelScheduler}

	apiGroup.GET("/logical-channels", h.List)
	apiGroup.GET("/logical-channels/dashboard", h.Dashboard)
	apiGroup.GET("/logical-channels/:uid", h.Get)
	apiGroup.POST("/logical-channels", h.Create)
	apiGroup.PUT("/logical-channels/:uid", h.Update)
	apiGroup.DELETE("/logical-channels/:uid", h.Delete)
}

// Handler 逻辑渠道 HTTP 处理器。
type Handler struct {
	cm        *config.ConfigManager
	scheduler *scheduler.ChannelScheduler
}

// ListResponse 列表响应。
type ListResponse struct {
	LogicalChannels []config.LogicalChannel `json:"logicalChannels"`
}

// CreateRequestBody POST 入参。
type CreateRequestBody struct {
	Name        string                      `json:"name"`
	Remark      string                      `json:"remark"`
	Description string                      `json:"description"`
	Website     string                      `json:"website"`
	ProviderID  string                      `json:"providerId"`
	AccountUID  string                      `json:"accountUid"`
	Kind        string                      `json:"kind"`
	BaseURLs    []string                    `json:"baseUrls"`
	APIKeys     []string                    `json:"apiKeys"` // 共享到所有 protocols（如未在 protocol 内覆盖）
	Tags        []string                    `json:"tags"`
	Protocols   []CreateRequestBodyProtocol `json:"protocols"`
	Placement   string                      `json:"placement"`
}

// CreateRequestBodyProtocol POST 单协议入参。
type CreateRequestBodyProtocol struct {
	Kind             string                `json:"kind"`
	ServiceType      string                `json:"serviceType"`
	APIKeys          []string              `json:"apiKeys"`
	APIKeyConfigs    []config.APIKeyConfig `json:"apiKeyConfigs"`
	BaseURLs         []string              `json:"baseUrls"`
	BaseURL          string                `json:"baseUrl"`
	ModelMapping     map[string]string     `json:"modelMapping"`
	ReasoningMapping map[string]string     `json:"reasoningMapping"`
	Priority         int                   `json:"priority"`
	Enabled          *bool                 `json:"enabled"`
	Status           string                `json:"status"`
	RoutePrefix      string                `json:"routePrefix"`
	SupportedModels  []string              `json:"supportedModels"`
	CustomHeaders    map[string]string     `json:"customHeaders"`
	ProxyURL         string                `json:"proxyUrl"`
}

// UpdateRequestBody PUT 入参。
type UpdateRequestBody struct {
	Common    *UpdateRequestBodyCommon    `json:"common"`
	Protocols []UpdateRequestBodyProtocol `json:"protocols"`
	Removals  []string                    `json:"removals"`
	Placement string                      `json:"placement"`
}

// UpdateRequestBodyCommon 跨协议共享字段更新。
type UpdateRequestBodyCommon struct {
	Name        *string   `json:"name"`
	Remark      *string   `json:"remark"`
	Description *string   `json:"description"`
	Website     *string   `json:"website"`
	Tags        *[]string `json:"tags"`
	BaseURLs    *[]string `json:"baseUrls"`
}

// UpdateRequestBodyProtocol 单协议更新/新增。
type UpdateRequestBodyProtocol struct {
	Kind             string                `json:"kind"`
	ServiceType      string                `json:"serviceType"`
	APIKeys          []string              `json:"apiKeys"`
	APIKeyConfigs    []config.APIKeyConfig `json:"apiKeyConfigs"`
	BaseURLs         []string              `json:"baseUrls"`
	BaseURL          string                `json:"baseUrl"`
	ModelMapping     map[string]string     `json:"modelMapping"`
	ReasoningMapping map[string]string     `json:"reasoningMapping"`
	Priority         int                   `json:"priority"`
	Enabled          *bool                 `json:"enabled"`
	Status           string                `json:"status"`
	RoutePrefix      string                `json:"routePrefix"`
	SupportedModels  []string              `json:"supportedModels"`
	CustomHeaders    map[string]string     `json:"customHeaders"`
	ProxyURL         string                `json:"proxyUrl"`
}

// List GET /api/logical-channels?kind=llm
func (h *Handler) List(c *gin.Context) {
	kind := c.Query("kind")
	if kind == "" {
		c.JSON(http.StatusOK, ListResponse{LogicalChannels: h.cm.ListLogicalChannels()})
		return
	}
	c.JSON(http.StatusOK, ListResponse{LogicalChannels: h.cm.ListLogicalChannelsWithKind(config.LogicalChannelKind(kind))})
}

// Dashboard GET /api/logical-channels/dashboard?kind=llm
// 返回按 logical channel 聚合的 dashboard 数据：每个 channel 对象代表一个逻辑渠道，
// 包含 protocolRoutes[] 指明其下的物理协议路由，供 Desktop 统一列表使用。
func (h *Handler) Dashboard(c *gin.Context) {
	kind := c.Query("kind")
	if kind == "" {
		kind = "llm"
	}
	cfg := h.cm.GetConfig()
	logicalChannels := h.cm.ListLogicalChannelsWithKind(config.LogicalChannelKind(kind))

	channels := make([]gin.H, 0, len(logicalChannels))
	metricsResult := make([]gin.H, 0, len(logicalChannels))
	recentActivity := make([]*metrics.ChannelRecentActivity, 0, len(logicalChannels))

	for _, lc := range logicalChannels {
		primary, primaryKind, primaryIndex := primaryPhysicalChannel(cfg, lc)
		if primary == nil {
			continue
		}
		view := common.BuildChannelView(*primary, primaryIndex)
		// 用逻辑渠道信息覆盖展示字段
		view["name"] = lc.Name
		view["logicalChannelUid"] = lc.LogicalChannelUID
		view["logicalName"] = lc.Name
		view["baseUrl"] = primaryBaseURLFromLogical(lc)
		view["baseUrls"] = lc.BaseURLs
		view["accountUid"] = lc.AccountUID
		view["providerId"] = lc.ProviderID
		view["remark"] = lc.Remark
		view["tags"] = lc.Tags

		// 协议路由胶囊
		protocolRoutes := buildLogicalProtocolRoutes(cfg, lc)
		view["protocolRoutes"] = protocolRoutes
		view["protocolCapsules"] = protocolRoutes

		channels = append(channels, view)

		// metrics/activity：用主物理路由在对应 kind 的 metrics manager 中查询
		mm := metricsManagerForKind(h.scheduler, primaryKind)
		idx := channelsIndexForLogical(len(channels) - 1)
		if mm != nil {
			resp := mm.ToResponseMultiURL(primaryIndex, primary.GetAllBaseURLs(), primary.APIKeys, scheduler.NormalizedMetricsServiceType(primaryKind, primary.ServiceType), 0, logicalChannelHistoricalAPIKeys(*primary))
			metricsResult = append(metricsResult, gin.H{
				"channelIndex":        idx,
				"channelName":         lc.Name,
				"requestCount":        resp.RequestCount,
				"successCount":        resp.SuccessCount,
				"failureCount":        resp.FailureCount,
				"successRate":         resp.SuccessRate,
				"errorRate":           resp.ErrorRate,
				"consecutiveFailures": resp.ConsecutiveFailures,
				"circuitState":        resp.CircuitState,
				"latency":             resp.Latency,
			})
			recentActivity = append(recentActivity, mm.GetRecentActivityMultiURL(primaryIndex, primary.GetAllBaseURLs(), logicalChannelStatsAPIKeys(*primary), scheduler.NormalizedMetricsServiceType(primaryKind, primary.ServiceType)))
		} else {
			metricsResult = append(metricsResult, gin.H{"channelIndex": idx})
			recentActivity = append(recentActivity, nil)
		}
	}

	stats := gin.H{
		"multiChannelMode":   h.scheduler != nil && h.scheduler.IsMultiChannelMode(scheduler.ChannelKindMessages),
		"activeChannelCount": countActiveLogical(logicalChannels),
	}

	c.JSON(http.StatusOK, gin.H{
		"channels":       channels,
		"current":        -1,
		"metrics":        metricsResult,
		"stats":          stats,
		"recentActivity": recentActivity,
	})
}

func channelsIndexForLogical(i int) int { return i }

func countActiveLogical(channels []config.LogicalChannel) int {
	count := 0
	for _, lc := range channels {
		for _, p := range lc.Protocols {
			if strings.EqualFold(p.Status, "active") {
				count++
				break
			}
		}
	}
	return count
}

func primaryBaseURLFromLogical(lc config.LogicalChannel) string {
	if len(lc.BaseURLs) > 0 {
		return lc.BaseURLs[0]
	}
	return ""
}

func primaryPhysicalChannel(cfg config.Config, lc config.LogicalChannel) (*config.UpstreamConfig, scheduler.ChannelKind, int) {
	order := []string{"messages", "chat", "responses", "gemini", "images", "vectors"}
	for _, kindStr := range order {
		for _, p := range lc.Protocols {
			if p.Kind != kindStr {
				continue
			}
			up, idx := findPhysicalChannel(cfg, p.Kind, p.ChannelUID)
			if up != nil {
				return up, schedulerChannelKind(p.Kind), idx
			}
		}
	}
	return nil, "", -1
}

func findPhysicalChannel(cfg config.Config, kind, channelUID string) (*config.UpstreamConfig, int) {
	var slice []config.UpstreamConfig
	switch kind {
	case "messages":
		slice = cfg.Upstream
	case "chat":
		slice = cfg.ChatUpstream
	case "responses":
		slice = cfg.ResponsesUpstream
	case "gemini":
		slice = cfg.GeminiUpstream
	case "images":
		slice = cfg.ImagesUpstream
	case "vectors":
		slice = cfg.VectorsUpstream
	}
	for i := range slice {
		if slice[i].ChannelUID == channelUID {
			return &slice[i], i
		}
	}
	return nil, -1
}

func buildLogicalProtocolRoutes(cfg config.Config, lc config.LogicalChannel) []gin.H {
	out := make([]gin.H, 0, len(lc.Protocols))
	order := []string{"messages", "chat", "responses", "gemini", "images", "vectors"}
	seen := make(map[string]struct{})
	for _, kind := range order {
		for _, p := range lc.Protocols {
			if p.Kind != kind {
				continue
			}
			if _, ok := seen[p.ChannelUID]; ok {
				continue
			}
			seen[p.ChannelUID] = struct{}{}
			up, idx := findPhysicalChannel(cfg, p.Kind, p.ChannelUID)
			name := p.Kind
			apiKeys := []string{}
			if up != nil {
				name = up.Name
				apiKeys = up.APIKeys
			}
			out = append(out, gin.H{
				"kind":        p.Kind,
				"index":       idx,
				"name":        name,
				"serviceType": p.ServiceType,
				"channelUid":  p.ChannelUID,
				"status":      p.Status,
				"apiKeys":     apiKeys,
			})
		}
	}
	return out
}

func metricsManagerForKind(s *scheduler.ChannelScheduler, kind scheduler.ChannelKind) *metrics.MetricsManager {
	if s == nil {
		return nil
	}
	switch kind {
	case scheduler.ChannelKindResponses:
		return s.GetResponsesMetricsManager()
	case scheduler.ChannelKindGemini:
		return s.GetGeminiMetricsManager()
	case scheduler.ChannelKindChat:
		return s.GetChatMetricsManager()
	case scheduler.ChannelKindImages:
		return s.GetImagesMetricsManager()
	case scheduler.ChannelKindVectors:
		return s.GetVectorsMetricsManager()
	default:
		return s.GetMessagesMetricsManager()
	}
}

func logicalChannelHistoricalAPIKeys(upstream config.UpstreamConfig) []string {
	seen := make(map[string]struct{})
	var result []string
	add := func(key string) {
		if key == "" {
			return
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	for _, key := range upstream.HistoricalAPIKeys {
		add(key)
	}
	for _, dk := range upstream.DisabledAPIKeys {
		add(dk.Key)
	}
	return result
}

func logicalChannelStatsAPIKeys(upstream config.UpstreamConfig) []string {
	seen := make(map[string]struct{})
	var result []string
	add := func(key string) {
		if key == "" {
			return
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	for _, key := range upstream.APIKeys {
		add(key)
	}
	for _, key := range upstream.HistoricalAPIKeys {
		add(key)
	}
	for _, dk := range upstream.DisabledAPIKeys {
		add(dk.Key)
	}
	return result
}

// Get GET /api/logical-channels/:uid
func (h *Handler) Get(c *gin.Context) {
	uid := c.Param("uid")
	l := h.cm.GetLogicalChannel(uid)
	if l == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "logical channel not found"})
		return
	}
	c.JSON(http.StatusOK, l)
}

// Create POST /api/logical-channels
func (h *Handler) Create(c *gin.Context) {
	var body CreateRequestBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}
	if len(body.Protocols) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "protocols 不能为空"})
		return
	}
	in := config.CreateLogicalChannelInput{
		Name:        body.Name,
		Remark:      body.Remark,
		Description: body.Description,
		Website:     body.Website,
		ProviderID:  body.ProviderID,
		AccountUID:  body.AccountUID,
		Kind:        config.LogicalChannelKind(body.Kind),
		BaseURLs:    body.BaseURLs,
		Tags:        body.Tags,
		Placement:   body.Placement,
		Protocols:   make([]config.CreateLogicalChannelProtocol, 0, len(body.Protocols)),
	}
	for _, p := range body.Protocols {
		keys := p.APIKeys
		if len(keys) == 0 {
			keys = body.APIKeys
		}
		in.Protocols = append(in.Protocols, config.CreateLogicalChannelProtocol{
			Kind:             strings.ToLower(strings.TrimSpace(p.Kind)),
			ServiceType:      p.ServiceType,
			APIKeys:          keys,
			APIKeyConfigs:    p.APIKeyConfigs,
			BaseURLs:         p.BaseURLs,
			BaseURL:          p.BaseURL,
			ModelMapping:     p.ModelMapping,
			ReasoningMapping: p.ReasoningMapping,
			Priority:         p.Priority,
			Enabled:          p.Enabled,
			Status:           p.Status,
			RoutePrefix:      p.RoutePrefix,
			SupportedModels:  p.SupportedModels,
			CustomHeaders:    p.CustomHeaders,
			ProxyURL:         p.ProxyURL,
		})
	}
	if in.Kind == "" {
		// 未指定时按 protocols 推断
		in.Kind = inferKindFromProtocols(in.Protocols)
	}
	logical, err := h.cm.CreateLogicalChannel(in)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, logical)
}

// Update PUT /api/logical-channels/:uid
func (h *Handler) Update(c *gin.Context) {
	uid := c.Param("uid")
	var body UpdateRequestBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body: " + err.Error()})
		return
	}
	in := config.UpdateLogicalChannelInput{
		LogicalChannelUID: uid,
		Removals:          body.Removals,
		Placement:         body.Placement,
		Protocols:         make([]config.UpdateLogicalChannelProtocol, 0, len(body.Protocols)),
	}
	if body.Common != nil {
		in.Common = &config.UpdateLogicalChannelCommon{
			Name:        body.Common.Name,
			Remark:      body.Common.Remark,
			Description: body.Common.Description,
			Website:     body.Common.Website,
			Tags:        body.Common.Tags,
			BaseURLs:    body.Common.BaseURLs,
		}
	}
	for _, p := range body.Protocols {
		in.Protocols = append(in.Protocols, config.UpdateLogicalChannelProtocol{
			Kind:             strings.ToLower(strings.TrimSpace(p.Kind)),
			ServiceType:      p.ServiceType,
			APIKeys:          p.APIKeys,
			APIKeyConfigs:    p.APIKeyConfigs,
			BaseURLs:         p.BaseURLs,
			BaseURL:          p.BaseURL,
			ModelMapping:     p.ModelMapping,
			ReasoningMapping: p.ReasoningMapping,
			Priority:         p.Priority,
			Enabled:          p.Enabled,
			Status:           p.Status,
			RoutePrefix:      p.RoutePrefix,
			SupportedModels:  p.SupportedModels,
			CustomHeaders:    p.CustomHeaders,
			ProxyURL:         p.ProxyURL,
		})
	}
	logical, err := h.cm.UpdateLogicalChannel(in)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, logical)
}

// Delete DELETE /api/logical-channels/:uid
func (h *Handler) Delete(c *gin.Context) {
	uid := c.Param("uid")
	removed, err := h.cm.DeleteLogicalChannel(uid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	// 清理 metrics / logs
	if h.scheduler != nil {
		for i := range removed {
			up := removed[i]
			kind := schedulerKindFromProtocol(protocolKindFromLogicalProtocol(up.ServiceType, up.LogicalChannelUID))
			// 由于 UpstreamConfig 没有存 kind 字段，我们用 LogicalChannelUID 重新查
			_ = kind
		}
		// 简化：直接按 LogicalChannelUID 在六类数组中查找并删除对应 metrics/log
		// （重复遍历以避免漏清理；GetLogicalChannel 已被删除，所以按 UID 查表）
		// 通过查询内存副本（锁内）已不实际；这里按 serviceType 推断 kind 走 fallback
		cleanupRemovedChannels(h.scheduler, removed)
	}
	c.JSON(http.StatusOK, gin.H{
		"message":           "逻辑渠道已删除",
		"logicalChannelUid": uid,
		"removedChannels":   removedChannelUIDs(removed),
	})
}

func inferKindFromProtocols(protocols []config.CreateLogicalChannelProtocol) config.LogicalChannelKind {
	hasImages, hasVectors := false, false
	for _, p := range protocols {
		switch p.Kind {
		case "images":
			hasImages = true
		case "vectors":
			hasVectors = true
		}
	}
	switch {
	case hasImages && !hasVectors:
		return config.LogicalChannelKindImages
	case hasVectors && !hasImages:
		return config.LogicalChannelKindEmbeddings
	}
	return config.LogicalChannelKindLLM
}

func schedulerKindFromProtocol(protocolKind string) string {
	return protocolKind
}

func protocolKindFromLogicalProtocol(serviceType, logicalChannelUID string) string {
	// 退化路径：仅在 serviceType 与 logicalChannelUID 都缺失时才返回空。
	if serviceType == "" && logicalChannelUID == "" {
		return ""
	}
	switch serviceType {
	case "claude":
		return "messages"
	case "openai":
		return "chat"
	case "responses":
		return "responses"
	case "gemini":
		return "gemini"
	}
	return ""
}

func removedChannelUIDs(channels []config.UpstreamConfig) []string {
	out := make([]string, 0, len(channels))
	for _, c := range channels {
		out = append(out, c.ChannelUID)
	}
	return out
}

// cleanupRemovedChannels 删除 metrics/log；kind 从 serviceType 推断
func cleanupRemovedChannels(s *scheduler.ChannelScheduler, channels []config.UpstreamConfig) {
	for i := range channels {
		up := &channels[i]
		var kind string
		switch up.ServiceType {
		case "claude":
			kind = "messages"
		case "openai":
			kind = "chat"
		case "responses":
			kind = "responses"
		case "gemini":
			kind = "gemini"
		}
		// images/vectors 没有专门的 scheduler ChannelKind 清理路径，
		// 但它们不通过 ChannelScheduler 记 metrics，所以这里无需处理。
		if kind == "" {
			continue
		}
		schedulerKind := schedulerChannelKind(kind)
		if schedulerKind == "" {
			continue
		}
		s.DeleteChannelLogs(up, schedulerKind)
		s.DeleteChannelMetrics(up, schedulerKind)
	}
}

func schedulerChannelKind(kind string) scheduler.ChannelKind {
	switch kind {
	case "messages":
		return scheduler.ChannelKindMessages
	case "chat":
		return scheduler.ChannelKindChat
	case "responses":
		return scheduler.ChannelKindResponses
	case "gemini":
		return scheduler.ChannelKindGemini
	case "images":
		return scheduler.ChannelKindImages
	case "vectors":
		return scheduler.ChannelKindVectors
	}
	return ""
}
