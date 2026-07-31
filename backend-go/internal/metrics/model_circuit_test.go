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

// TestModelCircuitProbeGating 熔断到期后只放行一个探针，
// 并发请求不应一拥而上把刚恢复的上游重新打死。
func TestModelCircuitProbeGating(t *testing.T) {
	base := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	tracker := NewModelCircuitTracker("messages")

	tracker.recordModelFailureAt(testChannelUID, testKeyHash, testModel, "err", base)
	opened, until := tracker.recordModelFailureAt(
		testChannelUID, testKeyHash, testModel, "err", base.Add(time.Minute))
	if !opened {
		t.Fatal("5 分钟内 2 次失败应触发熔断")
	}

	// 隔离期内一律视为 open（第二次失败在 base+1min，熔断 60s，到 base+2min 才到期）。
	if !tracker.isModelCircuitOpenAt(testChannelUID, testKeyHash, testModel, base.Add(90*time.Second)) {
		t.Fatal("隔离期内应为 open")
	}

	// 到期后首个查询者获得探针资格。
	afterExpiry := until.Add(time.Second)
	if tracker.isModelCircuitOpenAt(testChannelUID, testKeyHash, testModel, afterExpiry) {
		t.Fatal("到期后首个查询者应获得探针资格")
	}
	// 探针在途，其余查询者仍被隔离。
	if !tracker.isModelCircuitOpenAt(testChannelUID, testKeyHash, testModel, afterExpiry) {
		t.Fatal("探针在途时其余请求应继续被隔离")
	}

	// 探针成功 → 完全恢复，条目回收。
	tracker.RecordModelSuccess(testChannelUID, testKeyHash, testModel)
	if tracker.isModelCircuitOpenAt(testChannelUID, testKeyHash, testModel, afterExpiry) {
		t.Fatal("探针成功后应完全恢复")
	}
	if tracker.TrackedCount() != 0 {
		t.Fatalf("探针成功后应回收条目, TrackedCount = %d", tracker.TrackedCount())
	}
}

// TestModelCircuitProbeFailureBacksOff 探针失败应直接翻倍退避，
// 反复抖动的组合要被越来越长地隔离，而非每次从 base 重新开始。
func TestModelCircuitProbeFailureBacksOff(t *testing.T) {
	base := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	tracker := NewModelCircuitTracker("messages")

	tracker.recordModelFailureAt(testChannelUID, testKeyHash, testModel, "err", base)
	_, until := tracker.recordModelFailureAt(
		testChannelUID, testKeyHash, testModel, "err", base.Add(time.Minute))
	if got := until.Sub(base.Add(time.Minute)); got != modelCircuitBackoffBase {
		t.Fatalf("首次熔断时长 = %v, want %v", got, modelCircuitBackoffBase)
	}

	// 取得探针资格后失败 → 退避翻倍。
	afterExpiry := until.Add(time.Second)
	tracker.isModelCircuitOpenAt(testChannelUID, testKeyHash, testModel, afterExpiry)
	_, until2 := tracker.recordModelFailureAt(
		testChannelUID, testKeyHash, testModel, "err", afterExpiry)
	if got := until2.Sub(afterExpiry); got != 2*modelCircuitBackoffBase {
		t.Fatalf("探针失败后熔断时长 = %v, want %v", got, 2*modelCircuitBackoffBase)
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

// TestChannelModelCircuitOpenDoesNotConsumeProbe 渠道级判定不得消耗探针资格，
// 否则调度阶段的多次查询会让真正发请求的 Key 级检查拿不到探针。
func TestChannelModelCircuitOpenDoesNotConsumeProbe(t *testing.T) {
	base := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	tracker := NewModelCircuitTracker("messages")

	tracker.recordModelFailureAt(testChannelUID, testKeyHash, testModel, "err", base)
	_, until := tracker.recordModelFailureAt(
		testChannelUID, testKeyHash, testModel, "err", base.Add(time.Minute))

	afterExpiry := until.Add(time.Second)
	// 到期后渠道级判定应返回 false（该 Key 即将放行），且不消耗探针。
	if tracker.channelModelCircuitOpenAt(testChannelUID, []string{testKeyHash}, testModel, afterExpiry) {
		t.Fatal("到期且无探针在途时渠道级不应视为熔断")
	}
	// 探针资格仍应完好，由 Key 级检查取得。
	if tracker.isModelCircuitOpenAt(testChannelUID, testKeyHash, testModel, afterExpiry) {
		t.Fatal("渠道级判定消耗了探针资格")
	}
}

// TestModelCircuitIgnoresEmptyKeys channelUID 或 model 缺失时不记账，
// 避免把无法归因的失败攒到空键上。
func TestModelCircuitIgnoresEmptyKeys(t *testing.T) {
	tracker := NewModelCircuitTracker("messages")
	now := time.Now()

	tracker.recordModelFailureAt("", testKeyHash, testModel, "err", now)
	tracker.recordModelFailureAt(testChannelUID, testKeyHash, "", "err", now)

	if tracker.TrackedCount() != 0 {
		t.Fatalf("空键不应产生条目, TrackedCount = %d", tracker.TrackedCount())
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
