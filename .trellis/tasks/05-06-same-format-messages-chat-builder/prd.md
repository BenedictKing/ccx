# same-format Messages/Chat 接入 builder

## Goal

将 same-format `/v1/messages -> claude` 与 `/v1/chat/completions -> openai` 的 upstream request 构造接入 forwarding builder，使 body/header/response/SSE 语义向 AxonHub 风格统一，同时保留 ccx 控制面。

## Scope

- 迁移 Messages same-format 上游请求构造到 forwarding builder。
- 迁移 Chat same-format 上游请求构造到 forwarding builder。
- same-format body 尽量保真，只允许平台必须字段 patch，例如 model mapping。
- non-stream response 继续原样返回客户端，并旁路解析 usage/metrics。
- stream SSE 继续 raw bytes 返回客户端，并旁路解析 usage/metrics。

## Out of Scope

- 不迁移 Responses/Gemini same-format。
- 不迁移 cross-format 转换路径。
- 不替换 `TryUpstreamWithAllKeys`、failover、错误分类、拉黑、冷却、熔断、metrics finalize。
- 不恢复旧 passthrough 配置字段。

## Acceptance Criteria

- Messages same-format request body 未知字段不丢。
- Chat same-format request body 未知字段不丢。
- same-format response JSON 未知字段不丢。
- raw SSE 的 `event:`、`id:`、`retry:`、注释行、`data:` 格式保持不变。
- usage/metrics 旁路解析相关测试仍通过。
- cross-format 路径不会误入 raw passthrough。

## Dependencies

- 依赖 `forwarding-builder-scaffold`。
- 可与 `same-format-responses-gemini-builder` 并行。
- 与 `attempt-raw-stream-cleanup-verification` 在 stream cleanup 部分需要协调。

