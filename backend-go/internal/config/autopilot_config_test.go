package config

import (
	"math"
	"os"
	"testing"
	"time"
)

func TestDefaultAutopilotRoutingConfig(t *testing.T) {
	cfg := DefaultAutopilotRoutingConfig()

	if cfg.RoutingMode != AutopilotModeAuto {
		t.Errorf("默认 RoutingMode = %q, 期望 %q", cfg.RoutingMode, AutopilotModeAuto)
	}
	if cfg.KillSwitch {
		t.Error("默认 KillSwitch 应为 false")
	}
	if cfg.CostPreference.Mode != "balanced" {
		t.Errorf("默认 CostPreference.Mode = %q, 期望 %q", cfg.CostPreference.Mode, "balanced")
	}
	if !cfg.ModelFamilyPreference.Enabled {
		t.Error("默认 ModelFamilyPreference.Enabled 应为 true")
	}
	if len(cfg.ModelFamilyPreference.GlobalOrder) == 0 {
		t.Error("默认 ModelFamilyPreference.GlobalOrder 不应为空")
	}
	if cfg.ModelFamilyPreference.Weight != 0.2 {
		t.Errorf("默认 ModelFamilyPreference.Weight = %f, 期望 0.2", cfg.ModelFamilyPreference.Weight)
	}
}

func TestDeepSeekProviderTimePricingSchedule(t *testing.T) {
	cost := DefaultAutopilotRoutingConfig().CostOptimization
	tests := []struct {
		name string
		at   string
		want float64
	}{
		{name: "生效前高峰不加价", at: "2026-07-19T10:00:00+08:00", want: 1},
		{name: "上午高峰", at: "2026-07-20T09:00:00+08:00", want: 2},
		{name: "午间平峰", at: "2026-07-20T12:00:00+08:00", want: 1},
		{name: "下午高峰", at: "2026-07-20T17:59:00+08:00", want: 2},
		{name: "晚间平峰", at: "2026-07-20T18:00:00+08:00", want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339, tt.at)
			if err != nil {
				t.Fatal(err)
			}
			if got := cost.ProviderTimePricingMultiplier("deepseek", at); got != tt.want {
				t.Fatalf("multiplier = %v, want %v", got, tt.want)
			}
		})
	}
	if got := cost.ProviderTimePricingMultiplier("other-provider", time.Now()); got != 1 {
		t.Fatalf("未配置 provider multiplier = %v, want 1", got)
	}
}

func TestAutopilotRoutingConfig_Validate_ModeNormalization(t *testing.T) {
	for _, input := range []string{"", "off", "shadow", "assist", "auto", "active", "invalid"} {
		t.Run(input, func(t *testing.T) {
			cfg := AutopilotRoutingConfig{RoutingMode: input}
			cfg.Validate()
			if cfg.RoutingMode != AutopilotModeAuto {
				t.Errorf("输入 %q 后 RoutingMode = %q, 期望 %q", input, cfg.RoutingMode, AutopilotModeAuto)
			}
		})
	}
}

func TestAutopilotRoutingConfig_KillSwitchOverridesMode(t *testing.T) {
	for _, killSwitch := range []bool{false, true} {
		cfg := AutopilotRoutingConfig{KillSwitch: killSwitch, RoutingMode: "shadow"}
		cfg.Validate()

		expectedMode := AutopilotModeAuto
		if killSwitch {
			expectedMode = "off"
		}
		if effective := cfg.EffectiveRoutingMode(); effective != expectedMode {
			t.Errorf("KillSwitch=%v 时 EffectiveRoutingMode = %q, 期望 %q", killSwitch, effective, expectedMode)
		}
		if active := cfg.IsAutopilotActive(); active != !killSwitch {
			t.Errorf("KillSwitch=%v 时 IsAutopilotActive = %v, 期望 %v", killSwitch, active, !killSwitch)
		}
	}
}

