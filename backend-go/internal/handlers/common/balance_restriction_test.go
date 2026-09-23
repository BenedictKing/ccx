package common

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

// newBalanceRestrictionManager 构造持有单渠道双 Key 的 ConfigManager，
// 返回随配置热更新的渠道视图（仅用于读取 AutoBlacklistBalance 开关）。
func newBalanceRestrictionManager(t *testing.T, autoBlacklist *bool) (*config.ConfigManager, *config.UpstreamConfig) {
	t.Helper()
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	cfg := config.Config{Upstream: []config.UpstreamConfig{{
		Name:                 "balance-channel",
		BaseURL:              "https://example.com",
		APIKeys:              []string{"sk-a", "sk-b"},
		ServiceType:          "claude",
		AutoBlacklistBalance: autoBlacklist,
	}}}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	cm, err := config.NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })
	snapshot := cm.GetConfig()
	return cm, &snapshot.Upstream[0]
}

func TestHandleBalanceClassKeyFailureRestrictsModelOnly(t *testing.T) {
	cm, upstream := newBalanceRestrictionManager(t, nil)

	if HandleBalanceClassKeyFailure(cm, upstream, "Messages", 0, "sk-a", "model-x",
		"insufficient_balance", "Budget pool quota has been exhausted", "") {
		t.Fatal("首个模型受限不应升级整 Key 拉黑")
	}

	got := cm.GetConfig().Upstream[0]
	if len(got.DisabledAPIKeys) != 0 {
		t.Fatalf("不应整 Key 拉黑, DisabledAPIKeys = %#v", got.DisabledAPIKeys)
	}
	if len(got.DisabledKeyModels) != 1 || got.DisabledKeyModels[0].Model != "model-x" {
		t.Fatalf("应写入 (Key,模型) 组合限制, got %#v", got.DisabledKeyModels)
	}
	if got.DisabledKeyModels[0].Reason != "insufficient_balance" {
		t.Fatalf("限制 reason = %q, want insufficient_balance", got.DisabledKeyModels[0].Reason)
	}
	// 同 Key 其他模型不受影响
	if cm.IsKeyModelDisabled("Messages", 0, "sk-a", "model-y") {
		t.Fatal("其他模型不应被连累")
	}
	if cm.GetConfig().Upstream[0].APIKeys[0] != "sk-a" {
		t.Fatal("Key 仍应留在活跃列表")
	}
}

func TestHandleBalanceClassKeyFailureEscalatesAfterThreshold(t *testing.T) {
	cm, upstream := newBalanceRestrictionManager(t, nil)

	for _, model := range []string{"model-x", "model-y"} {
		if HandleBalanceClassKeyFailure(cm, upstream, "Messages", 0, "sk-a", model,
			"insufficient_balance", "402", "") {
			t.Fatalf("模型 %s 受限不应触发升级", model)
		}
	}
	if !HandleBalanceClassKeyFailure(cm, upstream, "Messages", 0, "sk-a", "model-z",
		"insufficient_balance", "402", "") {
		t.Fatal("第三个不同模型受限应升级整 Key 拉黑")
	}

	got := cm.GetConfig().Upstream[0]
	if len(got.DisabledAPIKeys) != 1 || got.DisabledAPIKeys[0].Key != "sk-a" {
		t.Fatalf("升级后应整 Key 拉黑, DisabledAPIKeys = %#v", got.DisabledAPIKeys)
	}
	if got.DisabledAPIKeys[0].Reason != "insufficient_balance" {
		t.Fatalf("拉黑 reason = %q, want insufficient_balance", got.DisabledAPIKeys[0].Reason)
	}
	if got.DisabledAPIKeys[0].RecoverAt == "" {
		t.Fatal("余额类拉黑应带自动恢复时间")
	}
	if _, err := time.Parse(time.RFC3339, got.DisabledAPIKeys[0].RecoverAt); err != nil {
		t.Fatalf("RecoverAt 非法 RFC3339: %v", err)
	}
}

func TestHandleBalanceClassKeyFailureRespectsSwitchAndGuard(t *testing.T) {
	off := false
	cm, upstream := newBalanceRestrictionManager(t, &off)
	if HandleBalanceClassKeyFailure(cm, upstream, "Messages", 0, "sk-a", "model-x",
		"insufficient_balance", "402", "") {
		t.Fatal("开关关闭时不应有任何处置")
	}
	got := cm.GetConfig().Upstream[0]
	if len(got.DisabledKeyModels) != 0 || len(got.DisabledAPIKeys) != 0 {
		t.Fatalf("开关关闭时不应写入任何限制: models=%#v keys=%#v", got.DisabledKeyModels, got.DisabledAPIKeys)
	}

	on := true
	cm2, upstream2 := newBalanceRestrictionManager(t, &on)
	if HandleBalanceClassKeyFailure(cm2, upstream2, "Messages", 0, "sk-a", "",
		"insufficient_balance", "402", "") {
		t.Fatal("无模型名时不应处置")
	}
	got2 := cm2.GetConfig().Upstream[0]
	if len(got2.DisabledKeyModels) != 0 || len(got2.DisabledAPIKeys) != 0 {
		t.Fatalf("无模型名时不应写入任何限制: models=%#v keys=%#v", got2.DisabledKeyModels, got2.DisabledAPIKeys)
	}
}
