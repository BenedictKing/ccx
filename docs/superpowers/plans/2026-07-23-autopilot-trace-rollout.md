# Autopilot Trace、灰度发布与分层测试实施计划

> 状态：提案。本文把 OpenSquilla 可借鉴的方法落到 CCX 既有 Autopilot 架构，不迁移其 Agent Runtime、MCP、记忆、工具沙箱或本地 ML Router。

**目标：** 将 `RoutingDecisionTrace` 升级为可脱敏回放、可跨重启追溯的决策事实源；以 shadow 到 active 的受控发布流程上线自动路由；用分层测试锁定隐私、回退、流式协议与真实上游调用的边界。

**范围：** `backend-go/internal/autopilot/`、`backend-go/internal/scheduler/`、`backend-go/internal/metrics/`、关联管理 API 与 Autopilot 驾驶舱。

**非目标：** 重写调度器、引入第二套决策记录模型、保存 prompt/密钥/敏感 header/multipart、让 Autopilot 覆盖用户显式路由意图，或在本轮实现新的模型推理服务。

## 目录

1. [背景、目标与设计边界](#1-背景目标与设计边界)
2. [现有实现与职责边界](#2-现有实现与职责边界)
3. [Trace v2 契约与只读回放](#3-trace-v2-契约与只读回放)
4. [持久化、隐私与保留策略](#4-持久化隐私与保留策略)
5. [Shadow 到 Active 的发布状态机](#5-shadow-到-active-的发布状态机)
6. [分层测试策略](#6-分层测试策略)
7. [分任务实施步骤](#7-分任务实施步骤)
8. [验证、提交范围与风险控制](#8-验证提交范围与风险控制)

## 1. 背景、目标与设计边界

OpenSquilla 对 CCX 的价值不在可直接复用的 Agent Runtime 代码，而在三项工程方法：将决策解释做成隐私受限、版本化、可回放的契约；先 shadow 比较真实结果再放量；按故障域分层测试。CCX 已有成熟的代理、Scheduler、Failover 和 Autopilot 边界，因此应把这些方法嵌入现有链路，而不是引入平行运行时。

### 1.1 本轮交付目标

1. `RoutingDecisionTrace` 成为 Autopilot 的唯一决策事实源：一个 trace 可以解释建议、Scheduler 最终选择、endpoint 尝试摘要和请求终态。
2. Trace v2 可在服务重启后完整读取，API 与管理界面只提供只读、脱敏视图；观测写入、查询和清理任何失败均不得影响代理请求。
3. 自动路由以 `off -> shadow -> assist -> auto -> active` 受控发布；在持续 SLO 回归时自动降级，显式用户意图和安全硬约束始终优先。
4. 测试将纯决策、调度与回退、SQLite 生命周期、流式转换和真实上游 smoke 分开，避免昂贵或易波动的验证掩盖基础逻辑缺陷。

### 1.2 验收标准

| 领域 | 完成定义 |
|---|---|
| 可观测性 | 每条持久化 trace 都带 schema 版本、稳定 `traceUid`、策略指纹和请求关联 ID，且能还原其决策与终态。 |
| 隐私 | 数据库、日志、API 响应和测试夹具均不得出现明文 API Key、Authorization、prompt 全文或 multipart 内容。 |
| 可用性 | Trace 的序列化、SQLite 写入、过期清理、详情查询失败时请求继续按现有 Scheduler/Failover 完成。 |
| 发布安全 | shadow 有可比较的建议/实际结果；进入 auto 前满足现有 readiness 门槛；连续三个合格样本窗口回归时自动降级。 |
| 可验证性 | 关键契约存在确定性测试；真实上游测试默认不运行，只有显式 opt-in 才发送网络请求。 |

### 1.3 不变量与非目标

- 不新增 `RoutingDecisionRecord` 或其他平行事实源；v2 直接演进 `RoutingDecisionTrace`。
- 不改变 `SelectionTrace` 和 `ChannelLog` 的所有权，只补充可关联字段。
- `X-Channel`、manual override、promotion、session affinity、显式 `modelMapping` 及协议/上下文/能力约束优先于 Autopilot 排序。
- 不记录可复原用户内容的请求摘录；`PromptHash` 仅作内部关联，管理 API 不返回它。
- 不把 trace 用作控制面输入、重放真实请求或自动重试工具；回放仅解释已发生的决策。
- 不以本轮计划替换现有 Scheduler 的候选筛选和 endpoint failover 实现。

## 2. 现有实现与职责边界

### 2.1 保留三个记录层，而非合并它们

| 记录层 | 当前事实 | v2 后职责 | 不承担的职责 |
|---|---|---|---|
| `autopilot.RoutingDecisionTrace` | 候选评分、shadow/actual、终态和内存/SQLite 抽样已存在。 | Autopilot 对“为何建议/如何影响候选/最终发生什么”的唯一事实源。 | 不替代 Scheduler 硬约束证据，也不保存每个 endpoint 请求细节。 |
| `scheduler.SelectionTrace` | 记录过滤阶段、跳过原因和 Scheduler 的最终渠道选择。 | 证明协议、上下文、能力、显式控制等硬约束如何裁决。 | 不重复 Autopilot 打分或持久化整个请求回放。 |
| `metrics.ChannelLog` | 每次真实 upstream 尝试已有 request 生命周期、流式时序、选路摘要。 | 承载 endpoint/base URL/key 掩码级尝试与失败信息，并链接所属 trace。 | 不变成候选评分或策略配置快照。 |

请求链路应保持为：请求画像 -> `RoutingDecisionTrace`（Autopilot 计划）-> `SelectionTrace`（调度裁决）-> `ChannelLog`（每次真实尝试）-> trace 请求终态。关联而非复制是可查询性和存储量之间的边界。

### 2.2 已有能力

- `TraceStore` 已具备内存环形缓存、SQLite 初始化、启动时加载、`RecordOutcome`、shadow/actual mismatch 记录及 API 输出脱敏。
- `RoutingDecisionTrace` 已含候选分数、过滤/排序说明、manual intent/advisor 关联、推荐与实际渠道、fallback 和请求终态等语义字段。
- `CandidateFilterFor` 的真实路径已产生 trace 并可回填实际渠道；`RoutingWindowSummary` 已按 15 分钟窗口计算成功率、fallback、fail-open 与延迟分布。
- `SelectionTrace` 已为现有 Scheduler 诊断、请求日志摘要和测试断言服务，不能被 Autopilot 结构替换。
- `ChannelLog` 已记录流式生命周期和 `SelectionTraceSummary`，适合作为从某次 upstream 失败跳回总决策的起点。

### 2.3 需要消除的缺口

1. 当前 SQLite 表只保存部分 trace 顶层字段和 `candidates_json`；`SortReasons`、全局过滤原因、manual/advisor 关联、成本与置信度等字段无法保证跨重启完整还原。
2. `BuildPlan` 只返回 dry-run 计划，尚未按设计意图写入 trace，因此“所有真实或 dry-run 路由都有 trace”并不成立。
3. `ChannelLog` 没有 `autopilotTraceUid`，一次 upstream 尝试无法直接跳转到其决策、Scheduler 证据和请求终态。
4. 现有 `1/10` 抽样只确保 mismatch 必落盘；失败、fail-open、Manual Intent、Advisor 等高价值样本仍可能在重启后消失。
5. Trace API 只有列表与统计；没有按 UID 的只读详情，也没有规定可回放的字段版本、详细记录 TTL 或清理任务。

这些缺口只在 Autopilot 观测边界内修复。Scheduler 的选择规则和既有 ChannelLog 生命周期保持原样，必要时仅增加 ID 传播和摘要引用。

## 3. Trace v2 契约与只读回放

Trace schema 版本独立于 SQLite 的 `PRAGMA user_version`：前者描述一条决策记录的 JSON 语义，后者只管理数据库迁移顺序。新增 v2 时保留 v1 读取适配，写入一律使用 v2；未知的未来 trace 版本只能只读展示原始安全摘要，不能被本服务改写。

### 3.1 v2 顶层契约

`RoutingDecisionTrace` 增加或规范以下字段。持久化实现使用显式 DTO，不直接把运行时内部结构 JSON 化，以免后续新增字段意外落盘。

| 字段组 | 字段 | 规则 |
|---|---|---|
| 身份与版本 | `traceUid`、`schemaVersion=2`、`createdAt` | UID 在首次决策前生成且全链路不变；新生成算法必须使用不可预测的碰撞安全随机源，而非仅依赖时间。 |
| 请求关联 | `requestCorrelationId`、`source` | 真实请求在入站时生成一次逻辑请求关联 ID；dry-run 使用服务端生成的调用 ID。不得信任或回显客户端提供的关联 ID。 |
| 策略快照 | `releaseId`、`policyFingerprint`、`mode`、`policyRevision`（如已有配置版本） | `releaseId` 标识一次独立发布；指纹由决策语义、权重、启用约束和策略版本的规范化、无密钥表示计算，用于解释当时决策，不保存整个配置或分桶 secret。 |
| Autopilot 计划 | 请求画像摘要、候选/分数、全局过滤原因、排序原因、建议渠道、成本及置信度、manual/advisor 关联 | 保留解释性事实，候选中的渠道使用稳定 `ChannelUID`；明确区分“被 Autopilot 推荐”和“实际尝试”。 |
| Scheduler 裁决 | 阶段计数、跳过原因代码、最终渠道 UID/原因 | 在选择完成后回填为规范化 `schedulerDecision` 摘要；不把 Scheduler 的整个运行时对象当作持久化契约。 |
| 尝试与终态 | endpoint 尝试摘要、实际渠道、match、fallback/fail-open、请求终态与延迟 | 每次尝试记录顺序、渠道 UID、已脱敏 endpoint 标识、结果分类、HTTP 状态码和时延；终态只由请求完成路径写入。 |

v2 用 `comparisonStatus` 枚举（`matched`、`mismatched`、`uncompared`）表达比较结果，不能继续用裸 `bool` 承担三态语义。只有 shadow 建议与可比较的实际渠道都存在时才计算；v1 的 `Match` 仅在两者齐全时适配到 matched/mismatched，否则一律是 `uncompared`。列表、窗口和统计必须据此排除未比较记录，避免把缺失数据误算为 mismatch。

### 3.2 脱敏与可回放边界

可回放是“重新解释已记录的决策”，不是重新执行请求。详情必须足以回答候选为何被筛除、Autopilot 推荐什么、Scheduler 为何选择、实际做过哪些尝试及其终态，但不得带入用户载荷。

- 允许：`ChannelUID`、模型名、任务类别、能力/过滤原因代码、稳定且不含地址的 `endpointLabel`、状态码、时延桶或精确时延、策略指纹。
- 禁止：API Key、`Authorization`、Cookie、完整 URL query、prompt/消息全文、工具参数、响应正文、multipart 名称与内容、原始上游错误 body。
- `PromptHash` 默认既不持久化也不返回；如某个受限内部关联场景确需短期散列，必须单独记录必要性、TTL 和访问边界，且该值仍不得进入列表、详情、导出、日志或浏览器响应。
- endpoint 标识应使用渠道 UID 加已脱敏的稳定端点标签；原始 `BaseURL` 只能继续留在受现有权限保护的 `ChannelLog`，不能复制到 Trace API。v2 不能把 `metricsKey` 或现有 `SanitizeMetricsKey` 的输出直接当标签：它可能保留 URL、host、path 或 query；标签必须由服务器端 endpoint UID/序号派生，不能含原始地址、key 或其拼接片段。
- 每个 API 响应在序列化前运行纯函数脱敏，数据库写入前也运行一次持久化脱敏，形成双重边界；测试必须证明两处均生效。

### 3.3 只读 API 与驾驶舱

保留 `GET /api/autopilot/traces` 和 `GET /api/autopilot/traces/stats`，并新增 `GET /api/autopilot/traces/:traceUid`。所有接口沿用现有 Autopilot 管理路由的鉴权，不新增代理端点，也不提供删除、导出、重放或策略修改操作。

| 接口 | 返回内容 | 约束 |
|---|---|---|
| 列表 | `TraceSummary`：UID、时间、版本、短 `releaseId`、cohort、mode、请求类别、比较状态、推荐/实际渠道、终态与轻量异常标记。 | 按 `(createdAt, traceUid)` 游标分页，默认和最大页大小固定；支持 release/cohort/mode 服务端过滤，不含候选、策略或 endpoint 尝试。 |
| 详情 | `TraceDetail`：v2 的安全决策、规范化 Scheduler 摘要、endpoint 尝试摘要及终态。 | 仅按 UID 查询；记录不存在、已过期或未被持久化时返回明确的 `404`，不得从内存泄露未脱敏对象。 |
| 统计 | 以持久化窗口聚合为准，返回 `compared`、`matched`、`mismatch`、`uncompared`、失败和 fail-open 数。 | 按当前或明确指定的 release/policy/cohort 聚合；比较计数由全量窗口列累加，不以详细 trace 抽样结果推导发布门槛。 |

`ChannelLog` 新增 `requestCorrelationId` 与 `autopilotTraceUid`（JSON 输出字段）。前者在入站请求只生成一次，后者由 Autopilot trace 生成；现有 `requestId` 继续作为每次 endpoint 尝试自己的日志 ID。trace 详情按 `requestCorrelationId` 汇集安全尝试摘要，频道日志页面可跳转到相应 Autopilot trace。关联缺失应显示“无 Autopilot trace”，不能阻断日志写入或页面显示。

前端将 `AutopilotTraceTable` 改为摘要行：点击后由 `AutopilotView` 请求详情并在单一只读详情抽屉中展示“决策 -> Scheduler 裁决 -> upstream 尝试 -> 终态”时间线。请求中显示固定尺寸 loading 状态，`404` 显示记录已过期/未采样，网络错误可重试；页面绝不缓存或渲染未脱敏的后端对象。现有本地 mismatch 开关改为服务端过滤，并提供当前 release/历史 release 的安全筛选，以确保分页和统计口径一致。

管理 API 的观测降级必须有稳定语义：仅“UID 不存在、已过期或正常抽样未持久化”返回 `404`；SQLite busy、关闭、查询超时等存储暂不可用返回不含底层错误的 `503`（可重试）；单条已持久化记录的安全 DTO 损坏返回固定错误码的 `500`，列表跳过坏行并显式标记 `partial=true`。查询使用有限 deadline（首版 2 秒），不得以未脱敏内存对象或其他日志回补；所有错误响应只含稳定机器码和安全文案。

### 3.4 回填顺序

1. 在请求画像和 Autopilot 计划产生时捕获不可变 `RoutingReleaseSnapshot`（release、策略、分桶、目标/实际 mode），创建 v2 trace 并把 UID/快照写入 request context；shadow 和 dry-run 都走这一步。
2. Scheduler 返回选择或 `SelectionTraceError` 时回填规范化调度摘要，保留失败路径已产生的硬约束证据。
3. 每个 endpoint 尝试创建/更新 `ChannelLog` 时复制 UID，并向 `TraceStore` 附加安全尝试摘要；attempt 写入失败只记内部告警。
4. 请求最终完成、耗尽、取消或 fail-open 时一次性回填终态和窗口聚合。重复回调必须幂等，先到的非终态尝试不可覆盖终态。

此顺序让一个 trace 在请求中途可解释“已知事实”，而在完成后具有稳定终态；任意回填失败都只降低观测完整度，绝不触发额外重试或改变既有请求响应。

热重载只影响之后进入的请求。后续 Scheduler、attempt、终态和窗口回填必须使用该请求入口捕获的同一 `RoutingReleaseSnapshot`，不得在回填时重新读取可变配置、重算 fingerprint 或把在途请求归属到新 release。

### 3.5 关联、尝试与终态的生命周期契约

- 实现前建立并测试入口矩阵：六类代理入口的单渠道/多渠道路径、管理端 dry-run 必须明确是否生成逻辑请求 ID 与 trace；health check、capability test、配置探测等非代理请求也必须明确为“有受限诊断关联”或“刻意不关联”，不能遗漏或复用一次 endpoint 的 `ChannelLog.RequestID`。
- 未采样 trace 从创建起登记在独立的 in-flight 索引，直至外层请求终结；500 条近期环形缓存只服务已完成的浏览体验，不能淘汰仍可能升级为异常持久化的快照。索引需有最大并发数、请求 deadline 后的过期清理和丢弃计数，避免取消/断连造成内存泄漏。
- 每个 endpoint 摘要携带不可预测 `attemptUid`、在 trace 内单调递增的 `attemptSeq` 及 `started/completed` 状态。详情最多保留 32 次尝试或 64 KiB 序列化内容；超限时保留总数、按结果聚合的省略计数和首末安全摘要，绝不无限追加。
- 每次可持久化状态变更在同一锁内递增 `traceRevision`；异步 writer 的 UPSERT 只接受不低于已存 revision 的快照，且对相同 revision 只做幂等合并。这样早到/晚到的队列项不会用局部计划覆盖已完成的 Scheduler、attempt 或终态事实。
- 尝试完成可乱序，但由同一 trace 的受锁状态机按 `attemptUid` 合并；外层 `Finalize` 是唯一可写请求终态和窗口聚合的入口，且只接受第一个有效终态。它从已合并的 attempt 状态一次性计算 `attempt_count`/`attempt_failure_count` 并写入单个窗口增量，不能让 retry 回调重复累计。attempt 失败不能提前终结仍在 failover 的逻辑请求，之后的回调也不能覆写已确认终态。

## 4. 持久化、隐私与保留策略

### 4.1 SQLite v5 到 v6 迁移

在 `ensureSchemaVersion` 中把当前 Autopilot DB 版本从 v5 升至 v6。迁移必须使用现有文件级版本机制，在同一事务内补列、建索引并最后更新 `PRAGMA user_version`；任何一步失败都让 Autopilot 初始化失败，由现有 `main.go` 的 fail-open 分支保留原代理调度。

`autopilot_routing_traces` 保留已有轻量可筛选列，并新增以下列：

| 列 | 目的 |
|---|---|
| `schema_version INTEGER NOT NULL DEFAULT 1` | 区分历史 v1 行与新 v2 行。 |
| `trace_revision INTEGER NOT NULL DEFAULT 0` | 拒绝异步队列中的旧快照覆盖较新的终态事实。 |
| `request_correlation_id TEXT NOT NULL DEFAULT ''` | 与 Trace 和多条 `ChannelLog.RequestCorrelationID` 的服务器端逻辑请求关联键。 |
| `release_id TEXT NOT NULL DEFAULT 'legacy'` | 将详情、窗口和安全事件关联到同一次发布批次。 |
| `policy_fingerprint TEXT NOT NULL DEFAULT ''` | 检索和解释当时的无敏感策略快照。 |
| `persistence_class TEXT NOT NULL DEFAULT 'sampled'` | 标记抽样、异常、manual intent、advisor 或 dry-run 等保留理由。 |
| `details_json TEXT NOT NULL DEFAULT '{}'` | 保存经持久化脱敏后的 v2 详情 DTO。 |

新增 `(request_correlation_id, created_at)`、`(release_id, policy_fingerprint, created_at)`、`(mode, created_at)` 和 `(persistence_class, created_at)` 索引；不为 JSON 内部字段建索引，也不把完整候选数组拆成独立关系表。列表所需的字段继续读顶层列，详情只在按 UID 查询时解析 `details_json`。

全新库的 `initTraceSchema` 直接创建 v2 列；历史库先 `ALTER TABLE`，再由 `ensureCurrentSchemaColumns` 做幂等自愈。v1 行不回填或重写：读取时通过适配器生成尽力而为的 `TraceSummary`，详情明确标示“历史 schema，部分字段不可用”。这样避免启动时全表写放大，也不把不存在的信息伪造成默认事实。

同一 v6 迁移还必须重建 `autopilot_routing_windows` 的聚合键。当前键仅含 `window_start/mode/request_kind/task_class`，会把不同策略、发布批次和对照组混为同一份 readiness 证据；新行增加 `release_id`、`policy_fingerprint`、`cohort`（`treatment`、`control`、`bypass`），主键改为 `(window_start, release_id, policy_fingerprint, cohort, mode, request_kind, task_class)`，并为发布批次/时间建索引。窗口还要新增全量 `compared_count`、`matched_count`、`mismatch_count`、`uncompared_count`，以稳定三态比较口径而非从抽样详情外推。SQLite 不能原地修改主键，迁移需在事务内新建表、复制历史行并以 `legacy` 维度标识后原子换表；历史聚合只用于展示，绝不作为新 release 的晋升或回滚基线。`autopilot_auto_safety_events` 同时增加 `release_id` 与 `policy_fingerprint`，使每次降级可准确复盘其证据来源。

### 4.2 落盘选择与异常提升

正常成功请求可保持 `1/10` 详细 trace 抽样，前提是无偏 `autopilot_routing_windows` 继续对全部请求记录统计。以下记录不受抽样限制，必须在获得足够事实后落盘为 v2：

- shadow 建议与实际渠道不一致；
- 终态失败、请求耗尽、endpoint/channel fallback 或 Autopilot fail-open；
- 命中 `ManualIntentUID` 或 `AdvisorDecisionUID`；
- 由管理端显式发起的 dry-run 诊断。

初始 trace 可以因正常抽样而未入库；当随后出现上述终态或关联时，`RecordOutcome`/回填路径必须从 in-flight 索引提升为持久化记录，而非依赖可能已淘汰的 500 条环形缓存或只执行 `UPDATE`。同一 `traceUid` 的多次回填带单调 `traceRevision`，UPSERT 必须拒绝旧 revision，并不得把已有的完整 `details_json` 覆盖为较早的局部快照。

落盘前调用 `SanitizeForPersistence`，落盘后的读模型再调用 `SanitizeForResponse`。两者分别维护白名单，禁止共用会修改运行时对象的函数。序列化失败时以明确的安全最小详情替代，不得回退到 `fmt` 输出原始对象。

### 4.3 保留、清理与故障隔离

| 数据 | 保留期 | 原因 |
|---|---|---|
| `autopilot_routing_traces` 的详细 trace | 7 天 | 限制诊断数据暴露面与 SQLite 增长；异常样本也遵守同一期限。 |
| `autopilot_routing_windows` | 30 天 | 支持 24 小时 readiness 与 7 天 rollback 基线，且为无敏感聚合。 |
| `autopilot_auto_safety_events` | 30 天 | 保留降级原因，便于复盘发布状态变化。 |
| `ChannelLog` | 沿用现有策略 | 本计划只增加关联 UID，不暗中改变既有日志保留行为。 |

首版将 7 天作为固定默认值，不增加 UI 配置或无限保留开关。若后续确有合规需求，再通过受验证的配置项单独设计；当前先保证行为可预测且便于测试。

`TraceStore` 在初始化后执行一次清理，并在随后写入路径中以原子时间门限至多每 24 小时执行一次。清理按小批次删除过期行，使用已有时间索引，避免长事务和启动时全库扫描。窗口与 safety event 的清理独立于详细 trace，不能因清除详情破坏 auto 的基线计算。

写入、查询、反序列化、清理和统计失败均按以下规则处理：记录不含 payload 的结构化告警和内部计数，返回观测降级结果或空详情，继续当前请求。不得因为 TraceStore 返回错误而改变 Scheduler 候选、增加 retry、修改 HTTP 响应或切换 routing mode。唯一例外是启动期 schema 迁移失败：按现有机制禁用 Autopilot，而不是让半迁移数据库参与决策。

内存环形缓存的 500 条上限继续只服务近期列表体验，不能被当作持久化 SLA。任何 API 详情都必须从已脱敏持久化 DTO 或已脱敏内存副本读取；清理后返回 `404`，不尝试从其他日志、请求缓存或外部上游补建 trace。

### 4.4 写入预算与背压

详细 trace、attempt 摘要和窗口聚合不得在代理请求 goroutine 中进行无 deadline 的 SQLite I/O。`TraceStore` 使用有界异步写入队列和单一批量 writer；单项在入队前截断至 64 KiB，数据库操作 deadline 为 250 ms。队列为请求终态/窗口聚合预留容量，普通抽样详情最先被丢弃；任何队列满、超时或落盘失败都只增加无敏感 `telemetry_dropped`/`telemetry_write_failed` 计数，不阻塞、重试或改变主请求。

终态命令中的 trace UPSERT 与其窗口增量必须在同一 SQLite 事务内提交；任何一步失败都不留下半个窗口增量。readiness/regression 只读取已结束且 writer 已确认提交的窗口；队列存在未完成的终态、丢弃率超限或窗口不连续时视为 telemetry unavailable，不使用可能滞后的数字自动晋升或回滚。服务正常关闭时最多给 writer 1 秒 drain，逾期记录安全丢弃计数并继续关闭。

异常详情和聚合在进程崩溃或持续存储故障时仍可能丢失，因此 readiness 必须把写入丢弃率、窗口连续性和存储健康纳入“telemetry unavailable”判定，拒绝晋升而不是以不完整样本判定安全。实现时为入队时延、队列深度、批量写时延和丢弃数增加内部指标；性能验收为开启观测后代理路径新增 p95 小于 2 ms，超过预算先降采样/禁用详细 trace，再调查数据库，而不是牺牲 fail-open。

## 5. Shadow 到 Active 的发布状态机

### 5.1 配置模型与状态语义

扩展现有 `AutopilotRoutingConfig`，而不是新增另一个发布数据库：增加受校验的 `rolloutPercent`、不可预测的内部 `releaseId`/`rolloutSeed` 与固定的小比例 `controlPercent`，将 `active` 纳入 `AutopilotMode`、配置归一化、管理 API 和前端类型。`RoutingMode` 表示目标状态，单次请求记录实际生效状态；因此 v2 trace 的策略快照补充 `releaseId`、`targetMode`、`effectiveMode`、`rolloutPercent`、cohort 和无敏感分桶来源标识，但不返回 `rolloutSeed`。

```text
off --管理员启用--> shadow --门槛满足--> assist --门槛满足--> auto --100% 稳定--> active
 ^                     |                    |                   |
 |                     +------人工降级-------+--------自动降级----+
 +------------------------ kill switch / 紧急回退 -----------------+
```

| 状态 | 实际请求行为 | 灰度规则 |
|---|---|---|
| `off` | 不调用 Autopilot，不产生真实请求 trace；显式管理端 dry-run 仍可产生受限诊断记录。 | `rolloutPercent=0`。 |
| `shadow` | 计算并记录建议，向 Scheduler 返回原始候选，回填真实选择用于比较。 | `rolloutPercent=0`；是所有未放量请求的安全基线。 |
| `assist` | 只重排候选，不删除候选；不受选中的请求继续以 shadow 行为运行。 | 由 1% 逐步提高至 100%。 |
| `auto` | 在硬约束之后过滤并重排；候选耗尽或内部异常时 fail-open。 | 由 1% 逐步提高；未命中的分桶仍走 shadow。 |
| `active` | 与 `auto` 的路由语义相同，但代表已完成目标 100% 发布并接受持续 SLO 守护。 | `rolloutPercent` 固定 100%；仅从可路由请求留出固定 1% 的 shadow control，不发送双请求。 |

管理员只能逐级晋升，不能从 `off` 直接跳到 `auto/active`。允许随时降级到较低状态，`killSwitch` 和 `AUTOPILOT_KILL_SWITCH` 始终直接生效为 `off`。`rolloutPercent` 仅在 `assist` 与 `auto` 有效，校验范围为 1-100；`shadow/off` 强制为 0，`active` 强制为 100。`controlPercent` 首版固定为 1，仅在 `active` 作用于未受保护的可路由请求；它是观测对照，不是第二个放量比例。

### 5.2 稳定分桶与优先级

灰度分桶先检查用户/安全边界，再决定是否应用目标模式：

1. 全局 kill switch、认证、协议、上下文、vision/tool/reasoning、images/vectors 原生能力和 embedding 维度等硬约束始终先执行。
2. `X-Channel`、manual override、promotion、已存在的 session pin/affinity 与显式 `modelMapping` 是受保护选择；Autopilot 不得过滤、重排或以灰度比例覆盖它们。
3. 对剩余请求，优先使用服务器端 session ID，否则使用内部 request correlation ID，与仅内部保存的 `rolloutSeed` 做稳定哈希分桶；`policyFingerprint` 只作审计与聚合维度，绝不作为分桶输入。不得使用 prompt、API Key 或客户端任意 header；没有持久 session 时仅保证同一逻辑请求的稳定性，不承诺跨请求粘连。
4. 在 `assist/auto` 中，命中 treatment 分桶才以目标模式生效，未命中请求统一执行 `shadow` 并记录比较数据；在 `active` 中，先划出固定 1% control 执行 `shadow`，其余可路由请求执行 `auto` treatment。control 不做双发，也不作用于受保护选择。

Scheduler 仍负责最终能力和运行可用性裁决。即使一个请求命中 auto 分桶，Autopilot 的模型映射、候选过滤和排序也不能越过上面的保护选择或 Scheduler 的硬约束；trace 必须记录 `bypassReason`、保护来源和实际生效模式。

### 5.3 晋升门槛与放量步骤

晋升 API 复用现有 smart-routing 管理路由，但由一个 `ReleaseController` 统一校验状态迁移、比例和 readiness，禁止 handler、SmartRouter、前端各自实现一份规则。每次晋升写入包含操作者、旧/新状态、比例、策略指纹和 readiness 快照的安全事件。

- `shadow -> assist`：至少观察 24 小时、当前 `releaseId + policyFingerprint` 下累计 500 个可比较样本；成功率、fallback、fail-open、p95 延迟均达到现有安全阈值，且没有未解决的关键 mismatch。
- `assist -> auto`：复用 `EvaluateAutoReadiness` 的 24 小时/500 样本门槛：成功率 `>=95%`、fallback `<=10%`、fail-open `<=2%`、近期 p95 不超过安全基线 `1.5x`；telemetry 不可用时拒绝晋升。
- `auto -> active`：要求 auto treatment 已在当前 release 和策略下完成一整段 24 小时观察、至少 500 个请求且最近没有 rollback；聚合查询参数化为 release、策略、cohort 和实际 mode，避免用旧策略或 shadow 样本冒充 auto 证据。

每个状态的灰度比例只按 `1% -> 5% -> 25% -> 50% -> 100%` 递增。两次递增之间至少完成 3 个 15 分钟窗口，每个窗口至少 20 个终态请求且没有 regression；低流量时保持当前比例，不用时间到期代替样本门槛。比例提升是人工确认动作，系统只负责给出 readiness 和建议，不自动扩大生产流量。门槛还须按已纳入此次发布的 `requestKind/taskClass` 分层检查：缺少足够样本的分层保持 shadow 或不纳入本次 release，禁止由全局平均值掩盖单协议/任务类别回归。

### 5.4 Fail-open 与自动降级

Fail-open 分两类记录，避免把业务约束与实现故障混为一谈：

- 合法的 auto 硬约束过滤后没有候选：沿用当前 `auto_failopen_reorder` 语义，只保留 Scheduler 原始候选并进行最小安全重排；标记 `fallbackUsed` 和具体原因。
- 配置、画像、评分、advisor、分桶或 TraceStore 自身异常：直接返回进入 Autopilot 前的原始候选顺序；记录 `routing_error`，不因观测失败再次调用上游。

复用 `EvaluateAutoRegression` 的 15 分钟窗口和 7 天基线：auto 使用同一 release 的 shadow/assist 比较样本；active 使用同一 release、同一策略和同一 `requestKind/taskClass` 的 1% shadow control。连续 3 个已完成窗口、每窗至少 20 个请求且同时满足 regression 条件时触发降级。普通成功率/fallback/p95 回归将 `auto/active -> assist`；fail-open 突增、硬约束异常或策略不可解释时直接 `auto/active -> shadow`。写入 `AutoSafetyEvent` 后禁止自动恢复，必须重新通过晋升门槛。

降级顺序是先设置进程内的强制安全覆盖，再最佳努力持久化配置和事件；配置文件写入失败不能让当前进程继续高风险模式。覆盖状态应在管理 API/readiness 中可见，并在下次人工晋升前保持有效。Trace/window 写入失败仍只影响观测，不改变这个安全覆盖动作。

### 5.5 发布批次、稳定分桶与持续对照

每次从 `off` 开始的新策略发布，或任何会改变候选过滤、评分、模型映射、硬约束语义的配置变更，都创建新的随机 `releaseId` 和 `rolloutSeed`，并强制回到 shadow 重新积累证据。`policyFingerprint` 只包含决策语义，明确排除 `rolloutPercent`、`controlPercent` 和 `rolloutSeed`；同一语义的重新发布仍使用新的 `releaseId`，防止不同批次的样本混合。比例变更不重置 seed，已命中的 session 因而不会被重新洗牌。

`ReleaseController` 为每个入站请求产出不可变 `RoutingReleaseSnapshot`；它是一次决策到终态之间唯一允许读取的 release/policy/cohort 数据源。配置热重载、人工放量和自动降级只替换后续请求的 snapshot，不能修改在途 trace、窗口归属或 safety event 的证据上下文。

所有 readiness、regression、统计和 `AutoSafetyEvent` 都必须携带并按 `releaseId + policyFingerprint + cohort` 查询，且对 treatment 与 control 使用相同的协议/任务类别分层。进入 active 后 control 只执行 shadow 计算和真实既有调度，不复制或延迟用户请求；它不是 A/B 双发。若 control 或最近安全基线因低流量、丢弃率或保留期不足而失效，报告 `baseline_stale` 并禁止进一步晋升/自动恢复；不得继续拿过期的 7 天历史替代当前策略对照。

## 6. 分层测试策略

测试按故障域分层；低层测试不依赖网络，高层测试不替代低层契约。每层都要断言“失败如何退回”，不能只断言成功路径。

| 层级 | 主要对象 | 必须覆盖的边界 |
|---|---|---|
| L1 纯函数 | 脱敏、DTO、hash、分桶、状态迁移、保留分类、mismatch 统计 | 输入为空、字段缺失、未知 schema、未比较、重复终态、异常字符串和非法比例。 |
| L2 组件集成 | SmartRouter、Scheduler `SelectionTrace`、Failover、`ChannelLog` | shadow/assist/auto/dry-run、显式控制优先、候选耗尽、advisor 超时、TraceStore 故障、trace UID 传播。 |
| L3 SQLite 生命周期 | v5->v6 migration、读写、重启、清理 | 旧行兼容、完整 v2 详情还原、抽样后异常提升、坏 JSON、未来 schema、7 天/30 天保留和锁/写入失败。 |
| L4 协议与 HTTP | 管理 API、messages/chat/responses/gemini 流式处理及转换 | SSE 帧顺序、首字节/空闲超时、取消、上游失败重试、详情 404、脱敏响应和无 trace 时的正常响应。 |
| L5 真实上游 smoke | 显式配置的真实模型调用 | 仅 opt-in、最小请求、明确清理、记录 request/trace 关联；失败不影响默认 CI。 |

### 6.1 L1：确定性契约测试

- 表驱动测试安全 endpoint label、`SanitizeForPersistence/Response`，逐项扫描 JSON 和错误日志中禁止的 key、Authorization、prompt、multipart、BaseURL/URL query 内容；不得以现有 `SanitizeMetricsKey` 的透传行为作为 v2 脱敏边界。
- 测试 v1 -> v2 读模型适配、v2 DTO 白名单、policy fingerprint 在 map 排序和字段顺序变化后仍稳定且排除比例/seed；未知版本只读且不被覆盖。
- 测试 stable bucket 在同一 release/session 下不漂移、比例变更不重洗 cohort、策略变更重置 release，比例边界覆盖 0、1、99、100；受保护选择永远返回 bypass。
- 测试状态迁移只允许相邻晋升、降级总是允许、active 只能 100% 且仅保留 1% control，readiness 不足或 `baseline_stale` 返回阻断原因；三窗口回归按严重度选择 assist 或 shadow。
- 测试 `comparisonStatus` 三态、fallback 分类、attempt 乱序/容量截断、trace revision 乱序 UPSERT、重复 `RecordOutcome` 幂等和“抽样后异常提升”判定。

### 6.2 L2：SmartRouter 与 Failover 集成

使用现有 fake channel/provider 和 `httptest`，断言同一 trace UID 贯穿 SmartRouter、Scheduler、每一次 ChannelLog 和终态。为失败选择使用 `SelectionTraceError`，确保已产生的硬约束证据仍可写入 TraceDetail。

- shadow/dry-run 只记录不改候选；assist 只重排；auto 过滤与 fail-open 分支分别测试。
- `X-Channel`、manual intent、promotion、session pin、显式 model mapping、协议/上下文/能力不被自动路由覆盖。
- 对六类代理入口及其单/多渠道外壳做关联矩阵测试；TraceStore `Record`、详情序列化、`ChannelLog` 关联、writer 队列和窗口写入逐个注入错误，确认代理返回与没有观测组件时一致。
- 模拟连续 upstream 失败、fallback、取消、流式中断和回调乱序，确认终态只写一次、异常样本可从 in-flight 索引提升持久化，且 in-flight 超限/过期不会泄漏。

### 6.3 L3：SQLite、重启与保留测试

使用独立临时 SQLite 文件而非 `:memory:` 测试重启：写入含候选、排序/过滤理由、manual/advisor、Scheduler 摘要、尝试和终态的 v2 trace，关闭 store 后新建 store，按 UID 读取并比较安全 DTO。测试同时覆盖：

- 从模拟 v5 库迁移到 v6，验证 trace 列、窗口换表主键、全量 comparison 计数、event 发布字段、索引和旧 trace 均保留；旧聚合被标为 legacy 且不能进入新 release 判定，重复迁移为 no-op，未来版本仍触发既有 fail-open 初始化路径。
- 普通成功抽样、mismatch/失败/fail-open/manual/advisor/dry-run 必落盘，以及先未采样后在终态提升为异常记录。
- `details_json` 损坏时列表返回 `partial=true` 且只跳过该条，详情返回固定安全错误，不能让其他 trace、Scheduler 或服务启动失败。
- 冻结时钟验证 7 天详细 trace 和 30 天聚合/事件的严格边界；清理重复执行、空表、分批删除和 SQLite busy/closed 错误均不影响请求路径。
- 所有写入后的 DB 行和重新加载后的 API JSON 再次扫描敏感 sentinel，保证持久化层没有因读写转换重新引入敏感字段。

### 6.4 L4：HTTP、SSE 与转换 golden

为 `GET /api/autopilot/traces`、`GET /api/autopilot/traces/:traceUid`、`/stats` 和发布配置路由编写 `httptest`：分页游标、mismatch/状态过滤、窗口 comparison 统计、空结果、过期/未采样 `404`、存储不可用 `503`、坏 DTO `500`/`partial`、权限继承、readiness `409`、非法状态/比例均应有稳定响应。

为 messages、chat、responses、gemini 建立本地 fake upstream 的非流式与 SSE golden fixtures。开启 Trace v2 后，断言输出状态码、header、SSE event/data 顺序、结束帧、取消语义与未开启 trace 时完全一致；另断言首字节/流式时延、每次 failover 的 `ChannelLog.autopilotTraceUid` 和终态已正确关联。images/vectors 只覆盖其非流式硬约束和“不做协议自动转换”的回归用例。

前端至少覆盖 API 类型编译、详情 loading/404/错误重试和列表摘要到详情抽屉的事件契约；复杂展示逻辑使用小型组件测试，否则以 `bun run type-check` 与生产构建作为最低验证。

### 6.5 L5：显式真实上游 smoke

新增独立 smoke 包或命令，默认 `t.Skip`，仅在 `CCX_RUN_REAL_UPSTREAM_SMOKE=1` 且测试专用渠道/凭证已由操作者配置时运行。它使用最小、无敏感内容的文本请求，分别验证一个非流式请求和一个流式请求能生成脱敏 Trace、匹配 ChannelLog、完成终态；不得在仓库写入真实 key、响应正文或 trace DB。

真实 smoke 不进入默认 `go test ./...` 或 CI，也不允许指向生产管理 API。调用失败应输出脱敏诊断并返回失败给显式执行者，但不能触发自动重试风暴、灰度晋升或配置改写。

## 7. 分任务实施步骤

实现顺序固定为“数据契约 -> 存储 -> 请求链路 -> 发布控制 -> 管理面 -> 全量验证”。每个任务先补对应的失败/边界测试，再实现代码；任务之间只通过已定义的 Trace/关联接口衔接，不跨层读取内部字段。

### Task 1：定义 Trace v2 DTO 与脱敏边界 ✅ 已完成

**文件：** `backend-go/internal/autopilot/routing_trace.go`、`backend-go/internal/autopilot/trace_contract.go`、`backend-go/internal/autopilot/trace_contract_test.go`。

- [x] 把 `RoutingDecisionTrace` 的运行时字段映射到显式 `TraceDetailV2`、`TraceSummary`、`SchedulerDecisionSummary`、`EndpointAttemptSummary`；补 `schemaVersion`、`traceRevision`、`requestCorrelationId`、`releaseId`、`policyFingerprint`、target/effective mode、cohort、`comparisonStatus` 和 attempt UID/序号/状态。
- [x] 保留 `RoutingDecisionTrace` 作为唯一事实源；禁止创建 `RoutingDecisionRecord` 或将 `SelectionTrace`/`ChannelLog` 深拷贝成第二份事实模型。
- [x] 用 `crypto/rand` 生成碰撞安全 `traceUid`；策略指纹使用排序后的无密钥决策语义摘要和 SHA-256，明确排除放量比例、control 比例与分桶 seed，不能把 prompt、key、header 或完整 URL 放入输入。
- [x] 分离 `SanitizeForPersistence` 与 `SanitizeForResponse`，使用白名单 DTO；v1 读取适配器对缺失字段返回”不可用”而非伪造默认值，`PromptHash`、原始 `metricsKey`、BaseURL/URL 片段默认不得进入 v2 DTO、数据库或响应。
- [x] 补齐 L1 表驱动测试：字段白名单、URL/key/prompt sentinel 扫描、稳定指纹/独立 release、三态 match、attempt 乱序、终态幂等、未知 schema 和空输入。

**完成条件：** 任何运行时 trace 都能生成 v2 安全 DTO；序列化/脱敏失败有最小安全结果，调用方无需捕获观测异常即可继续。

### Task 2：实现 SQLite v6 迁移、持久化与清理 ✅ 已完成

**文件：** `backend-go/internal/autopilot/schema_migration.go`、`routing_trace.go`、`routing_readiness.go`，以及对应的 `*_test.go`。

- [x] 新增 v5→v6 幂等迁移、`trace_revision` 与 v2 建表列/索引；同一事务内重建带 `release/policy/cohort` 维度及全量 comparison 计数的窗口主键，并为 safety event 增加 release/policy 字段，最后才更新 `PRAGMA user_version`，未来版本保持现有 fail-open。
- [x] 将安全 DTO 写入 `details_json`，顶层列只保留列表/筛选索引；实现按 UID 详情、游标列表和 v1/v2 读适配，避免启动时全表回写。
- [x] 把正常成功保留为 1/10 抽样；将 mismatch、失败、耗尽、fallback/fail-open、manual/advisor 和显式 dry-run 统一标记为必落盘类别。
- [x] 建立受限 in-flight 索引：未采样 trace 登记到独立 map（max 200），终态回填时从 in-flight 提升为带 revision 的 UPSERT。
- [x] 实现清理策略：7 天详细 trace、30 天窗口/event，启动一次并按 24 小时门限批量执行，清理/DB 错误只告警并 fail-open。
- [x] 实现有界异步 writer：512 容量队列、128 终态预留、64 KiB 单条上限、250ms DB deadline、50ms 批量间隔、1s drain 超时。

**完成条件：** 新旧数据库均可启动；重启后 v2 详情完整可读；异常样本不受抽样丢失；任何观测存储故障不改变代理响应。

### Task 3：把 Trace 关联接入真实请求生命周期 ✅ 部分完成

**文件：** `backend-go/internal/handlers/common/multi_channel_failover.go`、`upstream_failover.go`、`channel_log_helper.go`、`backend-go/internal/metrics/channel_log.go`、`request_correlation.go`。

- [x] 在公共多渠道外壳最早处生成一次 `requestCorrelationId`，用 gin context 传递；不复用客户端 header，不替换每次 endpoint 尝试已有的 `ChannelLog.RequestID`。
- [x] 为 `ChannelLog` 增加 `RequestCorrelationID` 和 `AutopilotTraceUID`，通过新增 `WithRequestCorrelationID`/`WithAutopilotTraceUID` ChannelLogOption 从公共尝试路径写入。
- [x] 选择渠道后将 `AutopilotTraceUID` 写入 gin context，供 ChannelLog 关联。
- [x] BuildPlan 写入 dry-run trace：Source=dry_run，必落盘。
- [x] Scheduler SelectionTrace 规范化后附到 trace：NormalizeSelectionTrace + AttachSchedulerDecision。
- [x] endpoint 尝试摘要追加到 TraceStore：AppendEndpointAttempt，乱序合并、容量截断。
- [x] 在 endpoint 尝试开始/结束时向 TraceStore 追加有序、容量受限的安全摘要：通过 SetAttemptRecorderHook 接入 upstream_failover，CreatePendingLog 后记 "started"，成功/错误时记 "completed"。
- [x] ReleaseController 接入 main.go 请求路径：SmartRouter 消费 RoutingReleaseSnapshot，入口冻结后回填 trace 发布维度。
- [x] 补 L2 集成测试：SmartRouter + TraceStore 生命周期（BuildPlan dry-run、ReleaseController 快照冻结、安全覆盖、in-flight 提升、Scheduler 裁决同步、必落盘类别）。
- [x] ChannelLog 展示面跳转入口：为非空 autopilotTraceUid 添加可点击 chip，点击打开 AutopilotTraceDetailDialog 展示决策详情。
- [x] 补 L3 SQLite 生命周期测试：重启还原、坏 JSON、过期清理、迁移幂等、脱敏验证（12 个）。
- [x] 补 L4 HTTP 契约测试：列表分页/过滤、详情 404/Found/脱敏、统计三态（9 个）。
- [x] 真实上游 smoke（opt-in，`CCX_RUN_REAL_UPSTREAM_SMOKE=1`，默认 t.Skip，2 个测试覆盖非流式/流式）
- [x] SSE golden 回归（本地 fake upstream，6 个测试覆盖帧顺序/trace 不改输出/取消不崩溃/脱敏）

**文件：** `backend-go/internal/autopilot/smart_router.go`、`routing_trace.go`、`backend-go/internal/handlers/common/multi_channel_failover.go`、`upstream_failover.go`、`channel_log_helper.go`、`backend-go/internal/metrics/channel_log.go` 和相关测试。

- [x] 在公共多渠道外壳最早处生成一次 `requestCorrelationId`，用 Gin context key 传递；不复用客户端 header，不替换每次 endpoint 尝试已有的 `ChannelLog.RequestID`。
- [x] 让 real、shadow、assist、auto、active 和显式 dry-run 共用同一"构建决策 + 创建 trace"路径；`BuildPlan` 也写入 dry-run trace。
- [x] 将 Scheduler 的 `SelectionTrace` 规范化后附到对应 trace（`NormalizeSelectionTrace` + `AttachSchedulerDecision`）；使用入口捕获的 `RoutingReleaseSnapshot` 回填 release/policy/cohort。
- [x] 为 `ChannelLog` 增加 `RequestCorrelationID` 和 `AutopilotTraceUID`，通过新增 `ChannelLogOption` 从公共尝试路径写入。
- [x] 在 endpoint 尝试开始/结束时向 TraceStore 追加有序、容量受限的安全摘要（`SetAttemptRecorderHook` + `AppendEndpointAttempt`）。
- [x] 对 trace 生成、回填、摘要持久化的 panic/错误做局部保护（`recordEndpointAttempt` defer recover），返回原 Scheduler/Failover 结果。
- [ ] 补集成测试覆盖多 key、多渠道 failover、attempt 乱序/截断、stream cancel、SelectionTraceError、无 Autopilot、记录失败、in-flight 过期和一次逻辑请求关联多条 ChannelLog。（TODO: 后续迭代）

**完成条件：** 一个逻辑请求可从 trace 详情定位所有安全尝试；每条尝试仍有自己的日志 ID；关闭或破坏 TraceStore 时代理行为与原行为一致。

### Task 4：实现受控发布与自动降级 ✅ 已完成

**文件：** `backend-go/internal/config/autopilot_config.go`、`release_controller.go`、`handlers_routing_config.go`、`routing_trace.go`。

- [x] 将 `active`、`rolloutPercent`、内部 `releaseId/rolloutSeed` 与 active control 配置加入 schema、归一化、持久化 setter 和安全响应；保留旧配置默认 shadow，非法状态/比例回退到安全值。
- [x] 对模式枚举做完整审计：常量、normalize、`EffectiveRoutingMode`、`IsAutopilotActive` 同时认识 `active`。
- [x] 新建 `ReleaseController`，集中处理相邻状态迁移、release 创建/重置、不可变 `RoutingReleaseSnapshot`、stable bucket、target/effective mode、安全覆盖。
- [x] 用 session ID 或内部 request correlation ID 与稳定 `rolloutSeed` 做哈希；先应用保护选择与硬约束，再决定 treatment/control/bypass。
- [x] 更新 `handlers_routing_config` 支持 active 模式、rolloutPercent 配置、releaseId 展示。
- [x] 新增 `SetAutopilotRolloutPercent` 持久化方法。
- [x] 参数化现有 readiness/回归聚合，按 release、policy、cohort 隔离：新增 `aggregateRoutingWindowsByRelease` 和 `AggregateComparisonStats`。
- [x] 比例提升限制为 `1/5/25/50/100`：`AllowedRolloutSteps` 和 `NextRolloutStep` 已实现。
- [x] 连续三窗回归降级实现：`EvaluateAndApplyRegression` 已实现。
- [ ] 补纯函数和 HTTP 测试（TODO: 后续迭代）

**文件：** `backend-go/internal/config/autopilot_config.go`、其测试、`backend-go/internal/autopilot/release_controller.go`（新增）、`routing_readiness.go`、`handlers_routing_config.go`、`smart_router.go` 和测试。

- [ ] 将 `active`、`rolloutPercent`、内部 `releaseId/rolloutSeed` 与 active control 配置加入 schema、归一化、持久化 setter 和安全响应；保留旧配置默认 shadow，非法状态/比例回退到安全值。
- [ ] 对模式枚举做完整审计：常量、normalize、`EffectiveRoutingMode`、`IsAutopilotActive`、所有 mode `switch`、SmartRouter、readiness、handler/API、配置热重载、前端类型/表单和旧配置迁移必须同时认识 `active`，不能只在 UI 或配置中接受它。
- [ ] 新建小型 `ReleaseController`，集中处理相邻状态迁移、release 创建/重置、不可变 `RoutingReleaseSnapshot`、stable bucket、target/effective mode、readiness、人工比例递增和运行态安全覆盖；SmartRouter 只消费其结果，不自行判断发布状态。
- [ ] 用 session ID 或内部 request correlation ID 与稳定 `rolloutSeed` 做哈希；先应用保护选择与硬约束，再决定 treatment/control/bypass，未命中分桶强制 shadow，比例变更不得重洗 cohort。
- [ ] 参数化现有 readiness/回归聚合，按 release、policy、cohort、协议/任务类别隔离样本，并从全量 comparison 计数读取 compared/match/mismatch；复用 24 小时、500 样本、成功率、fallback、fail-open 和 p95 基线阈值，把比例提升限制为 `1/5/25/50/100` 和三个完整窗口。
- [ ] 在每个完成窗口后低频检查 regression：连续三窗回归降为 assist 或 shadow，active 使用持续 shadow control；baseline stale/telemetry 不完整时阻断晋升与自动恢复，先设进程内覆盖再最佳努力保存配置和 `AutoSafetyEvent`，不自动恢复。
- [ ] 补纯函数和 HTTP 测试：非法跳级、比例边界、release 隔离、稳定 cohort、所有显式控制优先、readiness 409、按分层三窗降级、active 审计和配置保存失败时的安全覆盖。

**完成条件：** 不存在绕过状态机的高风险晋升；未放量和受保护请求保持 shadow/原调度；SLO 回归能在当前进程立即停止 auto/active 影响。

### Task 5：提供只读 Trace API 与驾驶舱详情 ✅ 已完成

**文件：** `backend-go/internal/autopilot/handlers_trace.go`、`routing_trace.go`。

- [x] 将列表响应改为 `TraceSummary`，实现 release/cohort/mode 服务端过滤；统计从按 release/policy/cohort 分区的窗口聚合读取，不从抽样详情外推。
- [x] 新增按 UID 的只读详情 handler，统一从安全读 DTO 获取数据；仅未找到/过期/未采样返回 `404`，数据库暂不可用返回脱敏 `503`，安全 DTO 损坏返回固定 `500`，列表用 `partial=true` 表示跳过坏行。
- [x] 新增 `ListTraceSummary`、`GetTraceDetail`、`GetV2Stats` TraceStore 方法。
- [x] 统计新增 matchedCount/uncomparedCount/failOpenCount 字段。
- [x] 前端详情抽屉：新增 AutopilotTraceDetailDialog.vue，展示决策→Scheduler→尝试→终态时间线，支持 loading/404/error 重试。
- [x] API 类型和客户端方法：新增 TraceDetailV2/TraceSummary 类型，getAutopilotTraceDetail 方法，release/cohort/mode 过滤。
- [x] AutopilotTraceTable 适配 TraceSummary，发出 select 事件，新增 comparison 三态列。
- [x] AutopilotView 管理详情对话框状态，连接表格选择事件。
- [x] 新增 zh-CN/en 双语 30+ 条 trace detail i18n 消息。
- [x] 前端 type-check 和 build 均通过。

**文件：** `backend-go/internal/autopilot/handlers_trace.go`、`routing_trace.go`、相关 HTTP 测试；`frontend/src/services/api.ts`、`api-types.ts`、`views/AutopilotView.vue`、`components/AutopilotTraceTable.vue`、新增详情组件及相关 locale 文件。

- [ ] 将列表响应改为 `TraceSummary`，实现 `(createdAt, traceUid)` 游标、release/cohort/mode 服务端过滤和固定页大小；统计从按 release/policy/cohort 分区的窗口聚合读取，不从抽样详情外推。
- [ ] 新增按 UID 的只读详情 handler，统一从安全读 DTO 获取数据；仅未找到/过期/未采样返回 `404`，数据库暂不可用返回脱敏 `503`、安全 DTO 损坏返回固定 `500`，列表用 `partial=true` 表示跳过坏行，绝不回退序列化内存原始 trace。
- [ ] 以与现有 Autopilot 管理 API 相同的路由组和鉴权注册接口；不增加删除、下载、重放、写策略或代理侧 endpoint。
- [ ] 扩展 API types 和客户端方法；移除列表中完整候选数组，提供当前/历史 release 筛选，避免初始加载过大和页面持有详情数据。
- [ ] 让 `AutopilotTraceTable` 发出选择事件，`AutopilotView` 管理单一详情抽屉的 loading、404、重试和关闭状态；展示固定顺序的决策、Scheduler、尝试、终态，不展示 prompt hash 或原始 endpoint。
- [ ] 在 ChannelLog 展示面为非空 `autopilotTraceUid` 增加跳转入口，空关联维持既有渲染；新增文案走现有中英 locale 机制。
- [ ] 用 `httptest` 断言 API 脱敏、分页、release/cohort 过滤、404/503/500、`partial`、查询 deadline、授权继承和发布批次统计口径；以前端类型检查/构建验证详情交互契约。

**完成条件：** 管理员能从摘要安全地查看跨重启的单条决策，并能从一次 upstream 尝试回到决策详情；页面和 API 均没有可见敏感字段。

### Task 6：完成跨层回归、真实 smoke 与设计同步 ✅ 已完成

**文件：** `docs/design/channel-autopilot.md`、`docs/superpowers/plans/2026-07-23-autopilot-trace-rollout.md`。

- [x] 更新 `channel-autopilot.md`：新增 P1.6 Trace v2 契约与灰度发布章节，覆盖 v2 DTO、SQLite v6 迁移、灰度发布状态机、ReleaseController、只读 Trace API。
- [x] 更新实施计划：标记 Task 1-5 完成状态。
- [x] 修复 lint 问题：移除 `release_controller.go` 未使用的 `lastPromotionAt` 字段。
- [x] 运行全量后端测试（29 个包全部通过）、构建验证成功。
- [ ] 真实上游 smoke（opt-in，`CCX_RUN_REAL_UPSTREAM_SMOKE=1`）（TODO: 独立 smoke 包）
- [ ] 前端详情抽屉和 SSE golden 回归（TODO: 后续迭代）

**文件：** 上述任务的 `*_test.go`、协议 handler 测试、`docs/design/channel-autopilot.md`、必要的开发文档。

- [ ] 为 messages/chat/responses/gemini 的本地 fake upstream 编写非流式与 SSE golden 回归，比较 trace 启用前后的响应帧、状态码、header、取消与 failover 语义，并覆盖 writer 背压不改变协议。
- [ ] 为 images/vectors 增加硬约束与无自动协议转换回归，确认本计划不意外把它们引入文本流式 Autopilot 路径。
- [ ] 新增默认跳过的真实上游 smoke，使用 `CCX_RUN_REAL_UPSTREAM_SMOKE=1` 及测试专用配置显式开启；文档说明凭证来源、费用风险与禁止指向生产管理 API。
- [ ] 更新 `channel-autopilot.md` 中 trace schema、release/cohort、保留期、rollout/active/control 定义、回退语义和 API 降级契约与测试入口，删除与实现不一致的“所有 dry-run 已自动记录”等陈述。
- [ ] 运行目标包测试后再运行全量后端、前端类型检查/构建；保留失败样本的 trace 作为测试断言，但不把真实请求内容写入仓库。

**完成条件：** 所有协议在本地回归中保持字节/事件语义，真实 smoke 不会自动运行，设计文档与 API/状态机实现一致。

## 8. 验证、提交范围与风险控制

### 8.1 验证顺序

每个实现任务先运行定向测试，合并前按以下顺序执行；所有命令都在当前仓库工作区运行，使用独立临时 Go cache，避免污染用户现有构建缓存。

1. 文档与格式：

   ```bash
   git diff --check -- "docs/superpowers/plans/2026-07-23-autopilot-trace-rollout.md"
   rg -n "[T]ODO|待[补]充|占[位]" "docs/superpowers/plans/2026-07-23-autopilot-trace-rollout.md"
   ```

   第二条命令预期无输出；若计划保留有意的后续项，必须改成明确的 backlog，而不是未填写的章节标记。

2. Trace/发布定向测试：

   ```bash
   cd "backend-go" && GOCACHE="/tmp/go-build" GOMODCACHE="/tmp/go-mod" go test ./internal/autopilot ./internal/scheduler ./internal/metrics ./internal/handlers/common -count=1
   ```

3. 后端完整验证：

   ```bash
   cd "backend-go" && make test
   ```

   ```bash
   make build
   ```

4. 前端类型与生产构建：

   ```bash
   cd "frontend" && bun run type-check
   cd "frontend" && bun run build
   ```

5. 真实上游 smoke 只在人工明确设置 `CCX_RUN_REAL_UPSTREAM_SMOKE=1`、测试专用渠道和凭证后单独执行；不得把该环境变量写入默认 Makefile、CI 或开发启动命令。

### 8.2 发布观察与回滚验收

- 发布前确认 TraceStore 初始化、v6 migration、release/policy/cohort 窗口、comparison 计数和管理 API 均健康；任一项不可用时保持 off/shadow，不直接放量。
- shadow 阶段在当前 release 观察 24 小时和至少 500 样本，检查全量 mismatch/uncompared、失败、fail-open、p95 和每种已纳入协议/TaskClass 的分布。
- 每次比例提升记录 release ID、策略指纹、操作者和前后状态；若低流量无法形成三个完整窗口，保持原比例而非强行晋升。
- auto/active 期间验证三窗口回归能把实际生效模式降到 assist/shadow；active 额外确认 1% control 连续有效且未 `baseline_stale`，回滚后不得自动恢复，需人工重新通过 readiness。
- 通过 trace 详情抽查一条成功、一条 mismatch、一条 failover、一条流式取消和一条 manual/advisor 记录，确认无敏感字段且能链接到 ChannelLog。

### 8.3 提交边界

本计划本身只新增：

```text
docs/superpowers/plans/2026-07-23-autopilot-trace-rollout.md
```

实现阶段按 Task 1-6 分开提交，遵循现有 conventional commit 风格，例如 `feat(autopilot): add versioned routing trace contract`、`feat(autopilot): add guarded routing rollout`。每次只 stage 当前任务文件；绝不把工作区已有的后端/前端改动、生成产物、配置密钥或 `dist/` 带入提交。完成验证后自动创建本地 commit，不执行 push。

### 8.4 风险与缓解

| 风险 | 影响 | 缓解与阻断条件 |
|---|---|---|
| 关联 ID 在入口或 retry/failover 中丢失 | 详情无法还原完整尝试链，或非代理诊断被误关联。 | 先完成六类入口的覆盖/排除矩阵；外层生成逻辑请求 ID，ChannelLog 保留 attempt ID；L2 强制测试一请求多尝试。 |
| 新字段意外包含敏感内容 | SQLite/API/浏览器泄露密钥、用户数据或内部 endpoint。 | 显式持久化/响应 DTO、endpoint label 替代 metricsKey/BaseURL、双重脱敏、sentinel 扫描；任一泄露测试失败即阻断发布。 |
| 抽样在终态前完成 | 失败或 fail-open 样本重启后消失。 | in-flight 索引保留未完成快照，终态分类触发升级落盘；容量/过期可观测，SQLite 重启测试必须验证。 |
| attempt 摘要无界或乱序 | SQLite 膨胀，或终态被早到/晚到回调覆盖。 | attempt UID/序号与受锁状态机，32 次/64 KiB 上限、聚合截断和唯一 `Finalize`；并发回调测试阻断。 |
| SQLite migration、writer 或清理锁库 | Autopilot 启动失败或请求延迟。 | migration 失败禁用 Autopilot；换表事务、单 writer、有界队列、deadline 和小批量清理；观测错误 fail-open 且 telemetry 不完整阻断晋升。 |
| rollout 分桶不稳定或策略混样 | 同一 session 被重新洗牌，旧样本错误证明新策略安全。 | release ID 与稳定 seed 分离；指纹排除比例/seed，窗口按 release/policy/cohort 分区，语义变更回 shadow。 |
| 热重载改变在途请求的证据归属 | 单条 trace 的决策、终态和窗口属于不同 release。 | 入口冻结 `RoutingReleaseSnapshot`，后续回填只读该快照；配置变更只作用于新请求。 |
| 显式控制被自动路由覆盖 | 用户指定渠道、协议或模型行为改变。 | 保护层先于分桶/排序；`X-Channel`、manual、promotion、session pin 和硬约束有独立不变量测试。 |
| active 或配置迁移被旧代码忽略 | 管理面显示已发布但真实模式不一致。 | 审计枚举、normalize、active 判定、所有 switch、SmartRouter、readiness、API、热重载与前端类型；未知值安全回 shadow。 |
| SLO 回归检测滞后、基线过期或聚合掩盖分层回归 | 高风险流量持续，或频繁抖动。 | 三个完整 15 分钟窗口、每窗最小样本、同 release 的 control、按协议/任务类别比较；`baseline_stale` 阻断晋升/自动恢复，严重硬约束直接 shadow。 |
| comparison 统计误用抽样详情 | mismatch 看似正常但全量差异已扩大。 | 在窗口持久化 compared/matched/mismatch/uncompared 计数；stats/readiness 只读取该聚合，迁移与 HTTP 测试验证口径。 |
| 观测 API 将暂态故障伪装为 404 或泄露诊断 | UI 误判数据不存在，或暴露 SQLite/原始对象。 | 404/503/固定 500/`partial` 契约、2 秒查询 deadline、无内存原始对象回补；HTTP 测试阻断。 |
| SSE 回调改变响应协议 | 客户端断流、转换结果变化。 | 本地 fake upstream golden 对比 trace 开关前后字节与事件；协议测试失败阻断。 |
| 真实 smoke 误发生产请求 | 费用、隐私或生产配置受影响。 | 默认跳过、显式 env、测试专用渠道/凭证、最小请求；不得进入 CI/Makefile。 |
| 当前工作区有无关改动 | 误提交用户工作。 | 只新增/提交计划文件；验证前后用 `git status --short` 和路径级 `git diff` 复核。 |

**最终验收：** 三类方法（可观测性契约、shadow/灰度发布、分层测试）均有对应实现任务、测试断言、运行命令和回滚条件；发布证据按 release/policy/cohort 隔离、active 保有有效对照、观测故障不影响代理请求；OpenSquilla 只作为方法论来源，不改变 CCX 的代理协议和调度所有权。
