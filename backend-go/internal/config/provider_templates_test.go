package config

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestCandidatesForKeyPrefix(t *testing.T) {
	tmpl, ok := GetProviderTemplate("mimo")
	if !ok {
		t.Fatal("未找到 mimo 模板")
	}

	// sk- 前缀 → payg 优先
	skCands := tmpl.CandidatesForKey("sk-abc")
	if len(skCands) == 0 || skCands[0].PlanTag != "payg" {
		t.Errorf("sk- key 首选应为 payg，实际 %+v", skCands)
	}

	// tp- 前缀 → token_plan 优先，且按 Priority（cn=0 先）
	tpCands := tmpl.CandidatesForKey("tp-xyz")
	if len(tpCands) == 0 || tpCands[0].PlanTag != "token_plan" || tpCands[0].Region != "cn" {
		t.Errorf("tp- key 首选应为 token_plan/cn，实际 %+v", tpCands)
	}
	// tp- 回退候选应包含 payg（全部候选都在）
	if len(tpCands) != len(tmpl.Candidates) {
		t.Errorf("tp- key 应返回全部候选（含回退）%d，实际 %d", len(tmpl.Candidates), len(tpCands))
	}

	// 无匹配前缀 → 返回全部候选
	unknownCands := tmpl.CandidatesForKey("xx-none")
	if len(unknownCands) != len(tmpl.Candidates) {
		t.Errorf("未匹配前缀应返回全部候选 %d，实际 %d", len(tmpl.Candidates), len(unknownCands))
	}
}

func TestProviderTemplateMiMoRoutes(t *testing.T) {
	tmpl, ok := GetProviderTemplate("mimo")
	if !ok {
		t.Fatal("未找到 mimo 模板")
	}
	routes := tmpl.AutoAddRoutes()
	if len(routes) != 4 {
		t.Fatalf("mimo 应创建 4 条 route，实际 %d: %+v", len(routes), routes)
	}

	want := map[string]struct {
		serviceType string
		baseSuffix  string
	}{
		"messages":  {serviceType: "claude", baseSuffix: "/anthropic"},
		"chat":      {serviceType: "openai", baseSuffix: "/v1"},
		"responses": {serviceType: "openai", baseSuffix: "/v1"},
		"gemini":    {serviceType: "openai", baseSuffix: "/v1"},
	}
	for _, route := range routes {
		expect, ok := want[route.ChannelKind]
		if !ok {
			t.Fatalf("未知 route kind: %+v", route)
		}
		if route.ServiceType != expect.serviceType {
			t.Fatalf("route %s serviceType=%s, want %s", route.ChannelKind, route.ServiceType, expect.serviceType)
		}
		candidates := tmpl.CandidatesForRouteKey(route, "tp-test")
		if len(candidates) == 0 {
			t.Fatalf("route %s 没有候选", route.ChannelKind)
		}
		if !strings.HasSuffix(candidates[0].BaseURL, expect.baseSuffix) {
			t.Fatalf("route %s 首选 baseURL=%s, want suffix %s", route.ChannelKind, candidates[0].BaseURL, expect.baseSuffix)
		}
	}
	if !tmpl.SupportsChannelKind("messages") || !tmpl.SupportsChannelKind("chat") || !tmpl.SupportsChannelKind("responses") || !tmpl.SupportsChannelKind("gemini") {
		t.Fatalf("mimo 应支持 messages/chat/responses/gemini routes: %+v", routes)
	}
}

func TestCandidatesForKeyNoPrefixRules(t *testing.T) {
	// deepseek 无前缀规则，任意 key 返回全部候选
	tmpl, ok := GetProviderTemplate("deepseek")
	if !ok {
		t.Fatal("未找到 deepseek 模板")
	}
	cands := tmpl.CandidatesForKey("anything")
	if len(cands) != len(tmpl.Candidates) {
		t.Errorf("无前缀规则应返回全部候选 %d，实际 %d", len(tmpl.Candidates), len(cands))
	}
}

