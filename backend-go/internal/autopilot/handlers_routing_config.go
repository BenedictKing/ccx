package autopilot

import (
	"net/http"
	"os"
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

// ─── 请求/响应类型 ────────────────────────────────────────────────────────────────────

// ScenarioPresetView 场景预设参数摘要（含配置覆盖后的生效值）。
type ScenarioPresetView struct {
	Key               string `json:"key"`
	MinQualityTier    string `json:"minQualityTier"`
	CostPreference    string `json:"costPreference"`
	EffortFloor       string `json:"effortFloor,omitempty"`
	EffortCeil        string `json:"effortCeil,omitempty"`
	QualityBenefitCap string `json:"qualityBenefitCap,omitempty"`
}

// RoutingConfigResponse GET /smart-routing/config 响应体。
// 安全视图，只暴露只读字段，不暴露完整配置。
type RoutingConfigResponse struct {
	KillSwitchActive bool                 `json:"killSwitchActive"`
	CostPreference   string               `json:"costPreference,omitempty"`
	Scenario         string               `json:"scenario,omitempty"`
	ScenarioPresets  []ScenarioPresetView `json:"scenarioPresets,omitempty"`
	L2ProbeEnabled   bool                 `json:"l2ProbeEnabled,omitempty"`
}

// RoutingConfigUpdateRequest PUT /smart-routing/config 请求体。
// 只允许修改 rolloutPercent、costPreference 和 scenario。
type RoutingConfigUpdateRequest struct {
	RolloutPercent *int   `json:"rolloutPercent,omitempty"`
	CostPreference string `json:"costPreference,omitempty"`
	Scenario       string `json:"scenario,omitempty"`
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
// 更新智能路由配置。只允许修改 costPreference 和 scenario。
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
			if err := deps.CfgManager.SetCostPreferenceMode(req.CostPreference); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "保存价格偏向失败"})
				return
			}
		}

		// 校验 scenario（auto / daily_dev / hard_problem / background / batch_cheap）
		if req.Scenario != "" {
			if !isValidScenarioMode(req.Scenario) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 scenario，可选值: " + strings.Join(config.ValidScenarioModes, "/")})
				return
			}
			if err := deps.CfgManager.SetScenarioMode(req.Scenario); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "保存场景预设失败"})
				return
			}
		}

		if req.CostPreference == "" && req.Scenario == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "至少需要提供 costPreference 或 scenario"})
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

// isValidScenarioMode 校验场景模式是否合法（大小写不敏感，规范化后比较）。
func isValidScenarioMode(mode string) bool {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	for _, valid := range config.ValidScenarioModes {
		if normalized == valid {
			return true
		}
	}
	return false
}

func routingConfigResponse(cfg config.AutopilotRoutingConfig, killSwitchActive bool) RoutingConfigResponse {
	presets := BuiltinScenarioPresets(cfg.Scenario)
	views := make([]ScenarioPresetView, 0, len(presets))
	for _, key := range []string{"daily_dev", "hard_problem", "background", "batch_cheap"} {
		if p, ok := presets[key]; ok {
			view := ScenarioPresetView{
				Key:            p.Key,
				MinQualityTier: string(p.MinQualityTier),
				CostPreference: p.CostPreference,
				EffortFloor:    string(p.EffortFloor),
				EffortCeil:     string(p.EffortCeil),
			}
			if p.HasBenefitCap {
				view.QualityBenefitCap = string(p.QualityBenefitCap)
			}
			views = append(views, view)
		}
	}

	scenario := cfg.Scenario.Mode
	if scenario == "" {
		scenario = ScenarioModeAuto
	}
	return RoutingConfigResponse{
		KillSwitchActive: killSwitchActive,
		CostPreference:   cfg.CostPreference.Mode,
		Scenario:         scenario,
		ScenarioPresets:  views,
		L2ProbeEnabled:   cfg.HealthCheck.L2ProbeEnabled,
	}
}

// isTruthyEnv 判断环境变量值是否为真。
func isTruthyEnv(val string) bool {
	v := strings.ToLower(strings.TrimSpace(val))
	return v == "true" || v == "1" || v == "yes" || v == "on"
}
