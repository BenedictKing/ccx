package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/errutil"
)

func newTempConfigManager(t *testing.T) *ConfigManager {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.json")
	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	t.Cleanup(func() {
		errutil.IgnoreDeferred(cm.Close)
	})
	return cm
}

func managedBoolPtr(v bool) *bool       { return &v }
func managedStringPtr(v string) *string { return &v }

// TestAddUpstream_StripsAutoManagedExplicitMappings 验证新建 AutoManaged 渠道时，
// 请求中携带的显式模型/推理映射与手工兼容开关会被清理，白名单类字段保留。
func TestAddUpstream_StripsAutoManagedExplicitMappings(t *testing.T) {
	cm := newTempConfigManager(t)
	trueValue := true

	err := cm.AddUpstream(UpstreamConfig{
		Name:                  "managed-channel",
		BaseURL:               "https://example.com",
		ServiceType:           "claude",
		ProviderID:            "glm",
		AutoManaged:           true,
		APIKeys:               []string{"sk-test"},
		ModelMapping:          map[string]string{"sonnet": "glm-5.2"},
		ReasoningMapping:      map[string]string{"sonnet": "high"},
		ReasoningParamStyle:   "thinking",
		FastMode:              true,
		CodexToolCompat:       &trueValue,
		StripCodexClientTools: true,
		SupportedModels:       []string{"glm-5.2"},
	})
	if err != nil {
		t.Fatalf("AddUpstream() error = %v", err)
	}

	stored := cm.GetConfig().Upstream[0]
	if len(stored.ModelMapping) != 0 || len(stored.ReasoningMapping) != 0 || stored.ReasoningParamStyle != "" || stored.FastMode {
		t.Fatalf("expected auto-managed explicit mapping stripped on add, got %+v", stored)
	}
	if stored.CodexToolCompat != nil || stored.StripCodexClientTools {
		t.Fatalf("expected auto-managed compat overrides stripped on add, got %+v", stored)
	}
	if len(stored.SupportedModels) != 1 || stored.SupportedModels[0] != "glm-5.2" {
		t.Fatalf("supportedModels should remain intact, got %+v", stored)
	}
}

func TestUpdateUpstream_StripsAutoManagedExplicitMappings(t *testing.T) {
	cm := newTempConfigManager(t)
	if err := cm.AddUpstream(UpstreamConfig{
		Name:            "managed-channel",
		BaseURL:         "https://example.com",
		ServiceType:     "claude",
		ProviderID:      "glm",
		AutoManaged:     true,
		APIKeys:         []string{"sk-test"},
		ModelMapping:    map[string]string{"legacy": "legacy-target"},
		SupportedModels: []string{"glm-5.2"},
	}); err != nil {
		t.Fatalf("AddUpstream() error = %v", err)
	}
	updates := UpstreamUpdate{
		ModelMapping:        map[string]string{"sonnet": "glm-5.2"},
		ReasoningMapping:    map[string]string{"sonnet": "high"},
		SupportedModels:     []string{"glm-5.2", "gpt-5.6-sol"},
		ReasoningParamStyle: managedStringPtr("thinking"),
		FastMode:            managedBoolPtr(true),
	}
	if _, err := cm.UpdateUpstream(0, updates); err != nil {
		t.Fatalf("UpdateUpstream() error = %v", err)
	}
	stored := cm.GetConfig().Upstream[0]
	if len(stored.ModelMapping) != 0 || len(stored.ReasoningMapping) != 0 || stored.ReasoningParamStyle != "" || stored.FastMode {
		t.Fatalf("expected auto-managed explicit mapping stripped on update, got %+v", stored)
	}
	if len(stored.SupportedModels) != 2 {
		t.Fatalf("supportedModels should remain after update, got %+v", stored.SupportedModels)
	}
}

func TestUpdateModelMapping_RejectsAutoManagedChannel(t *testing.T) {
	cm := newTempConfigManager(t)
	if err := cm.AddUpstream(UpstreamConfig{
		Name:         "managed-channel",
		BaseURL:      "https://example.com",
		ServiceType:  "claude",
		ProviderID:   "glm",
		AutoManaged:  true,
		APIKeys:      []string{"sk-test"},
		ModelMapping: map[string]string{"sonnet": "glm-5.2"},
	}); err != nil {
		t.Fatalf("AddUpstream() error = %v", err)
	}
	if err := cm.UpdateModelMapping(0, "sonnet", "glm-5.2", "high"); err == nil {
		t.Fatal("expected UpdateModelMapping to reject auto-managed channel")
	}
}

func TestAddUpstream_StripsDeprecatedGrokModelMapping(t *testing.T) {
	cm := newTempConfigManager(t)

	err := cm.AddUpstream(UpstreamConfig{
		Name:        "new-channel",
		BaseURL:     "https://example.com",
		ServiceType: "claude",
		APIKeys:     []string{"sk-test"},
		ModelMapping: map[string]string{
			"grok-4.1": "grok-4.1-thinking",
			"grok-4.2": "grok-4.20-beta",
			"opus":     "claude-3-7-sonnet",
		},
	})
	if err != nil {
		t.Fatalf("AddUpstream() error = %v", err)
	}

	mm := cm.GetConfig().Upstream[0].ModelMapping
	if _, ok := mm["grok-4.1"]; ok {
		t.Fatalf("expected deprecated grok-4.1 mapping stripped, got %v", mm)
	}
	if _, ok := mm["grok-4.2"]; ok {
		t.Fatalf("expected deprecated grok-4.2 mapping stripped, got %v", mm)
	}
	if mm["opus"] != "claude-3-7-sonnet" {
		t.Fatalf("expected unrelated mapping preserved, got %v", mm)
	}
}

