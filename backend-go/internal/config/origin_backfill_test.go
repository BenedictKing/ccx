package config

import (
	"encoding/json"
	"github.com/BenedictKing/ccx/internal/errutil"
	"os"
	"path/filepath"
	"testing"
)

// TestOriginBackfill_FillsUnknownOnMissing 测试缺失 originType/originTier 时补齐为 "unknown"
func TestOriginBackfill_FillsUnknownOnMissing(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.json")

	initial := `{"upstream":[{"baseUrl":"https://a.example.com","apiKeys":["k1"],"name":"ch1"}],"responsesUpstream":[],"geminiUpstream":[],"chatUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]}`
	if err := os.WriteFile(cfgFile, []byte(initial), 0600); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(cfgFile, "")
	if err != nil {
		t.Fatalf("NewConfigManager 失败: %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	cfg := cm.GetConfig()
	if len(cfg.Upstream) != 1 {
		t.Fatalf("期望 1 个 upstream，得到 %d", len(cfg.Upstream))
	}
	if cfg.Upstream[0].OriginType != "unknown" {
		t.Fatalf("OriginType 应补齐为 unknown, got %q", cfg.Upstream[0].OriginType)
	}
	if cfg.Upstream[0].OriginTier != "unknown" {
		t.Fatalf("OriginTier 应补齐为 unknown, got %q", cfg.Upstream[0].OriginTier)
	}
}

// TestOriginBackfill_PreservesExistingValues 测试已有非空值不被覆盖
func TestOriginBackfill_PreservesExistingValues(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.json")

	initial := `{"upstream":[{"baseUrl":"https://a.example.com","apiKeys":["k1"],"name":"ch1","originType":"official_api","originTier":"first"}],"responsesUpstream":[],"geminiUpstream":[],"chatUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]}`
	if err := os.WriteFile(cfgFile, []byte(initial), 0600); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(cfgFile, "")
	if err != nil {
		t.Fatalf("NewConfigManager 失败: %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	cfg := cm.GetConfig()
	if cfg.Upstream[0].OriginType != "official_api" {
		t.Fatalf("已有 OriginType 不应被覆盖: got %q", cfg.Upstream[0].OriginType)
	}
	if cfg.Upstream[0].OriginTier != "first" {
		t.Fatalf("已有 OriginTier 不应被覆盖: got %q", cfg.Upstream[0].OriginTier)
	}
}

// TestOriginBackfill_PartialFieldsOnlyFillsMissing 测试只设置了其中一个字段时，
// 只补齐缺失的那一个，已设置的不受影响
func TestOriginBackfill_PartialFieldsOnlyFillsMissing(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.json")

	initial := `{"upstream":[{"baseUrl":"https://a.example.com","apiKeys":["k1"],"name":"ch1","originType":"relay"}],"responsesUpstream":[],"geminiUpstream":[],"chatUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]}`
	if err := os.WriteFile(cfgFile, []byte(initial), 0600); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(cfgFile, "")
	if err != nil {
		t.Fatalf("NewConfigManager 失败: %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	cfg := cm.GetConfig()
	if cfg.Upstream[0].OriginType != "relay" {
		t.Fatalf("已有 OriginType 不应被覆盖: got %q", cfg.Upstream[0].OriginType)
	}
	if cfg.Upstream[0].OriginTier != "unknown" {
		t.Fatalf("缺失的 OriginTier 应补齐为 unknown, got %q", cfg.Upstream[0].OriginTier)
	}
}

// TestOriginBackfill_AllChannelKinds 测试全部六类渠道都覆盖了 origin backfill
func TestOriginBackfill_AllChannelKinds(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.json")

	initial := `{
		"upstream":[{"baseUrl":"https://msg.example.com","apiKeys":["k1"],"name":"msg"}],
		"responsesUpstream":[{"baseUrl":"https://resp.example.com","apiKeys":["k2"],"name":"resp"}],
		"geminiUpstream":[{"baseUrl":"https://gem.example.com","apiKeys":["k3"],"name":"gem"}],
		"chatUpstream":[{"baseUrl":"https://chat.example.com","apiKeys":["k4"],"name":"chat"}],
		"imagesUpstream":[{"baseUrl":"https://img.example.com","apiKeys":["k5"],"name":"img"}],
		"vectorsUpstream":[{"baseUrl":"https://vec.example.com","apiKeys":["k6"],"name":"vec"}]
	}`
	if err := os.WriteFile(cfgFile, []byte(initial), 0600); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(cfgFile, "")
	if err != nil {
		t.Fatalf("NewConfigManager 失败: %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	cfg := cm.GetConfig()

	type channelKind struct {
		name     string
		channels []UpstreamConfig
	}
	kinds := []channelKind{
		{"Messages", cfg.Upstream},
		{"Responses", cfg.ResponsesUpstream},
		{"Gemini", cfg.GeminiUpstream},
		{"Chat", cfg.ChatUpstream},
		{"Images", cfg.ImagesUpstream},
		{"Vectors", cfg.VectorsUpstream},
	}

	for _, k := range kinds {
		if len(k.channels) != 1 {
			t.Fatalf("%s: 期望 1 个渠道, got %d", k.name, len(k.channels))
		}
		if k.channels[0].OriginType != "unknown" {
			t.Fatalf("%s: OriginType 应补齐为 unknown, got %q", k.name, k.channels[0].OriginType)
		}
		if k.channels[0].OriginTier != "unknown" {
			t.Fatalf("%s: OriginTier 应补齐为 unknown, got %q", k.name, k.channels[0].OriginTier)
		}
	}
}

// TestOriginBackfill_PersistedAfterBackfill 测试补齐后持久化到文件
func TestOriginBackfill_PersistedAfterBackfill(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.json")

	initial := `{"upstream":[{"baseUrl":"https://a.example.com","apiKeys":["k1"],"name":"ch1"}],"responsesUpstream":[],"geminiUpstream":[],"chatUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]}`
	if err := os.WriteFile(cfgFile, []byte(initial), 0600); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(cfgFile, "")
	if err != nil {
		t.Fatalf("NewConfigManager 失败: %v", err)
	}
	_ = cm.Close()

	// 重新解析持久化后的配置文件，避免依赖 JSON 缩进/空格等格式细节
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatalf("读取配置文件失败: %v", err)
	}
	var persisted Config
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("解析持久化配置失败: %v", err)
	}
	// 波 3 后落盘只含 ChannelsV3（六数组不再持久化），在权威形态的协议成员上断言。
	if len(persisted.ChannelsV3) != 1 || len(persisted.ChannelsV3[0].Protocols) != 1 {
		t.Fatalf("期望持久化 1 个单协议渠道，得到 %+v", persisted.ChannelsV3)
	}
	member := persisted.ChannelsV3[0].Protocols[0].Upstream
	if member.OriginType != "unknown" {
		t.Fatalf("持久化文件中 OriginType 应为 unknown, got %q", member.OriginType)
	}
	if member.OriginTier != "unknown" {
		t.Fatalf("持久化文件中 OriginTier 应为 unknown, got %q", member.OriginTier)
	}
}

// TestOriginBackfill_DoesNotChangeChannelSelection 测试 backfill 不影响渠道选择结果
// （§12.2 P1.5 明确要求：backfill 时不改变原调度）
func TestOriginBackfill_DoesNotChangeChannelSelection(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.json")

	// 两个渠道，优先级不同，backfill 前后 active 渠道应保持一致
	initial := `{"upstream":[
		{"baseUrl":"https://a.example.com","apiKeys":["k1"],"name":"high-priority","priority":1,"status":"active"},
		{"baseUrl":"https://b.example.com","apiKeys":["k2"],"name":"low-priority","priority":2,"status":"active"}
	],"responsesUpstream":[],"geminiUpstream":[],"chatUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]}`
	if err := os.WriteFile(cfgFile, []byte(initial), 0600); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(cfgFile, "")
	if err != nil {
		t.Fatalf("NewConfigManager 失败: %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	cfg := cm.GetConfig()
	if len(cfg.Upstream) != 2 {
		t.Fatalf("期望 2 个 upstream，得到 %d", len(cfg.Upstream))
	}
	// backfill 只应新增 originType/originTier 字段，不应改变 priority/status/顺序
	if cfg.Upstream[0].Name != "high-priority" || cfg.Upstream[0].Priority != 1 || cfg.Upstream[0].Status != "active" {
		t.Fatalf("backfill 不应改变渠道 0 的调度相关字段: %+v", cfg.Upstream[0])
	}
	if cfg.Upstream[1].Name != "low-priority" || cfg.Upstream[1].Priority != 2 || cfg.Upstream[1].Status != "active" {
		t.Fatalf("backfill 不应改变渠道 1 的调度相关字段: %+v", cfg.Upstream[1])
	}
}
