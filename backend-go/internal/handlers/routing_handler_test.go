package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
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
