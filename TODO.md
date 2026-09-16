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

当前状态（2026-09-16 核对）：模型注册/窗口/基准已落地（luna/sol/terra + Bedrock，context 1.05M）。仍待办如下。

2.openai新增了"OpenAI-Beta": “{client_header:OpenAI-Beta}” 这个，来传输子代理相关信息，这个也需要调整
3.工具调用传参之类的都需要优化
4.提醒下同行，oai在最新版本codex里面加入了设备验证相关信息，可能会封pro号

---

## [ ] 在codex里面使用imagegen使用上游的文生图

当前状态（2026-09-16 核对）：Images 渠道已有 `ConvertImageURLToB64JSON`（百炼 URL → OpenAI `b64_json`），管理界面可开，默认关闭。Codex 自带 imagegen 技能走 `/v1/images` 或 Responses `image_generation` 打到百炼的端到端通路仍待验证。

百炼生图是生成url, 和 openai生成base64不一样，比如我在codex介入ccx, ccx配的是百炼，然后在codex里面用 codex自带技能 imagegen 能调用百炼生图模型 成功生图
https://github.com/QuantumNous/new-api/issues/5513

---

## [x] 火山方舟团队版套餐自动探测与用量查询（2026-08-30 后端落地）

当前状态：~~火山套餐绑定与用量刷新仅支持个人版~~ 后端已落地多套餐桶。数据模型选型为**多桶**：`VolcengineAccessKeyPair` 新增 `Plans []VolcenginePlanBucket`（product/edition/seatId/tier/status/usage/error 各桶独立），旧 `Plan`/`PlanTier`/`PlanStatus`/`Usage` 继续指向选定的主桶，模型清单、稀疏 L2 预算、恢复等既有消费链无感兼容；团队版结果不覆盖个人版主桶字段。

实现要点（`autopilot/volcengine_coding_plan.go` + `config/config_accounts.go` + `handlers_auto_managed.go`）：

- `DetectPlans` 四桶探测：personal×2 走 `GetPersonalPlan`（鉴权/网络错误上抛、404 静默，语义不变）；team×2 走 `GetSeatInfo(Scene)`（Agent 用 `agent_plan_enterprise`、Coding 用空串），席位未绑定时静默不出桶、探测失败记桶级 `Error` 不阻断其它桶。
- 主桶选择 `pickVolcenginePrimaryBucket`：hint 匹配（personal 优先于 team）→ personal 唯一/唯一 Running → team 同理 → 消歧错误（保持既有报错文案与行为）。
- 团队版用量：Agent 走 `GetSeatAFPUsage(SeatIDs)`、Coding 走 `GetSeatInfoUsage(SeatID, Scene="")`；响应契约未经真机验证（ark-cli 未开源 CLI 本体），按文档语义做**多字段路径宽松解析**（SeatID 兼容 `Result.SeatID/SeatId/Seat.*/Data.List[]`；AFP 窗口兼容 Result 顶层与 `Seats/Datas[]` 数组按席位匹配），字段路径集中在响应类型定义处，真机验证后如需调整只动一处。
- 绑定端点与手动刷新端点改为逐桶查询（`FetchBucketsUsage`，单桶失败记 `Usage.Error`）+ 双写（主桶字段 + `Plans` 数组）；旧数据（无 `Plans`）自动退回单桶查询路径。管理 DTO 新增 `volcenginePlanBuckets` 回显（不含 AK/SK）。
- 团队版模型清单**未接入**：`ListArk{Agent,Coding}PlanModel` 是套餐线级清单，团队版席位可见清单无公开契约，不猜测；模型发现仍由主桶驱动（现状）。

**关键提交：** 本次后端落地提交；测试覆盖四桶组合、无席位静默、personal+team 并存（主桶 personal 优先 + hint 选中 team）、`GetSeatAFPUsage` 顶层/数组两形态、`AccessDenied` 桶级隔离、多桶持久化不覆盖主桶。

**遗留：** ~~① 前端管理界面展示团队版桶~~ ✅ 已实现（渠道编辑器认证区「其他套餐」区块，`ed472970`）；② 真机验证团队版 API 响应字段路径后修正宽松解析；③ 同产品双 edition 的推理 Key 关联消歧 UI（当前 hint 与 personal 优先策略覆盖常见场景）。

