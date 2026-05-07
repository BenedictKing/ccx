// Package gemini —— PR1 outbound adapter for Gemini Contents API.
//
// outbound 复用 gemini.buildProviderRequest（handler.go 内已有），不重写
// 协议转换逻辑（PR1 硬约束）。
//
// 响应路径行为（PR3 T8d 补全，与 messages/chat/responses outbound 同模式）：
//   - same-format（gemini → gemini）：byte copy via adapters.CopyResponse + parse
//     Gemini usage（usageMetadata.promptTokenCount / candidatesTokenCount /
//     cachedContentTokenCount）；
//   - cross-format（claude/openai/responses → gemini）：
//     * 非流式：复用 handler.go 的 ClaudeResponseToGemini / OpenAIResponseToGemini
//       / ResponsesResponseToGemini 把上游 JSON 转 Gemini JSON；
//     * 流式：复用 handler.go (stream.go) 的 streamClaudeToGemini /
//       streamOpenAIToGemini / streamResponsesToGemini 等转换逻辑——这里通过
//       内部缓冲 fakeWriter 把转换函数的字节产出收集为独立 SSE 帧。
package gemini

import (
	"bufio"
	"bytes"
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
	"github.com/BenedictKing/ccx/internal/types"
)

// outboundAdapter 实现 pipeline.Outbound（Gemini Contents 出站）。
//
// LastStreamUsage 在 TransformStream 消费完上游 SSE 帧后被填充，供 handler.go
// 的 deferred Finalize 读取——pipeline.Result.Response 在 stream 路径下为 nil，
// 无法走 result.Response.Usage 通道，因此用 outbound 自身字段做侧信道（与
// messages/chat/responses outbound 一致）。
type outboundAdapter struct {
	mu              sync.Mutex
	LastStreamUsage *llm.Usage
}

// NewOutbound 构造出站 adapter。
func NewOutbound() *outboundAdapter { return &outboundAdapter{} }

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

// TransformRequest 调用现有 buildProviderRequest 构造发送请求。
//
// 从 llm.Request.Metadata 读取：
//   - inbound 写入的 *types.GeminiRequest（避免重复反序列化）；
//   - adapters 共享的 gin.Context / upstream / apiKey / baseURL。
func (a *outboundAdapter) TransformRequest(_ context.Context, req *llm.Request) (*http.Request, []byte, error) {
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

// TransformResponse 把上游 *http.Response 整体读入并按 cross-format 还原为
// Gemini 协议字节。
//
// ctx 中由 wire.LBOutboundAdapter 注入 *llm.Request；据此读取 upstream 选择
// 转换路径：
//   - "gemini" / 空 → byte copy + parse Gemini usage；
//   - claude → ClaudeResponseToGemini；
//   - openai → OpenAIResponseToGemini；
//   - responses → ResponsesResponseToGemini。
func (a *outboundAdapter) TransformResponse(ctx context.Context, resp *http.Response) (*llm.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("gemini outbound: nil response")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gemini outbound: read response body: %w", err)
	}

	upstream := upstreamFromCtx(ctx)
	upstreamType := ""
	if upstream != nil {
		upstreamType = upstream.ServiceType
	}

	if upstreamType == "" || upstreamType == "gemini" {
		return adapters.CopyResponse(llm.FormatGeminiContents, resp, body, parseGeminiUsage), nil
	}

	var geminiResp *types.GeminiResponse
	switch upstreamType {
	case "claude":
		var claudeResp map[string]interface{}
		if err := json.Unmarshal(body, &claudeResp); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidResponseBody, err)
		}
		geminiResp, err = converters.ClaudeResponseToGemini(claudeResp)
		if err != nil {
			return nil, fmt.Errorf("gemini outbound: convert claude response: %w", err)
		}
	case "openai":
		var openaiResp map[string]interface{}
		if err := json.Unmarshal(body, &openaiResp); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidResponseBody, err)
		}
		geminiResp, err = converters.OpenAIResponseToGemini(openaiResp)
		if err != nil {
			return nil, fmt.Errorf("gemini outbound: convert openai response: %w", err)
		}
	case "responses":
		var responsesResp map[string]interface{}
		if err := json.Unmarshal(body, &responsesResp); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidResponseBody, err)
		}
		geminiResp, err = converters.ResponsesResponseToGemini(responsesResp)
		if err != nil {
			return nil, fmt.Errorf("gemini outbound: convert responses response: %w", err)
		}
	default:
		// 未知类型：按 same-format 透传。
		return adapters.CopyResponse(llm.FormatGeminiContents, resp, body, parseGeminiUsage), nil
	}

	convertedBody, err := json.Marshal(geminiResp)
	if err != nil {
		return nil, fmt.Errorf("gemini outbound: marshal gemini response: %w", err)
	}
	out := &llm.Response{
		Format:     llm.FormatGeminiContents,
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       convertedBody,
	}
	if geminiResp.UsageMetadata != nil {
		out.Usage = usageFromGeminiMetadata(geminiResp.UsageMetadata)
	}
	return out, nil
}

