package config

import "testing"

func TestResolveUpstreamCapabilityUsesMappedModel(t *testing.T) {
	upstream := &UpstreamConfig{
		ModelMapping: map[string]string{"sonnet": "vendor-actual"},
		ModelCapabilities: map[string]UpstreamModelCapability{
			"vendor-actual": {ContextWindowTokens: 256000, MaxOutputTokens: 32768},
		},
	}
	resolved := ResolveUpstreamCapability("sonnet", upstream, nil)
	if !resolved.Known || resolved.ActualModel != "vendor-actual" || resolved.Source != "channel" {
		t.Fatalf("ResolveUpstreamCapability() = %+v, want mapped channel capability", resolved)
	}
	if resolved.Capability.ContextWindowTokens != 256000 || resolved.Capability.MaxOutputTokens != 32768 {
		t.Fatalf("Capability = %+v, want target model limits", resolved.Capability)
	}
}

func TestResolveEmbeddingCapabilityUsesMappedModel(t *testing.T) {
	upstream := &UpstreamConfig{
		ModelMapping: map[string]string{"embed-alias": "embed-actual"},
		EmbeddingCapabilities: map[string]EmbeddingCapability{
			"embed-actual": {EmbeddingSpaceID: "vendor-space", Dimensions: 1024},
		},
	}
	resolved := ResolveEmbeddingCapability("embed-alias", upstream)
	if !resolved.Known || resolved.ActualModel != "embed-actual" || resolved.MatchedPattern != "embed-actual" {
		t.Fatalf("ResolveEmbeddingCapability() = %+v, want mapped embedding capability", resolved)
	}
	if resolved.Capability.EmbeddingSpaceID != "vendor-space" || resolved.Capability.Dimensions != 1024 {
		t.Fatalf("Capability = %+v, want target embedding metadata", resolved.Capability)
	}
}

func TestModelMappingCloneUpdateAndManagedStrip(t *testing.T) {
	upstream := &UpstreamConfig{ModelMapping: map[string]string{"sonnet": "actual"}}
	cloned := upstream.Clone()
	cloned.ModelMapping["sonnet"] = "changed"
	if upstream.ModelMapping["sonnet"] != "actual" {
		t.Fatal("Clone 应与原 ModelMapping 解耦")
	}

	updated := map[string]string{"opus": "actual-opus"}
	if _, err := applyUpstreamUpdateFields(upstream, UpstreamUpdate{ModelMapping: updated}); err != nil {
		t.Fatal(err)
	}
	if upstream.ModelMapping["opus"] != "actual-opus" {
		t.Fatalf("ModelMapping 更新失败: %v", upstream.ModelMapping)
	}
	if _, err := applyUpstreamUpdateFields(upstream, UpstreamUpdate{ModelMapping: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if len(upstream.ModelMapping) != 0 {
		t.Fatalf("空 ModelMapping 应清空配置: %v", upstream.ModelMapping)
	}

	managed := &UpstreamConfig{AutoManaged: true, AutoManagedKind: "new_api", ModelMapping: map[string]string{"a": "b"}}
	if !stripAutoManagedExplicitOverrides(managed) || len(managed.ModelMapping) != 0 {
		t.Fatalf("托管渠道显式映射应被清理: %v", managed.ModelMapping)
	}
}
