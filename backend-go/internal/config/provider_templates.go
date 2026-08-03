package config

import (
	"math"
	"net/url"
	"regexp"
	"strings"
)

// ProviderTemplate 描述一个已知 provider 的模板化添加配置。
//
// 设计目标：用户只需选择 provider + 输入 API Key，系统按 key 前缀选候选 baseURL，
// 探测验证可用性后自动创建渠道，无需手填 baseURL / 选协议 / 配兼容开关。
//
// baseURL 按上游原生协议区分端点：Claude 请求优先走原生 Claude 入口；
// Chat / Codex Responses 请求走 OpenAI Chat 兼容入口，由后端做协议转换。
// ProviderTemplate 只描述来源和端点，不再承载 channel-presets 兼容开关；
// autoManaged 渠道的模型与能力差异由后端智能调度/ModelResolver 处理。
type ProviderTemplate struct {
	ProviderID     string              `json:"providerId"` // "mimo" / "deepseek" / ...
	Aliases        []string            `json:"aliases,omitempty"`
	DisplayName    string              `json:"displayName"`    // "小米 MiMo"
	Description    string              `json:"description"`    // key 前缀说明等
	ChannelKind    string              `json:"channelKind"`    // 默认 route 的渠道类型，兼容旧前端
	ServiceType    string              `json:"serviceType"`    // 默认 route 的服务类型，兼容旧前端
	OriginType     string              `json:"originType"`     // official_api / official_token_plan / relay
	OriginTier     string              `json:"originTier"`     // first / second
	KeyPrefixRules []KeyPrefixRule     `json:"keyPrefixRules"` // key 前缀 → plan 判别
	Candidates     []ProviderCandidate `json:"candidates"`     // 默认 route 的候选 baseURL
	Routes         []ProviderRoute     `json:"routes,omitempty"`
	// ModelCostMultipliers 描述同一 provider 套餐内每次模型调用的相对消耗。
	// key 支持模型精确 ID 或通配符；值越低越省，只在质量与实测表现相同后参与选优。
	ModelCostMultipliers map[string]float64 `json:"modelCostMultipliers,omitempty"`
	// ModelQualityPriorities 描述 provider 已确认的同质量档模型能力顺序，值越高越强。
	// 该映射允许不完整；解析器仅在同档候选全部命中时比较，避免未知模型被无依据降权。
	ModelQualityPriorities map[string]int `json:"modelQualityPriorities,omitempty"`
}

// ProviderRoute 描述同一 provider 在某个 CCX 渠道协议下使用的原生上游入口。
type ProviderRoute struct {
	ChannelKind string              `json:"channelKind"`           // "messages" / "chat" / "responses"
	ServiceType string              `json:"serviceType"`           // "claude" / "openai" / "responses"
	Description string              `json:"description,omitempty"` // route 说明，仅展示/诊断
	Candidates  []ProviderCandidate `json:"candidates"`            // 该 route 的候选 baseURL
	// SupportedModels 限定该 route 端点实际接受的模型，语义与 UpstreamConfig.SupportedModels 一致
	// （支持精确匹配、prefix* / *suffix / *contains*，以及 ! 前缀排除）。
	//
	// 仅在上游同一 baseURL 下不同协议端点的模型支持范围不一致时才需要填写：
	// 例如 DeepSeek 的 /v1/responses 只接受 deepseek-v4-flash，而 /v1/chat/completions
	// 同时接受 flash 与 pro。留空表示该 route 不额外限制模型。
	SupportedModels []string `json:"supportedModels,omitempty"`
}

// KeyPrefixRule 按 API Key 前缀判别 plan 类型。
type KeyPrefixRule struct {
	Prefix  string `json:"prefix"`  // "sk-" / "tp-"
	PlanTag string `json:"planTag"` // "payg" / "token_plan"
}

// ProviderCandidate 一个候选 baseURL 及其归属信息。
type ProviderCandidate struct {
	BaseURL  string `json:"baseUrl"`  // 完整 baseURL，如 https://api.xiaomimimo.com/anthropic
	PlanTag  string `json:"planTag"`  // "payg" / "token_plan" / ""
	Region   string `json:"region"`   // "cn" / "sgp" / "ams" / "global" / ""
	Priority int    `json:"priority"` // 同 plan 内探测优先级（数字越小越先探测）
}