func TestProviderTemplateDeepSeekRoutes(t *testing.T) {
	tmpl, ok := GetProviderTemplate("deepseek")
	if !ok {
		t.Fatal("未找到 deepseek 模板")
	}
	routes := tmpl.AutoAddRoutes()
	if len(routes) != 3 {
		t.Fatalf("deepseek 应创建 messages/chat/responses 三条 route，实际 %d: %+v", len(routes), routes)
	}
	want := map[string]struct {
		serviceType     string
		baseURL         string
		supportedModels []string
	}{
		"messages": {serviceType: "claude", baseURL: "https://api.deepseek.com/anthropic"},
		"chat":     {serviceType: "openai", baseURL: "https://api.deepseek.com"},
		// Responses 走官方原生入口，且仅 deepseek-v4-flash 可用
		"responses": {
			serviceType:     "responses",
			baseURL:         "https://api.deepseek.com/v1/responses",
			supportedModels: []string{"deepseek-v4-flash"},
		},
	}
	for _, route := range routes {
		expect, found := want[route.ChannelKind]
		if !found || route.ServiceType != expect.serviceType || len(route.Candidates) != 1 || route.Candidates[0].BaseURL != expect.baseURL {
			t.Fatalf("DeepSeek route 不符合预期: %+v", route)
		}
		if !slices.Equal(route.SupportedModels, expect.supportedModels) {
			t.Fatalf("DeepSeek route %s 模型白名单不符合预期: got %v, want %v",
				route.ChannelKind, route.SupportedModels, expect.supportedModels)
		}
	}
}

func TestProviderTemplateGLMRoutes(t *testing.T) {
	tmpl, ok := GetProviderTemplate("glm")
	if !ok {
		t.Fatal("未找到 GLM 模板")
	}
	routes := tmpl.AutoAddRoutes()
	if len(routes) != 3 {
		t.Fatalf("GLM 应创建 messages/chat/responses 三条 route，实际 %d: %+v", len(routes), routes)
	}
	want := map[string]struct {
		serviceType string
		baseURL     string
		candidates  int
	}{
		"messages":  {serviceType: "claude", baseURL: "https://open.bigmodel.cn/api/anthropic", candidates: 1},
		"chat":      {serviceType: "openai", baseURL: "https://open.bigmodel.cn/api/paas/v4#", candidates: 2},
		"responses": {serviceType: "openai", baseURL: "https://open.bigmodel.cn/api/paas/v4#", candidates: 2},
	}
	for _, route := range routes {
		expect, found := want[route.ChannelKind]
		if !found || route.ServiceType != expect.serviceType || len(route.Candidates) != expect.candidates || route.Candidates[0].BaseURL != expect.baseURL {
			t.Fatalf("GLM route 不符合预期: %+v", route)
		}
		if route.Candidates[0].PlanTag != "payg" {
			t.Fatalf("GLM route 应标记为 payg: %+v", route.Candidates[0])
		}
		if route.ChannelKind != "messages" && (route.Candidates[1].PlanTag != "coding_plan" || !strings.Contains(route.Candidates[1].BaseURL, "/api/coding/")) {
			t.Fatalf("GLM %s route 应包含 Coding Plan 候选: %+v", route.ChannelKind, route.Candidates)
		}
	}
}

func TestInferProviderIDFromBaseURL(t *testing.T) {
	tests := []struct {
		baseURL string
		want    string
		ok      bool
	}{
		{baseURL: "https://api.deepseek.com", want: "deepseek", ok: true},
		{baseURL: "https://api.deepseek.com/anthropic/v1", want: "deepseek", ok: true},
		{baseURL: "https://ark.cn-beijing.volces.com/api/plan/v3", want: "volcengine", ok: true},
		{baseURL: "https://open.bigmodel.cn/api/anthropic", want: "glm", ok: true},
		{baseURL: "https://open.bigmodel.cn/api/anthropic/v1/messages", want: "glm", ok: true},
		{baseURL: "https://open.bigmodel.cn/api/paas/v4/", want: "glm", ok: true},
		{baseURL: "https://open.bigmodel.cn/api/paas/v4/chat/completions", want: "glm", ok: true},
		{baseURL: "https://open.bigmodel.cn/api/coding/paas/v4/chat/completions", want: "glm", ok: true},
		{baseURL: "https://opencode.ai/zen/go/v1/chat/completions", want: "opencode-zen", ok: true},
		{baseURL: "https://opencode.ai/zen/v1", want: "opencode-zen", ok: true},
		{baseURL: "https://opencode.ai.evil.example/zen/v1", ok: false},
		{baseURL: "https://open.bigmodel.cn/api/other", ok: false},
		{baseURL: "https://open.bigmodel.cn.evil.example/api/paas/v4", ok: false},
		{baseURL: "https://relay.example/v1", ok: false},
		{baseURL: "https://api.deepseek.com.evil.example", ok: false},
	}
	for _, tt := range tests {
		got, ok := InferProviderIDFromBaseURL(tt.baseURL)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("InferProviderIDFromBaseURL(%q) = %q, %v; want %q, %v", tt.baseURL, got, ok, tt.want, tt.ok)
		}
	}
}

