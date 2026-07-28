package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func TestOpenAIProvider_InjectsModelLevelReasoningAndChannelLevelOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{"model":"gpt-5.1-codex","messages":[{"role":"user","content":"hi"}]}`), context.Background())
	upstream := &config.UpstreamConfig{
		BaseURL:     "https://api.example.com",
		ServiceType: "openai",
		ModelMapping: map[string]string{
			"gpt-5.1-codex": "gpt-5.4-mini",
		},
		ReasoningMapping: map[string]string{
			"gpt-5.1-codex": "xhigh",
		},
		TextVerbosity: "high",
		FastMode:      true,
	}

	p := &OpenAIProvider{}
	req, _, err := p.ConvertToProviderRequest(c, upstream, "sk-test")
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}

	if got := body["model"]; got != "gpt-5.4-mini" {
		t.Fatalf("model = %v, want gpt-5.4-mini", got)
	}

	reasoning, ok := body["reasoning"].(map[string]interface{})
	if !ok || reasoning["effort"] != "xhigh" {
		t.Fatalf("reasoning = %#v, want effort=xhigh", body["reasoning"])
	}

	text, ok := body["text"].(map[string]interface{})
	if !ok || text["verbosity"] != "high" {
		t.Fatalf("text = %#v, want verbosity=high", body["text"])
	}

	if got := body["service_tier"]; got != "priority" {
		t.Fatalf("service_tier = %v, want priority", got)
	}
}

func TestResponsesProvider_PassthroughInjectsModelLevelReasoningAndChannelLevelOptions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := newGinContext(http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-5","input":"hi"}`), context.Background())
	upstream := &config.UpstreamConfig{
		BaseURL:     "https://api.example.com",
		ServiceType: "responses",
		ModelMapping: map[string]string{
			"gpt-5": "gpt-5.4",
		},
		ReasoningMapping: map[string]string{
			"gpt-5": "high",
		},
		TextVerbosity: "medium",
		FastMode:      true,
	}

	p := &ResponsesProvider{}
	req, _, err := p.ConvertToProviderRequest(c, upstream, "sk-test")
	if err != nil {
		t.Fatalf("ConvertToProviderRequest() err = %v", err)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body: %v", err)
	}

	if got := body["model"]; got != "gpt-5.4" {
		t.Fatalf("model = %v, want gpt-5.4", got)
	}

	reasoning, ok := body["reasoning"].(map[string]interface{})
	if !ok || reasoning["effort"] != "high" {
		t.Fatalf("reasoning = %#v, want effort=high", body["reasoning"])
	}

	text, ok := body["text"].(map[string]interface{})
	if !ok || text["verbosity"] != "medium" {
		t.Fatalf("text = %#v, want verbosity=medium", body["text"])
	}

	if got := body["service_tier"]; got != "priority" {
		t.Fatalf("service_tier = %v, want priority", got)
	}
}

func TestResponsesProvider_PassthroughInjectsThinkingParamStyle(t *testing.T) {
	tests := []struct {
		name       string
		effort     string
		wantType   string
		wantEffort string
	}{
		{name: "none disables thinking", effort: "none", wantType: "disabled"},
		{name: "high enables thinking", effort: "high", wantType: "enabled", wantEffort: "high"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c := newGinContext(http.MethodPost, "/v1/responses", []byte(`{"model":"gpt-5","input":"hi","reasoning":{"effort":"medium"},"reasoning_effort":"medium"}`), context.Background())
			upstream := &config.UpstreamConfig{
				BaseURL:             "https://api.example.com",
				ServiceType:         "responses",
				ReasoningParamStyle: "thinking",
				ReasoningMapping: map[string]string{
					"gpt-5": tt.effort,
				},
			}

			req, _, err := (&ResponsesProvider{}).ConvertToProviderRequest(c, upstream, "sk-test")
			if err != nil {
				t.Fatalf("ConvertToProviderRequest() err = %v", err)
			}

			var body map[string]interface{}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode request body: %v", err)
			}

			thinking, ok := body["thinking"].(map[string]interface{})
			if !ok || thinking["type"] != tt.wantType {
				t.Fatalf("thinking = %#v, want type=%s; body=%#v", body["thinking"], tt.wantType, body)
			}
			if gotEffort, _ := thinking["effort"].(string); gotEffort != tt.wantEffort {
				t.Fatalf("thinking.effort = %q, want %q; thinking=%#v", gotEffort, tt.wantEffort, thinking)
			}
			if _, ok := body["reasoning"]; ok {
				t.Fatalf("reasoning should be removed for thinking style: %#v", body)
			}
			if _, ok := body["reasoning_effort"]; ok {
				t.Fatalf("reasoning_effort should be removed for thinking style: %#v", body)
			}
		})
	}
}
