# forwarding builder scaffold

## Goal

引入统一 forwarding request/builder scaffold，集中 URL、method、body、content type、safe inbound headers、custom headers、final auth、raw response/stream strategy 的构造入口，但不改变现有 handler 行为。

## Scope

- 新增内部 forwarding builder 数据结构和构造函数。
- 复用现有 `PrepareUpstreamHeaders`、`ApplyCustomHeaders`、`SetAuthenticationHeader`。
- 增加 builder 单元测试，锁定 header/auth 顺序。
- 只提供迁移入口，不把所有 handler 一次性迁移。

## Out of Scope

- 不改 scheduler、failover、key retry、blacklist、cooldown、circuit breaker、metrics finalize。
- 不迁移 AxonHub orchestrator/pipeline。
- 不恢复旧 passthrough 配置字段。
- 不改变现有客户端可见 response/SSE 行为。

## Acceptance Criteria

- builder 可以构造上游请求所需的 URL、method、body、content type 和 headers。
- 单元测试证明 inbound 敏感 headers 被剥离。
- 单元测试证明 custom auth-like headers 不能覆盖最终 selected key。
- 单元测试证明 User-Agent passthrough/fallback 行为不变。
- 现有 handler 行为不变，后端测试通过。

## Dependencies

- 依赖 `forwarding-contract-matrix` 的契约输出。
- `same-format-messages-chat-builder`、`same-format-responses-gemini-builder`、`cross-format-outbound-builder` 依赖本任务。

