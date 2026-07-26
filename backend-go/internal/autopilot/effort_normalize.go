package autopilot

import "strings"

// effortOrdinal 定义 EffortLevel 到序数值的映射，用于计算距离。
var effortOrdinal = map[EffortLevel]int{
	EffortOff:     0,
	EffortMinimal: 1,
	EffortLow:     2,
	EffortMedium:  3,
	EffortHigh:    4,
	EffortMax:     5,
}

// NormalizeEffortLevel 将供应商特定的 effort 名称标准化为规范 EffortLevel 枚举。
// 大小写不敏感，自动 trim。空串和无法识别的值返回空串（fail-open，不猜测）。
func NormalizeEffortLevel(raw string) EffortLevel {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "none", "off", "disabled":
		return EffortOff
	case "minimal", "min":
		return EffortMinimal
	case "low":
		return EffortLow
	case "medium", "med", "default":
		return EffortMedium
	case "high":
		return EffortHigh
	case "xhigh", "ultra", "max":
		return EffortMax
	default:
		return ""
	}
}

// EffortLevelDistance 返回两个 EffortLevel 在序数轴上的绝对距离。
// off=0, minimal=1, low=2, medium=3, high=4, max=5。
// 任一参数为空或无法识别时返回 -1（表示无法计算距离）。
func EffortLevelDistance(a, b EffortLevel) int {
	oa, okA := effortOrdinal[a]
	ob, okB := effortOrdinal[b]
	if !okA || !okB {
		return -1
	}
	d := oa - ob
	if d < 0 {
		return -d
	}
	return d
}

// EffortFallbackConfidence 根据 effort 距离返回跨 effort 证据的置信度乘数。
// distance 0 → 1.0, distance 1 → 0.7, distance 2 → 0.4, distance 3+ → 0.2。
// distance < 0（无效）视为 distance 3+，返回 0.2。
func EffortFallbackConfidence(distance int) float64 {
	switch distance {
	case 0:
		return 1.0
	case 1:
		return 0.7
	case 2:
		return 0.4
	default:
		return 0.2
	}
}
