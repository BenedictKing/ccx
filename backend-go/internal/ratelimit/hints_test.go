package ratelimit

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

// cooldownRemainingOf 构造 limiter 并应用响应头后返回剩余冷却时长。
func cooldownRemainingOf(t *testing.T, headers http.Header, statusCode int, now time.Time) time.Duration {
	t.Helper()
	l := NewChannelLimiter(Config{}, now)
	l.ApplyUpstreamHints(headers, statusCode, now)
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cooldownUntil.Sub(now)
}

func TestApplyUpstreamHints_RetryAfterCappedAtOneHour(t *testing.T) {
	now := time.Now()
	headers := http.Header{"Retry-After": []string{"86400"}} // 伪造/极端的一天

	got := cooldownRemainingOf(t, headers, http.StatusTooManyRequests, now)
	if got <= 0 || got > maxUpstreamResetDelay+time.Second {
		t.Fatalf("Retry-After 应被截断到 1h，得到 %v", got)
	}
	if got < maxUpstreamResetDelay-time.Minute {
		t.Fatalf("超上限指示应截断（而非丢弃），接近 1h，得到 %v", got)
	}
}

func TestApplyUpstreamHints_AnthropicFamilyTakesLatest(t *testing.T) {
	now := time.Now()
	// 多维度 reset 头并存：requests 30s 后重置，tokens 5 分钟后重置 → 取最晚
	headers := http.Header{
		"Anthropic-Ratelimit-Requests-Reset": []string{now.Add(30 * time.Second).Format(time.RFC3339)},
		"Anthropic-Ratelimit-Tokens-Reset":   []string{now.Add(5 * time.Minute).Format(time.RFC3339)},
	}

	got := cooldownRemainingOf(t, headers, http.StatusTooManyRequests, now)
	if got < 4*time.Minute {
		t.Fatalf("应取族内最晚的 reset（约 5 分钟），得到 %v", got)
	}
}

func TestApplyUpstreamHints_AnthropicFamilyEpochAndDuration(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		value  string
		expect time.Duration
	}{
		{"epoch seconds", formatEpochSeconds(now.Add(2 * time.Minute)), 2 * time.Minute},
		{"epoch millis", formatEpochMillis(now.Add(90 * time.Second)), 90 * time.Second},
		{"relative seconds", "120", 2 * time.Minute},
		{"duration", "90s", 90 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := http.Header{"Anthropic-Ratelimit-Requests-Reset": []string{tt.value}}
			got := cooldownRemainingOf(t, headers, http.StatusTooManyRequests, now)
			if got < tt.expect-time.Minute || got > tt.expect+time.Minute {
				t.Fatalf("冷却 %v，期望约 %v", got, tt.expect)
			}
		})
	}
}

func TestApplyUpstreamHints_OpenAIFamily(t *testing.T) {
	now := time.Now()
	headers := http.Header{
		"X-Ratelimit-Reset-Requests": []string{"6m0s"},
	}
	got := cooldownRemainingOf(t, headers, http.StatusTooManyRequests, now)
	if got < 5*time.Minute {
		t.Fatalf("x-ratelimit duration 应生效（约 6 分钟），得到 %v", got)
	}
}

func TestApplyUpstreamHints_PastResetIgnored(t *testing.T) {
	now := time.Now()
	headers := http.Header{
		"Anthropic-Ratelimit-Requests-Reset": []string{now.Add(-time.Hour).Format(time.RFC3339)},
	}
	got := cooldownRemainingOf(t, headers, http.StatusTooManyRequests, now)
	if got > 0 {
		t.Fatalf("过去时间的 reset 不应触发冷却，得到 %v", got)
	}
}

func TestApplyUpstreamHints_SuccessPathRemainingExhausted(t *testing.T) {
	now := time.Now()
	// 200 响应但配额耗尽：remaining<=1 时仍按 reset 冷却（受 1h 截断）
	headers := http.Header{
		"Anthropic-Ratelimit-Requests-Remaining": []string{"0"},
		"Anthropic-Ratelimit-Requests-Reset":     []string{now.Add(30 * time.Minute).Format(time.RFC3339)},
	}
	got := cooldownRemainingOf(t, headers, http.StatusOK, now)
	if got < 29*time.Minute {
		t.Fatalf("配额耗尽的 200 响应应冷却到 reset，得到 %v", got)
	}

	// remaining 充足时不冷却
	headers.Set("Anthropic-Ratelimit-Requests-Remaining", "50")
	if got := cooldownRemainingOf(t, headers, http.StatusOK, now); got > 0 {
		t.Fatalf("remaining 充足不应冷却，得到 %v", got)
	}
}

func TestApplyUpstreamHints_NeverShortensExistingCooldown(t *testing.T) {
	now := time.Now()
	l := NewChannelLimiter(Config{}, now)

	long := http.Header{"Retry-After": []string{"3600"}}
	l.ApplyUpstreamHints(long, http.StatusTooManyRequests, now)

	// 更短的重置指示不得缩短已建立的冷却
	short := http.Header{"Anthropic-Ratelimit-Requests-Reset": []string{now.Add(10 * time.Second).Format(time.RFC3339)}}
	l.ApplyUpstreamHints(short, http.StatusTooManyRequests, now)

	l.mu.Lock()
	remaining := l.cooldownUntil.Sub(now)
	l.mu.Unlock()
	if remaining < 59*time.Minute {
		t.Fatalf("短指示不应缩短冷却，剩余 %v", remaining)
	}
}

func TestParseResetValue_RejectsInvalid(t *testing.T) {
	now := time.Now()
	invalid := []string{"", "abc", "0", "-5", "-10s"}
	for _, value := range invalid {
		if _, ok := parseResetValue(value, now); ok {
			t.Fatalf("parseResetValue(%q) 不应有效", value)
		}
	}
	if _, ok := parseRetryAfterTime("not-a-date", now); ok {
		t.Fatal("parseRetryAfterTime 对非法值不应有效")
	}
	if _, ok := parseRetryAfterTime("0", now); ok {
		t.Fatal("parseRetryAfterTime(0) 不应有效")
	}
}

func formatEpochSeconds(t time.Time) string {
	return strconv.FormatInt(t.Unix(), 10)
}

func formatEpochMillis(t time.Time) string {
	return strconv.FormatInt(t.UnixMilli(), 10)
}
