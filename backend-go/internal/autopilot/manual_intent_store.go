package autopilot

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/BenedictKing/ccx/internal/errutil"
	"log"
	"sync"
	"time"
)

// ── ManualIntentStore: 内存缓存 + SQLite 持久化 ──

// ManualIntentStore 管理 ManualRoutingIntent 的内存缓存与 SQLite 持久化。
// Phase 1 shadow：仅存储意图与命中统计，绝不影响真实调度。
// 内存为主读取源，写入双写内存+即时落盘。
type ManualIntentStore struct {
	db *sql.DB

	cache map[string]*ManualRoutingIntent // key = intentUID
	mu    sync.RWMutex
}

// NewManualIntentStoreWithDB 使用外部提供的 *sql.DB 创建 ManualIntentStore。
// 调用方负责 db 的生命周期管理。
func NewManualIntentStoreWithDB(db *sql.DB) (*ManualIntentStore, error) {
	if err := initManualIntentSchema(db); err != nil {
		return nil, fmt.Errorf("[ManualIntentStore-Init] 建表失败: %w", err)
	}

	store := &ManualIntentStore{
		db:    db,
		cache: make(map[string]*ManualRoutingIntent),
	}

	if err := store.loadAll(); err != nil {
		return nil, fmt.Errorf("[ManualIntentStore-Init] 加载意图失败: %w", err)
	}

	log.Printf("[ManualIntentStore-Init] 初始化完成，已加载 %d 条意图", len(store.cache))
	return store, nil
}

// initManualIntentSchema 建表迁移。
// 表结构与设计 §3.8 一致，扩展 hit_count/success_count/failure_count/total_latency_ms 列用于索引查询。
func initManualIntentSchema(db *sql.DB) error {
	schema := `
CREATE TABLE IF NOT EXISTS manual_routing_intents (
    intent_uid       TEXT PRIMARY KEY,
    intent_type      TEXT    NOT NULL,
    channel_kind     TEXT    NOT NULL,
    intent_json      TEXT    NOT NULL,
    status           TEXT    NOT NULL,
    expires_at       TEXT    NOT NULL,
    hit_count        INTEGER NOT NULL DEFAULT 0,
    success_count    INTEGER NOT NULL DEFAULT 0,
    failure_count    INTEGER NOT NULL DEFAULT 0,
    total_latency_ms INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT    NOT NULL,
    updated_at       TEXT    NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_manual_routing_intents_active
    ON manual_routing_intents(channel_kind, status, expires_at);
`
	_, err := db.Exec(schema)
	return err
}

// loadAll 从 SQLite 加载全部意图到内存缓存。
func (s *ManualIntentStore) loadAll() error {
	rows, err := s.db.Query(`
		SELECT intent_uid, intent_json, hit_count, success_count, failure_count, total_latency_ms
		FROM manual_routing_intents`)
	if err != nil {
		return err
	}
	defer errutil.IgnoreDeferred(rows.Close)

	s.mu.Lock()
	defer s.mu.Unlock()

	for rows.Next() {
		var uid, intentJSON string
		var hitCount, successCount, failureCount int
		var totalLatencyMs int64
		if err := rows.Scan(&uid, &intentJSON, &hitCount, &successCount, &failureCount, &totalLatencyMs); err != nil {
			log.Printf("[ManualIntentStore-LoadAll] 跳过损坏行: %v", err)
			continue
		}
		var intent ManualRoutingIntent
		if err := json.Unmarshal([]byte(intentJSON), &intent); err != nil {
			log.Printf("[ManualIntentStore-LoadAll] 反序列化失败 uid=%s: %v", uid, err)
			continue
		}
		// 从列恢复命中统计（防止 JSON 中的值与列不一致，以列为准）
		intent.TrialResult.HitCount = hitCount
		intent.TrialResult.SuccessCount = successCount
		intent.TrialResult.FailureCount = failureCount
		intent.TrialResult.TotalLatencyMs = totalLatencyMs
		if hitCount > 0 {
			intent.TrialResult.AvgLatencyMs = float64(totalLatencyMs) / float64(hitCount)
		}

		// 惰性过期检查：加载时推导一次状态
		if newStatus, changed := intent.DeriveStatus(time.Now()); changed {
			intent.Status = newStatus
		}

		s.cache[uid] = &intent
	}
	return rows.Err()
}

