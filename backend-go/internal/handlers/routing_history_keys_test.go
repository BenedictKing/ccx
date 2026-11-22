package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/gin-gonic/gin"
)

func TestRoutingHistoryKeyFilterBindsCursorAndRejectsRawKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store, err := metrics.NewRoutingStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	key := "sk-request-history-secret"
	keyID := metrics.RoutingKeyID(key)
	for i, id := range []string{"new", "old"} {
		store.StartRequest(metrics.RoutingRequest{GatewayRequestID: id, APIType: "chat", ReceivedAt: time.Now().Add(-time.Duration(i+1) * time.Minute)})
		store.StartAttempt(id, metrics.RoutingAttempt{AttemptID: id, ChannelUID: "channel"})
		store.SetAttemptKey(id, id, keyID, "sk-***masked")
		store.FinishAttempt(id, id, metrics.RoutingStatusSuccess, 200)
		store.FinishRequest(id, metrics.RoutingStatusSuccess, "")
	}
	router := gin.New()
	RegisterRoutingRoutes(router.Group("/api"), store)
	get := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/api/chat/models/routing/history?duration=24h&"+query, nil))
		return w
	}
	first := get("keyId=" + keyID + "&limit=1")
	var page struct {
		NextCursor string
		KeyOptions []routingHistoryKeyOption
	}
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &page) != nil || page.NextCursor == "" || len(page.KeyOptions) != 1 {
		t.Fatalf("Key history page=%d %s", first.Code, first.Body.String())
	}
	if strings.Contains(first.Body.String(), key) {
		t.Fatal("history response leaked raw Key")
	}
	if response := get("keyId=" + metrics.RoutingKeyID("other-key") + "&limit=1&cursor=" + url.QueryEscape(page.NextCursor)); response.Code != 410 {
		t.Fatalf("cursor accepted different Key: %d", response.Code)
	}
	if response := get("keyId=" + key); response.Code != 400 {
		t.Fatalf("raw Key accepted as history filter: %d", response.Code)
	}
}