func TestAutopilotRoutingConfig_EnvKillSwitch(t *testing.T) {
	tests := []struct {
		name     string
		envVal   string
		expected bool
	}{
		{"true 字符串", "true", true},
		{"1 字符串", "1", true},
		{"yes 字符串", "yes", true},
		{"on 字符串", "on", true},
		{"TRUE 大写", "TRUE", true},
		{"Yes 混合大小写", "Yes", true},
		{"false 字符串", "false", false},
		{"0 字符串", "0", false},
		{"空字符串", "", false},
		{"random", "random", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 清理环境变量
			_ = os.Unsetenv(autopilotKillSwitchEnv)
			defer func() {
				_ = os.Unsetenv(autopilotKillSwitchEnv)
			}()

			if tt.envVal != "" {
				_ = os.Setenv(autopilotKillSwitchEnv, tt.envVal)
			}

			cfg := AutopilotRoutingConfig{
				KillSwitch:  false,
				RoutingMode: AutopilotModeAuto,
			}

			// 应用环境变量覆盖
			applyAutopilotEnvOverrides(&cfg)

			if cfg.KillSwitch != tt.expected {
				t.Errorf("环境变量 %q=%q → KillSwitch = %v, 期望 %v",
					autopilotKillSwitchEnv, tt.envVal, cfg.KillSwitch, tt.expected)
			}

			// 验证 KillSwitch 生效后模式回退到 off
			if tt.expected {
				cfg.Validate()
				effective := cfg.EffectiveRoutingMode()
				if effective != "off" {
					t.Errorf("KillSwitch=true 时 EffectiveRoutingMode = %q, 期望 %q",
						effective, "off")
				}
			}
		})
	}
}

func TestCostPreferenceConfig_Validate_Normalization(t *testing.T) {
	tests := []struct {
		name            string
		inputMode       string
		expectedMode    string
		expectedSavings float64
		expectedQuality float64
	}{
		{"quality_first", "quality_first", "quality_first", 0.3, 1.5},
		{"balanced", "balanced", "balanced", 1.0, 1.0},
		{"cost_first", "cost_first", "cost_first", 2.0, 0.5},
		{"custom 保留自定义值", "custom", "custom", 1.0, 1.0},
		{"QUALITY_FIRST 大写", "QUALITY_FIRST", "quality_first", 0.3, 1.5},
		{"空字符串回退 balanced", "", "balanced", 1.0, 1.0},
		{"非法值回退 balanced", "invalid", "balanced", 1.0, 1.0},
		{"带空格", "  cost_first  ", "cost_first", 2.0, 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := AutopilotRoutingConfig{
				CostPreference: CostPreferenceConfig{
					Mode: tt.inputMode,
					Custom: CostPreferenceCustom{
						SavingsMultiplier:         1.0,
						ProviderQualityMultiplier: 1.0,
					},
				},
			}
			cfg.Validate()

			if cfg.CostPreference.Mode != tt.expectedMode {
				t.Errorf("Mode = %q, 期望 %q", cfg.CostPreference.Mode, tt.expectedMode)
			}
			if cfg.CostPreference.Custom.SavingsMultiplier != tt.expectedSavings {
				t.Errorf("SavingsMultiplier = %f, 期望 %f",
					cfg.CostPreference.Custom.SavingsMultiplier, tt.expectedSavings)
			}
			if cfg.CostPreference.Custom.ProviderQualityMultiplier != tt.expectedQuality {
				t.Errorf("ProviderQualityMultiplier = %f, 期望 %f",
					cfg.CostPreference.Custom.ProviderQualityMultiplier, tt.expectedQuality)
			}
		})
	}
}

func TestCostPreferenceConfig_CustomClamping(t *testing.T) {
	tests := []struct {
		name            string
		savings         float64
		quality         float64
		expectedSavings float64
		expectedQuality float64
	}{
		{"正常范围", 1.5, 1.5, 1.5, 1.5},
		{"低于下界钳制到 0", -0.5, -0.5, 0, 0},
		{"高于上界钳制到 3.0", 5.0, 5.0, 3.0, 3.0},
		{"零值保留", 0, 0, 0, 0},
		{"边界值 3.0", 3.0, 3.0, 3.0, 3.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := AutopilotRoutingConfig{
				CostPreference: CostPreferenceConfig{
					Mode: "custom",
					Custom: CostPreferenceCustom{
						SavingsMultiplier:         tt.savings,
						ProviderQualityMultiplier: tt.quality,
					},
				},
			}
			cfg.Validate()

			if cfg.CostPreference.Custom.SavingsMultiplier != tt.expectedSavings {
				t.Errorf("SavingsMultiplier = %f, 期望 %f",
					cfg.CostPreference.Custom.SavingsMultiplier, tt.expectedSavings)
			}
			if cfg.CostPreference.Custom.ProviderQualityMultiplier != tt.expectedQuality {
				t.Errorf("ProviderQualityMultiplier = %f, 期望 %f",
					cfg.CostPreference.Custom.ProviderQualityMultiplier, tt.expectedQuality)
			}
		})
	}
}

