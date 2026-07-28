package config

import "sync"

// 渠道兼容性记忆的进程内共享实例。
//
// 为什么放在 config 包：写入方在 internal/handlers/common（failover 观测到上游报错），
// 读取方在 internal/autopilot（SmartRouter 选渠道时需要实测上下文上限）。
// autopilot 不能 import handlers/common（会与既有依赖方向相反并成环），
// 而两者都已 import config，所以把单例收在这里作为共同依赖。

var (
	sharedChannelCompatCache     *ChannelCompatCache
	sharedChannelCompatCacheOnce sync.Once
	sharedChannelCompatCacheMu   sync.RWMutex
)

// SharedChannelCompatCache 返回带落盘的全局兼容性记忆实例（首次调用时加载已有记忆）。
func SharedChannelCompatCache() *ChannelCompatCache {
	sharedChannelCompatCacheOnce.Do(func() {
		sharedChannelCompatCacheMu.Lock()
		defer sharedChannelCompatCacheMu.Unlock()
		if sharedChannelCompatCache == nil {
			sharedChannelCompatCache = NewChannelCompatCacheWithPersistence(ChannelCompatStatePath)
		}
	})
	sharedChannelCompatCacheMu.RLock()
	defer sharedChannelCompatCacheMu.RUnlock()
	return sharedChannelCompatCache
}

// SwapSharedChannelCompatCacheForTest 临时替换全局实例，返回还原函数。
//
// 仅供测试使用：全局实例带落盘，测试直接写它会在源码树里产生状态文件，
// 且上一次运行的记忆会影响下一次运行的结果。
func SwapSharedChannelCompatCacheForTest(replacement *ChannelCompatCache) func() {
	// 触发 once，避免还原后首次真实调用又被 once 覆盖成持久化实例
	SharedChannelCompatCache()

	sharedChannelCompatCacheMu.Lock()
	original := sharedChannelCompatCache
	sharedChannelCompatCache = replacement
	sharedChannelCompatCacheMu.Unlock()

	return func() {
		sharedChannelCompatCacheMu.Lock()
		sharedChannelCompatCache = original
		sharedChannelCompatCacheMu.Unlock()
	}
}
