package autopilot

import (
	"testing"
	"time"
)

// mockBucketReader 实现 BucketReader 接口，用于测试。
type mockBucketReader struct {
	buckets map[string][]*TimeBucketMetrics
}

func newMockBucketReader() *mockBucketReader {
	return &mockBucketReader{buckets: make(map[string][]*TimeBucketMetrics)}
}

func (m *mockBucketReader) GetBuckets(endpointUID string, n int) []*TimeBucketMetrics {
	bkts, ok := m.buckets[endpointUID]
	if !ok {
		return nil
	}
	if n > len(bkts) {
		n = len(bkts)
	}
	// 返回最近 n 个
	return bkts[len(bkts)-n:]
}

func (m *mockBucketReader) addBuckets(endpointUID string, buckets []*TimeBucketMetrics) {
	m.buckets[endpointUID] = append(m.buckets[endpointUID], buckets...)
}

// ── RecordRequest 测试 ──

func TestUsageMeter_RecordRequest(t *testing.T) {
	m := NewUsageMeter(UsageMeterConfig{QuietLogs: true}, nil)

	m.RecordRequest("ep-001", 100, RequestRecordMeta{})
	m.RecordRequest("ep-001", 200, RequestRecordMeta{})
	m.RecordRequest("ep-002", 50, RequestRecordMeta{})

	if m.EndpointCount() != 2 {
		t.Fatalf("EndpointCount = %d, 期望 2", m.EndpointCount())
	}

	windows := m.ComputeWindows("ep-001")
	if len(windows) != 4 {
		t.Fatalf("ComputeWindows 返回 %d 个窗口, 期望 4", len(windows))
	}
	for _, w := range windows {
		if w.Used != 2 {
			t.Errorf("窗口 %s Used = %f, 期望 2", w.Window, w.Used)
		}
		if w.Source != "local_metering" {
			t.Errorf("窗口 %s Source = %s, 期望 local_metering", w.Window, w.Source)
		}
		if w.Limit != 0 {
			t.Errorf("窗口 %s Limit = %f, 期望 0 (未知)", w.Window, w.Limit)
		}
	}
}

func TestUsageMeter_RecordRequestEmptyUID(t *testing.T) {
	m := NewUsageMeter(UsageMeterConfig{QuietLogs: true}, nil)
	m.RecordRequest("", 0, RequestRecordMeta{}) // 空 UID 应被忽略
	if m.EndpointCount() != 0 {
		t.Errorf("空 UID 后 EndpointCount = %d, 期望 0", m.EndpointCount())
	}
}

// ── ComputeWindows 测试 ──

func TestUsageMeter_ComputeWindowsNoData(t *testing.T) {
	m := NewUsageMeter(UsageMeterConfig{QuietLogs: true}, nil)
	windows := m.ComputeWindows("nonexistent")
	if len(windows) != 4 {
		t.Fatalf("无数据时 ComputeWindows 返回 %d 个窗口, 期望 4", len(windows))
	}
	for _, w := range windows {
		if w.Used != 0 {
			t.Errorf("无数据时窗口 %s Used = %f, 期望 0", w.Window, w.Used)
		}
	}
}

func TestUsageMeter_ComputeWindowsWithBuckets(t *testing.T) {
	fixedNow := time.Date(2025, 7, 8, 14, 30, 0, 0, time.UTC)
	reader := newMockBucketReader()

	// 构造桶数据：5 小时内有 10 个桶，每个桶 5 次请求
	buckets := make([]*TimeBucketMetrics, 0, 10)
	for i := 0; i < 10; i++ {
		bucketStart := fixedNow.Add(-time.Duration(10-i) * bucketSize)
		buckets = append(buckets, &TimeBucketMetrics{
			BucketStart:  bucketStart,
			BucketSize:   bucketSize,
			RequestCount: 5,
			SuccessCount: 5,
		})
	}
	reader.addBuckets("ep-001", buckets)

	cfg := UsageMeterConfig{
		Windows:   []string{"5h", "day"},
		QuietLogs: true,
	}
	m := NewUsageMeter(cfg, reader)
	m.nowFunc = func() time.Time { return fixedNow }

	windows := m.ComputeWindows("ep-001")
	if len(windows) != 2 {
		t.Fatalf("ComputeWindows 返回 %d 个窗口, 期望 2", len(windows))
	}

	// 5h 窗口：10 个桶 * 5 次 = 50
	found5h := false
	for _, w := range windows {
		if w.Window == "5h" {
			found5h = true
			if w.Used != 50 {
				t.Errorf("5h 窗口 Used = %f, 期望 50", w.Used)
			}
		}
	}
	if !found5h {
		t.Error("未找到 5h 窗口")
	}
}

