package common

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVectorsErrorBodySummaryForLogOmitsBodyAndMessage(t *testing.T) {
	body := []byte(`{"error":{"message":"embedding input secret customer text was rejected","type":"invalid_request_error","code":"invalid_request","param":"input"},"input":"secret customer text"}`)

	got := errorBodySummaryForLog("Vectors", 422, body)

	for _, want := range []string{"status=422", "type=invalid_request_error", "code=invalid_request", "param=input"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary = %q, want %q", got, want)
		}
	}
	for _, leaked := range []string{"secret customer text", "embedding input", "rejected", "message"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("summary leaked %q: %s", leaked, got)
		}
	}
}

// TestClassifyMessage 测试基于错误消息的分类
func TestClassifyMessage(t *testing.T) {
	tests := []struct {
		name         string
		message      string
		wantFailover bool
		wantQuota    bool
	}{
		// 配额相关
		{"insufficient credits", "You have insufficient credits", true, true},
		{"quota exceeded", "API quota exceeded for this month", true, true},
		{"rate limit", "Rate limit exceeded, please retry later", true, true},
		{"balance", "Account balance is zero", true, true},
		{"billing", "Billing issue detected", true, true},
		{"中文-积分不足", "您的积分不足，请充值", true, true},
		{"中文-余额不足", "账户余额不足", true, true},
		{"中文-请求数限制", "已达到请求数限制", true, true},

		// 认证相关
		{"invalid api key", "Invalid API key provided", true, false},
		{"unauthorized", "Unauthorized access", true, false},
		{"token expired", "Your token has expired", true, false},
		{"permission denied", "Permission denied for this resource", true, false},
		{"中文-密钥无效", "密钥无效，请检查", true, false},
		{"中文-令牌已过期", "该令牌已过期", true, false},

		// 临时错误
		{"timeout", "Request timeout, please retry", true, false},
		{"server overloaded", "Server is overloaded", true, false},
		{"temporarily unavailable", "Service temporarily unavailable", true, false},
		{"中文-超时", "请求超时", true, false},
		{"中文-稍后再试", "当前分组上游负载已饱和，请稍后再试", true, false},
		{"中文-负载饱和", "重试超时，上游负载已饱和，请稍后再试", true, false},

		// 不应 failover
		{"normal error", "Something went wrong", false, false},
		{"validation error", "Field 'name' is required", false, false},
		{"schema invalid value", "Invalid value: 'input_text'. Supported values are: 'output_text' and 'refusal'.", false, false},
		{"empty message", "", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := classifyMessage(tt.message)
			if gotFailover != tt.wantFailover {
				t.Errorf("classifyMessage(%q) failover = %v, want %v", tt.message, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("classifyMessage(%q) quota = %v, want %v", tt.message, gotQuota, tt.wantQuota)
			}
		})
	}
}

// TestClassifyMessageCapabilityMismatch 测试能力不匹配类错误的消息分类
func TestClassifyMessageCapabilityMismatch(t *testing.T) {
	tests := []struct {
		name         string
		message      string
		wantFailover bool
		wantQuota    bool
	}{
		// 能力不匹配 (应 failover)
		{"mimo multimodal error", "Multimodal data is corrupted or cannot be processed.", true, false},
		{"unsupported image", "Image input is not supported by this model", true, false},
		{"vision not supported", "Vision capability is not supported", true, false},
		{"cannot process image", "This model cannot process images", true, false},
		{"中文-无法处理", "该模型无法处理多模态输入", true, false},
		{"中文-不支持", "当前模型不支持图片输入", true, false},

		// 不应误判为能力不匹配 (原有测试覆盖)
		{"normal validation", "Field 'name' is required", false, false},
		{"schema error", "Invalid value: 'input_text'", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := classifyMessage(tt.message)
			if gotFailover != tt.wantFailover {
				t.Errorf("classifyMessage(%q) failover = %v, want %v", tt.message, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("classifyMessage(%q) quota = %v, want %v", tt.message, gotQuota, tt.wantQuota)
			}
		})
	}
}

