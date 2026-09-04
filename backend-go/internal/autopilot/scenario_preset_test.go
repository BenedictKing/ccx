package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func scenarioBoolPtr(v bool) *bool { return &v }

func TestResolveScenarioPresetPriority(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.ScenarioRoutingConfig
		header  string
		wantHit bool
		wantKey string
	}{
		{
			name:    "零值配置无头不命中",
			cfg:     config.ScenarioRoutingConfig{},
			wantHit: false,
		},
		{
			name:    "全局模式命中",
			cfg:     config.ScenarioRoutingConfig{Mode: "daily_dev"},
			wantHit: true, wantKey: "daily_dev",
		},
		{
			name:    "头覆盖全局",
			cfg:     config.ScenarioRoutingConfig{Mode: "daily_dev"},
			header:  "hard_problem",
			wantHit: true, wantKey: "hard_problem",
		},
		{
			name:    "头显式 auto 取消全局场景",
			cfg:     config.ScenarioRoutingConfig{Mode: "daily_dev"},
			header:  "auto",
			wantHit: false,
		},
		{
			name:    "头覆盖被配置禁用",
			cfg:     config.ScenarioRoutingConfig{Mode: "daily_dev", HeaderOverrideEnabled: scenarioBoolPtr(false)},
			header:  "hard_problem",
			wantHit: true, wantKey: "daily_dev",
		},
		{
			name:    "头覆盖默认允许（nil 指针）",
			cfg:     config.ScenarioRoutingConfig{Mode: "auto"},
			header:  "batch_cheap",
			wantHit: true, wantKey: "batch_cheap",
		},
		{
			name:    "非法头忽略沿用全局",
			cfg:     config.ScenarioRoutingConfig{Mode: "background"},
			header:  "not-a-scenario",
			wantHit: true, wantKey: "background",
		},
		{
			name:    "非法全局模式不命中",
			cfg:     config.ScenarioRoutingConfig{Mode: "yolo"},
			wantHit: false,
		},
		{
			name: "配置覆盖应用后命中",
			cfg: config.ScenarioRoutingConfig{
				Mode: "batch_cheap",
				Overrides: map[string]config.ScenarioPresetOverride{
					"batch_cheap": {MinQualityTier: "high"},
				},
			},
			wantHit: true, wantKey: "batch_cheap",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preset, hit := ResolveScenarioPreset(tt.cfg, tt.header)
			if hit != tt.wantHit {
				t.Fatalf("hit = %v, want %v", hit, tt.wantHit)
			}
			if hit && preset.Key != tt.wantKey {
				t.Fatalf("key = %q, want %q", preset.Key, tt.wantKey)
			}
			if tt.name == "配置覆盖应用后命中" && preset.MinQualityTier != QualityTierHigh {
				t.Fatalf("override 未生效: MinQualityTier = %v", preset.MinQualityTier)
			}
		})
	}
}

func TestBuiltinScenarioPresetParams(t *testing.T) {
	presets := BuiltinScenarioPresets(config.ScenarioRoutingConfig{})
	expectations := map[string]struct {
		minTier QualityTier
		cost    string
		floor   EffortLevel
		ceil    EffortLevel
		cap     QualityTier
		hasCap  bool
	}{
		"daily_dev":    {QualityTierNormal, "balanced", EffortMedium, "", QualityTierHigh, true},
		"hard_problem": {QualityTierHigh, "quality_first", EffortHigh, "", "", false},
		"background":   {QualityTierNormal, "cost_first", EffortLow, "", QualityTierHigh, true},
		"batch_cheap":  {QualityTierNormal, "cost_first", EffortLow, EffortMedium, QualityTierHigh, true},
	}
	for key, want := range expectations {
		got, ok := presets[key]
		if !ok {
			t.Fatalf("内置预设缺失: %s", key)
		}
		if got.MinQualityTier != want.minTier || got.CostPreference != want.cost ||
			got.EffortFloor != want.floor || got.EffortCeil != want.ceil ||
			got.HasBenefitCap != want.hasCap || got.QualityBenefitCap != want.cap {
			t.Fatalf("%s 参数不符: %+v", key, got)
		}
	}
}