// TestAddVectorsUpstream_StripsDeprecatedGrokModelMapping 验证新建 Vectors 渠道同样会清理。
func TestAddVectorsUpstream_StripsDeprecatedGrokModelMapping(t *testing.T) {
	cm := newTempConfigManager(t)

	err := cm.AddVectorsUpstream(UpstreamConfig{
		Name:        "new-vectors-channel",
		BaseURL:     "https://example.com",
		ServiceType: "openai",
		APIKeys:     []string{"sk-test"},
		ModelMapping: map[string]string{
			"grok-4.2": "grok-4.20-beta",
		},
	})
	if err != nil {
		t.Fatalf("AddVectorsUpstream() error = %v", err)
	}

	mm := cm.GetConfig().VectorsUpstream[0].ModelMapping
	if len(mm) != 0 {
		t.Fatalf("expected empty mapping after stripping deprecated pair, got %v", mm)
	}
}

// TestUpdateUpstream_FullReplace_StripsDeprecatedGrokModelMapping 验证整体更新提交的
// modelMapping 中过时 grok 精确映射会被剔除。
func TestUpdateUpstream_FullReplace_StripsDeprecatedGrokModelMapping(t *testing.T) {
	cm := newTempConfigManager(t)
	if err := cm.AddUpstream(UpstreamConfig{
		Name:        "test-channel",
		BaseURL:     "https://example.com",
		ServiceType: "claude",
		APIKeys:     []string{"sk-test"},
	}); err != nil {
		t.Fatalf("AddUpstream() error = %v", err)
	}

	newMapping := map[string]string{
		"grok-4.1": "grok-4.1-thinking",
		"gpt":      "gpt-5",
	}
	if _, err := cm.UpdateUpstream(0, UpstreamUpdate{ModelMapping: newMapping}); err != nil {
		t.Fatalf("UpdateUpstream() error = %v", err)
	}

	mm := cm.GetConfig().Upstream[0].ModelMapping
	if _, ok := mm["grok-4.1"]; ok {
		t.Fatalf("expected deprecated grok-4.1 mapping stripped, got %v", mm)
	}
	if mm["gpt"] != "gpt-5" {
		t.Fatalf("expected unrelated mapping preserved, got %v", mm)
	}
}

// TestUpdateUpstream_NameOnly_CleansExistingDeprecatedMapping 验证即使本次更新只改了
// 名称等字段，只要渠道原本带有历史遗留映射，也会一并清理并触发落盘。
func TestUpdateUpstream_NameOnly_CleansExistingDeprecatedMapping(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"upstream": [{
			"name": "legacy-channel",
			"baseUrl": "https://example.com",
			"apiKeys": ["sk-test"],
			"serviceType": "claude",
			"modelMapping": {
				"grok-4.1": "grok-4.1-thinking",
				"grok-4.2": "grok-4.20-beta",
				"opus": "claude-3-7-sonnet"
			}
		}]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	newName := "renamed-channel"
	if _, err := cm.UpdateUpstream(0, UpstreamUpdate{Name: &newName}); err != nil {
		t.Fatalf("UpdateUpstream() error = %v", err)
	}

	mm := cm.GetConfig().Upstream[0].ModelMapping
	if _, ok := mm["grok-4.1"]; ok {
		t.Fatalf("expected deprecated grok-4.1 mapping cleaned on name-only update, got %v", mm)
	}
	if _, ok := mm["grok-4.2"]; ok {
		t.Fatalf("expected deprecated grok-4.2 mapping cleaned on name-only update, got %v", mm)
	}
	if mm["opus"] != "claude-3-7-sonnet" {
		t.Fatalf("expected unrelated mapping preserved, got %v", mm)
	}

	persisted, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("读取持久化配置失败: %v", err)
	}
	if got := string(persisted); strings.Contains(got, "grok-4.1-thinking") || strings.Contains(got, "grok-4.20-beta") {
		t.Fatalf("expected deprecated mapping removed from disk, got %s", got)
	}
}

// TestUpdateModelMapping_StripsDeprecatedPairAfterWrite 验证单条映射更新接口写入
// 过时精确对时，同样会被清理。
func TestUpdateModelMapping_StripsDeprecatedPairAfterWrite(t *testing.T) {
	cm := newTempConfigManager(t)
	if err := cm.AddUpstream(UpstreamConfig{
		Name:        "test-channel",
		BaseURL:     "https://example.com",
		ServiceType: "claude",
		APIKeys:     []string{"sk-test"},
		ModelMapping: map[string]string{
			"grok-4.1": "placeholder",
		},
	}); err != nil {
		t.Fatalf("AddUpstream() error = %v", err)
	}

	if err := cm.UpdateModelMapping(0, "grok-4.1", "grok-4.1-thinking", ""); err != nil {
		t.Fatalf("UpdateModelMapping() error = %v", err)
	}

	mm := cm.GetConfig().Upstream[0].ModelMapping
	if _, ok := mm["grok-4.1"]; ok {
		t.Fatalf("expected deprecated grok-4.1 mapping stripped after single-mapping update, got %v", mm)
	}
}
