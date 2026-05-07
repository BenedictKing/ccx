// Package chat —— PR1 outbound adapter for OpenAI Chat Completions.
//
// outbound 负责"构造发往上游的 *http.Request"与"把上游响应转换为 llm.Response"。
// 实现内部 100% 复用 chat.buildProviderRequest（handler.go 已有），不重写协议
// 转换逻辑（PR1 硬约束）。
//
// 响应路径行为（PR3 T8b 补全，与 messages outbound 同模式）：
//   - same-format（openai → openai chat）：byte copy via adapters.CopyResponse +
//     parse OpenAI Chat usage（prompt_tokens / completion_tokens / cached_tokens）；
//   - cross-format（claude → openai chat）：
//     * 非流式：复用 handler.go 的 convertClaudeResponseToChat 把 Claude JSON 转
//       OpenAI Chat JSON；
//     * 流式：调 providers.ClaudeProvider.HandleStreamResponse 拿 Claude SSE 帧，
//       再用 handler.go 的 SSE→Chat 转换函数生成 OpenAI Chat SSE 帧。
//   - 其他 service type（gemini/responses/默认）：buildProviderRequest 已按
//     OpenAI 兼容协议透传，故响应也按 same-format 处理。
package chat

import (
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
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/adapters"
	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/pipeline"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/types"
)

// outboundAdapter 实现 pipeline.Outbound（OpenAI Chat 出站）。
//
// LastStreamUsage 在 TransformStream 消费完上游 SSE 帧后被填充，供 handler.go
// 的 deferred Finalize 读取——pipeline.Result.Response 在 stream 路径下为 nil，
// 无法走 result.Response.Usage 通道，因此用 outbound 自身字段做侧信道（与
// messages outbound 完全一致）。
type outboundAdapter struct {
	mu              sync.Mutex
	LastStreamUsage *llm.Usage
}

// NewOutbound 构造一个 OpenAI Chat 出站 adapter。
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
// 注意：buildProviderRequest 内部读取 c.Request.URL.Path 等字段判断 raw passthrough
// 行为，因此 *gin.Context 必须真实可用；测试请用 gin.CreateTestContext 构造。
func (*outboundAdapter) TransformRequest(_ context.Context, req *llm.Request) (*http.Request, []byte, error) {
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
	// httpReq.Body。这里返回 RawBody 的拷贝作为"日志用 realBody"占位。
	realBody := append([]byte(nil), req.RawBody...)
	return httpReq, realBody, nil
}

// TransformResponse 把上游 *http.Response 整体读入并按 cross-format 还原为
// OpenAI Chat 协议字节。
//
// ctx 中由 wire.LBOutboundAdapter 注入 *llm.Request；据此读取 upstream 选择
// 转换路径：
//   - claude → 调 handler.go 的 convertClaudeResponseToChat 转换；
//   - 其他（openai/gemini/responses/默认）→ byte copy（buildProviderRequest 已按
//     OpenAI 兼容协议向上游发送，响应同样按 OpenAI Chat 处理）。
func (*outboundAdapter) TransformResponse(ctx context.Context, resp *http.Response) (*llm.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("chat outbound: nil response")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("chat outbound: read response body: %w", err)
	}

	upstream := upstreamFromCtx(ctx)
	if upstream == nil || upstream.ServiceType == "" || upstream.ServiceType != "claude" {
		// same-format / OpenAI 兼容透传：byte copy + 解析 OpenAI Chat usage。
		return adapters.CopyResponse(llm.FormatOpenAIChat, resp, body, parseOpenAIChatUsage), nil
	}

	// cross-format（claude → openai chat）：复用 handler.go 中的
	// convertClaudeResponseToChat。
	var claudeResp map[string]interface{}
	if err := json.Unmarshal(body, &claudeResp); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponseBody, err)
	}
	model := modelFromCtx(ctx)
	openaiResp := convertClaudeResponseToChat(claudeResp, model)
	convertedBody, err := json.Marshal(openaiResp)
	if err != nil {
		return nil, fmt.Errorf("chat outbound: marshal openai chat response: %w", err)
	}

	out := &llm.Response{
		Format:     llm.FormatOpenAIChat,
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       convertedBody,
	}
	// 从原始 Claude usage 提取 input/output → OpenAI Chat usage 并塞进 llm.Usage。
	if u, ok := claudeResp["usage"].(map[string]interface{}); ok {
		inputTokens, _ := u["input_tokens"].(float64)
		outputTokens, _ := u["output_tokens"].(float64)
		out.Usage = &llm.Usage{
			Format: llm.FormatOpenAIChat,
			Inner: types.Usage{
				InputTokens:  int(inputTokens),
				OutputTokens: int(outputTokens),
			},
		}
	}
	return out, nil
}

