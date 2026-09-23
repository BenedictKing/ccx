package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestApplyNativeToolStreaming(t *testing.T) {
	glm := &config.UpstreamConfig{ProviderID: "glm", ServiceType: "openai"}

	t.Run("流式工具请求自动开启", func(t *testing.T) {
		req := map[string]interface{}{"stream": true, "tools": []interface{}{map[string]interface{}{"type": "function"}}}
		ApplyNativeToolStreaming(req, glm)
		if req["tool_stream"] != true {
			t.Fatalf("tool_stream = %#v, want true", req["tool_stream"])
		}
	})

	t.Run("尊重客户端显式关闭", func(t *testing.T) {
		req := map[string]interface{}{"stream": true, "tools": []interface{}{map[string]interface{}{"type": "function"}}, "tool_stream": false}
		ApplyNativeToolStreaming(req, glm)
		if req["tool_stream"] != false {
			t.Fatalf("tool_stream = %#v, want false", req["tool_stream"])
		}
	})

	t.Run("非官方 GLM 渠道不注入", func(t *testing.T) {
		req := map[string]interface{}{"stream": true, "tools": []interface{}{map[string]interface{}{"type": "function"}}}
		ApplyNativeToolStreaming(req, &config.UpstreamConfig{ServiceType: "openai"})
		if _, exists := req["tool_stream"]; exists {
			t.Fatalf("自定义渠道不应注入 tool_stream: %#v", req)
		}
	})
}

func TestGLMProviderConversionsEnableNativeToolStreaming(t *testing.T) {
	upstream := &config.UpstreamConfig{
		ProviderID:          "glm",
		ServiceType:         "openai",
		BaseURL:             "https://open.bigmodel.cn/api/paas/v4#",
		ReasoningParamStyle: "reasoning_effort",
	}

	t.Run("Messages 转 Chat", func(t *testing.T) {
		body := []byte(`{"model":"glm-5.2","stream":true,"messages":[{"role":"user","content":"天气"}],"tools":[{"name":"get_weather","description":"weather","input_schema":{"type":"object","properties":{}}}]}`)
		c := newGinContext(http.MethodPost, "/v1/messages", body, context.Background())
		req, _, err := (&OpenAIProvider{}).ConvertToProviderRequest(c, upstream, "test-key")
		if err != nil {
			t.Fatal(err)
		}
		assertToolStreamEnabled(t, req)
	})

	t.Run("Responses 转 Chat", func(t *testing.T) {
		body := []byte(`{"model":"glm-5.2","stream":true,"input":"天气","tools":[{"type":"function","name":"get_weather","description":"weather","parameters":{"type":"object","properties":{}}}]}`)
		c := newGinContext(http.MethodPost, "/v1/responses", body, context.Background())
		req, _, err := (&ResponsesProvider{}).ConvertToProviderRequest(c, upstream, "test-key")
		if err != nil {
			t.Fatal(err)
		}
		assertToolStreamEnabled(t, req)
	})
}

func assertToolStreamEnabled(t *testing.T, req *http.Request) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["tool_stream"] != true {
		t.Fatalf("tool_stream = %#v, body=%#v", body["tool_stream"], body)
	}
	return body
}
