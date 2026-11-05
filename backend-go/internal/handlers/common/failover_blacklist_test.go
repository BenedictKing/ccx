package common

import (
	"testing"

	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
)

func TestIsInsufficientBalanceMessage_HighConfidenceVariants(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want bool
	}{
		{name: "english insufficient credits", msg: "You have insufficient credits remaining", want: true},
		{name: "english out of credits", msg: "This account is out of credits", want: true},
		{name: "english no balance", msg: "no balance", want: true},
		{name: "english insufficient funds", msg: "payment declined: insufficient funds", want: true},
		{name: "english quota used up", msg: "quota used up for current billing period", want: true},
		{name: "english token quota not enough", msg: "token quota is not enough, token remain quota: ¥0.100000, need quota: ¥0.300000", want: true},
		{name: "english daily usage limit exceeded", msg: "daily usage limit exceeded", want: true},
		{name: "english daily limit exceeded", msg: "reason=\"DAILY_LIMIT_EXCEEDED\" message=\"daily usage limit exceeded\"", want: true},
		{name: "english monthly call limit", msg: "Monthly call limit exceeded for your plan", want: true},
		{name: "english monthly limit exceeded", msg: "monthly limit exceeded", want: true},
		{name: "english call limit exceeded for your plan", msg: "call limit exceeded for your plan", want: true},
		{name: "chinese balance exhausted", msg: "账户余额已用尽，请充值", want: true},
		{name: "chinese quota used up", msg: "账户额度已用完", want: true},
		{name: "chinese quota exhausted", msg: "当前额度耗尽", want: true},
		// 火山/火山方舟 quota exceeded 场景（exceeded 在 quota 前面的语序）
		{name: "volc ark 5-hour quota exceeded", msg: "You have exceeded the 5-hour usage quota. It will reset at 2026-07-03 11:37:21 +0800 CST. We recommend upgrading your plan for more quota, or waiting", want: true},
		{name: "volc ark account quota exceeded", msg: "Account quota exceeded. Quota reset time: 2026-07-03T00:00:00Z", want: true},
		{name: "generic quota exceeded", msg: "Monthly quota exceeded for your plan", want: true},
		{name: "english subscription not found", msg: "No active subscription found for this group", want: true},
		{name: "negative billing setup", msg: "billing not enabled for this account", want: false},
		// 临时限流错误不应被误判为余额不足
		{name: "rate limit exceeded", msg: "Rate limit exceeded, please retry later", want: false},
		{name: "upstream rate limit", msg: "Upstream rate limit exceeded, please retry later", want: false},
		{name: "too many requests", msg: "Too many requests, please try again later", want: false},
		{name: "chinese rate limit", msg: "请求过于频繁，请稍后重试", want: false},
		{name: "requests per minute", msg: "You have exceeded 60 requests per minute", want: false},
		// Gemini token-per-minute 临时配额限流（含 quota + exceeded 但属于短期限流）
		{name: "gemini tokens per minute", msg: "Quota exceeded for quota metric GenerateRequestsPerMinutePerProject of service generativelanguage.googleapis.com", want: false},
		{name: "gemini input tokens per minute", msg: "Quota exceeded for quota metric input tokens per minute per project", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isInsufficientBalanceMessage(tt.msg)
			if got != tt.want {
				t.Fatalf("isInsufficientBalanceMessage(%q) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
}

func TestShouldBlacklistKey_BalanceMessages(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       BlacklistResult
	}{
		{
			name:       "403 top level code insufficient balance should blacklist",
			statusCode: 403,
			body:       `{"code":"INSUFFICIENT_BALANCE","message":"Insufficient account balance"}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "Insufficient account balance",
			},
		},
		{
			name:       "403 nested error code insufficient balance should blacklist",
			statusCode: 403,
			body:       `{"error":{"code":"INSUFFICIENT_BALANCE","message":"Insufficient account balance"}}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "Insufficient account balance",
			},
		},
		{
			name:       "400 invalid request wrapper with balance message should blacklist",
			statusCode: 400,
			body:       `{"type":"error","error":{"type":"invalid_request_error","message":"余额不足，请联系客服微信 claudemaster 充值 (insufficient balance — add WeChat: claudemaster to top up)"},"request_id":"req_123"}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "余额不足，请联系客服微信 claudemaster 充值 (insufficient balance — add WeChat: claudemaster to top up)",
			},
		},
		{
			name:       "403 string error field with insufficient balance should blacklist",
			statusCode: 403,
			body:       `{"error":"API Key额度不足，请访问https://right.codes查看详情"}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "API Key额度不足，请访问https://right.codes查看详情",
			},
		},
		{
			name:       "401 string error should still honor top level authentication type",
			statusCode: 401,
			body:       `{"error":"认证失败","type":"authentication_error"}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "authentication_error",
				Message:         "认证失败",
			},
		},
		{
			name:       "401 string error invalid api key without type should blacklist",
			statusCode: 401,
			body:       `{"error":"无效的API Key"}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "authentication_error",
				Message:         "无效的API Key",
			},
		},
		{
			name:       "401 new api token expired message should blacklist as authentication",
			statusCode: 401,
			body:       `{"error":{"code":"","message":"该令牌已过期 (request id: 202605041407066680249308268d9d6QnF3nAtC)","type":"new_api_error"}}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "authentication_error",
				Message:         "该令牌已过期 (request id: 202605041407066680249308268d9d6QnF3nAtC)",
			},
		},
		{
			name:       "403 top level insufficient account balance message should blacklist",
			statusCode: 403,
			body:       `{"message":"Insufficient account balance"}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "Insufficient account balance",
			},
		},
		{
			name:       "403 prededuct quota message should blacklist as insufficient balance",
			statusCode: 403,
			body:       `{"error":{"type":"new_api_error","message":"预扣费额度失败, 用户剩余额度: ＄0.411202, 需要预扣费额度: ＄0.553368"},"type":"error"}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "预扣费额度失败, 用户剩余额度: ＄0.411202, 需要预扣费额度: ＄0.553368",
			},
		},
		{
			name:       "403 token quota not enough message should blacklist as insufficient balance",
			statusCode: 403,
			body:       `{"error":{"message":"token quota is not enough, token remain quota: ¥0.100000, need quota: ¥0.300000 (request id: 20260426121858142194522mDUp325B)","type":"new_api_error","param":"","code":"pre_consume_quota_failed"},"type":"error"}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "token quota is not enough, token remain quota: ¥0.100000, need quota: ¥0.300000 (request id: 20260426121858142194522mDUp325B)",
			},
		},
		{
			name:       "403 new api insufficient user quota should blacklist as insufficient balance",
			statusCode: 403,
			body:       `{"error":{"message":"用户额度不足, 剩余额度: ¥-0.136964 (request id: 202606221209254492365268268d9d6mwf4XMcd)","type":"new_api_error","param":"","code":"insufficient_user_quota"}}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "用户额度不足, 剩余额度: ¥-0.136964 (request id: 202606221209254492365268268d9d6mwf4XMcd)",
			},
		},
		{
			name:       "429 insufficient quota message should blacklist as insufficient balance",
			statusCode: 429,
			body:       `{"error":{"message":"insufficient quota for current billing period"}}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "insufficient quota for current billing period",
			},
		},
		{
			name:       "429 top level usage limit exceeded code should blacklist as insufficient balance",
			statusCode: 429,
			body:       `{"code":"USAGE_LIMIT_EXCEEDED","message":"error: code=429 reason=\"DAILY_LIMIT_EXCEEDED\" message=\"daily usage limit exceeded\" metadata=map[]"}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "error: code=429 reason=\"DAILY_LIMIT_EXCEEDED\" message=\"daily usage limit exceeded\" metadata=map[]",
			},
		},
		{
			name:       "429 nested daily limit exceeded code should blacklist as insufficient balance",
			statusCode: 429,
			body:       `{"error":{"code":"DAILY_LIMIT_EXCEEDED","message":"daily usage limit exceeded"}}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "daily usage limit exceeded",
			},
		},
		{
			name:       "429 volc ark AccountQuotaExceeded should blacklist as insufficient balance",
			statusCode: 429,
			body:       `{"error":{"code":"AccountQuotaExceeded","message":"You have exceeded the 5-hour usage quota. It will reset at 2026-07-03 11:37:21 +0800 CST. We recommend upgrading your plan for more quota, or waiting"}}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "You have exceeded the 5-hour usage quota. It will reset at 2026-07-03 11:37:21 +0800 CST. We recommend upgrading your plan for more quota, or waiting",
			},
		},
		{
			name:       "429 volc ark AccountQuotaExceeded with different wording should blacklist",
			statusCode: 429,
			body:       `{"error":{"code":"AccountQuotaExceeded","message":"Account quota exceeded for this billing period. Try again after quota resets."}}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "Account quota exceeded for this billing period. Try again after quota resets.",
			},
		},
		{
			name:       "401 token status exhausted message should blacklist as insufficient balance",
			statusCode: 401,
			body:       `{"error":{"code":"","message":"该令牌额度已用尽 TokenStatusExhausted[sk-duK***qqX]"}}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "该令牌额度已用尽 TokenStatusExhausted[sk-duK***qqX]",
			},
		},
		{
			name:       "401 out of credits message should blacklist as insufficient balance",
			statusCode: 401,
			body:       `{"error":{"message":"This account is out of credits"}}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "This account is out of credits",
			},
		},
		{
			name:       "403 billing not enabled should not be misclassified as balance",
			statusCode: 403,
			body:       `{"error":{"message":"billing not enabled for this account"}}`,
			want:       BlacklistResult{},
		},
		{
			name:       "403 permission denied should not be misclassified as balance",
			statusCode: 403,
			body:       `{"error":{"type":"forbidden","message":"permission denied for this resource"}}`,
			want:       BlacklistResult{},
		},
		{
			name:       "403 explicit permission error should still be permission blacklist",
			statusCode: 403,
			body:       `{"error":{"type":"permission_denied","message":"permission denied"}}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "permission_error",
				Message:         "permission denied",
			},
		},
		{
			name:       "403 subscription not found code should blacklist as insufficient balance",
			statusCode: 403,
			body:       `{"code":"SUBSCRIPTION_NOT_FOUND","message":"No active subscription found for this group"}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "No active subscription found for this group",
			},
		},
		{
			name:       "403 subscription not found message should blacklist as insufficient balance",
			statusCode: 403,
			body:       `{"message":"No active subscription found for this group"}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "No active subscription found for this group",
			},
		},
		{
			name:       "403 account balance is negative should blacklist as insufficient balance",
			statusCode: 403,
			body:       `{"error":{"message":"account balance is negative, please recharge first","type":"forbidden_error"},"type":"error"}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "account balance is negative, please recharge first",
			},
		},
		{
			name:       "403 subscription expired should blacklist as insufficient balance",
			statusCode: 403,
			body:       `{"error":{"code":"","message":"您的套餐已过期，请续费后继续使用 (request id: 202606040135546143661918268d9d6tMVNpobz)","type":"new_api_error"}}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "您的套餐已过期，请续费后继续使用 (request id: 202606040135546143661918268d9d6tMVNpobz)",
			},
		},
		{
			name:       "401 sub2api api key disabled code should blacklist as auth",
			statusCode: 401,
			body:       `{"code":"API_KEY_DISABLED","message":"API key is disabled"}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "authentication_error",
				Message:         "API key is disabled",
			},
		},
		{
			name:       "403 sub2api api key expired code should blacklist as auth",
			statusCode: 403,
			body:       `{"error":{"code":"API_KEY_EXPIRED","message":"API key 已过期","type":"error"}}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "authentication_error",
				Message:         "API key 已过期",
			},
		},
		{
			name:       "403 sub2api group disabled code should blacklist as permission",
			statusCode: 403,
			body:       `{"error":{"code":"GROUP_DISABLED","message":"API Key 所属分组已停用","type":"permission_error"}}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "permission_error",
				Message:         "API Key 所属分组已停用",
			},
		},
		{
			name:       "403 done hub quota code should blacklist even with generic wrapper type",
			statusCode: 403,
			body:       `{"error":{"code":"pre_consume_token_quota_failed","message":"error","type":"one_hub_error"}}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "error",
			},
		},
		{
			name:       "429 rate limit code should not blacklist as balance",
			statusCode: 429,
			body:       `{"error":{"code":"rate_limit_exceeded","message":"error","type":"one_hub_error"}}`,
			want:       BlacklistResult{},
		},
		// 以下用例验证双词组合匹配能覆盖旧精确关键词列表遗漏的变体
		{
			name:       "403 credit limit reached should blacklist via dual-word",
			statusCode: 403,
			body:       `{"error":{"message":"credit limit reached for this API key","type":"api_error"}}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "credit limit reached for this API key",
			},
		},
		{
			name:       "403 额度到期请充值 should blacklist via dual-word",
			statusCode: 403,
			body:       `{"error":{"message":"您的额度已到期，请充值后重试","type":"error"}}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "您的额度已到期，请充值后重试",
			},
		},
		{
			name:       "403 funds depleted should blacklist via dual-word",
			statusCode: 403,
			body:       `{"error":{"message":"Your account funds have been depleted. Please top up.","type":"billing_error"}}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "Your account funds have been depleted. Please top up.",
			},
		},
		// 429 临时限流错误不应拉黑（通过熔断机制处理）
		{
			name:       "429 rate_limit_error should not blacklist",
			statusCode: 429,
			body:       `{"error":{"message":"Upstream rate limit exceeded, please retry later","type":"rate_limit_error"}}`,
			want:       BlacklistResult{},
		},
		// Compshare 月度额度耗尽：大写字段 + 数字 RetCode，应拉黑
		{
			name:       "429 compshare monthly call limit uppercase RetCode should blacklist",
			statusCode: 429,
			body:       `{"RetCode":226615,"Message":"Monthly call limit exceeded for your plan"}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "Monthly call limit exceeded for your plan",
			},
		},
		{
			name:       "429 compshare monthly call limit lowercase retCode should blacklist",
			statusCode: 429,
			body:       `{"retCode":226615,"message":"Monthly call limit exceeded for your plan"}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "insufficient_balance",
				Message:         "Monthly call limit exceeded for your plan",
			},
		},
		{
			name:       "429 too many requests should not blacklist",
			statusCode: 429,
			body:       `{"error":{"message":"Too many requests, please try again later"}}`,
			want:       BlacklistResult{},
		},
		{
			name:       "429 rate limit exceeded should not blacklist",
			statusCode: 429,
			body:       `{"error":{"message":"Rate limit exceeded for this API key"}}`,
			want:       BlacklistResult{},
		},
		{
			name:       "429 请求过于频繁 should not blacklist",
			statusCode: 429,
			body:       `{"error":{"message":"请求过于频繁，请稍后重试"}}`,
			want:       BlacklistResult{},
		},
		{
			name:       "403 no permission to access group should blacklist as permission error",
			statusCode: 403,
			body:       `{"error":{"code":"","message":"No permission to access group 最后又最后又最后的回响. Switch this API key to an available group in the console (request id: 202607130111451433593328268d9d6aFT)","type":"new_api_error"}}`,
			want: BlacklistResult{
				ShouldBlacklist: true,
				Reason:          "permission_error",
				Message:         "No permission to access group 最后又最后又最后的回响. Switch this API key to an available group in the console (request id: 202607130111451433593328268d9d6aFT)",
			},
		},
		{
			name:       "503 no permission to access group should not blacklist (only 403)",
			statusCode: 503,
			body:       `{"error":{"code":"","message":"No permission to access group foo","type":"new_api_error"}}`,
			want:       BlacklistResult{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldBlacklistKey(tt.statusCode, []byte(tt.body))
			if got != tt.want {
				t.Fatalf("ShouldBlacklistKey(%d, %s) = %+v, want %+v", tt.statusCode, tt.body, got, tt.want)
			}
		})
	}
}

