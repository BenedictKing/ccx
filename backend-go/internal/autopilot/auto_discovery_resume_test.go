package autopilot

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/utils"
	_ "modernc.org/sqlite"
)

// newRunnerWithTaskStore 构造带 taskStore 的 runner，复用 ProfileStore 的 SQLite 连接。
func newRunnerWithTaskStore(t *testing.T) (*AutoDiscoveryRunner, *ProfileStore, *DiscoveryTaskStore) {
	t.Helper()
	store, err := NewProfileStore(filepath.Join(t.TempDir(), "profiles.db"))
	if err != nil {
		t.Fatalf("创建 ProfileStore 失败: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	taskStore, err := NewDiscoveryTaskStoreWithDB(store.DB())
	if err != nil {
		t.Fatalf("创建 DiscoveryTaskStore 失败: %v", err)
	}
	runner := NewAutoDiscoveryRunner(store, nil)
	runner.SetTaskStore(taskStore)
	return runner, store, taskStore
}

func TestResumeIncompleteDiscoveries_DeletedChannelMarkedFailed(t *testing.T) {
	runner, _, taskStore := newRunnerWithTaskStore(t)
	cfgManager := setupTestConfigManagerForDiscovery(t, "ch-exists", nil, nil)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	// 两条 running 记录：一条渠道仍存在，一条已删除。
	_ = taskStore.Start("ch-exists", "acct", "messages")
	_ = taskStore.Start("ch-deleted", "acct", "chat")

	runner.ResumeIncompleteDiscoveries(cfgManager)

	// ch-deleted 应被标记 failed。
	running, err := taskStore.LoadRunning()
	if err != nil {
		t.Fatalf("LoadRunning 失败: %v", err)
	}
	for _, rt := range running {
		if rt.ChannelUID == "ch-deleted" {
			t.Fatalf("ch-deleted 不应仍为 running: %#v", rt)
		}
	}
}

func TestResumeIncompleteDiscoveries_DuplicateRunningSkipped(t *testing.T) {
	runner, _, taskStore := newRunnerWithTaskStore(t)
	cfgManager := setupTestConfigManagerForDiscovery(t, "ch-dup", nil, nil)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	_ = taskStore.Start("ch-dup", "acct", "messages")
	// 内存中已有 running 任务（模拟并发触发或上一次未清理）。
	runner.mu.Lock()
	runner.tasks["ch-dup"] = &DiscoveryTask{ChannelUID: "ch-dup", Status: DiscoveryStatusRunning}
	runner.mu.Unlock()

	// 不应 panic、不应再启动 goroutine（无新任务）。
	runner.ResumeIncompleteDiscoveries(cfgManager)
	runner.mu.Lock()
	task := runner.tasks["ch-dup"]
	runner.mu.Unlock()
	if task == nil {
		t.Fatal("内存 running 任务被覆盖")
	}
}

func TestEndpointStillExists_KeyRotationMakesEndpointRetry(t *testing.T) {
	runner, _, _ := newRunnerWithTaskStore(t)
	channel := &config.UpstreamConfig{
		ChannelUID:  "ch-rot",
		ServiceType: "openai",
		BaseURL:     "https://api.example.com",
		APIKeys:     []string{"sk-new"},
	}
	oldKey := "sk-old"
	canonical := utils.CanonicalBaseURL("https://api.example.com", "openai")
	oldUID := GenerateEndpointUID("ch-rot", canonical, KeyHashFromAPIKey(oldKey))

	cp := CheckpointedEndpoint{EndpointUID: oldUID, KeyHash: KeyHashFromAPIKey(oldKey), ProfilePersisted: true}
	// 当前 key 已轮换为 sk-new，旧 endpointUID 不再匹配 → 应重试（返回 false）。
	if runner.endpointStillExists(channel, cp, []string{"https://api.example.com"}, []string{"sk-new"}) {
		t.Fatal("key 轮换后不应认为端点仍存在")
	}
}

func TestTriggerDiscoveryWithStatus_DBWriteFailureReturnsError(t *testing.T) {
	store, err := NewProfileStore(filepath.Join(t.TempDir(), "profiles.db"))
	if err != nil {
		t.Fatalf("创建 ProfileStore 失败: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	taskStore, err := NewDiscoveryTaskStoreWithDB(store.DB())
	if err != nil {
		t.Fatalf("创建 DiscoveryTaskStore 失败: %v", err)
	}
	runner := NewAutoDiscoveryRunner(store, nil)
	runner.SetTaskStore(taskStore)

	// 关闭底层 DB，使 Start 写入失败 → 应返回 (false, err) 且不启动 goroutine。
	_ = store.DB().Close()

	started, err := runner.TriggerDiscoveryWithStatus("ch-fail", &config.UpstreamConfig{ChannelUID: "ch-fail", BaseURL: "https://example.com", APIKeys: []string{"sk-test"}}, nil)
	if err == nil {
		t.Fatal("DB 写入失败时应返回 error")
	}
	if started {
		t.Fatal("DB 写入失败时不应 started=true")
	}
	runner.mu.Lock()
	_, hasTask := runner.tasks["ch-fail"]
	runner.mu.Unlock()
	if hasTask {
		t.Fatal("DB 写入失败时不应在内存创建 running 任务")
	}
}

func TestCheckpointPath_PersistsProfileBeforeCheckpoint(t *testing.T) {
	runner, store, taskStore := newRunnerWithTaskStore(t)
	channelUID := "ch-ckpt"
	baseURL := "https://api.example.com"
	apiKey := "sk-test-key"
	channel := &config.UpstreamConfig{
		AccountUID:  "acct-ckpt",
		ChannelUID:  channelUID,
		ServiceType: "claude",
		BaseURL:     baseURL,
		APIKeys:     []string{apiKey},
		AutoManaged: true,
	}
	cfgManager := setupTestConfigManagerForDiscovery(t, channelUID, nil, nil)
	defer errutil.IgnoreDeferred(cfgManager.Close)

	// 直接驱动 checkpoint 路径：先 Start，再写一个成功端点的画像 + Flush + checkpoint。
	_ = taskStore.Start(channelUID, "acct-ckpt", "messages")
	endpoint := EndpointDiscoveryResult{
		KeyMask:     utils.MaskAPIKey(apiKey),
		BaseURL:     baseURL,
		Models:      []string{"m1"},
		ModelsCount: 1,
		ProtocolOk:  true,
	}
	idx, channelKind := findChannelIndexAndKind(cfgManager.GetConfig(), channelUID)
	uid, err := runner.writeProfileForEndpoint(channelUID, channel, endpoint, idx, channelKind, nil)
	if err != nil {
		t.Fatalf("writeProfileForEndpoint 失败: %v", err)
	}
	if err := runner.flushStores(); err != nil {
		t.Fatalf("flushStores 失败: %v", err)
	}
	keyHash := KeyHashFromAPIKey(apiKey)
	if err := taskStore.UpsertEndpointCheckpoint(channelUID, CheckpointedEndpoint{
		EndpointUID:      uid,
		KeyHash:          keyHash,
		BaseURL:          utils.CanonicalBaseURL(baseURL, "claude"),
		Models:           []string{"m1"},
		ModelsCount:      1,
		ProtocolOk:       true,
		ProfilePersisted: true,
	}); err != nil {
		t.Fatalf("UpsertEndpointCheckpoint 失败: %v", err)
	}

	// 画像必须已落盘（Flush 成功）。
	if profile := store.Get(uid); profile == nil {
		t.Fatalf("画像未落盘: %s", uid)
	}
	// checkpoint 已记录 profilePersisted=true。
	running, _ := taskStore.LoadRunning()
	var found bool
	for _, rt := range running {
		if rt.ChannelUID == channelUID {
			for _, ep := range rt.Endpoints {
				if ep.EndpointUID == uid && ep.ProfilePersisted {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatal("checkpoint 未记录 profilePersisted=true")
	}

	// 模拟重启：清空内存 task，running 记录（含 checkpoint）仍在 DB。
	// ResumeIncompleteDiscoveries 应加载 checkpoint 并跳过该已持久化端点，不重新探测 /models。
	runner.mu.Lock()
	runner.tasks = map[string]*DiscoveryTask{}
	runner.mu.Unlock()
	runner.ResumeIncompleteDiscoveries(cfgManager)
	// 给 goroutine 时间完成；端点被跳过 → 不会真实请求 api.example.com。
	time.Sleep(300 * time.Millisecond)

	if profile := store.Get(uid); profile == nil {
		t.Fatal("续传后画像丢失")
	}
	// 续传后 task 应进入终态（done），而非卡在 running。
	running, _ = taskStore.LoadRunning()
	if len(running) != 0 {
		t.Fatalf("续传完成后应无 running 记录，实际 %d 条", len(running))
	}
}