func TestCostPreferenceConfig_PerTaskClassNormalization(t *testing.T) {
	cfg := AutopilotRoutingConfig{
		CostPreference: CostPreferenceConfig{
			Mode: "balanced",
			PerTaskClass: map[string]string{
				"supervisor":  "QUALITY_FIRST",
				"worker":      "COST_FIRST",
				"lightweight": "invalid",
			},
			Custom: CostPreferenceCustom{
				SavingsMultiplier:         1.0,
				ProviderQualityMultiplier: 1.0,
			},
		},
	}
	cfg.Validate()

	if cfg.CostPreference.PerTaskClass["supervisor"] != "quality_first" {
		t.Errorf("supervisor = %q, 期望 %q",
			cfg.CostPreference.PerTaskClass["supervisor"], "quality_first")
	}
	if cfg.CostPreference.PerTaskClass["worker"] != "cost_first" {
		t.Errorf("worker = %q, 期望 %q",
			cfg.CostPreference.PerTaskClass["worker"], "cost_first")
	}
	// 非法值回退到 balanced
	if cfg.CostPreference.PerTaskClass["lightweight"] != "balanced" {
		t.Errorf("lightweight = %q, 期望 %q",
			cfg.CostPreference.PerTaskClass["lightweight"], "balanced")
	}
}

func TestCostPreferenceConfig_GetEffectiveMultipliers(t *testing.T) {
	cfg := CostPreferenceConfig{
		Mode: "balanced",
		PerTaskClass: map[string]string{
			"supervisor": "quality_first",
			"worker":     "cost_first",
		},
		Custom: CostPreferenceCustom{
			SavingsMultiplier:         1.5,
			ProviderQualityMultiplier: 1.2,
		},
	}

	tests := []struct {
		taskClass       string
		expectedSavings float64
		expectedQuality float64
	}{
		{"", 1.0, 1.0},              // 空 TaskClass → 全局 balanced
		{"supervisor", 0.3, 1.5},    // PerTaskClass 覆盖
		{"worker", 2.0, 0.5},        // PerTaskClass 覆盖
		{"lightweight", 1.0, 1.0},   // 未覆盖 → 全局 balanced
		{"unknown_class", 1.0, 1.0}, // 未知 → 全局 balanced
	}

	for _, tt := range tests {
		t.Run("taskClass="+tt.taskClass, func(t *testing.T) {
			s, q := cfg.GetEffectiveMultipliers(tt.taskClass)
			if s != tt.expectedSavings {
				t.Errorf("savings = %f, 期望 %f", s, tt.expectedSavings)
			}
			if q != tt.expectedQuality {
				t.Errorf("quality = %f, 期望 %f", q, tt.expectedQuality)
			}
		})
	}
}

