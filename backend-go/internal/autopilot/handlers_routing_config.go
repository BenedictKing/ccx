package autopilot

import (
	"net/http"
	"os"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

// ─── 请求/响应类型 ────────────────────────────────────────────────────────────────────

// RoutingConfigResponse GET /smart-routing/config 响应体。
// 安全视图，只暴露只读字段，不暴露完整配置。
type RoutingConfigResponse struct {
	KillSwitchActive bool   `json:"killSwitchActive"`
	CostPreference   string `json:"costPreference,omitempty"`
	L2ProbeEnabled   bool   `json:"l2ProbeEnabled,omitempty"`
}

// RoutingConfigUpdateRequest PUT /smart-routing/config 请求体。
// 只允许修改 rolloutPercent 和 costPreference。
type RoutingConfigUpdateRequest struct {
	RolloutPercent *int   `json:"rolloutPercent,omitempty"`
	CostPreference string `json:"costPreference,omitempty"`
}

// ─── 路由注册 ─────────────────────────────────────────────────────────────────────────

// RoutingConfigDeps 智能路由配置路由的依赖注入。
type RoutingConfigDeps struct {
	CfgManager *config.ConfigManager
	TraceStore *TraceStore
}

// RegisterRoutingConfigRoutes 注册智能路由配置 API 路由。
// 路由挂载到 /api/smart-routing/config。
func RegisterRoutingConfigRoutes(group *gin.RouterGroup, deps *RoutingConfigDeps) {
	group.GET("/smart-routing/config", handleGetRoutingConfig(deps))
	group.PUT("/smart-routing/config", handleUpdateRoutingConfig(deps))
}

// ─── 处理函数 ─────────────────────────────────────────────────────────────────────────

// handleGetRoutingConfig GET /api/smart-routing/config
// 返回当前智能路由配置的安全视图。
func handleGetRoutingConfig(deps *RoutingConfigDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := deps.CfgManager.GetAutopilotRouting()

		// 综合判断 killSwitch：config 字段 OR 环境变量
		envKillSwitch := false
		if envVal := os.Getenv("AUTOPILOT_KILL_SWITCH"); isTruthyEnv(envVal) {
			envKillSwitch = true
		}
		killSwitchActive := cfg.KillSwitch || envKillSwitch

		c.JSON(http.StatusOK, routingConfigResponse(cfg, killSwitchActive))
	}
}

// handleUpdateRoutingConfig PUT /api/smart-routing/config
// 更新智能路由配置。只允许修改 mode 和 costPreference。
func handleUpdateRoutingConfig(deps *RoutingConfigDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req RoutingConfigUpdateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的请求体"})
			return
		}

		// 校验 costPreference
		if req.CostPreference != "" {
			validCP := map[string]bool{"quality_first": true, "balanced": true, "cost_first": true}
			if !validCP[strings.ToLower(req.CostPreference)] {
				c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 costPreference，可选值: quality_first/balanced/cost_first"})
				return
			}
			cpConfig := config.CostPreferenceConfig{Mode: req.CostPreference}
			if err := deps.CfgManager.SetCostPreference(cpConfig); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "保存价格偏向失败"})
				return
			}
		}

		if req.CostPreference == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "至少需要提供 costPreference"})
			return
		}

		// 返回更新后的安全视图
		cfg := deps.CfgManager.GetAutopilotRouting()
		envKillSwitch := false
		if envVal := os.Getenv("AUTOPILOT_KILL_SWITCH"); isTruthyEnv(envVal) {
			envKillSwitch = true
		}

		c.JSON(http.StatusOK, routingConfigResponse(cfg, cfg.KillSwitch || envKillSwitch))
	}
}

func routingConfigResponse(cfg config.AutopilotRoutingConfig, killSwitchActive bool) RoutingConfigResponse {
	return RoutingConfigResponse{
		KillSwitchActive: killSwitchActive,
		CostPreference:   cfg.CostPreference.Mode,
		L2ProbeEnabled:   cfg.HealthCheck.L2ProbeEnabled,
	}
}

// isTruthyEnv 判断环境变量值是否为真。
func isTruthyEnv(val string) bool {
	v := strings.ToLower(strings.TrimSpace(val))
	return v == "true" || v == "1" || v == "yes" || v == "on"
}
