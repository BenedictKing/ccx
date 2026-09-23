package channelpreset

import (
	"slices"
	"testing"
)

func TestBuildPayload(t *testing.T) {
	tests := []struct {
		name                  string
		req                   CreateChannelRequest
		wantBaseURL           string
		wantService           string
		wantVision            bool
		wantNormalizeMetadata *bool
		wantStripBilling      bool
		wantCodex             bool
		wantStripCodex        bool
		wantModels            []string
		wantReasoningStyle    string
		wantNormalizeSystem   bool
		wantAuthHeader        string
	}{
		{
			name:                  "deepseek messages (anthropic endpoint)",
			req:                   CreateChannelRequest{Provider: ProviderDeepSeek, Target: TargetMessages, APIKey: "sk-test"},
			wantBaseURL:           "https://api.deepseek.com/anthropic",
			wantService:           "claude",
			wantVision:            false,
			wantNormalizeMetadata: boolRef(true),
			wantStripBilling:      true,
			wantNormalizeSystem:   true,
		},
		{
			name:        "deepseek chat (openai endpoint)",
			req:         CreateChannelRequest{Provider: ProviderDeepSeek, Target: TargetChat, APIKey: "sk-test"},
			wantBaseURL: "https://api.deepseek.com/v1",
			wantService: "openai",
			wantVision:  false,
		},
		{
			name:        "deepseek responses (openai endpoint)",
			req:         CreateChannelRequest{Provider: ProviderDeepSeek, Target: TargetResponses, APIKey: "sk-test"},
			wantBaseURL: "https://api.deepseek.com/v1",
			wantService: "openai",
			wantVision:  false,
		},
		{
			name:                "mimo messages (token plan)",
			req:                 CreateChannelRequest{Provider: ProviderMiMo, Target: TargetMessages, PlanID: "token-sgp-anthropic", APIKey: "tp-test"},
			wantBaseURL:         "https://token-plan-sgp.xiaomimimo.com/anthropic",
			wantService:         "claude",
			wantNormalizeSystem: true,
			wantReasoningStyle:  "thinking",
		},
		{
			name:                "mimo messages (auto plan)",
			req:                 CreateChannelRequest{Provider: ProviderMiMo, Target: TargetMessages, APIKey: "tp-test"},
			wantBaseURL:         "https://api.xiaomimimo.com/anthropic",
			wantService:         "claude",
			wantNormalizeSystem: true,
			wantReasoningStyle:  "thinking",
		},
		{
			name:               "mimo chat",
			req:                CreateChannelRequest{Provider: ProviderMiMo, Target: TargetChat, APIKey: "tp-test"},
			wantBaseURL:        "https://api.xiaomimimo.com/v1",
			wantService:        "openai",
			wantReasoningStyle: "thinking",
		},
		{
			name:               "mimo responses",
			req:                CreateChannelRequest{Provider: ProviderMiMo, Target: TargetResponses, APIKey: "tp-test"},
			wantBaseURL:        "https://api.xiaomimimo.com/v1",
			wantService:        "responses",
			wantCodex:          true,
			wantStripCodex:     true,
			wantReasoningStyle: "reasoning",
		},
		{
			name:                "compshare messages",
			req:                 CreateChannelRequest{Provider: ProviderCompshare, Target: TargetMessages, APIKey: "cs-test"},
			wantBaseURL:         "https://cp.compshare.cn",
			wantService:         "claude",
			wantVision:          false,
			wantNormalizeSystem: true,
		},
		{
			name:        "compshare chat",
			req:         CreateChannelRequest{Provider: ProviderCompshare, Target: TargetChat, APIKey: "cs-test"},
			wantBaseURL: "https://cp.compshare.cn/v1",
			wantService: "openai",
			wantVision:  false,
		},
		{
			name:        "compshare responses",
			req:         CreateChannelRequest{Provider: ProviderCompshare, Target: TargetResponses, APIKey: "cs-test"},
			wantBaseURL: "https://cp.compshare.cn/v1",
			wantService: "openai",
			wantVision:  false,
		},
		{
			name:        "runapi messages",
			req:         CreateChannelRequest{Provider: ProviderRunAPI, Target: TargetMessages, APIKey: "runapi-test"},
			wantBaseURL: "https://runapi.co/v1",
			wantService: "claude",
		},
		{
			name:        "runapi chat",
			req:         CreateChannelRequest{Provider: ProviderRunAPI, Target: TargetChat, APIKey: "runapi-test"},
			wantBaseURL: "https://runapi.co/v1",
			wantService: "openai",
		},
		{
			name:           "runapi responses",
			req:            CreateChannelRequest{Provider: ProviderRunAPI, Target: TargetResponses, APIKey: "runapi-test"},
			wantBaseURL:    "https://runapi.co/v1",
			wantService:    "responses",
			wantCodex:      false,
			wantStripCodex: false,
		},
		{
			name:        "kimi chat",
			req:         CreateChannelRequest{Provider: ProviderKimi, Target: TargetChat, APIKey: "sk-test"},
			wantBaseURL: "https://api.moonshot.cn/v1",
			wantService: "openai",
		},
		{
			name:           "kimi responses",
			req:            CreateChannelRequest{Provider: ProviderKimi, Target: TargetResponses, APIKey: "sk-test"},
			wantBaseURL:    "https://api.moonshot.cn/v1",
			wantService:    "openai",
			wantCodex:      true,
			wantStripCodex: true,
		},
		{
			name:           "kimi coding plan responses",
			req:            CreateChannelRequest{Provider: ProviderKimi, Target: TargetResponses, PlanID: "coding-openai-chat", APIKey: "sk-test"},
			wantBaseURL:    "https://api.kimi.com/coding/v1",
			wantService:    "openai",
			wantCodex:      true,
			wantStripCodex: true,
		},
		{
			name:                "kimi coding plan messages",
			req:                 CreateChannelRequest{Provider: ProviderKimi, Target: TargetMessages, PlanID: "coding-anthropic", APIKey: "sk-test"},
			wantBaseURL:         "https://api.kimi.com/coding",
			wantService:         "claude",
			wantNormalizeSystem: true,
		},
		{
			name:        "glm chat",
			req:         CreateChannelRequest{Provider: ProviderGLM, Target: TargetChat, APIKey: "sk-test"},
			wantBaseURL: "https://open.bigmodel.cn/api/coding/paas/v4#",
			wantService: "openai",
		},
		{
			name:           "glm responses",
			req:            CreateChannelRequest{Provider: ProviderGLM, Target: TargetResponses, APIKey: "sk-test"},
			wantBaseURL:    "https://open.bigmodel.cn/api/coding/paas/v4#",
			wantService:    "openai",
			wantCodex:      true,
			wantStripCodex: true,
		},
		{
			name:        "minimax chat",
			req:         CreateChannelRequest{Provider: ProviderMiniMax, Target: TargetChat, APIKey: "sk-test"},
			wantBaseURL: "https://api.minimax.chat/v1",
			wantService: "openai",
		},
		{
			name:           "minimax responses",
			req:            CreateChannelRequest{Provider: ProviderMiniMax, Target: TargetResponses, APIKey: "sk-test"},
			wantBaseURL:    "https://api.minimax.chat/v1",
			wantService:    "openai",
			wantCodex:      false,
			wantStripCodex: false,
		},
		{
			name:        "dashscope chat",
			req:         CreateChannelRequest{Provider: ProviderDashScope, Target: TargetChat, APIKey: "sk-test"},
			wantBaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
			wantService: "openai",
		},
		{
			name:           "dashscope responses",
			req:            CreateChannelRequest{Provider: ProviderDashScope, Target: TargetResponses, APIKey: "sk-test"},
			wantBaseURL:    "https://dashscope.aliyuncs.com/compatible-mode/v1",
			wantService:    "openai",
			wantCodex:      true,
			wantStripCodex: true,
		},
		{
			name:           "dashscope coding plan responses",
			req:            CreateChannelRequest{Provider: ProviderDashScope, Target: TargetResponses, PlanID: "coding-openai-chat", APIKey: "sk-sp-test"},
			wantBaseURL:    "https://coding.dashscope.aliyuncs.com/v1",
			wantService:    "openai",
			wantCodex:      true,
			wantStripCodex: true,
		},
		{
			name:                "dashscope coding plan messages",
			req:                 CreateChannelRequest{Provider: ProviderDashScope, Target: TargetMessages, PlanID: "coding-anthropic", APIKey: "sk-sp-test"},
			wantBaseURL:         "https://coding.dashscope.aliyuncs.com/apps/anthropic",
			wantService:         "claude",
			wantNormalizeSystem: true,
		},
		{
			name:        "dashscope coding plan chat",
			req:         CreateChannelRequest{Provider: ProviderDashScope, Target: TargetChat, PlanID: "coding-openai-chat", APIKey: "sk-sp-test"},
			wantBaseURL: "https://coding.dashscope.aliyuncs.com/v1",
			wantService: "openai",
		},
		{
			name:                "dashscope token plan messages",
			req:                 CreateChannelRequest{Provider: ProviderDashScope, Target: TargetMessages, PlanID: "token-plan-anthropic", APIKey: "sk-tp-test"},
			wantBaseURL:         "https://token-plan.cn-beijing.maas.aliyuncs.com/apps/anthropic",
			wantService:         "claude",
			wantNormalizeSystem: true,
		},
		{
			name:        "dashscope token plan chat",
			req:         CreateChannelRequest{Provider: ProviderDashScope, Target: TargetChat, PlanID: "token-plan-openai-chat", APIKey: "sk-tp-test"},
			wantBaseURL: "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
			wantService: "openai",
		},
		{
			name:           "dashscope token plan responses",
			req:            CreateChannelRequest{Provider: ProviderDashScope, Target: TargetResponses, PlanID: "token-plan-openai-chat", APIKey: "sk-tp-test"},
			wantBaseURL:    "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
			wantService:    "openai",
			wantCodex:      true,
			wantStripCodex: true,
		},
		{
			name:                "opencode merged defaults to go messages",
			req:                 CreateChannelRequest{Provider: ProviderOpenCodeZen, Target: TargetMessages, APIKey: "sk-test"},
			wantBaseURL:         "https://opencode.ai/zen/go/v1",
			wantService:         "openai",
			wantNormalizeSystem: true,
			wantReasoningStyle:  "reasoning",
			wantAuthHeader:      "bearer",
		},
		{
			name:                "opencode zen messages",
			req:                 CreateChannelRequest{Provider: ProviderOpenCodeZen, Target: TargetMessages, PlanID: "openai-chat", APIKey: "sk-test"},
			wantBaseURL:         "https://opencode.ai/zen/v1",
			wantService:         "openai",
			wantNormalizeSystem: true,
			wantReasoningStyle:  "reasoning",
			wantAuthHeader:      "bearer",
		},
		{
			name:                "opencode go messages",
			req:                 CreateChannelRequest{Provider: ProviderOpenCodeGo, Target: TargetMessages, APIKey: "sk-test"},
			wantBaseURL:         "https://opencode.ai/zen/go/v1",
			wantService:         "openai",
			wantNormalizeSystem: true,
			wantReasoningStyle:  "reasoning",
			wantAuthHeader:      "bearer",
		},
		{
			name:               "opencode zen chat",
			req:                CreateChannelRequest{Provider: ProviderOpenCodeZen, Target: TargetChat, PlanID: "openai-chat", APIKey: "sk-test"},
			wantBaseURL:        "https://opencode.ai/zen/v1",
			wantService:        "openai",
			wantReasoningStyle: "reasoning",
			wantAuthHeader:     "bearer",
		},
		{
			name:               "opencode go chat",
			req:                CreateChannelRequest{Provider: ProviderOpenCodeGo, Target: TargetChat, APIKey: "sk-test"},
			wantBaseURL:        "https://opencode.ai/zen/go/v1",
			wantService:        "openai",
			wantReasoningStyle: "reasoning",
			wantAuthHeader:     "bearer",
		},
		{
			name:               "opencode zen responses",
			req:                CreateChannelRequest{Provider: ProviderOpenCodeZen, Target: TargetResponses, PlanID: "openai-chat", APIKey: "sk-test"},
			wantBaseURL:        "https://opencode.ai/zen/v1",
			wantService:        "openai",
			wantCodex:          true,
			wantStripCodex:     true,
			wantReasoningStyle: "reasoning",
		},
		{
			name:               "opencode go responses",
			req:                CreateChannelRequest{Provider: ProviderOpenCodeGo, Target: TargetResponses, APIKey: "sk-test"},
			wantBaseURL:        "https://opencode.ai/zen/go/v1",
			wantService:        "openai",
			wantCodex:          true,
			wantStripCodex:     true,
			wantReasoningStyle: "reasoning",
		},
		{
			name:                "sensenova messages",
			req:                 CreateChannelRequest{Provider: ProviderSenseNova, Target: TargetMessages, APIKey: "sk-test"},
			wantBaseURL:         "https://token.sensenova.cn",
			wantService:         "claude",
			wantNormalizeSystem: true,
		},
		{
			name:        "sensenova chat",
			req:         CreateChannelRequest{Provider: ProviderSenseNova, Target: TargetChat, APIKey: "sk-test"},
			wantBaseURL: "https://token.sensenova.cn/v1",
			wantService: "openai",
		},
		{
			name:           "sensenova responses",
			req:            CreateChannelRequest{Provider: ProviderSenseNova, Target: TargetResponses, APIKey: "sk-test"},
			wantBaseURL:    "https://token.sensenova.cn/v1",
			wantService:    "openai",
			wantCodex:      true,
			wantStripCodex: true,
		},
		{
			name:                "volc-ark messages (anthropic endpoint)",
			req:                 CreateChannelRequest{Provider: ProviderVolcArk, Target: TargetMessages, APIKey: "ark-test"},
			wantBaseURL:         "https://ark.cn-beijing.volces.com/api/coding",
			wantService:         "claude",
			wantNormalizeSystem: true,
		},
		{
			name:        "volc-ark chat",
			req:         CreateChannelRequest{Provider: ProviderVolcArk, Target: TargetChat, APIKey: "ark-test"},
			wantBaseURL: "https://ark.cn-beijing.volces.com/api/coding/v3",
			wantService: "openai",
		},
		{
			name:        "volc-ark responses",
			req:         CreateChannelRequest{Provider: ProviderVolcArk, Target: TargetResponses, APIKey: "ark-test"},
			wantBaseURL: "https://ark.cn-beijing.volces.com/api/coding/v3",
			wantService: "openai",
		},
		{
			name:                "qianfan messages (anthropic endpoint)",
			req:                 CreateChannelRequest{Provider: ProviderQianfan, Target: TargetMessages, APIKey: "qf-test"},
			wantBaseURL:         "https://qianfan.baidubce.com/anthropic/coding",
			wantService:         "claude",
			wantNormalizeSystem: true,
		},
		{
			name:        "qianfan chat",
			req:         CreateChannelRequest{Provider: ProviderQianfan, Target: TargetChat, APIKey: "qf-test"},
			wantBaseURL: "https://qianfan.baidubce.com/v2/coding#",
			wantService: "openai",
		},
		{
			name:           "qianfan responses",
			req:            CreateChannelRequest{Provider: ProviderQianfan, Target: TargetResponses, APIKey: "qf-test"},
			wantBaseURL:    "https://qianfan.baidubce.com/v2/coding#",
			wantService:    "openai",
			wantCodex:      true,
			wantStripCodex: true,
		},
		{
			name:                "xfyun messages (anthropic endpoint)",
			req:                 CreateChannelRequest{Provider: ProviderXFyun, Target: TargetMessages, APIKey: "xf-test"},
			wantBaseURL:         "https://maas-coding-api.cn-huabei-1.xf-yun.com/anthropic",
			wantService:         "claude",
			wantNormalizeSystem: true,
		},
		{
			name:        "xfyun chat",
			req:         CreateChannelRequest{Provider: ProviderXFyun, Target: TargetChat, APIKey: "xf-test"},
			wantBaseURL: "https://maas-coding-api.cn-huabei-1.xf-yun.com/v2",
			wantService: "openai",
		},
		{
			name:           "xfyun responses",
			req:            CreateChannelRequest{Provider: ProviderXFyun, Target: TargetResponses, APIKey: "xf-test"},
			wantBaseURL:    "https://maas-coding-api.cn-huabei-1.xf-yun.com/v1/responses",
			wantService:    "responses",
			wantCodex:      true,
			wantStripCodex: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildPayload(tt.req)
			if err != nil {
				t.Fatalf("BuildPayload() error = %v", err)
			}
			if got.BaseURL != tt.wantBaseURL {
				t.Fatalf("BaseURL = %q, want %q", got.BaseURL, tt.wantBaseURL)
			}
			if got.ServiceType != tt.wantService {
				t.Fatalf("ServiceType = %q, want %q", got.ServiceType, tt.wantService)
			}
			if got.AuthHeader != tt.wantAuthHeader {
				t.Fatalf("AuthHeader = %q, want %q", got.AuthHeader, tt.wantAuthHeader)
			}
			if got.NoVision != tt.wantVision {
				t.Fatalf("NoVision = %v, want %v", got.NoVision, tt.wantVision)
			}
			if tt.wantNormalizeMetadata != nil {
				if got.NormalizeMetadataUserId == nil {
					t.Fatalf("NormalizeMetadataUserId = nil, want %v", *tt.wantNormalizeMetadata)
				}
				if *got.NormalizeMetadataUserId != *tt.wantNormalizeMetadata {
					t.Fatalf("NormalizeMetadataUserId = %v, want %v", *got.NormalizeMetadataUserId, *tt.wantNormalizeMetadata)
				}
			}
			if got.StripBillingHeader != tt.wantStripBilling {
				t.Fatalf("StripBillingHeader = %v, want %v", got.StripBillingHeader, tt.wantStripBilling)
			}
			if got.CodexToolCompat != tt.wantCodex {
				t.Fatalf("CodexToolCompat = %v, want %v", got.CodexToolCompat, tt.wantCodex)
			}
			if got.StripCodexClientTools != tt.wantStripCodex {
				t.Fatalf("StripCodexClientTools = %v, want %v", got.StripCodexClientTools, tt.wantStripCodex)
			}
			if got.NormalizeSystemRoleToTopLevel != tt.wantNormalizeSystem {
				t.Fatalf("NormalizeSystemRoleToTopLevel = %v, want %v", got.NormalizeSystemRoleToTopLevel, tt.wantNormalizeSystem)
			}
			if tt.wantModels != nil {
				if !slices.Equal(got.SupportedModels, tt.wantModels) {
					t.Fatalf("SupportedModels = %v, want %v", got.SupportedModels, tt.wantModels)
				}
			}
			if tt.wantReasoningStyle != "" && got.ReasoningParamStyle != tt.wantReasoningStyle {
				t.Fatalf("ReasoningParamStyle = %q, want %q", got.ReasoningParamStyle, tt.wantReasoningStyle)
			}
		})
	}
}

