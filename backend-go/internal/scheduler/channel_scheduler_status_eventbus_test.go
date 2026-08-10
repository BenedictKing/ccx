package scheduler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/eventbus"
)

// newTestSchedulerWithBus 创建带事件总线注入的最小 ChannelScheduler。
func newTestSchedulerWithBus(t *testing.T, bus *eventbus.Bus) (*ChannelScheduler, func()) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cm, err := config.NewConfigManager(cfgPath, "")
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	cm.SetEventBus(bus)

	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			ChannelUID:  "ch-msg-1",
			Name:        "msg-channel",
			BaseURL:     "https://example.com",
			ServiceType: "claude",
			APIKeys:     []string{"sk-test"},
			Status:      "suspended",
		}},
		UpstreamModelCapabilities: map[string]config.UpstreamModelCapability{},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	_ = os.WriteFile(cfgPath, data, 0644)
	// NewConfigManager 构造函数会读取磁盘文件，覆盖内存默认值。
	cm2, err := config.NewConfigManager(cfgPath, "")
	if err != nil {
		t.Fatalf("new config manager from disk: %v", err)
	}
	_ = cm.Close()
	cm2.SetEventBus(bus)

	sch := NewChannelScheduler(cm2, nil, nil, nil, nil, nil, nil, nil)
	sch.SetEventBus(bus)
	return sch, func() { _ = cm2.Close() }
}

// TestSetChannelStatusByKind_PublishesStatusEvent 验证 scheduler 设置渠道状态时会发布 channel_status_changed。
func TestSetChannelStatusByKind_PublishesStatusEvent(t *testing.T) {
	bus := eventbus.NewBus()
	ch, unsub := bus.Subscribe(eventbus.TypeChannelStatusChanged)
	defer unsub()

	sch, cleanup := newTestSchedulerWithBus(t, bus)
	defer cleanup()

	if err := sch.setChannelStatusByKind(0, ChannelKindMessages, "active", "manual_resume"); err != nil {
		t.Fatalf("setChannelStatusByKind 失败: %v", err)
	}

	select {
	case ev := <-ch:
		if ev.Subject != "ch-msg-1" {
			t.Errorf("Subject=%s，期望 ch-msg-1", ev.Subject)
		}
		if ev.From != "suspended" || ev.To != "active" {
			t.Errorf("from/to=%s/%s，期望 suspended/active", ev.From, ev.To)
		}
		if ev.Cause != "manual_resume" {
			t.Errorf("Cause=%s，期望 manual_resume", ev.Cause)
		}
		p := ev.Payload
		if p["channelUID"] != "ch-msg-1" || p["channelName"] != "msg-channel" || p["kind"] != "messages" {
			t.Errorf("payload 字段不匹配: %v", p)
		}
		if p["oldStatus"] != "suspended" || p["newStatus"] != "active" || p["reason"] != "manual_resume" {
			t.Errorf("payload 状态字段不匹配: %v", p)
		}
		if _, ok := p["timestamp"]; !ok {
			t.Errorf("payload 缺少 timestamp")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("未收到 channel_status_changed 事件")
	}
}

// TestSetChannelStatusByKind_SameStatus_NoEvent 验证状态未变化时不发布事件。
func TestSetChannelStatusByKind_SameStatus_NoEvent(t *testing.T) {
	bus := eventbus.NewBus()
	ch, unsub := bus.Subscribe(eventbus.TypeChannelStatusChanged)
	defer unsub()

	sch, cleanup := newTestSchedulerWithBus(t, bus)
	defer cleanup()

	// 当前状态已经是 suspended，再设置一次 suspended
	if err := sch.setChannelStatusByKind(0, ChannelKindMessages, "suspended", "manual"); err != nil {
		t.Fatalf("setChannelStatusByKind 失败: %v", err)
	}

	select {
	case <-ch:
		t.Fatal("状态未变化时不应发布事件")
	case <-time.After(50 * time.Millisecond):
	}
}

// TestSetChannelStatusByKind_NilBus_NoPanic 验证未注入总线时不 panic。
func TestSetChannelStatusByKind_NilBus_NoPanic(t *testing.T) {
	bus := eventbus.NewBus()
	sch, cleanup := newTestSchedulerWithBus(t, bus)
	defer cleanup()

	// 重新构造一个没有总线的 scheduler
	schNoBus := NewChannelScheduler(sch.configManager, nil, nil, nil, nil, nil, nil, nil)
	if err := schNoBus.setChannelStatusByKind(0, ChannelKindMessages, "active", "manual"); err != nil {
		t.Fatalf("setChannelStatusByKind 失败: %v", err)
	}
}

// TestSetChannelStatusByKind_EmptyStatusDefaultsToActive 验证空旧状态按 active 发布。
func TestSetChannelStatusByKind_EmptyStatusDefaultsToActive(t *testing.T) {
	bus := eventbus.NewBus()
	ch, unsub := bus.Subscribe(eventbus.TypeChannelStatusChanged)
	defer unsub()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cm, err := config.NewConfigManager(cfgPath, "")
	if err != nil {
		t.Fatalf("new config manager: %v", err)
	}
	cm.SetEventBus(bus)
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			ChannelUID:  "ch-msg-2",
			Name:        "msg-channel-empty",
			BaseURL:     "https://example.com",
			ServiceType: "claude",
			APIKeys:     []string{"sk-test"},
			Status:      "",
		}},
		UpstreamModelCapabilities: map[string]config.UpstreamModelCapability{},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	_ = os.WriteFile(cfgPath, data, 0644)
	// NewConfigManager 构造函数会读取磁盘文件，覆盖内存默认值。
	cm2, err := config.NewConfigManager(cfgPath, "")
	if err != nil {
		t.Fatalf("new config manager from disk: %v", err)
	}
	_ = cm.Close()
	cm2.SetEventBus(bus)
	defer func() { _ = cm2.Close() }()

	sch := NewChannelScheduler(cm2, nil, nil, nil, nil, nil, nil, nil)
	sch.SetEventBus(bus)

	if err := sch.setChannelStatusByKind(0, ChannelKindMessages, "disabled", "manual"); err != nil {
		t.Fatalf("setChannelStatusByKind 失败: %v", err)
	}

	select {
	case ev := <-ch:
		if ev.From != "active" || ev.To != "disabled" {
			t.Errorf("from/to=%s/%s，期望 active/disabled", ev.From, ev.To)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("未收到 channel_status_changed 事件")
	}
}
