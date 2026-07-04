package common

import (
	"testing"

	"encoding/json"
)

// TestShouldRetryWithNextKey_403WithPredeductQuotaError 测试 403 + 预扣费额度失败的场景
// 这是生产环境实际发生的错误格式
func TestShouldRetryWithNextKey_403WithPredeductQuotaError(t *testing.T) {
	// 使用生产环境的精确 JSON 格式
	body := []byte(`{"error":{"type":"new_api_error","message":"预扣费额度失败, 用户剩余额度: ¥0.053950, 需要预扣费额度: ¥0.191160, 下次重置时间: 2025-01-01 00:00:00"},"type":"error"}`)

	gotFailover, gotQuota := ShouldRetryWithNextKey(403, body, false, "Messages")

	if !gotFailover {
		t.Errorf("ShouldRetryWithNextKey(403, prededuct_error, false) failover = %v, want true", gotFailover)
	}
	if !gotQuota {
		t.Errorf("ShouldRetryWithNextKey(403, prededuct_error, false) quota = %v, want true", gotQuota)
	}
}

// TestShouldRetryWithNextKey 测试完整的重试判断逻辑
func TestShouldRetryWithNextKey(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		body         map[string]interface{}
		wantFailover bool
		wantQuota    bool
	}{
		// 403 + 中文配额相关消息
		{
			name:       "403 with chinese quota message",
			statusCode: 403,
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"type":    "new_api_error",
					"message": "预扣费额度失败, 用户剩余额度: ¥0.053950",
				},
				"type": "error",
			},
			wantFailover: true,
			wantQuota:    true,
		},
		// 状态码优先
		{
			name:         "401 always failover",
			statusCode:   401,
			body:         map[string]interface{}{},
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "402 always failover with quota",
			statusCode:   402,
			body:         map[string]interface{}{},
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name:         "408 always failover",
			statusCode:   408,
			body:         map[string]interface{}{},
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "500 always failover",
			statusCode:   500,
			body:         map[string]interface{}{},
			wantFailover: true,
			wantQuota:    false,
		},
		// 400 需要检查消息体
		{
			name:       "400 with quota message",
			statusCode: 400,
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Quota exceeded",
				},
			},
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name:       "400 with auth message",
			statusCode: 400,
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Invalid API key",
				},
			},
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:       "400 without failover keywords",
			statusCode: 400,
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Bad request",
				},
			},
			wantFailover: false,
			wantQuota:    false,
		},
		{
			name:       "400 invalid_request_error should not failover",
			statusCode: 400,
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"type":    "invalid_request_error",
					"message": "Invalid value: 'input_text'. Supported values are: 'output_text' and 'refusal'.",
				},
			},
			wantFailover: false,
			wantQuota:    false,
		},
		{
			name:       "400 anthropic thinking field required should not failover",
			statusCode: 400,
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"type":    "invalid_request_error",
					"message": "messages.1213.content.0.thinking.thinking: Field required",
				},
			},
			wantFailover: false,
			wantQuota:    false,
		},
		{
			name:       "400 thinking mode reasoning_content must be passed back in param should not failover",
			statusCode: 400,
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"code":    "400",
					"message": "Param Incorrect",
					"param":   "The reasoning_content in the thinking mode must be passed back to the API.",
				},
			},
			wantFailover: false,
			wantQuota:    false,
		},
		// 404 不应 failover
		{
			name:         "404 never failover",
			statusCode:   404,
			body:         map[string]interface{}{},
			wantFailover: false,
			wantQuota:    false,
		},
		// 200 不应 failover
		{
			name:         "200 never failover",
			statusCode:   200,
			body:         map[string]interface{}{},
			wantFailover: false,
			wantQuota:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyBytes, _ := json.Marshal(tt.body)
			// 测试非 Fuzzy 模式（精确错误分类）
			gotFailover, gotQuota := ShouldRetryWithNextKey(tt.statusCode, bodyBytes, false, "Messages")
			if gotFailover != tt.wantFailover {
				t.Errorf("shouldRetryWithNextKey(%d, ..., false) failover = %v, want %v", tt.statusCode, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("shouldRetryWithNextKey(%d, ..., false) quota = %v, want %v", tt.statusCode, gotQuota, tt.wantQuota)
			}
		})
	}
}

