package config

import "testing"

func TestLookupBuiltinManifest_ExactHostMatch(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		serviceType string
		wantFound   bool
		wantModels  int
	}{
		{
			name:        "Anthropic API 精确 host 匹配",
			baseURL:     "https://api.anthropic.com",
			serviceType: "messages",
			wantFound:   true,
			wantModels:  10,
		},
		{
			name:        "Anthropic API 带尾部斜杠",
			baseURL:     "https://api.anthropic.com/",
			serviceType: "messages",
			wantFound:   true,
			wantModels:  10,
		},
		{
			name:        "Anthropic API 带路径前缀",
			baseURL:     "https://api.anthropic.com/v1",
			serviceType: "messages",
			wantFound:   true,
			wantModels:  10,
		},
		{
			name:        "Anthropic API 带 # 标记",
			baseURL:     "https://api.anthropic.com#",
			serviceType: "messages",
			wantFound:   true,
			wantModels:  10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest, found := LookupBuiltinManifest(tt.baseURL, tt.serviceType)
			if found != tt.wantFound {
				t.Fatalf("found = %v, want %v", found, tt.wantFound)
			}
			if found && len(manifest.ModelIDs) != tt.wantModels {
				t.Fatalf("modelIDs len = %d, want %d", len(manifest.ModelIDs), tt.wantModels)
			}
		})
	}
}

func TestLookupBuiltinManifest_ServiceTypeMismatch(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		serviceType string
	}{
		{
			name:        "Anthropic host 但 serviceType 不匹配",
			baseURL:     "https://api.anthropic.com",
			serviceType: "chat",
		},
		{
			name:        "Anthropic host 但 serviceType 为 responses",
			baseURL:     "https://api.anthropic.com",
			serviceType: "responses",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, found := LookupBuiltinManifest(tt.baseURL, tt.serviceType)
			if found {
				t.Fatalf("serviceType=%q 不应匹配 Anthropic messages 清单", tt.serviceType)
			}
		})
	}
}

func TestLookupBuiltinManifest_UnmatchedHost(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		serviceType string
	}{
		{
			name:        "未知域名不匹配",
			baseURL:     "https://custom-proxy.example.com",
			serviceType: "messages",
		},
		{
			name:        "相似但不同的域名不匹配",
			baseURL:     "https://api-anthropic.com",
			serviceType: "messages",
		},
		{
			name:        "子域名不匹配",
			baseURL:     "https://proxy.api.anthropic.com",
			serviceType: "messages",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, found := LookupBuiltinManifest(tt.baseURL, tt.serviceType)
			if found {
				t.Fatalf("baseURL=%q 不应匹配任何清单", tt.baseURL)
			}
		})
	}
}

func TestLookupBuiltinManifest_EmptyInputs(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		serviceType string
	}{
		{name: "空 baseURL", baseURL: "", serviceType: "messages"},
		{name: "空 serviceType", baseURL: "https://api.anthropic.com", serviceType: ""},
		{name: "两者都空", baseURL: "", serviceType: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, found := LookupBuiltinManifest(tt.baseURL, tt.serviceType)
			if found {
				t.Fatalf("空输入不应匹配任何清单")
			}
		})
	}
}

func TestLookupBuiltinManifest_ModelIDsContent(t *testing.T) {
	manifest, found := LookupBuiltinManifest("https://api.anthropic.com", "messages")
	if !found {
		t.Fatal("Anthropic 清单应存在")
	}

	// 验证包含关键模型
	expectedModels := []string{
		"claude-fable-5",
		"claude-sonnet-5",
		"claude-opus-4-8",
		"claude-sonnet-4-6",
		"claude-haiku-4-5",
	}
	modelSet := make(map[string]bool, len(manifest.ModelIDs))
	for _, id := range manifest.ModelIDs {
		modelSet[id] = true
	}
	for _, expected := range expectedModels {
		if !modelSet[expected] {
			t.Errorf("缺少模型 %q", expected)
		}
	}

	// 验证 PlanHint
	if manifest.PlanHint != "anthropic_api" {
		t.Errorf("planHint = %q, want anthropic_api", manifest.PlanHint)
	}

	// 验证 DisableProbe
	if manifest.DisableProbe {
		t.Errorf("Anthropic API 清单 disableProbe 应为 false")
	}
}

