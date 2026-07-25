package autopilot

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/errutil"
	_ "modernc.org/sqlite"
)

// newTestDB 创建内存 SQLite 数据库用于测试。
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// newTestProfile 构造测试用 KeyEndpointProfile。
func newTestProfile(uid, channelUID, serviceType, baseURL string) *KeyEndpointProfile {
	return &KeyEndpointProfile{
		EndpointUID:     uid,
		AccountUID:      "acct-" + channelUID,
		ChannelUID:      channelUID,
		ChannelID:       0,
		ChannelKind:     serviceType,
		ServiceType:     serviceType,
		BaseURL:         baseURL,
		IdentityBaseURL: baseURL,
		KeyMask:         "sk-***abc",
		KeyHash:         "kh-" + uid,
		CredentialUID:   "cred-" + uid,
		MetricsKey:      "mk-" + uid,
		HealthState:     HealthStateHealthy,
		QualityTier:     QualityTierHigh,
		StabilityTier:   StabilityTierStable,
		SpeedTier:       SpeedTierFast,
		CostTier:        CostTierNormal,
	}
}

func TestProfileStoreListByAccount(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB 失败: %v", err)
	}
	first := newTestProfile("ep-a", "ch-a", "messages", "https://a.example.com")
	second := newTestProfile("ep-b", "ch-b", "chat", "https://b.example.com")
	second.AccountUID = first.AccountUID
	third := newTestProfile("ep-c", "ch-c", "chat", "https://c.example.com")
	for _, profile := range []*KeyEndpointProfile{first, second, third} {
		if err := store.Upsert(profile); err != nil {
			t.Fatalf("Upsert 失败: %v", err)
		}
	}
	if got := store.ListByAccount(first.AccountUID); len(got) != 2 {
		t.Fatalf("ListByAccount 返回 %d 条，want 2", len(got))
	}
	if err := store.DeleteByCredential(first.AccountUID, first.CredentialUID); err != nil {
		t.Fatalf("DeleteByCredential 失败: %v", err)
	}
	if got := store.ListByAccount(first.AccountUID); len(got) != 1 {
		t.Fatalf("DeleteByCredential 后返回 %d 条，want 1", len(got))
	}
	if err := store.DeleteByAccount(first.AccountUID); err != nil {
		t.Fatalf("DeleteByAccount 失败: %v", err)
	}
	if got := store.ListByAccount(first.AccountUID); len(got) != 0 {
		t.Fatalf("DeleteByAccount 后仍有 %d 条画像", len(got))
	}
}

func TestProfileStore_UpsertAndGet(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB 失败: %v", err)
	}

	cases := []struct {
		name    string
		profile *KeyEndpointProfile
	}{
		{
			name:    "单条 upsert",
			profile: newTestProfile("ep-001", "ch-001", "messages", "https://api1.example.com"),
		},
		{
			name:    "不同 channel",
			profile: newTestProfile("ep-002", "ch-002", "chat", "https://api2.example.com"),
		},
		{
			name:    "同 channel 不同 endpoint",
			profile: newTestProfile("ep-003", "ch-001", "messages", "https://api3.example.com"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.Upsert(tc.profile); err != nil {
				t.Fatalf("Upsert 失败: %v", err)
			}
			got := store.Get(tc.profile.EndpointUID)
			if got == nil {
				t.Fatalf("Get(%s) 返回 nil", tc.profile.EndpointUID)
			}
			if got.EndpointUID != tc.profile.EndpointUID {
				t.Errorf("EndpointUID 不匹配: got=%s want=%s", got.EndpointUID, tc.profile.EndpointUID)
			}
			if got.ChannelUID != tc.profile.ChannelUID {
				t.Errorf("ChannelUID 不匹配: got=%s want=%s", got.ChannelUID, tc.profile.ChannelUID)
			}
			if got.ServiceType != tc.profile.ServiceType {
				t.Errorf("ServiceType 不匹配: got=%s want=%s", got.ServiceType, tc.profile.ServiceType)
			}
		})
	}
}

