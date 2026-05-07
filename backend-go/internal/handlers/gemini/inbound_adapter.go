// Package gemini —— PR1 inbound adapter for Gemini Contents API.
//
// Gemini API 的特殊点：
//   - model 通过 URL 路径携带（/v1beta/models/{model}:generateContent），不是 body 字段。
//   - isStream 通过 URL action 区分（generateContent vs streamGenerateContent）。
//
// 本 adapter 在 TransformRequest 阶段解析 URL 路径以提取 model + isStream，
// 与 handler.go 内 extractModelName / streamGenerateContent 的判断逻辑保持一致。
package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/types"
)

// inboundAdapter 实现 pipeline.Inbound（Gemini Contents 入站）。
type inboundAdapter struct{}

// NewInbound 构造入站 adapter。
func NewInbound() *inboundAdapter { return &inboundAdapter{} }

// Format 返回入站协议格式。
func (inboundAdapter) Format() llm.Format { return llm.FormatGeminiContents }

// TransformRequest 解析 Gemini 入站请求。
//
// 输入：
//   - req.URL.Path：必须包含 /models/{model}:{action}；adapter 提取 model 与 isStream。
//   - body：JSON 序列化的 types.GeminiRequest；adapter 反序列化以便 outbound 直接复用。
//
// llm.Request.Metadata 上写入键 "gemini:parsedRequest" → *types.GeminiRequest，
// 让 outbound 不必重复反序列化（buildProviderRequest 只接受已解析对象）。
func (inboundAdapter) TransformRequest(_ context.Context, req *http.Request, body []byte) (*llm.Request, error) {
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("gemini inbound: nil request or url")
	}
	model, isStream, err := parseGeminiRoute(req.URL.Path)
	if err != nil {
		return nil, err
	}

	parsed := &types.GeminiRequest{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, parsed); err != nil {
			return nil, fmt.Errorf("gemini inbound: parse body: %w", err)
		}
	}

	stream := isStream
	out := &llm.Request{
		Format:     llm.FormatGeminiContents,
		Model:      model,
		Stream:     &stream,
		RawBody:    append([]byte(nil), body...),
		RawRequest: req,
		Metadata: map[string]any{
			MetaParsedRequest: parsed,
		},
	}
	return out, nil
}

// MetaParsedRequest 是 inbound 写到 Metadata 的键，承载 *types.GeminiRequest。
const MetaParsedRequest = "gemini:parsedRequest"

// parseGeminiRoute 从 URL.Path 提取 model 与 isStream。
//
// 期望格式：
//   - /v1beta/models/{model}:generateContent
//   - /v1beta/models/{model}:streamGenerateContent
//   - 其他路径只要包含 "/models/{model}:..." 也接受。
func parseGeminiRoute(path string) (model string, isStream bool, err error) {
	idx := strings.Index(path, "/models/")
	if idx < 0 {
		return "", false, fmt.Errorf("gemini inbound: missing /models/ in path %q", path)
	}
	tail := path[idx+len("/models/"):]
	tail = strings.TrimPrefix(tail, "/")
	colonIdx := strings.Index(tail, ":")
	if colonIdx < 0 {
		// 兼容无 action 的形态（极端 case，理论上不应该到达）
		if tail == "" {
			return "", false, fmt.Errorf("gemini inbound: empty model in path %q", path)
		}
		return tail, false, nil
	}
	model = tail[:colonIdx]
	action := tail[colonIdx+1:]
	isStream = strings.Contains(action, "streamGenerateContent")
	if model == "" {
		return "", false, fmt.Errorf("gemini inbound: empty model in path %q", path)
	}
	return model, isStream, nil
}

// TransformResponse 把 llm.Response 还原为 Gemini 字节流。same-format 直通。
func (inboundAdapter) TransformResponse(_ context.Context, resp *llm.Response) ([]byte, http.Header, error) {
	if resp == nil {
		return nil, nil, fmt.Errorf("gemini inbound: nil response")
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

// TransformStream 把 *llm.Response 流转为 Gemini SSE 字节流（same-format 透传）。
//
// 与 chat 实现一致：每个 *llm.Response.Body 直接作为 SSE event 透出。
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