func TestShouldRetryWithNextKey_MessageWinsOverInvalidRequestCode(t *testing.T) {
	body := []byte(`{"type":"error","error":{"code":"invalid_request_error","type":"invalid_request_error","message":"余额不足，请联系客服微信 claudemaster 充值 (insufficient balance - add WeChat: claudemaster to top up)"}}`)

	tests := []struct {
		name      string
		fuzzyMode bool
	}{
		{name: "normal mode", fuzzyMode: false},
		{name: "fuzzy mode", fuzzyMode: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := ShouldRetryWithNextKey(400, body, tt.fuzzyMode, "Messages")
			if !gotFailover {
				t.Fatalf("ShouldRetryWithNextKey(400, balance invalid_request, %v) failover = false, want true", tt.fuzzyMode)
			}
			if !gotQuota {
				t.Fatalf("ShouldRetryWithNextKey(400, balance invalid_request, %v) quota = false, want true", tt.fuzzyMode)
			}
		})
	}
}

// TestShouldRetryWithNextKey_SensitiveWordsDetected 测试敏感词检测错误不应重试
// 这是修复的核心场景：500 + sensitive_words_detected 不应触发无限重试
func TestShouldRetryWithNextKey_SensitiveWordsDetected(t *testing.T) {
	// 模拟生产环境的敏感词检测错误
	body := []byte(`{"error":{"message":"sensitive words detected","type":"new_api_error","param":"","code":"sensitive_words_detected"}}`)

	tests := []struct {
		name         string
		statusCode   int
		fuzzyMode    bool
		wantFailover bool
		wantQuota    bool
	}{
		{
			name:         "500 with sensitive_words_detected - normal mode",
			statusCode:   500,
			fuzzyMode:    false,
			wantFailover: false, // 不应重试
			wantQuota:    false,
		},
		{
			name:         "500 with sensitive_words_detected - fuzzy mode",
			statusCode:   500,
			fuzzyMode:    true,
			wantFailover: false, // 即使在 fuzzy 模式下也不应重试
			wantQuota:    false,
		},
		{
			name:         "400 with sensitive_words_detected - normal mode",
			statusCode:   400,
			fuzzyMode:    false,
			wantFailover: false,
			wantQuota:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := ShouldRetryWithNextKey(tt.statusCode, body, tt.fuzzyMode, "Messages")
			if gotFailover != tt.wantFailover {
				t.Errorf("ShouldRetryWithNextKey(%d, sensitive_words_body, %v) failover = %v, want %v",
					tt.statusCode, tt.fuzzyMode, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("ShouldRetryWithNextKey(%d, sensitive_words_body, %v) quota = %v, want %v",
					tt.statusCode, tt.fuzzyMode, gotQuota, tt.wantQuota)
			}
		})
	}
}

