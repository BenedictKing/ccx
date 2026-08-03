package autopilot

import (
	"testing"
	"time"
)

func TestHealthAnalyzer_Diagnose(t *testing.T) {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	_ = now.Add(-1 * time.Hour) // reserved for future use
	sevenHoursAgo := now.Add(-7 * time.Hour)
	thirtyMinAgo := now.Add(-30 * time.Minute)

	ha := NewHealthAnalyzer()

	tests := []struct {
		name        string
		signals     EndpointSignals
		wantState   HealthState
		wantDeath   DeathType
		wantMinConf float64 // 最低置信度
	}{
		// ── Dead: 硬死 ──
		{
			name: "硬死-全部401",
			signals: EndpointSignals{
				TotalRequests1h:  10,
				AuthFailureCount: 10,
				SuccessCount1h:   0,
				FailureCount1h:   10,
				TotalRequests15m: 3,
				Now:              now,
			},
			wantState:   HealthStateDead,
			wantDeath:   DeathTypeHard,
			wantMinConf: 0.95,
		},
		{
			name: "硬死-部分认证失败且无成功",
			signals: EndpointSignals{
				TotalRequests1h:  5,
				AuthFailureCount: 3,
				SuccessCount1h:   0,
				FailureCount1h:   5,
				TotalRequests15m: 2,
				Now:              now,
			},
			wantState:   HealthStateDead,
			wantDeath:   DeathTypeHard,
			wantMinConf: 0.95,
		},
		{
			name: "硬死-全部DNS失败",
			signals: EndpointSignals{
				TotalRequests1h:  8,
				DNSFailureCount:  8,
				SuccessCount1h:   0,
				FailureCount1h:   8,
				TotalRequests15m: 3,
				Now:              now,
			},
			wantState:   HealthStateDead,
			wantDeath:   DeathTypeHard,
			wantMinConf: 0.95,
		},
		{
			name: "硬死-连续失败>=15",
			signals: EndpointSignals{
				TotalRequests1h:  20,
				SuccessCount1h:   0,
				FailureCount1h:   20,
				ConsecutiveFail:  15,
				TotalRequests15m: 5,
				Now:              now,
			},
			wantState:   HealthStateDead,
			wantDeath:   DeathTypeUnknown,
			wantMinConf: 0.95,
		},
		// ── Dead: 软死 ──
		{
			name: "软死-24小时无成功",
			signals: EndpointSignals{
				TotalRequests24h: 50,
				SuccessCount24h:  0,
				FailureCount24h:  50,
				TotalRequests1h:  5,
				SuccessCount1h:   0,
				FailureCount1h:   5,
				TotalRequests15m: 2,
				Now:              now,
			},
			wantState:   HealthStateDead,
			wantDeath:   DeathTypeSoft,
			wantMinConf: 0.80,
		},
		{
			name: "软死-熔断器open且超6小时无成功",
			signals: EndpointSignals{
				CircuitBreakerOpen: true,
				LastSuccessAt:      &sevenHoursAgo,
				TotalRequests1h:    3,
				SuccessCount1h:     0,
				FailureCount1h:     3,
				TotalRequests15m:   1,
				Now:                now,
			},
			wantState:   HealthStateDead,
			wantDeath:   DeathTypeSoft,
			wantMinConf: 0.80,
		},
		{
			name: "软死-成功率低于10%",
			signals: EndpointSignals{
				TotalRequests1h:  20,
				SuccessCount1h:   1,
				FailureCount1h:   19,
				SuccessRate1h:    0.05,
				TotalRequests15m: 5,
				Now:              now,
			},
			wantState:   HealthStateDead,
			wantDeath:   DeathTypeSoft,
			wantMinConf: 0.80,
		},
		// ── Limited ──
		{
			name: "限流-429过载占比>30%",
			signals: EndpointSignals{
				TotalRequests15m: 10,
				OverloadedCount:  4,
				TotalRequests1h:  30,
				SuccessCount1h:   20,
				FailureCount1h:   10,
				SuccessRate1h:    0.67,
				Now:              now,
			},
			wantState:   HealthStateLimited,
			wantDeath:   DeathTypeSoft,
			wantMinConf: 0.80,
		},
		{
			name: "限流-Retry-After头",
			signals: EndpointSignals{
				RetryAfterCount:  2,
				TotalRequests1h:  20,
				SuccessCount1h:   15,
				FailureCount1h:   5,
				SuccessRate1h:    0.75,
				TotalRequests15m: 5,
				Now:              now,
			},
			wantState:   HealthStateLimited,
			wantDeath:   DeathTypeSoft,
			wantMinConf: 0.80,
		},
		{
			name: "限流-配额耗尽",
			signals: EndpointSignals{
				QuotaFailureCount: 3,
				TotalRequests1h:   10,
				SuccessCount1h:    5,
				FailureCount1h:    5,
				SuccessRate1h:     0.50,
				TotalRequests15m:  3,
				Now:               now,
			},
			wantState:   HealthStateLimited,
			wantDeath:   DeathTypeSoft,
			wantMinConf: 0.80,
		},
		{
			name: "限流-熔断器open但有最近成功",
			signals: EndpointSignals{
				CircuitBreakerOpen: true,
				LastSuccessAt:      &thirtyMinAgo,
				TotalRequests1h:    10,
				SuccessCount1h:     3,
				FailureCount1h:     7,
				SuccessRate1h:      0.30,
				TotalRequests15m:   3,
				Now:                now,
			},
			wantState:   HealthStateLimited,
			wantDeath:   DeathTypeSoft,
			wantMinConf: 0.80,
		},
		// ── Misconfigured ──
		{
			name: "配置错误-全部404样本不足",
			signals: EndpointSignals{
				TotalRequests1h:  4,
				NotFoundCount:    4,
				SuccessCount1h:   0,
				FailureCount1h:   4,
				TotalRequests15m: 1,
				Now:              now,
			},
			// 样本 < 5 不触发软死成功率规则，全部404触发 Misconfigured
			wantState:   HealthStateMisconfigured,
			wantDeath:   DeathTypeModel,
			wantMinConf: 0.85,
		},
		{
			name: "配置错误-全部404但Dead优先",
			signals: EndpointSignals{
				TotalRequests1h:  5,
				NotFoundCount:    5,
				SuccessCount1h:   0,
				FailureCount1h:   5,
				TotalRequests15m: 2,
				Now:              now,
			},
			// Dead 规则优先于 Misconfigured：成功率 0%（样本 >= 5）触发软死。
			wantState:   HealthStateDead,
			wantDeath:   DeathTypeSoft,
			wantMinConf: 0.80,
		},
		{
			name: "配置错误-501协议不支持",
			signals: EndpointSignals{
				TotalRequests1h:    3,
				ProtocolErrorCount: 3,
				SuccessCount1h:     0,
				FailureCount1h:     3,
				TotalRequests15m:   1,
				Now:                now,
			},
			wantState:   HealthStateMisconfigured,
			wantDeath:   DeathTypeHard,
			wantMinConf: 0.85,
		},
		// ── Degraded ──
		{
			name: "质量降级-成功率50-80%",
			signals: EndpointSignals{
				TotalRequests1h:  20,
				SuccessCount1h:   13,
				FailureCount1h:   7,
				SuccessRate1h:    0.65,
				TotalRequests15m: 5,
				Now:              now,
			},
			wantState:   HealthStateDegraded,
			wantDeath:   DeathTypeUnknown,
			wantMinConf: 0.70,
		},
		{
			name: "质量降级-断流率>20%",
			signals: EndpointSignals{
				TotalRequests1h:  20,
				SuccessCount1h:   18,
				FailureCount1h:   2,
				SuccessRate1h:    0.90,
				StreamBreakCount: 6,
				TotalRequests15m: 5,
				Now:              now,
			},
			wantState:   HealthStateDegraded,
			wantDeath:   DeathTypeQuality,
			wantMinConf: 0.70,
		},
		{
			name: "质量降级-空响应率>10%",
			signals: EndpointSignals{
				TotalRequests1h:    20,
				SuccessCount1h:     18,
				FailureCount1h:     2,
				SuccessRate1h:      0.90,
				EmptyResponseCount: 4,
				TotalRequests15m:   5,
				Now:                now,
			},
			wantState:   HealthStateDegraded,
			wantDeath:   DeathTypeQuality,
			wantMinConf: 0.70,
		},
		// ── Healthy ──
		{
			name: "健康-高成功率充足样本",
			signals: EndpointSignals{
				TotalRequests1h:  50,
				SuccessCount1h:   48,
				FailureCount1h:   2,
				SuccessRate1h:    0.96,
				TotalRequests15m: 12,
				Now:              now,
			},
			wantState:   HealthStateHealthy,
			wantDeath:   DeathTypeUnknown,
			wantMinConf: 0.80,
		},
		{
			name: "健康-低置信度有成功但样本不足",
			signals: EndpointSignals{
				TotalRequests1h:  3,
				SuccessCount1h:   3,
				FailureCount1h:   0,
				SuccessRate1h:    1.0,
				TotalRequests15m: 1,
				Now:              now,
			},
			wantState:   HealthStateHealthy,
			wantDeath:   DeathTypeUnknown,
			wantMinConf: 0.50,
		},
		{
			name: "健康-边界恰好80%",
			signals: EndpointSignals{
				TotalRequests1h:  10,
				SuccessCount1h:   8,
				FailureCount1h:   2,
				SuccessRate1h:    0.80,
				TotalRequests15m: 3,
				Now:              now,
			},
			wantState:   HealthStateHealthy,
			wantDeath:   DeathTypeUnknown,
			wantMinConf: 0.80,
		},
		// ── Unknown ──
		{
			name: "未知-无任何请求",
			signals: EndpointSignals{
				TotalRequests1h:  0,
				SuccessCount1h:   0,
				FailureCount1h:   0,
				TotalRequests15m: 0,
				Now:              now,
			},
			wantState:   HealthStateUnknown,
			wantDeath:   DeathTypeUnknown,
			wantMinConf: 0.0,
		},
		{
			name: "未知-仅有失败无成功且样本小",
			signals: EndpointSignals{
				TotalRequests1h:  2,
				SuccessCount1h:   0,
				FailureCount1h:   2,
				TotalRequests15m: 1,
				Now:              now,
			},
			wantState:   HealthStateUnknown,
			wantDeath:   DeathTypeUnknown,
			wantMinConf: 0.0,
		},
		{
			name: "未知-零值Now自动填充",
			signals: EndpointSignals{
				TotalRequests1h: 0,
				// Now 零值，应自动填充
			},
			wantState:   HealthStateUnknown,
			wantDeath:   DeathTypeUnknown,
			wantMinConf: 0.0,
		},
		// ── 边界条件 ──
		{
			name: "边界-连续失败14次不死",
			signals: EndpointSignals{
				ConsecutiveFail:  14,
				TotalRequests1h:  4,
				SuccessCount1h:   0,
				FailureCount1h:   4,
				TotalRequests15m: 1,
				Now:              now,
			},
			// 14 连续失败不足 15，不会硬死；
			// 成功率为 0 但样本 = 4 < 5，不满足软死成功率条件；
			// 无 429/quota/404/501 等特殊错误；
			// 最终落到 unknown
			wantState:   HealthStateUnknown,
			wantDeath:   DeathTypeUnknown,
			wantMinConf: 0.0,
		},
		{
			name: "边界-成功率恰好10%不死",
			signals: EndpointSignals{
				TotalRequests1h:  10,
				SuccessCount1h:   1,
				FailureCount1h:   9,
				SuccessRate1h:    0.10,
				TotalRequests15m: 3,
				Now:              now,
			},
			// 成功率恰好 10%：不满足 < 10% 软死条件，也不满足 >= 80% 健康条件
			wantState:   HealthStateUnknown,
			wantDeath:   DeathTypeUnknown,
			wantMinConf: 0.0,
		},
		{
			name: "优先级-Dead高于Limited",
			signals: EndpointSignals{
				TotalRequests1h:  10,
				AuthFailureCount: 10,
				OverloadedCount:  5,
				SuccessCount1h:   0,
				FailureCount1h:   10,
				TotalRequests15m: 5,
				Now:              now,
			},
			wantState:   HealthStateDead,
			wantDeath:   DeathTypeHard,
			wantMinConf: 0.95,
		},
		{
			name: "优先级-Limited高于Degraded",
			signals: EndpointSignals{
				TotalRequests1h:  20,
				SuccessCount1h:   10,
				FailureCount1h:   10,
				SuccessRate1h:    0.50,
				TotalRequests15m: 10,
				OverloadedCount:  4, // 40% > 30%
				Now:              now,
			},
			wantState:   HealthStateLimited,
			wantDeath:   DeathTypeSoft,
			wantMinConf: 0.80,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ha.Diagnose(tt.signals)

			if result.State != tt.wantState {
				t.Errorf("State = %q, want %q (reason: %s)", result.State, tt.wantState, result.Reason)
			}
			if result.DeathType != tt.wantDeath {
				t.Errorf("DeathType = %q, want %q", result.DeathType, tt.wantDeath)
			}
			if result.Confidence < tt.wantMinConf {
				t.Errorf("Confidence = %.2f, want >= %.2f", result.Confidence, tt.wantMinConf)
			}
			if result.Reason == "" {
				t.Error("Reason should not be empty")
			}
		})
	}
}

