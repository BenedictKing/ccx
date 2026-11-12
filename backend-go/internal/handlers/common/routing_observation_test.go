package common

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/gin-gonic/gin"
)

func TestRoutingObservationModelFormatsAndLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("model", "image-client")
	_ = writer.Close()
	cases := []struct {
		name, path, contentType string
		body                    []byte
		want                    string
	}{
		{"json", "/v1/chat/completions", "application/json", []byte(`{"model":"client"}`), "client"},
		{"gemini", "/v1beta/models/client:generateContent", "application/json", []byte(`{}`), "client"},
		{"multipart", "/v1/images/edits", writer.FormDataContentType(), body.Bytes(), "image-client"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := requestModel(tc.path, tc.contentType, tc.body); got != tc.want {
				t.Fatalf("%q != %q", got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name                             string
		cancel, panicRequest, streamFail bool
		want                             string
	}{
		{"same name mapping", false, false, false, metrics.RoutingStatusSuccess},
		{"cancelled", true, false, false, metrics.RoutingStatusCancelled},
		{"stream fails after 200", false, false, true, metrics.RoutingStatusFailed},
		{"panic after 200", false, true, false, metrics.RoutingStatusFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := metrics.NewRoutingStore(nil)
			defer store.Close()
			router := gin.New()
			router.Use(gin.Recovery(), RoutingStoreMiddleware(store))
			router.POST("/v1/chat/completions", func(c *gin.Context) {
				defer ObserveRoutingRequest(c, "chat")()
				recordRoutingModel(c, []byte(`{"model":"client"}`))
				req := httptest.NewRequest(http.MethodPost, "http://upstream/v1/chat/completions", bytes.NewBufferString(`{"model":"client"}`))
				req.Header.Set("Content-Type", "application/json")
				StartRoutingAttempt(c, req, &config.UpstreamConfig{ChannelUID: "uid", Name: "same", ModelMapping: map[string]string{"client": "client"}}, "client", "attempt", "priority")
				c.Data(200, "text/event-stream", []byte("data: start\n\n"))
				if tc.panicRequest {
					panic("handler panic")
				}
				info := ""
				if tc.cancel {
					info = "client canceled"
				}
				completeRoutingLog(c, nil, "", "attempt", 200, !tc.streamFail && !tc.cancel, info, false)
			})
			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			if tc.cancel {
				ctx, cancel := context.WithCancel(req.Context())
				cancel()
				req = req.WithContext(ctx)
			}
			router.ServeHTTP(httptest.NewRecorder(), req)
			records, _ := store.Snapshot("chat", time.Now().Add(-time.Hour), time.Now().Add(time.Second))
			if len(records) != 1 || records[0].Status != tc.want {
				t.Fatalf("records=%+v", records)
			}
			if records[0].Attempts[0].MappingOutcome != metrics.MappingOutcomeMatchedUnchanged {
				t.Fatal("same-name mapping misclassified")
			}
		})
	}
}

func TestSocketRoutingCorrelatesTurnsAndSkipsWarmup(t *testing.T) {
	store, _ := metrics.NewRoutingStore(nil)
	defer store.Close()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), routingStoreContextKey{}, store))
	observer := NewSocketRoutingObserver(c)
	if observer.Begin([]byte(`{"type":"response.create","model":"client","generate":false}`)) != nil {
		t.Fatal("warmup was counted")
	}
	observer.Event([]byte(`{"type":"response.completed","response":{"id":"warmup"}}`))
	upstream := &config.UpstreamConfig{ChannelUID: "channel", Name: "snapshot", ModelMapping: map[string]string{"client": "actual"}}
	first := observer.Begin([]byte(`{"type":"response.create","model":"client"}`))
	second := observer.Begin([]byte(`{"type":"response.create","model":"client"}`))
	_, decision := PinSocketModelMapping(upstream, []byte(`{"model":"client"}`))
	observer.Sending(first, []byte(`{"model":"actual"}`), upstream, "priority_order", decision)
	observer.Sending(second, []byte(`{"model":"actual"}`), upstream, "websocket_connection", decision)
	observer.Event([]byte(`{"type":"response.created","response":{"id":"first"}}`))
	observer.Event([]byte(`{"type":"response.created","response":{"id":"second"}}`))
	observer.Event([]byte(`{"type":"response.completed","response":{"id":"second"}}`))
	observer.Event([]byte(`{"type":"response.failed","response":{"id":"first"}}`))
	pending := observer.Begin([]byte(`{"model":"client"}`))
	observer.Sending(pending, []byte(`{"model":"actual"}`), upstream, "websocket_connection", decision)
	observer.Close(metrics.RoutingStatusCancelled)
	for _, tc := range []struct {
		turn   *SocketRoutingTurn
		status string
	}{{first, metrics.RoutingStatusFailed}, {second, metrics.RoutingStatusSuccess}, {pending, metrics.RoutingStatusCancelled}} {
		request, ok := store.Get("responses", tc.turn.id)
		if !ok || request.Status != tc.status || len(request.Attempts) != 1 || request.Attempts[0].Transport != "websocket" || request.Attempts[0].SentModel != "actual" {
			t.Fatalf("turn=%+v", request)
		}
	}
	records, _ := store.Snapshot("responses", time.Now().Add(-time.Hour), time.Now().Add(time.Second))
	if len(records) != 3 {
		t.Fatalf("records=%+v", records)
	}
}