func TestProfileStore_RoundTripsProtocolDiscoveryFields(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB 失败: %v", err)
	}
	discoveredAt := time.Date(2026, 7, 25, 11, 0, 0, 0, time.UTC)
	profile := newTestProfile("ep-protocol", "ch-protocol", "responses", "https://api.example.com")
	profile.ProtocolModels = map[string][]string{"responses": {"gpt-5.4"}, "chat": {"gpt-5.4"}}
	profile.ProtocolModelsHash = hashProtocolModels(profile.ProtocolModels)
	profile.ProtocolDiscoveredAt = map[string]time.Time{"responses": discoveredAt, "chat": discoveredAt}
	profile.ProtocolDiscoverySource = map[string]string{"responses": "models_api", "chat": "protocol_probe"}
	profile.ProtocolDiscoveryError = map[string]string{"gemini": "HTTP 404"}
	if err := store.Upsert(profile); err != nil {
		t.Fatalf("Upsert 失败: %v", err)
	}
	if err := store.Flush(); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	got := store.Get(profile.EndpointUID)
	if got == nil || strings.Join(got.ProtocolModels["chat"], ",") != "gpt-5.4" {
		t.Fatalf("ProtocolModels 未正确持久化: %+v", got)
	}
	if got.ProtocolDiscoverySource["chat"] != "protocol_probe" || got.ProtocolDiscoveryError["gemini"] == "" {
		t.Fatalf("协议元数据未正确持久化: %+v", got)
	}
}

func TestProfileStore_UpsertOverwrite(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB 失败: %v", err)
	}

	p1 := newTestProfile("ep-001", "ch-001", "messages", "https://old.example.com")
	if err := store.Upsert(p1); err != nil {
		t.Fatalf("首次 Upsert 失败: %v", err)
	}

	// 覆盖更新
	p2 := newTestProfile("ep-001", "ch-001", "messages", "https://new.example.com")
	p2.HealthState = HealthStateDegraded
	if err := store.Upsert(p2); err != nil {
		t.Fatalf("覆盖 Upsert 失败: %v", err)
	}

	got := store.Get("ep-001")
	if got == nil {
		t.Fatal("Get(ep-001) 返回 nil")
	}
	if got.BaseURL != "https://new.example.com" {
		t.Errorf("BaseURL 未更新: got=%s want=https://new.example.com", got.BaseURL)
	}
	if got.HealthState != HealthStateDegraded {
		t.Errorf("HealthState 未更新: got=%s want=%s", got.HealthState, HealthStateDegraded)
	}
}

func TestProfileStore_GetNotExist(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB 失败: %v", err)
	}

	got := store.Get("nonexistent")
	if got != nil {
		t.Errorf("Get(nonexistent) 应返回 nil，实际: %+v", got)
	}
}

func TestProfileStore_Delete(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB 失败: %v", err)
	}

	p := newTestProfile("ep-del", "ch-001", "messages", "https://api.example.com")
	if err := store.Upsert(p); err != nil {
		t.Fatalf("Upsert 失败: %v", err)
	}

	if err := store.Delete("ep-del"); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}

	if got := store.Get("ep-del"); got != nil {
		t.Errorf("Delete 后 Get 应返回 nil，实际: %+v", got)
	}
}

func TestProfileStore_DeleteNotExist(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB 失败: %v", err)
	}

	// 删除不存在的记录不应报错
	if err := store.Delete("nonexistent"); err != nil {
		t.Fatalf("Delete 不存在的记录不应报错: %v", err)
	}
}

func TestProfileStore_ListByChannel(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB 失败: %v", err)
	}

	profiles := []*KeyEndpointProfile{
		newTestProfile("ep-1", "ch-A", "messages", "https://a1.com"),
		newTestProfile("ep-2", "ch-A", "messages", "https://a2.com"),
		newTestProfile("ep-3", "ch-B", "chat", "https://b1.com"),
	}
	for _, p := range profiles {
		if err := store.Upsert(p); err != nil {
			t.Fatalf("Upsert 失败: %v", err)
		}
	}

	cases := []struct {
		name       string
		channelUID string
		wantCount  int
	}{
		{"ch-A 有 2 条", "ch-A", 2},
		{"ch-B 有 1 条", "ch-B", 1},
		{"ch-C 有 0 条", "ch-C", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := store.ListByChannel(tc.channelUID)
			if len(result) != tc.wantCount {
				t.Errorf("ListByChannel(%s) 返回 %d 条，期望 %d", tc.channelUID, len(result), tc.wantCount)
			}
		})
	}
}

func TestProfileStore_ListByService(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB 失败: %v", err)
	}

	profiles := []*KeyEndpointProfile{
		newTestProfile("ep-1", "ch-1", "messages", "https://a.com"),
		newTestProfile("ep-2", "ch-2", "messages", "https://b.com"),
		newTestProfile("ep-3", "ch-3", "chat", "https://c.com"),
	}
	for _, p := range profiles {
		if err := store.Upsert(p); err != nil {
			t.Fatalf("Upsert 失败: %v", err)
		}
	}

	result := store.ListByService("messages")
	if len(result) != 2 {
		t.Errorf("ListByService(messages) 返回 %d 条，期望 2", len(result))
	}
	result = store.ListByService("gemini")
	if len(result) != 0 {
		t.Errorf("ListByService(gemini) 返回 %d 条，期望 0", len(result))
	}
}