// builtinProviderTemplates 编译期内置的已知 provider 模板。
//
// URL 来源（2026-07 联网核实官方文档）：
//   - MiMo:     https://mimo.mi.com/docs（按量 sk- / Token Plan tp-，TP 分 cn/sgp/ams 三区域集群）
//   - DeepSeek: https://api-docs.deepseek.com（Anthropic 兼容 /anthropic）
//   - Kimi:     https://api.moonshot.ai/anthropic（全球）/ https://api.moonshot.cn/anthropic（中国）
//   - GLM:      https://open.bigmodel.cn/api/anthropic（Claude）与 /api/paas/v4（OpenAI）
//   - 火山方舟: https://ark.cn-beijing.volces.com/api/plan（Agent Plan）与 /api/coding（Coding Plan）
//
// Claude route 的 baseURL 使用 Anthropic 兼容入口且不带 /v1（claude provider 会自动补 /v1/messages）。
// Chat/Responses route 使用 OpenAI Chat 兼容入口，由 provider 自动补协议端点。
var builtinProviderTemplates = []ProviderTemplate{
	{
		ProviderID:  "mimo",
		DisplayName: "小米 MiMo",
		Description: "sk- 按量付费 / tp- Token Plan 订阅（Claude 走 /anthropic，Chat/Codex/Gemini 走 /v1）",
		ChannelKind: "messages",
		ServiceType: "claude",
		OriginType:  "official_api",
		OriginTier:  "first",
		KeyPrefixRules: []KeyPrefixRule{
			{Prefix: "sk-", PlanTag: "payg"},
			{Prefix: "tp-", PlanTag: "token_plan"},
		},
		Candidates: mimoClaudeCandidates(),
		Routes: []ProviderRoute{
			{
				ChannelKind: "messages",
				ServiceType: "claude",
				Description: "Claude Messages 原生 Anthropic 兼容入口",
				Candidates:  mimoClaudeCandidates(),
			},
			{
				ChannelKind: "chat",
				ServiceType: "openai",
				Description: "OpenAI Chat Completions 兼容入口",
				Candidates:  mimoChatCandidates(),
			},
			{
				ChannelKind: "responses",
				ServiceType: "openai",
				Description: "Codex/Responses 请求转换到 OpenAI Chat Completions",
				Candidates:  mimoChatCandidates(),
			},
			{
				ChannelKind: "gemini",
				ServiceType: "openai",
				Description: "Gemini 请求转换到 OpenAI Chat Completions",
				Candidates:  mimoChatCandidates(),
			},
		},
	},
	{
		ProviderID:  "deepseek",
		DisplayName: "DeepSeek",
		Description: "DeepSeek 官方 API（Claude Messages、OpenAI Chat 与 Responses 三协议原生兼容；Responses 仅支持 deepseek-v4-flash）",
		ChannelKind: "messages",
		ServiceType: "claude",
		OriginType:  "official_api",
		OriginTier:  "first",
		Candidates:  deepseekClaudeCandidates(),
		Routes: []ProviderRoute{
			{
				ChannelKind: "messages",
				ServiceType: "claude",
				Description: "Claude Messages Anthropic 兼容入口",
				Candidates:  deepseekClaudeCandidates(),
			},
			{
				ChannelKind: "chat",
				ServiceType: "openai",
				Description: "OpenAI Chat Completions 兼容入口",
				Candidates:  deepseekOpenAICandidates(),
			},
			{
				ChannelKind: "responses",
				ServiceType: "responses",
				Description: "OpenAI Responses 原生入口（仅 deepseek-v4-flash；v4-pro 预计 2026-08 初支持）",
				Candidates:  deepseekResponsesCandidates(),
				// 上游对不支持的模型返回 400 而非忽略，因此在渠道层就限定为正向白名单：
				// 未知新模型不会被误放进该端点，v4-pro 上线后在此追加即可。
				SupportedModels: []string{"deepseek-v4-flash"},
			},
		},
	},
	newProviderTemplate(
		"atlascloud",
		"Atlas Cloud",
		"Atlas Cloud OpenAI-compatible LLM API",
		"official_api",
		"first",
		standardProviderRoutes("openai", atlasCloudCandidates(), atlasCloudCandidates()),
	),
	{
		ProviderID:  "volcengine",
		Aliases:     []string{"volc-ark"},
		DisplayName: "火山方舟 Agent/Coding Plan",
		Description: "ark- 套餐推理 Key。Agent Plan 支持 AFP 感知路由（详见 volcengine_afp_pricing.go）；Coding Plan 计费规则未核验，仅使用通用定价。模型发现需为每个 Key 绑定火山云 Access Key。",
		ChannelKind: "messages",
		ServiceType: "claude",
		OriginType:  "official_api",
		OriginTier:  "first",
		KeyPrefixRules: []KeyPrefixRule{
			{Prefix: "ark-", PlanTag: "personal_plan"},
		},
		Candidates: volcenginePlanClaudeCandidates(),
		Routes: []ProviderRoute{
			{
				ChannelKind: "messages",
				ServiceType: "claude",
				Description: "Agent/Coding Plan Claude Messages 入口",
				Candidates:  volcenginePlanClaudeCandidates(),
			},
			{
				ChannelKind: "chat",
				ServiceType: "openai",
				Description: "Agent/Coding Plan OpenAI Chat Completions 入口",
				Candidates:  volcenginePlanChatCandidates(),
			},
			{
				ChannelKind: "responses",
				ServiceType: "openai",
				Description: "Codex/Responses 请求转换到套餐 Chat Completions",
				Candidates:  volcenginePlanChatCandidates(),
			},
			{
				ChannelKind: "gemini",
				ServiceType: "openai",
				Description: "Gemini 请求转换到套餐 Chat Completions",
				Candidates:  volcenginePlanChatCandidates(),
			},
		},
	},
	{
		ProviderID:  "kimi",
		Aliases:     []string{"kimi-code"},
		DisplayName: "Kimi (Moonshot)",
		Description: "Moonshot Kimi 官方 API（按量与 Coding Plan，自动判别可用入口）",
		ChannelKind: "messages",
		ServiceType: "claude",
		OriginType:  "official_api",
		OriginTier:  "first",
		Candidates:  kimiClaudeCandidates(),
		Routes:      standardProviderRoutes("claude", kimiClaudeCandidates(), kimiOpenAICandidates()),
	},
	{
		ProviderID:  "glm",
		DisplayName: "智谱 GLM",
		Description: "智谱 GLM 官方 API（自动识别 id.secret Key；按量与 Coding Plan）",
		ChannelKind: "messages",
		ServiceType: "claude",
		OriginType:  "official_api",
		OriginTier:  "first",
		Candidates:  glmClaudeCandidates(),
		Routes:      standardProviderRoutes("claude", glmClaudeCandidates(), glmOpenAICandidates()),
	},
	compshareProviderTemplate(),
	newProviderTemplate(
		"sensenova",
		"SenseNova",
		"商汤日日新官方 API",
		"official_api",
		"first",
		standardProviderRoutes("claude", []ProviderCandidate{
			{BaseURL: "https://token.sensenova.cn", PlanTag: "payg", Region: "cn", Priority: 0},
		}, []ProviderCandidate{
			{BaseURL: "https://token.sensenova.cn/v1", PlanTag: "payg", Region: "cn", Priority: 0},
		}),
	),
	newProviderTemplate(
		"minimax",
		"MiniMax",
		"MiniMax 官方 API",
		"official_api",
		"first",
		standardProviderRoutes("claude", []ProviderCandidate{
			{BaseURL: "https://api.minimaxi.com/anthropic", PlanTag: "payg", Region: "global", Priority: 0},
		}, []ProviderCandidate{
			{BaseURL: "https://api.minimax.chat/v1", PlanTag: "payg", Region: "global", Priority: 0},
		}),
	),
	newProviderTemplate(
		"dashscope",
		"阿里云 DashScope",
		"阿里云百炼按量、Coding Plan 与 Token Plan",
		"official_api",
		"first",
		standardProviderRoutes("claude", dashScopeClaudeCandidates(), dashScopeOpenAICandidates()),
	),
	newProviderTemplateWithAliases(
		"opencode-zen",
		[]string{"opencode-go"},
		"OpenCode Zen / Go",
		"OpenCode 官方 Zen 按量与 Go 订阅模型网关",
		"relay",
		"second",
		standardProviderRoutes("openai", openCodeCandidates(), openCodeCandidates()),
	),
	newProviderTemplate(
		"tencent-lkeap",
		"腾讯云 TokenHub",
		"腾讯云大模型 Token Plan",
		"official_token_plan",
		"first",
		standardProviderRoutes("claude", []ProviderCandidate{
			{BaseURL: "https://api.lkeap.cloud.tencent.com/plan/anthropic", PlanTag: "token_plan", Region: "cn", Priority: 0},
		}, []ProviderCandidate{
			{BaseURL: "https://api.lkeap.cloud.tencent.com/plan/v3", PlanTag: "token_plan", Region: "cn", Priority: 0},
		}),
	),
	newProviderTemplate(
		"qianfan",
		"百度千帆 Coding Plan",
		"百度智能云千帆 Coding Plan",
		"official_token_plan",
		"first",
		standardProviderRoutes("claude", []ProviderCandidate{
			{BaseURL: "https://qianfan.baidubce.com/anthropic/coding", PlanTag: "coding_plan", Region: "cn", Priority: 0},
		}, []ProviderCandidate{
			{BaseURL: "https://qianfan.baidubce.com/v2/coding#", PlanTag: "coding_plan", Region: "cn", Priority: 0},
		}),
	),
	newProviderTemplate(
		"xfyun",
		"讯飞星辰",
		"科大讯飞星辰 MaaS Coding Plan",
		"official_token_plan",
		"first",
		xfYunRoutes(),
	),
	newProviderTemplate(
		"openrouter",
		"OpenRouter",
		"OpenRouter 多模型聚合平台",
		"relay",
		"second",
		standardProviderRoutes("claude", []ProviderCandidate{
			{BaseURL: "https://openrouter.ai/api", PlanTag: "payg", Region: "global", Priority: 0},
		}, []ProviderCandidate{
			{BaseURL: "https://openrouter.ai/api/v1", PlanTag: "payg", Region: "global", Priority: 0},
		}),
	),
	newProviderTemplate(
		"modelscope",
		"ModelScope 魔搭",
		"阿里 ModelScope 官方推理服务",
		"official_api",
		"first",
		standardProviderRoutes("claude", []ProviderCandidate{
			{BaseURL: "https://api-inference.modelscope.cn", PlanTag: "payg", Region: "cn", Priority: 0},
		}, []ProviderCandidate{
			{BaseURL: "https://api-inference.modelscope.cn/v1", PlanTag: "payg", Region: "cn", Priority: 0},
		}),
	),
	newProviderTemplate(
		"originrouter",
		"极易云 OriginRouter",
		"极易云多模型 API 转发平台",
		"relay",
		"second",
		standardProviderRoutes("claude", []ProviderCandidate{
			{BaseURL: "https://api.easytransnote.com/coding", PlanTag: "subscription", Region: "cn", Priority: 0},
		}, []ProviderCandidate{
			{BaseURL: "https://api.easytransnote.com/coding/v1", PlanTag: "subscription", Region: "cn", Priority: 0},
		}),
	),
}

