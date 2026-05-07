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
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/adapters"
	"github.com/BenedictKing/ccx/internal/llm"
	"github.com/BenedictKing/ccx/internal/pipeline"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/utils"
)

// outboundAdapter 实现 pipeline.Outbound（Claude Messages 出站）。
//
// LastStreamUsage 在 TransformStream 消费完上游 SSE 帧后被填充，供 handler.go
// 的 deferred Finalize 读取——pipeline.Result.Response 在 stream 路径下为 nil，
// 无法走 result.Response.Usage 通道，因此用 outbound 自身字段做侧信道。
type outboundAdapter struct {
	mu              sync.Mutex
	LastStreamUsage *llm.Usage
}

// NewOutbound 构造出站 adapter。
func NewOutbound() *outboundAdapter { return &outboundAdapter{} }

// TakeStreamUsage 取出最近一次 stream 路径累计的 usage 并清空（幂等）。
// handler.go 在 stream 消费完成后、Finalize 落账前调用，把 usage 注入
// llm.Usage 传给 lb.Finalize。
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

// requestBodyBytesContextKey 与 providers/provider.go 中的同名常量保持一致；
// 因为 providers 包内是 unexported，adapter 必须自己写一遍这个键名。
//
// providers.ConvertToProviderRequest 通过 c.Get("requestBodyBytes") 读取请求体，
// 这是 ccx 既有约定（handler 在入口处 c.Set 该键）。adapter 在调用 provider
// 之前先 Set 一次，确保即使 inbound 没经过 handler 入口也能正确工作（例如
// pipeline.Process 直接拿到 llm.Request 重放）。
const requestBodyBytesContextKey = "requestBodyBytes"

