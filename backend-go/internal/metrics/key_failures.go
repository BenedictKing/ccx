package metrics

import "time"

type KeyFailure struct {
	GatewayRequestID string    `json:"gatewayRequestId"`
	AttemptID        string    `json:"attemptId"`
	OccurredAt       time.Time `json:"occurredAt"`
	StatusCode       int       `json:"statusCode"`
	ErrorSummary     string    `json:"errorSummary,omitempty"`
}

// LatestKeyFailures 有界读取最近 1000 个渠道请求，包含最终成功请求中的失败尝试。
func (s *RoutingStore) LatestKeyFailures(apiType, channelUID string) (map[string]KeyFailure, RoutingCoverage, error) {
	requests, more, coverage, err := s.QueryRequests(apiType, time.Now().Add(-s.MaxHistoryAge()), time.Now(),
		RoutingHistoryFilter{RoutingFilter: RoutingFilter{ChannelUID: channelUID, Basis: "all_attempts"}}, time.Time{}, "", 1000)
	coverage.Limited = coverage.Limited || more
	failures := make(map[string]KeyFailure)
	for _, request := range requests {
		for _, attempt := range request.Attempts {
			if attempt.ChannelUID != channelUID || attempt.KeyID == "" || attempt.Status != RoutingStatusFailed {
				continue
			}
			at := attempt.StartedAt
			if attempt.CompletedAt != nil {
				at = *attempt.CompletedAt
			}
			if previous, ok := failures[attempt.KeyID]; ok && !at.After(previous.OccurredAt) {
				continue
			}
			failure := KeyFailure{GatewayRequestID: request.GatewayRequestID, AttemptID: attempt.AttemptID, OccurredAt: at, StatusCode: attempt.StatusCode}
			if attempt.Details != nil {
				failure.ErrorSummary = attempt.Details.ErrorSummary
			}
			failures[attempt.KeyID] = failure
		}
	}
	return failures, coverage, err
}
