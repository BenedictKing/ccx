package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChatAndGeminiCurrentUpstreamRequireActiveChannel(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"upstream": [],
		"chatUpstream": [
			{"name": "chat-suspended", "baseUrl": "https://chat-1.example.com", "apiKeys": ["chat-key-1"], "serviceType": "openai", "status": "suspended"},
			{"name": "chat-disabled", "baseUrl": "https://chat-2.example.com", "apiKeys": ["chat-key-2"], "serviceType": "openai", "status": "disabled"}
		],
		"geminiUpstream": [
			{"name": "gemini-suspended", "baseUrl": "https://gemini-1.example.com", "apiKeys": ["gemini-key-1"], "serviceType": "gemini", "status": "suspended"},
			{"name": "gemini-disabled", "baseUrl": "https://gemini-2.example.com", "apiKeys": ["gemini-key-2"], "serviceType": "gemini", "status": "disabled"}
		]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("初始化配置管理器失败: %v", err)
	}
	defer func() { _ = cm.Close() }()

	t.Run("chat current upstream returns explicit error", func(t *testing.T) {
		upstream, err := cm.GetCurrentChatUpstream()
		if err == nil {
			t.Fatal("期望 GetCurrentChatUpstream 返回错误")
		}
		if upstream != nil {
			t.Fatalf("期望无 active 渠道时不返回 upstream, 实际得到: %+v", upstream)
		}
		if !strings.Contains(err.Error(), "未找到 active 渠道") {
			t.Fatalf("错误信息 = %q, 期望包含未找到 active 渠道", err.Error())
		}
	})

	t.Run("chat current upstream with index returns explicit error", func(t *testing.T) {
		upstream, index, err := cm.GetCurrentChatUpstreamWithIndex()
		if err == nil {
			t.Fatal("期望 GetCurrentChatUpstreamWithIndex 返回错误")
		}
		if upstream != nil {
			t.Fatalf("期望无 active 渠道时不返回 upstream, 实际得到: %+v", upstream)
		}
		if index != -1 {
			t.Fatalf("index = %d, want -1", index)
		}
		if !strings.Contains(err.Error(), "未找到 active 渠道") {
			t.Fatalf("错误信息 = %q, 期望包含未找到 active 渠道", err.Error())
		}
	})

	t.Run("gemini current upstream returns explicit error", func(t *testing.T) {
		upstream, err := cm.GetCurrentGeminiUpstream()
		if err == nil {
			t.Fatal("期望 GetCurrentGeminiUpstream 返回错误")
		}
		if upstream != nil {
			t.Fatalf("期望无 active 渠道时不返回 upstream, 实际得到: %+v", upstream)
		}
		if !strings.Contains(err.Error(), "未找到 active 渠道") {
			t.Fatalf("错误信息 = %q, 期望包含未找到 active 渠道", err.Error())
		}
	})

	t.Run("gemini current upstream with index returns explicit error", func(t *testing.T) {
		upstream, index, err := cm.GetCurrentGeminiUpstreamWithIndex()
		if err == nil {
			t.Fatal("期望 GetCurrentGeminiUpstreamWithIndex 返回错误")
		}
		if upstream != nil {
			t.Fatalf("期望无 active 渠道时不返回 upstream, 实际得到: %+v", upstream)
		}
		if index != -1 {
			t.Fatalf("index = %d, want -1", index)
		}
		if !strings.Contains(err.Error(), "未找到 active 渠道") {
			t.Fatalf("错误信息 = %q, 期望包含未找到 active 渠道", err.Error())
		}
	})
}

func TestChatAndGeminiRenameRejectDuplicateNames(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"upstream": [],
		"chatUpstream": [
			{"name": "chat-primary", "baseUrl": "https://chat-1.example.com", "apiKeys": ["chat-key-1"], "serviceType": "openai"},
			{"name": "chat-secondary", "baseUrl": "https://chat-2.example.com", "apiKeys": ["chat-key-2"], "serviceType": "openai"}
		],
		"geminiUpstream": [
			{"name": "gemini-primary", "baseUrl": "https://gemini-1.example.com", "apiKeys": ["gemini-key-1"], "serviceType": "gemini"},
			{"name": "gemini-secondary", "baseUrl": "https://gemini-2.example.com", "apiKeys": ["gemini-key-2"], "serviceType": "gemini"}
		]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("初始化配置管理器失败: %v", err)
	}
	defer func() { _ = cm.Close() }()

	t.Run("chat rename duplicate is rejected", func(t *testing.T) {
		duplicateName := "chat-primary"
		_, err := cm.UpdateChatUpstream(1, UpstreamUpdate{Name: chatGeminiTestStrPtr(duplicateName)})
		if err == nil {
			t.Fatal("期望 UpdateChatUpstream 返回重名错误")
		}
		if !strings.Contains(err.Error(), "已存在") {
			t.Fatalf("错误信息 = %q, 期望包含已存在", err.Error())
		}

		cfg := cm.GetConfig()
		if cfg.ChatUpstream[1].Name != "chat-secondary" {
			t.Fatalf("重名更新失败后渠道名被意外修改: %q", cfg.ChatUpstream[1].Name)
		}
	})

	t.Run("gemini rename duplicate is rejected", func(t *testing.T) {
		duplicateName := "gemini-primary"
		_, err := cm.UpdateGeminiUpstream(1, UpstreamUpdate{Name: chatGeminiTestStrPtr(duplicateName)})
		if err == nil {
			t.Fatal("期望 UpdateGeminiUpstream 返回重名错误")
		}
		if !strings.Contains(err.Error(), "已存在") {
			t.Fatalf("错误信息 = %q, 期望包含已存在", err.Error())
		}

		cfg := cm.GetConfig()
		if cfg.GeminiUpstream[1].Name != "gemini-secondary" {
			t.Fatalf("重名更新失败后渠道名被意外修改: %q", cfg.GeminiUpstream[1].Name)
		}
	})
}

func chatGeminiTestStrPtr(value string) *string {
	return &value
}
