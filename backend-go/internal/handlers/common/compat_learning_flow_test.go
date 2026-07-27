package common

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/converters"
)

// 验证「学习 -> 注入 -> 改写」这条闭环在各环节的键与结论一致，
// 覆盖 failover 无法在单测中完整驱动的部分（真实 failover 需要 scheduler/metrics 全套依赖）。
func TestCompatLearningFlowDeveloperRole(t *testing.T) {
	const (
		channelUID = "ch_test"
		keyHash    = "kh_test"
		model      = "gpt-5"
	)
	cache := config.NewChannelCompatCacheWithPersistence(filepath.Join(t.TempDir(), "compat.json"))

	// 1. 首次请求：请求体带 developer role，上游以真实文案 400 拒绝
	body := []byte(`{"model":"gpt-5","input":[{"type":"message","role":"developer","content":"dev"}]}`)
	if !BodyHasDeveloperRole(body) {
		t.Fatal("应检出 Responses input 中的 developer role")
	}

	signal := CompatTraitFromError(http.StatusBadRequest, []byte(developerRoleErrorBody),
		CompatSignalContext{HasDeveloperRole: true})
	if signal == nil || signal.Trait != config.TraitDowngradeDeveloperRole {
		t.Fatalf("应识别出 developer role 信号, got %+v", signal)
	}

	// 2. 学习并落盘；首次记录返回 true 才允许重试
	if !cache.Record(channelUID, keyHash, model, signal.Trait, signal.Enabled, config.CompatSourceErrorSignal, signal.Evidence) {
		t.Fatal("首次学习应返回新增（触发同 Key 重试）")
	}
	// 同一结论再次记录必须返回 false，否则会无限重试
	if cache.Record(channelUID, keyHash, model, signal.Trait, signal.Enabled, config.CompatSourceErrorSignal, signal.Evidence) {
		t.Fatal("重复学习必须返回 false 以终止重试")
	}

	// 3. 重试前注入：模拟 failover 主动侧
	upstream := &config.UpstreamConfig{Name: "test", ChannelUID: channelUID}
	if state, ok := cache.Trait(channelUID, keyHash, model, config.TraitDowngradeDeveloperRole); ok && state.Enabled {
		upstream.LearnedDowngradeDeveloperRole = true
	}
	if !upstream.IsDowngradeDeveloperRoleEnabled() {
		t.Fatal("注入后 upstream 应报告需要降级 developer role")
	}

	// 4. provider 侧改写：转换后的 Chat 请求体 developer -> system
	reqMap := map[string]interface{}{
		"messages": []interface{}{
			map[string]interface{}{"role": "developer", "content": "dev"},
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	}
	if !converters.DowngradeDeveloperRoleToSystem(reqMap) {
		t.Fatal("应执行 developer -> system 改写")
	}
	first := reqMap["messages"].([]interface{})[0].(map[string]interface{})
	if first["role"] != "system" {
		t.Fatalf("role = %v, want system", first["role"])
	}
}

// 用户显式配置必须压过学习结论：这是整套自动机制的逃生阀。
func TestCompatUserConfigOverridesLearned(t *testing.T) {
	learnedTrue := config.BoolPtr(true)

	// 用户显式关闭 + 学到"应启用" -> 最终关闭
	userOff := &config.UpstreamConfig{StripImageGenerationTool: config.BoolPtr(false)}
	if userOff.IsStripImageGenerationToolEnabledWith(learnedTrue) {
		t.Error("用户显式关闭应压过学习结论")
	}

	// 用户未设置 + 学到"应启用" -> 最终启用
	unset := &config.UpstreamConfig{}
	if !unset.IsStripImageGenerationToolEnabledWith(learnedTrue) {
		t.Error("用户未设置时应采用学习结论")
	}

	// 用户未设置 + 无学习结论 -> 回落静态默认（false）
	if unset.IsStripImageGenerationToolEnabledWith(nil) {
		t.Error("无学习结论时应回落静态默认 false")
	}

	// 用户显式开启 + 无学习结论 -> 启用
	userOn := &config.UpstreamConfig{StripImageGenerationTool: config.BoolPtr(true)}
	if !userOn.IsStripImageGenerationToolEnabledWith(nil) {
		t.Error("用户显式开启应生效")
	}
}

// 迁移后的老用户渠道：手工值已降级为种子，学习结论必须能覆盖它，
// 这样用户此后不需要再手工干预。
func TestLearnedConclusionOverridesMigratedSeed(t *testing.T) {
	// 老用户曾手工开启剥离图片工具，迁移后成为种子、字段置 nil
	upstream := &config.UpstreamConfig{
		Name:       "migrated",
		ChannelUID: "ch_seed",
	}
	upstream.SetCompatSeed(config.TraitStripImageGenTool, true)

	// 无学习结论时按种子生效，行为与迁移前一致
	if !upstream.IsStripImageGenerationToolEnabled() {
		t.Fatal("无学习结论时应沿用种子（迁移不改变可感知行为）")
	}

	// 学到"该上游其实支持图片生成"后，注入 false 应压过种子
	upstreamCopy := upstream.Clone()
	upstreamCopy.StripImageGenerationTool = config.BoolPtr(false)
	if upstreamCopy.IsStripImageGenerationToolEnabled() {
		t.Error("学习结论应压过种子，否则用户还得手工去关")
	}

	// 种子不因 Clone 而与原对象共享（map 必须深拷贝）
	upstreamCopy.SetCompatSeed(config.TraitStripImageGenTool, false)
	if v := upstream.CompatSeeds[string(config.TraitStripImageGenTool)]; !v {
		t.Error("Clone 后修改副本种子不应影响原对象")
	}
}

// 自动托管渠道的兼容性开关被清为 nil（而非 false），学习结论才能生效。
func TestAutoManagedRuntimeKeepsCompatSwitchesUnset(t *testing.T) {
	upstream := &config.UpstreamConfig{
		ProviderID:               "mimo",
		AutoManaged:              true,
		ServiceType:              "claude",
		StripImageGenerationTool: config.BoolPtr(true),
		PassbackThinkingBlocks:   config.BoolPtr(true),
	}

	runtime := config.RuntimeUpstreamForAutoManagedProvider(upstream)
	if runtime.StripImageGenerationTool != nil || runtime.PassbackThinkingBlocks != nil {
		t.Fatalf("自动托管渠道的兼容性开关应清为 nil 以便学习生效: %+v", runtime)
	}
	// 清为 nil 后，学习结论可以生效
	if !runtime.IsStripImageGenerationToolEnabledWith(config.BoolPtr(true)) {
		t.Error("清为 nil 后学习结论应可生效")
	}
}
