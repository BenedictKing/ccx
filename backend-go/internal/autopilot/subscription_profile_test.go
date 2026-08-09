package autopilot

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/gin-gonic/gin"
)

// newSubTestDB 创建内存 SQLite 数据库用于订阅测试。
func newSubTestDB(t *testing.T) *sql.DB {
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

// newTestSubscription 构造测试用 SubscriptionProfile。
func newTestSubscription(uid, name string) *SubscriptionProfile {
	return &SubscriptionProfile{
		SubscriptionUID: uid,
		DisplayName:     name,
		Provider:        "openai",
		OriginType:      "official_api",
		OriginTier:      "first",
		BillingMode:     "official_api",
		Currency:        "USD",
		Source:          "manual",
	}
}

func TestSubscriptionStore_CreateAndGet(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	cases := []struct {
		name string
		sub  *SubscriptionProfile
	}{
		{"官方 API 订阅", newTestSubscription("sub-001", "OpenAI Prod")},
		{"中转套餐", newTestSubscription("sub-002", "A站充值组")},
		{"公益来源", newTestSubscription("sub-003", "临时公益池")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := store.Create(tc.sub); err != nil {
				t.Fatalf("Create 失败: %v", err)
			}
			got := store.Get(tc.sub.SubscriptionUID)
			if got == nil {
				t.Fatalf("Get(%s) 返回 nil", tc.sub.SubscriptionUID)
			}
			if got.DisplayName != tc.sub.DisplayName {
				t.Errorf("DisplayName 不匹配: got=%s want=%s", got.DisplayName, tc.sub.DisplayName)
			}
			if got.CreatedAt.IsZero() {
				t.Error("CreatedAt 不应为零值")
			}
		})
	}
}

func TestSubscriptionStore_CreateDuplicate(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	sub := newTestSubscription("sub-dup", "重复测试")
	if err := store.Create(sub); err != nil {
		t.Fatalf("首次 Create 失败: %v", err)
	}

	err = store.Create(sub)
	if err == nil {
		t.Fatal("重复 Create 应返回错误")
	}
}

func TestSubscriptionStore_CreateEmptyUID(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	sub := &SubscriptionProfile{DisplayName: "无UID"}
	if err := store.Create(sub); err == nil {
		t.Error("空 UID 的 Create 应返回错误")
	}
}

func TestSubscriptionStore_GetNotExist(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	if got := store.Get("nonexistent"); got != nil {
		t.Errorf("Get(nonexistent) 应返回 nil，实际: %+v", got)
	}
}

func TestSubscriptionStore_GetReturnsCopy(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	sub := newTestSubscription("sub-copy", "副本测试")
	sub.GroupMultipliers = map[string]float64{"opus": 1.5}
	if err := store.Create(sub); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	got1 := store.Get("sub-copy")
	got1.DisplayName = "被修改"
	got1.GroupMultipliers["opus"] = 99.9

	got2 := store.Get("sub-copy")
	if got2.DisplayName == "被修改" {
		t.Error("Get 返回的是引用而非副本，缓存被意外修改")
	}
	if got2.GroupMultipliers["opus"] == 99.9 {
		t.Error("Get 返回的 GroupMultipliers 副本不独立")
	}
}

func TestSubscriptionStore_Update(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	sub := newTestSubscription("sub-upd", "原始名称")
	if err := store.Create(sub); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	// 修改并更新
	sub.DisplayName = "更新后名称"
	sub.Balance = 42.0
	sub.GroupMultipliers = map[string]float64{"opus": 0.5}
	if err := store.Update(sub); err != nil {
		t.Fatalf("Update 失败: %v", err)
	}

	got := store.Get("sub-upd")
	if got == nil {
		t.Fatal("Get(sub-upd) 返回 nil")
	}
	if got.DisplayName != "更新后名称" {
		t.Errorf("DisplayName 未更新: got=%s", got.DisplayName)
	}
	if got.Balance != 42.0 {
		t.Errorf("Balance 未更新: got=%f", got.Balance)
	}
	if got.GroupMultipliers["opus"] != 0.5 {
		t.Errorf("GroupMultipliers 未更新: got=%v", got.GroupMultipliers)
	}
}

func TestSubscriptionStore_UpdateNotExist(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	sub := newTestSubscription("sub-ghost", "不存在")
	err = store.Update(sub)
	if err == nil {
		t.Errorf("Update 不存在的订阅应返回错误")
	}
}

