package config

import (
	"net/url"
	"strings"

	"github.com/BenedictKing/ccx/internal/presetstore"
)

// BuiltinModelsManifest 内置模型清单条目。
// 对部分官方订阅入口（如 Claude OAuth 订阅、Codex plan 入口），
// 上游 models 接口可能不存在（404）或返回错误清单。
// 命中时直接使用 ModelIDs 作为该入口的可用模型列表，跳过上游探测。
type BuiltinModelsManifest struct {
	// BaseURLPattern 精确 host 或 host+path 前缀匹配，
	// 如 "api.anthropic.com" 或 "api.anthropic.com/v1"。
	BaseURLPattern string `json:"baseUrlPattern"`

	// ServiceType 运行时上游服务类型；claude 会归一为 messages，OpenAI Chat 兼容入口用 openai。
	ServiceType string `json:"serviceType"`

	// PlanHint 订阅类型提示，仅用于文档和日志，不影响匹配逻辑。
	PlanHint string `json:"planHint,omitempty"`

	// ModelsURL 覆盖默认从 baseURL 拼接的模型列表地址。
	// 用于协议入口与官方模型列表入口不在同一路径下的 provider。
	ModelsURL string `json:"modelsUrl,omitempty"`

	// ModelIDs 该入口实际可用的模型 ID 清单。
	ModelIDs []string `json:"modelIds"`

	// ExcludeModelPatterns 从上游 /v1/models 返回中剔除的模型 ID 正则。
	ExcludeModelPatterns []string `json:"excludeModelPatterns,omitempty"`

	// DisableProbe 为 true 时 Discovery 流程跳过 GET /v1/models，
	// 直接用 ModelIDs 生成 ModelProfile[]。
	DisableProbe bool `json:"disableProbe"`
}

