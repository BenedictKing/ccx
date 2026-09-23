package autopilot

import "github.com/BenedictKing/ccx/internal/config"

// 工具调用能力记忆在路由侧的读取。
//
// 键口径：全部使用 config.ToolRouteIdentity 的返回值（逻辑渠道 UID × 协议 kind 的
// 稳定身份），而非物理 ChannelUID——物理 UID 会随渠道重建/账号同步被重铸，
// 以其为键的结论在重铸后静默失效（2026-09 ark 种子种在 7 月代幽灵 UID 上、
// 四层查询全部 miss 的根因）。
//
// 写入方是 internal/handlers 侧的能力测试工具探针（主动）与 internal/handlers/common
// 的 failover 运行期信号（被动：错误点名 tools / 强制 tool_choice 2xx 零工具调用 /
// 带工具请求成功且观察到真实 function_call），读取方是这里的 SmartRouter 与
// ModelResolver。两侧通过 config.SharedChannelCompatCache 共享同一实例：autopilot
// 不能 import handlers（依赖方向相反），config 是双方共同的下层依赖。
// 学习口径与读写约定见 docs/specs/tool-call-capability.md。

// learnedToolCallUnsupportedLookup 供测试替换的查询入口。
// 生产实现读共享兼容性记忆；测试里替换成内存桩，避免依赖落盘状态。
var learnedToolCallUnsupportedLookup = func(routeIdentity, model string) bool {
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return false
	}
	return cache.IsToolCallUnsupportedForChannelModel(routeIdentity, model)
}

// learnedToolCallUnsupported 返回该路由-模型是否实测不能执行工具调用。
//
// 为什么是任一命中即不支持：路由决策发生在选定具体 Key 之前，此刻只知道渠道与目标模型。
// 同渠道不同 Key 背后可能是不同上游，任一 Key 已知不支持就按不支持处理是保守的——
// 宁可绕开一个实际支持工具的 Key，也不要把 agent 流量送进已知不执行工具调用的组合。
//
// fail-open：无任何记忆时返回 false，调用方沿用注册表结论，不额外限制新渠道。
func learnedToolCallUnsupported(routeIdentity, model string) bool {
	if routeIdentity == "" || model == "" {
		return false
	}
	return learnedToolCallUnsupportedLookup(routeIdentity, model)
}

// verifiedToolCallModelsLookup 供测试替换的正向白名单查询入口。
var verifiedToolCallModelsLookup = func(routeIdentity string) map[string]bool {
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return nil
	}
	return cache.VerifiedToolCallModelsForChannel(routeIdentity, true)
}

// verifiedToolCallModels 返回该路由经运行期 auto 流量实测产生过真实
// function_call 事件的模型集合（小写模型名键，不含探针来源——探针只验证
// 强制 tool_choice，「强制通过、auto 文本化」的组合实测存在）。白名单模式
// 的触发判定与成员查询共用：空集合 = 路由无正向记录（fail-open）。
func verifiedToolCallModels(routeIdentity string) map[string]bool {
	if routeIdentity == "" {
		return nil
	}
	return verifiedToolCallModelsLookup(routeIdentity)
}

// verifiedToolCallRoutesLookup 供测试替换的路由集合查询入口。
var verifiedToolCallRoutesLookup = func(kind string) map[string]bool {
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return nil
	}
	return cache.VerifiedToolCallRoutesForExclusive(kind, true)
}

// verifiedToolCallRoutes 返回指定执行协议上足够多的运行期 auto 实测真实工具调用
// 路由身份集合（渠道间排他的判定依据；验证路由不足时 fail-open）。
// 按协议独立判定：messages 流量不被 responses 证据锁死，反之亦然。
func verifiedToolCallRoutes(kind string) map[string]bool {
	return verifiedToolCallRoutesLookup(kind)
}