// TransformRequest 调用 providers.ConvertToProviderRequest 构造发送请求。
func (*outboundAdapter) TransformRequest(_ context.Context, req *llm.Request) (*http.Request, []byte, error) {
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
//
// 如果 upstream.LowQuality=true，对解析后的 Claude 响应字节再做一次本地估算，
// 把 metrics 维度的 token 数（input/output）替换为估算值（不影响回客户端的字节）。
func (*outboundAdapter) TransformResponse(ctx context.Context, resp *http.Response) (*llm.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("messages outbound: nil response")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("messages outbound: read response body: %w", err)
	}

	upstream := upstreamFromCtx(ctx)
	requestBody := requestBodyFromCtx(ctx)
	lowQuality := upstream != nil && upstream.LowQuality

	if upstream == nil || upstream.ServiceType == "" || upstream.ServiceType == "claude" {
		out := adapters.CopyResponse(llm.FormatClaudeMessages, resp, body, parseClaudeUsage)
		applyLowQualityIfNeeded(out, body, requestBody, lowQuality)
		return out, nil
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
	applyLowQualityIfNeeded(out, convertedBody, requestBody, lowQuality)
	return out, nil
}

// applyLowQualityIfNeeded 在 LowQuality=true 时按本地估算覆盖 *llm.Response.Usage.Inner
// 的 input/output token 字段（cache 字段保留上游值）。lowQuality=false 时无副作用。
//
// 估算口径与 common.normalizeUsageForMetrics 中 LowQuality 分支一致：
//   - input：utils.EstimateRequestTokens(requestBody)，无 cache 时偏差 >5% 改写
//   - output：utils.EstimateResponseTokens(claudeResp.Content)，偏差 >5% 改写
func applyLowQualityIfNeeded(out *llm.Response, body, requestBody []byte, lowQuality bool) {
	if out == nil || !lowQuality {
		return
	}

	var parsed types.ClaudeResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return
	}
	estimatedInput := utils.EstimateRequestTokens(requestBody)
	estimatedOutput := utils.EstimateResponseTokens(parsed.Content)

	if out.Usage == nil {
		if estimatedInput <= 0 && estimatedOutput <= 0 {
			return
		}
		out.Usage = &llm.Usage{
			Format: llm.FormatClaudeMessages,
			Inner: types.Usage{
				InputTokens:  maxInt(estimatedInput, 0),
				OutputTokens: maxInt(estimatedOutput, 0),
			},
		}
		log.Printf("[Messages-Outbound] LowQuality 估算 (上游无 usage): input=%d output=%d", out.Usage.Inner.InputTokens, out.Usage.Inner.OutputTokens)
		return
	}

	hasCacheTokens := out.Usage.Inner.CacheCreationInputTokens > 0 ||
		out.Usage.Inner.CacheReadInputTokens > 0 ||
		out.Usage.Inner.CacheCreation5mInputTokens > 0 ||
		out.Usage.Inner.CacheCreation1hInputTokens > 0

	if estimatedInput > 0 {
		switch {
		case out.Usage.Inner.InputTokens > 0 && !hasCacheTokens:
			deviation := absInt(out.Usage.Inner.InputTokens-estimatedInput) * 100
			if deviation > 5*absInt(estimatedInput) {
				log.Printf("[Messages-Outbound] LowQuality input %d -> %d (estimated)", out.Usage.Inner.InputTokens, estimatedInput)
				out.Usage.Inner.InputTokens = estimatedInput
			}
		case out.Usage.Inner.InputTokens <= 1 && !hasCacheTokens:
			out.Usage.Inner.InputTokens = estimatedInput
		}
	}

	if estimatedOutput > 0 {
		switch {
		case out.Usage.Inner.OutputTokens > 0:
			deviation := absInt(out.Usage.Inner.OutputTokens-estimatedOutput) * 100
			if deviation > 5*absInt(estimatedOutput) {
				log.Printf("[Messages-Outbound] LowQuality output %d -> %d (estimated)", out.Usage.Inner.OutputTokens, estimatedOutput)
				out.Usage.Inner.OutputTokens = estimatedOutput
			}
		case out.Usage.Inner.OutputTokens <= 1:
			out.Usage.Inner.OutputTokens = estimatedOutput
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func absInt(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// TransformStream 把上游 *http.Response 流式 body 转换为 *llm.Response 流。
//
// same-format（claude）：按 SSE \n\n 切帧，原字节透传（PR1 行为）。
// cross-format（openai/gemini/responses）：调 provider.HandleStreamResponse 拿到
// "已转换为 Claude SSE 帧"的事件字符串 chan，逐帧包装为 *llm.Response。
// 该实现走选项 B —— 直接复用 provider 内部的 SSE 转换函数（即 HandleStreamResponse），
// 不需要把 c.Writer 替换成 io.Pipe / buffer，也不扩 provider 公共接口。
//
// 累计 usage：每帧解析 message_start.usage / message_delta.usage 中的
// input_tokens / output_tokens，stream 关闭后写入 a.LastStreamUsage 供 handler
// 在 deferred Finalize 中读取（pipeline.Result.Response 在 stream 路径下为 nil，
// 无法走 result.Response.Usage 通道）。
func (a *outboundAdapter) TransformStream(ctx context.Context, resp *http.Response) llm.Stream[*llm.Response] {
	upstream := upstreamFromCtx(ctx)
	if upstream == nil || upstream.ServiceType == "" || upstream.ServiceType == "claude" {
		return rawSSEPassthrough(ctx, resp, a)
	}
	provider := providers.GetProvider(upstream.ServiceType)
	if provider == nil {
		// 未知服务类型：回退到 raw passthrough，避免吞错；上层 middleware 会
		// 在错误状态码上触发 retry。
		return rawSSEPassthrough(ctx, resp, a)
	}
	return crossFormatSSEStream(ctx, resp, provider, a)
}

// rawSSEPassthrough 是 same-format 的 SSE \n\n 分帧透传实现（PR1 现状）。
//
// 透传同时累计 usage：每分出一帧就解析 SSE event 内的 usage，stream 正常结束
// （含 EOF 残留 buffer）时把累计结果写到 owner.LastStreamUsage。
func rawSSEPassthrough(ctx context.Context, resp *http.Response, owner *outboundAdapter) llm.Stream[*llm.Response] {
	ch := make(chan *llm.Response, 16)

	go func() {
		defer close(ch)
		defer func() { _ = resp.Body.Close() }()

		var collected types.Usage
		emit := func(frame []byte) bool {
			collectStreamFrameUsage(string(frame), &collected)
			select {
			case <-ctx.Done():
				return false
			case ch <- &llm.Response{Format: llm.FormatClaudeMessages, Body: frame}:
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
				finalizeStreamUsage(owner, collected)
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
//
// 与 rawSSEPassthrough 一致地累计 usage 并写到 owner.LastStreamUsage。
func crossFormatSSEStream(ctx context.Context, resp *http.Response, provider providers.Provider, owner *outboundAdapter) llm.Stream[*llm.Response] {
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
		var collected types.Usage
		for {
			select {
			case <-ctx.Done():
				if streamErr == nil {
					cs.SetErr(ctx.Err())
				}
				finalizeStreamUsage(owner, collected)
				return
			case ev, ok := <-eventChan:
				if !ok {
					eventChan = nil
				} else if ev != "" {
					collectStreamFrameUsage(ev, &collected)
					select {
					case <-ctx.Done():
						finalizeStreamUsage(owner, collected)
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
				finalizeStreamUsage(owner, collected)
				return
			}
		}
	}()
	return cs
}

// collectStreamFrameUsage 从单个 Claude SSE 帧（多行文本，含 event/data:... 行）
// 中提取 usage 字段并合并到 dst。支持两种位置：
//   - 顶层 usage（message_delta）
//   - data.message.usage（message_start）
//
// 合并策略与 common.updateCollectedUsage 一致：取最大值；cache 字段非零则覆盖。
func collectStreamFrameUsage(frame string, dst *types.Usage) {
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
		mergeUsageFromMap(dst, data["usage"])
		if msg, ok := data["message"].(map[string]interface{}); ok {
			mergeUsageFromMap(dst, msg["usage"])
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

// mergeUsageFromMap 把 map 里的 usage 字段合并进 dst，规则同 updateCollectedUsage：
//   - input/output：取较大值
//   - cache_*：非零即覆盖
func mergeUsageFromMap(dst *types.Usage, raw interface{}) {
	u, ok := raw.(map[string]interface{})
	if !ok {
		return
	}
	if v, ok := u["input_tokens"].(float64); ok && int(v) > dst.InputTokens {
		dst.InputTokens = int(v)
	} else if v, ok := u["prompt_tokens"].(float64); ok && int(v) > dst.InputTokens {
		dst.InputTokens = int(v)
	}
	if v, ok := u["output_tokens"].(float64); ok && int(v) > dst.OutputTokens {
		dst.OutputTokens = int(v)
	} else if v, ok := u["completion_tokens"].(float64); ok && int(v) > dst.OutputTokens {
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
}

// finalizeStreamUsage 把累计的 SSE usage 写到 outbound 的 LastStreamUsage 字段。
func finalizeStreamUsage(owner *outboundAdapter, collected types.Usage) {
	if owner == nil {
		return
	}
	if collected.InputTokens == 0 && collected.OutputTokens == 0 &&
		collected.CacheReadInputTokens == 0 && collected.CacheCreationInputTokens == 0 &&
		collected.CacheCreation5mInputTokens == 0 && collected.CacheCreation1hInputTokens == 0 {
		return
	}
	owner.setStreamUsage(&llm.Usage{Format: llm.FormatClaudeMessages, Inner: collected})
	log.Printf("[Messages-Outbound] stream usage finalized: input=%d output=%d cache_read=%d cache_creation=%d",
		collected.InputTokens, collected.OutputTokens, collected.CacheReadInputTokens, collected.CacheCreationInputTokens)
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

// requestBodyFromCtx 从 ctx 取 *llm.Request.RawBody，缺失时返回 nil。
// 用于 LowQuality 估算 input_tokens 时获取 messages 请求体。
func requestBodyFromCtx(ctx context.Context) []byte {
	req := adapters.RequestFromContext(ctx)
	if req == nil {
		return nil
	}
	return req.RawBody
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
	_ pipeline.Outbound = (*outboundAdapter)(nil)
)