func TestSubscriptionStore_Delete(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	sub := newTestSubscription("sub-del", "待删除")
	if err := store.Create(sub); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	if err := store.Delete("sub-del"); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}

	if got := store.Get("sub-del"); got != nil {
		t.Errorf("Delete 后 Get 应返回 nil，实际: %+v", got)
	}
}

func TestSubscriptionStore_DeleteNotExist(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	// 删除不存在的记录不应报错
	if err := store.Delete("nonexistent"); err != nil {
		t.Fatalf("Delete 不存在的记录不应报错: %v", err)
	}
}

func TestSubscriptionStore_ListAll(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	for i := 0; i < 3; i++ {
		sub := newTestSubscription("sub-list-"+string(rune('a'+i)), "订阅")
		if err := store.Create(sub); err != nil {
			t.Fatalf("Create 失败: %v", err)
		}
	}

	all := store.ListAll()
	if len(all) != 3 {
		t.Errorf("ListAll 返回 %d 条，期望 3", len(all))
	}
}

func TestSubscriptionStore_LinkChannel(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	sub := newTestSubscription("sub-link", "链接测试")
	if err := store.Create(sub); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	// 链接第一个渠道
	if err := store.LinkChannel("sub-link", "ch-001"); err != nil {
		t.Fatalf("LinkChannel 失败: %v", err)
	}
	// 链接第二个渠道
	if err := store.LinkChannel("sub-link", "ch-002"); err != nil {
		t.Fatalf("LinkChannel 第二次失败: %v", err)
	}

	got := store.Get("sub-link")
	if len(got.LinkedChannelUIDs) != 2 {
		t.Errorf("LinkedChannelUIDs 长度=%d，期望 2", len(got.LinkedChannelUIDs))
	}

	// 幂等：重复链接同一渠道
	if err := store.LinkChannel("sub-link", "ch-001"); err != nil {
		t.Fatalf("幂等 LinkChannel 不应报错: %v", err)
	}
	got = store.Get("sub-link")
	if len(got.LinkedChannelUIDs) != 2 {
		t.Errorf("幂等链接后 LinkedChannelUIDs 长度=%d，应仍为 2", len(got.LinkedChannelUIDs))
	}
}

func TestSubscriptionStore_LinkChannelNotExist(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	if err := store.LinkChannel("sub-ghost", "ch-001"); err == nil {
		t.Error("LinkChannel 不存在的订阅应返回错误")
	}
}

func TestSubscriptionStore_UnlinkChannel(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	sub := newTestSubscription("sub-unlink", "解绑测试")
	if err := store.Create(sub); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	_ =

		// 先链接两个
		store.LinkChannel("sub-unlink", "ch-001")
	_ = store.LinkChannel("sub-unlink", "ch-002")

	// 解绑一个
	if err := store.UnlinkChannel("sub-unlink", "ch-001"); err != nil {
		t.Fatalf("UnlinkChannel 失败: %v", err)
	}

	got := store.Get("sub-unlink")
	if len(got.LinkedChannelUIDs) != 1 {
		t.Errorf("解绑后 LinkedChannelUIDs 长度=%d，期望 1", len(got.LinkedChannelUIDs))
	}
	if got.LinkedChannelUIDs[0] != "ch-002" {
		t.Errorf("解绑后剩余渠道=%s，期望 ch-002", got.LinkedChannelUIDs[0])
	}

	// 幂等：解绑已解绑的渠道
	if err := store.UnlinkChannel("sub-unlink", "ch-001"); err != nil {
		t.Fatalf("幂等 UnlinkChannel 不应报错: %v", err)
	}
}

func TestSubscriptionStore_UnlinkChannelNotExist(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	if err := store.UnlinkChannel("sub-ghost", "ch-001"); err == nil {
		t.Error("UnlinkChannel 不存在的订阅应返回错误")
	}
}

