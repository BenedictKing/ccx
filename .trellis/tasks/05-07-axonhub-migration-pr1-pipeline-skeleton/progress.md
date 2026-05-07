# PR1 Phase 1+2+3+4 进度（全部完成）

## 已完成

### Phase 1+2：新增包（pipeline + llm 骨架）

`backend-go/internal/llm/`
- `streams.go` —— `Stream[T]` iterator 接口 + `ChanStream[T]` 默认实现（基于 chan + ctx）
- `event.go` —— `StreamEvent` 类型（保留原始 SSE 字段）
- `usage.go` —— `Usage` 封装 `types.Usage` + Format 元信息 + IsZero 判断
- `request.go` —— `Request` 中间格式（Format/Model/Stream/RawBody/RawRequest/Metadata + Clone）
- `response.go` —— `Response` 中间响应
- `streams_test.go` / `usage_test.go` —— 单测覆盖 ctx 取消、Close 幂等、Metadata 隔离等

`backend-go/internal/pipeline/`
- `transformer.go` —— `Inbound` / `Outbound` 接口
- `executor.go` —— `Executor` 接口 + `ExecutorFunc` + `ChannelCustomizedExecutor`
- `state.go` —— `AttemptState`
- `errors.go` —— `ErrEmptyResponse` / `ErrChannelExhausted` / `ErrSameChannelExhausted` / `ErrInvalidResponseBody`
- `retry.go` —— `Retryable` / `ChannelRetryable`
- `middleware.go` —— 7 类 hook + `BaseMiddleware` 默认透传
- `middleware_apply.go` —— middleware 链顺序应用辅助函数
- `options.go` —— `WithRetry` / `WithMiddlewares` / `WithEmptyResponseDetection`
- `pipeline.go` —— `Factory` + `Process` 主循环 + 双层 retry
- `pipeline_test.go` —— 6 个核心测试

### Phase 3：空流检测 + raw passthrough fan-out bridge

`backend-go/internal/pipeline/empty_response.go` + `empty_response_test.go`
- `prefetchLlmStream` —— `*llm.Response` 预读 + 重放适配器
- `hasMeaningfulResponse` —— Body/Usage 任一非空即认为有内容
- 5 测试覆盖：检测/底层错误优先/有内容透传/默认禁用/retry 触发

`backend-go/internal/handlers/common/pipeline_attempt.go` + `pipeline_attempt_test.go`
- `BindRawStreamFanout(parent, *http.Response, *AttemptState) (cleanup, error)` —— attempt 级 fan-out 生命周期管理
- `CollectFanoutErr(*AttemptState)` —— 收集 fan-out 终止错误
- 6 测试覆盖：完整流/cleanup-Reset/重 bind/拒绝重复 bind/nil 参数/父 ctx 取消

### Phase 4：8 个 handler adapter（chat/messages/responses/gemini × inbound/outbound）

`backend-go/internal/handlers/adapters/adapters.go`（公共契约）
- `MetaGinContext` / `MetaUpstreamConfig` / `MetaAPIKey` / `MetaBaseURL` —— `llm.Request.Metadata` 共享键
- `SetGinContext` / `SetUpstreamBinding` / `GinContext` / `UpstreamBinding` —— get/set helper
- `CopyResponse(format, *http.Response, body, usageParser)` —— 把上游 *http.Response → llm.Response 的样板代码
- 4 类 `ErrMissing*` 错误：编程错误（缺 metadata）的明确信号

`backend-go/internal/handlers/chat/`
- `inbound_adapter.go` —— OpenAI Chat 入站；解析 model/stream，RawBody 拷贝保留
- `outbound_adapter.go` —— 调用 chat.buildProviderRequest（"openai" 分支）；自带 SSE \n\n 帧切分
- `adapter_test.go` —— 8 个测试：解析/拒绝错误输入/响应直通/流透传/build/拒绝缺 metadata/round-trip/body 关闭

`backend-go/internal/handlers/gemini/`
- `inbound_adapter.go` —— Gemini Contents 入站；从 URL 路径解析 `:generateContent` vs `:streamGenerateContent`；body 反序列化为 *types.GeminiRequest 并写到 Metadata，避免 outbound 重复解析
- `outbound_adapter.go` —— 调用 gemini.buildProviderRequest（"gemini" 分支）
- `adapter_test.go` —— 8 个测试：URL 解析/stream 标记/拒绝无效路径/build URL 与 auth/拒绝缺 metadata/类型不匹配拒绝/响应 Content-Type fallback/流透传

`backend-go/internal/handlers/messages/`
- `inbound_adapter.go` —— Claude Messages 入站；body 反序列化为 *types.ClaudeRequest
- `outbound_adapter.go` —— 调用 `providers.GetProvider(upstream.ServiceType).ConvertToProviderRequest(c, upstream, apiKey)`；通过 `c.Set("requestBodyBytes", req.RawBody)` 兼容 providers 包对 gin context 的契约
- `adapter_test.go` —— 7 个测试：解析/拒绝错误输入/响应直通/流透传/build/拒绝缺 metadata/拒绝未知 ServiceType

`backend-go/internal/handlers/responses/`
- `inbound_adapter.go` —— OpenAI Responses 入站；body 反序列化为 *types.ResponsesRequest
- `outbound_adapter.go` —— 持有可选 `*session.SessionManager`；调用 `(&providers.ResponsesProvider{SessionManager}).ConvertToProviderRequest`；nil 时 provider 自动初始化默认 SessionManager
- `adapter_test.go` —— 6 个测试