func TestBuildPayloadRejectsUnsupportedTarget(t *testing.T) {
	// kimi 现已支持 Messages，此用例验证不支持的 target（如自定义拼接的错误值）仍能正确拒绝。
	_, err := BuildPayload(CreateChannelRequest{Provider: ProviderKimi, Target: "invalid-target", APIKey: "sk-test"})
	if err == nil {
		t.Fatal("BuildPayload() expected error for kimi+invalid-target")
	}
}

func TestBestPlanForTarget(t *testing.T) {
	tests := []struct {
		provider string
		target   string
		want     string
	}{
		{ProviderDeepSeek, TargetMessages, "anthropic"},
		{ProviderDeepSeek, TargetChat, "openai-chat"},
		{ProviderDeepSeek, TargetResponses, "openai-chat"},
		{ProviderCompshare, TargetMessages, "anthropic"},
		{ProviderCompshare, TargetChat, "openai-chat"},
		{ProviderCompshare, TargetResponses, "openai-chat"},
		{ProviderSenseNova, TargetMessages, "anthropic"},
		{ProviderSenseNova, TargetChat, "openai-chat"},
		{ProviderSenseNova, TargetResponses, "openai-chat"},
		{ProviderRunAPI, TargetMessages, "anthropic"},
		{ProviderRunAPI, TargetChat, "openai-chat"},
		{ProviderRunAPI, TargetResponses, "openai-chat"},
		{ProviderOpenCodeZen, TargetMessages, "go-openai-chat"},
		{ProviderOpenCodeZen, TargetChat, "go-openai-chat"},
		{ProviderOpenCodeZen, TargetResponses, "go-openai-chat"},
		{ProviderOpenCodeGo, TargetMessages, "openai-chat"},
		{ProviderOpenCodeGo, TargetChat, "openai-chat"},
		{ProviderOpenCodeGo, TargetResponses, "openai-chat"},
		{ProviderXFyun, TargetMessages, "anthropic"},
		{ProviderXFyun, TargetChat, "openai-chat"},
		{ProviderXFyun, TargetResponses, "responses"},
	}
	for _, tt := range tests {
		t.Run(tt.provider+"/"+tt.target, func(t *testing.T) {
			preset, ok := FindPreset(tt.provider)
			if !ok {
				t.Fatalf("FindPreset(%s) failed", tt.provider)
			}
			got := bestPlanForTarget(preset, tt.target)
			if got != tt.want {
				t.Fatalf("bestPlanForTarget(%s, %s) = %q, want %q", tt.provider, tt.target, got, tt.want)
			}
		})
	}
}

