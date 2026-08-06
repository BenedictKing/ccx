package config

import (
	"testing"
	"time"
)

func TestIsDocumentUnsupportedForChannelModelAggregation(t *testing.T) {
	c := NewChannelCompatCache()

	// 无记录时 fail-open
	if c.IsDocumentUnsupportedForChannelModel("ch1", "gpt-5.5") {
		t.Error("无学习记录应返回 false（fail-open）")
	}

	// khA 学到不支持 → 聚合并命中（路由先于选 Key，任一 Key 不支持即按不支持处理）
	c.Record("ch1", "khA", "gpt-5.5", TraitNoDocumentSupport, true, CompatSourceErrorSignal, "input tag 'document' ...")
	if !c.IsDocumentUnsupportedForChannelModel("ch1", "gpt-5.5") {
		t.Error("任一 Key 学到不支持后应返回 true")
	}
}

func TestIsDocumentUnsupportedForChannelModelIsolation(t *testing.T) {
	c := NewChannelCompatCache()
	c.Record("ch1", "khA", "gpt-5.5", TraitNoDocumentSupport, true, CompatSourceErrorSignal, "a")
	c.Record("ch1", "khA", "models/gemini-x:generateContent", TraitNoDocumentSupport, true, CompatSourceErrorSignal, "b")

	cases := []struct {
		name       string
		channelUID string
		model      string
		want       bool
	}{
		{"同渠道同模型", "ch1", "gpt-5.5", true},
		{"模型名大小写不敏感", "ch1", "GPT-5.5", true},
		{"含冒号模型名精确命中", "ch1", "models/gemini-x:generateContent", true},
		{"跨渠道隔离", "ch2", "gpt-5.5", false},
		{"跨模型隔离", "ch1", "gpt-5.6", false},
		{"含冒号模型名不误命中前缀", "ch1", "models/gemini-x", false},
		{"空渠道", "", "gpt-5.5", false},
		{"空模型", "ch1", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.IsDocumentUnsupportedForChannelModel(tc.channelUID, tc.model); got != tc.want {
				t.Errorf("IsDocumentUnsupportedForChannelModel(%q, %q) = %v, want %v",
					tc.channelUID, tc.model, got, tc.want)
			}
		})
	}
}

func TestIsDocumentUnsupportedForChannelModelSkipsExpired(t *testing.T) {
	c := NewChannelCompatCache()
	c.Record("ch1", "khA", "gpt-5.5", TraitNoDocumentSupport, true, CompatSourceErrorSignal, "a")

	// 手动把条目 DetectedAt 拨到 TTL 之前：过期条目等价于重新学习，不应命中
	key := GenerateCacheKey("ch1", "khA", "gpt-5.5")
	c.cache[key].DetectedAt = time.Now().Add(-channelCompatTTL - time.Hour)

	if c.IsDocumentUnsupportedForChannelModel("ch1", "gpt-5.5") {
		t.Error("超 TTL 的学习记录应被忽略")
	}
}

func TestIsDocumentUnsupportedIgnoresDisabledTrait(t *testing.T) {
	c := NewChannelCompatCache()
	// Enabled=false 的结论（理论上的翻转记录）不应视为"不支持"
	c.Record("ch1", "khA", "gpt-5.5", TraitNoDocumentSupport, false, CompatSourceErrorSignal, "a")
	if c.IsDocumentUnsupportedForChannelModel("ch1", "gpt-5.5") {
		t.Error("Enabled=false 的记录不应命中")
	}
	// 其他 trait 的记录不影响 document 判定
	c.Record("ch1", "khB", "gpt-5.5", TraitStripImageGenTool, true, CompatSourceErrorSignal, "b")
	if c.IsDocumentUnsupportedForChannelModel("ch1", "gpt-5.5") {
		t.Error("其他 trait 的记录不应命中 document 判定")
	}
}
