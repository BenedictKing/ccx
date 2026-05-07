// Package responses —— PR1 outbound adapter.
//
// outbound 复用 (&providers.ResponsesProvider{SessionManager}).ConvertToProviderRequest
// 不重写转换逻辑（PR1 硬约束）。stream 帧切分按 SSE \n\n。
package responses

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
	"github.com/BenedictKing/ccx/internal/session"
)

// outboundAdapter 实现 pipeline.Outbound（OpenAI Responses 出站）。
//
// 持有可选的 SessionManager 以支持 Responses API 的 trace 亲和性；nil 时
// providers.ResponsesProvider 会自动构造默认 SessionManager（见 responses.go
// 第 38-40 行 newDefaultSessionManager 初始化逻辑）。
type outboundAdapter struct {
	SessionManager *session.SessionManager
}

// NewOutbound 构造出站 adapter；sessionManager 可为 nil。
func NewOutbound(sessionManager *session.SessionManager) *outboundAdapter {
	return &outboundAdapter{SessionManager: sessionManager}
}

// requestBodyBytesContextKey 与 providers/provider.go 中的同名 unexported 常量
// 对齐；providers.ResponsesProvider 通过 c.Get 读取请求体。
const requestBodyBytesContextKey = "requestBodyBytes"

// TransformRequest 调用 ResponsesProvider.ConvertToProviderRequest。
func (o *outboundAdapter) TransformRequest(_ context.Context, req *llm.Request) (*http.Request, []byte, error) {
	if req == nil {
		return nil, nil, fmt.Errorf("responses outbound: nil request")
	}
	c, err := adapters.GinContext(req)
	if err != nil {
		return nil, nil, err
	}
	upstream, apiKey, _, err := adapters.UpstreamBinding(req)
	if err != nil {
		return nil, nil, err
	}
	c.Set(requestBodyBytesContextKey, req.RawBody)

	provider := &providers.ResponsesProvider{SessionManager: o.SessionManager}
	httpReq, realBody, err := provider.ConvertToProviderRequest(c, upstream, apiKey)
	if err != nil {
		return nil, nil, fmt.Errorf("responses outbound: convert provider request: %w", err)
	}
	return httpReq, realBody, nil
}

// TransformResponse 把上游 *http.Response 整体读入并转换为 llm.Response。
func (o *outboundAdapter) TransformResponse(_ context.Context, resp *http.Response) (*llm.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("responses outbound: nil response")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("responses outbound: read response body: %w", err)
	}
	return adapters.CopyResponse(llm.FormatOpenAIResponses, resp, body, nil), nil
}

// TransformStream 按 SSE \n\n 分帧把上游流转为 *llm.Response 流。
func (o *outboundAdapter) TransformStream(ctx context.Context, resp *http.Response) llm.Stream[*llm.Response] {
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
					case ch <- &llm.Response{Format: llm.FormatOpenAIResponses, Body: frame}:
					}
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) && len(buf) > 0 {
					frame := append([]byte(nil), buf...)
					select {
					case <-ctx.Done():
					case ch <- &llm.Response{Format: llm.FormatOpenAIResponses, Body: frame}:
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
	_ pipeline.Outbound = (*outboundAdapter)(nil)
)
