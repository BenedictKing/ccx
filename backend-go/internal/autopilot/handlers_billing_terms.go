package autopilot

import (
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type billingTermsPatchRequest struct {
	PaymentAmount   OptionalFloat64 `json:"paymentAmount"`
	PaymentUnit     *string         `json:"paymentUnit"`
	CreditAmount    OptionalFloat64 `json:"creditAmount"`
	CreditUnit      *string         `json:"creditUnit"`
	ExpectedVersion *uint64         `json:"expectedVersion,omitempty"`
}

type billingTermsResponse struct {
	PaymentAmount *float64 `json:"paymentAmount,omitempty"`
	PaymentUnit   string   `json:"paymentUnit,omitempty"`
	CreditAmount  *float64 `json:"creditAmount,omitempty"`
	CreditUnit    string   `json:"creditUnit,omitempty"`
	Version       uint64   `json:"version"`
	Preview       string   `json:"preview"`
}

func RegisterBillingTermsRoutes(router gin.IRouter, store *SubscriptionStore) {
	if router == nil || store == nil {
		return
	}
	router.PATCH("/subscriptions/:uid/billing-terms", handlePatchBillingTerms(store))
}

func handlePatchBillingTerms(store *SubscriptionStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := strings.TrimSpace(c.Param("uid"))
		if uid == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "uid 不能为空"})
			return
		}
		if store.Get(uid) == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "订阅不存在: " + uid})
			return
		}

		var req billingTermsPatchRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数无效: " + err.Error()})
			return
		}

		termsSpecified := req.PaymentAmount.Present || req.PaymentUnit != nil || req.CreditAmount.Present || req.CreditUnit != nil
		if !termsSpecified {
			c.JSON(http.StatusBadRequest, gin.H{"error": "至少提供一项 billing terms 字段"})
			return
		}

		paymentReset := req.PaymentAmount.Present && !req.PaymentAmount.Valid
		creditReset := req.CreditAmount.Present && !req.CreditAmount.Valid
		if paymentReset != creditReset || (req.PaymentUnit == nil) != (req.CreditUnit == nil) || paymentReset != (req.PaymentUnit != nil && strings.TrimSpace(*req.PaymentUnit) == "") {
			// 留给下方统一判定，避免遗漏。
		}

		setCount := boolCount(req.PaymentAmount.Present, req.PaymentUnit != nil, req.CreditAmount.Present, req.CreditUnit != nil)
		if setCount != 4 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "paymentAmount/paymentUnit/creditAmount/creditUnit 必须同时设置或同时清空"})
			return
		}

		resetAll := !req.PaymentAmount.Valid && !req.CreditAmount.Valid && req.PaymentUnit != nil && req.CreditUnit != nil && strings.TrimSpace(*req.PaymentUnit) == "" && strings.TrimSpace(*req.CreditUnit) == ""
		if !resetAll {
			if !req.PaymentAmount.Valid || !req.CreditAmount.Valid {
				c.JSON(http.StatusBadRequest, gin.H{"error": "金额字段必须同时为数值或同时为 null 清空"})
				return
			}
			if !isFinitePositiveValue(req.PaymentAmount.Value) || !isFinitePositiveValue(req.CreditAmount.Value) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "金额必须是有限且大于 0 的数值"})
				return
			}
		}

		var paymentUnit, creditUnit string
		if req.PaymentUnit != nil {
			paymentUnit = strings.ToUpper(strings.TrimSpace(*req.PaymentUnit))
			creditUnit = strings.ToUpper(strings.TrimSpace(*req.CreditUnit))
		}
		if !resetAll {
			if paymentUnit == "" || creditUnit == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "单位不能为空"})
				return
			}
		}

		err := store.Patch(uid, req.ExpectedVersion, func(profile *SubscriptionProfile) error {
			if resetAll {
				profile.PaymentAmount = nil
				profile.PaymentUnit = ""
				profile.CreditAmount = nil
				profile.CreditUnit = ""
				return nil
			}
			payment := req.PaymentAmount.Value
			credit := req.CreditAmount.Value
			profile.PaymentAmount = &payment
			profile.PaymentUnit = paymentUnit
			profile.CreditAmount = &credit
			profile.CreditUnit = creditUnit
			return nil
		})
		if err != nil {
			if isSubscriptionVersionConflict(err) {
				c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
				return
			}
			if isSubscriptionNotFound(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		updated := store.Get(uid)
		response := billingTermsResponse{Version: updated.Version, Preview: billingTermsPreview(updated)}
		if updated.PaymentAmount != nil {
			response.PaymentAmount = cloneFloat64Ptr(updated.PaymentAmount)
			response.PaymentUnit = updated.PaymentUnit
			response.CreditAmount = cloneFloat64Ptr(updated.CreditAmount)
			response.CreditUnit = updated.CreditUnit
		}
		c.JSON(http.StatusOK, response)
	}
}

func billingTermsPreview(profile *SubscriptionProfile) string {
	if profile == nil || profile.PaymentAmount == nil || profile.CreditAmount == nil || strings.TrimSpace(profile.PaymentUnit) == "" || strings.TrimSpace(profile.CreditUnit) == "" {
		return "未配置账期条款"
	}
	return fmt.Sprintf("支付 %.4g %s，获得 %.4g %s", *profile.PaymentAmount, profile.PaymentUnit, *profile.CreditAmount, profile.CreditUnit)
}

func isFinitePositiveValue(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
}

func boolCount(values ...bool) int {
	total := 0
	for _, v := range values {
		if v {
			total++
		}
	}
	return total
}

func isSubscriptionVersionConflict(err error) bool {
	return err != nil && strings.Contains(err.Error(), "version 冲突")
}

func isSubscriptionNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "不存在")
}
