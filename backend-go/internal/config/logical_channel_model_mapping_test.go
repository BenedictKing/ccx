package config

import (
	"testing"
)

// 协议级模型映射回归（docs/design/protocol-model-mapping-plan.md 阶段 1）：
// 显式 modelMapping 按执行协议成员存储和生效，共用匹配算法，
// 入口协议不把映射广播到兄弟成员。

// createProtocolMappingFixture 创建一个 chat+responses 双协议成员的逻辑渠道，
// 两个成员对同名源模型 gpt-5 配置不同目标。
func createProtocolMappingFixture(t *testing.T) (*ConfigManager, *LogicalChannel) {
	t.Helper()
	cm := newLogicalTestCM(t, nil)
	logical, err := cm.CreateLogicalChannel(CreateLogicalChannelInput{
		Name:     "协议级映射",
		Kind:     LogicalChannelKindLLM,
		BaseURLs: []string{"https://mapping.test.com/v1"},
		Protocols: []CreateLogicalChannelProtocol{
			{
				Kind:         "chat",
				ServiceType:  "openai",
				APIKeys:      []string{"k1"},
				ModelMapping: map[string]string{"gpt-5": "chat-target"},
			},
			{
				Kind:         "responses",
				ServiceType:  "responses",
				APIKeys:      []string{"k1"},
				ModelMapping: map[string]string{"gpt-5": "responses-target"},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateLogicalChannel 失败: %v", err)
	}
	return cm, logical
}

// protocolChannelUID 返回逻辑渠道指定 kind 成员的 channelUID。
func protocolChannelUID(t *testing.T, logical *LogicalChannel, kind string) string {
	t.Helper()
	for _, p := range logical.Protocols {
		if p.Kind == kind {
			return p.ChannelUID
		}
	}
	t.Fatalf("逻辑渠道 %s 缺少 %s 协议成员", logical.LogicalChannelUID, kind)
	return ""
}

// TestLogicalChannelModelMapping_PerProtocolScoped 验证同名源模型在兄弟协议成员上
// 各自独立存储，RedirectModel 按执行成员取各自目标。
func TestLogicalChannelModelMapping_PerProtocolScoped(t *testing.T) {
	cm, logical := createProtocolMappingFixture(t)
	cfg := cm.GetConfig()
	chatUID := protocolChannelUID(t, logical, "chat")
	responsesUID := protocolChannelUID(t, logical, "responses")

	chatUp := getChannelByUID(&cfg, "chat", chatUID)
	responsesUp := getChannelByUID(&cfg, "responses", responsesUID)
	if chatUp == nil || responsesUp == nil {
		t.Fatalf("物理成员缺失: chat=%v responses=%v", chatUp != nil, responsesUp != nil)
	}

	if got := RedirectModel("gpt-5", chatUp); got != "chat-target" {
		t.Fatalf("chat 成员映射结果 = %q, 期望 chat-target", got)
	}
	if got := RedirectModel("gpt-5", responsesUp); got != "responses-target" {
		t.Fatalf("responses 成员映射结果 = %q, 期望 responses-target", got)
	}
	// 未命中时各自透传，不借用兄弟成员的映射。
	if got := RedirectModel("unknown-model", chatUp); got != "unknown-model" {
		t.Fatalf("chat 成员未命中应透传, 实际 %q", got)
	}
}

// TestUpdateLogicalChannel_ModelMappingSemantics 验证协议成员更新时
// modelMapping 的差量语义：未传保留、空对象清空、兄弟成员不受影响。
func TestUpdateLogicalChannel_ModelMappingSemantics(t *testing.T) {
	cm, logical := createProtocolMappingFixture(t)
	chatUID := protocolChannelUID(t, logical, "chat")
	responsesUID := protocolChannelUID(t, logical, "responses")

	// 1) 只更新 chat 成员映射，responses 兄弟成员保持不变。
	if _, err := cm.UpdateLogicalChannel(UpdateLogicalChannelInput{
		LogicalChannelUID: logical.LogicalChannelUID,
		Protocols: []UpdateLogicalChannelProtocol{
			{Kind: "chat", ModelMapping: map[string]string{"gpt-5": "chat-v2", "codex": "chat-codex"}},
		},
	}); err != nil {
		t.Fatalf("更新 chat 成员映射失败: %v", err)
	}
	cfg := cm.GetConfig()
	if got := RedirectModel("gpt-5", getChannelByUID(&cfg, "chat", chatUID)); got != "chat-v2" {
		t.Fatalf("chat 成员映射更新后 = %q, 期望 chat-v2", got)
	}
	if got := RedirectModel("gpt-5", getChannelByUID(&cfg, "responses", responsesUID)); got != "responses-target" {
		t.Fatalf("更新 chat 成员不应影响 responses 成员, 实际 %q", got)
	}

	// 2) 未传 modelMapping（nil）→ 保留现有映射。
	if _, err := cm.UpdateLogicalChannel(UpdateLogicalChannelInput{
		LogicalChannelUID: logical.LogicalChannelUID,
		Protocols: []UpdateLogicalChannelProtocol{
			{Kind: "chat", Priority: 5},
		},
	}); err != nil {
		t.Fatalf("未传映射的更新失败: %v", err)
	}
	cfg = cm.GetConfig()
	chatUp := getChannelByUID(&cfg, "chat", chatUID)
	if got := RedirectModel("codex", chatUp); got != "chat-codex" {
		t.Fatalf("未传 modelMapping 应保留映射, 实际 %q", got)
	}

	// 3) 空对象 {} → 显式清空该成员映射，兄弟成员仍保留。
	if _, err := cm.UpdateLogicalChannel(UpdateLogicalChannelInput{
		LogicalChannelUID: logical.LogicalChannelUID,
		Protocols: []UpdateLogicalChannelProtocol{
			{Kind: "chat", ModelMapping: map[string]string{}},
		},
	}); err != nil {
		t.Fatalf("清空 chat 成员映射失败: %v", err)
	}
	cfg = cm.GetConfig()
	chatUp = getChannelByUID(&cfg, "chat", chatUID)
	if len(chatUp.ModelMapping) != 0 {
		t.Fatalf("空对象应清空成员映射, 实际 %v", chatUp.ModelMapping)
	}
	if got := RedirectModel("gpt-5", getChannelByUID(&cfg, "responses", responsesUID)); got != "responses-target" {
		t.Fatalf("清空 chat 成员不应影响 responses 成员, 实际 %q", got)
	}
}

// TestLogicalChannelModelMapping_ReloadFromDisk 验证按协议成员的映射
// 落盘后重新加载仍按成员隔离。
func TestLogicalChannelModelMapping_ReloadFromDisk(t *testing.T) {
	cm, logical := createProtocolMappingFixture(t)
	chatUID := protocolChannelUID(t, logical, "chat")
	responsesUID := protocolChannelUID(t, logical, "responses")
	if _, err := cm.UpdateLogicalChannel(UpdateLogicalChannelInput{
		LogicalChannelUID: logical.LogicalChannelUID,
		Protocols: []UpdateLogicalChannelProtocol{
			{Kind: "chat", ModelMapping: map[string]string{"gpt-5": "chat-v2"}},
		},
	}); err != nil {
		t.Fatalf("更新 chat 成员映射失败: %v", err)
	}

	// 用同一配置文件重新加载，模拟服务重启。
	reloaded, err := NewConfigManager(cm.configFile, cm.backupDir)
	if err != nil {
		t.Fatalf("重新加载配置失败: %v", err)
	}
	t.Cleanup(func() { reloaded.Close() })
	rcfg := reloaded.GetConfig()
	if got := RedirectModel("gpt-5", getChannelByUID(&rcfg, "chat", chatUID)); got != "chat-v2" {
		t.Fatalf("重载后 chat 成员映射 = %q, 期望 chat-v2", got)
	}
	if got := RedirectModel("gpt-5", getChannelByUID(&rcfg, "responses", responsesUID)); got != "responses-target" {
		t.Fatalf("重载后 responses 成员映射 = %q, 期望 responses-target", got)
	}
}

// TestLogicalChannelModelMapping_AccountIsolation 验证不同逻辑渠道（账号）间
// 同名源模型的映射互不影响。
func TestLogicalChannelModelMapping_AccountIsolation(t *testing.T) {
	cm := newLogicalTestCM(t, nil)
	first, err := cm.CreateLogicalChannel(CreateLogicalChannelInput{
		Name:     "账号A",
		Kind:     LogicalChannelKindLLM,
		BaseURLs: []string{"https://a.test.com/v1"},
		Protocols: []CreateLogicalChannelProtocol{
			{Kind: "chat", ServiceType: "openai", APIKeys: []string{"k1"}, ModelMapping: map[string]string{"gpt-5": "a-target"}},
		},
	})
	if err != nil {
		t.Fatalf("创建账号A失败: %v", err)
	}
	second, err := cm.CreateLogicalChannel(CreateLogicalChannelInput{
		Name:     "账号B",
		Kind:     LogicalChannelKindLLM,
		BaseURLs: []string{"https://b.test.com/v1"},
		Protocols: []CreateLogicalChannelProtocol{
			{Kind: "chat", ServiceType: "openai", APIKeys: []string{"k2"}, ModelMapping: map[string]string{"gpt-5": "b-target"}},
		},
	})
	if err != nil {
		t.Fatalf("创建账号B失败: %v", err)
	}

	cfg := cm.GetConfig()
	firstUp := getChannelByUID(&cfg, "chat", protocolChannelUID(t, first, "chat"))
	secondUp := getChannelByUID(&cfg, "chat", protocolChannelUID(t, second, "chat"))
	if got := RedirectModel("gpt-5", firstUp); got != "a-target" {
		t.Fatalf("账号A映射结果 = %q, 期望 a-target", got)
	}
	if got := RedirectModel("gpt-5", secondUp); got != "b-target" {
		t.Fatalf("账号B映射结果 = %q, 期望 b-target", got)
	}

	// 更新账号B成员映射不影响账号A。
	if _, err := cm.UpdateLogicalChannel(UpdateLogicalChannelInput{
		LogicalChannelUID: second.LogicalChannelUID,
		Protocols: []UpdateLogicalChannelProtocol{
			{Kind: "chat", ModelMapping: map[string]string{"gpt-5": "b-target-v2"}},
		},
	}); err != nil {
		t.Fatalf("更新账号B映射失败: %v", err)
	}
	cfg = cm.GetConfig()
	if got := RedirectModel("gpt-5", getChannelByUID(&cfg, "chat", protocolChannelUID(t, first, "chat"))); got != "a-target" {
		t.Fatalf("更新账号B不应影响账号A, 实际 %q", got)
	}
	if got := RedirectModel("gpt-5", getChannelByUID(&cfg, "chat", protocolChannelUID(t, second, "chat"))); got != "b-target-v2" {
		t.Fatalf("账号B映射更新后 = %q, 期望 b-target-v2", got)
	}
}
