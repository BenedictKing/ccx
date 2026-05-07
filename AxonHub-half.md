# AxonHub 迁移后续收口记录

## 本次是要干啥

本轮是在已完成 AxonHub passthrough 迁移的基础上，继续补齐 raw passthrough 的 usage / metrics 计费旁路准确性。

上一轮 AxonHub 收口已经完成这些主目标：

- 不恢复旧 passthrough 配置字段。
- same-format 链路自动 raw passthrough。
- cross-format 链路继续走现有转换路径。
- raw body / raw JSON / raw SSE 尽量保真。
- raw stream 的 client branch 和内部 usage / metrics parsing branch 分离。
- stream failover / retry / client cancel 前释放当前 attempt 的 reader/body/fan-out。
- header custom override 顺序固定为 custom headers before final selected auth。
- User-Agent passthrough 独立于 body/response passthrough。
- 敏感 inbound header 统一剥离。

本次新增关注点是：现在的“计费”是否准确。

结论是：改动前主链路没有重复计费，成功请求只会通过 `RecordRequestFinalizeSuccess(..., usage)` 统一落到 metrics；但部分 raw passthrough 旁路 usage 不够完整，存在漏记缓存 token 或大流末尾 usage 的风险。

本次要做的具体收口：

- OpenAI Chat same-format raw stream 和非流式 passthrough：
  - 保持客户端收到的 OpenAI response / SSE bytes 原样。
  - 从 `usage.prompt_tokens` / `usage.completion_tokens` 解析 metrics。
  - 新增解析 `usage.prompt_tokens_details.cached_tokens`。
  - 将 cached tokens 写入 `types.Usage.CacheReadInputTokens`。
  - 将 `types.Usage.PromptTokensTotal` 设置为上游 `prompt_tokens`，让 metrics 层按 `prompt_tokens - cached_tokens` 归一化 input tokens。
- Gemini native same-format raw stream 和非流式 passthrough：
  - 保持客户端收到的 Gemini response / SSE bytes 原样。
  - 从 `usageMetadata.promptTokenCount` / `usageMetadata.candidatesTokenCount` 解析 metrics。
  - 新增解析 `usageMetadata.cachedContentTokenCount`。
  - `InputTokens = max(promptTokenCount - cachedContentTokenCount, 0)`。
  - `CacheReadInputTokens = cachedContentTokenCount`。
  - `PromptTokensTotal = promptTokenCount`。
- Responses same-format raw stream：
  - 保持客户端收到的 raw SSE bytes 原样。
  - 不再只缓存前 1 MiB 后整体解析 usage。
  - 改为边转发边按 SSE event 增量解析 `response.completed.response.usage`。
  - usage 出现在大流末尾时仍能进入 metrics。
- 后端 spec 补充 raw passthrough 计费旁路契约。
- 增加 regression tests 锁定上述行为。

本轮仍明确禁止恢复这些旧字段：

- `streamPassthroughEnabled`
- `sub2apiPassthroughEnabled`
- `strictRequestPassthroughEnabled`
- `normalizeMetadataUserId`

## 参照物是啥

原始迁移仍以 AxonHub 的 passthrough / orchestrator / pipeline 思路为参照，但不照搬完整架构：

- `axonhub/internal/server/orchestrator/pass_through.go`
- `axonhub/internal/server/orchestrator/orchestrator.go`
- `axonhub/internal/server/orchestrator/state.go`
- `axonhub/internal/server/orchestrator/outbound.go`
- `axonhub/internal/server/orchestrator/request_execution.go`
- `axonhub/llm/pipeline/pipeline.go`
- `axonhub/llm/pipeline/stream.go`

本次计费准确性补丁主要参照 ccx 现有落账链路：

- `backend-go/internal/handlers/common/upstream_failover.go`
  - 成功请求统一由 `RecordRequestFinalizeSuccess(currentBaseURL, apiKey, metricsServiceType, requestID, usage)` 落 metrics。
  - client cancel / failover / retry 分支不会重复成功落账。
