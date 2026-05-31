package saas

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// GetMyUsage 获取用户本月用量（需 JWT 认证）
func GetMyUsage(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("userID")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未认证"})
			return
		}

		now := time.Now()
		yearMonth := now.Format("2006-01")

		usage, err := store.GetUserUsage(userID, yearMonth)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询用量失败"})
			return
		}

		user, err := store.GetUserByID(userID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
			return
		}

		limits := GetPlanLimits(user.Plan)

		c.JSON(http.StatusOK, gin.H{
			"usage":      usage,
			"planLimits": limits,
		})
	}
}

// GetCurrentMonthUsage 获取当前月用量
func GetCurrentMonthUsage(store *Store) gin.HandlerFunc {
	return GetMyUsage(store)
}
