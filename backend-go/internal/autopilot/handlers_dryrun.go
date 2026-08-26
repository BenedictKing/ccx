package autopilot

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ── Dry-Run API（设计 §4.6 + §9）──

// RegisterDryRunRoutes 注册 SmartRouter dry-run API 到给定路由组。
func RegisterDryRunRoutes(router gin.IRouter, smartRouter *SmartRouter) {
	handler := handleDryRunRoute(smartRouter)
	// 设计文档标准路径；挂载到 /api group 后为 /api/smart-routing/diagnose。
	router.POST("/smart-routing/diagnose", handler)
	// 兼容早期内部路径，避免已有调用方立即失效。
	router.POST("/route-dryrun", handler)
}

// DryRunRequest dry-run 请求体。
type DryRunRequest struct {
	// Model 请求的目标模型名。
	Model string `json:"model" binding:"required"`
	// ChannelKind messages | chat | responses | gemini | images | vectors。
	ChannelKind string `json:"channelKind" binding:"required"`
	// Operation completion | count_tokens | image_generation | embedding 等。
	Operation string `json:"operation"`
	// AgentRole main | subagent | ""。
	AgentRole string `json:"agentRole"`
	// AgentType codex_subagent | claude_code_subagent | ""。
	AgentType string `json:"agentType"`
	// HasImage 是否包含图片。
	HasImage bool `json:"hasImage"`
	// HasDocument 是否包含文档附件（PDF 等）。
	HasDocument bool `json:"hasDocument"`
	// EstTokens 估算输入 token 数。
	EstTokens int `json:"estTokens"`
	// VisionNeed 是否需要识图。
	VisionNeed bool `json:"visionNeed"`
	// DocumentNeed 是否需要文档（PDF 等）理解。
	DocumentNeed bool `json:"documentNeed"`
	// ImageGenNeed 是否需要原生生图端点。
	ImageGenNeed bool `json:"imageGenNeed"`
	// EmbeddingNeed 是否需要原生 embedding 端点。
	EmbeddingNeed bool `json:"embeddingNeed"`
	// ToolUseNeed 是否需要工具调用。
	ToolUseNeed bool `json:"toolUseNeed"`
	// ReasoningNeed 是否需要推理。
	ReasoningNeed bool `json:"reasoningNeed"`
	// ContextNeed 估算输入 token 数；输出上限由真实 scheduler 独立校验。
	ContextNeed int `json:"contextNeed"`
}

// DryRunResponse dry-run 响应体。
type DryRunResponse struct {
	// Plan 路由计划。
	Plan *RoutingPlan `json:"plan"`
	// Mode 当前生效模式。
	Mode string `json:"mode"`
	// Message 提示信息。
	Message string `json:"message,omitempty"`
}

// handleDryRunRoute POST /api/smart-routing/diagnose（兼容 /api/route-dryrun）。
// 根据请求特征计算路由计划，返回候选分数明细，不发真实请求。
func handleDryRunRoute(smartRouter *SmartRouter) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req DryRunRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误: " + err.Error()})
			return
		}

		if smartRouter == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "SmartRouter 未初始化"})
			return
		}

		// 检查 kill switch
		cfg := smartRouter.ConfigManager().GetConfig()
		autopilotCfg := cfg.AutopilotRouting
		mode := autopilotCfg.EffectiveRoutingMode()
		if autopilotCfg.KillSwitch {
			c.JSON(http.StatusOK, DryRunResponse{
				Plan:    nil,
				Mode:    mode,
				Message: "智能路由已关闭（mode=off 或 kill switch 已启用）",
			})
			return
		}

		// 与真实协议入口复用同一画像构建器，保证质量档、上下文默认值、
		// 图片能力需求和任务分类的推导语义一致。场景预设与请求级偏好头
		// 同样走 ResolveScenarioPreset 的生效链（头 > 全局 scenario.mode）。
		profile := BuildRequestProfile(RequestProfileFeatures{
			Model:         req.Model,
			ChannelKind:   req.ChannelKind,
			Operation:     req.Operation,
			AgentRole:     req.AgentRole,
			AgentType:     req.AgentType,
			HasImage:      req.HasImage,
			HasDocument:   req.HasDocument,
			EstTokens:     req.EstTokens,
			VisionNeed:    req.VisionNeed,
			DocumentNeed:  req.DocumentNeed,
			ImageGenNeed:  req.ImageGenNeed,
			EmbeddingNeed: req.EmbeddingNeed,
			ToolUseNeed:   req.ToolUseNeed,
			ReasoningNeed: req.ReasoningNeed,
			ContextNeed:   req.ContextNeed,
			ScenarioCfg:   cfg.AutopilotRouting.Scenario,

			RoutingScenarioHeader: c.GetHeader("X-Routing-Scenario"),
			CostPreferenceHeader:  c.GetHeader("X-Cost-Preference"),
		})

		plan := smartRouter.BuildPlan(&profile)
		c.JSON(http.StatusOK, DryRunResponse{
			Plan: plan,
			Mode: mode,
		})
	}
}
