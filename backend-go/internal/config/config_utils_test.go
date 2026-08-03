package config

import (
	"fmt"
	"testing"
)

func TestSupportsModel(t *testing.T) {
	tests := []struct {
		name            string
		supportedModels []string
		model           string
		want            bool
	}{
		{"空列表匹配所有", nil, "gpt-4o", true},
		{"空列表匹配空模型", nil, "", true},
		{"精确匹配", []string{"gpt-4o"}, "gpt-4o", true},
		{"精确不匹配", []string{"gpt-4o"}, "gpt-4-turbo", false},
		{"前缀匹配", []string{"gpt-4*"}, "gpt-4o", true},
		{"后缀匹配", []string{"*image"}, "gpt-image", true},
		{"包含匹配", []string{"*image*"}, "gpt-4-image-preview", true},
		{"通配符不匹配", []string{"gpt-4*"}, "o3", false},
		{"多模式匹配第二个", []string{"gpt-4*", "claude-*"}, "claude-3-opus", true},
		{"精确和通配符混合", []string{"o3", "gpt-4*"}, "o3", true},
		{"通配符星号本身", []string{"*"}, "anything", true},
		{"精确排除命中", []string{"gpt-4*", "!gpt-4-image-preview"}, "gpt-4-image-preview", false},
		{"包含排除命中", []string{"gpt-4*", "!*image*"}, "gpt-4-image-preview", false},
		{"后缀排除命中", []string{"*", "!*image"}, "gpt-image", false},
		{"仅排除且未命中时放行", []string{"!*image*"}, "gpt-4o", true},
		{"排除优先于包含", []string{"*image*", "!*image*"}, "gpt-image", false},
		{"非法中间通配被跳过且不影响合法规则", []string{"foo*bar", "gpt-4*"}, "gpt-4o", true},
		{"仅非法中间通配时等价于无有效规则", []string{"foo*bar"}, "foobar", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &UpstreamConfig{SupportedModels: tt.supportedModels}
			if got := u.SupportsModel(tt.model); got != tt.want {
				t.Errorf("SupportsModel(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

func TestExplainModelSupport(t *testing.T) {
	tests := []struct {
		name            string
		supportedModels []string
		model           string
		wantSupported   bool
		wantReason      string
	}{
		{"空列表匹配所有", nil, "gpt-5.5", true, ""},
		{"命中排除规则", []string{"*", "!gpt-5.5"}, "gpt-5.5", false, "命中排除规则 !gpt-5.5"},
		{"未命中包含规则", []string{"claude-*"}, "gpt-5.5", false, "未命中包含规则"},
		{"命中包含规则", []string{"gpt-*"}, "gpt-5.5", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := &UpstreamConfig{SupportedModels: tt.supportedModels}
			gotSupported, gotReason := u.ExplainModelSupport(tt.model)
			if gotSupported != tt.wantSupported || gotReason != tt.wantReason {
				t.Fatalf("ExplainModelSupport(%q) = (%v, %q), want (%v, %q)", tt.model, gotSupported, gotReason, tt.wantSupported, tt.wantReason)
			}
		})
	}
}

func TestRuntimeUpstreamForAutoManagedProviderStripsLegacyCompat(t *testing.T) {
	trueValue := true
	upstream := &UpstreamConfig{
		ProviderID:                    "mimo",
		AutoManaged:                   true,
		ServiceType:                   "claude",
		BaseURL:                       "https://token-plan-cn.xiaomimimo.com/anthropic",
		APIKeys:                       []string{"sk-test"},
		SupportedModels:               []string{"mimo-v2.5-pro", "mimo-v2.5"},
		RateLimitRPM:                  80,
		ModelMapping:                  map[string]string{"sonnet": "legacy-target"},
		ReasoningMapping:              map[string]string{"sonnet": "max"},
		ReasoningParamStyle:           "thinking",
		NormalizeMetadataUserID:       &trueValue,
		StripBillingHeader:            &trueValue,
		NormalizeSystemRoleToTopLevel: true,
		NoVision:                      true,
		NoVisionModels:                []string{"mimo-v2.5-pro"},
		VisionFallbackModel:           "mimo-v2.5",
		CodexToolCompat:               &trueValue,
		StripCodexClientTools:         true,
		ConvertImageURLToB64JSON:      true,
		InjectDummyThoughtSignature:   true,
		StripThoughtSignature:         true,
		HistoricalImageTurnLimit:      4,
		CompactModel:                  "legacy-compact",
	}
	// 六个兼容性开关不再是可写字段：历史手工值只会以种子形态存在，运行时归一化须清空它们。
	upstream.SetCompatSeed(TraitStripEmptyTextBlocks, true)
	upstream.SetCompatSeed(TraitPassbackReasoningContent, true)
	upstream.SetCompatSeed(TraitPassbackThinkingBlocks, true)
	upstream.SetCompatSeed(TraitStripImageGenTool, true)
	upstream.SetCompatSeed(TraitNormalizeNonstandardChatRoles, true)
	upstream.SetCompatSeed(TraitCodexNativeToolPassthrough, true)

	runtime := RuntimeUpstreamForAutoManagedProvider(upstream)
	if runtime == upstream {
		t.Fatal("autoManaged provider should return a sanitized clone")
	}
	if len(runtime.ModelMapping) != 0 || len(runtime.ReasoningMapping) != 0 || runtime.ReasoningParamStyle != "" {
		t.Fatalf("legacy model/reasoning fields not stripped: %#v", runtime)
	}
	if runtime.IsPassbackReasoningContentEnabled() || runtime.IsPassbackThinkingBlocksEnabled() || runtime.IsStripEmptyTextBlocksEnabled() || runtime.NormalizeSystemRoleToTopLevel {
		t.Fatalf("legacy Claude compat fields not stripped: %#v", runtime)
	}
	if runtime.NoVision || len(runtime.NoVisionModels) != 0 || runtime.VisionFallbackModel != "" {
		t.Fatalf("legacy vision compat fields not stripped: %#v", runtime)
	}
	if len(runtime.SupportedModels) != 2 || runtime.RateLimitRPM != 80 || runtime.ProviderID != "mimo" {
		t.Fatalf("runtime scheduling fields should be preserved: %#v", runtime)
	}
	if len(upstream.ModelMapping) == 0 || !upstream.IsPassbackReasoningContentEnabled() {
		t.Fatal("original upstream must not be mutated")
	}
}

func TestRuntimeUpstreamForAutoManagedProviderLeavesManualChannelUntouched(t *testing.T) {
	upstream := &UpstreamConfig{
		ProviderID:   "",
		AutoManaged:  false,
		ModelMapping: map[string]string{"sonnet": "manual-target"},
		NoVision:     true,
	}

	runtime := RuntimeUpstreamForAutoManagedProvider(upstream)
	if runtime != upstream {
		t.Fatal("manual channel should not be cloned or sanitized")
	}
	if runtime.ModelMapping["sonnet"] != "manual-target" || !runtime.NoVision {
		t.Fatalf("manual channel fields changed: %#v", runtime)
	}
}

func TestStripAutoManagedExplicitOverrides(t *testing.T) {
	trueValue := true
	upstream := &UpstreamConfig{
		ProviderID:                    "glm",
		AutoManaged:                   true,
		ModelMapping:                  map[string]string{"sonnet": "glm-5.2"},
		ReasoningMapping:              map[string]string{"sonnet": "high"},
		ReasoningParamStyle:           "thinking",
		FastMode:                      true,
		CompatSeeds:                   map[string]CompatSeedEntry{"passback_reasoning_content": {Enabled: true}},
		CodexToolCompat:               &trueValue,
		StripCodexClientTools:         true,
		ConvertImageURLToB64JSON:      true,
		NormalizeMetadataUserID:       &trueValue,
		StripBillingHeader:            &trueValue,
		NormalizeSystemRoleToTopLevel: true,
		InjectDummyThoughtSignature:   true,
		StripThoughtSignature:         true,
		NoVision:                      true,
		NoVisionModels:                []string{"glm-5.2"},
		VisionFallbackModel:           "glm-5.2-air",
		HistoricalImageTurnLimit:      4,
		CompactModel:                  "glm-5.2-mini",
		SupportedModels:               []string{"glm-5.2"},
	}

	if !stripAutoManagedExplicitOverrides(upstream) {
		t.Fatal("expected auto-managed explicit overrides to be stripped")
	}
	if len(upstream.ModelMapping) != 0 || len(upstream.ReasoningMapping) != 0 || upstream.ReasoningParamStyle != "" || upstream.FastMode {
		t.Fatalf("model/reasoning overrides not stripped: %#v", upstream)
	}
	if len(upstream.CompatSeeds) != 0 || upstream.CodexToolCompat != nil || upstream.StripCodexClientTools || upstream.ConvertImageURLToB64JSON {
		t.Fatalf("compat overrides not stripped: %#v", upstream)
	}
	if upstream.NormalizeMetadataUserID != nil || upstream.StripBillingHeader != nil || upstream.NormalizeSystemRoleToTopLevel || upstream.InjectDummyThoughtSignature || upstream.StripThoughtSignature {
		t.Fatalf("request normalization overrides not stripped: %#v", upstream)
	}
	if upstream.NoVision || len(upstream.NoVisionModels) != 0 || upstream.VisionFallbackModel != "" || upstream.HistoricalImageTurnLimit != 0 || upstream.CompactModel != "" {
		t.Fatalf("vision/compact overrides not stripped: %#v", upstream)
	}
	if len(upstream.SupportedModels) != 1 || upstream.SupportedModels[0] != "glm-5.2" {
		t.Fatalf("supportedModels should remain intact: %#v", upstream)
	}
	if stripAutoManagedExplicitOverrides(upstream) {
		t.Fatal("second strip should be idempotent")
	}
}

func TestRuntimeUpstreamForAutoManagedProviderStripsWithoutProviderID(t *testing.T) {
	upstream := &UpstreamConfig{
		AutoManaged:         true,
		ProviderID:          "",
		ServiceType:         "claude",
		ModelMapping:        map[string]string{"opus": "gpt-5.4"},
		ReasoningMapping:    map[string]string{"opus": "high"},
		ReasoningParamStyle: "thinking",
		FastMode:            true,
	}

	runtime := RuntimeUpstreamForAutoManagedProvider(upstream)
	if runtime == upstream {
		t.Fatal("autoManaged upstream without providerId should still return a sanitized clone")
	}
	if len(runtime.ModelMapping) != 0 || len(runtime.ReasoningMapping) != 0 || runtime.ReasoningParamStyle != "" || runtime.FastMode {
		t.Fatalf("runtime should strip stale explicit overrides even without providerId: %#v", runtime)
	}
	if runtime.NormalizeSystemRoleToTopLevel {
		t.Fatalf("no provider defaults should be applied when providerId is empty: %#v", runtime)
	}
	if len(upstream.ModelMapping) == 0 || upstream.ReasoningParamStyle != "thinking" {
		t.Fatal("original upstream must remain unchanged")
	}
}

func TestRuntimeUpstreamForAutoManagedProviderReappliesNativeDefaults(t *testing.T) {
	upstream := &UpstreamConfig{
		ProviderID:          "glm",
		AutoManaged:         true,
		ServiceType:         "openai",
		ReasoningParamStyle: "thinking",
	}
	// 历史手工关闭 passback 只会以种子形态残留；运行时归一化须清掉种子并恢复 GLM 静态默认。
	upstream.SetCompatSeed(TraitPassbackReasoningContent, false)

	runtime := RuntimeUpstreamForAutoManagedProvider(upstream)
	if runtime.ReasoningParamStyle != "reasoning_effort" || !runtime.IsPassbackReasoningContentEnabled() {
		t.Fatalf("GLM OpenAI 原生默认值未在运行时恢复: %#v", runtime)
	}
	if upstream.ReasoningParamStyle != "thinking" || upstream.IsPassbackReasoningContentEnabled() {
		t.Fatalf("原始配置不应被运行时归一化修改: %#v", upstream)
	}
}

func TestRuntimeUpstreamForAutoManagedProviderAppliesCompshareMessagesDefaults(t *testing.T) {
	upstream := &UpstreamConfig{
		ProviderID:                    "compshare",
		AutoManaged:                   true,
		ServiceType:                   "claude",
		NormalizeSystemRoleToTopLevel: false,
	}

	runtime := RuntimeUpstreamForAutoManagedProvider(upstream)
	if !runtime.NormalizeSystemRoleToTopLevel {
		t.Fatalf("Compshare Claude runtime 应启用 system role 归一化: %#v", runtime)
	}
	if upstream.NormalizeSystemRoleToTopLevel {
		t.Fatalf("原始配置不应被运行时默认值修改: %#v", upstream)
	}
}

func TestIsValidSupportedModelPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    bool
	}{
		{"精确匹配合法", "gpt-4o", true},
		{"前缀匹配合法", "gpt-4*", true},
		{"后缀匹配合法", "*image", true},
		{"包含匹配合法", "*image*", true},
		{"全通配合法", "*", true},
		{"空字符串非法", "", false},
		{"仅空白非法", "   ", false},
		{"空 contains 非法", "**", false},
		{"多重排除前缀非法", "!!gpt-4*", false},
		{"含中文顿号非法", "gpt-5*、ada*", false},
		{"含逗号非法", "gpt-5*,ada*", false},
		{"含空格非法", "gpt 5", false},
		{"含中文字符非法", "模型", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidSupportedModelPattern(tt.pattern); got != tt.want {
				t.Errorf("isValidSupportedModelPattern(%q) = %v, want %v", tt.pattern, got, tt.want)
			}
		})
	}
}

func TestSanitizeDeprecatedGrokModelMapping(t *testing.T) {
	t.Run("nil map 原样返回", func(t *testing.T) {
		cleaned, changed := sanitizeDeprecatedGrokModelMapping(nil)
		if changed {
			t.Fatalf("expected changed=false for nil map")
		}
		if cleaned != nil {
			t.Fatalf("expected nil map to remain nil, got %v", cleaned)
		}
	})

	t.Run("空 map 原样返回", func(t *testing.T) {
		mm := map[string]string{}
		cleaned, changed := sanitizeDeprecatedGrokModelMapping(mm)
		if changed {
			t.Fatalf("expected changed=false for empty map")
		}
		if len(cleaned) != 0 {
			t.Fatalf("expected empty map, got %v", cleaned)
		}
	})

	t.Run("无关映射不受影响", func(t *testing.T) {
		mm := map[string]string{"gpt": "gpt-5"}
		cleaned, changed := sanitizeDeprecatedGrokModelMapping(mm)
		if changed {
			t.Fatalf("expected changed=false, got true")
		}
		if cleaned["gpt"] != "gpt-5" {
			t.Fatalf("unrelated mapping got altered: %v", cleaned)
		}
	})

	t.Run("命中第一对精确映射被删除", func(t *testing.T) {
		mm := map[string]string{"grok-4.1": "grok-4.1-thinking", "gpt": "gpt-5"}
		cleaned, changed := sanitizeDeprecatedGrokModelMapping(mm)
		if !changed {
			t.Fatalf("expected changed=true")
		}
		if _, ok := cleaned["grok-4.1"]; ok {
			t.Fatalf("expected grok-4.1 removed, got %v", cleaned)
		}
		if cleaned["gpt"] != "gpt-5" {
			t.Fatalf("expected unrelated mapping preserved, got %v", cleaned)
		}
	})

	t.Run("命中第二对精确映射被删除", func(t *testing.T) {
		mm := map[string]string{"grok-4.2": "grok-4.20-beta"}
		cleaned, changed := sanitizeDeprecatedGrokModelMapping(mm)
		if !changed {
			t.Fatalf("expected changed=true")
		}
		if len(cleaned) != 0 {
			t.Fatalf("expected empty map after cleanup, got %v", cleaned)
		}
	})

	t.Run("两对同时命中且保留其他 key", func(t *testing.T) {
		mm := map[string]string{
			"grok-4.1": "grok-4.1-thinking",
			"grok-4.2": "grok-4.20-beta",
			"gpt":      "gpt-5",
		}
		cleaned, changed := sanitizeDeprecatedGrokModelMapping(mm)
		if !changed {
			t.Fatalf("expected changed=true")
		}
		if len(cleaned) != 1 || cleaned["gpt"] != "gpt-5" {
			t.Fatalf("expected only gpt mapping to remain, got %v", cleaned)
		}
	})

	t.Run("相同 key 不同 target 不删除", func(t *testing.T) {
		mm := map[string]string{"grok-4.1": "custom-grok-4.1"}
		cleaned, changed := sanitizeDeprecatedGrokModelMapping(mm)
		if changed {
			t.Fatalf("expected changed=false for custom target")
		}
		if cleaned["grok-4.1"] != "custom-grok-4.1" {
			t.Fatalf("expected custom mapping preserved, got %v", cleaned)
		}
	})

	t.Run("相同 target 不同 key 不删除", func(t *testing.T) {
		mm := map[string]string{"my-alias": "grok-4.1-thinking"}
		cleaned, changed := sanitizeDeprecatedGrokModelMapping(mm)
		if changed {
			t.Fatalf("expected changed=false for unrelated key with same target")
		}
		if cleaned["my-alias"] != "grok-4.1-thinking" {
			t.Fatalf("expected mapping preserved, got %v", cleaned)
		}
	})

	t.Run("对已清理结果二次调用保持幂等且不分配新 map", func(t *testing.T) {
		mm := map[string]string{"gpt": "gpt-5"}
		cleaned1, changed1 := sanitizeDeprecatedGrokModelMapping(mm)
		if changed1 {
			t.Fatalf("expected changed=false on first call without deprecated pairs")
		}
		cleaned2, changed2 := sanitizeDeprecatedGrokModelMapping(cleaned1)
		if changed2 {
			t.Fatalf("expected changed=false on second call")
		}
		if len(cleaned2) != 1 || cleaned2["gpt"] != "gpt-5" {
			t.Fatalf("expected mapping unchanged, got %v", cleaned2)
		}
	})
}

func TestMigrateAutoManagedExplicitMappings(t *testing.T) {
	cm := &ConfigManager{}
	legacy := func(name string) UpstreamConfig {
		trueValue := true
		return UpstreamConfig{
			Name:                  name,
			ProviderID:            "glm",
			AutoManaged:           true,
			ModelMapping:          map[string]string{"sonnet": "glm-5.2"},
			ReasoningMapping:      map[string]string{"sonnet": "high"},
			ReasoningParamStyle:   "thinking",
			FastMode:              true,
			CodexToolCompat:       &trueValue,
			StripCodexClientTools: true,
			NoVisionModels:        []string{"glm-5.2"},
			SupportedModels:       []string{"glm-5.2"},
		}
	}
	manual := UpstreamConfig{
		Name:            "manual",
		ProviderID:      "",
		AutoManaged:     false,
		ModelMapping:    map[string]string{"sonnet": "manual-target"},
		SupportedModels: []string{"manual-target"},
	}
	cm.config.Upstream = []UpstreamConfig{legacy("messages"), manual}
	cm.config.ResponsesUpstream = []UpstreamConfig{legacy("responses")}
	cm.config.GeminiUpstream = []UpstreamConfig{legacy("gemini")}
	cm.config.ChatUpstream = []UpstreamConfig{legacy("chat")}
	cm.config.ImagesUpstream = []UpstreamConfig{legacy("images")}
	cm.config.VectorsUpstream = []UpstreamConfig{legacy("vectors")}

	if !cm.migrateAutoManagedExplicitMappings() {
		t.Fatal("expected auto-managed mapping migration to report changes")
	}
	for _, channels := range [][]UpstreamConfig{cm.config.Upstream[:1], cm.config.ResponsesUpstream, cm.config.GeminiUpstream, cm.config.ChatUpstream, cm.config.ImagesUpstream, cm.config.VectorsUpstream} {
		if len(channels[0].ModelMapping) != 0 || len(channels[0].ReasoningMapping) != 0 || channels[0].ReasoningParamStyle != "" {
			t.Fatalf("auto-managed mapping overrides not stripped: %#v", channels[0])
		}
		if len(channels[0].SupportedModels) != 1 || channels[0].SupportedModels[0] != "glm-5.2" {
			t.Fatalf("supportedModels should remain intact after migration: %#v", channels[0])
		}
	}
	if cm.config.Upstream[1].ModelMapping["sonnet"] != "manual-target" {
		t.Fatalf("manual channel should remain untouched: %#v", cm.config.Upstream[1])
	}
	if cm.migrateAutoManagedExplicitMappings() {
		t.Fatal("second migration should be idempotent")
	}
}

func TestMigrateDeprecatedGrokModelMapping(t *testing.T) {
	newChannels := func() []UpstreamConfig {
		return []UpstreamConfig{
			{
				Name: "legacy",
				ModelMapping: map[string]string{
					"grok-4.1": "grok-4.1-thinking",
					"grok-4.2": "grok-4.20-beta",
				},
			},
			{
				Name: "custom",
				ModelMapping: map[string]string{
					"grok-4.1": "my-custom-target",
				},
			},
		}
	}

	cm := &ConfigManager{}
	cm.config.Upstream = newChannels()
	cm.config.ResponsesUpstream = newChannels()
	cm.config.GeminiUpstream = newChannels()
	cm.config.ChatUpstream = newChannels()
	cm.config.ImagesUpstream = newChannels()
	cm.config.VectorsUpstream = newChannels()

	if !cm.migrateDeprecatedGrokModelMapping() {
		t.Fatalf("expected migrateDeprecatedGrokModelMapping to return true")
	}

	channelSets := map[string][]UpstreamConfig{
		"Upstream":          cm.config.Upstream,
		"ResponsesUpstream": cm.config.ResponsesUpstream,
		"GeminiUpstream":    cm.config.GeminiUpstream,
		"ChatUpstream":      cm.config.ChatUpstream,
		"ImagesUpstream":    cm.config.ImagesUpstream,
		"VectorsUpstream":   cm.config.VectorsUpstream,
	}
	for name, channels := range channelSets {
		legacy := channels[0]
		if _, ok := legacy.ModelMapping["grok-4.1"]; ok {
			t.Fatalf("%s: expected legacy grok-4.1 mapping removed, got %v", name, legacy.ModelMapping)
		}
		if _, ok := legacy.ModelMapping["grok-4.2"]; ok {
			t.Fatalf("%s: expected legacy grok-4.2 mapping removed, got %v", name, legacy.ModelMapping)
		}
		custom := channels[1]
		if custom.ModelMapping["grok-4.1"] != "my-custom-target" {
			t.Fatalf("%s: expected custom grok-4.1 mapping preserved, got %v", name, custom.ModelMapping)
		}
	}

	// 再次调用应为幂等，不再产生变更
	if cm.migrateDeprecatedGrokModelMapping() {
		t.Fatalf("expected migrateDeprecatedGrokModelMapping to return false on second call")
	}
}

func TestMigrateFableReasoningMapping(t *testing.T) {
	cm := &ConfigManager{}
	cm.config.Upstream = []UpstreamConfig{
		{
			Name: "demo",
			ReasoningMapping: map[string]string{
				"opus": "high",
			},
		},
	}

	if !cm.migrateFableReasoningMapping() {
		t.Fatalf("expected migrateFableReasoningMapping to return true")
	}

	if got := cm.config.Upstream[0].ReasoningMapping["fable"]; got != "high" {
		t.Fatalf("ReasoningMapping[fable] = %q, want high", got)
	}

	// 已有 fable 配置时不应覆盖
	cm.config.Upstream[0].ReasoningMapping["fable"] = "medium"
	if cm.migrateFableReasoningMapping() {
		t.Fatalf("expected migrateFableReasoningMapping to return false when fable already exists")
	}
	if got := cm.config.Upstream[0].ReasoningMapping["fable"]; got != "medium" {
		t.Fatalf("ReasoningMapping[fable] = %q, want medium", got)
	}
}

func TestParseSupportedModelInput(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"中文顿号拆分", "GPT-5*、ada*", []string{"GPT-5*", "ada*"}},
		{"混合分隔符", "a, b ; c | d", []string{"a", "b", "c", "d"}},
		{"中文逗号与多余空白", "  gpt-4*  ，  *image*  ", []string{"gpt-4*", "*image*"}},
		{"纯分隔符返回空", "、、 ,, ；", []string{}},
		{"空字符串返回空", "", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSupportedModelInput(tt.raw)
			if len(got) != len(tt.want) {
				t.Fatalf("parseSupportedModelInput(%q) = %v, want %v", tt.raw, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("parseSupportedModelInput(%q) = %v, want %v", tt.raw, got, tt.want)
				}
			}
		})
	}
}

