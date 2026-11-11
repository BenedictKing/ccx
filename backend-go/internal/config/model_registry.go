package config

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

// builtinPatternCache 编译后的 builtin 正则，init 时填充
var builtinPatternCache = map[string]*compiledBuiltinPattern{}

type compiledBuiltinPattern struct {
	regex               *regexp.Regexp
	hasSuffixConstraint bool
}

func initBuiltinPatternCache(patterns []string) {
	for _, p := range patterns {
		if _, ok := builtinPatternCache[p]; ok {
			continue
		}
		compiled, err := compileBuiltinPattern(p)
		if err != nil {
			panic("invalid builtin model pattern regex: " + p + ": " + err.Error())
		}
		builtinPatternCache[p] = compiled
	}
}

// compileBuiltinPattern 将 pattern 编译为 Go RE2 兼容的正则。
// 对于包含 (?=$|@) 等 lookahead 的模式，提取主正则并标记需要后缀检查。
func compileBuiltinPattern(pattern string) (*compiledBuiltinPattern, error) {
	// 分离主模式和后缀 lookahead
	// 常见形式：^主模式(?:可选后缀)(?=$|@)
	// 用前缀 (?i) 加强，Go RE2 支持 (?i)
	rePattern := "(?i)" + pattern

	// 去除所有 (?=...) / (?!) 等 lookahead，记录是否有后缀约束
	// 正则：找到最后一个 (?=...) 部分
	hasSuffixConstraint := false
	if idx := strings.LastIndex(rePattern, "(?="); idx >= 0 {
		suffix := rePattern[idx:]
		if strings.HasSuffix(suffix, ")") {
			// 去掉 (?=...)，但保留主模式
			rePattern = rePattern[:idx]
			// 检查 lookahead 内容是否包含 $（字符串结束断言）
			hasSuffixConstraint = strings.Contains(suffix, "$") || strings.Contains(suffix, "@")
		}
	}

	re, err := regexp.Compile(rePattern)
	if err != nil {
		return nil, err
	}
	return &compiledBuiltinPattern{regex: re, hasSuffixConstraint: hasSuffixConstraint}, nil
}

func matchBuiltinRegexPattern(pattern, model string) bool {
	compiled, ok := builtinPatternCache[pattern]
	if !ok {
		var err error
		compiled, err = compileBuiltinPattern(pattern)
		if err != nil {
			return false
		}
		builtinPatternCache[pattern] = compiled
	}

	if !compiled.regex.MatchString(model) {
		return false
	}

	// 如果 pattern 有后缀约束（原始 lookahead 包含 $|@），
	// 检查匹配位置后模型是否以合法结尾（字符串结束或 @）。
	// 严格只允许 $ 或 @，不放行 `-` 等分隔符，避免 gpt-5.4 误吃 gpt-5.4-mini。
	if compiled.hasSuffixConstraint {
		loc := compiled.regex.FindStringIndex(model)
		if loc == nil {
			return false
		}
		endIdx := loc[1]
		if endIdx < len(model) {
			next := model[endIdx]
			// 只允许 @（模型 hash/版本后缀，如 model@hash）或字符串结尾
			if next != '@' {
				return false
			}
		}
	}

	return true
}

const (
	DefaultOutputReserveTokens     = 8192
	DefaultUnknownSafeWindowTokens = 200000
)

// ResolvedAgentModelProfile 描述下游 agent 模型解析结果。
type ResolvedAgentModelProfile struct {
	Profile        AgentModelProfile
	MatchedPattern string
	Source         string
	Known          bool
}

// ResolvedUpstreamCapability 描述实际模型能力解析结果。
type ResolvedUpstreamCapability struct {
	Capability     UpstreamModelCapability
	RequestModel   string
	ActualModel    string
	MatchedPattern string
	Source         string
	Known          bool
}

