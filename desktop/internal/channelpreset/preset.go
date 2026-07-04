package channelpreset

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

const (
	ProviderDeepSeek      = "deepseek"
	ProviderMiMo          = "mimo"
	ProviderCompshare     = "compshare"
	ProviderRunAPI        = "runapi"
	ProviderUnity2        = "unity2"
	ProviderKimi          = "kimi"
	ProviderGLM           = "glm"
	ProviderSenseNova     = "sensenova"
	ProviderMiniMax       = "minimax"
	ProviderDashScope     = "dashscope"
	ProviderTencentLkeap  = "tencent-lkeap"
	ProviderKimiCode      = "kimi-code"
	ProviderVolcArk       = "volc-ark"
	ProviderQianfan       = "qianfan"
	ProviderXFyun         = "xfyun"
	ProviderOriginRouter  = "originrouter"
	ProviderOpenRouter    = "openrouter"
	ProviderModelScope    = "modelscope"
	ProviderOpenCodeZen   = "opencode-zen"
	ProviderOpenCodeGo    = "opencode-go"
	ProviderGitHubCopilot = "github-copilot"

	TargetMessages  = "messages"
	TargetChat      = "chat"
	TargetResponses = "responses"
	TargetGemini    = "gemini"
)

type ProviderPreset struct {
	ID                  string          `json:"id"`
	Order               int             `json:"order"`
	Label               string          `json:"label"`
	Description         string          `json:"description"`
	DirectAgent         bool            `json:"directAgent"`
	NativeMessages      bool            `json:"nativeMessages"`
	ChatCompatible      bool            `json:"chatCompatible"`
	ResponsesCompatible bool            `json:"responsesCompatible"`
	Plans               []ProviderPlan  `json:"plans"`
	Targets             []ChannelTarget `json:"targets"`
	DefaultTarget       string          `json:"defaultTarget"`
}

type ProviderPlan struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	BaseURL     string `json:"baseUrl"`
	Description string `json:"description"`
	Recommended bool   `json:"recommended"`
	Custom      bool   `json:"custom"`
}

// Protocol 返回 plan 所属的协议类型。
func (p ProviderPlan) Protocol() string {
	id := strings.ToLower(p.ID)
	label := strings.ToLower(p.Label)
	baseURL := strings.TrimRight(strings.ToLower(p.BaseURL), "/")
	if strings.Contains(id, "responses") || strings.Contains(label, "responses") || strings.HasSuffix(baseURL, "/responses") {
		return "responses"
	}
	if strings.Contains(id, "anthropic") || strings.Contains(label, "anthropic") || strings.Contains(baseURL, "anthropic") || strings.HasSuffix(baseURL, "/messages") {
		return "anthropic"
	}
	return "openai"
}

type ChannelTarget struct {
	Type        string `json:"type"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Recommended bool   `json:"recommended"`
}

type CreateChannelRequest struct {
	Provider    string `json:"provider"`
	Target      string `json:"target"`
	PlanID      string `json:"planId"`
	BaseURL     string `json:"baseUrl"`
	APIKey      string `json:"apiKey"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ProxyURL    string `json:"proxyUrl"`
}

type CreateChannelResult struct {
	Provider string `json:"provider"`
	Target   string `json:"target"`
	Name     string `json:"name"`
	BaseURL  string `json:"baseUrl"`
	Message  string `json:"message"`
}

type ChannelPayload struct {
	Name                          string            `json:"name"`
	Description                   string            `json:"description,omitempty"`
	Website                       string            `json:"website,omitempty"`
	ServiceType                   string            `json:"serviceType"`
	AuthHeader                    string            `json:"authHeader,omitempty"`
	BaseURL                       string            `json:"baseUrl"`
	APIKeys                       []string          `json:"apiKeys"`
	ProxyURL                      string            `json:"proxyUrl,omitempty"`
	ModelMapping                  map[string]string `json:"modelMapping,omitempty"`
	ReasoningMapping              map[string]string `json:"reasoningMapping,omitempty"`
	ReasoningParamStyle           string            `json:"reasoningParamStyle,omitempty"`
	PassbackReasoningContent      bool              `json:"passbackReasoningContent,omitempty"`
	PassbackThinkingBlocks        bool              `json:"passbackThinkingBlocks,omitempty"`
	NormalizeSystemRoleToTopLevel bool              `json:"normalizeSystemRoleToTopLevel,omitempty"`
	StripBillingHeader            bool              `json:"stripBillingHeader,omitempty"`
	NoVision                      bool              `json:"noVision,omitempty"`
	NoVisionModels                []string          `json:"noVisionModels,omitempty"`
	VisionFallbackModel           string            `json:"visionFallbackModel,omitempty"`
	SupportedModels               []string          `json:"supportedModels,omitempty"`
	StripEmptyTextBlocks          bool              `json:"stripEmptyTextBlocks,omitempty"`
	InjectDummyThoughtSignature   bool              `json:"injectDummyThoughtSignature,omitempty"`
	StripThoughtSignature         bool              `json:"stripThoughtSignature,omitempty"`
	CodexNativeToolPassthrough    bool              `json:"codexNativeToolPassthrough,omitempty"`
	CodexToolCompat               bool              `json:"codexToolCompat,omitempty"`
	StripCodexClientTools         bool              `json:"stripCodexClientTools,omitempty"`
	StripImageGenerationTool      bool              `json:"stripImageGenerationTool,omitempty"`
	NormalizeNonstandardChatRoles bool              `json:"normalizeNonstandardChatRoles,omitempty"`
	NormalizeMetadataUserId       *bool             `json:"normalizeMetadataUserId,omitempty"`
	RateLimitRPM                  int               `json:"rateLimitRpm,omitempty"`
	RateLimitBurst                int               `json:"rateLimitBurst,omitempty"`
	RateLimitMaxConcurrent        int               `json:"rateLimitMaxConcurrent,omitempty"`
	RateLimitAutoFromHeaders      bool              `json:"rateLimitAutoFromHeaders,omitempty"`
	Priority                      int               `json:"priority,omitempty"`
	Status                        string            `json:"status,omitempty"`
}

