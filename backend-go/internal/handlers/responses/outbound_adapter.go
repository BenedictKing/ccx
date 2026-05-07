// Package responses —— PR1 outbound adapter.
//
// outbound 复用 (&providers.ResponsesProvider{SessionManager}).ConvertToProviderRequest
// 不重写转换逻辑（PR1 硬约束）。
//
// 响应路径行为（PR3 T8c 补全，与 messages/chat outbound 同模式）：
//   - same-format（responses → openai-responses）：byte copy via adapters.CopyResponse +
//     parse OpenAI Responses usage（input/output/cached/cache_creation 等）；
//   - cross-format（claude/openai/gemini → openai-responses）：
//     * 非流式：调 ResponsesProvider.ConvertToResponsesResponse 把上游 JSON 转
//       Responses JSON；
//     * 流式：按上游协议复用 converters.ConvertOpenAIChatToResponses /
//       ConvertGeminiStreamToResponses 把 SSE 帧转换为 Responses SSE 帧；
//       Claude 上游通过 ConvertToResponsesResponse 自身的 stream 路径处理。
package responses

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/converters"
	"github.com/BenedictKing/ccx/internal/handlers/adapters"
	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/pipeline"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
)

// outboundAdapter 实现 pipeline.Outbound（OpenAI Responses 出站）。
//
// 持有可选的 SessionManager 以支持 Responses API 的 trace 亲和性；nil 时
// providers.ResponsesProvider 会自动构造默认 SessionManager（见 responses.go
// 第 38-40 行 newDefaultSessionManager 初始化逻辑）。
//
// LastStreamUsage 在 TransformStream 消费完上游 SSE 帧后被填充，供 handler.go
// 的 deferred Finalize 读取——pipeline.Result.Response 在 stream 路径下为 nil，
// 无法走 result.Response.Usage 通道，因此用 outbound 自身字段做侧信道。
type outboundAdapter struct {
	SessionManager *session.SessionManager

	mu              sync.Mutex
	LastStreamUsage *llm.Usage
}

// NewOutbound 构造出站 adapter；sessionManager 可为 nil。
func NewOutbound(sessionManager *session.SessionManager) *outboundAdapter {
	return &outboundAdapter{SessionManager: sessionManager}
}

// TakeStreamUsage 取出最近一次 stream 路径累计的 usage 并清空（幂等）。
func (a *outboundAdapter) TakeStreamUsage() *llm.Usage {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	u := a.LastStreamUsage
	a.LastStreamUsage = nil
	return u
}

func (a *outboundAdapter) setStreamUsage(u *llm.Usage) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.LastStreamUsage = u
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

// TransformResponse 把上游 *http.Response 整体读入并按 cross-format 还原为
// OpenAI Responses 协议字节。
//
// ctx 中由 wire.LBOutboundAdapter 注入 *llm.Request；据此读取 upstream 选择
// 转换路径：
//   - "responses" / 空 → byte copy + parse Responses usage；
//   - 其他（claude/openai/gemini） → 调 ResponsesProvider.ConvertToResponsesResponse
//     转换。
func (o *outboundAdapter) TransformResponse(ctx context.Context, resp *http.Response) (*llm.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("responses outbound: nil response")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("responses outbound: read response body: %w", err)
	}

	upstream := upstreamFromCtx(ctx)
	upstreamType := ""
	if upstream != nil {
		upstreamType = upstream.ServiceType
	}

	if upstreamType == "" || upstreamType == "responses" {
		return adapters.CopyResponse(llm.FormatOpenAIResponses, resp, body, parseResponsesUsage), nil
	}

	provider := &providers.ResponsesProvider{SessionManager: o.SessionManager}
	providerResp := &types.ProviderResponse{
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
		Body:       body,
		Stream:     false,
	}
	responsesResp, err := provider.ConvertToResponsesResponse(providerResp, upstreamType, "")
	if err != nil {
		return nil, fmt.Errorf("responses outbound: convert responses response (%s): %w", upstreamType, err)
	}
	convertedBody, err := json.Marshal(responsesResp)
	if err != nil {
		return nil, fmt.Errorf("responses outbound: marshal responses response: %w", err)
	}

	out := &llm.Response{
		Format:     llm.FormatOpenAIResponses,
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       convertedBody,
	}
	out.Usage = usageFromResponsesUsage(responsesResp.Usage)
	return out, nil
}

