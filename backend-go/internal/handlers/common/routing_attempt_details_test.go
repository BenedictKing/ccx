package common

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

func TestRoutingDetailsPersistAfterLiveLogEviction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := filepath.Join(t.TempDir(), "metrics.db")
	db, err := metrics.NewSQLiteStore(&metrics.SQLiteStoreConfig{DBPath: path, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	store, err := metrics.NewRoutingStoreWithRetention(db, 14)
	if err != nil {
		t.Fatal(err)
	}
	logStore := metrics.NewChannelLogStore()
	apiKey := "sk-history-private-123456"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: content\n\n"))
	}))
	defer server.Close()
	router := gin.New()
	router.Use(RoutingStoreMiddleware(store))
	router.POST("/v1/messages", func(c *gin.Context) {
		defer ObserveRoutingRequest(c, "messages")()
		body := []byte(`{"model":"client","messages":[{"content":"private prompt body"}]}`)
		recordRoutingModel(c, body)
		id := CreatePendingLog(logStore, "bucket", 0, "history", "client", "client", "", "", apiKey, server.URL, "messages", "", metrics.RequestSourceProxy, nil, "", WithGatewayRequestID(routingRequestID(c)))
		req, _ := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, server.URL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		StartRoutingAttempt(c, req, &config.UpstreamConfig{ChannelUID: "channel", Name: "history"}, "client", id, "priority")
		tracker := routingTracker(c)
		bindRoutingAttemptKey(tracker, id, apiKey, req)
		started := time.Now()
		resp, err := SendRequestWithLifecycleTrace(req, &config.UpstreamConfig{BaseURL: server.URL}, config.NewEnvConfig(), true, "Messages", &RequestLifecycleTrace{OnFirstResponseByte: func() { recordRoutingFirstByte(tracker, id, time.Since(started)) }})
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		StartStreamTimeoutObservation(c, logStore, "bucket", id, time.Now())
		_, _ = io.ReadAll(resp.Body)
		MarkStreamFirstContent(c)
		// 进行中的日志被 50 条环形缓冲淘汰，历史仍应保留完整观测值。
		for i := 0; i < 60; i++ {
			logStore.Record("bucket", &metrics.ChannelLog{RequestID: fmt.Sprint(i), Status: metrics.StatusCompleted})
		}
		FinishStreamTimeoutObservation(c)
		completeRoutingLog(c, logStore, "bucket", id, 429, false, `{"error":{"type":"rate_limit","message":"too many requests `+apiKey+`"},"request_body":"private prompt body"}`, false, &types.Usage{InputTokens: 300, PromptTokensTotal: 300, CacheReadInputTokens: 100, OutputTokens: 25})
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/messages", nil))
	store.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopenedDB, err := metrics.NewSQLiteStore(&metrics.SQLiteStoreConfig{DBPath: path, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedDB.Close()
	recovered, err := metrics.NewRoutingStoreWithRetention(reopenedDB, 14)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	requests, _, _, err := recovered.QueryRequests("messages", time.Now().Add(-time.Hour), time.Now(), metrics.RoutingHistoryFilter{RoutingFilter: metrics.RoutingFilter{Basis: "all_attempts"}, KeyID: metrics.RoutingKeyID(apiKey)}, time.Time{}, "", 20)
	if err != nil || len(requests) != 1 {
		t.Fatalf("recovered requests=%+v err=%v", requests, err)
	}
	attempt := requests[0].Attempts[0]
	if attempt.Details == nil || attempt.Details.InputTokens != 200 || attempt.Details.CacheReadTokens != 100 || attempt.Details.OutputTokens != 25 || attempt.Details.FirstByteLatencyMs == nil || attempt.Details.FirstContentLatencyMs == nil {
		t.Fatalf("lost request details: %+v", attempt)
	}
	encoded, _ := json.Marshal(requests)
	if strings.Contains(string(encoded), apiKey) || strings.Contains(string(encoded), "private prompt body") {
		t.Fatalf("sensitive data persisted: %s", encoded)
	}
	live := logStore.Get("bucket")[0]
	if live.RequestID != attempt.AttemptID || live.GatewayRequestID != requests[0].GatewayRequestID {
		t.Fatalf("request/attempt association lost: %+v", live)
	}
}

func TestRoutingErrorSummaryRedactsBodiesAndSecrets(t *testing.T) {
	for _, tc := range []struct {
		info string
		code int
	}{
		{`{"error":{"message":"failure sk-secret-123 Bearer session-secret token=another-secret"},"body":"private-body"}`, 429},
		{"<html>private-body sk-secret-123</html>", 500},
		{"Post https://user:password@host/path?api_key=sk-secret-123: timeout", 0},
		{"invalid stream chunk: {\"content\":\"private-body\"}", 200},
		{`{"error":{"message":"token=\"another-secret\" key='session-secret'"}}`, 429},
	} {
		summary := routingErrorSummary(tc.info, tc.code, "sk-secret-123")
		for _, secret := range []string{"sk-secret-123", "session-secret", "another-secret", "private-body", "user:password"} {
			if strings.Contains(summary, secret) {
				t.Fatalf("summary leaked %q: %s", secret, summary)
			}
		}
	}
}

func TestSocketRoutingDetailsDoNotUseHandshakeLatency(t *testing.T) {
	store, err := metrics.NewRoutingStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), routingStoreContextKey{}, store))
	observer := NewSocketRoutingObserver(c)
	turn := observer.Begin([]byte(`{"model":"client"}`))
	observer.Sending(turn, []byte(`{"model":"client"}`), &config.UpstreamConfig{ChannelUID: "channel"}, "priority", RoutingMappingDecision{}, "sk-socket-secret")
	observer.Event([]byte(`{"type":"response.completed","response":{"id":"response","usage":{"input_tokens":300,"input_tokens_details":{"cached_tokens":100},"output_tokens":25}}}`))
	request, ok := store.Get("responses", turn.id)
	if !ok || len(request.Attempts) != 1 {
		t.Fatal("socket request missing")
	}
	attempt := request.Attempts[0]
	if attempt.KeyID != metrics.RoutingKeyID("sk-socket-secret") || attempt.Details == nil || attempt.Details.InputTokens != 200 || attempt.Details.CacheReadTokens != 100 || attempt.Details.FirstByteLatencyMs != nil {
		t.Fatalf("incorrect socket request details: %+v", attempt)
	}
	derived := observer.Begin([]byte(`{"model":"client"}`))
	observer.Sending(derived, []byte(`{"model":"client"}`), &config.UpstreamConfig{ChannelUID: "channel"}, "priority", RoutingMappingDecision{}, "sk-socket-secret", "Bearer derived-secret")
	next := observer.Begin([]byte(`{"model":"client"}`))
	observer.Sending(next, []byte(`{"model":"client"}`), &config.UpstreamConfig{ChannelUID: "channel"}, "priority", RoutingMappingDecision{}, "sk-socket-secret")
	observer.Event([]byte(`{"type":"response.created","response":{"id":"derived"}}`))
	observer.Event([]byte(`{"type":"response.created","response":{"id":"next"}}`))
	observer.Event([]byte(`{"type":"response.failed","response":{"id":"next","error":{"message":"invalid credential derived-secret"}}}`))
	request, _ = store.Get("responses", next.id)
	if strings.Contains(request.Attempts[0].Details.ErrorSummary, "derived-secret") {
		t.Fatal("derived handshake credential leaked in later turn")
	}
	observer.Close(metrics.RoutingStatusCancelled)
}
