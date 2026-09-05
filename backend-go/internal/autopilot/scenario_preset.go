package autopilot

import (
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
)

// ── 任务场景预设 ──
//
// 场景预设把「质量档下限 + 默认价格偏好 + effort 展开范围 + 质量收益帽」打包成
// 用户可一键切换的组合，覆盖复杂度/TaskClass 的自动推断。
// 生效链：请求头 X-Routing-Scenario > 全局配置 scenario.mode > 自动推断（不命中）。
// 场景是显式用户意图：MinQualityTier 直接成为 QualityTarget（跳过 QualityNeed 钳制），
// 能力硬约束（vision/context/tools）不受影响——它们在 CapabilityFloor 的独立字段上。

// ScenarioModeAuto 表示不命中任何场景预设，维持自动推断。
const ScenarioModeAuto = "auto"

// ScenarioPreset 场景预设参数。
type ScenarioPreset struct {
	// Key 场景标识（daily_dev / hard_problem / background / batch_cheap）。
	Key string

	// MinQualityTier 场景质量档下限，直接作为 QualityTarget。
	MinQualityTier QualityTier

	// CostPreference 场景默认价格偏向（quality_first / balanced / cost_first）。
	// 仅作为默认值：请求头 X-Cost-Preference 或全局 costPreference.mode 显式设置时优先。
	CostPreference string

	// EffortFloor / EffortCeil effort 档展开范围（含端点）；空 = 该侧不限。
	EffortFloor EffortLevel
	EffortCeil  EffortLevel

	// QualityBenefitCap 质量收益帽：超过该档后质量不再加分。
	QualityBenefitCap QualityTier
	HasBenefitCap     bool
}

// builtinScenarioPresets 内置四场景。参数依据 benchmark 图表（pass@1 × 实测成本）
// 上的推荐点位校准：
//   - daily_dev    日常开发：均衡，质量下限 normal，medium 起步（排除低档浪费与顶配溢价）
//   - hard_problem 难题攻坚：质量优先，high 下限，高 effort 区间，不设收益帽
//   - background   后台自动化：成本优先但保证能干活（normal 下限），低 effort 起步
//   - batch_cheap  批量省钱：绝对便宜优先，normal 下限（effort 级实测达标即可），effort 封顶 medium
var builtinScenarioPresets = map[string]ScenarioPreset{
	"daily_dev": {
		Key:               "daily_dev",
		MinQualityTier:    QualityTierNormal,
		CostPreference:    "balanced",
		EffortFloor:       EffortMedium,
		QualityBenefitCap: QualityTierHigh,
		HasBenefitCap:     true,
	},
	"hard_problem": {
		Key:            "hard_problem",
		MinQualityTier: QualityTierHigh,
		CostPreference: "quality_first",
		EffortFloor:    EffortHigh,
	},
	"background": {
		Key:               "background",
		MinQualityTier:    QualityTierNormal,
		CostPreference:    "cost_first",
		EffortFloor:       EffortLow,
		QualityBenefitCap: QualityTierHigh,
		HasBenefitCap:     true,
	},
	"batch_cheap": {
		Key:               "batch_cheap",
		MinQualityTier:    QualityTierNormal,
		CostPreference:    "cost_first",
		EffortFloor:       EffortLow,
		EffortCeil:        EffortMedium,
		QualityBenefitCap: QualityTierHigh,
		HasBenefitCap:     true,
	},
}

// scenarioOverrideEnabled 判断请求头覆盖是否被配置允许（默认允许）。
func scenarioOverrideEnabled(cfg config.ScenarioRoutingConfig) bool {
	return cfg.HeaderOverrideEnabled == nil || *cfg.HeaderOverrideEnabled
}

