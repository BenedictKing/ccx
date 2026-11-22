package metrics

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

const maxKeyOperationMemory = 1000

type KeyOperationCoverage struct {
	Persistent    bool      `json:"persistent"`
	RetentionDays int       `json:"retentionDays"`
	StartedAt     time.Time `json:"startedAt"`
	DroppedWrites uint64    `json:"droppedWrites"`
	Limited       bool      `json:"limited"`
}

// KeyOperationStore 接收脱敏的配置事件；配置变更不等待数据库写入。
type KeyOperationStore struct {
	mu        sync.RWMutex
	db        *sql.DB
	events    []config.KeyOperationEvent
	sequence  int64
	retention int
	startedAt time.Time
	writes    chan config.KeyOperationEvent
	done      chan struct{}
	closed    bool
	closeOnce sync.Once
	dropped   atomic.Uint64
	health    persistenceHealth
}

func NewKeyOperationStore(store *SQLiteStore, days int) (*KeyOperationStore, error) {
	if days < 1 || days > 366 {
		return nil, fmt.Errorf("invalid Key operation retention")
	}
	s := &KeyOperationStore{retention: days, startedAt: time.Now(), writes: make(chan config.KeyOperationEvent, 256), done: make(chan struct{})}
	if store != nil {
		s.db = store.db
		_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS key_operations_v1 (sequence INTEGER PRIMARY KEY, api_type TEXT NOT NULL, channel_uid TEXT NOT NULL, key_id TEXT NOT NULL, occurred_at INTEGER NOT NULL, payload TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_key_operations_scope ON key_operations_v1(api_type, channel_uid, key_id, sequence);
CREATE TABLE IF NOT EXISTS key_operation_metadata_v1 (id INTEGER PRIMARY KEY CHECK(id=1), started_at INTEGER NOT NULL, dropped INTEGER NOT NULL DEFAULT 0);`)
		if err != nil {
			return nil, err
		}
		if err := s.db.QueryRow("SELECT COALESCE(MAX(sequence),0) FROM key_operations_v1").Scan(&s.sequence); err != nil {
			return nil, err
		}
		if _, err := s.db.Exec("INSERT OR IGNORE INTO key_operation_metadata_v1(id,started_at) VALUES(1,?)", s.startedAt.UnixNano()); err != nil {
			return nil, err
		}
		var started int64
		var dropped uint64
		if err := s.db.QueryRow("SELECT started_at,dropped FROM key_operation_metadata_v1 WHERE id=1").Scan(&started, &dropped); err != nil {
			return nil, err
		}
		s.startedAt = time.Unix(0, started)
		s.dropped.Store(dropped)
	}
	go s.writeLoop()
	return s, nil
}

func (s *KeyOperationStore) Record(event config.KeyOperationEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.sequence++
	event.Sequence = s.sequence
	s.events = append(s.events, event)
	if len(s.events) > maxKeyOperationMemory {
		copy(s.events, s.events[len(s.events)-maxKeyOperationMemory:])
		s.events = s.events[:maxKeyOperationMemory]
	}
	select {
	case s.writes <- event:
	default:
		s.dropped.Add(1)
	}
}

func (s *KeyOperationStore) Query(apiType, channelUID, keyID string, before int64, limit int) ([]config.KeyOperationEvent, bool, KeyOperationCoverage, error) {
	if limit < 1 || limit > 100 {
		return nil, false, KeyOperationCoverage{}, fmt.Errorf("invalid operation page limit")
	}
	cutoff := time.Now().Add(-time.Duration(s.retention) * 24 * time.Hour)
	matched := func(event config.KeyOperationEvent) bool {
		return event.APIType == apiType && event.ChannelUID == channelUID && (keyID == "" || event.KeyID == keyID) &&
			(before == 0 || event.Sequence < before) && !event.OccurredAt.Before(cutoff)
	}
	bySequence := map[int64]config.KeyOperationEvent{}
	s.mu.RLock()
	coverage := KeyOperationCoverage{Persistent: s.db != nil, RetentionDays: s.retention, StartedAt: s.startedAt, DroppedWrites: s.dropped.Load(), Limited: s.db == nil && s.sequence > maxKeyOperationMemory}
	for _, event := range s.events {
		if matched(event) {
			bySequence[event.Sequence] = event
		}
	}
	s.mu.RUnlock()
	if s.db != nil {
		rows, err := s.db.Query(`SELECT payload FROM key_operations_v1 WHERE api_type=? AND channel_uid=? AND (?='' OR key_id=?) AND (?=0 OR sequence<?) AND occurred_at>=? ORDER BY sequence DESC LIMIT ?`, apiType, channelUID, keyID, keyID, before, before, cutoff.UnixNano(), limit+1)
		if err != nil {
			return nil, false, coverage, err
		}
		defer rows.Close()
		for rows.Next() {
			var payload string
			var event config.KeyOperationEvent
			if err := rows.Scan(&payload); err != nil {
				return nil, false, coverage, err
			}
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				return nil, false, coverage, err
			}
			bySequence[event.Sequence] = event
		}
		if err := rows.Err(); err != nil {
			return nil, false, coverage, err
		}
	}
	events := make([]config.KeyOperationEvent, 0, len(bySequence))
	for _, event := range bySequence {
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Sequence > events[j].Sequence })
	more := len(events) > limit
	if more {
		events = events[:limit]
	}
	return events, more, coverage, nil
}

func (s *KeyOperationStore) writeLoop() {
	defer close(s.done)
	timer := time.NewTicker(time.Hour)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-s.writes:
			if !ok {
				return
			}
			if s.db != nil {
				payload, err := json.Marshal(event)
				if err == nil {
					_, err = s.db.Exec("INSERT INTO key_operations_v1(sequence,api_type,channel_uid,key_id,occurred_at,payload) VALUES(?,?,?,?,?,?)", event.Sequence, event.APIType, event.ChannelUID, event.KeyID, event.OccurredAt.UnixNano(), string(payload))
				}
				if err != nil {
					s.dropped.Add(1)
				}
				s.health.recordWrite(err)
				_, _ = s.db.Exec("UPDATE key_operation_metadata_v1 SET dropped=? WHERE id=1", s.dropped.Load())
			}
		case <-timer.C:
			if s.db != nil {
				_, err := s.db.Exec("DELETE FROM key_operations_v1 WHERE occurred_at<?", time.Now().Add(-time.Duration(s.retention)*24*time.Hour).UnixNano())
				s.health.recordCleanup(err)
			}
		}
	}
}

func (s *KeyOperationStore) Close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		close(s.writes)
		s.mu.Unlock()
		<-s.done
	})
}
