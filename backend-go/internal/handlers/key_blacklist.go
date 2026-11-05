package handlers

import (
	"strconv"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

// RestoreBlacklistedKey 恢复被拉黑的 API Key
// POST /api/{type}/channels/:id/keys/restore
// Body: {"apiKey": "sk-xxx"}
func RestoreBlacklistedKey(cfgManager *config.ConfigManager, apiType string) gin.HandlerFunc {
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

		if err := cfgManager.RestoreKey(apiType, id, req.APIKey); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, gin.H{
			"message": "Key 已恢复",
			"success": true,
		})
	}
}

// RestoreKeyModel 手动移除 (Key, 模型) 组合级限制
// POST /api/{type}/channels/:id/keys/restore-model
// Body: {"apiKey": "sk-xxx", "model": "gpt-5.6-sol"}
func RestoreKeyModel(cfgManager *config.ConfigManager, apiType string) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid channel ID"})
			return
		}

		var req struct {
			APIKey string `json:"apiKey"`
			Model  string `json:"model"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.APIKey == "" || req.Model == "" {
			c.JSON(400, gin.H{"error": "apiKey and model are required"})
			return
		}

		if err := cfgManager.RestoreKeyModel(apiType, id, req.APIKey, req.Model); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		c.JSON(200, gin.H{
			"message": "(Key, 模型) 限制已移除",
			"success": true,
		})
	}
}
