package gemini

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

func TestBuildProviderRequest_ClaudeTargetUserAgentFallbackAndCustomOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)

	geminiReq := &types.GeminiRequest{
		Contents: []types.GeminiContent{{Parts: []types.GeminiPart{{Text: "hi"}}}},
	}

	t.Run("missing user agent gets fallback", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", nil).WithContext(context.Background())

		req, err := buildProviderRequest(c, &config.UpstreamConfig{ServiceType: "claude"}, "https://api.example.com", "sk-selected", geminiReq, "gemini-2.0-flash", false)
		if err != nil {
			t.Fatalf("buildProviderRequest() err = %v", err)
		}
		if got := req.Header.Get("User-Agent"); got != "claude-cli/2.0.34 (external, cli)" {
			t.Fatalf("User-Agent = %q, want Claude fallback", got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer sk-selected" {
			t.Fatalf("Authorization = %q, want selected key", got)
		}
	})

	t.Run("custom user agent wins", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.0-flash:generateContent", nil).WithContext(context.Background())
		c.Request.Header.Set("User-Agent", "InboundUA/1.0")
		upstream := &config.UpstreamConfig{
			ServiceType: "claude",
			CustomHeaders: map[string]string{
				"User-Agent":    "GeminiCustomUA/1.0",
				"Authorization": "Bearer custom",
			},
		}

		req, err := buildProviderRequest(c, upstream, "https://api.example.com", "sk-selected", geminiReq, "gemini-2.0-flash", false)
		if err != nil {
			t.Fatalf("buildProviderRequest() err = %v", err)
		}
		if got := req.Header.Get("User-Agent"); got != "GeminiCustomUA/1.0" {
			t.Fatalf("User-Agent = %q, want custom UA", got)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer sk-selected" {
			t.Fatalf("Authorization = %q, want selected key", got)
		}
	})
}
