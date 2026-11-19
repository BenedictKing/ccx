package config

import "testing"

func TestRedirectModelWithRuleDeterministic(t *testing.T) {
	tests := []struct {
		model   string
		mapping map[string]string
		target  string
		source  string
		match   string
	}{
		{"sol-sun", map[string]string{"sun": "sun-target", "sol": "sol-target"}, "sol-target", "sol", "contains"},
		{"SOL-sol", map[string]string{"sol": "lower", "SOL": "upper"}, "upper", "SOL", "contains"},
		{"gpt-sol", map[string]string{"sol": "short", "gpt-sol": "exact"}, "exact", "gpt-sol", "exact"},
		{"模型-sol", map[string]string{"sol": "ascii", "模型": "unicode"}, "unicode", "模型", "contains"},
		{"𐀀-\ue000a", map[string]string{"𐀀": "astral", "\ue000a": "bmp"}, "bmp", "\ue000a", "contains"},
		{"sol", map[string]string{"sol": ""}, "", "sol", "exact"},
		{"astra", map[string]string{"sol": "other"}, "astra", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			// 重复查找覆盖 Go map 的不稳定遍历顺序，同时校验日志记录的命中规则。
			for range 64 {
				target, source, match := RedirectModelWithRule(tt.model, &UpstreamConfig{ModelMapping: tt.mapping})
				if target != tt.target || source != tt.source || match != tt.match {
					t.Fatalf("got (%q, %q, %q), want (%q, %q, %q)", target, source, match, tt.target, tt.source, tt.match)
				}
			}
		})
	}
}

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
	valid := []string{"", "off", "none", "low", "medium", "high", "xhigh", "max"}
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

func TestRequestModelForAgent(t *testing.T) {
	upstream := &UpstreamConfig{SubagentModel: " sol "}
	if got := RequestModelForAgent("astra", upstream, "subagent"); got != "sol" {
		t.Fatalf("subagent model = %q, want sol", got)
	}
	if got := RequestModelForAgent("astra", upstream, "main"); got != "astra" {
		t.Fatalf("main model = %q, want astra", got)
	}
	if got := RequestModelForAgent("astra", &UpstreamConfig{}, "subagent"); got != "astra" {
		t.Fatalf("empty subagent model = %q, want astra", got)
	}
}

func TestWithAgentModelResolvesClassAndCustomModel(t *testing.T) {
	upstream := &UpstreamConfig{
		SubagentModel: "sol",
		ModelMapping: map[string]string{
			"astra": "gpt-main",
			"sol":   "gpt-subagent",
		},
	}

	resolved := WithAgentModel("astra", upstream, "subagent")
	if got := RedirectModel("astra", resolved); got != "gpt-subagent" {
		t.Fatalf("subagent redirected model = %q, want gpt-subagent", got)
	}
	if got := RedirectModel("astra", upstream); got != "gpt-main" {
		t.Fatalf("original channel mapping mutated: %q", got)
	}
	if got := RedirectModel("astra", WithAgentModel("astra", upstream, "main")); got != "gpt-main" {
		t.Fatalf("main redirected model = %q, want gpt-main", got)
	}

	upstream.SubagentModel = "gpt-exact"
	upstream.ModelMapping["gpt"] = "should-not-apply"
	if got := RedirectModel("astra", WithAgentModel("astra", upstream, "subagent")); got != "gpt-exact" {
		t.Fatalf("custom subagent model = %q, want gpt-exact", got)
	}
}

func TestApplySubagentModelUpdate(t *testing.T) {
	upstream := &UpstreamConfig{}
	value := " sol "
	applySubagentModelUpdate(upstream, UpstreamUpdate{SubagentModel: &value})
	if upstream.SubagentModel != "sol" {
		t.Fatalf("SubagentModel = %q, want sol", upstream.SubagentModel)
	}
	value = ""
	applySubagentModelUpdate(upstream, UpstreamUpdate{SubagentModel: &value})
	if upstream.SubagentModel != "" {
		t.Fatalf("SubagentModel after clear = %q, want empty", upstream.SubagentModel)
	}
}
