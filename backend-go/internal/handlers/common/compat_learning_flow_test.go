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

	// 3. 重试前注入：模拟 failover 主动侧，写入本次请求专用的 LearnedCompatTraits
	upstream := &config.UpstreamConfig{Name: "test", ChannelUID: channelUID}
	if state, ok := cache.Trait(channelUID, keyHash, model, config.TraitDowngradeDeveloperRole); ok && state.Enabled {
		upstream.SetLearnedCompatTrait(config.TraitDowngradeDeveloperRole, true)
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

// 学习结论压过种子：这是"此后不需要手工干预"的关键。种子只是历史手工值降级来的
// 一次性低置信度提示，一旦学到上游真实结论就必须让位。
func TestCompatLearnedConclusionOverridesSeed(t *testing.T) {
	upstream := &config.UpstreamConfig{Name: "test", ChannelUID: "ch_seed"}

	// 历史种子：曾经手工判断"需要剥离图片工具"
	upstream.SetCompatSeed(config.TraitStripImageGenTool, true)
	if !upstream.IsStripImageGenerationToolEnabled() {
		t.Fatal("无学习结论时应沿用种子")
	}

	// 学到"该上游其实支持图片生成"后，学习结论应压过种子
	upstream.SetLearnedCompatTrait(config.TraitStripImageGenTool, false)
	if upstream.IsStripImageGenerationToolEnabled() {
		t.Error("学习结论应压过种子，否则用户还得手工去关")
	}
}

// 种子有 14 天有效期：过期后不再参与判断，回落静态默认。
// 历史手工值不是永久事实——可能当初就设错，也可能上游后来有了新情况。
func TestCompatSeedExpiresAfterTTL(t *testing.T) {
	upstream := &config.UpstreamConfig{Name: "test", ChannelUID: "ch_expiry"}
	upstream.SetCompatSeed(config.TraitStripImageGenTool, true)

	// 手动把种子写成已过期，模拟 14 天后的状态
	entry := upstream.CompatSeeds[string(config.TraitStripImageGenTool)]
	entry.ExpiresAt = entry.ExpiresAt.Add(-2 * config.CompatSeedTTL)
	upstream.CompatSeeds[string(config.TraitStripImageGenTool)] = entry

	if upstream.IsStripImageGenerationToolEnabled() {
		t.Error("过期种子不应再参与判断，应回落静态默认 false")
	}
}

// Clone 必须深拷贝 CompatSeeds：否则副本上的种子修改会污染原始配置。
func TestCompatSeedsNotSharedAfterClone(t *testing.T) {
	upstream := &config.UpstreamConfig{Name: "migrated", ChannelUID: "ch_clone"}
	upstream.SetCompatSeed(config.TraitStripImageGenTool, true)

	clone := upstream.Clone()
	clone.SetCompatSeed(config.TraitStripImageGenTool, false)

	original := upstream.CompatSeeds[string(config.TraitStripImageGenTool)]
	if !original.Enabled {
		t.Error("Clone 后修改副本种子不应影响原对象")
	}
}

// 自动托管渠道的种子与学习结论一并清空：其兼容性完全由厂商原生默认加运行时学习决定，
// 不保留手工配置派生的历史证据。
func TestAutoManagedRuntimeClearsCompatSeeds(t *testing.T) {
	upstream := &config.UpstreamConfig{
		ProviderID:  "mimo",
		AutoManaged: true,
		ServiceType: "claude",
	}
	upstream.SetCompatSeed(config.TraitStripImageGenTool, true)
	upstream.SetCompatSeed(config.TraitPassbackThinkingBlocks, true)

	runtime := config.RuntimeUpstreamForAutoManagedProvider(upstream)
	if len(runtime.CompatSeeds) != 0 {
		t.Fatalf("自动托管渠道的种子应被清空: %+v", runtime.CompatSeeds)
	}

	// 清空后学习结论仍可正常生效
	runtime.SetLearnedCompatTrait(config.TraitStripImageGenTool, true)
	if !runtime.IsStripImageGenerationToolEnabled() {
		t.Error("清空种子后学习结论应仍可生效")
	}
}
