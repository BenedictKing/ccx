package ratelimit

import (
	"testing"
	"time"
)

func TestManager_GetOrCreate_New(t *testing.T) {
	m := NewManager()
	l := m.GetOrCreate("messages", 0, Config{RPM: 60})
	if l == nil {
		t.Fatal("expected non-nil limiter")
	}
	s := l.Status(time.Now())
	if s.MaxRequests != 60 {
		t.Fatalf("maxRequests = %v, want 60", s.MaxRequests)
	}
}

func TestManager_GetOrCreate_Existing(t *testing.T) {
	m := NewManager()
	l1 := m.GetOrCreate("messages", 0, Config{RPM: 60})
	l2 := m.GetOrCreate("messages", 0, Config{RPM: 120})
	if l1 != l2 {
		t.Fatal("expected same limiter instance for same key")
	}
	// Verify updated config
	s := l2.Status(time.Now())
	if s.MaxRequests != 120 {
		t.Fatalf("maxRequests = %v, want 120", s.MaxRequests)
	}
}

func TestManager_Get(t *testing.T) {
	m := NewManager()
	if m.Get("messages", 0) != nil {
		t.Fatal("expected nil for non-existent key")
	}
	m.GetOrCreate("messages", 0, Config{RPM: 60})
	if m.Get("messages", 0) == nil {
		t.Fatal("expected non-nil after create")
	}
}

func TestManager_SetCooldownCreatesLimiter(t *testing.T) {
	m := NewManager()
	now := time.Now()

	m.SetCooldown("Responses", 2, 30*time.Second, now)

	l := m.Get("Responses", 2)
	if l == nil {
		t.Fatal("expected limiter created for cooldown")
	}
	in, until := l.InCooldown(now)
	if !in {
		t.Fatal("expected cooldown")
	}
	if d := until.Sub(now); d != 30*time.Second {
		t.Fatalf("cooldown = %v, want 30s", d)
	}
}

func TestManager_SetCooldownKeepsExistingConfig(t *testing.T) {
	m := NewManager()
	now := time.Now()
	l := m.GetOrCreate("Responses", 2, Config{RPM: 120, MaxConcurrent: 4})

	m.SetCooldown("Responses", 2, 30*time.Second, now)

	if got := m.Get("Responses", 2); got != l {
		t.Fatal("expected existing limiter instance")
	}
	status := l.Status(now)
	if status.MaxRequests != 120 {
		t.Fatalf("maxRequests = %v, want 120", status.MaxRequests)
	}
	if status.MaxConcurrent != 4 {
		t.Fatalf("maxConcurrent = %v, want 4", status.MaxConcurrent)
	}
	if !status.InCooldown {
		t.Fatal("expected cooldown")
	}
}

func TestManager_Remove(t *testing.T) {
	m := NewManager()
	m.GetOrCreate("messages", 0, Config{RPM: 60})
	m.Remove("messages", 0)
	if m.Get("messages", 0) != nil {
		t.Fatal("expected nil after remove")
	}
}

func TestManager_UpdateAll(t *testing.T) {
	m := NewManager()
	m.GetOrCreate("messages", 0, Config{RPM: 60})
	m.GetOrCreate("chat", 1, Config{RPM: 30})

	m.UpdateAll(func(apiType string, channelIndex int) (Config, bool) {
		if apiType == "messages" {
			return Config{RPM: 120}, true
		}
		return Config{}, false
	})

	l0 := m.Get("messages", 0)
	if l0 == nil {
		t.Fatal("messages limiter disappeared")
	}
	if s := l0.Status(time.Now()); s.MaxRequests != 120 {
		t.Fatalf("messages maxRequests = %v, want 120", s.MaxRequests)
	}

	// chat unchanged
	l1 := m.Get("chat", 1)
	if l1 == nil {
		t.Fatal("chat limiter disappeared")
	}
	if s := l1.Status(time.Now()); s.MaxRequests != 30 {
		t.Fatalf("chat maxRequests = %v, want 30", s.MaxRequests)
	}
}

