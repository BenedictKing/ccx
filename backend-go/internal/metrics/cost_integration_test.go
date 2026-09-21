package metrics

import (
	"math"
	"sort"
	"testing"
	"time"
)

// TestGetGlobalHistoricalStatsWithTokens_ComputesCostUSD 验证全局统计会按模型价格
// 把 token 折算为 USD 成本，并汇总到 summary。
func TestGetGlobalHistoricalStatsWithTokens_ComputesCostUSD(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()

	baseURL := "https://example.com"
	apiKey := "sk-test"
	now := time.Now()

	// kimi-k2.7-code: input miss 0.95, output 4 (USD per 1M)
	m.mu.Lock()
	metrics := m.getOrCreateKey(baseURL, apiKey, "openai")
	metrics.requestHistory = append(metrics.requestHistory, RequestRecord{
		Timestamp:    now.Add(-2 * time.Minute),
		Success:      true,
		Model:        "kimi-k2.7-code",
		InputTokens:  1_000_000,
		OutputTokens: 1_000_000,
	})
	m.mu.Unlock()

	result := m.GetGlobalHistoricalStatsWithTokens(time.Hour, 5*time.Minute)

	if result.Summary.TotalCostUSD <= 0 {
		t.Fatalf("expected positive totalCostUSD, got %v", result.Summary.TotalCostUSD)
	}
	// 1M input @ 0.95 + 1M output @ 4 = 4.95 USD
	if math.Abs(result.Summary.TotalCostUSD-4.95) > 0.01 {
		t.Fatalf("expected ~4.95 USD, got %v", result.Summary.TotalCostUSD)
	}

	points, ok := result.ModelDataPoints["kimi-k2.7-code"]
	if !ok || len(points) == 0 {
		t.Fatalf("expected model data points for kimi-k2.7-code")
	}
	var modelCost float64
	for _, p := range points {
		modelCost += p.CostUSD
	}
	if math.Abs(modelCost-4.95) > 0.01 {
		t.Fatalf("expected model cost ~4.95 USD, got %v", modelCost)
	}

	// 全局点成本之和应等于 summary
	var pointCost float64
	for _, dp := range result.DataPoints {
		pointCost += dp.CostUSD
	}
	if math.Abs(pointCost-result.Summary.TotalCostUSD) > 0.0001 {
		t.Fatalf("dataPoints cost %v != summary %v", pointCost, result.Summary.TotalCostUSD)
	}
}

// TestGetKeyModelHistoricalStatsMultiURL_ComputesCostUSD 验证 key+model 维度也带成本。
func TestGetKeyModelHistoricalStatsMultiURL_ComputesCostUSD(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()

	baseURL := "https://example.com"
	apiKey := "sk-test"
	now := time.Now()

	m.mu.Lock()
	metrics := m.getOrCreateKey(baseURL, apiKey, "openai")
	metrics.requestHistory = append(metrics.requestHistory, RequestRecord{
		Timestamp:    now.Add(-2 * time.Minute),
		Success:      true,
		Model:        "kimi-k2.7-code",
		InputTokens:  500_000,
		OutputTokens: 500_000,
	})
	m.mu.Unlock()

	modelData := m.GetKeyModelHistoricalStatsMultiURL([]string{baseURL}, apiKey, "openai", time.Hour, 5*time.Minute)
	points, ok := modelData["kimi-k2.7-code"]
	if !ok || len(points) == 0 {
		t.Fatalf("expected model data points for kimi-k2.7-code")
	}
	var cost float64
	for _, p := range points {
		cost += p.CostUSD
	}
	// 0.5M input @ 0.95 + 0.5M output @ 4 = 2.475 USD
	if math.Abs(cost-2.475) > 0.01 {
		t.Fatalf("expected ~2.475 USD, got %v", cost)
	}
}

// TestRecordFailure_ModelAttribution 验证保活验证等旁路失败带上 model 后：
// per-key 模型分桶按真实模型归因（不再落入 unknown 桶）；传空保持 unknown 兜底。
func TestRecordFailure_ModelAttribution(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()

	baseURL := "https://example.com"
	apiKey := "sk-test"

	m.RecordFailure(baseURL, apiKey, "openai", "deepseek-v4-flash")
	m.RecordSuccess(baseURL, apiKey, "openai", "kimi-k3")
	m.RecordFailure(baseURL, apiKey, "openai", "")

	modelData := m.GetKeyModelHistoricalStatsMultiURL([]string{baseURL}, apiKey, "openai", time.Hour, 5*time.Minute)
	if _, ok := modelData["deepseek-v4-flash"]; !ok {
		t.Fatalf("expected deepseek-v4-flash bucket, got models %v", keysOf(modelData))
	}
	if _, ok := modelData["kimi-k3"]; !ok {
		t.Fatalf("expected kimi-k3 bucket, got models %v", keysOf(modelData))
	}
	unknownPoints, ok := modelData["unknown"]
	if !ok || len(unknownPoints) == 0 {
		t.Fatalf("expected unknown bucket for model-less failure, got models %v", keysOf(modelData))
	}
	var unknownRequests int64
	for _, p := range unknownPoints {
		unknownRequests += p.RequestCount
	}
	if unknownRequests != 1 {
		t.Fatalf("expected 1 unknown request, got %d", unknownRequests)
	}
}

func keysOf(m map[string][]KeyModelHistoryDataPoint) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