// ErrInvalidResponseBody 上游返回 200 但响应体不是合法 JSON。
var ErrInvalidResponseBody = errors.New("responses outbound: invalid response body")

// TransformStream 把上游 *http.Response 流式 body 转换为 *llm.Response 流。
//
// same-format（responses）：按 SSE \n\n 分帧原字节透传，同时累计 Responses 协议
// 的 usage 写到 LastStreamUsage。
// cross-format（openai/gemini） 通过 converters.* 转 Responses SSE 帧。
// claude 上游较为复杂，目前回退到 raw passthrough（保持 PR1 行为，后续 PR
// 处理 claude→responses cross-format stream 的特殊逻辑）。
func (a *outboundAdapter) TransformStream(ctx context.Context, resp *http.Response) llm.Stream[*llm.Response] {
	upstream := upstreamFromCtx(ctx)
	upstreamType := ""
	if upstream != nil {
		upstreamType = upstream.ServiceType
	}

	switch upstreamType {
	case "", "responses":
		return rawSSEPassthrough(ctx, resp, a)
	case "gemini":
		return crossFormatToResponsesStream(ctx, resp, upstreamType, a)
	default:
		// openai / claude / 其他：openai 兼容协议走 chat→responses 转换；
		// claude 上游目前回退到 raw passthrough，避免改写既有 SSE 协议。
		if upstreamType == "claude" {
			return rawSSEPassthrough(ctx, resp, a)
		}
		return crossFormatToResponsesStream(ctx, resp, upstreamType, a)
	}
}

// rawSSEPassthrough 是 same-format 的 SSE \n\n 分帧透传实现。
//
// 透传同时累计 usage：每分出一帧就解析 SSE event 内的 OpenAI Responses usage 字段。
// 若整个流没有任何 SSE `data:` 行（典型场景：上游返回 HTML 错误页），返回
// ErrInvalidResponseBody，让 pipeline 触发 failover（与 PR1 旧路径
// preflightInvalidBody 行为对齐）。
func rawSSEPassthrough(ctx context.Context, resp *http.Response, owner *outboundAdapter) llm.Stream[*llm.Response] {
	ch := make(chan *llm.Response, 16)
	cs := llm.NewChanStream(ctx, ch, func() error { return nil })

	go func() {
		defer close(ch)
		defer func() { _ = resp.Body.Close() }()

		var collected types.Usage
		var frames [][]byte
		seenDataLine := false

		buf := make([]byte, 0, 4096)
		readBuf := make([]byte, 4096)
		for {
			if ctx.Err() != nil {
				finalizeResponsesStreamUsage(owner, collected)
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
					if frameHasSSEDataLine(frame) {
						seenDataLine = true
					}
					frames = append(frames, frame)
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) && len(buf) > 0 {
					frame := append([]byte(nil), buf...)
					if frameHasSSEDataLine(frame) {
						seenDataLine = true
					}
					frames = append(frames, frame)
				}
				break
			}
		}

		// 没有任何 data: 行 = 上游返回非 SSE 字节（如 HTML 错误页）。
		if !seenDataLine && len(frames) > 0 {
			cs.SetErr(fmt.Errorf("%w: no SSE data lines in stream body", ErrInvalidResponseBody))
			return
		}

		// 正常缓冲完成后再统一发送，避免在中途收到 invalid body 时已经先发了一帧。
		for _, frame := range frames {
			collectResponsesStreamFrameUsage(string(frame), &collected)
			select {
			case <-ctx.Done():
				finalizeResponsesStreamUsage(owner, collected)
				return
			case ch <- &llm.Response{Format: llm.FormatOpenAIResponses, Body: frame}:
			}
		}
		finalizeResponsesStreamUsage(owner, collected)
	}()

	return cs
}

// frameHasSSEDataLine 判断 SSE 帧（多行，含末尾 \n\n）是否包含至少一行
// `data:`（OpenAI Responses 协议核心数据行）。
func frameHasSSEDataLine(frame []byte) bool {
	for _, line := range strings.Split(string(frame), "\n") {
		if strings.HasPrefix(strings.TrimRight(line, "\r"), "data:") {
			return true
		}
	}
	return false
}

