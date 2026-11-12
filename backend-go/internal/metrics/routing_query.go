package metrics

import (
	"sort"
	"time"
)

type RoutingFilter struct{ RequestedModel, ChannelUID, SentModel, Basis string }
type RoutingSummary struct {
	Requests          int `json:"requests"`
	Success           int `json:"success"`
	Failed            int `json:"failed"`
	Cancelled         int `json:"cancelled"`
	InProgress        int `json:"inProgress"`
	Unknown           int `json:"unknown"`
	Attempts          int `json:"attempts"`
	RetriedRequests   int `json:"retriedRequests"`
	NoAttemptRequests int `json:"noAttemptRequests"`
	MatchedCount      int `json:"matchedCount"`
}
type RoutingPath struct {
	RequestedModel  string         `json:"requestedModel"`
	ChannelUID      string         `json:"channelUid"`
	ChannelName     string         `json:"channelName"`
	ChannelDeleted  bool           `json:"channelDeleted,omitempty"`
	SentModel       string         `json:"sentModel"`
	Count           int            `json:"count"`
	Share           float64        `json:"share"`
	MappingOutcomes map[string]int `json:"mappingOutcomes"`
	Statuses        map[string]int `json:"statuses"`
}
type RoutingDistribution struct {
	From     time.Time       `json:"from"`
	To       time.Time       `json:"to"`
	Basis    string          `json:"basis"`
	Summary  RoutingSummary  `json:"summary"`
	Paths    []RoutingPath   `json:"paths"`
	Coverage RoutingCoverage `json:"coverage"`
}

func RoutingMatches(request RoutingRequest, filter RoutingFilter) []RoutingAttempt {
	if filter.RequestedModel != "" && request.RequestedModel != filter.RequestedModel {
		return nil
	}
	eligible := request.Attempts
	if filter.Basis == "successful_requests" {
		eligible = nil
		if request.Status == RoutingStatusSuccess {
			for i := len(request.Attempts) - 1; i >= 0; i-- {
				if !request.Attempts[i].Auxiliary && request.Attempts[i].Status == RoutingStatusSuccess {
					eligible = request.Attempts[i : i+1]
					break
				}
			}
		}
	}
	matches := []RoutingAttempt{}
	for _, attempt := range eligible {
		if attempt.Auxiliary {
			continue
		}
		if (filter.ChannelUID == "" || attempt.ChannelUID == filter.ChannelUID) && (filter.SentModel == "" || attempt.SentModel == filter.SentModel) {
			matches = append(matches, attempt)
		}
	}
	return matches
}
func AggregateRouting(requests []RoutingRequest, filter RoutingFilter) (RoutingDistribution, []RoutingRequest) {
	result := RoutingDistribution{Basis: filter.Basis, Paths: []RoutingPath{}}
	matchedRequests := []RoutingRequest{}
	type pathKey struct{ model, channel, sent string }
	paths := map[pathKey]*RoutingPath{}
	sort.Slice(requests, func(i, j int) bool {
		if requests[i].ReceivedAt.Equal(requests[j].ReceivedAt) {
			return requests[i].GatewayRequestID > requests[j].GatewayRequestID
		}
		return requests[i].ReceivedAt.After(requests[j].ReceivedAt)
	})
	for _, request := range requests {
		if filter.RequestedModel != "" && request.RequestedModel != filter.RequestedModel {
			continue
		}
		matches := RoutingMatches(request, filter)
		if (filter.ChannelUID != "" || filter.SentModel != "") && len(matches) == 0 {
			continue
		}
		matchedRequests = append(matchedRequests, request)
		s := &result.Summary
		s.Requests++
		proxyAttempts := 0
		for _, attempt := range request.Attempts {
			if !attempt.Auxiliary {
				proxyAttempts++
			}
		}
		s.Attempts += proxyAttempts
		switch request.Status {
		case RoutingStatusSuccess:
			s.Success++
		case RoutingStatusFailed:
			s.Failed++
		case RoutingStatusCancelled:
			s.Cancelled++
		case RoutingStatusInProgress:
			s.InProgress++
		default:
			s.Unknown++
		}
		if proxyAttempts > 1 {
			s.RetriedRequests++
		}
		if proxyAttempts == 0 {
			s.NoAttemptRequests++
		}
		for _, attempt := range matches {
			key := pathKey{request.RequestedModel, attempt.ChannelUID, attempt.SentModel}
			path := paths[key]
			if path == nil {
				path = &RoutingPath{RequestedModel: request.RequestedModel, ChannelUID: attempt.ChannelUID, ChannelName: attempt.ChannelName, ChannelDeleted: attempt.ChannelDeleted, SentModel: attempt.SentModel, MappingOutcomes: map[string]int{}, Statuses: map[string]int{}}
				paths[key] = path
			}
			path.Count++
			path.MappingOutcomes[attempt.MappingOutcome]++
			path.Statuses[attempt.Status]++
			s.MatchedCount++
		}
	}
	for _, path := range paths {
		if result.Summary.MatchedCount > 0 {
			path.Share = float64(path.Count) / float64(result.Summary.MatchedCount) * 100
		}
		result.Paths = append(result.Paths, *path)
	}
	sort.Slice(result.Paths, func(i, j int) bool {
		a, b := result.Paths[i], result.Paths[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if a.RequestedModel != b.RequestedModel {
			return a.RequestedModel < b.RequestedModel
		}
		if a.ChannelUID != b.ChannelUID {
			return a.ChannelUID < b.ChannelUID
		}
		return a.SentModel < b.SentModel
	})
	return result, matchedRequests
}
