package autopilot

import (
	"fmt"

	"github.com/BenedictKing/ccx/internal/config"
)

// ── Advisor Hint 路由约束效果（§4.7.2 执行规则）──

// AdvisorHintEffect 描述 advisor hint 转化后的路由约束效果。
// 当 Applied=false 时调用方应忽略此效果，按默认路由。
type AdvisorHintEffect struct {
	Applied             bool        // 是否真的生效（false 时调用方应忽略，按默认路由）
	MinQualityTier      QualityTier // 生效时的质量下界；hint 不确定时取 QualityTierHigh（§4.7.3）
	AllowLocalCandidate bool        // 是否允许本地模型进入候选集
	Reasons             []string    // 生效/未生效的原因记录
}

// ResolveAdvisorHintEffect 根据 advisor hint + 配置 + 任务类别，判定 hint 是否真正生效及生效后的约束。
// cfg 传 config.TrustedRoutingAdvisorConfig（值传递，避免循环依赖）。
//
// 生效规则（严格按设计 §4.7.2 执行规则）：
//   - cfg.Enabled=false 或 cfg.Mode 为 "shadow"/"disabled"/"" → Applied=false
//   - hint 为 nil 或 hint.Confidence < cfg.MinAdvisorConfidence → Applied=false
//   - taskClass 不在允许范围（只允许 lightweight/worker） → Applied=false
//   - taskClass 的字符串值在 cfg.NeverDemoteTaskClasses 列表中 → Applied=false（双重保险）
//   - 以上都通过后 Applied=true；若 ComplexityTier=="unknown" 则强制 MinQualityTier=QualityTierHigh
func ResolveAdvisorHintEffect(
	hint *TrustedRoutingHint,
	cfg config.TrustedRoutingAdvisorConfig,
	taskClass TaskClass,
) AdvisorHintEffect {
	// ── 规则1：配置未启用或模式不允许真实生效 ──
	if !cfg.Enabled {
		return AdvisorHintEffect{
			Applied: false,
			Reasons: []string{"advisor 配置未启用 (Enabled=false)"},
		}
	}

	// TrustedRoutingAdvisor.Mode 是独立子开关；兼容旧配置中的 shadow/off。
	mode := cfg.Mode
	if mode == "shadow" || mode == "off" || mode == "disabled" || mode == "" {
		return AdvisorHintEffect{
			Applied: false,
			Reasons: []string{fmt.Sprintf("advisor 模式不允许真实生效 (Mode=%s)", mode)},
		}
	}

	// ── 规则2：hint 为空或置信度不足 ──
	if hint == nil {
		return AdvisorHintEffect{
			Applied: false,
			Reasons: []string{"advisor hint 为 nil"},
		}
	}

	if hint.Confidence < cfg.MinAdvisorConfidence {
		return AdvisorHintEffect{
			Applied: false,
			Reasons: []string{fmt.Sprintf(
				"advisor 置信度不足 (Confidence=%.2f < Min=%.2f)",
				hint.Confidence, cfg.MinAdvisorConfidence,
			)},
		}
	}

	// ── 规则3：只允许 lightweight/worker 低风险请求生效 ──
	tcStr := string(taskClass)
	switch taskClass {
	case TaskClassLightweight, TaskClassWorker:
		// 允许继续判定
	default:
		return AdvisorHintEffect{
			Applied: false,
			Reasons: []string{fmt.Sprintf(
				"任务类别 %s 不在允许范围 (仅 lightweight/worker)", tcStr,
			)},
		}
	}

	// ── 规则4：NeverDemoteTaskClasses 双重保险 ──
	// 即使当前只允许 lightweight/worker 且默认 NeverDemoteTaskClasses=[supervisor,vision,long_context]
	// 不会冲突，但写这层防御确保未来配置变更时仍有保护
	if stringInSlice(cfg.NeverDemoteTaskClasses, tcStr) {
		return AdvisorHintEffect{
			Applied: false,
			Reasons: []string{fmt.Sprintf(
				"任务类别 %s 在 NeverDemoteTaskClasses 保护列表中", tcStr,
			)},
		}
	}

	// ── 所有前置检查通过 → Applied=true ──
	effect := AdvisorHintEffect{
		Applied:             true,
		MinQualityTier:      hint.SuggestedMinQualityTier,
		AllowLocalCandidate: hint.AllowLocalCandidate,
		Reasons:             append([]string{}, hint.Reasons...), // 拷贝避免外部修改
	}

	effect.Reasons = append(effect.Reasons, fmt.Sprintf(
		"advisor hint 生效 (Confidence=%.2f, TaskClass=%s)", hint.Confidence, tcStr,
	))

	// ── §4.7.3：ComplexityTier=="unknown" → 强制升级到 QualityTierHigh ──
	// "不确定→直接升级到 high/premium"，忽略 hint.SuggestedMinQualityTier 更低的建议
	if hint.ComplexityTier == "unknown" {
		effect.MinQualityTier = QualityTierHigh
		effect.Reasons = append(effect.Reasons,
			"ComplexityTier=unknown，强制升级质量下界到 high",
		)
	}

	return effect
}

// stringInSlice 检查字符串切片中是否包含目标值。
// 本地私有辅助，不依赖 intent_matcher.go 的 containsString 避免跨组件耦合。
func stringInSlice(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}
