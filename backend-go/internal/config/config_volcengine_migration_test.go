package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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

// TestMigrateVolcengineSurvivesAuthoritativeLoad 回归：加载期迁移（serviceType openai→responses）
// 不得被随后的 ChannelsV3 加载翻转撤销。旧落盘（磁盘六数组与 ChannelsV3 同为迁移前 openai）
// 经 NewConfigManager 加载后：迁移改写内存磁盘侧并触发 save（落盘迁移后形态），
// 翻转必须以同代 V3 重建——运行时 serviceType 应保持 responses。
// 曾有的 bug：saveConfigLocked 只把重建 V3 落盘而不同步内存，翻转用迁移前陈旧 V3
// 覆盖磁盘侧，迁移成果在首次启动被撤销（下次重启才自愈）。
func TestMigrateVolcengineSurvivesAuthoritativeLoad(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	// 手动置入迁移前形态渠道（绕过 loadConfig 迁移，模拟 826a5b4b 之前的存量配置）。
	cm.config.ResponsesUpstream = []UpstreamConfig{{
		ChannelUID: "ch_volc", Name: "volc-codex", ProviderID: "volcengine",
		ServiceType: "openai", BaseURL: "https://ark.cn-beijing.volces.com/api/coding/v3",
		Status: "active", APIKeys: []string{"sk-v"}, SupportedModels: []string{"m1"},
	}}
	if err := cm.SaveConfig(); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	cm.CloseWatcher()

	// 确认落盘为迁移前形态（波 3 后落盘只含 ChannelsV3，其协议成员应为 openai）。
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("读取落盘配置失败: %v", err)
	}
	var persisted Config
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("解析落盘配置失败: %v", err)
	}
	if len(persisted.ChannelsV3) == 0 || len(persisted.ChannelsV3[0].Protocols) == 0 ||
		persisted.ChannelsV3[0].Protocols[0].Upstream.ServiceType != "openai" {
		t.Fatalf("前置条件:落盘应为迁移前 openai 形态,V3 组数=%d", len(persisted.ChannelsV3))
	}

	cm2, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() reload error = %v", err)
	}
	defer cm2.CloseWatcher()
	got := cm2.GetConfig()
	if len(got.ResponsesUpstream) != 1 {
		t.Fatalf("重载后渠道数应为 1，实际 %d", len(got.ResponsesUpstream))
	}
	if got.ResponsesUpstream[0].ServiceType != "responses" {
		t.Fatalf("加载期迁移不得被 ChannelsV3 翻转撤销: serviceType=%q, 期望 responses",
			got.ResponsesUpstream[0].ServiceType)
	}
}