- `backend-go/internal/metrics/channel_metrics.go`
  - `extractUsageTokens` 会将 `types.Usage` 拆成 input / output / cache creation / cache read。
  - 当 `PromptTokensTotal > 0 && CacheReadInputTokens > 0` 时，input tokens 使用 `PromptTokensTotal - CacheReadInputTokens`，避免 cached tokens 重复进入 input。
- `backend-go/internal/handlers/common/stream.go`
  - same-format raw stream 的 side-channel usage parser 入口。
- `backend-go/internal/handlers/chat/handler.go`
  - OpenAI Chat same-format 非流式 passthrough usage 解析。
- `backend-go/internal/handlers/gemini/handler.go`
  - Gemini native same-format 非流式 passthrough usage 解析。
- `backend-go/internal/handlers/responses/handler.go`
  - Responses same-format raw JSON / raw SSE passthrough。

已迁移并继续保留的 AxonHub 契约：

- passthrough 是否开启由 inbound/outbound API format 一致性决定，而不是 channel 旧开关决定。
- same-format raw response/SSE 可以原样返回，但 usage / metrics 必须旁路解析。
- raw stream fan-out 是 attempt 级状态，不跨 retry 复用。
- retry / failover 前必须 cancel 当前 attempt、关闭 response body、等待 fan-out goroutine 退出。
- fan-out 写 channel 时必须观察 context，避免消费者停止后永久阻塞。
- User-Agent passthrough 独立于 body/response passthrough。
- custom headers 必须在 final selected auth 之前应用。

没有迁移、也不计划迁移的 AxonHub 内容：

- 完整 orchestrator。
- 完整 middleware pipeline。
- RequestExecution 持久化体系。
- AxonHub 的 channel candidate / outbound stream persistence 大架构。

## 当前进度

### P0：旧 passthrough 字段删除

状态：已完成，未在本轮恢复。

覆盖：

- 后端 config 结构删除旧字段。
- config update / clone 删除旧字段处理。
- channel API / dashboard 返回删除旧字段。
- 前端类型、payload、UI 删除旧开关。
- frontend payload 测试锁定不再发送旧字段。
- backend/frontend spec 同步旧字段删除契约。

### P0：协议一致性自动 passthrough

状态：已完成，未在本轮改变入口规则。

核心入口：

- `backend-go/internal/passthrough/passthrough.go`

当前行为：

- `AllowsStrictBodyPassthrough` 和 `AllowsRawResponsePassthrough` 只基于 inbound/outbound API format 一致性。
- same-format 保真透传，cross-format 继续转换。
- sub2api auth-only 分支已删除。

### P1：raw stream fan-out / cleanup

状态：已完成，未在本轮改动生命周期语义。

当前能力：

- provider stream reader 接收 `context.Context`。
- handler 为每个 stream attempt 创建 attempt context。
- context cancel 时关闭 response body，解除 scanner/read 阻塞。
- preflight error / blacklist / cooldown / empty stream / unsupported flusher / client cancel 分支会 cancel 当前 attempt。
- raw fan-out cleanup 会等待 goroutine 退出再进入下一 key/channel attempt。

### P1：header / middleware 顺序契约

状态：已完成，未在本轮改变。

最终契约：

- base upstream headers 先构造。
- 平台控制 header 由 handler/provider 设置。
- `ApplyCustomHeaders` 在最终认证 header 之前执行。
- `SetAuthenticationHeader` / `SetGeminiAuthenticationHeader` 最后设置。
- custom `Authorization` / `x-api-key` / `x-goog-api-key` 不能覆盖 selected key。

### P1：User-Agent passthrough 策略

状态：已完成，未在本轮改变。

最终契约：

- User-Agent passthrough 默认开启，是 header 层策略。
- inbound `User-Agent` 默认保留。
- channel `customHeaders.User-Agent` 可以覆盖 inbound `User-Agent`。
- Claude 目标如果最终没有 `User-Agent`，补既有 fallback：
  - `claude-cli/2.0.34 (external, cli)`

### P1：敏感 inbound header 剥离

状态：已完成，未在本轮改变。

