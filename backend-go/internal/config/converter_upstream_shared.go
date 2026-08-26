package config

import (
	"sync"
)

// 转换层上游指纹记忆的进程内共享实例。
//
// 为什么放在 config 包：写入方与读取方都在 internal/handlers/common（failover
// 观测响应头 / 构造请求前判定），但判定点可能扩展到其他包；统一收在 config
// 与 SharedChannelCompatCache 保持一致，避免未来出现反向依赖。

var (
	sharedConverterUpstreamCache     *ConverterUpstreamCache
	sharedConverterUpstreamCacheOnce sync.Once
	sharedConverterUpstreamCacheMu   sync.RWMutex
)

// SharedConverterUpstreamCache 返回带落盘的全局转换层指纹记忆实例（首次调用时加载已有记忆）。
func SharedConverterUpstreamCache() *ConverterUpstreamCache {
	sharedConverterUpstreamCacheOnce.Do(func() {
		sharedConverterUpstreamCacheMu.Lock()
		defer sharedConverterUpstreamCacheMu.Unlock()
		if sharedConverterUpstreamCache == nil {
			sharedConverterUpstreamCache = NewConverterUpstreamCacheWithPersistence(ConverterUpstreamStatePath)
		}
	})
	sharedConverterUpstreamCacheMu.RLock()
	defer sharedConverterUpstreamCacheMu.RUnlock()
	return sharedConverterUpstreamCache
}

// SwapSharedConverterUpstreamCacheForTest 临时替换全局实例，返回还原函数。
//
// 仅供测试使用：全局实例带落盘，测试直接写它会在源码树里产生状态文件，
// 且上一次运行的记忆会影响下一次运行的结果。
func SwapSharedConverterUpstreamCacheForTest(replacement *ConverterUpstreamCache) func() {
	// 触发 once，避免还原后首次真实调用又被 once 覆盖成持久化实例
	SharedConverterUpstreamCache()

	sharedConverterUpstreamCacheMu.Lock()
	original := sharedConverterUpstreamCache
	sharedConverterUpstreamCache = replacement
	sharedConverterUpstreamCacheMu.Unlock()

	return func() {
		sharedConverterUpstreamCacheMu.Lock()
		sharedConverterUpstreamCache = original
		sharedConverterUpstreamCacheMu.Unlock()
	}
}
