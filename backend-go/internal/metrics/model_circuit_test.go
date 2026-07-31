package metrics

import (
	"sync"
	"testing"
	"time"
)

const (
	testChannelUID = "ch_gorouter"
	testKeyHash    = "keyhash01"
	testModel      = "claude-sonnet-5"
)

// TestModelCircuitDualThreshold 覆盖时间感知双阈值：密集失败快速止损，
// 稀疏失败要求更多证据，跨窗口的独立故障不应被错误累积。
func TestModelCircuitDualThreshold(t *testing.T) {
	base := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		// offsets 为各次失败相对 base 的时间偏移
		offsets    []time.Duration
		wantOpened bool
		reason     string
	}{
		{
			name:       "快速通道：5 分钟内 2 次失败",
			offsets:    []time.Duration{0, 3 * time.Minute},
			wantOpened: true,
			reason:     "密集失败是持续故障的明确信号",
		},
		{
			name:       "快速通道边界：正好 5 分钟",
			offsets:    []time.Duration{0, 5 * time.Minute},
			wantOpened: true,
			reason:     "窗口为闭区间",
		},
		{
			name:       "超出快速窗口的 2 次失败不熔断",
			offsets:    []time.Duration{0, 10 * time.Minute},
			wantOpened: false,
			reason:     "慢速通道还差 1 次证据",
		},
		{
			name:       "慢速通道：30 分钟内 3 次失败",
			offsets:    []time.Duration{0, 10 * time.Minute, 20 * time.Minute},
			wantOpened: true,
			reason:     "稀疏但持续的失败累积到 3 次",
		},
		{
			name:       "跨慢速窗口的 3 次失败不熔断",
			offsets:    []time.Duration{0, 40 * time.Minute, 80 * time.Minute},
			wantOpened: false,
			reason:     "过期时间戳被剔除，序列始终不足",
		},
		{
			name:       "单次失败不熔断",
			offsets:    []time.Duration{0},
			wantOpened: false,
			reason:     "证据不足",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tracker := NewModelCircuitTracker("messages")
			var opened bool
			for _, offset := range tc.offsets {
				opened, _ = tracker.recordModelFailureAt(
					testChannelUID, testKeyHash, testModel, "HTTP 403", base.Add(offset))
			}
			if opened != tc.wantOpened {
				t.Fatalf("最后一次失败 opened = %v, want %v (%s)", opened, tc.wantOpened, tc.reason)
			}
		})
	}
}

// TestModelCircuitSuccessResetsFailures 成功必须清空失败序列，
// 否则"失败-成功-失败"会被误判为连续失败。
func TestModelCircuitSuccessResetsFailures(t *testing.T) {
	base := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	tracker := NewModelCircuitTracker("messages")

	if opened, _ := tracker.recordModelFailureAt(testChannelUID, testKeyHash, testModel, "err", base); opened {
		t.Fatal("首次失败不应熔断")
	}
	tracker.RecordModelSuccess(testChannelUID, testKeyHash, testModel)

	// 成功后再失败一次：序列已清空，仍处于"仅 1 次失败"状态。
	opened, _ := tracker.recordModelFailureAt(
		testChannelUID, testKeyHash, testModel, "err", base.Add(time.Minute))
	if opened {
		t.Fatal("成功已清空失败序列，单次失败不应熔断")
	}
}

// TestModelCircuitBackoffCap 退避有上限，不会无限增长。
func TestModelCircuitBackoffCap(t *testing.T) {
	if got := modelCircuitBackoff(100); got != modelCircuitBackoffMax {
		t.Fatalf("modelCircuitBackoff(100) = %v, want %v", got, modelCircuitBackoffMax)
	}
	if got := modelCircuitBackoff(-1); got != modelCircuitBackoffBase {
		t.Fatalf("负数级别应回退到 base, got %v", got)
	}
}

