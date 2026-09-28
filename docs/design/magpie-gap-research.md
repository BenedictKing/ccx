# Magpie 借鉴范围与 CCX 现状研究

本文以 CCX 当前代码为事实源，记录从 magpie 借鉴流式故障转移、冷却、额度、模型目录和会话亲和能力时的差距。

当前结论：Agent 配置接线暂不纳入本轮。CCX 已有 Agent Config、状态快照和恢复能力，本轮先研究网关运行时能力，避免重复建设。

## 研究原则

- 先复用 CCX 已有模块，再引入 magpie 的抽象。
- 不把外部参考项目的实现细节直接当成产品需求。
- 区分“客户端已经收到内容后无法切换”和“首个内容前可以安全切换”。
- 额度数据必须标明来源、可信度和采集时间。
- 动态模型发现不能绕过 CCX 的协议能力、上下文窗口和兼容性过滤。

## 当前能力矩阵

| 方向 | CCX 当前机制 | 仍需研究的缺口 |
| --- | --- | --- |
| 流式故障转移 | 流式预检测、空流重试、首字超时、首字后断流、SSE 拉黑错误、竞速提交闸门 | 早期 SSE 错误分类统一、错误事件协议覆盖、提交前重试边界 |
| 冷却与恢复 | Key/模型 `RecoverAt`、错误体重置时间、`Retry-After` 和 reset 头、渠道级限流冷却 | Key/模型/渠道三级规则统一、失败原因分级退避、成功后的冷却清理 |
| 额度 | provider API、响应头、配置额度、可信度合并、headroom、饱和渠道沉底 | subscription/plan/balance 统一模型、通用余额路径、窗口到期优先 |
| 模型目录 | `/models` 发现、套餐管控面、内置 manifest、运行时上下文学习、manifest drift | live/catalog 合并、外部目录元数据、自动回填策略 |
| 会话亲和 | Trace affinity、提示词指纹、上下文分桶、subagent 隔离 | turn 生命周期锁定、缓存命中信号、明确的解锁和迁移规则 |

## 后续顺序

1. 流式故障转移与冷却行为矩阵。（2026-09-29 完成研究与第一轮实现，见 §1 实现记录；短文本 EOF 对齐为可选后续项）
2. 额度来源、窗口和调度消费矩阵。（2026-09-29 完成研究与第二轮实现（freshness + 观测出口），见 §2；窗口键统一、Provider 结构适配、快过期优先排序待续）
3. 模型发现、注册表和运行时能力合并矩阵。
4. 会话亲和与 turn 路由矩阵。

## 1. 流式故障转移与冷却

### CCX 已有机制

`backend-go/internal/handlers/common/stream.go` 将流式处理分成两个阶段：

- 首个有效内容之前，缓冲事件并检查空流、错误事件、首字等待超时和 Key 拉黑条件；
- 首个有效内容之后，检查断流和工具调用空闲超时。

`stream_processor.go` 只有在预检测完成、竞速提交闸门通过后才写响应头。因而空流、首字超时、首字前余额/认证错误可以进入同一请求的 failover。响应已经提交后，CCX 会记录流中断并结束当前尝试，不会把第二个上游拼接到客户端已经收到的内容后面。

### 矩阵 1 结论：流式事件 × 协议 × 提交状态 × 重试语义（2026-09-29 验证）

四个边界问题的验证结论：

1. **四协议错误事件预检测识别原不成立**：HTTP 状态码错误与两级超时（首字、断流）四协议统一进 failover；但流内 SSE 错误事件（HTTP 200 + `data:` 携带错误对象）原只有 messages、responses 客户端识别，chat 与 gemini 完全不设防，错误体原样透传客户端且不触发 failover。messages+openai 上游还有裸错误（`chunk["error"]` → errChan → 裸 `fmt.Errorf`）落入 failover 执行器 else 分支、直接终结整条 failover 链的边角缺陷。**本轮已修复**（见实现记录）。
2. **四种异常流语义原不一致**：messages 全覆盖（空流/usage-only/短文本 EOF/malformed tool call 均 failover）；responses 覆盖除短文本 EOF 外三项；chat 仅识别 malformed tool call；gemini 仅识别 0 字节空流。**本轮已把 chat/gemini 的空流与 usage-only 语义对齐**（流结束仍无语义内容 → failover）；短文本 EOF 检测仍为 messages 独有（属后续可选对齐项）。
3. **重复计数核查结论：分层设计而非缺陷**。一次预检 failover 家族失败触及：请求内 `failedKeys`（轮转）、`MarkKeyAsFailed` 内存连败冷却（5min/连败翻倍）、metrics 失败率 + Key 级熔断、渠道×Key×模型熔断（`recordModelCircuitFailure`）、BaseURL 评分。Key 级与模型级熔断双计是刻意的双粒度设计（模型可归因的失败豁免 Key 级、由模型级承接）；额度/过载类失败被 `FailureClass` 门控排除出熔断，只进失败率。无需修复。
4. **竞速败者豁免正确**：纯败出（`ErrRacingSuperseded`）经 `RecordRequestFinalizeIgnored` 把 pending 记录从滑动窗口整条删除，不污染失败率、不熔断、不拉黑；真实上游错误即便发生在败出时刻也照常记账（有意设计）。无需修复。

