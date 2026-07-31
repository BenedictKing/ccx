# TODO

## 更新规范

### 提交问题

在本文档末尾添加新条目，格式：

```markdown
## [ ] 简短标题

问题描述，包含复现条件和预期行为。
```

如有对应 GitHub Issue，在标题中标注，如 `## [ ] 标题 (#issue号)`。

### 解决更新

问题修复后，将 `[ ]` 改为 `[x]`，并在描述下方追加：

```markdown
**关键提交：**
- `commit_hash` commit message
```

如涉及多文件变更，可补充 `**关键变更：**` 列出受影响文件。

---

## [ ] 管理后台使用报表导出 (#229)

来源：https://github.com/BenedictKing/ccx/issues/229

需求：在管理后台为渠道使用统计增加“导出使用报表”能力，支持按月份（默认当前月，后续可考虑自定义日期范围）导出当前渠道或全部渠道的使用数据。

建议范围：优先支持浏览器下载 CSV；JSON 可作为可选格式；XLSX 和定时邮件属于 nice-to-have，暂不作为首批必做项。

导出内容应覆盖当前仪表盘已有的核心指标：渠道名、服务类型、日期/小时桶、请求数、可用率、输入/输出 token、缓存读写、RPM、TPM。

备注：该需求目前不确定是否有更多用户需要，先记录为待评估项。

---

## [ ] gpt-5.6的适配

2.openai新增了"OpenAI-Beta": “{client_header:OpenAI-Beta}” 这个，来传输子代理相关信息，这个也需要调整
3.工具调用传参之类的都需要优化
4.提醒下同行，oai在最新版本codex里面加入了设备验证相关信息，可能会封pro号

---

## [ ] 在codex里面使用imagegen使用上游的文生图

百炼生图是生成url, 和 openai生成base64不一样，比如我在codex介入ccx, ccx配的是百炼，然后在codex里面用 codex自带技能 imagegen 能调用百炼生图模型 成功生图
https://github.com/QuantumNous/new-api/issues/5513

---

## [ ] 火山方舟团队版套餐自动探测与用量查询

当前状态：火山套餐绑定与用量刷新仅支持个人版 `Agent Plan` / `Coding Plan`。其中个人版分别使用 `GetAFPUsage` 和 `GetCodingPlanUsage`；`cd559c8a` 已修正个人版 Coding Plan 的 OpenAPI 地址、签名和百分比响应解析。团队版暂未纳入套餐识别、持久化和管理界面展示。

团队版候选调用链（统一使用 `open.volcengineapi.com`、`ark` 签名 scope）：

| 套餐 | 自动探测 | 用量查询 |
| --- | --- | --- |
| Agent Plan Team | `GetSeatInfo(Scene="agent_plan_enterprise")`，取得调用身份绑定的 `SeatID` | `GetSeatAFPUsage(SeatIDs=[SeatID])` |
| Coding Plan Team | `GetSeatInfo(Scene="")`，取得调用身份绑定的 `SeatID` | `GetSeatInfoUsage(SeatID=SeatID, Scene="")` |

设计约束：

- 同一火山账号可能同时拥有个人版与团队版，不能把“个人版查询失败后回退团队版”作为套餐判定；各套餐桶应独立探测、独立记录错误。
- 当前 `VolcengineAccessKeyPair` 只有单个 `Plan` 和单份 `Usage`。实现前需确定是增加 `edition: personal|team`、`seatId`，还是改为可同时保存多个套餐桶；不得让团队版结果覆盖个人版快照。
- 当同一产品同时存在个人版和团队版时，需要明确推理 Key 与订阅/席位的关联策略，无法可靠消歧时应展示候选并要求用户选择，不能静默猜测。
- 管理 API 仍不得回显 AK/SK；团队版权限错误和单个套餐桶失败不得阻断其他套餐的探测与展示。
- 保持现有个人版行为和配置文件向后兼容，并为四种套餐组合、无席位、多个套餐并存和 `AccessDenied` 增加测试。

参考：https://github.com/volcengine/ark-cli/blob/main/skills/arkcli-usage/references/arkcli-usage-plan.md

---

> **上游版本变更**

## [ ] Codex rust-v0.145.0 上游协议/工具变更评估

发现协议/工具/用法变更：audio inputs/outputs、reasoning parameters、response item ID prefixes、realtime V3、multi-agent V2、memories/paginated history。请评估对 ccx Responses 渠道的影响。

## [x] Codex rust-v0.146.0 上游协议/工具变更评估（2026-08-01 完成）

发现协议/工具/用法变更：Responses item ID 强制分配、自定义 Provider 独立 Web Search、Responses Lite Code Mode 元数据。评估结论见下，四项均为**观察项，当前无需改动代码**。