func TestSubscriptionStore_RestoreFromDisk(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subscriptions.db")

	// 第一轮：创建并写入（Create 自动落盘）
	store1, err := NewSubscriptionStore(dbPath)
	if err != nil {
		t.Fatalf("NewSubscriptionStore 失败: %v", err)
	}

	sub := newTestSubscription("sub-persist", "持久化测试")
	sub.Balance = 88.5
	sub.GroupMultipliers = map[string]float64{"opus": 0.5, "sonnet": 1.0}
	if err := store1.Create(sub); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	_ = store1.LinkChannel("sub-persist", "ch-aaa")
	_ = store1.LinkChannel("sub-persist", "ch-bbb")

	if err := store1.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	// 第二轮：重新打开验证
	store2, err := NewSubscriptionStore(dbPath)
	if err != nil {
		t.Fatalf("NewSubscriptionStore 重新打开失败: %v", err)
	}
	defer errutil.IgnoreDeferred(store2.Close)

	got := store2.Get("sub-persist")
	if got == nil {
		t.Fatal("重启后 Get(sub-persist) 返回 nil")
	}
	if got.DisplayName != "持久化测试" {
		t.Errorf("DisplayName 不匹配: got=%s", got.DisplayName)
	}
	if got.Balance != 88.5 {
		t.Errorf("Balance 不匹配: got=%f", got.Balance)
	}
	if got.GroupMultipliers["opus"] != 0.5 {
		t.Errorf("GroupMultipliers[opus] 不匹配: got=%f", got.GroupMultipliers["opus"])
	}
	if len(got.LinkedChannelUIDs) != 2 {
		t.Errorf("LinkedChannelUIDs 长度=%d，期望 2", len(got.LinkedChannelUIDs))
	}
}

func TestSubscriptionStore_RestoreMultiple(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subscriptions-multi.db")

	store1, err := NewSubscriptionStore(dbPath)
	if err != nil {
		t.Fatalf("NewSubscriptionStore 失败: %v", err)
	}

	for i := 0; i < 5; i++ {
		sub := newTestSubscription("sub-multi-"+string(rune('a'+i)), "批量")
		if err := store1.Create(sub); err != nil {
			t.Fatalf("Create %d 失败: %v", i, err)
		}
	}
	_ = store1.Close()

	store2, err := NewSubscriptionStore(dbPath)
	if err != nil {
		t.Fatalf("NewSubscriptionStore 重新打开失败: %v", err)
	}
	defer errutil.IgnoreDeferred(store2.Close)

	all := store2.ListAll()
	if len(all) != 5 {
		t.Errorf("重启后 ListAll 返回 %d 条，期望 5", len(all))
	}
}

func TestSubscriptionStore_DeleteThenRestart(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "subscriptions-del.db")

	store1, err := NewSubscriptionStore(dbPath)
	if err != nil {
		t.Fatalf("NewSubscriptionStore 失败: %v", err)
	}

	for _, uid := range []string{"sub-keep", "sub-gone"} {
		sub := newTestSubscription(uid, "删除测试")
		if err := store1.Create(sub); err != nil {
			t.Fatalf("Create %s 失败: %v", uid, err)
		}
	}

	if err := store1.Delete("sub-gone"); err != nil {
		t.Fatalf("Delete sub-gone 失败: %v", err)
	}
	_ = store1.Close()

	store2, err := NewSubscriptionStore(dbPath)
	if err != nil {
		t.Fatalf("NewSubscriptionStore 重新打开失败: %v", err)
	}
	defer errutil.IgnoreDeferred(store2.Close)

	if got := store2.Get("sub-keep"); got == nil {
		t.Error("重启后 sub-keep 应存在")
	}
	if got := store2.Get("sub-gone"); got != nil {
		t.Errorf("重启后 sub-gone 应已删除，实际: %+v", got)
	}
}

func TestSubscriptionStore_PersistOnCreate(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	sub := newTestSubscription("sub-sql", "SQL验证")
	if err := store.Create(sub); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	// 直接查 SQLite 验证落盘
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM autopilot_subscriptions WHERE subscription_uid = ?", "sub-sql").Scan(&count)
	if err != nil {
		t.Fatalf("查询 SQLite 失败: %v", err)
	}
	if count != 1 {
		t.Errorf("Create 后 SQLite 中 count=%d，期望 1", count)
	}

	// 验证 JSON 内容可解析
	var profileJSON string
	err = db.QueryRow("SELECT profile_json FROM autopilot_subscriptions WHERE subscription_uid = ?", "sub-sql").Scan(&profileJSON)
	if err != nil {
		t.Fatalf("查询 profile_json 失败: %v", err)
	}
	var parsed SubscriptionProfile
	if err := json.Unmarshal([]byte(profileJSON), &parsed); err != nil {
		t.Fatalf("反序列化 profile_json 失败: %v", err)
	}
	if parsed.SubscriptionUID != "sub-sql" {
		t.Errorf("JSON 中 subscriptionUid=%s，期望 sub-sql", parsed.SubscriptionUID)
	}
}

