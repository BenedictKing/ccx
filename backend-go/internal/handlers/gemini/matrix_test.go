package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

func TestGeminiEntry_RequestMatrix_AllFourUpstreams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	geminiReq := &types.GeminiRequest{
		Contents: []types.GeminiContent{{
			Role:  "user",
			Parts: []types.GeminiPart{{Text: "hi"}},
		}},
	}

	tests := []struct {
		name            string
		serviceType     string
		expectedURL     string
		expectFieldPath string
	}{
		{"gemini_to_gemini", "gemini", "https://api.example.com/v1beta/models/gemini-2.0-flash:generateContent", "contents"},
		{"gemini_to_claude", "claude", "https://api.example.com/v1/messages", "messages"},
		{"gemini_to_openai", "openai", "https://api.example.com/v1/chat/completions", "messages"},
		{"gemini_to_responses", "responses", "https://api.example.com/v1/responses", "input"},
		{"gemini_hash_baseurl_openai", "openai", "https://core.blink.new/api/v1/ai/chat/completions", "messages"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", nil).WithContext(context.Background())

			upstream := &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: tt.serviceType}
			if tt.name == "gemini_hash_baseurl_openai" {
				upstream.BaseURL = "https://core.blink.new/api/v1/ai#"
			}
			req, err := buildProviderRequest(c, upstream, upstream.BaseURL, "test-key", geminiReq, "gemini-2.0-flash", false)
			if err != nil {
				t.Fatalf("buildProviderRequest() err = %v", err)
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

func TestGeminiEntry_SameFormatPreservesUnknownRequestFields(t *testing.T) {
	gin.SetMode(gin.TestMode)

	bodyBytes := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi","vendorPart":{"kept":true}}],"vendorContent":1}],"vendorTop":{"nested":2}}`)
	var geminiReq types.GeminiRequest
	if err := json.Unmarshal(bodyBytes, &geminiReq); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", nil).WithContext(context.Background())
	c.Set("requestBodyBytes", bodyBytes)

	upstream := &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "gemini"}
	req, err := buildProviderRequest(c, upstream, upstream.BaseURL, "test-key", &geminiReq, "gemini-2.0-flash", false)
	if err != nil {
		t.Fatalf("buildProviderRequest() err = %v", err)
	}
	forwardedBody, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(forwardedBody, &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	vendorTop, ok := body["vendorTop"].(map[string]interface{})
	if !ok || vendorTop["nested"] != float64(2) {
		t.Fatalf("vendorTop = %#v, want nested=2; body=%s", body["vendorTop"], forwardedBody)
	}
	contents := body["contents"].([]interface{})
	content := contents[0].(map[string]interface{})
	if content["vendorContent"] != float64(1) {
		t.Fatalf("vendorContent = %#v, want 1; body=%s", content["vendorContent"], forwardedBody)
	}
	parts := content["parts"].([]interface{})
	part := parts[0].(map[string]interface{})
	vendorPart, ok := part["vendorPart"].(map[string]interface{})
	if !ok || vendorPart["kept"] != true {
		t.Fatalf("vendorPart = %#v, want kept=true; body=%s", part["vendorPart"], forwardedBody)
	}
}

func TestGeminiEntry_SameFormatNativeURLVersionRules(t *testing.T) {
	gin.SetMode(gin.TestMode)
	geminiReq := &types.GeminiRequest{
		Contents: []types.GeminiContent{{
			Role:  "user",
			Parts: []types.GeminiPart{{Text: "hi"}},
		}},
	}

	tests := []struct {
		name     string
		baseURL  string
		isStream bool
		wantURL  string
	}{
		{
			name:    "normal adds v1beta",
			baseURL: "https://api.example.com",
			wantURL: "https://api.example.com/v1beta/models/gemini-2.0-flash:generateContent",
		},
		{
			name:    "existing v1 is preserved",
			baseURL: "https://api.example.com/v1",
			wantURL: "https://api.example.com/v1/models/gemini-2.0-flash:generateContent",
		},
		{
			name:    "hash skips version prefix",
			baseURL: "https://api.example.com/gemini#",
			wantURL: "https://api.example.com/gemini/models/gemini-2.0-flash:generateContent",
		},
		{
			name:     "stream appends sse query",
			baseURL:  "https://api.example.com/v1beta",
			isStream: true,
			wantURL:  "https://api.example.com/v1beta/models/gemini-2.0-flash:streamGenerateContent?alt=sse",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", nil).WithContext(context.Background())

			upstream := &config.UpstreamConfig{BaseURL: tt.baseURL, ServiceType: "gemini"}
			req, err := buildProviderRequest(c, upstream, upstream.BaseURL, "test-key", geminiReq, "gemini-2.0-flash", tt.isStream)
			if err != nil {
				t.Fatalf("buildProviderRequest() err = %v", err)
			}
			if got := req.URL.String(); got != tt.wantURL {
				t.Fatalf("url = %s, want %s", got, tt.wantURL)
			}
		})
	}
}
