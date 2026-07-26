package config

import (
	"os"
	"path/filepath"
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

// TestDeprecatedParamCachePersistenceRoundTrip 验证记忆跨"重启"存活
func TestDeprecatedParamCachePersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deprecated_params.json")

	// 首次运行：探测到两个参数（Record 内部立即落盘）
	c1 := NewDeprecatedParamCacheWithPersistence(path)
	c1.Record("ch1", "k1", "claude-opus-4-8", "temperature")
	c1.Record("ch1", "k1", "claude-opus-4-8", "top_p")
	c1.Record("ch2", "k9", "model-b", "top_k")

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("落盘文件应存在: %v", err)
	}

	// 模拟重启：新实例从磁盘恢复，无需重新探测
	c2 := NewDeprecatedParamCacheWithPersistence(path)
	params := c2.Params("ch1", "k1", "claude-opus-4-8")
	if len(params) != 2 || params[0] != "temperature" || params[1] != "top_p" {
		t.Errorf("恢复后 Params() = %v, want [temperature top_p]", params)
	}
	if got := c2.Params("ch2", "k9", "model-b"); len(got) != 1 || got[0] != "top_k" {
		t.Errorf("恢复后第二组合 Params() = %v, want [top_k]", got)
	}

	// 恢复后重复 Record 应返回 false，不再触发重试
	if c2.Record("ch1", "k1", "claude-opus-4-8", "temperature") {
		t.Error("恢复后重复 Record 应返回 false，避免重启即重新探测")
	}
}

// TestDeprecatedParamCacheLoadSkipsExpired 验证过期记忆不会从磁盘复活
func TestDeprecatedParamCacheLoadSkipsExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deprecated_params.json")

	c1 := NewDeprecatedParamCacheWithPersistence(path)
	c1.Record("ch1", "k1", "model-a", "temperature")

	// 回拨探测时间到 TTL 之外并强制重写磁盘
	c1.mu.Lock()
	c1.cache[GenerateCacheKey("ch1", "k1", "model-a")].DetectedAt = time.Now().Add(-deprecatedParamTTL - time.Hour)
	c1.dirty = true
	c1.mu.Unlock()
	// Flush 只写未过期条目，因此这里应产出空集合
	if err := c1.Flush(); err != nil {
		t.Fatalf("Flush 失败: %v", err)
	}

	c2 := NewDeprecatedParamCacheWithPersistence(path)
	if params := c2.Params("ch1", "k1", "model-a"); params != nil {
		t.Errorf("过期记忆不应复活，实际 %v", params)
	}
}

// TestDeprecatedParamCacheLoadCorrupted 验证损坏/缺失文件不阻断启动
func TestDeprecatedParamCacheLoadCorrupted(t *testing.T) {
	dir := t.TempDir()

	corrupted := filepath.Join(dir, "corrupted.json")
	if err := os.WriteFile(corrupted, []byte("{not valid json"), 0644); err != nil {
		t.Fatalf("写入损坏文件失败: %v", err)
	}
	// 应退化为空缓存而非 panic
	if c := NewDeprecatedParamCacheWithPersistence(corrupted); c.Size() != 0 {
		t.Errorf("损坏文件应退化为空缓存，Size() = %d", c.Size())
	}

	// 文件不存在：首次启动的正常路径
	missing := filepath.Join(dir, "sub", "missing.json")
	c := NewDeprecatedParamCacheWithPersistence(missing)
	if c.Size() != 0 {
		t.Errorf("缺失文件应为空缓存，Size() = %d", c.Size())
	}
	// 目录不存在也应能落盘（MkdirAll）
	c.Record("ch1", "k1", "model-a", "temperature")
	if _, err := os.Stat(missing); err != nil {
		t.Errorf("应自动创建目录并落盘: %v", err)
	}
}

// TestDeprecatedParamCacheInMemoryNoFile 验证纯内存实例不产生文件
func TestDeprecatedParamCacheInMemoryNoFile(t *testing.T) {
	c := NewDeprecatedParamCache()
	c.Record("ch1", "k1", "model-a", "temperature")

	if err := c.Flush(); err != nil {
		t.Errorf("纯内存实例 Flush 应为空操作，实际 err=%v", err)
	}
	if params := c.Params("ch1", "k1", "model-a"); len(params) != 1 {
		t.Errorf("纯内存实例仍应正常记忆，实际 %v", params)
	}
}