// IsContextRoutingEnabled 返回上下文路由是否启用，默认启用。
func (c ContextRoutingConfig) IsContextRoutingEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// EffectiveOutputReserveTokens 返回未显式请求输出上限时的预留 token。
func (c ContextRoutingConfig) EffectiveOutputReserveTokens() int {
	if c.DefaultOutputReserveTokens > 0 {
		return c.DefaultOutputReserveTokens
	}
	return DefaultOutputReserveTokens
}

// EffectiveUnknownSafeWindowTokens 返回未知能力渠道可接受的安全窗口。
func (c ContextRoutingConfig) EffectiveUnknownSafeWindowTokens() int {
	if c.UnknownSafeWindowTokens > 0 {
		return c.UnknownSafeWindowTokens
	}
	return DefaultUnknownSafeWindowTokens
}

// BuiltinAgentModelProfiles 返回 CCX 内置的下游 agent 模型知识库。
func BuiltinAgentModelProfiles() map[string]AgentModelProfile {
	return map[string]AgentModelProfile{
		"gpt-6.1-sol": {
			DisplayName:            "GPT-6.1 Sol",
			ContextWindowTokens:    500000,
			MaxContextWindowTokens: 1050000,
			EffectiveContextRatio:  0.95,
			AutoCompactRatio:       0.90,
			TruncationMode:         "tokens",
			TruncationLimit:        10000,
			ReasoningEfforts:       []string{"low", "medium", "high", "xhigh", "max"},
			SupportsPriorityTier:   true,
		},
		"gpt-6*": {
			DisplayName:            "GPT-6 family (Astra / Sol / Luna)",
			ContextWindowTokens:    272000,
			MaxContextWindowTokens: 1050000,
			EffectiveContextRatio:  0.95,
			AutoCompactRatio:       0.90,
			TruncationMode:         "tokens",
			TruncationLimit:        10000,
			ReasoningEfforts:       []string{"low", "medium", "high", "xhigh", "max"},
			SupportsPriorityTier:   true,
		},
		"gpt-5.2": {
			DisplayName:            "GPT-5.5 / gpt-5.2",
			ContextWindowTokens:    272000,
			MaxContextWindowTokens: 272000,
			EffectiveContextRatio:  0.95,
			AutoCompactRatio:       0.90,
			TruncationMode:         "bytes",
			TruncationLimit:        10000,
			ReasoningEfforts:       []string{"low", "medium", "high", "xhigh"},
		},
		"gpt-5.4": {
			DisplayName:            "gpt-5.4",
			ContextWindowTokens:    272000,
			MaxContextWindowTokens: 1000000,
			EffectiveContextRatio:  0.95,
			AutoCompactRatio:       0.90,
			TruncationMode:         "tokens",
			TruncationLimit:        10000,
			ReasoningEfforts:       []string{"low", "medium", "high", "xhigh"},
			SupportsPriorityTier:   true,
		},
		"gpt-5.4-mini": {
			DisplayName:            "gpt-5.4-mini",
			ContextWindowTokens:    272000,
			MaxContextWindowTokens: 272000,
			EffectiveContextRatio:  0.95,
			AutoCompactRatio:       0.90,
			TruncationMode:         "tokens",
			TruncationLimit:        10000,
			ReasoningEfforts:       []string{"low", "medium", "high", "xhigh"},
		},
		"gpt-5.3-codex": {
			DisplayName:            "gpt-5.3-codex",
			ContextWindowTokens:    272000,
			MaxContextWindowTokens: 272000,
			EffectiveContextRatio:  0.95,
			AutoCompactRatio:       0.90,
			TruncationMode:         "tokens",
			TruncationLimit:        10000,
			ReasoningEfforts:       []string{"low", "medium", "high", "xhigh"},
		},
		"codex-auto-review": {
			DisplayName:            "Codex Auto Review",
			ContextWindowTokens:    272000,
			MaxContextWindowTokens: 1000000,
			EffectiveContextRatio:  0.95,
			AutoCompactRatio:       0.90,
			TruncationMode:         "tokens",
			TruncationLimit:        10000,
			ReasoningEfforts:       []string{"low", "medium", "high", "xhigh"},
		},
		"gpt-5.5": {
			DisplayName:            "GPT-5.5",
			ContextWindowTokens:    272000,
			MaxContextWindowTokens: 272000,
			EffectiveContextRatio:  0.95,
			AutoCompactRatio:       0.90,
			TruncationMode:         "tokens",
			TruncationLimit:        10000,
			ReasoningEfforts:       []string{"low", "medium", "high", "xhigh"},
			SupportsPriorityTier:   true,
		},
		"gpt-5.6-*": {
			DisplayName:            "Amazon Bedrock GPT-5.6",
			ContextWindowTokens:    272000,
			MaxContextWindowTokens: 272000,
			EffectiveContextRatio:  0.95,
			AutoCompactRatio:       0.90,
			TruncationMode:         "tokens",
			TruncationLimit:        10000,
			ReasoningEfforts:       []string{"low", "medium", "high", "xhigh", "max"},
		},
		"claude-haiku-4-5*": {
			DisplayName:         "Claude Haiku 4.5",
			ContextWindowTokens: 200000,
			MaxOutputTokens:     64000,
			ReasoningEfforts:    []string{"extended"},
		},
		"claude-sonnet-4-5*": {
			DisplayName:         "Claude Sonnet 4.5",
			ContextWindowTokens: 200000,
			MaxOutputTokens:     64000,
			ReasoningEfforts:    []string{"extended"},
		},
		"claude-opus-4-5*": {
			DisplayName:         "Claude Opus 4.5",
			ContextWindowTokens: 200000,
			MaxOutputTokens:     64000,
			ReasoningEfforts:    []string{"low", "medium", "high"},
		},
		"claude-sonnet-4-6*": {
			DisplayName:         "Claude Sonnet 4.6",
			ContextWindowTokens: 1000000,
			MaxOutputTokens:     64000,
			ReasoningEfforts:    []string{"low", "medium", "high", "max"},
		},
		"claude-opus-4-6*": {
			DisplayName:         "Claude Opus 4.6",
			ContextWindowTokens: 1000000,
			MaxOutputTokens:     128000,
			ReasoningEfforts:    []string{"low", "medium", "high", "max"},
		},
		"claude-opus-4-7*": {
			DisplayName:         "Claude Opus 4.7",
			ContextWindowTokens: 1000000,
			MaxOutputTokens:     128000,
			ReasoningEfforts:    []string{"low", "medium", "high", "xhigh", "max"},
		},
		"claude-opus-4-8*": {
			DisplayName:         "Claude Opus 4.8",
			ContextWindowTokens: 1000000,
			MaxOutputTokens:     128000,
			ReasoningEfforts:    []string{"low", "medium", "high", "xhigh", "max"},
		},
		"claude-haiku-5*": {
			DisplayName:         "Claude Haiku 5",
			ContextWindowTokens: 1000000,
			MaxOutputTokens:     128000,
			ReasoningEfforts:    []string{"low", "medium", "high", "xhigh", "max"},
		},
		"claude-opus-5*": {
			DisplayName:         "Claude Opus 5",
			ContextWindowTokens: 1000000,
			MaxOutputTokens:     128000,
			ReasoningEfforts:    []string{"low", "medium", "high", "xhigh", "max"},
		},
		"claude-sonnet-5*": {
			DisplayName:         "Claude Sonnet 5",
			ContextWindowTokens: 1000000,
			MaxOutputTokens:     128000,
			ReasoningEfforts:    []string{"low", "medium", "high", "xhigh", "max"},
		},
		"claude-fable-5*": {
			DisplayName:         "Claude Fable 5",
			ContextWindowTokens: 1000000,
			MaxOutputTokens:     128000,
			ReasoningEfforts:    []string{"low", "medium", "high", "xhigh", "max"},
		},
		"claude-mythos-5*": {
			DisplayName:         "Claude Mythos 5",
			ContextWindowTokens: 1000000,
			MaxOutputTokens:     128000,
			ReasoningEfforts:    []string{"low", "medium", "high", "xhigh", "max"},
		},
		"claude-mythos-preview*": {
			DisplayName:         "Claude Mythos Preview",
			ContextWindowTokens: 1000000,
			ReasoningEfforts:    []string{"max"},
		},
		"fable": {
			DisplayName:         "Claude Fable alias",
			ContextWindowTokens: 1000000,
			MaxOutputTokens:     128000,
		},
		"mythos": {
			DisplayName:         "Claude Mythos alias",
			ContextWindowTokens: 1000000,
			MaxOutputTokens:     128000,
		},
		"opus": {
			DisplayName:         "Claude Opus alias",
			ContextWindowTokens: 1000000,
			MaxOutputTokens:     128000,
		},
		"sonnet": {
			DisplayName:         "Claude Sonnet alias",
			ContextWindowTokens: 1000000,
			MaxOutputTokens:     64000,
		},
		"haiku": {
			DisplayName:         "Claude Haiku alias",
			ContextWindowTokens: 200000,
			MaxOutputTokens:     64000,
		},
		"*": {
			DisplayName:            "Codex fallback",
			ContextWindowTokens:    272000,
			MaxContextWindowTokens: 272000,
			EffectiveContextRatio:  0.95,
			AutoCompactRatio:       0.90,
			TruncationMode:         "bytes",
			TruncationLimit:        10000,
		},
	}
}

