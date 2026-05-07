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
	)

	for {
		result, attemptErr := p.processAttempt(ctx, llmReq)
		if attemptErr == nil {
			return result, nil
		}

		// 通知所有 middleware 错误（不短路、不修改 err）
		p.applyRawErrorResponseMiddlewares(ctx, attemptErr)
		lastErr = attemptErr

		// ctx 取消则立即终止 retry
		if ctx.Err() != nil {
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
func (p *pipeline) processStream(ctx context.Context, resp *http.Response) (*Result, error) {
	llmStream := p.Outbound.TransformStream(ctx, resp)
	llmStream = p.applyLlmStreamMiddlewares(ctx, llmStream)

	rawStream := p.Inbound.TransformStream(ctx, llmStream)
	rawStream = p.applyRawStreamMiddlewares(ctx, rawStream)
	rawStream = p.applyInboundRawStreamMiddlewares(ctx, rawStream)

	return &Result{Stream: true, EventStream: rawStream}, nil
}
