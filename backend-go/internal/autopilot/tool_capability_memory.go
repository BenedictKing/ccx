package autopilot

import "github.com/BenedictKing/ccx/internal/config"

// 工具调用不支持结论在路由侧的读取。
//
// 写入方是 internal/handlers 侧的能力测试工具探针（主动）与 internal/handlers/common
// 的 failover 运行期负信号（被动：错误点名 tools / 强制 tool_choice 请求 2xx 但
// 全程无工具调用），读取方是这里的 SmartRouter。两侧通过
// config.SharedChannelCompatCache 共享同一实例：autopilot 不能
// import handlers（依赖方向相反），config 是双方共同的下层依赖。
// 学习口径与读写约定见 docs/specs/tool-call-capability.md。

// learnedToolCallUnsupportedLookup 供测试替换的查询入口。
// 生产实现读共享兼容性记忆；测试里替换成内存桩，避免依赖落盘状态。
var learnedToolCallUnsupportedLookup = func(channelUID, model string) bool {
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return false
	}
	return cache.IsToolCallUnsupportedForChannelModel(channelUID, model)
}

// learnedToolCallUnsupported 返回该渠道-模型是否实测不能执行工具调用。
//
// 为什么是任一命中即不支持：路由决策发生在选定具体 Key 之前，此刻只知道渠道与目标模型。
// 同渠道不同 Key 背后可能是不同上游，任一 Key 已知不支持就按不支持处理是保守的——
// 宁可绕开一个实际支持工具的 Key，也不要把 agent 流量送进已知不执行工具调用的组合。
//
// fail-open：无任何记忆时返回 false，调用方沿用注册表结论，不额外限制新渠道。
func learnedToolCallUnsupported(channelUID, model string) bool {
	if channelUID == "" || model == "" {
		return false
	}
	return learnedToolCallUnsupportedLookup(channelUID, model)
}
