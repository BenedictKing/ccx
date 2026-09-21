package metrics

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

func TestMoveKeyToHalfOpenCreatesMetricsAndSwitchesState(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()

	m.MoveKeyToHalfOpen("https://example.com", "sk-test", "claude")

	if got := m.GetKeyCircuitState("https://example.com", "sk-test", "claude"); got != CircuitStateHalfOpen {
		t.Fatalf("circuit state = %v, want %v", got, CircuitStateHalfOpen)
	}

	metricsKey := GenerateMetricsIdentityKey("https://example.com", "sk-test", "claude")
	m.mu.RLock()
	metrics := m.keyMetrics[metricsKey]
	m.mu.RUnlock()
	if metrics == nil {
		t.Fatal("metrics should be created")
	}
	if metrics.NextRetryAt != nil {
		t.Fatalf("NextRetryAt = %v, want nil", metrics.NextRetryAt)
	}
	if metrics.HalfOpenAt == nil {
		t.Fatal("HalfOpenAt should be set")
	}
}

func TestMoveKeyToHalfOpenKeepsBreakerHistory(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()

	m.mu.Lock()
	metrics := m.getOrCreateKey("https://example.com", "sk-test", "claude")
	metrics.breakerResults = []bool{false, false, true}
	metrics.BackoffLevel = 2
	nextRetryAt := time.Now().Add(time.Minute)
	metrics.NextRetryAt = &nextRetryAt
	metrics.CircuitState = CircuitStateOpen
	m.mu.Unlock()

	m.MoveKeyToHalfOpen("https://example.com", "sk-test", "claude")

	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(metrics.breakerResults) != 3 {
		t.Fatalf("breakerResults len = %d, want 3", len(metrics.breakerResults))
	}
	if metrics.BackoffLevel != 2 {
		t.Fatalf("BackoffLevel = %d, want 2", metrics.BackoffLevel)
	}
	if metrics.CircuitState != CircuitStateHalfOpen {
		t.Fatalf("CircuitState = %v, want %v", metrics.CircuitState, CircuitStateHalfOpen)
	}
}

// 单一已知模型的过载不再立即熔断 Key：失败明确归因到单个模型时，
// 仅累计窗口与连续计数，不触发 Key 级熔断，避免殃及同 Key 下其他健康模型。
func TestOverloadedFailureClassSingleModelDoesNotOpenCircuit(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()

	recordModelFailure(m, "https://example.com", "sk-test", "claude", "gpt-5.6-luna", FailureClassOverloaded)

	if got := m.GetKeyCircuitState("https://example.com", "sk-test", "claude"); got != CircuitStateClosed {
		t.Fatalf("circuit state = %v, want %v (single-model overload must not open circuit)", got, CircuitStateClosed)
	}

	metricsKey := GenerateMetricsIdentityKey("https://example.com", "sk-test", "claude")
	m.mu.RLock()
	keyMetrics := m.keyMetrics[metricsKey]
	m.mu.RUnlock()
	if keyMetrics == nil {
		t.Fatal("metrics should be created")
	}
	if keyMetrics.ConsecutiveFailures != 1 {
		t.Fatalf("ConsecutiveFailures = %d, want 1", keyMetrics.ConsecutiveFailures)
	}
}

// seedModelFailures 向 Key 历史写入指定模型的失败记录，用于触发模型多样性熔断判定。
func seedModelFailures(m *MetricsManager, baseURL, apiKey, serviceType string, failures map[string]int, failureClass FailureClass) {
	m.mu.Lock()
	defer m.mu.Unlock()
	metrics := m.getOrCreateKey(baseURL, apiKey, serviceType)
	now := time.Now()
	for model, count := range failures {
		for i := 0; i < count; i++ {
			metrics.requestHistory = append(metrics.requestHistory, RequestRecord{
				Model:        model,
				Timestamp:    now,
				Success:      false,
				FailureClass: failureClass,
			})
			metrics.FailureCount++
			metrics.RequestCount++
		}
	}
	metrics.LastFailureAt = &now
}

// recordModelFailure 通过连接+回写路径记录一次带模型名的失败，驱动熔断状态机评估。
func recordModelFailure(m *MetricsManager, baseURL, apiKey, serviceType, model string, failureClass FailureClass) {
	reqID := m.RecordRequestConnected(baseURL, apiKey, serviceType, model)
	m.RecordRequestFinalizeFailureWithClass(baseURL, apiKey, serviceType, reqID, failureClass)
}

