// Package messages —— PR1 outbound adapter for Claude Messages API.
//
// outbound 复用 providers.GetProvider(upstream.ServiceType).ConvertToProviderRequest，
// 不重写转换逻辑（PR1 硬约束）。stream 帧切分按 SSE \n\n。
package messages

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/BenedictKing/ccx/internal/handlers/adapters"
	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/pipeline"
	"github.com/BenedictKing/ccx/internal/providers"
)

// outboundAdapter 实现 pipeline.Outbound（Claude Messages 出站）。
type outboundAdapter struct{}

// NewOutbound 构造出站 adapter。
func NewOutbound() *outboundAdapter { return &outboundAdapter{} }

// requestBodyBytesContextKey 与 providers/provider.go 中的同名常量保持一致；
// 因为 providers 包内是 unexported，adapter 必须自己写一遍这个键名。
//
// providers.ConvertToProviderRequest 通过 c.Get("requestBodyBytes") 读取请求体，
// 这是 ccx 既有约定（handler 在入口处 c.Set 该键）。adapter 在调用 provider
// 之前先 Set 一次，确保即使 inbound 没经过 handler 入口也能正确工作（例如
// pipeline.Process 直接拿到 llm.Request 重放）。
const requestBodyBytesContextKey = "requestBodyBytes"

// TransformRequest 调用 providers.ConvertToProviderRequest 构造发送请求。
func (outboundAdapter) TransformRequest(_ context.Context, req *llm.Request) (*http.Request, []byte, error) {
	if req == nil {
		return nil, nil, fmt.Errorf("messages outbound: nil request")
	}
	c, err := adapters.GinContext(req)
	if err != nil {
		return nil, nil, err
	}
	upstream, apiKey, _, err := adapters.UpstreamBinding(req)
	if err != nil {
		return nil, nil, err
	}
	// providers.ConvertToProviderRequest 通过 c.Get(requestBodyBytesContextKey)
	// 读取请求体；adapter 把 RawBody 写入 gin context 以兼容此契约。
	c.Set(requestBodyBytesContextKey, req.RawBody)

	provider := providers.GetProvider(upstream.ServiceType)
	if provider == nil {
		return nil, nil, fmt.Errorf("messages outbound: unknown service type %q", upstream.ServiceType)
	}
	httpReq, realBody, err := provider.ConvertToProviderRequest(c, upstream, apiKey)
	if err != nil {
		return nil, nil, fmt.Errorf("messages outbound: convert provider request: %w", err)
	}
	return httpReq, realBody, nil
}

// TransformResponse 把上游 *http.Response 整体读入并转换为 llm.Response。
func (outboundAdapter) TransformResponse(_ context.Context, resp *http.Response) (*llm.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("messages outbound: nil response")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("messages outbound: read response body: %w", err)
	}
	return adapters.CopyResponse(llm.FormatClaudeMessages, resp, body, nil), nil
}

// TransformStream 按 SSE \n\n 分帧把上游流转为 *llm.Response 流。
func (outboundAdapter) TransformStream(ctx context.Context, resp *http.Response) llm.Stream[*llm.Response] {
	ch := make(chan *llm.Response, 16)

	go func() {
		defer close(ch)
		defer func() { _ = resp.Body.Close() }()

		buf := make([]byte, 0, 4096)
		readBuf := make([]byte, 4096)
		for {
			if ctx.Err() != nil {
				return
			}
			n, err := resp.Body.Read(readBuf)
			if n > 0 {
				buf = append(buf, readBuf[:n]...)
				for {
					idx := indexDoubleNewline(buf)
					if idx < 0 {
						break
					}
					frame := append([]byte(nil), buf[:idx+2]...)
					buf = buf[idx+2:]
					select {
					case <-ctx.Done():
						return
					case ch <- &llm.Response{Format: llm.FormatClaudeMessages, Body: frame}:
					}
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) && len(buf) > 0 {
					frame := append([]byte(nil), buf...)
					select {
					case <-ctx.Done():
					case ch <- &llm.Response{Format: llm.FormatClaudeMessages, Body: frame}:
					}
				}
				return
			}
		}
	}()

	return llm.NewChanStream(ctx, ch, func() error { return nil })
}

// indexDoubleNewline 按 \n\n 或 \r\n\r\n 分帧。
func indexDoubleNewline(b []byte) int {
	for i := 0; i+1 < len(b); i++ {
		if b[i] == '\n' && b[i+1] == '\n' {
			return i
		}
		if i+3 < len(b) && b[i] == '\r' && b[i+1] == '\n' && b[i+2] == '\r' && b[i+3] == '\n' {
			return i + 2
		}
	}
	return -1
}

// 编译期接口断言。
var (
	_ pipeline.Inbound  = inboundAdapter{}
	_ pipeline.Outbound = outboundAdapter{}
)
