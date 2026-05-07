package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/BenedictKing/ccx/internal/llm"
)

// Result 是 Pipeline.Process 的成功返回值。
//
// Stream 字段标识本次响应是否为流式：
//   - Stream=false：Response 字段非空，调用方据此回写客户端。
//   - Stream=true ：EventStream 非空，调用方负责逐 event 转发并 Close。
type Result struct {
	Stream      bool
	Response    *llm.Response
	EventStream llm.Stream[*llm.StreamEvent]
}

// Factory 用于构造 pipeline 实例；持有共享 Executor。
type Factory struct {
	Executor Executor
}

// NewFactory 构造 Factory，executor 不可为 nil。
func NewFactory(executor Executor) *Factory {
	if executor == nil {
		panic("pipeline.NewFactory: executor must not be nil")
	}
	return &Factory{Executor: executor}
}

// Pipeline 构造一个 pipeline 实例；inbound 与 outbound 不可为 nil。
//
// opts 可使用 WithRetry / WithMiddlewares / WithEmptyResponseDetection 等。
func (f *Factory) Pipeline(inbound Inbound, outbound Outbound, opts ...Option) *pipeline {
	if inbound == nil {
		panic("pipeline.Factory.Pipeline: inbound must not be nil")
	}
	if outbound == nil {
		panic("pipeline.Factory.Pipeline: outbound must not be nil")
	}
	p := &pipeline{
		Executor: f.Executor,
		Inbound:  inbound,
		Outbound: outbound,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// pipeline 是 Process 主循环的承载者；不公开，调用方仅看到 *pipeline 句柄。
type pipeline struct {
	Executor               Executor
	Inbound                Inbound
	Outbound               Outbound
	middlewares            []Middleware
	maxChannelRetries      int
	maxSameChannelRetries  int
	retryDelay             time.Duration
	emptyResponseDetection bool
}

// cleanupAttemptStreamResources 在 retry / failover / ctx 取消之前释放
// 当前 attempt 持有的 stream 资源，对应 AxonHub-half.md 第 138-142 行
// 锁定的契约：cancel 当前 attempt → close response body → wait fan-out
// goroutine 退出 → 重置 AttemptState 字段。
//
// LIFO 顺序：
//  1. RawStreamCancel：取消 attempt-scoped ctx，触发 fan-out goroutine 退出。
//  2. RawProviderResponse.Body：关闭 body，让阻塞在 Read 上的 goroutine 返回。
//  3. RawStreamCh：drain 并等待 close（最长 5s 超时，避免无限阻塞）。
//  4. AttemptState.Reset：清空 attempt 级字段，准备下一次 attempt。
//
// 该函数幂等：state 为 nil 或字段已清空时直接返回。
func cleanupAttemptStreamResources(state *AttemptState) {
	if state == nil {
		return
	}
	if state.RawStreamCancel == nil &&
		state.RawProviderResponse == nil &&
		state.RawStreamCh == nil {
		return
	}

	slog.Debug("[Pipeline-Cleanup] releasing attempt-scoped stream resources")

	if state.RawStreamCancel != nil {
		state.RawStreamCancel()
	}
	if state.RawProviderResponse != nil && state.RawProviderResponse.Body != nil {
		_ = state.RawProviderResponse.Body.Close()
	}
	if state.RawStreamCh != nil {
		drainCh := make(chan struct{})
		ch := state.RawStreamCh
		go func() {
			for range ch {
				// drain 直到上游 fan-out goroutine 关闭 channel
			}
			close(drainCh)
		}()
		select {
		case <-drainCh:
		case <-time.After(5 * time.Second):
			// 强行返回；优先 goroutine 泄漏而非 deadlock。
			slog.Warn("[Pipeline-Cleanup] drain raw stream channel timed out after 5s")
		}
	}
	state.Reset()
}

// Process 是 pipeline 主循环。
//
// 执行步骤（与 axonhub/llm/pipeline.pipeline.Process 行为对齐）：
//
//  1. Inbound.TransformRequest(http.Request, body) → llm.Request
//  2. 应用所有 Middleware.BeforeRequest
//  3. for { processAttempt(); 成功 return / 失败按 retry 策略决定 continue or break }
//
// retry 策略：
//
//   - 优先尝试 ChannelRetryable.CanRetry → PrepareForRetry（同 channel 重试）；
//   - 同 channel 重试耗尽后，尝试 Retryable.HasMoreChannels → NextChannel；
//   - 两者都无法继续时退出，返回最近一次错误；
//   - ctx 取消立即返回，不再重试。
func (p *pipeline) Process(ctx context.Context, req *http.Request, body []byte) (*Result, error) {
	llmReq, err := p.Inbound.TransformRequest(ctx, req, body)
	if err != nil {
		return nil, fmt.Errorf("pipeline: inbound transform request: %w", err)
	}

	llmReq, err = p.applyBeforeRequestMiddlewares(ctx, llmReq)
	if err != nil {
		return nil, fmt.Errorf("pipeline: before request middlewares: %w", err)
	}
	llmReq.RawRequest = req

	var (
		lastErr            error
		channelSwitches    int
		sameChannelRetries int
		// attemptState 持有当前 attempt 的 stream 资源；retry 之前必须清理。
		// 由 processAttempt 在每次进入时通过 ctx 注入；处理完成后由本循环
		// 在 retry / ctx 取消 / break 路径上调用 cleanupAttemptStreamResources。
		attemptState *AttemptState
	)

	// 兜底：函数返回前确保任何残留 stream 资源被释放（仅在错误路径生效；
	// 成功路径返回的 Result.EventStream 由调用方持有）。
	defer func() {
		if lastErr != nil {
			cleanupAttemptStreamResources(attemptState)
		}
	}()

	for {
		attemptState = &AttemptState{}
		ctxAttempt := withAttemptState(ctx, attemptState)
		result, attemptErr := p.processAttempt(ctxAttempt, llmReq)
		if attemptErr == nil {
			return result, nil
		}

		// 通知所有 middleware 错误（不短路、不修改 err）
		p.applyRawErrorResponseMiddlewares(ctx, attemptErr)
		lastErr = attemptErr

		// ctx 取消则立即终止 retry；先释放 attempt 资源避免 goroutine 泄漏。
		if ctx.Err() != nil {
			cleanupAttemptStreamResources(attemptState)
			return nil, lastErr
		}

		canRetry := false

		// 1. 同 channel 重试优先
		if cr, ok := p.Outbound.(ChannelRetryable); ok {
			if sameChannelRetries < p.maxSameChannelRetries && cr.CanRetry(lastErr) {
				if perr := cr.PrepareForRetry(ctx); perr == nil {
					sameChannelRetries++
					canRetry = true
					slog.DebugContext(ctx, "pipeline: retrying same channel",
						slog.Int("attempt", sameChannelRetries),
						slog.Int("max", p.maxSameChannelRetries),
					)
				} else {
					slog.WarnContext(ctx, "pipeline: prepare same-channel retry failed", slog.Any("error", perr))
				}
			}
		}

		// 2. 同 channel 不可重试 → 切下一个 channel
		if !canRetry {
			if r, ok := p.Outbound.(Retryable); ok {
				if channelSwitches < p.maxChannelRetries && r.HasMoreChannels() {
					if nerr := r.NextChannel(ctx); nerr == nil {
						channelSwitches++
						sameChannelRetries = 0
						canRetry = true
						slog.DebugContext(ctx, "pipeline: switched to next channel",
							slog.Int("attempt", channelSwitches),
							slog.Int("max", p.maxChannelRetries),
						)
					} else {
						slog.WarnContext(ctx, "pipeline: next channel failed", slog.Any("error", nerr))
					}
				}
			}
		}

		if !canRetry {
			break
		}

		// 进入下一次 attempt 之前，必须释放当前 attempt 持有的 stream 资源
		// （cancel ctx → close body → wait fan-out 退出）。
		cleanupAttemptStreamResources(attemptState)

		if p.retryDelay > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(p.retryDelay):
			}
		}
	}

	return nil, lastErr
}

