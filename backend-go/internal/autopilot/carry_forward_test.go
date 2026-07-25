package autopilot

import (
	"github.com/BenedictKing/ccx/internal/errutil"
	"path/filepath"
	"testing"
	"time"
)

func TestCarryForwardProbeFields_NilOldIsNoop(t *testing.T) {
	current := newTestProfile("ep-1", "ch-1", "messages", "https://example.com")
	current.ProbeSuccess = false

	carryForwardProbeFields(nil, current)

	if current.ProbeSuccess {
		t.Error("old 为 nil 时不应修改 current")
	}
}

func TestCarryForwardProbeFields_NilCurrentIsNoop(t *testing.T) {
	old := newTestProfile("ep-1", "ch-1", "messages", "https://example.com")
	// 不应 panic
	carryForwardProbeFields(old, nil)
}

func TestCarryForwardProbeFields_CopiesProbeFields(t *testing.T) {
	lastProbe := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)

	old := newTestProfile("ep-1", "ch-1", "messages", "https://example.com")
	old.LastProbeAt = &lastProbe
	old.ProbeSuccess = true
	old.ProbeLatencyMs = 123
	old.ProbeConfidence = 0.8
	old.ProbeStatusCode = 200
	old.ConsecutiveProbeSuccess = 3

	// current 模拟 DeriveEndpointProfile 每轮新构造的零值 struct，
	// 但 L1 诊断字段（HealthState/HealthConfidence）已经是本轮真实计算结果。
	current := newTestProfile("ep-1", "ch-1", "messages", "https://example.com")
	current.HealthState = HealthStateDegraded
	current.HealthConfidence = 0.5

	carryForwardProbeFields(old, current)

	if current.LastProbeAt == nil || !current.LastProbeAt.Equal(lastProbe) {
		t.Errorf("LastProbeAt 未搬运: got %v, want %v", current.LastProbeAt, lastProbe)
	}
	if !current.ProbeSuccess {
		t.Error("ProbeSuccess 未搬运")
	}
	if current.ProbeLatencyMs != 123 {
		t.Errorf("ProbeLatencyMs: got %d, want 123", current.ProbeLatencyMs)
	}
	if current.ProbeConfidence != 0.8 {
		t.Errorf("ProbeConfidence: got %f, want 0.8", current.ProbeConfidence)
	}
	if current.ProbeStatusCode != 200 {
		t.Errorf("ProbeStatusCode: got %d, want 200", current.ProbeStatusCode)
	}
	if current.ConsecutiveProbeSuccess != 3 {
		t.Errorf("ConsecutiveProbeSuccess: got %d, want 3", current.ConsecutiveProbeSuccess)
	}

	// L1 诊断字段不应被 carry-forward 覆盖，保持本轮真实计算结果
	if current.HealthState != HealthStateDegraded {
		t.Errorf("HealthState 不应被覆盖: got %s, want degraded", current.HealthState)
	}
	if current.HealthConfidence != 0.5 {
		t.Errorf("HealthConfidence 不应被覆盖: got %f, want 0.5", current.HealthConfidence)
	}
}

func TestCarryForwardDiscoveryFields_CopiesDiscoveryFields(t *testing.T) {
	old := newTestProfile("ep-1", "ch-1", "messages", "https://example.com")
	old.AccountUID = "acct-1"
	old.CredentialUID = "cred-1"
	old.AvailableModels = []string{"mimo-v2-flash", "mimo-v2-pro"}
	old.ModelListHash = "models-hash"
	old.ModelMapping = map[string]string{"mimo-v2": "mimo-v2-pro"}
	old.MiniMaxTokenPlanUsage = &MiniMaxTokenPlanUsage{
		Models: []MiniMaxTokenPlanModelUsage{{ModelName: "MiniMax-M3", CurrentIntervalRemainingPercent: 80}},
	}

	current := newTestProfile("ep-1", "ch-1", "messages", "https://example.com")
	current.HealthState = HealthStateDegraded
	current.HealthConfidence = 0.5

	carryForwardDiscoveryFields(old, current)

	if current.AccountUID != "acct-1" || current.CredentialUID != "cred-1" {
		t.Fatalf("账号绑定字段未搬运: account=%q credential=%q", current.AccountUID, current.CredentialUID)
	}
	if len(current.AvailableModels) != 2 || current.AvailableModels[1] != "mimo-v2-pro" {
		t.Fatalf("AvailableModels 未搬运: %v", current.AvailableModels)
	}
	if current.ModelListHash != "models-hash" {
		t.Fatalf("ModelListHash 未搬运: %q", current.ModelListHash)
	}
	if current.ModelMapping["mimo-v2"] != "mimo-v2-pro" {
		t.Fatalf("ModelMapping 未搬运: %v", current.ModelMapping)
	}
	if current.MiniMaxTokenPlanUsage == nil || current.MiniMaxTokenPlanUsage.Models[0].ModelName != "MiniMax-M3" {
		t.Fatalf("MiniMax Token Plan 用量未搬运: %+v", current.MiniMaxTokenPlanUsage)
	}
	if current.HealthState != HealthStateDegraded || current.HealthConfidence != 0.5 {
		t.Fatalf("L1 健康字段不应被覆盖: state=%s confidence=%f", current.HealthState, current.HealthConfidence)
	}

	current.AvailableModels[0] = "changed"
	current.ModelMapping["mimo-v2"] = "changed"
	current.MiniMaxTokenPlanUsage.Models[0].ModelName = "changed"
	if old.AvailableModels[0] != "mimo-v2-flash" || old.ModelMapping["mimo-v2"] != "mimo-v2-pro" || old.MiniMaxTokenPlanUsage.Models[0].ModelName != "MiniMax-M3" {
		t.Fatal("自动发现字段必须深拷贝，不能修改旧画像")
	}
}