// TestClassifyErrorType 测试基于错误类型的分类
func TestClassifyErrorType(t *testing.T) {
	tests := []struct {
		name         string
		errType      string
		wantFailover bool
		wantQuota    bool
	}{
		// 配额相关
		{"over_quota", "over_quota", true, true},
		{"quota_exceeded", "quota_exceeded", true, true},
		{"rate_limit_exceeded", "rate_limit_exceeded", true, true},
		{"billing_error", "billing_error", true, true},
		{"insufficient_funds", "insufficient_funds", true, true},

		// 认证相关
		{"authentication_error", "authentication_error", true, false},
		{"invalid_api_key", "invalid_api_key", true, false},
		{"permission_denied", "permission_denied", true, false},

		// 服务端错误
		{"server_error", "server_error", true, false},
		{"internal_error", "internal_error", true, false},
		{"service_unavailable", "service_unavailable", true, false},

		// 不应 failover
		{"invalid_request", "invalid_request", false, false},
		{"validation_error", "validation_error", false, false},
		{"unknown_error", "unknown_error", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := classifyErrorType(tt.errType)
			if gotFailover != tt.wantFailover {
				t.Errorf("classifyErrorType(%q) failover = %v, want %v", tt.errType, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("classifyErrorType(%q) quota = %v, want %v", tt.errType, gotQuota, tt.wantQuota)
			}
		})
	}
}

func TestClassifyErrorCode(t *testing.T) {
	tests := []struct {
		name         string
		code         string
		wantFailover bool
		wantQuota    bool
	}{
		// sub2api 业务码
		{"sub2api api key quota exhausted", "API_KEY_QUOTA_EXHAUSTED", true, true},
		{"sub2api usage limit exceeded", "USAGE_LIMIT_EXCEEDED", true, true},
		{"sub2api insufficient balance", "INSUFFICIENT_BALANCE", true, true},
		{"sub2api subscription invalid", "SUBSCRIPTION_INVALID", true, true},
		{"sub2api api key disabled", "API_KEY_DISABLED", true, false},
		{"sub2api api key expired", "API_KEY_EXPIRED", true, false},
		{"sub2api group deleted", "GROUP_DELETED", true, false},
		{"sub2api group disabled", "GROUP_DISABLED", true, false},
		{"google service disabled", "SERVICE_DISABLED", true, false},

		// done-hub / new-api 包装码
		{"done hub insufficient user quota", "insufficient_user_quota", true, true},
		{"done hub pre consume token quota failed", "pre_consume_token_quota_failed", true, true},
		{"done hub service unavailable", "service_unavailable", true, false},
		{"done hub rate limit exceeded", "rate_limit_exceeded", true, true},
		{"new api model not found", "model_not_found", true, false},
		{"new api do request failed", "do_request_failed", true, false},
		{"new api bad response body", "bad_response_body", true, false},
		{"new api read response headers failed", "read_response_headers_failed", true, false},
		{"new api no available key", "channel:no_available_key", true, false},
		{"new api channel invalid key", "channel:invalid_key", true, false},
		{"gemini resource exhausted", "RESOURCE_EXHAUSTED", true, true},

		// 不应由 code 正向 failover
		{"invalid request", "invalid_request", false, false},
		{"sensitive words", "sensitive_words_detected", false, false},
		{"content moderation failed", "content_moderation_failed", false, false},
		{"contains rate limit but unknown", "not_rate_limit_related", false, false},
		{"unknown channel code", "channel:param_override_invalid", false, false},
		{"provider invalid parameter", "InvalidParameter", false, false},
		{"unknown", "unknown_error", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := classifyErrorCode(tt.code)
			if gotFailover != tt.wantFailover {
				t.Errorf("classifyErrorCode(%q) failover = %v, want %v", tt.code, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("classifyErrorCode(%q) quota = %v, want %v", tt.code, gotQuota, tt.wantQuota)
			}
		})
	}
}