func TestScenarioPresetEffortWithin(t *testing.T) {
	preset := ScenarioPreset{EffortFloor: EffortLow, EffortCeil: EffortMedium}
	if !preset.EffortWithin(EffortLow) || !preset.EffortWithin(EffortMedium) {
		t.Fatal("区间端点应包含在内")
	}
	if preset.EffortWithin(EffortHigh) || preset.EffortWithin(EffortOff) {
		t.Fatal("超出区间的档位应被排除")
	}
	open := ScenarioPreset{EffortFloor: EffortHigh}
	if !open.EffortWithin(EffortUltra) {
		t.Fatal("无上限时高端档应通过")
	}
	if open.EffortWithin(EffortMedium) {
		t.Fatal("低于下限应被排除")
	}
}

func TestApplyScenarioOverride(t *testing.T) {
	preset := builtinScenarioPresets["daily_dev"]
	applyScenarioOverride(&preset, config.ScenarioPresetOverride{
		MinQualityTier:    "high",
		CostPreference:    "cost_first",
		EffortCeil:        "xhigh",
		QualityBenefitCap: "none",
	})
	if preset.MinQualityTier != QualityTierHigh || preset.CostPreference != "cost_first" || preset.EffortCeil != EffortXhigh {
		t.Fatalf("覆盖未生效: %+v", preset)
	}
	if preset.HasBenefitCap {
		t.Fatal("none 应清除收益帽")
	}
	// 非法值沿用内置默认
	preset2 := builtinScenarioPresets["daily_dev"]
	applyScenarioOverride(&preset2, config.ScenarioPresetOverride{MinQualityTier: "bogus", EffortFloor: "nope"})
	if preset2.MinQualityTier != QualityTierNormal || preset2.EffortFloor != EffortMedium {
		t.Fatalf("非法覆盖不应改变内置值: %+v", preset2)
	}
}

