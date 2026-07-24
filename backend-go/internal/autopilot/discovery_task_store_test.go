package autopilot

import (
	"database/sql"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/errutil"
	_ "modernc.org/sqlite"
)

func newTestDiscoveryTaskStore(t *testing.T) *DiscoveryTaskStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewDiscoveryTaskStoreWithDB(db)
	if err != nil {
		t.Fatalf("创建 DiscoveryTaskStore 失败: %v", err)
	}
	return store
}

func TestDiscoveryTaskStore_StartCheckpointFinishLoadRunning(t *testing.T) {
	store := newTestDiscoveryTaskStore(t)

	if err := store.Start("ch-1", "acct-1", "chat"); err != nil {
		t.Fatalf("Start 失败: %v", err)
	}
	running, err := store.LoadRunning()
	if err != nil {
		t.Fatalf("LoadRunning 失败: %v", err)
	}
	if len(running) != 1 || running[0].ChannelUID != "ch-1" || running[0].ChannelKind != "chat" {
		t.Fatalf("LoadRunning=%#v", running)
	}

	// 端点级 checkpoint：先 flush 成功再标记 profilePersisted。
	ep := CheckpointedEndpoint{
		EndpointUID:      "ep-a",
		KeyHash:          "kh-a",
		CredentialUID:    "cred_a",
		BaseURL:          "https://example.com",
		Models:           []string{"m1", "m2"},
		ModelsCount:      2,
		ProtocolOk:       true,
		ProfilePersisted: true,
	}
	if err := store.UpsertEndpointCheckpoint("ch-1", ep); err != nil {
		t.Fatalf("UpsertEndpointCheckpoint 失败: %v", err)
	}
	// 再 upsert 第二个端点，验证 payload 追加而非覆盖。
	ep2 := ep
	ep2.EndpointUID = "ep-b"
	ep2.KeyHash = "kh-b"
	if err := store.UpsertEndpointCheckpoint("ch-1", ep2); err != nil {
		t.Fatalf("第二次 UpsertEndpointCheckpoint 失败: %v", err)
	}
	// 更新 ep-a（幂等覆盖同 endpointUID）。
	ep.ModelsCount = 3
	if err := store.UpsertEndpointCheckpoint("ch-1", ep); err != nil {
		t.Fatalf("更新 UpsertEndpointCheckpoint 失败: %v", err)
	}

	running, err = store.LoadRunning()
	if err != nil {
		t.Fatalf("LoadRunning 失败: %v", err)
	}
	if len(running) != 1 || len(running[0].Endpoints) != 2 {
		t.Fatalf("endpoints=%#v", running)
	}
	for _, e := range running[0].Endpoints {
		if e.EndpointUID == "ep-a" && e.ModelsCount != 3 {
			t.Fatalf("ep-a 未按幂等覆盖: %#v", e)
		}
	}

	// Finish 后不再出现在 running 中。
	if err := store.Finish("ch-1", DiscoveryStatusDone, ""); err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	running, err = store.LoadRunning()
	if err != nil {
		t.Fatalf("LoadRunning 失败: %v", err)
	}
	if len(running) != 0 {
		t.Fatalf("Finish 后仍出现 running: %#v", running)
	}
}

func TestDiscoveryTaskStore_GC(t *testing.T) {
	store := newTestDiscoveryTaskStore(t)

	// 一个 done 且过期的任务、一个 done 但新的任务、一个 running 任务。
	_ = store.Start("ch-old", "", "")
	_ = store.Finish("ch-old", DiscoveryStatusDone, "")
	// 手动把 finished_at 推到 25 小时前。
	_, err := store.db.Exec(`UPDATE autopilot_discovery_tasks SET finished_at=? WHERE channel_uid=?`,
		time.Now().Add(-25*time.Hour).UnixMilli(), "ch-old")
	if err != nil {
		t.Fatalf("回写 finished_at 失败: %v", err)
	}

	_ = store.Start("ch-new", "", "")
	_ = store.Finish("ch-new", DiscoveryStatusFailed, "boom")

	_ = store.Start("ch-running", "", "")

	deleted, err := store.GC(24 * time.Hour)
	if err != nil {
		t.Fatalf("GC 失败: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("GC 删除行数=%d, want 1", deleted)
	}

	// 剩余：ch-new（done 但未过期）与 ch-running（running）。
	var remaining []string
	rows, err := store.db.Query(`SELECT channel_uid FROM autopilot_discovery_tasks ORDER BY channel_uid`)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	defer errutil.IgnoreDeferred(rows.Close)
	for rows.Next() {
		var uid string
		_ = rows.Scan(&uid)
		remaining = append(remaining, uid)
	}
	if len(remaining) != 2 || remaining[0] != "ch-new" || remaining[1] != "ch-running" {
		t.Fatalf("remaining=%#v", remaining)
	}
}

func TestEnsureSchemaVersion_V6ToV7_CreatesDiscoveryTasksTable(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	defer errutil.IgnoreDeferred(db.Close)

	// 模拟已有 v6 库。
	if _, err := db.Exec("PRAGMA user_version = 6"); err != nil {
		t.Fatalf("写入 v6 失败: %v", err)
	}
	if err := ensureSchemaVersion(db); err != nil {
		t.Fatalf("ensureSchemaVersion 失败: %v", err)
	}

	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("读取版本失败: %v", err)
	}
	if version != 7 {
		t.Fatalf("user_version=%d, want 7", version)
	}
	exists, err := tableExists(db, "autopilot_discovery_tasks")
	if err != nil {
		t.Fatalf("检查表失败: %v", err)
	}
	if !exists {
		t.Fatal("autopilot_discovery_tasks 表未在 v6->v7 迁移中创建")
	}
}

func TestEnsureSchemaVersion_FreshDB_ThenStoreCreatesTable(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	defer errutil.IgnoreDeferred(db.Close)

	// 全新库（version=0）只写基线版本，表由 store 的 initSchema 创建。
	if err := ensureSchemaVersion(db); err != nil {
		t.Fatalf("ensureSchemaVersion 失败: %v", err)
	}
	var version int
	_ = db.QueryRow("PRAGMA user_version").Scan(&version)
	if version != 7 {
		t.Fatalf("user_version=%d, want 7", version)
	}

	if _, err := NewDiscoveryTaskStoreWithDB(db); err != nil {
		t.Fatalf("NewDiscoveryTaskStoreWithDB 失败: %v", err)
	}
	exists, err := tableExists(db, "autopilot_discovery_tasks")
	if err != nil {
		t.Fatalf("检查表失败: %v", err)
	}
	if !exists {
		t.Fatal("全新库下 store 未创建 autopilot_discovery_tasks 表")
	}
}
