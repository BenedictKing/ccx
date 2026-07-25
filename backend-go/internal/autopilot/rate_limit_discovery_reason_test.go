package autopilot

import (
	"net/http"
	"testing"
	"time"
)

func TestDiscoverer_429_NoRetryAfter_LowConfidence(t *testing.T) {
	d := NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: true, MaxAutoRPM: 120})
	sig := RateLimitSignal{Source: SignalSource429, Timestamp: time.Now()}
	d.Observe("ep-a", sig)
	s := d.SuggestedLimit("ep-a")
	if s.Confidence < 0.5 || s.Confidence >= 0.6 {
		t.Fatalf("普通无 Retry-After 429 confidence=%.2f, want [0.5,0.6)", s.Confidence)
	}
	if s.RPM <= 0 {
		t.Fatalf("RPM=%d, 应 > 0", s.RPM)
	}
}

func TestDiscoverer_429_AccountRateLimit_MidConfidence(t *testing.T) {
	d := NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: true, MaxAutoRPM: 120})
	sig := RateLimitSignal{Source: SignalSource429, Reason: RateLimitReasonAccountRateLimitExceeded, Timestamp: time.Now()}
	d.Observe("ep-b", sig)
	s := d.SuggestedLimit("ep-b")
	if s.Confidence < 0.6 || s.Confidence >= 0.7 {
		t.Fatalf("已确认账号限流 confidence=%.2f, want [0.6,0.7)", s.Confidence)
	}
}

func TestDiscoverer_429_RetryAfter_HighConfidence(t *testing.T) {
	d := NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: true, MaxAutoRPM: 120})
	sig := RateLimitSignal{Source: SignalSource429, HasRetryAfter: true, RetryAfterSeconds: 30, Timestamp: time.Now()}
	d.Observe("ep-c", sig)
	s := d.SuggestedLimit("ep-c")
	if s.Confidence < 0.7 {
		t.Fatalf("Retry-After confidence=%.2f, want >= 0.7", s.Confidence)
	}
}

func TestDiscoverer_429_DoesNotLowerExistingHighConfidence(t *testing.T) {
	d := NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: true, MaxAutoRPM: 120, HeaderConfidenceMax: 0.9})
	// 先用 header 建立高置信基线
	hdrSig := RateLimitSignal{Source: SignalSourceHeader, Limit: 60, WindowSeconds: 60, Timestamp: time.Now()}
	d.Observe("ep-d", hdrSig)
	before := d.SuggestedLimit("ep-d")
	if before.Confidence < 0.85 {
		t.Fatalf("header baseline confidence=%.2f, want >= 0.85", before.Confidence)
	}
	// 再来一个普通 429（无 Retry-After）：RPM 应降，confidence 不应降低
	sig429 := RateLimitSignal{Source: SignalSource429, Timestamp: time.Now()}
	d.Observe("ep-d", sig429)
	after := d.SuggestedLimit("ep-d")
	if after.Confidence < before.Confidence {
		t.Fatalf("429 不应降低既有 confidence: %.2f -> %.2f", before.Confidence, after.Confidence)
	}
	if after.RPM >= before.RPM {
		t.Fatalf("429 应降低 RPM: %d -> %d", before.RPM, after.RPM)
	}
}

func TestDiscoverer_429_PassiveAimdDisabled_NoChange(t *testing.T) {
	d := NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: false, MaxAutoRPM: 120})
	sig := RateLimitSignal{Source: SignalSource429, Reason: RateLimitReasonAccountRateLimitExceeded, Timestamp: time.Now()}
	d.Observe("ep-e", sig)
	s := d.SuggestedLimit("ep-e")
	// PassiveAimdEnabled=false：429 不改变估算（RPM 仍 0，confidence 仍 0）
	if s.RPM != 0 {
		t.Fatalf("PassiveAimdEnabled=false 时 424 RPM 应不变 (0), got %d", s.RPM)
	}
	if s.Confidence != 0 {
		t.Fatalf("PassiveAimdEnabled=false 时 confidence 应不变 (0), got %.2f", s.Confidence)
	}
	// 但 Last429At 仍记录（画像展示）
	st := d.GetState("ep-e")
	if st == nil || st.Last429At == nil {
		t.Fatal("Last429At 应被记录")
	}
}