// ErrInvalidResponseBody 上游返回 200 但响应体不是合法 JSON。
// 借用 common 的同名 error 不需要在此暴露——直接 fmt.Errorf 包装即可让 handler 归类。
var ErrInvalidResponseBody = errors.New("chat outbound: invalid response body")

// TransformStream 把上游 SSE 流式 body 转换为 llm.Stream[*llm.Response]。
//
// same-format 透传（openai/gemini/responses/默认）：按 SSE \n\n 分帧原字节透传；
// 同时解析 OpenAI Chat 的 usage / delta.usage 字段累计到 LastStreamUsage。
//
// cross-format（claude → openai chat）：调 ClaudeProvider.HandleStreamResponse
// 拿 Claude SSE 事件 chan，把每帧转成 OpenAI Chat SSE chunk 字节。
func (a *outboundAdapter) TransformStream(ctx context.Context, resp *http.Response) llm.Stream[*llm.Response] {
	upstream := upstreamFromCtx(ctx)
	if upstream != nil && upstream.ServiceType == "claude" {
		return crossFormatClaudeToChatStream(ctx, resp, modelFromCtx(ctx), a)
	}
	return rawSSEPassthrough(ctx, resp, a)
}

// rawSSEPassthrough 是 same-format 的 SSE \n\n 分帧透传实现。
//
// 透传同时累计 usage：每分出一帧就解析 SSE event 内的 OpenAI Chat usage 字段。
func rawSSEPassthrough(ctx context.Context, resp *http.Response, owner *outboundAdapter) llm.Stream[*llm.Response] {
	ch := make(chan *llm.Response, 16)

	go func() {
		defer close(ch)
		defer func() { _ = resp.Body.Close() }()

		var collected types.Usage
		emit := func(frame []byte) bool {
			collectChatStreamFrameUsage(string(frame), &collected)
			select {
			case <-ctx.Done():
				return false
			case ch <- &llm.Response{Format: llm.FormatOpenAIChat, Body: frame}:
				return true
			}
		}

		buf := make([]byte, 0, 4096)
		readBuf := make([]byte, 4096)
		for {
			if ctx.Err() != nil {
				finalizeChatStreamUsage(owner, collected)
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
						finalizeChatStreamUsage(owner, collected)
						return
					}
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) && len(buf) > 0 {
					frame := append([]byte(nil), buf...)
					_ = emit(frame)
				}
				finalizeChatStreamUsage(owner, collected)
				return
			}
		}
	}()

	return llm.NewChanStream(ctx, ch, func() error { return nil })
}

