package autopilot

import (
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

// ─── 请求/响应类型 ────────────────────────────────────────────────────────────────────

// RoutingConfigResponse GET /smart-routing/config 响应体。
// 安全视图，只暴露只读字段，不暴露完整配置。
type RoutingConfigResponse struct {
	Mode             string               `json:"mode"`
	KillSwitchActive bool                 `json:"killSwitchActive"`
	RolloutPercent   int                  `json:"rolloutPercent"`
	ControlPercent   int                  `json:"controlPercent"`
	ReleaseID        string               `json:"releaseId"`
	CostPreference   string               `json:"costPreference,omitempty"`
	L2ProbeEnabled   bool                 `json:"l2ProbeEnabled,omitempty"`
	Readiness        *AutoReadinessReport `json:"readiness,omitempty"`
}

// RoutingConfigUpdateRequest PUT /smart-routing/config 请求体。
// 只允许修改 mode、rolloutPercent 和 costPreference。
type RoutingConfigUpdateRequest struct {
	Mode           string `json:"mode,omitempty"`
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

		c.JSON(http.StatusOK, routingConfigResponse(cfg, killSwitchActive, deps.TraceStore))
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

		// 校验 mode
		if req.Mode != "" {
			previousMode := deps.CfgManager.GetEffectiveRoutingMode()
			normalizedMode := strings.ToLower(req.Mode)
			validModes := map[string]bool{"off": true, "shadow": true, "assist": true, "auto": true, "active": true}
			if !validModes[normalizedMode] {
				c.JSON(http.StatusBadRequest, gin.H{"error": "无效的 mode，可选值: off/shadow/assist/auto/active"})
				return
			}
			if normalizedMode == config.AutopilotModeAuto && deps.CfgManager.GetEffectiveRoutingMode() != config.AutopilotModeAuto {
				readiness := deps.TraceStore.EvaluateAutoReadiness(time.Now())
				if !readiness.Ready {
					c.JSON(http.StatusConflict, gin.H{
						"error":     "auto 模式尚未达到安全上线门槛",
						"readiness": readiness,
					})
					return
				}
			}
			if err := deps.CfgManager.SetAutopilotRoutingMode(req.Mode); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "保存路由模式失败"})
				return
			}
			// 人工模式选择是显式接管信号。即使仍选择 assist，也要覆盖先前的
			// auto->assist 安全事件，防止后台恢复任务违背用户意图自动升回 auto。
			if deps.TraceStore != nil {
				event := AutoSafetyEvent{
					FromMode:  previousMode,
					ToMode:    deps.CfgManager.GetEffectiveRoutingMode(),
					Reasons:   []string{"manual_mode_change"},
					CreatedAt: time.Now().UTC(),
				}
				if err := deps.TraceStore.RecordAutoSafetyEvent(event); err != nil {
					log.Printf("[Autopilot-Config] 警告: 人工模式变更事件落盘失败: %v", err)
				}
			}
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

		if req.Mode == "" && req.CostPreference == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "至少需要提供 mode 或 costPreference"})
			return
		}

		// 返回更新后的安全视图
		cfg := deps.CfgManager.GetAutopilotRouting()
		envKillSwitch := false
		if envVal := os.Getenv("AUTOPILOT_KILL_SWITCH"); isTruthyEnv(envVal) {
			envKillSwitch = true
		}

		c.JSON(http.StatusOK, routingConfigResponse(cfg, cfg.KillSwitch || envKillSwitch, deps.TraceStore))
	}
}

func routingConfigResponse(cfg config.AutopilotRoutingConfig, killSwitchActive bool, store *TraceStore) RoutingConfigResponse {
	resp := RoutingConfigResponse{
		Mode:             cfg.EffectiveRoutingMode(),
		KillSwitchActive: killSwitchActive,
		RolloutPercent:   cfg.RolloutPercent,
		ControlPercent:   cfg.ControlPercent,
		ReleaseID:        cfg.ReleaseID,
		CostPreference:   cfg.CostPreference.Mode,
		L2ProbeEnabled:   cfg.HealthCheck.L2ProbeEnabled,
	}
	if store != nil {
		readiness := store.EvaluateAutoReadiness(time.Now())
		resp.Readiness = &readiness
	}
	return resp
}

// isTruthyEnv 判断环境变量值是否为真。
func isTruthyEnv(val string) bool {
	v := strings.ToLower(strings.TrimSpace(val))
	return v == "true" || v == "1" || v == "yes" || v == "on"
}
