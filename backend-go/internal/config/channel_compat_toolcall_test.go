package config

import (
	"os"
	"testing"
	"time"
)

// IsToolCallUnsupportedForChannelModel 的查询口径：
// 渠道×模型命中、跨模型/跨渠道隔离、TTL 过期失效（docs/specs/tool-call-capability.md §4.1）。
func TestIsToolCallUnsupportedForChannelModel(t *testing.T) {
	cache := NewChannelCompatCache()

	if cache.IsToolCallUnsupportedForChannelModel("ch_a", "m1") {
		t.Fatal("无学习记录时应 fail-open 返回 false")
	}

	if !cache.Record("ch_a", "keyhash1", "m1", TraitNoToolCallSupport, true, CompatSourceProbe, "probe evidence") {
		t.Fatal("首次记录应返回 true")
	}

	if !cache.IsToolCallUnsupportedForChannelModel("ch_a", "m1") {
		t.Fatal("命中记录应返回 true")
	}
	if cache.IsToolCallUnsupportedForChannelModel("ch_a", "m2") {
		t.Fatal("同渠道其他模型不应命中")
	}
	if cache.IsToolCallUnsupportedForChannelModel("ch_b", "m1") {
		t.Fatal("其他渠道不应命中")
	}

	// 重复记录相同结论不视为新增
	if cache.Record("ch_a", "keyhash1", "m1", TraitNoToolCallSupport, true, CompatSourceProbe, "probe evidence") {
		t.Fatal("相同结论重复记录应返回 false")
	}
}

func TestIsToolCallUnsupportedForChannelModelTTL(t *testing.T) {
	// 通过落盘文件注入过期条目，验证 TTL 判定（无需等待真实 24h）。
	expired := time.Now().Add(-25 * time.Hour).Format(time.RFC3339Nano)
	path := t.TempDir() + "/compat.json"
	state := `{
		"ch_old:keyhash1:m1": {
			"traits": {"no_tool_call_support": {"enabled": true, "source": "probe", "evidence": "old", "learned_at": "` + expired + `"}},
			"detected_at": "` + expired + `"
		}
	}`
	if err := os.WriteFile(path, []byte(state), 0o600); err != nil {
		t.Fatalf("写入状态文件失败: %v", err)
	}

	cache := NewChannelCompatCacheWithPersistence(path)
	if cache.IsToolCallUnsupportedForChannelModel("ch_old", "m1") {
		t.Fatal("TTL 过期条目不应命中")
	}
}

func TestIsToolCallUnsupportedForChannelModelEmptyArgs(t *testing.T) {
	cache := NewChannelCompatCache()
	_ = cache.Record("ch_a", "k", "m1", TraitNoToolCallSupport, true, CompatSourceProbe, "e")
	if cache.IsToolCallUnsupportedForChannelModel("", "m1") || cache.IsToolCallUnsupportedForChannelModel("ch_a", "") {
		t.Fatal("空参数应直接返回 false")
	}
}