// crossFormatClaudeToChatStream 把 Claude SSE 流转成 OpenAI Chat SSE 流。
//
// 调 ClaudeProvider.HandleStreamResponse 直接拿到 Claude 协议事件 chan（每个
// 事件已是完整 SSE 帧 + 末尾 "\n\n"），再用 buildChatChunkFromClaudeEvent 包装
// 为 OpenAI Chat chunk 并写回字节。usage 由 message_start / message_delta 字段
// 累计到 owner.LastStreamUsage（与 handler.go streamClaudeToChat 行为一致）。
func crossFormatClaudeToChatStream(ctx context.Context, resp *http.Response, model string, owner *outboundAdapter) llm.Stream[*llm.Response] {
	out := make(chan *llm.Response, 16)
	cs := llm.NewChanStream(ctx, out, func() error { return nil })

	provider := providers.GetProvider("claude")
	if provider == nil {
		cs.SetErr(fmt.Errorf("chat outbound: claude provider not registered"))
		close(out)
		return cs
	}

	eventChan, errChan, hsErr := provider.HandleStreamResponse(ctx, resp.Body)
	if hsErr != nil {
		cs.SetErr(fmt.Errorf("chat outbound: claude stream init: %w", hsErr))
		close(out)
		return cs
	}

	go func() {
		defer close(out)
		var streamErr error
		var collected types.Usage
		var doneSent bool
		emitFrame := func(frame []byte) bool {
			select {
			case <-ctx.Done():
				return false
			case out <- &llm.Response{Format: llm.FormatOpenAIChat, Body: frame}:
				return true
			}
		}
		for {
			select {
			case <-ctx.Done():
				if streamErr == nil {
					cs.SetErr(ctx.Err())
				}
				finalizeChatStreamUsage(owner, collected)
				return
			case ev, ok := <-eventChan:
				if !ok {
					eventChan = nil
				} else if ev != "" {
					chunks := convertClaudeSSEEventToChatChunks(ev, model, &collected)
					for _, frame := range chunks {
						if !emitFrame(frame) {
							finalizeChatStreamUsage(owner, collected)
							return
						}
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
				if !doneSent {
					_ = emitFrame([]byte("data: [DONE]\n\n"))
					doneSent = true
				}
				if streamErr != nil {
					cs.SetErr(streamErr)
				}
				finalizeChatStreamUsage(owner, collected)
				return
			}
		}
	}()
	return cs
}

// convertClaudeSSEEventToChatChunks 把单个 Claude SSE 事件转为 0..N 个 OpenAI
// Chat SSE chunk 字节（含末尾 "\n\n"）。同时把 usage 合并到 dst。
//
// 实现思路：扫描事件文本里的 data: 行，按 event type 调度——和 handler.go
// streamClaudeToChat 中的 switch 完全对齐。
func convertClaudeSSEEventToChatChunks(eventText string, model string, dst *types.Usage) [][]byte {
	var chunks [][]byte
	for _, line := range strings.Split(eventText, "\n") {
		jsonStr, ok := extractSSEData(line)
		if !ok {
			continue
		}
		var event map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &event); err != nil {
			continue
		}
		eventType, _ := event["type"].(string)
		switch eventType {
		case "content_block_delta":
			delta, ok := event["delta"].(map[string]interface{})
			if !ok {
				continue
			}
			deltaType, _ := delta["type"].(string)
			if deltaType != "text_delta" {
				continue
			}
			text, _ := delta["text"].(string)
			chatChunk := map[string]interface{}{
				"id":      "chatcmpl-claude",
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   model,
				"choices": []map[string]interface{}{
					{
						"index":         0,
						"delta":         map[string]interface{}{"content": text},
						"finish_reason": nil,
					},
				},
			}
			b, _ := json.Marshal(chatChunk)
			chunks = append(chunks, []byte(fmt.Sprintf("data: %s\n\n", string(b))))
		case "message_delta":
			stopChunk := map[string]interface{}{
				"id":      "chatcmpl-claude",
				"object":  "chat.completion.chunk",
				"created": time.Now().Unix(),
				"model":   model,
				"choices": []map[string]interface{}{
					{
						"index":         0,
						"delta":         map[string]interface{}{},
						"finish_reason": "stop",
					},
				},
			}
			if usage, ok := event["usage"].(map[string]interface{}); ok {
				inputTokens, _ := usage["input_tokens"].(float64)
				outputTokens, _ := usage["output_tokens"].(float64)
				if int(inputTokens) > dst.InputTokens {
					dst.InputTokens = int(inputTokens)
				}
				if int(outputTokens) > dst.OutputTokens {
					dst.OutputTokens = int(outputTokens)
				}
				stopChunk["usage"] = map[string]interface{}{
					"prompt_tokens":     int(inputTokens),
					"completion_tokens": int(outputTokens),
					"total_tokens":      int(inputTokens + outputTokens),
				}
			}
			b, _ := json.Marshal(stopChunk)
			chunks = append(chunks, []byte(fmt.Sprintf("data: %s\n\n", string(b))))
		case "message_start":
			if msg, ok := event["message"].(map[string]interface{}); ok {
				if usage, ok := msg["usage"].(map[string]interface{}); ok {
					inputTokens, _ := usage["input_tokens"].(float64)
					if int(inputTokens) > dst.InputTokens {
						dst.InputTokens = int(inputTokens)
					}
				}
			}
		}
	}
	return chunks
}

// collectChatStreamFrameUsage 从单个 OpenAI Chat SSE 帧中提取 usage 字段并合并
// 到 dst。支持顶层 usage（最终 chunk）以及 delta.usage（部分 vendor 在中途也写）。
//
// 合并策略：取最大值；cached_tokens 非零即覆盖。
func collectChatStreamFrameUsage(frame string, dst *types.Usage) {
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
		mergeChatUsageFromMap(dst, data["usage"])
		// delta.usage：少数实现把 usage 放进 choices[0].delta.usage。
		if choices, ok := data["choices"].([]interface{}); ok {
			for _, ch := range choices {
				cm, ok := ch.(map[string]interface{})
				if !ok {
					continue
				}
				if d, ok := cm["delta"].(map[string]interface{}); ok {
					mergeChatUsageFromMap(dst, d["usage"])
				}
			}
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

// mergeChatUsageFromMap 把 OpenAI Chat usage map 合并进 dst。
//
// 设置 PromptTokensTotal=prompt_tokens 让下游 metrics.extractUsageTokens 做
// "input - cached" 的标准化（与 common.OpenAIChatUsageFromCounts 保持一致）。
func mergeChatUsageFromMap(dst *types.Usage, raw interface{}) {
	u, ok := raw.(map[string]interface{})
	if !ok {
		return
	}
	if v, ok := u["prompt_tokens"].(float64); ok {
		if int(v) > dst.InputTokens {
			dst.InputTokens = int(v)
		}
		if int(v) > dst.PromptTokensTotal {
			dst.PromptTokensTotal = int(v)
		}
	}
	if v, ok := u["completion_tokens"].(float64); ok && int(v) > dst.OutputTokens {
		dst.OutputTokens = int(v)
	}
	// prompt_tokens_details.cached_tokens → CacheReadInputTokens
	if details, ok := u["prompt_tokens_details"].(map[string]interface{}); ok {
		if v, ok := details["cached_tokens"].(float64); ok && int(v) > 0 {
			dst.CacheReadInputTokens = int(v)
		}
	}
}

// finalizeChatStreamUsage 把累计的 SSE usage 写到 outbound 的 LastStreamUsage 字段。
func finalizeChatStreamUsage(owner *outboundAdapter, collected types.Usage) {
	if owner == nil {
		return
	}
	if collected.InputTokens == 0 && collected.OutputTokens == 0 && collected.CacheReadInputTokens == 0 {
		return
	}
	owner.setStreamUsage(&llm.Usage{Format: llm.FormatOpenAIChat, Inner: collected})
	log.Printf("[Chat-Outbound] stream usage finalized: input=%d output=%d cache_read=%d",
		collected.InputTokens, collected.OutputTokens, collected.CacheReadInputTokens)
}

// parseOpenAIChatUsage 解析 OpenAI Chat 非流式响应 body 中的 usage 字段。
// 解析失败返回 nil（不影响业务，仅 metrics 缺失）。
func parseOpenAIChatUsage(body []byte) *llm.Usage {
	if len(body) == 0 {
		return nil
	}
	var resp struct {
		Usage *struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&resp); err != nil {
		return nil
	}
	if resp.Usage == nil {
		return nil
	}
	return &llm.Usage{
		Format: llm.FormatOpenAIChat,
		Inner: types.Usage{
			InputTokens:          resp.Usage.PromptTokens,
			OutputTokens:         resp.Usage.CompletionTokens,
			CacheReadInputTokens: resp.Usage.PromptTokensDetails.CachedTokens,
			PromptTokensTotal:    resp.Usage.PromptTokens,
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

// modelFromCtx 从 ctx 取 *llm.Request.Model。空字符串时返回 ""，调用方各自兜底。
func modelFromCtx(ctx context.Context) string {
	req := adapters.RequestFromContext(ctx)
	if req == nil {
		return ""
	}
	return req.Model
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

// 编译期接口断言。
var (
	_ pipeline.Inbound  = inboundAdapter{}
	_ pipeline.Outbound = (*outboundAdapter)(nil)
)
