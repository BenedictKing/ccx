package healthcheck

import (
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/utils"
)

func waitForCondition(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("条件未在 %s 内满足: %s", timeout, desc)
}

func TestManager启动首扫从未验证渠道立即到期(t *testing.T) {
	srv := newModelsServer(t, 200, `{"data":[{"id":"m1"},{"id":"m2"}]}`)
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "ch0", ChannelUID: "ch_uid_0", BaseURL: srv.URL, APIKeys: []string{"sk-key-1"}, Status: "active"},
			{Name: "ch1", BaseURL: srv.URL, APIKeys: []string{"sk-key-2"}, Status: "suspended"}, // 非 active 跳过
		},
		ChatUpstream: []config.UpstreamConfig{
			{Name: "chat0", BaseURL: srv.URL, APIKeys: []string{"sk-key-3"}, HealthCheck: &config.ChannelHealthCheckConfig{Enabled: boolPtr(false)}}, // 策略禁用跳过
		},
	}
	store := newFakeKeyHealthStore()
	m := NewManager(
		func() config.Config { return cfg },
		store, nil, nil,
		Options{ScanInterval: time.Hour}, // 长间隔：只验证启动首扫行为
	)
	m.RegisterL1Fetcher("messages", testWrappedFetcher())
	m.RegisterL1Fetcher("chat", testWrappedFetcher())
	m.Start()
	defer m.Stop()

	waitForCondition(t, 2*time.Second, "ch0 的 L1 记录写入", func() bool {
		recs, _ := store.GetKeyHealthForChannel("messages", "ch_uid_0")
		return len(recs) == 1
	})

	recs, _ := store.GetKeyHealthForChannel("messages", "ch_uid_0")
	rec := recs[0]
	if rec.LastStatus != StatusOK || rec.ModelCount != 2 || rec.CheckKind != CheckKindL1 {
		t.Fatalf("记录内容错误: %+v", rec)
	}
	if rec.KeyMask != utils.MaskAPIKey("sk-key-1") {
		t.Fatalf("KeyMask = %q", rec.KeyMask)
	}

	// 等待一轮后确认 suspended 与策略禁用渠道没有记录
	time.Sleep(200 * time.Millisecond)
	if n := store.count(); n != 1 {
		t.Fatalf("总记录数 = %d, 期望 1（suspended/策略禁用渠道应跳过）", n)
	}
}

func TestManager最近已验证渠道首扫不重复验证(t *testing.T) {
	srv := newModelsServer(t, 200, `{"data":[{"id":"m1"}]}`)
	keyMask := utils.MaskAPIKey("sk-key-1")
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "ch0", ChannelUID: "ch_uid_0", BaseURL: srv.URL, APIKeys: []string{"sk-key-1"}, Status: "active"},
		},
	}
	store := newFakeKeyHealthStore()
	// 预置最近的 L1 记录：间隔默认 6h，首扫不应到期
	_ = store.UpsertKeyHealth(metrics.KeyHealthRecord{
		ChannelType: "messages", ChannelID: "ch_uid_0", KeyMask: keyMask,
		CheckKind: CheckKindL1, LastCheckAt: time.Now(), LastStatus: StatusOK,
	})

	m := NewManager(
		func() config.Config { return cfg },
		store, nil, nil,
		Options{ScanInterval: time.Hour},
	)
	m.RegisterL1Fetcher("messages", testWrappedFetcher())
	m.Start()
	defer m.Stop()

	// 首扫在启动时同步触发，等待一小段时间确认没有新验证（记录数不变）
	time.Sleep(300 * time.Millisecond)
	if n := store.count(); n != 1 {
		t.Fatalf("记录数 = %d, 期望 1（未到期不应重复验证）", n)
	}
}

