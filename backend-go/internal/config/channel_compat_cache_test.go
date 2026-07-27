package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestChannelCompatCacheRecordAndQuery(t *testing.T) {
	c := NewChannelCompatCache()

	if _, ok := c.Trait("ch1", "kh1", "m1", TraitDowngradeDeveloperRole); ok {
		t.Fatal("空缓存不应命中")
	}

	if !c.Record("ch1", "kh1", "m1", TraitDowngradeDeveloperRole, true, CompatSourceErrorSignal, "unknown variant developer") {
		t.Fatal("首次记录应返回新增")
	}
	// 相同结论重复记录不应再触发重试
	if c.Record("ch1", "kh1", "m1", TraitDowngradeDeveloperRole, true, CompatSourceErrorSignal, "again") {
		t.Fatal("相同结论重复记录应返回 false，否则会死循环重试")
	}

	state, ok := c.Trait("ch1", "kh1", "m1", TraitDowngradeDeveloperRole)
	if !ok || !state.Enabled {
		t.Fatalf("应命中且结论为启用, got ok=%v state=%+v", ok, state)
	}
	if state.Source != CompatSourceErrorSignal {
		t.Errorf("Source = %q, want %q", state.Source, CompatSourceErrorSignal)
	}

	// 结论翻转应覆盖并视为新增
	if !c.Record("ch1", "kh1", "m1", TraitDowngradeDeveloperRole, false, CompatSourceProbe, "probe says supported") {
		t.Fatal("结论翻转应返回新增")
	}
	if state, _ := c.Trait("ch1", "kh1", "m1", TraitDowngradeDeveloperRole); state.Enabled {
		t.Error("翻转后结论应为不启用")
	}
}

func TestChannelCompatCacheDimensionIsolation(t *testing.T) {
	c := NewChannelCompatCache()
	c.Record("ch1", "kh1", "m1", TraitStripImageGenTool, true, CompatSourceErrorSignal, "e")

	// 学习结论不得外溢到同渠道的其他 Key / 模型
	if _, ok := c.Trait("ch1", "kh2", "m1", TraitStripImageGenTool); ok {
		t.Error("不同 keyHash 不应命中")
	}
	if _, ok := c.Trait("ch1", "kh1", "m2", TraitStripImageGenTool); ok {
		t.Error("不同模型不应命中")
	}
	if _, ok := c.Trait("ch2", "kh1", "m1", TraitStripImageGenTool); ok {
		t.Error("不同渠道不应命中")
	}
	// 同组合的其他 trait 也不应被误命中
	if _, ok := c.Trait("ch1", "kh1", "m1", TraitDowngradeDeveloperRole); ok {
		t.Error("未学习的 trait 不应命中")
	}
}

func TestChannelCompatCachePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channel_compat.json")

	first := NewChannelCompatCacheWithPersistence(path)
	first.Record("ch1", "kh1", "m1", TraitDowngradeDeveloperRole, true, CompatSourceErrorSignal, "evidence")

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("记录后应已落盘: %v", err)
	}

	// 重启后应免重学
	reloaded := NewChannelCompatCacheWithPersistence(path)
	state, ok := reloaded.Trait("ch1", "kh1", "m1", TraitDowngradeDeveloperRole)
	if !ok || !state.Enabled {
		t.Fatalf("重载后应命中已学结论, got ok=%v state=%+v", ok, state)
	}
	if state.Evidence != "evidence" {
		t.Errorf("Evidence = %q, want evidence", state.Evidence)
	}
}

func TestChannelCompatCacheCorruptedFileFailsOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "channel_compat.json")
	if err := os.WriteFile(path, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}

	// 损坏文件应退化为空缓存而非 panic / 阻断启动
	c := NewChannelCompatCacheWithPersistence(path)
	if c.Size() != 0 {
		t.Errorf("损坏文件应退化为空缓存, size = %d", c.Size())
	}
	if !c.Record("ch1", "kh1", "m1", TraitStripEmptyTextBlocks, true, CompatSourceProbe, "e") {
		t.Error("退化后仍应可正常记录")
	}
}

func TestChannelCompatCacheConcurrentAccess(t *testing.T) {
	c := NewChannelCompatCache()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.Record("ch1", "kh1", "m1", TraitDowngradeDeveloperRole, true, CompatSourceErrorSignal, "e")
			c.Trait("ch1", "kh1", "m1", TraitDowngradeDeveloperRole)
			c.MarkApplied("ch1", "kh1", "m1", TraitDowngradeDeveloperRole)
			c.EnabledTraitNames("ch1", "kh1", "m1")
		}(i)
	}
	wg.Wait()

	if _, ok := c.Trait("ch1", "kh1", "m1", TraitDowngradeDeveloperRole); !ok {
		t.Error("并发写入后应命中")
	}
}