func TestSocketRoutingIgnoresDuplicateForeignAndWarmupEvents(t *testing.T) {
	for _, test := range []struct {
		name   string
		events []string
		bound  bool
	}{
		{"duplicate terminal before next created", []string{`{"type":"response.completed","response":{"id":"first"}}`}, false},
		{"foreign terminal after next created", []string{`{"type":"response.created","response":{"id":"second"}}`, `{"type":"response.failed","response":{"id":"unrelated"}}`}, true},
		{"response_id terminal", []string{`{"type":"response.created","response":{"id":"second"}}`, `{"type":"response.completed","response_id":"first"}`}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, _ := metrics.NewRoutingStore(nil)
			defer store.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), routingStoreContextKey{}, store))
			observer := NewSocketRoutingObserver(c)
			first := observer.Begin([]byte(`{"type":"response.create","model":"first"}`))
			observer.Event([]byte(`{"type":"response.created","response":{"id":"first"}}`))
			observer.Event([]byte(`{"type":"response.completed","response":{"id":"first"}}`))
			second := observer.Begin([]byte(`{"type":"response.create","model":"second"}`))
			for _, event := range test.events {
				observer.Event([]byte(event))
			}
			request, _ := store.Get("responses", second.id)
			if request.Status != metrics.RoutingStatusInProgress {
				t.Fatalf("unrelated event finalized next request: %+v", request)
			}
			original, _ := store.Get("responses", first.id)
			if original.Status != metrics.RoutingStatusSuccess {
				t.Fatal("original status changed")
			}
			if !test.bound {
				observer.Event([]byte(`{"type":"response.created","response":{"id":"second"}}`))
			}
			observer.Event([]byte(`{"type":"response.failed","response_id":"second"}`))
			request, _ = store.Get("responses", second.id)
			if request.Status != metrics.RoutingStatusFailed {
				t.Fatalf("actual terminal not associated: %+v", request)
			}
		})
	}
	t.Run("overlapping warmup", func(t *testing.T) {
		store, _ := metrics.NewRoutingStore(nil)
		defer store.Close()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), routingStoreContextKey{}, store))
		observer := NewSocketRoutingObserver(c)
		observer.Begin([]byte(`{"type":"response.create","generate":false}`))
		actual := observer.Begin([]byte(`{"type":"response.create","model":"actual"}`))
		observer.Event([]byte(`{"type":"response.created","response":{"id":"warm"}}`))
		observer.Event([]byte(`{"type":"response.completed","response":{"id":"warm"}}`))
		request, _ := store.Get("responses", actual.id)
		if request.Status != metrics.RoutingStatusInProgress {
			t.Fatal("warmup response finalized real request")
		}
		observer.Event([]byte(`{"type":"response.completed","response":{"id":"real"}}`))
		records, _ := store.Snapshot("responses", time.Now().Add(-time.Hour), time.Now().Add(time.Second))
		if len(records) != 1 || records[0].Status != metrics.RoutingStatusSuccess {
			t.Fatalf("records=%+v", records)
		}
	})
}

func TestAuxiliaryOutcomeOverridesHTTP200AndUsesActualMapping(t *testing.T) {
	for _, tc := range []struct {
		name         string
		innerSuccess bool
	}{{"stream fails after 200", false}, {"nested fallback succeeds", true}} {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := metrics.NewRoutingStore(nil)
			defer store.Close()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), routingStoreContextKey{}, store))
			finish := ObserveRoutingRequest(c, "responses")
			recordRoutingModel(c, []byte(`{"model":"client"}`))
			original, _ := http.NewRequest("POST", "http://upstream/compact", bytes.NewBufferString(`{"model":"client"}`))
			outer := ObserveAuxiliaryAttempt(c, original, &config.UpstreamConfig{ChannelUID: "channel"}, "client")
			actual, _ := http.NewRequest("POST", "http://upstream/responses", bytes.NewBufferString(`{"model":"summary-model"}`))
			inner := ObserveAuxiliaryAttempt(c, actual, &config.UpstreamConfig{ChannelUID: "channel", CompactModel: "summary-model"}, "summary-model")
			c.Data(200, "text/event-stream", []byte("data: partial\n\n"))
			inner(tc.innerSuccess, 200)
			outer(false, 503)
			finish()
			records, _ := store.Snapshot("responses", time.Now().Add(-time.Hour), time.Now().Add(time.Second))
			expected := metrics.RoutingStatusFailed
			if tc.innerSuccess {
				expected = metrics.RoutingStatusSuccess
			}
			if len(records) != 1 || records[0].Status != expected {
				t.Fatalf("records=%+v", records)
			}
			second := records[0].Attempts[1]
			if second.MappingOutcome != metrics.MappingOutcomeUnmatched || second.TransformReason != "compact_model" || second.SentModel != "summary-model" {
				t.Fatalf("compact mapping=%+v", second)
			}
		})
	}
}