// TestChannelModelCircuitOpen 渠道级升级：只有全部候选 Key 都熔断才排除整个渠道，
// 任一 Key 健康时必须让请求走 Key 级过滤去命中它。
func TestChannelModelCircuitOpen(t *testing.T) {
	base := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	tracker := NewModelCircuitTracker("messages")
	keyA, keyB := "hashA", "hashB"

	openKey := func(keyHash string) {
		tracker.recordModelFailureAt(testChannelUID, keyHash, testModel, "err", base)
		tracker.recordModelFailureAt(testChannelUID, keyHash, testModel, "err", base.Add(time.Minute))
	}

	// 取隔离期内的时刻：两次失败发生在 base 与 base+1min，熔断 60s 后到 base+2min 到期。
	now := base.Add(90 * time.Second)
	openKey(keyA)
	if tracker.channelModelCircuitOpenAt(testChannelUID, []string{keyA, keyB}, testModel, now) {
		t.Fatal("keyB 仍健康时不应升级为渠道级排除")
	}

	openKey(keyB)
	if !tracker.channelModelCircuitOpenAt(testChannelUID, []string{keyA, keyB}, testModel, now) {
		t.Fatal("全部 Key 熔断时应升级为渠道级排除")
	}

	// 其他模型不受影响——这是本机制的核心保证。
	if tracker.channelModelCircuitOpenAt(testChannelUID, []string{keyA, keyB}, "claude-opus-5", now) {
		t.Fatal("其他模型不应被连累")
	}
	// 空 keyHashes 时 fail-open。
	if tracker.channelModelCircuitOpenAt(testChannelUID, nil, testModel, now) {
		t.Fatal("无 keyHash 可判断时应 fail-open")
	}
}

// TestModelCircuitIgnoresEmptyKeys 空 channelUID 不记账，空 model 计入全局桶。
func TestModelCircuitIgnoresEmptyKeys(t *testing.T) {
	tracker := NewModelCircuitTracker("messages")
	now := time.Now()

	// 空 channelUID 仍拒绝：无法归因的失败不记账。
	tracker.recordModelFailureAt("", testKeyHash, testModel, "err", now)

	// 空 model = 全局桶，是合法条目。
	tracker.recordModelFailureAt(testChannelUID, testKeyHash, "", "err", now)
	if tracker.TrackedCount() != 1 {
		// 全局桶条目 + 空 channelUID 被拒绝 = 只有 1 条
		t.Fatalf("空 model（全局桶）应产生条目, TrackedCount = %d", tracker.TrackedCount())
	}

	if tracker.isModelCircuitOpenAt("", testKeyHash, testModel, now) {
		t.Fatal("空 channelUID 应 fail-open")
	}
}

// TestModelCircuitCleanup 长期无活动的条目应被回收。
func TestModelCircuitCleanup(t *testing.T) {
	base := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	tracker := NewModelCircuitTracker("messages")

	tracker.recordModelFailureAt(testChannelUID, testKeyHash, testModel, "err", base)
	if tracker.TrackedCount() != 1 {
		t.Fatalf("应有 1 个条目, got %d", tracker.TrackedCount())
	}

	tracker.cleanupAt(base.Add(modelCircuitStaleAfter / 2))
	if tracker.TrackedCount() != 1 {
		t.Fatal("未过期条目不应被回收")
	}
	tracker.cleanupAt(base.Add(modelCircuitStaleAfter + time.Minute))
	if tracker.TrackedCount() != 0 {
		t.Fatal("过期条目应被回收")
	}
}

// TestModelCircuitConcurrent 并发读写不应触发竞态（配合 -race）。
func TestModelCircuitConcurrent(t *testing.T) {
	tracker := NewModelCircuitTracker("messages")
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			tracker.RecordModelFailure(testChannelUID, testKeyHash, testModel, "err")
		}()
		go func() {
			defer wg.Done()
			tracker.IsModelCircuitOpen(testChannelUID, testKeyHash, testModel)
		}()
		go func() {
			defer wg.Done()
			tracker.ChannelModelCircuitOpen(testChannelUID, []string{testKeyHash}, testModel)
		}()
	}
	wg.Wait()
}

// TestTruncateModelCircuitError 中文错误信息不应被截成半个字符。
func TestTruncateModelCircuitError(t *testing.T) {
	long := ""
	for i := 0; i < 300; i++ {
		long += "渠"
	}
	got := truncateModelCircuitError(long)
	if !utf8ValidString(got) {
		t.Fatal("截断产生了非法 UTF-8 序列")
	}
	if short := truncateModelCircuitError("  err  "); short != "err" {
		t.Fatalf("应 trim 空白, got %q", short)
	}
}

func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

