package autopilot

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestExchangeRatesHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	manager := newTestConfigManager(t, config.Config{})
	router := gin.New()
	RegisterCostRoutes(router, manager)

	get := performRawRequest(router, http.MethodGet, "/autopilot/cost/exchange-rates", nil)
	if get.Code != http.StatusOK {
		t.Fatalf("GET status=%d body=%s", get.Code, get.Body.String())
	}
	var initial exchangeRatesResponse
	decodeJSONResponse(t, get, &initial)
	if len(initial.Quotes) == 0 || initial.Snapshot == nil || initial.Snapshot.USDUnitPrices["USD"] != 1 {
		t.Fatalf("unexpected defaults: %+v", initial)
	}

	quotes := []config.ExchangeRateQuote{
		{SourceAmount: 1, SourceUnit: "USD", TargetAmount: 7, TargetUnit: "CNY"},
		{SourceAmount: 10, SourceUnit: "CNY", TargetAmount: 500, TargetUnit: "LDC"},
	}
	put := performJSONRequest(t, router, http.MethodPut, "/autopilot/cost/exchange-rates", map[string]any{
		"quotes":                  quotes,
		"expectedSnapshotVersion": initial.Snapshot.Version,
	})
	if put.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", put.Code, put.Body.String())
	}
	var updated exchangeRatesUpdateResponse
	decodeJSONResponse(t, put, &updated)
	if updated.Version <= initial.Snapshot.Version || updated.Snapshot == nil || updated.Snapshot.USDUnitPrices["LDC"] <= 0 {
		t.Fatalf("unexpected update: %+v", updated)
	}

	before := manager.GetConfig().AutopilotRouting.CostOptimization.ExchangeRateSnapshot
	bad := performJSONRequest(t, router, http.MethodPut, "/autopilot/cost/exchange-rates", map[string]any{
		"quotes": []config.ExchangeRateQuote{
			{SourceAmount: 1, SourceUnit: "USD", TargetAmount: 7, TargetUnit: "CNY"},
			{SourceAmount: 1, SourceUnit: "USD", TargetAmount: 8, TargetUnit: "CNY"},
		},
		"expectedSnapshotVersion": updated.Version,
	})
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid graph status=%d body=%s", bad.Code, bad.Body.String())
	}
	after := manager.GetConfig().AutopilotRouting.CostOptimization.ExchangeRateSnapshot
	if before == nil || after == nil || before.Version != after.Version || before.USDUnitPrices["LDC"] != after.USDUnitPrices["LDC"] {
		t.Fatalf("failed update polluted snapshot: before=%+v after=%+v", before, after)
	}

	conflict := performJSONRequest(t, router, http.MethodPut, "/autopilot/cost/exchange-rates", map[string]any{
		"quotes":                  quotes,
		"expectedSnapshotVersion": initial.Snapshot.Version,
	})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}

	empty := performJSONRequest(t, router, http.MethodPut, "/autopilot/cost/exchange-rates", map[string]any{
		"quotes":                  []config.ExchangeRateQuote{},
		"expectedSnapshotVersion": updated.Version,
	})
	if empty.Code != http.StatusOK {
		t.Fatalf("empty status=%d body=%s", empty.Code, empty.Body.String())
	}
	var cleared exchangeRatesUpdateResponse
	decodeJSONResponse(t, empty, &cleared)
	if len(cleared.Quotes) != 0 || len(cleared.Snapshot.USDUnitPrices) != 1 || cleared.Snapshot.USDUnitPrices["USD"] != 1 {
		t.Fatalf("unexpected empty snapshot: %+v", cleared)
	}
	stored := manager.GetConfig().AutopilotRouting.CostOptimization
	if !stored.ExchangeRateQuotesConfigured || len(stored.ExchangeRateQuotes) != 0 {
		t.Fatalf("explicit empty was not preserved: %+v", stored)
	}
}

func TestHandleUpdateSubscriptionExpectedVersion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newTestSubscriptionStore(t)
	seedSubscription(t, store, &SubscriptionProfile{SubscriptionUID: "sub-version", DisplayName: "before"})
	router := gin.New()
	RegisterSubscriptionRoutes(router, store, nil)

	ok := performJSONRequest(t, router, http.MethodPut, "/subscriptions/sub-version", map[string]any{
		"displayName": "after", "expectedVersion": uint64(1),
	})
	if ok.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", ok.Code, ok.Body.String())
	}
	conflict := performJSONRequest(t, router, http.MethodPut, "/subscriptions/sub-version", map[string]any{
		"displayName": "stale", "expectedVersion": uint64(1),
	})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d body=%s", conflict.Code, conflict.Body.String())
	}
	if got := store.Get("sub-version"); got == nil || got.DisplayName != "after" || got.Version != 2 {
		t.Fatalf("stale update mutated profile: %+v", got)
	}
}