func TestShouldRetryWithNextKey_TopLevelDetailAndAuthMessages(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		body         string
		fuzzyMode    bool
		wantFailover bool
		wantQuota    bool
	}{
		{
			name:         "top level detail not found remains non quota failover in fuzzy mode",
			statusCode:   404,
			body:         `{"detail":"Not Found"}`,
			fuzzyMode:    true,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "top level message chinese auth error",
			statusCode:   401,
			body:         `{"message":"身份验证失败。","type":"authentication_error"}`,
			fuzzyMode:    false,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "top level detail chinese invalid token",
			statusCode:   401,
			body:         `{"detail":"无效的令牌","type":"authentication_error"}`,
			fuzzyMode:    false,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "string error field auth message",
			statusCode:   401,
			body:         `{"error":"身份验证失败。"}`,
			fuzzyMode:    false,
			wantFailover: true,
			wantQuota:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := ShouldRetryWithNextKey(tt.statusCode, []byte(tt.body), tt.fuzzyMode, "Messages")
			if gotFailover != tt.wantFailover {
				t.Fatalf("ShouldRetryWithNextKey(%d, %s, %v) failover = %v, want %v", tt.statusCode, tt.body, tt.fuzzyMode, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Fatalf("ShouldRetryWithNextKey(%d, %s, %v) quota = %v, want %v", tt.statusCode, tt.body, tt.fuzzyMode, gotQuota, tt.wantQuota)
			}
		})
	}
}

// TestShouldRetryWithNextKeyFuzzyMode 测试 Fuzzy 模式下的错误分类
// Fuzzy 模式：所有非 2xx 错误都触发 failover
func TestShouldRetryWithNextKeyFuzzyMode(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		wantFailover bool
		wantQuota    bool
	}{
		// 2xx 成功响应不 failover
		{
			name:         "200 OK - no failover",
			statusCode:   200,
			wantFailover: false,
			wantQuota:    false,
		},
		{
			name:         "201 Created - no failover",
			statusCode:   201,
			wantFailover: false,
			wantQuota:    false,
		},
		// 3xx 重定向在 Fuzzy 模式下触发 failover
		{
			name:         "301 Redirect - failover in fuzzy mode",
			statusCode:   301,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "302 Found - failover in fuzzy mode",
			statusCode:   302,
			wantFailover: true,
			wantQuota:    false,
		},
		// 4xx 客户端错误在 Fuzzy 模式下都触发 failover
		{
			name:         "400 Bad Request - failover in fuzzy mode",
			statusCode:   400,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "401 Unauthorized - failover in fuzzy mode",
			statusCode:   401,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "402 Payment Required - failover with quota",
			statusCode:   402,
			wantFailover: true,
			wantQuota:    true, // 配额相关
		},
		{
			name:         "403 Forbidden - failover in fuzzy mode",
			statusCode:   403,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "404 Not Found - failover in fuzzy mode",
			statusCode:   404,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "422 Unprocessable Entity - failover in fuzzy mode",
			statusCode:   422,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "429 Too Many Requests - failover with quota",
			statusCode:   429,
			wantFailover: true,
			wantQuota:    true, // 配额相关
		},
		// 5xx 服务端错误在 Fuzzy 模式下触发 failover
		{
			name:         "500 Internal Server Error - failover in fuzzy mode",
			statusCode:   500,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "502 Bad Gateway - failover in fuzzy mode",
			statusCode:   502,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "503 Service Unavailable - failover in fuzzy mode",
			statusCode:   503,
			wantFailover: true,
			wantQuota:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 测试 Fuzzy 模式（所有非 2xx 都 failover）
			gotFailover, gotQuota := ShouldRetryWithNextKey(tt.statusCode, nil, true, "Messages")
			if gotFailover != tt.wantFailover {
				t.Errorf("shouldRetryWithNextKey(%d, nil, true) failover = %v, want %v", tt.statusCode, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("shouldRetryWithNextKey(%d, nil, true) quota = %v, want %v", tt.statusCode, gotQuota, tt.wantQuota)
			}
		})
	}
}

