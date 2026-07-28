package autopilot

import (
	"log"

	"github.com/BenedictKing/ccx/internal/config"
)

// ── 本地运行时候选提供器（设计 §4.7 TrustedRoutingAdvisor 执行面） ──
//
// 从 LocalRuntimeStore 收集健康且符合配置门槛的本地运行时，
// 将它们转化为轻量候选条目供 SmartRouter 纳入候选池。
//
// 本模块是纯函数，不依赖 SmartRouter 内部类型（如 channelScoreEntry）。
// 集成 agent 负责将 LocalCandidateEntry 转换为 channelScoreEntry。

// LocalCandidateEntry 本地运行时转化后的候选条目（字段对齐 channelScoreEntry 的最小子集）。
type LocalCandidateEntry struct {
	RuntimeUID          string
	DisplayName         string
	SupportsVision      bool
	SupportsToolCalls   bool
	SupportsReasoning   bool
	ContextWindowTokens int
	EstimatedCost       float64 // 本地模型固定 0（无远程调用成本）
}

// CollectLocalCandidates 从 LocalRuntimeStore 收集可作为候选的本地运行时。
//
// 门槛过滤逻辑（design §4.7 + line 3522 "必须满足硬约束和 shadow 门槛"）：
//   - cfg.Enabled=false 或 cfg.Mode=="shadow"/"disabled"/"" → 返回空（默认配置下必须走这个分支）
//   - taskClass 不在 cfg.AllowLocalForTaskClasses 中 → 返回空
//   - taskClass 在 cfg.NeverDemoteTaskClasses 中 → 返回空（双重防御：设计 §4.7 NeverDemoteTaskClasses）
//   - 只返回 status==LocalRuntimeHealthy 的运行时
//
// 注意：本函数只负责门槛过滤和数据转换。调用方必须在硬约束过滤
// （vision/tool/reasoning）之后才将条目真正纳入候选列表。
func CollectLocalCandidates(
	store *LocalRuntimeStore,
	cfg config.LocalModelRoutingConfig,
	taskClass TaskClass,
) []LocalCandidateEntry {
	// 1. 未启用 → 零行为变化
	if !cfg.Enabled {
		return nil
	}

	// 2. LocalModelRouting.Mode 是独立子开关；兼容旧配置中的 shadow/off。
	mode := cfg.Mode
	if mode == "" || mode == "shadow" || mode == "off" || mode == "disabled" {
		return nil
	}

	// 3. taskClass 不在允许列表中 → 返回空
	if !stringSliceContains(cfg.AllowLocalForTaskClasses, string(taskClass)) {
		return nil
	}

	// 4. taskClass 在 NeverDemoteTaskClasses 中 → 返回空（双重防御）
	if stringSliceContains(cfg.NeverDemoteTaskClasses, string(taskClass)) {
		return nil
	}

	// 5. 遍历运行时，只取健康态
	runtimes := store.ListAll()
	if len(runtimes) == 0 {
		return nil
	}

	entries := make([]LocalCandidateEntry, 0, len(runtimes))
	for _, rt := range runtimes {
		if rt.Status != LocalRuntimeHealthy {
			continue
		}

		entry := LocalCandidateEntry{
			RuntimeUID:          rt.RuntimeUID,
			DisplayName:         rt.Name,
			SupportsVision:      rt.SupportsVision,
			SupportsToolCalls:   rt.SupportsTools,
			SupportsReasoning:   rt.SupportsReasoning,
			ContextWindowTokens: rt.ContextTokens,
			EstimatedCost:       0, // 本地运行时无远程调用成本
		}

		// 如果 Name 为空，回退到 runtimeUID 作为显示名
		if entry.DisplayName == "" {
			entry.DisplayName = rt.RuntimeUID
		}

		entries = append(entries, entry)
	}

	if len(entries) == 0 {
		return nil
	}

	log.Printf("[LocalCandidateProvider-Collect] taskClass=%s 候选数=%d", taskClass, len(entries))
	return entries
}

// stringSliceContains 检查字符串切片中是否包含目标值。
// 复用 intent_matcher.go containsTaskClass 的写法。
func stringSliceContains(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}
