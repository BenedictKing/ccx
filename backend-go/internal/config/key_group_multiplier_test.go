package config

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestGroupMultiplierLimitUpdateAndRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cm, err := NewConfigManager(path, "")
	if err != nil {
		t.Fatal(err)
	}
	zero, paid := 0.0, 0.5
	ch := UpstreamConfig{Name: "prices", BaseURL: "https://relay.example", APIKeys: []string{"free", "paid", "unknown"}, APIKeyConfigs: []APIKeyConfig{{Key: "free", GroupMultiplier: &zero}, {Key: "paid", GroupMultiplier: &paid}}}
	if err := cm.AddResponsesUpstream(ch); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		payload       string
		paid, unknown bool
	}{{`{"maxGroupMultiplier":0}`, false, false}, {`{"maxGroupMultiplier":1}`, true, false}, {`{"maxGroupMultiplier":null}`, true, true}} {
		var update UpstreamUpdate
		if err := json.Unmarshal([]byte(tc.payload), &update); err != nil {
			t.Fatal(err)
		}
		if _, err := cm.UpdateResponsesUpstream(0, update); err != nil {
			t.Fatal(err)
		}
		got := cm.GetConfig().ResponsesUpstream[0]
		if !got.IsAPIKeyGroupMultiplierAllowed("free") || got.IsAPIKeyGroupMultiplierAllowed("paid") != tc.paid || got.IsAPIKeyGroupMultiplierAllowed("unknown") != tc.unknown {
			t.Fatalf("倍率语义错误: %s", tc.payload)
		}
	}
	if _, err := cm.UpdateResponsesUpstream(0, UpstreamUpdate{MaxGroupMultiplier: OptionalMultiplier{Present: true, Value: &zero}}); err != nil {
		t.Fatal(err)
	}
	copy := cm.GetConfig().ResponsesUpstream[0]
	*copy.MaxGroupMultiplier = 10
	*copy.APIKeyConfigs[0].GroupMultiplier = 10
	if *cm.GetConfig().ResponsesUpstream[0].MaxGroupMultiplier != 0 || *cm.GetConfig().ResponsesUpstream[0].APIKeyConfigs[0].GroupMultiplier != 0 {
		t.Fatal("配置快照未深拷贝")
	}
	if err := cm.Close(); err != nil {
		t.Fatal(err)
	}
	cm, err = NewConfigManager(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()
	got := cm.GetConfig().ResponsesUpstream[0]
	if got.MaxGroupMultiplier == nil || *got.MaxGroupMultiplier != 0 {
		t.Fatal("重启丢失零倍率上限")
	}
	bad := -1.0
	name := "must-not-change"
	if _, err := cm.UpdateResponsesUpstream(0, UpstreamUpdate{Name: &name, MaxGroupMultiplier: OptionalMultiplier{Present: true, Value: &bad}}); err == nil || cm.GetConfig().ResponsesUpstream[0].Name != ch.Name {
		t.Fatal("非法更新应在修改前拒绝")
	}
}