~~当前状态：火山套餐绑定与用量刷新仅支持个人版 `Agent Plan` / `Coding Plan`。其中个人版分别使用 `GetAFPUsage` 和 `GetCodingPlanUsage`；`cd559c8a` 已修正个人版 Coding Plan 的 OpenAPI 地址、签名和百分比响应解析。团队版暂未纳入套餐识别、持久化和管理界面展示。~~

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

## [x] Codex rust-v0.145.0 上游协议/工具变更评估（2026-08-01 完成）

发现协议/工具/用法变更：audio inputs/outputs、reasoning parameters、response item ID prefixes、realtime V3、multi-agent V2、memories/paginated history。评估结论如下：无需要立即实施的改动；音频估算与转换路径列为后续观察项。

### 1. Audio input/output（#33923 / #33932 / #34080 / #34385）

上游 Responses wire format 使用 `{"type":"input_audio","audio_url":"data:audio/wav;base64,..."}`，支持本地 `wav/mp3/m4a/webm/ogg` 转 data URL，并校验格式、Base64 和 50 MiB 上限。音频 output 也主要属于 Codex app-server / dynamic tools / realtime 能力。

CCX 现状：
- Responses 透传分支保留未知 `input` item 和 content block，可将 `input_audio` 原样转给原生支持音频的 Responses 上游。
- Responses 转 Chat 分支明确记录并丢弃 `input_audio`/`audio`，因为 Chat Completions 目标协议不支持音频输入；这是当前显式兼容行为，不应静默宣称音频可用。
- `StreamSynthesizer` 已识别 `response.audio.delta` 和 `response.audio_transcript.delta`，但 `ResponsesItem` 没有专用音频字段，原生 Responses 透传仍是主要支持路径。
- 请求体全局上限默认 50 MiB，覆盖请求级大小保护。

已修复（2026-08-01）：媒体剥离泛化为图片+音频+附件三类。音频按真实时长 × 32 tokens/s 估算（WAV/FLAC 精确，MP3/M4A/WebM 头解析 + 码率回退），PDF 等附件固定保守值，1MiB payload 从约 391K tokens 降到几百~4K。见 `internal/utils/audio_tokens.go` 与 `image_tokens_gjson.go` 的 `mediaPayloadFromBlock`。后续若要 per-model 精确率，可在 `UpstreamModelCapability` 加 `audioInputTokensPerSecond` 数值能力。

结论：原生 Responses 透传可以保留 `input_audio`；转 Chat 路径会明确丢弃音频，因为目标协议不支持。~~发现一个实际观察项：1 MiB 音频 Base64 被估算为约 **391K tokens**，可能导致 SmartRouter 误判上下文超限~~ 已修复（2026-08-01）：音频/附件 token 估算重写为按真实时长 × 32 tokens/s，详见上方 0.145 评估第 1 节。

### 2. Reasoning parameters（#32206 / #32290）

上游 0.145 开始所有 Responses 请求都发送 reasoning 参数，并按最终模型能力决定是否发送 summary；旧的 `supports_reasoning_summaries` 配置覆盖被移除。CCX 不依赖该客户端配置：
- 透传 Responses 请求保留客户端原始 `reasoning`，并按渠道 `ReasoningParamStyle`、`ReasoningMapping` 和模型映射改写 effort。
- `NormalizeReasoningObjectForUpstream` 对 MiMo 做渠道专属 effort 归一化。
- 非透传路径也复用同一套 effort 映射，且已有 `reasoning_summary_*` 事件处理。

结论：CCX 不需要跟随 Codex 的客户端 capability 字段变更；自定义 Responses 上游是否接受 `reasoning.summary` 仍由既有渠道配置和兼容性学习处理。没有证据表明应全局强制注入 `reasoning.encrypted_content`，因此不改默认请求。

### 3. Response item ID 前缀（#32312）

上游开始使用 `ResponseItemId` 和 UUIDv7 后缀，发往 HTTP/WS 上游时省略空 ID 与无前缀 legacy ID；0.146 又将分配扩展为所有 item。CCX 当前已满足兼容要求：
- 透传路径保留 `input[].id`；非透传路径读取 ID，但工具关联优先使用 `call_id`。
- CCX 生成的 `msg_`、`rs_`、`fc_`、`ts_`、`ctc_` 等 ID 都带前缀。
- Responses 响应解析保留上游 output item ID。

结论：与 0.146 评估相同，无需改动。legacy `tool_call` 在 `call_id` 缺失时会回退到 item ID，属于既有兼容边界，暂不扩大修复范围。

### 4. Realtime V3（#33261 / #33856 / #33893 / #33903）