var providerConsoleURLs = map[string]string{
	ProviderDeepSeek:     "https://platform.deepseek.com/usage",
	ProviderMiMo:         "https://platform.xiaomimimo.com/console/balance",
	ProviderCompshare:    "https://console.compshare.cn/light-gpu/model-subscription",
	ProviderRunAPI:       "https://runapi.co/console",
	ProviderUnity2:       "https://unity2.ai/dashboard",
	ProviderKimi:         "https://platform.moonshot.cn/console/account",
	ProviderGLM:          "https://open.bigmodel.cn/coding-plan/personal/overview",
	ProviderSenseNova:    "https://platform.sensenova.cn/console",
	ProviderMiniMax:      "https://platform.minimaxi.com/user-center/payment/balance",
	ProviderDashScope:    "https://bailian.console.aliyun.com/cn-beijing?tab=model#/api-key",
	ProviderTencentLkeap: "https://console.cloud.tencent.com/lkeap/token-plan",
	ProviderKimiCode:     "https://www.kimi.com/code/console",
	ProviderVolcArk:      "https://console.volcengine.com/ark/region:cn-beijing/subscription/coding-plan?projectName=default",
	ProviderQianfan:      "https://console.bce.baidu.com/qianfan/resource/subscribe",
	ProviderXFyun:        "https://console.xfyun.cn/",
	ProviderOriginRouter: "https://easytransnote.com/ai/console/#key",
	ProviderOpenRouter:   "https://openrouter.ai/keys",
	ProviderModelScope:   "https://modelscope.cn/my/myaccesstoken",
	ProviderOpenCodeZen:  "https://opencode.ai/",
	ProviderOpenCodeGo:   "https://opencode.ai/",
}

// defaultTargets 返回标准的三目标配置（Messages、Responses、Chat），Messages 默认推荐。
func defaultTargets() []ChannelTarget {
	return []ChannelTarget{
		{Type: TargetMessages, Label: "Claude Messages", Description: "Claude native Messages protocol, supports Claude Code direct or via CCX proxy", Recommended: true},
		{Type: TargetResponses, Label: "Codex Responses", Description: "OpenAI Responses protocol, for Codex CLI and compatible clients"},
		{Type: TargetChat, Label: "OpenAI Chat", Description: "OpenAI Chat Completions protocol, compatible with various Chat clients and third-party tools"},
	}
}

// newFullCapabilityPreset 创建一个具有完整能力的 Preset 模板（全协议支持、全目标、默认 Messages）。
// 调用者只需设置 ID、Order、Label、Description 和 Plans。
func newFullCapabilityPreset(id, label, description string, order int, plans []ProviderPlan) ProviderPreset {
	return ProviderPreset{
		ID:                  id,
		Order:               order,
		Label:               label,
		Description:         description,
		DirectAgent:         true,
		NativeMessages:      true,
		ChatCompatible:      true,
		ResponsesCompatible: true,
		Plans:               plans,
		Targets:             defaultTargets(),
		DefaultTarget:       TargetMessages,
	}
}

// dualProtocolPlans 生成标准的双协议 Plans（Anthropic + OpenAI）。
// anthropicURL 是 Anthropic 协议的完整 URL。
// openaiURL 是 OpenAI 协议的完整 URL。
func dualProtocolPlans(anthropicURL, openaiURL string) []ProviderPlan {
	return []ProviderPlan{
		{ID: "anthropic", Label: "Anthropic-compatible", BaseURL: anthropicURL, Description: "Claude Messages 原生入口", Recommended: true},
		{ID: "openai-chat", Label: "OpenAI-compatible", BaseURL: openaiURL, Description: "Chat / Responses 通用入口"},
	}
}

// dualProtocolPlansSimple 生成标准的双协议 Plans，anthropic 和 openai 使用相同的 baseURL。
// 适用于 RunAPI、Unity2 等统一入口的服务商。
func dualProtocolPlansSimple(baseURL string) []ProviderPlan {
	return dualProtocolPlans(baseURL, baseURL)
}