func TestFilterPlansForTarget_MiMoTokenPlans(t *testing.T) {
	preset, ok := FindPreset(ProviderMiMo)
	if !ok {
		t.Fatal("FindPreset(mimo) failed")
	}

	hasPlan := func(plans []ProviderPlan, id string) bool {
		return slices.ContainsFunc(plans, func(plan ProviderPlan) bool {
			return plan.ID == id
		})
	}

	messagesPlans := FilterPlansForTarget(preset, TargetMessages)
	if !hasPlan(messagesPlans, "token-cn-anthropic") {
		t.Fatalf("messages plans should include token-cn-anthropic: %#v", messagesPlans)
	}
	if hasPlan(messagesPlans, "token-cn") {
		t.Fatalf("messages plans should not include OpenAI token plan: %#v", messagesPlans)
	}

	chatPlans := FilterPlansForTarget(preset, TargetChat)
	if !hasPlan(chatPlans, "token-cn") {
		t.Fatalf("chat plans should include OpenAI token plan: %#v", chatPlans)
	}
	if hasPlan(chatPlans, "token-cn-anthropic") {
		t.Fatalf("chat plans should not include Anthropic token plan: %#v", chatPlans)
	}

	responsesPlans := FilterPlansForTarget(preset, TargetResponses)
	if !hasPlan(responsesPlans, "openai-chat") {
		t.Fatalf("responses plans should include pay-as-you-go OpenAI plan: %#v", responsesPlans)
	}
	for _, id := range []string{"token-cn", "token-sgp", "token-ams"} {
		if !hasPlan(responsesPlans, id) {
			t.Fatalf("responses plans should include MiMo OpenAI token plan %q: %#v", id, responsesPlans)
		}
	}
	if hasPlan(responsesPlans, "token-cn-anthropic") {
		t.Fatalf("responses plans should not include Anthropic token plan: %#v", responsesPlans)
	}
}