// ResolveScenarioPreset 解析当前生效的场景预设。
// 优先级：headerScenario（配置允许且合法时）> cfg.Mode；解析结果为 auto 时不命中。
// 头值三种语义：合法场景覆盖全局；显式 "auto" 取消全局场景；非法值忽略（沿用全局）。
// 返回的预设已应用 cfg.Overrides 的参数覆盖。
func ResolveScenarioPreset(cfg config.ScenarioRoutingConfig, headerScenario string) (ScenarioPreset, bool) {
	mode := config.NormalizeScenarioMode(cfg.Mode)
	if headerScenario != "" && scenarioOverrideEnabled(cfg) {
		trimmed := strings.ToLower(strings.TrimSpace(headerScenario))
		switch normalized := config.NormalizeScenarioMode(headerScenario); {
		case trimmed == ScenarioModeAuto:
			mode = ScenarioModeAuto
		case normalized != ScenarioModeAuto:
			mode = normalized
		}
	}
	if mode == ScenarioModeAuto {
		return ScenarioPreset{}, false
	}

	preset, ok := builtinScenarioPresets[mode]
	if !ok {
		return ScenarioPreset{}, false
	}
	if override, ok := cfg.Overrides[preset.Key]; ok {
		applyScenarioOverride(&preset, override)
	}
	return preset, true
}

// applyScenarioOverride 把配置覆盖项合并进预设（空字段沿用内置值）。
func applyScenarioOverride(preset *ScenarioPreset, override config.ScenarioPresetOverride) {
	if tier := parseQualityTier(override.MinQualityTier); tier != "" {
		preset.MinQualityTier = tier
	}
	if override.CostPreference != "" {
		preset.CostPreference = config.NormalizeCostPreferenceMode(override.CostPreference)
	}
	if level := parseEffortLevel(override.EffortFloor); level != "" {
		preset.EffortFloor = level
	}
	if level := parseEffortLevel(override.EffortCeil); level != "" {
		preset.EffortCeil = level
	}
	switch override.QualityBenefitCap {
	case "":
		// 不覆盖
	case "none", "-":
		preset.HasBenefitCap = false
		preset.QualityBenefitCap = ""
	default:
		if tier := parseQualityTier(override.QualityBenefitCap); tier != "" {
			preset.QualityBenefitCap = tier
			preset.HasBenefitCap = true
		}
	}
}

// parseQualityTier 把字符串解析为 QualityTier（非法返回空）。
// avoid 有意不在此列：它是实测结果档而非路由目标，设为地板会排除所有候选。
func parseQualityTier(v string) QualityTier {
	switch QualityTier(v) {
	case QualityTierPremium, QualityTierHigh, QualityTierNormal, QualityTierLow:
		return QualityTier(v)
	default:
		return ""
	}
}

// parseEffortLevel 把字符串解析为 EffortLevel（非法返回空）。
func parseEffortLevel(v string) EffortLevel {
	switch EffortLevel(v) {
	case EffortOff, EffortMinimal, EffortLow, EffortMedium, EffortHigh, EffortXhigh, EffortMax, EffortUltra:
		return EffortLevel(v)
	default:
		return ""
	}
}

// EffortWithin 判断 effort 档是否落在预设区间内（含端点；未设边界视为不限）。
func (p ScenarioPreset) EffortWithin(e EffortLevel) bool {
	if p.EffortFloor != "" && EffortLevelOrdinal(e) < EffortLevelOrdinal(p.EffortFloor) {
		return false
	}
	if p.EffortCeil != "" && EffortLevelOrdinal(e) > EffortLevelOrdinal(p.EffortCeil) {
		return false
	}
	return true
}

// BuiltinScenarioPresets 返回内置场景预设（含配置覆盖后的视图），供 API 展示。
func BuiltinScenarioPresets(cfg config.ScenarioRoutingConfig) map[string]ScenarioPreset {
	out := make(map[string]ScenarioPreset, len(builtinScenarioPresets))
	for key, preset := range builtinScenarioPresets {
		if override, ok := cfg.Overrides[key]; ok {
			applyScenarioOverride(&preset, override)
		}
		out[key] = preset
	}
	return out
}