// ErrInvalidResponseBody 上游返回 200 但响应体不是合法 JSON。
var ErrInvalidResponseBody = errors.New("gemini outbound: invalid response body")

// TransformStream 把上游 *http.Response 流式 body 转换为 *llm.Response 流。
//
// same-format（gemini）：按 SSE \n\n 分帧原字节透传，同时累计 usage。
// cross-format（claude/openai/responses）：
//   - claude / openai：复用本包 stream.go 的 streamClaudeToGemini /
//     streamOpenAIToGemini，通过 fakeWriter 收集字节作为独立帧；
//   - responses：复用 converters.ConvertResponsesToGeminiStream（按行喂入）。
//
// no-data-line guard：rawSSEPassthrough 若整个流没有任何 `data:` 行（典型
// 场景上游返回 HTML 错误页），返回 ErrInvalidResponseBody，让 pipeline 触发
// failover（与 responses outbound 行为对齐）。
func (a *outboundAdapter) TransformStream(ctx context.Context, resp *http.Response) llm.Stream[*llm.Response] {
	upstream := upstreamFromCtx(ctx)
	upstreamType := ""
	model := ""
	if upstream != nil {
		upstreamType = upstream.ServiceType
	}
	if req := adapters.RequestFromContext(ctx); req != nil {
		model = req.Model
	}

	switch upstreamType {
	case "", "gemini":
		return rawSSEPassthrough(ctx, resp, a)
	case "claude":
		return crossFormatClaudeToGeminiStream(ctx, resp, model, a)
	case "openai":
		return crossFormatOpenAIToGeminiStream(ctx, resp, model, a)
	case "responses":
		return crossFormatResponsesToGeminiStream(ctx, resp, model, a)
	default:
		return rawSSEPassthrough(ctx, resp, a)
	}
}

// rawSSEPassthrough 是 same-format 的 SSE \n\n 分帧透传实现。
//
// 透传同时累计 usage：每分出一帧就解析 SSE event 内的 Gemini usageMetadata。
// no-data-line guard：若整个流读完后没有任何 SSE `data:` 帧（典型场景：上游
// 返回非 SSE 字节，例如 HTML 错误页），通过 ChanStream.SetErr 抛出
// ErrInvalidResponseBody，让 pipeline 触发 failover（对齐 responses outbound）。
//
// 关键：每分出一帧立即推到 channel（而非缓冲全部），保证：
//   - sse_error_event 中间件能第一时间看到上游 SSE error frame 触发 failover；
//   - 上游若卡住等 ctx 关闭，pipeline 取消 ctx 时本 goroutine 也能立即退出。
func rawSSEPassthrough(ctx context.Context, resp *http.Response, owner *outboundAdapter) llm.Stream[*llm.Response] {
	ch := make(chan *llm.Response, 16)
	cs := llm.NewChanStream(ctx, ch, func() error { return nil })

	go func() {
		defer close(ch)
		defer func() { _ = resp.Body.Close() }()

		var collected types.Usage
		seenDataLine := false
		emittedAny := false

		emit := func(frame []byte) bool {
			if frameHasSSEDataLine(frame) {
				seenDataLine = true
			}
			collectGeminiStreamFrameUsage(string(frame), &collected)
			select {
			case <-ctx.Done():
				return false
			case ch <- &llm.Response{Format: llm.FormatGeminiContents, Body: frame}:
				emittedAny = true
				return true
			}
		}

		buf := make([]byte, 0, 4096)
		readBuf := make([]byte, 4096)
		for {
			if ctx.Err() != nil {
				finalizeStreamUsage(owner, collected)
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
					if !emit(frame) {
						finalizeStreamUsage(owner, collected)
						return
					}
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) && len(buf) > 0 {
					frame := append([]byte(nil), buf...)
					_ = emit(frame)
				}
				// 没有任何 data: 帧 = 上游返回非 SSE 字节（如 HTML 错误页）。
				// 仅当尚未推过任何帧时才报错（已经推过的内容会被下游消费，不能反悔）。
				if !seenDataLine && !emittedAny {
					cs.SetErr(fmt.Errorf("%w: no SSE data lines in stream body", ErrInvalidResponseBody))
				}
				finalizeStreamUsage(owner, collected)
				return
			}
		}
	}()

	return cs
}