统一剥离范围包括：

- `Authorization`
- `x-api-key`
- `x-goog-api-key`
- `Cookie`
- `Set-Cookie`
- `Proxy-Authorization`
- `x-proxy-key`
- `X-Forwarded-For`
- `X-Forwarded-Host`
- `X-Forwarded-Proto`
- `X-Real-IP`
- `Forwarded`
- `Via`
- hop-by-hop headers
- `Accept-Encoding`
- vendor/client IP headers

### P1：计费 / usage metrics 旁路准确性

状态：本轮已完成代码补丁和测试。

已修复：

- Chat/OpenAI raw stream：
  - `prompt_tokens_details.cached_tokens` 进入 `CacheReadInputTokens`。
  - metrics input 从 `prompt_tokens - cached_tokens` 得出。
  - raw SSE bytes 保持不变。
- Chat/OpenAI non-stream raw passthrough：
  - 原样返回 JSON。
  - 旁路解析 cached tokens。
- Gemini native raw stream：
  - `cachedContentTokenCount` 进入 `CacheReadInputTokens`。
  - `InputTokens = max(promptTokenCount - cachedContentTokenCount, 0)`。
  - raw SSE bytes 保持不变。
- Gemini native non-stream same-format：
  - 原样返回 JSON body。
  - 旁路解析 `usageMetadata`。
  - 未知字段不被重写丢弃。
- Responses raw stream：
  - 从“最多缓存前 1 MiB 整体解析 usage”改为“按 SSE event 增量解析 usage”。
  - usage 在大流末尾时仍能进入 metrics。
  - 客户端收到的 raw SSE bytes 保持不变。
- backend spec：
  - `.trellis/spec/backend/quality-guidelines.md` 已补充 raw passthrough 计费旁路契约。

相关代码文件：

- `backend-go/internal/handlers/common/stream.go`
- `backend-go/internal/handlers/chat/handler.go`
- `backend-go/internal/handlers/gemini/handler.go`
- `backend-go/internal/handlers/responses/handler.go`

相关测试文件：

- `backend-go/internal/handlers/chat/handler_response_matrix_test.go`
- `backend-go/internal/handlers/gemini/handler_response_matrix_test.go`
- `backend-go/internal/handlers/responses/handler_response_matrix_test.go`

相关 spec 文件：

- `.trellis/spec/backend/quality-guidelines.md`

### 当前测试状态

已通过：

- `cd backend-go && go fmt ./internal/handlers/common ./internal/handlers/chat ./internal/handlers/gemini ./internal/handlers/responses`
- `cd backend-go && go test ./internal/handlers/chat -run "TestChatHandler_(StreamRawPassthroughPreservesOpenAIUpstreamSSEBytesAndMetrics|NonStreamRawPassthroughRecordsCachedTokens|StreamRawPassthroughCancelsFirstAttemptBeforeFailover|CrossFormatStreamDoesNotUseRawPassthrough)" -count=1 -v`
- `cd backend-go && go test ./internal/handlers/gemini -run "TestGeminiHandler_(StreamRawPassthroughPreservesNativeSSEBytesAndMetrics|NonStreamRawPassthroughRecordsCachedContentTokens|StreamRawPassthroughCancelsFirstAttemptBeforeFailover|CrossFormatStreamDoesNotUseRawPassthrough)" -count=1 -v`
- `cd backend-go && go test ./internal/handlers/responses -run "TestResponsesHandler_(StreamRawPassthroughPreservesSSEBytes|StreamRawPassthroughRecordsUsageAfterLargePrefix|StreamSameFormatAlwaysUsesRawPassthrough|NonStreamRawPassthroughPreservesUnknownFieldsAndRecordsMetrics)" -count=1 -v`
- `cd backend-go && go test ./internal/handlers/common ./internal/handlers/chat ./internal/handlers/gemini ./internal/handlers/responses ./internal/metrics -count=1`
- `cd backend-go && go vet ./...`
- `cd backend-go && go test ./...`
- `git diff --check`

注意：