// builtinModelsManifests 内置模型清单。
// 当前仅收录从仓库代码/模板/测试中能确认的官方入口。
// 新增条目须附注证据来源，不臆造 baseURL。
var builtinModelsManifests = []BuiltinModelsManifest{
	// Anthropic 官方 API 入口（来源：buildClaudeCompatibleModelsURLs 测试、
	// expectedRequestUrls 测试、前端 locale placeholder 确认 api.anthropic.com）。
	// 该入口的 /v1/models 正常可用，DisableProbe=false；
	// 当上游探测失败时回退使用此清单。
	{
		BaseURLPattern: "api.anthropic.com",
		ServiceType:    "messages",
		PlanHint:       "anthropic_api",
		ModelIDs: []string{
			"claude-fable-5",
			"claude-fable-5-1",
			"claude-mythos-5",
			"claude-mythos-5-1",
			"claude-opus-4-8",
			"claude-opus-4-7",
			"claude-opus-4-6",
			"claude-sonnet-5",
			"claude-sonnet-4-6",
			"claude-sonnet-4-5",
			"claude-opus-4-5",
			"claude-haiku-4-5",
		},
		DisableProbe: false,
	},
	// DeepSeek 官方兼容入口（来源：provider_templates.go 与 DeepSeek API 文档）。
	// 两种协议统一通过官方 GET https://api.deepseek.com/models 获取模型清单；
	// 请求入口仍分别使用 /anthropic 与 OpenAI 兼容根地址。
	{
		BaseURLPattern: "api.deepseek.com/anthropic",
		ServiceType:    "messages",
		PlanHint:       "deepseek_anthropic",
		ModelsURL:      "https://api.deepseek.com/models",
		ModelIDs:       deepseekModelIDs(),
		DisableProbe:   false,
	},
	{
		BaseURLPattern: "api.deepseek.com",
		ServiceType:    "openai",
		PlanHint:       "deepseek_openai",
		ModelsURL:      "https://api.deepseek.com/models",
		ModelIDs:       deepseekModelIDs(),
		DisableProbe:   false,
	},
	// 小米 MiMo Anthropic 兼容入口（来源：provider_templates.go 与 docs/providers/mimo.md）。
	// Anthropic 协议入口可用性通过 /v1/messages 验证；/v1/models 不作为能力判定依据。
	{
		BaseURLPattern:       "api.xiaomimimo.com/anthropic",
		ServiceType:          "messages",
		PlanHint:             "mimo_payg_anthropic",
		ModelIDs:             mimoModelIDs(),
		ExcludeModelPatterns: mimoExcludeModelPatterns(),
		DisableProbe:         true,
	},
	{
		BaseURLPattern:       "token-plan-cn.xiaomimimo.com/anthropic",
		ServiceType:          "messages",
		PlanHint:             "mimo_token_plan_cn_anthropic",
		ModelIDs:             mimoModelIDs(),
		ExcludeModelPatterns: mimoExcludeModelPatterns(),
		DisableProbe:         true,
	},
	{
		BaseURLPattern:       "token-plan-sgp.xiaomimimo.com/anthropic",
		ServiceType:          "messages",
		PlanHint:             "mimo_token_plan_sgp_anthropic",
		ModelIDs:             mimoModelIDs(),
		ExcludeModelPatterns: mimoExcludeModelPatterns(),
		DisableProbe:         true,
	},
	{
		BaseURLPattern:       "token-plan-ams.xiaomimimo.com/anthropic",
		ServiceType:          "messages",
		PlanHint:             "mimo_token_plan_ams_anthropic",
		ModelIDs:             mimoModelIDs(),
		ExcludeModelPatterns: mimoExcludeModelPatterns(),
		DisableProbe:         true,
	},
	// 小米 MiMo OpenAI Chat 兼容入口（来源：provider_templates.go 与 docs/providers/mimo.md）。
	// /v1/models 可用于发现；返回 ASR/TTS 等非文本模型时通过 ExcludeModelPatterns 过滤。
	{
		BaseURLPattern:       "api.xiaomimimo.com/v1",
		ServiceType:          "openai",
		PlanHint:             "mimo_payg_openai",
		ModelIDs:             mimoModelIDs(),
		ExcludeModelPatterns: mimoExcludeModelPatterns(),
		DisableProbe:         false,
	},
	{
		BaseURLPattern:       "token-plan-cn.xiaomimimo.com/v1",
		ServiceType:          "openai",
		PlanHint:             "mimo_token_plan_cn_openai",
		ModelIDs:             mimoModelIDs(),
		ExcludeModelPatterns: mimoExcludeModelPatterns(),
		DisableProbe:         false,
	},
	{
		BaseURLPattern:       "token-plan-sgp.xiaomimimo.com/v1",
		ServiceType:          "openai",
		PlanHint:             "mimo_token_plan_sgp_openai",
		ModelIDs:             mimoModelIDs(),
		ExcludeModelPatterns: mimoExcludeModelPatterns(),
		DisableProbe:         false,
	},
	{
		BaseURLPattern:       "token-plan-ams.xiaomimimo.com/v1",
		ServiceType:          "openai",
		PlanHint:             "mimo_token_plan_ams_openai",
		ModelIDs:             mimoModelIDs(),
		ExcludeModelPatterns: mimoExcludeModelPatterns(),
		DisableProbe:         false,
	},
	// Kimi Code 官方订阅入口（来源：Kimi Code 模型配置与概览文档）。
	// /coding/v1/models 会按 API Key 返回当前会员档位可用的模型；
	// ModelIDs 仅作为 models 端点不可用时的静态回退，只保留所有套餐都可用的基础模型。
	{
		BaseURLPattern: "api.kimi.com/coding",
		ServiceType:    "messages",
		PlanHint:       "kimi_code_anthropic",
		ModelsURL:      "https://api.kimi.com/coding/v1/models",
		ModelIDs:       kimiCodeModelIDs(),
		DisableProbe:   false,
	},
	{
		BaseURLPattern: "api.kimi.com/coding/v1",
		ServiceType:    "openai",
		PlanHint:       "kimi_code_openai",
		ModelsURL:      "https://api.kimi.com/coding/v1/models",
		ModelIDs:       kimiCodeModelIDs(),
		DisableProbe:   false,
	},
	// 火山方舟 Agent/Coding Plan 套餐入口（来源：provider_templates.go volcengine 模板）。
	// 套餐模型发现依赖火山云管控面签名接口，普通推理 Key 无法通过 /v1/models 探测；
	// 未绑定 Access Key 时用此清单兜底，让渠道立即可用。DisableProbe=true。
	// BaseURLPattern 用 host+path 前缀（不含版本段），以同时匹配 openai 入口的 /v3 后缀。
	// ExcludeModelPatterns 过滤管控面权益清单里的非对话模型（embedding/视频/绘图/语音族），
	// 避免它们进入画像成为对话协议的调度候选。
	{
		BaseURLPattern:       "ark.cn-beijing.volces.com/api/plan",
		ServiceType:          "messages",
		PlanHint:             "volcengine_plan_anthropic",
		ModelIDs:             volcengineAgentPlanModelIDs(),
		ExcludeModelPatterns: volcengineExcludeModelPatterns(),
		DisableProbe:         true,
	},
	{
		BaseURLPattern:       "ark.cn-beijing.volces.com/api/coding",
		ServiceType:          "messages",
		PlanHint:             "volcengine_coding_anthropic",
		ModelIDs:             volcengineCodingPlanModelIDs(),
		ExcludeModelPatterns: volcengineExcludeModelPatterns(),
		DisableProbe:         true,
	},
	{
		BaseURLPattern:       "ark.cn-beijing.volces.com/api/plan",
		ServiceType:          "openai",
		PlanHint:             "volcengine_plan_openai",
		ModelIDs:             volcengineAgentPlanModelIDs(),
		ExcludeModelPatterns: volcengineExcludeModelPatterns(),
		DisableProbe:         true,
	},
	{
		BaseURLPattern:       "ark.cn-beijing.volces.com/api/coding",
		ServiceType:          "openai",
		PlanHint:             "volcengine_coding_openai",
		ModelIDs:             volcengineCodingPlanModelIDs(),
		ExcludeModelPatterns: volcengineExcludeModelPatterns(),
		DisableProbe:         true,
	},
	// 火山套餐原生 Responses 入口（Codex wire_api=responses）：模型清单与 Chat API 一致。
	{
		BaseURLPattern:       "ark.cn-beijing.volces.com/api/plan",
		ServiceType:          "responses",
		PlanHint:             "volcengine_plan_responses",
		ModelIDs:             volcengineAgentPlanModelIDs(),
		ExcludeModelPatterns: volcengineExcludeModelPatterns(),
		DisableProbe:         true,
	},
	{
		BaseURLPattern:       "ark.cn-beijing.volces.com/api/coding",
		ServiceType:          "responses",
		PlanHint:             "volcengine_coding_responses",
		ModelIDs:             volcengineCodingPlanModelIDs(),
		ExcludeModelPatterns: volcengineExcludeModelPatterns(),
		DisableProbe:         true,
	},
}

