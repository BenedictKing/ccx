package loadbalance

import "context"

// 私有 ctx key 类型，避免外部冲突。
type (
	requestedModelKey struct{}
	requestStreamKey  struct{}
	traceIDKey        struct{}
)

// ContextWithRequestedModel 把客户端请求的 model 注入 ctx，供 LatencyAware
// 等"按 model 维度查询 metrics"的 strategy 读取。
//
// 调用时机：scheduler 改造（PR2 Phase 3）后，在 Sort 之前由调用方注入。
func ContextWithRequestedModel(ctx context.Context, modelID string) context.Context {
	if modelID == "" {
		return ctx
	}
	return context.WithValue(ctx, requestedModelKey{}, modelID)
}

// RequestedModelFromContext 取出 ctx 上的 requested model；缺失返回空串。
func RequestedModelFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestedModelKey{}).(string)
	return v
}

// ContextWithRequestStream 把"是否为流式请求"注入 ctx，供候选过滤阶段使用。
func ContextWithRequestStream(ctx context.Context, stream bool) context.Context {
	return context.WithValue(ctx, requestStreamKey{}, stream)
}

// RequestStreamFromContext 取出 ctx 上的 stream flag；缺失返回 false。
func RequestStreamFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(requestStreamKey{}).(bool)
	return v
}

// ContextWithTraceID 把 ccx 的统一会话 ID 注入 ctx，供 TraceAware strategy
// 读取以查询 LastSuccessfulChannelByTrace。
//
// ccx 现有 utils.ExtractUnifiedSessionID 的结果在 handler 入口处即可注入。
func ContextWithTraceID(ctx context.Context, traceID string) context.Context {
	if traceID == "" {
		return ctx
	}
	return context.WithValue(ctx, traceIDKey{}, traceID)
}

// TraceIDFromContext 取出 ctx 上的 trace ID；缺失返回空串。
func TraceIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(traceIDKey{}).(string)
	return v
}
