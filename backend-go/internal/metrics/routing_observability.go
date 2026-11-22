package metrics

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
	Transport             string                 `json:"transport"`
	Auxiliary             bool                   `json:"auxiliary,omitempty"`
	AttemptID             string                 `json:"attemptId"`
	AttemptSequence       int                    `json:"attemptSequence"`
	EffectiveModel        string                 `json:"effectiveModel"`
	MappedModel           string                 `json:"mappedModel"`
	SentModel             string                 `json:"sentModel"`
	ChannelUID            string                 `json:"channelUid"`
	ChannelName           string                 `json:"channelName"`
	ChannelDeleted        bool                   `json:"channelDeleted,omitempty"`
	ServiceType           string                 `json:"serviceType"`
	ConfigRevision        string                 `json:"configRevision"`
	MappingOutcome        string                 `json:"mappingOutcome"`
	MappingSource         string                 `json:"mappingSource,omitempty"`
	MappingTarget         string                 `json:"mappingTarget,omitempty"`
	MatchType             string                 `json:"matchType,omitempty"`
	TransformReason       string                 `json:"transformReason,omitempty"`
	ModelChanged          bool                   `json:"modelChanged"`
	TransformSteps        []RoutingTransform     `json:"transformSteps,omitempty"`
	SelectionReason       string                 `json:"selectionReason,omitempty"`
	SelectionTraceSummary string                 `json:"selectionTraceSummary,omitempty"`
	Status                string                 `json:"status"`
	StatusCode            int                    `json:"statusCode"`
	StartedAt             time.Time              `json:"startedAt"`
	CompletedAt           *time.Time             `json:"completedAt,omitempty"`
	KeyID                 string                 `json:"keyId,omitempty"`
	KeyMask               string                 `json:"keyMask,omitempty"`
	Details               *RoutingAttemptDetails `json:"details,omitempty"`
}

type RoutingTransform struct {
	Kind string `json:"kind"`
	From string `json:"from"`
	To   string `json:"to"`
}

type RoutingCoverage struct {
	RetentionDays   int       `json:"retentionDays"`
	StartedAt       time.Time `json:"startedAt"`
	Persistent      bool      `json:"persistent"`
	DroppedWrites   uint64    `json:"droppedWrites"`
	UnknownRequests int       `json:"unknownRequests"`
	Limited         bool      `json:"limited"`
}

type RoutingHistoryFilter struct {
	RoutingFilter
	KeyID  string
	Status string
	Search string
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
	health        persistenceHealth
}

const maxRoutingRequests = 50000

// RoutingSnapshotMaxAge 允许用户在同一查询窗口内翻页；缓存额外保留这段宽限期。
const RoutingSnapshotMaxAge = 15 * time.Minute
const routingHistoryWindow = 24*time.Hour + RoutingSnapshotMaxAge

func NewRoutingStore(store *SQLiteStore) (*RoutingStore, error) {
	return newRoutingStore(store, 0)
}

func NewRoutingStoreWithRetention(store *SQLiteStore, days int) (*RoutingStore, error) {
	if days < 1 || days > 366 {
		return nil, fmt.Errorf("request history retention must be between 1 and 366 days")
	}
	return newRoutingStore(store, days)
}

