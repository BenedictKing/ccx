# 统计 AxonHub 风格转发使用情况

## Goal

在保留 ccx 控制面的前提下，统计哪些请求实际走了 AxonHub 风格转发数据面，帮助后续观察迁移覆盖率、排查路径差异，并避免把 usage/metrics 旁路解析和客户端响应语义混在一起。

## What I Already Know

* 上一轮 `.trellis/tasks/05-06-unify-axonhub-forwarding` 已归档，相关实现提交包括 `docs: record axonhub forwarding contract`、`feat: unify axonhub forwarding request construction`、`fix: use shared raw cleanup for responses streams`。
* 当前已有 `backend-go/internal/forwarding` builder，并且 Messages/Chat/Responses/Gemini 的多个 upstream request 构造路径已经接入 builder。
* ccx 控制面仍应保留：scheduler、failover、key retry、blacklist、cooldown、circuit breaker、metrics finalize 不迁移到 AxonHub orchestrator/pipeline。
* 新统计需求应复用现有 metrics/logging 边界，不能污染客户端可见 response/SSE。

## Assumptions

* “使用 axonhub”指使用本轮迁移后的 AxonHub 风格数据面转发路径，而不是引入完整 AxonHub 服务或 pipeline。
* 统计优先服务运维观测，不改变转发行为。

## Open Questions

* None.

## Requirements

* 记录请求是否走 AxonHub 风格 forwarding builder 或 raw passthrough 数据面。
* 同时统计请求次数和 token usage。
* 请求次数统计必须覆盖所有 AxonHub 风格转发路径。
* token usage 只在现有 usage 旁路解析能拿到时累计；不能为了统计读取、缓存、改写或重放客户端可见响应。
* 区分 inbound 协议族：Messages、Chat、Responses、Gemini。
* 区分 same-format raw forwarding 与 cross-format converted forwarding。
* 统计逻辑不得修改客户端可见 body、header、response 或 SSE。
* 统计逻辑不得替换 ccx 现有 metrics finalize、failover、blacklist、cooldown、circuit breaker。

## Acceptance Criteria

* [ ] 能在现有观测面中看到 AxonHub 风格转发使用情况。
* [ ] 能看到 AxonHub 风格转发的请求次数。
* [ ] 能在 usage 可得时看到 AxonHub 风格转发的 token usage 累计。
* [ ] same-format 与 cross-format 能被区分统计。
* [ ] 统计不会改变现有请求转发和响应透传语义。
* [ ] 新增或更新后端测试覆盖统计标记/累计逻辑。
* [ ] `go test ./...` 和 `git diff --check` 通过。

## Definition of Done

* Tests added/updated for changed metrics/logging behavior.
* Backend verification is green.
* Specs/notes updated if a new observability contract is introduced.
* Rollback risk is low because forwarding behavior is not changed.

## Out of Scope

* 不引入完整 AxonHub orchestrator/pipeline。
* 不恢复旧 passthrough 配置字段。
* 不改变 scheduler/failover/key retry/blacklist/cooldown/circuit breaker/metrics finalize 的归属。
* 不为了统计改写客户端响应或 SSE。

## Technical Notes

* Existing forwarding builder: `backend-go/internal/forwarding/builder.go`.
* Existing forwarding boundary spec: `.trellis/spec/backend/quality-guidelines.md`.
* Existing metrics and channel logs live under `backend-go/internal/metrics` and handler failover paths.
* Parent task archive: `.trellis/tasks/archive/2026-05/05-06-unify-axonhub-forwarding/`.

## Decision

统计口径采用请求次数 + token usage 两者都要。请求次数作为基础覆盖率指标；token usage 作为用量指标，在现有 usage 旁路解析产出数据后累计，不引入新的响应解析路径，也不改变客户端可见响应。