// BuiltinUpstreamModelCapabilities 返回 CCX 内置的实际上游模型能力知识库。
var (
	builtinOnce             sync.Once
	builtinCapabilitiesOnce map[string]UpstreamModelCapability
)

func BuiltinUpstreamModelCapabilities() map[string]UpstreamModelCapability {
	builtinOnce.Do(func() {
		builtinCapabilitiesOnce = generatedBuiltinUpstreamModelCapabilities()
		initBuiltinPatternCache(precisionKeys(builtinCapabilitiesOnce))
	})
	return builtinCapabilitiesOnce
}

func precisionKeys(m map[string]UpstreamModelCapability) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// ResolveAgentModelProfile 解析下游 agent 模型语义。
func ResolveAgentModelProfile(requestModel string, global map[string]AgentModelProfile) ResolvedAgentModelProfile {
	if profile, pattern, ok := resolvePatternValue(requestModel, global); ok {
		return ResolvedAgentModelProfile{Profile: profile, MatchedPattern: pattern, Source: "global", Known: true}
	}
	if profile, pattern, ok := resolvePatternValue(requestModel, BuiltinAgentModelProfiles()); ok {
		return ResolvedAgentModelProfile{Profile: profile, MatchedPattern: pattern, Source: "builtin", Known: true}
	}
	return ResolvedAgentModelProfile{}
}

