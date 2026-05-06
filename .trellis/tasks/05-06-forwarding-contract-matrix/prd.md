# 转发契约和现状矩阵

## Goal

建立 ccx 当前转发数据面的行为矩阵，明确 Messages、Chat、Responses、Gemini 在 body、header、response、SSE、usage/metrics 旁路解析上的现状与目标行为，为后续 forwarding builder 和 handler 接入提供边界。

## Scope

- 只做行为梳理和文档产出，不修改运行时代码。
- 覆盖四类入口：Messages、Chat Completions、Responses、Gemini native。
- 明确 same-format 与 cross-format 的差异：same-format 倾向 raw forwarding，cross-format 必须继续显式协议转换。
- 明确 header 清洗、custom headers、最终 selected auth 的顺序。
- 明确 usage/metrics 只能旁路解析，不能污染客户端可见响应。

## Out of Scope

- 不实现 forwarding builder。
- 不迁移 AxonHub orchestrator/pipeline。
- 不替换 scheduler、failover、key retry、blacklist、cooldown、circuit breaker、metrics finalize。
- 不恢复旧 passthrough 配置字段。

## Deliverables

- 在本任务下新增 `research/forwarding-contract-matrix.md`。
- 文档必须包含当前行为矩阵和目标行为矩阵。
- 文档必须列出本阶段明确不改的路径和原因。

## Acceptance Criteria

- Messages、Chat、Responses、Gemini 的 inbound format、outbound service type、same-format/cross-format 行为清晰列出。
- 每条路径说明 body 是否保真、是否 patch model/platform fields、是否协议转换。
- 每条路径说明 response 是否 raw passthrough、SSE 是否 raw bytes 转发、usage 是否旁路解析。
- header 顺序明确为：清洗 inbound 敏感/代理头 -> 应用安全 passthrough header -> 应用 custom headers -> 写入最终 selected auth。
- 明确 ccx 控制面保持不变。

## Dependencies

- 无前置依赖。
- `forwarding-builder-scaffold` 必须参考本任务输出。