// ── CRUD ──

// Create 创建一条意图。自动填充 CreatedAt / IntentUID（如未设置）、默认 TrafficPercent / Status。
func (s *ManualIntentStore) Create(intent *ManualRoutingIntent) error {
	if err := intent.Validate(); err != nil {
		return err
	}

	now := time.Now().UTC()
	if intent.CreatedAt.IsZero() {
		intent.CreatedAt = now
	}
	if intent.IntentUID == "" {
		intent.IntentUID = GenerateIntentUID(intent.IntentType, intent.ChannelKind, intent.Model, intent.ChannelUID, intent.CreatedAt)
	}
	if intent.TrafficPercent == 0 {
		intent.TrafficPercent = 100
	}
	if !intent.RequireHardConstraints && intent.Status == "" {
		// 默认 true；只有显式设置 false 才关闭
		intent.RequireHardConstraints = true
	}
	intent.Status = IntentStatusActive

	// 写入内存
	s.mu.Lock()
	s.cache[intent.IntentUID] = intent
	s.mu.Unlock()

	// 即时落盘
	return s.persistIntent(intent)
}

// Get 按 intentUID 从内存缓存获取意图副本。不存在返回 nil。
func (s *ManualIntentStore) Get(intentUID string) *ManualRoutingIntent {
	s.mu.RLock()
	intent, ok := s.cache[intentUID]
	s.mu.RUnlock()
	if !ok {
		return nil
	}

	// 惰性过期检查：在返回副本上设置推导状态（不修改缓存，避免 RLock→Lock 升级）
	newStatus, _ := intent.DeriveStatus(time.Now())

	cp := *intent
	cp.Status = newStatus
	return &cp
}

// Delete 从内存和 SQLite 删除指定意图。
func (s *ManualIntentStore) Delete(intentUID string) error {
	s.mu.Lock()
	_, ok := s.cache[intentUID]
	if ok {
		delete(s.cache, intentUID)
	}
	s.mu.Unlock()

	if !ok {
		return ErrIntentNotFound
	}

	if _, err := s.db.Exec("DELETE FROM manual_routing_intents WHERE intent_uid = ?", intentUID); err != nil {
		return fmt.Errorf("[ManualIntentStore-Delete] 删除失败 uid=%s: %w", intentUID, err)
	}
	return nil
}

// ── 查询 ──

// ListActive 返回所有 status=active 的意图副本。
// 内部惰性检查：过期或超预算的意图自动转态后不包含在结果中。
func (s *ManualIntentStore) ListActive() []*ManualRoutingIntent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	// 显式初始化为空切片：nil slice 会被 JSON 序列化成 null，
	// 前端拿到 null 后对其取 .length 会抛错并导致整页渲染失败。
	result := make([]*ManualRoutingIntent, 0, len(s.cache))
	var stateChanged []*ManualRoutingIntent

	for _, intent := range s.cache {
		newStatus, changed := intent.DeriveStatus(now)
		if changed {
			intent.Status = newStatus
			stateChanged = append(stateChanged, intent)
		}
		if intent.Status == IntentStatusActive {
			cp := *intent
			result = append(result, &cp)
		}
	}

	// 异步落盘变更的状态（不阻塞读路径）
	if len(stateChanged) > 0 {
		go func() {
			for _, intent := range stateChanged {
				if err := s.persistIntent(intent); err != nil {
					log.Printf("[ManualIntentStore-ListActive] 异步落盘变更状态失败 uid=%s: %v", intent.IntentUID, err)
				}
			}
		}()
	}

	return result
}

// ListAll 返回全部意图副本（含 active/expired/exhausted/disabled）。
func (s *ManualIntentStore) ListAll() []*ManualRoutingIntent {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	result := make([]*ManualRoutingIntent, 0, len(s.cache))
	for _, intent := range s.cache {
		newStatus, _ := intent.DeriveStatus(now)
		cp := *intent
		cp.Status = newStatus
		result = append(result, &cp)
	}
	return result
}

// ── 命中统计 ──

