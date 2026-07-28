package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/stretchr/testify/assert"
)

// ── ResolveAdvisorHintEffect 表驱动测试（§4.7.2 执行规则）──

func TestResolveAdvisorHintEffect(t *testing.T) {
	// 辅助：构造 active 模式的有效配置
	activeCfg := func() config.TrustedRoutingAdvisorConfig {
		return config.TrustedRoutingAdvisorConfig{
			Enabled:                true,
			Mode:                   "active",
			MinAdvisorConfidence:   0.75,
			NeverDemoteTaskClasses: []string{"supervisor", "vision", "long_context"},
		}
	}

	// 辅助：构造有效 hint（lightweight，中等置信度）
	validHint := func() *TrustedRoutingHint {
		return &TrustedRoutingHint{
			TaskClass:               TaskClassLightweight,
			ComplexityTier:          "trivial",
			SuggestedMinQualityTier: QualityTierLow,
			AllowLocalCandidate:     true,
			Confidence:              0.85,
			Reasons:                 []string{"轻任务，优先本地/便宜渠道"},
		}
	}

	// 辅助：构造有效 hint（worker）
	workerHint := func() *TrustedRoutingHint {
		return &TrustedRoutingHint{
			TaskClass:               TaskClassWorker,
			ComplexityTier:          "routine",
			SuggestedMinQualityTier: QualityTierNormal,
			AllowLocalCandidate:     true,
			Confidence:              0.80,
			Reasons:                 []string{"子代理任务，可尝试性价比渠道"},
		}
	}

	// ── 默认配置（shadow 模式）─── 最关键的零变化不变量
	defaultCfg := config.DefaultAutopilotRoutingConfig()
	defaultAdCfg := defaultCfg.TrustedRoutingAdvisor

	tests := []struct {
		name      string
		hint      *TrustedRoutingHint
		cfg       config.TrustedRoutingAdvisorConfig
		taskClass TaskClass
		want      AdvisorHintEffect
	}{
		// ===== 最关键的零变化不变量 =====
		{
			name:      "默认配置（Mode=shadow）→ Applied=false（零变化不变量）",
			hint:      validHint(),
			cfg:       defaultAdCfg,
			taskClass: TaskClassLightweight,
			want: AdvisorHintEffect{
				Applied: false,
			},
		},
		{
			name: "Enabled=false → Applied=false",
			hint: validHint(),
			cfg: func() config.TrustedRoutingAdvisorConfig {
				c := activeCfg()
				c.Enabled = false
				return c
			}(),
			taskClass: TaskClassLightweight,
			want: AdvisorHintEffect{
				Applied: false,
			},
		},
		{
			name: "Mode=shadow → Applied=false",
			hint: validHint(),
			cfg: func() config.TrustedRoutingAdvisorConfig {
				c := activeCfg()
				c.Mode = "shadow"
				return c
			}(),
			taskClass: TaskClassLightweight,
			want: AdvisorHintEffect{
				Applied: false,
			},
		},
		{
			name: "Mode=disabled → Applied=false",
			hint: validHint(),
			cfg: func() config.TrustedRoutingAdvisorConfig {
				c := activeCfg()
				c.Mode = "disabled"
				return c
			}(),
			taskClass: TaskClassLightweight,
			want: AdvisorHintEffect{
				Applied: false,
			},
		},
		{
			name: "Mode=空字符串 → Applied=false",
			hint: validHint(),
			cfg: func() config.TrustedRoutingAdvisorConfig {
				c := activeCfg()
				c.Mode = ""
				return c
			}(),
			taskClass: TaskClassLightweight,
			want: AdvisorHintEffect{
				Applied: false,
			},
		},

		// ===== hint 为空或置信度不足 =====
		{
			name:      "hint=nil → Applied=false",
			hint:      nil,
			cfg:       activeCfg(),
			taskClass: TaskClassLightweight,
			want: AdvisorHintEffect{
				Applied: false,
			},
		},
		{
			name: "置信度不足 → Applied=false",
			hint: func() *TrustedRoutingHint {
				h := validHint()
				h.Confidence = 0.70 // 低于 activeCfg 的 MinAdvisorConfidence=0.75
				return h
			}(),
			cfg:       activeCfg(),
			taskClass: TaskClassLightweight,
			want: AdvisorHintEffect{
				Applied: false,
			},
		},
		{
			name: "置信度恰好等于阈值 → Applied=true",
			hint: func() *TrustedRoutingHint {
				h := validHint()
				h.Confidence = 0.75 // 等于 MinAdvisorConfidence
				return h
			}(),
			cfg:       activeCfg(),
			taskClass: TaskClassLightweight,
			want: AdvisorHintEffect{
				Applied:             true,
				MinQualityTier:      QualityTierLow,
				AllowLocalCandidate: true,
			},
		},

		// ===== taskClass 限制：只允许 lightweight/worker =====
		{
			name:      "taskClass=supervisor → Applied=false",
			hint:      validHint(),
			cfg:       activeCfg(),
			taskClass: TaskClassSupervisor,
			want: AdvisorHintEffect{
				Applied: false,
			},
		},
		{
			name:      "taskClass=vision → Applied=false",
			hint:      validHint(),
			cfg:       activeCfg(),
			taskClass: TaskClassVision,
			want: AdvisorHintEffect{
				Applied: false,
			},
		},
		{
			name:      "taskClass=long_context → Applied=false",
			hint:      validHint(),
			cfg:       activeCfg(),
			taskClass: TaskClassLongContext,
			want: AdvisorHintEffect{
				Applied: false,
			},
		},
		{
			name:      "taskClass=image_generation → Applied=false",
			hint:      validHint(),
			cfg:       activeCfg(),
			taskClass: TaskClassImageGen,
			want: AdvisorHintEffect{
				Applied: false,
			},
		},
		{
			name:      "taskClass=embedding → Applied=false",
			hint:      validHint(),
			cfg:       activeCfg(),
			taskClass: TaskClassEmbedding,
			want: AdvisorHintEffect{
				Applied: false,
			},
		},
		{
			name:      "taskClass=空字符串 → Applied=false",
			hint:      validHint(),
			cfg:       activeCfg(),
			taskClass: TaskClass(""),
			want: AdvisorHintEffect{
				Applied: false,
			},
		},

		// ===== NeverDemoteTaskClasses 双重保险 =====
		{
			name: "taskClass=lightweight 但手动加入 NeverDemoteTaskClasses → Applied=false",
			hint: validHint(),
			cfg: func() config.TrustedRoutingAdvisorConfig {
				c := activeCfg()
				c.NeverDemoteTaskClasses = append(c.NeverDemoteTaskClasses, "lightweight")
				return c
			}(),
			taskClass: TaskClassLightweight,
			want: AdvisorHintEffect{
				Applied: false,
			},
		},

		// ===== 正常生效路径 =====
		{
			name:      "lightweight + trivial → Applied=true, QualityTierLow",
			hint:      validHint(),
			cfg:       activeCfg(),
			taskClass: TaskClassLightweight,
			want: AdvisorHintEffect{
				Applied:             true,
				MinQualityTier:      QualityTierLow,
				AllowLocalCandidate: true,
			},
		},
		{
			name:      "worker + routine → Applied=true, QualityTierNormal",
			hint:      workerHint(),
			cfg:       activeCfg(),
			taskClass: TaskClassWorker,
			want: AdvisorHintEffect{
				Applied:             true,
				MinQualityTier:      QualityTierNormal,
				AllowLocalCandidate: true,
			},
		},

		// ===== ComplexityTier=="unknown" 强制升级 =====
		{
			name: "ComplexityTier=unknown → 强制 QualityTierHigh（即使 hint 建议 Low）",
			hint: func() *TrustedRoutingHint {
				h := validHint()
				h.ComplexityTier = "unknown"
				h.SuggestedMinQualityTier = QualityTierLow
				return h
			}(),
			cfg:       activeCfg(),
			taskClass: TaskClassLightweight,
			want: AdvisorHintEffect{
				Applied:             true,
				MinQualityTier:      QualityTierHigh,
				AllowLocalCandidate: true,
			},
		},
		{
			name: "ComplexityTier=unknown + SuggestedMinQualityTier=High → 保持 High",
			hint: func() *TrustedRoutingHint {
				h := validHint()
				h.ComplexityTier = "unknown"
				h.SuggestedMinQualityTier = QualityTierHigh
				return h
			}(),
			cfg:       activeCfg(),
			taskClass: TaskClassLightweight,
			want: AdvisorHintEffect{
				Applied:             true,
				MinQualityTier:      QualityTierHigh,
				AllowLocalCandidate: true,
			},
		},

		// ===== AllowLocalCandidate 透传 =====
		{
			name: "AllowLocalCandidate=false → 效果中为 false",
			hint: func() *TrustedRoutingHint {
				h := validHint()
				h.AllowLocalCandidate = false
				return h
			}(),
			cfg:       activeCfg(),
			taskClass: TaskClassLightweight,
			want: AdvisorHintEffect{
				Applied:             true,
				MinQualityTier:      QualityTierLow,
				AllowLocalCandidate: false,
			},
		},

		// ===== candidate 模式也应该生效 =====
		{
			name: "Mode=candidate → Applied=true",
			hint: validHint(),
			cfg: func() config.TrustedRoutingAdvisorConfig {
				c := activeCfg()
				c.Mode = "candidate"
				return c
			}(),
			taskClass: TaskClassLightweight,
			want: AdvisorHintEffect{
				Applied:             true,
				MinQualityTier:      QualityTierLow,
				AllowLocalCandidate: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveAdvisorHintEffect(tt.hint, tt.cfg, tt.taskClass)

			assert.Equal(t, tt.want.Applied, got.Applied, "Applied 不匹配")

			if tt.want.Applied {
				assert.Equal(t, tt.want.MinQualityTier, got.MinQualityTier, "MinQualityTier 不匹配")
				assert.Equal(t, tt.want.AllowLocalCandidate, got.AllowLocalCandidate, "AllowLocalCandidate 不匹配")
				assert.NotEmpty(t, got.Reasons, "生效时 Reasons 不应为空")
			}
		})
	}
}

