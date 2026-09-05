package common

import (
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

// 上下文窗口自学习（放宽侧）。
//
// 写入方是本文件（成功路径），读取方是 scheduler 的上下文过滤与 autopilot 的
// SmartRouter/ModelResolver（经 config.SharedChannelCompatCache 共享，依赖方向
// 与 context_limit_memory.go 相同）。证据语义：请求 2xx 完成即实证该渠道×协议×模型
// 能承载本次输入，棘轮只升不降——渠道渐进扩容（200K→272K→372K→1M）由成功请求
// 逐步顶开，注册表滞后不再把长对话锁死在发前过滤。

// MaybeRecordContextWindowProven 在请求成功后记录实证输入承载量。
// 只在 handleSuccess 无错误（流式正常完成/非流式 2xx 落定）且 usage 携带输入量时学习；
// 中途出错、客户端取消、空响应都不构成"实证"。
func MaybeRecordContextWindowProven(c *gin.Context, apiType string, upstream *config.UpstreamConfig, kind scheduler.ChannelKind, model string, usage *types.Usage, err error) {
	if err != nil || upstream == nil || upstream.ChannelUID == "" || model == "" || usage == nil {
		return
	}
	inputTokens := usageTotalContextTokens(usage)
	if inputTokens <= 0 {
		return
	}
	if channelCompatCache.RecordContextWindowProven(upstream.ChannelUID, string(kind), model, inputTokens, time.Now()) {
		RequestLogf(c, "[%s-ContextWindow] 渠道 %s 模型 %s 实证可承载输入 %d tokens（棘轮上调）",
			apiType, upstream.Name, model, inputTokens)
	}
}

// usageTotalContextTokens 取本次请求占用的总上下文输入（含缓存命中部分）。
// 窗口约束作用于总上下文而非增量内容：500K 对话即使 480K 命中缓存也需要 500K 窗口。
// - OpenAI/Responses 风格：PromptTokensTotal 为总口径；未提供时 InputTokens 通常已含缓存；
// - Anthropic 风格：input_tokens 不含缓存，需补 cache_creation + cache_read。
func usageTotalContextTokens(usage *types.Usage) int {
	if usage == nil {
		return 0
	}
	if usage.PromptTokensTotal > 0 {
		return usage.PromptTokensTotal
	}
	total := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
	if total > 0 {
		return total
	}
	return usage.PromptTokens
}
