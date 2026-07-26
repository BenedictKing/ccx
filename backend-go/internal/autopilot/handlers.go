package autopilot

import (
	"net/http"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

// ─── 响应类型 ────────────────────────────────────────────────────────────────────

// OverviewResponse GET /api/health-center/overview 返回结构。
type OverviewResponse struct {
	TotalChannels  int            `json:"totalChannels"`
	TotalEndpoints int            `json:"totalEndpoints"`
	StateCounts    map[string]int `json:"stateCounts"` // key=HealthState 常量, value=数量
}

// ChannelHealthItem 渠道级聚合健康信息。
type ChannelHealthItem struct {
	ChannelUID            string  `json:"channelUid"`
	ChannelID             int     `json:"channelId"`
	ChannelKind           string  `json:"channelKind"`
	ChannelName           string  `json:"channelName,omitempty"`
	AggState              string  `json:"aggState"` // AggregateHealthState 结果
	EndpointCount         int     `json:"endpointCount"`
	HealthyCount          int     `json:"healthyCount"`
	DegradedCount         int     `json:"degradedCount"`
	LimitedCount          int     `json:"limitedCount"`
	DeadCount             int     `json:"deadCount"`
	UnknownCount          int     `json:"unknownCount"`
	AvgSuccessRate        float64 `json:"avgSuccessRate,omitempty"`
	SpeedTier             string  `json:"speedTier,omitempty"`
	ConnectSampleCount    int64   `json:"connectSampleCount,omitempty"`
	P95ConnectLatencyMs   int64   `json:"p95ConnectLatencyMs,omitempty"`
	FirstByteSampleCount  int64   `json:"firstByteSampleCount,omitempty"`
	P95FirstByteLatencyMs int64   `json:"p95FirstByteLatencyMs,omitempty"`
}

// ChannelsResponse GET /api/health-center/channels 返回结构。
type ChannelsResponse struct {
	Channels []ChannelHealthItem `json:"channels"`
}

// EndpointDetailItem endpoint 级详情（Key 已脱敏）。
type EndpointDetailItem struct {
	EndpointUID           string  `json:"endpointUid"`
	ChannelUID            string  `json:"channelUid"`
	ChannelKind           string  `json:"channelKind"`
	BaseURL               string  `json:"baseUrl"`
	KeyHash               string  `json:"keyHash"` // SHA256 前 16 位，用于稳定关联
	KeyMask               string  `json:"keyMask"` // 脱敏后的 Key，仅用于展示
	HealthState           string  `json:"healthState"`
	HealthConfidence      float64 `json:"healthConfidence"`
	HealthEvidence        string  `json:"healthEvidence,omitempty"`
	SuggestedAction       string  `json:"suggestedAction,omitempty"`
	QualityTier           string  `json:"qualityTier,omitempty"`
	StabilityTier         string  `json:"stabilityTier,omitempty"`
	SpeedTier             string  `json:"speedTier,omitempty"`
	SuccessRate15m        float64 `json:"successRate15m,omitempty"`
	SuccessRate1h         float64 `json:"successRate1h,omitempty"`
	P95LatencyMs          float64 `json:"p95LatencyMs,omitempty"`
	ConnectSampleCount    int64   `json:"connectSampleCount,omitempty"`
	P95ConnectLatencyMs   float64 `json:"p95ConnectLatencyMs,omitempty"`
	FirstByteSampleCount  int64   `json:"firstByteSampleCount,omitempty"`
	P95FirstByteLatencyMs float64 `json:"p95FirstByteLatencyMs,omitempty"`
	ConsecutiveFail       int     `json:"consecutiveFail"`
	LastSuccessAt         string  `json:"lastSuccessAt,omitempty"`
	UpdatedAt             string  `json:"updatedAt,omitempty"`

	// Phase 1 新增字段：限速建议（向后兼容，omitempty）
	SuggestedRPM       int     `json:"suggestedRpm,omitempty"`
	SuggestedRPMSource string  `json:"suggestedRpmSource,omitempty"`
	SuggestedRPMConf   float64 `json:"suggestedRpmConfidence,omitempty"`
	SuggestedRPMTPM    int     `json:"suggestedRpmTpm,omitempty"`
	SuggestedRPMRPD    int     `json:"suggestedRpmRpd,omitempty"`

	// Phase 1 新增字段：质量趋势（向后兼容，omitempty）
	QualityTrend *QualityTrend `json:"qualityTrend,omitempty"`

	// Phase 1 新增字段：分组变更（向后兼容，omitempty）
	ModelListHash   string             `json:"modelListHash,omitempty"`
	GroupChangedAt  string             `json:"groupChangedAt,omitempty"`
	LastGroupChange *GroupChangeResult `json:"lastGroupChange,omitempty"`

	// Phase 1 新增字段：用量窗口 + 订阅继承 + 能力漂移（向后兼容，omitempty）
	UsageWindows               []UsageWindow           `json:"usageWindows,omitempty"`
	InheritedFromSubscription  bool                    `json:"inheritedFromSubscription,omitempty"`
	EndpointInconsistencies    []EndpointInconsistency `json:"endpointInconsistencies,omitempty"`
	MiniMaxTokenPlanUsage      *MiniMaxTokenPlanUsage  `json:"miniMaxTokenPlanUsage,omitempty"`
	MiniMaxTokenPlanUsageError string                  `json:"miniMaxTokenPlanUsageError,omitempty"`
	TokenPlanUsageSupported    bool                    `json:"tokenPlanUsageSupported,omitempty"`
}

// EndpointsResponse GET /api/health-center/channels/:channelUid/endpoints 返回结构。
type EndpointsResponse struct {
	ChannelUID string               `json:"channelUid"`
	Endpoints  []EndpointDetailItem `json:"endpoints"`
}

// ─── 路由注册 ────────────────────────────────────────────────────────────────────

// RegisterRoutes 注册健康中心只读 API 到给定路由组。
func RegisterRoutes(router gin.IRouter, mgr *Manager) {
	group := router.Group("/health-center")
	{
		group.GET("/overview", handleOverview(mgr))
		group.GET("/channels", handleChannels(mgr))
		group.GET("/channels/:channelUid/endpoints", handleEndpoints(mgr))
		group.POST("/endpoints/:endpointUid/token-plan-usage/refresh", handleRefreshTokenPlanUsage(mgr))
		group.GET("/provider-quality/budget", handleProviderQualityBudget(mgr.ProviderQualityProbe()))
		group.POST("/provider-quality/probe", handleProviderQualityProbe(mgr.ProviderQualityProbe()))

		// Phase 3A：画像变更事件（只读展示，不影响调度）
		group.GET("/changelog", handleChangelog(mgr))
		group.GET("/events", handleChangelogEvents(mgr))
	}
}

// ─── Handler 实现 ────────────────────────────────────────────────────────────────

// handleOverview GET /api/health-center/overview
// 全局汇总：各 HealthState 计数、渠道数、endpoint 数。
func handleOverview(mgr *Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		allProfiles := mgr.ProfileStore().ListActive()

		stateCounts := map[string]int{
			string(HealthStateUnknown):       0,
			string(HealthStateHealthy):       0,
			string(HealthStateDegraded):      0,
			string(HealthStateLimited):       0,
			string(HealthStateMisconfigured): 0,
			string(HealthStateDead):          0,
		}

		channelSet := make(map[string]struct{})
		for _, p := range allProfiles {
			channelSet[p.ChannelUID] = struct{}{}
			state := string(p.HealthState)
			stateCounts[state]++
		}

		c.JSON(http.StatusOK, OverviewResponse{
			TotalChannels:  len(channelSet),
			TotalEndpoints: len(allProfiles),
			StateCounts:    stateCounts,
		})
	}
}