func TestFilterPlansForTarget_XFyunCodingPlan(t *testing.T) {
	preset, ok := FindPreset(ProviderXFyun)
	if !ok {
		t.Fatal("FindPreset(xfyun) failed")
	}

	hasPlan := func(plans []ProviderPlan, id string) bool {
		return slices.ContainsFunc(plans, func(plan ProviderPlan) bool {
			return plan.ID == id
		})
	}

	messagesPlans := FilterPlansForTarget(preset, TargetMessages)
	if !hasPlan(messagesPlans, "anthropic") || hasPlan(messagesPlans, "openai-chat") || hasPlan(messagesPlans, "responses") {
		t.Fatalf("messages plans should only include Anthropic plan: %#v", messagesPlans)
	}

	chatPlans := FilterPlansForTarget(preset, TargetChat)
	if !hasPlan(chatPlans, "openai-chat") || hasPlan(chatPlans, "responses") || hasPlan(chatPlans, "anthropic") {
		t.Fatalf("chat plans should only include OpenAI Chat plan: %#v", chatPlans)
	}

	responsesPlans := FilterPlansForTarget(preset, TargetResponses)
	if !hasPlan(responsesPlans, "responses") || !hasPlan(responsesPlans, "openai-chat") || hasPlan(responsesPlans, "anthropic") {
		t.Fatalf("responses plans should include Responses/OpenAI plans only: %#v", responsesPlans)
	}
}

