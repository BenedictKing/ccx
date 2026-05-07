# Pipeline Architecture (PR1 骨架)

参照 axonhub `llm/pipeline` + `orchestrator` 设计抽象。本 spec 锁定 `internal/pipeline/` 与 `internal/llm/` 的对外契约。PR1 阶段 handler 尚未切流量。

## 核心接口

- `llm.Stream[T]` — iterator 风格（Next/Current/Err/Close）；`ChanStream[T]` 基于 ctx+chan，Close 幂等。
- `pipeline.Inbound` — 入站协议解析与响应回写：`TransformRequest(ctx, *http.Request, body) -> *llm.Request`；`TransformResponse` / `TransformStream` 负责把 llm 中间格式还原为入站字节流。
- `pipeline.Outbound` — 出站构造与响应解析；可选实现 `Retryable` / `ChannelRetryable` / `ChannelCustomizedExecutor`。
- `pipeline.Executor` — `Execute(ctx, *http.Request) (*http.Response, error)`；不消费 body。
- `pipeline.Middleware` — 9 个 hook（BeforeRequest / RawRequest / RawResponse / RawStream / RawErrorResponse / LlmResponse / LlmStream / InboundRawResponse / InboundRawStream）；通过 embedding `BaseMiddleware` 跳过不关心的 hook。
- `pipeline.AttemptState` — attempt 级 raw passthrough fan-out 字段（`RawStreamCh` / `RawStreamErrRef` / `RawStreamCancel`）。

## Process 主循环

1. `Inbound.TransformRequest` → `llm.Request`
2. 所有 middleware `BeforeRequest`（任一报错立即中止，不进 retry）
3. 循环：`processAttempt` → 成功返回；失败先试 `ChannelRetryable`（同 channel），再试 `Retryable.NextChannel`；都不行退出
4. ctx 取消立即退出；`retryDelay` 期间可被 ctx 中断

## 不可回退的契约（AxonHub-half.md 已锁定）

- retry/failover 前必须 cancel 当前 attempt、关闭 response body、等待 fan-out goroutine 退出
- raw passthrough fan-out 是 attempt 级状态，不跨 retry 复用
- User-Agent passthrough 独立于 body/response passthrough
- custom headers 在 final selected auth 之前应用
- sensitive inbound header 统一剥离

## 相关文件

- `backend-go/internal/llm/*.go`
- `backend-go/internal/pipeline/*.go`
- `.trellis/tasks/05-07-axonhub-migration-pr1-pipeline-skeleton/progress.md`