var zhipuAPIKeyPattern = regexp.MustCompile(`^[A-Za-z0-9]{20,}\.[A-Za-z0-9]{10,}$`)

func standardProviderRoutes(messagesServiceType string, messagesCandidates, openAICandidates []ProviderCandidate) []ProviderRoute {
	messagesDescription := "Claude Messages 原生 Anthropic 兼容入口"
	if messagesServiceType == "openai" {
		messagesDescription = "Claude Messages 请求转换到 OpenAI Chat Completions"
	}
	return []ProviderRoute{
		{
			ChannelKind: "messages",
			ServiceType: messagesServiceType,
			Description: messagesDescription,
			Candidates:  messagesCandidates,
		},
		{
			ChannelKind: "chat",
			ServiceType: "openai",
			Description: "OpenAI Chat Completions 兼容入口",
			Candidates:  openAICandidates,
		},
		{
			ChannelKind: "responses",
			ServiceType: "openai",
			Description: "Responses 请求转换到 OpenAI Chat Completions",
			Candidates:  openAICandidates,
		},
	}
}

func newProviderTemplate(providerID, displayName, description, originType, originTier string, routes []ProviderRoute) ProviderTemplate {
	return newProviderTemplateWithAliases(providerID, nil, displayName, description, originType, originTier, routes)
}