post-commit 断流（`ErrStreamPostCommitStalled`）统一「结束当前尝试、不重试」——Header 已发送不可拼接，行为正确。

### 矩阵 2 结论：失败原因 × 层级 × RecoverAt 来源 × 恢复动作

| 层级 | 机制 | RecoverAt 来源 | 恢复动作 |
| --- | --- | --- | --- |
| Key 持久级 | `DisabledAPIKeys`（`BlacklistKeyWithRecoverAt`，跨协议级联） | 显式时间 > 错误文案提取（`ExtractQuotaRecoverAt`）> 1h 兜底；认证/权限类无 RecoverAt 永不自动恢复 | 调度槽位（UTC 0/8/16 点）+ 1min ticker 兜底；套餐用量刷新数据驱动恢复；手动 |
| Key×模型组合 | `DisabledKeyModels` | 原固定 1h 不透传上游时间；**本轮起透传**（无效/过期兜底 1h） | `RestoreExpiredKeyModels` 定时；手动 |
| 渠道/Key scope 运行时 | `ChannelLimiter.cooldownUntil` | Retry-After / anthropic/openai reset 头，clamp 至 1h，只延长不缩短 | 自然过期；无清除 API |
| 内存短期 | `failedKeysCache`（`MarkKeyAsFailed`） | 固定 5min / 连败>3 翻倍 | 每分钟清过期；成功不清理 |
| 熔断 | Key 级 breaker + ModelCircuit | FailureClass 门控（额度类不进熔断） | **成功即清零**（连败、half-open 探针、自动权重） |

对照 magpie 的差异：CCX 缺「成功即清零」覆盖到内存冷却与 limiter 冷却层（本轮确认这是有意的固定窗口分层，不改）；额度错误恢复时间信任链（显式 > 文案 > 兜底）与 magpie 的 `restAfter` 优先级链语义一致，本轮把组合级也接入了该信任链。

### 第一轮实现记录（流式 failover 与冷却统一）

- `common.DetectStreamDataError`：单条 SSE data JSON 的跨协议错误识别（OpenAI/Gemini 顶层 error 键、Claude `type=="error"`、Responses `response.failed`），拉黑分类复用 `DetectStreamBlacklistError` 词汇表（code 回填 type 的兜底与 responses 预检一致），并提取错误文案中的恢复时间。
- chat/gemini 预检测接入上述识别：拉黑类返回 `ErrBlacklistKey`，非拉黑类按 `ErrEmptyStreamResponse` 语义进入 failover；流结束仍无语义内容（空流/usage-only）同样 failover。gemini 预检把思考类增量（thinking_delta / reasoning_content / reasoning_summary）计为可交付内容，纯思考流不再被判空。
- `common.ClassifyPreflightStreamError`：预检阶段的裸错误（provider 裸包装的上游错误、传输层错误）统一归入 failover 家族，修复 messages+openai 上游裸错误终结整条 failover 链的缺陷；chat/gemini 的 handleStreamSuccess 同步接入。
- `ErrBlacklistKey` 增加 `RecoverAt` 字段，messages/responses 全部构造点（含 fold 路径）从拉黑文案提取恢复时间；SSE 流内余额类失败（`upstream_failover.go`）将恢复时间透传给组合级限制（原来固定 1h 丢弃上游时间）。
- `DisableKeyModel` 签名增加 recoverAt 参数：显式未来时间透传，无效/过期沿用 1h 兜底（与 Key 级拉黑语义一致）。

### CCX 当前冷却层级

