// Package chat —— PR1 outbound adapter for OpenAI Chat Completions.
//
// outbound 负责"构造发往上游的 *http.Request"与"把上游响应转换为 llm.Response"。
// 实现内部 100% 复用 chat.buildProviderRequest（handler.go 已有），不重写协议
// 转换逻辑（PR1 硬约束）。
package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/BenedictKing/ccx/internal/handlers/adapters"
	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/pipeline"
)

// outboundAdapter 实现 pipeline.Outbound（OpenAI Chat 出站）。
//
// 工作模式：
//   - 上游 channel/key 由调度器/loadbalancer 在 attempt 之前写入 llm.Request.Metadata
//     （adapters.MetaUpstreamConfig / MetaAPIKey / MetaBaseURL）。
//   - inbound 在 TransformRequest 阶段写入 adapters.MetaGinContext。
//   - outbound.TransformRequest 取出三件套 + gin.Context，调用 buildProviderRequest
//     得到完整的发送请求；不向 channel 状态做任何修改（PR1 不做 retry 接入）。
//
// PR1 阶段不实现 Retryable / ChannelRetryable / ChannelCustomizedExecutor —— PR2
// 在 LoadBalancer 上落 Retryable，本 adapter 仅作为"纯转换器"使用。
type outboundAdapter struct{}

// NewOutbound 构造一个 OpenAI Chat 出站 adapter。
func NewOutbound() *outboundAdapter { return &outboundAdapter{} }

// TransformRequest 调用现有 buildProviderRequest 构造发送请求。
//
// 注意：buildProviderRequest 内部读取 c.Request.URL.Path 等字段判断 raw passthrough
// 行为，因此 *gin.Context 必须真实可用；测试请用 gin.CreateTestContext 构造。
func (outboundAdapter) TransformRequest(_ context.Context, req *llm.Request) (*http.Request, []byte, error) {
	if req == nil {
		return nil, nil, fmt.Errorf("chat outbound: nil request")
	}
	c, err := adapters.GinContext(req)
	if err != nil {
		return nil, nil, err
	}
	upstream, apiKey, baseURL, err := adapters.UpstreamBinding(req)
	if err != nil {
		return nil, nil, err
	}
	httpReq, err := buildProviderRequest(c, upstream, baseURL, apiKey, req.RawBody, req.Model, req.IsStream())
	if err != nil {
		return nil, nil, fmt.Errorf("chat outbound: build provider request: %w", err)
	}
	// realBody 与 RawBody 对齐：buildProviderRequest 内部根据 ServiceType 可能
	// 重新打包 body（如 claude 上游会转换协议），但具体 bytes 已经放在
	// httpReq.Body。我们这里返回 RawBody 的拷贝作为"日志用 realBody"占位，
	// 真正的字节由 forwarding.PreparedRequest 统一管理。
	realBody := append([]byte(nil), req.RawBody...)
	return httpReq, realBody, nil
}

// TransformResponse 把上游 *http.Response 整体读入并转换为 llm.Response。
//
// 注意：本函数会消费并关闭 resp.Body。如果调用方需要 raw passthrough，应在
// pipeline.Middleware.RawResponse 阶段拦截，不要走 TransformResponse 路径。
func (outboundAdapter) TransformResponse(_ context.Context, resp *http.Response) (*llm.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("chat outbound: nil response")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("chat outbound: read response body: %w", err)
	}
	return adapters.CopyResponse(llm.FormatOpenAIChat, resp, body, nil), nil
}

// TransformStream 把上游 SSE 流式 body 转换为 llm.Stream[*llm.Response]。
//
// same-format 透传：每行 "data: ..." 帧整体作为一个 *llm.Response.Body 推出，
// 让 inbound.TransformStream 直接复制为 SSE event。ctx 取消时 goroutine 立即
// 退出并关闭 resp.Body。
//
// 注意：当前实现刻意保持简单（按 SSE 规范的 \n\n 分帧），不做 usage / 错误
// 解析；PR2 引入 LlmStream middleware 后再叠加。
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
				// 按 \n\n 切帧。
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
					case ch <- &llm.Response{Format: llm.FormatOpenAIChat, Body: frame}:
					}
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					if len(buf) > 0 {
						frame := append([]byte(nil), buf...)
						select {
						case <-ctx.Done():
						case ch <- &llm.Response{Format: llm.FormatOpenAIChat, Body: frame}:
						}
					}
				}
				return
			}
		}
	}()

	return llm.NewChanStream(ctx, ch, func() error { return nil })
}

// indexDoubleNewline 返回 \n\n 的起始下标；找不到返回 -1。也兼容 \r\n\r\n。
func indexDoubleNewline(b []byte) int {
	for i := 0; i+1 < len(b); i++ {
		if b[i] == '\n' && b[i+1] == '\n' {
			return i
		}
		if i+3 < len(b) && b[i] == '\r' && b[i+1] == '\n' && b[i+2] == '\r' && b[i+3] == '\n' {
			return i + 2 // 让 idx+2 指向第二个 \n 之后
		}
	}
	return -1
}

// 编译期接口断言。这里集中放在 outbound 文件，避免 inbound 文件也 import pipeline。
var (
	_ pipeline.Inbound  = inboundAdapter{}
	_ pipeline.Outbound = outboundAdapter{}
)
