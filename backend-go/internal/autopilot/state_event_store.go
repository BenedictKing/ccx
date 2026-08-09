package autopilot

import (
	"database/sql"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/eventbus"
)

// ── StateEventStore（Phase B.1：跨模块状态事件持久化）──
//
// 与 ProfileChangelogStore 形状一致，但承载 eventbus.Event。
// 内存环形保留 + SQLite 落盘（30 天滚动），用于前端拉历史对齐。
// 仅关键状态事件（熔断、Key）会落盘；高频请求类不接此 store。

const (
	stateEventMaxRecords      = 500
	stateEventRetentionDays   = 30
	stateEventPruneEveryWrite = 100
)

// StateEventStore 管理跨模块状态事件的内存环形 + SQLite 持久化。
type StateEventStore struct {
	db *sql.DB

	records []*eventbus.Event
	mu      sync.RWMutex

	writeCount atomic.Int64
}

// NewStateEventStoreWithDB 使用外部 *sql.DB 创建。db 为 nil 时仅内存环形，不落盘（fail-safe）。
func NewStateEventStoreWithDB(db *sql.DB) (*StateEventStore, error) {
	if db != nil {
		if err := initStateEventSchema(db); err != nil {
			return nil, err
		}
	}
	store := &StateEventStore{
		db:      db,
		records: make([]*eventbus.Event, 0, stateEventMaxRecords),
	}
	if db != nil {
		if err := store.loadRecent(); err != nil {
			log.Printf("[StateEventStore] 警告: 加载最近事件失败: %v", err)
		}
	}
	return store, nil
}

func initStateEventSchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS state_events (
    uid TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    scope TEXT NOT NULL,
    subject TEXT,
    channel_kind TEXT,
    from_state TEXT,
    to_state TEXT,
    cause TEXT,
    payload_json TEXT,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_state_events_created_at ON state_events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_state_events_subject ON state_events(subject, created_at DESC);
	`)
	return err
}

// Record 写入一条事件（内存 + SQLite）。事件 UID 由 bus.EnsureUID 保证非空。
func (s *StateEventStore) Record(ev eventbus.Event) {
	if s == nil {
		return
	}
	if ev.UID == "" {
		ev.EnsureUID()
	}
	evCopy := ev
	s.mu.Lock()
	s.records = append(s.records, &evCopy)
	if len(s.records) > stateEventMaxRecords {
		s.records = append(s.records[:0], s.records[len(s.records)-stateEventMaxRecords:]...)
	}
	writes := s.writeCount.Add(1)
	shouldPrune := writes%stateEventPruneEveryWrite == 0
	s.mu.Unlock()

	if s.db != nil {
		if err := s.persist(&evCopy); err != nil {
			log.Printf("[StateEventStore] 警告: 持久化状态事件失败 (uid=%s): %v", evCopy.UID, err)
		}
		if shouldPrune {
			if err := s.pruneExpired(); err != nil {
				log.Printf("[StateEventStore] 警告: 清理过期事件失败: %v", err)
			}
		}
	}
}

// ListRecent 返回最近 n 条（按时间降序）。
func (s *StateEventStore) ListRecent(n int) []*eventbus.Event {
	if s == nil {
		return nil
	}
	if n <= 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if n > len(s.records) {
		n = len(s.records)
	}
	out := make([]*eventbus.Event, n)
	copy(out, s.records[len(s.records)-n:])
	// 返回时间降序
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// ListBySubject 返回指定 subject 的最近 n 条（按时间降序）。
func (s *StateEventStore) ListBySubject(subject string, n int) []*eventbus.Event {
	if s == nil || subject == "" {
		return nil
	}
	if n <= 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*eventbus.Event, 0, n)
	for i := len(s.records) - 1; i >= 0 && len(out) < n; i-- {
		if s.records[i].Subject == subject {
			out = append(out, s.records[i])
		}
	}
	return out
}

// loadRecent 启动时从 SQLite 加载最近 stateEventMaxRecords 条到内存环形。
func (s *StateEventStore) loadRecent() error {
	rows, err := s.db.Query(`
SELECT uid, type, scope, subject, channel_kind, from_state, to_state, cause, payload_json, created_at
FROM state_events
ORDER BY created_at DESC
LIMIT ?`, stateEventMaxRecords)
	if err != nil {
		return err
	}
	defer errutil.IgnoreDeferred(rows.Close)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = s.records[:0]
	for rows.Next() {
		ev, err := scanStateEventRow(rows)
		if err != nil {
			return err
		}
		// 按时间正序追加（最旧在前），与 ListRecent 的翻转一致
		s.records = append(s.records, ev)
	}
	return rows.Err()
}

func scanStateEventRow(rows *sql.Rows) (*eventbus.Event, error) {
	var (
		ev         eventbus.Event
		payloadStr sql.NullString
		createdAt  int64
		subject    sql.NullString
		kind       sql.NullString
		fromSt     sql.NullString
		toSt       sql.NullString
		cause      sql.NullString
	)
	if err := rows.Scan(&ev.UID, &ev.Type, &ev.Scope, &subject, &kind, &fromSt, &toSt, &cause, &payloadStr, &createdAt); err != nil {
		return nil, err
	}
	ev.Subject = subject.String
	ev.ChannelKind = kind.String
	ev.From = fromSt.String
	ev.To = toSt.String
	ev.Cause = cause.String
	ev.CreatedAt = time.Unix(0, createdAt).UTC()
	if payloadStr.Valid && payloadStr.String != "" {
		// Payload 反序列化在此可加；当前不解析 JSON 字段，前端按需追加
		ev.Payload = nil
	}
	return &ev, nil
}

func (s *StateEventStore) persist(ev *eventbus.Event) error {
	if s.db == nil {
		return nil
	}
	_, err := s.db.Exec(`
INSERT OR REPLACE INTO state_events (uid, type, scope, subject, channel_kind, from_state, to_state, cause, payload_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.UID, ev.Type, ev.Scope, ev.Subject, ev.ChannelKind, ev.From, ev.To, ev.Cause, "", ev.CreatedAt.UnixNano())
	return err
}

func (s *StateEventStore) pruneExpired() error {
	if s.db == nil {
		return nil
	}
	cutoff := time.Now().Add(-stateEventRetentionDays * 24 * time.Hour).UnixNano()
	_, err := s.db.Exec(`DELETE FROM state_events WHERE created_at < ?`, cutoff)
	return err
}
