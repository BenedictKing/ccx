package compression

import (
	"strings"
)

// CompressionLevel 压缩强度级别。
type CompressionLevel string

const (
	LevelMinimal    CompressionLevel = "minimal"    // 保守，保留更多上下文
	LevelStandard   CompressionLevel = "standard"   // 标准
	LevelAggressive CompressionLevel = "aggressive" // 激进，压得更狠
)

// Plan 描述当前请求的压缩计划。
type Plan struct {
	// Enabled 是否启用压缩
	Enabled bool
	// Level 压缩强度
	Level CompressionLevel
	// Source 启用来源（"global" / "channel" / "scenario_preset" / "opt_out"）
	Source string
	// MaxToolResults 最多处理多少条 tool_result 消息（超出跳过）
	MaxToolResults int
	// MaxBytesPerResult 单条 tool_result 最大处理字节数
	MaxBytesPerResult int
}

// DefaultPlan 返回默认关闭的计划。
func DefaultPlan() Plan {
	return Plan{
		Enabled:           false,
		Level:             LevelMinimal,
		Source:            "default_off",
		MaxToolResults:    50,
		MaxBytesPerResult: 256 * 1024, // 256KB
	}
}

// effectiveMaxLines 根据强度级别返回有效最大行数。
func (p Plan) effectiveMaxLines(base int) int {
	switch p.Level {
	case LevelAggressive:
		return base / 2
	case LevelMinimal:
		return int(float64(base) * 1.5)
	default: // standard
		return base
	}
}

// effectiveMaxChars 根据强度级别返回有效最大字符数。
func (p Plan) effectiveMaxChars(base int) int {
	switch p.Level {
	case LevelAggressive:
		return base / 2
	case LevelMinimal:
		return int(float64(base) * 1.5)
	default:
		return base
	}
}

// ResolvePlan 解析当前请求的压缩计划。
//
// 开关层级（优先级从高到低）：
//  1. 请求头 opt-out: x-ccx-compression: off → 关闭
//  2. 场景预设联动（batch_cheap 等价格敏感预设默认开）
//  3. 渠道级开关（暂未暴露配置，预留）
//  4. 全局默认关
func ResolvePlan(headerCompression string, scenarioKey string, globalEnabled bool, channelEnabled bool) Plan {
	plan := DefaultPlan()
	plan.Level = LevelStandard

	// 1. 请求头 opt-out
	if strings.EqualFold(strings.TrimSpace(headerCompression), "off") {
		plan.Enabled = false
		plan.Source = "opt_out_header"
		return plan
	}

	// 2. 场景预设：价格敏感预设默认开
	if isPriceSensitiveScenario(scenarioKey) {
		plan.Enabled = true
		plan.Source = "scenario_preset:" + scenarioKey
		plan.Level = LevelStandard
		return plan
	}

	// 3. 渠道级开关
	if channelEnabled {
		plan.Enabled = true
		plan.Source = "channel"
		return plan
	}

	// 4. 全局默认
	if globalEnabled {
		plan.Enabled = true
		plan.Source = "global"
	}

	return plan
}

// isPriceSensitiveScenario 判断场景预设是否价格敏感。
func isPriceSensitiveScenario(key string) bool {
	switch key {
	case "batch_cheap":
		return true
	default:
		return false
	}
}
