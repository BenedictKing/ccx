package ratelimit

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestApplyUpstreamHintsResetHeaders(t *testing.T) {
	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name    string
		status  int
		headers http.Header
		want    time.Duration
	}{
		{"retry seconds", 429, http.Header{"Retry-After": {"120"}}, 2 * time.Minute},
		{"retry HTTP date", 503, http.Header{"Retry-After": {now.Add(3 * time.Minute).Format(http.TimeFormat)}}, 3 * time.Minute},
		{"retry capped", 429, http.Header{"Retry-After": {"86400"}}, time.Hour},
		{"retry overflow", 429, http.Header{"Retry-After": {"9223372036854775807"}}, time.Hour},
		{"retry multiple", 429, http.Header{"retry-after": {"invalid", "10", "120"}}, 2 * time.Minute},
		{"anthropic dimensions", 429, http.Header{
			"anthropic-ratelimit-requests-reset": {"30s"},
			"ANTHROPIC-RATELIMIT-TOKENS-RESET":   {"invalid", "5m"},
		}, 5 * time.Minute},
		{"openai bare epoch", 429, http.Header{"X-Ratelimit-Reset": {strconv.FormatInt(now.Add(90*time.Second).Unix(), 10)}}, 90 * time.Second},
		{"openai epoch millis", 503, http.Header{"X-Ratelimit-Reset-Tokens": {strconv.FormatInt(now.Add(2*time.Minute).UnixMilli(), 10)}}, 2 * time.Minute},
		{"latest across families", 429, http.Header{
			"Retry-After":                        {"30"},
			"Anthropic-Ratelimit-Requests-Reset": {"2m"},
			"X-Ratelimit-Reset-Tokens":           {"6m0s"},
		}, 6 * time.Minute},
		{"absolute capped", 429, http.Header{"X-Ratelimit-Reset": {now.Add(7 * 24 * time.Hour).Format(time.RFC3339)}}, time.Hour},
		{"duration capped", 429, http.Header{"X-Ratelimit-Reset-Requests": {"24h"}}, time.Hour},
		{"relative seconds capped", 429, http.Header{"Anthropic-Ratelimit-Tokens-Reset": {"86400"}}, time.Hour},
		{"past reset", 429, http.Header{"X-Ratelimit-Reset": {now.Add(-time.Minute).Format(time.RFC3339)}}, 0},
		{"invalid reset", 429, http.Header{"Retry-After": {"-1"}, "X-Ratelimit-Reset": {"invalid", "0", "-5s"}}, 0},
		{"unrelated prefix", 429, http.Header{"X-Ratelimit-Resetting": {"10s"}, "Anthropic-Ratelimit-Tokens": {"10s"}}, 0},
		{"success without exhaustion", 200, http.Header{"Retry-After": {"120"}, "X-Ratelimit-Reset": {"120"}}, 0},
		{"client error without exhaustion", 400, http.Header{"X-Ratelimit-Reset": {"120"}}, 0},
		{"anthropic exhausted", 200, http.Header{
			"Anthropic-Ratelimit-Requests-Remaining": {"0"},
			"Anthropic-Ratelimit-Requests-Reset":     {"2m"},
		}, 2 * time.Minute},
		{"openai exhausted capped", 200, http.Header{
			"X-Ratelimit-Remaining-Requests": {"1"},
			"X-Ratelimit-Reset-Requests":     {"24h"},
		}, time.Hour},
		{"remaining available", 200, http.Header{
			"Anthropic-Ratelimit-Requests-Remaining": {"50"},
			"Anthropic-Ratelimit-Requests-Reset":     {"2m"},
		}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			limiter := NewChannelLimiter(Config{}, now)
			limiter.ApplyUpstreamHints(tt.headers, tt.status, now)
			if tt.want == 0 {
				if !limiter.cooldownUntil.IsZero() {
					t.Errorf("unexpected cooldown: %v", limiter.cooldownUntil)
				}
			} else if got := limiter.cooldownUntil.Sub(now); got != tt.want {
				t.Errorf("cooldown = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseResetValue(t *testing.T) {
	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	for _, value := range []string{
		now.Add(90 * time.Second).Format(time.RFC3339),
		" 1m30s ", "90",
		strconv.FormatInt(now.Add(90*time.Second).Unix(), 10),
		strconv.FormatInt(now.Add(90*time.Second).UnixMilli(), 10),
	} {
		if got, ok := parseResetValue(value, now); !ok || !got.Equal(now.Add(90*time.Second)) {
			t.Errorf("parseResetValue(%q) = %v, %v", value, got, ok)
		}
	}
	for _, value := range []string{"", "abc", "0", "-5", "-10s", "0s", "9223372036854775808", now.Format(time.RFC3339), strconv.FormatInt(now.Add(-time.Minute).Unix(), 10)} {
		if _, ok := parseResetValue(value, now); ok {
			t.Errorf("parseResetValue(%q) should be invalid", value)
		}
	}
	for _, value := range []string{"", "not-a-date", "0", "-1", now.Add(-time.Minute).Format(http.TimeFormat)} {
		if _, ok := parseRetryAfterTime(value, now); ok {
			t.Errorf("parseRetryAfterTime(%q) should be invalid", value)
		}
	}
}

func TestApplyUpstreamHintsNeverShortensCooldown(t *testing.T) {
	now := time.Now()
	for _, existing := range []time.Duration{30 * time.Minute, 2 * time.Hour} {
		limiter := NewChannelLimiter(Config{}, now)
		limiter.cooldownUntil = now.Add(existing)
		limiter.ApplyUpstreamHints(http.Header{"Retry-After": {"10"}}, http.StatusTooManyRequests, now)
		if !limiter.cooldownUntil.Equal(now.Add(existing)) {
			t.Errorf("existing %v cooldown changed to %v", existing, limiter.cooldownUntil.Sub(now))
		}
	}
	var missing *ChannelLimiter
	missing.ApplyUpstreamHints(http.Header{"Retry-After": {"10"}}, 429, now)
	limiter := NewChannelLimiter(Config{}, now)
	limiter.ApplyUpstreamHints(nil, 429, now)
	if !limiter.cooldownUntil.IsZero() {
		t.Error("nil headers changed cooldown")
	}
}
