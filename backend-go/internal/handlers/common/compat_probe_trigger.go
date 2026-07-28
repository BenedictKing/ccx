package common

import (
	"log"
	"sync"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
)

// 内容启发式兼容项的探针驱动学习。
//
// 部分兼容项（是否需要回传 reasoning_content、是否需要剥离空 text block）没有上游报错信号：
// 上游返回 200 但响应形态不同，只能靠观察响应内容判断。这类项目原先依赖用户在渠道诊断面板
// 手工点按钮，本文件把它改为首次遇到该 渠道-Key-模型 组合时自动在后台探测一次并记忆结论。
//
// 探测实现位于 internal/handlers（compat_diagnose_handler.go），而该包 import 了本包，
// 因此这里用 hook 注册反转依赖，与 SetNotifyEndpointResultHook 等既有做法一致。

// CompatProbeFunc 执行一次兼容性探测，返回 trait -> 是否应启用。
// 探测失败时返回 nil，调用方不写入任何结论（fail-open，等下次请求再试）。
type CompatProbeFunc func(upstream *config.UpstreamConfig, apiKey, baseURL, model string) map[config.CompatTrait]bool

var compatProbeHook CompatProbeFunc

// SetCompatProbeHook 注册兼容性探测实现。未注册时探针学习整体禁用。
func SetCompatProbeHook(hook CompatProbeFunc) {
	compatProbeHook = hook
}

// compatProbeInflight 保证同一 渠道-Key-模型 组合同时只有一个探测在跑。
// 探测会真实消耗一次上游调用，并发请求各自探测既浪费额度又可能写入冲突结论。
var compatProbeInflight sync.Map

// swapChannelCompatCacheForTest 临时替换全局兼容性记忆，返回还原函数。
// 仅供测试使用：全局实例带落盘，测试直接写它会在源码树里产生状态文件，
// 且上一次运行的记忆会影响下一次运行的结果。
//
// 同时替换 config 侧的共享实例：SmartRouter 从那里读取实测上下文上限，
// 只换本包变量会让读写两侧在测试里指向不同实例。
func swapChannelCompatCacheForTest(replacement *config.ChannelCompatCache) func() {
	original := channelCompatCache
	channelCompatCache = replacement
	restoreShared := config.SwapSharedChannelCompatCacheForTest(replacement)
	return func() {
		restoreShared()
		channelCompatCache = original
	}
}

// maybeTriggerCompatProbe 在该组合尚无任何学习记录时，异步触发一次探测。
// 不阻塞当前请求：当前请求按静态默认走，探测结论对后续请求生效。
func maybeTriggerCompatProbe(upstream *config.UpstreamConfig, apiKey, baseURL, model string) {
	if compatProbeHook == nil || upstream == nil || upstream.ChannelUID == "" || apiKey == "" {
		return
	}
	// 仅对 Claude 协议渠道有意义：这几个内容启发式兼容项都是 Claude 协议形态差异
	if upstream.ServiceType != "claude" && upstream.ServiceType != "messages" {
		return
	}

	keyHash := autopilot.KeyHashFromAPIKey(apiKey)
	if channelCompatCache.HasAnyTrait(upstream.ChannelUID, keyHash, model) {
		return
	}

	inflightKey := config.GenerateCacheKey(upstream.ChannelUID, keyHash, model)
	if _, loaded := compatProbeInflight.LoadOrStore(inflightKey, struct{}{}); loaded {
		return
	}

	probe := compatProbeHook
	// 传入快照副本：探测在后台运行，不应受调用方后续改写影响
	upstreamSnapshot := upstream.Clone()

	go func() {
		defer compatProbeInflight.Delete(inflightKey)
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[ChannelCompat-Probe] 探测 panic 已恢复: %v", r)
			}
		}()

		results := probe(upstreamSnapshot, apiKey, baseURL, model)
		if len(results) == 0 {
			return
		}
		for trait, enabled := range results {
			if channelCompatCache.Record(upstreamSnapshot.ChannelUID, keyHash, model, trait, enabled,
				config.CompatSourceProbe, "自动探测响应形态得出") {
				log.Printf("[ChannelCompat-Probe] 渠道 %s 模型 %s 探测得出 %s=%v",
					upstreamSnapshot.Name, model, trait, enabled)
			}
		}
	}()
}
