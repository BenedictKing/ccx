package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// TestCalibrateOfficialReleaseEffort 验证官方公告 DeepSWE 等价证据的校准链：
// medium 等价分直取（×100 对齐直测尺度）、Evidence=Calibrated、封顶 high
// （锚点相对折算属估计值，不得证明 premium）。
func TestCalibrateOfficialReleaseEffort(t *testing.T) {
	makeEquiv := func(effort string, raw float64) config.ModelBenchmarkEvidence {
		return config.ModelBenchmarkEvidence{
			Benchmark: "official_release", Domain: "coding", Metric: "deepswe_equivalent",
			Effort: effort, RawValue: raw, TaskCount: 1, CohortSize: 1,
		}
	}

	// medium 等价分直取：0.728 → 72.8，EvidenceCalibrated
	result, ok := calibrateOfficialReleaseEffort([]config.ModelBenchmarkEvidence{makeEquiv("medium", 0.728)})
	if !ok || result.Score != 72.8 || result.Class != EvidenceCalibrated {
		t.Fatalf("calibrate = %v/%v/%v, want 72.8/true/calibrated", result.Score, ok, result.Class)
	}

	// 跨条目取最大
	result, ok = calibrateOfficialReleaseEffort([]config.ModelBenchmarkEvidence{
		makeEquiv("medium", 0.689), makeEquiv("medium", 0.728),
	})
	if !ok || result.Score != 72.8 {
		t.Fatalf("multi-entry = %v/%v, want 72.8/true", result.Score, ok)
	}

	// 封顶：72.8 分按分值可进 premium，但 calibrated 证据封顶 high
	if tier := qualityTierFromCalibration(result); tier != QualityTierHigh {
		t.Fatalf("tier = %v, want high（calibrated 封顶）", tier)
	}

	// 非 medium 档按 effort 比率折算（防御路径）：0.728@max / 1.975
	result, ok = calibrateOfficialReleaseEffort([]config.ModelBenchmarkEvidence{makeEquiv("max", 0.728)})
	if !ok || result.Class != EvidenceCalibrated || result.MeasuredEffort != EffortMax {
		t.Fatalf("max-effort deflate = %v/%v/%v, want calibrated/max", result.Score, ok, result.MeasuredEffort)
	}
	wantScore := 0.728 * 100 / effortQualityRatio["max"]
	if diff := result.Score - wantScore; diff > 0.01 || diff < -0.01 {
		t.Fatalf("max-effort score = %v, want ~%v", result.Score, wantScore)
	}

	// 无等价证据 → false
	if _, ok := calibrateOfficialReleaseEffort([]config.ModelBenchmarkEvidence{
		{Benchmark: "deepswe", Domain: "coding", Metric: "pass_at_1", Effort: "medium", RawValue: 0.7, TaskCount: 100},
	}); ok {
		t.Fatal("non-official evidence should not calibrate via official chain")
	}
}

// TestCalibrateModelCapabilityOfficialReleaseFallback 验证优先级链：
// 直测 > AA coding_index > official_release 等价；无直测/AA 证据的模型
// 通过 official 链拿到 Calibrated 档而非退回族先验。
func TestCalibrateModelCapabilityOfficialReleaseFallback(t *testing.T) {
	// claude-opus-5-5 经 prefill 后只有 AA intelligence_index + official_release 等价，
	// 无 deepswe 直测与 AA coding_index → 应命中 official 链（EvidenceCalibrated）。
	if calib, ok := calibrateModelCapability("claude-opus-5-5"); ok {
		if calib.Class != EvidenceCalibrated {
			t.Fatalf("class = %v, want calibrated (official fallback)", calib.Class)
		}
		// 72.8 分 + calibrated 封顶 → high
		if tier := qualityTierFromCalibration(calib); tier != QualityTierHigh {
			t.Fatalf("tier = %v, want high", tier)
		}
	}
	// registry 有 profile 但无任何校准证据的模型 → false（族先验路径不受影响）
	if _, ok := calibrateModelCapability("claude-mythos-preview"); ok {
		t.Fatal("model without calibration evidence should return false")
	}
}
