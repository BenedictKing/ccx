package handlers

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/gin-gonic/gin"
)

// testCostReportStore 实现 PersistenceStore 接口，仅关注成本报表相关方法
type testCostReportStore struct {
	reportRows    []metrics.CostReportRow
	modelRows     []metrics.ModelCostBreakdownRow
	modelRowsErr  error
	reportRowsErr error
}

func (t *testCostReportStore) AddRecord(record metrics.PersistentRecord) {}
func (t *testCostReportStore) LoadRecords(since time.Time, apiType string) ([]metrics.PersistentRecord, error) {
	return nil, nil
}
func (t *testCostReportStore) LoadLatestTimestamps(apiType string) (map[string]*metrics.KeyLatestTimestamps, error) {
	return nil, nil
}
func (t *testCostReportStore) LoadCircuitStates(apiType string) (map[string]*metrics.PersistentCircuitState, error) {
	return nil, nil
}
func (t *testCostReportStore) UpsertCircuitState(state metrics.PersistentCircuitState) error {
	return nil
}
func (t *testCostReportStore) QueryModelAggregatedHistory(apiType string, since time.Time, intervalSeconds int64, metricsKey string, baseURL string) ([]metrics.ModelAggregatedBucket, error) {
	return nil, nil
}
func (t *testCostReportStore) QueryAggregatedHistory(apiType string, since time.Time, intervalSeconds int64, metricsKey string, baseURL string) ([]metrics.AggregatedBucket, error) {
	return nil, nil
}
func (t *testCostReportStore) CleanupOldRecords(before time.Time) (int64, error) { return 0, nil }
func (t *testCostReportStore) DeleteRecordsByMetricsKeys(metricsKeys []string, apiType string) (int64, error) {
	return 0, nil
}
func (t *testCostReportStore) DeleteCircuitStatesByMetricsKeys(metricsKeys []string, apiType string) (int64, error) {
	return 0, nil
}
func (t *testCostReportStore) QueryCostReport(apiType string, since time.Time, groupBy string) ([]metrics.CostReportRow, error) {
	return t.reportRows, t.reportRowsErr
}
func (t *testCostReportStore) QueryModelCostBreakdown(apiType string, since time.Time, groupBy string, filterGroupKey string) ([]metrics.ModelCostBreakdownRow, error) {
	return t.modelRows, t.modelRowsErr
}
func (t *testCostReportStore) QueryConsumptionPolicyDistribution(apiType string, since time.Time) (metrics.ConsumptionPolicyDistribution, error) {
	return nil, nil
}
func (t *testCostReportStore) Close() error { return nil }

func newTestMetricsManager(store metrics.PersistenceStore) *metrics.MetricsManager {
	return metrics.NewMetricsManagerWithPersistence(10, 0.5, store, "messages")
}

func TestGetCostReport_BasicFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	store := &testCostReportStore{
		reportRows: []metrics.CostReportRow{
			{GroupKey: "sk-aa****bb", TotalRequests: 100, SuccessCount: 95, InputTokens: 50000, OutputTokens: 10000},
			{GroupKey: "sk-cc****dd", TotalRequests: 50, SuccessCount: 48, InputTokens: 25000, OutputTokens: 5000},
		},
		modelRows: []metrics.ModelCostBreakdownRow{
			{Model: "claude-sonnet-4-20250514", InputTokens: 50000, OutputTokens: 10000},
		},
	}

	deps := &CostReportDeps{
		MetricsManagers: map[string]*metrics.MetricsManager{
			"messages": newTestMetricsManager(store),
		},
	}

	w := httptest.NewRecorder()
	c, r := gin.CreateTestContext(w)
	r.GET("/api/reports/cost", GetCostReport(deps))

	c.Request = httptest.NewRequest(http.MethodGet, "/api/reports/cost?groupBy=user&duration=7d&type=messages", nil)
	r.ServeHTTP(w, c.Request)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["groupBy"] != "user" {
		t.Errorf("expected groupBy=user, got %v", resp["groupBy"])
	}
	if resp["apiType"] != "messages" {
		t.Errorf("expected apiType=messages, got %v", resp["apiType"])
	}

	rows, ok := resp["rows"].([]interface{})
	if !ok {
		t.Fatalf("expected rows array, got %T", resp["rows"])
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	// Verify first row
	row0 := rows[0].(map[string]interface{})
	if row0["groupKey"] != "sk-aa****bb" {
		t.Errorf("expected groupKey=sk-aa****bb, got %v", row0["groupKey"])
	}
	if row0["totalRequests"].(float64) != 100 {
		t.Errorf("expected totalRequests=100, got %v", row0["totalRequests"])
	}
}

