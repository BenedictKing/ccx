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
	calib, ok := calibrateModelCapability("hy4-preview")
	if !ok {
		t.Fatal("hy4-preview calibration must be available for precedence regression test")
	}
	if calibratedTier := qualityTierFromCalibration(calib); calibratedTier == QualityTierLow {
		t.Fatalf("test setup no longer distinguishes precedence: calibrated tier = %q", calibratedTier)
	}
	if got := ModelProfileQualityTier("hy4-preview", ModelFamilyUnknown); got != QualityTierLow {
		t.Fatalf("ModelProfileQualityTier(hy4-preview) = %q, want explicit low", got)
	}
}
