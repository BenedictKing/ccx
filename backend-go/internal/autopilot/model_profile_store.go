package autopilot

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/errutil"
	"log"
)

// ── ModelProfileStore: 内存缓存 + SQLite 异步持久化 ──

// ModelProfileStore 管理 ModelProfile 的内存缓存与 SQLite 持久化。
// 每行以 profile_json TEXT 列存储完整 ModelProfile JSON。
// 复用 ProfileStore.DB() 返回的 *sql.DB（同一 autopilot.db 文件）。
// 内存为主读取源，写入双写内存+异步落盘。
type ModelProfileStore struct {
	db     *sql.DB
	dbPath string // 仅由 NewModelProfileStore 设置，NewModelProfileStoreWithDB 为空

	cache map[string]*ModelProfile // key = modelProfileKey(profile)
	mu    sync.RWMutex
	// activeBindings 来自当前配置的 endpoint 清单，只过滤运行态读取，
	// 不删除 SQLite 中用于审计的历史模型画像。
	activeBindings       map[string]struct{}
	activeInventoryReady bool

	flushMu sync.Mutex

	dirtyKeys   map[string]struct{}          // 待落盘的 composite key 集合
	deletedKeys map[string]modelProfileRowID // 待删除落盘的 composite key 集合
}

// modelProfileRowID 是模型画像的持久化主键四元组，供删除落盘使用。
type modelProfileRowID struct {
	channelUID  string
	channelKind string
	metricsKey  string
	modelID     string
}

// DB 返回底层 *sql.DB，供 Manager 内部复用连接。
func (s *ModelProfileStore) DB() *sql.DB { return s.db }