func TestSplitSupportedModelRulesSeparators(t *testing.T) {
	includes, excludes := splitSupportedModelRules([]string{"GPT-5*、ada*", "!*image*"})
	wantIncludes := []string{"GPT-5*", "ada*"}
	wantExcludes := []string{"*image*"}

	if len(includes) != len(wantIncludes) {
		t.Fatalf("includes = %v, want %v", includes, wantIncludes)
	}
	for i := range includes {
		if includes[i] != wantIncludes[i] {
			t.Fatalf("includes = %v, want %v", includes, wantIncludes)
		}
	}
	if len(excludes) != len(wantExcludes) || excludes[0] != wantExcludes[0] {
		t.Fatalf("excludes = %v, want %v", excludes, wantExcludes)
	}
}

func TestResolveReasoningEffort(t *testing.T) {
	upstream := &UpstreamConfig{
		ReasoningMapping: map[string]string{
			"gpt-5":         "high",
			"gpt-5.1-codex": "xhigh",
			"o3":            "medium",
		},
	}

	tests := []struct {
		name  string
		model string
		want  string
	}{
		{"精确匹配", "o3", "medium"},
		{"最长匹配优先", "gpt-5.1-codex", "xhigh"},
		{"模糊匹配回退", "gpt-5.1", "high"},
		{"未匹配返回空", "claude-3-7-sonnet", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveReasoningEffort(tt.model, upstream); got != tt.want {
				t.Fatalf("ResolveReasoningEffort(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}

func TestNormalizeMiMoResponsesReasoningEffort(t *testing.T) {
	upstream := &UpstreamConfig{
		ServiceType: "responses",
		BaseURL:     "https://token-plan-sgp.xiaomimimo.com",
		ReasoningMapping: map[string]string{
			"gpt":  "max",
			"mini": "xhigh",
			"off":  "off",
		},
	}

	if got := ResolveReasoningEffort("gpt-5.5", upstream); got != "high" {
		t.Fatalf("ResolveReasoningEffort gpt = %q, want high", got)
	}
	if got := ResolveReasoningEffort("mini", upstream); got != "high" {
		t.Fatalf("ResolveReasoningEffort mini = %q, want high", got)
	}
	if got := ResolveReasoningEffort("off", upstream); got != "none" {
		t.Fatalf("ResolveReasoningEffort off = %q, want none", got)
	}

	req := map[string]interface{}{
		"reasoning": map[string]interface{}{"effort": "max"},
	}
	NormalizeReasoningObjectForUpstream(req, upstream)
	reasoning := req["reasoning"].(map[string]interface{})
	if reasoning["effort"] != "high" {
		t.Fatalf("reasoning.effort = %q, want high", reasoning["effort"])
	}
}

func TestIsValidReasoningEffort(t *testing.T) {
	valid := []string{"", "off", "none", "minimal", "low", "medium", "high", "xhigh", "max"}
	for _, effort := range valid {
		t.Run("valid_"+effort, func(t *testing.T) {
			if !isValidReasoningEffort(effort) {
				t.Fatalf("isValidReasoningEffort(%q) = false, want true", effort)
			}
		})
	}

	if isValidReasoningEffort("ultra") {
		t.Fatalf("isValidReasoningEffort(%q) = true, want false", "ultra")
	}
}

func TestApplyReasoningParamStyle(t *testing.T) {
	tests := []struct {
		name       string
		style      string
		effort     string
		initial    map[string]interface{}
		wantKey    string
		wantNil    bool // true = want no reasoning/thinking key at all
		wantType   string
		wantEffort string
	}{
		// ── thinking style ──
		{
			name:       "thinking: enabled with effort",
			style:      "thinking",
			effort:     "high",
			initial:    map[string]interface{}{},
			wantKey:    "thinking",
			wantType:   "enabled",
			wantEffort: "high",
		},
		{
			name:     "thinking: disabled on off",
			style:    "thinking",
			effort:   "off",
			initial:  map[string]interface{}{},
			wantKey:  "thinking",
			wantType: "disabled",
		},
		{
			name:     "thinking: disabled on none",
			style:    "thinking",
			effort:   "none",
			initial:  map[string]interface{}{},
			wantKey:  "thinking",
			wantType: "disabled",
		},
		{
			name:    "thinking: empty effort = passthrough (no thinking key)",
			style:   "thinking",
			effort:  "",
			initial: map[string]interface{}{},
			wantNil: true,
		},
		{
			name:       "thinking: preserves existing thinking map",
			style:      "thinking",
			effort:     "medium",
			initial:    map[string]interface{}{"thinking": map[string]interface{}{"type": "enabled", "budget_tokens": 5000}},
			wantKey:    "thinking",
			wantType:   "enabled",
			wantEffort: "medium",
		},
		{
			name:    "thinking: cleans up stale reasoning key",
			style:   "thinking",
			effort:  "high",
			initial: map[string]interface{}{"reasoning": map[string]interface{}{"effort": "low"}},
			wantKey: "thinking",
		},
		// ── reasoning_effort style ──
		{
			name:    "reasoning_effort: sets effort string",
			style:   "reasoning_effort",
			effort:  "high",
			initial: map[string]interface{}{},
			wantKey: "reasoning_effort",
		},
		{
			name:    "reasoning_effort: empty effort = no key",
			style:   "reasoning_effort",
			effort:  "",
			initial: map[string]interface{}{},
			wantNil: true,
		},
		// ── default (reasoning object) style ──
		{
			name:    "default: sets reasoning.effort",
			style:   "reasoning",
			effort:  "medium",
			initial: map[string]interface{}{},
			wantKey: "reasoning",
		},
		{
			name:    "default: empty effort = no key",
			style:   "reasoning",
			effort:  "",
			initial: map[string]interface{}{},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := make(map[string]interface{})
			for k, v := range tt.initial {
				req[k] = v
			}
			ApplyReasoningParamStyle(req, tt.style, tt.effort)

			if tt.wantNil {
				if _, ok := req["thinking"]; ok {
					t.Errorf("want no 'thinking' key, got %v", req["thinking"])
				}
				if _, ok := req["reasoning"]; ok {
					t.Errorf("want no 'reasoning' key, got %v", req["reasoning"])
				}
				if _, ok := req["reasoning_effort"]; ok {
					t.Errorf("want no 'reasoning_effort' key, got %v", req["reasoning_effort"])
				}
				return
			}

			if tt.wantKey == "thinking" {
				thinking, ok := req["thinking"].(map[string]interface{})
				if !ok {
					t.Fatalf("want thinking map, got %T: %v", req["thinking"], req["thinking"])
				}
				if tt.wantType != "" {
					if got, _ := thinking["type"].(string); got != tt.wantType {
						t.Errorf("thinking.type = %q, want %q", got, tt.wantType)
					}
				}
				if tt.wantEffort != "" {
					if got, _ := thinking["effort"].(string); got != tt.wantEffort {
						t.Errorf("thinking.effort = %q, want %q", got, tt.wantEffort)
					}
				}
				// budget_tokens must be cleaned up
				if _, ok := thinking["budget_tokens"]; ok {
					t.Error("thinking.budget_tokens should be deleted")
				}
				// stale reasoning key must be cleaned up
				if _, ok := req["reasoning"]; ok {
					t.Error("stale 'reasoning' key should be deleted when style=thinking")
				}
				if _, ok := req["reasoning_effort"]; ok {
					t.Error("stale 'reasoning_effort' key should be deleted when style=thinking")
				}
			}
		})
	}
}

// TestApplyReasoningParamStyle_Gemini 覆盖 Gemini 原生 thinkingConfig 注入形态。
func TestApplyReasoningParamStyle_Gemini(t *testing.T) {
	tests := []struct {
		name        string
		effort      string
		initial     map[string]interface{}
		wantLevel   string // 期望的 thinkingLevel；空串表示不应存在该字段
		wantBudget  bool   // 期望 thinkingBudget=0（关闭思考）
		wantNoInjet bool   // 期望完全不注入 generationConfig.thinkingConfig
	}{
		{name: "minimal maps to minimal", effort: "minimal", wantLevel: "minimal"},
		{name: "low maps to low", effort: "low", wantLevel: "low"},
		{name: "medium maps to medium", effort: "medium", wantLevel: "medium"},
		{name: "high maps to high", effort: "high", wantLevel: "high"},
		// Gemini 官方枚举没有 max/xhigh，最高档收敛到 high
		{name: "max clamps to high", effort: "max", wantLevel: "high"},
		{name: "xhigh clamps to high", effort: "xhigh", wantLevel: "high"},
		// off/none 走 thinkingBudget=0，而不是发明新的 level token
		{name: "off disables via thinkingBudget", effort: "off", wantBudget: true},
		{name: "none disables via thinkingBudget", effort: "none", wantBudget: true},
		// 无法映射与空档位都必须 fail-open：不注入任何字段
		{name: "empty effort injects nothing", effort: "", wantNoInjet: true},
		{name: "unmappable effort injects nothing", effort: "turbo", wantNoInjet: true},
		{
			name:       "existing thinkingBudget is cleared when level is set",
			effort:     "high",
			initial:    map[string]interface{}{"generationConfig": map[string]interface{}{"thinkingConfig": map[string]interface{}{"thinkingBudget": 1024}}},
			wantLevel:  "high",
			wantBudget: false,
		},
		{
			name:    "existing thinkingLevel is cleared when disabled",
			effort:  "off",
			initial: map[string]interface{}{"generationConfig": map[string]interface{}{"thinkingConfig": map[string]interface{}{"thinkingLevel": "high"}}},
			// 关闭时不能残留 thinkingLevel，否则与 thinkingBudget 冲突
			wantBudget: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := make(map[string]interface{})
			for k, v := range tt.initial {
				req[k] = v
			}
			ApplyReasoningParamStyle(req, ReasoningParamStyleGemini, tt.effort)

			// Gemini 形态绝不写 Claude/OpenAI/Responses 的互斥字段
			for _, forbidden := range []string{"thinking", "reasoning", "reasoning_effort"} {
				if _, ok := req[forbidden]; ok {
					t.Errorf("gemini style must not set %q, got %v", forbidden, req[forbidden])
				}
			}

			generationConfig, hasGenerationConfig := req["generationConfig"].(map[string]interface{})
			if tt.wantNoInjet {
				if hasGenerationConfig {
					if _, ok := generationConfig["thinkingConfig"]; ok {
						t.Fatalf("want no thinkingConfig injection, got %v", generationConfig["thinkingConfig"])
					}
				}
				return
			}
			if !hasGenerationConfig {
				t.Fatalf("want generationConfig, got %T: %v", req["generationConfig"], req["generationConfig"])
			}
			thinkingConfig, ok := generationConfig["thinkingConfig"].(map[string]interface{})
			if !ok {
				t.Fatalf("want thinkingConfig map, got %T: %v", generationConfig["thinkingConfig"], generationConfig["thinkingConfig"])
			}

			if tt.wantLevel != "" {
				if got, _ := thinkingConfig["thinkingLevel"].(string); got != tt.wantLevel {
					t.Errorf("thinkingLevel = %q, want %q", got, tt.wantLevel)
				}
				if _, ok := thinkingConfig["thinkingBudget"]; ok {
					t.Error("thinkingBudget must be cleared when thinkingLevel is set")
				}
			}
			if tt.wantBudget {
				budget, ok := thinkingConfig["thinkingBudget"]
				if !ok {
					t.Fatal("want thinkingBudget=0 for disabled thinking")
				}
				if fmt.Sprintf("%v", budget) != "0" {
					t.Errorf("thinkingBudget = %v, want 0", budget)
				}
				if _, ok := thinkingConfig["thinkingLevel"]; ok {
					t.Error("thinkingLevel must be cleared when thinking is disabled")
				}
			}
		})
	}
}
