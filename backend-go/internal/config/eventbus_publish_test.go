package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/BenedictKing/ccx/internal/eventbus"
)

// newTestCMWithBus 创建带 EventBus 注入的 ConfigManager。
func newTestCMWithBus(t *testing.T, bus *eventbus.Bus) (*ConfigManager, *eventbus.Bus, func()) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cm := &ConfigManager{
		configFile: cfgPath,
	}
	cm.config = Config{
		Upstream: []UpstreamConfig{{
			ChannelUID:  "ch-1",
			Name:        "test",
			BaseURL:     "https://example.com",
			ServiceType: "claude",
			APIKeys:     []string{"sk-test-key-1234"},
			Status:      "active",
			Priority:    1,
		}},
		UpstreamModelCapabilities: map[string]UpstreamModelCapability{},
	}
	if bus != nil {
		cm.SetEventBus(bus)
	}
	data, _ := json.MarshalIndent(cm.config, "", "  ")
	_ = os.WriteFile(cfgPath, data, 0644)
	return cm, bus, func() { _ = cm.Close() }
}

// TestConfig_PublishKeyBlacklisted 验证 B.1：BlacklistKeyWithRecoverAt 发布 key_blacklisted 事件。
func TestConfig_PublishKeyBlacklisted(t *testing.T) {
	bus := eventbus.NewBus()
	ch, unsub := bus.Subscribe(eventbus.TypeKeyBlacklisted)
	defer unsub()

	cm, _, cleanup := newTestCMWithBus(t, bus)
	defer cleanup()

	if err := cm.BlacklistKeyWithRecoverAt("Messages", 0, "sk-test-key-1234", "auth_failed", "test", ""); err != nil {
		t.Fatalf("BlacklistKey 失败: %v", err)
	}

	ev := <-ch
	if ev.Subject != "ch-1" {
		t.Errorf("Subject=%s，期望 ch-1", ev.Subject)
	}
	if ev.Cause != "auth_failed" {
		t.Errorf("Cause=%s，期望 auth_failed", ev.Cause)
	}
	if ev.Payload["keyMask"] == nil {
		t.Errorf("Payload 应包含 keyMask")
	}
}

// TestConfig_PublishKeyRestored 验证：RestoreKey 发布 key_restored 事件。
func TestConfig_PublishKeyRestored(t *testing.T) {
	bus := eventbus.NewBus()
	ch, unsub := bus.Subscribe(eventbus.TypeKeyRestored)
	defer unsub()

	cm, _, cleanup := newTestCMWithBus(t, bus)
	defer cleanup()

	// 先拉黑
	if err := cm.BlacklistKeyWithRecoverAt("Messages", 0, "sk-test-key-1234", "auth_failed", "test", ""); err != nil {
		t.Fatalf("BlacklistKey 失败: %v", err)
	}
	// 再恢复
	if err := cm.RestoreKey("Messages", 0, "sk-test-key-1234"); err != nil {
		t.Fatalf("RestoreKey 失败: %v", err)
	}

	ev := <-ch
	if ev.Subject != "ch-1" {
		t.Errorf("Subject=%s，期望 ch-1", ev.Subject)
	}
}

// TestConfig_PublishKeyModelDisabled 验证：DisableKeyModel 发布 key_model_disabled 事件。
func TestConfig_PublishKeyModelDisabled(t *testing.T) {
	bus := eventbus.NewBus()
	ch, unsub := bus.Subscribe(eventbus.TypeKeyModelDisabled)
	defer unsub()

	cm, _, cleanup := newTestCMWithBus(t, bus)
	defer cleanup()

	if err := cm.DisableKeyModel("Messages", 0, "sk-test-key-1234", "claude-sonnet", "context_exceeded", "test"); err != nil {
		t.Fatalf("DisableKeyModel 失败: %v", err)
	}

	ev := <-ch
	if ev.Payload["model"] != "claude-sonnet" {
		t.Errorf("Payload.model=%v，期望 claude-sonnet", ev.Payload["model"])
	}
}

// TestConfig_NilBus_NoPanic 验证：bus 未注入时 Key 变更不 panic。
func TestConfig_NilBus_NoPanic(t *testing.T) {
	cm, _, cleanup := newTestCMWithBus(t, nil)
	defer cleanup()

	if err := cm.BlacklistKeyWithRecoverAt("Messages", 0, "sk-test-key-1234", "auth_failed", "test", ""); err != nil {
		t.Fatalf("BlacklistKey 失败: %v", err)
	}
	if err := cm.RestoreKey("Messages", 0, "sk-test-key-1234"); err != nil {
		t.Fatalf("RestoreKey 失败: %v", err)
	}
	if err := cm.DisableKeyModel("Messages", 0, "sk-test-key-1234", "claude-sonnet", "ctx", "test"); err != nil {
		t.Fatalf("DisableKeyModel 失败: %v", err)
	}
}
