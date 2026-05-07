// Package chat provides the OpenAI Chat Completions API handler.
//
// 此文件为 PR1 引入的 inbound adapter：把入站 *http.Request 解析为
// llm.Request 中间格式。当前 handler.go 仍走旧 TryUpstreamWithAllKeys 路径，
// 不调用本 adapter；PR2/PR3 切流量时由 pipeline.Factory 注入。
package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/BenedictKing/ccx/internal/llm"
)

// inboundAdapter 实现 pipeline.Inbound，输入是 OpenAI Chat Completions 协议。
//
// 设计原则：
//   - 不做协议级别转换：仅解析出关键字段（model / stream），其余完整保留在
//     RawBody 中供 outbound 复用 PatchPlatformFields 等现有路径。
//   - 不解析 messages / tools 等复杂字段：由 outbound 在调用 buildProviderRequest
//     时按需消费 RawBody。
//   - inbound 可由测试构造一次性使用；不持有可变状态。
type inboundAdapter struct{}

// NewInbound 构造一个 OpenAI Chat 入站 adapter。
func NewInbound() *inboundAdapter { return &inboundAdapter{} }

// Format 返回入站协议格式。
func (inboundAdapter) Format() llm.Format { return llm.FormatOpenAIChat }

// TransformRequest 把入站 OpenAI Chat 请求解析为 llm.Request。
//
// 解析规则：
//   - model：必填字段，缺失返回错误（与 ccx 现有 handler 行为一致）。
//   - stream：可选，缺失则 Stream 字段为 nil。
//   - 其余字段不解析，全部保留在 RawBody 的一份拷贝中，避免外部修改污染。
func (inboundAdapter) TransformRequest(_ context.Context, req *http.Request, body []byte) (*llm.Request, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("chat inbound: empty request body")
	}
	var head struct {
		Model  string `json:"model"`
		Stream *bool  `json:"stream"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return nil, fmt.Errorf("chat inbound: parse body: %w", err)
	}
	if head.Model == "" {
		return nil, fmt.Errorf("chat inbound: model field required")
	}
	return &llm.Request{
		Format:     llm.FormatOpenAIChat,
		Model:      head.Model,
		Stream:     head.Stream,
		RawBody:    append([]byte(nil), body...),
		RawRequest: req,
	}, nil
}

// TransformResponse 把 llm.Response 还原为 OpenAI Chat 字节流。
//
// same-format 直通：直接复用 Body / Header（具体协议级 patch 已经在 outbound
// 完成，inbound 不二次序列化）。
func (inboundAdapter) TransformResponse(_ context.Context, resp *llm.Response) ([]byte, http.Header, error) {
	if resp == nil {
		return nil, nil, fmt.Errorf("chat inbound: nil response")
	}
	hdr := resp.Header
	if hdr == nil {
		hdr = make(http.Header)
	}
	if hdr.Get("Content-Type") == "" {
		hdr = hdr.Clone()
		hdr.Set("Content-Type", "application/json")
	}
	return resp.Body, hdr, nil
}

// TransformStream 把出站统一 *llm.Response 流转为入站协议字节流。
//
// 在 same-format 透传场景下，每个 *llm.Response.Body 直接作为 SSE event 的
// data 字节透出（outbound 已保留原始 SSE 帧）。ctx 取消时 goroutine 立即退出。
func (inboundAdapter) TransformStream(ctx context.Context, in llm.Stream[*llm.Response]) llm.Stream[*llm.StreamEvent] {
	ch := make(chan *llm.StreamEvent)
	go func() {
		defer close(ch)
		for in.Next() {
			cur := in.Current()
			if cur == nil || len(cur.Body) == 0 {
				continue
			}
			data := append([]byte(nil), cur.Body...)
			select {
			case <-ctx.Done():
				return
			case ch <- &llm.StreamEvent{Data: data}:
			}
		}
	}()
	return llm.NewChanStream(ctx, ch, func() error { return in.Close() })
}

// 编译期接口断言：inboundAdapter 满足 pipeline.Inbound（在 outbound_adapter.go
// 末尾统一做，避免每个文件都 import pipeline）。
