package metrics

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

type PersistenceDiagnostics struct {
	Persistent      bool       `json:"persistent"`
	RetentionDays   int        `json:"retentionDays"`
	BufferedWrites  int        `json:"bufferedWrites"`
	WriteFailures   uint64     `json:"writeFailures"`
	DroppedWrites   uint64     `json:"droppedWrites"`
	CleanupFailures uint64     `json:"cleanupFailures"`
	LastWriteAt     *time.Time `json:"lastWriteAt,omitempty"`
	LastCleanupAt   *time.Time `json:"lastCleanupAt,omitempty"`
	LastCleanupOK   bool       `json:"lastCleanupOK"`
	RecordCount     int64      `json:"recordCount"`
	OldestRecordAt  *time.Time `json:"oldestRecordAt,omitempty"`
	QueryFailed     bool       `json:"queryFailed"`
}

// 持久化状态只保留计数与时间，不记录数据库错误文本、文件路径或配置内容。
type persistenceHealth struct {
	mu     sync.Mutex
	status PersistenceDiagnostics
}

func (h *persistenceHealth) recordWrite(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err != nil {
		h.status.WriteFailures++
		return
	}
	now := time.Now()
	h.status.LastWriteAt = &now
}

func (h *persistenceHealth) recordCleanup(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	h.status.LastCleanupAt, h.status.LastCleanupOK = &now, err == nil
	if err != nil {
		h.status.CleanupFailures++
	}
}

func (h *persistenceHealth) recordDrop(count int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.status.DroppedWrites += uint64(count)
}

func (h *persistenceHealth) diagnostics(db *sql.DB, table, column string, unit time.Duration, days, buffered int, dropped uint64) PersistenceDiagnostics {
	h.mu.Lock()
	status := h.status
	if status.LastWriteAt != nil {
		at := *status.LastWriteAt
		status.LastWriteAt = &at
	}
	if status.LastCleanupAt != nil {
		at := *status.LastCleanupAt
		status.LastCleanupAt = &at
	}
	h.mu.Unlock()
	status.Persistent, status.RetentionDays, status.BufferedWrites = db != nil, days, buffered
	status.DroppedWrites += dropped
	if db == nil {
		return status
	}
	cutoff := time.Now().Add(-time.Duration(days)*24*time.Hour).UnixNano() / int64(unit)
	var oldest int64
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := db.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(MIN("+column+"),0) FROM "+table+" WHERE "+column+">=?", cutoff).Scan(&status.RecordCount, &oldest)
	status.QueryFailed = err != nil
	if err == nil && oldest > 0 {
		at := time.Unix(0, oldest*int64(unit))
		status.OldestRecordAt = &at
	}
	return status
}

func (s *SQLiteStore) Diagnostics() PersistenceDiagnostics {
	if s == nil {
		return PersistenceDiagnostics{}
	}
	s.bufferMu.Lock()
	buffered := len(s.writeBuffer)
	s.bufferMu.Unlock()
	return s.health.diagnostics(s.db, "request_records", "timestamp", time.Second, s.retentionDays, buffered, 0)
}

func (s *RoutingStore) Diagnostics() PersistenceDiagnostics {
	return s.health.diagnostics(s.db, "routing_records_v1", "received_at", time.Nanosecond, s.retentionDays, len(s.writes), s.dropped.Load())
}

func (s *KeyOperationStore) Diagnostics() PersistenceDiagnostics {
	return s.health.diagnostics(s.db, "key_operations_v1", "occurred_at", time.Nanosecond, s.retention, len(s.writes), s.dropped.Load())
}
