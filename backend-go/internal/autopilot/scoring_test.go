package autopilot

import (
	"math"
	"testing"
)

// ── 评分引擎单元测试（表驱动）──

// ── CalcFamilyPreferenceScore 测试 ──

func TestCalcFamilyPreferenceScore(t *testing.T) {
	tests := []struct {
		name   string
		family ModelFamily
		prefs  []ModelFamily
		want   float64
	}{
		{
			name:   "偏好列表首位得 n 分",
			family: ModelFamilyClaude,
			prefs:  []ModelFamily{ModelFamilyClaude, ModelFamilyOpenAI, ModelFamilyDeepSeek},
			want:   3,
		},
		{
			name:   "偏好列表第二位得 n-1 分",
			family: ModelFamilyOpenAI,
			prefs:  []ModelFamily{ModelFamilyClaude, ModelFamilyOpenAI, ModelFamilyDeepSeek},
			want:   2,
		},
		{
			name:   "偏好列表末位得 1 分",
			family: ModelFamilyDeepSeek,
			prefs:  []ModelFamily{ModelFamilyClaude, ModelFamilyOpenAI, ModelFamilyDeepSeek},
			want:   1,
		},
		{
			name:   "不在偏好列表得 0 分",
			family: ModelFamilyQwen,
			prefs:  []ModelFamily{ModelFamilyClaude, ModelFamilyOpenAI},
			want:   0,
		},
		{
			name:   "空偏好列表得 0 分",
			family: ModelFamilyClaude,
			prefs:  nil,
			want:   0,
		},
		{
			name:   "单元素偏好列表得 1 分",
			family: ModelFamilyClaude,
			prefs:  []ModelFamily{ModelFamilyClaude},
			want:   1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CalcFamilyPreferenceScore(tt.family, tt.prefs)
			if got != tt.want {
				t.Errorf("CalcFamilyPreferenceScore(%v, %v) = %v, want %v",
					tt.family, tt.prefs, got, tt.want)
			}
		})
	}
}

// ── ApplyCostPreference 测试 ──

func TestApplyCostPreference(t *testing.T) {
	base := ScoringWeights{
		WQuality: 2, WStability: 2, WSpeed: 1, WCost: 1, WSavings: 1,
		WTierMatch: 1, WFamily: 0.2, WProviderQuality: 1.0, WDomain: 0.5,
	}

	tests := []struct {
		name                string
		mode                CostPreferenceMode
		wantSavings         float64
		wantProviderQuality float64
	}{
		{
			name:                "quality_first 放大供应商质量 缩小省钱",
			mode:                CostPrefQualityFirst,
			wantSavings:         1.0 * 0.3, // 0.3
			wantProviderQuality: 1.0 * 1.5, // 1.5
		},
		{
			name:                "balanced 不变",
			mode:                CostPrefBalanced,
			wantSavings:         1.0,
			wantProviderQuality: 1.0,
		},
		{
			name:                "cost_first 放大省钱 缩小供应商质量",
			mode:                CostPrefCostFirst,
			wantSavings:         1.0 * 2.0, // 2.0
			wantProviderQuality: 1.0 * 0.5, // 0.5
		},
		{
			name:                "未知模式回退 balanced",
			mode:                CostPreferenceMode("unknown"),
			wantSavings:         1.0,
			wantProviderQuality: 1.0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyCostPreference(base, tt.mode)
			if !floatEq(got.WSavings, tt.wantSavings) {
				t.Errorf("WSavings = %v, want %v", got.WSavings, tt.wantSavings)
			}
			if !floatEq(got.WProviderQuality, tt.wantProviderQuality) {
				t.Errorf("WProviderQuality = %v, want %v", got.WProviderQuality, tt.wantProviderQuality)
			}
			// 其他权重不变
			if !floatEq(got.WQuality, base.WQuality) {
				t.Errorf("WQuality changed: %v -> %v", base.WQuality, got.WQuality)
			}
			if !floatEq(got.WStability, base.WStability) {
				t.Errorf("WStability changed: %v -> %v", base.WStability, got.WStability)
			}
		})
	}
}