func TestLookupBuiltinManifest_MiMoAnthropicTokenPlan(t *testing.T) {
	manifest, found := LookupBuiltinManifest("https://token-plan-cn.xiaomimimo.com/anthropic", "messages")
	if !found {
		t.Fatal("MiMo token plan Anthropic 清单应存在")
	}
	if !manifest.DisableProbe {
		t.Fatal("MiMo Anthropic 清单应跳过 /v1/models 探测")
	}
	if manifest.PlanHint != "mimo_token_plan_cn_anthropic" {
		t.Fatalf("planHint = %q, want mimo_token_plan_cn_anthropic", manifest.PlanHint)
	}
	expected := []string{"mimo-v2.5-pro", "mimo-v2.5"}
	if len(manifest.ModelIDs) != len(expected) {
		t.Fatalf("ModelIDs len = %d, want %d", len(manifest.ModelIDs), len(expected))
	}
	for i, modelID := range expected {
		if manifest.ModelIDs[i] != modelID {
			t.Fatalf("ModelIDs[%d] = %q, want %q", i, manifest.ModelIDs[i], modelID)
		}
	}
	expectedExcludes := []string{`^mimo-v2\.5-(?:asr|tts(?:-.+)?)$`}
	if len(manifest.ExcludeModelPatterns) != len(expectedExcludes) {
		t.Fatalf("ExcludeModelPatterns len = %d, want %d", len(manifest.ExcludeModelPatterns), len(expectedExcludes))
	}
	for i, pattern := range expectedExcludes {
		if manifest.ExcludeModelPatterns[i] != pattern {
			t.Fatalf("ExcludeModelPatterns[%d] = %q, want %q", i, manifest.ExcludeModelPatterns[i], pattern)
		}
	}
}

func TestLookupBuiltinManifest_VolcengineAgentPlanIncludesKimiK3(t *testing.T) {
	for _, tt := range []struct {
		baseURL     string
		serviceType string
	}{
		{baseURL: "https://ark.cn-beijing.volces.com/api/plan", serviceType: "messages"},
		{baseURL: "https://ark.cn-beijing.volces.com/api/plan/v3", serviceType: "openai"},
	} {
		manifest, found := LookupBuiltinManifest(tt.baseURL, tt.serviceType)
		if !found {
			t.Fatalf("Agent Plan 清单应存在: baseURL=%q serviceType=%q", tt.baseURL, tt.serviceType)
		}
		foundK3 := false
		for _, modelID := range manifest.ModelIDs {
			if modelID == "kimi-k3" {
				foundK3 = true
				break
			}
		}
		if !foundK3 {
			t.Fatalf("Agent Plan 清单应包含 kimi-k3: %+v", manifest.ModelIDs)
		}
	}
}

func TestLookupBuiltinManifest_VolcengineResponses(t *testing.T) {
	for _, tt := range []struct {
		baseURL    string
		wantModels []string
	}{
		{baseURL: "https://ark.cn-beijing.volces.com/api/plan/v3", wantModels: volcengineAgentPlanModelIDs()},
		{baseURL: "https://ark.cn-beijing.volces.com/api/coding/v3", wantModels: volcengineCodingPlanModelIDs()},
	} {
		manifest, found := LookupBuiltinManifest(tt.baseURL, "responses")
		if !found {
			t.Fatalf("火山套餐 responses 清单应存在: baseURL=%q", tt.baseURL)
		}
		if len(manifest.ModelIDs) != len(tt.wantModels) {
			t.Fatalf("responses 清单模型数 = %d, 期望 %d (baseURL=%q)", len(manifest.ModelIDs), len(tt.wantModels), tt.baseURL)
		}
		if !manifest.DisableProbe {
			t.Fatalf("火山套餐清单应 DisableProbe: %+v", manifest)
		}
	}
}

func TestResolveBuiltinModelsURL_DeepSeekUsesOfficialEndpoint(t *testing.T) {
	tests := []struct {
		baseURL     string
		serviceType string
	}{
		{baseURL: "https://api.deepseek.com/anthropic", serviceType: "messages"},
		{baseURL: "https://api.deepseek.com", serviceType: "openai"},
	}
	for _, tt := range tests {
		modelsURL, ok := ResolveBuiltinModelsURL(tt.baseURL, tt.serviceType)
		if !ok || modelsURL != "https://api.deepseek.com/models" {
			t.Fatalf("ResolveBuiltinModelsURL(%q, %q) = %q, %v", tt.baseURL, tt.serviceType, modelsURL, ok)
		}
	}
}

func TestResolveBuiltinModelsURLRejectsDifferentServer(t *testing.T) {
	manifests := builtinModelsManifests
	t.Cleanup(func() { builtinModelsManifests = manifests })
	builtinModelsManifests = []BuiltinModelsManifest{{
		BaseURLPattern: "api.example.com", ServiceType: "openai", ModelsURL: "https://api.example.com:8443/models",
	}}
	if modelsURL, ok := ResolveBuiltinModelsURL("https://api.example.com", "OPENAI"); ok || modelsURL != "" {
		t.Fatalf("不同端口不应接收渠道凭证: modelsURL=%q ok=%v", modelsURL, ok)
	}
}

