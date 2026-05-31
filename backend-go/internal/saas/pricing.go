package saas

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// PricingPlan 定价计划（前端展示用）
type PricingPlan struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Price       int64  `json:"price"` // 分
	MaxRequests int64  `json:"maxRequests"`
	MaxTokens   int64  `json:"maxTokens"`
	MaxChannels int    `json:"maxChannels"`
	MaxAPIKeys  int    `json:"maxApiKeys"`
	Popular     bool   `json:"popular"`
}

// GetPricing 获取定价信息
func GetPricing() gin.HandlerFunc {
	return func(c *gin.Context) {
		plans := []PricingPlan{
			{
				ID:          "free",
				Name:        "免费版",
				Description: "适合个人开发者体验",
				Price:       0,
				MaxRequests: GetPlanLimits(PlanFree).MaxRequests,
				MaxTokens:   GetPlanLimits(PlanFree).MaxTokens,
				MaxChannels: GetPlanLimits(PlanFree).MaxChannels,
				MaxAPIKeys:  GetPlanLimits(PlanFree).MaxAPIKeys,
				Popular:     false,
			},
			{
				ID:          "pro",
				Name:        "专业版",
				Description: "适合专业开发者和小团队",
				Price:       GetPlanLimits(PlanPro).PriceMonthly,
				MaxRequests: GetPlanLimits(PlanPro).MaxRequests,
				MaxTokens:   GetPlanLimits(PlanPro).MaxTokens,
				MaxChannels: GetPlanLimits(PlanPro).MaxChannels,
				MaxAPIKeys:  GetPlanLimits(PlanPro).MaxAPIKeys,
				Popular:     true,
			},
			{
				ID:          "team",
				Name:        "团队版",
				Description: "适合团队和企业",
				Price:       GetPlanLimits(PlanTeam).PriceMonthly,
				MaxRequests: GetPlanLimits(PlanTeam).MaxRequests,
				MaxTokens:   GetPlanLimits(PlanTeam).MaxTokens,
				MaxChannels: GetPlanLimits(PlanTeam).MaxChannels,
				MaxAPIKeys:  GetPlanLimits(PlanTeam).MaxAPIKeys,
				Popular:     false,
			},
		}
		c.JSON(http.StatusOK, plans)
	}
}
