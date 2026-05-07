// Package gemini —— PR1 outbound adapter.
//
// outbound 复用 gemini.buildProviderRequest（handler.go 内已有），不重写
// 协议转换逻辑（PR1 硬约束）。Stream 帧切分与 chat outbound 一致按 \n\n 分。
package gemini

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/BenedictKing/ccx/internal/handlers/adapters"
	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/pipeline"
	"github.com/BenedictKing/ccx/internal/types"
)

// outboundAdapter 实现 pipeline.Outbound（Gemini Contents 出站）。
type outboundAdapter struct{}

// NewOutbound 构造出站 adapter。
func NewOutbound() *outboundAdapter { return &outboundAdapter{} }

// TransformRequest 调用现有 buildProviderRequest 构造发送请求。
//
// 从 llm.Request.Metadata 读取：
//   - inbound 写入的 *types.GeminiRequest（避免重复反序列化）；
//   - adapters 共享的 gin.Context / upstream / apiKey / baseURL。
func (outboundAdapter) TransformRequest(_ context.Context, req *llm.Request) (*http.Request, []byte, error) {
	if req == nil {
		return nil, nil, fmt.Errorf("gemini outbound: nil request")
	}
	c, err := adapters.GinContext(req)
	if err != nil {
		return nil, nil, err
	}
	upstream, apiKey, baseURL, err := adapters.UpstreamBinding(req)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := loadParsedRequest(req)
	if err != nil {
		return nil, nil, err
	}
	httpReq, err := buildProviderRequest(c, upstream, baseURL, apiKey, parsed, req.Model, req.IsStream())
	if err != nil {
		return nil, nil, fmt.Errorf("gemini outbound: build provider request: %w", err)
	}
	realBody := append([]byte(nil), req.RawBody...)
	return httpReq, realBody, nil
}

// loadParsedRequest 取出 inbound 写入的 *types.GeminiRequest。
//
// 该值由 inbound.TransformRequest 注入；如果缺失说明调用方绕过了 inbound，
// 视为编程错误。
func loadParsedRequest(req *llm.Request) (*types.GeminiRequest, error) {
	if req.Metadata == nil {
		return nil, fmt.Errorf("gemini outbound: parsed request missing in metadata")
	}
	v, ok := req.Metadata[MetaParsedRequest]
	if !ok {
		return nil, fmt.Errorf("gemini outbound: parsed request missing in metadata")
	}
	parsed, ok := v.(*types.GeminiRequest)
	if !ok || parsed == nil {
		return nil, fmt.Errorf("gemini outbound: parsed request type mismatch")
	}
	return parsed, nil
}

// TransformResponse 把上游 *http.Response 整体读入并转换为 llm.Response。
func (outboundAdapter) TransformResponse(_ context.Context, resp *http.Response) (*llm.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("gemini outbound: nil response")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gemini outbound: read response body: %w", err)
	}
	return adapters.CopyResponse(llm.FormatGeminiContents, resp, body, nil), nil
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
					case ch <- &llm.Response{Format: llm.FormatGeminiContents, Body: frame}:
					}
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) && len(buf) > 0 {
					frame := append([]byte(nil), buf...)
					select {
					case <-ctx.Done():
					case ch <- &llm.Response{Format: llm.FormatGeminiContents, Body: frame}:
					}
				}
				return
			}
		}
	}()

	return llm.NewChanStream(ctx, ch, func() error { return nil })
}

// indexDoubleNewline 与 chat outbound 同名函数等价（按 \n\n 或 \r\n\r\n 分帧）。
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