// ResolveUpstreamCapability 解析渠道中实际模型的能力。
func ResolveUpstreamCapability(requestModel string, upstream *UpstreamConfig, global map[string]UpstreamModelCapability) ResolvedUpstreamCapability {
	actualModel := requestModel
	if upstream != nil {
		actualModel = RedirectModel(requestModel, upstream)
		if capability, pattern, ok := resolveCapabilityForModels(actualModel, requestModel, upstream.ModelCapabilities); ok {
			return ResolvedUpstreamCapability{Capability: capability, RequestModel: requestModel, ActualModel: actualModel, MatchedPattern: pattern, Source: "channel", Known: true}
		}
	}
	if capability, pattern, ok := resolveCapabilityForModels(actualModel, requestModel, global); ok {
		return ResolvedUpstreamCapability{Capability: capability, RequestModel: requestModel, ActualModel: actualModel, MatchedPattern: pattern, Source: "global", Known: true}
	}
	if capability, pattern, ok := resolveCapabilityForModelsFold(actualModel, requestModel, BuiltinUpstreamModelCapabilities()); ok {
		return ResolvedUpstreamCapability{Capability: capability, RequestModel: requestModel, ActualModel: actualModel, MatchedPattern: pattern, Source: "builtin", Known: true}
	}
	if upstream != nil && (upstream.DefaultCapability.ContextWindowTokens > 0 || upstream.DefaultCapability.MaxOutputTokens > 0) {
		return ResolvedUpstreamCapability{Capability: upstream.DefaultCapability, RequestModel: requestModel, ActualModel: actualModel, Source: "channel_default", Known: true}
	}
	return ResolvedUpstreamCapability{RequestModel: requestModel, ActualModel: actualModel}
}