func TestFilterPlansForTarget_OpenCodeMessagesUsesChatUpstream(t *testing.T) {
	hasPlan := func(plans []ProviderPlan, id string) bool {
		return slices.ContainsFunc(plans, func(plan ProviderPlan) bool {
			return plan.ID == id
		})
	}

	zenPreset, ok := FindPreset(ProviderOpenCodeZen)
	if !ok {
		t.Fatalf("FindPreset(%s) failed", ProviderOpenCodeZen)
	}
	zenMessagesPlans := FilterPlansForTarget(zenPreset, TargetMessages)
	if !hasPlan(zenMessagesPlans, "go-openai-chat") || !hasPlan(zenMessagesPlans, "openai-chat") {
		t.Fatalf("zen messages plans should include Go and Zen Chat upstream plans: %#v", zenMessagesPlans)
	}
	if hasPlan(zenMessagesPlans, "anthropic") {
		t.Fatalf("zen messages plans should not include Anthropic upstream: %#v", zenMessagesPlans)
	}
	if got := bestPlanForTarget(zenPreset, TargetMessages); got != "go-openai-chat" {
		t.Fatalf("zen bestPlanForTarget(messages) = %q, want go-openai-chat", got)
	}

	goPreset, ok := FindPreset(ProviderOpenCodeGo)
	if !ok {
		t.Fatalf("FindPreset(%s) failed", ProviderOpenCodeGo)
	}
	goMessagesPlans := FilterPlansForTarget(goPreset, TargetMessages)
	if len(goMessagesPlans) != 1 || goMessagesPlans[0].ID != "openai-chat" {
		t.Fatalf("go messages plans = %#v, want only openai-chat", goMessagesPlans)
	}
	if got := bestPlanForTarget(goPreset, TargetMessages); got != "openai-chat" {
		t.Fatalf("go bestPlanForTarget(messages) = %q, want openai-chat", got)
	}
}

