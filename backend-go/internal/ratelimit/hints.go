package ratelimit

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

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
