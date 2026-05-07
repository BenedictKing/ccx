package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// fakeClock 用于单测中可控时间的 clock 实现。
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t
}

// makeStore 构造一个 store 用于测试，内含极短 flush 间隔便于触发后台 tick。
func makeStore(t *testing.T, dir string, c clock, retention int) *NDJSONStore {
	t.Helper()
	cfg := Config{
		Dir:           dir,
		RetentionDays: retention,
		FlushInterval: 20 * time.Millisecond,
	}
	s, err := newNDJSONStoreWithClock(cfg, c)
	if err != nil {
		t.Fatalf("newNDJSONStoreWithClock failed: %v", err)
	}
	return s
}

// TestNDJSONStore_AppendBasic 单条记录写入 + Close 后能从磁盘读回 + JSON 可解析。
func TestNDJSONStore_AppendBasic(t *testing.T) {
	dir := t.TempDir()
	clk := &fakeClock{now: time.Date(2026, 5, 7, 8, 0, 0, 0, time.UTC)}
	s := makeStore(t, dir, clk, 30)

	cost := decimal.RequireFromString("1.2345")
	rec := UsageRecord{
		RequestID:    "r1",
		Timestamp:    clk.Now(),
		ChannelID:    1,
		ChannelName:  "c1",
		ModelID:      "m1",
		InputTokens:  10,
		OutputTokens: 5,
		TotalTokens:  15,
		TotalCost:    &cost,
		Success:      true,
	}
	if err := s.Append(context.Background(), rec); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	want := filepath.Join(dir, "usage-2026-05-07.ndjson")
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read file failed: %v", err)
	}
	var back UsageRecord
	if err := json.Unmarshal(data[:len(data)-1], &back); err != nil { // 去掉行末换行
		t.Fatalf("unmarshal failed: %v\n%s", err, data)
	}
	if back.RequestID != "r1" || back.TotalCost == nil || !back.TotalCost.Equal(cost) {
		t.Errorf("roundtrip mismatch: %#v", back)
	}
}

// TestNDJSONStore_ConcurrentAppend 200 goroutine × 10 record，全部落盘且每行可解析。
func TestNDJSONStore_ConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	clk := &fakeClock{now: time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)}
	s := makeStore(t, dir, clk, 30)

	const goroutines = 200
	const perGoroutine = 10
	var wg sync.WaitGroup
	var failed atomic.Int64
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				rec := UsageRecord{
					RequestID:    fmt.Sprintf("g%d-i%d", gid, i),
					Timestamp:    clk.Now(),
					ChannelID:    gid,
					ModelID:      "m",
					InputTokens:  int64(i),
					OutputTokens: int64(i * 2),
					TotalTokens:  int64(i * 3),
					Success:      true,
				}
				if err := s.Append(context.Background(), rec); err != nil {
					failed.Add(1)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	if err := s.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if failed.Load() != 0 {
		t.Fatalf("有 %d 次 Append 失败", failed.Load())
	}

	path := filepath.Join(dir, "usage-2026-05-07.ndjson")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open file failed: %v", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<16), 1<<20)
	count := 0
	seen := make(map[string]bool, goroutines*perGoroutine)
	for scanner.Scan() {
		var rec UsageRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			t.Fatalf("解析第 %d 行失败: %v\n行内容: %s", count+1, err, scanner.Text())
		}
		if seen[rec.RequestID] {
			t.Fatalf("重复 RequestID: %s", rec.RequestID)
		}
		seen[rec.RequestID] = true
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner err: %v", err)
	}
	if count != goroutines*perGoroutine {
		t.Fatalf("期望 %d 行，实际 %d 行", goroutines*perGoroutine, count)
	}
}

