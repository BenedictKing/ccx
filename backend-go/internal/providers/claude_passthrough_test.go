package providers

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func TestClaudeProvider_Sub2APIPassthrough_AuthOnlyAndHeaderFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	enabled := true
	requestBody := []byte(`{"model":"claude-3-5-sonnet-latest","messages":[{"role":"user","content":"hi"}]}`)
	c := newGinContext(http.MethodPost, "/v1/messages", requestBody, context.Background())
	c.Request.Header.Set("Authorization", "Bearer inbound-token")
	c.Request.Header.Set("X-Api-Key", "inbound-api-key")
	c.Request.Header.Set("Cookie", "session=abc")
	c.Request.Header.Set("Proxy-Authorization", "Basic abc")

	upstream := &config.UpstreamConfig{
		BaseURL:                         "https://api.anthropic.com",
		ServiceType:                     "claude",
		Sub2APIPassthroughEnabled:       &enabled,
		ModelMapping:                    map[string]string{"claude-3-5-sonnet-latest": "claude-3-opus-20240229"},
		StrictRequestPassthroughEnabled: &enabled,
	}

	p := &ClaudeProvider{}
	req, forwardedBody, err := p.ConvertToProviderRequest(c, upstream, "sk-ant-test-key")
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}

	if got := req.Header.Get("x-api-key"); got != "sk-ant-test-key" {
		t.Fatalf("x-api-key = %q, want %q", got, "sk-ant-test-key")
	}
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization should be cleared, got %q", got)
	}
	if got := req.Header.Get("Cookie"); got != "" {
		t.Fatalf("Cookie should be stripped in auth-only passthrough, got %q", got)
	}
	if got := req.Header.Get("Proxy-Authorization"); got != "" {
		t.Fatalf("Proxy-Authorization should be stripped in auth-only passthrough, got %q", got)
	}
	if gjson.GetBytes(forwardedBody, "model").String() != "claude-3-5-sonnet-latest" {
		t.Fatalf("model should keep original in auth-only passthrough, got %s", gjson.GetBytes(forwardedBody, "model").String())
	}
}

func TestClaudeProvider_Sub2APIPassthrough_OnlyAppliesToAnthropicKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	enabled := true
	requestBody := []byte(`{"model":"claude-3-5-sonnet-latest","messages":[{"role":"user","content":"hi"}]}`)
	c := newGinContext(http.MethodPost, "/v1/messages", requestBody, context.Background())

	upstream := &config.UpstreamConfig{
		BaseURL:                   "https://api.anthropic.com",
		ServiceType:               "claude",
		Sub2APIPassthroughEnabled: &enabled,
		ModelMapping:              map[string]string{"claude-3-5-sonnet-latest": "claude-3-opus-20240229"},
	}

	p := &ClaudeProvider{}
	req, _, err := p.ConvertToProviderRequest(c, upstream, "sk-non-anthropic")
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}

	if got := req.Header.Get("Authorization"); got != "Bearer sk-non-anthropic" {
		t.Fatalf("Authorization = %q, want %q", got, "Bearer sk-non-anthropic")
	}
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read req body: %v", err)
	}
	if gjson.GetBytes(bodyBytes, "model").String() != "claude-3-opus-20240229" {
		t.Fatalf("model should be rewritten when not Anthropic API key passthrough, got %s", gjson.GetBytes(bodyBytes, "model").String())
	}
}