func TestModelFamilyPreferenceConfig_Validate(t *testing.T) {
	t.Run("空 GlobalOrder 保留", func(t *testing.T) {
		cfg := AutopilotRoutingConfig{
			ModelFamilyPreference: ModelFamilyPreferenceConfig{
				Enabled:     true,
				Weight:      0.2,
				GlobalOrder: nil,
			},
		}
		cfg.Validate()
		if cfg.ModelFamilyPreference.GlobalOrder != nil {
			t.Error("nil GlobalOrder 经 validate 后应仍为 nil")
		}
	})

	t.Run("空值条目被移除", func(t *testing.T) {
		cfg := AutopilotRoutingConfig{
			ModelFamilyPreference: ModelFamilyPreferenceConfig{
				Enabled: true,
				Weight:  0.2,
				GlobalOrder: []string{
					"claude", "", "openai", "  ", "gemini",
				},
				PerTaskClass: map[string][]string{
					"supervisor": {"claude", "", "openai"},
					"empty":      {"", "  "},
				},
			},
		}
		cfg.Validate()

		order := cfg.ModelFamilyPreference.GlobalOrder
		if len(order) != 3 {
			t.Errorf("GlobalOrder 长度 = %d, 期望 3 (含空值移除后)", len(order))
		}
		if len(order) >= 1 && order[0] != "claude" {
			t.Errorf("GlobalOrder[0] = %q, 期望 %q", order[0], "claude")
		}

		// 空 TaskClass 条目被删除
		if _, exists := cfg.ModelFamilyPreference.PerTaskClass["empty"]; exists {
			t.Error("全空条目 'empty' 应被删除")
		}
		// supervisor 空值被移除
		supervisor := cfg.ModelFamilyPreference.PerTaskClass["supervisor"]
		if len(supervisor) != 2 {
			t.Errorf("supervisor 长度 = %d, 期望 2", len(supervisor))
		}
	})

	t.Run("权重钳制", func(t *testing.T) {
		tests := []struct {
			name     string
			input    float64
			expected float64
		}{
			{"正常范围 0.2", 0.2, 0.2},
			{"下界钳制", -0.5, 0},
			{"上界钳制", 1.5, 1.0},
			{"零值保留", 0, 0},
			{"边界值 1.0", 1.0, 1.0},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				cfg := AutopilotRoutingConfig{
					ModelFamilyPreference: ModelFamilyPreferenceConfig{
						Weight: tt.input,
					},
				}
				cfg.Validate()
				if cfg.ModelFamilyPreference.Weight != tt.expected {
					t.Errorf("Weight = %f, 期望 %f",
						cfg.ModelFamilyPreference.Weight, tt.expected)
				}
			})
		}
	})
}

func TestModelFamilyPreferenceConfig_GetEffectiveOrder(t *testing.T) {
	global := []string{"claude", "openai", "deepseek", "gemini"}
	supervisor := []string{"claude", "openai"}

	cfg := ModelFamilyPreferenceConfig{
		Enabled:     true,
		Weight:      0.2,
		GlobalOrder: global,
		PerTaskClass: map[string][]string{
			"supervisor": supervisor,
		},
	}

	tests := []struct {
		taskClass string
		expected  []string
	}{
		{"supervisor", supervisor},
		{"worker", global},
		{"", global},
		{"unknown", global},
	}

	for _, tt := range tests {
		t.Run("taskClass="+tt.taskClass, func(t *testing.T) {
			order := cfg.GetEffectiveOrder(tt.taskClass)
			if len(order) != len(tt.expected) {
				t.Errorf("长度 = %d, 期望 %d", len(order), len(tt.expected))
				return
			}
			for i, v := range order {
				if v != tt.expected[i] {
					t.Errorf("order[%d] = %q, 期望 %q", i, v, tt.expected[i])
				}
			}
		})
	}
}

func TestModelFamilyPreferenceConfig_FamilyRank(t *testing.T) {
	cfg := ModelFamilyPreferenceConfig{
		GlobalOrder: []string{"claude", "openai", "deepseek", "gemini"},
		PerTaskClass: map[string][]string{
			"supervisor": {"claude", "openai"},
		},
	}

	tests := []struct {
		name      string
		taskClass string
		family    string
		expected  int
	}{
		{"全局排名 0", "worker", "claude", 0},
		{"全局排名 2", "worker", "deepseek", 2},
		{"全局未找到", "worker", "qwen", -1},
		{"per-task 排名 1", "supervisor", "openai", 1},
		{"per-task 未找到", "supervisor", "deepseek", -1},
		{"大小写不敏感", "worker", "Claude", 0},
		{"带空格", "worker", "  openai  ", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rank := cfg.FamilyRank(tt.taskClass, tt.family)
			if rank != tt.expected {
				t.Errorf("FamilyRank(%q, %q) = %d, 期望 %d",
					tt.taskClass, tt.family, rank, tt.expected)
			}
		})
	}
}

func TestWeightOverrides_NaNRemoval(t *testing.T) {
	cfg := AutopilotRoutingConfig{
		WeightOverrides: map[string]float64{
			"w_quality": 0.5,
			"w_cost":    0.3,
			"w_nan":     math.NaN(), // NaN
		},
	}
	cfg.Validate()

	if _, exists := cfg.WeightOverrides["w_nan"]; exists {
		t.Error("NaN 权重应被移除")
	}
	if cfg.WeightOverrides["w_quality"] != 0.5 {
		t.Errorf("w_quality = %f, 期望 0.5", cfg.WeightOverrides["w_quality"])
	}
}