func TestSubscriptionStore_LinkPersistsOnUpdate(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	sub := newTestSubscription("sub-lp", "链接落盘")
	if err := store.Create(sub); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	_ = store.LinkChannel("sub-lp", "ch-xxx")

	// 直接查 SQLite 验证 linkedChannelUids 已落盘
	var profileJSON string
	err = db.QueryRow("SELECT profile_json FROM autopilot_subscriptions WHERE subscription_uid = ?", "sub-lp").Scan(&profileJSON)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	var parsed SubscriptionProfile
	if err := json.Unmarshal([]byte(profileJSON), &parsed); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if len(parsed.LinkedChannelUIDs) != 1 || parsed.LinkedChannelUIDs[0] != "ch-xxx" {
		t.Errorf("LinkedChannelUIDs=%v，期望 [ch-xxx]", parsed.LinkedChannelUIDs)
	}
}

func TestSubscriptionStore_IdempotentSchema(t *testing.T) {
	db := newSubTestDB(t)

	store1, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("首次 NewSubscriptionStoreWithDB 失败: %v", err)
	}
	_ = store1.Close()

	// 重复打开同一 db
	store2, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("重复 NewSubscriptionStoreWithDB 失败: %v", err)
	}
	_ = store2.Close()
}

func TestSubscriptionStore_UpdatePreservesCreatedAt(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	sub := newTestSubscription("sub-ts", "时间戳测试")
	if err := store.Create(sub); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	original := store.Get("sub-ts")
	createdAt := original.CreatedAt

	// 更新
	time.Sleep(10 * time.Millisecond)
	sub.DisplayName = "更新后"
	if err := store.Update(sub); err != nil {
		t.Fatalf("Update 失败: %v", err)
	}

	updated := store.Get("sub-ts")
	if !updated.CreatedAt.Equal(createdAt) {
		t.Errorf("CreatedAt 被 Update 修改: before=%v after=%v", createdAt, updated.CreatedAt)
	}
	if !updated.UpdatedAt.After(createdAt) {
		t.Error("UpdatedAt 未在 Update 时更新")
	}
}

// ── AccessToken 加密测试 ───────────────────────────────────────────────────────

func TestSubscriptionStore_AccessToken_EncryptedAtRest(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	const plaintext = "sk-newapi-access-token-12345"
	sub := newTestSubscription("sub-token-enc", "Token加密测试")
	sub.Provider = "new_api"
	sub.AccessToken = plaintext
	if err := store.Create(sub); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	// 1) 读库时透明解密
	got := store.Get("sub-token-enc")
	if got == nil {
		t.Fatal("Get 返回 nil")
	}
	if got.AccessToken != plaintext {
		t.Errorf("AccessToken 未透明解密: got=%q want=%q", got.AccessToken, plaintext)
	}

	// 2) 直接查 SQLite，明文不应出现
	var profileJSON string
	err = db.QueryRow("SELECT profile_json FROM autopilot_subscriptions WHERE subscription_uid = ?", "sub-token-enc").Scan(&profileJSON)
	if err != nil {
		t.Fatalf("查询 profile_json 失败: %v", err)
	}
	if strings.Contains(profileJSON, plaintext) {
		t.Errorf("SQLite 中仍包含明文 AccessToken: %s", profileJSON)
	}
	var parsed SubscriptionProfile
	if err := json.Unmarshal([]byte(profileJSON), &parsed); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if !strings.HasPrefix(parsed.AccessToken, encryptedTokenPrefix) {
		t.Errorf("落库 AccessToken 应带加密前缀 %q，实际=%q", encryptedTokenPrefix, parsed.AccessToken)
	}
}

