package handlers

import "testing"

func TestGetProbeModelsForCapabilityProtocol_UsesSourceProtocol(t *testing.T) {
	got, err := getProbeModelsForCapabilityProtocol("messages->chat")
	if err != nil {
		t.Fatalf("getProbeModelsForCapabilityProtocol() error = %v", err)
	}
	want, err := getCapabilityProbeModels("messages")
	if err != nil {
		t.Fatalf("getCapabilityProbeModels() error = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("models len=%d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("models[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}