func newProviderTemplateWithAliases(providerID string, aliases []string, displayName, description, originType, originTier string, routes []ProviderRoute) ProviderTemplate {
	tmpl := ProviderTemplate{
		ProviderID:  providerID,
		Aliases:     aliases,
		DisplayName: displayName,
		Description: description,
		OriginType:  originType,
		OriginTier:  originTier,
		Routes:      routes,
	}
	if len(routes) > 0 {
		tmpl.ChannelKind = routes[0].ChannelKind
		tmpl.ServiceType = routes[0].ServiceType
		tmpl.Candidates = routes[0].Candidates
	}
	return tmpl
}

func compshareProviderTemplate() ProviderTemplate {
	tmpl := newProviderTemplate(
		"compshare",
		"优云智算套餐",
		"UCloud 优云智算模型套餐与聚合服务",
		"relay",
		"second",
		standardProviderRoutes("claude", []ProviderCandidate{
			{BaseURL: "https://cp.compshare.cn", PlanTag: "subscription", Region: "cn", Priority: 0},
		}, []ProviderCandidate{
			{BaseURL: "https://cp.compshare.cn/v1", PlanTag: "subscription", Region: "cn", Priority: 0},
		}),
	)
	// 优云智算控制台的套餐消耗次数；glm-5.2 当前为限时倍率。
	tmpl.ModelCostMultipliers = map[string]float64{
		"glm-5.2":                   2,
		"glm-5.1":                   6,
		"MiniMax-M2.7":              2,
		"kimi-k2.6":                 5,
		"deepseek-ai/DeepSeek-V3.2": 1,
		"deepseek-v4-flash":         1,
	}
	tmpl.ModelQualityPriorities = map[string]int{
		"glm-5.1":   2,
		"kimi-k2.6": 1,
	}
	return tmpl
}

