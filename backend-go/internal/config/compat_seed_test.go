package config

import "testing"

func TestResolveCompatSwitchPriority(t *testing.T) {
	yes, no := BoolPtr(true), BoolPtr(false)

	tests := []struct {
		name          string
		userSet       *bool
		learned       *bool
		seed          *bool
		staticDefault bool
		want          bool
	}{
		// 用户当前显式设置仍是最高优先级（临时强制压制的逃生阀）
		{"用户关闭压过学习开启", no, yes, yes, true, false},
		{"用户开启压过学习关闭", yes, no, no, false, true},
		// 学习结论压过种子：这是"此后不需要手工干预"的关键
		{"学习开启压过种子关闭", nil, yes, no, false, true},
		{"学习关闭压过种子开启", nil, no, yes, true, false},
		// 学习缺失时种子兜底（保留用户历史观察到的行为）
		{"无学习时用种子", nil, nil, yes, false, true},
		{"无学习时种子关闭", nil, nil, no, true, false},
		// 都没有则回落静态默认
		{"全无回落默认true", nil, nil, nil, true, true},
		{"全无回落默认false", nil, nil, nil, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveCompatSwitch(tt.userSet, tt.learned, tt.seed, tt.staticDefault); got != tt.want {
				t.Errorf("resolveCompatSwitch() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMigrateManualCompatSwitchesToSeeds(t *testing.T) {
	cm := &ConfigManager{config: Config{
		Upstream: []UpstreamConfig{{
			Name:                     "manual-ch",
			StripEmptyTextBlocks:     BoolPtr(true),
			PassbackReasoningContent: BoolPtr(false),
		}},
		ChatUpstream: []UpstreamConfig{{
			Name:                     "chat-ch",
			StripImageGenerationTool: BoolPtr(true),
		}},
		VectorsUpstream: []UpstreamConfig{{
			Name:                          "vec-ch",
			NormalizeNonstandardChatRoles: BoolPtr(true),
		}},
	}}

	if !cm.migrateManualCompatSwitchesToSeeds() {
		t.Fatal("存在手工配置时应报告已迁移")
	}

	ch := cm.config.Upstream[0]
	// 原字段清空，避免继续以第 1 优先级挡住学习
	if ch.StripEmptyTextBlocks != nil || ch.PassbackReasoningContent != nil {
		t.Errorf("迁移后原字段应置 nil: %+v", ch)
	}
	// 值本身保留为种子（含 false，用户"显式关闭"的观察同样是证据）
	if v, ok := ch.CompatSeeds[string(TraitStripEmptyTextBlocks)]; !ok || !v {
		t.Errorf("种子应保留 true, got %v/%v", v, ok)
	}
	if v, ok := ch.CompatSeeds[string(TraitPassbackReasoningContent)]; !ok || v {
		t.Errorf("种子应保留 false, got %v/%v", v, ok)
	}
	// 迁移后生效值不变（用户可感知行为一致）
	if !ch.IsStripEmptyTextBlocksEnabled() {
		t.Error("迁移后生效值应保持 true")
	}
	if ch.IsPassbackReasoningContentEnabled() {
		t.Error("迁移后生效值应保持 false")
	}

	// Vectors 渠道也要覆盖（历史迁移函数常漏这一类）
	if v, ok := cm.config.VectorsUpstream[0].CompatSeeds[string(TraitNormalizeNonstandardChatRoles)]; !ok || !v {
		t.Errorf("Vectors 渠道种子未迁移, got %v/%v", v, ok)
	}
}
