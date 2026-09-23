package racing

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestFamilyForModel(t *testing.T) {
	cases := []struct {
		model string
		want  Family
	}{
		{"claude-opus-5", FamilyClaude},
		{"claude-fable-5-1", FamilyClaude},
		{"Claude-Sonnet-4", FamilyClaude},
		{"gpt-5.6", FamilyGPT},
		{"GPT-4o", FamilyGPT},
		{"o3-mini", FamilyGPT},
		{"gemini-3.8-pro", FamilyGemini},
		{"deepseek-v4", FamilyOther},
		{"kimi-k3", FamilyOther},
		{"", FamilyOther},
	}
	for _, tc := range cases {
		if got := FamilyForModel(tc.model); got != tc.want {
			t.Errorf("FamilyForModel(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestBehaviorForCostPreference(t *testing.T) {
	cases := []struct {
		pref          string
		wantShadows   int
		wantFloorMs   int
		wantCheapOnly bool
	}{
		{"quality_first", 3, 4000, false},
		{"balanced", 1, 8000, false},
		{"", 1, 8000, false},
		{"unknown", 1, 8000, false},
		{"cost_first", 1, 8000, true},
	}
	for _, tc := range cases {
		got := BehaviorForCostPreference(tc.pref)
		if got.MaxShadows != tc.wantShadows || got.StreamFloorMs != tc.wantFloorMs || got.CheapCandidateOnly != tc.wantCheapOnly {
			t.Errorf("BehaviorForCostPreference(%q) = %+v, want shadows=%d floor=%d cheapOnly=%v",
				tc.pref, got, tc.wantShadows, tc.wantFloorMs, tc.wantCheapOnly)
		}
	}
}

func TestStreamFloorForReasoning(t *testing.T) {
	cases := []struct {
		effort string
		want   int
	}{
		{"", 24_000},
		{"low", 16_000},
		{"medium", 24_000},
		{"high", 30_000},
		{"xhigh", 30_000},
		{"none", 8_000},
	}
	for _, tc := range cases {
		if got := StreamFloorForReasoning(8_000, tc.effort); got != tc.want {
			t.Fatalf("StreamFloorForReasoning(%q) = %d, want %d", tc.effort, got, tc.want)
		}
	}
	if got := StreamFloorForReasoning(35_000, "low"); got != 35_000 {
		t.Fatalf("已有更高 floor 时不应降低，got %d", got)
	}
}

func TestRegistryThresholdFallbackAndClamp(t *testing.T) {
	r := NewRegistry()
	// 样本不足：回退 floor
	if got := r.ThresholdMs(FamilyClaude, StageStreamFirstContent, 3000, 60000); got != 3000 {
		t.Fatalf("样本不足时应回退 floor, got %d", got)
	}
	// 灌入 20 个 1000ms 样本（低于 floor）：p90=1000 → clamp 到 floor
	for i := 0; i < sampleFloor; i++ {
		r.Record(FamilyClaude, StageStreamFirstContent, 1000)
	}
	if got := r.ThresholdMs(FamilyClaude, StageStreamFirstContent, 3000, 60000); got != 3000 {
		t.Fatalf("p90 低于 floor 应 clamp 到 floor, got %d", got)
	}
	// 灌入更高样本把 p90 抬到 50000：超过 ceiling → clamp 到 ceiling
	for i := 0; i < sampleFloor; i++ {
		r.Record(FamilyClaude, StageStreamFirstContent, 50000)
	}
	if got := r.ThresholdMs(FamilyClaude, StageStreamFirstContent, 3000, 40000); got != 40000 {
		t.Fatalf("p90 超过 ceiling 应 clamp 到 ceiling, got %d", got)
	}
	if got := r.ThresholdMs(FamilyClaude, StageStreamFirstContent, 3000, 60000); got != 50000 {
		t.Fatalf("p90 在界内应取 p90, got %d", got)
	}
	// 家族×阶段窗口隔离
	if got := r.SampleCount(FamilyGPT, StageStreamFirstContent); got != 0 {
		t.Fatalf("家族窗口应隔离, GPT 样本数 %d", got)
	}
	if got := r.SampleCount(FamilyClaude, StageNonStreamComplete); got != 0 {
		t.Fatalf("阶段窗口应隔离, nonstream 样本数 %d", got)
	}
}

func TestRegistryWindowPrune(t *testing.T) {
	r := NewRegistry()
	base := time.Unix(1700000000, 0)
	r.now = func() time.Time { return base }
	for i := 0; i < 10; i++ {
		r.Record(FamilyClaude, StageStreamFirstContent, int64(1000+i))
	}
	// 时间前进 16 分钟：全部过期
	r.now = func() time.Time { return base.Add(16 * time.Minute) }
	if got := r.SampleCount(FamilyClaude, StageStreamFirstContent); got != 0 {
		t.Fatalf("过期样本应被淘汰, got %d", got)
	}
	// 过期后回退 floor
	if got := r.ThresholdMs(FamilyClaude, StageStreamFirstContent, 3000, 60000); got != 3000 {
		t.Fatalf("全部过期后应回退 floor, got %d", got)
	}
}

func TestRegistryCapacityCap(t *testing.T) {
	r := NewRegistry()
	for i := 0; i < maxSamplesPerWindow+100; i++ {
		r.Record(FamilyClaude, StageStreamFirstContent, 1000)
	}
	if got := r.SampleCount(FamilyClaude, StageStreamFirstContent); got != maxSamplesPerWindow {
		t.Fatalf("样本数应封顶 %d, got %d", maxSamplesPerWindow, got)
	}
}

func TestRegistryInvalidSampleIgnored(t *testing.T) {
	r := NewRegistry()
	r.Record(FamilyClaude, StageStreamFirstContent, 0)
	r.Record(FamilyClaude, StageStreamFirstContent, -5)
	if got := r.SampleCount(FamilyClaude, StageStreamFirstContent); got != 0 {
		t.Fatalf("非正样本应被忽略, got %d", got)
	}
}

func TestGateClaimOnce(t *testing.T) {
	g := NewGate()
	if !g.Claim() {
		t.Fatal("首次 claim 应成功")
	}
	if g.Claim() {
		t.Fatal("二次 claim 应失败")
	}
	if !g.Claimed() {
		t.Fatal("Claimed 应为 true")
	}
}

func TestGateConcurrentClaimSingleWinner(t *testing.T) {
	g := NewGate()
	const n = 32
	var wg sync.WaitGroup
	winners := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if g.Claim() {
				winners <- struct{}{}
			}
		}()
	}
	wg.Wait()
	close(winners)
	count := 0
	for range winners {
		count++
	}
	if count != 1 {
		t.Fatalf("并发 claim 应只有 1 个赢家, got %d", count)
	}
}

func TestSemaphore(t *testing.T) {
	s := NewSemaphore(2)
	if !s.TryAcquire() || !s.TryAcquire() {
		t.Fatal("容量内应可获取")
	}
	if s.TryAcquire() {
		t.Fatal("超容量应失败")
	}
	s.Release()
	if !s.TryAcquire() {
		t.Fatal("释放后应可获取")
	}
	s.Release()
	s.Release()
	s.Release() // 多余 release 不应 panic 或泄漏
	if !s.TryAcquire() || !s.TryAcquire() {
		t.Fatal("多余 release 后容量应恢复")
	}
}

func TestErrRacingSupersededIs(t *testing.T) {
	wrapped := errors.Join(ErrRacingSuperseded, contextCanceled())
	if !errors.Is(wrapped, ErrRacingSuperseded) {
		t.Fatal("errors.Join 包装后应可被 errors.Is 识别")
	}
}

func contextCanceled() error { return errCanceled }

var errCanceled = errors.New("canceled stub")