func newRoutingStore(store *SQLiteStore, days int) (*RoutingStore, error) {
	s := &RoutingStore{requests: make(map[string]RoutingRequest), startedAt: time.Now(), retentionDays: 3, writes: make(chan RoutingRequest, 2048), done: make(chan struct{})}
	if store != nil {
		s.db, s.retentionDays = store.db, store.retentionDays
	}
	if days > 0 {
		s.retentionDays = days
	}
	if store != nil {
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
	rows, err := s.db.Query("SELECT payload FROM routing_records_v1 WHERE received_at >= ? ORDER BY received_at DESC LIMIT ?", time.Now().Add(-routingHistoryWindow).UnixNano(), maxRoutingRequests+1)
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
	for i := range request.Attempts {
		request.Attempts[i].Details = cloneRoutingAttemptDetails(request.Attempts[i].Details)
		request.Attempts[i].TransformSteps = append([]RoutingTransform(nil), request.Attempts[i].TransformSteps...)
	}
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
		cutoff := time.Now().Add(-routingHistoryWindow)
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
func (s *RoutingStore) FinishAttempt(id, attemptID, status string, code int, details ...*RoutingAttemptDetails) {
	s.update(id, func(r *RoutingRequest) {
		for i := range r.Attempts {
			a := &r.Attempts[i]
			if a.AttemptID == attemptID && a.Status == RoutingStatusInProgress {
				now := time.Now()
				if len(details) > 0 && details[0] != nil {
					completed := cloneRoutingAttemptDetails(details[0])
					if completed.FirstByteLatencyMs == nil && a.Details != nil {
						completed.FirstByteLatencyMs = cloneRoutingAttemptDetails(a.Details).FirstByteLatencyMs
					}
					duration := float64(now.Sub(a.StartedAt)) / float64(time.Millisecond)
					completed.DurationMs = &duration
					a.Details = completed
				}
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
	coverage := RoutingCoverage{StartedAt: s.startedAt, Persistent: s.db != nil, DroppedWrites: s.dropped.Load(), Limited: s.limited, RetentionDays: int(s.MaxHistoryAge() / (24 * time.Hour))}
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

func (s *RoutingStore) MaxHistoryAge() time.Duration {
	if s == nil || s.db == nil {
		return routingHistoryWindow
	}
	return time.Duration(s.retentionDays) * 24 * time.Hour
}

func (s *RoutingStore) QueryRequests(apiType string, from, to time.Time, filter RoutingHistoryFilter, after time.Time, afterID string, limit int) ([]RoutingRequest, bool, RoutingCoverage, error) {
	if s == nil || limit < 1 {
		return nil, false, RoutingCoverage{}, nil
	}

	s.mu.RLock()
	coverage := RoutingCoverage{StartedAt: s.startedAt, Persistent: s.db != nil, DroppedWrites: s.dropped.Load(), Limited: s.limited, RetentionDays: int(s.MaxHistoryAge() / (24 * time.Hour))}
	now := time.Now()
	memoryTo := to
	if memoryTo.After(now) {
		memoryTo = now
	}
	requests := make([]RoutingRequest, 0, limit+1)
	for _, request := range s.requests {
		if request.APIType != apiType || request.ReceivedAt.Before(from) || !request.ReceivedAt.Before(memoryTo) || !afterRoutingCursor(request, after, afterID) || !matchesRoutingHistoryFilter(request, filter) {
			continue
		}
		requests = append(requests, cloneRoutingRequest(request))
	}
	db := s.db
	s.mu.RUnlock()

	sortRoutingRequests(requests)
	if len(requests) > limit+1 {
		requests = requests[:limit+1]
	}
	if db != nil {
		persisted, err := queryRoutingRequests(db, apiType, from, to, filter, after, afterID, limit+1)
		if err != nil {
			return nil, false, coverage, fmt.Errorf("query persistent routing history: %w", err)
		}
		byID := make(map[string]RoutingRequest, len(requests)+len(persisted))
		for _, request := range persisted {
			byID[request.GatewayRequestID] = request
		}
		for _, request := range requests {
			byID[request.GatewayRequestID] = request
		}
		requests = requests[:0]
		for _, request := range byID {
			requests = append(requests, request)
		}
	}
	sortRoutingRequests(requests)
	hasMore := len(requests) > limit
	if hasMore {
		requests = requests[:limit]
	}
	return requests, hasMore, routingCoverageForRequests(coverage, requests), nil
}

func queryRoutingRequests(db *sql.DB, apiType string, from, to time.Time, filter RoutingHistoryFilter, after time.Time, afterID string, limit int) ([]RoutingRequest, error) {
	pattern := "%" + escapeLike(strings.ToLower(strings.TrimSpace(filter.Search))) + "%"
	query := `SELECT r.payload FROM routing_records_v1 AS r
		WHERE r.api_type = ? AND r.received_at >= ? AND r.received_at < ?
		AND (? = '' OR json_extract(r.payload, '$.status') = ?)
		AND (? = '' OR json_extract(r.payload, '$.requestedModel') = ?)
		AND ((? = '' AND ? = '' AND ? = '') OR EXISTS (
			SELECT 1 FROM json_each(r.payload, '$.attempts') AS a
			WHERE (? = '' OR json_extract(a.value, '$.channelUid') = ?)
			AND (? = '' OR json_extract(a.value, '$.sentModel') = ?)
			AND (? = '' OR json_extract(a.value, '$.keyId') = ?)
		))
		AND (? = '' OR lower(r.request_id) LIKE ? ESCAPE '\'
			OR lower(json_extract(r.payload, '$.requestedModel')) LIKE ? ESCAPE '\'
			OR lower(json_extract(r.payload, '$.failureStage')) LIKE ? ESCAPE '\'
			OR EXISTS (
				SELECT 1 FROM json_each(r.payload, '$.attempts') AS a
				WHERE lower(json_extract(a.value, '$.channelName')) LIKE ? ESCAPE '\'
				OR lower(json_extract(a.value, '$.channelUid')) LIKE ? ESCAPE '\'
				OR lower(json_extract(a.value, '$.sentModel')) LIKE ? ESCAPE '\'
				OR lower(json_extract(a.value, '$.attemptId')) LIKE ? ESCAPE '\'
				OR lower(json_extract(a.value, '$.details.errorSummary')) LIKE ? ESCAPE '\'
			)
		)
		AND (? = 0 OR r.received_at < ? OR (r.received_at = ? AND r.request_id < ?))
		ORDER BY r.received_at DESC, r.request_id DESC LIMIT ?`
	rows, err := db.Query(query,
		apiType, from.UnixNano(), to.UnixNano(),
		filter.Status, filter.Status,
		filter.RequestedModel, filter.RequestedModel,
		filter.ChannelUID, filter.SentModel, filter.KeyID, filter.ChannelUID, filter.ChannelUID, filter.SentModel, filter.SentModel, filter.KeyID, filter.KeyID,
		filter.Search, pattern, pattern, pattern, pattern, pattern, pattern, pattern, pattern,
		boolInt(!after.IsZero()), after.UnixNano(), after.UnixNano(), afterID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	requests := make([]RoutingRequest, 0, limit)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var request RoutingRequest
		if err := json.Unmarshal([]byte(payload), &request); err != nil {
			return nil, fmt.Errorf("decode routing record: %w", err)
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

func matchesRoutingHistoryFilter(request RoutingRequest, filter RoutingHistoryFilter) bool {
	if filter.Status != "" && request.Status != filter.Status {
		return false
	}
	if filter.RequestedModel != "" && request.RequestedModel != filter.RequestedModel {
		return false
	}
	if filter.ChannelUID != "" || filter.SentModel != "" || filter.KeyID != "" {
		matched := false
		for _, attempt := range RoutingMatches(request, filter.RoutingFilter) {
			if filter.ChannelUID == "" || attempt.ChannelUID == filter.ChannelUID {
				if (filter.SentModel == "" || attempt.SentModel == filter.SentModel) && (filter.KeyID == "" || attempt.KeyID == filter.KeyID) {
					matched = true
					break
				}
			}
		}
		if !matched {
			return false
		}
	}
	search := strings.ToLower(strings.TrimSpace(filter.Search))
	if search == "" {
		return true
	}
	if strings.Contains(strings.ToLower(request.GatewayRequestID), search) || strings.Contains(strings.ToLower(request.RequestedModel), search) || strings.Contains(strings.ToLower(request.FailureStage), search) {
		return true
	}
	for _, attempt := range request.Attempts {
		if strings.Contains(strings.ToLower(attempt.AttemptID), search) || attempt.Details != nil && strings.Contains(strings.ToLower(attempt.Details.ErrorSummary), search) {
			return true
		}
		if strings.Contains(strings.ToLower(attempt.ChannelName), search) || strings.Contains(strings.ToLower(attempt.ChannelUID), search) || strings.Contains(strings.ToLower(attempt.SentModel), search) {
			return true
		}
	}
	return false
}

func afterRoutingCursor(request RoutingRequest, receivedAt time.Time, id string) bool {
	return id == "" || request.ReceivedAt.Before(receivedAt) || request.ReceivedAt.Equal(receivedAt) && request.GatewayRequestID < id
}

func sortRoutingRequests(requests []RoutingRequest) {
	sort.Slice(requests, func(i, j int) bool {
		if requests[i].ReceivedAt.Equal(requests[j].ReceivedAt) {
			return requests[i].GatewayRequestID > requests[j].GatewayRequestID
		}
		return requests[i].ReceivedAt.After(requests[j].ReceivedAt)
	})
}

func routingCoverageForRequests(coverage RoutingCoverage, requests []RoutingRequest) RoutingCoverage {
	for _, request := range requests {
		if request.Status == RoutingStatusUnknown {
			coverage.UnknownRequests++
		}
	}
	return coverage
}

func escapeLike(value string) string {
	return strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(value)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func (s *RoutingStore) persist(request RoutingRequest) error {
	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO routing_records_v1(request_id,api_type,received_at,payload) VALUES(?,?,?,?) ON CONFLICT(request_id) DO UPDATE SET payload=excluded.payload`, request.GatewayRequestID, request.APIType, request.ReceivedAt.UnixNano(), string(payload))
	return err
}

func (s *RoutingStore) cleanupHistory(now time.Time) error {
	_, err := s.db.Exec("DELETE FROM routing_records_v1 WHERE received_at < ?", now.Add(-time.Duration(s.retentionDays)*24*time.Hour).UnixNano())
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
			if s.db != nil {
				err := s.persist(request)
				s.health.recordWrite(err)
				if err != nil {
					s.dropped.Add(1)
				}
			}
			if s.db != nil && s.dropped.Load() != storedDropped {
				value := s.dropped.Load()
				if _, err := s.db.Exec("INSERT INTO routing_coverage_v1(name,value) VALUES('dropped',?) ON CONFLICT(name) DO UPDATE SET value=excluded.value", value); err == nil {
					storedDropped = value
				}
			}
		case <-timer.C:
			if s.db != nil {
				s.health.recordCleanup(s.cleanupHistory(time.Now()))
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
