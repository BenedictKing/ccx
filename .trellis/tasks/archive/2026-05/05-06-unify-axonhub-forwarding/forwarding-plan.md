# ccx 按 AxonHub 风格统一转发：总结与任务拆分

## 目标结论

本次目标不是把 ccx 完整替换成 AxonHub 核心，而是把“转发数据面”统一到 AxonHub 风格，同时保留 ccx 已有控制面。

要对齐 AxonHub 的部分：

* 客户端 request body 如何进入上游请求。
* inbound headers 如何清洗、保留、覆盖。
* custom headers 和最终 auth 的顺序。
* same-format non-stream response 如何原样返回。
* same-format stream SSE 如何原始返回。
* usage/metrics 如何旁路解析，不污染客户端可见响应。
* 每次 attempt 的 response body、reader、fan-out goroutine 如何释放。

继续保留 ccx 的部分：

* channel scheduler。
* trace affinity。
* key/BaseURL retry。
* failover rule。
* 错误分类。
* key 拉黑。
* 冷却。
* 熔断。
* metrics 成功/失败结算。

推荐最终方向：先把 same-format raw forwarding 严格收敛，再把 cross-format 转换后的 outbound request 也接入统一 forwarding builder。

## 当前 ccx 请求路径

当前 ccx 是按协议分 handler：

* Messages: `backend-go/internal/handlers/messages/handler.go`
* Chat: `backend-go/internal/handlers/chat/handler.go`
* Responses: `backend-go/internal/handlers/responses/handler.go`
* Gemini: `backend-go/internal/handlers/gemini/handler.go`

公共控制面主要在：

* passthrough 判定：`backend-go/internal/passthrough/passthrough.go`
* 多渠道选择：`backend-go/internal/handlers/common/multi_channel_failover.go`
* key/BaseURL attempt 循环：`backend-go/internal/handlers/common/upstream_failover.go`
* stream/raw fan-out：`backend-go/internal/handlers/common/stream.go`
* header 清洗/auth：`backend-go/internal/utils/headers.go`

当前已经具备的基础：

* same-format 是否 raw passthrough 已统一由 inbound/outbound API format 决定。
* raw stream 已有 Messages、Chat/OpenAI、Gemini、Responses 的保真和 usage 旁路解析。
* header 已有敏感 inbound header 剥离、custom headers before final auth 的基本契约。
* failover、拉黑、冷却、熔断仍在 ccx 控制面。

当前主要问题：

* 转发构造逻辑仍散在各协议 handler/provider 中。
* same-format 和 cross-format 的 outbound request 构造规则不够集中。
* body patch、URL 拼接、headers、auth、stream strategy 的边界还不是一个统一抽象。
* 以后新增协议/路径时，容易重新出现 handler 局部实现差异。

## 设计原则

1. 数据面统一，控制面不替换。
2. same-format 优先保真，cross-format 明确转换。
3. usage/metrics 只能旁路解析，不能改写客户端可见 raw response/SSE。
4. final selected auth 永远最后写入。
5. failover 前必须释放当前 attempt 的 body/reader/fan-out。
6. 不恢复旧 passthrough 配置字段。
7. 不一次性迁移 AxonHub 完整 orchestrator/pipeline。

## 推荐架构

建议新增或收敛出一个 forwarding builder 层，职责是构造“已准备好发给上游”的请求和响应策略。

目标形态：

```text
protocol handler
  -> 读取/解析客户端请求
  -> 判断目标上游 service type
  -> same-format: 生成 passthrough outbound payload
  -> cross-format: 先生成目标协议 payload
  -> forwarding builder 统一构造 upstream request
       - URL
       - method
       - body
       - content type
       - safe inbound headers
       - custom headers
       - final selected auth
       - raw/non-raw response strategy
  -> ccx TryUpstreamWithAllKeys 发送请求和处理控制面
```

forwarding builder 不负责：

* 选渠道。
* 选 key。
* 重试。
* failover。
* 拉黑/冷却/熔断。
* metrics finalize。

forwarding builder 负责：

* 标准化 outbound URL。
* 标准化 header 顺序。
* 标准化 body patch 入口。
* 标准化 same-format raw response 策略。
* 为 cross-format 提供统一 outbound request 构造入口。

## 任务拆分

### Task 1: 建立转发契约和现状矩阵

目的：先把行为写清楚，避免实现时扩大范围。

工作内容：

* 列出四类入口的 inbound format、outbound service type、same-format/cross-format 行为。
* 列出每条路径当前 body 是否保真、是否 patch model、是否转换协议。
* 列出每条路径当前 response 是否 raw passthrough、是否旁路解析 usage。
* 列出 header 清洗和 auth 写入顺序。
* 写入 `.trellis/spec/backend/quality-guidelines.md` 或任务 research 文档。

验收：

* 有一张明确矩阵说明 Messages/Chat/Responses/Gemini 的当前行为和目标行为。
* 明确哪些路径本阶段不改。