### 测试结果（最终全量回归）

```
go vet ./...                       # 全过
go test ./... -count=1             # 22 个包全过
```

PR1 硬约束**全部达成**：
- `git diff backend-go/internal/handlers/{chat,messages,responses,gemini}/handler.go` 为空
- 所有现有 `*_test.go` 一字未改
- 唯一改动的现有文件：`backend-go/internal/pipeline/pipeline.go`（仅在 processStream 增加 prefetch 分支）

## 关键设计决策记录

### 1. `pipeline.processStream` 不消费 stream

只完成"包装链"构造，把 `Result.EventStream` 交给 handler。理由：handler 需要在写每个 event 后调用 `flusher.Flush()`，pipeline 不应该接管这层 IO 细节。

### 2. ChanStream 用 ctx 而非 sentinel

axonhub `streams.Stream` 用 `Next/Current/Err/Close`，但 ctx 取消语义在 axonhub 内部由各实现自管。ccx 直接把 ctx 提到 `ChanStream` 内部，避免每个上游都重写一遍。

### 3. Middleware hook 顺序：先注册先执行

axonhub 默认是这种顺序，ccx 保持一致。注意：`RawStream` / `LlmStream` / `InboundRawStream` 是包装链，先注册的在最外层（最先看到事件流）。

### 4. AttemptState 分离 fan-out 字段

axonhub `PersistenceState` 把所有字段堆在一起，包括 ent 持久化。ccx 拆出 `AttemptState`，只放 attempt 级字段，避免歧义。

### 5. 空流检测放在 `*llm.Response` 层而非 `*llm.StreamEvent` 层

PRD options.go docstring 锁定"在把 llm.Stream 交给 Inbound 之前预读"，这要求检测点在 outbound stream → inbound stream 之间，即 `llm.Stream[*llm.Response]` 层。检测器实现为预读 + 重放，确保第一条非空 Response 不会丢失。

### 6. fan-out bridge 为何放 `internal/handlers/common`

`startRawStreamFanout` / `cleanupRawStreamFanout` 是 unexported（同包 common），而 bridge 必须复用它们以保留 AxonHub-half.md 已锁定的 5s 超时、drain 行为等契约。把 bridge 放在同包是最小入侵方案；PR2/PR3 切流量时直接 import `common.BindRawStreamFanout` 即可。

### 7. 翻译 goroutine 不并发观察两条 chan

`startRawStreamFanout` 内部 `defer close(errChan); defer close(eventChan)` 是 LIFO，errChan 实际比 eventChan 先关闭。如果在 select 主循环里同时 `case <-rawErrCh:`，会因为 errChan 早关而立刻 return，丢失 rawCh 缓冲区里尚未消费的 event。修复：select 只观察 `ctx.Done()` 与 `rawCh`；rawCh 关闭后再非阻塞 receive 错误。

### 8. adapter 通过 `llm.Request.Metadata` 携带 *gin.Context

为遵守 PR1"不重写转换逻辑"硬约束，outbound adapter 必须复用现有 buildProviderRequest / `providers.ConvertToProviderRequest`，这些函数都需要 `*gin.Context`。我们在 `internal/handlers/adapters` 包定义 4 个共享 metadata 键，inbound 写入 ginContext，outbound 由 LB（PR2 落）写入 upstream/apiKey/baseURL。LB 切 channel 时只需更新 metadata 三件套，不必动 llm.Request 主结构 —— 与 axonhub `PersistenceState` 字段独立分离的思路一致。

### 9. messages/responses outbound 通过 `c.Set("requestBodyBytes", ...)` 兼容 providers 包

providers.ResponsesProvider / ClaudeProvider 等内部用 `getRequestBodyBytes(c)` 读取请求体（unexported helper，依赖 c.Get(requestBodyBytesContextKey)）。adapter 在调用 provider 之前先 `c.Set` 一次，确保即使绕过 handler 入口（如 pipeline 重放）也能正确工作。

### 10. gemini inbound 反序列化 *types.GeminiRequest 并写到 Metadata

gemini.buildProviderRequest 接受已解析 `*types.GeminiRequest` 而非字节；为避免 outbound 二次反序列化（重复成本），inbound 在 `MetaParsedRequest` 写入解析结果。messages/responses 不需要这样做，因为它们的 provider 都从 RawBody 重新解析（保留协议 patch 时机）。

## 下一步（不在 PR1 范围内）

PR1 全部完成。下一步进入 PR2：

- LoadBalancer + 6 个调度策略（priority/random/weighted-random/round-robin/weighted-round-robin/affinity）
- LoadBalancer 实现 `pipeline.Retryable` / `ChannelRetryable`，在 NextChannel 时调用 `adapters.SetUpstreamBinding`
- handler 仍走旧路径，PR2 末尾通过特性开关初步接通"新 pipeline + LB"

PR3 才是真正切流量：
- handler 切到 `pipeline.Factory.Pipeline(inbound, outbound).Process(...)`
- ccx key 级 BlacklistKey/MarkKeyAsFailed/MatchPauseRule middleware
- 价格计算（shopspring/decimal）
- UsageStore NDJSON
- 前端 dashboard


