package utils

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPrepareUpstreamHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name        string
		headers     map[string]string
		targetHost  string
		wantHost    string
		shouldExist map[string]bool
	}{
		{
			name: "移除代理相关头部",
			headers: map[string]string{
				"Content-Type":      "application/json",
				"x-proxy-key":       "secret",
				"X-Forwarded-Host":  "original.host",
				"X-Forwarded-Proto": "https",
			},
			targetHost: "upstream.api.com",
			wantHost:   "upstream.api.com",
			shouldExist: map[string]bool{
				"Content-Type":      true,
				"x-proxy-key":       false,
				"X-Forwarded-Host":  false,
				"X-Forwarded-Proto": false,
			},
		},
		{
			name: "保留其他头部",
			headers: map[string]string{
				"Content-Type":  "application/json",
				"User-Agent":    "TestClient/1.0",
				"Accept":        "*/*",
				"Custom-Header": "custom-value",
			},
			targetHost: "api.example.com",
			wantHost:   "api.example.com",
			shouldExist: map[string]bool{
				"Content-Type":  true,
				"User-Agent":    true,
				"Accept":        true,
				"Custom-Header": true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 创建测试请求
			req := httptest.NewRequest("POST", "/test", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			// 创建Gin上下文
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = req

			// 调用函数
			result := PrepareUpstreamHeaders(c, tt.targetHost)

			// 验证Host头部
			if result.Get("Host") != tt.wantHost {
				t.Errorf("Host = %v, want %v", result.Get("Host"), tt.wantHost)
			}

			// 验证头部是否存在
			for header, shouldExist := range tt.shouldExist {
				exists := result.Get(header) != ""
				if exists != shouldExist {
					t.Errorf("Header %s existence = %v, want %v", header, exists, shouldExist)
				}
			}
		})
	}
}

func TestSetAuthenticationHeader(t *testing.T) {
	tests := []struct {
		name              string
		apiKey            string
		wantXApiKey       string
		wantAuthorization string
	}{
		{
			name:              "Claude官方格式密钥",
			apiKey:            "sk-ant-api03-1234567890",
			wantXApiKey:       "sk-ant-api03-1234567890",
			wantAuthorization: "",
		},
		{
			name:              "通用Bearer格式密钥",
			apiKey:            "sk-1234567890abcdef",
			wantXApiKey:       "",
			wantAuthorization: "Bearer sk-1234567890abcdef",
		},
		{
			name:              "其他格式密钥",
			apiKey:            "custom-key-format",
			wantXApiKey:       "",
			wantAuthorization: "Bearer custom-key-format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := http.Header{}
			SetAuthenticationHeader(headers, tt.apiKey)

			if tt.wantXApiKey != "" {
				if got := headers.Get("x-api-key"); got != tt.wantXApiKey {
					t.Errorf("x-api-key = %v, want %v", got, tt.wantXApiKey)
				}
				if headers.Get("Authorization") != "" {
					t.Errorf("Authorization should be empty, got %v", headers.Get("Authorization"))
				}
			} else {
				if got := headers.Get("Authorization"); got != tt.wantAuthorization {
					t.Errorf("Authorization = %v, want %v", got, tt.wantAuthorization)
				}
				if headers.Get("x-api-key") != "" {
					t.Errorf("x-api-key should be empty, got %v", headers.Get("x-api-key"))
				}
			}
		})
	}
}

func TestSetAuthenticationHeaderWithOverride(t *testing.T) {
	tests := []struct {
		name              string
		apiKey            string
		authHeader        string
		wantXApiKey       string
		wantAuthorization string
		wantOverride      bool
	}{
		{
			name:              "空值保持智能选择",
			apiKey:            "sk-1234567890abcdef",
			authHeader:        "",
			wantAuthorization: "Bearer sk-1234567890abcdef",
		},
		{
			name:        "auto保持智能选择",
			apiKey:      "sk-ant-api03-1234567890",
			authHeader:  "auto",
			wantXApiKey: "sk-ant-api03-1234567890",
		},
		{
			name:              "bearer覆盖Claude官方格式",
			apiKey:            "sk-ant-api03-1234567890",
			authHeader:        "bearer",
			wantAuthorization: "Bearer sk-ant-api03-1234567890",
			wantOverride:      true,
		},
		{
			name:         "x-api-key覆盖通用格式",
			apiKey:       "sk-1234567890abcdef",
			authHeader:   "x-api-key",
			wantXApiKey:  "sk-1234567890abcdef",
			wantOverride: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := http.Header{
				"Authorization":  []string{"Bearer old"},
				"X-Api-Key":      []string{"old"},
				"X-Goog-Api-Key": []string{"old"},
			}
			SetAuthenticationHeaderWithOverride(headers, tt.apiKey, tt.authHeader)

			if got := headers.Get("x-api-key"); got != tt.wantXApiKey {
				t.Errorf("x-api-key = %v, want %v", got, tt.wantXApiKey)
			}
			if got := headers.Get("Authorization"); got != tt.wantAuthorization {
				t.Errorf("Authorization = %v, want %v", got, tt.wantAuthorization)
			}
			if headers.Get("x-goog-api-key") != "" {
				t.Errorf("x-goog-api-key should be empty, got %v", headers.Get("x-goog-api-key"))
			}
			if got := HasAuthenticationHeaderOverride(tt.authHeader); got != tt.wantOverride {
				t.Errorf("HasAuthenticationHeaderOverride() = %v, want %v", got, tt.wantOverride)
			}
		})
	}
}