func deepseekClaudeCandidates() []ProviderCandidate {
	return []ProviderCandidate{
		{BaseURL: "https://api.deepseek.com/anthropic", PlanTag: "", Region: "", Priority: 0},
	}
}

func deepseekOpenAICandidates() []ProviderCandidate {
	return []ProviderCandidate{
		{BaseURL: "https://api.deepseek.com", PlanTag: "", Region: "", Priority: 0},
	}
}

func atlasCloudCandidates() []ProviderCandidate {
	return []ProviderCandidate{
		{BaseURL: "https://api.atlascloud.ai/v1", PlanTag: "payg", Region: "global", Priority: 0},
	}
}

// deepseekResponsesCandidates DeepSeek 原生 Responses 入口。
// 官方 base_url 为 https://api.deepseek.com，端点为标准 /v1/responses。
func deepseekResponsesCandidates() []ProviderCandidate {
	return []ProviderCandidate{
		{BaseURL: "https://api.deepseek.com/v1/responses", PlanTag: "", Region: "", Priority: 0},
	}
}

func glmClaudeCandidates() []ProviderCandidate {
	return []ProviderCandidate{
		{BaseURL: "https://open.bigmodel.cn/api/anthropic", PlanTag: "payg", Region: "cn", Priority: 0},
	}
}

func glmOpenAICandidates() []ProviderCandidate {
	return []ProviderCandidate{
		// # 表示该路径已经是版本根，调用方不得再自动补 /v1。
		{BaseURL: "https://open.bigmodel.cn/api/paas/v4#", PlanTag: "payg", Region: "cn", Priority: 0},
		{BaseURL: "https://open.bigmodel.cn/api/coding/paas/v4#", PlanTag: "coding_plan", Region: "cn", Priority: 1},
	}
}

