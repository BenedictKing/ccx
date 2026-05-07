// Package messages —— PR1 outbound adapter for Claude Messages API.
//
// outbound 复用 providers.GetProvider(upstream.ServiceType).ConvertToProviderRequest /
// ConvertToClaudeResponse / HandleStreamResponse，不重写转换逻辑（PR1 硬约束）。
//
// 响应路径行为（PR3 T8a Stage B1 补全）：
//   - same-format（claude → claude messages）：byte copy via adapters.CopyResponse；
//   - cross-format（openai/gemini/responses → claude messages）：
//     * 非流式：调 provider.ConvertToClaudeResponse 把上游 JSON 转 Claude JSON；
//     * 流式：调 provider.HandleStreamResponse 拿到 SSE 事件 chan，逐事件包装为
//       *llm.Response 流（事件字符串本身已是 Claude SSE 协议帧）。
package messages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/adapters"
	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/pipeline"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/types"
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

// TransformResponse 把上游 *http.Response 整体读入并按 cross-format 还原为
// Claude Messages 协议字节。
//
// ctx 中由 wire.LBOutboundAdapter 注入 *llm.Request；据此读取 upstream
// 选择转换路径：
//   - claude / 未知 → byte copy（兼容 PR1 直通行为）；
//   - 其他 → providers.GetProvider(serviceType).ConvertToClaudeResponse 转换
//     再 json.Marshal 写回 llm.Response.Body。
func (outboundAdapter) TransformResponse(ctx context.Context, resp *http.Response) (*llm.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("messages outbound: nil response")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("messages outbound: read response body: %w", err)
	}

	upstream := upstreamFromCtx(ctx)
	if upstream == nil || upstream.ServiceType == "" || upstream.ServiceType == "claude" {
		return adapters.CopyResponse(llm.FormatClaudeMessages, resp, body, parseClaudeUsage), nil
	}

	provider := providers.GetProvider(upstream.ServiceType)
	if provider == nil {
		return nil, fmt.Errorf("messages outbound: unknown service type %q", upstream.ServiceType)
	}

	providerResp := &types.ProviderResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		Body:       body,
		Stream:     false,
	}
	claudeResp, err := provider.ConvertToClaudeResponse(providerResp)
	if err != nil {
		return nil, fmt.Errorf("messages outbound: convert claude response (%s): %w", upstream.ServiceType, err)
	}
	convertedBody, err := json.Marshal(claudeResp)
	if err != nil {
		return nil, fmt.Errorf("messages outbound: marshal claude response: %w", err)
	}

	out := &llm.Response{
		Format:     llm.FormatClaudeMessages,
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       convertedBody,
	}
	if claudeResp.Usage != nil {
		out.Usage = &llm.Usage{Format: llm.FormatClaudeMessages, Inner: *claudeResp.Usage}
	}
	return out, nil
}

// TransformStream 把上游 *http.Response 流式 body 转换为 *llm.Response 流。
//
// same-format（claude）：按 SSE \n\n 切帧，原字节透传（PR1 行为）。
// cross-format（openai/gemini/responses）：调 provider.HandleStreamResponse 拿到
// "已转换为 Claude SSE 帧"的事件字符串 chan，逐帧包装为 *llm.Response。
// 该实现走选项 B —— 直接复用 provider 内部的 SSE 转换函数（即 HandleStreamResponse），
// 不需要把 c.Writer 替换成 io.Pipe / buffer，也不扩 provider 公共接口。
func (outboundAdapter) TransformStream(ctx context.Context, resp *http.Response) llm.Stream[*llm.Response] {
	upstream := upstreamFromCtx(ctx)
	if upstream == nil || upstream.ServiceType == "" || upstream.ServiceType == "claude" {
		return rawSSEPassthrough(ctx, resp)
	}
	provider := providers.GetProvider(upstream.ServiceType)
	if provider == nil {
		// 未知服务类型：回退到 raw passthrough，避免吞错；上层 middleware 会
		// 在错误状态码上触发 retry。
		return rawSSEPassthrough(ctx, resp)
	}
	return crossFormatSSEStream(ctx, resp, provider)
}

// rawSSEPassthrough 是 same-format 的 SSE \n\n 分帧透传实现（PR1 现状）。
func rawSSEPassthrough(ctx context.Context, resp *http.Response) llm.Stream[*llm.Response] {
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

// crossFormatSSEStream 调 provider.HandleStreamResponse 把上游 SSE 转 Claude SSE，
// 再把事件字符串 chan 包装为 *llm.Response 流。
//
// provider.HandleStreamResponse 返回的每个 string 都是一个完整 Claude SSE
// 事件（已含末尾 "\n\n"），故无需在此处再切帧 / 拼接。错误通过 ChanStream.SetErr
// 透出，由 pipeline / inbound 决定是否触发空响应重试或回写客户端。
func crossFormatSSEStream(ctx context.Context, resp *http.Response, provider providers.Provider) llm.Stream[*llm.Response] {
	out := make(chan *llm.Response, 16)
	cs := llm.NewChanStream(ctx, out, func() error { return nil })

	eventChan, errChan, hsErr := provider.HandleStreamResponse(ctx, resp.Body)
	if hsErr != nil {
		cs.SetErr(fmt.Errorf("messages outbound: provider stream init: %w", hsErr))
		close(out)
		return cs
	}

	go func() {
		defer close(out)
		var streamErr error
		for {
			select {
			case <-ctx.Done():
				if streamErr == nil {
					cs.SetErr(ctx.Err())
				}
				return
			case ev, ok := <-eventChan:
				if !ok {
					eventChan = nil
				} else if ev != "" {
					select {
					case <-ctx.Done():
						return
					case out <- &llm.Response{Format: llm.FormatClaudeMessages, Body: []byte(ev)}:
					}
				}
			case e, ok := <-errChan:
				if !ok {
					errChan = nil
				} else if e != nil {
					streamErr = e
				}
			}
			if eventChan == nil && errChan == nil {
				if streamErr != nil {
					cs.SetErr(streamErr)
				}
				return
			}
		}
	}()
	return cs
}

// upstreamFromCtx 从 ctx 取出 *llm.Request 并读取 Metadata 中的 upstream。
// nil-safe：缺失或类型错误时返回 nil。
func upstreamFromCtx(ctx context.Context) *config.UpstreamConfig {
	req := adapters.RequestFromContext(ctx)
	if req == nil || req.Metadata == nil {
		return nil
	}
	v, ok := req.Metadata[adapters.MetaUpstreamConfig]
	if !ok {
		return nil
	}
	u, _ := v.(*config.UpstreamConfig)
	return u
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

// parseClaudeUsage 解析 Claude Messages 响应 body 中的 usage 字段。
// 解析失败返回 nil（不影响业务，仅 metrics 缺失）。
func parseClaudeUsage(body []byte) *llm.Usage {
	if len(body) == 0 {
		return nil
	}
	var resp types.ClaudeResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	if resp.Usage == nil {
		return nil
	}
	return &llm.Usage{Format: llm.FormatClaudeMessages, Inner: *resp.Usage}
}

// 编译期接口断言。
var (
	_ pipeline.Inbound  = inboundAdapter{}
	_ pipeline.Outbound = outboundAdapter{}
)