// RecordHit 记录一次命中。success=true 时计入成功，否则计入失败。
// latencyMs 为本次请求延迟（毫秒）。
// Phase 1 shadow：仅更新统计，不影响真实调度。
func (s *ManualIntentStore) RecordHit(intentUID string, success bool, latencyMs int64) error {
	s.mu.Lock()
	intent, ok := s.cache[intentUID]
	if !ok {
		s.mu.Unlock()
		return ErrIntentNotFound
	}

	intent.TrialResult.HitCount++
	if success {
		intent.TrialResult.SuccessCount++
	} else {
		intent.TrialResult.FailureCount++
	}
	intent.TrialResult.TotalLatencyMs += latencyMs
	if intent.TrialResult.HitCount > 0 {
		intent.TrialResult.AvgLatencyMs = float64(intent.TrialResult.TotalLatencyMs) / float64(intent.TrialResult.HitCount)
	}

	// 惰性状态推导（DeriveStatus 是纯函数，必须赋值回 intent.Status）
	if newStatus, changed := intent.DeriveStatus(time.Now()); changed {
		intent.Status = newStatus
	}
	s.mu.Unlock()

	// 即时落盘命中统计
	return s.persistHitStats(intentUID, intent)
}

// RecordFallback 记录一次 fallback 到默认路由。
func (s *ManualIntentStore) RecordFallback(intentUID string) error {
	s.mu.Lock()
	intent, ok := s.cache[intentUID]
	if !ok {
		s.mu.Unlock()
		return ErrIntentNotFound
	}
	intent.TrialResult.FallbackCount++
	s.mu.Unlock()

	return s.persistHitStats(intentUID, intent)
}

// ── 持久化辅助 ──

// persistIntent 将意图全量写入 SQLite。
func (s *ManualIntentStore) persistIntent(intent *ManualRoutingIntent) error {
	intentJSON, err := json.Marshal(intent)
	if err != nil {
		return fmt.Errorf("[ManualIntentStore-Persist] 序列化失败 uid=%s: %w", intent.IntentUID, err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.Exec(`
INSERT INTO manual_routing_intents
    (intent_uid, intent_type, channel_kind, intent_json, status, expires_at,
     hit_count, success_count, failure_count, total_latency_ms, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(intent_uid) DO UPDATE SET
    intent_type      = excluded.intent_type,
    channel_kind     = excluded.channel_kind,
    intent_json      = excluded.intent_json,
    status           = excluded.status,
    expires_at       = excluded.expires_at,
    hit_count        = excluded.hit_count,
    success_count    = excluded.success_count,
    failure_count    = excluded.failure_count,
    total_latency_ms = excluded.total_latency_ms,
    updated_at       = excluded.updated_at
`,
		intent.IntentUID,
		string(intent.IntentType),
		intent.ChannelKind,
		string(intentJSON),
		string(intent.Status),
		intent.ExpiresAt.UTC().Format(time.RFC3339),
		intent.TrialResult.HitCount,
		intent.TrialResult.SuccessCount,
		intent.TrialResult.FailureCount,
		intent.TrialResult.TotalLatencyMs,
		intent.CreatedAt.UTC().Format(time.RFC3339),
		now,
	)
	if err != nil {
		return fmt.Errorf("[ManualIntentStore-Persist] 写入失败 uid=%s: %w", intent.IntentUID, err)
	}
	return nil
}

// persistHitStats 仅更新命中统计列和 intent_json（避免覆盖其他字段的并发变更）。
func (s *ManualIntentStore) persistHitStats(intentUID string, intent *ManualRoutingIntent) error {
	intentJSON, err := json.Marshal(intent)
	if err != nil {
		return fmt.Errorf("[ManualIntentStore-PersistHit] 序列化失败 uid=%s: %w", intentUID, err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.Exec(`
UPDATE manual_routing_intents
SET hit_count = ?, success_count = ?, failure_count = ?,
    total_latency_ms = ?, status = ?, intent_json = ?, updated_at = ?
WHERE intent_uid = ?`,
		intent.TrialResult.HitCount,
		intent.TrialResult.SuccessCount,
		intent.TrialResult.FailureCount,
		intent.TrialResult.TotalLatencyMs,
		string(intent.Status),
		string(intentJSON),
		now,
		intentUID,
	)
	if err != nil {
		return fmt.Errorf("[ManualIntentStore-PersistHit] 更新失败 uid=%s: %w", intentUID, err)
	}
	return nil
}

// Close 关闭 ManualIntentStore。当前无需特殊清理。
func (s *ManualIntentStore) Close() error {
	return nil
}
