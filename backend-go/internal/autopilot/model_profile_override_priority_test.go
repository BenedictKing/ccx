package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestModelProfileQualityTierExplicitBoundaryPrecedesBenchmark(t *testing.T) {
	benchmark := config.ResolveModelBenchmarkProfile("hy4-preview")
	if !benchmark.Known {
		t.Fatal("hy4-preview benchmark must be present for precedence regression test")
	}
	// hy4-preview 的 codexradar 证据随雷达Bench v2 收敛退役（2026-10），
	// 全局注册表已无该模型直测证据；改用合成直测证据在函数层复现
	// 「若无人工边界，校准会给出 premium」的前提，验证显式边界仍然优先。
	calib, ok := calibrateBenchmarkEvidence([]config.ModelBenchmarkEvidence{
		{Benchmark: "codexradar", Domain: "coding", Metric: "mean_reward",
			Effort: "medium", RawValue: 0.8, TaskCount: 64},
	})
	if !ok {
		t.Fatal("synthetic calibration must be available for precedence regression test")
	}
	if calibratedTier := qualityTierFromCalibration(calib); calibratedTier != QualityTierPremium {
		t.Fatalf("test setup no longer distinguishes precedence: calibrated tier = %q, want premium", calibratedTier)
	}
	if got := ModelProfileQualityTier("hy4-preview", ModelFamilyUnknown); got != QualityTierLow {
		t.Fatalf("ModelProfileQualityTier(hy4-preview) = %q, want explicit low", got)
	}
}
