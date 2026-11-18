# GitHub 反馈首批实现与交付记录

日期：2026-11-18

基线：`codex/v3-lean-from-2.9.37` / `b0da2056`。用户自行合并 main 并更新版本。

## 实际完成

本批实现 #273 的模型级 Copilot 路由，补齐 PR #306/#308/#309/#310 对应的转换终态缺陷，并修订全量处理方案。GitHub issue/PR 保持原状态；未变更版本、推送、发布或重启运行实例。

### 行为与边界

- Copilot 按映射后模型的 `supported_endpoints` 选择 HTTP/WS 协议；请求构造与响应解析使用同一结果。认证、日志和计费仍归原 Copilot 渠道，配置副本不会写回持久配置。
- 模型发现与文本能力测试复用解析器；发现结果保留 `modelDetails`，Web/Desktop API 类型同步该可选字段。未对模型名做协议猜测。
- 能力缓存按 runtime token、API 地址、代理和 TLS 配置隔离，5 分钟刷新、限制容量、合并并发请求。临时发现失败不猜测协议；只有模型缺元数据或 `/models` 明确返回 404/405 时保留有诊断的历史默认值。
- 原生 Responses SSE 不再按 Chat `choices` 解析；同协议继续走既有透传路径。默认没有额外推理调用，显式折叠续轮保留受保护认证。
- 声明 `ws:/responses` 的模型使用原生 WebSocket v2，保留 previous_response_id 与密文；不能跨协议恢复的密文/历史快照明确失败。WS JSON 解析保留大整数精度。
- 本地 compact v1/v2 同样按实际摘要模型选择协议，并使用对应响应解析器。
- Claude 非流式文本转为合法 Responses `message/output_text`。Chat 工具＋stop 输出 `tool_use`，Chat/Responses 内容过滤输出 `refusal`，Gemini 流式/非流式使用同一停止原因映射。

## 验证

- 后端 `go test ./...`、`go vet ./...` 通过；全量 race 已通过，compact 补齐后对 copilot、providers 和全部 handlers 再跑 race 通过。
- Copilot 集成矩阵覆盖四入口 × 三种上游协议 × 流式/非流式，共 24 个场景；另覆盖发现/探测、原生 WS 两轮、显式推理开关、compact v1/v2、加密历史与缓存隔离/刷新/并发。
- 停止原因回归先在旧实现复现失败，再验证修复；Claude 非流式正文符合 Responses message/output_text 结构。
- Desktop 独立模块 `go test ./...`、`go vet ./...` 通过。
- Web 252 项、Desktop 前端 97 项测试通过；两端构建及最终 API 类型检查通过。当前 worktree 使用独立现有依赖，没有复制主工作区缓存。
- 增量 `golangci-lint --new-from-rev=HEAD` 为 0 issues；修改的 Web 类型文件 ESLint、Go 格式和任务 diff 检查通过。
- 根 `make build` 通过。验证使用隔离上游；未调用真实 Copilot 账号或生产推理 API，未停止或重启现有服务。

## 后续验收与待处理

- #273 已完成隔离上游验证；真实 Copilot 账号及用户环境验收仍待完成。工具/图片/思考能力未实测的项目明确保持“未验证”。
- #275/#282/#297/#263 仍需原始请求、响应和选路样本，不能据本批通用修复宣布解决。#282 还必须验证任务是否正常继续，不能只清理标签。
- #294 普通渠道的候选链配置与 Web/Desktop 编辑流程尚待实现，不能把站点向导的全局策略算作完整交付。
- #247/#280/#295/#299 按总方案保持独立功能评审，不纳入本批缺陷完成声明。
- main 合并后重新检查 PR 的实际缺失行为，特别是 lean 不含的 autopilot 模块和 #307；不能按 lean 状态直接关闭面向 main 的 PR。

## 本批文件清单

- [backend-go/internal/converters/responses_converter.go](../../../backend-go/internal/converters/responses_converter.go)
- [backend-go/internal/converters/responses_converter_test.go](../../../backend-go/internal/converters/responses_converter_test.go)
- [backend-go/internal/copilot/models.go](../../../backend-go/internal/copilot/models.go)
- [backend-go/internal/copilot/models_test.go](../../../backend-go/internal/copilot/models_test.go)
- [backend-go/internal/handlers/capability_copilot.go](../../../backend-go/internal/handlers/capability_copilot.go)
- [backend-go/internal/handlers/capability_copilot_test.go](../../../backend-go/internal/handlers/capability_copilot_test.go)
- [backend-go/internal/handlers/capability_test_redirect.go](../../../backend-go/internal/handlers/capability_test_redirect.go)
- [backend-go/internal/handlers/capability_test_request.go](../../../backend-go/internal/handlers/capability_test_request.go)
- [backend-go/internal/handlers/capability_test_runner.go](../../../backend-go/internal/handlers/capability_test_runner.go)
- [backend-go/internal/handlers/channel_discovery.go](../../../backend-go/internal/handlers/channel_discovery.go)
- [backend-go/internal/handlers/common/upstream_failover.go](../../../backend-go/internal/handlers/common/upstream_failover.go)
- [backend-go/internal/handlers/copilot_integration_test.go](../../../backend-go/internal/handlers/copilot_integration_test.go)
- [backend-go/internal/handlers/messages/handler.go](../../../backend-go/internal/handlers/messages/handler.go)
- [backend-go/internal/handlers/responses/compact_local.go](../../../backend-go/internal/handlers/responses/compact_local.go)
- [backend-go/internal/handlers/responses/compact_v2.go](../../../backend-go/internal/handlers/responses/compact_v2.go)
- [backend-go/internal/handlers/responses/copilot_compact_test.go](../../../backend-go/internal/handlers/responses/copilot_compact_test.go)
- [backend-go/internal/handlers/responses/copilot_websocket_test.go](../../../backend-go/internal/handlers/responses/copilot_websocket_test.go)
- [backend-go/internal/handlers/responses/fold.go](../../../backend-go/internal/handlers/responses/fold.go)
- [backend-go/internal/handlers/responses/handler.go](../../../backend-go/internal/handlers/responses/handler.go)
- [backend-go/internal/handlers/responses/websocket.go](../../../backend-go/internal/handlers/responses/websocket.go)
- [backend-go/internal/providers/copilot_route.go](../../../backend-go/internal/providers/copilot_route.go)
- [backend-go/internal/providers/copilot_route_test.go](../../../backend-go/internal/providers/copilot_route_test.go)
- [backend-go/internal/providers/gemini.go](../../../backend-go/internal/providers/gemini.go)
- [backend-go/internal/providers/openai.go](../../../backend-go/internal/providers/openai.go)
- [backend-go/internal/providers/protocol_stop_reason_test.go](../../../backend-go/internal/providers/protocol_stop_reason_test.go)
- [backend-go/internal/providers/responses.go](../../../backend-go/internal/providers/responses.go)
- [backend-go/internal/providers/responses_stop_reason.go](../../../backend-go/internal/providers/responses_stop_reason.go)
- [desktop/frontend/src/services/admin-api.ts](../../../desktop/frontend/src/services/admin-api.ts)
- [docs/providers/copilot.md](../../../docs/providers/copilot.md)
- [docs/superpowers/plans/2026-11-18-github-feedback-implementation.md](../../../docs/superpowers/plans/2026-11-18-github-feedback-implementation.md)
- [docs/superpowers/plans/2026-11-18-github-feedback-remediation.md](../../../docs/superpowers/plans/2026-11-18-github-feedback-remediation.md)
- [frontend/src/services/api-types.ts](../../../frontend/src/services/api-types.ts)
