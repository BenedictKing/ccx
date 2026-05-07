// Package messages —— PR1 inbound adapter for Claude Messages API.
package messages

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/types"
)

// inboundAdapter 实现 pipeline.Inbound（Claude Messages 入站）。
type inboundAdapter struct{}

// NewInbound 构造 Claude Messages 入站 adapter。
func NewInbound() *inboundAdapter { return &inboundAdapter{} }

// Format 返回入站协议格式。
func (inboundAdapter) Format() llm.Format { return llm.FormatClaudeMessages }

// MetaParsedRequest 是 inbound 写到 Metadata 的键，承载 *types.ClaudeRequest。
const MetaParsedRequest = "messages:parsedRequest"

// TransformRequest 解析 Claude Messages 请求并写入 Metadata。
//
// model 与 stream 都来自 body 字段。outbound 复用 providers.ClaudeProvider /
// providers.OpenAIProvider 等，那些 provider 通过 c.Get("requestBodyBytes")
// 获取 body 字节，因此 adapter 不需要在 Metadata 里再放一份解析结果（保留 ParsedRequest
// 仅作为可选辅助，不是必需）。
func (inboundAdapter) TransformRequest(_ context.Context, req *http.Request, body []byte) (*llm.Request, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("messages inbound: empty request body")
	}
	parsed := &types.ClaudeRequest{}
	if err := json.Unmarshal(body, parsed); err != nil {
		return nil, fmt.Errorf("messages inbound: parse body: %w", err)
	}
	if parsed.Model == "" {
		return nil, fmt.Errorf("messages inbound: model field required")
	}
	stream := parsed.Stream
	out := &llm.Request{
		Format:     llm.FormatClaudeMessages,
		Model:      parsed.Model,
		Stream:     &stream,
		RawBody:    append([]byte(nil), body...),
		RawRequest: req,
		Metadata: map[string]any{
			MetaParsedRequest: parsed,
		},
	}
	return out, nil
}

// TransformResponse 把 llm.Response 还原为 Claude Messages 字节流。same-format 直通。
func (inboundAdapter) TransformResponse(_ context.Context, resp *llm.Response) ([]byte, http.Header, error) {
	if resp == nil {
		return nil, nil, fmt.Errorf("messages inbound: nil response")
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

// TransformStream 把 *llm.Response 流转为 Claude SSE 字节流（same-format 透传）。
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