// TestClassifyByErrorMessage 测试基于响应体的分类
func TestClassifyByErrorMessage(t *testing.T) {
	tests := []struct {
		name         string
		body         map[string]interface{}
		wantFailover bool
		wantQuota    bool
	}{
		{
			name: "quota error in message",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "You have exceeded your quota",
					"type":    "error",
				},
			},
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name: "auth error in message",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Invalid API key",
					"type":    "error",
				},
			},
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name: "quota error in type",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Error occurred",
					"type":    "over_quota",
				},
			},
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name: "server error in type",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Error occurred",
					"type":    "server_error",
				},
			},
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name: "no failover keywords",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Bad request format",
					"type":    "invalid_request",
				},
			},
			wantFailover: false,
			wantQuota:    false,
		},
		{
			name: "schema invalid value in message",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "Invalid value: 'input_text'. Supported values are: 'output_text' and 'refusal'.",
					"type":    "invalid_request_error",
				},
			},
			wantFailover: false,
			wantQuota:    false,
		},
		{
			name:         "empty body",
			body:         map[string]interface{}{},
			wantFailover: false,
			wantQuota:    false,
		},
		{
			name: "no error field",
			body: map[string]interface{}{
				"status": "error",
			},
			wantFailover: false,
			wantQuota:    false,
		},
		// upstream_error 字段支持（Responses API 错误格式）
		{
			name: "upstream_error string field - auth error",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"type":           "upstream_error",
					"upstream_error": "Invalid API key provided",
				},
			},
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name: "upstream_error string field - quota error",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"type":           "upstream_error",
					"upstream_error": "Rate limit exceeded, please retry later",
				},
			},
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name: "upstream_error nested object with message",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"type": "upstream_error",
					"upstream_error": map[string]interface{}{
						"message": "Insufficient credits",
					},
				},
			},
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name: "detail field - auth error",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"type":   "error",
					"detail": "Token expired, please refresh",
				},
			},
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name: "top level code only - sub2api quota",
			body: map[string]interface{}{
				"code":    "API_KEY_QUOTA_EXHAUSTED",
				"message": "error",
			},
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name: "nested code only - sub2api disabled key",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"code":    "API_KEY_DISABLED",
					"message": "error",
					"type":    "error",
				},
			},
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name: "nested code only - done-hub quota",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"code":    "pre_consume_token_quota_failed",
					"message": "error",
					"type":    "one_hub_error",
				},
			},
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name: "gemini status resource exhausted",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"code":    429,
					"message": "error",
					"status":  "RESOURCE_EXHAUSTED",
				},
			},
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name: "google error info rate limit reason",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "error",
					"details": []interface{}{
						map[string]interface{}{
							"@type":  "type.googleapis.com/google.rpc.ErrorInfo",
							"reason": "RATE_LIMIT_EXCEEDED",
						},
					},
				},
			},
			wantFailover: true,
			wantQuota:    true,
		},
		{
			name: "google error info service disabled reason",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"message": "error",
					"details": []interface{}{
						map[string]interface{}{
							"@type":  "type.googleapis.com/google.rpc.ErrorInfo",
							"reason": "SERVICE_DISABLED",
						},
					},
				},
			},
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name: "new-api transient code only",
			body: map[string]interface{}{
				"error": map[string]interface{}{
					"code":    "do_request_failed",
					"message": "error",
					"type":    "new_api_error",
				},
			},
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name: "done-hub provider invalid parameter is not retryable",
			body: map[string]interface{}{
				"code":    "InvalidParameter",
				"message": "Role must be user or assistant and Content length must be greater than 0",
			},
			wantFailover: false,
			wantQuota:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyBytes, _ := json.Marshal(tt.body)
			gotFailover, gotQuota := classifyByErrorMessage(bodyBytes, "Messages")
			if gotFailover != tt.wantFailover {
				t.Errorf("classifyByErrorMessage() failover = %v, want %v", gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("classifyByErrorMessage() quota = %v, want %v", gotQuota, tt.wantQuota)
			}
		})
	}
}

// TestClassifyByErrorMessage_InvalidJSON 测试无效 JSON 的处理
func TestClassifyByErrorMessage_InvalidJSON(t *testing.T) {
	invalidBodies := [][]byte{
		[]byte("not json"),
		[]byte("{invalid}"),
		[]byte(""),
		nil,
	}

	for _, body := range invalidBodies {
		gotFailover, gotQuota := classifyByErrorMessage(body, "Messages")
		if gotFailover || gotQuota {
			t.Errorf("classifyByErrorMessage(%q) should return (false, false) for invalid JSON", string(body))
		}
	}
}

// TestClassifyMessage_ChineseQuotaKeywords 测试中文额度关键词
func TestClassifyMessage_ChineseQuotaKeywords(t *testing.T) {
	tests := []struct {
		name         string
		message      string
		wantFailover bool
		wantQuota    bool
	}{
		{"预扣费额度失败", "预扣费额度失败, 用户剩余额度: ¥0.053950", true, true},
		{"额度不足", "账户额度不足", true, true},
		{"预扣费失败", "预扣费失败，请充值", true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := classifyMessage(tt.message)
			if gotFailover != tt.wantFailover {
				t.Errorf("classifyMessage(%q) failover = %v, want %v", tt.message, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("classifyMessage(%q) quota = %v, want %v", tt.message, gotQuota, tt.wantQuota)
			}
		})
	}
}
