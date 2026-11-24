package channelpreset

import "testing"

func TestGLMDefaultsUseChatCompletion(t *testing.T) {
	preset, ok := FindPreset(ProviderGLM)
	if !ok {
		t.Fatal("GLM preset missing")
	}
	for _, target := range []string{TargetMessages, TargetChat, TargetResponses} {
		t.Run(target, func(t *testing.T) {
			payload, err := BuildPayload(CreateChannelRequest{Provider: ProviderGLM, Target: target, APIKey: "sk-test"})
			if err != nil {
				t.Fatal(err)
			}
			if payload.BaseURL != "https://open.bigmodel.cn/api/paas/v4#" || payload.ServiceType != "openai" {
				t.Fatalf("wrong default endpoint: %+v", payload)
			}
			if !payload.NoVision {
				t.Fatal("text-only GLM preset enables vision")
			}
			if payload.ReasoningParamStyle != "reasoning_effort" {
				t.Fatalf("style=%q", payload.ReasoningParamStyle)
			}
			if target == TargetMessages {
				if payload.ModelMapping["haiku"] != "glm-5.3-flash" || payload.ModelMapping["sonnet"] != "glm-5.3-flash" {
					t.Fatalf("wrong flash source categories: %+v", payload.ModelMapping)
				}
				if payload.ReasoningMapping["haiku"] != "high" || payload.ReasoningMapping["sonnet"] != "max" {
					t.Fatalf("wrong reasoning mapping: %+v", payload.ReasoningMapping)
				}
			}
			if target == TargetResponses && payload.ModelMapping["sol"] != "glm-5.4" {
				t.Fatalf("wrong source categories: %+v", payload.ModelMapping)
			}
			for _, plan := range FilterPlansForTarget(preset, target) {
				if plan.Protocol() == "anthropic" {
					t.Fatalf("incompatible default choice: %s", plan.ID)
				}
			}
		})
	}
}

func TestGLMCodingPlanMessagesUsesChatCompletion(t *testing.T) {
	payload, err := BuildPayload(CreateChannelRequest{Provider: ProviderGLM, Target: TargetMessages, PlanID: "coding", APIKey: "sk-test"})
	if err != nil {
		t.Fatal(err)
	}
	if payload.ServiceType != "openai" || payload.BaseURL != "https://open.bigmodel.cn/api/coding/paas/v4#" {
		t.Fatalf("wrong coding endpoint: %+v", payload)
	}
	if payload.ModelMapping["sonnet"] != "glm-5.3-flash" || payload.ReasoningMapping["sonnet"] != "max" || !payload.NoVision {
		t.Fatalf("wrong coding model config: %+v", payload)
	}
}
