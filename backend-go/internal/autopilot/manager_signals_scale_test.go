package autopilot

import "testing"

// 回归：MetricsManager 的 SuccessRate 是 0-100，信号/画像层统一 0-1。
// 曾经 0-100 直通，导致健康判据（<0.10 软死、0.50-0.80 质量降级）几乎永不触发、
// 评分 clamp 饱和，前端成功率显示为 10000%。
func TestCollectSignalsNormalizesSuccessRateScale(t *testing.T) {
	m := &Manager{metrics: newMockProvider(
		TimeWindowStats{RequestCount: 10, SuccessCount: 9, SuccessRate: 90},
		KeyCircuitSnapshot{},
	)}
	signals := m.collectSignals("messages", "https://upstream.example", "sk-test", "chat")
	if signals.SuccessRate1h != 0.90 {
		t.Fatalf("SuccessRate1h = %v, want 0.90（0-1 刻度）", signals.SuccessRate1h)
	}
	if signals.SuccessRate15m != 0.90 {
		t.Fatalf("SuccessRate15m = %v, want 0.90（0-1 刻度）", signals.SuccessRate15m)
	}
}

// 15m 无流量保持零值（DTO omitempty → 前端 '-'）；1h 空窗口沿用 metrics 的 100 惯例 → 1.0
func TestCollectSignalsEmptyWindowScale(t *testing.T) {
	m := &Manager{metrics: newMockProvider(
		TimeWindowStats{RequestCount: 0, SuccessRate: 100},
		KeyCircuitSnapshot{},
	)}
	signals := m.collectSignals("messages", "https://upstream.example", "sk-test", "chat")
	if signals.SuccessRate15m != 0 {
		t.Fatalf("SuccessRate15m = %v, want 0（15m 无流量保持零值）", signals.SuccessRate15m)
	}
	if signals.SuccessRate1h != 1.0 {
		t.Fatalf("SuccessRate1h = %v, want 1.0", signals.SuccessRate1h)
	}
}

// L1 画像刷新是另一个独立的注入点，同样必须归一化
func TestDeriveEndpointProfileNormalizesSuccessRateScale(t *testing.T) {
	p := NewProfiler(newMockProvider(
		TimeWindowStats{RequestCount: 10, SuccessCount: 9, SuccessRate: 90},
		KeyCircuitSnapshot{},
	))
	profile := p.DeriveEndpointProfile("ch-1", 1, "messages", "https://upstream.example", "sk-test", "chat", "", "")
	if profile.SuccessRate15m != 0.90 {
		t.Fatalf("profile.SuccessRate15m = %v, want 0.90（0-1 刻度）", profile.SuccessRate15m)
	}
}
