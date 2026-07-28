package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigMigratesMissingAutopilotBlock(t *testing.T) {
	configPath := writeAutopilotMigrationConfig(t, nil)

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	cm.CloseWatcher()

	cfg := cm.GetAutopilotRouting()
	assertCurrentAutopilotDefaults(t, cfg)

	persisted := readPersistedAutopilotConfig(t, configPath)
	assertCurrentAutopilotDefaults(t, persisted)
}

func TestLoadConfigUpgradesPartialAutopilotAndPreservesExplicitValues(t *testing.T) {
	legacy := json.RawMessage(`{
		"mode": "ASSIST",
		"modelFamilyPreference": {
			"enabled": false,
			"weight": 0,
			"globalOrder": []
		},
		"reasoningEffort": {
			"enabled": false,
			"perTaskClass": {}
		},
		"modelMapping": {
			"autoResolve": true
		},
		"trustedRoutingAdvisor": {
			"enabled": false
		}
	}`)
	configPath := writeAutopilotMigrationConfig(t, legacy)

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	cm.CloseWatcher()

	assertLegacyAutopilotOverrides(t, cm.GetAutopilotRouting())
	assertLegacyAutopilotOverrides(t, readPersistedAutopilotConfig(t, configPath))

	// schemaVersion 防止 omitempty 删除显式零值后，下次启动又把它们误判为缺失字段。
	reloaded, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("重新加载迁移后的配置失败: %v", err)
	}
	reloaded.CloseWatcher()
	assertLegacyAutopilotOverrides(t, reloaded.GetAutopilotRouting())
}

func TestLoadConfigDoesNotPersistAutopilotEnvKillSwitch(t *testing.T) {
	t.Setenv(autopilotKillSwitchEnv, "true")
	configPath := writeAutopilotMigrationConfig(t, json.RawMessage(`{"mode":"shadow"}`))

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	cm.CloseWatcher()

	runtimeCfg := cm.GetAutopilotRouting()
	if !runtimeCfg.KillSwitch || runtimeCfg.EffectiveRoutingMode() != "off" {
		t.Fatalf("运行态环境急停未生效: %+v", runtimeCfg)
	}
	if !cm.GetConfig().AutopilotRouting.KillSwitch {
		t.Fatal("GetConfig() 应返回已叠加环境急停的运行态快照")
	}

	persisted := readPersistedAutopilotConfig(t, configPath)
	if persisted.KillSwitch {
		t.Fatal("AUTOPILOT_KILL_SWITCH 不应写回 config.json")
	}
	if persisted.RoutingMode != AutopilotModeAuto {
		t.Fatalf("持久化 mode = %q, want %q", persisted.RoutingMode, AutopilotModeAuto)
	}

	setters := []struct {
		name string
		set  func() error
	}{
		{name: "cost preference", set: func() error {
			return cm.SetCostPreference(CostPreferenceConfig{Mode: "quality_first"})
		}},
		{name: "A/B test", set: func() error { return cm.SetABTestEnabled(true) }},
	}
	for _, setter := range setters {
		t.Run(setter.name, func(t *testing.T) {
			if err := setter.set(); err != nil {
				t.Fatalf("更新配置失败: %v", err)
			}
			if readPersistedAutopilotConfig(t, configPath).KillSwitch {
				t.Fatal("Set API 不应将环境急停写入 config.json")
			}
			if !cm.GetAutopilotRouting().KillSwitch {
				t.Fatal("Set API 后环境急停仍应在运行态生效")
			}
		})
	}
}

func TestLoadConfigFallsBackWhenAutopilotDecodeFails(t *testing.T) {
	configPath := writeAutopilotMigrationConfig(t, json.RawMessage(`{
		"schemaVersion": 0,
		"mode": {"invalid": true}
	}`))

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() 应回退 autopilot 配置，而不是失败: %v", err)
	}
	cm.CloseWatcher()

	assertCurrentAutopilotDefaults(t, cm.GetAutopilotRouting())
	assertCurrentAutopilotDefaults(t, readPersistedAutopilotConfig(t, configPath))
}

func TestOverlayJSONStructPreservesPointerStructDefaults(t *testing.T) {
	type nestedConfig struct {
		Enabled bool `json:"enabled"`
		Limit   int  `json:"limit"`
	}
	type pointerConfig struct {
		Nested *nestedConfig `json:"nested"`
	}

	cfg := pointerConfig{Nested: &nestedConfig{Enabled: true, Limit: 10}}
	if err := overlayJSONStruct(&cfg, []byte(`{"nested":{"enabled":false}}`)); err != nil {
		t.Fatalf("overlayJSONStruct() error = %v", err)
	}
	if cfg.Nested == nil || cfg.Nested.Enabled || cfg.Nested.Limit != 10 {
		t.Fatalf("pointer-to-struct overlay 未保留默认字段: %+v", cfg.Nested)
	}

	if err := overlayJSONStruct(&cfg, []byte(`{"nested":null}`)); err != nil {
		t.Fatalf("overlayJSONStruct(null) error = %v", err)
	}
	if cfg.Nested != nil {
		t.Fatalf("显式 null 应清空指针字段: %+v", cfg.Nested)
	}
}