// ── 窗口滚动与清零测试 ──

func TestUsageMeter_WindowRolling(t *testing.T) {
	fixedNow := time.Date(2025, 7, 8, 14, 30, 0, 0, time.UTC)
	reader := newMockBucketReader()

	// 构造 10 小时前的桶（超出 5h 窗口）和当前的桶
	oldBucket := &TimeBucketMetrics{
		BucketStart:  fixedNow.Add(-10 * time.Hour),
		BucketSize:   bucketSize,
		RequestCount: 999,
	}
	newBucket := &TimeBucketMetrics{
		BucketStart:  fixedNow.Add(-bucketSize),
		BucketSize:   bucketSize,
		RequestCount: 7,
	}
	reader.addBuckets("ep-old", []*TimeBucketMetrics{oldBucket, newBucket})

	cfg := UsageMeterConfig{
		Windows:   []string{"5h"},
		QuietLogs: true,
	}
	m := NewUsageMeter(cfg, reader)
	m.nowFunc = func() time.Time { return fixedNow }

	windows := m.ComputeWindows("ep-old")
	if len(windows) != 1 {
		t.Fatalf("期望 1 个窗口, 得到 %d", len(windows))
	}
	// 5h 窗口：只有 newBucket 在窗口内
	if windows[0].Used != 7 {
		t.Errorf("5h 窗口 Used = %f, 期望 7（旧桶应在窗口外）", windows[0].Used)
	}
}

func TestUsageMeter_WindowResetTimes(t *testing.T) {
	m := NewUsageMeter(UsageMeterConfig{QuietLogs: true}, nil)
	fixedNow := time.Date(2025, 7, 8, 14, 30, 0, 0, time.UTC) // 周二
	m.nowFunc = func() time.Time { return fixedNow }

	windows := m.ComputeWindows("ep-test")
	for _, w := range windows {
		switch w.Window {
		case "5h":
			// 5h 滚动：resetsAt = now + 5h
			expected := fixedNow.Add(5 * time.Hour)
			if !w.ResetsAt.Equal(expected) {
				t.Errorf("5h ResetsAt = %v, 期望 %v", w.ResetsAt, expected)
			}
		case "day":
			// 日窗口：resetsAt = 次日 00:00 UTC
			expected := time.Date(2025, 7, 9, 0, 0, 0, 0, time.UTC)
			if !w.ResetsAt.Equal(expected) {
				t.Errorf("day ResetsAt = %v, 期望 %v", w.ResetsAt, expected)
			}
		case "week":
			// 周二 -> 下周一 = 6 天后
			expected := time.Date(2025, 7, 14, 0, 0, 0, 0, time.UTC)
			if !w.ResetsAt.Equal(expected) {
				t.Errorf("week ResetsAt = %v, 期望 %v", w.ResetsAt, expected)
			}
		case "month":
			// 7月8日 -> 8月1日
			expected := time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)
			if !w.ResetsAt.Equal(expected) {
				t.Errorf("month ResetsAt = %v, 期望 %v", w.ResetsAt, expected)
			}
		}
	}
}

// ── Clear 测试 ──

func TestUsageMeter_Clear(t *testing.T) {
	m := NewUsageMeter(UsageMeterConfig{QuietLogs: true}, nil)
	m.RecordRequest("ep-001", 10, RequestRecordMeta{})
	if m.EndpointCount() != 1 {
		t.Fatalf("Clear 前 EndpointCount = %d", m.EndpointCount())
	}
	m.Clear("ep-001")
	if m.EndpointCount() != 0 {
		t.Errorf("Clear 后 EndpointCount = %d, 期望 0", m.EndpointCount())
	}
	// Clear 后 ComputeWindows 应返回零值
	windows := m.ComputeWindows("ep-001")
	for _, w := range windows {
		if w.Used != 0 {
			t.Errorf("Clear 后窗口 %s Used = %f, 期望 0", w.Window, w.Used)
		}
	}
}

// ── windowToDuration 测试 ──

func TestWindowToDuration(t *testing.T) {
	cases := []struct {
		window   string
		expected time.Duration
	}{
		{"5h", 5 * time.Hour},
		{"day", 24 * time.Hour},
		{"week", 7 * 24 * time.Hour},
		{"month", 30 * 24 * time.Hour},
		{"unknown", 0},
	}
	for _, tc := range cases {
		t.Run(tc.window, func(t *testing.T) {
			got := windowToDuration(tc.window)
			if got != tc.expected {
				t.Errorf("windowToDuration(%q) = %v, 期望 %v", tc.window, got, tc.expected)
			}
		})
	}
}

