package saas

import (
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// EnsureAdmin 确保默认管理员用户存在
func EnsureAdmin(store *Store, email, password string) {
	var count int
	err := store.db.QueryRow("SELECT COUNT(*) FROM users WHERE is_admin = 1").Scan(&count)
	if err != nil {
		log.Printf("[SaaS-Admin] 检查管理员失败: %v", err)
		return
	}
	if count > 0 {
		log.Printf("[SaaS-Admin] 管理员已存在，跳过创建")
		return
	}

	passwordHash, err := hashPassword(password)
	if err != nil {
		log.Printf("[SaaS-Admin] 创建管理员密码哈希失败: %v", err)
		return
	}

	now := time.Now()
	user := NewUser(email, passwordHash, "Admin")
	user.IsAdmin = true
	user.Plan = PlanTeam

	_, err = store.db.Exec(
		`INSERT INTO users (id, email, password_hash, name, api_key, plan, is_admin, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		user.ID, user.Email, user.PasswordHash, user.Name, user.APIKey, user.Plan, 1, now.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	if err != nil {
		log.Printf("[SaaS-Admin] 创建管理员失败: %v", err)
		return
	}

	log.Printf("[SaaS-Admin] 默认管理员已创建: %s", email)
}

// ListUsersHandler 管理员查询用户列表
func ListUsersHandler(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		limitStr := c.DefaultQuery("limit", "20")
		offsetStr := c.DefaultQuery("offset", "0")

		limit, err := strconv.Atoi(limitStr)
		if err != nil || limit < 1 || limit > 100 {
			limit = 20
		}
		offset, err := strconv.Atoi(offsetStr)
		if err != nil || offset < 0 {
			offset = 0
		}

		users, total, err := store.ListUsers(limit, offset)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询失败"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"users": users, "total": total})
	}
}

// UpdateUserPlanHandler 管理员更新用户套餐
func UpdateUserPlanHandler(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.Param("id")
		var req struct {
			Plan string `json:"plan" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误"})
			return
		}

		// 校验套餐
		switch req.Plan {
		case "free", "pro", "team":
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的套餐类型，可选: free, pro, team"})
			return
		}

		if err := store.UpdateUserPlan(userID, req.Plan); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新失败"})
			return
		}

		log.Printf("[SaaS-Admin] 管理员更新用户 %s 套餐为 %s", userID, req.Plan)
		c.JSON(http.StatusOK, gin.H{"message": "更新成功"})
	}
}

// DeleteUserHandler 管理员删除用户
func DeleteUserHandler(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.Param("id")

		if err := store.DeleteUserByID(userID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "删除失败"})
			return
		}

		log.Printf("[SaaS-Admin] 管理员删除了用户 %s", userID)
		c.JSON(http.StatusOK, gin.H{"message": "删除成功"})
	}
}