func TestHealthAnalyzer_Diagnose_ReasonContainsChinese(t *testing.T) {
	ha := NewHealthAnalyzer()
	now := time.Now()

	result := ha.Diagnose(EndpointSignals{
		TotalRequests1h:  10,
		SuccessCount1h:   9,
		FailureCount1h:   1,
		SuccessRate1h:    0.90,
		TotalRequests15m: 3,
		Now:              now,
	})
	if result.State != HealthStateHealthy {
		t.Errorf("expected healthy, got %q", result.State)
	}
	// Reason 应包含中文
	hasChinese := false
	for _, r := range result.Reason {
		if r >= 0x4E00 && r <= 0x9FFF {
			hasChinese = true
			break
		}
	}
	if !hasChinese {
		t.Errorf("Reason should contain Chinese characters: %q", result.Reason)
	}
}

func TestAggregateHealthState(t *testing.T) {
	tests := []struct {
		name    string
		results []DiagnosisResult
		want    HealthState
	}{
		{
			name:    "空列表返回unknown",
			results: nil,
			want:    HealthStateUnknown,
		},
		{
			name: "全部healthy返回healthy",
			results: []DiagnosisResult{
				{State: HealthStateHealthy},
				{State: HealthStateHealthy},
			},
			want: HealthStateHealthy,
		},
		{
			name: "混合healthy和dead降为degraded",
			results: []DiagnosisResult{
				{State: HealthStateHealthy},
				{State: HealthStateHealthy},
				{State: HealthStateDead},
			},
			want: HealthStateDegraded,
		},
		{
			name: "degraded和limited取limited",
			results: []DiagnosisResult{
				{State: HealthStateDegraded},
				{State: HealthStateLimited},
			},
			want: HealthStateLimited,
		},
		{
			name: "单个endpoint",
			results: []DiagnosisResult{
				{State: HealthStateDegraded},
			},
			want: HealthStateDegraded,
		},
		{
			name: "unknown和healthy取degraded",
			results: []DiagnosisResult{
				{State: HealthStateHealthy},
				{State: HealthStateUnknown},
			},
			want: HealthStateDegraded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AggregateHealthState(tt.results)
			if got != tt.want {
				t.Errorf("AggregateHealthState() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{42, "42"},
		{100, "100"},
		{-1, "-1"},
		{-15, "-15"},
		{999999, "999999"},
	}
	for _, tt := range tests {
		got := itoa(tt.input)
		if got != tt.want {
			t.Errorf("itoa(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestFormatPercent(t *testing.T) {
	tests := []struct {
		input float64
		want  string
	}{
		{0.0, "0.0%"},
		{1.0, "100.0%"},
		{0.5, "50.0%"},
		{0.735, "73.5%"},
		{0.10, "10.0%"},
	}
	for _, tt := range tests {
		got := formatPercent(tt.input)
		if got != tt.want {
			t.Errorf("formatPercent(%f) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
