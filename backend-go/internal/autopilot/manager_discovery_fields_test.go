package autopilot

import (
	"testing"
	"time"
)

func TestCarryForwardDiscoveryFieldsPreservesProtocolInventory(t *testing.T) {
	discoveredAt := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	old := &KeyEndpointProfile{
		ProtocolModels:           map[string][]string{"responses": {"gpt-5.4"}},
		ProtocolModelsHash:       map[string]string{"responses": "hash"},
		ProtocolDiscoveredAt:     map[string]time.Time{"responses": discoveredAt},
		ProtocolDiscoverySource:  map[string]string{"responses": "models_api"},
		ProtocolDiscoveryMessage: map[string]string{"responses": "实时模型清单"},
		ProtocolDiscoveryError:   map[string]string{"gemini": "HTTP 404"},
	}
	current := &KeyEndpointProfile{}

	carryForwardDiscoveryFields(old, current)

	if len(current.ProtocolModels["responses"]) != 1 || current.ProtocolModelsHash["responses"] != "hash" {
		t.Fatalf("协议模型未保留: %+v", current)
	}
	if !current.ProtocolDiscoveredAt["responses"].Equal(discoveredAt) || current.ProtocolDiscoveryError["gemini"] == "" {
		t.Fatalf("协议发现元数据未保留: %+v", current)
	}
	current.ProtocolModels["responses"][0] = "changed"
	if old.ProtocolModels["responses"][0] != "gpt-5.4" {
		t.Fatal("协议模型 map 未深拷贝")
	}
}
