package quota

import (
	"testing"
)

// ── BucketManager 测试 ──

func TestBucketBasics(t *testing.T) {
	bm := NewBucketManager()
	const now = 1000000

	// 初始状态：未饱和
	if bm.IsSaturated("acc1", "tokens", now) {
		t.Error("new bucket should not be saturated")
	}

	// 低于阈值：不饱和
	bm.RecordUsage("acc1", "tokens", 50.0, 0, now)
	if bm.IsSaturated("acc1", "tokens", now) {
		t.Error("50% used should not be saturated")
	}

	// 达到阈值：饱和
	bm.RecordUsage("acc1", "tokens", 100.0, 0, now)
	if !bm.IsSaturated("acc1", "tokens", now) {
		t.Error("100% used should be saturated")
	}

	// 超过阈值：饱和
	bm.RecordUsage("acc1", "tokens", 120.0, 0, now)
	if !bm.IsSaturated("acc1", "tokens", now) {
		t.Error("120% used should be saturated")
	}

	// 不同维度独立
	if bm.IsSaturated("acc1", "requests", now) {
		t.Error("requests dimension should not be saturated")
	}

	// 不同账号独立
	if bm.IsSaturated("acc2", "tokens", now) {
		t.Error("acc2 should not be saturated")
	}
}

func TestBucketLazyReset(t *testing.T) {
	bm := NewBucketManager()
	const now = 1000000
	const resetAt = now + 5000 // 5 秒后重置

	// 设置带重置时间的饱和桶
	bm.RecordUsage("acc1", "tokens", 100.0, resetAt, now)
	if !bm.IsSaturated("acc1", "tokens", now) {
		t.Error("should be saturated before reset")
	}

	// 重置前一刻：仍饱和
	if !bm.IsSaturated("acc1", "tokens", resetAt-1) {
		t.Error("should be saturated just before reset")
	}

	// 重置时刻：清零（懒重置）
	if bm.IsSaturated("acc1", "tokens", resetAt) {
		t.Error("should NOT be saturated at reset time (lazy reset)")
	}

	// 重置后：保持未饱和
	if bm.IsSaturated("acc1", "tokens", resetAt+1000) {
		t.Error("should NOT be saturated after reset")
	}
}

func TestBucketRecoveryWhenUsageDrops(t *testing.T) {
	bm := NewBucketManager()
	const now = 1000000

	// 先饱和
	bm.RecordUsage("acc1", "tokens", 100.0, 0, now)
	if !bm.IsSaturated("acc1", "tokens", now) {
		t.Fatal("should be saturated")
	}

	// 用量下降到阈值以下 → 清除饱和状态
	bm.RecordUsage("acc1", "tokens", 30.0, 0, now)
	if bm.IsSaturated("acc1", "tokens", now) {
		t.Error("should not be saturated after usage drops")
	}
}

func TestBucketStaleSignal(t *testing.T) {
	bm := NewBucketManager()
	const now = 1000000
	const pastReset = now - 1000 // 重置时间已经过去

	// 收到的信号已经过期（重置时间在过去）
	bm.RecordUsage("acc1", "tokens", 100.0, pastReset, now)
	if bm.IsSaturated("acc1", "tokens", now) {
		t.Error("stale signal (past reset) should not saturate")
	}
}

func TestBucketFailOpen(t *testing.T) {
	bm := NewBucketManager()

	// 空账号：不饱和
	if bm.IsSaturated("", "tokens", 1000) {
		t.Error("empty accountUID should be fail-open (not saturated)")
	}

	// 空窗口：不饱和
	if bm.IsSaturated("acc1", "", 1000) {
		t.Error("empty windowKey should be fail-open (not saturated)")
	}

	// nil manager
	var nilBm *BucketManager
	if nilBm.IsSaturated("acc1", "tokens", 1000) {
		t.Error("nil manager should be fail-open")
	}
}

func TestUpdateFromValues(t *testing.T) {
	bm := NewBucketManager()
	const now = 1000000

	values := []Value{
		{Dimension: DimTokens, Limit: ptrF(1000), Used: ptrF(950), ResetAtMs: now + 300000},
		{Dimension: DimRequests, Limit: ptrF(100), Used: ptrF(50), ResetAtMs: now + 60000},
	}

	bm.UpdateFromValues("acc1", values, now)

	// tokens: 95% → 饱和（阈值 100%）
	// 实际上 95% < 100% → 不饱和
	if bm.IsSaturated("acc1", string(DimTokens), now) {
		t.Error("tokens at 95% should NOT be saturated (threshold is 100%)")
	}

	// requests: 50% → 不饱和
	if bm.IsSaturated("acc1", string(DimRequests), now) {
		t.Error("requests at 50% should not be saturated")
	}

	// 把 tokens 设为 100%
	values[0].Used = ptrF(1000)
	bm.UpdateFromValues("acc1", values, now)
	if !bm.IsSaturated("acc1", string(DimTokens), now) {
		t.Error("tokens at 100% should be saturated")
	}
}

func TestBucketWithRemaining(t *testing.T) {
	bm := NewBucketManager()
	const now = 1000000

	// remaining=0 → used=100% → 饱和
	values := []Value{
		{Dimension: DimTokens, Limit: ptrF(1000), Remaining: ptrF(0)},
	}
	bm.UpdateFromValues("acc1", values, now)
	if !bm.IsSaturated("acc1", string(DimTokens), now) {
		t.Error("remaining=0 should be saturated")
	}

	// remaining=500 → used=50% → 不饱和
	values[0].Remaining = ptrF(500)
	bm.UpdateFromValues("acc1", values, now)
	if bm.IsSaturated("acc1", string(DimTokens), now) {
		t.Error("remaining=500 of 1000 should not be saturated")
	}
}

func TestSaturatedAccounts(t *testing.T) {
	bm := NewBucketManager()
	const now = 1000000

	bm.RecordUsage("acc1", "tokens", 100.0, 0, now)
	bm.RecordUsage("acc2", "tokens", 50.0, 0, now)
	bm.RecordUsage("acc3", "tokens", 100.0, 0, now)
	bm.RecordUsage("acc1", "requests", 100.0, 0, now)

	saturatedTokens := bm.SaturatedAccounts("tokens", now)
	if len(saturatedTokens) != 2 {
		t.Errorf("saturated tokens accounts = %d, want 2", len(saturatedTokens))
	}

	saturatedRequests := bm.SaturatedAccounts("requests", now)
	if len(saturatedRequests) != 1 {
		t.Errorf("saturated requests accounts = %d, want 1", len(saturatedRequests))
	}
}
