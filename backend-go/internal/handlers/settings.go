// Package handlers 提供 HTTP 处理器
package handlers

import (
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/gin-gonic/gin"
)

// GetCircuitBreaker 获取熔断器运行时配置
// getCurrent: 返回当前运行时生效的熔断器参数的函数
func GetCircuitBreaker(getCurrent func() metrics.CircuitBreakerParams, envCfg *config.EnvConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		params := getCurrent()
		c.JSON(200, gin.H{
			"windowSize":                   params.WindowSize,
			"failureThreshold":             params.FailureThreshold,
			"consecutiveFailuresThreshold": params.ConsecutiveFailuresThreshold,
			"requestTimeoutMs":             config.GetRuntimeRequestTimeoutMs(envCfg.RequestTimeout),
			"responseHeaderTimeoutMs":      config.GetRuntimeResponseHeaderTimeoutMs(envCfg.ResponseHeaderTimeout * 1000),
			"streamFirstContentTimeoutMs":  params.StreamFirstContentTimeoutMs,
			"streamInactivityTimeoutMs":    params.StreamInactivityTimeoutMs,
			"streamToolCallIdleTimeoutMs":  params.StreamToolCallIdleTimeoutMs,
		})
	}
}

// SetCircuitBreaker 更新熔断器运行时配置
func SetCircuitBreaker(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			WindowSize                   *int     `json:"windowSize"`
			FailureThreshold             *float64 `json:"failureThreshold"`
			ConsecutiveFailuresThreshold *int     `json:"consecutiveFailuresThreshold"`
			RequestTimeoutMs             *int     `json:"requestTimeoutMs"`
			ResponseHeaderTimeoutMs      *int     `json:"responseHeaderTimeoutMs"`
			StreamFirstContentTimeoutMs  *int     `json:"streamFirstContentTimeoutMs"`
			StreamInactivityTimeoutMs    *int     `json:"streamInactivityTimeoutMs"`
			StreamToolCallIdleTimeoutMs  *int     `json:"streamToolCallIdleTimeoutMs"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "请求格式无效"})
			return
		}

		// 参数校验
		if req.WindowSize != nil {
			if *req.WindowSize < 3 || *req.WindowSize > 100 {
				c.JSON(400, gin.H{"error": "windowSize 必须在 3-100 之间"})
				return
			}
		}
		if req.FailureThreshold != nil {
			if *req.FailureThreshold < 0.01 || *req.FailureThreshold > 1.0 {
				c.JSON(400, gin.H{"error": "failureThreshold 必须在 0.01-1.0 之间"})
				return
			}
		}
		if req.ConsecutiveFailuresThreshold != nil {
			if *req.ConsecutiveFailuresThreshold < 1 || *req.ConsecutiveFailuresThreshold > 100 {
				c.JSON(400, gin.H{"error": "consecutiveFailuresThreshold 必须在 1-100 之间"})
				return
			}
		}
		if req.RequestTimeoutMs != nil {
			if err := config.ValidateRuntimeRequestTimeoutMs(*req.RequestTimeoutMs); err != nil {
				c.JSON(400, gin.H{"error": err.Error()})
				return
			}
		}
		if req.ResponseHeaderTimeoutMs != nil {
			if err := config.ValidateRuntimeResponseHeaderTimeoutMs(*req.ResponseHeaderTimeoutMs); err != nil {
				c.JSON(400, gin.H{"error": err.Error()})
				return
			}
		}
		if req.StreamFirstContentTimeoutMs != nil {
			if *req.StreamFirstContentTimeoutMs < 5000 || *req.StreamFirstContentTimeoutMs > 300000 {
				c.JSON(400, gin.H{"error": "streamFirstContentTimeoutMs 必须在 5000-300000 之间"})
				return
			}
		}
		if req.StreamInactivityTimeoutMs != nil {
			if *req.StreamInactivityTimeoutMs < 1000 || *req.StreamInactivityTimeoutMs > 180000 {
				c.JSON(400, gin.H{"error": "streamInactivityTimeoutMs 必须在 1000-180000 之间"})
				return
			}
		}
		if req.StreamToolCallIdleTimeoutMs != nil {
			if *req.StreamToolCallIdleTimeoutMs < 30000 || *req.StreamToolCallIdleTimeoutMs > 300000 {
				c.JSON(400, gin.H{"error": "streamToolCallIdleTimeoutMs 必须在 30000-300000 之间"})
				return
			}
		}

		if err := cfgManager.SetCircuitBreakerConfig(config.CircuitBreakerConfig{
			WindowSize:                   req.WindowSize,
			FailureThreshold:             req.FailureThreshold,
			ConsecutiveFailuresThreshold: req.ConsecutiveFailuresThreshold,
			RequestTimeoutMs:             req.RequestTimeoutMs,
			ResponseHeaderTimeoutMs:      req.ResponseHeaderTimeoutMs,
			StreamFirstContentTimeoutMs:  req.StreamFirstContentTimeoutMs,
			StreamInactivityTimeoutMs:    req.StreamInactivityTimeoutMs,
			StreamToolCallIdleTimeoutMs:  req.StreamToolCallIdleTimeoutMs,
		}); err != nil {
			c.JSON(500, gin.H{"error": "保存配置失败"})
			return
		}

		// 返回更新后的完整配置
		updated := cfgManager.GetCircuitBreakerConfig()
		c.JSON(200, gin.H{
			"success": true,
			"circuitBreaker": gin.H{
				"windowSize":                   updated.WindowSize,
				"failureThreshold":             updated.FailureThreshold,
				"consecutiveFailuresThreshold": updated.ConsecutiveFailuresThreshold,
				"requestTimeoutMs":             updated.RequestTimeoutMs,
				"responseHeaderTimeoutMs":      updated.ResponseHeaderTimeoutMs,
				"streamFirstContentTimeoutMs":  updated.StreamFirstContentTimeoutMs,
				"streamInactivityTimeoutMs":    updated.StreamInactivityTimeoutMs,
				"streamToolCallIdleTimeoutMs":  updated.StreamToolCallIdleTimeoutMs,
			},
		})
	}
}
