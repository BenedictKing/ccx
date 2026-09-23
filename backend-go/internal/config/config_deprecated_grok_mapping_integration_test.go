package config

import (
	"path/filepath"
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
// 请求中携带的手工兼容开关会被清理，白名单类字段保留。
// （显式模型/推理映射字段已随渠道级 ModelMapping/ReasoningMapping 退役。）
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
	if stored.ReasoningParamStyle != "" || stored.FastMode {
		t.Fatalf("expected auto-managed explicit overrides stripped on add, got %+v", stored)
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
		SupportedModels: []string{"glm-5.2"},
	}); err != nil {
		t.Fatalf("AddUpstream() error = %v", err)
	}
	updates := UpstreamUpdate{
		SupportedModels:     []string{"glm-5.2", "gpt-5.6-sol"},
		ReasoningParamStyle: managedStringPtr("thinking"),
		FastMode:            managedBoolPtr(true),
	}
	if _, err := cm.UpdateUpstream(0, updates); err != nil {
		t.Fatalf("UpdateUpstream() error = %v", err)
	}
	stored := cm.GetConfig().Upstream[0]
	if stored.ReasoningParamStyle != "" || stored.FastMode {
		t.Fatalf("expected auto-managed explicit overrides stripped on update, got %+v", stored)
	}
	if len(stored.SupportedModels) != 2 {
		t.Fatalf("supportedModels should remain after update, got %+v", stored.SupportedModels)
	}
}