func TestDiscoverer_Success_PassiveAimdDisabled_NoIncrease(t *testing.T) {
	d := NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: false, MaxAutoRPM: 120})
	// 预置基线
	d.Observe("ep-f", RateLimitSignal{Source: SignalSource429, HasRetryAfter: true, RetryAfterSeconds: 30, Timestamp: time.Now()})
	before := d.SuggestedLimit("ep-f")
	// 连续成功信号（时间推进超过 grace）
	now := time.Now()
	succSig := RateLimitSignal{Source: SignalSourceSuccess, LatencyMs: 100, Timestamp: now.Add(20 * time.Minute)}
	d.Observe("ep-f", succSig)
	after := d.SuggestedLimit("ep-f")
	if after.RPM > before.RPM {
		t.Fatalf("PassiveAimdEnabled=false 时成功不应上调 RPM: %d -> %d", before.RPM, after.RPM)
	}
}

func TestDiscoverer_UpdateConfig_Applied(t *testing.T) {
	d := NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: true, MaxAutoRPM: 120})
	// 关闭 AIMD
	d.UpdateConfig(RateLimitDiscovererConfig{PassiveAimdEnabled: false, MaxAutoRPM: 120})
	sig := RateLimitSignal{Source: SignalSource429, Timestamp: time.Now()}
	d.Observe("ep-g", sig)
	s := d.SuggestedLimit("ep-g")
	if s.RPM != 0 {
		t.Fatalf("UpdateConfig 后 PassiveAimdEnabled=false 应阻止 429 降速, got RPM=%d", s.RPM)
	}
}

func TestDiscoverer_429_RetryAfter_HalvesRPM(t *testing.T) {
	d := NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: true, MaxAutoRPM: 120})
	d.Observe("ep-h", RateLimitSignal{Source: SignalSource429, HasRetryAfter: true, RetryAfterSeconds: 30, Timestamp: time.Now()})
	s := d.SuggestedLimit("ep-h")
	// MaxAutoRPM/2=60 起点降半 -> 30
	if s.RPM != 30 {
		t.Fatalf("Retry-After ×0.5: RPM=%d, want 30", s.RPM)
	}
}

func TestDiscoverer_429_AccountRateLimit_07Factor(t *testing.T) {
	d := NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: true, MaxAutoRPM: 120})
	d.Observe("ep-i", RateLimitSignal{Source: SignalSource429, Reason: RateLimitReasonAccountRateLimitExceeded, Timestamp: time.Now()})
	s := d.SuggestedLimit("ep-i")
	// 60 × 0.7 = 42
	if s.RPM != 42 {
		t.Fatalf("AccountRateLimit ×0.7: RPM=%d, want 42", s.RPM)
	}
}

// 验证 HTTP 429 头解析 Retry-After 后 reason 仍透传到 discoverer
func TestDiscoverer_ReasonPropagatedThroughSignal(t *testing.T) {
	d := NewRateLimitDiscoverer(RateLimitDiscovererConfig{PassiveAimdEnabled: true, MaxAutoRPM: 120})
	sig := RateLimitSignal{Source: SignalSource429, Reason: RateLimitReasonAccountRateLimitExceeded, HasRetryAfter: true, RetryAfterSeconds: 30, Timestamp: time.Now()}
	d.Observe("ep-j", sig)
	s := d.SuggestedLimit("ep-j")
	// Retry-After 优先：×0.5 -> 30, confidence >= 0.7
	if s.RPM != 30 {
		t.Fatalf("Retry-After+reason: RPM=%d, want 30 (Retry-After 优先)", s.RPM)
	}
	if s.Confidence < 0.7 {
		t.Fatalf("Retry-After+reason: confidence=%.2f, want >= 0.7", s.Confidence)
	}
}

// 占位确保 http import 被使用（未来集成测试可能用到）
var _ = http.StatusTooManyRequests