func TestAutopilotRoutingConfig_DeepCopy(t *testing.T) {
	original := DefaultAutopilotRoutingConfig()
	original.CostPreference.PerTaskClass = map[string]string{
		"supervisor": "quality_first",
	}
	original.ModelFamilyPreference.PerTaskClass = map[string][]string{
		"supervisor": {"claude", "openai"},
	}
	original.WeightOverrides = map[string]float64{
		"w_quality": 0.5,
	}
	original.TaskDomainStrength.SeedMatrixOverrides = map[string]map[string]float64{
		"claude/fable": {"coding": 0.9},
	}

	cp := original.deepCopy()

	// 修改副本不应影响原始
	cp.CostPreference.PerTaskClass["worker"] = "cost_first"
	cp.ModelFamilyPreference.GlobalOrder[0] = "modified"
	cp.WeightOverrides["w_new"] = 1.0
	cp.TaskDomainStrength.SeedMatrixOverrides["openai/gpt-5"] = map[string]float64{"reasoning": 0.8}
	deepseekRule := cp.CostOptimization.ProviderTimePricing["deepseek"]
	deepseekRule.PeakWindows[0].Start = "10:00"
	cp.CostOptimization.ProviderTimePricing["deepseek"] = deepseekRule

	if _, exists := original.CostPreference.PerTaskClass["worker"]; exists {
		t.Error("修改副本 PerTaskClass 不应影响原始")
	}
	if original.ModelFamilyPreference.GlobalOrder[0] != "claude" {
		t.Error("修改副本 GlobalOrder 不应影响原始")
	}
	if _, exists := original.WeightOverrides["w_new"]; exists {
		t.Error("修改副本 WeightOverrides 不应影响原始")
	}
	if _, exists := original.TaskDomainStrength.SeedMatrixOverrides["openai/gpt-5"]; exists {
		t.Error("修改副本 SeedMatrixOverrides 不应影响原始")
	}
	if original.CostOptimization.ProviderTimePricing["deepseek"].PeakWindows[0].Start != "09:00" {
		t.Error("修改副本 ProviderTimePricing 不应影响原始")
	}
}

func TestGetAutopilotRoutingReturnsDeepCopy(t *testing.T) {
	t.Setenv(autopilotKillSwitchEnv, "false")
	original := DefaultAutopilotRoutingConfig()
	original.WeightOverrides = map[string]float64{"w_quality": 0.5}
	original.ModelFamilyPreference.GlobalOrder = []string{"claude", "openai"}
	cm := &ConfigManager{config: Config{AutopilotRouting: original}}

	first := cm.GetAutopilotRouting()
	first.WeightOverrides["w_quality"] = 1
	first.ModelFamilyPreference.GlobalOrder[0] = "modified"
	rule := first.CostOptimization.ProviderTimePricing["deepseek"]
	rule.PeakWindows[0].Start = "10:00"
	first.CostOptimization.ProviderTimePricing["deepseek"] = rule

	second := cm.GetAutopilotRouting()
	if second.WeightOverrides["w_quality"] != 0.5 {
		t.Fatal("修改 getter 返回的 map 不应污染 ConfigManager")
	}
	if second.ModelFamilyPreference.GlobalOrder[0] != "claude" {
		t.Fatal("修改 getter 返回的 slice 不应污染 ConfigManager")
	}
	if second.CostOptimization.ProviderTimePricing["deepseek"].PeakWindows[0].Start != "09:00" {
		t.Fatal("修改 getter 返回的 ProviderTimePricing 不应污染 ConfigManager")
	}
}

func TestNormalizeCostPreferenceMode(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"quality_first", "quality_first"},
		{"QUALITY_FIRST", "quality_first"},
		{"balanced", "balanced"},
		{"cost_first", "cost_first"},
		{"custom", "custom"},
		{"", "balanced"},
		{"invalid", "balanced"},
		{"  cost_first  ", "cost_first"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizeCostPreferenceMode(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeCostPreferenceMode(%q) = %q, 期望 %q",
					tt.input, result, tt.expected)
			}
		})
	}
}

func TestRemoveEmptyStrings(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{"nil 输入", nil, []string{}},
		{"空切片", []string{}, []string{}},
		{"全空", []string{"", "  ", ""}, []string{}},
		{"有值有空", []string{"a", "", "b", "  ", "c"}, []string{"a", "b", "c"}},
		{"全有值", []string{"a", "b"}, []string{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := removeEmptyStrings(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("长度 = %d, 期望 %d", len(result), len(tt.expected))
				return
			}
			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("[%d] = %q, 期望 %q", i, v, tt.expected[i])
				}
			}
		})
	}
}

