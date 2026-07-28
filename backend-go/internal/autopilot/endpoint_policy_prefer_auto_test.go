package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// TestResolveMappedModel_PreferAutoOverManual 覆盖 Goal A 的优先级反转开关：
//   - PreferAutoOverManual=false（默认）：手动映射优先，保持历史行为不变。
//   - PreferAutoOverManual=true：ModelResolver 自动决策优先。
//   - PreferAutoOverManual=true 但自动决策 fail-open（未命中）：回退到手动映射，
//     Reason 为 "manual_mapping_fallback"。
func TestResolveMappedModel_PreferAutoOverManual(t *testing.T) {
	const (
		channelUID   = "ch_prefer_auto_test"
		requestModel = "claude-sonnet-5"
		manualModel  = "manual-mapped-model"
		autoModel    = "auto-mapped-model"
	)

	baseProfile := func() *KeyEndpointProfile {
		return &KeyEndpointProfile{
			ChannelUID:  channelUID,
			ChannelKind: "messages",
			MetricsKey:  "metrics_prefer_auto_test",
			ModelMapping: map[string]string{
				requestModel: manualModel,
			},
		}
	}

	req := &RequestProfile{Model: requestModel, ChannelKind: "messages"}

	// 自动决策能命中的 resolver：ModelProfileStore 中存在与请求模型精确匹配的画像。
	resolvableResolver := newTestResolver(t, []ModelProfile{
		{
			ChannelUID:   channelUID,
			ChannelKind:  "messages",
			MetricsKey:   "metrics_prefer_auto_test",
			ModelID:      autoModel,
			ModelFamily:  ModelFamilyClaude,
			QualityTier:  QualityTierHigh,
			ProbeSuccess: true,
		},
	})

	// 自动决策 fail-open 的 resolver：ModelProfileStore 中没有任何画像。
	failOpenResolver := newTestResolver(t, nil)

	makeRoutingCfg := func(preferAuto bool) config.AutopilotRoutingConfig {
		cfg := config.DefaultAutopilotRoutingConfig()
		cfg.RoutingMode = config.AutopilotModeAuto
		cfg.ModelMapping.AutoResolve = true
		cfg.ModelMapping.PreferAutoOverManual = preferAuto
		return cfg
	}

	cases := []struct {
		name           string
		preferAuto     bool
		resolver       *ModelResolver
		wantModel      string
		wantReason     string
		wantEffortSeen bool // 是否期望 EffortDecided 可能为 true（仅自动决策路径）
	}{
		{
			name:       "prefer_auto_false_manual_first",
			preferAuto: false,
			resolver:   resolvableResolver,
			wantModel:  manualModel,
			wantReason: "manual_mapping",
		},
		{
			name:       "prefer_auto_true_auto_wins",
			preferAuto: true,
			resolver:   resolvableResolver,
			wantModel:  autoModel,
		},
		{
			name:       "prefer_auto_true_auto_fails_open_falls_back_to_manual",
			preferAuto: true,
			resolver:   failOpenResolver,
			wantModel:  manualModel,
			wantReason: "manual_mapping_fallback",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			routingCfg := makeRoutingCfg(tc.preferAuto)
			deps := &EndpointPolicyDeps{
				ModelResolver: tc.resolver,
				GetRoutingCfg: func() config.AutopilotRoutingConfig { return routingCfg },
			}

			got := resolveMappedModel(baseProfile(), requestModel, req, deps)
			if got == nil {
				t.Fatalf("resolveMappedModel() = nil, want non-nil target")
			}
			if got.Model != tc.wantModel {
				t.Fatalf("Model = %q, want %q (reason=%q)", got.Model, tc.wantModel, got.Reason)
			}
			if tc.wantReason != "" && got.Reason != tc.wantReason {
				t.Fatalf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
			if tc.wantReason != "" {
				// 手动映射路径永远不决定 effort。
				if got.EffortDecided {
					t.Fatalf("EffortDecided = true, want false for manual mapping path")
				}
			}
		})
	}
}

// TestResolveMappedModel_PreferAutoOverManual_DefaultFalse 确认结构体默认值为 false，
// 保证未显式配置时行为与历史一致。
func TestResolveMappedModel_PreferAutoOverManual_DefaultFalse(t *testing.T) {
	cfg := config.DefaultAutopilotRoutingConfig()
	if cfg.ModelMapping.PreferAutoOverManual {
		t.Fatalf("DefaultAutopilotRoutingConfig().ModelMapping.PreferAutoOverManual = true, want false")
	}
}
