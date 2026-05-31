package saas

import (
	"time"

	"github.com/google/uuid"
)

// Plan 套餐类型
type Plan string

const (
	PlanFree Plan = "free"
	PlanPro  Plan = "pro"
	PlanTeam Plan = "team"
)

// PlanLimits 套餐限制
type PlanLimits struct {
	MaxAPIKeys   int   // 最大 API Key 数
	MaxRequests  int64 // 每月最大请求数
	MaxTokens    int64 // 每月最大 Token 数
	MaxChannels  int   // 最大渠道数
	PriceMonthly int64 // 月费（分）
}

// GetPlanLimits 获取套餐限制
func GetPlanLimits(plan Plan) PlanLimits {
	switch plan {
	case PlanPro:
		return PlanLimits{
			MaxAPIKeys:   10,
			MaxRequests:  1_000_000,
			MaxTokens:    100_000_000,
			MaxChannels:  20,
			PriceMonthly: 999, // $9.99
		}
	case PlanTeam:
		return PlanLimits{
			MaxAPIKeys:   50,
			MaxRequests:  10_000_000,
			MaxTokens:    1_000_000_000,
			MaxChannels:  100,
			PriceMonthly: 4999, // $49.99
		}
	default: // Free
		return PlanLimits{
			MaxAPIKeys:   2,
			MaxRequests:  10_000,
			MaxTokens:    1_000_000,
			MaxChannels:  5,
			PriceMonthly: 0,
		}
	}
}

// User 用户模型
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Name         string    `json:"name"`
	APIKey       string    `json:"apiKey"`
	Plan         Plan      `json:"plan"`
	IsAdmin      bool      `json:"isAdmin"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// NewUser 创建新用户
func NewUser(email, passwordHash, name string) *User {
	now := time.Now()
	return &User{
		ID:           uuid.New().String(),
		Email:        email,
		PasswordHash: passwordHash,
		Name:         name,
		APIKey:       "sk-" + uuid.New().String() + uuid.New().String(),
		Plan:         PlanFree,
		IsAdmin:      false,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

// LoginRequest 登录请求
type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
}

// RegisterRequest 注册请求
type RegisterRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
	Name     string `json:"name" binding:"required,min=1,max=50"`
}

// AuthResponse 认证响应
type AuthResponse struct {
	Token string `json:"token"`
	User  *User  `json:"user"`
}

// UsageRecord 用量记录
type UsageRecord struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	Date      string    `json:"date"` // YYYY-MM-DD
	APICalls  int64     `json:"apiCalls"`
	TokensIn  int64     `json:"tokensIn"`
	TokensOut int64     `json:"tokensOut"`
	CreatedAt time.Time `json:"createdAt"`
}

// Subscription 订阅记录
type Subscription struct {
	ID                 string    `json:"id"`
	UserID             string    `json:"userId"`
	Plan               Plan      `json:"plan"`
	Status             string    `json:"status"` // active, canceled, past_due
	CurrentPeriodStart time.Time `json:"currentPeriodStart"`
	CurrentPeriodEnd   time.Time `json:"currentPeriodEnd"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// RegenerateKeyRequest 重新生成 API Key 请求
type RegenerateKeyRequest struct{}

// MeResponse 用户信息响应（包含 API Key 和用量）
type MeResponse struct {
	User       *User       `json:"user"`
	Usage      *UsageStats `json:"usage"`
	PlanLimits PlanLimits  `json:"planLimits"`
}

// UsageStats 用量统计
type UsageStats struct {
	APICalls  int64 `json:"apiCalls"`
	TokensIn  int64 `json:"tokensIn"`
	TokensOut int64 `json:"tokensOut"`
}

// UsersListResponse 用户列表响应
type UsersListResponse struct {
	Users []*User `json:"users"`
	Total int     `json:"total"`
}

// UpdateSubscriptionRequest 更新订阅请求
type UpdateSubscriptionRequest struct {
	Plan   string `json:"plan" binding:"required"`
	UserID string `json:"userId" binding:"required"`
}