func TestCarryForwardDiscoveryFields_NilInputsAreNoop(t *testing.T) {
	current := newTestProfile("ep-1", "ch-1", "messages", "https://example.com")
	carryForwardDiscoveryFields(nil, current)
	carryForwardDiscoveryFields(current, nil)
}

func TestCarryForwardFirstByteStats(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	recentAt := now.Add(-30 * time.Minute)
	staleAt := now.Add(-adaptiveFirstByteStatsMaxAge - time.Minute)

	newProfileWithStats := func(updatedAt time.Time) *KeyEndpointProfile {
		profile := newTestProfile("ep-ttfb", "ch-1", "messages", "https://example.com")
		profile.FirstByteSampleCount = 42
		profile.P95FirstByteLatencyMs = 2_300
		profile.FirstByteStatsUpdatedAt = &updatedAt
		return profile
	}

	t.Run("最近画像被保留且时间戳不续期", func(t *testing.T) {
		old := newProfileWithStats(recentAt)
		current := newTestProfile("ep-ttfb", "ch-1", "messages", "https://example.com")

		carryForwardFirstByteStats(old, current, now)

		if current.FirstByteSampleCount != 42 || current.P95FirstByteLatencyMs != 2_300 {
			t.Fatalf("TTFB 画像未保留: samples=%d p95=%d", current.FirstByteSampleCount, current.P95FirstByteLatencyMs)
		}
		if current.FirstByteStatsUpdatedAt == nil || !current.FirstByteStatsUpdatedAt.Equal(recentAt) {
			t.Fatalf("时间戳不应续期: got=%v want=%v", current.FirstByteStatsUpdatedAt, recentAt)
		}
		if current.FirstByteStatsUpdatedAt == old.FirstByteStatsUpdatedAt {
			t.Fatal("时间戳指针应复制，不能与旧画像共享")
		}
	})

	t.Run("超过一小时的画像被丢弃", func(t *testing.T) {
		current := newTestProfile("ep-ttfb", "ch-1", "messages", "https://example.com")

		carryForwardFirstByteStats(newProfileWithStats(staleAt), current, now)

		if current.FirstByteSampleCount != 0 || current.P95FirstByteLatencyMs != 0 || current.FirstByteStatsUpdatedAt != nil {
			t.Fatalf("陈旧 TTFB 画像不应保留: %+v", current)
		}
	})

	t.Run("本轮新样本优先", func(t *testing.T) {
		currentAt := now.Add(-time.Minute)
		current := newTestProfile("ep-ttfb", "ch-1", "messages", "https://example.com")
		current.FirstByteSampleCount = 3
		current.P95FirstByteLatencyMs = 900
		current.FirstByteStatsUpdatedAt = &currentAt

		carryForwardFirstByteStats(newProfileWithStats(recentAt), current, now)

		if current.FirstByteSampleCount != 3 || current.P95FirstByteLatencyMs != 900 {
			t.Fatalf("本轮 TTFB 画像被旧值覆盖: samples=%d p95=%d", current.FirstByteSampleCount, current.P95FirstByteLatencyMs)
		}
		if current.FirstByteStatsUpdatedAt == nil || !current.FirstByteStatsUpdatedAt.Equal(currentAt) {
			t.Fatalf("本轮时间戳被旧值覆盖: got=%v want=%v", current.FirstByteStatsUpdatedAt, currentAt)
		}
	})
}