// TestResolveAdvisorHintEffect_Reasons 测试 Reasons 搭载内容是否合理。
func TestResolveAdvisorHintEffect_Reasons(t *testing.T) {
	cfg := config.TrustedRoutingAdvisorConfig{
		Enabled:                true,
		Mode:                   "active",
		MinAdvisorConfidence:   0.70,
		NeverDemoteTaskClasses: []string{"supervisor", "vision", "long_context"},
	}

	t.Run("生效时包含 hint 原始 Reasons", func(t *testing.T) {
		hint := &TrustedRoutingHint{
			TaskClass:               TaskClassLightweight,
			ComplexityTier:          "trivial",
			SuggestedMinQualityTier: QualityTierLow,
			Confidence:              0.85,
			Reasons:                 []string{"原始原因1", "原始原因2"},
		}

		effect := ResolveAdvisorHintEffect(hint, cfg, TaskClassLightweight)
		assert.True(t, effect.Applied)
		assert.Len(t, effect.Reasons, 3) // 原始2 + 生效标记1
		assert.Contains(t, effect.Reasons[0], "原始原因1")
		assert.Contains(t, effect.Reasons[1], "原始原因2")
	})

	t.Run("不生效时 Reasons 包含拒因", func(t *testing.T) {
		effect := ResolveAdvisorHintEffect(nil, cfg, TaskClassLightweight)
		assert.False(t, effect.Applied)
		assert.NotEmpty(t, effect.Reasons)
		assert.Contains(t, effect.Reasons[0], "nil")
	})
}

