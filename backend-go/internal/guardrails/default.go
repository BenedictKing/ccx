package guardrails

import "sync"

var (
	defaultRegistry     *Registry
	defaultRegistryOnce sync.Once
)

// DefaultRegistry 返回全局默认 guardrail 注册表（懒初始化）。
func DefaultRegistry() *Registry {
	defaultRegistryOnce.Do(func() {
		defaultRegistry = NewRegistry()
	})
	return defaultRegistry
}

// ResetDefaultRegistry 用于测试，重置全局注册表。
func ResetDefaultRegistry() {
	defaultRegistry = nil
	defaultRegistryOnce = sync.Once{}
}