func TestCarryForwardConnectStats(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	updatedAt := now.Add(-20 * time.Minute)
	old := newTestProfile("ep-connect", "ch-1", "messages", "https://example.com")
	old.ConnectSampleCount = 18
	old.P95ConnectLatencyMs = 2_200
	old.ConnectStatsUpdatedAt = &updatedAt
	current := newTestProfile("ep-connect", "ch-1", "messages", "https://example.com")

	carryForwardConnectStats(old, current, now)

	if current.ConnectSampleCount != 18 || current.P95ConnectLatencyMs != 2_200 {
		t.Fatalf("连接延迟画像未保留: samples=%d p95=%d", current.ConnectSampleCount, current.P95ConnectLatencyMs)
	}
	if current.ConnectStatsUpdatedAt == nil || !current.ConnectStatsUpdatedAt.Equal(updatedAt) ||
		current.ConnectStatsUpdatedAt == old.ConnectStatsUpdatedAt {
		t.Fatalf("连接延迟时间戳复制错误: old=%p current=%p value=%v",
			old.ConnectStatsUpdatedAt, current.ConnectStatsUpdatedAt, current.ConnectStatsUpdatedAt)
	}
}

func TestCarryForwardFirstByteStats_SurvivesProfileStoreRestart(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	updatedAt := now.Add(-20 * time.Minute)
	dbPath := filepath.Join(t.TempDir(), "profiles.db")

	store1, err := NewProfileStore(dbPath)
	if err != nil {
		t.Fatalf("创建首个画像存储失败: %v", err)
	}
	old := newTestProfile("ep-restart", "ch-1", "messages", "https://example.com")
	old.FirstByteSampleCount = 25
	old.P95FirstByteLatencyMs = 1_800
	old.FirstByteStatsUpdatedAt = &updatedAt
	if err := store1.Upsert(old); err != nil {
		t.Fatalf("写入 TTFB 画像失败: %v", err)
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("关闭首个画像存储失败: %v", err)
	}

	store2, err := NewProfileStore(dbPath)
	if err != nil {
		t.Fatalf("重启后打开画像存储失败: %v", err)
	}
	defer errutil.IgnoreDeferred(store2.Close)
	current := newTestProfile("ep-restart", "ch-1", "messages", "https://example.com")
	carryForwardFirstByteStats(store2.Get("ep-restart"), current, now)

	if current.FirstByteSampleCount != 25 || current.P95FirstByteLatencyMs != 1_800 {
		t.Fatalf("重启后 TTFB 画像未恢复: samples=%d p95=%d", current.FirstByteSampleCount, current.P95FirstByteLatencyMs)
	}
	if current.FirstByteStatsUpdatedAt == nil || !current.FirstByteStatsUpdatedAt.Equal(updatedAt) {
		t.Fatalf("重启后 TTFB 时间戳未恢复: got=%v want=%v", current.FirstByteStatsUpdatedAt, updatedAt)
	}
}

// TestCarryForwardProbeFields_SurvivesL1OverwriteBug 回归测试：
// 模拟 collectAll 每轮调用 DeriveEndpointProfile 构造全新 struct 后 Upsert 的场景。
// 若不 carry-forward，Probe* 字段会在 L1 循环中被无声清零，破坏 scanAndEnqueue 的冷却期判定。
func TestCarryForwardProbeFields_SurvivesL1OverwriteBug(t *testing.T) {
	store := newTestProfileStore(t)

	lastProbe := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	probed := newTestProfile("ep-1", "ch-1", "messages", "https://example.com")
	probed.LastProbeAt = &lastProbe
	probed.ConsecutiveProbeSuccess = 1
	if err := store.Upsert(probed); err != nil {
		t.Fatalf("写入探测后画像失败: %v", err)
	}

	// 模拟下一轮 L1：DeriveEndpointProfile 构造的全新 struct，Probe* 字段零值
	freshFromL1 := newTestProfile("ep-1", "ch-1", "messages", "https://example.com")
	freshFromL1.HealthState = HealthStateHealthy // 本轮真实流量信号

	carryForwardProbeFields(store.Get("ep-1"), freshFromL1)
	if err := store.Upsert(freshFromL1); err != nil {
		t.Fatalf("写入本轮画像失败: %v", err)
	}

	result := store.Get("ep-1")
	if result.LastProbeAt == nil || !result.LastProbeAt.Equal(lastProbe) {
		t.Errorf("carry-forward 后 LastProbeAt 应保留: got %v", result.LastProbeAt)
	}
	if result.ConsecutiveProbeSuccess != 1 {
		t.Errorf("carry-forward 后 ConsecutiveProbeSuccess 应保留: got %d, want 1", result.ConsecutiveProbeSuccess)
	}
	if result.HealthState != HealthStateHealthy {
		t.Errorf("HealthState 应使用本轮 L1 计算结果: got %s, want healthy", result.HealthState)
	}
}