- `ShouldBlacklistKey` 负责识别认证、权限、余额和额度类错误。
- `BlacklistKeyWithRecoverAt` 持久化 Key 级恢复时间；余额/额度类没有明确时间时默认使用一小时。
- `DisabledKeyModels` 提供 Key 与模型组合级限制；上游给出恢复时间时透传，否则一小时兜底（本轮统一）。
- `ratelimit.ChannelLimiter` 解析 `Retry-After`、Anthropic reset 头和 OpenAI reset 头，施加渠道或 Key scope 的运行时冷却。
- 托管账号用量刷新后，Kimi、MiMo、Compshare 和 Volcengine 可以按实际窗口恢复被禁用的 Key。

### 借鉴重点

magpie 的价值主要在失败原因分级和恢复原因可解释性：credit、quota、rate、backoff、window 等原因应进入统一的状态模型。不能简单把 CCX 的一小时默认值替换掉，因为它仍需要作为上游没有提供恢复信息时的安全兜底。

## 2. 额度与成本

### CCX 已有机制

`internal/quota` 已将额度观测抽象成带来源的值：

- `provider_api`：供应商或托管账号的用量接口；
- `response_headers`：从 Anthropic/OpenAI 风格响应头提取的 remaining、limit、reset；
- `configured`：new-api multiplier 或用户配置的静态额度。

`Quota Manager` 会合并不同来源，计算渠道 headroom，并通过饱和桶判断窗口是否耗尽。调度器把饱和渠道沉到底部，所有非饱和候选不可用时仍会 fail-open 回退，避免额度数据错误直接阻断服务。Smart Router 当前消费的是 headroom 分数，不是重置时间排序。

### 当前缺口

1. 额度对象还没有明确区分 subscription、plan 和 balance 三种产品语义。
2. 窗口 scope、model scope、account scope 的关系分散在不同 Provider 结构中。
3. 任意中转站需要自定义余额 JSON 路径时，缺少通用配置能力。
4. 当前调度关注“剩余是否紧张”，还没有“哪个窗口更快过期”这一排序维度。
5. 本地 request meter 可以统计请求窗口，但不能冒充供应商真实余额。

### 矩阵 3 结论：额度来源 × truth level × freshness × 调度消费点（2026-09-29 验证）

| 来源 | 生产者 | 产出 | freshness 机制 |
| --- | --- | --- | --- |
| provider_api | SubscriptionRefreshWorker 的三个 fetcher（OpenAI/Anthropic/Google，**现均只做存活检测返回 -1，产不出真实余额**） | DimCurrency.Remaining（无 Limit/Reset） | 24h 周期 + 每日预算；无 TTL |
| response_headers | 每请求响应头（仅 anthropic/openai 映射，gemini/images/vectors 无头映射） | tokens/requests 的 Limit+Remaining+ResetAtMs | 请求驱动滚动刷新 |
| configured | new-api 同步 + 配置热更重放 | DimCurrency.Remaining+Used（无 Limit，headroom 恒中性，仅 Balance=0 提供 exhausted 信号） | 同步/热更驱动 |
| estimated | 无生产者（枚举占位） | — | — |

关键结论：

1. **freshness 原先完全缺失**：Value 无时间戳、无 TTL，跨窗口后陈旧观测（上窗口剩 5%）持续压底渠道；exhausted 有饱和桶懒重置兜底，**approaching_limit 原先无任何恢复路径**，且被压底的渠道拿不到流量也就等不到响应头刷新（死锁）。**本轮已修复**（读路径惰性剪除，见实现记录）。
2. **reset 时间只进桶懒重置，不进排序**：`Value.ResetAtMs` 在 quota 包之外零引用；「快过期额度优先」排序维度不存在（属下一阶段，模型稳定后再做）。
3. **暗状态**：`GetChannelState`/`ChannelSaturationRank` 原先无生产调用方，headroom/truth/饱和状态无任何 API/UI 出口。**本轮已补观测出口**。
4. **托管套餐（Kimi/MiMo/Compshare/Volcengine）与 quota.Manager 是平行体系**：console 用量落 ConfigManager 快照，只供前端展示与 `TryRestoreDisabledKeysByUsage` 恢复判定，不进 quota 包；订阅余额（new-api）走 configured 级是唯一进包的余额数据。
5. 额度对路由的影响是双通道：软通道（headroom 进 SmartRouter 评分第 10 项，权重 0.3/0.5）+ 硬通道（饱和沉底 + 全员饱和 fail-open 回退）；与 Key 拉黑/ChannelLimiter 冷却无交叉触发。
6. 观测纯内存不落盘，重启冷启动（configured 级靠 new-api 同步/热更重放重建）。

### 第二轮实现记录（额度 freshness 与可观测出口）