// ── computeWindowResetAt 测试 ──

func TestComputeWindowResetAt(t *testing.T) {
	// 周二 14:30 UTC
	now := time.Date(2025, 7, 8, 14, 30, 0, 0, time.UTC)

	cases := []struct {
		name     string
		window   string
		expected time.Time
	}{
		{
			name:     "5h滚动窗口",
			window:   "5h",
			expected: time.Date(2025, 7, 8, 19, 30, 0, 0, time.UTC),
		},
		{
			name:     "日窗口对齐到次日零点",
			window:   "day",
			expected: time.Date(2025, 7, 9, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "周窗口对齐到下周一",
			window:   "week",
			expected: time.Date(2025, 7, 14, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "月窗口对齐到下月1号",
			window:   "month",
			expected: time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "未知窗口返回零值",
			window:   "unknown",
			expected: time.Time{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeWindowResetAt(tc.window, now)
			if !got.Equal(tc.expected) {
				t.Errorf("computeWindowResetAt(%q, %v) = %v, 期望 %v", tc.window, now, got, tc.expected)
			}
		})
	}
}

// ── 周窗口边界测试：周日 ──

func TestComputeWindowResetAt_WeekSunday(t *testing.T) {
	// 周日 -> 下周一 = 1 天后
	sunday := time.Date(2025, 7, 13, 10, 0, 0, 0, time.UTC)
	expected := time.Date(2025, 7, 14, 0, 0, 0, 0, time.UTC)
	got := computeWindowResetAt("week", sunday)
	if !got.Equal(expected) {
		t.Errorf("周日 week ResetsAt = %v, 期望 %v", got, expected)
	}
}

func TestComputeWindowResetAt_WeekMonday(t *testing.T) {
	// 周一 -> 下周一 = 7 天后
	monday := time.Date(2025, 7, 7, 10, 0, 0, 0, time.UTC)
	expected := time.Date(2025, 7, 14, 0, 0, 0, 0, time.UTC)
	got := computeWindowResetAt("week", monday)
	if !got.Equal(expected) {
		t.Errorf("周一 week ResetsAt = %v, 期望 %v", got, expected)
	}
}

// ── 月窗口边界测试：月末 ──

func TestComputeWindowResetAt_MonthEnd(t *testing.T) {
	// 7月31日 -> 8月1日
	jul31 := time.Date(2025, 7, 31, 23, 59, 0, 0, time.UTC)
	expected := time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC)
	got := computeWindowResetAt("month", jul31)
	if !got.Equal(expected) {
		t.Errorf("月末 month ResetsAt = %v, 期望 %v", got, expected)
	}
}

// ── 并发安全测试 ──

func TestUsageMeter_ConcurrentSafety(t *testing.T) {
	m := NewUsageMeter(UsageMeterConfig{QuietLogs: true}, nil)

	done := make(chan struct{}, 20)
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			m.RecordRequest("ep-concurrent", 10, RequestRecordMeta{})
		}()
	}
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			m.ComputeWindows("ep-concurrent")
		}()
	}
	for i := 0; i < 20; i++ {
		<-done
	}

	windows := m.ComputeWindows("ep-concurrent")
	for _, w := range windows {
		if w.Used < 10 {
			t.Errorf("并发测试窗口 %s Used = %f, 期望 >= 10", w.Window, w.Used)
		}
	}
}

// ── BucketReader 回退测试（无桶数据时回退到内部计数器）──

func TestUsageMeter_FallbackToCounter(t *testing.T) {
	reader := newMockBucketReader() // 空的 bucket reader
	cfg := UsageMeterConfig{
		Windows:   []string{"5h"},
		QuietLogs: true,
	}
	m := NewUsageMeter(cfg, reader)

	m.RecordRequest("ep-fallback", 0, RequestRecordMeta{})
	m.RecordRequest("ep-fallback", 0, RequestRecordMeta{})
	m.RecordRequest("ep-fallback", 0, RequestRecordMeta{})

	windows := m.ComputeWindows("ep-fallback")
	if len(windows) != 1 {
		t.Fatalf("期望 1 个窗口, 得到 %d", len(windows))
	}
	// 无桶数据，应从内部计数器得到 3
	if windows[0].Used != 3 {
		t.Errorf("回退到计数器: Used = %f, 期望 3", windows[0].Used)
	}
}