func TestProfileStore_ListAll(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB 失败: %v", err)
	}

	for i := 0; i < 5; i++ {
		p := newTestProfile(
			"ep-"+string(rune('a'+i)),
			"ch-1", "messages", "https://api.example.com",
		)
		if err := store.Upsert(p); err != nil {
			t.Fatalf("Upsert 失败: %v", err)
		}
	}

	all := store.ListAll()
	if len(all) != 5 {
		t.Errorf("ListAll 返回 %d 条，期望 5", len(all))
	}
}

func TestProfileStore_ActiveViewsFilterWithoutDeletingHistory(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB 失败: %v", err)
	}

	profiles := []*KeyEndpointProfile{
		newTestProfile("ep-active-a", "ch-a", "messages", "https://a.example.com"),
		newTestProfile("ep-stale-a", "ch-a", "messages", "https://old-a.example.com"),
		newTestProfile("ep-active-b", "ch-b", "chat", "https://b.example.com"),
	}
	for _, profile := range profiles {
		if err := store.Upsert(profile); err != nil {
			t.Fatalf("Upsert 失败: %v", err)
		}
	}

	// 清单初始化前保持 fail-open，避免启动瞬间把全部画像隐藏。
	if got := len(store.ListActive()); got != 3 {
		t.Fatalf("初始化前有效画像数=%d, want 3", got)
	}
	store.ReplaceActiveEndpointUIDs(map[string]struct{}{
		"ep-active-a": {},
		"ep-active-b": {},
	})

	if got := len(store.ListActive()); got != 2 {
		t.Fatalf("有效画像数=%d, want 2", got)
	}
	if got := len(store.ListActiveByChannel("ch-a")); got != 1 {
		t.Fatalf("ch-a 有效画像数=%d, want 1", got)
	}
	if got := len(store.ListAll()); got != 3 {
		t.Fatalf("历史画像被删除: ListAll=%d, want 3", got)
	}
	if store.Get("ep-stale-a") == nil {
		t.Fatal("失效画像仍应保留，供审计或显式清理")
	}

	store.ReplaceActiveEndpointUIDs(nil)
	if got := len(store.ListActive()); got != 0 {
		t.Fatalf("空配置下有效画像数=%d, want 0", got)
	}
	if got := len(store.ListAll()); got != 3 {
		t.Fatalf("空配置不应删除历史画像: ListAll=%d, want 3", got)
	}
}

func TestProfileStore_Flush(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB 失败: %v", err)
	}

	p := newTestProfile("ep-flush", "ch-001", "messages", "https://api.example.com")
	if err := store.Upsert(p); err != nil {
		t.Fatalf("Upsert 失败: %v", err)
	}

	if err := store.Flush(); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	// 验证 SQLite 中已写入
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM autopilot_endpoint_profiles WHERE endpoint_uid = ?", "ep-flush").Scan(&count)
	if err != nil {
		t.Fatalf("查询 SQLite 失败: %v", err)
	}
	if count != 1 {
		t.Errorf("Flush 后 SQLite 中 count=%d，期望 1", count)
	}
}

func TestProfileStore_FlushNoDirty(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB 失败: %v", err)
	}

	// 无 dirty key 时 Flush 应不报错
	if err := store.Flush(); err != nil {
		t.Fatalf("无 dirty 时 Flush 失败: %v", err)
	}
}

func TestProfileStore_RestoreFromDisk(t *testing.T) {
	// 用临时文件数据库，验证重启后恢复
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "profiles.db")

	// 第一轮：写入并 Flush
	store1, err := NewProfileStore(dbPath)
	if err != nil {
		t.Fatalf("NewProfileStore 失败: %v", err)
	}

	p := newTestProfile("ep-persist", "ch-001", "messages", "https://persist.example.com")
	p.QualityTier = QualityTierPremium
	if err := store1.Upsert(p); err != nil {
		t.Fatalf("Upsert 失败: %v", err)
	}
	if err := store1.Flush(); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	// 第二轮：重新打开，验证从磁盘加载
	store2, err := NewProfileStore(dbPath)
	if err != nil {
		t.Fatalf("NewProfileStore 重新打开失败: %v", err)
	}
	defer errutil.IgnoreDeferred(store2.Close)

	got := store2.Get("ep-persist")
	if got == nil {
		t.Fatal("重启后 Get(ep-persist) 返回 nil，持久化未生效")
	}
	if got.BaseURL != "https://persist.example.com" {
		t.Errorf("重启后 BaseURL 不匹配: got=%s", got.BaseURL)
	}
	if got.QualityTier != QualityTierPremium {
		t.Errorf("重启后 QualityTier 不匹配: got=%s want=premium", got.QualityTier)
	}
	if got.ChannelUID != "ch-001" {
		t.Errorf("重启后 ChannelUID 不匹配: got=%s want=ch-001", got.ChannelUID)
	}
}

