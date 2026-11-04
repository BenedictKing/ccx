package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func TestRedirectModelInBodyPreservesFields(t *testing.T) {
	body := []byte(`{"model":"sonnet","max_tokens":64000,"messages":[{"role":"user","content":"hi"}],"system":"<total_tokens>100 tokens left</total_tokens>"}`)
	got := redirectModelInBody(body, &config.UpstreamConfig{ModelMapping: map[string]string{"sonnet": "vendor-claude"}})
	var decoded map[string]interface{}
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["model"] != "vendor-claude" || decoded["max_tokens"] != float64(64000) {
		t.Fatalf("请求字段未正确保留或改写: %s", got)
	}
	if _, exists := decoded["system"]; exists {
		t.Fatalf("换模型后应剥离失真的预算提醒: %s", got)
	}
	unchanged := redirectModelInBody(body, &config.UpstreamConfig{})
	if string(unchanged) != string(body) {
		t.Fatal("未配置映射时应原样返回请求体")
	}
}

func TestOpenAIProviderMapsModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{"model":"sonnet","messages":[{"role":"user","content":"hi"}]}`), context.Background())
	upstream := &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "openai", ModelMapping: map[string]string{"sonnet": "gpt-target"}}
	req, _, err := (&OpenAIProvider{}).ConvertToProviderRequest(c, upstream, "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "gpt-target" {
		t.Fatalf("model = %v, want gpt-target", body["model"])
	}
}

func TestResponsesProviderMapsModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := newGinContext(http.MethodPost, "/v1/responses", []byte(`{"model":"codex","input":"hi"}`), context.Background())
	upstream := &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "responses", ModelMapping: map[string]string{"codex": "gpt-target"}}
	req, _, err := (&ResponsesProvider{}).ConvertToProviderRequest(c, upstream, "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "gpt-target" {
		t.Fatalf("model = %v, want gpt-target", body["model"])
	}
}

func TestGeminiProviderMapsModelInURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c := newGinContext(http.MethodPost, "/v1/messages", []byte(`{"model":"sonnet","messages":[{"role":"user","content":"hi"}]}`), context.Background())
	upstream := &config.UpstreamConfig{BaseURL: "https://api.example.com", ServiceType: "gemini", ModelMapping: map[string]string{"sonnet": "gemini-target"}}
	req, _, err := (&GeminiProvider{}).ConvertToProviderRequest(c, upstream, "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(req.URL.String(), "/models/gemini-target:generateContent") {
		t.Fatalf("url = %s, want mapped Gemini model", req.URL.String())
	}
}