// ── ValidateWeightInvariants 测试 ──

func TestValidateWeightInvariants(t *testing.T) {
	tests := []struct {
		name          string
		weights       ScoringWeights
		maxFamilyPref int
		wantErr       bool
	}{
		{
			name: "默认 supervisor 权重通过",
			weights: ScoringWeights{
				WQuality: 3, WStability: 2, WSpeed: 1, WCost: 0, WSavings: 0.5,
				WTierMatch: 1, WFamily: 0.2, WProviderQuality: 1.0, WDomain: 0.5,
			},
			maxFamilyPref: 5,
			wantErr:       false,
		},
		{
			name: "默认 embedding 权重通过（w_family=0 无约束）",
			weights: ScoringWeights{
				WQuality: 0, WStability: 2, WSpeed: 2, WCost: 3, WSavings: 3,
				WTierMatch: 1, WFamily: 0, WProviderQuality: 0, WDomain: 0,
			},
			maxFamilyPref: 5,
			wantErr:       false,
		},
		{
			name: "w_stability 太低违反不变量 1",
			weights: ScoringWeights{
				WStability: 0.1, WFamily: 0.5,
			},
			maxFamilyPref: 3,
			wantErr:       true,
		},
		{
			name: "w_provider_quality 过高违反不变量 2",
			weights: ScoringWeights{
				WStability: 0.5, WProviderQuality: 2.0,
			},
			maxFamilyPref: 1,
			wantErr:       true,
		},
		{
			name: "w_domain 过高违反不变量 3",
			weights: ScoringWeights{
				WStability: 0.5, WDomain: 2.0,
			},
			maxFamilyPref: 1,
			wantErr:       true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWeightInvariants(tt.weights, tt.maxFamilyPref)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateWeightInvariants() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateDefaultWeights(t *testing.T) {
	// 所有七类 TaskClass 的默认权重必须满足不变量
	if err := ValidateDefaultWeights(5); err != nil {
		t.Errorf("ValidateDefaultWeights(5) = %v, want nil", err)
	}
}

// ── ScoreCandidate 核心测试 ──

// §5.3 权重约束验证用例 1：stable 的非偏好派系胜过 unstable 的偏好派系
func TestScoreCandidate_StabilityBeatsFamilyPref(t *testing.T) {
	weights := DefaultTaskWeights()[TaskClassSupervisor]
	ctx := ScoringContext{
		TaskClass:   TaskClassSupervisor,
		TaskDomain:  TaskDomainGeneral,
		FamilyPrefs: []ModelFamily{ModelFamilyClaude, ModelFamilyOpenAI, ModelFamilyDeepSeek},
		Weights:     weights,
	}

	// stable + 非偏好派系（Qwen）
	stableNonPref := ScoringCandidate{
		ChannelUID:                "stable-qwen",
		QualityTier:               QualityTierPremium,
		StabilityTier:             StabilityTierStable,
		SpeedTier:                 SpeedTierNormal,
		CostTier:                  CostTierNormal,
		HealthState:               HealthStateHealthy,
		ProviderQualityScore:      0.8,
		ProviderQualityConfidence: 0.9,
		ModelFamily:               ModelFamilyQwen,
		SavingsScore:              0.5,
		DomainStrengthScore:       0.5,
	}

	// unstable + 偏好派系（Claude，排第一得 3 分）
	unstablePref := ScoringCandidate{
		ChannelUID:                "unstable-claude",
		QualityTier:               QualityTierPremium,
		StabilityTier:             StabilityTierUnstable,
		SpeedTier:                 SpeedTierNormal,
		CostTier:                  CostTierNormal,
		HealthState:               HealthStateHealthy,
		ProviderQualityScore:      0.95,
		ProviderQualityConfidence: 0.9,
		ModelFamily:               ModelFamilyClaude,
		SavingsScore:              0.5,
		DomainStrengthScore:       0.5,
	}

	resultStable := ScoreCandidate(stableNonPref, ctx)
	resultUnstable := ScoreCandidate(unstablePref, ctx)

	if resultStable.Score <= resultUnstable.Score {
		t.Errorf("stable+non-pref score(%v) should > unstable+pref score(%v)",
			resultStable.Score, resultUnstable.Score)
		t.Logf("stable detail: stability=%v, family=%v, pq=%v",
			resultStable.StabilityScore, resultStable.FamilyPrefScore, resultStable.ProviderQualityScore)
		t.Logf("unstable detail: stability=%v, family=%v, pq=%v",
			resultUnstable.StabilityScore, resultUnstable.FamilyPrefScore, resultUnstable.ProviderQualityScore)
	}
}

// §5.3 权重约束验证用例 2：stable 低供应商质量胜过 unstable 高质量
func TestScoreCandidate_StabilityBeatsProviderQuality(t *testing.T) {
	weights := DefaultTaskWeights()[TaskClassSupervisor]
	ctx := ScoringContext{
		TaskClass:   TaskClassSupervisor,
		TaskDomain:  TaskDomainGeneral,
		FamilyPrefs: []ModelFamily{},
		Weights:     weights,
	}

	// stable + 低供应商质量
	stableLowPQ := ScoringCandidate{
		ChannelUID:                "stable-low-pq",
		QualityTier:               QualityTierPremium,
		StabilityTier:             StabilityTierStable,
		SpeedTier:                 SpeedTierNormal,
		CostTier:                  CostTierNormal,
		HealthState:               HealthStateHealthy,
		ProviderQualityScore:      0.3,
		ProviderQualityConfidence: 0.9,
		ModelFamily:               ModelFamilyClaude,
		SavingsScore:              0.5,
		DomainStrengthScore:       0.5,
	}

	// unstable + 高供应商质量
	unstableHighPQ := ScoringCandidate{
		ChannelUID:                "unstable-high-pq",
		QualityTier:               QualityTierPremium,
		StabilityTier:             StabilityTierUnstable,
		SpeedTier:                 SpeedTierNormal,
		CostTier:                  CostTierNormal,
		HealthState:               HealthStateHealthy,
		ProviderQualityScore:      1.0,
		ProviderQualityConfidence: 0.9,
		ModelFamily:               ModelFamilyClaude,
		SavingsScore:              0.5,
		DomainStrengthScore:       0.5,
	}

	resultStable := ScoreCandidate(stableLowPQ, ctx)
	resultUnstable := ScoreCandidate(unstableHighPQ, ctx)

	if resultStable.Score <= resultUnstable.Score {
		t.Errorf("stable+low-pq score(%v) should > unstable+high-pq score(%v)",
			resultStable.Score, resultUnstable.Score)
	}
}

// ── §5.6.3 典型场景：官方/Bedrock/kiro 在三档价格偏向下的排序 ──

func TestScoreCandidate_CostPreferenceScenarios(t *testing.T) {
	// 场景基础设置（§5.6.3）：
	// 官方 Anthropic: $15/$75, providerQuality=0.95, stable
	// AWS Bedrock:   $15/$75, providerQuality=0.85, stable
	// kiro 中转:     0.4x 折扣, providerQuality=0.70, stable
	// savingsScore: 官方=0.3, Bedrock=0.3, kiro=0.9（越便宜越高）

	baseCandidate := func(uid string, pq, savings float64) ScoringCandidate {
		return ScoringCandidate{
			ChannelUID:                uid,
			QualityTier:               QualityTierPremium,
			StabilityTier:             StabilityTierStable,
			SpeedTier:                 SpeedTierNormal,
			CostTier:                  CostTierNormal,
			HealthState:               HealthStateHealthy,
			ProviderQualityScore:      pq,
			ProviderQualityConfidence: 0.9,
			ModelFamily:               ModelFamilyClaude,
			SavingsScore:              savings,
			DomainStrengthScore:       0.5,
		}
	}

	official := baseCandidate("official", 0.95, 0.3)
	bedrock := baseCandidate("bedrock", 0.85, 0.3)
	kiro := baseCandidate("kiro", 0.70, 0.9)

	tests := []struct {
		name      string
		mode      CostPreferenceMode
		taskClass TaskClass
		// expectedRank 为 uid 列表，表示期望的从高到低排序
		expectedRank []string
	}{
		{
			name:         "quality_first: 官方 > Bedrock > kiro",
			mode:         CostPrefQualityFirst,
			taskClass:    TaskClassSupervisor,
			expectedRank: []string{"official", "bedrock", "kiro"},
		},
		{
			name:      "balanced: kiro ≈ 官方 > Bedrock（kiro 靠 0.4x 价格 + 供应商质量差距追平官方）",
			mode:      CostPrefBalanced,
			taskClass: TaskClassSupervisor,
			// balanced 下，kiro(0.70+高savings) 与 official(0.95+低savings) 接近
			// kiro 的 savings 优势被 pq 劣势抵消，两者非常接近
			expectedRank: []string{"kiro"},
		},
		{
			name:         "cost_first: kiro > 官方 ≈ Bedrock",
			mode:         CostPrefCostFirst,
			taskClass:    TaskClassWorker,
			expectedRank: []string{"kiro"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseWeights := DefaultTaskWeights()[tt.taskClass]
			weights := ApplyCostPreference(baseWeights, tt.mode)
			ctx := ScoringContext{
				TaskClass:   tt.taskClass,
				TaskDomain:  TaskDomainGeneral,
				FamilyPrefs: nil,
				Weights:     weights,
			}

			resultOfficial := ScoreCandidate(official, ctx)
			resultBedrock := ScoreCandidate(bedrock, ctx)
			resultKiro := ScoreCandidate(kiro, ctx)

			results := []struct {
				uid   string
				score float64
			}{
				{"official", resultOfficial.Score},
				{"bedrock", resultBedrock.Score},
				{"kiro", resultKiro.Score},
			}

			// 验证首位
			if len(tt.expectedRank) >= 1 {
				// 找到最高分
				best := results[0]
				for _, r := range results[1:] {
					if r.score > best.score {
						best = r
					}
				}
				if best.uid != tt.expectedRank[0] {
					t.Errorf("expected best = %s (%.3f), got %s (%.3f); scores: official=%.3f, bedrock=%.3f, kiro=%.3f",
						tt.expectedRank[0], 0.0, best.uid, best.score,
						resultOfficial.Score, resultBedrock.Score, resultKiro.Score)
				}
			}

			// 完整排序日志
			t.Logf("scores: official=%.3f, bedrock=%.3f, kiro=%.3f",
				resultOfficial.Score, resultBedrock.Score, resultKiro.Score)
		})
	}
}

// ── §5.6.2 CapabilityFloor 不被 cost_first 突破 ──
// 设计要求：cost_first 也不能把 supervisor 降到不满足质量下界的模型。
// 本测试验证：low 质量档在 cost_first 下仍然比 premium 差（通过质量分差距体现）。
func TestScoreCandidate_CostFirstDoesNotBreakQualityFloor(t *testing.T) {
	baseWeights := DefaultTaskWeights()[TaskClassSupervisor]
	weights := ApplyCostPreference(baseWeights, CostPrefCostFirst)
	ctx := ScoringContext{
		TaskClass:   TaskClassSupervisor,
		TaskDomain:  TaskDomainGeneral,
		FamilyPrefs: nil,
		Weights:     weights,
	}

	// premium 级渠道（即使更贵，savings 更低）
	premiumCandidate := ScoringCandidate{
		ChannelUID:                "premium",
		QualityTier:               QualityTierPremium,
		StabilityTier:             StabilityTierStable,
		SpeedTier:                 SpeedTierNormal,
		CostTier:                  CostTierExpensive,
		HealthState:               HealthStateHealthy,
		ProviderQualityScore:      0.8,
		ProviderQualityConfidence: 0.9,
		ModelFamily:               ModelFamilyClaude,
		SavingsScore:              0.1, // 贵，省钱分低
		DomainStrengthScore:       0.5,
	}

	// low 级渠道（便宜但质量差）
	lowCandidate := ScoringCandidate{
		ChannelUID:                "low",
		QualityTier:               QualityTierLow,
		StabilityTier:             StabilityTierStable,
		SpeedTier:                 SpeedTierNormal,
		CostTier:                  CostTierCheap,
		HealthState:               HealthStateHealthy,
		ProviderQualityScore:      0.5,
		ProviderQualityConfidence: 0.9,
		ModelFamily:               ModelFamilyClaude,
		SavingsScore:              0.9, // 便宜，省钱分高
		DomainStrengthScore:       0.5,
	}

	resultPremium := ScoreCandidate(premiumCandidate, ctx)
	resultLow := ScoreCandidate(lowCandidate, ctx)

	// qualityScore 差距 = w_quality * (premium - low) = 3 * (4-1) = 9
	// savings 差距 = w_savings * cost_first_mul * (0.9-0.1) = 0.5 * 2.0 * 0.8 = 0.8
	// quality 差距远大于 savings 差距，premium 应该胜出
	if resultPremium.Score <= resultLow.Score {
		t.Errorf("premium score(%v) should > low score(%v) even under cost_first",
			resultPremium.Score, resultLow.Score)
		t.Logf("premium detail: quality=%.1f, savings=%.3f", resultPremium.QualityScore, resultPremium.SavingsScore)
		t.Logf("low detail: quality=%.1f, savings=%.3f", resultLow.QualityScore, resultLow.SavingsScore)
	}
}

// ── NormalizeSavingsScore 测试 ──

func TestNormalizeSavingsScore(t *testing.T) {
	t.Run("正常归一化", func(t *testing.T) {
		costs := map[string]float64{
			"cheap":     0.5,
			"normal":    2.0,
			"expensive": 5.0,
		}
		result := NormalizeSavingsScore(costs)

		// 最便宜得 1.0
		if !floatEq(result["cheap"], 1.0) {
			t.Errorf("cheap savings = %v, want 1.0", result["cheap"])
		}
		// 最贵得 0.0
		if !floatEq(result["expensive"], 0.0) {
			t.Errorf("expensive savings = %v, want 0.0", result["expensive"])
		}
		// 中间值：1.0 - (2.0-0.5)/(5.0-0.5) = 1.0 - 1.5/4.5 ≈ 0.667
		expected := 1.0 - (2.0-0.5)/(5.0-0.5)
		if !floatEq(result["normal"], expected) {
			t.Errorf("normal savings = %v, want %v", result["normal"], expected)
		}
	})

	t.Run("全部相同成本给 0.5", func(t *testing.T) {
		costs := map[string]float64{
			"a": 1.0,
			"b": 1.0,
		}
		result := NormalizeSavingsScore(costs)
		if !floatEq(result["a"], 0.5) {
			t.Errorf("a savings = %v, want 0.5", result["a"])
		}
		if !floatEq(result["b"], 0.5) {
			t.Errorf("b savings = %v, want 0.5", result["b"])
		}
	})

	t.Run("未知成本保持中性且不被当作免费", func(t *testing.T) {
		result := NormalizeSavingsScore(map[string]float64{
			"unknown": -1,
			"cheap":   1,
			"costly":  3,
		})
		if !floatEq(result["unknown"], 0.5) || !floatEq(result["cheap"], 1) || !floatEq(result["costly"], 0) {
			t.Fatalf("unexpected savings scores: %+v", result)
		}
	})

	t.Run("空输入返回 nil", func(t *testing.T) {
		result := NormalizeSavingsScore(nil)
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})
}

// ── BuildDomainStrengthScore 测试 ──

func TestBuildDomainStrengthScore(t *testing.T) {
	t.Run("nil profile 返回 0.5", func(t *testing.T) {
		got := BuildDomainStrengthScore(nil, TaskDomainAestheticsUI)
		if !floatEq(got, 0.5) {
			t.Errorf("BuildDomainStrengthScore(nil) = %v, want 0.5", got)
		}
	})

	t.Run("有覆盖值返回覆盖值", func(t *testing.T) {
		profile := &ModelProfile{
			ModelFamily: ModelFamilyClaude,
			ModelID:     "claude-fable-4",
			TaskDomainStrengths: map[TaskDomain]float64{
				TaskDomainAestheticsUI: 0.95,
			},
		}
		got := BuildDomainStrengthScore(profile, TaskDomainAestheticsUI)
		if !floatEq(got, 0.95) {
			t.Errorf("BuildDomainStrengthScore(overridden) = %v, want 0.95", got)
		}
	})

	t.Run("无覆盖回退到种子矩阵", func(t *testing.T) {
		profile := &ModelProfile{
			ModelFamily: ModelFamilyClaude,
			ModelID:     "claude-fable-4",
		}
		got := BuildDomainStrengthScore(profile, TaskDomainAestheticsUI)
		// 种子矩阵 claude/fable aesthetics_ui = 0.90
		if !floatEq(got, 0.90) {
			t.Errorf("BuildDomainStrengthScore(seed) = %v, want 0.90", got)
		}
	})
}

// ── ProviderQuality 置信度门槛测试 ──

func TestScoreCandidate_ProviderQualityConfidenceThreshold(t *testing.T) {
	weights := DefaultTaskWeights()[TaskClassSupervisor]
	ctx := ScoringContext{
		TaskClass:  TaskClassSupervisor,
		TaskDomain: TaskDomainGeneral,
		Weights:    weights,
	}

	// 高置信度：providerQualityScore 参与评分
	highConf := ScoringCandidate{
		ChannelUID:                "high-conf",
		QualityTier:               QualityTierPremium,
		StabilityTier:             StabilityTierStable,
		SpeedTier:                 SpeedTierNormal,
		CostTier:                  CostTierNormal,
		HealthState:               HealthStateHealthy,
		ProviderQualityScore:      1.0,
		ProviderQualityConfidence: 0.9,
		ModelFamily:               ModelFamilyClaude,
		SavingsScore:              0.5,
		DomainStrengthScore:       0.5,
	}

	// 低置信度：providerQualityScore 应被清零
	lowConf := highConf
	lowConf.ChannelUID = "low-conf"
	lowConf.ProviderQualityConfidence = 0.3

	resultHigh := ScoreCandidate(highConf, ctx)
	resultLow := ScoreCandidate(lowConf, ctx)

	// 高置信度应该更高（providerQuality 项有贡献）
	if resultHigh.Score <= resultLow.Score {
		t.Errorf("high-confidence score(%v) should > low-confidence score(%v)",
			resultHigh.Score, resultLow.Score)
	}

	// 低置信度时 providerQualityScore 在结果中应为 0
	if resultLow.ProviderQualityScore != 0 {
		t.Errorf("low-confidence ProviderQualityScore = %v, want 0", resultLow.ProviderQualityScore)
	}
}

func TestScoreCandidate_QualityTracksTaskTarget(t *testing.T) {
	weights := ScoringWeights{WQuality: 1}
	low := ScoringCandidate{ChannelUID: "low", QualityTier: QualityTierLow}
	premium := ScoringCandidate{
		ChannelUID: "premium", QualityTier: QualityTierPremium,
		QualityBenchmarkKnown: true, QualityBenchmarkScore: 100,
	}

	lightweight := ScoringContext{
		Weights:           weights,
		TargetQualityTier: QualityTierLow,
		QualityBenefitCap: QualityTierLow,
	}
	if gotLow, gotPremium := ScoreCandidate(low, lightweight).Score, ScoreCandidate(premium, lightweight).Score; gotLow != gotPremium {
		t.Fatalf("quality and benchmark benefit above the low cap should be saturated, low=%v premium=%v", gotLow, gotPremium)
	}

	complex := ScoringContext{Weights: weights, TargetQualityTier: QualityTierPremium}
	if ScoreCandidate(premium, complex).Score <= ScoreCandidate(low, complex).Score {
		t.Fatal("premium target should prefer premium tier over low tier")
	}
}

func TestScoreCandidate_HigherQualityIsNotPenalizedAboveFloor(t *testing.T) {
	weights := ScoringWeights{WQuality: 1}
	ctx := ScoringContext{Weights: weights, TargetQualityTier: QualityTierHigh}
	high := ScoreCandidate(ScoringCandidate{QualityTier: QualityTierHigh}, ctx)
	premium := ScoreCandidate(ScoringCandidate{QualityTier: QualityTierPremium}, ctx)
	if premium.QualityScore <= high.QualityScore {
		t.Fatalf("premium quality should not be penalized above high floor: high=%v premium=%v", high.QualityScore, premium.QualityScore)
	}
}

func TestScoreCandidate_EffortQualityBreaksSameTierTie(t *testing.T) {
	ctx := ScoringContext{Weights: ScoringWeights{WQuality: 1}}
	base := ScoringCandidate{QualityTier: QualityTierHigh, EffortQualityConfidence: 1}
	lowEffort := base
	lowEffort.EffortQualityScore = 60
	highEffort := base
	highEffort.EffortQualityScore = 80
	low := ScoreCandidate(lowEffort, ctx)
	high := ScoreCandidate(highEffort, ctx)
	if high.Score <= low.Score {
		t.Fatalf("同质量档的连续 effort 分应参与排序: high=%v low=%v", high.Score, low.Score)
	}
	if high.Score-low.Score >= 1 {
		t.Fatalf("连续 tie-break 不得跨越质量档间距: delta=%v", high.Score-low.Score)
	}
	if high.QualityScore <= low.QualityScore {
		t.Fatalf("质量分明细应包含连续 effort 加成: high=%v low=%v", high.QualityScore, low.QualityScore)
	}
}

// ── Penalty 测试 ──

func TestScoreCandidate_Penalty(t *testing.T) {
	weights := DefaultTaskWeights()[TaskClassSupervisor]
	ctx := ScoringContext{
		TaskClass:  TaskClassSupervisor,
		TaskDomain: TaskDomainGeneral,
		Weights:    weights,
	}

	base := ScoringCandidate{
		ChannelUID:                "test",
		QualityTier:               QualityTierPremium,
		StabilityTier:             StabilityTierStable,
		SpeedTier:                 SpeedTierNormal,
		CostTier:                  CostTierNormal,
		HealthState:               HealthStateHealthy,
		ProviderQualityScore:      0.8,
		ProviderQualityConfidence: 0.9,
		ModelFamily:               ModelFamilyClaude,
		SavingsScore:              0.5,
		DomainStrengthScore:       0.5,
	}

	healthy := ScoreCandidate(base, ctx)
	tests := []struct {
		name        string
		healthState HealthState
		wantPenalty float64
	}{
		{name: "unknown", healthState: HealthStateUnknown, wantPenalty: 0},
		{name: "degraded", healthState: HealthStateDegraded, wantPenalty: 5},
		{name: "limited", healthState: HealthStateLimited, wantPenalty: 20},
		{name: "misconfigured", healthState: HealthStateMisconfigured, wantPenalty: 100},
		{name: "dead", healthState: HealthStateDead, wantPenalty: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := base
			candidate.ChannelUID = tt.name
			candidate.HealthState = tt.healthState
			result := ScoreCandidate(candidate, ctx)
			if !floatEq(result.Penalty, tt.wantPenalty) {
				t.Fatalf("Penalty = %v, want %v", result.Penalty, tt.wantPenalty)
			}
			if !floatEq(healthy.Score-result.Score, tt.wantPenalty) {
				t.Fatalf("score diff = %v, want %v", healthy.Score-result.Score, tt.wantPenalty)
			}
		})
	}
}

// ── 辅助函数 ──

func floatEq(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}
