package metrics

import (
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/utils"
)

func TestMultiURLHealthTreatsMissingKeyAsAvailableCandidate(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()

	baseURL := "https://example.com"
	oldKey := "old-key"
	newKey := "new-key"

	m.mu.Lock()
	metrics := m.getOrCreateKey(baseURL, oldKey, "openai")
	metrics.CircuitState = CircuitStateOpen
	m.mu.Unlock()

	if !m.IsChannelHealthyMultiURL([]string{baseURL}, []string{oldKey, newKey}, "openai") {
		t.Fatal("expected channel to remain healthy when a new key has no metrics yet")
	}
	if got := m.CalculateChannelFailureRateMultiURL([]string{baseURL}, []string{oldKey, newKey}, "openai"); got != 0 {
		t.Fatalf("expected failure rate 0 for missing-key candidate, got %v", got)
	}
}

func TestBreakerHealthWindowExpiresOldFailures(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()

	baseURL := "https://example.com"
	apiKey := "sk-test"
	serviceType := "openai"
	old := time.Now().Add(-defaultBreakerHealthWindow - time.Minute)

	m.mu.Lock()
	metrics := m.getOrCreateKey(baseURL, apiKey, serviceType)
	metrics.requestHistory = append(metrics.requestHistory,
		RequestRecord{Timestamp: old, Success: false, FailureClass: FailureClassRetryable},
		RequestRecord{Timestamp: old.Add(time.Second), Success: true},
	)
	metrics.recentResults = []bool{false, true}
	metrics.breakerResults = []bool{false, true}
	metrics.ConsecutiveFailures = 1
	m.mu.Unlock()

	if !m.IsChannelHealthyMultiURL([]string{baseURL}, []string{apiKey}, serviceType) {
		t.Fatal("expected channel to become healthy after breaker health window expires")
	}
	if got := m.CalculateChannelFailureRateMultiURL([]string{baseURL}, []string{apiKey}, serviceType); got != 0 {
		t.Fatalf("expected expired breaker failure rate 0, got %v", got)
	}
	if got := m.GetKeyMetrics(baseURL, apiKey, serviceType).ConsecutiveFailures; got != 0 {
		t.Fatalf("expected expired consecutive failures reset to 0, got %d", got)
	}
}

func TestBreakerHealthWindowKeepsRecentFailures(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()

	baseURL := "https://example.com"
	apiKey := "sk-test"
	serviceType := "openai"
	now := time.Now()

	m.mu.Lock()
	metrics := m.getOrCreateKey(baseURL, apiKey, serviceType)
	metrics.requestHistory = append(metrics.requestHistory,
		RequestRecord{Timestamp: now.Add(-10 * time.Minute), Success: false, FailureClass: FailureClassRetryable},
		RequestRecord{Timestamp: now.Add(-9 * time.Minute), Success: false, FailureClass: FailureClassRetryable},
		RequestRecord{Timestamp: now.Add(-8 * time.Minute), Success: false, FailureClass: FailureClassRetryable},
		RequestRecord{Timestamp: now.Add(-7 * time.Minute), Success: false, FailureClass: FailureClassRetryable},
		RequestRecord{Timestamp: now.Add(-6 * time.Minute), Success: false, FailureClass: FailureClassRetryable},
		RequestRecord{Timestamp: now.Add(-5 * time.Minute), Success: false, FailureClass: FailureClassRetryable},
		RequestRecord{Timestamp: now.Add(-4 * time.Minute), Success: false, FailureClass: FailureClassRetryable},
		RequestRecord{Timestamp: now.Add(-3 * time.Minute), Success: false, FailureClass: FailureClassRetryable},
		RequestRecord{Timestamp: now.Add(-2 * time.Minute), Success: true},
		RequestRecord{Timestamp: now.Add(-time.Minute), Success: true},
	)
	m.refreshBreakerWindowsLocked(metrics, now)
	m.mu.Unlock()

	if m.IsChannelHealthyMultiURL([]string{baseURL}, []string{apiKey}, serviceType) {
		t.Fatal("expected channel to remain unhealthy while recent breaker failures are inside health window")
	}
	if got := m.CalculateChannelFailureRateMultiURL([]string{baseURL}, []string{apiKey}, serviceType); got != 0.8 {
		t.Fatalf("expected recent breaker failure rate 0.8, got %v", got)
	}
}

func TestGetHistoricalStatsMultiURL_DeduplicatesEquivalentURLs(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()

	baseURL := "https://gemini.example.com"
	apiKey := "test-key"
	now := time.Now()

	m.mu.Lock()
	metrics := m.getOrCreateKey(baseURL, apiKey, "")
	metrics.requestHistory = append(metrics.requestHistory, RequestRecord{
		Timestamp: now,
		Success:   true,
	})
	m.mu.Unlock()

	result := m.GetHistoricalStatsMultiURL([]string{baseURL, baseURL + "/v1"}, []string{apiKey}, "", time.Hour, 5*time.Minute)

	var totalRequests int64
	for _, point := range result {
		totalRequests += point.RequestCount
	}
	if totalRequests != 1 {
		t.Fatalf("expected 1 request after deduplicating equivalent URLs, got %d", totalRequests)
	}
}