func mimoModelIDs() []string {
	return []string{
		"mimo-v2.5-pro",
		"mimo-v2.5",
	}
}

func kimiCodeModelIDs() []string {
	return []string{
		"kimi-for-coding",
	}
}

func deepseekModelIDs() []string {
	return []string{
		"deepseek-v4-pro",
		"deepseek-v4-flash",
	}
}

// volcengineAgentPlanModelIDs 火山方舟 Agent Plan(/api/plan) 入口的兜底文本模型清单。
// 当用户未绑定火山云 Access Key（无法调用管控面模型发现接口）时，
// 用此清单让渠道立即可用；绑定 Access Key 后由 FetchModels 覆盖为真实清单。
// 清单来源：火山方舟 Agent Plan 套餐概览(2026-08)；minimax-m2.7/kimi-k2.6 已于 2026-08-18 下线，
// glm-5.2 自 2026-08-18 起停止新用户服务并于 2026-08-31 下线路由至 glm-5.3，均已移除。
func VolcengineAgentPlanModelIDs() []string {
	return volcengineAgentPlanModelIDs()
}

func volcengineAgentPlanModelIDs() []string {
	return []string{
		"doubao-seed-2.1-turbo",
		"doubao-seed-2.0-lite",
		"doubao-seed-2.0-mini",
		"doubao-seed-evolving",
		"minimax-m3",
		"glm-5.3",
		"glm-latest",
		"deepseek-v4-flash",
		"deepseek-v4-pro",
		"kimi-k3",
		"kimi-k2.7-code",
	}
}