// handleChannels GET /api/health-center/channels
// 渠道级聚合列表。
func handleChannels(mgr *Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		allProfiles := mgr.ProfileStore().ListActive()

		// 按 channelUID 分组
		grouped := make(map[string][]*KeyEndpointProfile)
		for _, p := range allProfiles {
			grouped[p.ChannelUID] = append(grouped[p.ChannelUID], p)
		}

		// 从配置获取渠道名称映射
		cfg := mgr.cfgManager.GetConfig()
		channelNames := buildChannelNameMap(cfg)

		items := make([]ChannelHealthItem, 0, len(grouped))
		for channelUID, profiles := range grouped {
			item := aggregateChannel(channelUID, profiles)
			if name, ok := channelNames[channelUID]; ok {
				item.ChannelName = name
			}
			items = append(items, item)
		}

		c.JSON(http.StatusOK, ChannelsResponse{Channels: items})
	}
}

// handleEndpoints GET /api/health-center/channels/:channelUid/endpoints
// endpoint 级展开（Key 用 hash 展示，绝不返回明文 key）。
func handleEndpoints(mgr *Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		channelUID := c.Param("channelUid")
		if channelUID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "channelUid 不能为空"})
			return
		}

		profiles := mgr.ProfileStore().ListActiveByChannel(channelUID)
		endpoints := make([]EndpointDetailItem, 0, len(profiles))
		for _, p := range profiles {
			keyHash := p.KeyHash
			if keyHash == "" {
				keyHash = p.MetricsKey
			}
			apiKey, hasAPIKey := mgr.ResolveAPIKey(p.ChannelUID, keyHash)
			item := EndpointDetailItem{
				EndpointUID:           p.EndpointUID,
				ChannelUID:            p.ChannelUID,
				ChannelKind:           p.ChannelKind,
				BaseURL:               p.BaseURL,
				KeyHash:               keyHash,
				KeyMask:               p.KeyMask,
				HealthState:           string(p.HealthState),
				HealthConfidence:      p.HealthConfidence,
				HealthEvidence:        strings.Join(p.HealthEvidence, "; "),
				SuggestedAction:       string(p.SuggestedAction),
				QualityTier:           string(p.QualityTier),
				StabilityTier:         string(p.StabilityTier),
				SpeedTier:             string(p.SpeedTier),
				SuccessRate15m:        p.SuccessRate15m,
				P95LatencyMs:          float64(p.P95LatencyMs),
				ConnectSampleCount:    p.ConnectSampleCount,
				P95ConnectLatencyMs:   float64(p.P95ConnectLatencyMs),
				FirstByteSampleCount:  p.FirstByteSampleCount,
				P95FirstByteLatencyMs: float64(p.P95FirstByteLatencyMs),
				ConsecutiveFail:       p.ConsecutiveFail,

				// Phase 1 新增：限速建议
				SuggestedRPM:       p.DiscoveredRPM,
				SuggestedRPMSource: p.SuggestedRPMSource,
				SuggestedRPMConf:   p.RateLimitConfidence,
				SuggestedRPMTPM:    p.SuggestedRPMTPM,
				SuggestedRPMRPD:    p.SuggestedRPMRPD,

				// Phase 1 新增：质量趋势
				QualityTrend: p.QualityTrend,

				// Phase 1 新增：分组变更
				ModelListHash:   p.ModelListHash,
				LastGroupChange: p.LastGroupChange,

				// Phase 1 新增：用量窗口 + 订阅继承 + 能力漂移
				UsageWindows:               p.UsageWindows,
				InheritedFromSubscription:  p.InheritedFromSubscription,
				EndpointInconsistencies:    p.EndpointInconsistencies,
				MiniMaxTokenPlanUsage:      cloneMiniMaxTokenPlanUsage(p.MiniMaxTokenPlanUsage),
				MiniMaxTokenPlanUsageError: p.MiniMaxTokenPlanUsageError,
				TokenPlanUsageSupported:    hasAPIKey && isMiniMaxTokenPlanKey(apiKey) && len(miniMaxTokenPlanUsageEndpoints(p.BaseURL)) > 0,
			}
			if p.LastSuccessAt != nil {
				item.LastSuccessAt = p.LastSuccessAt.Format("2006-01-02T15:04:05Z07:00")
			}
			if !p.UpdatedAt.IsZero() {
				item.UpdatedAt = p.UpdatedAt.Format("2006-01-02T15:04:05Z07:00")
			}
			if p.GroupChangedAt != nil {
				item.GroupChangedAt = p.GroupChangedAt.Format("2006-01-02T15:04:05Z07:00")
			}
			endpoints = append(endpoints, item)
		}

		c.JSON(http.StatusOK, EndpointsResponse{
			ChannelUID: channelUID,
			Endpoints:  endpoints,
		})
	}
}

