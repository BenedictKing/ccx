# 统一 AxonHub 转发逻辑

## Goal

让 ccx 在“客户端数据如何转发到上游服务端”这一层与 AxonHub 的转发语义保持一致，重点是 same-format 请求的 body/header/response/SSE 保真、attempt 生命周期清理、usage/metrics 旁路解析，以及 cross-format 路径继续明确转换。

## What I Already Know

* 用户希望 ccx 在“转发上面”和 AxonHub 一致。
* 用户进一步确认：希望按 AxonHub 格式/语义转发，但重试、错误处理、拉黑、冷却/熔断等机制继续使用 ccx 现有实现。
* 当前 ccx 不是完整 AxonHub 架构；仍是各协议 handler + scheduler + `TryUpstreamWithAllKeys`。
* 当前 same-format passthrough 判定集中在 `backend-go/internal/passthrough/passthrough.go`，依据 inbound/outbound API format 是否一致。
* 当前渠道/Key/BaseURL failover 集中在 `backend-go/internal/handlers/common/upstream_failover.go`。
* 当前 raw stream fan-out/helper 集中在 `backend-go/internal/handlers/common/stream.go`，Messages/OpenAI Chat/Gemini/Responses 已有 raw stream 保真和 usage 旁路解析。
* 当前 header 清洗和最终认证写入集中在 `backend-go/internal/utils/headers.go`。
* `axonhub/` 和 `axonhub.md` 当前不存在；`AxonHub-half.md` 仍存在，记录上轮迁移/计费旁路收口信息。

## Assumptions

* “一致”明确指请求转发格式/语义一致，不要求完整迁移 AxonHub 的 orchestrator/pipeline 代码结构。
* ccx 的 scheduler、channel/key/baseURL retry、failover、错误分类、拉黑、冷却、熔断、metrics finalize 仍作为权威控制面。
* 不恢复旧 passthrough 配置字段；继续允许破坏旧格式兼容。
* cross-format 请求仍需要转换，不能盲目 raw passthrough。

## Open Questions

* AxonHub 格式转发是否只要求 same-format raw forwarding 完全对齐，还是 cross-format 转换后的 outbound request 也要按 AxonHub 的 request construction/header/body 规则统一？

## Recommended Direction

综合效果最好的方向是分阶段采用“统一 forwarding builder”：

* 第一阶段先把 same-format raw forwarding 的客户端可见行为严格对齐 AxonHub，保证 body/header/response/SSE 保真和 usage 旁路解析稳定。
* 第二阶段把 cross-format 转换后的 outbound request 也接入同一 forwarding builder，让不同协议 handler 只负责协议转换结果，统一层负责 URL、headers、auth、body patch、stream/non-stream forwarding。
* 控制面始终保留 ccx：scheduler、key/BaseURL retry、failover、错误分类、拉黑、冷却、熔断和 metrics finalize 不替换。

这个方向比只做 same-format 更一致，也比一次性迁移完整 AxonHub 核心风险更低。

## Requirements

* 明确 ccx 当前每个入口协议的请求转发路径：Messages、Chat、Responses、Gemini。
* 转发数据面向 AxonHub 对齐：request body、upstream headers、non-stream response、stream SSE 的客户端可见内容应尽量按 AxonHub passthrough 语义保真。
* 控制面保留 ccx：渠道选择、trace affinity、key/baseURL retry、failover rule、流内错误拦截、拉黑、冷却、熔断、metrics 成功/失败结算继续走 ccx 现有路径。
* 对 same-format 转发建立统一语义：客户端 body 尽量保真，只允许平台必须控制的字段 patch。
* 对 upstream header 建立统一语义：清洗 inbound 敏感/代理头，保留安全 passthrough header，custom headers 在最终 auth 之前应用。
* 对 response 建立统一语义：same-format non-stream/raw stream 尽量原样返回客户端，usage/metrics 只能旁路解析。
* 对 attempt 生命周期建立统一语义：failover/client cancel 前释放当前 response body、fan-out goroutine 和 reader。
* 保持 cross-format 路径走转换，不把不同协议之间伪装成 raw passthrough。

## Acceptance Criteria

* [ ] PRD 明确目标中的“一致”范围。
* [ ] 明确区分“AxonHub 风格转发数据面”和“ccx 控制面”边界。
* [ ] 列出 Messages、Chat、Responses、Gemini 的当前转发差异点。
* [ ] 明确哪些行为需要改到 AxonHub 一致，哪些保持 ccx 架构。
* [ ] 如果进入实现，新增/更新回归测试覆盖 same-format body/header/response/SSE 保真和 cross-format 非 raw passthrough。
* [ ] 如果进入实现，后端 `go test ./...` 通过，`git diff --check` 通过。

## Definition of Done

* Tests added/updated for changed forwarding behavior.
* Backend verification is green.
* Specs/notes updated if forwarding contracts change.
* Rollback risk is considered because request forwarding is core proxy behavior.

## Out of Scope

* Reintroducing removed passthrough compatibility fields.
* UI redesign.
* Full AxonHub architecture migration.
* Replacing ccx scheduler/failover/blacklist/cooldown/circuit-breaker behavior with AxonHub equivalents.

## Technical Notes

* Summary and task split: `.trellis/tasks/05-06-unify-axonhub-forwarding/forwarding-plan.md`.
* Current passthrough decision: `backend-go/internal/passthrough/passthrough.go`.
* Current channel/key/baseURL retry loop: `backend-go/internal/handlers/common/upstream_failover.go`.
* Current multi-channel selection wrapper: `backend-go/internal/handlers/common/multi_channel_failover.go`.
* Current raw stream fan-out helpers: `backend-go/internal/handlers/common/stream.go`.
* Current upstream header helper: `backend-go/internal/utils/headers.go`.
* Current protocol handlers:
  * `backend-go/internal/handlers/messages/handler.go`
  * `backend-go/internal/handlers/chat/handler.go`
  * `backend-go/internal/handlers/responses/handler.go`
  * `backend-go/internal/handlers/gemini/handler.go`