func TestManager_GetStatus(t *testing.T) {
	m := NewManager()
	m.GetOrCreate("messages", 0, Config{RPM: 60, MaxConcurrent: 5})
	m.GetOrCreate("chat", 1, Config{RPM: 30})

	statuses := m.GetStatus(time.Now())
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}
}

func TestManager_DifferentChannelTypes(t *testing.T) {
	m := NewManager()

	kinds := []struct {
		apiType      string
		channelIndex int
	}{
		{"messages", 0},
		{"chat", 0},
		{"responses", 0},
		{"gemini", 0},
		{"images", 0},
	}

	for _, k := range kinds {
		m.GetOrCreate(k.apiType, k.channelIndex, Config{RPM: 60})
	}

	for _, k := range kinds {
		if m.Get(k.apiType, k.channelIndex) == nil {
			t.Fatalf("missing limiter for %s[%d]", k.apiType, k.channelIndex)
		}
	}
}

func TestManager_MultipleChannelsSameType(t *testing.T) {
	m := NewManager()
	m.GetOrCreate("messages", 0, Config{RPM: 60})
	m.GetOrCreate("messages", 1, Config{RPM: 120})
	m.GetOrCreate("messages", 2, Config{RPM: 30})

	if m.Get("messages", 0) == m.Get("messages", 1) {
		t.Fatal("different indices should have different limiters")
	}
}

func TestParseKey(t *testing.T) {
	tests := []struct {
		key      string
		wantType string
		wantIdx  int
	}{
		{"messages:0", "messages", 0},
		{"chat:3", "chat", 3},
		{"responses:10", "responses", 10},
		{"unknown", "unknown", 0},
	}
	for _, tt := range tests {
		apiType, idx := parseKey(tt.key)
		if apiType != tt.wantType || idx != tt.wantIdx {
			t.Errorf("parseKey(%q) = (%q, %d), want (%q, %d)",
				tt.key, apiType, idx, tt.wantType, tt.wantIdx)
		}
	}
}

// TestManager_UpdateAllAppliesToScopedLimiters 验证 UpdateAll 也会更新 scoped limiter（key/quota 级），
// 而不是只更新 channel 级 limiter。
func TestManager_UpdateAllAppliesToScopedLimiters(t *testing.T) {
	m := NewManager()
	// 创建 channel 级
	m.GetOrCreate("messages", 0, Config{RPM: 10})
	// 创建 scoped 级
	m.GetOrCreateScoped("messages", 0, "key:abc", Config{RPM: 20})
	m.GetOrCreateScoped("messages", 0, "quota:groupA", Config{RPM: 30})

	// fetch 返回新的 RPM
	m.UpdateAll(func(apiType string, idx int) (Config, bool) {
		if apiType == "messages" && idx == 0 {
			return Config{RPM: 99}, true
		}
		return Config{}, false
	})

	now := time.Now()
	// 检查 channel 级
	if got := m.Get("messages", 0).Status(now).MaxRequests; got != 99 {
		t.Errorf("channel-level MaxRequests = %d, want 99", got)
	}
	// 检查 scoped 级
	if got := m.GetScoped("messages", 0, "key:abc").Status(now).MaxRequests; got != 99 {
		t.Errorf("scoped key MaxRequests = %d, want 99", got)
	}
	if got := m.GetScoped("messages", 0, "quota:groupA").Status(now).MaxRequests; got != 99 {
		t.Errorf("scoped quota MaxRequests = %d, want 99", got)
	}
}