func TestKeyCircuitBreakerModelDiversityGate(t *testing.T) {
	tests := []struct {
		name         string
		failures     map[string]int // model -> 连续失败次数
		driverModel  string         // 驱动状态机的本次失败所用模型
		failureClass FailureClass
		wantState    CircuitState
	}{
		{
			name:         "单模型过载达到连续阈值不熔断",
			failures:     map[string]int{"gpt-5.6-luna": 6},
			driverModel:  "gpt-5.6-luna",
			failureClass: FailureClassOverloaded,
			wantState:    CircuitStateClosed,
		},
		{
			name:         "单模型可重试失败达到连续阈值不熔断",
			failures:     map[string]int{"gpt-5.6-luna": 6},
			driverModel:  "gpt-5.6-luna",
			failureClass: FailureClassRetryable,
			wantState:    CircuitStateClosed,
		},
		{
			name:         "跨两模型可重试失败达到阈值熔断",
			failures:     map[string]int{"gpt-5.6-luna": 3, "claude-opus": 3},
			driverModel:  "claude-opus",
			failureClass: FailureClassRetryable,
			wantState:    CircuitStateOpen,
		},
		{
			name:         "跨两模型过载立即熔断",
			failures:     map[string]int{"gpt-5.6-luna": 1, "claude-opus": 1},
			driverModel:  "claude-opus",
			failureClass: FailureClassOverloaded,
			wantState:    CircuitStateOpen,
		},
		{
			name:         "未知模型（空桶）失败达到连续阈值仍熔断",
			failures:     map[string]int{"": 6},
			driverModel:  "",
			failureClass: FailureClassRetryable,
			wantState:    CircuitStateOpen,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMetricsManager()
			defer m.Stop()

			seedModelFailures(m, "https://example.com", "sk-test", "claude", tt.failures, tt.failureClass)

			// 通过一次带模型名的失败驱动熔断状态机评估
			recordModelFailure(m, "https://example.com", "sk-test", "claude", tt.driverModel, tt.failureClass)

			if got := m.GetKeyCircuitState("https://example.com", "sk-test", "claude"); got != tt.wantState {
				t.Fatalf("circuit state = %v, want %v", got, tt.wantState)
			}
		})
	}
}

func TestQuotaFailureClassDoesNotAffectCircuit(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()

	m.RecordFailureWithClass("https://example.com", "sk-test", "claude", "", FailureClassQuota)

	if got := m.GetKeyCircuitState("https://example.com", "sk-test", "claude"); got != CircuitStateClosed {
		t.Fatalf("circuit state = %v, want %v", got, CircuitStateClosed)
	}

	metricsKey := GenerateMetricsIdentityKey("https://example.com", "sk-test", "claude")
	m.mu.RLock()
	keyMetrics := m.keyMetrics[metricsKey]
	m.mu.RUnlock()
	if keyMetrics == nil {
		t.Fatal("metrics should be created")
	}
	if keyMetrics.ConsecutiveFailures != 0 {
		t.Fatalf("ConsecutiveFailures = %d, want 0", keyMetrics.ConsecutiveFailures)
	}
}

func TestToResponseMultiURLCircuitStateUsesChannelAvailability(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()

	m.MoveKeyToHalfOpen("https://example.com", "sk-recovered", "claude")
	m.RecordSuccess("https://example.com", "sk-active", "claude", "")

	resp := m.ToResponseMultiURL(0, []string{"https://example.com"}, []string{"sk-active", "sk-recovered"}, "claude", 0)

	if resp.CircuitState != "closed" {
		t.Fatalf("CircuitState = %q, want closed when another active key is healthy", resp.CircuitState)
	}
}

func TestToResponseMultiURLCircuitStateClosedWhenOneBaseURLRecovered(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()

	baseURLs := []string{"https://primary.example.com", "https://backup.example.com"}
	for _, baseURL := range baseURLs {
		m.MoveKeyToHalfOpen(baseURL, "sk-recovered", "claude")
	}
	m.RecordSuccess("https://primary.example.com", "sk-recovered", "claude", "")

	resp := m.ToResponseMultiURL(0, baseURLs, []string{"sk-recovered"}, "claude", 0)

	if resp.CircuitState != "closed" {
		t.Fatalf("CircuitState = %q, want closed when any baseURL/key candidate is healthy", resp.CircuitState)
	}
}

func TestCircuitLogsIncludeTransitionFields(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()

	var buf bytes.Buffer
	origWriter := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(origWriter)

	for i := 0; i < 5; i++ {
		m.RecordFailure("https://example.com", "sk-test", "claude", "")
	}
	m.MoveKeyToHalfOpen("https://example.com", "sk-test", "claude")
	m.RecordSuccess("https://example.com", "sk-test", "claude", "")

	output := buf.String()
	if !strings.Contains(output, "from=closed") || !strings.Contains(output, "to=open") || !strings.Contains(output, "cause=breaker_threshold") {
		t.Fatalf("open transition fields missing: %q", output)
	}
	if !strings.Contains(output, "from=half_open") || !strings.Contains(output, "to=closed") || !strings.Contains(output, "cause=probe_success") {
		t.Fatalf("probe success transition fields missing: %q", output)
	}
}
