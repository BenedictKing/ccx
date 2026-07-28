package config

import (
	"testing"
	"time"
)

func TestResolveCompatSwitchPriority(t *testing.T) {
	yes, no := BoolPtr(true), BoolPtr(false)

	tests := []struct {
		name          string
		learned       *bool
		seed          *bool
		staticDefault bool
		want          bool
	}{
		// 学习结论压过种子：这是"此后不需要手工干预"的关键
		{"学习开启压过种子关闭", yes, no, false, true},
		{"学习关闭压过种子开启", no, yes, true, false},
		// 学习缺失时种子兜底（历史手工值的一次性低置信度提示）
		{"无学习时用种子", nil, yes, false, true},
		{"无学习时种子关闭", nil, no, true, false},
		// 都没有则回落静态默认
		{"全无回落默认true", nil, nil, true, true},
		{"全无回落默认false", nil, nil, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveCompatSwitch(tt.learned, tt.seed, tt.staticDefault); got != tt.want {
				t.Errorf("resolveCompatSwitch() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMigrateManualCompatSwitchesToSeeds(t *testing.T) {
	// migrateManualCompatSwitchesToSeeds 读磁盘原始 JSON（字段已从结构体删除，
	// 只能从历史配置文件里读到老用户的值），因此测试必须构造 rawJSON 而不是结构体字面量。
	rawJSON := []byte(`{
		"upstream": [{
			"name": "manual-ch",
			"stripEmptyTextBlocks": true,
			"passbackReasoningContent": false
		}],
		"chatUpstream": [{
			"name": "chat-ch",
			"stripImageGenerationTool": true
		}],
		"vectorsUpstream": [{
			"name": "vec-ch",
			"normalizeNonstandardChatRoles": true
		}]
	}`)

	cm := &ConfigManager{config: Config{
		Upstream:        []UpstreamConfig{{Name: "manual-ch"}},
		ChatUpstream:    []UpstreamConfig{{Name: "chat-ch"}},
		VectorsUpstream: []UpstreamConfig{{Name: "vec-ch"}},
	}}

	if !cm.migrateManualCompatSwitchesToSeeds(rawJSON) {
		t.Fatal("存在手工配置时应报告已迁移")
	}

	ch := cm.config.Upstream[0]
	// 值本身保留为种子（含 false，用户"显式关闭"的观察同样是证据），带有效期
	stripEntry, ok := ch.CompatSeeds[string(TraitStripEmptyTextBlocks)]
	if !ok || !stripEntry.Enabled {
		t.Errorf("种子应保留 true, got %+v/%v", stripEntry, ok)
	}
	if stripEntry.ExpiresAt.Before(time.Now()) || stripEntry.ExpiresAt.After(time.Now().Add(CompatSeedTTL+time.Minute)) {
		t.Errorf("种子应带 ~%s 有效期, got %v", CompatSeedTTL, stripEntry.ExpiresAt)
	}
	reasoningEntry, ok := ch.CompatSeeds[string(TraitPassbackReasoningContent)]
	if !ok || reasoningEntry.Enabled {
		t.Errorf("种子应保留 false, got %+v/%v", reasoningEntry, ok)
	}
	// 迁移后生效值不变（用户可感知行为一致，学习结论出现前按种子生效）
	if !ch.IsStripEmptyTextBlocksEnabled() {
		t.Error("迁移后生效值应保持 true")
	}
	if ch.IsPassbackReasoningContentEnabled() {
		t.Error("迁移后生效值应保持 false")
	}

	// Vectors 渠道也要覆盖（历史迁移函数常漏这一类）
	vecEntry, ok := cm.config.VectorsUpstream[0].CompatSeeds[string(TraitNormalizeNonstandardChatRoles)]
	if !ok || !vecEntry.Enabled {
		t.Errorf("Vectors 渠道种子未迁移, got %+v/%v", vecEntry, ok)
	}
}

// 迁移按数组下标把原始 JSON 对到当前渠道，因此必须在 mergeManagedProviderAccounts 之前执行：
// merge 会把同 provider 的多账号渠道合并成一条（out 比输入短），先 merge 再迁移会让种子写错渠道。
// 本测试固定这个顺序契约：如果有人把迁移调用移到 merge 之后，第二条渠道的种子会错位或丢失。
func TestMigrateCompatSeedsRunsBeforeAccountMerge(t *testing.T) {
	rawJSON := []byte(`{
		"upstream": [
			{"name": "ch-a", "stripEmptyTextBlocks": true},
			{"name": "ch-b", "passbackThinkingBlocks": true}
		]
	}`)

	cm := &ConfigManager{config: Config{
		Upstream: []UpstreamConfig{
			{Name: "ch-a"},
			{Name: "ch-b"},
		},
	}}

	if !cm.migrateManualCompatSwitchesToSeeds(rawJSON) {
		t.Fatal("应报告已迁移")
	}

	// 下标 0 只该拿到 stripEmptyTextBlocks，下标 1 只该拿到 passbackThinkingBlocks
	if _, ok := cm.config.Upstream[0].CompatSeeds[string(TraitStripEmptyTextBlocks)]; !ok {
		t.Error("ch-a 应迁移到 stripEmptyTextBlocks 种子")
	}
	if _, ok := cm.config.Upstream[0].CompatSeeds[string(TraitPassbackThinkingBlocks)]; ok {
		t.Error("ch-a 不应拿到属于 ch-b 的种子（下标错位）")
	}
	if _, ok := cm.config.Upstream[1].CompatSeeds[string(TraitPassbackThinkingBlocks)]; !ok {
		t.Error("ch-b 应迁移到 passbackThinkingBlocks 种子")
	}
	if _, ok := cm.config.Upstream[1].CompatSeeds[string(TraitStripEmptyTextBlocks)]; ok {
		t.Error("ch-b 不应拿到属于 ch-a 的种子（下标错位）")
	}
}

// mergeManagedProviderAccounts 合并渠道时必须带走 CompatSeeds，
// 否则被合并掉的渠道上的历史证据会随渠道一起丢失。
func TestMergeManagedProviderAccountsPreservesCompatSeeds(t *testing.T) {
	cm := &ConfigManager{config: Config{
		ManagedAccounts: []ManagedAccountConfig{
			{ProviderID: "mimo", AccountUID: "acct_1", Name: "MiMo"},
			{ProviderID: "mimo", AccountUID: "acct_2", Name: "MiMo"},
		},
		Upstream: []UpstreamConfig{
			{Name: "mimo-1", ProviderID: "mimo", AccountUID: "acct_1", AutoManaged: true, APIKeys: []string{"sk-1"}},
			{Name: "mimo-2", ProviderID: "mimo", AccountUID: "acct_2", AutoManaged: true, APIKeys: []string{"sk-2"}},
		},
	}}
	// 只在"会被合并掉"的那条上放种子
	cm.config.Upstream[1].SetCompatSeed(TraitStripEmptyTextBlocks, true)

	cm.mergeManagedProviderAccounts()

	if len(cm.config.Upstream) != 1 {
		t.Fatalf("两个同 provider 账号应合并为 1 条渠道, got %d", len(cm.config.Upstream))
	}
	entry, ok := cm.config.Upstream[0].CompatSeeds[string(TraitStripEmptyTextBlocks)]
	if !ok || !entry.Enabled {
		t.Errorf("被合并渠道的种子应保留到合并后的渠道, got %+v/%v", entry, ok)
	}
}
