package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestChannelMultiplierUpdateAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cm, err := NewConfigManager(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()
	ch := UpstreamConfig{Name: "prices", BaseURL: "https://relay.example", APIKeys: []string{"key-a", "key-b"}}
	if err := cm.AddResponsesUpstream(ch); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		payload string
		want    float64
	}{{`{"groupMultiplier":2.5}`, 2.5}, {`{"maxGroupMultiplier":0}`, 2.5}, {`{"groupMultiplier":0}`, 0}, {`{"description":"unchanged price"}`, 0}, {`{"groupMultiplier":null}`, 1}} {
		var update UpstreamUpdate
		if err := json.Unmarshal([]byte(tc.payload), &update); err != nil {
			t.Fatal(err)
		}
		if _, err := cm.UpdateResponsesUpstream(0, update); err != nil {
			t.Fatal(err)
		}
		got := cm.GetConfig().ResponsesUpstream[0]
		if got.BillingMultiplier() != tc.want {
			t.Fatalf("%s: got multiplier %v, want %v", tc.payload, got.BillingMultiplier(), tc.want)
		}
		if got.GroupMultiplier != nil {
			*got.GroupMultiplier = 99
			if cm.GetConfig().ResponsesUpstream[0].BillingMultiplier() != tc.want {
				t.Fatal("配置快照未深拷贝")
			}
		}
		if _, err := cm.GetNextAPIKey(&got, map[string]bool{"key-a": true}, "Responses"); err != nil {
			t.Fatalf("费用倍率不应阻止换 Key: %v", err)
		}
		if err := cm.loadConfig(); err != nil {
			t.Fatal(err)
		}
		reloaded := cm.GetConfig().ResponsesUpstream[0]
		if reloaded.BillingMultiplier() != tc.want {
			t.Fatalf("%s: 重载倍率为 %v，期望 %v", tc.payload, reloaded.BillingMultiplier(), tc.want)
		}
	}
	bad, newName := -1.0, "invalid"
	if _, err := cm.UpdateResponsesUpstream(0, UpstreamUpdate{Name: &newName, GroupMultiplier: OptionalMultiplier{Present: true, Value: &bad}}); err == nil ||
		cm.GetConfig().ResponsesUpstream[0].Name != ch.Name {
		t.Fatal("非法更新应在修改前拒绝")
	}
}

func TestLegacyChannelMultiplierMigrationPreservesOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	original := []byte(`{"upstream":[
		{"name":"consistent","apiKeys":["a","b"],"maxGroupMultiplier":0,"apiKeyConfigs":[{"key":"a","groupMultiplier":0},{"key":"b","groupMultiplier":0}],"disabledApiKeys":[{"key":"c","config":{"key":"c","groupMultiplier":0}}]},
		{"name":"mixed","apiKeys":["d","e"],"apiKeyConfigs":[{"key":"d","groupMultiplier":0.4}]},
		{"name":"explicit","apiKeys":["f"],"groupMultiplier":2,"apiKeyConfigs":[{"key":"f","groupMultiplier":0.4}]}
	]}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "backups")
	cm, err := NewConfigManager(path, backup)
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()
	for i, want := range []float64{0, 1, 2} {
		channel := cm.GetConfig().Upstream[i]
		if got := channel.BillingMultiplier(); got != want {
			t.Fatalf("%s: got %v, want %v", channel.Name, got, want)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("maxGroupMultiplier")) || bytes.Count(data, []byte(`"groupMultiplier"`)) != 2 {
		t.Fatalf("旧倍率字段仍存在: %s", data)
	}
	entries, err := os.ReadDir(backup)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		saved, err := os.ReadFile(filepath.Join(backup, entry.Name()))
		if err == nil && bytes.Equal(saved, original) {
			return
		}
	}
	t.Fatal("找不到原始配置的备份")
}
