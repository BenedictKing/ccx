package metrics

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	RoutingStatusSuccess           = "success"
	RoutingStatusFailed            = "failed"
	RoutingStatusCancelled         = "cancelled"
	RoutingStatusInProgress        = "in_progress"
	RoutingStatusUnknown           = "unknown"
	MappingOutcomeUnmatched        = "unmatched"
	MappingOutcomeMatchedUnchanged = "matched_unchanged"
	MappingOutcomeChanged          = "changed"
)

// RoutingRequest 保存一次逻辑请求的调用时快照，不包含请求正文或密钥。
type RoutingRequest struct {
	GatewayRequestID string           `json:"gatewayRequestId"`
	APIType          string           `json:"apiType"`
	RequestedModel   string           `json:"requestedModel"`
	Operation        string           `json:"operation,omitempty"`
	RequestSource    string           `json:"requestSource"`
	ReceivedAt       time.Time        `json:"receivedAt"`
	CompletedAt      *time.Time       `json:"completedAt,omitempty"`
	Status           string           `json:"status"`
	FailureStage     string           `json:"failureStage,omitempty"`
	Attempts         []RoutingAttempt `json:"attempts"`
}

type RoutingAttempt struct {
	Transport             string             `json:"transport"`
	Auxiliary             bool               `json:"auxiliary,omitempty"`
	AttemptID             string             `json:"attemptId"`
	AttemptSequence       int                `json:"attemptSequence"`
	EffectiveModel        string             `json:"effectiveModel"`
	MappedModel           string             `json:"mappedModel"`
	SentModel             string             `json:"sentModel"`
	ChannelUID            string             `json:"channelUid"`
	ChannelName           string             `json:"channelName"`
	ChannelDeleted        bool               `json:"channelDeleted,omitempty"`
	ServiceType           string             `json:"serviceType"`
	ConfigRevision        string             `json:"configRevision"`
	MappingOutcome        string             `json:"mappingOutcome"`
	MappingSource         string             `json:"mappingSource,omitempty"`
	MappingTarget         string             `json:"mappingTarget,omitempty"`
	MatchType             string             `json:"matchType,omitempty"`
	TransformReason       string             `json:"transformReason,omitempty"`
	ModelChanged          bool               `json:"modelChanged"`
	TransformSteps        []RoutingTransform `json:"transformSteps,omitempty"`
	SelectionReason       string             `json:"selectionReason,omitempty"`
	SelectionTraceSummary string             `json:"selectionTraceSummary,omitempty"`
	Status                string             `json:"status"`
	StatusCode            int                `json:"statusCode"`
	StartedAt             time.Time          `json:"startedAt"`
	CompletedAt           *time.Time         `json:"completedAt,omitempty"`
}

type RoutingTransform struct {
	Kind string `json:"kind"`
	From string `json:"from"`
	To   string `json:"to"`
}

type RoutingCoverage struct {
	StartedAt       time.Time `json:"startedAt"`
	Persistent      bool      `json:"persistent"`
	DroppedWrites   uint64    `json:"droppedWrites"`
	UnknownRequests int       `json:"unknownRequests"`
	Limited         bool      `json:"limited"`
}

// RoutingStore 的内存窗口为 24 小时，后台持久化独立快照，写入失败不影响代理。
// 用请求快照原子保存请求与尝试，避免跨表部分写入造成虚假计数。
type RoutingStore struct {
	mu            sync.RWMutex
	requests      map[string]RoutingRequest
	db            *sql.DB
	startedAt     time.Time
	retentionDays int
	writes        chan RoutingRequest
	done          chan struct{}
	closed        bool
	closeOnce     sync.Once
	dropped       atomic.Uint64
	lastCleanup   time.Time
	order         []string
	limited       bool
}

const maxRoutingRequests = 50000

func NewRoutingStore(store *SQLiteStore) (*RoutingStore, error) {
	s := &RoutingStore{requests: make(map[string]RoutingRequest), startedAt: time.Now(), retentionDays: 3, writes: make(chan RoutingRequest, 2048), done: make(chan struct{})}
	if store != nil {
		s.db, s.retentionDays = store.db, store.retentionDays
		if err := s.init(); err != nil {
			return nil, err
		}
	}
	go s.writeLoop()
	return s, nil
}

