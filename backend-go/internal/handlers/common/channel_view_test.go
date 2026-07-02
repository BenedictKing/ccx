package common

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func boolPtr(b bool) *bool { return &b }

func TestBuildChannelView_TuningBenchFields(t *testing.T) {
	up := config.UpstreamConfig{
		Name:                        "test-channel",
		ServiceType:                 "openai",
		AuthHeader:                  "x-api-key",
		RateLimitRPM:                120,
		RateLimitBurst:              20,
		RateLimitMaxConcurrent:      8,
		RateLimitAutoFromHeaders:    boolPtr(true),
		RequestTimeoutMs:            60000,
		ResponseHeaderTimeoutMs:     90000,
		StreamFirstContentTimeoutMs: 30000,
		StreamInactivityTimeoutMs:   20000,
		StreamToolCallIdleTimeoutMs: 45000,
		HistoricalImageTurnLimit:    5,
		ConvertImageURLToB64JSON:    true,
	}

	view := BuildChannelView(up, 0)

	assertions := []struct {
		key      string
		expected interface{}
	}{
		{"rateLimitRpm", 120},
		{"authHeader", "x-api-key"},
		{"rateLimitBurst", 20},
		{"rateLimitMaxConcurrent", 8},
		{"rateLimitAutoFromHeaders", true},
		{"requestTimeoutMs", 60000},
		{"responseHeaderTimeoutMs", 90000},
		{"streamFirstContentTimeoutMs", 30000},
		{"streamInactivityTimeoutMs", 20000},
		{"streamToolCallIdleTimeoutMs", 45000},
		{"historicalImageTurnLimit", 5},
		{"convertImageUrlToB64Json", true},
	}

	for _, a := range assertions {
		got, ok := view[a.key]
		if !ok {
			t.Errorf("BuildChannelView missing key %q", a.key)
			continue
		}
		if got != a.expected {
			t.Errorf("BuildChannelView[%q] = %v (%T), want %v (%T)", a.key, got, got, a.expected, a.expected)
		}
	}
}

func TestBuildChannelView_RateLimitDefaults(t *testing.T) {
	// 当 RateLimitAutoFromHeaders 为 nil（未设置），默认值为 true（自动学习上游限速）
	up := config.UpstreamConfig{
		Name:        "test-channel",
		ServiceType: "openai",
	}

	view := BuildChannelView(up, 0)

	if v, ok := view["rateLimitAutoFromHeaders"]; !ok || v != true {
		t.Errorf("expected rateLimitAutoFromHeaders=true when nil (default enabled), got %v", v)
	}
	if v, ok := view["rateLimitRpm"]; !ok || v != 0 {
		t.Errorf("expected rateLimitRpm=0 when unset, got %v", v)
	}
}
