package autopilot

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/errutil"
	_ "modernc.org/sqlite"
)

// ── ProfileStore: 内存缓存 + SQLite 异步持久化 ──

// ProfileStore 管理 KeyEndpointProfile 的内存缓存与 SQLite 持久化。
// Phase 1：画像本体存 profile_json TEXT 列，避免宽表迁移。
// 内存为主读取源，写入双写内存+异步落盘。
type ProfileStore struct {
	db     *sql.DB
	dbPath string // 仅由 NewProfileStore 设置，NewProfileStoreWithDB 为空

	cache map[string]*KeyEndpointProfile // key = endpointUID
	mu    sync.RWMutex
	// activeEndpointUIDs 是当前配置可达的 endpoint 清单，仅影响运行态视图，
	// 不删除 SQLite 中的历史画像。未初始化时读取保持 fail-open。
	activeEndpointUIDs   map[string]struct{}
	activeInventoryReady bool

	flushMu sync.Mutex

	dirtyKeys map[string]struct{} // 待落盘的 endpointUID 集合
}

// DB 返回底层 *sql.DB，供 Manager 内部复用连接。
func (s *ProfileStore) DB() *sql.DB { return s.db }

// NewProfileStore 创建 ProfileStore，自行管理 SQLite 连接。
// dbPath 为数据库文件路径，启动时自动建表并 LoadAll 回内存。
func NewProfileStore(dbPath string) (*ProfileStore, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("[ProfileStore-Init] 创建数据库目录失败: %w", err)
	}

	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("[ProfileStore-Init] 打开数据库失败: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	store, err := newProfileStoreFromDB(db, dbPath)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// NewProfileStoreWithDB 使用外部提供的 *sql.DB 创建 ProfileStore（便于测试/复用连接）。
// 调用方负责 db 的生命周期管理；Close() 不会关闭该 db。
func NewProfileStoreWithDB(db *sql.DB) (*ProfileStore, error) {
	return newProfileStoreFromDB(db, "")
}

func newProfileStoreFromDB(db *sql.DB, dbPath string) (*ProfileStore, error) {
	if err := ensureSchemaVersion(db); err != nil {
		return nil, err
	}
	if err := initProfileStoreSchema(db); err != nil {
		return nil, fmt.Errorf("[ProfileStore-Init] 建表失败: %w", err)
	}

	store := &ProfileStore{
		db:        db,
		dbPath:    dbPath,
		cache:     make(map[string]*KeyEndpointProfile),
		dirtyKeys: make(map[string]struct{}),
	}

	if err := store.loadAll(); err != nil {
		return nil, fmt.Errorf("[ProfileStore-Init] 加载画像失败: %w", err)
	}

	log.Printf("[ProfileStore-Init] 初始化完成，已加载 %d 条画像", len(store.cache))
	return store, nil
}

// initProfileStoreSchema 建表迁移。
func initProfileStoreSchema(db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS autopilot_endpoint_profiles (
    endpoint_uid  TEXT PRIMARY KEY,
    account_uid   TEXT NOT NULL DEFAULT '',
    credential_uid TEXT NOT NULL DEFAULT '',
    channel_uid   TEXT NOT NULL,
    service_type  TEXT NOT NULL,
    base_url      TEXT NOT NULL,
    key_hash      TEXT NOT NULL,
    profile_json  TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_endpoint_profiles_channel ON autopilot_endpoint_profiles(channel_uid);
CREATE INDEX IF NOT EXISTS idx_endpoint_profiles_account ON autopilot_endpoint_profiles(account_uid);
CREATE INDEX IF NOT EXISTS idx_endpoint_profiles_service ON autopilot_endpoint_profiles(service_type);
`
	_, err := db.Exec(schema)
	return err
}

// loadAll 从 SQLite 加载全部画像到内存缓存。
func (s *ProfileStore) loadAll() error {
	rows, err := s.db.Query("SELECT endpoint_uid, profile_json FROM autopilot_endpoint_profiles")
	if err != nil {
		return err
	}
	defer errutil.IgnoreDeferred(rows.Close)

	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for rows.Next() {
		var uid string
		var profileJSON string
		if err := rows.Scan(&uid, &profileJSON); err != nil {
			log.Printf("[ProfileStore-LoadAll] 跳过损坏行: %v", err)
			continue
		}
		var profile KeyEndpointProfile
		if err := json.Unmarshal([]byte(profileJSON), &profile); err != nil {
			log.Printf("[ProfileStore-LoadAll] 反序列化失败 uid=%s: %v", uid, err)
			continue
		}
		s.cache[uid] = &profile
		count++
	}
	return rows.Err()
}

// ── 公开方法 ──

// Upsert 插入或更新一条 endpoint 画像。
// 内存立即生效，标记 dirty 后由 Flush 落盘。
func (s *ProfileStore) Upsert(profile *KeyEndpointProfile) error {
	if profile.EndpointUID == "" {
		return fmt.Errorf("[ProfileStore-Upsert] endpoint_uid 不能为空")
	}

	s.mu.Lock()
	s.cache[profile.EndpointUID] = profile
	s.mu.Unlock()

	s.flushMu.Lock()
	s.dirtyKeys[profile.EndpointUID] = struct{}{}
	s.flushMu.Unlock()

	return nil
}

// Get 按 endpointUID 从内存缓存获取画像。不存在返回 nil。
func (s *ProfileStore) Get(endpointUID string) *KeyEndpointProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := s.cache[endpointUID]
	if p == nil {
		return nil
	}
	// 返回副本，避免调用方修改缓存
	cp := *p
	return &cp
}

// ReplaceActiveEndpointUIDs 原子替换当前配置中的有效 endpoint 清单。
// 该清单只存在于内存，不修改或删除历史画像。
func (s *ProfileStore) ReplaceActiveEndpointUIDs(endpointUIDs map[string]struct{}) {
	active := make(map[string]struct{}, len(endpointUIDs))
	for endpointUID := range endpointUIDs {
		if endpointUID != "" {
			active[endpointUID] = struct{}{}
		}
	}

	s.mu.Lock()
	s.activeEndpointUIDs = active
	s.activeInventoryReady = true
	s.mu.Unlock()
}

func (s *ProfileStore) isActiveLocked(endpointUID string) bool {
	if !s.activeInventoryReady {
		return true
	}
	_, ok := s.activeEndpointUIDs[endpointUID]
	return ok
}

// ListByChannel 返回指定 channelUID 下的全部 endpoint 画像副本。
func (s *ProfileStore) ListByChannel(channelUID string) []*KeyEndpointProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*KeyEndpointProfile
	for _, p := range s.cache {
		if p.ChannelUID == channelUID {
			cp := *p
			result = append(result, &cp)
		}
	}
	return result
}

// ListActiveByChannel 返回当前配置仍可达的指定渠道画像。
// 有效清单尚未初始化时 fail-open，等价于 ListByChannel。
func (s *ProfileStore) ListActiveByChannel(channelUID string) []*KeyEndpointProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*KeyEndpointProfile
	for endpointUID, p := range s.cache {
		if p.ChannelUID == channelUID && s.isActiveLocked(endpointUID) {
			cp := *p
			result = append(result, &cp)
		}
	}
	return result
}

// ListByAccount 返回指定 accountUID 下的全部 endpoint 画像副本。
func (s *ProfileStore) ListByAccount(accountUID string) []*KeyEndpointProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*KeyEndpointProfile
	for _, p := range s.cache {
		if p.AccountUID == accountUID {
			cp := *p
			result = append(result, &cp)
		}
	}
	return result
}

// ListByService 返回指定 serviceType 下的全部 endpoint 画像副本。
func (s *ProfileStore) ListByService(serviceType string) []*KeyEndpointProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*KeyEndpointProfile
	for _, p := range s.cache {
		if p.ServiceType == serviceType {
			cp := *p
			result = append(result, &cp)
		}
	}
	return result
}

// ListByUpstreamIdentity 返回同一上游端点身份（identityBaseURL + keyHash）下的全部画像副本。
//
// 同一个 baseURL + Key 只对应一个真实上游端点：无论 CCX 侧把它拆成 messages / chat /
// responses 几条协议渠道，GET /v1/models 打的都是同一个 URL 带同一个凭证，因此模型清单
// 本身是共享的一份事实，只有"某模型在某协议下是否可用"才是协议维度的差异。
// excludeChannelUID 用于排除调用方自己所属的渠道，只看兄弟渠道的证据。
func (s *ProfileStore) ListByUpstreamIdentity(identityBaseURL, keyHash, excludeChannelUID string) []*KeyEndpointProfile {
	if identityBaseURL == "" || keyHash == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*KeyEndpointProfile
	for _, p := range s.cache {
		if p.IdentityBaseURL != identityBaseURL || p.KeyHash != keyHash {
			continue
		}
		if excludeChannelUID != "" && p.ChannelUID == excludeChannelUID {
			continue
		}
		cp := *p
		result = append(result, &cp)
	}
	return result
}

// ListAll 返回全部 endpoint 画像副本。
func (s *ProfileStore) ListAll() []*KeyEndpointProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*KeyEndpointProfile, 0, len(s.cache))
	for _, p := range s.cache {
		cp := *p
		result = append(result, &cp)
	}
	return result
}

// ListActive 返回当前配置仍可达的全部 endpoint 画像。
// 历史画像仍由 ListAll 返回，供显式审计或清理流程使用。
func (s *ProfileStore) ListActive() []*KeyEndpointProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*KeyEndpointProfile, 0, len(s.cache))
	for endpointUID, p := range s.cache {
		if !s.isActiveLocked(endpointUID) {
			continue
		}
		cp := *p
		result = append(result, &cp)
	}
	return result
}

// Delete 从内存和 SQLite 删除指定 endpoint 画像。
func (s *ProfileStore) Delete(endpointUID string) error {
	s.mu.Lock()
	delete(s.cache, endpointUID)
	s.mu.Unlock()

	s.flushMu.Lock()
	delete(s.dirtyKeys, endpointUID)
	s.flushMu.Unlock()

	// 直接落盘删除（不等 Flush 周期）
	if _, err := s.db.Exec("DELETE FROM autopilot_endpoint_profiles WHERE endpoint_uid = ?", endpointUID); err != nil {
		return fmt.Errorf("[ProfileStore-Delete] 删除失败 uid=%s: %w", endpointUID, err)
	}
	return nil
}

// DeleteByAccount 删除账号下全部 endpoint 画像。
func (s *ProfileStore) DeleteByAccount(accountUID string) error {
	s.mu.Lock()
	var endpointUIDs []string
	for endpointUID, profile := range s.cache {
		if profile.AccountUID == accountUID {
			delete(s.cache, endpointUID)
			endpointUIDs = append(endpointUIDs, endpointUID)
		}
	}
	s.mu.Unlock()

	s.flushMu.Lock()
	for _, endpointUID := range endpointUIDs {
		delete(s.dirtyKeys, endpointUID)
	}
	s.flushMu.Unlock()

	if _, err := s.db.Exec("DELETE FROM autopilot_endpoint_profiles WHERE account_uid = ?", accountUID); err != nil {
		return fmt.Errorf("[ProfileStore-DeleteByAccount] 删除失败 account=%s: %w", accountUID, err)
	}
	return nil
}

// DeleteByCredential 删除账号内指定凭证的全部 endpoint 画像。
func (s *ProfileStore) DeleteByCredential(accountUID, credentialUID string) error {
	s.mu.Lock()
	var endpointUIDs []string
	for endpointUID, profile := range s.cache {
		if profile.AccountUID == accountUID && profile.CredentialUID == credentialUID {
			delete(s.cache, endpointUID)
			endpointUIDs = append(endpointUIDs, endpointUID)
		}
	}
	s.mu.Unlock()
	s.flushMu.Lock()
	for _, endpointUID := range endpointUIDs {
		delete(s.dirtyKeys, endpointUID)
	}
	s.flushMu.Unlock()
	if _, err := s.db.Exec("DELETE FROM autopilot_endpoint_profiles WHERE account_uid = ? AND credential_uid = ?", accountUID, credentialUID); err != nil {
		return fmt.Errorf("[ProfileStore-DeleteByCredential] 删除失败 account=%s credential=%s: %w", accountUID, credentialUID, err)
	}
	return nil
}

// Flush 将内存中标记为 dirty 的画像批量写入 SQLite。
// 非 dirty 的画像不重复写入；已删除的画像由 Delete 即时落盘。
func (s *ProfileStore) Flush() error {
	s.flushMu.Lock()
	if len(s.dirtyKeys) == 0 {
		s.flushMu.Unlock()
		return nil
	}
	// 取出 dirty keys 并清空
	keys := make([]string, 0, len(s.dirtyKeys))
	for k := range s.dirtyKeys {
		keys = append(keys, k)
	}
	s.dirtyKeys = make(map[string]struct{})
	s.flushMu.Unlock()

	// 批量读取需要写入的画像
	s.mu.RLock()
	profiles := make([]*KeyEndpointProfile, 0, len(keys))
	for _, k := range keys {
		if p, ok := s.cache[k]; ok {
			profiles = append(profiles, p)
		}
	}
	s.mu.RUnlock()

	if len(profiles) == 0 {
		return nil
	}

	// 事务批量 upsert
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("[ProfileStore-Flush] 开启事务失败: %w", err)
	}
	defer errutil.IgnoreDeferred(tx.Rollback)

	stmt, err := tx.Prepare(`
INSERT INTO autopilot_endpoint_profiles (endpoint_uid, account_uid, credential_uid, channel_uid, service_type, base_url, key_hash, profile_json, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(endpoint_uid) DO UPDATE SET
    account_uid = excluded.account_uid,
    credential_uid = excluded.credential_uid,
    channel_uid = excluded.channel_uid,
    service_type = excluded.service_type,
    base_url = excluded.base_url,
    key_hash = excluded.key_hash,
    profile_json = excluded.profile_json,
    updated_at = excluded.updated_at
`)
	if err != nil {
		return fmt.Errorf("[ProfileStore-Flush] 准备语句失败: %w", err)
	}
	defer errutil.IgnoreDeferred(stmt.Close)

	for _, p := range profiles {
		profileJSON, err := json.Marshal(p)
		if err != nil {
			log.Printf("[ProfileStore-Flush] 序列化失败 uid=%s: %v", p.EndpointUID, err)
			continue
		}

		_, err = stmt.Exec(
			p.EndpointUID,
			p.AccountUID,
			p.CredentialUID,
			p.ChannelUID,
			p.ServiceType,
			p.BaseURL,
			p.KeyHash,
			string(profileJSON),
			time.Now().UTC().Format(time.RFC3339),
		)
		if err != nil {
			log.Printf("[ProfileStore-Flush] 写入失败 uid=%s: %v", p.EndpointUID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("[ProfileStore-Flush] 提交事务失败: %w", err)
	}

	return nil
}

// Close 关闭 ProfileStore。先 Flush 剩余 dirty 数据。
// 仅 NewProfileStore（自管连接）会关闭 db；NewProfileStoreWithDB 不关闭。
func (s *ProfileStore) Close() error {
	if err := s.Flush(); err != nil {
		log.Printf("[ProfileStore-Close] Flush 失败: %v", err)
	}

	// 仅自管连接才关闭 db
	if s.dbPath != "" {
		if err := s.db.Close(); err != nil {
			return fmt.Errorf("[ProfileStore-Close] 关闭数据库失败: %w", err)
		}
	}
	return nil
}
