package config

import (
	"testing"
)

func TestAutopilotRoutingConfig_IsFrontierRoutingEnabled(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		enabled  bool
		expected bool
	}{
		{"off", "off", true, false},
		{"shadow", "shadow", true, false},
		{"assist_enabled", "assist", true, true},
		{"auto_enabled", "auto", true, true},
		{"assist_disabled", "assist", false, false},
		{"auto_disabled", "auto", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &AutopilotRoutingConfig{
				RoutingMode:            tt.mode,
				FrontierRoutingEnabled: tt.enabled,
			}
			got := c.IsFrontierRoutingEnabled()
			if got != tt.expected {
				t.Fatalf("IsFrontierRoutingEnabled() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestAutopilotRoutingConfig_IsAFPCostRoutingEnabled(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		expected bool
	}{
		{"off", "off", false},
		{"shadow", "shadow", false},
		{"assist", "assist", true},
		{"auto", "auto", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &AutopilotRoutingConfig{RoutingMode: tt.mode}
			got := c.IsAFPCostRoutingEnabled()
			if got != tt.expected {
				t.Fatalf("IsAFPCostRoutingEnabled() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestAutopilotRoutingConfig_AFPDefaultEnabled(t *testing.T) {
	c := &AutopilotRoutingConfig{RoutingMode: "assist"}
	if !c.IsAFPCostRoutingEnabled() {
		t.Fatal("AFPCostRouting should be enabled by default in assist/auto mode")
	}
	if c.RoutingMode == "off" && c.IsAFPCostRoutingEnabled() {
		t.Fatal("AFPCostRouting should be disabled in off mode")
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