// processAttempt 执行单次 attempt：构造请求 → 发送 → 解析响应。
//
// 拆分为独立函数以便 Process 主循环聚焦于 retry 控制流。
func (p *pipeline) processAttempt(ctx context.Context, req *llm.Request) (*Result, error) {
	httpReq, _, err := p.Outbound.TransformRequest(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("pipeline: outbound transform request: %w", err)
	}

	httpReq, err = p.applyRawRequestMiddlewares(ctx, httpReq)
	if err != nil {
		return nil, fmt.Errorf("pipeline: raw request middlewares: %w", err)
	}

	exec := p.Executor
	if cust, ok := p.Outbound.(ChannelCustomizedExecutor); ok {
		exec = cust.CustomizeExecutor(exec)
	}

	resp, err := exec.Execute(ctx, httpReq)
	if err != nil {
		return nil, err
	}

	resp, err = p.applyRawResponseMiddlewares(ctx, resp)
	if err != nil {
		return nil, err
	}

	if req.IsStream() {
		return p.processStream(ctx, resp)
	}
	return p.processNonStream(ctx, resp)
}

// processNonStream 处理非流式响应：Outbound.TransformResponse → 应用 LlmResponse middleware。
func (p *pipeline) processNonStream(ctx context.Context, resp *http.Response) (*Result, error) {
	llmResp, err := p.Outbound.TransformResponse(ctx, resp)
	if err != nil {
		return nil, fmt.Errorf("pipeline: outbound transform response: %w", err)
	}
	llmResp, err = p.applyLlmResponseMiddlewares(ctx, llmResp)
	if err != nil {
		return nil, fmt.Errorf("pipeline: llm response middlewares: %w", err)
	}
	return &Result{Stream: false, Response: llmResp}, nil
}

// processStream 处理流式响应：Outbound.TransformStream → 应用 LlmStream middleware。
//
// 注意：本函数不消费 stream，仅完成"包装链"构造；调用方（handler）负责从
// EventStream 拉取事件并写回客户端。
//
// 当 emptyResponseDetection=true 时，会在 LlmStream middleware 之前对底层
// llm.Response 流做一次预读：若底层流尚未发送任何 *llm.Response 即正常
// 结束，则返回 ErrEmptyResponse 触发外层 retry / failover；否则把预读到的
// 首个 Response 重放给后续 stream 链路。
func (p *pipeline) processStream(ctx context.Context, resp *http.Response) (*Result, error) {
	llmStream := p.Outbound.TransformStream(ctx, resp)

	if p.emptyResponseDetection {
		ps := newPrefetchLlmStream(ctx, llmStream)
		if !ps.HasPrefetched() {
			// 流未产生任何 Response 即结束，关闭底层流释放资源。
			_ = ps.Close()
			if err := ps.PrefetchErr(); err != nil {
				return nil, err
			}
			return nil, ErrEmptyResponse
		}
		llmStream = ps
	}

	llmStream = p.applyLlmStreamMiddlewares(ctx, llmStream)

	rawStream := p.Inbound.TransformStream(ctx, llmStream)
	rawStream = p.applyRawStreamMiddlewares(ctx, rawStream)
	rawStream = p.applyInboundRawStreamMiddlewares(ctx, rawStream)

	return &Result{Stream: true, EventStream: rawStream}, nil
}