// TestModelCircuitIsolationNotClearedByLateSuccess 隔离期内的成功不得提前解除隔离。
//
// 回归防护：熔断前就已发出、此刻才返回的请求（长流式尤其常见）会调 RecordModelSuccess，
// 若据此清零 openUntil，剩余隔离时间被腰斩，流量立刻涌回未验证的组合。
// 这类成功只证明"那一次调用没问题"，不证明"故障已恢复"。
func TestModelCircuitIsolationNotClearedByLateSuccess(t *testing.T) {
	base := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	tracker := NewModelCircuitTracker("messages")

	tracker.recordModelFailureAt(testChannelUID, testKeyHash, testModel, "err", base)
	_, until := tracker.recordModelFailureAt(
		testChannelUID, testKeyHash, testModel, "err", base.Add(time.Minute))

	// 隔离期内（尚未到期）一个迟到的旧请求成功返回。
	mid := base.Add(90 * time.Second)
	if !mid.Before(until) {
		t.Fatalf("测试前提错误: mid=%v 应早于 until=%v", mid, until)
	}
	tracker.recordModelSuccessAt(testChannelUID, testKeyHash, testModel, mid)

	if !tracker.isModelCircuitOpenAt(testChannelUID, testKeyHash, testModel, mid) {
		t.Fatalf("隔离期内的成功不应解除隔离，剩余 %v 被跳过", until.Sub(mid))
	}
	// 到期后正常放行。
	if tracker.isModelCircuitOpenAt(testChannelUID, testKeyHash, testModel, until.Add(time.Second)) {
		t.Fatal("到期后应放行")
	}
}

// TestModelCircuitRecoveryThenFailureBacksOff 到期放行后若立即再失败，
// 应按递增退避直接重新熔断，不必再等第二次失败累积证据。
func TestModelCircuitRecoveryThenFailureBacksOff(t *testing.T) {
	base := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	tracker := NewModelCircuitTracker("messages")

	tracker.recordModelFailureAt(testChannelUID, testKeyHash, testModel, "err", base)
	_, until := tracker.recordModelFailureAt(
		testChannelUID, testKeyHash, testModel, "err", base.Add(time.Minute))
	if got := until.Sub(base.Add(time.Minute)); got != modelCircuitBackoffBase {
		t.Fatalf("首次熔断时长 = %v, want %v", got, modelCircuitBackoffBase)
	}

	// 到期后第一个请求就失败 → 单次失败即重新熔断，退避翻倍。
	afterExpiry := until.Add(time.Second)
	opened, until2 := tracker.recordModelFailureAt(
		testChannelUID, testKeyHash, testModel, "err", afterExpiry)
	if !opened {
		t.Fatal("恢复后立即失败应重新熔断，不必再累积第二次证据")
	}
	if got := until2.Sub(afterExpiry); got != 2*modelCircuitBackoffBase {
		t.Fatalf("重新熔断时长 = %v, want %v（退避应翻倍）", got, 2*modelCircuitBackoffBase)
	}
}

// TestModelCircuitSuccessAfterExpiryRecovers 到期后成功应完全恢复并回收条目，
// 退避级别随之清除。
func TestModelCircuitSuccessAfterExpiryRecovers(t *testing.T) {
	base := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	tracker := NewModelCircuitTracker("messages")

	tracker.recordModelFailureAt(testChannelUID, testKeyHash, testModel, "err", base)
	_, until := tracker.recordModelFailureAt(
		testChannelUID, testKeyHash, testModel, "err", base.Add(time.Minute))

	afterExpiry := until.Add(time.Second)
	tracker.recordModelSuccessAt(testChannelUID, testKeyHash, testModel, afterExpiry)

	if tracker.TrackedCount() != 0 {
		t.Fatalf("恢复后应回收条目, TrackedCount = %d", tracker.TrackedCount())
	}
	// 退避已清除：下次触发从 base 重新起步。
	tracker.recordModelFailureAt(testChannelUID, testKeyHash, testModel, "err", afterExpiry.Add(time.Minute))
	_, until3 := tracker.recordModelFailureAt(
		testChannelUID, testKeyHash, testModel, "err", afterExpiry.Add(2*time.Minute))
	if got := until3.Sub(afterExpiry.Add(2 * time.Minute)); got != modelCircuitBackoffBase {
		t.Fatalf("确认恢复后退避应重置为 base, got %v", got)
	}
}

