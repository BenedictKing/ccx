package config

import (
	"testing"
)

func TestAutopilotRoutingConfig_IsAFPCostRoutingEnabled(t *testing.T) {
	for _, legacyMode := range []string{"off", "shadow", "assist", "auto", "active"} {
		c := &AutopilotRoutingConfig{RoutingMode: legacyMode}
		if !c.IsAFPCostRoutingEnabled() {
			t.Fatalf("legacy mode=%q 不应关闭 AFP 成本路由", legacyMode)
		}
	}
}

func TestAutopilotRoutingConfig_AFPDefaultEnabled(t *testing.T) {
	if !DefaultAutopilotRoutingConfig().IsAFPCostRoutingEnabled() {
		t.Fatal("Autopilot 唯一运行态应默认启用 AFP 成本路由")
	}
}

func TestAutopilotRoutingConfig_IndependentSwitches(t *testing.T) {
	// Frontier/Ladder 已默认启用（frontierRoutingEnabled 字段废弃仅为 JSON 兼容），
	// afpCostRoutingEnabled 同样默认开启，二者互不影响。
	c := &AutopilotRoutingConfig{
		RoutingMode:            "auto",
		FrontierRoutingEnabled: false,
	}
	if !c.IsAFPCostRoutingEnabled() {
		t.Fatal("AFPCostRoutingEnabled should be true by default")
	}
}
