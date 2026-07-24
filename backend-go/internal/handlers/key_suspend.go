package handlers

import (
	"strconv"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

// SuspendAPIKey 手动暂停指定 API Key（设置 Enabled=false）
// POST /api/{type}/channels/:id/keys/suspend
// Body: {"apiKey": "sk-xxx"}
func SuspendAPIKey(cfgManager *config.ConfigManager, apiType string) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid channel ID"})
			return
		}

		var req struct {
			APIKey string `json:"apiKey"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.APIKey == "" {
			c.JSON(400, gin.H{"error": "apiKey is required"})
			return
		}

		if err := cfgManager.SuspendKey(apiType, id, req.APIKey); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, gin.H{
			"message": "Key 已暂停",
			"success": true,
		})
	}
}

// ResumeAPIKey 手动恢复指定 API Key（移除 Enabled 限制）
// POST /api/{type}/channels/:id/keys/resume
// Body: {"apiKey": "sk-xxx"}
func ResumeAPIKey(cfgManager *config.ConfigManager, apiType string) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid channel ID"})
			return
		}

		var req struct {
			APIKey string `json:"apiKey"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.APIKey == "" {
			c.JSON(400, gin.H{"error": "apiKey is required"})
			return
		}

		if err := cfgManager.ResumeKey(apiType, id, req.APIKey); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, gin.H{
			"message": "Key 已恢复",
			"success": true,
		})
	}
}