// TestIsModelRoutingError 测试模型路由错误识别（仅用于状态码归一化）
func TestIsModelRoutingError(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "model_not_found code",
			body: `{"error":{"code":"model_not_found","message":"No available channel for model gpt-5.4 under group codex (distributor)","type":"new_api_error"}}`,
			want: true,
		},
		{
			name: "no available channel message without code",
			body: `{"error":{"message":"No available channel for model gpt-5.4 under group codex","type":"new_api_error"}}`,
			want: true,
		},
		{
			name: "model_not_found code case insensitive",
			body: `{"error":{"code":"MODEL_NOT_FOUND","message":"some error"}}`,
			want: true,
		},
		{
			name: "generic model not found without no available channel",
			body: `{"error":{"message":"model not found: gpt-5.4","type":"error"}}`,
			want: false,
		},
		{
			name: "quota error not client config",
			body: `{"error":{"type":"new_api_error","message":"预扣费额度失败, 用户剩余额度: ¥0.053950"}}`,
			want: false,
		},
		{
			name: "auth error not client config",
			body: `{"error":{"type":"new_api_error","message":"该令牌已过期","code":""}}`,
			want: false,
		},
		{
			name: "invalid request not client config",
			body: `{"error":{"code":"invalid_request","message":"bad request"}}`,
			want: false,
		},
		{
			name: "invalid json",
			body: `not json`,
			want: false,
		},
		{
			name: "no error object",
			body: `{"status":"ok"}`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isModelRoutingError([]byte(tt.body))
			if got != tt.want {
				t.Errorf("isModelRoutingError() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestNormalizeUpstreamErrorStatus 测试状态码归一化
func TestNormalizeUpstreamErrorStatus(t *testing.T) {
	modelNotFoundBody := []byte(`{"error":{"code":"model_not_found","message":"No available channel for model gpt-5.4 under group codex","type":"new_api_error"}}`)

	tests := []struct {
		name       string
		status     int
		body       []byte
		wantStatus int
	}{
		{"503 model_not_found normalizes to 404", 503, modelNotFoundBody, 404},
		{"500 model_not_found normalizes to 404", 500, modelNotFoundBody, 404},
		{"502 model_not_found normalizes to 404", 502, modelNotFoundBody, 404},
		{"403 model_not_found stays 403", 403, modelNotFoundBody, 403},
		{"200 model_not_found stays 200", 200, modelNotFoundBody, 200},
		{"503 empty body stays 503", 503, []byte{}, 503},
		{"503 nil body stays 503", 503, nil, 503},
		{"503 quota error stays 503", 503, []byte(`{"error":{"message":"quota exceeded"}}`), 503},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeUpstreamErrorStatus(tt.status, tt.body)
			if got != tt.wantStatus {
				t.Errorf("normalizeUpstreamErrorStatus(%d, ...) = %d, want %d", tt.status, got, tt.wantStatus)
			}
		})
	}
}

func TestHandleAllFailedFuzzyMode_ModelNotFoundNormalizesTo404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	modelNotFoundBody := []byte(`{"error":{"code":"model_not_found","message":"No available channel for model gpt-5.4 under group codex","type":"new_api_error"}}`)
	quotaBody := []byte(`{"error":{"message":"quota exceeded"}}`)

	tests := []struct {
		name       string
		handle     func(*gin.Context)
		wantStatus int
	}{
		{
			name: "all channels failed - model_not_found normalizes to 404",
			handle: func(c *gin.Context) {
				HandleAllChannelsFailed(c, true, &FailoverError{Status: http.StatusServiceUnavailable, Body: modelNotFoundBody}, nil, "Messages")
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "all keys failed - model_not_found normalizes to 404",
			handle: func(c *gin.Context) {
				HandleAllKeysFailed(c, true, &FailoverError{Status: http.StatusServiceUnavailable, Body: modelNotFoundBody}, nil, "Messages")
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "all channels failed - 429 quota stays generic 503 in fuzzy mode",
			handle: func(c *gin.Context) {
				HandleAllChannelsFailed(c, true, &FailoverError{Status: http.StatusTooManyRequests, Body: quotaBody}, nil, "Messages")
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "all keys failed - 429 quota stays generic 503 in fuzzy mode",
			handle: func(c *gin.Context) {
				HandleAllKeysFailed(c, true, &FailoverError{Status: http.StatusTooManyRequests, Body: quotaBody}, nil, "Messages")
			},
			wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)

			tt.handle(ctx)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tt.wantStatus)
			}
			if recorder.Body.String() == "" {
				t.Fatal("response body is empty")
			}
		})
	}
}