func TestBuildRequestProfileScenarioOverride(t *testing.T) {
	// gpt-5.6-luna 的模型级常规档是 low：场景 daily_dev(normal) 命中时
	// QualityTarget 应直接取预设值，跳过 QualityNeed=low 的天花板钳制。
	base := RequestProfileFeatures{
		Model:       "gpt-5.6-luna",
		ChannelKind: "messages",
		AgentRole:   "main",
		Operation:   "completion",
	}

	scenarioCfg := config.ScenarioRoutingConfig{Mode: "daily_dev"}
	profile := BuildRequestProfile(func() RequestProfileFeatures {
		f := base
		f.ScenarioCfg = scenarioCfg
		return f
	}())
	if profile.ScenarioPreset == nil {
		t.Fatal("场景未命中")
	}
	if profile.QualityTarget != QualityTierNormal {
		t.Fatalf("QualityTarget = %v, want normal（场景覆盖）", profile.QualityTarget)
	}
	if qualityTierRank(profile.QualityTarget) <= qualityTierRank(profile.QualityNeed) {
		t.Fatalf("场景应跳过 QualityNeed 钳制: need=%v target=%v", profile.QualityNeed, profile.QualityTarget)
	}
	floor := BuildCapabilityFloorFromRequestProfile(&profile)
	if floor.EffortFloor != EffortMedium || floor.EffortCeil != "" {
		t.Fatalf("effort 区间未注入: floor=%v ceil=%v", floor.EffortFloor, floor.EffortCeil)
	}
	if floor.CostPreferenceOverride != "balanced" {
		t.Fatalf("场景默认价格偏好未注入: %v", floor.CostPreferenceOverride)
	}
	if floor.QualityBenefitCap != QualityTierHigh {
		t.Fatalf("场景收益帽未注入: %v", floor.QualityBenefitCap)
	}

	// 请求头声明 hard_problem + 合法价格偏好覆盖
	profile2 := BuildRequestProfile(func() RequestProfileFeatures {
		f := base
		f.ScenarioCfg = scenarioCfg
		f.RoutingScenarioHeader = "hard_problem"
		f.CostPreferenceHeader = "cost_first"
		return f
	}())
	if profile2.ScenarioPreset == nil || profile2.ScenarioPreset.Key != "hard_problem" {
		t.Fatalf("头应覆盖全局场景: %+v", profile2.ScenarioPreset)
	}
	if profile2.QualityTarget != QualityTierHigh {
		t.Fatalf("QualityTarget = %v, want high", profile2.QualityTarget)
	}
	if profile2.CostPreferenceOverride != "cost_first" {
		t.Fatalf("价格偏好头未生效: %v", profile2.CostPreferenceOverride)
	}

	// 非法价格偏好头表现为未声明（沿用场景默认），不得静默变成 balanced
	profile3 := BuildRequestProfile(func() RequestProfileFeatures {
		f := base
		f.ScenarioCfg = config.ScenarioRoutingConfig{Mode: "hard_problem"}
		f.CostPreferenceHeader = "garbage"
		return f
	}())
	if profile3.CostPreferenceOverride != "" {
		t.Fatalf("非法头应被忽略: %v", profile3.CostPreferenceOverride)
	}
	floor3 := BuildCapabilityFloorFromRequestProfile(&profile3)
	if floor3.CostPreferenceOverride != "quality_first" {
		t.Fatalf("应回落到场景默认: %v", floor3.CostPreferenceOverride)
	}

	// auto：行为与现状一致，不命中场景
	profile4 := BuildRequestProfile(func() RequestProfileFeatures {
		f := base
		f.ScenarioCfg = config.ScenarioRoutingConfig{Mode: "auto"}
		return f
	}())
	if profile4.ScenarioPreset != nil {
		t.Fatal("auto 不应命中场景")
	}
}

func TestRequestQualityBenefitCapScenario(t *testing.T) {
	// hard_problem 无帽：即使任务是 routine 也不设帽（显式意图优先）
	preset := builtinScenarioPresets["hard_problem"]
	profile := RequestProfile{
		Complexity:     TaskComplexityRoutine,
		ScenarioPreset: &preset,
	}
	if got := requestQualityBenefitCap(&profile); got != "" {
		t.Fatalf("hard_problem 场景不应设帽: %v", got)
	}
	// daily_dev 有帽：即使任务复杂度未知也按场景帽
	presetDaily := builtinScenarioPresets["daily_dev"]
	profile2 := RequestProfile{ScenarioPreset: &presetDaily}
	if got := requestQualityBenefitCap(&profile2); got != QualityTierHigh {
		t.Fatalf("daily_dev 场景帽 = %v, want high", got)
	}
}

