package handlers

import (
	"strconv"
	"strings"

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

// DisableGroupModel 人工禁用目标 Key 所属配额组的模型。
func DisableGroupModel(cfgManager *config.ConfigManager, apiType string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid channel ID"})
			return
		}
		var req struct {
			APIKey string `json:"apiKey"`
			Model  string `json:"model"`
			Note   string `json:"note"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.APIKey) == "" || strings.TrimSpace(req.Model) == "" {
			c.JSON(400, gin.H{"error": "apiKey and model are required"})
			return
		}
		quotaGroup, affectedKeyCount, err := cfgManager.DisableGroupModel(apiType, id, req.APIKey, req.Model, req.Note)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{
			"success":          true,
			"quotaGroup":       quotaGroup,
			"model":            strings.TrimSpace(req.Model),
			"affectedKeyCount": affectedKeyCount,
		})
	}
}

// RestoreGroupModel 移除人工分组模型限制。
func RestoreGroupModel(cfgManager *config.ConfigManager, apiType string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid channel ID"})
			return
		}
		var req struct {
			APIKey     string `json:"apiKey"`
			QuotaGroup string `json:"quotaGroup"`
			Model      string `json:"model"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Model) == "" ||
			(strings.TrimSpace(req.APIKey) == "" && strings.TrimSpace(req.QuotaGroup) == "") {
			c.JSON(400, gin.H{"error": "model and apiKey or quotaGroup are required"})
			return
		}
		quotaGroup, affectedKeyCount, err := cfgManager.RestoreGroupModel(apiType, id, req.APIKey, req.QuotaGroup, req.Model)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{
			"success":          true,
			"quotaGroup":       quotaGroup,
			"model":            strings.TrimSpace(req.Model),
			"affectedKeyCount": affectedKeyCount,
		})
	}
}
