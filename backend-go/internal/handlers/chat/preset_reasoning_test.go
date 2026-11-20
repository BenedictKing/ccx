package chat

import (
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/tidwall/gjson"
	"testing"
)

func TestChatPresetReasoningCompatibility(t *testing.T) {
	for _, model := range []string{"kimi-k3.1-pro", "glm-5.3"} {
		t.Run(model, func(t *testing.T) {
			body := []byte(`{"model":"` + model + `","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"xhigh"}`)
			upstream := &config.UpstreamConfig{ServiceType: "openai", ReasoningParamStyle: "reasoning_effort"}
			out, err := buildChatCompletionRequestBody(body, model, model, upstream, true)
			if err != nil {
				t.Fatal(err)
			}
			if got := gjson.GetBytes(out, "reasoning_effort").String(); got != "max" {
				t.Fatalf("effort=%q; %s", got, out)
			}
			upstream.ReasoningParamStyle = ""
			out, err = buildChatCompletionRequestBody(body, model, model, upstream, true)
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != string(body) {
				t.Fatalf("default passthrough rewritten: %s", out)
			}
		})
	}
}

func TestGLMPresetMigratesDisabledThinking(t *testing.T) {
	body := []byte(`{"model":"glm-5.3","messages":[{"role":"user","content":"hi"}],"thinking":{"type":"disabled"}}`)
	upstream := &config.UpstreamConfig{ServiceType: "openai", ReasoningParamStyle: "reasoning_effort"}
	out, err := buildChatCompletionRequestBody(body, "glm-5.3", "glm-5.3", upstream, true)
	if err != nil {
		t.Fatal(err)
	}
	if gjson.GetBytes(out, "thinking.type").String() != "enabled" || gjson.GetBytes(out, "reasoning_effort").String() != "low" {
		t.Fatalf("disabled thinking not migrated to low effort: %s", out)
	}
}