func TestGetCostReport_ReportsIncompletePricing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	store := &testCostReportStore{
		reportRows: []metrics.CostReportRow{{
			GroupKey: "sk-aa****bb", TotalRequests: 2, SuccessCount: 2,
			InputTokens: 2_000_000, OutputTokens: 2_000_000,
		}},
		modelRows: []metrics.ModelCostBreakdownRow{
			{Model: "claude-opus-4-8", InputTokens: 1_000_000, OutputTokens: 1_000_000},
			{Model: "unpriced-model", InputTokens: 1_000_000, OutputTokens: 1_000_000},
		},
	}
	deps := &CostReportDeps{MetricsManagers: map[string]*metrics.MetricsManager{
		"messages": newTestMetricsManager(store),
	}}

	w := httptest.NewRecorder()
	c, r := gin.CreateTestContext(w)
	r.GET("/api/reports/cost", GetCostReport(deps))
	c.Request = httptest.NewRequest(http.MethodGet, "/api/reports/cost?type=messages", nil)
	r.ServeHTTP(w, c.Request)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var response struct {
		Rows []costReportRow `json:"rows"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(response.Rows) != 1 {
		t.Fatalf("expected one row, got %d", len(response.Rows))
	}
	row := response.Rows[0]
	if row.PricingComplete {
		t.Fatal("row with an unpriced model must not be reported as fully priced")
	}
	if math.Abs(row.ListCostUSD-30) > 0.0001 {
		t.Fatalf("expected known cost subtotal 30 USD, got %v", row.ListCostUSD)
	}
	if len(row.UnpricedModels) != 1 || row.UnpricedModels[0] != "unpriced-model" {
		t.Fatalf("unpricedModels = %v, want [unpriced-model]", row.UnpricedModels)
	}
}

func TestGetCostReport_ReportsEffectiveCostReasonBreakdown(t *testing.T) {
	gin.SetMode(gin.TestMode)

	store := &testCostReportStore{
		reportRows: []metrics.CostReportRow{{
			GroupKey: "sk-aa****bb", TotalRequests: 10, SuccessCount: 10,
			InputTokens: 1_000_000, OutputTokens: 500_000,
			EffectivePricedCount:      10,
			EffectiveUnavailableCount: 0,
			ZeroCostCount:             3,
			ConfiguredMultiplierCount: 4,
			SubscriptionCostCount:     2,
			UnpricedCostCount:         1,
		}},
		modelRows: []metrics.ModelCostBreakdownRow{
			{Model: "claude-sonnet-4-8", InputTokens: 1_000_000, OutputTokens: 500_000},
		},
	}
	deps := &CostReportDeps{MetricsManagers: map[string]*metrics.MetricsManager{
		"messages": newTestMetricsManager(store),
	}}

	w := httptest.NewRecorder()
	c, r := gin.CreateTestContext(w)
	r.GET("/api/reports/cost", GetCostReport(deps))
	c.Request = httptest.NewRequest(http.MethodGet, "/api/reports/cost?type=messages", nil)
	r.ServeHTTP(w, c.Request)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var response struct {
		Rows []costReportRow `json:"rows"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(response.Rows) != 1 {
		t.Fatalf("expected one row, got %d", len(response.Rows))
	}
	row := response.Rows[0]
	if row.ZeroCostCount != 3 {
		t.Errorf("zeroCostCount = %d, want 3", row.ZeroCostCount)
	}
	if row.ConfiguredMultiplierCount != 4 {
		t.Errorf("configuredMultiplierCount = %d, want 4", row.ConfiguredMultiplierCount)
	}
	if row.SubscriptionCostCount != 2 {
		t.Errorf("subscriptionCostCount = %d, want 2", row.SubscriptionCostCount)
	}
	if row.UnpricedCostCount != 1 {
		t.Errorf("unpricedCostCount = %d, want 1", row.UnpricedCostCount)
	}
}