### 1. Responses item ID 强制分配（#34645）

上游行为：`features.item_ids` 退化为 no-op，客户端为所有 item 无条件分配 ID（含流式、fork 历史、compaction 结果、非 OpenAI provider）。发往上游前 `prepare_response_items_for_request` 只清掉**不带前缀**的 ID（`is_prefixed` = 存在非空 `prefix_suffix` 分隔），带前缀 ID（`msg_x` / `fc_x` / `ctc_x`）一律保留。此前 `store=false` 且 feature 关闭时会清掉全部 ID，现在不再清。

CCX 现状（已实测验证）：
- 透传分支 `normalizeResponsesInputForPassthrough` 不删 `input[].id`，`msg_`/`fc_`/`function_call_output` 的 ID 原样转发（实测确认）。
- 非透传分支 `parseResponsesInput` → `responsesItemFromMap` 读取 `id` 到 `ResponsesItem.ID`，转 Chat/Claude/Gemini 时用 `call_id` 而非 `id`，不受影响。
- 响应侧 `ResponsesPassthroughConverter.FromProviderResponse` 保留 `itemMap["id"]`；`chat_to_responses` / `claude_to_responses` 自造 `msg_`/`rs_`/`fc_`/`ts_`/`ctc_` 前缀 ID，格式与上游 `is_prefixed` 约定一致。

潜在影响（低，均为观察项）：
- 第三方 Responses 镜像若对 `input[].id` 做严格校验，可能因客户端现在总带 ID 而报 400。命中后走既有 `ChannelCompatCache` 三态自学习加 trait，不要加静态 bool 开关。
- `EstimateResponsesRequestTokens` 不计 `item.ID`，历史变长后估算会系统性略低（每 item 约 3~6 token）。当前上下文硬约束留有余量，暂不调整。
- `NormalizeResponsesItem` 对 legacy `tool_call` 走 `firstNonEmpty(item.CallID, item.ID)`，当 `call_id` 缺失而 `id` 存在时会把 item ID 当 call ID 用（实测：`ToolUse.ID=call_real` + `id=ctc_x` 时得到 `ctc_x`）。仅影响 legacy `tool_*` 兼容输入路径，Codex 0.146 发的是 `function_call`（带 `call_id`），不触发；如后续出现工具结果配对错乱再修 `firstNonEmpty` 顺序。

### 2. 自定义 Provider 独立 Web Search（#34846）

上游行为：新增 `supports_standalone_web_search` provider 配置（默认 false），开启后 Codex 启用独立 `web.run` 工具，把搜索请求发到自定义 provider 的 `/v1/alpha/search`（官方为 `/api/codex/alpha/search`），复用该 provider 的鉴权。

CCX 现状：默认 false 且需用户在 `config.toml` 的 provider 块显式开启，Desktop 生成的 `[model_providers.ccx]` 不含该字段，因此**默认不触发**。CCX 未注册 `/v1/alpha/search` 路由；`isAPIPath` 覆盖 `/v1` 前缀，未知路径由 `NoRoute` 返回 JSON 404 而非 SPA 兜底，行为可预期。tools 侧 `web_search` 已在 `codex_tools.go` 与 `shouldDropResponsesToolObject` 中有处理路径。

结论：不主动开启即无影响。若将来要支持，需要新增 `/v1/alpha/search` 代理入口并决定转发目标，属独立特性而非兼容性修复。

### 3. Responses Lite Code Mode 元数据（#35271 / #35364）

上游行为：Lite turn metadata 新增 `code_mode_tool_names`（保留键，禁止客户端覆盖，且不暴露给外部 MCP）。#35364 又把它从 `x-codex-turn-metadata` 直接头中移除，只保留在 `client_metadata["x-codex-turn-metadata"]` 里，避免头无界增长。

CCX 现状：HTTP 透传分支不读也不删 `client_metadata`，整体转发；WebSocket 分支 `normalizeWebSocketResponseCreatePayload` 显式 `delete(req, "client_metadata")`。`ExtractAgentContext` 只读 `x-openai-subagent` / `x-codex-parent-thread-id` 两个键，对新增键无感。

结论：无需改动。仅 WebSocket 桥接模式下 subagent 识别本就丢 `client_metadata`，是既有行为，与本次变更无关。

### 4. Realtime session headers（#34681）

上游行为：realtime WebSocket/WebRTC 会话建立时附加 Codex `session-id` / `thread-id` 头。

CCX 现状：不代理 realtime 链路（`/v1/responses` 的 WS 是 Responses over WebSocket，非 realtime）。

结论：无影响。

---

## [ ] 英伟达渠道导入

可能模型太多响应太慢导致预检时间过长，或者需要代理？