func TestSubscriptionStore_AccessToken_BackwardCompatiblePlaintext(t *testing.T) {
	db := newSubTestDB(t)

	// 先初始化 schema，再构造一条旧明文记录，模拟升级前已存在的数据。
	const plaintext = "legacy-plain-token"
	oldProfile := SubscriptionProfile{
		SubscriptionUID: "sub-token-legacy",
		DisplayName:     "旧明文兼容",
		Provider:        "new_api",
		Source:          "manual",
		AccessToken:     plaintext,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	data, err := json.Marshal(oldProfile)
	if err != nil {
		t.Fatalf("marshal 旧记录失败: %v", err)
	}

	// 显式建表，避免 newSubTestDB 仅返回裸 *sql.DB。
	if err := initSubscriptionStoreSchema(db); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	_, err = db.Exec("INSERT INTO autopilot_subscriptions (subscription_uid, profile_json, created_at, updated_at) VALUES (?, ?, ?, ?)",
		oldProfile.SubscriptionUID, string(data), time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("插入旧明文记录失败: %v", err)
	}

	// 重新打开 store 触发 loadAll 解密（实际为兼容明文）。
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	got := store.Get("sub-token-legacy")
	if got == nil {
		t.Fatal("Get 返回 nil")
	}
	if got.AccessToken != plaintext {
		t.Errorf("旧明文 AccessToken 应原样返回: got=%q want=%q", got.AccessToken, plaintext)
	}
}

func TestSubscriptionStore_AccessToken_ReloadWithSameKey(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "token-reload.db")

	const plaintext = "reload-token-secret"
	store1, err := NewSubscriptionStore(dbPath)
	if err != nil {
		t.Fatalf("NewSubscriptionStore 失败: %v", err)
	}
	sub := newTestSubscription("sub-reload", "重启解密测试")
	sub.Provider = "new_api"
	sub.AccessToken = plaintext
	if err := store1.Create(sub); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}
	_ = store1.Close()

	store2, err := NewSubscriptionStore(dbPath)
	if err != nil {
		t.Fatalf("重新打开失败: %v", err)
	}
	defer errutil.IgnoreDeferred(store2.Close)

	got := store2.Get("sub-reload")
	if got == nil {
		t.Fatal("重启后 Get 返回 nil")
	}
	if got.AccessToken != plaintext {
		t.Errorf("重启后 AccessToken 解密不一致: got=%q want=%q", got.AccessToken, plaintext)
	}
}

func TestSubscriptionStore_AccountAccessToken_EncryptedAtRest(t *testing.T) {
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}

	const plaintext = "account-token-secret"
	sub := newTestSubscription("sub-account-enc", "多账号Token加密")
	sub.Provider = "new_api"
	sub.Accounts = []NewApiAccount{{
		AccountUID:  "acct_1",
		AccessToken: plaintext,
		CreatedAt:   time.Now(),
	}}
	if err := store.Create(sub); err != nil {
		t.Fatalf("Create 失败: %v", err)
	}

	got := store.Get("sub-account-enc")
	if got == nil || len(got.Accounts) != 1 {
		t.Fatal("Get 返回异常")
	}
	if got.Accounts[0].AccessToken != plaintext {
		t.Errorf("Accounts[].AccessToken 未透明解密: got=%q want=%q", got.Accounts[0].AccessToken, plaintext)
	}

	var profileJSON string
	err = db.QueryRow("SELECT profile_json FROM autopilot_subscriptions WHERE subscription_uid = ?", "sub-account-enc").Scan(&profileJSON)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if strings.Contains(profileJSON, plaintext) {
		t.Errorf("SQLite 中仍包含明文账号 AccessToken: %s", profileJSON)
	}
}

// ── HTTP Handler 测试 ──────────────────────────────────────────────────────────

// performRequest 发起 HTTP 请求并返回录制的响应。
func performRequest(router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	var reqBody *strings.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	} else {
		reqBody = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, reqBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func setupSubRouter(t *testing.T) (*SubscriptionStore, *gin.Engine) {
	t.Helper()
	db := newSubTestDB(t)
	store, err := NewSubscriptionStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}
	router := gin.New()
	RegisterSubscriptionRoutes(router, store, nil)
	return store, router
}

func TestHandler_CreateAndGet(t *testing.T) {
	_, router := setupSubRouter(t)

	body := `{"subscriptionUid":"sub-h1","displayName":"测试订阅","provider":"openai","source":"manual"}`
	w := performRequest(router, "POST", "/subscriptions", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /subscriptions 返回 %d，期望 201: %s", w.Code, w.Body.String())
	}

	w = performRequest(router, "GET", "/subscriptions/sub-h1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /subscriptions/sub-h1 返回 %d，期望 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"subscriptionUid":"sub-h1"`) {
		t.Errorf("GET 响应缺少 subscriptionUid: %s", w.Body.String())
	}
}

func TestHandler_CreateDuplicate(t *testing.T) {
	_, router := setupSubRouter(t)

	body := `{"subscriptionUid":"sub-dup","displayName":"重复","source":"manual"}`
	performRequest(router, "POST", "/subscriptions", body)
	w := performRequest(router, "POST", "/subscriptions", body)
	if w.Code != http.StatusConflict {
		t.Errorf("重复 POST 返回 %d，期望 409", w.Code)
	}
}

