package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func TestResponsesEntry_RequestMatrix_AllFourUpstreams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name            string
		serviceType     string
		path            string
		body            string
		expectedURL     string
		expectedModel   string
		expectFieldPath string
	}{
		{
			name:            "responses_to_responses",
			serviceType:     "responses",
			path:            "/v1/responses",
			body:            `{"model":"gpt-5","input":"hello","stream":true}`,
			expectedURL:     "https://api.example.com/v1/responses",
			expectedModel:   "gpt-5.4",
			expectFieldPath: "input",
		},
		{
			name:            "responses_to_claude",
			serviceType:     "claude",
			path:            "/v1/responses",
			body:            `{"model":"gpt-5","input":"hello","stream":false}`,
			expectedURL:     "https://api.example.com/v1/messages",
			expectedModel:   "claude-3-5-sonnet",
			expectFieldPath: "messages",
		},
		{
			name:            "responses_to_openai",
			serviceType:     "openai",
			path:            "/v1/responses",
			body:            `{"model":"gpt-5","input":"hello","stream":false}`,
			expectedURL:     "https://api.example.com/v1/chat/completions",
			expectedModel:   "gpt-5.4",
			expectFieldPath: "messages",
		},
		{
			name:            "responses_to_gemini",
			serviceType:     "gemini",
			path:            "/v1/responses",
			body:            `{"model":"gpt-5","input":"hello","stream":false}`,
			expectedURL:     "https://api.example.com/v1beta/models/gpt-5.4:generateContent",
			expectedModel:   "gpt-5.4",
			expectFieldPath: "contents",
		},
		{
			name:            "responses_hash_baseurl_openai",
			serviceType:     "openai",
			path:            "/v1/responses",
			body:            `{"model":"gpt-5","input":"hello"}`,
			expectedURL:     "https://core.blink.new/api/v1/ai/chat/completions",
			expectedModel:   "gpt-5.4",
			expectFieldPath: "messages",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 显式 ModelMapping 已退役：客户端直接以上游真实模型名发起请求。
			reqJSON := strings.Replace(tt.body, `"model":"gpt-5"`, fmt.Sprintf(`"model":%q`, tt.expectedModel), 1)
			c := newGinContext(http.MethodPost, tt.path, []byte(reqJSON), context.Background())
			upstream := &config.UpstreamConfig{
				BaseURL:     "https://api.example.com",
				ServiceType: tt.serviceType,
			}
			if tt.name == "responses_hash_baseurl_openai" {
				upstream.BaseURL = "https://core.blink.new/api/v1/ai#"
			}

			provider := &ResponsesProvider{}
			req, _, err := provider.ConvertToProviderRequest(c, upstream, "sk-test")
			if err != nil {
				t.Fatalf("ConvertToProviderRequest() err = %v", err)
			}
			if req.URL.String() != tt.expectedURL {
				t.Fatalf("url = %s, want %s", req.URL.String(), tt.expectedURL)
			}

			var body map[string]interface{}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if _, ok := body[tt.expectFieldPath]; !ok {
				t.Fatalf("expected field %q in request body, got %#v", tt.expectFieldPath, body)
			}
		})
	}
}

