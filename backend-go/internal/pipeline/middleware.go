package pipeline

import (
	"context"
	"net/http"

	"github.com/BenedictKing/ccx/internal/llm"
)

// Middleware 暴露 7 类 hook，参照 axonhub/llm/pipeline.Middleware 设计。
//
// 各 hook 触发时机：
//
//	BeforeRequest        在 Inbound.TransformRequest 之后、Outbound.TransformRequest
//	                     之前；用于在 llm.Request 上做预处理（例如注入 prompt）。
//
//	RawRequest           在 Outbound.TransformRequest 之后、Executor.Execute
//	                     之前；用于改写 *http.Request（例如 header 注入、签名）。
//
//	RawResponse          在 Executor.Execute 返回 *http.Response 之后、
//	                     Outbound.TransformResponse 之前；ccx key 级 BlacklistKey /
//	                     MarkKeyAsFailedWithDuration / MatchPauseRule 在此层注入。
//
//	RawStream            对应 Stream 路径，包装上游原始 SSE 流。
//
//	RawErrorResponse     当 Executor.Execute 或后续阶段返回错误时调用，便于
//	                     middleware 记录日志 / metrics（不修改错误本身）。
//
//	LlmResponse          在 Outbound.TransformResponse 之后、
//	                     Inbound.TransformResponse 之前；可用于 usage 二次解析。
//
//	LlmStream            对应 Stream 路径的 llm.Response 包装层。
//
//	InboundRawResponse   在 Inbound.TransformResponse 之前、最终响应回写客户端
//	                     之前；用于在入站协议字节流上做最后改写（如压缩）。
//
//	InboundRawStream     对应 Stream 路径的入站协议字节流包装层。
//
// 实现可通过 embedding BaseMiddleware 跳过不关心的 hook。
type Middleware interface {
	BeforeRequest(ctx context.Context, req *llm.Request) (*llm.Request, error)
	RawRequest(ctx context.Context, req *http.Request) (*http.Request, error)
	RawResponse(ctx context.Context, resp *http.Response) (*http.Response, error)
	RawStream(ctx context.Context, stream llm.Stream[*llm.StreamEvent]) llm.Stream[*llm.StreamEvent]
	RawErrorResponse(ctx context.Context, err error)
	LlmResponse(ctx context.Context, resp *llm.Response) (*llm.Response, error)
	LlmStream(ctx context.Context, stream llm.Stream[*llm.Response]) llm.Stream[*llm.Response]
	InboundRawResponse(ctx context.Context, resp *http.Response) (*http.Response, error)
	InboundRawStream(ctx context.Context, stream llm.Stream[*llm.StreamEvent]) llm.Stream[*llm.StreamEvent]
}

// BaseMiddleware 提供所有 Middleware hook 的默认空实现（透传）。
//
// 实际 middleware 通过 embedding 该结构，仅覆盖关心的 hook：
//
//	type LoggingMiddleware struct { pipeline.BaseMiddleware }
//	func (m *LoggingMiddleware) RawRequest(ctx, req) (*http.Request, error) { ... }
type BaseMiddleware struct{}

// BeforeRequest 默认透传。
func (BaseMiddleware) BeforeRequest(_ context.Context, req *llm.Request) (*llm.Request, error) {
	return req, nil
}

// RawRequest 默认透传。
func (BaseMiddleware) RawRequest(_ context.Context, req *http.Request) (*http.Request, error) {
	return req, nil
}

// RawResponse 默认透传。
func (BaseMiddleware) RawResponse(_ context.Context, resp *http.Response) (*http.Response, error) {
	return resp, nil
}

// RawStream 默认透传。
func (BaseMiddleware) RawStream(_ context.Context, stream llm.Stream[*llm.StreamEvent]) llm.Stream[*llm.StreamEvent] {
	return stream
}

// RawErrorResponse 默认 no-op。
func (BaseMiddleware) RawErrorResponse(_ context.Context, _ error) {}

// LlmResponse 默认透传。
func (BaseMiddleware) LlmResponse(_ context.Context, resp *llm.Response) (*llm.Response, error) {
	return resp, nil
}

// LlmStream 默认透传。
func (BaseMiddleware) LlmStream(_ context.Context, stream llm.Stream[*llm.Response]) llm.Stream[*llm.Response] {
	return stream
}

// InboundRawResponse 默认透传。
func (BaseMiddleware) InboundRawResponse(_ context.Context, resp *http.Response) (*http.Response, error) {
	return resp, nil
}

// InboundRawStream 默认透传。
func (BaseMiddleware) InboundRawStream(_ context.Context, stream llm.Stream[*llm.StreamEvent]) llm.Stream[*llm.StreamEvent] {
	return stream
}
