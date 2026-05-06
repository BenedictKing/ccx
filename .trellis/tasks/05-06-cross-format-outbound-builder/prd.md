# cross-format outbound request 接入 builder

## Goal

让协议转换后的 outbound request 也走 forwarding builder，统一 URL、headers、auth、body 构造入口，同时保持 handler/provider 的协议转换职责和 ccx 控制面。

## Scope

- handler/provider 继续负责协议转换，例如 Chat -> Claude、Responses -> Claude/OpenAI/Gemini。
- 转换结果交给 forwarding builder 构造上游请求。
- 收敛重复 URL 拼接和 header/auth 设置代码。
- 保持 cross-format response 按现有目标 handler 逻辑转换回客户端协议。

## Out of Scope

- 不把 cross-format 伪装成 raw passthrough。
- 不替换 scheduler、failover、key retry、blacklist、cooldown、circuit breaker、metrics finalize。
- 不迁移 AxonHub orchestrator/pipeline。
- 不恢复旧 passthrough 配置字段。

## Acceptance Criteria

- Chat -> Claude 转换结果与现有行为一致。
- Responses -> Claude/OpenAI/Gemini 等转换路径不回归。
- cross-format response 仍按当前协议转换路径返回客户端。
- failover、拉黑、冷却相关测试不回归。
- URL 拼接规则中 `#` 跳过版本前缀、已有 `/v1`、Gemini native path 保持不变。

## Dependencies

- 依赖 `forwarding-builder-scaffold`。
- 建议在两个 same-format builder 接入任务完成后推进。
- 与 `attempt-raw-stream-cleanup-verification` 在 failover cleanup 部分需要协调。

