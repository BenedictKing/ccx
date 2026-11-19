package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newSubagentRoutingTestManager(t *testing.T, cfg Config) *ConfigManager {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cm, err := NewConfigManager(path, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cm.Close() })
	return cm
}

func TestSubagentRoutingLegacyDraftAndAtomicSave(t *testing.T) {
	cm := newSubagentRoutingTestManager(t, Config{ResponsesUpstream: []UpstreamConfig{
		{Name: "a", ChannelUID: "uid-a", Status: "active", SubagentModel: " sol ", ModelMapping: map[string]string{"sol": "actual-a"}},
		{Name: "b", ChannelUID: "uid-b", Status: "disabled", SubagentModel: "luna"},
	}})
	view, err := cm.GetSubagentRouting("responses")
	if err != nil {
		t.Fatal(err)
	}
	if view.Activated || view.Policy.Model != "" || !view.LegacyMixed || len(view.Policy.Channels) != 2 {
		t.Fatalf("unexpected legacy draft: %+v", view)
	}
	if view.Policy.Channels[1].Enabled {
		t.Fatal("普通 disabled 渠道在转换草稿中应关闭")
	}

	saved, err := cm.UpdateSubagentRouting("responses", view.Revision, SubagentRoutingPolicy{
		Model:    " sol ",
		Channels: []SubagentRoutingChannel{{ChannelUID: "uid-b", Enabled: true}, {ChannelUID: "uid-a", Enabled: false}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Activated || saved.Policy.Model != "sol" || saved.Policy.Channels[0].ChannelUID != "uid-b" {
		t.Fatalf("unexpected saved policy: %+v", saved)
	}
	if saved.Channels[0].ResolvedModel != "sol" || saved.Channels[1].ResolvedModel != "actual-a" {
		t.Fatalf("模型预览未复用渠道映射: %+v", saved.Channels)
	}
	if _, err := cm.UpdateSubagentRouting("responses", view.Revision, saved.Policy); !errors.Is(err, ErrSubagentRoutingRevisionConflict) {
		t.Fatalf("过期 revision 应返回冲突，实际 %v", err)
	}
	model := "legacy"
	if _, err := cm.UpdateResponsesUpstream(0, UpstreamUpdate{SubagentModel: &model}); !errors.Is(err, ErrSubagentLegacyModelConflict) {
		t.Fatalf("激活后旧字段写入应冲突，实际 %v", err)
	}
}

func TestSubagentRoutingRejectsInvalidUIDAndCopiesSlices(t *testing.T) {
	cm := newSubagentRoutingTestManager(t, Config{Upstream: []UpstreamConfig{{Name: "a", ChannelUID: "uid-a", Status: "active"}}})
	view, err := cm.GetSubagentRouting("messages")
	if err != nil {
		t.Fatal(err)
	}
	_, err = cm.UpdateSubagentRouting("messages", view.Revision, SubagentRoutingPolicy{Channels: []SubagentRoutingChannel{{ChannelUID: "uid-other", Enabled: true}}})
	if !errors.Is(err, ErrInvalidSubagentRouting) {
		t.Fatalf("跨协议 UID 应被拒绝，实际 %v", err)
	}
	_, err = cm.UpdateSubagentRouting("messages", view.Revision, SubagentRoutingPolicy{Channels: []SubagentRoutingChannel{{ChannelUID: "uid-a"}, {ChannelUID: "uid-a"}}})
	if !errors.Is(err, ErrInvalidSubagentRouting) {
		t.Fatalf("重复 UID 应被拒绝，实际 %v", err)
	}
	policy := SubagentRoutingPolicy{Channels: []SubagentRoutingChannel{{ChannelUID: "uid-a", Enabled: true}}}
	if _, err := cm.UpdateSubagentRouting("messages", view.Revision, policy); err != nil {
		t.Fatal(err)
	}
	policy.Channels[0].Enabled = false
	if !cm.GetConfig().SubagentRouting["messages"].Channels[0].Enabled {
		t.Fatal("调用方切片不应修改已保存配置")
	}
}

func TestSubagentRoutingReloadAndDiskFailure(t *testing.T) {
	cm := newSubagentRoutingTestManager(t, Config{Upstream: []UpstreamConfig{{Name: "a", ChannelUID: "uid-a", Status: "active"}}})
	view, _ := cm.GetSubagentRouting("messages")
	_, err := cm.UpdateSubagentRouting("messages", view.Revision, SubagentRoutingPolicy{Model: "sol", Channels: view.Policy.Channels})
	if err != nil {
		t.Fatal(err)
	}
	path := cm.configFile
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw["subagentRouting"].(map[string]any)["messages"].(map[string]any)["model"] = "luna"
	data, _ = json.Marshal(raw)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := cm.loadConfig(); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := cm.GetSubagentRouting("messages")
	if reloaded.Policy.Model != "luna" {
		t.Fatalf("热重载后模型=%q", reloaded.Policy.Model)
	}

	cm.configFile = t.TempDir()
	_, err = cm.UpdateSubagentRouting("messages", reloaded.Revision, SubagentRoutingPolicy{Model: "opus", Channels: reloaded.Policy.Channels})
	if err == nil {
		t.Fatal("写盘失败应返回错误")
	}
	current, _ := cm.GetSubagentRouting("messages")
	if current.Policy.Model != "luna" || current.Revision != reloaded.Revision {
		t.Fatalf("写盘失败不应发布半更新: %+v", current)
	}
}

func TestSubagentRoutingTracksAddedAndRemovedChannels(t *testing.T) {
	cm := newSubagentRoutingTestManager(t, Config{Upstream: []UpstreamConfig{{Name: "a", ChannelUID: "uid-a", Status: "active"}}})
	view, _ := cm.GetSubagentRouting("messages")
	if _, err := cm.UpdateSubagentRouting("messages", view.Revision, view.Policy); err != nil {
		t.Fatal(err)
	}
	if err := cm.AddUpstream(UpstreamConfig{Name: "b", BaseURL: "https://b.example", APIKeys: []string{"key"}, ServiceType: "claude"}); err != nil {
		t.Fatal(err)
	}
	cfg := cm.GetConfig()
	added := cfg.SubagentRouting["messages"].Channels[1]
	if added.ChannelUID != cfg.Upstream[0].ChannelUID || added.Enabled {
		t.Fatalf("新增渠道应追加且默认关闭: %+v", added)
	}
	if _, err := cm.RemoveUpstream(0); err != nil {
		t.Fatal(err)
	}
	channels := cm.GetConfig().SubagentRouting["messages"].Channels
	if len(channels) != 1 || channels[0].ChannelUID != "uid-a" {
		t.Fatalf("删除渠道应清理 UID 引用: %+v", channels)
	}
}