func Presets() []ProviderPreset {
	return []ProviderPreset{
		newFullCapabilityPreset(
			ProviderDeepSeek,
			"DeepSeek",
			"国产高性能推理模型，V4-Pro / V4-Flash 双旗舰，以极低成本实现顶级推理能力，完全开源（MIT），性价比极高。",
			10,
			dualProtocolPlans("https://api.deepseek.com/anthropic", "https://api.deepseek.com/v1"),
		),
		{
			ID:                  ProviderMiMo,
			Order:               20,
			Label:               "MiMo",
			Description:         "小米自研大模型，面向 Agent 时代，旗舰 MiMo-V2.5-Pro 万亿参数、支持百万上下文，深度赋能人车家全生态。",
			DirectAgent:         true,
			NativeMessages:      true,
			ChatCompatible:      true,
			ResponsesCompatible: true,
			Plans: []ProviderPlan{
				{ID: "anthropic", Label: "按量 (Anthropic)", BaseURL: "https://api.xiaomimimo.com/anthropic", Description: "Claude Messages 原生入口", Recommended: true},
				{ID: "openai-chat", Label: "按量 (OpenAI)", BaseURL: "https://api.xiaomimimo.com/v1", Description: "Chat / Responses 通用入口"},
				{ID: "token-cn", Label: "Token Plan - 中国 (OpenAI)", BaseURL: "https://token-plan-cn.xiaomimimo.com/v1", Description: "中国区 Token Plan Chat / Responses 通用入口"},
				{ID: "token-sgp", Label: "Token Plan - 新加坡 (OpenAI)", BaseURL: "https://token-plan-sgp.xiaomimimo.com/v1", Description: "新加坡区 Token Plan Chat / Responses 通用入口"},
				{ID: "token-ams", Label: "Token Plan - 欧洲 (OpenAI)", BaseURL: "https://token-plan-ams.xiaomimimo.com/v1", Description: "欧洲区 Token Plan Chat / Responses 通用入口"},
				{ID: "token-cn-anthropic", Label: "Token Plan - 中国 (Anthropic)", BaseURL: "https://token-plan-cn.xiaomimimo.com/anthropic", Description: "中国区 Token Plan Claude Messages 原生入口"},
				{ID: "token-sgp-anthropic", Label: "Token Plan - 新加坡 (Anthropic)", BaseURL: "https://token-plan-sgp.xiaomimimo.com/anthropic", Description: "新加坡区 Token Plan Claude Messages 原生入口"},
				{ID: "token-ams-anthropic", Label: "Token Plan - 欧洲 (Anthropic)", BaseURL: "https://token-plan-ams.xiaomimimo.com/anthropic", Description: "欧洲区 Token Plan Claude Messages 原生入口"},
			},
			Targets:       defaultTargets(),
			DefaultTarget: TargetMessages,
		},
		newFullCapabilityPreset(
			ProviderCompshare,
			"优云智算套餐",
			"优云智算是 UCloud 旗下 AI 云平台，提供高性价比国内 AI 模型 Agent Plan 套餐，支持包月订阅或按量付费（49 元/月起），同时提供官方海外模型稳定接入，支持 Claude Code、Codex 与 API 集成，具备企业级高并发、7×24 技术支持和自助开票能力；通过推广链接注册可领取 5 元平台试用金。",
			30,
			dualProtocolPlans("https://cp.compshare.cn", "https://cp.compshare.cn/v1"),
		),
		newFullCapabilityPreset(
			ProviderRunAPI,
			"RunAPI",
			"RunAPI 是高效稳定的API OpenRouter平替平台，一个 API Key 即可访问 OpenAI、Claude、Gemini、DeepSeek、Grok 等 150+ 主流模型，低至 1 折，极其稳定，可以无缝兼容 Claude Code、OpenClaw 等工具。RunAPI 为 CCX用户提供专属福利：注册联系管理员即可领取￥7的免费额度",
			40,
			dualProtocolPlansSimple("https://runapi.co/v1"),
		),
		newFullCapabilityPreset(
			ProviderUnity2,
			"Unity2.ai",
			"Unity2.ai 是面向个人开发者、团队、企业的高性能 AI 模型 API 中转平台，长期服务国内头部企业，日均承载超 300 亿 token 调用，支持 5000 RPM 级高并发。一个 API Key 即可适配 Claude Code、Codex、OpenAI 模型、IDE 插件和 Agent 工作流等场景。具备企业级稳定供应能力，在高并发、持续调用和团队集中采购场景下依然保持低延迟、高可用。现在注册 Unity2.ai 可领取 $2 余额，加入官方群再送 $10 余额，合计最高可领 $12 免费额度。",
			45,
			dualProtocolPlansSimple("https://unity2.ai/v1"),
		),
		{
			ID:                  ProviderKimi,
			Order:               50,
			Label:               "Kimi / Moonshot",
			Description:         "月之暗面旗下智能助手平台，旗舰 Kimi K2.6 原生多模态，以超长上下文和 Agent 集群能力著称，深度研究表现突出。",
			DirectAgent:         true,
			NativeMessages:      true,
			ChatCompatible:      true,
			ResponsesCompatible: true,
			Plans: []ProviderPlan{
				{ID: "anthropic", Label: "按量 (Anthropic)", BaseURL: "https://api.moonshot.cn/anthropic", Description: "Claude Messages 原生入口", Recommended: true},
				{ID: "openai-chat", Label: "按量 (OpenAI)", BaseURL: "https://api.moonshot.cn/v1", Description: "Chat / Responses 通用入口"},
				{ID: "coding-anthropic", Label: "Coding Plan (Anthropic)", BaseURL: "https://api.kimi.com/coding", Description: "Coding Plan Claude Messages 原生入口"},
				{ID: "coding-openai-chat", Label: "Coding Plan (OpenAI)", BaseURL: "https://api.kimi.com/coding/v1", Description: "Coding Plan Chat / Responses 通用入口"},
			},
			Targets:       defaultTargets(),
			DefaultTarget: TargetMessages,
		},
		{
			ID:                  ProviderGLM,
			Order:               60,
			Label:               "GLM / BigModel",
			Description:         "智谱 AI 大模型开放平台，旗舰 GLM-5.1 编程能力对标前沿闭源模型，港股上市公司，全程华为昇腾训练，性价比突出。",
			DirectAgent:         true,
			NativeMessages:      true,
			ChatCompatible:      true,
			ResponsesCompatible: true,
			Plans: []ProviderPlan{
				{ID: "anthropic", Label: "Anthropic-compatible", BaseURL: "https://open.bigmodel.cn/api/anthropic", Description: "Claude Messages 原生入口", Recommended: true},
				{ID: "coding", Label: "Coding Plan (OpenAI)", BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4#", Description: "Coding Plan Chat / Responses 通用入口"},
				{ID: "openai-chat", Label: "通用 (OpenAI)", BaseURL: "https://open.bigmodel.cn/api/paas/v4#", Description: "通用 Chat / Responses 入口"},
			},
			Targets:       defaultTargets(),
			DefaultTarget: TargetMessages,
		},
		{
			ID:                  ProviderSenseNova,
			Order:               65,
			Label:               "SenseNova",
			Description:         "商汤日日新 SenseNova LLM API 平台，兼容 OpenAI 与 Anthropic 协议，支持轻量多模态模型和主流编程模型接入。",
			DirectAgent:         true,
			NativeMessages:      true,
			ChatCompatible:      true,
			ResponsesCompatible: true,
			Plans: []ProviderPlan{
				{ID: "anthropic", Label: "Anthropic-compatible", BaseURL: "https://token.sensenova.cn", Description: "Claude Messages 兼容入口", Recommended: true},
				{ID: "openai-chat", Label: "OpenAI-compatible", BaseURL: "https://token.sensenova.cn/v1", Description: "Chat 入口，Responses 由 CCX 转换"},
			},
			Targets:       defaultTargets(),
			DefaultTarget: TargetMessages,
		},
		{
			ID:                  ProviderMiniMax,
			Order:               70,
			Label:               "MiniMax",
			Description:         "全球化多模态 AI 公司（港股上市），旗舰 M3 支持百万上下文、原生视觉与 Agent 能力，海螺视频 / 音频模型全球第一梯队，海外收入超七成。",
			DirectAgent:         true,
			NativeMessages:      true,
			ChatCompatible:      true,
			ResponsesCompatible: true,
			Plans: []ProviderPlan{
				{ID: "anthropic", Label: "Anthropic-compatible", BaseURL: "https://api.minimaxi.com/anthropic", Description: "Claude Messages 原生入口", Recommended: true},
				{ID: "openai-chat", Label: "OpenAI-compatible", BaseURL: "https://api.minimax.chat/v1", Description: "Chat / Responses 通用入口"},
			},
			Targets:       defaultTargets(),
			DefaultTarget: TargetMessages,
		},
		{
			ID:                  ProviderDashScope,
			Order:               80,
			Label:               "阿里云 DashScope",
			Description:         "阿里云一站式大模型服务平台，集成千问全系列及 DeepSeek 等主流模型，覆盖文本、图像、音视频全模态，提供按量、Coding Plan、Token Plan 多种计费。",
			DirectAgent:         true,
			NativeMessages:      true,
			ChatCompatible:      true,
			ResponsesCompatible: true,
			Plans: []ProviderPlan{
				{ID: "anthropic", Label: "按量 (Anthropic)", BaseURL: "https://dashscope.aliyuncs.com/apps/anthropic", Description: "Claude Messages 原生入口", Recommended: true},
				{ID: "openai-chat", Label: "按量 (OpenAI)", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", Description: "Chat / Responses 通用入口"},
				{ID: "coding-anthropic", Label: "Coding Plan (Anthropic)", BaseURL: "https://coding.dashscope.aliyuncs.com/apps/anthropic", Description: "Coding Plan Claude Messages 原生入口"},
				{ID: "coding-openai-chat", Label: "Coding Plan (OpenAI)", BaseURL: "https://coding.dashscope.aliyuncs.com/v1", Description: "Coding Plan Chat / Responses 通用入口"},
				{ID: "token-plan-anthropic", Label: "Token Plan (Anthropic)", BaseURL: "https://token-plan.cn-beijing.maas.aliyuncs.com/apps/anthropic", Description: "Token Plan Claude Messages 原生入口"},
				{ID: "token-plan-openai-chat", Label: "Token Plan (OpenAI)", BaseURL: "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1", Description: "Token Plan Chat / Responses 通用入口"},
			},
			Targets:       defaultTargets(),
			DefaultTarget: TargetMessages,
		},
		{
			ID:                  ProviderOpenCodeZen,
			Order:               90,
			Label:               "OpenCode Zen / Go",
			Description:         "OpenCode 官方精选 AI 编程模型网关，按量 Zen 与低成本 Go 订阅共用同一套模型预设。",
			DirectAgent:         true,
			NativeMessages:      true,
			ChatCompatible:      true,
			ResponsesCompatible: true,
			Plans: []ProviderPlan{
				{ID: "go-openai-chat", Label: "Go (OpenAI-compatible)", BaseURL: "https://opencode.ai/zen/go/v1", Description: "订阅套餐入口，Messages 经 CCX 转 Chat 上游", Recommended: true},
				{ID: "openai-chat", Label: "Zen (OpenAI-compatible)", BaseURL: "https://opencode.ai/zen/v1", Description: "按量付费入口，Messages 经 CCX 转 Chat 上游"},
			},
			Targets:       defaultTargets(),
			DefaultTarget: TargetMessages,
		},
		{
			ID:                  ProviderOpenCodeGo,
			Order:               100,
			Label:               "OpenCode Go",
			Description:         "OpenCode 低成本开源编程模型订阅服务（$5/月起），深度优化编程 Agent 场景。",
			DirectAgent:         true,
			NativeMessages:      true,
			ChatCompatible:      true,
			ResponsesCompatible: true,
			Plans: []ProviderPlan{
				{ID: "anthropic", Label: "Anthropic-compatible", BaseURL: "https://opencode.ai/zen/go/v1", Description: "Claude Messages 原生入口"},
				{ID: "openai-chat", Label: "OpenAI-compatible", BaseURL: "https://opencode.ai/zen/go/v1", Description: "Messages 经 CCX 转 Chat 上游，Chat / Responses 直连该入口", Recommended: true},
			},
			Targets:       defaultTargets(),
			DefaultTarget: TargetMessages,
		},
		{
			ID:                  ProviderTencentLkeap,
			Order:               85,
			Label:               "腾讯云 TokenHub",
			Description:         "腾讯云大模型服务平台，整合混元及 DeepSeek、Kimi 等主流模型，提供 Token Plan 订阅和按量计费，适配 Claude Code、Codex 等主流编程工具。",
			DirectAgent:         false,
			NativeMessages:      true,
			ChatCompatible:      true,
			ResponsesCompatible: true,
			Plans: []ProviderPlan{
				{ID: "anthropic", Label: "Anthropic-compatible", BaseURL: "https://api.lkeap.cloud.tencent.com/plan/anthropic", Description: "Claude Messages 原生入口", Recommended: true},
				{ID: "openai-chat", Label: "OpenAI-compatible", BaseURL: "https://api.lkeap.cloud.tencent.com/plan/v3", Description: "Chat / Responses 通用入口"},
			},
			Targets:       defaultTargets(),
			DefaultTarget: TargetMessages,
		},
		{
			ID:                  ProviderVolcArk,
			Order:               86,
			Label:               "火山方舟 Coding Plan",
			Description:         "字节跳动火山引擎旗下一站式 AI 平台，提供豆包系列模型精调、推理、评测全链路服务，覆盖多厂商模型，企业级保障完善。",
			DirectAgent:         false,
			NativeMessages:      true,
			ChatCompatible:      true,
			ResponsesCompatible: true,
			Plans: []ProviderPlan{
				{ID: "anthropic", Label: "Anthropic-compatible", BaseURL: "https://ark.cn-beijing.volces.com/api/coding", Description: "Claude Messages 原生入口", Recommended: true},
				{ID: "openai-chat", Label: "OpenAI-compatible", BaseURL: "https://ark.cn-beijing.volces.com/api/coding/v3", Description: "Chat / Responses 通用入口"},
			},
			Targets:       defaultTargets(),
			DefaultTarget: TargetMessages,
		},
		{
			ID:                  ProviderQianfan,
			Order:               87,
			Label:               "百度千帆 Coding Plan",
			Description:         "百度智能云企业级大模型平台，内置文心 5.0 原生全模态大模型，支持多智能体协同，提供从开发到部署的全链路工具。",
			DirectAgent:         false,
			NativeMessages:      true,
			ChatCompatible:      true,
			ResponsesCompatible: true,
			Plans: []ProviderPlan{
				{ID: "anthropic", Label: "Anthropic-compatible", BaseURL: "https://qianfan.baidubce.com/anthropic/coding", Description: "Claude Messages 原生入口", Recommended: true},
				{ID: "openai-chat", Label: "OpenAI-compatible", BaseURL: "https://qianfan.baidubce.com/v2/coding#", Description: "Chat / Responses 通用入口"},
			},
			Targets:       defaultTargets(),
			DefaultTarget: TargetMessages,
		},
		{
			ID:                  ProviderXFyun,
			Order:               88,
			Label:               "讯飞星辰",
			Description:         "科大讯飞星辰 MaaS Astron Coding Plan，提供 OpenAI、Anthropic 与 Responses 三个独立 Coding Plan 入口。",
			DirectAgent:         true,
			NativeMessages:      true,
			ChatCompatible:      true,
			ResponsesCompatible: true,
			Plans: []ProviderPlan{
				{ID: "anthropic", Label: "Anthropic-compatible", BaseURL: "https://maas-coding-api.cn-huabei-1.xf-yun.com/anthropic", Description: "Claude Messages Coding Plan 入口", Recommended: true},
				{ID: "openai-chat", Label: "OpenAI-compatible", BaseURL: "https://maas-coding-api.cn-huabei-1.xf-yun.com/v2", Description: "OpenAI Chat Coding Plan 入口"},
				{ID: "responses", Label: "Responses-compatible", BaseURL: "https://maas-coding-api.cn-huabei-1.xf-yun.com/v1/responses", Description: "OpenAI Responses Coding Plan 入口"},
			},
			Targets:       defaultTargets(),
			DefaultTarget: TargetMessages,
		},
		{
			ID:                  ProviderOpenRouter,
			Order:               35,
			Label:               "OpenRouter",
			Description:         "全球最大的 AI 模型聚合平台，一个 API 密钥访问数百个模型，兼容 OpenAI 接口，提供安全治理和预算管控能力。",
			DirectAgent:         true,
			NativeMessages:      true,
			ChatCompatible:      true,
			ResponsesCompatible: true,
			Plans: []ProviderPlan{
				{ID: "anthropic", Label: "Anthropic-compatible", BaseURL: "https://openrouter.ai/api", Description: "Claude Messages 原生入口", Recommended: true},
				{ID: "openai-chat", Label: "OpenAI-compatible", BaseURL: "https://openrouter.ai/api/v1", Description: "Chat / Responses 通用入口"},
			},
			Targets:       defaultTargets(),
			DefaultTarget: TargetMessages,
		},
		{
			ID:                  ProviderModelScope,
			Order:               82,
			Label:               "ModelScope 魔搭",
			Description:         "阿里达摩院发起的开源模型社区，汇聚上万个模型，覆盖文本、图像、语音等全模态，支持模型探索、推理与训练全流程。",
			DirectAgent:         true,
			NativeMessages:      true,
			ChatCompatible:      true,
			ResponsesCompatible: true,
			Plans: []ProviderPlan{
				{ID: "anthropic", Label: "Anthropic-compatible", BaseURL: "https://api-inference.modelscope.cn", Description: "Claude Messages 原生入口", Recommended: true},
				{ID: "openai-chat", Label: "OpenAI-compatible", BaseURL: "https://api-inference.modelscope.cn/v1", Description: "Chat / Responses 通用入口"},
			},
			Targets:       defaultTargets(),
			DefaultTarget: TargetMessages,
		},
		{
			// 极易云仅在渠道中心可用，刻意不纳入 Agent（Claude/Codex/OpenCode）直连下拉。
			// 即便三个协议标志均为 true，也不要将其加入 desktop/internal/configservice 的直连 provider 列表。
			ID:                  ProviderOriginRouter,
			Order:               200,
			Label:               "极易云 OriginRouter",
			Description:         "极易云统一模型 API 转发平台，一个密钥无缝切换 GPT、Claude、Gemini 等主流模型，高速稳定，适合多模型混用场景。",
			DirectAgent:         false,
			NativeMessages:      true,
			ChatCompatible:      true,
			ResponsesCompatible: true,
			Plans: []ProviderPlan{
				{ID: "anthropic", Label: "Anthropic-compatible", BaseURL: "https://api.easytransnote.com/coding", Description: "Claude Messages 原生入口", Recommended: true},
				{ID: "openai-chat", Label: "OpenAI-compatible", BaseURL: "https://api.easytransnote.com/coding/v1", Description: "Chat / Responses 通用入口"},
			},
			Targets:       defaultTargets(),
			DefaultTarget: TargetMessages,
		},
	}
}

func BuildPayload(req CreateChannelRequest) (ChannelPayload, error) {
	if strings.EqualFold(strings.TrimSpace(req.Provider), ProviderGitHubCopilot) {
		return buildGitHubCopilotPayload(req)
	}

	preset, ok := FindPreset(req.Provider)
	if !ok {
		return ChannelPayload{}, fmt.Errorf("不支持的 provider: %s", req.Provider)
	}
	target := strings.TrimSpace(req.Target)
	if target == "" {
		target = preset.DefaultTarget
	}
	if !supportsTarget(preset, target) {
		return ChannelPayload{}, fmt.Errorf("%s 不支持添加到 %s 渠道", preset.Label, target)
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		return ChannelPayload{}, fmt.Errorf("API Key 不能为空")
	}
	planID := strings.TrimSpace(req.PlanID)
	if planID == "" {
		planID = bestPlanForTarget(preset, target)
	}
	baseURL, err := ResolveBaseURL(preset, planID, req.BaseURL)
	if err != nil {
		return ChannelPayload{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = defaultChannelNameForPlan(preset.ID, target, planID)
	}
	payload := ChannelPayload{
		Name:        name,
		Description: strings.TrimSpace(req.Description),
		Website:     providerConsoleURLs[preset.ID],
		BaseURL:     baseURL,
		APIKeys:     []string{apiKey},
		Priority:    1,
		Status:      "active",
	}
	applyTargetDefaults(&payload, preset.ID, target, planID)
	if targetConfigs, ok := channelTargetConfigs[target]; ok {
		if targetConfig, ok := targetConfigs[preset.ID]; ok {
			if preset.ID == ProviderKimi {
				targetConfig = applyKimiPlanOverrides(targetConfig, target, planID)
			}
			applyChannelTargetConfig(&payload, targetConfig)
		}
	}
	return payload, nil
}

func buildGitHubCopilotPayload(req CreateChannelRequest) (ChannelPayload, error) {
	target := strings.TrimSpace(req.Target)
	if target == "" {
		target = TargetResponses
	}
	if !isGitHubCopilotTarget(target) {
		return ChannelPayload{}, fmt.Errorf("GitHub Copilot 不支持添加到 %s 渠道", target)
	}

	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		return ChannelPayload{}, fmt.Errorf("API Key 不能为空")
	}

	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL == "" {
		baseURL = "https://api.githubcopilot.com"
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = defaultChannelName(ProviderGitHubCopilot, target)
	}

	return ChannelPayload{
		Name:        name,
		Description: strings.TrimSpace(req.Description),
		Website:     "https://github.com/settings/copilot",
		ServiceType: "copilot",
		BaseURL:     baseURL,
		APIKeys:     []string{apiKey},
		ProxyURL:    strings.TrimSpace(req.ProxyURL),
		Priority:    1,
		Status:      "active",
	}, nil
}

func isGitHubCopilotTarget(target string) bool {
	switch target {
	case TargetMessages, TargetChat, TargetResponses, TargetGemini:
		return true
	default:
		return false
	}
}

// targetMatchesURL 判断 URL 是否与 target 协议兼容。
// messages target 使用 Anthropic 协议，需要 anthropic 路径；
// chat/responses target 使用 OpenAI 协议，不应使用 anthropic 路径。

// bestPlanForTarget 根据 target 自动选择最合适的 plan。
// 当 provider 有多个 plan（如 DeepSeek 的 /anthropic 和 /v1）时，
// 确保 chat/responses 选择 OpenAI-compatible 入口，messages 选择 Anthropic 入口。
func bestPlanForTarget(preset ProviderPreset, target string) string {
	if len(preset.Plans) == 0 {
		return ""
	}
	if len(preset.Plans) == 1 {
		return preset.Plans[0].ID
	}
	if usesChatUpstreamForMessages(preset.ID) && target == TargetMessages {
		for _, plan := range preset.Plans {
			if !plan.Custom && plan.Recommended && isChatUpstreamPlan(plan) {
				return plan.ID
			}
		}
		for _, plan := range preset.Plans {
			if isChatUpstreamPlan(plan) {
				return plan.ID
			}
		}
	}
	if target == TargetResponses {
		for _, plan := range preset.Plans {
			if !plan.Custom && plan.Protocol() == "responses" {
				return plan.ID
			}
		}
	}
	for _, plan := range preset.Plans {
		if !plan.Custom && planCompatibleWithTarget(plan, target) {
			return plan.ID
		}
	}
	for _, plan := range preset.Plans {
		if plan.Recommended {
			return plan.ID
		}
	}
	return preset.Plans[0].ID
}

func FindPreset(provider string) (ProviderPreset, bool) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	for _, preset := range Presets() {
		if preset.ID == provider {
			return preset, true
		}
	}
	return ProviderPreset{}, false
}

// FilterPlansForTarget 按 target 协议过滤 plans，只返回兼容的入口。
// messages 使用 Anthropic 协议，chat/responses 使用 OpenAI 协议。
// 自定义 plan 始终保留。
func FilterPlansForTarget(preset ProviderPreset, target string) []ProviderPlan {
	plans := preset.Plans
	if len(plans) <= 1 {
		return plans
	}
	if usesChatUpstreamForMessages(preset.ID) && target == TargetMessages {
		filtered := make([]ProviderPlan, 0, len(plans))
		for _, plan := range plans {
			if plan.Custom || isChatUpstreamPlan(plan) {
				filtered = append(filtered, plan)
			}
		}
		if len(filtered) > 0 {
			return filtered
		}
	}
	var filtered []ProviderPlan
	for _, plan := range plans {
		if planCompatibleWithTarget(plan, target) {
			filtered = append(filtered, plan)
		}
	}
	if len(filtered) == 0 {
		return plans
	}
	return filtered
}

func usesChatUpstreamForMessages(provider string) bool {
	return provider == ProviderOpenCodeZen || provider == ProviderOpenCodeGo
}

func isChatUpstreamPlan(plan ProviderPlan) bool {
	return plan.Protocol() == "openai"
}

func planCompatibleWithTarget(plan ProviderPlan, target string) bool {
	if plan.Custom {
		return true
	}

	protocol := plan.Protocol()
	switch target {
	case TargetMessages:
		return protocol == "anthropic"
	case TargetChat:
		return protocol == "openai"
	case TargetResponses:
		return protocol == "responses" || protocol == "openai"
	default:
		return true
	}
}

func ResolveBaseURL(preset ProviderPreset, planID string, customBaseURL string) (string, error) {
	customBaseURL = strings.TrimSpace(customBaseURL)
	planID = strings.TrimSpace(planID)
	if planID == "" {
		for _, plan := range preset.Plans {
			if plan.Recommended {
				planID = plan.ID
				break
			}
		}
	}
	if planID == "" && len(preset.Plans) > 0 {
		planID = preset.Plans[0].ID
	}
	for _, plan := range preset.Plans {
		if plan.ID != planID {
			continue
		}
		if plan.Custom {
			if customBaseURL == "" {
				return "", fmt.Errorf("自定义 plan 需要填写 Base URL")
			}
			return customBaseURL, nil
		}
		if customBaseURL != "" {
			return customBaseURL, nil
		}
		if plan.BaseURL == "" {
			return "", fmt.Errorf("plan %s 缺少 Base URL", plan.ID)
		}
		return plan.BaseURL, nil
	}
	if customBaseURL != "" {
		return customBaseURL, nil
	}
	return "", fmt.Errorf("未找到 provider %s 的 plan: %s", preset.ID, planID)
}

func supportsTarget(preset ProviderPreset, target string) bool {
	for _, item := range preset.Targets {
		if item.Type == target {
			return true
		}
	}
	return false
}

func defaultChannelName(provider string, target string) string {
	return fmt.Sprintf("desktop-%s-%s", provider, target)
}

func defaultChannelNameForPlan(provider string, target string, planID string) string {
	if provider == ProviderOpenCodeZen && strings.HasPrefix(strings.TrimSpace(planID), "go-") {
		return defaultChannelName(ProviderOpenCodeGo, target)
	}
	return defaultChannelName(provider, target)
}

type channelTargetConfig struct {
	ServiceType                   string
	AuthHeader                    string
	ModelMapping                  map[string]string
	ReasoningMapping              map[string]string
	ReasoningParamStyle           string
	PassbackReasoningContent      bool
	PassbackThinkingBlocks        bool
	NormalizeSystemRoleToTopLevel bool
	NormalizeMetadataUserId       *bool
	StripBillingHeader            bool
	NoVision                      bool
	NoVisionModels                []string
	VisionFallbackModel           string
	StripEmptyTextBlocks          bool
	CodexNativeToolPassthrough    bool
	CodexToolCompat               *bool
	StripCodexClientTools         *bool
	StripImageGenerationTool      bool
	NormalizeNonstandardChatRoles bool
	RateLimitRPM                  int // 主动限速默认 RPM（0=不设默认）
}

var channelTargetConfigs = map[string]map[string]channelTargetConfig{
	TargetMessages:  generatedClaudeMessagesTargetConfigs(),
	TargetChat:      generatedOpenAIChatTargetConfigs(),
	TargetResponses: generatedCodexResponsesTargetConfigs(),
}

func boolRef(value bool) *bool {
	return &value
}

// applyKimiPlanOverrides 根据 Kimi planID 覆盖模型映射。
// coding plan 使用 kimi-for-coding，普通按量使用 kimi-k2.7。
func applyKimiPlanOverrides(config channelTargetConfig, target string, planID string) channelTargetConfig {
	isCodingPlan := strings.HasPrefix(planID, "coding-")

	if target == TargetMessages {
		if isCodingPlan {
			// Coding Plan: 使用 kimi-for-coding
			config.ModelMapping = map[string]string{
				"fable":  "kimi-for-coding",
				"haiku":  "kimi-for-coding",
				"opus":   "kimi-for-coding",
				"sonnet": "kimi-for-coding",
			}
		} else {
			// 普通按量: 使用 kimi-k2.7
			config.ModelMapping = map[string]string{
				"fable":  "kimi-k2.7",
				"haiku":  "kimi-k2.7",
				"opus":   "kimi-k2.7",
				"sonnet": "kimi-k2.7",
			}
		}
	} else if target == TargetResponses {
		if isCodingPlan {
			// Coding Plan: 使用 kimi-for-coding
			config.ModelMapping = map[string]string{
				"codex": "kimi-for-coding",
				"gpt":   "kimi-for-coding",
			}
		} else {
			// 普通按量: 使用 kimi-k2.7
			config.ModelMapping = map[string]string{
				"codex": "kimi-k2.7",
				"gpt":   "kimi-k2.7",
			}
		}
	}

	return config
}

func applyChannelTargetConfig(payload *ChannelPayload, config channelTargetConfig) {
	if config.ServiceType != "" {
		payload.ServiceType = config.ServiceType
	}
	if config.AuthHeader != "" {
		payload.AuthHeader = config.AuthHeader
	}
	payload.ModelMapping = maps.Clone(config.ModelMapping)
	payload.ReasoningMapping = maps.Clone(config.ReasoningMapping)
	payload.NoVisionModels = slices.Clone(config.NoVisionModels)
	payload.ReasoningParamStyle = config.ReasoningParamStyle
	payload.PassbackReasoningContent = payload.PassbackReasoningContent || config.PassbackReasoningContent
	payload.PassbackThinkingBlocks = payload.PassbackThinkingBlocks || config.PassbackThinkingBlocks
	payload.NormalizeSystemRoleToTopLevel = payload.NormalizeSystemRoleToTopLevel || config.NormalizeSystemRoleToTopLevel
	if config.NormalizeMetadataUserId != nil {
		payload.NormalizeMetadataUserId = config.NormalizeMetadataUserId
	}
	payload.StripBillingHeader = payload.StripBillingHeader || config.StripBillingHeader
	payload.NoVision = payload.NoVision || config.NoVision
	payload.VisionFallbackModel = config.VisionFallbackModel
	payload.StripEmptyTextBlocks = payload.StripEmptyTextBlocks || config.StripEmptyTextBlocks
	payload.CodexNativeToolPassthrough = payload.CodexNativeToolPassthrough || config.CodexNativeToolPassthrough
	payload.StripImageGenerationTool = payload.StripImageGenerationTool || config.StripImageGenerationTool
	payload.NormalizeNonstandardChatRoles = payload.NormalizeNonstandardChatRoles || config.NormalizeNonstandardChatRoles
	if config.CodexToolCompat != nil {
		payload.CodexToolCompat = *config.CodexToolCompat
	}
	if config.StripCodexClientTools != nil {
		payload.StripCodexClientTools = *config.StripCodexClientTools
	}
	if config.RateLimitRPM > 0 {
		payload.RateLimitRPM = config.RateLimitRPM
	}
}

func applyTargetDefaults(payload *ChannelPayload, provider string, target string, planID string) {
	switch target {
	case TargetMessages:
		payload.ServiceType = "claude"
		payload.StripEmptyTextBlocks = true
		payload.StripThoughtSignature = true
		if provider == ProviderOpenCodeGo {
			payload.AuthHeader = "x-api-key"
		}
		if provider == ProviderRunAPI {
			payload.NormalizeMetadataUserId = boolRef(false)
		}
	case TargetChat:
		payload.ServiceType = "openai"
		payload.NormalizeNonstandardChatRoles = true
	case TargetResponses:
		payload.ServiceType = "openai"
		payload.CodexToolCompat = true
		payload.StripCodexClientTools = true
		if provider == ProviderRunAPI || provider == ProviderUnity2 {
			payload.ServiceType = "responses"
			payload.CodexToolCompat = false
			payload.StripCodexClientTools = false
		}
		if provider == ProviderXFyun && planID == "responses" {
			payload.ServiceType = "responses"
		}
	}

	configs, ok := channelTargetConfigs[target]
	if !ok {
		return
	}
	config, ok := configs[provider]
	if !ok {
		return
	}

	// Kimi 根据 planID 选择不同的模型映射
	if provider == ProviderKimi {
		config = applyKimiPlanOverrides(config, target, planID)
	}

	applyChannelTargetConfig(payload, config)
}