// volcengineCodingPlanModelIDs 火山方舟 Coding Plan(/api/coding) 入口的兜底文本模型清单。
// 与 Agent Plan 差异：Coding Plan 不含 doubao-seed-2.0-mini、doubao-seed-evolving、glm-latest、kimi-k3。
// 清单来源：火山方舟 Coding Plan 套餐概览(2026-08)；下线的 minimax-m2.7/kimi-k2.6/glm-5.2 已移除。
// VolcengineCodingPlanModelIDs 火山方舟 Coding Plan(/api/coding) 入口的兜底文本模型清单，语义同 Agent Plan 版。
func VolcengineCodingPlanModelIDs() []string {
	return volcengineCodingPlanModelIDs()
}

func volcengineCodingPlanModelIDs() []string {
	return []string{
		"doubao-seed-2.1-turbo",
		"doubao-seed-2.0-lite",
		"minimax-m3",
		"glm-5.3",
		"deepseek-v4-flash",
		"deepseek-v4-pro",
		"kimi-k2.7-code",
	}
}

func mimoExcludeModelPatterns() []string {
	return []string{
		`^mimo-v2\.5-(?:asr|tts(?:-.+)?)$`,
	}
}

// volcengineExcludeModelPatterns 剔除火山管控面套餐权益清单里明确非对话的模型命名族。
// 管控面返回的是套餐全量权益（含 embedding/视频 seedance/绘图 seedream/语音 tts/asr 等），
// 原样进入画像会成为对话协议的调度候选（调用必失败）；分段边界匹配避免误伤
// doubao-seed-code 这类对话模型（seed 不等于 seedance/seedream）。
// VolcengineExcludeModelPatterns 供发现层复用同一组排除规则。
func VolcengineExcludeModelPatterns() []string {
	return volcengineExcludeModelPatterns()
}

func volcengineExcludeModelPatterns() []string {
	return []string{
		`(?:^|[-_/])(?:embedding|seedance|seedream|tts|asr|ocr)(?:[-_.]|$)`,
	}
}

// LookupBuiltinManifest 根据上游 baseURL 和 serviceType 查找匹配的内置清单。
// 匹配规则：BaseURLPattern 作为 host 或 host+path 前缀匹配。
// 优先精确 host 匹配，其次 host+path 前缀匹配（最长前缀优先）。
// 返回匹配的清单和 true；未命中返回零值和 false。
func LookupBuiltinManifest(baseURL string, serviceType string) (BuiltinModelsManifest, bool) {
	normalized := normalizeBaseURLForManifest(baseURL)
	if normalized == "" {
		return BuiltinModelsManifest{}, false
	}

	if manifest, found := lookupBuiltinManifestIn(runtimeBuiltinModelsManifests(), normalized, serviceType); found {
		return manifest, true
	}
	return lookupBuiltinManifestIn(builtinModelsManifests, normalized, serviceType)
}

