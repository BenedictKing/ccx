package metrics

import (
	"testing"
	"time"
)

const (
	evidenceBaseURL     = "https://api.kimi.com/coding/v1"
	evidenceAPIKey      = "sk-test"
	evidenceServiceType = "openai"
)

func TestGetBreakerEvidenceByMetricsKeys(t *testing.T) {
	metricsKey := GenerateMetricsIdentityKey(evidenceBaseURL, evidenceAPIKey, evidenceServiceType)
	stale := processStartedAt.Add(-2 * time.Hour)
	fresh := time.Now()
	retry := time.Now().Add(time.Hour)

	tests := []struct {
		name         string
		setup        func(*KeyMetrics)
		metricsKeys  []string
		wantNil      bool
		wantState    string
		wantPredates bool
	}{
		{
			name:        "无匹配 metricsKey 返回 nil",
			setup:       func(*KeyMetrics) {},
			metricsKeys: []string{"nonexistent"},
			wantNil:     true,
		},
		{
			name:        "空 metricsKeys 返回 nil",
			setup:       func(*KeyMetrics) {},
			metricsKeys: nil,
			wantNil:     true,
		},
		{
			name: "open 且失败早于启动时间标记 predatesRestart",
			setup: func(km *KeyMetrics) {
				km.CircuitState = CircuitStateOpen
				km.CircuitBrokenAt = &stale
				km.LastFailureAt = &stale
				km.NextRetryAt = &retry
				km.BackoffLevel = 3
			},
			metricsKeys:  []string{metricsKey},
			wantState:    "open",
			wantPredates: true,
		},
		{
			name: "open 且失败晚于启动时间不标记 predatesRestart",
			setup: func(km *KeyMetrics) {
				km.CircuitState = CircuitStateOpen
				km.LastFailureAt = &fresh
				km.NextRetryAt = &retry
			},
			metricsKeys:  []string{metricsKey},
			wantState:    "open",
			wantPredates: false,
		},
		{
			name: "closed 态不推导 predatesRestart",
			setup: func(km *KeyMetrics) {
				km.CircuitState = CircuitStateClosed
				km.LastFailureAt = &stale
			},
			metricsKeys:  []string{metricsKey},
			wantState:    "closed",
			wantPredates: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMetricsManager()
			defer m.Stop()

			m.mu.Lock()
			km := m.getOrCreateKeyLocked(evidenceBaseURL, metricsKey, "sk-t***est")
			tt.setup(km)
			m.mu.Unlock()

			got := m.GetBreakerEvidenceByMetricsKeys(tt.metricsKeys)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("evidence = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("evidence = nil, want non-nil")
			}
			if got.CircuitState != tt.wantState {
				t.Errorf("CircuitState = %q, want %q", got.CircuitState, tt.wantState)
			}
			if got.PredatesRestart != tt.wantPredates {
				t.Errorf("PredatesRestart = %v, want %v", got.PredatesRestart, tt.wantPredates)
			}
			if got.ProcessStartedAt.IsZero() {
				t.Error("ProcessStartedAt 不应为零值")
			}
		})
	}
}

// 多 key 渠道应取最严重状态：open 优先于 half_open，避免界面把渠道显示成尚可用。
func TestGetBreakerEvidencePicksMostSevereState(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()

	halfOpenKey := GenerateMetricsIdentityKey(evidenceBaseURL, "sk-half", evidenceServiceType)
	openKey := GenerateMetricsIdentityKey(evidenceBaseURL, "sk-open", evidenceServiceType)
	retry := time.Now().Add(time.Hour)

	m.mu.Lock()
	halfOpen := m.getOrCreateKeyLocked(evidenceBaseURL, halfOpenKey, "sk-h***alf")
	halfOpen.CircuitState = CircuitStateHalfOpen
	open := m.getOrCreateKeyLocked(evidenceBaseURL, openKey, "sk-o***pen")
	open.CircuitState = CircuitStateOpen
	open.NextRetryAt = &retry
	m.mu.Unlock()

	got := m.GetBreakerEvidenceByMetricsKeys([]string{halfOpenKey, openKey})
	if got == nil {
		t.Fatal("evidence = nil, want non-nil")
	}
	if got.CircuitState != "open" {
		t.Errorf("CircuitState = %q, want \"open\"", got.CircuitState)
	}
	// NextRetryAt 取自 open 那个 key，确认选中的确实是最严重者而非 half_open
	if got.NextRetryAt == nil {
		t.Error("NextRetryAt = nil, want open key 的重试时间")
	}
}