// crossFormatToResponsesStream 把上游（openai/gemini）SSE 流转为 Responses SSE 流。
//
// 按行读取上游 body（SSE 行而非帧），调对应 converters 函数生成 Responses 事件，
// 每个事件作为独立 *llm.Response 推到下游 channel。usage 仍以 Responses 协议
// 累计到 owner.LastStreamUsage。
func crossFormatToResponsesStream(ctx context.Context, resp *http.Response, upstreamType string, owner *outboundAdapter) llm.Stream[*llm.Response] {
	out := make(chan *llm.Response, 16)
	cs := llm.NewChanStream(ctx, out, func() error { return nil })

	go func() {
		defer close(out)
		defer func() { _ = resp.Body.Close() }()

		var collected types.Usage
		var converterState any

		// SSE 按行读取（converters 期望"逐行喂入"）。
		buf := make([]byte, 0, 4096)
		readBuf := make([]byte, 4096)

		emitEvents := func(events []string) bool {
			for _, ev := range events {
				if ev == "" {
					continue
				}
				collectResponsesStreamFrameUsage(ev, &collected)
				select {
				case <-ctx.Done():
					return false
				case out <- &llm.Response{Format: llm.FormatOpenAIResponses, Body: []byte(ev)}:
				}
			}
			return true
		}

		runLine := func(line []byte) []string {
			switch upstreamType {
			case "gemini":
				return converters.ConvertGeminiStreamToResponses(ctx, "", nil, nil, line, &converterState)
			default:
				return converters.ConvertOpenAIChatToResponses(ctx, "", nil, nil, line, &converterState)
			}
		}

		for {
			if ctx.Err() != nil {
				cs.SetErr(ctx.Err())
				finalizeResponsesStreamUsage(owner, collected)
				return
			}
			n, err := resp.Body.Read(readBuf)
			if n > 0 {
				buf = append(buf, readBuf[:n]...)
				for {
					idx := -1
					for i := 0; i < len(buf); i++ {
						if buf[i] == '\n' {
							idx = i
							break
						}
					}
					if idx < 0 {
						break
					}
					line := append([]byte(nil), buf[:idx]...)
					buf = buf[idx+1:]
					// 去 \r 尾巴
					if len(line) > 0 && line[len(line)-1] == '\r' {
						line = line[:len(line)-1]
					}
					events := runLine(line)
					if !emitEvents(events) {
						finalizeResponsesStreamUsage(owner, collected)
						return
					}
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) && len(buf) > 0 {
					line := append([]byte(nil), buf...)
					if len(line) > 0 && line[len(line)-1] == '\r' {
						line = line[:len(line)-1]
					}
					events := runLine(line)
					_ = emitEvents(events)
				}
				if err != nil && !errors.Is(err, io.EOF) {
					cs.SetErr(err)
				}
				finalizeResponsesStreamUsage(owner, collected)
				return
			}
		}
	}()

	return cs
}

// collectResponsesStreamFrameUsage 从单个 Responses SSE 帧（多行文本）中提取
// usage 字段并合并到 dst。Responses 协议 usage 出现在 response.completed 事件
// 的 response.usage 子对象中。
func collectResponsesStreamFrameUsage(frame string, dst *types.Usage) {
	if dst == nil {
		return
	}
	for _, line := range strings.Split(frame, "\n") {
		jsonStr, ok := extractSSEData(line)
		if !ok {
			continue
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}
		// 顶层 usage（少数实现把 usage 直接放在 data 顶层）。
		mergeResponsesUsageFromMap(dst, data["usage"])
		// response.usage（标准位置）。
		if resp, ok := data["response"].(map[string]interface{}); ok {
			mergeResponsesUsageFromMap(dst, resp["usage"])
		}
	}
}

// extractSSEData 取 "data:..." / "data: ..." 行中 JSON 部分；不是 data 行返回 false。
func extractSSEData(line string) (string, bool) {
	line = strings.TrimRight(line, "\r")
	const prefix = "data:"
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	rest := strings.TrimLeft(line[len(prefix):], " ")
	if rest == "" || rest == "[DONE]" {
		return "", false
	}
	return rest, true
}

