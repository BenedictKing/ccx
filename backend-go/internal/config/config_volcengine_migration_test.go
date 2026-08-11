package config

import "testing"

// TestMigrateVolcengineResponsesServiceType 验证火山套餐存量 Responses 渠道
// 从 Chat Completions 转换（openai）翻转为原生 Responses API（responses），
// 且不影响其他 kind 渠道与非火山 openai 渠道，二次迁移幂等。
func TestMigrateVolcengineResponsesServiceType(t *testing.T) {
	cm := &ConfigManager{}
	cm.config.ResponsesUpstream = []UpstreamConfig{
		{Name: "volc-codex", ProviderID: "volcengine", ServiceType: "openai", BaseURL: "https://ark.cn-beijing.volces.com/api/plan/v3"},
		{Name: "volc-by-url", ServiceType: "openai", BaseURL: "https://ark.cn-beijing.volces.com/api/coding/v3"},
		{Name: "volc-by-key-url", ServiceType: "openai", BaseURL: "https://example.com/v1", APIKeyConfigs: []APIKeyConfig{{Key: "k", BaseURL: "https://ark.cn-beijing.volces.com/api/plan/v3"}}},
		{Name: "volc-already", ProviderID: "volcengine", ServiceType: "responses", BaseURL: "https://ark.cn-beijing.volces.com/api/plan/v3"},
		{Name: "other-openai", ServiceType: "openai", BaseURL: "https://api.deepseek.com/v1"},
	}
	cm.config.ChatUpstream = []UpstreamConfig{
		{Name: "volc-chat", ProviderID: "volcengine", ServiceType: "openai", BaseURL: "https://ark.cn-beijing.volces.com/api/plan/v3"},
	}

	if !cm.migrateVolcengineResponsesServiceType() {
		t.Fatal("期望迁移报告变更")
	}
	want := []string{"responses", "responses", "responses", "responses", "openai"}
	for i, w := range want {
		if got := cm.config.ResponsesUpstream[i].ServiceType; got != w {
			t.Fatalf("ResponsesUpstream[%d] serviceType = %q, 期望 %q", i, got, w)
		}
	}
	if got := cm.config.ChatUpstream[0].ServiceType; got != "openai" {
		t.Fatalf("Chat 渠道不应被翻转: %q", got)
	}
	if cm.migrateVolcengineResponsesServiceType() {
		t.Fatal("二次迁移应幂等")
	}
}

// TestIsVolcenginePlanChannel 命中判定：providerId 优先，其次任一 baseURL（含 Key 级）官方入口。
func TestIsVolcenginePlanChannel(t *testing.T) {
	tests := []struct {
		name string
		ch   UpstreamConfig
		want bool
	}{
		{"providerId 命中", UpstreamConfig{ProviderID: "volcengine"}, true},
		{"baseURL 命中 plan", UpstreamConfig{BaseURL: "https://ark.cn-beijing.volces.com/api/plan/v3"}, true},
		{"baseURLs 命中 coding", UpstreamConfig{BaseURLs: []string{"https://ark.cn-beijing.volces.com/api/coding/v3"}}, true},
		{"key 级 baseURL 命中", UpstreamConfig{APIKeyConfigs: []APIKeyConfig{{BaseURL: "https://ark.cn-beijing.volces.com/api/plan"}}}, true},
		{"hash 后缀命中", UpstreamConfig{BaseURL: "https://ark.cn-beijing.volces.com/api/plan#"}, true},
		{"中转站同 path 不命中", UpstreamConfig{BaseURL: "https://relay.example.com/api/plan/v3"}, false},
		{"常规 ark 入口不命中", UpstreamConfig{BaseURL: "https://ark.cn-beijing.volces.com/api/v3"}, false},
		{"空配置不命中", UpstreamConfig{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isVolcenginePlanChannel(&tt.ch); got != tt.want {
				t.Fatalf("isVolcenginePlanChannel() = %v, 期望 %v", got, tt.want)
			}
		})
	}
}
