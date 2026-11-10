package config

import (
	"path/filepath"
	"testing"
)

func TestGroupModelDisableAndRestore(t *testing.T) {
	apiTypes := []string{"Messages", "Responses", "Gemini", "Chat", "Images", "Vectors"}
	for _, apiType := range apiTypes {
		t.Run(apiType, func(t *testing.T) {
			cm := &ConfigManager{configFile: filepath.Join(t.TempDir(), "config.json")}
			upstreams := cm.getUpstreamSliceLocked(apiType)
			*upstreams = []UpstreamConfig{{
				APIKeys: []string{"sk-a", "sk-b", "sk-other"},
				APIKeyConfigs: []APIKeyConfig{
					{Key: "sk-a", QuotaGroup: "vip"},
					{Key: "sk-b", QuotaGroup: "vip"},
					{Key: "sk-other", QuotaGroup: "basic"},
				},
			}}
			upstream := &(*upstreams)[0]

			quotaGroup, affected, err := cm.DisableGroupModel(apiType, 0, "sk-a", "Model-A")
			if err != nil {
				t.Fatalf("DisableGroupModel() error = %v", err)
			}
			if quotaGroup != "vip" || affected != 2 {
				t.Fatalf("quotaGroup = %q, affected = %d, want vip, 2", quotaGroup, affected)
			}
			if !upstream.IsGroupModelDisabled("sk-a", "vip", "model-a") || !upstream.IsGroupModelDisabled("sk-b", "vip", "MODEL-A") {
				t.Fatal("同组 Key 应共享模型限制")
			}
			if upstream.IsGroupModelDisabled("sk-other", "basic", "model-a") || upstream.IsGroupModelDisabled("sk-a", "vip", "model-b") {
				t.Fatal("其他分组或模型不应受影响")
			}

			_, _, err = cm.RestoreGroupModel(apiType, 0, "", "vip", "model-a")
			if err != nil {
				t.Fatalf("RestoreGroupModel() error = %v", err)
			}
			if len(upstream.DisabledGroupModels) != 0 {
				t.Fatalf("恢复后仍残留限制: %+v", upstream.DisabledGroupModels)
			}
		})
	}
}

func TestGroupModelWithoutQuotaGroupOnlyAffectsOneKey(t *testing.T) {
	cm := &ConfigManager{configFile: filepath.Join(t.TempDir(), "config.json")}
	cm.config.Upstream = []UpstreamConfig{{APIKeys: []string{"sk-a", "sk-b"}}}
	upstream := &cm.config.Upstream[0]

	quotaGroup, affected, err := cm.DisableGroupModel("Messages", 0, "sk-a", "model-a")
	if err != nil {
		t.Fatalf("DisableGroupModel() error = %v", err)
	}
	if quotaGroup != "" || affected != 1 {
		t.Fatalf("quotaGroup = %q, affected = %d, want empty, 1", quotaGroup, affected)
	}
	if !upstream.IsGroupModelDisabled("sk-a", "", "model-a") || upstream.IsGroupModelDisabled("sk-b", "", "model-a") {
		t.Fatal("空分组限制应只作用于目标 Key")
	}
	if _, _, err := cm.RestoreGroupModel("Messages", 0, "sk-a", "", "model-a"); err != nil {
		t.Fatalf("RestoreGroupModel() error = %v", err)
	}
	if upstream.IsGroupModelDisabled("sk-a", "", "model-a") {
		t.Fatal("单 Key 模型限制未恢复")
	}
}