func TestSetGeminiAuthenticationHeader(t *testing.T) {
	headers := http.Header{}
	apiKey := "AIzaSyABC123DEF456"

	SetGeminiAuthenticationHeader(headers, apiKey)

	if got := headers.Get("x-goog-api-key"); got != apiKey {
		t.Errorf("x-goog-api-key = %v, want %v", got, apiKey)
	}

	// 验证其他认证头被删除
	if headers.Get("authorization") != "" {
		t.Errorf("authorization should be empty, got %v", headers.Get("authorization"))
	}
	if headers.Get("x-api-key") != "" {
		t.Errorf("x-api-key should be empty, got %v", headers.Get("x-api-key"))
	}
}

func TestExtractAgentContextClaudeCodeSubagentFromRawMetadata(t *testing.T) {
	body := []byte(`{
		"model": "claude-sonnet-4-20250514",
		"metadata": {
			"user_id": "{\"device_id\":\"dev-123\",\"session_id\":\"sess-456\"}"
		},
		"messages": [
			{"role": "user", "content": "检查这个模块"}
		],
		"tools": [{}, {}, {}, {}, {}]
	}`)

	ctx := ExtractAgentContext(nil, body)
	if ctx == nil {
		t.Fatal("expected agent context")
	}
	if ctx.AgentRole != "subagent" {
		t.Fatalf("expected AgentRole=subagent, got %q", ctx.AgentRole)
	}
	if ctx.AgentType != "claude_code_subagent" {
		t.Fatalf("expected AgentType=claude_code_subagent, got %q", ctx.AgentType)
	}
	if ctx.Confidence != "heuristic" {
		t.Fatalf("expected Confidence=heuristic, got %q", ctx.Confidence)
	}
}

