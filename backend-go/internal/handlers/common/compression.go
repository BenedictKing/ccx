package common

import (
	"log"

	"github.com/BenedictKing/ccx/internal/compression"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

const (
	// CompressionHeader 请求头控制压缩开关。
	CompressionHeader = "X-Ccx-Compression"
)

// CompressionContext 存储请求级压缩上下文（结果统计）。
type CompressionContext struct {
	OriginalTokens   int
	CompressedTokens int
	SavingsPercent   float64
	Compressed       bool
	Technique        string
	FallbackReason   string
}

// compressionContextKey 是 gin context 中压缩上下文的 key。
const compressionContextKey = "compressionContext"

// ApplyRequestCompression 在转发主链中应用请求侧 tool_result 压缩。
// 复用 guardrails 落定的钩子姿势：在 TryUpstreamWithAllKeys 入口附近调用。
//
// 只对 messages 类入口生效；images/vectors 入口不适用。
// fail-open：任何异常（panic / 解析失败）均返回原文，不阻断请求。
func ApplyRequestCompression(
	c *gin.Context,
	bodyBytes []byte,
	kind scheduler.ChannelKind,
	scenarioKey string,
	globalEnabled bool,
	channelEnabled bool,
) []byte {
	if len(bodyBytes) == 0 {
		return bodyBytes
	}

	// 仅 messages 类入口压缩
	switch kind {
	case scheduler.ChannelKindMessages,
		scheduler.ChannelKindChat,
		scheduler.ChannelKindResponses,
		scheduler.ChannelKindGemini:
		// 适用
	default:
		return bodyBytes
	}

	// 解析压缩计划
	headerCompression := c.GetHeader(CompressionHeader)
	plan := compression.ResolvePlan(headerCompression, scenarioKey, globalEnabled, channelEnabled)
	if !plan.Enabled {
		return bodyBytes
	}

	// 执行压缩
	result, err := compression.CompressRequestBody(bodyBytes, plan)
	if err != nil {
		log.Printf("[Compression-Apply] 压缩失败，fail-open: %v", err)
		return bodyBytes
	}

	// 写入 gin context 供日志/遥测使用
	if result.Compressed {
		ctx := &CompressionContext{
			OriginalTokens:   result.OriginalTokens,
			CompressedTokens: result.CompressedTokens,
			SavingsPercent:   result.SavingsPercent,
			Compressed:       true,
			Technique:        result.Technique,
		}
		c.Set(compressionContextKey, ctx)
	} else if result.FallbackReason != "" {
		ctx := &CompressionContext{
			Compressed:     false,
			FallbackReason: result.FallbackReason,
			OriginalTokens: result.OriginalTokens,
		}
		c.Set(compressionContextKey, ctx)
	}

	return result.Body
}

// GetCompressionContext 从 gin context 读取压缩统计（用于日志、metrics、成本报表）。
func GetCompressionContext(c *gin.Context) *CompressionContext {
	if c == nil {
		return nil
	}
	if val, ok := c.Get(compressionContextKey); ok {
		if ctx, ok := val.(*CompressionContext); ok {
			return ctx
		}
	}
	return nil
}
