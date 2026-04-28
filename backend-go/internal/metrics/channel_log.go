package metrics

import (
	"sync"
	"time"
)

// ChannelLog records one upstream request attempt.
type ChannelLog struct {
	RequestID     string    `json:"requestId"`
	ChannelIndex  int       `json:"-"`
	Timestamp     time.Time `json:"timestamp"`
	Model         string    `json:"model"`
	OriginalModel string    `json:"originalModel,omitempty"`
	StatusCode    int       `json:"statusCode"`
	DurationMs    int64     `json:"durationMs"`
	Success       bool      `json:"success"`
	KeyMask       string    `json:"keyMask"`
	BaseURL       string    `json:"baseUrl"`
	ErrorInfo     string    `json:"errorInfo"`
	IsRetry       bool      `json:"isRetry"`
	InterfaceType string    `json:"interfaceType"`
	RequestSource string    `json:"requestSource,omitempty"`

	Status      string     `json:"status"`
	StartTime   time.Time  `json:"startTime"`
	ConnectedAt *time.Time `json:"connectedAt,omitempty"`
	FirstByteAt *time.Time `json:"firstByteAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

const (
	RequestSourceProxy          = "proxy"
	RequestSourceCapabilityTest = "capability_test"
	maxChannelLogs              = 50

	StatusPending    = "pending"
	StatusConnecting = "connecting"
	StatusFirstByte  = "first_byte"
	StatusStreaming  = "streaming"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusCancelled  = "cancelled"
)

func isTerminalStatus(status string) bool {
	return status == StatusCompleted || status == StatusFailed || status == StatusCancelled
}

// ChannelLogStore stores per-channel logs in an in-memory ring buffer.
type ChannelLogStore struct {
	mu               sync.RWMutex
	logs             map[int][]*ChannelLog
	requestLocations map[string]int
}

func NewChannelLogStore() *ChannelLogStore {
	return &ChannelLogStore{
		logs:             make(map[int][]*ChannelLog),
		requestLocations: make(map[string]int),
	}
}

func (s *ChannelLogStore) Record(channelIndex int, log *ChannelLog) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if log != nil && log.RequestID != "" {
		log.ChannelIndex = channelIndex
		if !isTerminalStatus(log.Status) {
			s.requestLocations[log.RequestID] = channelIndex
		} else {
			delete(s.requestLocations, log.RequestID)
		}
	}

	s.logs[channelIndex] = append(s.logs[channelIndex], log)
	if len(s.logs[channelIndex]) > maxChannelLogs {
		s.logs[channelIndex] = s.logs[channelIndex][len(s.logs[channelIndex])-maxChannelLogs:]
	}
}

// RemoveAndShift removes logs for a deleted channel and shifts later indexes.
func (s *ChannelLogStore) RemoveAndShift(channelIndex int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.logs) == 0 && len(s.requestLocations) == 0 {
		return
	}

	for requestID, idx := range s.requestLocations {
		switch {
		case idx == channelIndex:
			delete(s.requestLocations, requestID)
		case idx > channelIndex:
			s.requestLocations[requestID] = idx - 1
		}
	}

	if len(s.logs) == 0 {
		return
	}

	shifted := make(map[int][]*ChannelLog, len(s.logs))
	for idx, logs := range s.logs {
		switch {
		case idx == channelIndex:
			continue
		case idx > channelIndex:
			for _, log := range logs {
				if log != nil {
					log.ChannelIndex = idx - 1
				}
			}
			shifted[idx-1] = logs
		default:
			shifted[idx] = logs
		}
	}

	s.logs = shifted
}

func (s *ChannelLogStore) ClearAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = make(map[int][]*ChannelLog)
	s.requestLocations = make(map[string]int)
}

func (s *ChannelLogStore) Get(channelIndex int) []*ChannelLog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	src := s.logs[channelIndex]
	if len(src) == 0 {
		return nil
	}
	result := make([]*ChannelLog, len(src))
	for i, j := 0, len(src)-1; j >= 0; i, j = i+1, j-1 {
		if src[j] == nil {
			continue
		}
		logCopy := *src[j]
		result[i] = &logCopy
	}
	return result
}

type UpdateStatus int

const (
	UpdateFound UpdateStatus = iota
	UpdateMissingEvicted
	UpdateMissingDeleted
)

func (s *ChannelLogStore) Update(channelIndex int, requestID string, updateFn func(*ChannelLog)) (UpdateStatus, int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if requestID == "" {
		return UpdateMissingDeleted, -1
	}

	actualIndex, tracking := s.requestLocations[requestID]
	if !tracking {
		return UpdateMissingDeleted, -1
	}

	logs, ok := s.logs[actualIndex]
	if !ok {
		delete(s.requestLocations, requestID)
		return UpdateMissingDeleted, -1
	}

	for i := range logs {
		if logs[i] != nil && logs[i].RequestID == requestID {
			updateFn(logs[i])
			if isTerminalStatus(logs[i].Status) {
				delete(s.requestLocations, requestID)
			}
			return UpdateFound, actualIndex
		}
	}

	return UpdateMissingEvicted, actualIndex
}