// TestManager_RemoveCleansUpScopedLimiters 验证 Remove 同时清理同 channel 下所有 scoped limiter。
func TestManager_RemoveCleansUpScopedLimiters(t *testing.T) {
	m := NewManager()
	m.GetOrCreate("messages", 0, Config{RPM: 10})
	m.GetOrCreateScoped("messages", 0, "key:abc", Config{RPM: 20})
	m.GetOrCreateScoped("messages", 0, "quota:groupA", Config{RPM: 30})
	// 同 type 但不同 idx 应保留
	m.GetOrCreate("messages", 1, Config{RPM: 40})
	m.GetOrCreateScoped("messages", 1, "key:def", Config{RPM: 50})

	m.Remove("messages", 0)

	// channel 0 的 channel 级和 scoped 级应全部删除
	if m.Get("messages", 0) != nil {
		t.Error("channel-level limiter not removed")
	}
	if m.GetScoped("messages", 0, "key:abc") != nil {
		t.Error("scoped key limiter not removed")
	}
	if m.GetScoped("messages", 0, "quota:groupA") != nil {
		t.Error("scoped quota limiter not removed")
	}
	// channel 1 不应受影响
	if m.Get("messages", 1) == nil {
		t.Error("messages:1 channel-level limiter unexpectedly removed")
	}
	if m.GetScoped("messages", 1, "key:def") == nil {
		t.Error("messages:1 scoped limiter unexpectedly removed")
	}
}

func TestManager_ReconcileChannelConfigsRemovesChangedUIDState(t *testing.T) {
	m := NewManager()
	m.ReconcileChannelConfigs([]ChannelConfig{{
		APIType: "Messages", ChannelIndex: 0, ChannelUID: "channel-a", Config: Config{RPM: 60},
	}})
	oldChannel := m.Get("Messages", 0)
	oldScoped := m.GetOrCreateScoped("Messages", 0, "key:old", Config{})
	oldScoped.SetDiscoveredRPM(30)

	m.ReconcileChannelConfigs([]ChannelConfig{{
		APIType: "Messages", ChannelIndex: 0, ChannelUID: "channel-b", Config: Config{RPM: 90},
	}})

	newChannel := m.Get("Messages", 0)
	if newChannel == nil || newChannel == oldChannel {
		t.Fatal("ChannelUID 变化后应移除旧 limiter 并创建新 limiter")
	}
	if newChannel.GetRPM() != 90 {
		t.Fatalf("新渠道 RPM = %d, want 90", newChannel.GetRPM())
	}
	if m.GetScoped("Messages", 0, "key:old") != nil {
		t.Fatal("ChannelUID 变化后旧 scoped limiter 应被移除")
	}
}

func TestManager_ReconcileChannelConfigsRemovesDisappearedIndex(t *testing.T) {
	m := NewManager()
	m.ReconcileChannelConfigs([]ChannelConfig{
		{APIType: "Messages", ChannelIndex: 0, ChannelUID: "channel-a", Config: Config{}},
		{APIType: "Messages", ChannelIndex: 1, ChannelUID: "channel-b", Config: Config{}},
	})
	m.GetOrCreateScoped("Messages", 1, "key:removed", Config{}).SetDiscoveredRPM(30)

	m.ReconcileChannelConfigs([]ChannelConfig{{
		APIType: "Messages", ChannelIndex: 0, ChannelUID: "channel-a", Config: Config{},
	}})

	if m.Get("Messages", 1) != nil || m.GetScoped("Messages", 1, "key:removed") != nil {
		t.Fatal("索引消失后 channel/scoped limiter 应全部移除")
	}
}

// TestChannelLimiter_UpdateConfigSkipsWhenUnchanged 验证 UpdateConfig 在配置不变时跳过 applyConfig，
// 通过观察 sem 通道是否被重建判断。
func TestChannelLimiter_UpdateConfigSkipsWhenUnchanged(t *testing.T) {
	cfg := Config{RPM: 60, WindowSeconds: 60, MaxConcurrent: 5}
	l := NewChannelLimiter(cfg, time.Now())
	originalSem := l.sem

	// 相同配置 → 不应重建 sem
	l.UpdateConfig(cfg)
	if l.sem != originalSem {
		t.Error("sem channel rebuilt when config unchanged")
	}

	// 改变 MaxConcurrent → 应重建 sem
	newCfg := cfg
	newCfg.MaxConcurrent = 10
	l.UpdateConfig(newCfg)
	if l.sem == originalSem {
		t.Error("sem channel not rebuilt when MaxConcurrent changed")
	}
	if cap(l.sem) != 10 {
		t.Errorf("new sem cap = %d, want 10", cap(l.sem))
	}
}