上游新增 Frameless Bidi realtime V3、`delegation.*` 事件、音频/转录/交接和 session world state。CCX 没有 realtime `/live`、`thread/realtime/start` 或 WebRTC 代理入口；`/v1/responses` WebSocket 是 Responses over WebSocket，不是 Codex realtime V3。

结论：无影响。除非未来明确新增 realtime 产品能力，否则不应在 Responses handler 中猜测兼容。

### 5. Multi-agent V2（#32749 / #33550 / #33631 / #33656 / #34383）

上游变化集中在 Codex app-server：`agents.enabled`、子 Agent 模型/effort override、并发上限、角色恢复和稳定性标记。CCX 只从 `client_metadata` 提取 subagent 角色及 parent thread 做路由观测，不实现 Codex app-server 的 `spawn_agent` 协议。

结论：不会改变 CCX 的 Responses 请求/响应格式，也不需要新增路由或配置字段。既有 `client_metadata` 透传/WS 桥接差异保持不变。

### 6. Paginated history / memories（#32234 / #33364 / #33432 / #34386）

上游新增独立 SQLite 分页历史、`historyMode: "paginated"`、cursor、继承 rollout 前缀及 memories reconciliation，均为 Codex 客户端 / app-server 本地存储能力。CCX 的 `previous_response_id` 仍由自身 session manager 维护，不理解 Codex app-server 的 thread history cursor 或 memory 数据库。

结论：无影响。不要把 Codex 本地分页 history 字段或 memories metadata 映射进 CCX 的 Responses session，除非未来收到明确 wire-level 请求样本。

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

## [x] Claude Code v2.1.251 上游协议/工具变更评估（2026-08-30 完成）

发现协议/工具/用法变更（2.1.242→2.1.251）。评估结论：第 1、2 项无需改动代码，第 3 项存在两个 tool_use id 兜底缺口并已修复。

### 1. effort × thinking 约束（Opus 5）——无需改动

CCX 所有自身构造路径都不会产生 `thinking disabled + effort xhigh/max` 非法组合：

- `applyClaudeThinkingEffort`（`providers/claude.go:85`，messages 渠道配置了 ReasoningMapping 时介入）：effort 为 off/none → 写纯 `{"type":"disabled"}` 并删除 `reasoning`/`reasoning_effort`/`output_config.effort`；effort 生效 → 强制 `type:"enabled" + effort`。两种输出均合法。注意渠道级 effort 语义是覆盖：客户端 thinking disabled 会被渠道映射改写为 enabled+effort，属既有设计而非本次上游变更问题。
- `ApplyReasoningParamStyle` 的 thinking style（`config/config_utils.go:441`，Responses 上游）：同构逻辑，干净。
- Responses→Claude 桥接（`converters/claude_converter.go`）不构造顶层 thinking；场景预设路由只按 effort 维度选路，不改写请求体。
- 残留暴露面：渠道未配 ReasoningMapping 时客户端请求原样透传——仅**旧版** Claude Code（<2.1.251）+ Opus 5 + thinking disabled + xhigh/max 会 400。新版客户端已自愈；CCX 侧由 ChannelCompatCache 失败学习 + failover 兜住重复失败。按「无真实流量证据不加静态行为」原则不加全局护栏；如后续真实流量命中该 400，在 `applyClaudeThinkingEffort` 补 xhigh/max→high 降级即可（MiMo 的 `NormalizeReasoningEffortForUpstream` 已有同款收敛模板，`config_utils.go:399`）。

### 2. thinking-only 轮次的空文本块——无需改动

- CCX 流式 normalizer（`claudeStreamNormalizer`）不会凭空合成空 text block：`ensureTextBlock`（`claude.go:1210`）仅在收到 `text_delta`（有真实文本）时打开文本块，`content_block_start` 的 text 块仅转发放上游真实事件。
- 请求侧已有条件清洗：`stripEmptyTextBlocksFromBody`（`claude.go:671`）在渠道开启 `stripEmptyTextBlocks`（渠道级配置 / ChannelCompatCache 自学习 trait，兼容诊断端点检测推荐，DeepSeek 严格端点默认开启）时剔除空 text block。
- 已知边界：`shouldStripEmptyTextBlock` 仅剔除恰好 type+text 两字段的空块（带 cache_control 的空 text 不剔除），且原生 Anthropic 上游默认不清洗——Anthropic 官方与 Claude Code v2.1.251 均已自身修复不再产生该形态，CCX 无需跟随。

### 3. 第三方 Anthropic 兼容端点的 tool_use id——修复两个兜底缺口

