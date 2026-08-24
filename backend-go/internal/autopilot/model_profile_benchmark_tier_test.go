package autopilot

import (
	"sync"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestComputeQualityTierBoundariesCachedByGeneration(t *testing.T) {
	t.Cleanup(func() { benchmarkTierBoundariesCache.Store(nil) })
	// 注入过期世代的伪缓存，验证调用后按当前世代重算并覆盖缓存。
	benchmarkTierBoundariesCache.Store(&benchmarkTierBoundaries{generation: 0, premiumMin: -1, highMin: -2, normalMin: -3})
	premiumMin, highMin, normalMin := computeQualityTierBoundaries()
	if premiumMin == -1 || highMin == -2 || normalMin == -3 {
		t.Fatalf("过期缓存不应被复用: %v/%v/%v", premiumMin, highMin, normalMin)
	}
	cached := benchmarkTierBoundariesCache.Load()
	if cached == nil || cached.generation != config.BuiltinSnapshotGeneration() {
		t.Fatalf("缓存未按当前世代更新: %+v", cached)
	}
	if cached.premiumMin != premiumMin || cached.highMin != highMin || cached.normalMin != normalMin {
		t.Fatalf("缓存值与重算结果不一致: %+v vs %v/%v/%v", cached, premiumMin, highMin, normalMin)
	}

	// 世代不变时直接复用缓存：篡改缓存值后应原样返回。
	benchmarkTierBoundariesCache.Store(&benchmarkTierBoundaries{
		generation: config.BuiltinSnapshotGeneration(),
		premiumMin: 11, highMin: 22, normalMin: 33,
	})
	if p, h, n := computeQualityTierBoundaries(); p != 11 || h != 22 || n != 33 {
		t.Fatalf("同世代应复用缓存: %v/%v/%v", p, h, n)
	}
}

func TestComputeQualityTierBoundariesConsistentAcrossCalls(t *testing.T) {
	t.Cleanup(func() { benchmarkTierBoundariesCache.Store(nil) })
	wantPremium, wantHigh, wantNormal := computeQualityTierBoundaries()
	for i := 0; i < 3; i++ {
		if p, h, n := computeQualityTierBoundaries(); p != wantPremium || h != wantHigh || n != wantNormal {
			t.Fatalf("边界计算不稳定: %v/%v/%v vs %v/%v/%v", p, h, n, wantPremium, wantHigh, wantNormal)
		}
	}
}

func TestComputeQualityTierBoundariesConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ModelProfileQualityTier("gpt-5.4", ModelFamilyOpenAI)
			_, _, _ = computeQualityTierBoundaries()
		}()
	}
	wg.Wait()
}

func TestComputeQualityTierBoundariesWithinTopRegion(t *testing.T) {
	t.Cleanup(func() { benchmarkTierBoundariesCache.Store(nil) })
	// premium 断层必须在最高分下方 25% 量表区间内：60% 锚在分数集中分布时
	// 会把中段空隙包进"顶部区域"，曾把 premiumMin 塌到 49、53 分模型全部
	// 升入 premium。修复后的不变量：premium 边界不低于 0.75×最高分。
	premiumMin, _, _ := computeQualityTierBoundaries()

	maxScore := 0.0
	seen := make(map[string]struct{})
	for _, bp := range config.BuiltinModelBenchmarkProfiles() {
		if _, ok := seen[bp.CanonicalModel]; ok {
			continue
		}
		seen[bp.CanonicalModel] = struct{}{}
		if score, _, ok := regularEffortBaselineScore(bp.BenchmarkEvidence); ok && score > maxScore {
			maxScore = score
		}
	}
	if maxScore <= 0 {
		t.Skip("注册表无直测分数，使用默认边界")
	}
	if premiumMin < maxScore*0.75 {
		t.Fatalf("premiumMin=%.2f 低于顶部区域下限 %.2f（75%% 最高分 %.2f）", premiumMin, maxScore*0.75, maxScore)
	}
}

func TestInferModelFamilyDottedAndNewPrefixes(t *testing.T) {
	cases := []struct {
		id   string
		want ModelFamily
	}{
		{"qwen3.8-max", ModelFamilyQwen}, // 点号命名走 qwen3. 前缀
		{"qwen3-8-max", ModelFamilyQwen}, // 连字符命名不受影响
		{"grok-4.5", ModelFamilyGrok},
		{"grok-4.6", ModelFamilyGrok},
		{"muse-spark-1.1", ModelFamilyMuse},
		{"muse-spark-1-2", ModelFamilyMuse},
		{"claude-opus-5", ModelFamilyClaude},
	}
	for _, tc := range cases {
		if got := InferModelFamily(tc.id, ""); got != tc.want {
			t.Errorf("InferModelFamily(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}