// NewModelProfileStore 创建 ModelProfileStore，自行管理 SQLite 连接。
// dbPath 为数据库文件路径，启动时自动建表并 LoadAll 回内存。
func NewModelProfileStore(dbPath string) (*ModelProfileStore, error) {
	// 确保目录存在（复用 ProfileStore 的路径）
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("[ModelProfileStore-Init] 打开数据库失败: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	store, err := newModelProfileStoreFromDB(db, dbPath)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// NewModelProfileStoreWithDB 使用外部提供的 *sql.DB 创建 ModelProfileStore（便于测试/复用连接）。
// 调用方负责 db 的生命周期管理；Close() 不会关闭该 db。
func NewModelProfileStoreWithDB(db *sql.DB) (*ModelProfileStore, error) {
	return newModelProfileStoreFromDB(db, "")
}

func newModelProfileStoreFromDB(db *sql.DB, dbPath string) (*ModelProfileStore, error) {
	if err := initModelProfileStoreSchema(db); err != nil {
		return nil, fmt.Errorf("[ModelProfileStore-Init] 建表失败: %w", err)
	}

	store := &ModelProfileStore{
		db:          db,
		dbPath:      dbPath,
		cache:       make(map[string]*ModelProfile),
		dirtyKeys:   make(map[string]struct{}),
		deletedKeys: make(map[string]modelProfileRowID),
	}

	if err := store.loadAll(); err != nil {
		return nil, fmt.Errorf("[ModelProfileStore-Init] 加载画像失败: %w", err)
	}

	log.Printf("[ModelProfileStore-Init] 初始化完成，已加载 %d 条模型画像", len(store.cache))
	return store, nil
}

// initModelProfileStoreSchema 建表迁移。
// 表结构与设计 doc §12.2 model_profiles 一致，主键 (channel_uid, channel_kind, metrics_key, model_id)。
func initModelProfileStoreSchema(db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS autopilot_model_profiles (
    channel_uid  TEXT    NOT NULL,
    channel_id   INTEGER NOT NULL,
    channel_kind TEXT    NOT NULL,
    service_type TEXT    NOT NULL,
    metrics_key  TEXT    NOT NULL,
    model_id     TEXT    NOT NULL,
    profile_json TEXT    NOT NULL,
    updated_at   TEXT    NOT NULL,
    PRIMARY KEY (channel_uid, channel_kind, metrics_key, model_id)
);
CREATE INDEX IF NOT EXISTS idx_model_profiles_channel ON autopilot_model_profiles(channel_uid);
CREATE INDEX IF NOT EXISTS idx_model_profiles_kind_index ON autopilot_model_profiles(channel_kind, channel_id);
`
	_, err := db.Exec(schema)
	return err
}

// modelProfileKey 生成 ModelProfile 的内存缓存复合键。
func modelProfileKey(p *ModelProfile) string {
	return p.ChannelUID + "|" + p.ChannelKind + "|" + p.MetricsKey + "|" + p.ModelID
}

func modelProfileBindingKey(channelUID, channelKind, metricsKey string) string {
	return channelUID + "|" + channelKind + "|" + metricsKey
}

// parseModelProfileKey 解析复合键为四元组。

// loadAll 从 SQLite 加载全部画像到内存缓存。
func (s *ModelProfileStore) loadAll() error {
	rows, err := s.db.Query("SELECT channel_uid, channel_kind, metrics_key, model_id, profile_json FROM autopilot_model_profiles")
	if err != nil {
		return err
	}
	defer errutil.IgnoreDeferred(rows.Close)

	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for rows.Next() {
		var channelUID, channelKind, metricsKey, modelID, profileJSON string
		if err := rows.Scan(&channelUID, &channelKind, &metricsKey, &modelID, &profileJSON); err != nil {
			log.Printf("[ModelProfileStore-LoadAll] 跳过损坏行: %v", err)
			continue
		}
		var profile ModelProfile
		if err := json.Unmarshal([]byte(profileJSON), &profile); err != nil {
			log.Printf("[ModelProfileStore-LoadAll] 反序列化失败 key=%s|%s|%s|%s: %v",
				channelUID, channelKind, metricsKey, modelID, err)
			continue
		}
		s.cache[modelProfileKey(&profile)] = &profile
		count++
	}
	return rows.Err()
}

// ── 公开方法 ──

// Upsert 插入或更新一条模型画像。
// 内存立即生效，标记 dirty 后由 Flush 落盘。
func (s *ModelProfileStore) Upsert(profile *ModelProfile) error {
	if profile.ChannelUID == "" || profile.ModelID == "" {
		return fmt.Errorf("[ModelProfileStore-Upsert] channel_uid 和 model_id 不能为空")
	}

	key := modelProfileKey(profile)

	s.mu.Lock()
	s.cache[key] = profile
	s.mu.Unlock()

	s.flushMu.Lock()
	s.dirtyKeys[key] = struct{}{}
	// 同一 key 先删后写以写入为准：从待删集合移除，避免 Flush 时误删。
	delete(s.deletedKeys, key)
	s.flushMu.Unlock()

	return nil
}

// ReconcileModels 把 (channelUID, channelKind, metricsKey) binding 下的模型画像
// 收敛到 keep 集合：不在清单内的旧行（上游已下架/更名的模型）从内存与 SQLite 移除，
// 返回被移除的 modelID 列表（字典序）。
// keep 为空时是 no-op：空清单通常意味着本端点探测失败，不允许据此清空画像。
func (s *ModelProfileStore) ReconcileModels(channelUID, channelKind, metricsKey string, keep map[string]struct{}) []string {
	if s == nil || len(keep) == 0 {
		return nil
	}

	var removed []string
	s.mu.Lock()
	for k, p := range s.cache {
		if p.ChannelUID != channelUID || p.ChannelKind != channelKind || p.MetricsKey != metricsKey {
			continue
		}
		if _, ok := keep[p.ModelID]; ok {
			continue
		}
		delete(s.cache, k)
		removed = append(removed, p.ModelID)
	}
	s.mu.Unlock()

	if len(removed) == 0 {
		return nil
	}
	sort.Strings(removed)

	s.flushMu.Lock()
	for _, modelID := range removed {
		s.deletedKeys[channelUID+"|"+channelKind+"|"+metricsKey+"|"+modelID] = modelProfileRowID{
			channelUID:  channelUID,
			channelKind: channelKind,
			metricsKey:  metricsKey,
			modelID:     modelID,
		}
	}
	s.flushMu.Unlock()

	return removed
}

// Get 返回指定复合主键对应的模型画像副本。不存在时返回 nil。
func (s *ModelProfileStore) Get(channelUID, channelKind, metricsKey, modelID string) *ModelProfile {
	key := modelProfileKey(&ModelProfile{
		ChannelUID:  channelUID,
		ChannelKind: channelKind,
		MetricsKey:  metricsKey,
		ModelID:     modelID,
	})

	s.mu.RLock()
	defer s.mu.RUnlock()
	profile := s.cache[key]
	if profile == nil {
		return nil
	}
	copy := *profile
	return &copy
}

// GetModelProfiles 返回指定 (channelUID, channelKind, metricsKey) 下的全部模型画像副本。
// 这是 ModelResolver 设计 doc §5.4 的直接查询签名；返回 slice 可能为空但绝不为 nil。
func (s *ModelProfileStore) GetModelProfiles(channelUID, channelKind, metricsKey string) []ModelProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.isActiveBindingLocked(channelUID, channelKind, metricsKey) {
		return []ModelProfile{}
	}

	var result []ModelProfile
	for _, p := range s.cache {
		if p.ChannelUID == channelUID && p.ChannelKind == channelKind && p.MetricsKey == metricsKey {
			result = append(result, *p) // 返回副本
		}
	}
	return result
}

// ReplaceActiveBindings 原子替换当前配置可达的模型画像 binding 清单。
// 清单仅存在内存中；空清单表示当前没有可达 endpoint，而不是未初始化。
func (s *ModelProfileStore) ReplaceActiveBindings(bindings map[string]struct{}) {
	active := make(map[string]struct{}, len(bindings))
	for binding := range bindings {
		if binding != "" {
			active[binding] = struct{}{}
		}
	}

	s.mu.Lock()
	s.activeBindings = active
	s.activeInventoryReady = true
	s.mu.Unlock()
}

func (s *ModelProfileStore) isActiveBindingLocked(channelUID, channelKind, metricsKey string) bool {
	if !s.activeInventoryReady {
		return true
	}
	_, ok := s.activeBindings[modelProfileBindingKey(channelUID, channelKind, metricsKey)]
	return ok
}

// ListByChannel 返回指定 channelUID 下的全部模型画像副本。
func (s *ModelProfileStore) ListByChannel(channelUID string) []ModelProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []ModelProfile
	for _, p := range s.cache {
		if p.ChannelUID == channelUID {
			result = append(result, *p)
		}
	}
	return result
}

// ListActiveByChannel 返回当前配置仍可达的指定渠道模型画像。
// 有效清单尚未初始化时 fail-open，等价于 ListByChannel。
func (s *ModelProfileStore) ListActiveByChannel(channelUID string) []ModelProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []ModelProfile
	for _, p := range s.cache {
		if p.ChannelUID == channelUID && s.isActiveBindingLocked(p.ChannelUID, p.ChannelKind, p.MetricsKey) {
			result = append(result, *p)
		}
	}
	return result
}

// Flush 将内存中标记为 dirty 的画像批量写入 SQLite。
// 非 dirty 的画像不重复写入。
func (s *ModelProfileStore) Flush() error {
	s.flushMu.Lock()
	dirty := s.dirtyKeys
	deleted := s.deletedKeys
	s.dirtyKeys = make(map[string]struct{})
	s.deletedKeys = make(map[string]modelProfileRowID)
	s.flushMu.Unlock()

	if len(dirty) == 0 && len(deleted) == 0 {
		return nil
	}

	s.mu.RLock()
	profiles := make([]*ModelProfile, 0, len(dirty))
	for k := range dirty {
		if p, ok := s.cache[k]; ok {
			profiles = append(profiles, p)
		}
	}
	s.mu.RUnlock()

	if len(profiles) == 0 && len(deleted) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("[ModelProfileStore-Flush] 开启事务失败: %w", err)
	}
	defer errutil.IgnoreDeferred(tx.Rollback)

	// 先执行删除：同一 key 若随后被 UPSERT（删除后重新发现），最终以写入为准。
	if len(deleted) > 0 {
		delStmt, err := tx.Prepare(`DELETE FROM autopilot_model_profiles
WHERE channel_uid = ? AND channel_kind = ? AND metrics_key = ? AND model_id = ?`)
		if err != nil {
			return fmt.Errorf("[ModelProfileStore-Flush] 准备删除语句失败: %w", err)
		}
		for _, id := range deleted {
			if _, err := delStmt.Exec(id.channelUID, id.channelKind, id.metricsKey, id.modelID); err != nil {
				log.Printf("[ModelProfileStore-Flush] 删除失败 model=%s channel=%s: %v", id.modelID, id.channelUID, err)
			}
		}
		defer errutil.IgnoreDeferred(delStmt.Close)
	}

	stmt, err := tx.Prepare(`
INSERT INTO autopilot_model_profiles (channel_uid, channel_id, channel_kind, service_type, metrics_key, model_id, profile_json, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(channel_uid, channel_kind, metrics_key, model_id) DO UPDATE SET
    channel_id   = excluded.channel_id,
    service_type = excluded.service_type,
    profile_json = excluded.profile_json,
    updated_at   = excluded.updated_at
`)
	if err != nil {
		return fmt.Errorf("[ModelProfileStore-Flush] 准备语句失败: %w", err)
	}
	defer errutil.IgnoreDeferred(stmt.Close)

	for _, p := range profiles {
		profileJSON, err := json.Marshal(p)
		if err != nil {
			log.Printf("[ModelProfileStore-Flush] 序列化失败 key=%s: %v", modelProfileKey(p), err)
			continue
		}

		_, err = stmt.Exec(
			p.ChannelUID,
			p.ChannelID,
			p.ChannelKind,
			p.ServiceType,
			p.MetricsKey,
			p.ModelID,
			string(profileJSON),
			time.Now().UTC().Format(time.RFC3339),
		)
		if err != nil {
			log.Printf("[ModelProfileStore-Flush] 写入失败 key=%s: %v", modelProfileKey(p), err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("[ModelProfileStore-Flush] 提交事务失败: %w", err)
	}

	log.Printf("[ModelProfileStore-Flush] 已落盘 %d 条模型画像，删除 %d 条", len(profiles), len(deleted))
	return nil
}

// Close 关闭 ModelProfileStore。先 Flush 剩余 dirty 数据。
// 仅 NewModelProfileStore（自管连接）会关闭 db；NewModelProfileStoreWithDB 不关闭。
func (s *ModelProfileStore) Close() error {
	if err := s.Flush(); err != nil {
		log.Printf("[ModelProfileStore-Close] Flush 失败: %v", err)
	}
	if s.dbPath != "" {
		if err := s.db.Close(); err != nil {
			return fmt.Errorf("[ModelProfileStore-Close] 关闭数据库失败: %w", err)
		}
	}
	return nil
}