// TestNDJSONStore_DailyRotation mock clock 跳到下一天时新文件被创建，旧文件不被覆盖。
func TestNDJSONStore_DailyRotation(t *testing.T) {
	dir := t.TempDir()
	day1 := time.Date(2026, 5, 7, 23, 30, 0, 0, time.UTC)
	clk := &fakeClock{now: day1}
	s := makeStore(t, dir, clk, 30)

	if err := s.Append(context.Background(), UsageRecord{
		RequestID: "day1", Timestamp: day1, ChannelID: 1, ModelID: "m",
		InputTokens: 1, OutputTokens: 1, TotalTokens: 2, Success: true,
	}); err != nil {
		t.Fatalf("Append day1 failed: %v", err)
	}

	// 跨天
	day2 := time.Date(2026, 5, 8, 0, 5, 0, 0, time.UTC)
	clk.Set(day2)
	// 等待后台 tick 触发 reopen（FlushInterval=20ms，留 200ms）
	time.Sleep(200 * time.Millisecond)

	if err := s.Append(context.Background(), UsageRecord{
		RequestID: "day2", Timestamp: day2, ChannelID: 1, ModelID: "m",
		InputTokens: 1, OutputTokens: 1, TotalTokens: 2, Success: true,
	}); err != nil {
		t.Fatalf("Append day2 failed: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	day1Path := filepath.Join(dir, "usage-2026-05-07.ndjson")
	day2Path := filepath.Join(dir, "usage-2026-05-08.ndjson")
	for _, p := range []string{day1Path, day2Path} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("文件 %s 不存在: %v", p, err)
		}
		if fi.Size() == 0 {
			t.Fatalf("文件 %s 为空", p)
		}
	}
	// 验证 day1 文件没有 day2 记录
	day1Data, _ := os.ReadFile(day1Path)
	if !contains(day1Data, "day1") || contains(day1Data, "day2") {
		t.Fatalf("day1 文件内容污染: %s", day1Data)
	}
	day2Data, _ := os.ReadFile(day2Path)
	if !contains(day2Data, "day2") || contains(day2Data, "day1") {
		t.Fatalf("day2 文件内容污染: %s", day2Data)
	}
}

// TestNDJSONStore_RetentionCleanup 启动时清理超期文件。
func TestNDJSONStore_RetentionCleanup(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

	// 制造 31 天前的旧文件 + 1 天前的近期文件 + 一个非法文件名
	old := now.AddDate(0, 0, -31).UTC().Format(fileNameLayout)
	recent := now.AddDate(0, 0, -1).UTC().Format(fileNameLayout)
	unrelated := "random.log"
	for _, n := range []string{old, recent, unrelated} {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("写入 %s 失败: %v", p, err)
		}
	}

	clk := &fakeClock{now: now}
	s := makeStore(t, dir, clk, 30)
	defer s.Close()

	// 旧文件应被删
	if _, err := os.Stat(filepath.Join(dir, old)); !os.IsNotExist(err) {
		t.Errorf("超期文件未被删除: %v", err)
	}
	// 近期文件应保留
	if _, err := os.Stat(filepath.Join(dir, recent)); err != nil {
		t.Errorf("近期文件被误删: %v", err)
	}
	// 非法文件名应保留
	if _, err := os.Stat(filepath.Join(dir, unrelated)); err != nil {
		t.Errorf("非法文件名被误删: %v", err)
	}
}

// TestNDJSONStore_AppendAfterClose Close 后再 Append 返回 ErrStoreClosed。
func TestNDJSONStore_AppendAfterClose(t *testing.T) {
	dir := t.TempDir()
	clk := &fakeClock{now: time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)}
	s := makeStore(t, dir, clk, 30)
	if err := s.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	err := s.Append(context.Background(), UsageRecord{RequestID: "after"})
	if err == nil {
		t.Fatalf("期望 Append 在 Close 后返回错误，得到 nil")
	}
	if err != ErrStoreClosed {
		t.Errorf("期望 ErrStoreClosed，得到 %v", err)
	}
	// 再次 Close 应该幂等
	if err := s.Close(); err != nil {
		t.Errorf("二次 Close 应幂等，得到 %v", err)
	}
}

// TestNDJSONStore_FlushBeforeClose Close 应把缓冲区里的数据刷盘。
func TestNDJSONStore_FlushBeforeClose(t *testing.T) {
	dir := t.TempDir()
	clk := &fakeClock{now: time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)}
	cfg := Config{
		Dir:           dir,
		RetentionDays: 30,
		FlushInterval: 1 * time.Hour, // 故意拉长，确保 ticker 不会跑
	}
	s, err := newNDJSONStoreWithClock(cfg, clk)
	if err != nil {
		t.Fatalf("new failed: %v", err)
	}
	for i := 0; i < 5; i++ {
		_ = s.Append(context.Background(), UsageRecord{
			RequestID: fmt.Sprintf("r%d", i), Timestamp: clk.Now(),
			ChannelID: 1, ModelID: "m", Success: true,
		})
	}
	// Close 之前文件可能为空（因为还在 buffer），调用 Close 后应有 5 行
	if err := s.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "usage-2026-05-07.ndjson"))
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	count := 0
	for _, b := range data {
		if b == '\n' {
			count++
		}
	}
	if count != 5 {
		t.Errorf("期望 5 行，实际 %d 行", count)
	}
}

// contains helper for byte-slice search without importing bytes in too many places.
func contains(b []byte, s string) bool {
	return len(s) == 0 || (len(b) >= len(s) && (string(b) == s || indexOf(b, s) >= 0))
}

func indexOf(haystack []byte, needle string) int {
	hs := string(haystack)
	for i := 0; i+len(needle) <= len(hs); i++ {
		if hs[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