func TestPresetsIncludesCompshareAtThirdPosition(t *testing.T) {
	presets := Presets()
	if len(presets) < 3 {
		t.Fatalf("Presets() length = %d, want at least 3", len(presets))
	}
	if presets[2].ID != ProviderCompshare {
		t.Fatalf("Presets()[2].ID = %q, want %q", presets[2].ID, ProviderCompshare)
	}
}

func TestPresetsIncludesRunAPIAfterCompshare(t *testing.T) {
	presets := Presets()
	if len(presets) < 4 {
		t.Fatalf("Presets() length = %d, want at least 4", len(presets))
	}
	if presets[3].ID != ProviderRunAPI {
		t.Fatalf("Presets()[3].ID = %q, want %q", presets[3].ID, ProviderRunAPI)
	}
}

func TestBuildPayloadAutoCorrectsPlan(t *testing.T) {
	// 前端应在 target 变化时自动切换 plan，后端尊重显式 planID
	// 此测试验证：未指定 planID 时，chat target 自动选择 openai-chat plan
	got, err := BuildPayload(CreateChannelRequest{
		Provider: ProviderDeepSeek,
		Target:   TargetChat,
		APIKey:   "sk-test",
	})
	if err != nil {
		t.Fatalf("BuildPayload() error = %v", err)
	}
	if got.BaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("BaseURL = %q, want https://api.deepseek.com/v1", got.BaseURL)
	}
	if got.ServiceType != "openai" {
		t.Fatalf("ServiceType = %q, want openai", got.ServiceType)
	}
}