CCX 五个 tool_use 生成点核查：三处本就安全——`claudeStreamNormalizer.ensureToolBlock`（`claude.go:1224`，合成 `toolu_<ns>_<index>`）、gemini→claude（`gemini.go:441`，合成 `toolu_<n>`）、chat 流式（`openai.go:693`，`acc.ID != ""` 才发射，不会发出无 id 块；上游漏 id 时该工具调用被静默丢弃，属极端异常，暂不扩大修复范围）。两处直接透传上游 ID 无兜底，为空时产出无 id tool_use block → 客户端渲染错误 + 历史回放 400，本次修复：

- 非流式 chat→claude（`openai.go:390`）与 responses→claude（`responses.go:590`）经新增的 `fallbackToolUseID`（`openai.go`，`toolu_<unixnano>` 前缀兜底）合成合法 id；回归测试 `providers/tool_use_id_fallback_test.go` 覆盖「上游漏 id 合成 / 上游有 id 原样保留」两方向。

注：PreModelSwitch/PostModelSwitch hooks、`/effort` per-model 记忆、subagent 流式、spend limit、Bash 沙箱等均为 Claude Code 客户端内部能力，不影响 CCX 代理层协议。

---

## [ ] 英伟达渠道导入

当前状态（2026-09-16 核对）：`builtinProviderTemplates` 仍无 nvidia/NIM 模板；`docs/design/channel-autopilot.md` 标为 P2。Claude Code v2.1.269 新增 `CLAUDE_CODE_GATEWAY_MODEL_DISCOVERY_TIMEOUT_MS`（默认 3s），客户端侧可缓解 `/v1/models` 超时，不替代 CCX 导入模板。

可能模型太多响应太慢导致预检时间过长，或者需要代理？

## [x] Codex token budget 记忆系统适配 + 客户端预算提醒剔除统一机制（2026-09-11 完成）

背景：Codex CLI 引入 token budget 记忆系统（feature 默认关，config.toml `[features] token_budget = true` 手动开启），三层架构：感知层（developer message 裸文本 `You have N tokens left in this context window.` + 旧版 `<token_budget>` 包装 + 本地 `get_context_remaining` 工具）、管理层（本地 `new_context` 工具切换无摘要新窗口）、记忆层（`history.*`/`notes.*` namespace 工具，后端 HTTP 调 `POST {provider base_url}/alpha/history|notes/v2/*`，部分端点带 `x-openai-encrypted-tool-arguments` 头）。源码核实：openai/codex `codex-rs/ext/history-notes/`。

### 统一预算提醒剔除（providers/client_budget_reminders.go）

统一语义：客户端按「自己认知的模型窗口」注入余量提醒，网关改写实际执行模型后数字必然失真 → 剔除；模型语义未变则保留。CC 的 tokens-left 剔除（1c63bbb2 临时处理）转正并接入 Codex，五个接线点：

- 三转换路径（messages→chat/gemini/responses）保持无条件剔（`isClaudeCodeSystemHeader` 合并「身份清单 ∪ 预算清单」判定，行为不变）；
- 补缺口 A：Claude 直通 + ModelMapping 命中（`redirectModelInBody` 换模型时剔 system 预算块，未命中零改动保缓存）；
- 补缺口 B：调度器联邦/溢出跨模型改写点（`upstream_failover.go` 按 kind 剥离 messages/responses 两形态）；
- 补缺口 C：Responses 直通 + ModelMapping 命中（`RedirectModelWithMatch` 判定）；converter 分支跨协议无条件剔。

保留物：`<context_window>` 窗口 UUID 标识与 `<context_window_guidance>`（无失真数字、模型靠它调 history 工具）；CC 身份块在原生直通场景保留。

### /v1/alpha/* 记忆层透传端点（handlers/alpha/）

粘 Responses 渠道池，session 粘性三级（X-Channel pin > 推理亲和桶扫描 `PreferredChannelNameForUserSweep` > SelectChannel 无桶亲和自粘）。刻意隔离：指标用独立 identity（serviceType 标签 "Alpha"），不写 RecordSuccess/RecordFailure/MarkKeyAsFailed——中转站普遍不支持 alpha 端点，失败并入推理指标会把渠道打成不健康。上游 404 如实透传（候选耗尽回写最后上游响应），Codex 客户端自行降级。已知取舍：跨端点粘性（推理↔记忆同渠道）是尽力而为——Codex 推理请求 userID 提取链未必等于记忆请求的 `context.session_id`；key 级粘性未做（扩展点：`ResolvePinnedAPIKey` 反查）。

