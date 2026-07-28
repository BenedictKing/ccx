package config

import (
	"testing"
)

func TestAutopilotRoutingConfig_IsFrontierRoutingEnabled(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		c := &AutopilotRoutingConfig{RoutingMode: "shadow", FrontierRoutingEnabled: enabled}
		if got := c.IsFrontierRoutingEnabled(); got != enabled {
			t.Fatalf("IsFrontierRoutingEnabled() = %v, want %v", got, enabled)
		}
	}
}

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
	// frontierRoutingEnabled 关闭不阻止 afpCostRoutingEnabled（默认开启）
	c := &AutopilotRoutingConfig{
		RoutingMode:            "auto",
		FrontierRoutingEnabled: false,
	}
	if c.IsFrontierRoutingEnabled() {
		t.Fatal("FrontierRoutingEnabled should be false")
	}
	if !c.IsAFPCostRoutingEnabled() {
		t.Fatal("AFPCostRoutingEnabled should be true by default")
	}

	// 反向：afpCostRoutingEnabled 默认开启不阻止 frontierRoutingEnabled 关闭
	c2 := &AutopilotRoutingConfig{
		RoutingMode:            "auto",
		FrontierRoutingEnabled: true,
	}
	if !c2.IsFrontierRoutingEnabled() {
		t.Fatal("FrontierRoutingEnabled should be true")
	}
	if !c2.IsAFPCostRoutingEnabled() {
		t.Fatal("AFPCostRoutingEnabled should be true by default")
	}
}