func TestHandler_GetNotFound(t *testing.T) {
	_, router := setupSubRouter(t)

	w := performRequest(router, "GET", "/subscriptions/none", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("GET 不存在的订阅返回 %d，期望 404", w.Code)
	}
}

func TestHandler_ListEmpty(t *testing.T) {
	_, router := setupSubRouter(t)

	w := performRequest(router, "GET", "/subscriptions", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /subscriptions 返回 %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"total":0`) {
		t.Errorf("空列表 total 应为 0: %s", w.Body.String())
	}
}

func TestHandler_Update(t *testing.T) {
	_, router := setupSubRouter(t)

	body := `{"subscriptionUid":"sub-upd","displayName":"原始","source":"manual"}`
	performRequest(router, "POST", "/subscriptions", body)

	update := `{"displayName":"更新后","balance":42.5}`
	w := performRequest(router, "PUT", "/subscriptions/sub-upd", update)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT 返回 %d，期望 200: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"displayName":"更新后"`) {
		t.Errorf("PUT 响应 displayName 未更新: %s", w.Body.String())
	}
}

func TestHandler_UpdateNotFound(t *testing.T) {
	_, router := setupSubRouter(t)

	w := performRequest(router, "PUT", "/subscriptions/none", `{"displayName":"x"}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("PUT 不存在的订阅返回 %d，期望 404", w.Code)
	}
}

func TestHandler_Delete(t *testing.T) {
	_, router := setupSubRouter(t)

	body := `{"subscriptionUid":"sub-del","displayName":"待删","source":"manual"}`
	performRequest(router, "POST", "/subscriptions", body)

	w := performRequest(router, "DELETE", "/subscriptions/sub-del", "")
	if w.Code != http.StatusNoContent {
		t.Errorf("DELETE 返回 %d，期望 204", w.Code)
	}

	// 验证确实删除
	w = performRequest(router, "GET", "/subscriptions/sub-del", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("删除后 GET 返回 %d，期望 404", w.Code)
	}
}

func TestHandler_LinkChannel(t *testing.T) {
	_, router := setupSubRouter(t)

	body := `{"subscriptionUid":"sub-link","displayName":"链接","source":"manual"}`
	performRequest(router, "POST", "/subscriptions", body)

	link := `{"channelUid":"ch-test"}`
	w := performRequest(router, "POST", "/subscriptions/sub-link/link", link)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /link 返回 %d，期望 200: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"ch-test"`) {
		t.Errorf("链接后响应缺少 channelUid: %s", w.Body.String())
	}
}

func TestHandler_LinkEmptyBody(t *testing.T) {
	_, router := setupSubRouter(t)

	body := `{"subscriptionUid":"sub-eb","displayName":"空body","source":"manual"}`
	performRequest(router, "POST", "/subscriptions", body)

	// 空 body
	w := performRequest(router, "POST", "/subscriptions/sub-eb/link", "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("POST /link 空 body 返回 %d，期望 400", w.Code)
	}
}

func TestHandler_LinkEmptyJSON(t *testing.T) {
	_, router := setupSubRouter(t)

	body := `{"subscriptionUid":"sub-ej","displayName":"空JSON","source":"manual"}`
	performRequest(router, "POST", "/subscriptions", body)

	// 空 JSON（缺少 channelUid）
	w := performRequest(router, "POST", "/subscriptions/sub-ej/link", `{}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("POST /link 空 JSON 返回 %d，期望 400", w.Code)
	}
}

func TestHandler_UnlinkChannel(t *testing.T) {
	_, router := setupSubRouter(t)

	body := `{"subscriptionUid":"sub-ul","displayName":"解绑","source":"manual"}`
	performRequest(router, "POST", "/subscriptions", body)

	// 先链接
	performRequest(router, "POST", "/subscriptions/sub-ul/link", `{"channelUid":"ch-x"}`)

	w := performRequest(router, "POST", "/subscriptions/sub-ul/unlink", `{"channelUid":"ch-x"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("POST /unlink 返回 %d，期望 200: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"ch-x"`) {
		t.Errorf("解绑后不应再包含 ch-x: %s", w.Body.String())
	}
}

func TestHandler_LinkNotFound(t *testing.T) {
	_, router := setupSubRouter(t)

	w := performRequest(router, "POST", "/subscriptions/none/link", `{"channelUid":"ch-x"}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("POST /link 不存在订阅返回 %d，期望 404", w.Code)
	}
}
