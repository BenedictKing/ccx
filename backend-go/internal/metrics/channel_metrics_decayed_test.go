package metrics

import (
	"math"
	"testing"
	"time"
)

func TestRecencyWeight(t *testing.T) {
	tests := []struct {
		name string
		age  time.Duration
		want float64
	}{
		{"当前", 0, 1},
		{"负值", -time.Minute, 1},
		{"半衰点 t50=4h", 4 * time.Hour, 0.5},
		{"超过 t50 6h", 6 * time.Hour, 1 / (1 + math.Exp(2))},
		{"久远 24h", 24 * time.Hour, 1 / (1 + math.Exp(20))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RecencyWeight(tt.age)
			if math.Abs(got-tt.want) > 1e-6 {
				t.Errorf("RecencyWeight(%v) = %v, want %v", tt.age, got, tt.want)
			}
		})
	}
}

func TestGetDecayedStatsForKey(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()

	baseURL := "http://test.com"
	apiKey := "test-key"
	now := time.Now()

	m.mu.Lock()
	km := m.getOrCreateKey(baseURL, apiKey, "openai")
	// 10 分钟前失败：权重接近 1
	km.requestHistory = append(km.requestHistory, RequestRecord{Timestamp: now.Add(-10 * time.Minute), Success: false})
	// 4 小时前失败：权重 0.5
	km.requestHistory = append(km.requestHistory, RequestRecord{Timestamp: now.Add(-4 * time.Hour), Success: false})
	// 8 小时前成功：权重 1/(1+e^4) ≈ 0.018
	km.requestHistory = append(km.requestHistory, RequestRecord{Timestamp: now.Add(-8 * time.Hour), Success: true})
	// 30 小时前失败：超出 24h 窗口，不计入
	km.requestHistory = append(km.requestHistory, RequestRecord{Timestamp: now.Add(-30 * time.Hour), Success: false})
	m.mu.Unlock()

	stats := m.GetDecayedStatsForKey(baseURL, apiKey, "openai", 24*time.Hour)

	wantFailure := RecencyWeight(10*time.Minute) + RecencyWeight(4*time.Hour)
	if math.Abs(stats.FailureCount-wantFailure) > 1e-6 {
		t.Errorf("FailureCount = %v, want %v", stats.FailureCount, wantFailure)
	}
	wantSuccess := RecencyWeight(8 * time.Hour)
	if math.Abs(stats.SuccessCount-wantSuccess) > 1e-6 {
		t.Errorf("SuccessCount = %v, want %v", stats.SuccessCount, wantSuccess)
	}
}

func TestGetDecayedStatsForKey_NoRecords(t *testing.T) {
	m := NewMetricsManager()
	defer m.Stop()

	stats := m.GetDecayedStatsForKey("http://none.example", "key", "openai", 24*time.Hour)
	if stats.FailureCount != 0 || stats.SuccessCount != 0 {
		t.Errorf("expected zero stats for unknown key, got %+v", stats)
	}
}
