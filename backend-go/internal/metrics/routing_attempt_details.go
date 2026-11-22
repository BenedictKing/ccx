package metrics

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// RoutingAttemptDetails 只保存排障元数据；缺失指针表示该耗时未被观测。
type RoutingAttemptDetails struct {
	ErrorSummary          string   `json:"errorSummary,omitempty"`
	UsageObserved         bool     `json:"usageObserved"`
	InputTokens           int64    `json:"inputTokens"`
	OutputTokens          int64    `json:"outputTokens"`
	CacheReadTokens       int64    `json:"cacheReadTokens"`
	CacheCreationTokens   int64    `json:"cacheCreationTokens"`
	FirstByteLatencyMs    *float64 `json:"firstByteLatencyMs,omitempty"`
	FirstContentLatencyMs *float64 `json:"firstContentLatencyMs,omitempty"`
	DurationMs            *float64 `json:"durationMs,omitempty"`
	MaxStreamIdleMs       int64    `json:"maxStreamIdleMs,omitempty"`
	MaxToolCallIdleMs     int64    `json:"maxToolCallIdleMs,omitempty"`
}

// RoutingKeyID 跨 URL 稳定标识同一把上游 Key，接口和持久化均不需要明文。
func RoutingKeyID(apiKey string) string {
	if apiKey == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(digest[:])
}

func cloneRoutingAttemptDetails(details *RoutingAttemptDetails) *RoutingAttemptDetails {
	if details == nil {
		return nil
	}
	copy := *details
	if details.FirstByteLatencyMs != nil {
		value := *details.FirstByteLatencyMs
		copy.FirstByteLatencyMs = &value
	}
	if details.FirstContentLatencyMs != nil {
		value := *details.FirstContentLatencyMs
		copy.FirstContentLatencyMs = &value
	}
	if details.DurationMs != nil {
		value := *details.DurationMs
		copy.DurationMs = &value
	}
	return &copy
}

func (s *RoutingStore) SetAttemptKey(id, attemptID, keyID, keyMask string) {
	s.update(id, func(request *RoutingRequest) {
		for i := range request.Attempts {
			if attempt := &request.Attempts[i]; attempt.AttemptID == attemptID {
				attempt.KeyID, attempt.KeyMask = keyID, keyMask
				return
			}
		}
	})
}

func (s *RoutingStore) RecordAttemptFirstByte(id, attemptID string, latency time.Duration) {
	if latency <= 0 {
		return
	}
	s.update(id, func(request *RoutingRequest) {
		for i := range request.Attempts {
			attempt := &request.Attempts[i]
			if attempt.AttemptID != attemptID || attempt.Status != RoutingStatusInProgress {
				continue
			}
			if attempt.Details == nil {
				attempt.Details = &RoutingAttemptDetails{}
			}
			if attempt.Details.FirstByteLatencyMs == nil {
				value := float64(latency) / float64(time.Millisecond)
				attempt.Details.FirstByteLatencyMs = &value
			}
			return
		}
	})
}
