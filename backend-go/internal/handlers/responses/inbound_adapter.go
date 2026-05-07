// Package responses —— PR1 inbound adapter for OpenAI Responses API.
package responses

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/types"
)

// inboundAdapter 实现 pipeline.Inbound（OpenAI Responses 入站）。
type inboundAdapter struct{}

// NewInbound 构造入站 adapter。
func NewInbound() *inboundAdapter { return &inboundAdapter{} }

// Format 返回入站协议格式。
func (inboundAdapter) Format() llm.Format { return llm.FormatOpenAIResponses }

// MetaParsedRequest 是 inbound 写到 Metadata 的键，承载 *types.ResponsesRequest。
//
// outbound 不直接消费它（providers.ResponsesProvider 通过 c.Get(requestBodyBytes)
// 读取原始字节自己反序列化）；保留该键作为可选辅助，供未来 cross-format 转换使用。
const MetaParsedRequest = "responses:parsedRequest"

// TransformRequest 解析 Responses 请求。
func (inboundAdapter) TransformRequest(_ context.Context, req *http.Request, body []byte) (*llm.Request, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("responses inbound: empty request body")
	}
	parsed := &types.ResponsesRequest{}
	if err := json.Unmarshal(body, parsed); err != nil {
		return nil, fmt.Errorf("responses inbound: parse body: %w", err)
	}
	if parsed.Model == "" {
		return nil, fmt.Errorf("responses inbound: model field required")
	}
	stream := parsed.Stream
	out := &llm.Request{
		Format:     llm.FormatOpenAIResponses,
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

// TransformResponse 把 llm.Response 还原为 Responses 字节流。same-format 直通。
func (inboundAdapter) TransformResponse(_ context.Context, resp *llm.Response) ([]byte, http.Header, error) {
	if resp == nil {
		return nil, nil, fmt.Errorf("responses inbound: nil response")
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

// TransformStream 把 *llm.Response 流转为 Responses SSE 字节流（same-format 透传）。
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
