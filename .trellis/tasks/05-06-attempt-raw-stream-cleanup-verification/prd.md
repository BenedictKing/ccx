# attempt 生命周期、raw stream cleanup 和最终验证

## Goal

审计并收口 attempt 生命周期和 raw stream cleanup，确保统一 forwarding builder 后不会引入 response body、reader、fan-out goroutine 泄漏；最终更新 spec 并完成后端验证。

## Scope

- 审计所有 stream 路径的 cancel、body close、fan-out done wait。
- 统一 failover 前 cleanup helper 的使用方式。
- 覆盖 client cancel、preflight error、流内 cooldown/blacklist、empty stream。
- 更新 backend spec，记录 forwarding builder 数据面/ccx 控制面边界。
- 运行 `go fmt ./...`、`go test ./...`、`git diff --check`。

## Out of Scope

- 不替换 ccx 控制面。
- 不恢复旧 passthrough 配置字段。
- 不引入 AxonHub 完整 orchestrator/pipeline。

## Acceptance Criteria

- failover 前当前 attempt body 已关闭。
- client cancel 后不会继续阻塞 fan-out。
- preflight 失败不会写出客户端 header。
- 所有现有 stream 回归测试通过。
- backend spec 明确说明 forwarding builder 负责数据面构造，不负责 scheduler/failover/key retry/blacklist/cooldown/circuit breaker/metrics finalize。
- `go fmt ./...`、`go test ./...`、`git diff --check` 通过。

## Dependencies

- 最终验证依赖 `forwarding-builder-scaffold`、两个 same-format 接入任务和 `cross-format-outbound-builder`。
- cleanup 审计可在 same-format 接入期间并行进行，但最终验证必须最后执行。