func TestProfileStore_RestoreMultiple(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "profiles-multi.db")

	store1, err := NewProfileStore(dbPath)
	if err != nil {
		t.Fatalf("NewProfileStore 失败: %v", err)
	}

	for i := 0; i < 10; i++ {
		p := newTestProfile(
			"ep-multi-"+string(rune('a'+i)),
			"ch-multi", "messages", "https://api.example.com",
		)
		if err := store1.Upsert(p); err != nil {
			t.Fatalf("Upsert ep-%d 失败: %v", i, err)
		}
	}
	if err := store1.Flush(); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}
	_ = store1.Close()

	store2, err := NewProfileStore(dbPath)
	if err != nil {
		t.Fatalf("NewProfileStore 重新打开失败: %v", err)
	}
	defer errutil.IgnoreDeferred(store2.Close)

	all := store2.ListAll()
	if len(all) != 10 {
		t.Errorf("重启后 ListAll 返回 %d 条，期望 10", len(all))
	}
}

func TestProfileStore_DeleteThenRestart(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "profiles-del.db")

	store1, err := NewProfileStore(dbPath)
	if err != nil {
		t.Fatalf("NewProfileStore 失败: %v", err)
	}

	// 写入 2 条
	for _, uid := range []string{"ep-keep", "ep-gone"} {
		p := newTestProfile(uid, "ch-001", "messages", "https://api.example.com")
		if err := store1.Upsert(p); err != nil {
			t.Fatalf("Upsert %s 失败: %v", uid, err)
		}
	}
	if err := store1.Flush(); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	// 删除一条
	if err := store1.Delete("ep-gone"); err != nil {
		t.Fatalf("Delete ep-gone 失败: %v", err)
	}
	_ = store1.Close()

	// 重启验证
	store2, err := NewProfileStore(dbPath)
	if err != nil {
		t.Fatalf("NewProfileStore 重新打开失败: %v", err)
	}
	defer errutil.IgnoreDeferred(store2.Close)

	if got := store2.Get("ep-keep"); got == nil {
		t.Error("重启后 ep-keep 应存在")
	}
	if got := store2.Get("ep-gone"); got != nil {
		t.Errorf("重启后 ep-gone 应已删除，实际: %+v", got)
	}
}

func TestProfileStore_UpsertEmptyUID(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB 失败: %v", err)
	}

	p := &KeyEndpointProfile{
		EndpointUID: "", // 空 UID
	}
	if err := store.Upsert(p); err == nil {
		t.Error("Upsert 空 EndpointUID 应返回错误")
	}
}

func TestProfileStore_GetReturnsCopy(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB 失败: %v", err)
	}

	p := newTestProfile("ep-copy", "ch-001", "messages", "https://api.example.com")
	if err := store.Upsert(p); err != nil {
		t.Fatalf("Upsert 失败: %v", err)
	}

	got1 := store.Get("ep-copy")
	got1.BaseURL = "https://modified.example.com" // 修改副本

	got2 := store.Get("ep-copy")
	if got2.BaseURL == "https://modified.example.com" {
		t.Error("Get 返回的是引用而非副本，缓存被意外修改")
	}
}

func TestProfileStore_ExistingTableNoError(t *testing.T) {
	// 验证幂等建表：重复打开不报错
	db := newTestDB(t)

	store1, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("首次 NewProfileStoreWithDB 失败: %v", err)
	}
	_ = store1.Close()

	// 再次打开同一 db
	store2, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("重复 NewProfileStoreWithDB 失败: %v", err)
	}
	_ = store2.Close()
}

func TestProfileStore_FileDBCreation(t *testing.T) {
	// 验证目录不存在时自动创建
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subdir", "deep", "profiles.db")

	store, err := NewProfileStore(dbPath)
	if err != nil {
		t.Fatalf("NewProfileStore 自动创建目录失败: %v", err)
	}
	defer errutil.IgnoreDeferred(store.Close)

	// 文件应该存在
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("数据库文件未创建")
	}
}