// mergeResponsesUsageFromMap 把 Responses usage map 合并进 dst。
//
// 规则：input/output/total 取最大值；cache_* 非零即覆盖；OpenAI 格式
// input_tokens_details.cached_tokens 在 CacheReadInputTokens=0 时回填。
func mergeResponsesUsageFromMap(dst *types.Usage, raw interface{}) {
	u, ok := raw.(map[string]interface{})
	if !ok {
		return
	}
	if v, ok := u["input_tokens"].(float64); ok && int(v) > dst.InputTokens {
		dst.InputTokens = int(v)
	}
	if v, ok := u["output_tokens"].(float64); ok && int(v) > dst.OutputTokens {
		dst.OutputTokens = int(v)
	}
	if v, ok := u["cache_creation_input_tokens"].(float64); ok && int(v) > 0 {
		dst.CacheCreationInputTokens = int(v)
	}
	if v, ok := u["cache_read_input_tokens"].(float64); ok && int(v) > 0 {
		dst.CacheReadInputTokens = int(v)
	}
	if v, ok := u["cache_creation_5m_input_tokens"].(float64); ok && int(v) > 0 {
		dst.CacheCreation5mInputTokens = int(v)
	}
	if v, ok := u["cache_creation_1h_input_tokens"].(float64); ok && int(v) > 0 {
		dst.CacheCreation1hInputTokens = int(v)
	}
	// OpenAI 格式 cached_tokens
	if details, ok := u["input_tokens_details"].(map[string]interface{}); ok {
		if v, ok := details["cached_tokens"].(float64); ok && int(v) > 0 && dst.CacheReadInputTokens == 0 {
			dst.CacheReadInputTokens = int(v)
		}
	}
}

// finalizeResponsesStreamUsage 把累计的 SSE usage 写到 outbound 的 LastStreamUsage。
func finalizeResponsesStreamUsage(owner *outboundAdapter, collected types.Usage) {
	if owner == nil {
		return
	}
	if collected.InputTokens == 0 && collected.OutputTokens == 0 &&
		collected.CacheReadInputTokens == 0 && collected.CacheCreationInputTokens == 0 &&
		collected.CacheCreation5mInputTokens == 0 && collected.CacheCreation1hInputTokens == 0 {
		return
	}
	owner.setStreamUsage(&llm.Usage{Format: llm.FormatOpenAIResponses, Inner: collected})
	log.Printf("[Responses-Outbound] stream usage finalized: input=%d output=%d cache_read=%d cache_creation=%d",
		collected.InputTokens, collected.OutputTokens, collected.CacheReadInputTokens, collected.CacheCreationInputTokens)
}

// parseResponsesUsage 解析 OpenAI Responses 非流式响应 body 中的 usage 字段。
func parseResponsesUsage(body []byte) *llm.Usage {
	if len(body) == 0 {
		return nil
	}
	var resp types.ResponsesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	return usageFromResponsesUsage(resp.Usage)
}

// usageFromResponsesUsage 把 types.ResponsesUsage 映射为 *llm.Usage。
func usageFromResponsesUsage(u types.ResponsesUsage) *llm.Usage {
	cacheRead := u.CacheReadInputTokens
	if cacheRead == 0 && u.InputTokensDetails != nil && u.InputTokensDetails.CachedTokens > 0 {
		cacheRead = u.InputTokensDetails.CachedTokens
	}
	if u.InputTokens == 0 && u.OutputTokens == 0 && cacheRead == 0 &&
		u.CacheCreationInputTokens == 0 && u.CacheCreation5mInputTokens == 0 && u.CacheCreation1hInputTokens == 0 {
		return nil
	}
	return &llm.Usage{
		Format: llm.FormatOpenAIResponses,
		Inner: types.Usage{
			InputTokens:                u.InputTokens,
			OutputTokens:               u.OutputTokens,
			CacheCreationInputTokens:   u.CacheCreationInputTokens,
			CacheReadInputTokens:       cacheRead,
			CacheCreation5mInputTokens: u.CacheCreation5mInputTokens,
			CacheCreation1hInputTokens: u.CacheCreation1hInputTokens,
			CacheTTL:                   u.CacheTTL,
		},
	}
}

// upstreamFromCtx 从 ctx 取 *llm.Request.Metadata 中的 upstream。nil-safe。
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

// 编译期接口断言。
var (
	_ pipeline.Inbound  = inboundAdapter{}
	_ pipeline.Outbound = (*outboundAdapter)(nil)
)
