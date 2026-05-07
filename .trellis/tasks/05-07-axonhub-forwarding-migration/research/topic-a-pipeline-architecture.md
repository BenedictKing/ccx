# Research Topic A: AxonHub Pipeline 架构

## 文件
- `axonhub/llm/pipeline/pipeline.go`
- `axonhub/internal/server/orchestrator/state.go`
- `axonhub/internal/server/orchestrator/orchestrator.go`

## 核心抽象（必须迁移）

### 1. Inbound / Outbound Transformer（双向转换）

```go
type Inbound interface {
    TransformRequest(ctx, *httpclient.Request) (*llm.Request, error)
    TransformResponse(...) (*httpclient.Response, error)
    TransformStream(...) streams.Stream[*httpclient.StreamEvent]
}

type Outbound interface {
    TransformRequest(ctx, *llm.Request) (*httpclient.Request, error)
    TransformResponse(...) (*llm.Response, error)
    TransformStream(...) streams.Stream[*llm.Response]
}
```

中间格式 `llm.Request` 是统一抽象（OpenAI/Claude/Gemini 都先转成它再转出去）。

### 2. Pipeline 主流程（关键！）

```
Process(ctx, httpReq):
  1. inbound.TransformRequest(httpReq) → llm.Request
  2. applyBeforeRequestMiddlewares(llm.Request)
  3. for { processRequest(llm.Request) → 成功 return / 失败重试 }
     重试策略：
       a. ChannelRetryable.CanRetry(err) → PrepareForRetry → 同 channel 重试
       b. Retryable.HasMoreChannels() → NextChannel → 切下一个 channel
       c. 都不行 → break
  4. processRequest:
     - outbound.TransformRequest(llm.Request) → httpclient.Request
     - MergeInboundRequest（合并 header）
     - FinalizeAuthHeaders
     - applyRawRequestMiddlewares
     - executor.Execute(httpReq) → response 或 stream
```

### 3. Retry 双层机制

- `maxChannelRetries`：跨 channel 切换次数
- `maxSameChannelRetries`：同 channel 重试次数（如临时网络错误）
- `Retryable.NextChannel(ctx)`：切下一个 channel 时调用，由 outbound 内部维护 channel 游标
- `ChannelRetryable.PrepareForRetry`：同 channel 重试前重置状态

### 4. Middleware 7 类（按时机分）

- `BeforeRequest`：llm.Request 上的预处理
- `RawRequest`：httpclient.Request 上的拦截（认证/header 改写）
- `RawResponse`：httpclient.Response 同 channel 拦截
- `RawStream`：raw stream 拦截（用于 raw passthrough fan-out）
- `RawErrorResponse`：错误响应回写
- `LlmResponse`：转换后的 llm.Response 拦截
- `LlmStream`：转换后 llm 流拦截
- `InboundRawResponse` / `InboundRawStream`：inbound 侧的 raw 处理

### 5. PersistenceState 字段（state.go）

axonhub 重 DB，但纯 transit 字段是值得保留的：
- `OriginalModel` / `RawRequest` / `LlmRequest`
- `ChannelModelsCandidates` / `CurrentCandidateIndex` / `CurrentCandidate` / `CurrentModelIndex`
- `Perf`（性能记录）
- `StreamCompleted`（区分客户端断开 vs 流中错）
- `RawProviderRequest` / `RawProviderResponse` / `RawStreamCh` / `RawStreamErrRef` / `RawStreamCancel`（raw passthrough fan-out 状态机，**ccx 已迁移**，参见 AxonHub-half.md）

需要剥离的 axonhub 专属字段：
- `APIKey *ent.APIKey`、`RequestService` / `UsageLogService` / `ChannelService`
- `Request *ent.Request` / `RequestExec *ent.RequestExecution`
- `PromptProtecter`（仅企业版）

## 行为对齐迁移点

迁移 axonhub pipeline 到 ccx 需要：

1. **新增 `internal/pipeline/` 包**（不要叫 `internal/handlers/common`，独立出来）
   - `pipeline.go`：核心 Process 循环
   - `transformer.go`：Inbound/Outbound interface
   - `middleware.go`：7 类 middleware 接口
   - `state.go`：去掉 ent 字段的 PersistenceState

2. **新增 `internal/llm/` 中间格式**
   - `request.go` `response.go`：统一 llm 请求/响应

3. **改造现有 4 个 handler**
   - 把每个 handler 的 buildProviderRequest 改成实现 `Outbound`
   - request 解析改成实现 `Inbound`
   - 用 `pipeline.NewFactory(executor).Pipeline(inbound, outbound, opts...)` 替换 `TryUpstreamWithAllKeys`

4. **保留 ccx 的 key 级失败逻辑**
   - 通过实现自定义 Middleware（`RawResponse` 阶段）注入：
     - 状态码判定 → MarkKeyAsFailed/BlacklistKey/MatchPauseRule
   - 这层与 axonhub Retryable 接口正交：channel 级走 Retryable，key 级走 middleware

5. **`Retryable.NextChannel` 的实现**：把 ccx scheduler 的 channel 选择逻辑挂进去
   - `HasMoreChannels`: 调度器还有候选返回 true
   - `NextChannel`: 调度器取下一个 channel + 切 baseURL

## 关键决策

- ✅ 抽 pipeline + transformer + middleware 7 类纯接口
- ✅ 保留 retry 双层（cross-channel + same-channel）
- ✅ 保留 raw passthrough fan-out 字段（已迁移过）
- ❌ 不要 ent.Request / ent.RequestExecution（不持久化每次 attempt）
- ❌ 不要 PromptProtecter（企业级）

## 风险

- Middleware 7 类 + Retryable 接口看起来很多，但底层调用顺序固定，可以**把 ccx 现有 `TryUpstreamWithAllKeys` 函数式拆解成对应接口**而不是另起炉灶
- raw passthrough fan-out 字段 ccx 已实现（见 AxonHub-half.md 第 138 行 P1: raw stream fan-out / cleanup），迁移时**别覆盖现有实现**