func kimiClaudeCandidates() []ProviderCandidate {
	return []ProviderCandidate{
		{BaseURL: "https://api.moonshot.cn/anthropic", PlanTag: "payg", Region: "cn", Priority: 0},
		{BaseURL: "https://api.moonshot.ai/anthropic", PlanTag: "payg", Region: "global", Priority: 1},
		{BaseURL: "https://api.kimi.com/coding", PlanTag: "coding_plan", Region: "global", Priority: 2},
	}
}

func kimiOpenAICandidates() []ProviderCandidate {
	return []ProviderCandidate{
		{BaseURL: "https://api.moonshot.cn/v1", PlanTag: "payg", Region: "cn", Priority: 0},
		{BaseURL: "https://api.moonshot.ai/v1", PlanTag: "payg", Region: "global", Priority: 1},
		{BaseURL: "https://api.kimi.com/coding/v1", PlanTag: "coding_plan", Region: "global", Priority: 2},
	}
}

func dashScopeClaudeCandidates() []ProviderCandidate {
	return []ProviderCandidate{
		{BaseURL: "https://dashscope.aliyuncs.com/apps/anthropic", PlanTag: "payg", Region: "cn", Priority: 0},
		{BaseURL: "https://coding.dashscope.aliyuncs.com/apps/anthropic", PlanTag: "coding_plan", Region: "cn", Priority: 1},
		{BaseURL: "https://token-plan.cn-beijing.maas.aliyuncs.com/apps/anthropic", PlanTag: "token_plan", Region: "cn", Priority: 2},
	}
}

func dashScopeOpenAICandidates() []ProviderCandidate {
	return []ProviderCandidate{
		{BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", PlanTag: "payg", Region: "cn", Priority: 0},
		{BaseURL: "https://coding.dashscope.aliyuncs.com/v1", PlanTag: "coding_plan", Region: "cn", Priority: 1},
		{BaseURL: "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1", PlanTag: "token_plan", Region: "cn", Priority: 2},
	}
}

func openCodeCandidates() []ProviderCandidate {
	return []ProviderCandidate{
		{BaseURL: "https://opencode.ai/zen/go/v1", PlanTag: "subscription", Region: "global", Priority: 0},
		{BaseURL: "https://opencode.ai/zen/v1", PlanTag: "payg", Region: "global", Priority: 1},
	}
}

func xfYunRoutes() []ProviderRoute {
	return []ProviderRoute{
		{
			ChannelKind: "messages",
			ServiceType: "claude",
			Description: "Claude Messages Coding Plan 入口",
			Candidates: []ProviderCandidate{
				{BaseURL: "https://maas-coding-api.cn-huabei-1.xf-yun.com/anthropic", PlanTag: "coding_plan", Region: "cn", Priority: 0},
			},
		},
		{
			ChannelKind: "chat",
			ServiceType: "openai",
			Description: "OpenAI Chat Completions Coding Plan 入口",
			Candidates: []ProviderCandidate{
				{BaseURL: "https://maas-coding-api.cn-huabei-1.xf-yun.com/v2", PlanTag: "coding_plan", Region: "cn", Priority: 0},
			},
		},
		{
			ChannelKind: "responses",
			ServiceType: "responses",
			Description: "OpenAI Responses Coding Plan 原生入口",
			Candidates: []ProviderCandidate{
				{BaseURL: "https://maas-coding-api.cn-huabei-1.xf-yun.com/v1/responses", PlanTag: "coding_plan", Region: "cn", Priority: 0},
			},
		},
	}
}

// ListProviderTemplates 返回所有内置 provider 模板。
func ListProviderTemplates() []ProviderTemplate {
	out := make([]ProviderTemplate, len(builtinProviderTemplates))
	copy(out, builtinProviderTemplates)
	return out
}

// GetProviderTemplate 按 providerId 查找模板，未找到返回 (nil, false)。
func GetProviderTemplate(providerID string) (*ProviderTemplate, bool) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	for i := range builtinProviderTemplates {
		tmpl := &builtinProviderTemplates[i]
		if strings.EqualFold(tmpl.ProviderID, providerID) {
			return tmpl, true
		}
		for _, alias := range tmpl.Aliases {
			if strings.EqualFold(alias, providerID) {
				return tmpl, true
			}
		}
	}
	return nil, false
}

