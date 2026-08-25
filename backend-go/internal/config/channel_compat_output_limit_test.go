package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordOutputLimitTakesMinimum(t *testing.T) {
	c := NewChannelCompatCache()

	if _, ok := c.OutputLimit("ch1", "kh1", "kimi-k2.6"); ok {
		t.Fatal("空缓存不应命中输出上限")
	}

	if !c.RecordOutputLimit("ch1", "kh1", "kimi-k2.6", 32768, CompatSourceUpstreamDeclared, "expected a value <= 32768", 64000) {
		t.Fatal("首次记录应返回新增")
	}
	state, ok := c.OutputLimit("ch1", "kh1", "kimi-k2.6")
	if !ok || state.MaxOutputTokens != 32768 {
		t.Fatalf("OutputLimit = %+v ok=%v, want 32768", state, ok)
	}
	if state.RejectedTokens != 64000 {
		t.Errorf("RejectedTokens = %d, want 64000", state.RejectedTokens)
	}

	// 更严格的证据应收紧
	if !c.RecordOutputLimit("ch1", "kh1", "kimi-k2.6", 16384, CompatSourceUpstreamDeclared, "max 16384", 64000) {
		t.Fatal("更小的上限应写入")
	}
	if state, _ := c.OutputLimit("ch1", "kh1", "kimi-k2.6"); state.MaxOutputTokens != 16384 {
		t.Errorf("MaxOutputTokens = %d, want 16384", state.MaxOutputTokens)
	}

	// 更宽松的证据不得放宽已有结论：放宽只能靠 TTL 过期后重新学习
	if c.RecordOutputLimit("ch1", "kh1", "kimi-k2.6", 262144, CompatSourceUpstreamDeclared, "max 262144", 300000) {
		t.Error("更大的上限不应覆盖已学到的更严格结论")
	}
	if state, _ := c.OutputLimit("ch1", "kh1", "kimi-k2.6"); state.MaxOutputTokens != 16384 {
		t.Errorf("放宽尝试后 MaxOutputTokens = %d, want 16384", state.MaxOutputTokens)
	}
}

func TestRecordOutputLimitRejectsImplausiblySmall(t *testing.T) {
	c := NewChannelCompatCache()

	// 低于下界的值更可能来自误判的无关 400（如 temperature <= 2）
	if c.RecordOutputLimit("ch1", "kh1", "m1", 2, CompatSourceUpstreamDeclared, "temperature <= 2", 64000) {
		t.Error("低于最小可学习上限的值不应写入")
	}
	if _, ok := c.OutputLimit("ch1", "kh1", "m1"); ok {
		t.Error("不可信的上限不应产生记忆")
	}
}

func TestOutputLimitSurvivesReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channel_compat.json")

	c1 := NewChannelCompatCacheWithPersistence(path)
	if !c1.RecordOutputLimit("ch1", "kh1", "kimi-k2.6", 32768, CompatSourceUpstreamDeclared, "declared", 64000) {
		t.Fatal("首次记录应写入")
	}

	// 只有 OutputLimit 没有 trait 的条目也必须能加载回来
	c2 := NewChannelCompatCacheWithPersistence(path)
	state, ok := c2.OutputLimit("ch1", "kh1", "kimi-k2.6")
	if !ok || state.MaxOutputTokens != 32768 {
		t.Fatalf("重载后 OutputLimit = %+v ok=%v, want 32768", state, ok)
	}
	if state.Source != CompatSourceUpstreamDeclared {
		t.Errorf("Source = %q, want %q", state.Source, CompatSourceUpstreamDeclared)
	}
}

func TestOutputLimitExpiresIndependentlyOfTraitRefresh(t *testing.T) {
	c := NewChannelCompatCache()

	c.RecordOutputLimit("ch1", "kh1", "m1", 32768, CompatSourceUpstreamDeclared, "declared", 64000)
	c.Record("ch1", "kh1", "m1", TraitStripEmptyTextBlocks, true, CompatSourceProbe, "probe")

	// 手工把输出上限的学习时间推到 TTL 之外，同时让 trait 侧保持新鲜
	key := GenerateCacheKey("ch1", "kh1", "m1")
	c.mu.Lock()
	c.cache[key].OutputLimit.LearnedAt = time.Now().Add(-channelCompatTTL - time.Hour)
	c.mu.Unlock()
	c.MarkApplied("ch1", "kh1", "m1", TraitStripEmptyTextBlocks)

	if _, ok := c.OutputLimit("ch1", "kh1", "m1"); ok {
		t.Error("输出上限应按自己的 LearnedAt 过期，不应被 trait 命中续期")
	}
	if _, ok := c.Trait("ch1", "kh1", "m1", TraitStripEmptyTextBlocks); !ok {
		t.Error("trait 侧结论应仍然有效")
	}
}

func TestOutputLimitNotLoadedWhenExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channel_compat.json")
	stale := `{"ch1:kh1:m1":{"traits":{},"output_limit":{"max_output_tokens":32768,` +
		`"source":"upstream_declared","learned_at":"2020-01-01T00:00:00Z"},` +
		`"detected_at":"` + time.Now().UTC().Format(time.RFC3339) + `"}}`
	if err := os.WriteFile(path, []byte(stale), 0644); err != nil {
		t.Fatalf("写入测试状态文件失败: %v", err)
	}

	c := NewChannelCompatCacheWithPersistence(path)
	if _, ok := c.OutputLimit("ch1", "kh1", "m1"); ok {
		t.Error("过期的输出上限不应被加载")
	}
}