func TestEffortLevelQualityAdmission(t *testing.T) {
	// gpt-5.6-luna 模型级常规档为 low，但非常规档（max）直测显著更高：
	// 凭 effort 级实测应通过与「该模型最佳非常规档直测分」匹配的档位下限。
	// 档位下限随基准分布漂移，这里独立复算：admission(tier) 必须等价于
	// 最佳非常规档直测分 ≥ tier 下限（图上「Luna max 做日常开发」的复现路径）。
	lunaBest := 0.0
	benchmark := config.ResolveModelBenchmarkProfile("gpt-5.6-luna")
	if !benchmark.Known {
		t.Fatal("gpt-5.6-luna missing from builtin benchmark profiles")
	}
	for _, ev := range benchmark.Profile.BenchmarkEvidence {
		if ev.Domain != "coding" || ev.Metric != "pass_at_1" {
			continue
		}
		if ev.Benchmark != "deepswe" && ev.Benchmark != "codexradar" {
			continue
		}
		if isSmallSampleEvidence(ev) {
			continue
		}
		effort := NormalizeEffortLevel(ev.Effort)
		if effort == "" || effort == EffortOff ||
			EffortLevelOrdinal(effort) <= EffortLevelOrdinal(EffortMedium) {
			continue
		}
		if score := ev.RawValue * 100; score > lunaBest {
			lunaBest = score
		}
	}
	if lunaBest <= 0 {
		t.Fatal("gpt-5.6-luna has no usable above-medium evidence")
	}
	premiumMin, highMin, normalMin := computeQualityTierBoundaries()
	for _, tt := range []struct {
		tier   QualityTier
		cutoff float64
	}{
		{QualityTierNormal, normalMin},
		{QualityTierHigh, highMin},
		{QualityTierPremium, premiumMin},
	} {
		want := lunaBest >= tt.cutoff
		if got := effortLevelQualityAdmission("gpt-5.6-luna", tt.tier); got != want {
			t.Fatalf("luna admission(%v) = %v, want %v (best=%.1f cutoff=%.2f)",
				tt.tier, got, want, lunaBest, tt.cutoff)
		}
	}
	// 未知模型不豁免：保持模型级过滤语义
	if effortLevelQualityAdmission("totally-unknown-model", QualityTierNormal) {
		t.Fatal("未知模型不应豁免")
	}
	// low 下限恒通过
	if !effortLevelQualityAdmission("totally-unknown-model", QualityTierLow) {
		t.Fatal("low 下限应恒通过")
	}
}

func TestFilterByCapabilityFloorEffortAdmission(t *testing.T) {
	// 低常规档模型（QualityTier=low）+ normal 下限：凭 effort 级证据应放行
	profiles := []ModelProfile{
		{ModelID: "gpt-5.6-luna", QualityTier: QualityTierLow, ProbeSuccess: true, ContextTokens: 200_000},
		{ModelID: "unknown-model", QualityTier: QualityTierLow, ProbeSuccess: true, ContextTokens: 200_000},
	}
	floor := CapabilityFloor{MinQualityTier: QualityTierNormal}
	eligible := filterByCapabilityFloor(profiles, floor)
	if len(eligible) != 1 || eligible[0].ModelID != "gpt-5.6-luna" {
		t.Fatalf("应仅放行有 effort 级实测证据的模型: %+v", eligible)
	}
	// 无豁免时质量降档兜底路径保持可用
	eligible2, fallback := filterByCapabilityFloorWithQualityFallback([]ModelProfile{
		{ModelID: "unknown-model", QualityTier: QualityTierLow, ProbeSuccess: true, ContextTokens: 200_000},
	}, floor)
	if len(eligible2) != 1 || !fallback {
		t.Fatalf("全被过滤时应走质量降档兜底: %+v fallback=%v", eligible2, fallback)
	}
}

func TestFilterEffortFloorCeil(t *testing.T) {
	candidates := []rankedModelCandidate{
		{effort: EffortLow, effortDecided: true},
		{effort: EffortMedium, effortDecided: true},
		{effort: EffortHigh, effortDecided: true},
		{effort: "", effortDecided: false}, // 未决定 effort 的候选不受约束
	}
	// 仅上限 batch_cheap 语义：low..medium
	filtered := filterEffortFloor(candidates, CapabilityFloor{EffortFloor: EffortLow, EffortCeil: EffortMedium})
	if len(filtered) != 3 {
		t.Fatalf("low..medium 应保留 2 个已决定候选 + 1 个未决定: %+v", filtered)
	}
	for _, c := range filtered {
		if c.effortDecided && (c.effort == EffortHigh) {
			t.Fatal("high 档应被上限排除")
		}
	}
	// 全部被过滤时 fail-open
	filteredAll := filterEffortFloor([]rankedModelCandidate{
		{effort: EffortUltra, effortDecided: true},
	}, CapabilityFloor{EffortFloor: EffortLow, EffortCeil: EffortMedium})
	if len(filteredAll) != 1 {
		t.Fatal("区间全滤时应 fail-open 保留原始候选")
	}
}
