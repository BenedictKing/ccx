# PR1: pipeline skeleton (transformer + middleware + state, no traffic cutover)

> 父任务: [05-07-axonhub-forwarding-migration](../05-07-axonhub-forwarding-migration/prd.md)

## Goal

引入 axonhub 风格的 pipeline 骨架到 ccx：Inbound/Outbound transformer + 7 类 middleware + 双层 retry。**本 PR 不切流量**：新 pipeline 完整实现 + 单测，但 4 个 handler 仍走旧 `TryUpstreamWithAllKeys` 路径。

## Why（为什么独立成 PR）

- 引入新代码不改变现有行为，risk = 0
- PR2/PR3 依赖此 PR 的接口定义
- 完整单测先行，PR3 切流量时直接验证通过

## Requirements

### 新增包

```
backend-go/internal/llm/
  request.go                    # 中间格式 llm.Request
  response.go                   # llm.Response
  usage.go                      # llm.Usage（沿用现有 types.Usage 字段，增加 Format 等元信息）
  stream.go                     # streams.Stream[T] 抽象（封装 chan + ctx.Done）

backend-go/internal/pipeline/
  transformer.go                # Inbound / Outbound interface
  middleware.go                 # 7 类 middleware interface
  pipeline.go                   # Process() 主循环 + 双层 retry
  executor.go                   # Executor interface（封装 SendRequest）
  state.go                      # AttemptState（剥离 ent 字段后的 PersistenceState）
  options.go                    # WithRetry / WithMiddlewares / WithEmptyResponseDetection
  retry.go                      # Retryable / ChannelRetryable interfaces
  errors.go                     # ErrEmptyResponse / ErrChannelExhausted 等
```

### 4 个 handler 的 Inbound/Outbound adapter（不接入 handler 流量）

```
backend-go/internal/handlers/chat/inbound_adapter.go         # 把 OpenAI Chat 请求 → llm.Request
backend-go/internal/handlers/chat/outbound_adapter.go        # llm.Request → 各上游 buildProviderRequest
backend-go/internal/handlers/messages/inbound_adapter.go     # 同理 Claude Messages
backend-go/internal/handlers/messages/outbound_adapter.go
backend-go/internal/handlers/responses/inbound_adapter.go
backend-go/internal/handlers/responses/outbound_adapter.go
backend-go/internal/handlers/gemini/inbound_adapter.go
backend-go/internal/handlers/gemini/outbound_adapter.go
```

每个 adapter 内部**调用现有 buildProviderRequest 函数**，不要重写转换逻辑（保留行为）。

### 接口契约

#### `transformer.Inbound`（参考 axonhub `llm/transformer/`）
```go
type Inbound interface {
    TransformRequest(ctx, *http.Request, []byte) (*llm.Request, error)
    TransformResponse(ctx, *llm.Response) ([]byte, http.Header, error)
    TransformStream(ctx, streams.Stream[*llm.Response]) streams.Stream[[]byte]
}
```

#### `transformer.Outbound`
```go
type Outbound interface {
    TransformRequest(ctx, *llm.Request) (*http.Request, []byte, error)
    TransformResponse(ctx, *http.Response) (*llm.Response, error)
    TransformStream(ctx, *http.Response) streams.Stream[*llm.Response]
}
```

#### `pipeline.Retryable` / `pipeline.ChannelRetryable`
照搬 axonhub `llm/pipeline/pipeline.go` 第 16-35 行定义。

#### `pipeline.Middleware`（7 类）
```go
type Middleware interface {
    BeforeRequest(ctx, *llm.Request) (*llm.Request, error)              // 可选
    RawRequest(ctx, *http.Request) (*http.Request, error)               // 可选
    RawResponse(ctx, *http.Response) (*http.Response, error)            // 可选
    RawStream(ctx, streams.Stream[*StreamEvent]) streams.Stream[*StreamEvent]
    RawErrorResponse(ctx, error)
    LlmResponse(ctx, *llm.Response) (*llm.Response, error)
    LlmStream(ctx, streams.Stream[*llm.Response]) streams.Stream[*llm.Response]
    InboundRawResponse(ctx, *http.Response) (*http.Response, error)
    InboundRawStream(ctx, streams.Stream[*StreamEvent]) streams.Stream[*StreamEvent]
}
```

