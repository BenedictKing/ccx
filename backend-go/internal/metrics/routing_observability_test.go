package metrics

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestRoutingStorePersistenceRecoveryAndConcurrentAttempts(t *testing.T) {
	db, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: filepath.Join(t.TempDir(), "metrics.db"), RetentionDays: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewRoutingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	store.StartRequest(RoutingRequest{GatewayRequestID: "in-flight", APIType: "chat", RequestedModel: "client", ReceivedAt: time.Now()})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprint(i)
			store.StartAttempt("in-flight", RoutingAttempt{AttemptID: id, ChannelUID: "a", SentModel: "actual"})
			store.FinishAttempt("in-flight", id, RoutingStatusSuccess, 200)
		}(i)
	}
	wg.Wait()
	request, ok := store.Get("chat", "in-flight")
	if !ok || len(request.Attempts) != 20 {
		t.Fatalf("attempts=%+v", request)
	}
	sequences := map[int]bool{}
	for _, a := range request.Attempts {
		if sequences[a.AttemptSequence] {
			t.Fatal("duplicate sequence")
		}
		sequences[a.AttemptSequence] = true
	}
	if _, ok := store.Get("messages", "in-flight"); ok {
		t.Fatal("protocol isolation")
	}
	store.StartAttempt("in-flight", RoutingAttempt{AttemptID: "unfinished"})
	store.Close()
	reopened, err := NewRoutingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, _ := reopened.Get("chat", "in-flight")
	if recovered.Status != RoutingStatusUnknown || recovered.Attempts[20].Status != RoutingStatusUnknown {
		t.Fatalf("recovery=%+v", recovered)
	}
	if len(recovered.Attempts) != 21 {
		t.Fatal("history lost")
	}
}

func TestRoutingAggregationCountsPathsAndFilters(t *testing.T) {
	now := time.Now()
	requests := []RoutingRequest{
		{GatewayRequestID: "retry", APIType: "chat", RequestedModel: "client", ReceivedAt: now, Status: RoutingStatusSuccess, Attempts: []RoutingAttempt{
			{ChannelUID: "a", ChannelName: "old name", SentModel: "model-a", Status: RoutingStatusFailed, MappingOutcome: MappingOutcomeUnmatched},
			{ChannelUID: "b", ChannelName: "backup", SentModel: "model-b", Status: RoutingStatusSuccess, MappingOutcome: MappingOutcomeChanged},
			{ChannelUID: "a", SentModel: "compact", Status: RoutingStatusSuccess, Auxiliary: true},
		}},
		{GatewayRequestID: "none", APIType: "chat", RequestedModel: "client", ReceivedAt: now.Add(-time.Minute), Status: RoutingStatusFailed},
		{GatewayRequestID: "same", APIType: "chat", RequestedModel: "client", ReceivedAt: now, Status: RoutingStatusSuccess, Attempts: []RoutingAttempt{{ChannelUID: "a", ChannelName: "old name", SentModel: "client", Status: RoutingStatusSuccess, MappingOutcome: MappingOutcomeMatchedUnchanged}}},
	}
	for _, tc := range []struct {
		name                             string
		filter                           RoutingFilter
		requests, attempts, count, paths int
	}{
		{"successful", RoutingFilter{Basis: "successful_requests"}, 3, 3, 2, 2},
		{"all", RoutingFilter{Basis: "all_attempts"}, 3, 3, 3, 3},
		{"backup full chain", RoutingFilter{Basis: "all_attempts", ChannelUID: "b"}, 1, 2, 1, 1},
		{"same attempt predicates", RoutingFilter{Basis: "all_attempts", ChannelUID: "a", SentModel: "model-b"}, 0, 0, 0, 0},
		{"success excludes failed primary", RoutingFilter{Basis: "successful_requests", SentModel: "model-a"}, 0, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, _ := AggregateRouting(requests, tc.filter)
			if result.Summary.Requests != tc.requests || result.Summary.Attempts != tc.attempts || result.Summary.MatchedCount != tc.count || len(result.Paths) != tc.paths {
				t.Fatalf("result=%+v", result)
			}
			share := 0.0
			for _, path := range result.Paths {
				share += path.Share
			}
			if tc.count > 0 && (share < 99.999 || share > 100.001) {
				t.Fatalf("share=%v", share)
			}
		})
	}
}
