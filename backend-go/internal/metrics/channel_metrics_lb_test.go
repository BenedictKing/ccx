package metrics

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestRecordFirstToken_BasicAndConcurrency 表驱动 + 并发安全验证
func TestRecordFirstToken_BasicAndConcurrency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		channel   string
		latencies []int64
		wantAvg   int64
	}{
		{
			name:      "single sample",
			channel:   "ch-ft-single",
			latencies: []int64{100},
			wantAvg:   100,
		},
		{
			name:      "multi sample average",
			channel:   "ch-ft-multi",
			latencies: []int64{100, 200, 300},
			wantAvg:   200,
		},
		{
			name:      "ignore non positive",
			channel:   "ch-ft-skip",
			latencies: []int64{0, -10, 50, 150},
			wantAvg:   100,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := NewMetricsManager()
			defer m.Stop()
			for _, l := range tc.latencies {
				m.RecordFirstToken(tc.channel, l)
			}
			got := m.GetFirstTokenLatencyAvg(tc.channel)
			if got != tc.wantAvg {
				t.Fatalf("avg latency = %d, want %d", got, tc.wantAvg)
			}
		})
	}

	t.Run("concurrent writers same channel", func(t *testing.T) {
		t.Parallel()
		m := NewMetricsManager()
		defer m.Stop()
		const goroutines = 20
		const perG = 50
		var wg sync.WaitGroup
		wg.Add(goroutines)
		for g := 0; g < goroutines; g++ {
			go func() {
				defer wg.Done()
				for i := 0; i < perG; i++ {
					m.RecordFirstToken("ch-ft-conc", 100)
				}
			}()
		}
		wg.Wait()
		// 所有样本均为 100，平均值必为 100；并发安全则不会 panic
		if got := m.GetFirstTokenLatencyAvg("ch-ft-conc"); got != 100 {
			t.Fatalf("concurrent avg = %d, want 100", got)
		}
	})
}

// TestRecordTPS_Calculation 验证按时间加权平均
func TestRecordTPS_Calculation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		channel string
		samples []struct{ tokens, durMs int64 }
		wantTPS float64
	}{
		{
			name:    "single sample 1000 tokens / 1000ms",
			channel: "ch-tps-1",
			samples: []struct{ tokens, durMs int64 }{{1000, 1000}},
			wantTPS: 1000.0,
		},
		{
			name:    "weighted average prefers long requests",
			channel: "ch-tps-2",
			samples: []struct{ tokens, durMs int64 }{
				{100, 100},   // 1000 tps for 100ms
				{1000, 9900}, // ~101 tps for 9900ms
			},
			// (100+1000) / (100+9900) * 1000 = 110.0
			wantTPS: 110.0,
		},
		{
			name:    "ignore non positive samples",
			channel: "ch-tps-3",
			samples: []struct{ tokens, durMs int64 }{
				{0, 100},
				{100, 0},
				{500, 1000},
			},
			wantTPS: 500.0,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := NewMetricsManager()
			defer m.Stop()
			for _, s := range tc.samples {
				m.RecordTPS(tc.channel, s.tokens, s.durMs)
			}
			got := m.GetTPSAvg(tc.channel)
			if got != tc.wantTPS {
				t.Fatalf("tps avg = %v, want %v", got, tc.wantTPS)
			}
		})
	}
}

// TestRecordActiveConnDelta_AddSubAndConcurrent 验证 +1/-1 累加 + 归零 + 并发
func TestRecordActiveConnDelta_AddSubAndConcurrent(t *testing.T) {
	t.Parallel()

	t.Run("add and subtract back to zero", func(t *testing.T) {
		t.Parallel()
		m := NewMetricsManager()
		defer m.Stop()
		ch := "ch-act-1"
		m.RecordActiveConnDelta(ch, 1)
		m.RecordActiveConnDelta(ch, 1)
		m.RecordActiveConnDelta(ch, 1)
		if got := m.GetActiveConnections(ch); got != 3 {
			t.Fatalf("after +3 got %d, want 3", got)
		}
		m.RecordActiveConnDelta(ch, -1)
		m.RecordActiveConnDelta(ch, -1)
		m.RecordActiveConnDelta(ch, -1)
		if got := m.GetActiveConnections(ch); got != 0 {
			t.Fatalf("after balanced got %d, want 0", got)
		}
	})

	t.Run("unknown channel returns zero", func(t *testing.T) {
		t.Parallel()
		m := NewMetricsManager()
		defer m.Stop()
		if got := m.GetActiveConnections("ch-never-recorded"); got != 0 {
			t.Fatalf("unknown channel got %d, want 0", got)
		}
	})

	t.Run("zero delta no entry created", func(t *testing.T) {
		t.Parallel()
		m := NewMetricsManager()
		defer m.Stop()
		m.RecordActiveConnDelta("ch-zero", 0)
		// zero delta 不应当创建 entry，读取仍为 0
		if got := m.GetActiveConnections("ch-zero"); got != 0 {
			t.Fatalf("zero delta got %d, want 0", got)
		}
	})

	t.Run("concurrent +1 / -1 balanced", func(t *testing.T) {
		t.Parallel()
		m := NewMetricsManager()
		defer m.Stop()
		ch := "ch-act-conc"
		const goroutines = 32
		const perG = 100
		var wg sync.WaitGroup
		wg.Add(goroutines * 2)
		for g := 0; g < goroutines; g++ {
			go func() {
				defer wg.Done()
				for i := 0; i < perG; i++ {
					m.RecordActiveConnDelta(ch, 1)
				}
			}()
			go func() {
				defer wg.Done()
				for i := 0; i < perG; i++ {
					m.RecordActiveConnDelta(ch, -1)
				}
			}()
		}
		wg.Wait()
		if got := m.GetActiveConnections(ch); got != 0 {
			t.Fatalf("concurrent balanced got %d, want 0", got)
		}
	})
}