### Task 2: 抽象 forwarding request model

目的：先引入统一数据结构，不急着大规模迁移。

建议新增内部结构，例如：

```go
type ForwardingRequest struct {
    Method          string
    URL             string
    Body            []byte
    ContentType     string
    ServiceType     string
    CustomHeaders   map[string]string
    AuthKind        AuthKind
    APIKey          string
    RawResponse     bool
    RawStream       bool
}
```

工作内容：

* 定义 forwarding builder 的输入/输出。
* 复用现有 `PrepareUpstreamHeaders`、`ApplyCustomHeaders`、`SetAuthenticationHeader`。
* 不改变现有 handler 行为，只增加测试覆盖 builder 的 header/auth 顺序。

验收：

* 单元测试证明 inbound 敏感 headers 被剥离。
* 单元测试证明 custom auth-like headers 不能覆盖 selected key。
* 单元测试证明 User-Agent passthrough/fallback 行为不变。

### Task 3: same-format 路径接入 builder

目的：先对齐最接近 AxonHub 的 raw forwarding。

优先顺序：

1. `/v1/messages -> claude`
2. `/v1/chat/completions -> openai`
3. `/v1/responses -> responses`
4. Gemini native -> `gemini`

工作内容：

* same-format body 通过统一 builder 构造上游请求。
* body 只允许平台字段 patch，例如 model mapping、Responses platform fields。
* non-stream response 继续原样返回客户端并旁路解析 usage。
* stream SSE 继续原始 bytes 返回客户端并旁路解析 usage。

验收：

* same-format request body 未知字段不丢。
* same-format response JSON 未知字段不丢。
* raw SSE 的 `event:`、`id:`、`retry:`、注释行、`data:` 格式保持不变。
* usage/metrics 测试仍通过。
* cross-format 测试证明不会误入 raw passthrough。

### Task 4: cross-format outbound request 接入 builder

目的：让转换后的请求也走统一转发构造层。

工作内容：

* handler/provider 仍负责协议转换，例如 Chat -> Claude、Responses -> Claude。
* 转换结果交给 forwarding builder 统一构造 URL、headers、auth、body。
* 收敛重复 URL 拼接和 header 设置代码。
* 保持 ccx 现有转换结果和错误处理行为。

验收：

* Chat -> Claude 转换结果与现有行为一致。
* Responses -> Claude/OpenAI/Gemini 等现有转换路径不回归。
* cross-format response 仍按目标 handler 需要转换回客户端协议。
* failover、拉黑、冷却测试不回归。

### Task 5: attempt 生命周期和 raw stream 清理审计

目的：确保统一转发后不会引入 goroutine/body 泄漏。

工作内容：

* 审计所有 stream 路径的 cancel、body close、fan-out done wait。
* 统一 failover 前 cleanup 的 helper 使用方式。
* 覆盖 client cancel、preflight error、流内 cooldown/blacklist、empty stream。

验收：

* failover 前当前 attempt body 已关闭。
* client cancel 后不会继续阻塞 fan-out。
* preflight 失败不会写出客户端 header。
* 现有 stream 回归测试通过。

### Task 6: spec、测试和清理

目的：把新转发契约固化下来。

工作内容：

* 更新 backend spec，记录 forwarding builder 边界。
* 删除被 builder 取代的重复 helper。
* 保留 ccx 控制面，不引入 AxonHub 完整 orchestrator。
* 运行完整后端测试。

验收：

* `go fmt ./...` 通过。
* `go test ./...` 通过。
* `git diff --check` 通过。
* spec 明确说明数据面/控制面边界。

## 建议实施顺序

推荐分 3 个 PR/提交批次：

1. **契约 + builder scaffold**
   * 完成 Task 1、Task 2。
   * 风险低，先建立统一抽象和测试。

2. **same-format forwarding 收敛**
   * 完成 Task 3、Task 5 的 same-format 部分。
   * 这是最关键的 AxonHub 风格对齐。

3. **cross-format outbound 收敛 + 清理**
   * 完成 Task 4、Task 5 剩余部分、Task 6。
   * 降低长期维护成本。

## 风险点

* response 已经写出 header 后，某些错误不能再安全 failover。
* raw stream 保真和 usage 解析不能共用会改写内容的 parser。
* header 统一时不能让 custom headers 覆盖 selected auth。
* cross-format 转换容易改变旧客户端可见响应格式，需要测试锁定。
* URL 拼接规则中 `#` 跳过版本前缀、已有 `/v1`、Gemini native path 都要保留。

## 最小可行版本

如果只做 MVP，建议范围是：

* 建立 forwarding builder。
* 先迁移 same-format Messages/Chat/Gemini/Responses 的 upstream request 构造。
* 不动 cross-format 转换结果。
* 保持所有 ccx 控制面原样。

这样可以最快获得 AxonHub 风格转发的主要效果，同时把核心回归风险压低。
