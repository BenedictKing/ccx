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

## 空流检测（`WithEmptyResponseDetection`）

启用后，`processStream` 在 `Outbound.TransformStream` 与 `LlmStream` middleware 之间插入 `prefetchLlmStream` 适配器：

- 构造时立刻 `inner.Next()` 预读首条 `*llm.Response`；
- 未拿到任何 Response ⇒ 关闭底层流，返回 `ErrEmptyResponse`（进 retry 分支）；
- 拿到了首条 ⇒ 后续 `Next()` 把缓存值重放给消费端，确保第一条不丢失。

判定"有内容"采用粗粒度规则（`hasMeaningfulResponse`）：`Body` 非空且非 `[DONE]`，或 `Usage` 非空。

## raw passthrough fan-out bridge（`internal/handlers/common`）

`BindRawStreamFanout(parent, *http.Response, *AttemptState) (cleanup, error)`：

- 启动现有 `startRawStreamFanout`（unexported，复用其 5s drain / EOF / `ErrInvalidResponseBody` 阈值等已锁定行为）；
- 用一个翻译 goroutine 把 `rawStreamEvent.Bytes` 拷贝为 `*llm.StreamEvent` 写入 `state.RawStreamCh`；
- 在 `state` 上挂载 `RawStreamCancel` / `RawStreamErrRef` / `RawProviderResponse`；
- 返回的 `cleanup` 顺序执行 cancel ctx → drain & wait done（5s 超时由 `cleanupRawStreamFanout` 控制）→ `state.Reset()`，幂等。

PR1 阶段为"准备好的死代码"：handler.go 不调用本桥接函数；PR2/PR3 切流量后由新 handler 路径调用，确保 attempt 级 fan-out 生命周期与 AxonHub-half.md 已锁定契约一致。

## 8 个 handler adapter（`internal/handlers/{chat,messages,responses,gemini}/{inbound,outbound}_adapter.go`）

每个 handler 提供一对 inbound/outbound adapter，PR1 仅作为"接口实现"存在，handler.go 不调用：

- inbound：把入站 *http.Request 解析为 `llm.Request`；把 `llm.Response` 还原为入站协议字节流；same-format 流透传时把每个 `*llm.Response.Body` 直接当 SSE event。
- outbound：通过 `llm.Request.Metadata` 上 4 个共享键（`adapter:ginContext` / `adapter:upstreamConfig` / `adapter:apiKey` / `adapter:baseURL`）取出运行时上下文，调用现有 `chat.buildProviderRequest` / `gemini.buildProviderRequest` / `providers.GetProvider(...).ConvertToProviderRequest` / `providers.ResponsesProvider.ConvertToProviderRequest`，**不重写任何协议转换逻辑**（PR1 硬约束）。
- 流式出站统一按 SSE `\n\n` / `\r\n\r\n` 分帧把 body 切成 `*llm.Response`；usage / 错误解析延迟到 PR2/PR3 的 LlmStream middleware 处理。

公共契约位于 `internal/handlers/adapters/adapters.go`：
- `MetaGinContext` / `MetaUpstreamConfig` / `MetaAPIKey` / `MetaBaseURL` 常量（4 个共享键）
- `SetGinContext` / `SetUpstreamBinding` / `GinContext` / `UpstreamBinding` helper
- `CopyResponse(format, *http.Response, body, usageParser)` 模板
- `ErrMissing*` 4 类编程错误

PR2 LoadBalancer 在 NextChannel 时调用 `adapters.SetUpstreamBinding(req, newUpstream, newKey, newBaseURL)`，pipeline 即可重放新 attempt。

## 不可回退的契约（AxonHub-half.md 已锁定）

- retry/failover 前必须 cancel 当前 attempt、关闭 response body、等待 fan-out goroutine 退出
- raw passthrough fan-out 是 attempt 级状态，不跨 retry 复用
- User-Agent passthrough 独立于 body/response passthrough
- custom headers 在 final selected auth 之前应用
- sensitive inbound header 统一剥离

## 相关文件

- `backend-go/internal/llm/*.go`
- `backend-go/internal/pipeline/*.go`（含 `empty_response.go`）
- `backend-go/internal/handlers/common/pipeline_attempt.go`（fan-out bridge）
- `backend-go/internal/handlers/adapters/adapters.go`（adapter 公共契约）
- `backend-go/internal/handlers/{chat,messages,responses,gemini}/{inbound,outbound}_adapter.go`
- `.trellis/tasks/05-07-axonhub-migration-pr1-pipeline-skeleton/progress.md`