// ResolveBuiltinModelsURL 返回清单声明的模型列表地址。
// 为避免把 API Key 发送到第三方主机，覆盖地址必须使用 HTTPS 且与渠道 baseURL 同主机。
func ResolveBuiltinModelsURL(baseURL string, serviceType string) (string, bool) {
	manifest, ok := LookupBuiltinManifest(baseURL, strings.ToLower(strings.TrimSpace(serviceType)))
	if !ok || strings.TrimSpace(manifest.ModelsURL) == "" {
		return "", false
	}
	base, baseErr := url.Parse(strings.TrimSuffix(strings.TrimSpace(baseURL), "#"))
	models, modelsErr := url.Parse(strings.TrimSpace(manifest.ModelsURL))
	if baseErr != nil || modelsErr != nil || !strings.EqualFold(models.Scheme, "https") ||
		base.Hostname() == "" || models.User != nil || models.Fragment != "" || !sameURLServer(base, models) {
		return "", false
	}
	return models.String(), true
}

func runtimeBuiltinModelsManifests() []BuiltinModelsManifest {
	bundle := presetstore.Default().Get()
	if bundle == nil || bundle.BuiltinModelsManifests == nil || len(bundle.BuiltinModelsManifests.Manifests) == 0 {
		return nil
	}
	manifests := make([]BuiltinModelsManifest, 0, len(bundle.BuiltinModelsManifests.Manifests))
	for _, entry := range bundle.BuiltinModelsManifests.Manifests {
		manifests = append(manifests, BuiltinModelsManifest{
			BaseURLPattern:       entry.BaseURLPattern,
			ServiceType:          entry.ServiceType,
			PlanHint:             entry.PlanHint,
			ModelsURL:            entry.ModelsURL,
			ModelIDs:             append([]string(nil), entry.ModelIDs...),
			ExcludeModelPatterns: append([]string(nil), entry.ExcludeModelPatterns...),
			DisableProbe:         entry.DisableProbe,
		})
	}
	return manifests
}

func lookupBuiltinManifestIn(manifests []BuiltinModelsManifest, normalized string, serviceType string) (BuiltinModelsManifest, bool) {
	if len(manifests) == 0 {
		return BuiltinModelsManifest{}, false
	}
	var bestMatch BuiltinModelsManifest
	var bestMatchLen int
	found := false
	for _, manifest := range manifests {
		if manifest.ServiceType != serviceType {
			continue
		}
		pattern := strings.ToLower(manifest.BaseURLPattern)
		if !matchManifestPattern(normalized, pattern) {
			continue
		}
		if len(pattern) > bestMatchLen {
			bestMatch = manifest
			bestMatchLen = len(pattern)
			found = true
		}
	}
	return bestMatch, found
}

// matchManifestPattern 检查 normalized baseURL 是否匹配 manifest pattern。
// pattern 可以是纯 host（精确匹配）或 host+path 前缀匹配。
func matchManifestPattern(normalized string, pattern string) bool {
	// 精确匹配
	if normalized == pattern {
		return true
	}
	// host 精确匹配（pattern 不含路径，normalized 可能含路径）
	if !strings.Contains(pattern, "/") && strings.HasPrefix(normalized, pattern) {
		rest := normalized[len(pattern):]
		return rest == "" || rest[0] == '/'
	}
	// host+path 前缀匹配
	return strings.HasPrefix(normalized, pattern)
}

// normalizeBaseURLForManifest 将 baseURL 规范化为 host 或 host+path 形式用于匹配。
// 去掉 scheme（https://）、尾部 /、# 标记、版本段（/v1 等）。
func normalizeBaseURLForManifest(baseURL string) string {
	s := strings.ToLower(strings.TrimSpace(baseURL))
	if s == "" {
		return ""
	}
	// 去掉 scheme
	if idx := strings.Index(s, "://"); idx >= 0 {
		s = s[idx+3:]
	}
	// 去掉 #
	s = strings.TrimSuffix(s, "#")
	// 去掉尾部 /
	s = strings.TrimRight(s, "/")
	return s
}