func resolveCapabilityForModels(actualModel, requestModel string, capabilities map[string]UpstreamModelCapability) (UpstreamModelCapability, string, bool) {
	if capability, pattern, ok := resolvePatternValue(actualModel, capabilities); ok {
		return capability, pattern, true
	}
	if requestModel != actualModel {
		if capability, pattern, ok := resolvePatternValue(requestModel, capabilities); ok {
			return capability, pattern, true
		}
	}
	return UpstreamModelCapability{}, "", false
}

func resolveCapabilityForModelsFold(actualModel, requestModel string, capabilities map[string]UpstreamModelCapability) (UpstreamModelCapability, string, bool) {
	if capability, pattern, ok := resolvePatternValueFold(actualModel, capabilities); ok {
		return capability, pattern, true
	}
	if requestModel != actualModel {
		if capability, pattern, ok := resolvePatternValueFold(requestModel, capabilities); ok {
			return capability, pattern, true
		}
	}
	return UpstreamModelCapability{}, "", false
}

func resolvePatternValue[T any](model string, values map[string]T) (T, string, bool) {
	var zero T
	model = strings.TrimSpace(model)
	if model == "" || len(values) == 0 {
		return zero, "", false
	}
	if value, ok := values[model]; ok {
		return value, model, true
	}

	patterns := make([]string, 0, len(values))
	for pattern := range values {
		if pattern == model {
			continue
		}
		if isValidSupportedModelPattern(pattern) {
			patterns = append(patterns, pattern)
		}
	}
	sort.Slice(patterns, func(i, j int) bool {
		if len(patterns[i]) == len(patterns[j]) {
			return patterns[i] < patterns[j]
		}
		return len(patterns[i]) > len(patterns[j])
	})

	for _, pattern := range patterns {
		if matchSupportedModelPattern(pattern, model) {
			return values[pattern], pattern, true
		}
	}
	return zero, "", false
}

func resolvePatternValueFold[T any](model string, values map[string]T) (T, string, bool) {
	var zero T
	model = strings.TrimSpace(model)
	if model == "" || len(values) == 0 {
		return zero, "", false
	}
	if value, ok := values[model]; ok {
		return value, model, true
	}
	for pattern, value := range values {
		if strings.EqualFold(pattern, model) {
			return value, pattern, true
		}
	}

	patterns := make([]string, 0, len(values))
	for pattern := range values {
		if strings.EqualFold(pattern, model) {
			continue
		}
		patterns = append(patterns, pattern)
	}
	sort.Slice(patterns, func(i, j int) bool {
		if len(patterns[i]) == len(patterns[j]) {
			return patterns[i] < patterns[j]
		}
		return len(patterns[i]) > len(patterns[j])
	})

	for _, pattern := range patterns {
		// 优先用正则匹配（builtin 正则），失败再回退通配符
		if matchBuiltinRegexPattern(pattern, model) {
			return values[pattern], pattern, true
		}
		if matchSupportedModelPatternFold(pattern, model) {
			return values[pattern], pattern, true
		}
	}
	return zero, "", false
}

func matchSupportedModelPatternFold(pattern, model string) bool {
	return matchSupportedModelPattern(strings.ToLower(pattern), strings.ToLower(model))
}
