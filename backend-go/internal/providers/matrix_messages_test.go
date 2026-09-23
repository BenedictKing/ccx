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

func TestMessagesEntry_RequestMatrix_AllFourUpstreams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name            string
		serviceType     string
		provider        Provider
		path            string
		body            string
		expectedURL     string
		expectedModel   string
		expectFieldPath string
	}{
		{
			name:            "messages_to_claude",
			serviceType:     "claude",
			provider:        &ClaudeProvider{},
			path:            "/v1/messages",
			body:            `{"model":"sonnet","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`,
			expectedURL:     "https://api.example.com/v1/messages",
			expectedModel:   "claude-3-5-sonnet",
			expectFieldPath: "messages",
		},
		{
			name:            "messages_to_openai",
			serviceType:     "openai",
			provider:        &OpenAIProvider{},
			path:            "/v1/messages",
			body:            `{"model":"sonnet","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`,
			expectedURL:     "https://api.example.com/v1/chat/completions",
			expectedModel:   "gpt-5.4",
			expectFieldPath: "messages",
		},
		{
			name:            "messages_to_gemini",
			serviceType:     "gemini",
			provider:        &GeminiProvider{},
			path:            "/v1/messages",
			body:            `{"model":"sonnet","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`,
			expectedURL:     "https://api.example.com/v1beta/models/gemini-2.5-pro:generateContent",
			expectedModel:   "gemini-2.5-pro",
			expectFieldPath: "contents",
		},
		{
			name:            "messages_to_responses",
			serviceType:     "responses",
			provider:        &ResponsesProvider{},
			path:            "/v1/messages",
			body:            `{"model":"sonnet","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`,
			expectedURL:     "https://api.example.com/v1/responses",
			expectedModel:   "gpt-5.4",
			expectFieldPath: "input",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 显式 ModelMapping 已退役：客户端直接以上游真实模型名发起请求。
			reqJSON := strings.Replace(tt.body, `"model":"sonnet"`, fmt.Sprintf(`"model":%q`, tt.expectedModel), 1)
			c := newGinContext(http.MethodPost, tt.path, []byte(reqJSON), context.Background())
			upstream := &config.UpstreamConfig{
				BaseURL:     "https://api.example.com",
				ServiceType: tt.serviceType,
			}

			req, _, err := tt.provider.ConvertToProviderRequest(c, upstream, "sk-test")
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
