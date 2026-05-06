# same-format Responses/Gemini 接入 builder

## Goal

将 same-format `/v1/responses -> responses` 与 Gemini native -> `gemini` 的 upstream request 构造接入 forwarding builder，使 body/header/response/SSE 语义向 AxonHub 风格统一，同时保留 ccx 控制面。

## Scope

- 迁移 Responses same-format 上游请求构造到 forwarding builder。
- 迁移 Gemini native same-format 上游请求构造到 forwarding builder。
- Responses 只允许平台必须字段 patch。
- Gemini native 保持原生路径和 URL 拼接规则。
- non-stream response 和 stream SSE 继续客户端可见保真，usage/metrics 只旁路解析。

## Out of Scope

- 不迁移 Messages/Chat same-format。
- 不迁移 cross-format 转换路径。
- 不替换 scheduler、failover、key retry、blacklist、cooldown、circuit breaker、metrics finalize。
- 不恢复旧 passthrough 配置字段。

## Acceptance Criteria

- Responses same-format request body 未知字段不丢。
- Gemini native same-format request body 未知字段不丢。
- Responses/Gemini same-format response JSON 未知字段不丢。
- raw SSE 的 `event:`、`id:`、`retry:`、注释行、`data:` 格式保持不变。
- Gemini native path、已有 `/v1`、`#` 跳过版本前缀规则不回归。
- usage/metrics 旁路解析相关测试仍通过。

## Dependencies

- 依赖 `forwarding-builder-scaffold`。
- 可与 `same-format-messages-chat-builder` 并行。
- 与 `attempt-raw-stream-cleanup-verification` 在 stream cleanup 部分需要协调。

