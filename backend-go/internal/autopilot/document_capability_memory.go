package autopilot

import "github.com/BenedictKing/ccx/internal/config"

// document 不支持结论在路由侧的读取。
//
// 写入方是 internal/handlers/common 的 failover（请求携带 document 块且上游以
// 400/422 拒绝后学习），读取方是这里的 SmartRouter。两侧通过
// config.SharedChannelCompatCache 共享同一实例：autopilot 不能
// import handlers/common（依赖方向相反），config 是双方共同的下层依赖。

// learnedDocumentUnsupportedLookup 供测试替换的查询入口。
// 生产实现读共享兼容性记忆；测试里替换成内存桩，避免依赖落盘状态。
var learnedDocumentUnsupportedLookup = func(channelUID, model string) bool {
	cache := config.SharedChannelCompatCache()
	if cache == nil {
		return false
	}
	return cache.IsDocumentUnsupportedForChannelModel(channelUID, model)
}

// learnedDocumentUnsupported 返回该渠道-模型是否在任一已知 Key 上实测拒绝 document 块。
//
// 为什么是任一命中即不支持：路由决策发生在选定具体 Key 之前，此刻只知道渠道与目标模型。
// 同渠道不同 Key 背后可能是不同上游，任一 Key 已知拒绝就按不支持处理是保守的——
// 宁可绕开一个实际支持的 Key，也不要把 PDF 请求送进已知会 400 的组合。
//
// fail-open：无任何记忆时返回 false，调用方沿用注册表结论，不额外限制新渠道。
func learnedDocumentUnsupported(channelUID, model string) bool {
	if channelUID == "" || model == "" {
		return false
	}
	return learnedDocumentUnsupportedLookup(channelUID, model)
}