func (s *RoutingStore) init() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS routing_records_v1 (request_id TEXT PRIMARY KEY, api_type TEXT NOT NULL, received_at INTEGER NOT NULL, payload TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS idx_routing_type_time ON routing_records_v1(api_type, received_at);
 CREATE TABLE IF NOT EXISTS routing_metadata_v1 (id INTEGER PRIMARY KEY CHECK(id=1), started_at INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS routing_coverage_v1 (name TEXT PRIMARY KEY, value INTEGER NOT NULL);
 INSERT OR IGNORE INTO routing_metadata_v1(id, started_at) VALUES(1, unixepoch());`)
	if err != nil {
		return fmt.Errorf("initialize routing observation: %w", err)
	}
	var started int64
	if err := s.db.QueryRow("SELECT started_at FROM routing_metadata_v1 WHERE id=1").Scan(&started); err != nil {
		return err
	}
	s.startedAt = time.Unix(started, 0)
	var dropped uint64
	if err := s.db.QueryRow("SELECT COALESCE((SELECT value FROM routing_coverage_v1 WHERE name='dropped'), 0)").Scan(&dropped); err != nil {
		return err
	}
	s.dropped.Store(dropped)
	rows, err := s.db.Query("SELECT payload FROM routing_records_v1 WHERE received_at >= ? ORDER BY received_at DESC LIMIT ?", time.Now().Add(-24*time.Hour).UnixNano(), maxRoutingRequests+1)
	if err != nil {
		return err
	}
	var recovered []RoutingRequest
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			rows.Close()
			return err
		}
		var request RoutingRequest
		if err := json.Unmarshal([]byte(payload), &request); err != nil {
			rows.Close()
			return err
		}
		if len(s.requests) == maxRoutingRequests {
			s.limited = true
			continue
		}
		if request.Status == RoutingStatusInProgress {
			now := time.Now()
			request.Status, request.CompletedAt = RoutingStatusUnknown, &now
			for i := range request.Attempts {
				if request.Attempts[i].Status == RoutingStatusInProgress {
					request.Attempts[i].Status, request.Attempts[i].CompletedAt = RoutingStatusUnknown, &now
				}
			}
			recovered = append(recovered, request)
		}
		s.requests[request.GatewayRequestID] = request
		s.order = append(s.order, request.GatewayRequestID)
	}
	err = rows.Err()
	rows.Close()
	for i, j := 0, len(s.order)-1; i < j; i, j = i+1, j-1 {
		s.order[i], s.order[j] = s.order[j], s.order[i]
	}
	if err != nil {
		return err
	}
	for _, request := range recovered {
		if err := s.persist(request); err != nil {
			return err
		}
	}
	return nil
}

func cloneRoutingRequest(request RoutingRequest) RoutingRequest {
	request.Attempts = append([]RoutingAttempt{}, request.Attempts...)
	return request
}

// update 将请求快照与入队操作放在同一锁内，保持后台落盘顺序。
func (s *RoutingStore) update(id string, change func(*RoutingRequest)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	request, ok := s.requests[id]
	if !ok {
		return
	}
	change(&request)
	s.requests[id] = request
	if s.db != nil {
		select {
		case s.writes <- cloneRoutingRequest(request):
		default:
			s.dropped.Add(1)
		}
	}
}

func (s *RoutingStore) StartRequest(request RoutingRequest) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if _, ok := s.requests[request.GatewayRequestID]; ok {
		s.mu.Unlock()
		return
	}
	if len(s.requests) >= maxRoutingRequests {
		s.limited = true
		removed := false
		for i, id := range s.order {
			if s.requests[id].Status != RoutingStatusInProgress {
				delete(s.requests, id)
				s.order = append(s.order[:i], s.order[i+1:]...)
				removed = true
				break
			}
		}
		if !removed {
			s.dropped.Add(1)
			s.mu.Unlock()
			return
		}
	}
	request.Status, request.RequestSource, request.Attempts = RoutingStatusInProgress, RequestSourceProxy, []RoutingAttempt{}
	s.requests[request.GatewayRequestID] = request
	s.order = append(s.order, request.GatewayRequestID)
	if time.Since(s.lastCleanup) > time.Minute {
		cutoff := time.Now().Add(-24 * time.Hour)
		for id, old := range s.requests {
			if old.ReceivedAt.Before(cutoff) && old.Status != RoutingStatusInProgress {
				delete(s.requests, id)
			}
		}
		s.lastCleanup = time.Now()
		kept := s.order[:0]
		for _, id := range s.order {
			if _, exists := s.requests[id]; exists {
				kept = append(kept, id)
			}
		}
		s.order = kept
	}
	s.mu.Unlock()
	s.update(request.GatewayRequestID, func(*RoutingRequest) {})
}

func (s *RoutingStore) SetRequestModel(id, model string) {
	s.update(id, func(r *RoutingRequest) { r.RequestedModel = model })
}
func (s *RoutingStore) StartAttempt(id string, attempt RoutingAttempt) {
	s.update(id, func(r *RoutingRequest) {
		attempt.AttemptSequence = len(r.Attempts) + 1
		attempt.Status, attempt.StartedAt = RoutingStatusInProgress, time.Now()
		r.Attempts = append(r.Attempts, attempt)
	})
}
func (s *RoutingStore) FinishAttempt(id, attemptID, status string, code int) {
	s.update(id, func(r *RoutingRequest) {
		for i := range r.Attempts {
			a := &r.Attempts[i]
			if a.AttemptID == attemptID && a.Status == RoutingStatusInProgress {
				now := time.Now()
				a.Status, a.StatusCode, a.CompletedAt = status, code, &now
				break
			}
		}
	})
}
func (s *RoutingStore) FinishRequest(id, status, failureStage string) {
	s.update(id, func(r *RoutingRequest) {
		if r.Status == RoutingStatusInProgress {
			now := time.Now()
			r.Status, r.CompletedAt, r.FailureStage = status, &now, failureStage
			for i := range r.Attempts {
				if r.Attempts[i].Status == RoutingStatusInProgress {
					r.Attempts[i].Status, r.Attempts[i].CompletedAt = RoutingStatusUnknown, &now
				}
			}
		}
	})
}
func (s *RoutingStore) Snapshot(apiType string, from, to time.Time) ([]RoutingRequest, RoutingCoverage) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	requests := []RoutingRequest{}
	coverage := RoutingCoverage{StartedAt: s.startedAt, Persistent: s.db != nil, DroppedWrites: s.dropped.Load(), Limited: s.limited}
	for _, r := range s.requests {
		if r.APIType != apiType || r.ReceivedAt.Before(from) || !r.ReceivedAt.Before(to) {
			continue
		}
		requests = append(requests, cloneRoutingRequest(r))
		if r.Status == RoutingStatusUnknown {
			coverage.UnknownRequests++
		}
	}
	return requests, coverage
}
func (s *RoutingStore) Get(apiType, id string) (RoutingRequest, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.requests[id]
	return cloneRoutingRequest(r), ok && r.APIType == apiType
}
func (s *RoutingStore) persist(request RoutingRequest) error {
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO routing_records_v1(request_id,api_type,received_at,payload) VALUES(?,?,?,?) ON CONFLICT(request_id) DO UPDATE SET payload=excluded.payload`, request.GatewayRequestID, request.APIType, request.ReceivedAt.UnixNano(), string(payload))
	return err
}
func (s *RoutingStore) writeLoop() {
	defer close(s.done)
	timer := time.NewTicker(time.Hour)
	defer timer.Stop()
	storedDropped := s.dropped.Load()
	for {
		select {
		case request, ok := <-s.writes:
			if !ok {
				return
			}
			if s.db != nil && s.persist(request) != nil {
				s.dropped.Add(1)
			}
			if s.db != nil && s.dropped.Load() != storedDropped {
				value := s.dropped.Load()
				if _, err := s.db.Exec("INSERT INTO routing_coverage_v1(name,value) VALUES('dropped',?) ON CONFLICT(name) DO UPDATE SET value=excluded.value", value); err == nil {
					storedDropped = value
				}
			}
		case <-timer.C:
			if s.db != nil {
				if _, err := s.db.Exec("DELETE FROM routing_records_v1 WHERE received_at < ?", time.Now().Add(-time.Duration(s.retentionDays)*24*time.Hour).UnixNano()); err != nil {
					s.dropped.Add(1)
				}
			}
		}
	}
}
func (s *RoutingStore) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() { s.mu.Lock(); s.closed = true; close(s.writes); s.mu.Unlock(); <-s.done })
}
