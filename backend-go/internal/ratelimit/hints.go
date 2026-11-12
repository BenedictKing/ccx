package ratelimit

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxUpstreamResetDelay 上游重置指示的冷却上限。
// 异常值（如伪造的 Retry-After: 86400 或周窗口 reset 时间戳）不该冻结渠道一整天，
// 超过上限的指示截断到上限；更长的硬隔离由持久限制/熔断层负责。
const maxUpstreamResetDelay = time.Hour

// ApplyUpstreamHints 从上游响应头解析限流信息，动态调整 limiter 状态。
//
// 429/5xx 时按三族重置头取最晚有效值冷却（蓝本 GPT-Load v2 ParseRateLimitReset）：
//
//	通用：      Retry-After（秒整数 或 HTTP-date）
//	Anthropic: anthropic-ratelimit-*-reset（RFC3339 / duration / epoch）
//	OpenAI:    x-ratelimit-reset / x-ratelimit-reset-*（duration / epoch / RFC3339）
//
// 所有冷却指示统一截断到 now+1h，只延长不缩短。
// 非 429 响应上仍保留 remaining 耗尽的探测（anthropic requests remaining<=1、
// openai remaining-requests<=1），同样受 1h 上限约束。
func (l *ChannelLimiter) ApplyUpstreamHints(headers http.Header, statusCode int, now time.Time) {
	if l == nil || headers == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// 429 或 5xx + 重置头 → cooldown（5xx 语义为服务暂时不可用，尊重上游退避指示）
	if statusCode == http.StatusTooManyRequests || statusCode >= 500 {
		if until, ok := latestUpstreamReset(headers, now); ok {
			l.extendCooldownUntil(until, now)
		}
	}

	// Anthropic remaining/reset headers（非 429 时也可能带出耗尽信号）
	remaining := headers.Get("anthropic-ratelimit-requests-remaining")
	resetStr := headers.Get("anthropic-ratelimit-requests-reset")
	if remaining != "" && resetStr != "" {
		rem, err1 := strconv.ParseInt(remaining, 10, 64)
		resetTime, ok := parseResetValue(resetStr, now)
		if err1 == nil && ok && rem <= 1 && resetTime.After(now) {
			// 无剩余配额：在 reset 前完全冷却
			l.extendCooldownUntil(resetTime, now)
		}
	}

	// OpenAI remaining/reset headers
	remaining = headers.Get("x-ratelimit-remaining-requests")
	resetStr = headers.Get("x-ratelimit-reset-requests")
	if remaining != "" && resetStr != "" {
		rem, err1 := strconv.ParseInt(remaining, 10, 64)
		if err1 == nil && rem <= 1 {
			if resetTime, ok := parseResetValue(resetStr, now); ok && resetTime.After(now) {
				l.extendCooldownUntil(resetTime, now)
			}
		}
	}
}

// extendCooldownUntil 将冷却截止延长到 until（已持有 mu）。
// 超过上限的指示截断到 now+maxUpstreamResetDelay，永不缩短现有冷却。
func (l *ChannelLimiter) extendCooldownUntil(until time.Time, now time.Time) {
	if cap := now.Add(maxUpstreamResetDelay); until.After(cap) {
		until = cap
	}
	if until.After(l.cooldownUntil) {
		l.cooldownUntil = until
	}
}

// latestUpstreamReset 从三族重置头中取最晚的有效值。
// 任一族有有效值即返回；全部无效返回 false。
func latestUpstreamReset(headers http.Header, now time.Time) (time.Time, bool) {
	var latest time.Time
	consider := func(candidate time.Time, ok bool) {
		if !ok || !candidate.After(now) {
			return
		}
		if latest.IsZero() || candidate.After(latest) {
			latest = candidate
		}
	}

	// 遍历三族头部的全部值，兼容大小写与重复头部。
	for name, values := range headers {
		lower := strings.ToLower(name)
		isRetryAfter := lower == "retry-after"
		isAnthropicReset := strings.HasPrefix(lower, "anthropic-ratelimit-") && strings.HasSuffix(lower, "-reset")
		isOpenAIReset := lower == "x-ratelimit-reset" || strings.HasPrefix(lower, "x-ratelimit-reset-")
		if !isRetryAfter && !isAnthropicReset && !isOpenAIReset {
			continue
		}
		for _, value := range values {
			if isRetryAfter {
				consider(parseRetryAfterTime(value, now))
			} else {
				consider(parseResetValue(value, now))
			}
		}
	}

	return latest, !latest.IsZero()
}

// parseRetryAfterTime 解析 Retry-After 单值，返回冷却截止时刻。
// 支持秒整数与 HTTP-date 两种格式。Retry-After 是上游显式退避指示，
// 超长的合法值（如 86400）保留解析、由 extendCooldownUntil 截断到 1h，
// 而不是丢弃让渠道继续被打。
func parseRetryAfterTime(value string, now time.Time) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, false
	}
	if secs, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		if secs <= 0 {
			return time.Time{}, false
		}
		// 先截断再转为 duration，避免极大的秒数乘法溢出。
		if secs > int64(maxUpstreamResetDelay/time.Second) {
			secs = int64(maxUpstreamResetDelay / time.Second)
		}
		return now.Add(time.Duration(secs) * time.Second), true
	}
	if t, err := http.ParseTime(trimmed); err == nil {
		return t, t.After(now)
	}
	return time.Time{}, false
}

// parseResetValue 解析单个重置头值，兼容四种上游风格：
// RFC3339 时间戳；Go duration（"1s"/"6m0s"）；epoch 秒/毫秒数值；相对秒数。
// 过去时间无效；合法的超长时长与绝对时间统一由上层截断。
func parseResetValue(value string, now time.Time) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return t, t.After(now)
	}
	if d, err := time.ParseDuration(trimmed); err == nil {
		if d <= 0 {
			return time.Time{}, false
		}
		return now.Add(d), true
	}
	numeric, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	var reset time.Time
	switch {
	case numeric >= 1_000_000_000_000: // epoch 毫秒
		reset = time.UnixMilli(numeric)
	case numeric >= 1_000_000_000: // epoch 秒
		reset = time.Unix(numeric, 0)
	case numeric > 0: // 相对秒；此分支小于 10 亿秒，转 duration 不会溢出。
		reset = now.Add(time.Duration(numeric) * time.Second)
	default:
		return time.Time{}, false
	}
	return reset, reset.After(now)
}
