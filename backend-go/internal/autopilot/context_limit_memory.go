package autopilot

import "github.com/BenedictKing/ccx/internal/config"

// 实测上下文上限在路由侧的读取。
//
// 写入方是 internal/handlers/common 的 failover（观测上游 400 后学习），读取方是这里的
// SmartRouter。两侧通过 config.SharedChannelCompatCache 共享同一实例：autopilot 不能
// import handlers/common（依赖方向相反），config 是双方共同的下层依赖。

// learnedContextLimitLookup 供测试替换的查询入口。
// 生产实现读共享兼容性记忆；测试里替换成内存桩，避免依赖落盘状态。
var learnedContextLimitLookup = func(channelUID, model string) (int, bool) {
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return 0, false
	}
	return cache.MinContextLimitForChannelModel(channelUID, model)
}

// learnedContextLimit 返回该渠道-模型在所有已知 Key 上实测到的最小上下文上限。
//
// 为什么取最小值：路由决策发生在选定具体 Key 之前，此刻只知道渠道与目标模型。
// 同渠道不同 Key 可能因套餐不同而窗口不同，取最小是保守的——宁可放过一个窗口更大的 Key，
// 也不要把长上下文请求送进已知会 400 的组合。
//
// fail-open：无任何记忆时返回 false，调用方沿用注册表窗口，不额外限制新渠道。
func learnedContextLimit(channelUID, model string) (int, bool) {
	if channelUID == "" || model == "" {
		return 0, false
	}
	return learnedContextLimitLookup(channelUID, model)
}
