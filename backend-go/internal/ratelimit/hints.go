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
// channelUID 是渠道稳定标识（配额真相按渠道聚合必需；空串时配额侧跳过）。
// endpointUID 和 metricsKey 由调用方（upstream_failover.go）在当前请求上下文中计算。
// channelName 为渠道可读名（upstream.Name），仅用于日志展示，可能为空。
// reason 携带 429 细分原因（如 account_rate_limit_exceeded），用于 AIMD 精确置信度。
var UpstreamSignalCallback func(channelUID, endpointUID, metricsKey, serviceType, channelName string, isStream bool, latencyMs int64, headers http.Header, statusCode int, reason string)

// SetUpstreamSignalCallback 设置上游信号回调（main.go 调用）。
// 传 nil 可清除回调。
func SetUpstreamSignalCallback(cb func(channelUID, endpointUID, metricsKey, serviceType, channelName string, isStream bool, latencyMs int64, headers http.Header, statusCode int, reason string)) {
	UpstreamSignalCallback = cb
}

// NotifySignal 若回调已注册，触发信号回调。
// channelUID 为渠道稳定标识；endpointUID / metricsKey 由调用方在请求上下文中计算好后传入。
// channelName 为渠道可读名，仅用于日志展示。
// reason 为 429 细分原因（非 429 传空串）。
// 失败安全：回调 panic 不影响主流程。
func NotifySignal(channelUID, endpointUID, metricsKey, serviceType, channelName string, isStream bool, latencyMs int64, headers http.Header, statusCode int, reason string) {
	cb := UpstreamSignalCallback
	if cb == nil || headers == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[RateLimit-Signal] 回调 panic（已忽略）: %v", r)
		}
	}()
	cb(channelUID, endpointUID, metricsKey, serviceType, channelName, isStream, latencyMs, headers, statusCode, reason)
}

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

	// 族 1：Retry-After（秒整数 / HTTP-date）
	consider(parseRetryAfterTime(headers.Get("Retry-After"), now))

	// 族 2：anthropic-ratelimit-*-reset（requests/tokens 等多维度，逐头解析）
	for name, values := range headers {
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, "anthropic-ratelimit-") || !strings.HasSuffix(lower, "-reset") {
			continue
		}
		for _, value := range values {
			consider(parseResetValue(value, now))
		}
	}

	// 族 3：x-ratelimit-reset / x-ratelimit-reset-*（OpenAI 及兼容网关）
	for name, values := range headers {
		lower := strings.ToLower(name)
		if lower != "x-ratelimit-reset" && !strings.HasPrefix(lower, "x-ratelimit-reset-") {
			continue
		}
		for _, value := range values {
			consider(parseResetValue(value, now))
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
		return now.Add(time.Duration(secs) * time.Second), true
	}
	if t, err := http.ParseTime(trimmed); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// parseResetValue 解析单个重置头值，兼容四种上游风格：
// RFC3339 时间戳；Go duration（"1s"/"6m0s"）；epoch 秒/毫秒数值；相对秒数。
// 过去时间与超上限的相对时长返回无效（超上限的绝对时间仍返回，由上层截断）。
func parseResetValue(value string, now time.Time) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, trimmed); err == nil {
		return t, true
	}
	if d, err := time.ParseDuration(trimmed); err == nil {
		if d <= 0 || d > maxUpstreamResetDelay {
			return time.Time{}, false
		}
		return now.Add(d), true
	}
	numeric, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	switch {
	case numeric >= 1_000_000_000_000: // epoch 毫秒
		return time.UnixMilli(numeric), true
	case numeric >= 1_000_000_000: // epoch 秒
		return time.Unix(numeric, 0), true
	case numeric > 0 && numeric <= int64(maxUpstreamResetDelay/time.Second): // 相对秒
		return now.Add(time.Duration(numeric) * time.Second), true
	default:
		return time.Time{}, false
	}
}
