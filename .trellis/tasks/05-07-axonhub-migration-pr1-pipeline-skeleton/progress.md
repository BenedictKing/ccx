# PR1 Phase 1+2 进度

## 已完成

### 新增包

`backend-go/internal/llm/`
- `streams.go` —— `Stream[T]` iterator 接口 + `ChanStream[T]` 默认实现（基于 chan + ctx）
- `event.go` —— `StreamEvent` 类型（保留原始 SSE 字段）
- `usage.go` —— `Usage` 封装 `types.Usage` + Format 元信息 + IsZero 判断
- `request.go` —— `Request` 中间格式（Format/Model/Stream/RawBody/RawRequest/Metadata + Clone）
- `response.go` —— `Response` 中间响应
- `streams_test.go` / `usage_test.go` —— 单测覆盖 ctx 取消、Close 幂等、Metadata 隔离等

`backend-go/internal/pipeline/`
- `transformer.go` —— `Inbound` / `Outbound` 接口（含 Format/TransformRequest/TransformResponse/TransformStream）
- `executor.go` —— `Executor` 接口 + `ExecutorFunc` + `ChannelCustomizedExecutor`
- `state.go` —— `AttemptState`（剥离 ent 字段后的 PersistenceState）
- `errors.go` —— `ErrEmptyResponse` / `ErrChannelExhausted` / `ErrSameChannelExhausted` / `ErrInvalidResponseBody`
- `retry.go` —— `Retryable` / `ChannelRetryable` 接口
- `middleware.go` —— 7 类 hook 接口 + `BaseMiddleware` 默认透传
- `middleware_apply.go` —— middleware 链顺序应用辅助函数
- `options.go` —— `WithRetry` / `WithMiddlewares` / `WithEmptyResponseDetection`
- `pipeline.go` —— `Factory` + `pipeline.Process` 主循环 + 双层 retry
- `pipeline_test.go` —— 6 个核心测试覆盖：成功路径、跨 channel failover、同 channel retry 优先、channel 耗尽、ctx 取消、middleware 顺序、before-request 错误中止

### 测试结果

```
ok  github.com/BenedictKing/ccx/internal/llm        0.219s
ok  github.com/BenedictKing/ccx/internal/pipeline   0.803s
```

全量回归：
- `go vet ./...` 全过
- `go test ./...` 全部 22 个测试包通过
- 现有 handler_test / matrix_test / failover_test 一字未改全部通过（PR1 硬约束达成）

## 待办

### 下一阶段（PR1 后续）

1. **adapters**：4 个 handler 的 `inbound_adapter.go` + `outbound_adapter.go`（共 8 个文件）
   - 内部调用现有 buildProviderRequest / handleSuccess 等函数，**不重写转换逻辑**
   - 每对 adapter 配套 round-trip 单测（OpenAI Chat 请求 → llm.Request → OpenAI Chat 请求）

2. **raw passthrough fan-out 集成**
   - 把 AxonHub-half.md 第 138-142 行已落地的 RawStreamCh / RawStreamCancel / 等待 goroutine 退出 契约迁入 `pipeline.AttemptState`
   - 验证 attempt 级 cleanup 在 retry/failover 前生效

3. **空流检测**
   - 实现 `WithEmptyResponseDetection` 启用后的预读逻辑（参考 axonhub `pipeline.empty_response.go`）
   - 单测：流正常结束但无内容 → 返回 `ErrEmptyResponse`

4. **spec 同步**
   - 新增 `.trellis/spec/backend/pipeline-architecture.md` 记录新接口契约
   - 列出 7 类 middleware hook 触发顺序、双层 retry 决策树

### 不在本 PR 范围（PR2/PR3）

- LoadBalancer + 6 个策略（PR2）
- handler 切流量（PR3）
- ccx key 级 BlacklistKey/MarkKeyAsFailed/MatchPauseRule middleware（PR3）
- 价格计算（PR3）
- UsageStore NDJSON（PR3）
- 前端 dashboard（PR3）

## 关键设计决策记录

### 1. `pipeline.processStream` 不消费 stream

只完成"包装链"构造，把 `Result.EventStream` 交给 handler。理由：handler 需要在写每个 event 后调用 `flusher.Flush()`，pipeline 不应该接管这层 IO 细节。

### 2. ChanStream 用 ctx 而非 sentinel

axonhub `streams.Stream` 用 `Next/Current/Err/Close`，但 ctx 取消语义在 axonhub 内部由各实现自管。ccx 直接把 ctx 提到 `ChanStream` 内部，避免每个上游都重写一遍。

### 3. Middleware hook 顺序：先注册先执行

axonhub 默认是这种顺序，ccx 保持一致。注意：`RawStream` / `LlmStream` / `InboundRawStream` 是包装链，先注册的在最外层（最先看到事件流）。

### 4. AttemptState 分离 fan-out 字段

axonhub `PersistenceState` 把所有字段堆在一起，包括 ent 持久化。ccx 拆出 `AttemptState`，只放 attempt 级字段，避免歧义。
