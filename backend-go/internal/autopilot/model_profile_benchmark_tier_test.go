package autopilot

import (
	"math"
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

func TestRegularEffortBaselineScoreSingleEffortOnly(t *testing.T) {
	ev := func(benchmark, effort string, raw float64) config.ModelBenchmarkEvidence {
		return config.ModelBenchmarkEvidence{
			Benchmark: benchmark, Domain: "coding", Metric: "pass_at_1",
			Effort: effort, RawValue: raw,
		}
	}
	// evN 带任务格数：用于小样本剔除行为验证
	evN := func(benchmark, effort string, raw float64, taskCount int) config.ModelBenchmarkEvidence {
		e := ev(benchmark, effort, raw)
		e.TaskCount = taskCount
		return e
	}
	tests := []struct {
		name       string
		evidence   []config.ModelBenchmarkEvidence
		singleWant bool
		okWant     bool
		scoreWant  float64
	}{
		{
			name:       "常规口径实测不算单一档",
			evidence:   []config.ModelBenchmarkEvidence{ev("deepswe", "default", 0.60)},
			singleWant: false, okWant: true, scoreWant: 60,
		},
		{
			name:       "仅 low 档证据标记单一档（上折虚高需封顶）",
			evidence:   []config.ModelBenchmarkEvidence{ev("deepswe", "low", 0.50)},
			singleWant: true, okWant: true, scoreWant: 0.50 * 100 / 0.686,
		},
		{
			name:       "仅 max 档证据标记单一档",
			evidence:   []config.ModelBenchmarkEvidence{ev("codexradar", "max", 0.90)},
			singleWant: true, okWant: true, scoreWant: 0.90 * 100 / 1.975,
		},
		{
			name: "小样本 low 档被剔除：1 任务 100% 不得抬高插值（hy4-preview 回归）",
			evidence: []config.ModelBenchmarkEvidence{
				evN("codexradar", "low", 1.0, 1),   // 1 任务全过，纯噪声
				evN("codexradar", "max", 0.333, 6), // 唯一可信档
			},
			singleWant: true, okWant: true,
			scoreWant: 0.333 * 100 / 1.975,
		},
		{
			name: "全部档位均小样本则无可用等效分",
			evidence: []config.ModelBenchmarkEvidence{
				evN("codexradar", "low", 1.0, 1),
				evN("codexradar", "max", 0.5, 2),
			},
			singleWant: false, okWant: false,
		},
		{
			name:       "任务格数达到阈值即可信",
			evidence:   []config.ModelBenchmarkEvidence{evN("deepswe", "low", 0.50, 3)},
			singleWant: true, okWant: true, scoreWant: 0.50 * 100 / 0.686,
		},
		{
			name: "low+high 跨 medium 相邻两档线性插值",
			evidence: []config.ModelBenchmarkEvidence{
				ev("deepswe", "low", 0.50), ev("deepswe", "high", 0.80),
			},
			singleWant: false, okWant: true,
			scoreWant: 65, // ranks 2,4 → t=0.5：(50+80)/2
		},
		{
			name: "low+xhigh 跨 medium 插值按序数加权",
			evidence: []config.ModelBenchmarkEvidence{
				ev("deepswe", "low", 0.50), ev("deepswe", "xhigh", 0.80),
			},
			singleWant: false, okWant: true,
			scoreWant: 50 + (80-50)/3.0, // ranks 2,5 → t=(3-2)/(5-2)=1/3
		},
		{
			name: "high+max 同侧保留全局比率折算取最小",
			evidence: []config.ModelBenchmarkEvidence{
				ev("deepswe", "high", 0.80), ev("deepswe", "max", 0.90),
			},
			singleWant: false, okWant: true,
			scoreWant: 0.90 * 100 / 1.975, // min(56.6, 45.6)
		},
		{
			name: "强证据层胜出：跨档插值不被单点折算否决",
			evidence: []config.ModelBenchmarkEvidence{
				ev("deepswe", "low", 0.50), ev("deepswe", "high", 0.80), // 插值 65
				ev("codexradar", "max", 0.90), // 单点折算 45.6（弱证据层）
			},
			singleWant: false, okWant: true,
			scoreWant: 65,
		},
		{
			name: "直测层胜出：medium 实测不被单侧折算否决",
			evidence: []config.ModelBenchmarkEvidence{
				ev("deepswe", "medium", 0.54), ev("codexradar", "high", 0.607), // 单侧折算 43.0
			},
			singleWant: false, okWant: true,
			scoreWant: 54,
		},
		{
			name: "同侧档跨源合并后折算取最小",
			evidence: []config.ModelBenchmarkEvidence{
				ev("deepswe", "max", 0.628), ev("codexradar", "high", 0.444), ev("codexradar", "max", 0.54),
			},
			singleWant: false, okWant: true,
			scoreWant: math.Min(0.628*100/1.975, 0.444*100/1.413), // min(31.8, 31.4)
		},
		{
			name:       "非 coding 或非 deepswe/codexradar 证据忽略",
			evidence:   []config.ModelBenchmarkEvidence{ev("artificial_analysis", "low", 0.50)},
			singleWant: false, okWant: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score, single, ok := regularEffortBaselineScore(tt.evidence)
			if ok != tt.okWant || single != tt.singleWant {
				t.Fatalf("single/ok = %v/%v, want %v/%v", single, ok, tt.singleWant, tt.okWant)
			}
			if tt.okWant && math.Abs(score-tt.scoreWant) > 1e-9 {
				t.Fatalf("score = %v, want %v", score, tt.scoreWant)
			}
		})
	}
}

func TestIsSmallSampleEvidenceBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		taskCount int
		want      bool
	}{
		{"未提供任务数视为可信", 0, false},
		{"单任务属小样本", 1, true},
		{"两任务属小样本", 2, true},
		{"达到阈值即可信", 3, false},
		{"大样本可信", 100, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := config.ModelBenchmarkEvidence{Benchmark: "codexradar", TaskCount: tt.taskCount}
			if got := isSmallSampleEvidence(ev); got != tt.want {
				t.Fatalf("isSmallSampleEvidence(taskCount=%d) = %v, want %v", tt.taskCount, got, tt.want)
			}
		})
	}
}
