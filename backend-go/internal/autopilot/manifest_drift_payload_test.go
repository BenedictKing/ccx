package autopilot

import (
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/eventbus"
)

// manifest_drift 事件 payload 应携带回填上下文（baseURL/source/discoveredAt），
// 为后续「审核后回填注册表」保留所需信息；added/removed 原有字段保持不变。
func TestPublishManifestDriftIfNeededPayloadContext(t *testing.T) {
	bus := eventbus.NewBus()
	sub, unsubscribe := bus.Subscribe(eventbus.TypeManifestDrift)
	defer unsubscribe()

	runner := &AutoDiscoveryRunner{eventBus: bus}
	channelUID := "ch_drift_ctx_001"

	// 内置清单命中（火山套餐入口，messages 协议），发现清单与内置 ModelIDs
	// 有差异 → 发布 drift。
	channel := &config.UpstreamConfig{
		ChannelUID:  channelUID,
		ServiceType: "claude",
	}
	baseURL := "https://ark.cn-beijing.volces.com/api/coding"
	discovered := builtinDriftDiscoveredModels(t, channel, baseURL)

	runner.publishManifestDriftIfNeeded(channelUID, channel, baseURL, discovered)

	select {
	case ev := <-sub:
		if ev.Type != eventbus.TypeManifestDrift {
			t.Fatalf("Type = %q, want manifest_drift", ev.Type)
		}
		if ev.Subject != channelUID {
			t.Fatalf("Subject = %q, want %q", ev.Subject, channelUID)
		}
		baseURLOk, _ := ev.Payload["baseURL"].(string)
		if baseURLOk != baseURL {
			t.Fatalf("payload.baseURL = %q, want %q", baseURLOk, baseURL)
		}
		source, _ := ev.Payload["source"].(string)
		if source != ModelDiscoverySourceControlPlane {
			t.Fatalf("payload.source = %q, want %q", source, ModelDiscoverySourceControlPlane)
		}
		discoveredAt, _ := ev.Payload["discoveredAt"].(string)
		if parsed, err := time.Parse(time.RFC3339, discoveredAt); err != nil || time.Since(parsed) > time.Minute {
			t.Fatalf("payload.discoveredAt = %q, want fresh RFC3339", discoveredAt)
		}
		added, _ := ev.Payload["added"].([]string)
		if len(added) != 1 || added[0] != "drift-probe-new-model-xyz" {
			t.Fatalf("payload.added = %v, want [drift-probe-new-model-xyz]", added)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("应发布 manifest_drift 事件")
	}
}

// builtinDriftDiscoveredModels 返回一份与该渠道内置 manifest 存在差异的发现清单。
// 内置清单命中且差异非空是发布 drift 的前提；若内置条目变化导致全等，
// 这里给出一个必然新增的模型名保证差异存在。
func builtinDriftDiscoveredModels(t *testing.T, channel *config.UpstreamConfig, baseURL string) []string {
	t.Helper()
	manifest, ok := lookupDiscoveryBuiltinManifest(channel, baseURL)
	if !ok || len(manifest.ModelIDs) == 0 {
		t.Skip("未命中内置清单，跳过 payload 上下文断言")
	}
	discovered := append([]string{}, manifest.ModelIDs...)
	return append(discovered, "drift-probe-new-model-xyz")
}