// TestBreakerScopeIsAvailableChecksGlobalAndExact 验证调度放行唯一入口：
// global 桶或 exact 桶任一 open 即不可用，两者都 closed 才可用。
func TestBreakerScopeIsAvailableChecksGlobalAndExact(t *testing.T) {
	base := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	tracker := NewModelCircuitTracker("messages")

	// 初始状态：全部可用。
	if !tracker.isAvailableAt("ch", "kh", "sonnet", base) {
		t.Fatal("无任何故障时 IsAvailable 应返回 true")
	}

	// global bucket open → 整把 Key 不可用。
	tracker.recordModelFailureAt("ch", "kh", "", "auth_error", base)
	tracker.recordModelFailureAt("ch", "kh", "", "auth_error", base.Add(time.Minute))
	// 第二次失败在 base+1min，熔断 60s 后到 base+2min 到期，base+90s 仍在隔离期。
	now := base.Add(90 * time.Second)
	if tracker.isAvailableAt("ch", "kh", "sonnet", now) {
		t.Fatal("global bucket open 时即使 exact 无记录也应不可用")
	}
	if tracker.isAvailableAt("ch", "kh", "opus", now) {
		t.Fatal("global bucket open 时所有模型都应不可用")
	}

	// 不同 channel/key 不受影响。
	if !tracker.isAvailableAt("ch2", "kh2", "sonnet", now) {
		t.Fatal("不同 channel/key 不应被连累")
	}
}

// TestBreakerScopeExactBucketOnlyAffectsTargetModel 验证 exact 桶只隔离指定模型，
// 同 Key 的其他模型仍可用。
func TestBreakerScopeExactBucketOnlyAffectsTargetModel(t *testing.T) {
	base := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	tracker := NewModelCircuitTracker("messages")

	tracker.recordModelFailureAt("ch", "kh", "sonnet", "403", base)
	tracker.recordModelFailureAt("ch", "kh", "sonnet", "403", base.Add(time.Minute))

	now := base.Add(90 * time.Second)
	if tracker.isAvailableAt("ch", "kh", "sonnet", now) {
		t.Fatal("sonnet exact bucket open 时应不可用")
	}
	if !tracker.isAvailableAt("ch", "kh", "opus", now) {
		t.Fatal("opus exact bucket 无记录时应可用（核心不变量：模型隔离）")
	}
}

// TestBreakerScopeDeleteByChannelUID 验证渠道删除时清理所有关联条目。
func TestBreakerScopeDeleteByChannelUID(t *testing.T) {
	base := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	tracker := NewModelCircuitTracker("messages")

	tracker.recordModelFailureAt("ch_a", "kh", "sonnet", "e", base)
	tracker.recordModelFailureAt("ch_a", "kh", "sonnet", "e", base.Add(time.Minute))
	tracker.recordModelFailureAt("ch_a", "kh", "", "auth", base)
	tracker.recordModelFailureAt("ch_a", "kh", "", "auth", base.Add(time.Minute))
	tracker.recordModelFailureAt("ch_b", "kh", "sonnet", "e", base)
	tracker.recordModelFailureAt("ch_b", "kh", "sonnet", "e", base.Add(time.Minute))

	tracker.DeleteByChannelUID("ch_a")
	if tracker.TrackedCount() != 1 {
		t.Fatalf("删除 ch_a 后应只剩 ch_b 的条目, TrackedCount = %d", tracker.TrackedCount())
	}
	if !tracker.isModelCircuitOpenAt("ch_b", "kh", "sonnet", base.Add(90*time.Second)) {
		t.Fatal("ch_b 的条目不应被删除")
	}
}

// TestBreakerScopeDeleteByChannelUIDAndKeyHash 验证单 Key 删除。
func TestBreakerScopeDeleteByChannelUIDAndKeyHash(t *testing.T) {
	base := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	tracker := NewModelCircuitTracker("messages")

	tracker.recordModelFailureAt("ch", "ka", "sonnet", "e", base)
	tracker.recordModelFailureAt("ch", "ka", "sonnet", "e", base.Add(time.Minute))
	tracker.recordModelFailureAt("ch", "kb", "sonnet", "e", base)
	tracker.recordModelFailureAt("ch", "kb", "sonnet", "e", base.Add(time.Minute))

	tracker.DeleteByChannelUIDAndKeyHash("ch", "ka")
	if tracker.TrackedCount() != 1 {
		t.Fatalf("删除 ka 后应只剩 kb 的条目, TrackedCount = %d", tracker.TrackedCount())
	}
	if !tracker.isModelCircuitOpenAt("ch", "kb", "sonnet", base.Add(90*time.Second)) {
		t.Fatal("kb 的条目不应被删除")
	}
}
