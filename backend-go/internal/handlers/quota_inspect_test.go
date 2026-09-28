package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/quota"
	"github.com/gin-gonic/gin"
)

func TestGetQuotaChannels_NilManagerReturnsEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/quota/channels", GetQuotaChannels(nil))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/quota/channels", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		Channels []json.RawMessage `json:"channels"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Channels) != 0 {
		t.Fatalf("expected empty channels for nil manager, got %d", len(payload.Channels))
	}
}

func TestGetQuotaChannels_ReturnsDerivedView(t *testing.T) {
	gin.SetMode(gin.TestMode)
	qm := quota.NewManager()
	headers := make(http.Header)
	headers.Set("anthropic-ratelimit-input-tokens-limit", "10000")
	headers.Set("anthropic-ratelimit-input-tokens-remaining", "9000")
	qm.UpdateChannelResponseHeaders("ch_view", "ep_1", "anthropic", headers)

	router := gin.New()
	router.GET("/api/quota/channels", GetQuotaChannels(qm))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/quota/channels", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		Channels []struct {
			ChannelUID string  `json:"channelUid"`
			Status     string  `json:"status"`
			Headroom   float64 `json:"headroom"`
			Saturated  bool    `json:"saturated"`
			Values     map[string]struct {
				ResetAtMs int64  `json:"resetAtMs"`
				Source    string `json:"source"`
			} `json:"values"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(payload.Channels))
	}
	ch := payload.Channels[0]
	if ch.ChannelUID != "ch_view" || ch.Status != "healthy" || ch.Headroom != 0.9 || ch.Saturated {
		t.Fatalf("unexpected view: %+v", ch)
	}
	if len(ch.Values) == 0 {
		t.Fatal("expected dimension values in snapshot")
	}
	for _, v := range ch.Values {
		if v.Source != "response_headers" {
			t.Fatalf("value source = %q, want response_headers", v.Source)
		}
	}
}
