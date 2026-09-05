package autopilot

import "github.com/BenedictKing/ccx/internal/config"

// 实测上下文上限在路由侧的读取。
//
// 写入方是 internal/handlers/common 的 failover（观测上游 400 后学习），读取方是这里的
// SmartRouter。两侧通过 config.SharedChannelCompatCache 共享同一实例：autopilot 不能
// import handlers/common（依赖方向相反），config 是双方共同的下层依赖。

// effectiveContextWindowLookup 供测试替换的有效窗口查询入口。
// 生产实现读共享兼容性记忆做三源合成；测试里替换成内存桩，避免依赖落盘状态。
var effectiveContextWindowLookup = func(channelUID, channelKind, model string, registryWindow int) int {
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return registryWindow
	}
	return cache.EffectiveContextWindow(channelUID, channelKind, model, registryWindow)
}

// effectiveContextWindow 返回渠道×协议×模型的有效输入窗口：
//
//	eff = min(实测收紧上限, max(注册表窗口, 成功实证棘轮, models API 声明))
//
// 与 learnedContextLimit 的只收紧不同，这里是双向合成：注册表滞后偏低时由
// 放宽证据顶开（渠道渐进扩容自愈），实测 400 学到的收紧上限永远压得住。
// kind 为空时按 "unknown" 桶查询；无任何证据时等于 registryWindow（fail-open）。
func effectiveContextWindow(channelUID, channelKind, model string, registryWindow int) int {
	if channelUID == "" || model == "" {
		return registryWindow
	}
	return effectiveContextWindowLookup(channelUID, channelKind, model, registryWindow)
}

// learnedDeclaredContextLimitLookup 供测试替换的实测收紧上限查询入口。
var learnedDeclaredContextLimitLookup = func(channelUID, model string) (int, bool) {
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return 0, false
	}
	return cache.MinContextLimitForChannelModel(channelUID, model)
}

// learnedDeclaredContextLimit 返回该渠道-模型实测学到的收紧上限。
// 有值意味着真实 400 已证明窗口不高于此值——上下文试探必须排除这类组合。
func learnedDeclaredContextLimit(channelUID, model string) (int, bool) {
	if channelUID == "" || model == "" {
		return 0, false
	}
	return learnedDeclaredContextLimitLookup(channelUID, model)
}
