package common

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestIsUpstreamAccountRateLimited_TopLevelCode(t *testing.T) {
	body := []byte(`{"error":{"code":"AccountRateLimitExceeded","message":"requests are too frequent"}}`)
	if !IsUpstreamAccountRateLimited(http.StatusTooManyRequests, body) {
		t.Fatal("top-level error.code=AccountRateLimitExceeded on 429 should match")
	}
}

func TestIsUpstreamAccountRateLimited_NestedUpstreamError(t *testing.T) {
	body := []byte(`{"error":{"upstream_error":{"code":"AccountRateLimitExceeded"}}}`)
	if !IsUpstreamAccountRateLimited(http.StatusTooManyRequests, body) {
		t.Fatal("nested upstream_error.code=AccountRateLimitExceeded on 429 should match")
	}
}

func TestIsUpstreamAccountRateLimited_NormalizedForm(t *testing.T) {
	body := []byte(`{"error":{"code":"account_rate_limit_exceeded"}}`)
	if !IsUpstreamAccountRateLimited(http.StatusTooManyRequests, body) {
		t.Fatal("normalized account_rate_limit_exceeded should match")
	}
}

func TestIsUpstreamAccountRateLimited_MessageFallback(t *testing.T) {
	body := []byte(`{"error":{"message":"Requests are too frequent, please retry later"}}`)
	if !IsUpstreamAccountRateLimited(http.StatusTooManyRequests, body) {
		t.Fatal("message fallback 'requests are too frequent' on 429 should match")
	}
}

func TestIsUpstreamAccountRateLimited_MessageWithSpaces(t *testing.T) {
	// 火山实际返回的 code 可能是带空格的 "Account Rate Limit Exceeded"
	body := []byte(`{"error":{"code":"Account Rate Limit Exceeded"}}`)
	if !IsUpstreamAccountRateLimited(http.StatusTooManyRequests, body) {
		t.Fatal("'Account Rate Limit Exceeded' (with spaces) should match after normalization")
	}
}

func TestIsUpstreamAccountRateLimited_OnlyOn429(t *testing.T) {
	body := []byte(`{"error":{"code":"AccountRateLimitExceeded"}}`)
	// 非 429 状态码不应触发账号级限流标记
	if IsUpstreamAccountRateLimited(http.StatusOK, body) {
		t.Fatal("200 should not trigger account rate limit")
	}
	if IsUpstreamAccountRateLimited(http.StatusServiceUnavailable, body) {
		t.Fatal("503 should not trigger account rate limit (use overloaded path)")
	}
	if IsUpstreamAccountRateLimited(http.StatusInternalServerError, body) {
		t.Fatal("500 should not trigger account rate limit")
	}
}

func TestIsUpstreamAccountRateLimited_MessageFallbackOnlyOn429(t *testing.T) {
	body := []byte(`{"error":{"message":"requests are too frequent"}}`)
	// 非 429 语境下，消息兜底不应触发账号限流（避免升级为 channel overloaded）
	if IsUpstreamAccountRateLimited(http.StatusInternalServerError, body) {
		t.Fatal("message fallback must not trigger on non-429 status")
	}
}

func TestIsUpstreamAccountRateLimited_NotTemporarilyOverloaded(t *testing.T) {
	// 账号级限流命中时不应被误判为通用 CPU/service overloaded
	body := []byte(`{"error":{"code":"AccountRateLimitExceeded","message":"requests are too frequent"}}`)
	if IsUpstreamTemporarilyOverloaded(body) {
		t.Fatal("AccountRateLimitExceeded should not be classified as temporarily overloaded")
	}
}

func TestIsUpstreamAccountRateLimited_PlainServerErrorNotMatched(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{"server error", []byte(`{"error":{"code":"server_error","message":"internal"}}`)},
		{"insufficient balance", []byte(`{"error":{"code":"insufficient_balance","message":"no balance"}}`)},
		{"authentication", []byte(`{"error":{"code":"invalid_api_key","message":"unauthorized"}}`)},
		{"generic 429 too many requests", []byte(`{"error":{"message":"too many requests"}}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if IsUpstreamAccountRateLimited(http.StatusTooManyRequests, tt.body) {
				t.Fatalf("should not match account rate limit: %s", tt.body)
			}
		})
	}
}

func TestIsUpstreamAccountRateLimited_MalformedBody(t *testing.T) {
	if IsUpstreamAccountRateLimited(http.StatusTooManyRequests, []byte("not json")) {
		t.Fatal("malformed body should not match")
	}
	if IsUpstreamAccountRateLimited(http.StatusTooManyRequests, nil) {
		t.Fatal("nil body should not match")
	}
}

func TestIsUpstreamAccountRateLimited_TypeField(t *testing.T) {
	body := []byte(`{"error":{"type":"AccountRateLimitExceeded"}}`)
	if !IsUpstreamAccountRateLimited(http.StatusTooManyRequests, body) {
		t.Fatal("error.type=AccountRateLimitExceeded should match")
	}
}

// 验证火山真实返回结构（顶层 error + 嵌套 upstream_error 同时存在）
func TestIsUpstreamAccountRateLimited_VolcRealisticShape(t *testing.T) {
	body, _ := json.Marshal(map[string]interface{}{
		"error": map[string]interface{}{
			"code":    "AccountRateLimitExceeded",
			"message": "Request rate limit exceeded. Please try again later.",
			"upstream_error": map[string]interface{}{
				"code": "AccountRateLimitExceeded",
			},
		},
	})
	if !IsUpstreamAccountRateLimited(http.StatusTooManyRequests, body) {
		t.Fatal("realistic volc 429 body should match")
	}
}