func handleRefreshTokenPlanUsage(mgr *Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		endpointUID := strings.TrimSpace(c.Param("endpointUid"))
		if endpointUID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "endpointUid 不能为空"})
			return
		}
		usage, cached, err := mgr.RefreshMiniMaxTokenPlanUsage(c.Request.Context(), endpointUID, c.Query("force") == "true")
		if err != nil {
			status := http.StatusBadGateway
			if strings.Contains(err.Error(), "不存在") {
				status = http.StatusNotFound
			} else if strings.Contains(err.Error(), "不是 MiniMax Token Plan") || strings.Contains(err.Error(), "无法解析") {
				status = http.StatusBadRequest
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"usage": usage, "cached": cached})
	}
}

// ─── 内部辅助 ────────────────────────────────────────────────────────────────────

// aggregateChannel 将同一 channelUID 下的 endpoint profiles 聚合为渠道级摘要。
func aggregateChannel(channelUID string, profiles []*KeyEndpointProfile) ChannelHealthItem {
	item := ChannelHealthItem{
		ChannelUID:    channelUID,
		EndpointCount: len(profiles),
	}

	if len(profiles) > 0 {
		item.ChannelID = profiles[0].ChannelID
		item.ChannelKind = profiles[0].ChannelKind
	}

	var totalSuccessRate float64
	var states []DiagnosisResult
	var maxConnectP95 int64
	var maxDisplayConnectP95 int64
	var firstByteP95Values []int64

	for _, p := range profiles {
		switch p.HealthState {
		case HealthStateHealthy:
			item.HealthyCount++
		case HealthStateDegraded:
			item.DegradedCount++
		case HealthStateLimited:
			item.LimitedCount++
		case HealthStateDead:
			item.DeadCount++
		default:
			item.UnknownCount++
		}
		totalSuccessRate += p.SuccessRate15m
		states = append(states, DiagnosisResult{State: p.HealthState})
		if p.ConnectSampleCount > 0 && p.P95ConnectLatencyMs > 0 {
			item.ConnectSampleCount += p.ConnectSampleCount
			if p.P95ConnectLatencyMs > maxDisplayConnectP95 {
				maxDisplayConnectP95 = p.P95ConnectLatencyMs
			}
			// 只有 endpoint 自身样本量达到 DeriveSpeedTierFromConnectStats 的最小门槛并被判定为
			// SpeedTierSlow 时，才允许把渠道级 SpeedTier 标为慢速，避免样本不足的孤立异常
			// 被拿来判定渠道整体慢速。
			if p.ConnectSampleCount >= connectLatencyMinSamples && p.SpeedTier == SpeedTierSlow && p.P95ConnectLatencyMs > maxConnectP95 {
				maxConnectP95 = p.P95ConnectLatencyMs
			}
		}
		if p.FirstByteSampleCount > 0 && p.P95FirstByteLatencyMs > 0 {
			item.FirstByteSampleCount += p.FirstByteSampleCount
			firstByteP95Values = append(firstByteP95Values, p.P95FirstByteLatencyMs)
		}
	}

	if len(profiles) > 0 {
		item.AvgSuccessRate = totalSuccessRate / float64(len(profiles))
	}
	if maxConnectP95 > 0 {
		item.P95ConnectLatencyMs = maxConnectP95
		item.SpeedTier = string(SpeedTierSlow)
	} else if maxDisplayConnectP95 > 0 {
		// 展示层仍给出可读的 P95 数值，但 SpeedTier 保持默认（未设置），不会触发前端红色 chip。
		item.P95ConnectLatencyMs = maxDisplayConnectP95
	}
	if len(firstByteP95Values) > 0 {
		item.P95FirstByteLatencyMs = medianInt64(firstByteP95Values)
	}

	item.AggState = string(AggregateHealthState(states))
	return item
}

// buildChannelNameMap 从配置中构建 channelUID -> 名称 映射。
func buildChannelNameMap(cfg config.Config) map[string]string {
	names := make(map[string]string)

	type upstreamList struct {
		channels []config.UpstreamConfig
		prefix   string
	}
	lists := []upstreamList{
		{cfg.Upstream, "messages"},
		{cfg.ResponsesUpstream, "responses"},
		{cfg.GeminiUpstream, "gemini"},
		{cfg.ChatUpstream, "chat"},
		{cfg.ImagesUpstream, "images"},
		{cfg.VectorsUpstream, "vectors"},
	}

	for _, ul := range lists {
		for _, ch := range ul.channels {
			if ch.ChannelUID == "" {
				continue
			}
			name := ch.Name
			if name == "" {
				name = ul.prefix + "#" + strings.TrimPrefix(ch.ChannelUID, "ch_")
			}
			names[ch.ChannelUID] = name
		}
	}
	return names
}