// TestShouldRetryWithNextKey_FuzzyMode_403WithQuotaMessage 测试 Fuzzy 模式下 403 + 预扣费消息
// 验证修复：Fuzzy 模式下也会检查消息体中的配额相关关键词
func TestShouldRetryWithNextKey_FuzzyMode_403WithQuotaMessage(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		body         []byte
		wantFailover bool
		wantQuota    bool
	}{
		{
			name:         "403 with prededuct quota error in fuzzy mode",
			statusCode:   403,
			body:         []byte(`{"error":{"type":"new_api_error","message":"预扣费额度失败, 用户剩余额度: ¥0.053950, 需要预扣费额度: ¥0.191160"},"type":"error"}`),
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name:         "403 with insufficient balance in fuzzy mode",
			statusCode:   403,
			body:         []byte(`{"error":{"message":"余额不足，请充值"}}`),
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name:         "403 without quota keywords in fuzzy mode",
			statusCode:   403,
			body:         []byte(`{"error":{"message":"Access denied"}}`),
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "403 with empty body in fuzzy mode",
			statusCode:   403,
			body:         nil,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "500 with quota message in fuzzy mode",
			statusCode:   500,
			body:         []byte(`{"error":{"message":"Quota exceeded"}}`),
			wantFailover: true,
			wantQuota:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := ShouldRetryWithNextKey(tt.statusCode, tt.body, true, "Messages")
			if gotFailover != tt.wantFailover {
				t.Errorf("ShouldRetryWithNextKey(%d, body, true) failover = %v, want %v", tt.statusCode, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("ShouldRetryWithNextKey(%d, body, true) quota = %v, want %v", tt.statusCode, gotQuota, tt.wantQuota)
			}
		})
	}
}