// TestResolveAdvisorHintEffect_DefaultConfigInvariant 专门验证"默认配置→零变化"不变量。
// 这是最关键的安全性测试：不改配置时新逻辑必须零行为变化。
func TestResolveAdvisorHintEffect_DefaultConfigInvariant(t *testing.T) {
	cfg := config.DefaultAutopilotRoutingConfig()
	adCfg := cfg.TrustedRoutingAdvisor

	// 确认默认配置确实是 shadow 模式
	assert.Equal(t, "shadow", adCfg.Mode, "默认模式应为 shadow")

	// 对所有 TaskClass 种类进行穷举，确认默认配置下全部返回 Applied=false
	allTaskClasses := []TaskClass{
		TaskClassSupervisor,
		TaskClassWorker,
		TaskClassLightweight,
		TaskClassVision,
		TaskClassLongContext,
		TaskClassImageGen,
		TaskClassEmbedding,
		TaskClass("unknown_class"),
	}

	for _, tc := range allTaskClasses {
		t.Run("默认配置下 "+string(tc)+" → Applied=false", func(t *testing.T) {
			hint := &TrustedRoutingHint{
				TaskClass:               tc,
				ComplexityTier:          "routine",
				SuggestedMinQualityTier: QualityTierNormal,
				Confidence:              0.95,
				Reasons:                 []string{"test"},
			}
			effect := ResolveAdvisorHintEffect(hint, adCfg, tc)
			assert.False(t, effect.Applied,
				"默认配置（Mode=shadow）下 %s 不应生效", tc)
		})
	}
}

// TestStringInSlice 辅助函数单元测试。
func TestStringInSlice(t *testing.T) {
	tests := []struct {
		name   string
		list   []string
		target string
		want   bool
	}{
		{"空列表", []string{}, "a", false},
		{"nil 列表", nil, "a", false},
		{"存在", []string{"a", "b", "c"}, "b", true},
		{"不存在", []string{"a", "b", "c"}, "d", false},
		{"完全匹配", []string{"supervisor"}, "supervisor", true},
		{"部分匹配不等于存在", []string{"super"}, "supervisor", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringInSlice(tt.list, tt.target)
			assert.Equal(t, tt.want, got)
		})
	}
}
