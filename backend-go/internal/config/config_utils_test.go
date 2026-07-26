package config

import "testing"

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
		StripEmptyTextBlocks:          true,
		NormalizeSystemRoleToTopLevel: true,
		PassbackReasoningContent:      true,
		PassbackThinkingBlocks:        true,
		NoVision:                      true,
		NoVisionModels:                []string{"mimo-v2.5-pro"},
		VisionFallbackModel:           "mimo-v2.5",
		StripImageGenerationTool:      true,
		NormalizeNonstandardChatRoles: true,
		CodexNativeToolPassthrough:    true,
		CodexToolCompat:               &trueValue,
		StripCodexClientTools:         true,
		ConvertImageURLToB64JSON:      true,
		InjectDummyThoughtSignature:   true,
		StripThoughtSignature:         true,
		HistoricalImageTurnLimit:      4,
		CompactModel:                  "legacy-compact",
	}

	runtime := RuntimeUpstreamForAutoManagedProvider(upstream)
	if runtime == upstream {
		t.Fatal("autoManaged provider should return a sanitized clone")
	}
	if len(runtime.ModelMapping) != 0 || len(runtime.ReasoningMapping) != 0 || runtime.ReasoningParamStyle != "" {
		t.Fatalf("legacy model/reasoning fields not stripped: %#v", runtime)
	}
	if runtime.PassbackReasoningContent || runtime.PassbackThinkingBlocks || runtime.StripEmptyTextBlocks || runtime.NormalizeSystemRoleToTopLevel {
		t.Fatalf("legacy Claude compat fields not stripped: %#v", runtime)
	}
	if runtime.NoVision || len(runtime.NoVisionModels) != 0 || runtime.VisionFallbackModel != "" {
		t.Fatalf("legacy vision compat fields not stripped: %#v", runtime)
	}
	if len(runtime.SupportedModels) != 2 || runtime.RateLimitRPM != 80 || runtime.ProviderID != "mimo" {
		t.Fatalf("runtime scheduling fields should be preserved: %#v", runtime)
	}
	if len(upstream.ModelMapping) == 0 || upstream.PassbackReasoningContent == false {
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

func TestRuntimeUpstreamForAutoManagedProviderReappliesNativeDefaults(t *testing.T) {
	upstream := &UpstreamConfig{
		ProviderID:               "glm",
		AutoManaged:              true,
		ServiceType:              "openai",
		ReasoningParamStyle:      "thinking",
		PassbackReasoningContent: false,
	}

	runtime := RuntimeUpstreamForAutoManagedProvider(upstream)
	if runtime.ReasoningParamStyle != "reasoning_effort" || !runtime.PassbackReasoningContent {
		t.Fatalf("GLM OpenAI 原生默认值未在运行时恢复: %#v", runtime)
	}
	if upstream.ReasoningParamStyle != "thinking" || upstream.PassbackReasoningContent {
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