// ModelCostMultiplierForModel 返回 provider 套餐内指定模型的相对消耗倍率。
// 精确 ID 优先，随后按最长通配符匹配；非法、负数或非有限值视为未知。
func (t *ProviderTemplate) ModelCostMultiplierForModel(modelID string) (float64, bool) {
	if t == nil {
		return 0, false
	}
	multiplier, _, ok := resolvePatternValueFold(modelID, t.ModelCostMultipliers)
	if !ok || multiplier < 0 || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
		return 0, false
	}
	return multiplier, true
}

// ModelQualityPriorityForModel 返回 provider 已确认的同档模型能力优先级。
func (t *ProviderTemplate) ModelQualityPriorityForModel(modelID string) (int, bool) {
	if t == nil {
		return 0, false
	}
	priority, _, ok := resolvePatternValueFold(modelID, t.ModelQualityPriorities)
	if !ok || priority <= 0 {
		return 0, false
	}
	return priority, true
}

// InferProviderIDFromBaseURL 仅按已知模板候选端点识别 provider。
// 使用最长路径匹配，避免把仅承载同名模型的其他中转站误判为该渠道。
func InferProviderIDFromBaseURL(baseURL string) (string, bool) {
	target, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(baseURL), "#"))
	if err != nil || target.Hostname() == "" {
		return "", false
	}
	targetPath := strings.TrimRight(target.EscapedPath(), "/")
	bestProvider := ""
	bestPathLen := -1
	for _, tmpl := range builtinProviderTemplates {
		for _, route := range tmpl.AutoAddRoutes() {
			for _, candidate := range route.Candidates {
				candidateURL, parseErr := url.Parse(strings.TrimSpace(candidate.BaseURL))
				if parseErr != nil || !sameURLServer(target, candidateURL) {
					continue
				}
				candidatePath := strings.TrimRight(candidateURL.EscapedPath(), "/")
				if candidatePath != "" && targetPath != candidatePath && !strings.HasPrefix(targetPath, candidatePath+"/") {
					continue
				}
				if len(candidatePath) > bestPathLen {
					bestProvider = tmpl.ProviderID
					bestPathLen = len(candidatePath)
				}
			}
		}
	}
	return bestProvider, bestProvider != ""
}

// InferProviderIDFromAPIKey 仅识别具有官方唯一格式的 API Key。
// sk- 等共享格式无法可靠区分 provider，因此不会参与推断。
func InferProviderIDFromAPIKey(apiKey string) (string, bool) {
	if zhipuAPIKeyPattern.MatchString(strings.TrimSpace(apiKey)) {
		return "glm", true
	}
	return "", false
}

func sameURLServer(left, right *url.URL) bool {
	if left == nil || right == nil || !strings.EqualFold(left.Hostname(), right.Hostname()) {
		return false
	}
	return effectiveURLPort(left) == effectiveURLPort(right)
}

