package autopilot

import "testing"

func TestInferModelFamily_KimiCodeModels(t *testing.T) {
	for _, model := range []string{"k3", "k3[1m]", "kimi-k3", "k3.1-pro", "kimi-k3.1-pro", "k3.1-pro-highspeed", "kimi-k3.1-pro-highspeed", "kimi-for-coding", "kimi-for-coding-highspeed"} {
		if got := InferModelFamily(model, ""); got != ModelFamilyKimi {
			t.Errorf("InferModelFamily(%q) = %q, want %q", model, got, ModelFamilyKimi)
		}
	}
}

func TestModelProfileQualityTierFromFamily_KimiCodeModels(t *testing.T) {
	tests := []struct {
		model string
		want  QualityTier
	}{
		{model: "k3", want: QualityTierPremium},
		{model: "k3[1m]", want: QualityTierPremium},
		{model: "kimi-k3", want: QualityTierPremium},
		{model: "k3.1-pro", want: QualityTierPremium},
		{model: "kimi-k3.1-pro", want: QualityTierPremium},
		{model: "kimi-k3.1-pro-highspeed", want: QualityTierPremium},
		{model: "kimi-for-coding", want: QualityTierHigh},
		{model: "kimi-for-coding-highspeed", want: QualityTierHigh},
		{model: "kimi-k2.6", want: QualityTierHigh},
	}
	for _, tt := range tests {
		if got := ModelProfileQualityTierFromFamily(ModelFamilyKimi, tt.model); got != tt.want {
			t.Errorf("ModelProfileQualityTierFromFamily(%q) = %q, want %q", tt.model, got, tt.want)
		}
	}
}

func TestModelProfileQualityTierFromFamily_MultimodalFallbacks(t *testing.T) {
	tests := []struct {
		family ModelFamily
		model  string
		want   QualityTier
	}{
		{family: ModelFamilyMiniMax, model: "minimax-m3", want: QualityTierPremium},
		{family: ModelFamilyMiMo, model: "mimo-v2.5-pro", want: QualityTierHigh},
		{family: ModelFamilyMiMo, model: "mimo-v2.5", want: QualityTierNormal},
	}
	for _, tt := range tests {
		if got := ModelProfileQualityTierFromFamily(tt.family, tt.model); got != tt.want {
			t.Errorf("ModelProfileQualityTierFromFamily(%q) = %q, want %q", tt.model, got, tt.want)
		}
	}
}
