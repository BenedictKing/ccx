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
				RoutingMode:           tt.mode,
				AFPCostRoutingEnabled: tt.enabled,
			}
			got := c.IsAFPCostRoutingEnabled()
			if got != tt.expected {
				t.Fatalf("IsAFPCostRoutingEnabled() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestAutopilotRoutingConfig_DefaultBothDisabled(t *testing.T) {
	c := &AutopilotRoutingConfig{RoutingMode: "assist"}
	if c.IsFrontierRoutingEnabled() {
		t.Fatal("FrontierRoutingEnabled should be false by default")
	}
	if c.IsAFPCostRoutingEnabled() {
		t.Fatal("AFPCostRoutingEnabled should be false by default")
	}
}

func TestAutopilotRoutingConfig_IndependentSwitches(t *testing.T) {
	// frontierRoutingEnabled 关闭不阻止 afpCostRoutingEnabled
	c := &AutopilotRoutingConfig{
		RoutingMode:           "auto",
		FrontierRoutingEnabled: false,
		AFPCostRoutingEnabled:  true,
	}
	if c.IsFrontierRoutingEnabled() {
		t.Fatal("FrontierRoutingEnabled should be false")
	}
	if !c.IsAFPCostRoutingEnabled() {
		t.Fatal("AFPCostRoutingEnabled should be true independently")
	}

	// 反向：afpCostRoutingEnabled 关闭不阻止 frontierRoutingEnabled
	c2 := &AutopilotRoutingConfig{
		RoutingMode:            "auto",
		FrontierRoutingEnabled: true,
		AFPCostRoutingEnabled:  false,
	}
	if !c2.IsFrontierRoutingEnabled() {
		t.Fatal("FrontierRoutingEnabled should be true independently")
	}
	if c2.IsAFPCostRoutingEnabled() {
		t.Fatal("AFPCostRoutingEnabled should be false")
	}
}