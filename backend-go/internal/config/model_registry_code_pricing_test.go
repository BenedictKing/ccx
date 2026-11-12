package config

import "testing"

func TestKimiCodeAliasesDoNotInheritAPIPricing(t *testing.T) {
	for _, model := range []string{"k3", "k3[1m]", "k3-256k", "k3.1-pro", "k3.1-pro-highspeed", "kimi-for-coding", "kimi-for-coding-highspeed"} {
		capability := ResolveUpstreamCapability(model, nil, nil).Capability
		if capability.ContextWindowTokens == 0 || capability.Pricing != nil {
			t.Errorf("%s: window=%d pricing=%+v", model, capability.ContextWindowTokens, capability.Pricing)
		}
	}
}
