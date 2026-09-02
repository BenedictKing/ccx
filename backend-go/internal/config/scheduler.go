package config

import "sync/atomic"

// SchedulerConfig 调度软增强的运行时配置（config.json "scheduler" 段，可选）。
// 两项默认启用；字段缺省（nil）即取默认值，便于一键回退旧行为。
type SchedulerConfig struct {
	// PromptAffinityFallback 匿名请求用对话内容指纹（system + 首条 user 消息）
	// 作为 Trace 亲和的会话标识回退。默认 true。
	PromptAffinityFallback *bool `json:"promptAffinityFallback,omitempty"`
	// KeyAutoWeight per-key 滑窗自动权重：5 分钟窗口成功率 × 连续失败衰减，
	// 作为手控 weight 之外的软降权系数（硬隔离仍由熔断负责）。默认 true。
	KeyAutoWeight *bool `json:"keyAutoWeight,omitempty"`
}

// PromptAffinityFallbackEnabled 解析 promptAffinityFallback 生效值（nil → 默认开）。
func (s *SchedulerConfig) PromptAffinityFallbackEnabled() bool {
	if s == nil || s.PromptAffinityFallback == nil {
		return true
	}
	return *s.PromptAffinityFallback
}

// KeyAutoWeightEnabled 解析 keyAutoWeight 生效值（nil → 默认开）。
func (s *SchedulerConfig) KeyAutoWeightEnabled() bool {
	if s == nil || s.KeyAutoWeight == nil {
		return true
	}
	return *s.KeyAutoWeight
}

// runtimeSchedulerTuning 保存热更新后的生效值，供 handlers 在无 ConfigManager
// 引用的位置读取（与 runtimeTimeouts 同款模式：main.go 启动与热重载时写入）。
type schedulerTuning struct {
	promptAffinityFallback bool
	keyAutoWeight          bool
}

var runtimeSchedulerTuning atomic.Pointer[schedulerTuning]

// ApplySchedulerTuning 由 main.go 在启动与配置热重载时写入生效值。
func ApplySchedulerTuning(cfg SchedulerConfig) {
	runtimeSchedulerTuning.Store(&schedulerTuning{
		promptAffinityFallback: cfg.PromptAffinityFallbackEnabled(),
		keyAutoWeight:          cfg.KeyAutoWeightEnabled(),
	})
}

// RuntimeKeyAutoWeightEnabled 返回 keyAutoWeight 当前生效值（未初始化时默认开）。
func RuntimeKeyAutoWeightEnabled() bool {
	if current := runtimeSchedulerTuning.Load(); current != nil {
		return current.keyAutoWeight
	}
	return true
}
