package config

import (
	"testing"
)

// TestAddUpstream_AutoDerivedName 验证非托管渠道新增时名称由首个 baseURL 派生，忽略客户端传入的名称。
func TestAddUpstream_AutoDerivedName(t *testing.T) {
	cm := newTestConfigManager(t, `{"upstream":[]}`)

	if err := cm.AddUpstream(UpstreamConfig{
		Name:    "my-custom-name",
		BaseURL: "https://api.openai.com/v1",
		APIKeys: []string{"k1"},
	}); err != nil {
		t.Fatalf("AddUpstream 失败: %v", err)
	}

	cfg := cm.GetConfig()
	if len(cfg.Upstream) != 1 {
		t.Fatalf("渠道数 = %d, want 1", len(cfg.Upstream))
	}
	if got := cfg.Upstream[0].Name; got != "openai" {
		t.Errorf("Name = %q, want %q（应忽略客户端名称，按 baseURL 派生）", got, "openai")
	}
}

// TestAddUpstream_AutoDerivedNameDedup 验证同站多渠道复用同一派生名（同站合一），
// 仅当同名渠道指向不同 baseURL 时才追加序号消歧。
func TestAddUpstream_AutoDerivedNameDedup(t *testing.T) {
	cm := newTestConfigManager(t, `{"upstream":[{"name":"openai","baseUrl":"https://api.openai.com/v1","apiKeys":["k0"],"serviceType":"claude"}]}`)

	// 同站（baseURL canonical 相同）：复用派生名 openai，不加序号
	if err := cm.AddUpstream(UpstreamConfig{BaseURL: "https://api.openai.com/v1", APIKeys: []string{"k1"}}); err != nil {
		t.Fatalf("AddUpstream 同站失败: %v", err)
	}
	// 不同站：与 openai 同名冲突，追加序号
	if err := cm.AddUpstream(UpstreamConfig{BaseURL: "https://openai.example.com/v1", APIKeys: []string{"k2"}}); err != nil {
		t.Fatalf("AddUpstream 异站失败: %v", err)
	}

	cfg := cm.GetConfig()
	if len(cfg.Upstream) != 3 {
		t.Fatalf("渠道数 = %d, want 3", len(cfg.Upstream))
	}
	nameCount := map[string]int{}
	for _, ch := range cfg.Upstream {
		nameCount[ch.Name]++
	}
	// 同站两条（api.openai.com）复用派生名 openai
	if nameCount["openai"] != 2 {
		t.Errorf("同站应复用派生名 openai, got nameCount=%v", nameCount)
	}
	// openai.example.com 派生名为 openai-example（剥公共后缀，与 openai 不同名，无需消歧）
	if nameCount["openai-example"] != 1 {
		t.Errorf("异站派生名应为 openai-example, got nameCount=%v", nameCount)
	}
}

// TestUpdateUpstream_NameFollowsFirstBaseURL 验证调整 baseURL 顺序后名称跟随新的首地址变化，
// 且忽略手工传入的名称。
func TestUpdateUpstream_NameFollowsFirstBaseURL(t *testing.T) {
	cm := newTestConfigManager(t, `{"upstream":[{"name":"alpha","baseUrl":"https://api.alpha.com","baseUrls":["https://api.alpha.com","https://api.beta.com"],"apiKeys":["k1"],"serviceType":"claude"}]}`)

	// 调整 baseURL 顺序：beta 提到首位，同时尝试手工改名（应被忽略）
	name := "should-be-ignored"
	baseURLs := []string{"https://api.beta.com", "https://api.alpha.com"}
	if _, err := cm.UpdateUpstream(0, UpstreamUpdate{Name: &name, BaseURLs: baseURLs}); err != nil {
		t.Fatalf("UpdateUpstream 失败: %v", err)
	}

	cfg := cm.GetConfig()
	if got := cfg.Upstream[0].Name; got != "beta" {
		t.Errorf("Name = %q, want %q（应跟随首个 baseURL，忽略手工改名）", got, "beta")
	}
}

// TestUpdateUpstream_ManagedChannelNameFollowsBaseURL 验证托管渠道同样按首个 baseURL 派生名称。
func TestUpdateUpstream_ManagedChannelNameFollowsBaseURL(t *testing.T) {
	cm := newTestConfigManager(t, `{"upstream":[{"name":"mimo-claude","accountUid":"acct-1","providerId":"mimo","autoManaged":true,"channelUid":"ch-1","baseUrl":"https://api.mimo.com","apiKeys":["k1"],"serviceType":"claude"}]}`)

	baseURL := "https://api.renamed-host.com"
	if _, err := cm.UpdateUpstream(0, UpstreamUpdate{BaseURL: &baseURL}); err != nil {
		t.Fatalf("UpdateUpstream 失败: %v", err)
	}

	cfg := cm.GetConfig()
	if got := cfg.Upstream[0].Name; got != "renamed-host" {
		t.Errorf("托管渠道 Name = %q, want %q（应按首个 baseURL 派生）", got, "renamed-host")
	}
}

// TestUniqueAutoDerivedChannelName 验证序号消歧逻辑跳过已占用名称。
// 同站（首 baseURL canonical 相同）允许复用；仅当同名渠道指向不同 baseURL 时才加序号。
func TestUniqueAutoDerivedChannelName(t *testing.T) {
	// 不同 baseURL 同名：追加 -2
	channels := []UpstreamConfig{
		{Name: "openai", BaseURL: "https://api.openai.com/v1"},
		{Name: "openai", BaseURL: "https://openai.example.com/v1"},
	}
	got := uniqueAutoDerivedChannelName(channels, nil, "openai", "https://api.openai.com/v1", "claude")
	if got != "openai-2" {
		t.Errorf("异站同名应追加 -2, got %q", got)
	}
	// 同站（canonical 相同）：复用 openai
	channels = []UpstreamConfig{
		{Name: "openai", BaseURL: "https://api.openai.com/v1"},
	}
	got = uniqueAutoDerivedChannelName(channels, nil, "openai", "https://api.openai.com/v1", "claude")
	if got != "openai" {
		t.Errorf("同站应复用派生名, got %q", got)
	}
}