func TestInferProviderIDFromAPIKey(t *testing.T) {
	tests := []struct {
		name   string
		apiKey string
		want   string
		ok     bool
	}{
		{name: "zhipu id.secret", apiKey: "0123456789abcdef0123456789abcdef.ABCDEFGHIJKLMNO1", want: "glm", ok: true},
		{name: "trim spaces", apiKey: " 269abc123456789012345678.r8abcdef1234 ", want: "glm", ok: true},
		{name: "shared sk prefix", apiKey: "sk-abcdefghijklmnopqrstuvwxyz123456", ok: false},
		{name: "short dotted token", apiKey: "abc.def", ok: false},
		{name: "jwt", apiKey: "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := InferProviderIDFromAPIKey(tt.apiKey)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("InferProviderIDFromAPIKey() = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestProviderTemplateVolcenginePlanRoutes(t *testing.T) {
	tmpl, ok := GetProviderTemplate("volcengine")
	if !ok {
		t.Fatal("未找到火山方舟模板")
	}
	if tmpl.DisplayName != "火山方舟 Agent/Coding Plan" || len(tmpl.AutoAddRoutes()) != 4 {
		t.Fatalf("火山模板不完整: %+v", tmpl)
	}
	wantBases := map[string][2]string{
		"messages":  {"https://ark.cn-beijing.volces.com/api/plan", "https://ark.cn-beijing.volces.com/api/coding"},
		"chat":      {"https://ark.cn-beijing.volces.com/api/plan/v3", "https://ark.cn-beijing.volces.com/api/coding/v3"},
		"responses": {"https://ark.cn-beijing.volces.com/api/plan/v3", "https://ark.cn-beijing.volces.com/api/coding/v3"},
		"gemini":    {"https://ark.cn-beijing.volces.com/api/plan/v3", "https://ark.cn-beijing.volces.com/api/coding/v3"},
	}
	// 官方 /v3 入口原生支持 Responses API（Codex wire_api=responses），responses 路由应原生接入；
	// gemini 无对应上游协议，维持 Chat Completions 转换。
	wantServiceTypes := map[string]string{
		"messages":  "claude",
		"chat":      "openai",
		"responses": "responses",
		"gemini":    "openai",
	}
	for _, route := range tmpl.AutoAddRoutes() {
		candidates := tmpl.CandidatesForRouteKey(route, "ark-test")
		if len(candidates) != 2 {
			t.Fatalf("route %s 应包含 Agent/Coding 两个候选: %+v", route.ChannelKind, candidates)
		}
		want, exists := wantBases[route.ChannelKind]
		if !exists || candidates[0].BaseURL != want[0] || candidates[1].BaseURL != want[1] {
			t.Fatalf("route %s Base URL 错误: got=%q/%q want=%q/%q", route.ChannelKind,
				candidates[0].BaseURL, candidates[1].BaseURL, want[0], want[1])
		}
		if st, ok := wantServiceTypes[route.ChannelKind]; !ok || route.ServiceType != st {
			t.Fatalf("route %s ServiceType = %q, 期望 %q", route.ChannelKind, route.ServiceType, st)
		}
	}
}

func TestListAndGetProviderTemplate(t *testing.T) {
	all := ListProviderTemplates()
	if len(all) < 16 {
		t.Errorf("内置 provider 模板应至少为 16 个，实际 %d", len(all))
	}
	for _, id := range []string{
		"mimo", "deepseek", "kimi", "glm", "volcengine", "compshare", "sensenova", "minimax",
		"dashscope", "opencode-zen", "tencent-lkeap", "qianfan", "xfyun", "openrouter", "modelscope", "originrouter",
	} {
		if _, ok := GetProviderTemplate(id); !ok {
			t.Errorf("缺少 provider 模板: %s", id)
		}
	}
	for alias, canonical := range map[string]string{
		" opencode-go ": "opencode-zen",
		"VOLC-ARK":      "volcengine",
		"kimi-code":     "kimi",
	} {
		tmpl, ok := GetProviderTemplate(alias)
		if !ok || tmpl.ProviderID != canonical {
			t.Errorf("provider 别名 %q 应解析为 %q，实际 %+v", alias, canonical, tmpl)
		}
	}
	for _, id := range []string{"runapi", "unity2"} {
		if _, ok := GetProviderTemplate(id); ok {
			t.Errorf("中转站 %s 不应进入自动识别模板", id)
		}
	}
	if _, ok := GetProviderTemplate("nonexistent"); ok {
		t.Error("不存在的 providerId 应返回 false")
	}
}

func TestProviderTemplateCompshareModelCostMultipliers(t *testing.T) {
	tmpl, ok := GetProviderTemplate("compshare")
	if !ok {
		t.Fatal("未找到优云智算模板")
	}

	want := map[string]float64{
		"glm-5.2":                   2,
		"glm-5.1":                   6,
		"minimax-m2.7":              2,
		"kimi-k2.6":                 5,
		"deepseek-ai/deepseek-v3.2": 1,
		"deepseek-v4-flash":         1,
	}
	for modelID, expected := range want {
		got, found := tmpl.ModelCostMultiplierForModel(strings.ToUpper(modelID))
		if !found || got != expected {
			t.Errorf("模型 %s 的优云倍率 = %v, %v；期望 %v", modelID, got, found, expected)
		}
	}
	if _, found := tmpl.ModelCostMultiplierForModel("unknown-model"); found {
		t.Error("未知优云模型不应返回成本倍率")
	}

	glmPriority, glmFound := tmpl.ModelQualityPriorityForModel("GLM-5.1")
	kimiPriority, kimiFound := tmpl.ModelQualityPriorityForModel("KIMI-K2.6")
	if !glmFound || !kimiFound || glmPriority <= kimiPriority {
		t.Fatalf("优云同档质量优先级错误: glm=%d/%v kimi=%d/%v", glmPriority, glmFound, kimiPriority, kimiFound)
	}
	if _, found := tmpl.ModelQualityPriorityForModel("unknown-model"); found {
		t.Error("未知优云模型不应返回质量优先级")
	}
}

func TestProviderTemplateModelCostMultiplierPrefersSpecificPattern(t *testing.T) {
	tmpl := ProviderTemplate{ModelCostMultipliers: map[string]float64{
		"deepseek-*":    3,
		"deepseek-v4-*": 1,
	}}
	got, found := tmpl.ModelCostMultiplierForModel("DeepSeek-V4-Flash")
	if !found || got != 1 {
		t.Fatalf("最长通配符倍率 = %v, %v；期望 1", got, found)
	}
}

func TestEveryProviderCandidateCanBeInferred(t *testing.T) {
	for _, tmpl := range ListProviderTemplates() {
		for _, route := range tmpl.AutoAddRoutes() {
			if len(route.Candidates) == 0 {
				t.Errorf("provider %s route %s 没有候选端点", tmpl.ProviderID, route.ChannelKind)
				continue
			}
			for _, candidate := range route.Candidates {
				baseURL := strings.TrimSuffix(candidate.BaseURL, "#")
				got, ok := InferProviderIDFromBaseURL(baseURL)
				if !ok || got != tmpl.ProviderID {
					t.Errorf("候选端点 %q 反向识别为 %q, %v；期望 %q", candidate.BaseURL, got, ok, tmpl.ProviderID)
				}
			}
		}
	}
}

func TestProviderTemplateOpenCodeRoutes(t *testing.T) {
	tmpl, ok := GetProviderTemplate("opencode-go")
	if !ok {
		t.Fatal("未找到 OpenCode 模板")
	}
	if tmpl.ProviderID != "opencode-zen" || len(tmpl.AutoAddRoutes()) != 3 {
		t.Fatalf("OpenCode 模板不完整: %+v", tmpl)
	}
	for _, route := range tmpl.AutoAddRoutes() {
		if route.ServiceType != "openai" || len(route.Candidates) != 2 {
			t.Fatalf("OpenCode %s route 应通过 OpenAI Chat 上游探测 Zen/Go 两个入口: %+v", route.ChannelKind, route)
		}
		if route.Candidates[0].BaseURL != "https://opencode.ai/zen/go/v1" || route.Candidates[1].BaseURL != "https://opencode.ai/zen/v1" {
			t.Fatalf("OpenCode 候选顺序错误: %+v", route.Candidates)
		}
	}
}

func TestProviderTemplateXFyunNativeResponsesRoute(t *testing.T) {
	tmpl, ok := GetProviderTemplate("xfyun")
	if !ok {
		t.Fatal("未找到讯飞模板")
	}
	for _, route := range tmpl.AutoAddRoutes() {
		if route.ChannelKind != "responses" {
			continue
		}
		if route.ServiceType != "responses" || len(route.Candidates) != 1 || !strings.HasSuffix(route.Candidates[0].BaseURL, "/v1/responses") {
			t.Fatalf("讯飞 Responses route 不符合预期: %+v", route)
		}
		return
	}
	t.Fatal("讯飞模板缺少原生 Responses route")
}

func TestProviderTemplatesDoNotExposeChannelPresetRefs(t *testing.T) {
	data, err := json.Marshal(ListProviderTemplates())
	if err != nil {
		t.Fatalf("序列化 provider templates 失败: %v", err)
	}
	for _, field := range [][]byte{[]byte("presetRef"), []byte("presetCollection")} {
		if bytes.Contains(data, field) {
			t.Fatalf("provider template 不应暴露 channel preset 字段 %q: %s", field, data)
		}
	}
}
