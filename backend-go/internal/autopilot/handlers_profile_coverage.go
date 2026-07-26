package autopilot

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// handlers_profile_coverage.go 注册模型画像覆盖率诊断的只读 API。
// 用途：手工映射表退役前，验证目标渠道的每个 endpoint 是否已具备
// 可用于自动决策的模型画像（Task 7 覆盖率门槛，§6.3）。
//
// 只读：本文件不包含任何写入/变更路径。

// RegisterProfileCoverageRoutes 注册画像覆盖率诊断只读 API。
func RegisterProfileCoverageRoutes(router gin.IRouter, mgr *Manager) {
	group := router.Group("/health-center")
	{
		group.GET("/profile-coverage", handleProfileCoverage(mgr))
	}
}

// handleProfileCoverage GET /api/health-center/profile-coverage
// 返回每个渠道的画像覆盖率诊断：endpoint 是否有画像、画像来源、是否探测成功、是否声明思考等级控制。
func handleProfileCoverage(mgr *Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		resp := computeProfileCoverage(mgr)
		c.JSON(http.StatusOK, resp)
	}
}