func TestBuildPayloadSetsProviderConsoleWebsite(t *testing.T) {
	got, err := BuildPayload(CreateChannelRequest{
		Provider: ProviderCompshare,
		Target:   TargetResponses,
		APIKey:   "cs-test",
	})
	if err != nil {
		t.Fatalf("BuildPayload() error = %v", err)
	}
	want := "https://console.compshare.cn/light-gpu/model-subscription"
	if got.Website != want {
		t.Fatalf("Website = %q, want %q", got.Website, want)
	}
}

func TestBuildPayloadSetsRunAPIWebsite(t *testing.T) {
	got, err := BuildPayload(CreateChannelRequest{
		Provider: ProviderRunAPI,
		Target:   TargetResponses,
		APIKey:   "runapi-test",
	})
	if err != nil {
		t.Fatalf("BuildPayload() error = %v", err)
	}
	want := "https://runapi.co/console"
	if got.Website != want {
		t.Fatalf("Website = %q, want %q", got.Website, want)
	}
}

func TestBuildPayloadGitHubCopilotSetsProxyURL(t *testing.T) {
	got, err := BuildPayload(CreateChannelRequest{
		Provider: ProviderGitHubCopilot,
		Target:   TargetResponses,
		APIKey:   "gho_test",
		ProxyURL: " socks5://127.0.0.1:1080 ",
	})
	if err != nil {
		t.Fatalf("BuildPayload() error = %v", err)
	}
	if got.ServiceType != "copilot" {
		t.Fatalf("ServiceType = %q, want copilot", got.ServiceType)
	}
	if got.BaseURL != "https://api.githubcopilot.com" {
		t.Fatalf("BaseURL = %q, want https://api.githubcopilot.com", got.BaseURL)
	}
	if got.ProxyURL != "socks5://127.0.0.1:1080" {
		t.Fatalf("ProxyURL = %q, want socks5://127.0.0.1:1080", got.ProxyURL)
	}
}

func TestBuildPayloadGitHubCopilotSupportsGeminiTarget(t *testing.T) {
	got, err := BuildPayload(CreateChannelRequest{
		Provider: ProviderGitHubCopilot,
		Target:   TargetGemini,
		APIKey:   "gho_test",
	})
	if err != nil {
		t.Fatalf("BuildPayload() error = %v", err)
	}
	if got.ServiceType != "copilot" {
		t.Fatalf("ServiceType = %q, want copilot", got.ServiceType)
	}
}

func TestBuildPayloadGitHubCopilotRejectsUnsupportedTarget(t *testing.T) {
	_, err := BuildPayload(CreateChannelRequest{
		Provider: ProviderGitHubCopilot,
		Target:   "images",
		APIKey:   "gho_test",
	})
	if err == nil {
		t.Fatal("expected error")
	}
}