- `ChannelState.pruneExpiredValues`/`hasExpiredValues`（quota/truth.go）：窗口已翻转（`now >= ResetAtMs`）的观测属上一窗口，剪除后状态回退 unknown（fail-open）。无窗口语义的余额（ResetAtMs=0）永不剪除。
- `Manager.pruneIfExpired`（quota/manager.go）：读路径惰性剪除——RLock 快查无过期值零开销，发现过期才升级写锁剪除一次；接入 `GetChannelHeadroom`/`GetChannelTruth`/`IsChannelSaturated`/`GetChannelState` 四个读 API。修复 approaching_limit 渠道在窗口翻转后的自愈死锁。
- `Manager.SnapshotAll`：全部渠道配额快照（先剪除、按 ChannelUID 排序），配套管理端点 `GET /api/quota/channels`（handlers/quota_inspect.go，含 headroom/saturated 派生视图；autopilot 关闭时返回空列表）。

### 研究顺序

先保持现有 `quota.Manager` 和 `TruthLevel`，补充统一的窗口键、来源新鲜度和 reset 时间，再把 Provider 特有结构适配到该模型。只有在统一模型稳定后，才增加“快过期额度优先”和任意余额路径。（freshness 与可观测出口已落地，2026-09-29；窗口键统一与 Provider 结构适配为下一轮，快过期优先排序与任意余额路径在其后。）

## 3. 模型目录与运行时能力

### CCX 已有机制

模型来源目前包括：

- 普通渠道的 `/models` 发现；
- 火山套餐的管控面模型清单；
- 内置模型 manifest 和 fallback；
- 运行时上下文窗口、输出上限和兼容性学习；
- PresetStore 的远程或磁盘快照更新。

模型发现结果会携带 source 和 message。发现清单与内置 manifest 不一致时，Auto Discovery 发布 `manifest_drift` 事件，但当前主要用于健康中心告警，不会直接修改共享模型注册表。

### 当前缺口

1. 真实 live 模型清单与静态 registry 的合并规则尚未统一。
2. `/models` 返回的模型通常缺少显示名、价格、effort 和完整上下文元数据。
3. 外部目录不能直接作为协议能力和调度能力的事实源。
4. manifest drift 尚未形成“审核后回填”的闭环。

### 研究顺序

先定义元数据来源优先级：真实渠道清单优先，运行时探测次之，远程目录和内置 registry 只补充元数据。未知模型可以进入“待验证”状态，但必须经过协议、上下文和兼容性过滤后才能成为调度候选。

## 4. 会话亲和与 turn 路由

### CCX 已有机制

`TraceAffinityManager` 默认以 30 分钟 TTL 保存用户或会话到物理路由的亲和关系。调度时会优先尝试亲和路由，但更高优先级渠道、熔断、运行时冷却、Key 不可用和模型能力过滤都可以使其失效。

匿名请求还可以通过 system 与首条 user 内容生成提示词指纹，进入同一套 session/trace affinity 通路。调度键还会纳入协议和上下文窗口分桶，subagent 使用独立亲和键，避免主对话和子任务互相污染。

### 当前缺口

1. 亲和是请求间的偏好，不是一个 turn 内的严格决策锁。
2. 当前没有统一记录“本 turn 已经使用的模型、渠道和失败迁移次数”。
3. `cache_read`、缓存年龄和缓存成本尚未参与是否继续亲和的判断。
4. 上下文溢出、额度耗尽和渠道故障触发迁移时，解锁规则分散在调度和 failover 路径中。

### 研究顺序

先用现有 trace/session 标识定义 turn 生命周期，再明确三类迁移：

- 首次选择：可以按 Smart Router 正常排序；
- turn 内普通请求：优先保持已选路由；
- turn 内不可恢复故障：允许 failover，并记录迁移原因。

缓存命中阈值和“是否值得固定”应作为后续实验参数，不应直接复制 magpie 的固定数值。

## 5. 后续验证产物

实现前先完成以下只读研究和测试矩阵：

1. 流式事件类型 × 协议 × 提交前后状态 × 重试结果。
2. 失败原因 × Key/模型/渠道层级 × `RecoverAt` 来源 × 恢复动作。
3. 额度来源 × truth level × freshness × 调度消费点。
4. 模型来源 × 元数据字段 × 能力过滤 × drift 行为。
5. session/turn 状态 × affinity 命中 × failover 解锁条件。

完成矩阵后，第一项实际实现仍然是“流式 failover 与冷却统一”，Agent 配置阶段保持不动。