- Trellis implement 子任务曾因上游 503 失败。
- 后续重试的 implement 子任务写入了部分补丁，但被关闭前没有完成汇报。
- Trellis check 子任务长时间无响应，已关闭。
- 主线程已补完、修正、格式化并完成上述验证。

## 当前判断：现在的计费准吗

当前结论：

- 对“上游按标准字段返回 usage / usageMetadata”的 same-format passthrough 路径，metrics token 口径现在是准的。
- 没有看到 raw passthrough 导致重复成功计费的主路径。
- 成功请求只通过统一的 `RecordRequestFinalizeSuccess(..., usage)` 落 metrics。
- cache read token 已从 input token 中拆分，避免 cached tokens 重复算入 input。
- Responses raw stream 不再因为 usage 位于 1 MiB 之后而漏记。

仍需明确的边界：

- 仓库里没有独立价格扣费器；这里的“计费”实际是 token/cache metrics。
- 如果某个 raw stream 上游完全不返回 usage / usageMetadata，本地无法得到精确上游计费，只能依赖已有估算或保持 nil usage。
- Messages/Claude 仍有低质量渠道估算兜底，但估算不等于上游精确账单。

## 待办清单

### 已完成，不再作为功能待办

- 删除旧 passthrough 配置字段。
- 协议一致性自动 passthrough。
- raw body / raw JSON / raw SSE 保真。
- stream attempt context / cancel / cleanup。
- raw stream fan-out across same-format native streams。
- header override 顺序。
- User-Agent passthrough 策略。
- sensitive inbound header stripping。
- Chat/OpenAI raw stream cached token metrics。
- Chat/OpenAI non-stream cached token metrics。
- Gemini native raw stream cached content metrics。
- Gemini native non-stream raw response 保真和 usage metrics。
- Responses raw stream 大流后置 usage 增量解析。
- backend spec 同步 raw passthrough 计费旁路契约。
- `go vet ./...`、`go test ./...`、`git diff --check` 已通过。

### 残余事项

1. 提交前需要重新确认脏文件归属。
   - 本轮 AxonHub 计费旁路相关文件：
     - `.trellis/spec/backend/quality-guidelines.md`
     - `backend-go/internal/handlers/chat/handler.go`
     - `backend-go/internal/handlers/chat/handler_response_matrix_test.go`
     - `backend-go/internal/handlers/common/stream.go`
     - `backend-go/internal/handlers/gemini/handler.go`
     - `backend-go/internal/handlers/gemini/handler_response_matrix_test.go`
     - `backend-go/internal/handlers/responses/handler.go`
     - `backend-go/internal/handlers/responses/handler_response_matrix_test.go`
     - `AxonHub-half.md`
   - 工作区仍有 Trellis v0.5 迁移相关 dirty files，不能和 AxonHub 补丁混在一起。

2. 如果要继续提升“精确计费”，需要明确无上游 usage 场景的产品策略。
   - 选项 A：保持 nil usage，不伪造精确计费。
   - 选项 B：沿用本地估算，但 UI/日志上标识为估算。
   - 选项 C：对特定低质量渠道启用估算覆盖。

3. Docker image workflow 失败仍未处理。
   - 二进制 release 已成功。
   - Docker 失败需要单独看 GitHub Action 日志。

4. 当前任务还可以执行 Trellis finish。
   - 需要先按文件分组准备提交计划。
   - AxonHub 代码改动不要混入 Trellis v0.5 迁移 dirty files。

## 提交记录

已存在的 AxonHub 提交：

- `53961bc fix: complete axonhub passthrough migration`
- `712ab31 docs: record axonhub passthrough closeout`

本轮新增 AxonHub 计费旁路补丁尚未提交。

## 2026-05-07 收尾：完整 axonhub forwarding 迁移完成（PR1+PR2+PR3）

本节标注 axonhub 风格 pipeline + load balancer + 计费 + dashboard 全套契约迁移完成。passthrough/billing/dashboard 三块对照 axonhub 已基本对齐。

