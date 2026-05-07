package pipeline

import (
	"context"
	"net/http"

	"github.com/BenedictKing/ccx/internal/llm"
)

// applyBeforeRequestMiddlewares 顺序调用所有 Middleware.BeforeRequest。
//
// 任一步返回错误即立即中止，剩余 middleware 不执行；这与 axonhub 行为一致：
// before-request 阶段的错误属于不可恢复的预处理失败，不应继续 retry。
func (p *pipeline) applyBeforeRequestMiddlewares(ctx context.Context, req *llm.Request) (*llm.Request, error) {
	for _, mw := range p.middlewares {
		next, err := mw.BeforeRequest(ctx, req)
		if err != nil {
			return nil, err
		}
		if next != nil {
			req = next
		}
	}
	return req, nil
}

// applyRawRequestMiddlewares 顺序调用所有 Middleware.RawRequest。
//
// 任一步返回错误即立即中止；调用方在错误时通常调用
// applyRawErrorResponseMiddlewares 通知所有 middleware。
func (p *pipeline) applyRawRequestMiddlewares(ctx context.Context, req *http.Request) (*http.Request, error) {
	for _, mw := range p.middlewares {
		next, err := mw.RawRequest(ctx, req)
		if err != nil {
			return nil, err
		}
		if next != nil {
			req = next
		}
	}
	return req, nil
}

// applyRawResponseMiddlewares 顺序调用所有 Middleware.RawResponse。
//
// ccx key 级 BlacklistKey / MarkKeyAsFailedWithDuration / MatchPauseRule 在此层注入。
func (p *pipeline) applyRawResponseMiddlewares(ctx context.Context, resp *http.Response) (*http.Response, error) {
	for _, mw := range p.middlewares {
		next, err := mw.RawResponse(ctx, resp)
		if err != nil {
			return nil, err
		}
		if next != nil {
			resp = next
		}
	}
	return resp, nil
}

// applyRawStreamMiddlewares 顺序包装上游原始 SSE 流。
//
// 链式包装：mw1 → mw2 → mw3，先注册先在最外层。
func (p *pipeline) applyRawStreamMiddlewares(ctx context.Context, stream llm.Stream[*llm.StreamEvent]) llm.Stream[*llm.StreamEvent] {
	for _, mw := range p.middlewares {
		stream = mw.RawStream(ctx, stream)
	}
	return stream
}

// applyRawErrorResponseMiddlewares 通知所有 middleware 当前 attempt 出错。
//
// 不短路、不修改错误本身；用于日志 / metrics / 清理动作。
func (p *pipeline) applyRawErrorResponseMiddlewares(ctx context.Context, err error) {
	for _, mw := range p.middlewares {
		mw.RawErrorResponse(ctx, err)
	}
}

// applyLlmResponseMiddlewares 顺序调用所有 Middleware.LlmResponse。
func (p *pipeline) applyLlmResponseMiddlewares(ctx context.Context, resp *llm.Response) (*llm.Response, error) {
	for _, mw := range p.middlewares {
		next, err := mw.LlmResponse(ctx, resp)
		if err != nil {
			return nil, err
		}
		if next != nil {
			resp = next
		}
	}
	return resp, nil
}

// applyLlmStreamMiddlewares 顺序包装 llm.Response 流。
func (p *pipeline) applyLlmStreamMiddlewares(ctx context.Context, stream llm.Stream[*llm.Response]) llm.Stream[*llm.Response] {
	for _, mw := range p.middlewares {
		stream = mw.LlmStream(ctx, stream)
	}
	return stream
}

// applyInboundRawStreamMiddlewares 顺序包装入站协议字节流。
func (p *pipeline) applyInboundRawStreamMiddlewares(ctx context.Context, stream llm.Stream[*llm.StreamEvent]) llm.Stream[*llm.StreamEvent] {
	for _, mw := range p.middlewares {
		stream = mw.InboundRawStream(ctx, stream)
	}
	return stream
}
