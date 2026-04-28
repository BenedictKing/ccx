package handlers

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type ChannelStatusConfigManager interface {
	SetChannelStatus(index int, status string) error
}

type ChannelStatusConfigManagerFunc func(index int, status string) error

func (f ChannelStatusConfigManagerFunc) SetChannelStatus(index int, status string) error {
	return f(index, status)
}

func NamedChannelStatusHandler(cfgManager ChannelStatusConfigManager, successMessage string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid channel ID"})
			return
		}

		var req struct {
			Status string `json:"status"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "Invalid request body"})
			return
		}

		if err := cfgManager.SetChannelStatus(id, req.Status); err != nil {
			if strings.Contains(err.Error(), "无效的上游索引") || strings.Contains(err.Error(), "invalid upstream index") {
				c.JSON(404, gin.H{"error": "Channel not found"})
			} else {
				c.JSON(400, gin.H{"error": err.Error()})
			}
			return
		}

		c.JSON(200, gin.H{"success": true, "message": successMessage, "status": req.Status})
	}
}

type PromotionConfigManager interface {
	SetChannelPromotion(index int, duration time.Duration) error
}

type PromotionConfigManagerFunc func(index int, duration time.Duration) error

func (f PromotionConfigManagerFunc) SetChannelPromotion(index int, duration time.Duration) error {
	return f(index, duration)
}

func NamedChannelPromotionHandler(cfgManager PromotionConfigManager, invalidIDMsg, invalidReqMsg, clearedMsg, setMsg string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(400, gin.H{"error": invalidIDMsg})
			return
		}

		var req struct {
			Duration int `json:"duration"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": invalidReqMsg})
			return
		}

		duration := time.Duration(req.Duration) * time.Second
		if err := cfgManager.SetChannelPromotion(id, duration); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		if req.Duration <= 0 {
			c.JSON(200, gin.H{"success": true, "message": clearedMsg})
			return
		}
		c.JSON(200, gin.H{"success": true, "message": setMsg, "duration": req.Duration})
	}
}
