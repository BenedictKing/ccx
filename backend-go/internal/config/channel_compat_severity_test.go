package config

import (
	"testing"
)

// TraitNoSeverityClass 的记录/查询/翻转/清除口径（docs/specs/severity-class-capability.md）。
func TestSeverityClassTraitRecordAndLookup(t *testing.T) {
	cache := NewChannelCompatCache()

	if cache.IsSeverityClassUnsupportedForChannelModel("ch_a", "m1") {
		t.Fatal("无学习记录时应 fail-open 返回 false")
	}
	if !cache.Record("ch_a", "keyhash1", "m1", TraitNoSeverityClass, true, CompatSourceRuntimeSignal, "分类请求 2xx 完成但输出无标记") {
		t.Fatal("首次负结论应返回 true")
	}
	if !cache.IsSeverityClassUnsupportedForChannelModel("ch_a", "m1") {
		t.Fatal("命中负结论应返回 true")
	}
	if cache.IsSeverityClassUnsupportedForChannelModel("ch_a", "m2") || cache.IsSeverityClassUnsupportedForChannelModel("ch_b", "m1") {
		t.Fatal("跨模型/跨渠道应隔离")
	}
	if cache.IsToolCallUnsupportedForChannelModel("ch_a", "m1") {
		t.Fatal("trait 之间不应串扰")
	}

	// 相同结论重复记录不算新增
	if cache.Record("ch_a", "keyhash1", "m1", TraitNoSeverityClass, true, CompatSourceRuntimeSignal, "again") {
		t.Fatal("相同结论重复记录应返回 false")
	}
	// 能力确认翻转：enabled true -> false 视为新增，查询随之失效
	if !cache.Record("ch_a", "keyhash1", "m1", TraitNoSeverityClass, false, CompatSourceRuntimeSignal, "输出含标记，能力确认") {
		t.Fatal("结论翻转应返回 true")
	}
	if cache.IsSeverityClassUnsupportedForChannelModel("ch_a", "m1") {
		t.Fatal("翻转后不应再命中")
	}
}

func TestSeverityClassTraitClearAndSnapshot(t *testing.T) {
	cache := NewChannelCompatCache()
	cache.Record("ch_a", "kh", "m1", TraitNoSeverityClass, true, CompatSourceRuntimeSignal, "ev")
	cache.Record("ch_a", "kh", "m1", TraitNoToolCallSupport, true, CompatSourceProbe, "ev2")

	// 按 trait 清除只动目标 trait
	if removed := cache.ClearTrait(TraitNoSeverityClass); removed != 1 {
		t.Fatalf("应清除 1 条，got %d", removed)
	}
	if cache.IsSeverityClassUnsupportedForChannelModel("ch_a", "m1") {
		t.Fatal("清除后不应命中")
	}
	if !cache.IsToolCallUnsupportedForChannelModel("ch_a", "m1") {
		t.Fatal("其他 trait 不应被误清")
	}

	// Snapshot 反映现存事实
	cache.Record("ch_b", "kh", "m2", TraitNoSeverityClass, true, CompatSourceRuntimeSignal, "ev3")
	entries := cache.Snapshot()
	if len(entries) != 2 {
		t.Fatalf("快照应含 2 条记录，got %d", len(entries))
	}
	found := false
	for _, e := range entries {
		if e.ChannelUID == "ch_b" && e.Model == "m2" {
			if _, ok := e.Traits[string(TraitNoSeverityClass)]; ok {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("快照应包含 ch_b/m2 的 severity 负结论")
	}
}