func TestShouldRetryWithNextKey_FuzzyMode_InvalidRequestShouldNotFailover(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{
			name: "invalid_request_error type",
			body: []byte(`{"error":{"type":"invalid_request_error","message":"Invalid value: 'input_text'. Supported values are: 'output_text' and 'refusal'."}}`),
		},
		{
			name: "schema validation message in upstream_error",
			body: []byte(`{"error":{"type":"upstream_error","upstream_error":{"message":"Schema validation failed: unsupported content type input_text"}}}`),
		},
		{
			name: "anthropic thinking field required",
			body: []byte(`{"error":{"type":"invalid_request_error","message":"messages.1213.content.0.thinking.thinking: Field required"},"type":"error"}`),
		},
		{
			name: "thinking mode reasoning_content must be passed back in param",
			body: []byte(`{"error":{"code":"400","message":"Param Incorrect","param":"The reasoning_content in the thinking mode must be passed back to the API.","type":""}}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := ShouldRetryWithNextKey(400, tt.body, true, "Messages")
			if gotFailover {
				t.Errorf("ShouldRetryWithNextKey(400, invalid_request_body, true) failover = %v, want false", gotFailover)
			}
			if gotQuota {
				t.Errorf("ShouldRetryWithNextKey(400, invalid_request_body, true) quota = %v, want false", gotQuota)
			}
		})
	}
}

func TestShouldRetryWithNextKey_InvalidRequest5xxShouldFailover(t *testing.T) {
	tests := []struct {
		name      string
		body      []byte
		fuzzyMode bool
	}{
		{
			name:      "invalid_request code - normal mode",
			body:      []byte(`{"error":{"code":"invalid_request","message":"invalid request from upstream"}}`),
			fuzzyMode: false,
		},
		{
			name:      "invalid_request code - fuzzy mode",
			body:      []byte(`{"error":{"code":"invalid_request","message":"invalid request from upstream"}}`),
			fuzzyMode: true,
		},
		{
			name:      "schema validation message - normal mode",
			body:      []byte(`{"error":{"type":"upstream_error","upstream_error":{"message":"Schema validation failed: unsupported content type input_text"}}}`),
			fuzzyMode: false,
		},
		{
			name:      "schema validation message - fuzzy mode",
			body:      []byte(`{"error":{"type":"upstream_error","upstream_error":{"message":"Schema validation failed: unsupported content type input_text"}}}`),
			fuzzyMode: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := ShouldRetryWithNextKey(500, tt.body, tt.fuzzyMode, "Messages")
			if !gotFailover {
				t.Errorf("ShouldRetryWithNextKey(500, invalid_request_body, %v) failover = %v, want true", tt.fuzzyMode, gotFailover)
			}
			if gotQuota {
				t.Errorf("ShouldRetryWithNextKey(500, invalid_request_body, %v) quota = %v, want false", tt.fuzzyMode, gotQuota)
			}
		})
	}
}

func TestShouldRetryWithNextKey_BusinessCodeOnlyErrors(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		body         string
		wantFailover bool
		wantQuota    bool
	}{
		{
			name:         "sub2api quota code on 400 still failovers as quota",
			statusCode:   400,
			body:         `{"code":"API_KEY_QUOTA_EXHAUSTED","message":"error"}`,
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name:         "sub2api disabled key code on 400 still failovers as auth",
			statusCode:   400,
			body:         `{"error":{"code":"API_KEY_DISABLED","message":"error","type":"error"}}`,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "done-hub service unavailable code on 400 still failovers as transient",
			statusCode:   400,
			body:         `{"error":{"code":"service_unavailable","message":"error","type":"one_hub_error"}}`,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "new-api sensitive words code blocks failover even on 500",
			statusCode:   500,
			body:         `{"error":{"code":"violation_fee.grok.csam","message":"error","type":"new_api_error"}}`,
			wantFailover: false,
			wantQuota:    false,
		},
		{
			name:         "new-api content moderation failed blocks failover even on 500",
			statusCode:   500,
			body:         `{"error":{"code":"content_moderation_failed","message":"content moderation failed","type":"upstream_error"}}`,
			wantFailover: false,
			wantQuota:    false,
		},
		{
			name:         "gemini resource exhausted status on 400 failovers as quota",
			statusCode:   400,
			body:         `{"error":{"code":429,"message":"error","status":"RESOURCE_EXHAUSTED"}}`,
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name:         "google error info rate limit reason on 400 failovers as quota",
			statusCode:   400,
			body:         `{"error":{"message":"error","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"RATE_LIMIT_EXCEEDED"}]}}`,
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name:         "google service disabled reason on 400 failovers as permission",
			statusCode:   400,
			body:         `{"error":{"message":"error","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"SERVICE_DISABLED"}]}}`,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "provider invalid parameter on 400 does not failover",
			statusCode:   400,
			body:         `{"code":"InvalidParameter","message":"Role must be user or assistant and Content length must be greater than 0"}`,
			wantFailover: false,
			wantQuota:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := ShouldRetryWithNextKey(tt.statusCode, []byte(tt.body), false, "Messages")
			if gotFailover != tt.wantFailover {
				t.Errorf("ShouldRetryWithNextKey(%d, %s) failover = %v, want %v", tt.statusCode, tt.body, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("ShouldRetryWithNextKey(%d, %s) quota = %v, want %v", tt.statusCode, tt.body, gotQuota, tt.wantQuota)
			}
		})
	}
}

// TestIsNonRetryableErrorCode 测试参数校验类不可重试错误码判断
func TestIsNonRetryableErrorCode(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		// 请求内容无效 - 不应重试
		{"invalid_request", true},
		{"invalid_request_error", true},
		{"bad_request", true},
		// 内容审核相关 - 已拆分到 isContentModerationErrorCode，此处应返回 false
		{"sensitive_words_detected", false},
		{"content_policy_violation", false},
		{"content_filter", false},
		{"content_blocked", false},
		{"moderation_blocked", false},
		// 其他错误码 - 应该重试
		{"server_error", false},
		{"rate_limit", false},
		{"authentication_error", false},
		{"unknown_error", false},
		{"", false},
	}

	for _, tt := range tests {
		name := tt.code
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			got := isNonRetryableErrorCode(tt.code)
			if got != tt.want {
				t.Errorf("isNonRetryableErrorCode(%q) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}

func TestIsUpstreamAccountPoolUnavailable(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "error_code",
			body: `{"error":{"message":"无可用账号，请稍后重试","type":"server_error","param":"","code":"no_available_account"}}`,
			want: true,
		},
		{
			name: "english_message",
			body: `{"error":{"message":"no available accounts, retry later","type":"server_error"}}`,
			want: true,
		},
		{
			name: "sub2api_gemini_accounts",
			body: `{"error":{"code":503,"message":"No available Gemini accounts","status":"UNAVAILABLE"}}`,
			want: true,
		},
		{
			name: "accounts_exhausted",
			body: `{"error":{"message":"All available accounts exhausted","type":"api_error"}}`,
			want: true,
		},
		{
			name: "chinese_account_pool",
			body: `{"error":{"message":"账号池不可用，请稍后重试","type":"server_error"}}`,
			want: true,
		},
		{
			name: "chinese_account_variant",
			body: `{"error":{"message":"无可用账户，请稍后重试","type":"server_error"}}`,
			want: true,
		},
		{
			name: "top_level_message",
			body: `{"message":"No available account for upstream pool"}`,
			want: true,
		},
		{
			name: "generic_server_error",
			body: `{"error":{"message":"upstream temporarily unavailable","type":"server_error","code":"server_error"}}`,
			want: false,
		},
		{
			name: "invalid_json",
			body: `not json`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsUpstreamAccountPoolUnavailable([]byte(tt.body))
			if got != tt.want {
				t.Fatalf("IsUpstreamAccountPoolUnavailable() = %v, want %v", got, tt.want)
			}
		})
	}
}


func TestIsUpstreamTemporarilyOverloaded(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "system_cpu_overloaded",
			body: `{"error":{"message":"system cpu overloaded (current: 92.1%, threshold: 90%)","type":"new_api_error","param":"","code":"system_cpu_overloaded"}}`,
			want: true,
		},
		{
			name: "anthropic_overloaded_error",
			body: `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`,
			want: true,
		},
		{
			name: "service_unavailable_code",
			body: `{"error":{"code":"service_unavailable","message":"service unavailable","type":"api_error"}}`,
			want: true,
		},
		{
			name: "chinese_overloaded",
			body: `{"error":{"message":"系统过载，请稍后重试","type":"server_error"}}`,
			want: true,
		},
		{
			name: "nested_upstream_error",
			body: `{"error":{"upstream_error":{"code":"system_cpu_overloaded","message":"cpu overloaded"}}}`,
			want: true,
		},
		{
			name: "top_level_overloaded",
			body: `{"message":"service_temporarily_unavailable"}`,
			want: true,
		},
		{
			name: "account_pool_not_overload",
			body: `{"error":{"message":"无可用账号，请稍后重试","type":"server_error","code":"no_available_account"}}`,
			want: false,
		},
		{
			name: "generic_server_error",
			body: `{"error":{"message":"internal error","type":"server_error","code":"server_error"}}`,
			want: false,
		},
		{
			name: "invalid_json",
			body: `not json`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsUpstreamTemporarilyOverloaded([]byte(tt.body))
			if got != tt.want {
				t.Fatalf("IsUpstreamTemporarilyOverloaded() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestIsContentModerationErrorCode 测试内容审核类错误码判断
func TestIsContentModerationErrorCode(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		// 内容审核相关 - 不应重试
		{"sensitive_words_detected", true},
		{"violation_fee.grok.csam", true},
		{"content_moderation_failed", true},
		{"content_policy_violation", true},
		{"content_filter", true},
		{"content_blocked", true},
		{"moderation_blocked", true},
		{"prompt_blocked", true},
		// 大小写不敏感
		{"SENSITIVE_WORDS_DETECTED", true},
		{"Content_Policy_Violation", true},
		// 参数校验类 - 不属于内容审核
		{"invalid_request", false},
		{"invalid_request_error", false},
		{"bad_request", false},
		// 其他错误码
		{"server_error", false},
		{"rate_limit", false},
		{"authentication_error", false},
		{"", false},
	}

	for _, tt := range tests {
		name := tt.code
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			got := isContentModerationErrorCode(tt.code)
			if got != tt.want {
				t.Errorf("isContentModerationErrorCode(%q) = %v, want %v", tt.code, got, tt.want)
			}
		})
	}
}
