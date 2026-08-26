package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConverterUpstreamCache_MarkAndIsConverter(t *testing.T) {
	c := NewConverterUpstreamCache()

	if c.IsConverter("ch-1") {
		t.Fatal("未学习的渠道不应被识别为转换层")
	}
	if !c.Mark("ch-1", "x-new-api-version") {
		t.Fatal("首次 Mark 应返回 isNew=true")
	}
	if !c.IsConverter("ch-1") {
		t.Fatal("Mark 后应识别为转换层")
	}
	if c.Mark("ch-1", "x-new-api-version") {
		t.Fatal("重复 Mark 应返回 isNew=false（仅续期）")
	}
	if c.Mark("", "x-new-api-version") {
		t.Fatal("空 channelUID 不应记录")
	}
	if c.IsConverter("") {
		t.Fatal("空 channelUID 不应命中")
	}
}

func TestConverterUpstreamCache_TTLExpiry(t *testing.T) {
	c := NewConverterUpstreamCache()
	c.Mark("ch-1", "x-oneapi-request-id")

	// 手工把条目时间戳改成 TTL 之前，验证惰性过期
	c.mu.Lock()
	c.cache["ch-1"].UpdatedAt = time.Now().Add(-converterUpstreamTTL - time.Hour)
	c.mu.Unlock()

	if c.IsConverter("ch-1") {
		t.Fatal("过期条目不应命中")
	}

	// 重新 Mark 后复活
	if !c.Mark("ch-1", "x-new-api-version") {
		t.Fatal("过期后重新 Mark 应视为新增")
	}
	if !c.IsConverter("ch-1") {
		t.Fatal("重新 Mark 后应再次命中")
	}
}

func TestConverterUpstreamCache_Persistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "converter_upstreams.json")

	c1 := NewConverterUpstreamCacheWithPersistence(path)
	c1.Mark("ch-1", "x-new-api-version")
	c1.Mark("ch-2", "x-oneapi-request-id")

	// 新实例从磁盘加载
	c2 := NewConverterUpstreamCacheWithPersistence(path)
	if !c2.IsConverter("ch-1") || !c2.IsConverter("ch-2") {
		t.Fatalf("落盘后再加载应保留记忆: size=%d", c2.Size())
	}

	// 过期条目不落盘：构造一个过期条目后 Flush，再加载应为空
	c3 := NewConverterUpstreamCacheWithPersistence(path)
	c3.Mark("ch-3", "x-new-api-version")
	c3.mu.Lock()
	c3.cache["ch-3"].UpdatedAt = time.Now().Add(-converterUpstreamTTL - time.Hour)
	c3.dirty = true // 手工改时间戳后需要重新标记，否则 Flush 是空操作
	c3.mu.Unlock()
	if err := c3.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	c4 := NewConverterUpstreamCacheWithPersistence(path)
	if c4.IsConverter("ch-3") {
		t.Fatal("过期条目不应被加载")
	}
}

func TestConverterUpstreamCache_LoadCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "converter_upstreams.json")
	if err := os.WriteFile(path, []byte("{bad json"), 0644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	// 损坏文件不应 panic，退化为空缓存
	c := NewConverterUpstreamCacheWithPersistence(path)
	if c.Size() != 0 {
		t.Fatalf("损坏文件应退化为空缓存, size=%d", c.Size())
	}
}