// TestManager数组重排后不复用旧下标记录 验证 key_health 以 ChannelUID 隔离，
// 新渠道即使占用历史数组下标，也会在启动首扫立即验证。
func TestManager数组重排后不复用旧下标记录(t *testing.T) {
	srv := newModelsServer(t, 200, `{"data":[{"id":"m1"}]}`)
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name: "new-channel", ChannelUID: "ch_new", BaseURL: srv.URL,
			APIKeys: []string{"sk-same-key"}, Status: "active",
		}},
	}
	store := newFakeKeyHealthStore()
	// 模拟被删除的旧渠道占用 index=0，并留下未到期记录。
	_ = store.UpsertKeyHealth(metrics.KeyHealthRecord{
		ChannelType: "messages", ChannelID: "0", KeyMask: utils.MaskAPIKey("sk-same-key"),
		CheckKind: CheckKindL1, LastCheckAt: time.Now(), LastStatus: StatusOK,
	})

	m := NewManager(func() config.Config { return cfg }, store, nil, nil, Options{ScanInterval: time.Hour})
	m.RegisterL1Fetcher("messages", testWrappedFetcher())
	m.Start()
	defer m.Stop()

	waitForCondition(t, 2*time.Second, "新 ChannelUID 的 L1 记录写入", func() bool {
		recs, _ := store.GetKeyHealthForChannel("messages", "ch_new")
		return len(recs) == 1
	})
}

func TestManagerStop幂等(t *testing.T) {
	cfg := config.Config{}
	m := NewManager(
		func() config.Config { return cfg },
		newFakeKeyHealthStore(), nil, nil,
		Options{ScanInterval: time.Hour},
	)
	// 未启动时 Stop 直接返回
	m.Stop()

	m.Start()
	m.Stop()
	// 重复 Stop 不应 panic 或阻塞
	m.Stop()

	// Stop 后提交任务被拒绝
	if m.TriggerChannelCheck("messages", 0) {
		t.Fatal("Stop 后不应接受新任务")
	}
}

func TestManagerTriggerChannelCheck(t *testing.T) {
	srv := newModelsServer(t, 200, `{"data":[{"id":"m1"}]}`)
	keyMask := utils.MaskAPIKey("sk-key-1")
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "ch0", ChannelUID: "ch_uid_0", BaseURL: srv.URL, APIKeys: []string{"sk-key-1"}, Status: "active"},
		},
	}
	store := newFakeKeyHealthStore()
	// 预置最近的 L1 记录：首扫未到期不提交，手动触发才执行验证
	_ = store.UpsertKeyHealth(metrics.KeyHealthRecord{
		ChannelType: "messages", ChannelID: "ch_uid_0", KeyMask: keyMask,
		CheckKind: CheckKindL1, LastCheckAt: time.Now(), LastStatus: StatusOK,
	})
	m := NewManager(
		func() config.Config { return cfg },
		store, nil, nil,
		Options{ScanInterval: time.Hour},
	)
	m.RegisterL1Fetcher("messages", testWrappedFetcher())

	// 不存在的渠道拒绝触发
	if m.TriggerChannelCheck("messages", 5) {
		t.Fatal("不存在的渠道索引应返回 false")
	}
	if m.TriggerChannelCheck("unknown", 0) {
		t.Fatal("未知渠道类型应返回 false")
	}

	m.Start()
	defer m.Stop()

	if !m.TriggerChannelCheck("messages", 0) {
		t.Fatal("存在的渠道应接受触发")
	}
	// 手动触发跳过到期判定，记录会被刷新（ModelCount 0 → 1）
	waitForCondition(t, 2*time.Second, "触发后 L1 记录刷新", func() bool {
		recs, _ := store.GetKeyHealthForChannel("messages", "ch_uid_0")
		return len(recs) == 1 && recs[0].ModelCount == 1
	})
}

func TestManager重复触发去重(t *testing.T) {
	// 慢速上游：第一个任务执行期间重复触发应被去重
	srv := newModelsServer(t, 200, `{"data":[{"id":"m1"}]}`)
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "ch0", ChannelUID: "ch_uid_0", BaseURL: srv.URL, APIKeys: []string{"sk-key-1"}, Status: "active"},
		},
	}
	store := newFakeKeyHealthStore()
	m := NewManager(
		func() config.Config { return cfg },
		store, nil, nil,
		Options{ScanInterval: time.Hour},
	)
	m.RegisterL1Fetcher("messages", testWrappedFetcher())

	// 手动占位 inFlight，模拟任务执行中
	m.mu.Lock()
	m.inFlight["messages/0"] = struct{}{}
	m.running = true
	m.stopCh = make(chan struct{})
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.running = false
		close(m.stopCh)
		m.mu.Unlock()
	}()

	if m.TriggerChannelCheck("messages", 0) {
		t.Fatal("执行中的渠道重复触发应返回 false")
	}
}
