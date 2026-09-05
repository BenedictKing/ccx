package config

import (
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/BenedictKing/ccx/internal/presetstore"
)

type compiledBuiltinPattern struct {
	regex               *regexp.Regexp
	hasSuffixConstraint bool
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

func matchBuiltinRegexPatternWithCache(pattern, model string, patternCache map[string]*compiledBuiltinPattern) bool {
	if len(patternCache) == 0 {
		return false
	}
	compiled, ok := patternCache[pattern]
	if !ok {
		return false
	}
	if !compiled.regex.MatchString(model) {
		return false
	}
	if compiled.hasSuffixConstraint {
		loc := compiled.regex.FindStringIndex(model)
		if loc == nil {
			return false
		}
		endIdx := loc[1]
		if endIdx < len(model) && model[endIdx] != '@' {
			return false
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

// ResolvedModelBenchmarkProfile 描述规范模型能力基准的匹配结果。
type ResolvedModelBenchmarkProfile struct {
	Profile        ModelBenchmarkProfile
	Model          string
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

func builtinGPTAgentModelProfile(displayName string, contextWindowTokens, maxContextWindowTokens, maxOutputTokens int, truncationMode string, reasoningEfforts []string, supportsPriorityTier bool) AgentModelProfile {
	return AgentModelProfile{
		DisplayName:            displayName,
		ContextWindowTokens:    contextWindowTokens,
		MaxContextWindowTokens: maxContextWindowTokens,
		MaxOutputTokens:        maxOutputTokens,
		EffectiveContextRatio:  0.95,
		AutoCompactRatio:       0.90,
		TruncationMode:         truncationMode,
		TruncationLimit:        10000,
		ReasoningEfforts:       reasoningEfforts,
		SupportsPriorityTier:   supportsPriorityTier,
	}
}

// BuiltinAgentModelProfiles 返回 CCX 内置的下游 agent 模型知识库。
func BuiltinAgentModelProfiles() map[string]AgentModelProfile {
	return map[string]AgentModelProfile{
		"gpt-5.2": builtinGPTAgentModelProfile(
			"GPT-5.2", 272000, 272000, 128000, "bytes",
			[]string{"none", "minimal", "low", "medium", "high", "xhigh"}, true,
		),
		"gpt-5.2-2025-12-11": builtinGPTAgentModelProfile(
			"GPT-5.2", 272000, 272000, 128000, "bytes",
			[]string{"none", "minimal", "low", "medium", "high", "xhigh"}, true,
		),
		"gpt-5.2-chat-latest": builtinGPTAgentModelProfile(
			"GPT-5.2 Chat Latest", 128000, 128000, 16384, "tokens",
			[]string{"minimal", "low", "medium", "high"}, false,
		),
		"gpt-5.2-pro": builtinGPTAgentModelProfile(
			"GPT-5.2 Pro", 272000, 272000, 128000, "tokens",
			[]string{"minimal", "low", "medium", "high", "xhigh"}, false,
		),
		"gpt-5.2-pro-2025-12-11": builtinGPTAgentModelProfile(
			"GPT-5.2 Pro", 272000, 272000, 128000, "tokens",
			[]string{"minimal", "low", "medium", "high", "xhigh"}, false,
		),
		"gpt-5.2-codex": builtinGPTAgentModelProfile(
			"GPT-5.2 Codex", 272000, 272000, 128000, "tokens",
			[]string{"minimal", "low", "medium", "high", "xhigh"}, false,
		),
		"gpt-5.3-codex": builtinGPTAgentModelProfile(
			"GPT-5.3 Codex", 272000, 272000, 128000, "tokens",
			[]string{"minimal", "low", "medium", "high"}, false,
		),
		"gpt-5.3-chat-latest": builtinGPTAgentModelProfile(
			"GPT-5.3 Chat Latest", 128000, 128000, 16384, "tokens",
			[]string{"minimal", "low", "medium", "high"}, false,
		),
		"gpt-5.4": builtinGPTAgentModelProfile(
			"GPT-5.4", 272000, 1050000, 128000, "tokens",
			[]string{"none", "minimal", "low", "medium", "high", "xhigh"}, true,
		),
		"gpt-5.4-2026-03-05": builtinGPTAgentModelProfile(
			"GPT-5.4", 272000, 1050000, 128000, "tokens",
			[]string{"none", "minimal", "low", "medium", "high", "xhigh"}, true,
		),
		"gpt-5.4-openai-compact": builtinGPTAgentModelProfile(
			"GPT-5.4", 272000, 1050000, 128000, "tokens",
			[]string{"none", "minimal", "low", "medium", "high", "xhigh"}, true,
		),
		"gpt-5.4-pro": builtinGPTAgentModelProfile(
			"GPT-5.4 Pro", 272000, 1050000, 128000, "tokens",
			[]string{"minimal", "low", "medium", "high", "xhigh"}, true,
		),
		"gpt-5.4-pro-2026-03-05": builtinGPTAgentModelProfile(
			"GPT-5.4 Pro", 272000, 1050000, 128000, "tokens",
			[]string{"minimal", "low", "medium", "high", "xhigh"}, true,
		),
		"gpt-5.4-mini": builtinGPTAgentModelProfile(
			"GPT-5.4 Mini", 272000, 272000, 128000, "tokens",
			[]string{"none", "low", "medium", "high", "xhigh"}, true,
		),
		"gpt-5.4-mini-2026-03-17": builtinGPTAgentModelProfile(
			"GPT-5.4 Mini", 272000, 272000, 128000, "tokens",
			[]string{"none", "low", "medium", "high", "xhigh"}, true,
		),
		"gpt-5.4-nano": builtinGPTAgentModelProfile(
			"GPT-5.4 Nano", 272000, 272000, 128000, "tokens",
			[]string{"none", "low", "medium", "high", "xhigh"}, true,
		),
		"gpt-5.4-nano-2026-03-17": builtinGPTAgentModelProfile(
			"GPT-5.4 Nano", 272000, 272000, 128000, "tokens",
			[]string{"none", "low", "medium", "high", "xhigh"}, true,
		),
		"codex-auto-review": builtinGPTAgentModelProfile(
			"Codex Auto Review", 272000, 1000000, 128000, "tokens",
			[]string{"low", "medium", "high", "xhigh"}, false,
		),
		"gpt-5.5": builtinGPTAgentModelProfile(
			"GPT-5.5", 272000, 1050000, 128000, "tokens",
			[]string{"none", "minimal", "low", "medium", "high", "xhigh"}, true,
		),
		"gpt-5.5-2026-04-23": builtinGPTAgentModelProfile(
			"GPT-5.5", 272000, 1050000, 128000, "tokens",
			[]string{"none", "minimal", "low", "medium", "high", "xhigh"}, true,
		),
		"gpt-5.5-pro": builtinGPTAgentModelProfile(
			"GPT-5.5 Pro", 272000, 1050000, 128000, "tokens",
			[]string{"minimal", "low", "medium", "high", "xhigh"}, true,
		),
		"gpt-5.5-pro-2026-04-23": builtinGPTAgentModelProfile(
			"GPT-5.5 Pro", 272000, 1050000, 128000, "tokens",
			[]string{"minimal", "low", "medium", "high", "xhigh"}, true,
		),
		"gpt-5.6": builtinGPTAgentModelProfile(
			"Amazon Bedrock GPT-5.6", 272000, 1050000, 128000, "tokens",
			[]string{"low", "medium", "high", "xhigh", "max"}, false,
		),
		"gpt-5.6-*": {
			DisplayName:            "Amazon Bedrock GPT-5.6",
			ContextWindowTokens:    272000,
			MaxContextWindowTokens: 1050000,
			EffectiveContextRatio:  0.95,
			AutoCompactRatio:       0.90,
			TruncationMode:         "tokens",
			TruncationLimit:        10000,
			ReasoningEfforts:       []string{"low", "medium", "high", "xhigh", "max"},
		},
		"gpt-6-astra": builtinGPTAgentModelProfile(
			"GPT-6 Astra", 272000, 1050000, 128000, "tokens",
			[]string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}, true,
		),
		"gpt-6": builtinGPTAgentModelProfile(
			"GPT-6 Astra", 272000, 1050000, 128000, "tokens",
			[]string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}, true,
		),
		"astra": builtinGPTAgentModelProfile(
			"GPT-6 Astra", 272000, 1050000, 128000, "tokens",
			[]string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}, true,
		),
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
		// Fable 5.1 是独立模型（非 Fable 5 别名）；pattern 更长，resolvePatternValue 长度降序保证优先命中。
		"claude-fable-5-1*": {
			DisplayName:         "Claude Fable 5.1",
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
		// Mythos 5.1 是独立模型（非 Mythos 5 别名）；pattern 更长，resolvePatternValue 长度降序保证优先命中。
		"claude-mythos-5-1*": {
			DisplayName:         "Claude Mythos 5.1",
			ContextWindowTokens: 1000000,
			MaxOutputTokens:     128000,
			ReasoningEfforts:    []string{"low", "medium", "high", "xhigh", "max"},
		},
		"claude-mythos-preview*": {
			DisplayName:         "Claude Mythos Preview",
			ContextWindowTokens: 1000000,
			ReasoningEfforts:    []string{"max"},
		},
		// Kimi Code 的 K3 在 Moderato 及以上套餐至少提供 256K，
		// Allegretto 及以上套餐可扩展到 1M；实际可用范围仍以按 Key 发现结果为准。
		"k3": {
			DisplayName:            "Kimi K3",
			ContextWindowTokens:    262144,
			MaxContextWindowTokens: 1048576,
			ReasoningEfforts:       []string{"low", "high", "max"},
		},
		"k3[1m]": {
			DisplayName:            "Kimi K3 (1M)",
			ContextWindowTokens:    1048576,
			MaxContextWindowTokens: 1048576,
			ReasoningEfforts:       []string{"low", "high", "max"},
		},
		"k3-256k": {
			DisplayName:            "Kimi K3 (256K)",
			ContextWindowTokens:    262144,
			MaxContextWindowTokens: 262144,
			ReasoningEfforts:       []string{"low", "high", "max"},
		},
		"kimi-for-coding*": {
			DisplayName:         "Kimi K2.7 Code",
			ContextWindowTokens: 262144,
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
	builtinOnce       sync.Once
	builtinRebuildMu  sync.Mutex
	builtinSnapshotMu sync.RWMutex
	builtinSnapshot   upstreamCapabilitySnapshot
	builtinObservers  sync.Map
	// builtinGeneration 在每次快照重建发布时递增，供下游做"数据变了才重算"的缓存失效判断。
	builtinGeneration atomic.Uint64
)

type upstreamCapabilitySnapshot struct {
	store                 *presetstore.PresetStore
	dataVersion           string
	source                string
	capabilities          map[string]UpstreamModelCapability
	patternCache          map[string]*compiledBuiltinPattern
	benchmarks            map[string]ModelBenchmarkProfile
	benchmarkPatternCache map[string]*compiledBuiltinPattern
}

func BuiltinUpstreamModelCapabilities() map[string]UpstreamModelCapability {
	return cloneCapabilitiesMap(currentBuiltinSnapshot().capabilities)
}

// BuiltinModelBenchmarkProfiles 返回规范模型能力基准的深拷贝。
func BuiltinModelBenchmarkProfiles() map[string]ModelBenchmarkProfile {
	return cloneBenchmarkProfilesMap(currentBuiltinSnapshot().benchmarks)
}

func currentBuiltinSnapshot() upstreamCapabilitySnapshot {
	builtinOnce.Do(func() {
		rebuildBuiltinSnapshotForStore(presetstore.Default())
	})
	store := presetstore.Default()
	snapshot := getBuiltinSnapshot()
	if shouldRebuildBuiltinSnapshot(snapshot, store) {
		rebuildBuiltinSnapshotForStore(store)
	}
	return getBuiltinSnapshot()
}

func shouldRebuildBuiltinSnapshot(snapshot upstreamCapabilitySnapshot, store *presetstore.PresetStore) bool {
	if store == nil {
		return snapshot.store != nil || len(snapshot.capabilities) == 0
	}
	return snapshot.store != store || len(snapshot.capabilities) == 0
}

func getBuiltinSnapshot() upstreamCapabilitySnapshot {
	builtinSnapshotMu.RLock()
	defer builtinSnapshotMu.RUnlock()
	// snapshot 发布后保持不可变；浅拷贝持有旧 map 引用在并发替换后仍然安全，
	// 避免每次模型解析都深拷贝整个注册表。
	return builtinSnapshot
}

func rebuildBuiltinSnapshotForStore(store *presetstore.PresetStore) {
	builtinRebuildMu.Lock()
	defer builtinRebuildMu.Unlock()
	if store == nil {
		store = presetstore.Default()
	}
	if _, loaded := builtinObservers.LoadOrStore(store, struct{}{}); !loaded {
		store.RegisterOnChange(func(*presetstore.PresetBundle) {
			rebuildBuiltinSnapshotForStore(store)
		})
	}

	bundle := store.Get()
	source := "presetstore"
	capabilities := convertRuntimeCapabilities(bundle.ModelRegistry)
	benchmarks := convertRuntimeBenchmarkProfiles(bundle.ModelRegistry)
	if len(capabilities) == 0 || len(benchmarks) == 0 {
		embedded := presetstore.EmbeddedBundle()
		if len(capabilities) == 0 {
			capabilities = convertRuntimeCapabilities(embedded.ModelRegistry)
		}
		if len(benchmarks) == 0 {
			benchmarks = convertRuntimeBenchmarkProfiles(embedded.ModelRegistry)
		}
		source = "presetstore+embedded-fallback"
	}

	snapshot := upstreamCapabilitySnapshot{
		store:                 store,
		dataVersion:           bundle.DataVersion,
		source:                source,
		capabilities:          cloneCapabilitiesMap(capabilities),
		patternCache:          buildPatternCache(precisionKeys(capabilities)),
		benchmarks:            cloneBenchmarkProfilesMap(benchmarks),
		benchmarkPatternCache: buildPatternCache(benchmarkPatternKeys(benchmarks)),
	}
	builtinSnapshotMu.Lock()
	builtinSnapshot = snapshot
	builtinSnapshotMu.Unlock()
	builtinGeneration.Add(1)
	log.Printf("[ModelRegistry-Snapshot] source=%s dataVersion=%s capabilities=%d benchmarks=%d", source, bundle.DataVersion, len(capabilities), len(benchmarks))
}

// BuiltinSnapshotGeneration 返回内置能力/基准快照的世代号。
// 每次快照重建（首次初始化、SetDefault 注入新 store 或 store 数据更新回调）时递增；
// 下游派生数据（如 autopilot 质量档边界）以世代号做缓存键即可在数据更新后自动失效。
func BuiltinSnapshotGeneration() uint64 {
	currentBuiltinSnapshot()
	return builtinGeneration.Load()
}

func precisionKeys(m map[string]UpstreamModelCapability) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func benchmarkPatternKeys(m map[string]ModelBenchmarkProfile) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func buildPatternCache(patterns []string) map[string]*compiledBuiltinPattern {
	cache := make(map[string]*compiledBuiltinPattern, len(patterns))
	for _, p := range patterns {
		compiled, err := compileBuiltinPattern(p)
		if err != nil {
			panic("invalid builtin model pattern regex: " + p + ": " + err.Error())
		}
		cache[p] = compiled
	}
	return cache
}

func cloneCapabilitiesMap(src map[string]UpstreamModelCapability) map[string]UpstreamModelCapability {
	if len(src) == 0 {
		return map[string]UpstreamModelCapability{}
	}
	dst := make(map[string]UpstreamModelCapability, len(src))
	for pattern, capability := range src {
		dst[pattern] = cloneCapability(capability)
	}
	return dst
}

func cloneCapability(src UpstreamModelCapability) UpstreamModelCapability {
	dst := src
	if len(src.ReasoningEfforts) > 0 {
		dst.ReasoningEfforts = append([]string(nil), src.ReasoningEfforts...)
	}
	if len(src.ContextWindowTiers) > 0 {
		dst.ContextWindowTiers = append([]int(nil), src.ContextWindowTiers...)
	}
	if len(src.Sources) > 0 {
		dst.Sources = append([]string(nil), src.Sources...)
	}
	if len(src.Capabilities) > 0 {
		dst.Capabilities = make(map[string]bool, len(src.Capabilities))
		for key, value := range src.Capabilities {
			dst.Capabilities[key] = value
		}
	}
	if src.Pricing != nil {
		pricing := *src.Pricing
		if pricing.InputCacheHitPrice != nil {
			v := *pricing.InputCacheHitPrice
			pricing.InputCacheHitPrice = &v
		}
		if pricing.InputCacheMissPrice != nil {
			v := *pricing.InputCacheMissPrice
			pricing.InputCacheMissPrice = &v
		}
		if pricing.OutputPrice != nil {
			v := *pricing.OutputPrice
			pricing.OutputPrice = &v
		}
		if len(src.Pricing.Tiers) > 0 {
			pricing.Tiers = make([]ModelPricingTier, len(src.Pricing.Tiers))
			for i, tier := range src.Pricing.Tiers {
				pricing.Tiers[i] = clonePricingTier(tier)
			}
		}
		dst.Pricing = &pricing
	}
	if src.ParamConstraints != nil {
		constraints := *src.ParamConstraints
		if len(src.ParamConstraints.FixedParams) > 0 {
			constraints.FixedParams = append([]string(nil), src.ParamConstraints.FixedParams...)
		}
		if len(src.ParamConstraints.ThinkingFixedValue) > 0 {
			thinking := make(map[string]interface{}, len(src.ParamConstraints.ThinkingFixedValue))
			for k, v := range src.ParamConstraints.ThinkingFixedValue {
				thinking[k] = v
			}
			constraints.ThinkingFixedValue = thinking
		}
		dst.ParamConstraints = &constraints
	}
	return dst
}

func cloneBenchmarkProfilesMap(src map[string]ModelBenchmarkProfile) map[string]ModelBenchmarkProfile {
	if len(src) == 0 {
		return map[string]ModelBenchmarkProfile{}
	}
	dst := make(map[string]ModelBenchmarkProfile, len(src))
	for pattern, profile := range src {
		dst[pattern] = cloneBenchmarkProfile(profile)
	}
	return dst
}

func cloneBenchmarkProfile(src ModelBenchmarkProfile) ModelBenchmarkProfile {
	dst := src
	if len(src.CategoryScores) > 0 {
		dst.CategoryScores = make(map[string]float64, len(src.CategoryScores))
		for category, score := range src.CategoryScores {
			dst.CategoryScores[category] = score
		}
	}
	if len(src.Sources) > 0 {
		dst.Sources = append([]string(nil), src.Sources...)
	}
	if len(src.BenchmarkEvidence) > 0 {
		dst.BenchmarkEvidence = make([]ModelBenchmarkEvidence, len(src.BenchmarkEvidence))
		for i, evidence := range src.BenchmarkEvidence {
			dst.BenchmarkEvidence[i] = evidence
			if evidence.CostUSD != nil {
				v := *evidence.CostUSD
				dst.BenchmarkEvidence[i].CostUSD = &v
			}
		}
	}
	return dst
}

func clonePricingTier(src ModelPricingTier) ModelPricingTier {
	dst := src
	if src.InputCacheHitPrice != nil {
		v := *src.InputCacheHitPrice
		dst.InputCacheHitPrice = &v
	}
	if src.InputCacheMissPrice != nil {
		v := *src.InputCacheMissPrice
		dst.InputCacheMissPrice = &v
	}
	if src.OutputPrice != nil {
		v := *src.OutputPrice
		dst.OutputPrice = &v
	}
	return dst
}

// normalizeContextWindowTiers 把分段阶梯整理为升序去重的正数序列。
// 数据源手写容错：乱序、重复、非正值都不阻断加载，只是被规整。
func normalizeContextWindowTiers(tiers []int) []int {
	if len(tiers) == 0 {
		return nil
	}
	normalized := make([]int, 0, len(tiers))
	for _, tier := range tiers {
		if tier > 0 {
			normalized = append(normalized, tier)
		}
	}
	sort.Ints(normalized)
	deduped := normalized[:0]
	for i, tier := range normalized {
		if i == 0 || tier != normalized[i-1] {
			deduped = append(deduped, tier)
		}
	}
	if len(deduped) == 0 {
		return nil
	}
	return deduped
}

func convertRuntimeCapabilities(preset *presetstore.ModelRegistryPreset) map[string]UpstreamModelCapability {
	if preset == nil || len(preset.UpstreamCapabilities) == 0 {
		return nil
	}
	capabilities := make(map[string]UpstreamModelCapability)
	for _, entry := range preset.UpstreamCapabilities {
		capability := UpstreamModelCapability{
			ContextWindowTokens:     entry.ContextWindowTokens,
			ContextWindowTiers:      normalizeContextWindowTiers(entry.ContextWindowTiers),
			MaxOutputTokens:         entry.MaxOutputTokens,
			DefaultOutputTokens:     entry.DefaultOutputTokens,
			RecommendedOutputTokens: entry.RecommendedOutputTokens,
			ThinkingMode:            entry.ThinkingMode,
			ReasoningEfforts:        append([]string(nil), entry.ReasoningEfforts...),
			Provider:                entry.Provider,
			DisplayName:             entry.DisplayName,
			Description:             entry.Description,
			Sources:                 append([]string(nil), entry.Sources...),
		}
		if len(entry.Capabilities) > 0 {
			capability.Capabilities = make(map[string]bool, len(entry.Capabilities))
			for key, value := range entry.Capabilities {
				capability.Capabilities[key] = value
			}
		}
		if entry.Pricing != nil {
			capability.Pricing = &ModelPricing{
				Unit:                coalesceString(entry.Pricing.Unit, preset.PricingUnit),
				Currency:            entry.Pricing.Currency,
				InputCacheHitPrice:  cloneFloatPointer(entry.Pricing.InputCacheHitPrice),
				InputCacheMissPrice: cloneFloatPointer(entry.Pricing.InputCacheMissPrice),
				OutputPrice:         cloneFloatPointer(entry.Pricing.OutputPrice),
			}
			if len(entry.Pricing.Tiers) > 0 {
				capability.Pricing.Tiers = make([]ModelPricingTier, len(entry.Pricing.Tiers))
				for i, tier := range entry.Pricing.Tiers {
					capability.Pricing.Tiers[i] = ModelPricingTier{
						Label:               tier.Label,
						InputTokensAbove:    tier.InputTokensAbove,
						InputTokensUpTo:     tier.InputTokensUpTo,
						InputCacheHitPrice:  cloneFloatPointer(tier.InputCacheHitPrice),
						InputCacheMissPrice: cloneFloatPointer(tier.InputCacheMissPrice),
						OutputPrice:         cloneFloatPointer(tier.OutputPrice),
					}
				}
			}
		}
		if entry.ParamConstraints != nil {
			capability.ParamConstraints = &ModelParamConstraints{
				FixedParams:                   append([]string(nil), entry.ParamConstraints.FixedParams...),
				ToolChoiceRequiredUnsupported: entry.ParamConstraints.ToolChoiceRequiredUnsupported,
			}
			if len(entry.ParamConstraints.ThinkingFixedValue) > 0 {
				thinking := make(map[string]interface{}, len(entry.ParamConstraints.ThinkingFixedValue))
				for k, v := range entry.ParamConstraints.ThinkingFixedValue {
					thinking[k] = v
				}
				capability.ParamConstraints.ThinkingFixedValue = thinking
			}
		}
		for _, pattern := range entry.Patterns {
			capabilities[pattern] = cloneCapability(capability)
		}
	}
	return capabilities
}

func convertRuntimeBenchmarkProfiles(preset *presetstore.ModelRegistryPreset) map[string]ModelBenchmarkProfile {
	if preset == nil || len(preset.BenchmarkProfiles) == 0 {
		return nil
	}
	profiles := make(map[string]ModelBenchmarkProfile)
	for _, entry := range preset.BenchmarkProfiles {
		profile := ModelBenchmarkProfile{
			CanonicalModel:       entry.CanonicalModel,
			OverallScore:         entry.OverallScore,
			Sources:              append([]string(nil), entry.Sources...),
			VerifiedAt:           entry.VerifiedAt,
			Lane:                 entry.Lane,
			SharedResults:        entry.SharedResults,
			ComparableCategories: entry.ComparableCategories,
			TotalCategories:      entry.TotalCategories,
		}
		if len(entry.CategoryScores) > 0 {
			profile.CategoryScores = make(map[string]float64, len(entry.CategoryScores))
			for category, score := range entry.CategoryScores {
				profile.CategoryScores[category] = score
			}
		}
		if len(entry.BenchmarkEvidence) > 0 {
			profile.BenchmarkEvidence = make([]ModelBenchmarkEvidence, len(entry.BenchmarkEvidence))
			for i, evidence := range entry.BenchmarkEvidence {
				profile.BenchmarkEvidence[i] = ModelBenchmarkEvidence{
					Benchmark:        evidence.Benchmark,
					BenchmarkVersion: evidence.BenchmarkVersion,
					SourceModel:      evidence.SourceModel,
					Domain:           evidence.Domain,
					Metric:           evidence.Metric,
					RawValue:         evidence.RawValue,
					Uncertainty:      evidence.Uncertainty,
					CohortPercentile: evidence.CohortPercentile,
					TaskCount:        evidence.TaskCount,
					CohortSize:       evidence.CohortSize,
					Effort:           evidence.Effort,
					SelectionBasis:   evidence.SelectionBasis,
					SourceURL:        evidence.SourceURL,
					CapturedAt:       evidence.CapturedAt,
					CostUSD:          cloneFloatPointer(evidence.CostUSD),
				}
			}
		}
		for _, pattern := range entry.Patterns {
			profiles[pattern] = cloneBenchmarkProfile(profile)
		}
	}
	return profiles
}

func cloneFloatPointer(src *float64) *float64 {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}

func coalesceString(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
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
	}
	// allowRequestModelFallback=true：未提供动态映射结果时，保留历史行为——渠道/全局能力表
	// 按 requestModel 的通配符也可命中，兼容"能力表只登记请求侧别名"的既有配置。
	return resolveUpstreamCapabilityForModels(requestModel, actualModel, upstream, global, true)
}

// ResolveMappedUpstreamCapability 解析动态路由已决定的实际上游模型能力。
// mappedModel 来自 endpoint/SmartRouter 映射，不能再回退用 requestModel 的通配符命中；
// 否则会把下游 1M 模型窗口误套到实际发往上游的短窗口模型上。
func ResolveMappedUpstreamCapability(requestModel, mappedModel string, upstream *UpstreamConfig, global map[string]UpstreamModelCapability) ResolvedUpstreamCapability {
	actualModel := strings.TrimSpace(mappedModel)
	if actualModel == "" {
		return ResolveUpstreamCapability(requestModel, upstream, global)
	}
	return resolveUpstreamCapabilityForModels(requestModel, actualModel, upstream, global, false)
}

func resolveUpstreamCapabilityForModels(requestModel, actualModel string, upstream *UpstreamConfig, global map[string]UpstreamModelCapability, allowRequestModelFallback bool) ResolvedUpstreamCapability {
	if resolved := resolveUpstreamCapabilityExact(requestModel, actualModel, upstream, global, allowRequestModelFallback); resolved.Known {
		return resolved
	}
	// 命名习惯差异兜底：数字间的点号/连字符互换（如 claude-haiku-4.5 ↔ claude-haiku-4-5）。
	// 下游别名常用点号形式而能力表 pattern 用连字符（或反之），精确匹配失败时按变体重试。
	for _, variant := range digitDotDashVariants(actualModel) {
		if resolved := resolveUpstreamCapabilityExact(requestModel, variant, upstream, global, false); resolved.Known {
			return resolved
		}
	}
	return ResolvedUpstreamCapability{RequestModel: requestModel, ActualModel: actualModel}
}

func resolveUpstreamCapabilityExact(requestModel, actualModel string, upstream *UpstreamConfig, global map[string]UpstreamModelCapability, allowRequestModelFallback bool) ResolvedUpstreamCapability {
	if upstream != nil {
		if capability, pattern, ok := resolveCapabilityForModelsAllowFallback(actualModel, requestModel, upstream.ModelCapabilities, allowRequestModelFallback); ok {
			return ResolvedUpstreamCapability{Capability: capability, RequestModel: requestModel, ActualModel: actualModel, MatchedPattern: pattern, Source: "channel", Known: true}
		}
	}
	if capability, pattern, ok := resolveCapabilityForModelsAllowFallback(actualModel, requestModel, global, allowRequestModelFallback); ok {
		return ResolvedUpstreamCapability{Capability: capability, RequestModel: requestModel, ActualModel: actualModel, MatchedPattern: pattern, Source: "global", Known: true}
	}
	snapshot := currentBuiltinSnapshot()
	if capability, pattern, ok := resolvePatternValueFold(actualModel, snapshot.capabilities, snapshot.patternCache); ok {
		return ResolvedUpstreamCapability{Capability: cloneCapability(capability), RequestModel: requestModel, ActualModel: actualModel, MatchedPattern: pattern, Source: "builtin", Known: true}
	}
	if upstream != nil && (upstream.DefaultCapability.ContextWindowTokens > 0 || upstream.DefaultCapability.MaxOutputTokens > 0) {
		return ResolvedUpstreamCapability{Capability: upstream.DefaultCapability, RequestModel: requestModel, ActualModel: actualModel, Source: "channel_default", Known: true}
	}
	return ResolvedUpstreamCapability{RequestModel: requestModel, ActualModel: actualModel}
}

// digitBoundaryDotDash 分别匹配数字间的点号与连字符，用于生成命名变体。
var (
	digitBoundaryDot  = regexp.MustCompile(`(\d)\.(\d)`)
	digitBoundaryDash = regexp.MustCompile(`(\d)-(\d)`)
)

// digitDotDashVariants 返回数字间点号与连字符互换后的去重变体（不含原串）。
func digitDotDashVariants(model string) []string {
	variants := make([]string, 0, 2)
	if v := digitBoundaryDot.ReplaceAllString(model, "$1-$2"); v != model {
		variants = append(variants, v)
	}
	if v := digitBoundaryDash.ReplaceAllString(model, "$1.$2"); v != model {
		variants = append(variants, v)
	}
	return variants
}

func resolveCapabilityForModelsAllowFallback(actualModel, requestModel string, capabilities map[string]UpstreamModelCapability, allowRequestModelFallback bool) (UpstreamModelCapability, string, bool) {
	if capability, pattern, ok := resolvePatternValue(actualModel, capabilities); ok {
		return capability, pattern, true
	}
	if allowRequestModelFallback && requestModel != actualModel {
		if capability, pattern, ok := resolvePatternValue(requestModel, capabilities); ok {
			return capability, pattern, true
		}
	}
	return UpstreamModelCapability{}, "", false
}

// ResolveModelBenchmarkProfile 解析规范模型的能力上界证据。
// 基准只提供软评分依据，不参与 supportedModels 或能力下界判断。
func ResolveModelBenchmarkProfile(model string) ResolvedModelBenchmarkProfile {
	model = strings.TrimSpace(model)
	if model == "" {
		return ResolvedModelBenchmarkProfile{}
	}
	snapshot := currentBuiltinSnapshot()
	if profile, pattern, ok := resolvePatternValueFold(model, snapshot.benchmarks, snapshot.benchmarkPatternCache); ok {
		return ResolvedModelBenchmarkProfile{
			Profile:        cloneBenchmarkProfile(profile),
			Model:          model,
			MatchedPattern: pattern,
			Source:         "builtin",
			Known:          true,
		}
	}
	return ResolvedModelBenchmarkProfile{Model: model}
}

func resolveCapabilityForModelsFold(actualModel, requestModel string, capabilities map[string]UpstreamModelCapability, patternCache map[string]*compiledBuiltinPattern) (UpstreamModelCapability, string, bool) {
	if capability, pattern, ok := resolvePatternValueFold(actualModel, capabilities, patternCache); ok {
		return capability, pattern, true
	}
	if requestModel != actualModel {
		if capability, pattern, ok := resolvePatternValueFold(requestModel, capabilities, patternCache); ok {
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

func resolvePatternValueFold[T any](model string, values map[string]T, patternCache ...map[string]*compiledBuiltinPattern) (T, string, bool) {
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
		var cache map[string]*compiledBuiltinPattern
		if len(patternCache) > 0 {
			cache = patternCache[0]
		}
		// 优先用正则匹配（builtin 正则），失败再回退通配符
		if matchBuiltinRegexPatternWithCache(pattern, model, cache) {
			return values[pattern], pattern, true
		}
		if matchSupportedModelPatternFold(pattern, model, cache) {
			return values[pattern], pattern, true
		}
	}
	return zero, "", false
}

func matchSupportedModelPatternFold(pattern, model string, patternCache map[string]*compiledBuiltinPattern) bool {
	return matchSupportedModelPattern(strings.ToLower(pattern), strings.ToLower(model))
}
