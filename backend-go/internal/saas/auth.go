package saas

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// JWTSecret JWT 签名密钥（从环境变量读取）
var JWTSecret []byte

// SetJWTSecret 设置 JWT 密钥
func SetJWTSecret(secret string) {
	if secret == "" {
		JWTSecret = []byte("ccx-saas-jwt-secret-change-me")
		log.Println("[SaaS-Auth] 警告: 使用默认 JWT 密钥，请通过 SAAS_JWT_SECRET 环境变量设置")
	} else {
		JWTSecret = []byte(secret)
	}
}

// JWTClaims JWT 声明
type JWTClaims struct {
	UserID string `json:"userId"`
	Email  string `json:"email"`
	Admin  bool   `json:"admin"`
	jwt.RegisteredClaims
}

// generateToken 生成 JWT Token
func generateToken(userID, email string, isAdmin bool) (string, error) {
	claims := JWTClaims{
		UserID: userID,
		Email:  email,
		Admin:  isAdmin,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "ccx-saas",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(JWTSecret)
}

// hashPassword 哈希密码
func hashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("密码哈希失败: %w", err)
	}
	return string(bytes), nil
}

// checkPassword 校验密码
func checkPassword(password, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// Register 用户注册
func Register(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req RegisterRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
			return
		}

		// 检查邮箱是否已注册
		var exists bool
		err := store.db.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE email = ?)", req.Email).Scan(&exists)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
			return
		}
		if exists {
			c.JSON(http.StatusConflict, gin.H{"error": "该邮箱已注册"})
			return
		}

		// 创建用户
		passwordHash, err := hashPassword(req.Password)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
			return
		}

		user := NewUser(req.Email, passwordHash, req.Name)
		now := time.Now()

		_, err = store.db.Exec(
			`INSERT INTO users (id, email, password_hash, name, api_key, plan, is_admin, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			user.ID, user.Email, user.PasswordHash, user.Name, user.APIKey, user.Plan, boolToInt(user.IsAdmin), now.Format(time.RFC3339), now.Format(time.RFC3339),
		)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "注册失败: " + err.Error()})
			return
		}

		// 生成 Token
		token, err := generateToken(user.ID, user.Email, user.IsAdmin)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Token 生成失败"})
			return
		}

		log.Printf("[SaaS-Register] 新用户注册: %s (%s)", user.Email, user.ID)
		c.JSON(http.StatusCreated, AuthResponse{Token: token, User: user})
	}
}

// Login 用户登录
func Login(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req LoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "参数错误: " + err.Error()})
			return
		}

		// 查询用户
		user, err := getUserByEmail(store.db, req.Email)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "邮箱或密码错误"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
			return
		}

		// 校验密码
		if !checkPassword(req.Password, user.PasswordHash) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "邮箱或密码错误"})
			return
		}

		// 生成 Token
		token, err := generateToken(user.ID, user.Email, user.IsAdmin)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Token 生成失败"})
			return
		}

		user.PasswordHash = "" // 不返回密码
		log.Printf("[SaaS-Login] 用户登录: %s (%s)", user.Email, user.ID)
		c.JSON(http.StatusOK, AuthResponse{Token: token, User: user})
	}
}

// Me 获取当前用户信息
func Me(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("userID")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未认证"})
			return
		}

		user, err := getUserByID(store.db, userID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
			return
		}
		user.PasswordHash = ""
		c.JSON(http.StatusOK, user)
	}
}

// GetUserByAPIKey 通过 API Key 获取用户
func GetUserByAPIKey(store *Store, apiKey string) (*User, error) {
	return getUserByAPIKey(store.db, apiKey)
}

// getUserByEmail 通过邮箱获取用户
func getUserByEmail(db *sql.DB, email string) (*User, error) {
	user := &User{}
	var createdAt, updatedAt string
	err := db.QueryRow(
		`SELECT id, email, password_hash, name, api_key, plan, is_admin, created_at, updated_at FROM users WHERE email = ?`,
		email,
	).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.APIKey, &user.Plan, &user.IsAdmin, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	user.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	user.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return user, nil
}

// getUserByID 通过 ID 获取用户
func getUserByID(db *sql.DB, id string) (*User, error) {
	user := &User{}
	var createdAt, updatedAt string
	err := db.QueryRow(
		`SELECT id, email, password_hash, name, api_key, plan, is_admin, created_at, updated_at FROM users WHERE id = ?`,
		id,
	).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.APIKey, &user.Plan, &user.IsAdmin, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	user.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	user.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return user, nil
}

// getUserByAPIKey 通过 API Key 获取用户
func getUserByAPIKey(db *sql.DB, apiKey string) (*User, error) {
	user := &User{}
	var createdAt, updatedAt string
	err := db.QueryRow(
		`SELECT id, email, password_hash, name, api_key, plan, is_admin, created_at, updated_at FROM users WHERE api_key = ?`,
		apiKey,
	).Scan(&user.ID, &user.Email, &user.PasswordHash, &user.Name, &user.APIKey, &user.Plan, &user.IsAdmin, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	user.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	user.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return user, nil
}

// generateAPIKey 生成 sk- 开头的 API Key
func generateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成 API Key 失败: %w", err)
	}
	return "sk-" + hex.EncodeToString(b), nil
}

// RegenerateAPIKey 重新生成 API Key（需 JWT 认证）
func RegenerateAPIKey(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("userID")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未认证"})
			return
		}

		newKey, err := generateAPIKey()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
			return
		}

		_, err = store.db.Exec("UPDATE users SET api_key = ?, updated_at = ? WHERE id = ?", newKey, time.Now().Format(time.RFC3339), userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "更新 API Key 失败: " + err.Error()})
			return
		}

		log.Printf("[SaaS-APIKey] 用户 %s 重新生成了 API Key", userID)
		c.JSON(http.StatusOK, gin.H{"api_key": newKey})
	}
}

// GetAPIKeyInfo 获取当前 API Key 信息（需 JWT 认证）
func GetAPIKeyInfo(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("userID")
		if userID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未认证"})
			return
		}

		user, err := getUserByID(store.db, userID)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "用户不存在"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"api_key": user.APIKey})
	}
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