// TestShouldRetryWithNextKey_ModelNotFound 测试 model_not_found 允许 failover
// 模拟生产环境真实响应：上游 new-api 对 model_not_found 返回 503
// model_not_found 应允许 failover（不同 channel/上游实例可能支持该模型）
func TestShouldRetryWithNextKey_ModelNotFound(t *testing.T) {
	// 用户实际遇到的生产环境响应体
	body := []byte(`{"error":{"code":"model_not_found","message":"No available channel for model gpt-5.4 under group codex (distributor) (request id: 20260506023117409510104ddJSBEMJ)","type":"new_api_error"}}`)

	tests := []struct {
		name         string
		statusCode   int
		fuzzyMode    bool
		wantFailover bool
		wantQuota    bool
	}{
		{
			name:         "503 model_not_found - normal mode allows failover",
			statusCode:   503,
			fuzzyMode:    false,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "503 model_not_found - fuzzy mode allows failover",
			statusCode:   503,
			fuzzyMode:    true,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "500 model_not_found - normal mode allows failover",
			statusCode:   500,
			fuzzyMode:    false,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "500 model_not_found - fuzzy mode allows failover",
			statusCode:   500,
			fuzzyMode:    true,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "403 model_not_found - normal mode allows failover",
			statusCode:   403,
			fuzzyMode:    false,
			wantFailover: true,
			wantQuota:    false,
		},
		{
			name:         "403 model_not_found - fuzzy mode allows failover",
			statusCode:   403,
			fuzzyMode:    true,
			wantFailover: true,
			wantQuota:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFailover, gotQuota := ShouldRetryWithNextKey(tt.statusCode, body, tt.fuzzyMode, "Messages")
			if gotFailover != tt.wantFailover {
				t.Errorf("ShouldRetryWithNextKey(%d, model_not_found_body, %v) failover = %v, want %v",
					tt.statusCode, tt.fuzzyMode, gotFailover, tt.wantFailover)
			}
			if gotQuota != tt.wantQuota {
				t.Errorf("ShouldRetryWithNextKey(%d, model_not_found_body, %v) quota = %v, want %v",
					tt.statusCode, tt.fuzzyMode, gotQuota, tt.wantQuota)
			}
		})
	}
}