func effectiveURLPort(value *url.URL) string {
	if value == nil {
		return ""
	}
	if port := value.Port(); port != "" {
		return port
	}
	switch strings.ToLower(value.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}

// AutoAddRoutes 返回 provider 快速添加时需要创建的渠道 route。
func (t *ProviderTemplate) AutoAddRoutes() []ProviderRoute {
	if t == nil {
		return nil
	}
	if len(t.Routes) > 0 {
		out := make([]ProviderRoute, len(t.Routes))
		copy(out, t.Routes)
		return out
	}
	if t.ChannelKind == "" || t.ServiceType == "" {
		return nil
	}
	return []ProviderRoute{{
		ChannelKind: t.ChannelKind,
		ServiceType: t.ServiceType,
		Candidates:  append([]ProviderCandidate(nil), t.Candidates...),
	}}
}

// SupportsChannelKind 判断 provider 是否可从指定渠道页发起快速添加。
func (t *ProviderTemplate) SupportsChannelKind(kind string) bool {
	for _, route := range t.AutoAddRoutes() {
		if route.ChannelKind == kind {
			return true
		}
	}
	return false
}

// CandidatesForKey 按 API Key 前缀返回默认 route 的候选 baseURL 顺序。
//
// 规则（对应用户决策）：
//  1. key 前缀命中某 PlanTag → 该 plan 的候选优先（按 Priority 升序），其余候选追加在后作为回退
//  2. 前缀不匹配任何规则（或模板无前缀规则）→ 返回全部候选（按 Priority 升序）
//
// 返回的候选已按探测顺序排列，调用方依次探测，命中即用。
func (t *ProviderTemplate) CandidatesForKey(apiKey string) []ProviderCandidate {
	if t == nil {
		return nil
	}
	return t.CandidatesForRouteKey(ProviderRoute{Candidates: t.Candidates}, apiKey)
}

// CandidatesForRouteKey 按 API Key 前缀返回指定 route 的候选 baseURL 顺序。
func (t *ProviderTemplate) CandidatesForRouteKey(route ProviderRoute, apiKey string) []ProviderCandidate {
	if t == nil {
		return nil
	}
	matchedPlan := ""
	for _, rule := range t.KeyPrefixRules {
		if rule.Prefix != "" && strings.HasPrefix(apiKey, rule.Prefix) {
			matchedPlan = rule.PlanTag
			break
		}
	}

	sortByPriority := func(list []ProviderCandidate) []ProviderCandidate {
		sorted := make([]ProviderCandidate, len(list))
		copy(sorted, list)
		// 稳定插入排序（候选数量极少，无需 sort 包）
		for i := 1; i < len(sorted); i++ {
			for j := i; j > 0 && sorted[j].Priority < sorted[j-1].Priority; j-- {
				sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
			}
		}
		return sorted
	}

	if matchedPlan == "" {
		return sortByPriority(route.Candidates)
	}

	preferred := make([]ProviderCandidate, 0, len(route.Candidates))
	fallback := make([]ProviderCandidate, 0, len(route.Candidates))
	for _, cand := range route.Candidates {
		if cand.PlanTag == matchedPlan {
			preferred = append(preferred, cand)
		} else {
			fallback = append(fallback, cand)
		}
	}
	return append(sortByPriority(preferred), sortByPriority(fallback)...)
}

func mimoClaudeCandidates() []ProviderCandidate {
	return []ProviderCandidate{
		{BaseURL: "https://api.xiaomimimo.com/anthropic", PlanTag: "payg", Region: "global", Priority: 0},
		{BaseURL: "https://token-plan-cn.xiaomimimo.com/anthropic", PlanTag: "token_plan", Region: "cn", Priority: 0},
		{BaseURL: "https://token-plan-sgp.xiaomimimo.com/anthropic", PlanTag: "token_plan", Region: "sgp", Priority: 1},
		{BaseURL: "https://token-plan-ams.xiaomimimo.com/anthropic", PlanTag: "token_plan", Region: "ams", Priority: 2},
	}
}

func mimoChatCandidates() []ProviderCandidate {
	return []ProviderCandidate{
		{BaseURL: "https://api.xiaomimimo.com/v1", PlanTag: "payg", Region: "global", Priority: 0},
		{BaseURL: "https://token-plan-cn.xiaomimimo.com/v1", PlanTag: "token_plan", Region: "cn", Priority: 0},
		{BaseURL: "https://token-plan-sgp.xiaomimimo.com/v1", PlanTag: "token_plan", Region: "sgp", Priority: 1},
		{BaseURL: "https://token-plan-ams.xiaomimimo.com/v1", PlanTag: "token_plan", Region: "ams", Priority: 2},
	}
}

func volcenginePlanClaudeCandidates() []ProviderCandidate {
	return []ProviderCandidate{
		{BaseURL: "https://ark.cn-beijing.volces.com/api/plan", PlanTag: "personal_plan", Region: "cn", Priority: 0},
		{BaseURL: "https://ark.cn-beijing.volces.com/api/coding", PlanTag: "personal_plan", Region: "cn", Priority: 1},
	}
}

func volcenginePlanChatCandidates() []ProviderCandidate {
	return []ProviderCandidate{
		{BaseURL: "https://ark.cn-beijing.volces.com/api/plan/v3", PlanTag: "personal_plan", Region: "cn", Priority: 0},
		{BaseURL: "https://ark.cn-beijing.volces.com/api/coding/v3", PlanTag: "personal_plan", Region: "cn", Priority: 1},
	}
}
