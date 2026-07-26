package config

import (
	"testing"
	"time"
)

// TestDeprecatedParamCacheRecordAndParams 覆盖记录与读取的基本语义
func TestDeprecatedParamCacheRecordAndParams(t *testing.T) {
	c := NewDeprecatedParamCache()

	// 未记录时返回 nil
	if params := c.Params("ch1", "k1", "claude-opus-4-8"); params != nil {
		t.Errorf("未记录时应返回 nil，实际 %v", params)
	}

	// 首次记录返回 true（触发重试）
	if !c.Record("ch1", "k1", "claude-opus-4-8", "temperature") {
		t.Error("首次 Record 应返回 true")
	}
	// 重复记录返回 false（避免死循环）
	if c.Record("ch1", "k1", "claude-opus-4-8", "temperature") {
		t.Error("重复 Record 应返回 false")
	}
	// 同组合的新参数仍返回 true
	if !c.Record("ch1", "k1", "claude-opus-4-8", "top_p") {
		t.Error("新参数 Record 应返回 true")
	}

	// Params 按字母序返回
	params := c.Params("ch1", "k1", "claude-opus-4-8")
	if len(params) != 2 || params[0] != "temperature" || params[1] != "top_p" {
		t.Errorf("Params() = %v, want [temperature top_p]", params)
	}

	// 空参数名不记录
	if c.Record("ch1", "k1", "claude-opus-4-8", "") {
		t.Error("空参数名应返回 false")
	}
}

// TestDeprecatedParamCacheIsolation 验证记忆不跨渠道/Key/模型泄漏
func TestDeprecatedParamCacheIsolation(t *testing.T) {
	c := NewDeprecatedParamCache()
	c.Record("ch1", "k1", "model-a", "temperature")

	cases := []struct {
		name                       string
		channelUID, keyHash, model string
	}{
		{"不同渠道", "ch2", "k1", "model-a"},
		{"不同 Key", "ch1", "k2", "model-a"},
		{"不同模型", "ch1", "k1", "model-b"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if params := c.Params(tt.channelUID, tt.keyHash, tt.model); params != nil {
				t.Errorf("%s 不应命中记忆，实际 %v", tt.name, params)
			}
		})
	}
}

// TestDeprecatedParamCacheExpiry 验证 TTL 过期后记忆失效，允许重新探测
func TestDeprecatedParamCacheExpiry(t *testing.T) {
	c := NewDeprecatedParamCache()
	c.Record("ch1", "k1", "model-a", "temperature")

	// 手动将探测时间回拨到 TTL 之外
	key := GenerateCacheKey("ch1", "k1", "model-a")
	c.mu.Lock()
	c.cache[key].DetectedAt = time.Now().Add(-deprecatedParamTTL - time.Minute)
	c.mu.Unlock()

	if params := c.Params("ch1", "k1", "model-a"); params != nil {
		t.Errorf("过期条目应返回 nil，实际 %v", params)
	}
	// 过期后重新记录应视为首次（返回 true，重新触发重试探测）
	if !c.Record("ch1", "k1", "model-a", "temperature") {
		t.Error("过期后 Record 应返回 true")
	}
}

// TestDeprecatedParamCacheMarkStripped 验证主动剥离计数与有效期刷新
func TestDeprecatedParamCacheMarkStripped(t *testing.T) {
	c := NewDeprecatedParamCache()
	c.Record("ch1", "k1", "model-a", "temperature")

	c.MarkStripped("ch1", "k1", "model-a")
	c.MarkStripped("ch1", "k1", "model-a")

	c.mu.RLock()
	count := c.cache[GenerateCacheKey("ch1", "k1", "model-a")].StripCount
	c.mu.RUnlock()
	if count != 2 {
		t.Errorf("StripCount = %d, want 2", count)
	}

	// 对不存在的条目调用应安全无副作用
	c.MarkStripped("nope", "nope", "nope")
	if c.Size() != 1 {
		t.Errorf("Size() = %d, want 1", c.Size())
	}
}