// frameHasSSEDataLine 判断 SSE 帧（多行，含末尾 \n\n）是否包含至少一行 `data:`。
func frameHasSSEDataLine(frame []byte) bool {
	for _, line := range strings.Split(string(frame), "\n") {
		if strings.HasPrefix(strings.TrimRight(line, "\r"), "data:") {
			return true
		}
	}
	return false
}

// crossFormatClaudeToGeminiStream 把 Claude SSE 流转为 Gemini SSE 流。
// 通过对 SSE 行调用 buildGeminiChunkFromClaudeEvent 生成 Gemini chunk 字节。
func crossFormatClaudeToGeminiStream(ctx context.Context, resp *http.Response, model string, owner *outboundAdapter) llm.Stream[*llm.Response] {
	out := make(chan *llm.Response, 16)
	cs := llm.NewChanStream(ctx, out, func() error { return nil })

	go func() {
		defer close(out)
		defer func() { _ = resp.Body.Close() }()

		var collected types.Usage
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

		emitFrame := func(frame []byte) bool {
			collectGeminiStreamFrameUsage(string(frame), &collected)
			select {
			case <-ctx.Done():
				return false
			case out <- &llm.Response{Format: llm.FormatGeminiContents, Body: frame}:
				return true
			}
		}

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			jsonData := strings.TrimPrefix(line, "data: ")
			if jsonData == "[DONE]" {
				break
			}
			frames := convertClaudeSSEEventToGeminiChunks(jsonData)
			for _, fr := range frames {
				if !emitFrame(fr) {
					finalizeStreamUsage(owner, collected)
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			cs.SetErr(err)
		}
		finalizeStreamUsage(owner, collected)
	}()
	return cs
}

// convertClaudeSSEEventToGeminiChunks 把单个 Claude SSE event JSON 串转换为 0..N
// 个 Gemini SSE chunk 字节（含末尾 "\n\n"）。逻辑对齐 stream.go 的
// streamClaudeToGemini。
func convertClaudeSSEEventToGeminiChunks(jsonData string) [][]byte {
	var event map[string]interface{}
	if err := json.Unmarshal([]byte(jsonData), &event); err != nil {
		return nil
	}
	eventType, _ := event["type"].(string)
	switch eventType {
	case "content_block_delta":
		delta, ok := event["delta"].(map[string]interface{})
		if !ok {
			return nil
		}
		deltaType, _ := delta["type"].(string)
		if deltaType != "text_delta" {
			return nil
		}
		text, _ := delta["text"].(string)
		geminiChunk := types.GeminiStreamChunk{
			Candidates: []types.GeminiCandidate{
				{
					Content: &types.GeminiContent{
						Parts: []types.GeminiPart{{Text: text}},
						Role:  "model",
					},
				},
			},
		}
		b, _ := json.Marshal(geminiChunk)
		return [][]byte{[]byte(fmt.Sprintf("data: %s\n\n", string(b)))}
	case "message_delta":
		usage, _ := event["usage"].(map[string]interface{})
		inputTokens, outputTokens := 0, 0
		if usage != nil {
			if v, ok := usage["input_tokens"].(float64); ok {
				inputTokens = int(v)
			}
			if v, ok := usage["output_tokens"].(float64); ok {
				outputTokens = int(v)
			}
		}
		geminiChunk := types.GeminiStreamChunk{
			Candidates: []types.GeminiCandidate{{FinishReason: "STOP"}},
			UsageMetadata: &types.GeminiUsageMetadata{
				PromptTokenCount:     inputTokens,
				CandidatesTokenCount: outputTokens,
				TotalTokenCount:      inputTokens + outputTokens,
			},
		}
		b, _ := json.Marshal(geminiChunk)
		return [][]byte{[]byte(fmt.Sprintf("data: %s\n\n", string(b)))}
	}
	return nil
}

// crossFormatOpenAIToGeminiStream 把 OpenAI Chat SSE 流转为 Gemini SSE 流。
// 逻辑对齐 stream.go 的 streamOpenAIToGemini。
func crossFormatOpenAIToGeminiStream(ctx context.Context, resp *http.Response, model string, owner *outboundAdapter) llm.Stream[*llm.Response] {
	out := make(chan *llm.Response, 16)
	cs := llm.NewChanStream(ctx, out, func() error { return nil })

	go func() {
		defer close(out)
		defer func() { _ = resp.Body.Close() }()

		var collected types.Usage
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

		emitFrame := func(frame []byte) bool {
			collectGeminiStreamFrameUsage(string(frame), &collected)
			select {
			case <-ctx.Done():
				return false
			case out <- &llm.Response{Format: llm.FormatGeminiContents, Body: frame}:
				return true
			}
		}

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			jsonData := strings.TrimPrefix(line, "data: ")
			if jsonData == "[DONE]" {
				break
			}
			frames := convertOpenAISSEChunkToGeminiChunks(jsonData)
			for _, fr := range frames {
				if !emitFrame(fr) {
					finalizeStreamUsage(owner, collected)
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			cs.SetErr(err)
		}
		finalizeStreamUsage(owner, collected)
	}()
	return cs
}

// convertOpenAISSEChunkToGeminiChunks 把单个 OpenAI Chat SSE chunk JSON 串
// 转为 0..N 个 Gemini SSE chunk。对齐 stream.go 的 streamOpenAIToGemini。
func convertOpenAISSEChunkToGeminiChunks(jsonData string) [][]byte {
	var chunk map[string]interface{}
	if err := json.Unmarshal([]byte(jsonData), &chunk); err != nil {
		return nil
	}
	var out [][]byte

	choices, _ := chunk["choices"].([]interface{})
	if len(choices) == 0 {
		// 仅 usage 的尾帧（如 stream_options.include_usage=true）。
		if usage, ok := chunk["usage"].(map[string]interface{}); ok {
			promptTokens, completionTokens := 0, 0
			if v, ok := usage["prompt_tokens"].(float64); ok {
				promptTokens = int(v)
			}
			if v, ok := usage["completion_tokens"].(float64); ok {
				completionTokens = int(v)
			}
			gc := types.GeminiStreamChunk{
				UsageMetadata: &types.GeminiUsageMetadata{
					PromptTokenCount:     promptTokens,
					CandidatesTokenCount: completionTokens,
					TotalTokenCount:      promptTokens + completionTokens,
				},
			}
			b, _ := json.Marshal(gc)
			out = append(out, []byte(fmt.Sprintf("data: %s\n\n", string(b))))
		}
		return out
	}

	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return out
	}
	finishReason, hasFinish := choice["finish_reason"].(string)
	delta, _ := choice["delta"].(map[string]interface{})
	if delta != nil {
		if content, _ := delta["content"].(string); content != "" {
			gc := types.GeminiStreamChunk{
				Candidates: []types.GeminiCandidate{
					{
						Content: &types.GeminiContent{
							Parts: []types.GeminiPart{{Text: content}},
							Role:  "model",
						},
					},
				},
			}
			b, _ := json.Marshal(gc)
			out = append(out, []byte(fmt.Sprintf("data: %s\n\n", string(b))))
		}
	}
	if hasFinish && finishReason != "" {
		gc := types.GeminiStreamChunk{
			Candidates: []types.GeminiCandidate{
				{FinishReason: openaiFinishReasonToGemini(finishReason)},
			},
		}
		b, _ := json.Marshal(gc)
		out = append(out, []byte(fmt.Sprintf("data: %s\n\n", string(b))))
	}
	return out
}

// crossFormatResponsesToGeminiStream 把 Responses SSE 流按行喂入
// converters.ConvertResponsesToGeminiStream，得到 Gemini SSE 帧字符串列表。
func crossFormatResponsesToGeminiStream(ctx context.Context, resp *http.Response, model string, owner *outboundAdapter) llm.Stream[*llm.Response] {
	out := make(chan *llm.Response, 16)
	cs := llm.NewChanStream(ctx, out, func() error { return nil })

	go func() {
		defer close(out)
		defer func() { _ = resp.Body.Close() }()

		var collected types.Usage
		var converterState any
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

		emitFrame := func(frame []byte) bool {
			collectGeminiStreamFrameUsage(string(frame), &collected)
			select {
			case <-ctx.Done():
				return false
			case out <- &llm.Response{Format: llm.FormatGeminiContents, Body: frame}:
				return true
			}
		}

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			events := converters.ConvertResponsesToGeminiStream(ctx, model, append([]byte(nil), line...), &converterState)
			for _, ev := range events {
				if ev == "" {
					continue
				}
				if !emitFrame([]byte(ev)) {
					finalizeStreamUsage(owner, collected)
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			cs.SetErr(err)
		}
		finalizeStreamUsage(owner, collected)
	}()
	return cs
}

// collectGeminiStreamFrameUsage 从单个 Gemini SSE 帧（多行文本）中提取
// usageMetadata 并合并到 dst（input/output 取最大值，cache 字段非零即覆盖）。
func collectGeminiStreamFrameUsage(frame string, dst *types.Usage) {
	if dst == nil {
		return
	}
	for _, line := range strings.Split(frame, "\n") {
		jsonStr, ok := extractSSEData(line)
		if !ok {
			continue
		}
		var chunk types.GeminiStreamChunk
		if err := json.Unmarshal([]byte(jsonStr), &chunk); err != nil {
			continue
		}
		if chunk.UsageMetadata == nil {
			continue
		}
		mergeUsageFromGeminiMetadata(dst, chunk.UsageMetadata)
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

// mergeUsageFromGeminiMetadata 把 Gemini usageMetadata 合并到 dst：
//   - PromptTokensTotal 跟踪原始 promptTokenCount（取最大值）；
//   - CacheReadInputTokens 跟踪 cachedContentTokenCount（非零即覆盖最大值）；
//   - InputTokens = PromptTokensTotal - CacheReadInputTokens（每次合并都重算，
//     避免 prompt / cached 在不同帧中先后到达时的顺序问题，与 stream.go 行为对齐）；
//   - OutputTokens = candidatesTokenCount（取最大值）。
func mergeUsageFromGeminiMetadata(dst *types.Usage, m *types.GeminiUsageMetadata) {
	if m == nil {
		return
	}
	if m.PromptTokenCount > dst.PromptTokensTotal {
		dst.PromptTokensTotal = m.PromptTokenCount
	}
	if m.CachedContentTokenCount > dst.CacheReadInputTokens {
		dst.CacheReadInputTokens = m.CachedContentTokenCount
	}
	if m.CandidatesTokenCount > dst.OutputTokens {
		dst.OutputTokens = m.CandidatesTokenCount
	}
	input := dst.PromptTokensTotal - dst.CacheReadInputTokens
	if input < 0 {
		input = 0
	}
	dst.InputTokens = input
}

// finalizeStreamUsage 把累计的 SSE usage 写到 outbound 的 LastStreamUsage。
func finalizeStreamUsage(owner *outboundAdapter, collected types.Usage) {
	if owner == nil {
		return
	}
	if collected.InputTokens == 0 && collected.OutputTokens == 0 &&
		collected.CacheReadInputTokens == 0 {
		return
	}
	owner.setStreamUsage(&llm.Usage{Format: llm.FormatGeminiContents, Inner: collected})
	log.Printf("[Gemini-Outbound] stream usage finalized: input=%d output=%d cache_read=%d",
		collected.InputTokens, collected.OutputTokens, collected.CacheReadInputTokens)
}

// parseGeminiUsage 解析 Gemini 非流式响应 body 中的 usageMetadata。
func parseGeminiUsage(body []byte) *llm.Usage {
	if len(body) == 0 {
		return nil
	}
	var resp types.GeminiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil
	}
	if resp.UsageMetadata == nil {
		return nil
	}
	return usageFromGeminiMetadata(resp.UsageMetadata)
}

// usageFromGeminiMetadata 把 *types.GeminiUsageMetadata 映射为 *llm.Usage。
// 与 mergeUsageFromGeminiMetadata 同口径。
func usageFromGeminiMetadata(m *types.GeminiUsageMetadata) *llm.Usage {
	if m == nil {
		return nil
	}
	input := m.PromptTokenCount - m.CachedContentTokenCount
	if input < 0 {
		input = 0
	}
	if input == 0 && m.CandidatesTokenCount == 0 && m.CachedContentTokenCount == 0 {
		return nil
	}
	return &llm.Usage{
		Format: llm.FormatGeminiContents,
		Inner: types.Usage{
			InputTokens:          input,
			OutputTokens:         m.CandidatesTokenCount,
			CacheReadInputTokens: m.CachedContentTokenCount,
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
	_ pipeline.Outbound = (*outboundAdapter)(nil)
)
