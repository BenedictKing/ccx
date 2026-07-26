package ratelimit

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ── 上游信号回调（供 Autopilot 限速发现器消费）──

// UpstreamSignalCallback 可选的信号回调，上游响应后触发。
// 由 main.go 注册，仅传递解析后的信号；默认 nil 不影响现有行为。
// endpointUID 和 metricsKey 由调用方（upstream_failover.go）在当前请求上下文中计算。
// channelName 为渠道可读名（upstream.Name），仅用于日志展示，可能为空。
// reason 携带 429 细分原因（如 account_rate_limit_exceeded），用于 AIMD 精确置信度。
var UpstreamSignalCallback func(endpointUID, metricsKey, serviceType, channelName string, isStream bool, latencyMs int64, headers http.Header, statusCode int, reason string)

// SetUpstreamSignalCallback 设置上游信号回调（main.go 调用）。
// 传 nil 可清除回调。
func SetUpstreamSignalCallback(cb func(endpointUID, metricsKey, serviceType, channelName string, isStream bool, latencyMs int64, headers http.Header, statusCode int, reason string)) {
	UpstreamSignalCallback = cb
}

// NotifySignal 若回调已注册，触发信号回调。
// endpointUID / metricsKey 由调用方在请求上下文中计算好后传入。
// channelName 为渠道可读名，仅用于日志展示，可能为空。
// reason 为 429 细分原因（非 429 传空串）。
// 失败安全：回调 panic 不影响主流程。
func NotifySignal(endpointUID, metricsKey, serviceType, channelName string, isStream bool, latencyMs int64, headers http.Header, statusCode int, reason string) {
	cb := UpstreamSignalCallback
	if cb == nil || headers == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[RateLimit-Signal] 回调 panic（已忽略）: %v", r)
		}
	}()
	cb(endpointUID, metricsKey, serviceType, channelName, isStream, latencyMs, headers, statusCode, reason)
}

// ApplyUpstreamHints 从上游响应头解析限流信息，动态调整 limiter 状态。
// 支持的 header（按 provider 分类）：
//
//	通用：    Retry-After（秒整数 或 HTTP-date）
//	Anthropic: anthropic-ratelimit-requests-remaining / -reset（RFC3339）
//	OpenAI:    x-ratelimit-remaining-requests / x-ratelimit-reset-requests（duration 如 "1s","6m0s"）
//
// headers 传入上游 resp.Header；statusCode 用于判断 429 冷却。
func (l *ChannelLimiter) ApplyUpstreamHints(headers http.Header, statusCode int, now time.Time) {
	if l == nil || headers == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// 429 或 5xx + Retry-After → cooldown（5xx 语义为服务暂时不可用，尊重上游退避指示）
	if statusCode == http.StatusTooManyRequests || statusCode >= 500 {
		if ra := parseRetryAfter(headers, now); ra > 0 {
			candidate := now.Add(ra)
			if candidate.After(l.cooldownUntil) {
				l.cooldownUntil = candidate
			}
		}
	}

	// Anthropic remaining/reset headers
	remaining := headers.Get("anthropic-ratelimit-requests-remaining")
	resetStr := headers.Get("anthropic-ratelimit-requests-reset")
	if remaining != "" && resetStr != "" {
		rem, err1 := strconv.ParseInt(remaining, 10, 64)
		resetTime, err2 := time.Parse(time.RFC3339, resetStr)
		if err1 == nil && err2 == nil && rem <= 1 && resetTime.After(now) {
			// 无剩余配额：在 reset 前完全冷却
			if resetTime.After(l.cooldownUntil) {
				l.cooldownUntil = resetTime
			}
		}
	}

	// OpenAI remaining/reset headers
	remaining = headers.Get("x-ratelimit-remaining-requests")
	resetStr = headers.Get("x-ratelimit-reset-requests")
	if remaining != "" && resetStr != "" {
		rem, err1 := strconv.ParseInt(remaining, 10, 64)
		if err1 == nil && rem <= 1 {
			if d := parseDuration(resetStr); d > 0 {
				candidate := now.Add(d)
				if candidate.After(l.cooldownUntil) {
					l.cooldownUntil = candidate
				}
			}
		}
	}
}

// parseRetryAfter 解析 Retry-After header，返回等待时长。
// 支持秒整数 和 HTTP-date 两种格式。
func parseRetryAfter(headers http.Header, now time.Time) time.Duration {
	ra := headers.Get("Retry-After")
	if ra == "" {
		return 0
	}
	// 尝试秒数
	if secs, err := strconv.ParseInt(ra, 10, 64); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	// 尝试 HTTP-date
	if t, err := time.Parse(http.TimeFormat, ra); err == nil {
		d := t.Sub(now)
		if d > 0 {
			return d
		}
	}
	return 0
}

// parseDuration 解析 OpenAI 风格的 duration 字符串，如 "1s", "6m0s", "30s"。
// 返回 0 表示无法解析。
func parseDuration(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return 0
}
