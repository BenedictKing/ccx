package transitions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/eventbus"
	"github.com/BenedictKing/ccx/internal/metrics"
)

// TestRestoreDisabledKeysAndActivate_PublishesStatusEvent 验证恢复并激活渠道时发布 channel_status_changed。
func TestRestoreDisabledKeysAndActivate_PublishesStatusEvent(t *testing.T) {
	bus := eventbus.NewBus()
	ch, unsub := bus.Subscribe(eventbus.TypeChannelStatusChanged)
	defer unsub()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := config.Config{Upstream: []config.UpstreamConfig{{
		ChannelUID:  "ch-msg-1",
		Name:        "msg-channel",
		BaseURL:     "https://example.com",
		Status:      "suspended",
		APIKeys:     nil,
		ServiceType: "claude",
		DisabledAPIKeys: []config.DisabledKeyInfo{{
			Key: "sk-ready", Reason: "insufficient_balance", DisabledAt: time.Now().Add(-2 * time.Hour).Format(time.RFC3339), RecoverAt: time.Now().Add(-time.Minute).Format(time.RFC3339),
		}},
	}}}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfgManager, err := config.NewConfigManager(cfgPath, "")
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	defer cfgManager.Close()
	cfgManager.SetEventBus(bus)

	metricsManager := metrics.NewMetricsManager()
	defer metricsManager.Stop()

	activated := false
	result, err := RestoreDisabledKeysAndActivate(
		func(keys []string) ([]string, error) {
			return cfgManager.RestoreDisabledKeys("Messages", 0, keys)
		},
		func(_ string, apiKey string) {
			metricsManager.MoveKeyToHalfOpen("https://example.com/v1", apiKey, "claude")
		},
		func(status string) error {
			activated = true
			return cfgManager.SetChannelStatus(0, status)
		},
		func() bool {
			latest := cfgManager.GetConfig().Upstream[0]
			return latest.Status == "suspended"
		},
		[]string{"sk-ready"},
		PublishChannelStatusEvent(bus, "ch-msg-1", "msg-channel", "messages"),
	)
	if err != nil {
		t.Fatalf("RestoreDisabledKeysAndActivate() error = %v", err)
	}
	if !activated || !result.ActivatedChannel {
		t.Fatalf("ActivatedChannel = %v/%v, want true/true", activated, result.ActivatedChannel)
	}

	select {
	case ev := <-ch:
		if ev.Subject != "ch-msg-1" {
			t.Errorf("Subject=%s，期望 ch-msg-1", ev.Subject)
		}
		if ev.From != "suspended" || ev.To != "active" {
			t.Errorf("from/to=%s/%s，期望 suspended/active", ev.From, ev.To)
		}
		if ev.Cause != "scheduled_recovery" {
			t.Errorf("Cause=%s，期望 scheduled_recovery", ev.Cause)
		}
		p := ev.Payload
		if p["channelUID"] != "ch-msg-1" || p["channelName"] != "msg-channel" || p["kind"] != "messages" {
			t.Errorf("payload 字段不匹配: %v", p)
		}
		if p["oldStatus"] != "suspended" || p["newStatus"] != "active" || p["reason"] != "scheduled_recovery" {
			t.Errorf("payload 状态字段不匹配: %v", p)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("未收到 channel_status_changed 事件")
	}
}

// TestRestoreDisabledKeysAndActivate_NoPublishWhenNotActivated 验证未激活渠道时不发布状态事件。
func TestRestoreDisabledKeysAndActivate_NoPublishWhenNotActivated(t *testing.T) {
	bus := eventbus.NewBus()
	ch, unsub := bus.Subscribe(eventbus.TypeChannelStatusChanged)
	defer unsub()

	result, err := RestoreDisabledKeysAndActivate(
		func(keys []string) ([]string, error) { return keys, nil },
		func(_ string, _ string) {},
		func(string) error { return nil },
		func() bool { return false },
		[]string{"sk-ready"},
		PublishChannelStatusEvent(bus, "ch-msg-1", "msg-channel", "messages"),
	)
	if err != nil {
		t.Fatalf("RestoreDisabledKeysAndActivate() error = %v", err)
	}
	if result.ActivatedChannel {
		t.Fatal("ActivatedChannel = true, want false")
	}

	select {
	case <-ch:
		t.Fatal("未激活时不应发布 channel_status_changed")
	case <-time.After(50 * time.Millisecond):
	}
}

// TestRestoreDisabledKeysAndActivate_NilPublishCallback 验证 nil 回调不 panic。
func TestRestoreDisabledKeysAndActivate_NilPublishCallback(t *testing.T) {
	_, err := RestoreDisabledKeysAndActivate(
		func(keys []string) ([]string, error) { return keys, nil },
		func(_ string, _ string) {},
		func(string) error { return nil },
		func() bool { return true },
		[]string{"sk-ready"},
		nil,
	)
	if err != nil {
		t.Fatalf("RestoreDisabledKeysAndActivate() error = %v", err)
	}
}