func TestExtractAgentContextClaudeCodeSubagentFromAgentHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{
		"model": "claude-fable-5",
		"metadata": {
			"user_id": "{\"device_id\":\"dev-123\",\"session_id\":\"sess-456\"}"
		},
		"messages": [
			{"role": "user", "content": "第一轮"},
			{"role": "assistant", "content": "ok"},
			{"role": "user", "content": "第二轮"},
			{"role": "assistant", "content": "ok"},
			{"role": "user", "content": "第三轮"}
		],
		"tools": [{}, {}, {}, {}, {}]
	}`)
	req := httptest.NewRequest("POST", "/v1/messages", nil)
	req.Header.Set("X-Claude-Code-Agent-Id", "agent-abc")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	ctx := ExtractAgentContext(c, body)
	if ctx == nil {
		t.Fatal("expected agent context")
	}
	if ctx.AgentRole != "subagent" {
		t.Fatalf("expected AgentRole=subagent, got %q", ctx.AgentRole)
	}
	if ctx.AgentType != "claude_code_subagent" {
		t.Fatalf("expected AgentType=claude_code_subagent, got %q", ctx.AgentType)
	}
	if ctx.Confidence != "exact" {
		t.Fatalf("expected Confidence=exact, got %q", ctx.Confidence)
	}
}

func TestExtractAgentContextClaudeCodeMainWithoutAgentHeader(t *testing.T) {
	body := []byte(`{
		"model": "claude-fable-5",
		"metadata": {
			"user_id": "{\"device_id\":\"dev-123\",\"session_id\":\"sess-456\"}"
		},
		"messages": [
			{"role": "user", "content": "第一轮"},
			{"role": "assistant", "content": "ok"},
			{"role": "user", "content": "第二轮"},
			{"role": "assistant", "content": "ok"},
			{"role": "user", "content": "第三轮"}
		],
		"tools": [{}, {}, {}, {}, {}]
	}`)

	ctx := ExtractAgentContext(nil, body)
	if ctx == nil {
		t.Fatal("expected agent context")
	}
	if ctx.AgentRole != "main" {
		t.Fatalf("expected AgentRole=main, got %q", ctx.AgentRole)
	}
}

func TestEnsureCompatibleUserAgent(t *testing.T) {
	tests := []struct {
		name            string
		serviceType     string
		initialUA       string
		expectedUA      string
		shouldBeChanged bool
	}{
		{
			name:            "Claude服务 - 空User-Agent",
			serviceType:     "claude",
			initialUA:       "",
			expectedUA:      "claude-cli/2.0.34 (external, cli)",
			shouldBeChanged: true,
		},
		{
			name:            "Claude服务 - 非Claude-CLI User-Agent（透传，不替换）",
			serviceType:     "claude",
			initialUA:       "Mozilla/5.0",
			expectedUA:      "Mozilla/5.0",
			shouldBeChanged: false,
		},
		{
			name:            "Claude服务 - 已有Claude-CLI User-Agent",
			serviceType:     "claude",
			initialUA:       "claude-cli/2.0.34 (external, cli)",
			expectedUA:      "claude-cli/2.0.34 (external, cli)",
			shouldBeChanged: false,
		},
		{
			name:            "非Claude服务 - 保留原User-Agent",
			serviceType:     "openai",
			initialUA:       "CustomClient/1.0",
			expectedUA:      "CustomClient/1.0",
			shouldBeChanged: false,
		},
		{
			name:            "Gemini服务 - 保留原User-Agent",
			serviceType:     "gemini",
			initialUA:       "GeminiClient/2.0",
			expectedUA:      "GeminiClient/2.0",
			shouldBeChanged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := http.Header{}
			if tt.initialUA != "" {
				headers.Set("User-Agent", tt.initialUA)
			}

			EnsureCompatibleUserAgent(headers, tt.serviceType)

			got := headers.Get("User-Agent")
			if got != tt.expectedUA {
				t.Errorf("User-Agent = %v, want %v", got, tt.expectedUA)
			}
		})
	}
}

func TestApplyCustomHeaders(t *testing.T) {
	tests := []struct {
		name        string
		initial     map[string]string
		custom      map[string]string
		wantHeaders map[string]string
	}{
		{
			name:    "添加新头部",
			initial: map[string]string{"Content-Type": "application/json"},
			custom:  map[string]string{"X-Custom": "value"},
			wantHeaders: map[string]string{
				"Content-Type": "application/json",
				"X-Custom":     "value",
			},
		},
		{
			name:    "覆盖已有头部",
			initial: map[string]string{"Authorization": "Bearer old"},
			custom:  map[string]string{"Authorization": "Bearer new"},
			wantHeaders: map[string]string{
				"Authorization": "Bearer new",
			},
		},
		{
			name:    "跳过空白key或value",
			initial: map[string]string{},
			custom:  map[string]string{"": "value", "Key": "", "  ": "x", "Valid": "ok"},
			wantHeaders: map[string]string{
				"Valid": "ok",
			},
		},
		{
			name:        "空customHeaders",
			initial:     map[string]string{"Keep": "this"},
			custom:      nil,
			wantHeaders: map[string]string{"Keep": "this"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := http.Header{}
			for k, v := range tt.initial {
				headers.Set(k, v)
			}

			ApplyCustomHeaders(headers, tt.custom)

			for k, want := range tt.wantHeaders {
				if got := headers.Get(k); got != want {
					t.Errorf("Header %s = %v, want %v", k, got, want)
				}
			}
		})
	}
}

func TestExtractUnifiedSessionID_UsesTopLevelUserID(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4-pro","user_id":"deepseek_user_123"}`)

	if got := ExtractUnifiedSessionID(nil, body); got != "deepseek_user_123" {
		t.Fatalf("ExtractUnifiedSessionID() = %q, want deepseek_user_123", got)
	}
}

func TestExtractUnifiedSessionID_ClientRequestIDIsFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Header.Set("X-Client-Request-Id", "req_per_request")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	body := []byte(`{"model":"gpt-5.5","prompt_cache_key":"stable_cache_key","metadata":{"user_id":"meta_user"}}`)
	if got := ExtractUnifiedSessionID(c, body); got != "stable_cache_key" {
		t.Fatalf("ExtractUnifiedSessionID() = %q, want stable_cache_key", got)
	}

	if got := ExtractUnifiedSessionID(c, []byte(`{}`)); got != "req_per_request" {
		t.Fatalf("ExtractUnifiedSessionID() fallback = %q, want req_per_request", got)
	}
}