// TestLBSlidingWindow_BoundaryPrune 验证窗口外样本被裁剪
func TestLBSlidingWindow_BoundaryPrune(t *testing.T) {
	t.Parallel()

	t.Run("FTTL prunes stale records", func(t *testing.T) {
		t.Parallel()
		m := NewMetricsManager()
		defer m.Stop()
		ch := "ch-ft-window"
		entry := m.getOrCreateLBEntry(ch)

		// 直接注入一条窗口外样本（测试白盒，模拟 6min 前）
		stale := time.Now().Add(-(lbWindowDuration + time.Minute))
		entry.mu.Lock()
		entry.ftRecords = append(entry.ftRecords, ftLatencyRecord{ts: stale, latencyMs: 9999})
		entry.mu.Unlock()

		// 注入一条新样本
		m.RecordFirstToken(ch, 100)

		got := m.GetFirstTokenLatencyAvg(ch)
		if got != 100 {
			t.Fatalf("FTTL after prune = %d, want 100 (stale 9999 should be dropped)", got)
		}

		// 验证 stale 样本已被裁剪
		entry.mu.Lock()
		n := len(entry.ftRecords)
		entry.mu.Unlock()
		if n != 1 {
			t.Fatalf("ftRecords len after prune = %d, want 1", n)
		}
	})

	t.Run("TPS prunes stale records", func(t *testing.T) {
		t.Parallel()
		m := NewMetricsManager()
		defer m.Stop()
		ch := "ch-tps-window"
		entry := m.getOrCreateLBEntry(ch)

		stale := time.Now().Add(-(lbWindowDuration + time.Minute))
		entry.mu.Lock()
		entry.tpsRecords = append(entry.tpsRecords, tpsRecord{ts: stale, totalTokens: 1_000_000, durationMs: 1})
		entry.mu.Unlock()

		m.RecordTPS(ch, 500, 1000)

		got := m.GetTPSAvg(ch)
		if got != 500.0 {
			t.Fatalf("TPS after prune = %v, want 500.0", got)
		}
	})

	t.Run("empty channel returns zero", func(t *testing.T) {
		t.Parallel()
		m := NewMetricsManager()
		defer m.Stop()
		if got := m.GetFirstTokenLatencyAvg("ch-empty"); got != 0 {
			t.Fatalf("empty FTTL got %d, want 0", got)
		}
		if got := m.GetTPSAvg("ch-empty"); got != 0 {
			t.Fatalf("empty TPS got %v, want 0", got)
		}
	})
}

// TestLBMetrics_PerChannelIsolation 验证不同 channelKey 间互不干扰
func TestLBMetrics_PerChannelIsolation(t *testing.T) {
	t.Parallel()

	m := NewMetricsManager()
	defer m.Stop()

	m.RecordFirstToken("ch-a", 100)
	m.RecordFirstToken("ch-b", 500)
	m.RecordTPS("ch-a", 1000, 1000)
	m.RecordTPS("ch-b", 2000, 1000)
	m.RecordActiveConnDelta("ch-a", 5)
	m.RecordActiveConnDelta("ch-b", 7)

	if got := m.GetFirstTokenLatencyAvg("ch-a"); got != 100 {
		t.Fatalf("ch-a FTTL = %d, want 100", got)
	}
	if got := m.GetFirstTokenLatencyAvg("ch-b"); got != 500 {
		t.Fatalf("ch-b FTTL = %d, want 500", got)
	}
	if got := m.GetTPSAvg("ch-a"); got != 1000.0 {
		t.Fatalf("ch-a TPS = %v, want 1000", got)
	}
	if got := m.GetTPSAvg("ch-b"); got != 2000.0 {
		t.Fatalf("ch-b TPS = %v, want 2000", got)
	}
	if got := m.GetActiveConnections("ch-a"); got != 5 {
		t.Fatalf("ch-a active = %d, want 5", got)
	}
	if got := m.GetActiveConnections("ch-b"); got != 7 {
		t.Fatalf("ch-b active = %d, want 7", got)
	}
}

// TestLBMetrics_AtomicReadAfterConcurrentWrites 用 atomic.LoadInt64 验证并发后状态一致
func TestLBMetrics_AtomicReadAfterConcurrentWrites(t *testing.T) {
	t.Parallel()
	m := NewMetricsManager()
	defer m.Stop()
	ch := "ch-atomic"

	const writers = 16
	const perW = 250
	var wg sync.WaitGroup
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perW; j++ {
				m.RecordActiveConnDelta(ch, 1)
			}
		}()
	}
	wg.Wait()

	entry := m.getOrCreateLBEntry(ch)
	if got := atomic.LoadInt64(&entry.activeConn); got != int64(writers*perW) {
		t.Fatalf("atomic load activeConn = %d, want %d", got, writers*perW)
	}
}