func writeAutopilotMigrationConfig(t *testing.T, autopilot json.RawMessage) string {
	t.Helper()

	root := map[string]any{
		"upstream":          []any{},
		"responsesUpstream": []any{},
		"geminiUpstream":    []any{},
		"fuzzyModeEnabled":  true,
		"thinkingCache": map[string]any{
			"ttlHours": ThinkingCacheDefaultTTLHours,
		},
	}
	if autopilot != nil {
		var value any
		if err := json.Unmarshal(autopilot, &value); err != nil {
			t.Fatalf("解析测试 autopilot 配置失败: %v", err)
		}
		root["autopilot"] = value
	}

	data, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("序列化测试配置失败: %v", err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}
	return configPath
}

func readPersistedAutopilotConfig(t *testing.T, configPath string) AutopilotRoutingConfig {
	t.Helper()

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("读取迁移后配置失败: %v", err)
	}
	var persisted struct {
		Autopilot AutopilotRoutingConfig `json:"autopilot"`
	}
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("解析迁移后配置失败: %v", err)
	}
	return persisted.Autopilot
}

func assertCurrentAutopilotDefaults(t *testing.T, cfg AutopilotRoutingConfig) {
	t.Helper()
	if cfg.SchemaVersion != currentAutopilotConfigSchemaVersion {
		t.Fatalf("schemaVersion = %d, want %d", cfg.SchemaVersion, currentAutopilotConfigSchemaVersion)
	}
	if cfg.RoutingMode != AutopilotModeAuto {
		t.Fatalf("mode = %q, want %q", cfg.RoutingMode, AutopilotModeAuto)
	}
	if !cfg.HealthCheck.Enabled || !cfg.ModelMapping.CapabilityFloorEnabled {
		t.Fatalf("缺失当前默认能力配置: health=%v modelMapping=%v", cfg.HealthCheck, cfg.ModelMapping)
	}
	if cfg.SLORollback.ConsecutiveWindows != 3 || cfg.ABTest.ShadowCandidateCount != 1 {
		t.Fatalf("缺失新增子配置默认值: slo=%+v abTest=%+v", cfg.SLORollback, cfg.ABTest)
	}
	assertDeepSeekTimePricingDefault(t, cfg)
}

func assertLegacyAutopilotOverrides(t *testing.T, cfg AutopilotRoutingConfig) {
	t.Helper()
	if cfg.SchemaVersion != currentAutopilotConfigSchemaVersion {
		t.Fatalf("schemaVersion = %d, want %d", cfg.SchemaVersion, currentAutopilotConfigSchemaVersion)
	}
	if cfg.RoutingMode != AutopilotModeAuto {
		t.Fatalf("legacy mode 应迁移为 %q，实际 %q", AutopilotModeAuto, cfg.RoutingMode)
	}
	if cfg.ModelFamilyPreference.Enabled || cfg.ModelFamilyPreference.Weight != 0 || len(cfg.ModelFamilyPreference.GlobalOrder) != 0 {
		t.Fatalf("显式模型派系零值未保留: %+v", cfg.ModelFamilyPreference)
	}
	if cfg.ReasoningEffort.Enabled || len(cfg.ReasoningEffort.PerTaskClass) != 0 {
		t.Fatalf("显式 reasoningEffort 配置未保留: %+v", cfg.ReasoningEffort)
	}
	if !cfg.ModelMapping.AutoResolve || !cfg.ModelMapping.CapabilityFloorEnabled {
		t.Fatalf("modelMapping 显式值或新增默认值错误: %+v", cfg.ModelMapping)
	}
	if cfg.TrustedRoutingAdvisor.Enabled {
		t.Fatal("显式关闭 trustedRoutingAdvisor 未保留")
	}
	if !cfg.HealthCheck.Enabled || cfg.SLORollback.ConsecutiveWindows != 3 {
		t.Fatalf("缺失字段未升级到当前默认值: health=%+v slo=%+v", cfg.HealthCheck, cfg.SLORollback)
	}
	assertDeepSeekTimePricingDefault(t, cfg)
}

func assertDeepSeekTimePricingDefault(t *testing.T, cfg AutopilotRoutingConfig) {
	t.Helper()
	rule, ok := cfg.CostOptimization.ProviderTimePricing["deepseek"]
	if !ok || rule.EffectiveFrom != "2026-07-20T00:00:00+08:00" || rule.TimeZone != "Asia/Shanghai" ||
		rule.PeakMultiplier != 2 || len(rule.PeakWindows) != 2 {
		t.Fatalf("DeepSeek 峰谷定价默认值未迁移: %+v", rule)
	}
}