func TestLookupBuiltinManifest_MiMoOpenAIChatTokenPlan(t *testing.T) {
	manifest, found := LookupBuiltinManifest("https://token-plan-cn.xiaomimimo.com/v1", "openai")
	if !found {
		t.Fatal("MiMo token plan OpenAI Chat 清单应存在")
	}
	if manifest.DisableProbe {
		t.Fatal("MiMo OpenAI Chat 清单应允许 /models 探测并过滤非文本模型")
	}
	if manifest.PlanHint != "mimo_token_plan_cn_openai" {
		t.Fatalf("planHint = %q, want mimo_token_plan_cn_openai", manifest.PlanHint)
	}
	expected := []string{"mimo-v2.5-pro", "mimo-v2.5"}
	if len(manifest.ModelIDs) != len(expected) {
		t.Fatalf("ModelIDs len = %d, want %d", len(manifest.ModelIDs), len(expected))
	}
	for i, modelID := range expected {
		if manifest.ModelIDs[i] != modelID {
			t.Fatalf("ModelIDs[%d] = %q, want %q", i, manifest.ModelIDs[i], modelID)
		}
	}
	if len(manifest.ExcludeModelPatterns) != 1 || manifest.ExcludeModelPatterns[0] != `^mimo-v2\.5-(?:asr|tts(?:-.+)?)$` {
		t.Fatalf("ExcludeModelPatterns = %v", manifest.ExcludeModelPatterns)
	}
}

func TestLookupBuiltinManifest_KimiCodeUsesPerKeyModelsEndpoint(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		serviceType string
	}{
		{name: "Anthropic 入口", baseURL: "https://api.kimi.com/coding/", serviceType: "messages"},
		{name: "OpenAI 入口", baseURL: "https://api.kimi.com/coding/v1", serviceType: "openai"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest, found := LookupBuiltinManifest(tt.baseURL, tt.serviceType)
			if !found {
				t.Fatalf("Kimi Code 清单未匹配: %s %s", tt.baseURL, tt.serviceType)
			}
			if manifest.DisableProbe {
				t.Fatal("Kimi Code 应允许通过 /coding/v1/models 按 Key 探测")
			}
			if manifest.ModelsURL != "https://api.kimi.com/coding/v1/models" {
				t.Fatalf("ModelsURL = %q", manifest.ModelsURL)
			}
			want := []string{"kimi-for-coding"}
			if len(manifest.ModelIDs) != len(want) {
				t.Fatalf("ModelIDs = %v, want %v", manifest.ModelIDs, want)
			}
			for i := range want {
				if manifest.ModelIDs[i] != want[i] {
					t.Fatalf("ModelIDs[%d] = %q, want %q", i, manifest.ModelIDs[i], want[i])
				}
			}
		})
	}
}

func TestMatchManifestPattern(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		pattern string
		want    bool
	}{
		{"精确匹配", "api.anthropic.com", "api.anthropic.com", true},
		{"host 匹配带路径", "api.anthropic.com/v1", "api.anthropic.com", true},
		{"host+path 前缀匹配", "api.anthropic.com/v1", "api.anthropic.com/v1", true},
		{"子域名不匹配", "proxy.api.anthropic.com", "api.anthropic.com", false},
		{"不同域名", "api.openai.com", "api.anthropic.com", false},
		{"空 host", "", "api.anthropic.com", false},
		{"空 pattern", "api.anthropic.com", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchManifestPattern(tt.host, tt.pattern)
			if got != tt.want {
				t.Errorf("matchManifestPattern(%q, %q) = %v, want %v", tt.host, tt.pattern, got, tt.want)
			}
		})
	}
}

func TestNormalizeBaseURLForManifest(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"标准 HTTPS URL", "https://api.anthropic.com", "api.anthropic.com"},
		{"带路径", "https://api.anthropic.com/v1", "api.anthropic.com/v1"},
		{"带尾部斜杠", "https://api.anthropic.com/", "api.anthropic.com"},
		{"带 # 标记", "https://api.anthropic.com#", "api.anthropic.com"},
		{"带端口", "https://localhost:8080", "localhost:8080"},
		{"空字符串", "", ""},
		{"纯空格", "   ", ""},
		{"无 scheme", "api.anthropic.com", "api.anthropic.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeBaseURLForManifest(tt.input)
			if got != tt.want {
				t.Errorf("normalizeBaseURLForManifest(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