通过 embedding `BaseMiddleware` 实现可选方法（默认透传）。

#### `pipeline.AttemptState`（剥离 ent 字段）
```go
type AttemptState struct {
    OriginalModel string
    RawRequest    *http.Request
    LlmRequest    *llm.Request

    // raw passthrough fan-out（保留 AxonHub-half.md 已迁移字段）
    StreamCompleted     bool
    RawProviderResponse *http.Response
    RawProviderRequest  *http.Request
    RawStreamCh         chan *StreamEvent
    RawStreamErrRef     *error
    RawStreamCancel     context.CancelFunc
}
```

### 单测要求

- 每个 adapter 双向转换的 round-trip 等价性（OpenAI Chat 请求 → llm.Request → OpenAI Chat 请求）
- pipeline retry 双层触发（mock outbound 报错，验证 NextChannel / PrepareForRetry 调用次数）
- middleware 7 类 hook 触发顺序
- raw passthrough fan-out 不回归（参考 AxonHub-half.md 第 138 行 P1）
- streams.Stream context 取消行为

## Acceptance Criteria

- [ ] `go vet ./internal/pipeline/... ./internal/llm/...` 通过
- [ ] `go test ./internal/pipeline/... ./internal/llm/... -count=1 -race` 通过
- [ ] 所有 adapter 单测覆盖率 ≥ 80%
- [ ] **现有 `internal/handlers/{chat,messages,responses,gemini}` 的 handler_test / matrix_test 一字不改且全部通过**（证明 handler 行为没变）
- [ ] `git diff backend-go/internal/handlers/` 应只包含 `*_adapter.go` 新增，不应有 `handler.go` / `handler_test.go` 修改

## Definition of Done

- 新代码全部带 docstring（包级 + 导出符号）
- 文件级 godoc 引用对应 axonhub 源文件位置
- spec：`.trellis/spec/backend/pipeline-architecture.md` 新增（描述新接口契约）

## Out of Scope

- ❌ 不切 handler 流量（handler 仍走 TryUpstreamWithAllKeys）
- ❌ 不引入 LB（PR2 做）
- ❌ 不实现 ccx key 级 middleware（PR3 做）
- ❌ 不动 scheduler
- ❌ 不引入 shopspring/decimal（PR3 做）

## Technical Notes

### 关键参考

- `axonhub/llm/pipeline/pipeline.go` 第 246-392 行（Process / processRequest 核心循环）
- `axonhub/llm/pipeline/middleware.go`（7 类 middleware 定义）
- `axonhub/internal/server/orchestrator/state.go`（PersistenceState 字段）
- `AxonHub-half.md` 第 138-142 行（raw stream fan-out / cleanup 已迁移契约）
- ccx 现有 `backend-go/internal/handlers/common/upstream_failover.go`（接口对接点）
- ccx 现有 `backend-go/internal/handlers/{chat,messages,responses,gemini}/handler.go`（adapter 内部调用）

### 不要回归的契约（AxonHub-half.md 已落地）

- raw passthrough fan-out 是 attempt 级状态，不跨 retry 复用
- retry/failover 前必须 cancel 当前 attempt、关闭 response body、等待 fan-out goroutine 退出
- fan-out 写 channel 时必须观察 context
- User-Agent passthrough 独立于 body/response passthrough
- custom headers 必须在 final selected auth 之前应用
- sensitive inbound header 剥离规则不变

### 风险

- adapter 调用现有 buildProviderRequest **可能存在循环依赖**，必要时把 buildProviderRequest 抽到 outbound_adapter.go 之内，handler.go 反过来调用 adapter
- middleware 7 类接口比较多，但实际很少有 middleware 会同时实现多类，可用 `BaseMiddleware` 默认空实现