### encrypted_function_args 全链路（types/converters/token_counter）

Codex namespace 工具的 function_call 可携带分段加密参数：`ResponsesItem` 加字段 + 解析/响应侧枚举 + passthrough 天然透传 + converter 密文丢弃但空 arguments 补 `"{}"` 占位（DeepSeek 拒收空串）+ token 保守计数 + 日志截断白名单。

明确不做：不改写 `get_context_remaining` 的 function_call_output（客户端本地计算，改写风险大于收益）；不动 RemoteCompactionV2 兼容路径与 thinkingcache reasoning encrypted_content。

---

> **上游版本变更**

## [x] Claude Code v2.1.273 上游协议/工具变更评估（2026-09-16 完成）

发现协议/工具/用法变更（2.1.263→2.1.273）。评估结论：第 1 项 hint 头按透传兼容、无需改代码；第 2 项非流式 Content-Type 已由 CCX 自身保证 JSON；第 3 项均为客户端侧，不影响 CCX 透传。

### 1. 网关 hint 头（v2.1.273）——无需改动

上游新增 opt-in 请求头（`CLAUDE_CODE_GATEWAY_HINT_HEADERS=1`）：`x-claude-code-request-class` / `x-claude-code-agent-type` / `x-claude-code-prev-tool-durations` / `x-claude-code-compaction` / `x-claude-code-context-compacted`。

CCX 现状：
- `PrepareUpstreamHeaders`（`utils/headers.go`）Clone 客户端头后只删除代理/内部路由头（`x-proxy-key`、`X-Forwarded-*`、`X-Task-Domain` / `X-Routing-Scenario` / `X-Cost-Preference`、`Accept-Encoding`），**不剥离** `x-claude-code-*`。
- Messages / Chat / Responses / Gemini 构造路径均走该函数，客户端 opt-in 后 hint 头会原样到达 Anthropic 兼容上游。
- `ExtractAgentContext` 仍用 `X-Claude-Code-Agent-Id` + 启发式识别 subagent，不消费新 hint 头；路由不依赖这些头。

结论：协议兼容无需改动。第三方 Anthropic 镜像若因未知头 400，走既有 `ChannelCompatCache` 三态自学习，不加静态剥离开关。可选观察项：日后可用 `x-claude-code-agent-type` 替代启发式 subagent 识别（当前不强制）。

### 2. 非流式 text/plain（v2.1.271）——无需改动

上游客户端修复：LLM gateway 非流式 `text/plain` 不再被判 malformed。这是**客户端容错**，不是要求网关改 Content-Type。

CCX 写回：
- Messages 非流式 `handleNormalResponse`：`json.Unmarshal` 成功后 `c.JSON(200, claudeResp)`，恒为 `application/json`；上游 body 非 JSON（含 text/plain/HTML）走 `ErrInvalidResponseBody` failover，不把畸形体转给客户端。
- Chat 成功路径 `c.Data(..., "application/json", ...)`；Responses `c.JSON(200, responsesResp)`。
- `ForwardContentType` 仅用于 Gemini/Images 等透传分支，Claude Code Messages 不走该路径。

结论：CCX 作为 Claude Code 的 Messages/Chat/Responses 网关不会发出成功路径的 `text/plain`。无需改动。

### 3. maxEffortLevel / task-tracking 白名单 / Artifact schema 400——无需改动

- v2.1.267 `maxEffortLevel`：客户端在发往网关前封顶 effort。CCX `applyClaudeThinkingEffort` 按渠道 ReasoningMapping 覆盖或透传已封顶值；不实现组织级 cap。
- v2.1.268 task-tracking tools 模型白名单：客户端按模型决定是否投放 TaskCreate/TodoWrite。CCX 不注入这些工具；映射到不支持模型后的 400 由既有工具白名单/兼容性学习处理。
- v2.1.265 Artifact input schema 正则导致第三方 Anthropic 兼容端点整轮 400，2.1.268 已在客户端修复。CCX 不构造 Artifact schema。

注：v2.1.269 `CLAUDE_CODE_GATEWAY_MODEL_DISCOVERY_TIMEOUT_MS`（默认 3s）是客户端 `/v1/models` 超时，与「英伟达渠道导入」预检过慢观察项相关，非协议变更。PreModelSwitch hooks、Remote Control、Artifact 发布、MCP 重连等均为客户端内部能力，不影响 CCX 代理层协议。
