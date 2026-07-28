package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordContextLimitTakesMinimum(t *testing.T) {
	c := NewChannelCompatCache()

	if _, ok := c.ContextLimit("ch1", "kh1", "gpt-5.5"); ok {
		t.Fatal("空缓存不应命中上下文上限")
	}

	if !c.RecordContextLimit("ch1", "kh1", "gpt-5.5", 272_000, CompatSourceUpstreamDeclared, "maximum context length is 272000", 1_050_000) {
		t.Fatal("首次记录应返回新增")
	}
	state, ok := c.ContextLimit("ch1", "kh1", "gpt-5.5")
	if !ok || state.MaxInputTokens != 272_000 {
		t.Fatalf("ContextLimit = %+v ok=%v, want 272000", state, ok)
	}

	// 更严格的证据应收紧
	if !c.RecordContextLimit("ch1", "kh1", "gpt-5.5", 131_072, CompatSourceUpstreamDeclared, "max 131072", 200_000) {
		t.Fatal("更小的上限应写入")
	}
	if state, _ := c.ContextLimit("ch1", "kh1", "gpt-5.5"); state.MaxInputTokens != 131_072 {
		t.Errorf("MaxInputTokens = %d, want 131072", state.MaxInputTokens)
	}

	// 更宽松的证据不得放宽已有结论：放宽只能靠 TTL 过期后重新学习
	if c.RecordContextLimit("ch1", "kh1", "gpt-5.5", 400_000, CompatSourceUpstreamDeclared, "max 400000", 900_000) {
		t.Error("更大的上限不应覆盖已学到的更严格结论")
	}
	if state, _ := c.ContextLimit("ch1", "kh1", "gpt-5.5"); state.MaxInputTokens != 131_072 {
		t.Errorf("放宽尝试后 MaxInputTokens = %d, want 131072", state.MaxInputTokens)
	}
}

func TestRecordContextLimitRejectsImplausiblySmall(t *testing.T) {
	c := NewChannelCompatCache()

	// 低于下界的值更可能来自误判的无关 400，学进去会把渠道永久排除
	if c.RecordContextLimit("ch1", "kh1", "m1", 512, CompatSourceRejectedEstimate, "too long", 600) {
		t.Error("低于最小可学习上限的值不应写入")
	}
	if _, ok := c.ContextLimit("ch1", "kh1", "m1"); ok {
		t.Error("不可信的上限不应产生记忆")
	}
}

func TestMinContextLimitForChannelModelIsolation(t *testing.T) {
	c := NewChannelCompatCache()

	c.RecordContextLimit("ch1", "khA", "gpt-5.5", 272_000, CompatSourceUpstreamDeclared, "a", 500_000)
	c.RecordContextLimit("ch1", "khB", "gpt-5.5", 131_072, CompatSourceUpstreamDeclared, "b", 500_000)
	c.RecordContextLimit("ch1", "khA", "gpt-5.6", 1_000_000, CompatSourceUpstreamDeclared, "c", 2_000_000)
	c.RecordContextLimit("ch2", "khA", "gpt-5.5", 8_192, CompatSourceUpstreamDeclared, "d", 50_000)

	// 同渠道同模型跨 Key 取最小值：路由发生在选 Key 之前，保守优先
	got, ok := c.MinContextLimitForChannelModel("ch1", "gpt-5.5")
	if !ok || got != 131_072 {
		t.Errorf("MinContextLimitForChannelModel(ch1, gpt-5.5) = %d, %v, want 131072, true", got, ok)
	}

	// 不同模型互不影响
	if got, _ := c.MinContextLimitForChannelModel("ch1", "gpt-5.6"); got != 1_000_000 {
		t.Errorf("gpt-5.6 上限 = %d, want 1000000", got)
	}

	// 不同渠道互不影响
	if got, _ := c.MinContextLimitForChannelModel("ch2", "gpt-5.5"); got != 8_192 {
		t.Errorf("ch2 上限 = %d, want 8192", got)
	}

	// 未学习过的组合 fail-open
	if _, ok := c.MinContextLimitForChannelModel("ch3", "gpt-5.5"); ok {
		t.Error("未学习过的渠道应 fail-open")
	}
}

func TestContextLimitSurvivesReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channel_compat.json")

	c1 := NewChannelCompatCacheWithPersistence(path)
	if !c1.RecordContextLimit("ch1", "kh1", "gpt-5.5", 272_000, CompatSourceUpstreamDeclared, "declared", 900_000) {
		t.Fatal("首次记录应写入")
	}

	// 只有 ContextLimit 没有 trait 的条目也必须能加载回来
	c2 := NewChannelCompatCacheWithPersistence(path)
	state, ok := c2.ContextLimit("ch1", "kh1", "gpt-5.5")
	if !ok || state.MaxInputTokens != 272_000 {
		t.Fatalf("重载后 ContextLimit = %+v ok=%v, want 272000", state, ok)
	}
	if state.Source != CompatSourceUpstreamDeclared {
		t.Errorf("Source = %q, want %q", state.Source, CompatSourceUpstreamDeclared)
	}
}

func TestContextLimitExpiresIndependentlyOfTraitRefresh(t *testing.T) {
	c := NewChannelCompatCache()

	c.RecordContextLimit("ch1", "kh1", "m1", 100_000, CompatSourceUpstreamDeclared, "declared", 500_000)
	c.Record("ch1", "kh1", "m1", TraitStripEmptyTextBlocks, true, CompatSourceProbe, "probe")

	// 手工把上下文上限的学习时间推到 TTL 之外，同时让 trait 侧保持新鲜
	// （MarkApplied 会刷新 DetectedAt，若共用时间戳会让上限被无限续期）
	key := GenerateCacheKey("ch1", "kh1", "m1")
	c.mu.Lock()
	c.cache[key].ContextLimit.LearnedAt = time.Now().Add(-channelCompatTTL - time.Hour)
	c.mu.Unlock()
	c.MarkApplied("ch1", "kh1", "m1", TraitStripEmptyTextBlocks)

	if _, ok := c.ContextLimit("ch1", "kh1", "m1"); ok {
		t.Error("上下文上限应按自己的 LearnedAt 过期，不应被 trait 命中续期")
	}
	if _, ok := c.Trait("ch1", "kh1", "m1", TraitStripEmptyTextBlocks); !ok {
		t.Error("trait 侧结论应仍然有效")
	}
	if _, ok := c.MinContextLimitForChannelModel("ch1", "m1"); ok {
		t.Error("过期上限不应参与最小值计算")
	}
}

func TestContextLimitNotLoadedWhenExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channel_compat.json")
	stale := `{"ch1:kh1:m1":{"traits":{},"context_limit":{"max_input_tokens":100000,` +
		`"source":"upstream_declared","learned_at":"2020-01-01T00:00:00Z"},` +
		`"detected_at":"` + time.Now().UTC().Format(time.RFC3339) + `"}}`
	if err := os.WriteFile(path, []byte(stale), 0644); err != nil {
		t.Fatalf("写入测试状态文件失败: %v", err)
	}

	c := NewChannelCompatCacheWithPersistence(path)
	if _, ok := c.ContextLimit("ch1", "kh1", "m1"); ok {
		t.Error("过期的上下文上限不应被加载")
	}
}