func TestToResponseMultiURLIncludesHistoricalOnlyChannelWindows(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()

	baseURL := "https://example.com"
	apiKey := "sk-disabled"
	now := time.Now()

	m.mu.Lock()
	metrics := m.getOrCreateKey(baseURL, apiKey, "claude")
	metrics.RequestCount = 2
	metrics.SuccessCount = 1
	metrics.FailureCount = 1
	metrics.LastSuccessAt = &now
	metrics.requestHistory = append(metrics.requestHistory,
		RequestRecord{Timestamp: now.Add(-time.Minute), Success: true, InputTokens: 10, FirstByteLatencyMs: 100},
		RequestRecord{Timestamp: now.Add(-2 * time.Minute), Success: false, OutputTokens: 5, FirstByteLatencyMs: 200},
	)
	m.mu.Unlock()

	resp := m.ToResponseMultiURL(0, []string{baseURL}, nil, "claude", 0, []string{apiKey})
	if resp.RequestCount != 2 {
		t.Fatalf("request count = %d, want 2", resp.RequestCount)
	}
	if resp.LastSuccessAt == nil {
		t.Fatal("lastSuccessAt should be populated for historical-only channel")
	}
	if got := resp.TimeWindows["15m"].RequestCount; got != 2 {
		t.Fatalf("15m request count = %d, want 2", got)
	}
	if stats := resp.TimeWindows["15m"]; stats.FirstByteLatencySamples != 2 || stats.FirstByteLatencyP50Ms != 150 || stats.FirstByteLatencyP95Ms != 195 {
		t.Fatalf("15m first-byte latency stats = %+v, want 2 samples and p50/p95 150/195", stats)
	}
}

func TestGetOrCreateKey_PromotesLegacyMetricsToIdentity(t *testing.T) {
	m := NewMetricsManagerWithConfig(10, 0.5)

	baseURL := "https://api.example.com"
	apiKey := "sk-test"
	serviceType := "openai"
	legacyKey := GenerateMetricsKey(baseURL, apiKey)
	identityKey := GenerateMetricsIdentityKey(baseURL, apiKey, serviceType)
	identityBaseURL := utils.MetricsIdentityBaseURL(baseURL, serviceType)

	legacyMetrics := &KeyMetrics{
		MetricsKey:        legacyKey,
		BaseURL:           baseURL,
		KeyMask:           utils.MaskAPIKey(apiKey),
		CircuitState:      CircuitStateHalfOpen,
		recentResults:     []bool{true},
		breakerResults:    []bool{false},
		pendingHistoryIdx: map[uint64]int{},
	}

	m.mu.Lock()
	m.keyMetrics[legacyKey] = legacyMetrics
	promoted := m.getOrCreateKey(baseURL, apiKey, serviceType)
	m.mu.Unlock()

	if promoted != legacyMetrics {
		t.Fatalf("expected promoted metrics to reuse legacy instance")
	}
	if promoted.MetricsKey != identityKey {
		t.Fatalf("metrics key = %s, want %s", promoted.MetricsKey, identityKey)
	}
	if promoted.BaseURL != identityBaseURL {
		t.Fatalf("baseURL = %s, want %s", promoted.BaseURL, identityBaseURL)
	}
	if _, exists := m.keyMetrics[legacyKey]; exists {
		t.Fatalf("expected legacy key entry removed after promotion")
	}
	if current, exists := m.keyMetrics[identityKey]; !exists || current != legacyMetrics {
		t.Fatalf("expected identity key to point to promoted legacy metrics")
	}
}

func TestGetIdentityMetricsLocked_FindsEquivalentLegacyVariant(t *testing.T) {
	m := NewMetricsManagerWithConfig(10, 0.5)

	baseURL := "https://api.example.com"
	apiKey := "sk-test"
	serviceType := "openai"
	legacyKey := GenerateMetricsKey(baseURL, apiKey)
	legacyMetrics := &KeyMetrics{
		MetricsKey:        legacyKey,
		BaseURL:           baseURL,
		KeyMask:           utils.MaskAPIKey(apiKey),
		CircuitState:      CircuitStateOpen,
		pendingHistoryIdx: map[uint64]int{},
	}

	m.mu.Lock()
	m.keyMetrics[legacyKey] = legacyMetrics
	found := m.getIdentityMetricsLocked(baseURL, apiKey, serviceType)
	m.mu.Unlock()

	if found != legacyMetrics {
		t.Fatalf("expected identity lookup to find equivalent legacy metrics")
	}
}
