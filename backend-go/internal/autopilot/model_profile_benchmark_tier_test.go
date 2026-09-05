package autopilot

import (
	"math"
	"sync"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestComputeQualityTierBoundariesConsistentAcrossCalls(t *testing.T) {
	wantPremium, wantHigh, wantNormal, wantAvoid := computeQualityTierBoundaries()
	for i := 0; i < 3; i++ {
		if p, h, n, a := computeQualityTierBoundaries(); p != wantPremium || h != wantHigh || n != wantNormal || a != wantAvoid {
			t.Fatalf("边界计算不稳定: %v/%v/%v/%v vs %v/%v/%v/%v", p, h, n, a, wantPremium, wantHigh, wantNormal, wantAvoid)
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
			_, _, _, _ = computeQualityTierBoundaries()
		}()
	}
	wg.Wait()
}

// TestQualityTierThresholdsAnchoredToDirectEvidence 是固定阈值的离线校准锚定测试：
// 注册表直测分布只在此处（离线）验证阈值的合理性，不进入请求热路径。
// 断言的是结构性不变量而非精确值——数据温和漂移时保持稳定，分布形态剧变
// （premium singleton、档位成员枯竭）时报警，提示重新校准阈值并升版本号。
func TestQualityTierThresholdsAnchoredToDirectEvidence(t *testing.T) {
	premiumMin, highMin, normalMin, avoidMax := computeQualityTierBoundaries()
	if !(premiumMin > highMin && highMin > normalMin && normalMin > avoidMax && avoidMax > 0) {
		t.Fatalf("阈值必须严格递减为正: %.2f/%.2f/%.2f/%.2f", premiumMin, highMin, normalMin, avoidMax)
	}

	// 收集 medium/default 直测池（离线校准证据源）。
	scores := make([]float64, 0, 16)
	seen := make(map[string]struct{})
	for _, bp := range config.BuiltinModelBenchmarkProfiles() {
		if _, ok := seen[bp.CanonicalModel]; ok {
			continue
		}
		seen[bp.CanonicalModel] = struct{}{}
		if score, ok := directRegularEffortScore(bp.BenchmarkEvidence); ok {
			scores = append(scores, score)
		}
	}
	if len(scores) < 8 {
		t.Skipf("直测池仅 %d 个模型，不足以锚定", len(scores))
	}
	sortFloat64s(scores)

	countAtLeast := func(min float64) int {
		n := 0
		for _, s := range scores {
			if s >= min {
				n++
			}
		}
		return n
	}
	// premium 档在直测池中至少 2 个成员（singleton 回归报警——2026-09-04
	// hy4-preview 插值登顶曾让动态边界把 premium 塌缩成 singleton）。
	if n := countAtLeast(premiumMin); n < 2 {
		t.Fatalf("premium 档直测成员仅 %d 个（阈值 %.2f），分布形态剧变，需重新校准阈值", n, premiumMin)
	}
	// high 档同样不枯竭。
	if n := countAtLeast(highMin); n < 4 {
		t.Fatalf("high 档及以上直测成员仅 %d 个（阈值 %.2f），分布形态剧变，需重新校准阈值", n, highMin)
	}
	// premium 边界仍在顶部区域（不低于直测池 75 分位值）。
	q3 := scores[(3*len(scores))/4]
	if premiumMin < q3 {
		t.Fatalf("premiumMin=%.2f 低于直测池 75 分位 %.2f，premium 档会被中段模型涌入", premiumMin, q3)
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

func TestDirectRegularEffortScore(t *testing.T) {
	makeEvidence := func(benchmark, effort string, raw float64, tasks int) config.ModelBenchmarkEvidence {
		return config.ModelBenchmarkEvidence{
			Benchmark: benchmark, Domain: "coding", Metric: "pass_at_1",
			Effort: effort, RawValue: raw, TaskCount: tasks,
		}
	}
	if got, ok := directRegularEffortScore([]config.ModelBenchmarkEvidence{
		makeEvidence("deepswe", "low", 0.99, 113),
		makeEvidence("deepswe", "medium", 0.72, 113),
		makeEvidence("codexradar", "default", 0.70, 98),
	}); !ok || got != 72 {
		t.Fatalf("direct regular score = %v/%v, want 72/true", got, ok)
	}
	if got, ok := directRegularEffortScore([]config.ModelBenchmarkEvidence{
		makeEvidence("deepswe", "low", 0.99, 113),
		makeEvidence("deepswe", "high", 0.90, 113),
	}); ok || got != -1 {
		t.Fatalf("estimated-only evidence = %v/%v, want -1/false", got, ok)
	}
	if got, ok := directRegularEffortScore([]config.ModelBenchmarkEvidence{
		makeEvidence("deepswe", "medium", 1.0, 1),
	}); ok || got != -1 {
		t.Fatalf("small sample evidence = %v/%v, want -1/false", got, ok)
	}
}

// TestCalibrateRegularEffortEvidenceClass 验证三层证据合成的分数与证据等级：
// 只有 medium/default 直测是 direct（唯一可证明 premium 的等级），
// 跨 medium 插值是 interpolated，其余折算是 deflated（旧 singleEffortOnly
// 语义已并入 deflated 封顶，且同侧多档折算从"不封顶"收紧为封顶 high）。
func TestCalibrateRegularEffortEvidenceClass(t *testing.T) {
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
		name      string
		evidence  []config.ModelBenchmarkEvidence
		classWant EvidenceClass
		okWant    bool
		scoreWant float64
	}{
		{
			name:      "常规口径实测为 direct",
			evidence:  []config.ModelBenchmarkEvidence{ev("deepswe", "default", 0.60)},
			classWant: EvidenceDirect, okWant: true, scoreWant: 60,
		},
		{
			name:      "仅 low 档证据折算为 deflated（上折虚高需封顶）",
			evidence:  []config.ModelBenchmarkEvidence{ev("deepswe", "low", 0.50)},
			classWant: EvidenceDeflated, okWant: true, scoreWant: 0.50 * 100 / 0.686,
		},
		{
			name:      "仅 max 档证据折算为 deflated",
			evidence:  []config.ModelBenchmarkEvidence{ev("codexradar", "max", 0.90)},
			classWant: EvidenceDeflated, okWant: true, scoreWant: 0.90 * 100 / 1.975,
		},
		{
			name: "小样本 low 档被剔除：1 任务 100% 不得抬高插值（hy4-preview 回归）",
			evidence: []config.ModelBenchmarkEvidence{
				evN("codexradar", "low", 1.0, 1),   // 1 任务全过，纯噪声
				evN("codexradar", "max", 0.333, 6), // 唯一可信档
			},
			classWant: EvidenceDeflated, okWant: true,
			scoreWant: 0.333 * 100 / 1.975,
		},
		{
			name: "全部档位均小样本则无可用等效分",
			evidence: []config.ModelBenchmarkEvidence{
				evN("codexradar", "low", 1.0, 1),
				evN("codexradar", "max", 0.5, 2),
			},
			classWant: "", okWant: false,
		},
		{
			name:      "任务格数达到阈值即可信",
			evidence:  []config.ModelBenchmarkEvidence{evN("deepswe", "low", 0.50, 3)},
			classWant: EvidenceDeflated, okWant: true, scoreWant: 0.50 * 100 / 0.686,
		},
		{
			name: "low+high 跨 medium 相邻两档线性插值为 interpolated",
			evidence: []config.ModelBenchmarkEvidence{
				ev("deepswe", "low", 0.50), ev("deepswe", "high", 0.80),
			},
			classWant: EvidenceInterpolated, okWant: true,
			scoreWant: 65, // ranks 2,4 → t=0.5：(50+80)/2
		},
		{
			name: "low+xhigh 跨 medium 插值按序数加权",
			evidence: []config.ModelBenchmarkEvidence{
				ev("deepswe", "low", 0.50), ev("deepswe", "xhigh", 0.80),
			},
			classWant: EvidenceInterpolated, okWant: true,
			scoreWant: 50 + (80-50)/3.0, // ranks 2,5 → t=(3-2)/(5-2)=1/3
		},
		{
			name: "high+max 同侧保留全局比率折算（deflated，统一封顶 high）",
			evidence: []config.ModelBenchmarkEvidence{
				ev("deepswe", "high", 0.80), ev("deepswe", "max", 0.90),
			},
			classWant: EvidenceDeflated, okWant: true,
			scoreWant: 0.90 * 100 / 1.975, // min(56.6, 45.6)
		},
		{
			name: "强证据层胜出：跨档插值不被单点折算否决",
			evidence: []config.ModelBenchmarkEvidence{
				ev("deepswe", "low", 0.50), ev("deepswe", "high", 0.80), // 插值 65
				ev("codexradar", "max", 0.90), // 单点折算 45.6（弱证据层）
			},
			classWant: EvidenceInterpolated, okWant: true,
			scoreWant: 65,
		},
		{
			name: "直测层胜出：medium 实测不被单侧折算否决",
			evidence: []config.ModelBenchmarkEvidence{
				ev("deepswe", "medium", 0.54), ev("codexradar", "high", 0.607), // 单侧折算 43.0
			},
			classWant: EvidenceDirect, okWant: true,
			scoreWant: 54,
		},
		{
			name: "同侧档跨源合并后折算取最小",
			evidence: []config.ModelBenchmarkEvidence{
				ev("deepswe", "max", 0.628), ev("codexradar", "high", 0.444), ev("codexradar", "max", 0.54),
			},
			classWant: EvidenceDeflated, okWant: true,
			scoreWant: math.Min(0.628*100/1.975, 0.444*100/1.413), // min(31.8, 31.4)
		},
		{
			name:      "非 coding 或非 deepswe/codexradar 证据忽略",
			evidence:  []config.ModelBenchmarkEvidence{ev("artificial_analysis", "low", 0.50)},
			classWant: "", okWant: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := calibrateRegularEffort(tt.evidence)
			if ok != tt.okWant {
				t.Fatalf("ok = %v, want %v", ok, tt.okWant)
			}
			if tt.okWant {
				if result.Class != tt.classWant {
					t.Fatalf("class = %v, want %v", result.Class, tt.classWant)
				}
				if math.Abs(result.Score-tt.scoreWant) > 1e-9 {
					t.Fatalf("score = %v, want %v", result.Score, tt.scoreWant)
				}
			}
		})
	}
}

// TestQualityTierFromCalibrationCapsEstimates 验证证据等级封顶：
// 估计类证据（interpolated/deflated/calibrated）即使分数达标也不得证明 premium。
func TestQualityTierFromCalibrationCapsEstimates(t *testing.T) {
	tests := []struct {
		name  string
		calib CalibrationResult
		want  QualityTier
	}{
		{"direct 高分进 premium", CalibrationResult{Score: 80, Class: EvidenceDirect}, QualityTierPremium},
		{"interpolated 高分封顶 high", CalibrationResult{Score: 90, Class: EvidenceInterpolated}, QualityTierHigh},
		{"deflated 高分封顶 high", CalibrationResult{Score: 70, Class: EvidenceDeflated}, QualityTierHigh},
		{"calibrated 高分封顶 high", CalibrationResult{Score: 90, Class: EvidenceCalibrated}, QualityTierHigh},
		{"direct 中段进 high", CalibrationResult{Score: 65, Class: EvidenceDirect}, QualityTierHigh},
		{"direct 中低段进 normal", CalibrationResult{Score: 50, Class: EvidenceDirect}, QualityTierNormal},
		{"低分为 low", CalibrationResult{Score: 20, Class: EvidenceDeflated}, QualityTierLow},
		{"封顶不影响更低档位", CalibrationResult{Score: 50, Class: EvidenceInterpolated}, QualityTierNormal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := qualityTierFromCalibration(tt.calib); got != tt.want {
				t.Fatalf("qualityTierFromCalibration(%+v) = %v, want %v", tt.calib, got, tt.want)
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

func sortFloat64s(vals []float64) {
	for i := 1; i < len(vals); i++ {
		for j := i; j > 0 && vals[j] < vals[j-1]; j-- {
			vals[j], vals[j-1] = vals[j-1], vals[j]
		}
	}
}

// TestModelProfileQualityTierRegistryReplay 锁定当前注册表在 v3 固定阈值下的
// 档位快照（回放验证）。快照随注册表数据演进而维护：基准数据大改后按
// 「锚定测试报警 → 重新校准阈值 → 升版本号 → 更新本快照」的流程处理。
func TestModelProfileQualityTierRegistryReplay(t *testing.T) {
	tests := []struct {
		model  string
		family ModelFamily
		want   QualityTier
		note   string
	}{
		{"gpt-6-astra", ModelFamilyOpenAI, QualityTierPremium, "direct 72.8 ≥ 69.95"},
		{"gemini-3.8-flash", ModelFamilyGemini, QualityTierPremium, "direct 71.0 ≥ 69.95"},
		// 2026-09-05 codexradar 覆盖率门槛（<60% 任务覆盖剔除）后 hy4-preview
		// 无可靠证据（原插值 76.1 的源行全部被门槛/小样本剔除），回落模型族 low
		{"hy4-preview", ModelFamilyUnknown, QualityTierLow, "覆盖门槛后无可靠证据，回落模型族"},
		{"claude-opus-5", ModelFamilyClaude, QualityTierHigh, "direct 68.9 < 69.95"},
		{"claude-fable-5", ModelFamilyClaude, QualityTierHigh, "direct 65.4"},
		{"gpt-5.6-sol", ModelFamilyOpenAI, QualityTierHigh, "direct 64.3"},
		{"kimi-k3", ModelFamilyKimi, QualityTierNormal, "interpolated 56.7 < 59.15"},
		{"gpt-5.5", ModelFamilyOpenAI, QualityTierNormal, "direct 54.0 < 59.15"},
		{"glm-5.3", ModelFamilyGLM, QualityTierNormal, "interpolated 54.0 < 59.15"},
		{"claude-opus-4-8", ModelFamilyClaude, QualityTierNormal, "direct 48.7"},
		{"claude-sonnet-5", ModelFamilyClaude, QualityTierLow, "direct 39.8 < 44.25"},
		{"grok-4.5", ModelFamilyGrok, QualityTierLow, "deflated 38.1"},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := ModelProfileQualityTier(tt.model, tt.family); got != tt.want {
				t.Fatalf("ModelProfileQualityTier(%s) = %v, want %v（%s）", tt.model, got, tt.want, tt.note)
			}
		})
	}
}

// TestQualityTierAvoidBoundary 锁定 v3 新增的 avoid 档边界语义：
// direct 证据 <15 分判 avoid、≥15 分判 low；估计类证据同样落 avoid（封顶只压上限）。
func TestQualityTierAvoidBoundary(t *testing.T) {
	_, _, _, avoidMax := computeQualityTierBoundaries()
	if avoidMax != 15.0 {
		t.Fatalf("avoidMax = %.2f, want 15.0（产品拍板值，改动须升阈值版本）", avoidMax)
	}
	cases := []struct {
		score float64
		class EvidenceClass
		want  QualityTier
		note  string
	}{
		{avoidMax - 0.01, EvidenceDirect, QualityTierAvoid, "直测 14.99 判不推荐"},
		{avoidMax, EvidenceDirect, QualityTierLow, "直测恰 15 分判 low（含边界）"},
		{44.24, EvidenceDirect, QualityTierLow, "直测 44.24 仍为 low"},
		{avoidMax - 0.01, EvidenceInterpolated, QualityTierAvoid, "估计证据低分同样 avoid（封顶不抬下限）"},
		{avoidMax - 0.01, EvidenceDeflated, QualityTierAvoid, "折算证据低分同样 avoid"},
	}
	for _, tt := range cases {
		if got := qualityTierFromCalibration(CalibrationResult{Score: tt.score, Class: tt.class}); got != tt.want {
			t.Fatalf("score=%.2f class=%s: tier = %v, want %v（%s）", tt.score, tt.class, got, tt.want, tt.note)
		}
	}
}
