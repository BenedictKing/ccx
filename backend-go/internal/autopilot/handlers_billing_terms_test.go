package autopilot

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestHandlePatchBillingTerms(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newTestSubscriptionStore(t)
	seedSubscription(t, store, &SubscriptionProfile{
		SubscriptionUID: "sub-1",
		DisplayName:     "Sub 1",
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	})

	router := gin.New()
	RegisterBillingTermsRoutes(router, store)

	t.Run("set terms", func(t *testing.T) {
		body := map[string]any{
			"paymentAmount":   20,
			"paymentUnit":     " usd ",
			"creditAmount":    100,
			"creditUnit":      "ldc",
			"expectedVersion": uint64(1),
		}
		resp := performJSONRequest(t, router, http.MethodPatch, "/subscriptions/sub-1/billing-terms", body)
		if resp.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
		var got billingTermsResponse
		decodeJSONResponse(t, resp, &got)
		if got.PaymentAmount == nil || *got.PaymentAmount != 20 || got.PaymentUnit != "USD" || got.CreditAmount == nil || *got.CreditAmount != 100 || got.CreditUnit != "LDC" {
			t.Fatalf("unexpected response: %+v", got)
		}
		if got.Version != 2 {
			t.Fatalf("version=%d", got.Version)
		}
	})

	t.Run("half configured rejected", func(t *testing.T) {
		body := map[string]any{"paymentAmount": 10}
		resp := performJSONRequest(t, router, http.MethodPatch, "/subscriptions/sub-1/billing-terms", body)
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("zero rejected", func(t *testing.T) {
		body := map[string]any{
			"paymentAmount": 0,
			"paymentUnit":   "USD",
			"creditAmount":  10,
			"creditUnit":    "USD",
		}
		resp := performJSONRequest(t, router, http.MethodPatch, "/subscriptions/sub-1/billing-terms", body)
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("nan rejected", func(t *testing.T) {
		payload := []byte(`{"paymentAmount":NaN,"paymentUnit":"USD","creditAmount":1,"creditUnit":"USD"}`)
		resp := performRawRequest(router, http.MethodPatch, "/subscriptions/sub-1/billing-terms", payload)
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("version conflict", func(t *testing.T) {
		body := map[string]any{
			"paymentAmount":   30,
			"paymentUnit":     "USD",
			"creditAmount":    60,
			"creditUnit":      "USD",
			"expectedVersion": uint64(1),
		}
		resp := performJSONRequest(t, router, http.MethodPatch, "/subscriptions/sub-1/billing-terms", body)
		if resp.Code != http.StatusConflict {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
	})

	t.Run("reset terms", func(t *testing.T) {
		body := map[string]any{
			"paymentAmount":   nil,
			"paymentUnit":     "",
			"creditAmount":    nil,
			"creditUnit":      "",
			"expectedVersion": uint64(2),
		}
		resp := performJSONRequest(t, router, http.MethodPatch, "/subscriptions/sub-1/billing-terms", body)
		if resp.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", resp.Code, resp.Body.String())
		}
		var got billingTermsResponse
		decodeJSONResponse(t, resp, &got)
		if got.PaymentAmount != nil || got.CreditAmount != nil || got.Preview == "" {
			t.Fatalf("unexpected reset response: %+v", got)
		}
	})
}

func newTestSubscriptionStore(t *testing.T) *SubscriptionStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewSubscriptionStore(filepath.Join(dir, "subscriptions.db"))
	if err != nil {
		t.Fatalf("NewSubscriptionStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedSubscription(t *testing.T, store *SubscriptionStore, profile *SubscriptionProfile) {
	t.Helper()
	if err := store.Create(profile); err != nil {
		t.Fatalf("Create subscription: %v", err)
	}
}

func performJSONRequest(t *testing.T, router http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return performRawRequest(router, method, path, payload)
}

func performRawRequest(router http.Handler, method, path string, payload []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	return resp
}

func decodeJSONResponse(t *testing.T, resp *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(resp.Body.Bytes(), target); err != nil {
		t.Fatalf("json.Unmarshal response: %v body=%s", err, resp.Body.String())
	}
}