func TestResponsesEntry_RequestMatrix_PreservesKeyParams(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("responses_passthrough_preserves_instructions_and_stream", func(t *testing.T) {
		c := newGinContext(http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-5","input":"hi","instructions":"be helpful","stream":true,"max_output_tokens":1024}`), context.Background())
		upstream := &config.UpstreamConfig{
			BaseURL:     "https://api.example.com",
			ServiceType: "responses",
		}

		provider := &ResponsesProvider{}
		req, _, err := provider.ConvertToProviderRequest(c, upstream, "sk-test")
		if err != nil {
			t.Fatalf("ConvertToProviderRequest() err = %v", err)
		}

		var body map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}

		if body["instructions"] != "be helpful" {
			t.Fatalf("instructions = %v, want be helpful", body["instructions"])
		}
		if body["stream"] != true {
			t.Fatalf("stream = %v, want true", body["stream"])
		}
		// passthrough 模式下 max_output_tokens 保留原字段名
		if maxTok, ok := body["max_output_tokens"]; !ok || maxTok == nil {
			t.Fatalf("max_output_tokens field missing in passthrough body, got %#v", body)
		}
	})

	t.Run("responses_passthrough_strips_internal_metadata", func(t *testing.T) {
		c := newGinContext(http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-5","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}],"internal_chat_message_metadata_passthrough":{"conversation_id":"local"},"status":"completed"}],"internal_chat_message_metadata_passthrough":{"conversation_id":"local"},"transformer_metadata":{"codex_tool_compat_enabled":false}}`), context.Background())
		upstream := &config.UpstreamConfig{
			BaseURL:     "https://api.example.com",
			ServiceType: "responses",
		}

		provider := &ResponsesProvider{}
		req, _, err := provider.ConvertToProviderRequest(c, upstream, "sk-test")
		if err != nil {
			t.Fatalf("ConvertToProviderRequest() err = %v", err)
		}

		var body map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}

		if _, ok := body["internal_chat_message_metadata_passthrough"]; ok {
			t.Fatalf("不应透传顶层客户端内部元数据: %#v", body)
		}
		if _, ok := body["transformer_metadata"]; ok {
			t.Fatalf("不应透传转换器内部元数据: %#v", body)
		}
		input, ok := body["input"].([]interface{})
		if !ok || len(input) != 1 {
			t.Fatalf("input = %#v，期望 1 个条目", body["input"])
		}
		item, ok := input[0].(map[string]interface{})
		if !ok {
			t.Fatalf("input[0] = %#v，期望 object", input[0])
		}
		if _, ok := item["internal_chat_message_metadata_passthrough"]; ok {
			t.Fatalf("不应透传 input 条目客户端内部元数据: %#v", item)
		}
	})

	t.Run("responses_to_claude_preserves_instructions_as_system", func(t *testing.T) {
		c := newGinContext(http.MethodPost, "/v1/responses", []byte(`{"model":"claude","input":"hi","instructions":"be helpful","stream":false}`), context.Background())
		upstream := &config.UpstreamConfig{
			BaseURL:     "https://api.example.com",
			ServiceType: "claude",
		}

		provider := &ResponsesProvider{}
		req, _, err := provider.ConvertToProviderRequest(c, upstream, "sk-test")
		if err != nil {
			t.Fatalf("ConvertToProviderRequest() err = %v", err)
		}

		var body map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}

		// Claude 上游应该将 instructions 转为 system 字段
		if system, ok := body["system"]; !ok || system == nil {
			t.Fatalf("system field missing in Claude request body, got %#v", body)
		}
	})

	t.Run("responses_to_gemini_uses_model_in_url", func(t *testing.T) {
		c := newGinContext(http.MethodPost, "/v1/responses", []byte(`{"model":"gemini-2.5-pro","input":"hi","stream":false}`), context.Background())
		upstream := &config.UpstreamConfig{
			BaseURL:     "https://api.example.com",
			ServiceType: "gemini",
		}

		provider := &ResponsesProvider{}
		req, _, err := provider.ConvertToProviderRequest(c, upstream, "sk-test")
		if err != nil {
			t.Fatalf("ConvertToProviderRequest() err = %v", err)
		}

		expectedURL := "https://api.example.com/v1beta/models/gemini-2.5-pro:generateContent"
		if req.URL.String() != expectedURL {
			t.Fatalf("url = %s, want %s", req.URL.String(), expectedURL)
		}
	})
}

func TestResponsesProvider_NormalizeNonstandardChatRolesForOpenAIChatUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name      string
		enabled   bool
		wantFirst string
	}{
		{name: "default_off", enabled: false, wantFirst: "developer"},
		{name: "enabled", enabled: true, wantFirst: "user"},
	}

	body := []byte(`{"model":"gpt-5","input":[{"type":"message","role":"developer","content":"dev message"},{"type":"message","role":"user","content":"user message"}]}`)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newGinContext(http.MethodPost, "/v1/responses", body, context.Background())
			upstream := &config.UpstreamConfig{
				BaseURL:     "https://api.example.com",
				ServiceType: "openai",
			}
			upstream.SetLearnedCompatTrait(config.TraitNormalizeNonstandardChatRoles, tt.enabled)

			provider := &ResponsesProvider{}
			req, _, err := provider.ConvertToProviderRequest(c, upstream, "sk-test")
			if err != nil {
				t.Fatalf("ConvertToProviderRequest() err = %v", err)
			}

			var got map[string]interface{}
			if err := json.NewDecoder(req.Body).Decode(&got); err != nil {
				t.Fatalf("decode body: %v", err)
			}

			messages, ok := got["messages"].([]interface{})
			if !ok || len(messages) != 2 {
				t.Fatalf("messages = %#v, want 2 items", got["messages"])
			}
			first, ok := messages[0].(map[string]interface{})
			if !ok {
				t.Fatalf("message[0] = %#v, want object", messages[0])
			}
			if first["role"] != tt.wantFirst {
				t.Fatalf("message[0].role = %v, want %s", first["role"], tt.wantFirst)
			}
		})
	}
}

// TestResponsesEntry_BudgetReminderStripMatrix 验证 Codex 预算提醒剔除的分支语义：
// passthrough 命中映射才剔、未命中保留；converter 跨协议无条件剔。
func TestResponsesEntry_BudgetReminderStripMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reminderBody := `{"model":"gpt-5","input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"You have 710 tokens left in this context window."}]},{"type":"message","role":"user","content":"hi"}]}`

	tests := []struct {
		name          string
		serviceType   string
		wantStripped  bool
		checkField    string
		wantRemaining int
	}{
		{
			name:          "passthrough_no_mapping_keeps",
			serviceType:   "responses",
			wantStripped:  false,
			checkField:    "input",
			wantRemaining: 2,
		},
		{
			name:          "converter_chat_unconditional_strips",
			serviceType:   "openai",
			wantStripped:  true,
			checkField:    "messages",
			wantRemaining: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newGinContext(http.MethodPost, "/v1/responses", []byte(reminderBody), context.Background())
			upstream := &config.UpstreamConfig{
				BaseURL:     "https://api.example.com",
				ServiceType: tt.serviceType,
			}

			provider := &ResponsesProvider{}
			req, _, err := provider.ConvertToProviderRequest(c, upstream, "sk-test")
			if err != nil {
				t.Fatalf("ConvertToProviderRequest() err = %v", err)
			}

			var body map[string]interface{}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			items, ok := body[tt.checkField].([]interface{})
			if !ok {
				t.Fatalf("field %q missing: %#v", tt.checkField, body)
			}
			if len(items) != tt.wantRemaining {
				t.Fatalf("%s: got %d items, want %d (stripped=%v)", tt.name, len(items), tt.wantRemaining, tt.wantStripped)
			}
			// converter 分支 developer 提醒被剔后仅剩 user 消息；
			// passthrough 未命中时 developer 提醒原样保留
			raw, _ := json.Marshal(body[tt.checkField])
			if tt.wantStripped && strings.Contains(string(raw), "tokens left") {
				t.Fatalf("%s: reminder must be stripped, got %s", tt.name, raw)
			}
			if !tt.wantStripped && !strings.Contains(string(raw), "tokens left") {
				t.Fatalf("%s: reminder must be preserved", tt.name)
			}
		})
	}
}