### PR1 pipeline skeleton（740fa26 / 809723e / 8cf36ec / 04e4299 / 0733bbd）
- `internal/llm/`：Stream[T] + Request/Response/Usage/StreamEvent
- `internal/pipeline/`：Inbound/Outbound/Executor + 9-hook Middleware + AttemptState + Process 主循环 + 空流检测 prefetch + raw fan-out bridge
- `internal/handlers/{chat,messages,responses,gemini}/{inbound,outbound}_adapter.go`：8 个 adapter 复用现有 buildProviderRequest / providers.ConvertToProviderRequest，不重写协议转换
- handler.go / *_test.go 一字未改

### PR2 load balancer（6f5254e / 89b8830 / 7bb3b3b / 965d2a0）
- `internal/loadbalance/`：LoadBalancer + 6 strategy（Promotion / TraceAware / WeightRR / ErrorAware / LatencyAware / RateLimitAware）+ partial sort + 13 方法 ChannelMetricsProvider 接口
- 自实现 partialSortTopK，未引入 viterin/partial

### PR3 cutover + billing（4b846e1 → eb5c0e6，30 个 commit）
T1 metrics LB 数据面（FTTL/TPS/ActiveConn）；T2 stream 首事件 hook；T3 LBMetricsProvider 桥接 ccx (baseURL,apiKey,serviceType) tuple；T4 SelectChannel 拆解 + LB.Sort 接通；T5 ccx key/pause middleware（5xx/429/failoverRule/pauseRule → 9-hook）；T6 pricing 包（embed.FS + 12 模型 + decimal-as-string）；T7 NDJSON UsageStore（snake_case 对齐 axonhub + 200×10 并发 100% 落盘）；T8 4 handler 全部切到 pipeline.Process（messages/chat/responses/gemini，含各自 outbound cross-format dispatch + token normalization）；T8e 旧 handler dead helpers 清理；T9 dashboard cost + cache 字段；T10 前端 ChannelDashboardCard.vue。

### 已迁移契约（axonhub 对照）
- ✅ retry/failover 前 cancel + close body + wait fan-out goroutine（`pipeline.cleanupAttemptStreamResources` LIFO，feebbb6）
- ✅ User-Agent passthrough 独立
- ✅ custom headers 在 final selected auth 之前
- ✅ sensitive inbound header 剥离
- ✅ raw passthrough fan-out 是 attempt 级状态
- ✅ same-format raw / cross-format provider conversion 两路分流
- ✅ HTTP 200 + SSE event:error 帧识别为 retryable failure（pipeline middleware/sse_error_event）
- ✅ ChannelRetryable.NextKey 在同 channel 内换 key（wire LB 实现）
- ✅ per-key BlacklistKey / MarkKeyAsFailed / MatchPauseRule（ccx middleware）
- ✅ 价格计算 + NDJSON usage 落账 + dashboard 可视化

### Out-of-Scope（用户明确拒绝）
- feature flag 回退路径
- SQLite 持久化
- channel × model × key 二维明细 dashboard
- 配额预扣 / 限流 / model access control
- 外部价格源同步

### 残留 / 后续 PR 处理
- `internal/handlers/images/handler.go` 仍用 `TryUpstreamWithAllKeys`（不在 PR3 范围）；待 follow-up PR 切换后即可统删 `upstream_failover.go::TryUpstreamWithAllKeys` 函数本体
- `responses/handler.go` 的 `handleSuccess / handleStreamSuccess / handleRawResponsesStreamPassthrough` 因 `handler_session_test.go:54` 直接调用而保留，待测试重写后再清理
- `gemini/stream.go::handleStreamSuccess` lint hint 未处理（不阻塞）
- ChannelDashboardCard 集成到 Channels.vue / ChannelOrchestration.vue 视图层等下一 PR

### Spec 索引
- `.trellis/spec/backend/pipeline-architecture.md`（PR1 已落）
- `.trellis/spec/backend/pricing.md`（PR3 T6 落地）
- `.trellis/spec/backend/usage-store.md`（PR3 T7 落地）
- `.trellis/spec/frontend/channel-dashboard.md`（PR3 T9+T10 落地）