func TestGetCostReport_InvalidAPIType(t *testing.T) {
	gin.SetMode(gin.TestMode)

	deps := &CostReportDeps{
		MetricsManagers: map[string]*metrics.MetricsManager{},
	}

	w := httptest.NewRecorder()
	c, r := gin.CreateTestContext(w)
	r.GET("/api/reports/cost", GetCostReport(deps))

	c.Request = httptest.NewRequest(http.MethodGet, "/api/reports/cost?type=invalid", nil)
	r.ServeHTTP(w, c.Request)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetCostReport_NilPersistenceStore(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// MetricsManager without persistence store
	mgr := metrics.NewMetricsManager()
	deps := &CostReportDeps{
		MetricsManagers: map[string]*metrics.MetricsManager{
			"messages": mgr,
		},
	}

	w := httptest.NewRecorder()
	c, r := gin.CreateTestContext(w)
	r.GET("/api/reports/cost", GetCostReport(deps))

	c.Request = httptest.NewRequest(http.MethodGet, "/api/reports/cost?type=messages", nil)
	r.ServeHTTP(w, c.Request)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetCostReport_DefaultGroupBy(t *testing.T) {
	gin.SetMode(gin.TestMode)

	store := &testCostReportStore{
		reportRows: []metrics.CostReportRow{},
		modelRows:  []metrics.ModelCostBreakdownRow{},
	}

	deps := &CostReportDeps{
		MetricsManagers: map[string]*metrics.MetricsManager{
			"messages": newTestMetricsManager(store),
		},
	}

	w := httptest.NewRecorder()
	c, r := gin.CreateTestContext(w)
	r.GET("/api/reports/cost", GetCostReport(deps))

	// No groupBy specified, should default to "user"
	c.Request = httptest.NewRequest(http.MethodGet, "/api/reports/cost", nil)
	r.ServeHTTP(w, c.Request)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["groupBy"] != "user" {
		t.Errorf("expected default groupBy=user, got %v", resp["groupBy"])
	}
}

func TestGetCostReport_AllSupportedAPITypes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	apiTypes := []string{"messages", "responses", "chat", "gemini", "images", "vectors"}
	mgrs := make(map[string]*metrics.MetricsManager)
	for _, at := range apiTypes {
		mgrs[at] = metrics.NewMetricsManager()
	}
	deps := &CostReportDeps{MetricsManagers: mgrs}

	for _, at := range apiTypes {
		w := httptest.NewRecorder()
		c, r := gin.CreateTestContext(w)
		r.GET("/api/reports/cost", GetCostReport(deps))

		c.Request = httptest.NewRequest(http.MethodGet, "/api/reports/cost?type="+at, nil)
		r.ServeHTTP(w, c.Request)

		// All should return 400 because no persistence store (nil)
		if w.Code != http.StatusBadRequest {
			t.Errorf("apiType=%s: expected 400 for nil persistence store, got %d", at, w.Code)
		}
	}
}

func TestParseCostReportDuration(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"1h", time.Hour},
		{"6h", 6 * time.Hour},
		{"24h", 24 * time.Hour},
		{"1d", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"30d", 30 * 24 * time.Hour},
		{"90d", 90 * 24 * time.Hour},
		{"365d", 365 * 24 * time.Hour},
		{"", 7 * 24 * time.Hour},                // default
		{"invalid", 7 * 24 * time.Hour},         // fallback
		{"5m", 5 * time.Minute},                 // standard Go duration
		{"2h30m", 2*time.Hour + 30*time.Minute}, // standard Go duration
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := parseCostReportDuration(tc.input)
			if got != tc.expected {
				t.Errorf("parseCostReportDuration(%q) = %v, want %v", tc.input, got, tc.expected)
			}
		})
	}
}
