package metrics

import (
	"math"
	"testing"
	"time"
)

func TestKeyAutoWeight_InsufficientSamplesReturnsOne(t *testing.T) {
	tracker := NewKeyAutoWeightTracker()
	now := time.Now()

	for i := 0; i < minWeightSamples-1; i++ {
		tracker.RecordSuccess("ch", "kh", now)
	}
	if got := tracker.WeightFactor("ch", "kh", now); got != 1.0 {
		t.Fatalf("样本不足时应返回 1.0，得到 %v", got)
	}

	// 达到样本下限后开始参与
	tracker.RecordFailure("ch", "kh", now)
	got := tracker.WeightFactor("ch", "kh", now)
	if got >= 1.0 || got <= 0 {
		t.Fatalf("有失败样本后系数应在 (0,1)，得到 %v", got)
	}
}

func TestKeyAutoWeight_FailureDecayAndConsecutiveHalving(t *testing.T) {
	tracker := NewKeyAutoWeightTracker()
	now := time.Now()

	// 10 成功 + 2 失败，连续失败 2 次：factor = (11/14) × 0.25
	for i := 0; i < 10; i++ {
		tracker.RecordSuccess("ch", "kh", now)
	}
	tracker.RecordFailure("ch", "kh", now)
	tracker.RecordFailure("ch", "kh", now)

	want := (11.0 / 14.0) * math.Pow(0.5, 2)
	got := tracker.WeightFactor("ch", "kh", now)
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("系数 = %v，期望 %v", got, want)
	}

	// 成功清零连续失败：滑窗计数为 11 成功 + 2 失败 → (11+1)/(13+2) = 0.8
	tracker.RecordSuccess("ch", "kh", now)
	want = 12.0 / 15.0
	got = tracker.WeightFactor("ch", "kh", now)
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("成功后系数 = %v，期望 %v", got, want)
	}
}

func TestKeyAutoWeight_FloorClamp(t *testing.T) {
	tracker := NewKeyAutoWeightTracker()
	now := time.Now()

	// 大量失败：连续失败足够多时命中 0.05 下限
	for i := 0; i < 30; i++ {
		tracker.RecordFailure("ch", "kh", now)
	}
	if got := tracker.WeightFactor("ch", "kh", now); got != minWeightFactor {
		t.Fatalf("连续失败后应触底 %v，得到 %v", minWeightFactor, got)
	}
}

func TestKeyAutoWeight_WindowRollover(t *testing.T) {
	tracker := NewKeyAutoWeightTracker()
	base := time.Now()

	// 窗口外（6 分钟前）的 20 次失败不应计入当前窗口
	stale := base.Add(-6 * time.Minute)
	for i := 0; i < 20; i++ {
		tracker.RecordFailure("ch", "kh", stale)
	}
	if got := tracker.WeightFactor("ch", "kh", base); got != 1.0 {
		t.Fatalf("窗口滚动后旧失败不应影响系数，得到 %v", got)
	}
}

func TestKeyAutoWeight_UnknownKeyReturnsOne(t *testing.T) {
	tracker := NewKeyAutoWeightTracker()
	if got := tracker.WeightFactor("ch", "none", time.Now()); got != 1.0 {
		t.Fatalf("未跟踪 Key 应返回 1.0，得到 %v", got)
	}
}

func TestKeyAutoWeight_CleanupIdleEntries(t *testing.T) {
	tracker := NewKeyAutoWeightTracker()
	now := time.Now()

	tracker.RecordSuccess("ch", "kh", now)
	if tracker.TrackedCount() != 1 {
		t.Fatalf("TrackedCount() = %d，期望 1", tracker.TrackedCount())
	}
	if removed := tracker.Cleanup(now.Add(autoWeightIdleTTL + time.Minute)); removed != 1 {
		t.Fatalf("闲置条目应被清理，removed = %d", removed)
	}
	if tracker.TrackedCount() != 0 {
		t.Fatal("清理后不应残留条目")
	}
}

func TestKeyAutoWeight_ChannelIsolation(t *testing.T) {
	tracker := NewKeyAutoWeightTracker()
	now := time.Now()

	for i := 0; i < 20; i++ {
		tracker.RecordFailure("ch-a", "kh", now)
	}
	if got := tracker.WeightFactor("ch-b", "kh", now); got != 1.0 {
		t.Fatalf("不同渠道的同 hash Key 不应互相影响，得到 %v", got)
	}
}
