package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/gin-gonic/gin"
)

func TestRoutingAPIValidationIsolationAndCursor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, _ := metrics.NewRoutingStore(nil)
	defer store.Close()
	for i, id := range []string{"a", "b", "c"} {
		store.StartRequest(metrics.RoutingRequest{GatewayRequestID: id, APIType: "chat", RequestedModel: "source", ReceivedAt: time.Now().Add(-time.Duration(i+1) * time.Minute)})
		store.FinishRequest(id, metrics.RoutingStatusFailed, "selection")
	}
	router := gin.New()
	RegisterRoutingRoutes(router.Group("/api"), store)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	first := get("/api/chat/models/routing/requests?duration=1h&limit=1")
	var page struct {
		Requests   []metrics.RoutingRequest
		NextCursor string
	}
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &page) != nil || len(page.Requests) != 1 || page.NextCursor == "" {
		t.Fatalf("page=%s", first.Body.String())
	}
	cursor := url.QueryEscape(page.NextCursor)
	second := get("/api/chat/models/routing/requests?limit=1&cursor=" + cursor)
	var next struct{ Requests []metrics.RoutingRequest }
	_ = json.Unmarshal(second.Body.Bytes(), &next)
	if second.Code != 200 || len(next.Requests) != 1 || next.Requests[0].GatewayRequestID == page.Requests[0].GatewayRequestID {
		t.Fatalf("next=%s", second.Body.String())
	}
	for _, path := range []string{"/api/chat/models/routing?duration=7d", "/api/chat/models/routing?basis=wrong", "/api/chat/models/routing/requests?cursor=invalid", "/api/messages/models/routing/requests?cursor=" + cursor} {
		if w := get(path); w.Code != 400 {
			t.Fatalf("%s => %d", path, w.Code)
		}
	}
	if w := get("/api/messages/models/routing/requests/a"); w.Code != 404 {
		t.Fatal("cross-protocol detail leaked")
	}
}

func TestRoutingSnapshotAllowsReadingAfterOneMinute(t *testing.T) {
	store, _ := metrics.NewRoutingStore(nil)
	defer store.Close()
	router := gin.New()
	RegisterRoutingRoutes(router.Group("/api"), store)
	end := time.Now().Add(-3 * time.Minute)
	start := end.Add(-24 * time.Hour)
	for i, id := range []string{"recent", "old"} {
		store.StartRequest(metrics.RoutingRequest{GatewayRequestID: id, APIType: "chat", RequestedModel: "source", ReceivedAt: end.Add(-time.Duration(i+1) * time.Minute)})
		store.FinishRequest(id, metrics.RoutingStatusFailed, "selection")
	}
	path := "/api/chat/models/routing/requests?from=" + url.QueryEscape(start.Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(end.Format(time.RFC3339Nano)) + "&limit=1"
	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest("GET", path, nil))
	if first.Code != 200 {
		t.Fatalf("open snapshot became invalid: %d %s", first.Code, first.Body.String())
	}
	var page struct{ NextCursor string }
	_ = json.Unmarshal(first.Body.Bytes(), &page)
	next := httptest.NewRecorder()
	router.ServeHTTP(next, httptest.NewRequest("GET", "/api/chat/models/routing/requests?cursor="+url.QueryEscape(page.NextCursor), nil))
	if next.Code != 200 {
		t.Fatalf("paging failed: %d %s", next.Code, next.Body.String())
	}
}

func TestRoutingExpiredSnapshotReturnsRefreshableError(t *testing.T) {
	store, _ := metrics.NewRoutingStore(nil)
	defer store.Close()
	router := gin.New()
	RegisterRoutingRoutes(router.Group("/api"), store)
	end := time.Now().Add(-metrics.RoutingSnapshotMaxAge - time.Minute)
	path := "/api/chat/models/routing/requests?from=" + url.QueryEscape(end.Add(-time.Hour).Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(end.Format(time.RFC3339Nano))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
	if recorder.Code != 410 {
		t.Fatalf("expected expiry, got %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestRoutingHistorySearchStatusCursorAndRetention(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := metrics.NewRoutingStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for i, id := range []string{"first", "second", "third"} {
		store.StartRequest(metrics.RoutingRequest{GatewayRequestID: id, APIType: "chat", RequestedModel: "source-model", ReceivedAt: time.Now().Add(-time.Duration(i+1) * time.Minute)})
		status := metrics.RoutingStatusFailed
		if id == "second" {
			status = metrics.RoutingStatusSuccess
		}
		store.FinishRequest(id, status, "selection")
	}
	router := gin.New()
	RegisterRoutingRoutes(router.Group("/api"), store)
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	first := get("/api/chat/models/routing/history?duration=24h&status=failed&q=source&limit=1")
	var page struct {
		Requests   []metrics.RoutingRequest
		NextCursor string
	}
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &page) != nil || len(page.Requests) != 1 || page.Requests[0].GatewayRequestID != "first" || page.NextCursor == "" {
		t.Fatalf("first history page = %d %s", first.Code, first.Body.String())
	}
	second := get("/api/chat/models/routing/history?duration=24h&status=failed&q=source&limit=1&cursor=" + url.QueryEscape(page.NextCursor))
	var next struct{ Requests []metrics.RoutingRequest }
	if second.Code != 200 || json.Unmarshal(second.Body.Bytes(), &next) != nil || len(next.Requests) != 1 || next.Requests[0].GatewayRequestID != "third" {
		t.Fatalf("second history page = %d %s", second.Code, second.Body.String())
	}
	if response := get("/api/chat/models/routing/history?duration=7d"); response.Code != 400 {
		t.Fatalf("nonpersistent history range = %d, want 400", response.Code)
	}
}

func TestRoutingHistoryEndpointReadsPersistentRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := metrics.NewSQLiteStore(&metrics.SQLiteStoreConfig{DBPath: filepath.Join(t.TempDir(), "metrics.db"), RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := metrics.NewRoutingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	store.StartRequest(metrics.RoutingRequest{GatewayRequestID: "persistent-request", APIType: "chat", RequestedModel: "source-model", ReceivedAt: old})
	store.FinishRequest("persistent-request", metrics.RoutingStatusFailed, "selection")
	store.Close()

	recovered, err := metrics.NewRoutingStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	router := gin.New()
	RegisterRoutingRoutes(router.Group("/api"), recovered)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/api/chat/models/routing/history?duration=7d&status=failed&q=source", nil))
	var page struct {
		Requests []metrics.RoutingRequest
		Coverage metrics.RoutingCoverage
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Requests) != 1 || page.Requests[0].GatewayRequestID != "persistent-request" || !page.Coverage.Persistent {
		t.Fatalf("persistent history response = %d %s", response.Code, response.Body.String())
	}
}
