package config

import (
	"strings"
	"testing"
)

// TestUpdateModelMapping_Upsert 验证模型映射的 upsert 语义：键不存在时插入、存在时更新
func TestUpdateModelMapping_Upsert(t *testing.T) {
	cm := newTempConfigManager(t)
	if err := cm.AddUpstream(UpstreamConfig{
		Name:        "ch",
		BaseURL:     "https://api.example.com",
		ServiceType: "claude",
		APIKeys:     []string{"k1"},
	}); err != nil {
		t.Fatalf("AddUpstream() error = %v", err)
	}

	// 新增键（ModelMapping 为 nil 时初始化）
	if err := cm.UpdateModelMapping(0, "claude-opus-4-8", "kimi-k3", "high"); err != nil {
		t.Fatalf("新增映射失败: %v", err)
	}
	cfg := cm.GetConfig()
	if got := cfg.Upstream[0].ModelMapping["claude-opus-4-8"]; got != "kimi-k3" {
		t.Fatalf("ModelMapping[claude-opus-4-8] = %q, want kimi-k3", got)
	}
	if got := cfg.Upstream[0].ReasoningMapping["claude-opus-4-8"]; got != "high" {
		t.Fatalf("ReasoningMapping[claude-opus-4-8] = %q, want high", got)
	}

	// 更新已有键
	if err := cm.UpdateModelMapping(0, "claude-opus-4-8", "glm-5.3", ""); err != nil {
		t.Fatalf("更新映射失败: %v", err)
	}
	cfg = cm.GetConfig()
	if got := cfg.Upstream[0].ModelMapping["claude-opus-4-8"]; got != "glm-5.3" {
		t.Fatalf("更新后 ModelMapping[claude-opus-4-8] = %q, want glm-5.3", got)
	}
	// reasoning 为空时清除 ReasoningMapping 对应项
	if _, exists := cfg.Upstream[0].ReasoningMapping["claude-opus-4-8"]; exists {
		t.Fatal("reasoning 为空时应清除 ReasoningMapping 对应项")
	}
}

// TestUpdateModelMapping_UpsertGuards 验证 upsert 保留的护栏：AutoManaged 拒绝、reasoning 校验
func TestUpdateModelMapping_UpsertGuards(t *testing.T) {
	cm := newTempConfigManager(t)
	if err := cm.AddUpstream(UpstreamConfig{
		Name:        "managed",
		BaseURL:     "https://api.example.com",
		ServiceType: "claude",
		ProviderID:  "glm",
		AutoManaged: true,
		APIKeys:     []string{"k1"},
	}); err != nil {
		t.Fatalf("AddUpstream() error = %v", err)
	}
	if err := cm.AddUpstream(UpstreamConfig{
		Name:        "ch",
		BaseURL:     "https://api2.example.com",
		ServiceType: "claude",
		APIKeys:     []string{"k2"},
	}); err != nil {
		t.Fatalf("AddUpstream() error = %v", err)
	}

	// AddUpstream 会按 baseURL 归一生成名称并重排：api2（普通）在 index 0，api（AutoManaged）在 index 1
	if err := cm.UpdateModelMapping(1, "claude-opus-4-8", "kimi-k3", ""); err == nil || !strings.Contains(err.Error(), "自动托管") {
		t.Fatalf("AutoManaged 渠道应拒绝写入, err=%v", err)
	}
	if err := cm.UpdateModelMapping(0, "claude-opus-4-8", "kimi-k3", "bogus"); err == nil || !strings.Contains(err.Error(), "无效的 reasoning") {
		t.Fatalf("无效 reasoning 应拒绝, err=%v", err)
	}
}

// TestUpdateChatModelMapping_Upsert 验证同构方法（chat）同样支持新增键
func TestUpdateChatModelMapping_Upsert(t *testing.T) {
	cm := newTempConfigManager(t)
	if err := cm.AddChatUpstream(UpstreamConfig{
		Name:        "chat-ch",
		BaseURL:     "https://api.example.com",
		ServiceType: "openai",
		APIKeys:     []string{"k1"},
	}); err != nil {
		t.Fatalf("AddChatUpstream() error = %v", err)
	}

	if err := cm.UpdateChatModelMapping(0, "gpt-5.5", "kimi-k3", ""); err != nil {
		t.Fatalf("chat 新增映射失败: %v", err)
	}
	cfg := cm.GetConfig()
	if got := cfg.ChatUpstream[0].ModelMapping["gpt-5.5"]; got != "kimi-k3" {
		t.Fatalf("ChatUpstream ModelMapping[gpt-5.5] = %q, want kimi-k3", got)
	}
}