func TestIsTruthyEnv(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"true", true},
		{"TRUE", true},
		{"True", true},
		{"1", true},
		{"yes", true},
		{"YES", true},
		{"on", true},
		{"ON", true},
		{"  true  ", true},
		{"false", false},
		{"0", false},
		{"no", false},
		{"", false},
		{"random", false},
		{"truee", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := isTruthyEnv(tt.input); got != tt.expected {
				t.Errorf("isTruthyEnv(%q) = %v, 期望 %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestDefaultAutopilotRoutingConfig_SLORollback(t *testing.T) {
	cfg := DefaultAutopilotRoutingConfig()

	if cfg.SLORollback.Enabled {
		t.Error("默认 SLORollback.Enabled 应为 false")
	}
	if cfg.SLORollback.ConsecutiveWindows != 3 {
		t.Errorf("默认 SLORollback.ConsecutiveWindows = %d, 期望 3", cfg.SLORollback.ConsecutiveWindows)
	}
}

func TestAutopilotRoutingConfig_Validate_SLORollbackConsecutiveWindows(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{"正数保持不变", 5, 5},
		{"零回退为默认 3", 0, 3},
		{"负数回退为默认 3", -1, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := AutopilotRoutingConfig{
				SLORollback: SLORollbackConfig{
					ConsecutiveWindows: tt.input,
				},
			}
			cfg.Validate()
			if cfg.SLORollback.ConsecutiveWindows != tt.expected {
				t.Errorf("Validate 后 ConsecutiveWindows = %d, 期望 %d",
					cfg.SLORollback.ConsecutiveWindows, tt.expected)
			}
		})
	}
}

func TestAutopilotRoutingConfig_Validate_ABTestFallback(t *testing.T) {
	tests := []struct {
		name                 string
		input                ABTestConfig
		expectedSampleRatio  float64
		expectedMaxPerHour   int
		expectedCandidateCnt int
	}{
		{
			name:                 "正常值保持不变",
			input:                ABTestConfig{SampleRatio: 0.05, MaxShadowRequestsPerHour: 100, ShadowCandidateCount: 2},
			expectedSampleRatio:  0.05,
			expectedMaxPerHour:   100,
			expectedCandidateCnt: 2,
		},
		{
			name:                 "零值回退默认",
			input:                ABTestConfig{SampleRatio: 0, MaxShadowRequestsPerHour: 0, ShadowCandidateCount: 0},
			expectedSampleRatio:  0.01,
			expectedMaxPerHour:   60,
			expectedCandidateCnt: 1,
		},
		{
			name:                 "负数回退默认",
			input:                ABTestConfig{SampleRatio: -0.5, MaxShadowRequestsPerHour: -1, ShadowCandidateCount: -1},
			expectedSampleRatio:  0.01,
			expectedMaxPerHour:   60,
			expectedCandidateCnt: 1,
		},
		{
			name:                 "SampleRatio 超过 1 回退默认",
			input:                ABTestConfig{SampleRatio: 1.5, MaxShadowRequestsPerHour: 60, ShadowCandidateCount: 1},
			expectedSampleRatio:  0.01,
			expectedMaxPerHour:   60,
			expectedCandidateCnt: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := AutopilotRoutingConfig{ABTest: tt.input}
			cfg.Validate()
			if cfg.ABTest.SampleRatio != tt.expectedSampleRatio {
				t.Errorf("Validate 后 SampleRatio = %v, 期望 %v", cfg.ABTest.SampleRatio, tt.expectedSampleRatio)
			}
			if cfg.ABTest.MaxShadowRequestsPerHour != tt.expectedMaxPerHour {
				t.Errorf("Validate 后 MaxShadowRequestsPerHour = %d, 期望 %d", cfg.ABTest.MaxShadowRequestsPerHour, tt.expectedMaxPerHour)
			}
			if cfg.ABTest.ShadowCandidateCount != tt.expectedCandidateCnt {
				t.Errorf("Validate 后 ShadowCandidateCount = %d, 期望 %d", cfg.ABTest.ShadowCandidateCount, tt.expectedCandidateCnt)
			}
		})
	}
}
