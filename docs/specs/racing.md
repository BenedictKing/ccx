# 竞速模式（Racing：跨渠道影子请求）

> 首字明显慢的渠道（如中转站排队）触发跨渠道影子竞速：谁先交付有效响应，客户端用谁。
> 机制参照兄弟项目 kiro.rs 的对冲竞速（hedging），裁决点借 CCX preflight 缓冲的"未提交可弃"窗口。

## 用户可见面（极简）

只有两个开关，其余行为参数全部由策略自动推导：

| 开关 | 位置 | 语义 |
|---|---|---|
| `racing.enabled`（全局） | 智能路由面板「竞速模式」开关（随 `PUT /smart-routing/config` 整卡保存，字段名 `racingEnabled`；另有独立端点 `PUT /api/racing/config` 供脚本直调） | 竞速总开关，默认关 |
| `racing.enabled`（渠道级） | 渠道编辑 → 自定义参数 | 渠道参与开关：关闭 = 既不做主触发，其候选也不进影子池 |

## 策略表（代码内常量，`internal/racing/registry.go`）

按请求生效的 CostPreference（X-Cost-Preference > 场景预设 > 全局 Mode）推导：

| CostPreference | 影子数 | 流式触发 floor | 影子候选过滤 |
|---|---|---|---|
| quality_first | 3 | 4s | 无限制 |
| balanced（默认） | 1 | 8s | 无限制 |
| cost_first | 1 | 8s | 仅综合倍率 ≤ 主候选一半（渠道 CostMultiplier × key GroupMultiplier）；无合适候选不派 |

其余内部常量：分位数 p90、样本门槛 20、观测窗口 15 分钟、窗口容量 512、全局并发影子信号量 12、非流式 floor 10s。

## 触发阈值

`threshold = p90(近 15 分钟同家族×阶段成功样本，n≥20) 否则 floor，再 clamp[floor, ceiling]`

- 维度：模型家族（claude/gpt/gemini/other）× 阶段（流式首字 / 非流式完成时长），家族全局共用一个窗口（慢性慢渠道持续超 p90 会被持续竞速）。
- ceiling：流式 = 渠道 StreamFirstContentTimeoutMs 解析值；非流式 = 渠道 ResponseHeaderTimeout（同源超时，等过它请求已失败）。
- 样本消赢家偏差：流式分支出过首字即记（含被取消的败者）；非流式仅记赢家完成耗时。
- 计时起点 = 外壳 attemptStartedAt（含渠道内 key 轮转等待，符合用户体感；与 kiro 的 attempt 级重置刻意不同）。

## 影子候选（五元组粒度）

1. 主源：SmartRouter 排名缓存（`ABTestSampler.CandidateCache()`），`autopilot.RacingShadowCandidates` 从可行集（Selected=true）选取并排除与主尝试相同的五元组（channelUID+keyIdentity+actualModel）——同渠道不同 key / 不同执行模型可作影子；多样性纪律：主 key 一律不作影子（同账号并发是放大器）、异渠道候选优先、多影子按 keyIdentity 去重。
2. 回退：缓存空 → `SelectChannelWithOptions` 按 FailedRoutes（已用路由）重选（纯跨渠道）。
3. 执行：候选反查渠道构造 SelectionResult + pin（`WithExecutionPin` 经 `WithSelectionTrace` 透传），reason 记 `racing_shadow`。

## 裁决与分支治理

- **提交闸门** `racing.Gate`（claim-once）：claim 点在 preflight 首字确认后 / 非流式完整响应校验后、写客户端之前；胜者 claim 即取消其余分支（ctx 级），败者 claim 失败以 `ErrRacingSuperseded` 收尾。主分支返回后的编排器止血同样只取消非赢家分支（`CancelExcept(ClaimedBy())`）——影子已 claim 时必须保留赢家透传（固定 `CancelExcept(0)` 会把赢家一并取消、客户端流被切断 context canceled，248deb01 回归修复）；无 claim（主真实失败）时保留在飞影子，由结算路径兜底接管。
- **分支写出隔离** `racingBranchWriter`：主/影子分支各挂独立分支 writer（pre-commit 头/状态/体写私有缓冲），claim 赢家 Commit 时一次性桥接到真实客户端 writer 并转透传，败者 Discard 后写出静默丢弃——真实 writer 只被赢家触碰（构造保证的单写者，取代早期"影子回填主 Writer + meta 锁串行化 echo 头"的约定式模型）。
- **败者治理**：不计失败指标、不熔断、不拉黑、不标 URL 失败、不参与自学习（工具调用/严重度/上下文棘轮）；唯一例外是质量闸门让出——伪标记已在败者分支自身首包缓冲实测命中（已完成观察，非部分流推断），让出时单独计一次白名单负反馈（`notePseudoToolCallYield`，见 tool-call-capability.md §4.5）。渠道日志终态 `racing_lost` + `racingStatus=lost`，赢家 `racingStatus=won`。影子真实上游错误（超时/500/拉黑）仍照常记账。主/影子尝试共享请求 correlation ID，渠道日志按用户请求折叠为一行（「N 次尝试」徽章，见 web-ui-dialogs.md §6）。
- **防误判赢家**：被取消的影子可能以空流 EOF → 内部轮转 → context.Canceled + Handled=true 收尾，pickWinner 判据为 `Handled && LastError == nil`；cancel 连带的空流响应直接按败出终止。
- 影子赢时分支 gin keys 回拷主 context（responseText/lastUserMessage 不丢）；primary selection 补记 trace 终态（防悬空）。
- 防放大：每请求最多 maxShadows 条（策略表）；全局并发信号量 12；影子分支内禁递归竞速；X-Channel pin / 含图请求 / 非四对话协议不竞速；**限流热渠道不派影子**（渠道级冷却中或全部 key scope 被限速延迟时该候选被跳过——排名缓存路径绕过调度器冷却过滤，候选构造处兜底，防止 429 风暴中 shadow 放大真实上游消耗）。
- 影子用自己的完整 ceiling 超时窗口（从影子发出起算），不共享主链剩余 deadline。

## 后端结构

| 位置 | 职责 |
|---|---|
| `internal/racing/` | 阈值注册表、Gate、信号量、策略表、ErrRacingSuperseded |
| `internal/config/racing_config.go` | 全局/渠道级开关 + ResolveRacingPolicy + SetRacingEnabled |
| `internal/handlers/common/racing.go` | 编排器 RunRacingAttempt（武装检查/定时器/影子派发/裁决归并/样本记录） |
| `internal/handlers/common/multi_channel_failover.go` | 外壳接线（TrySelectedChannelFunc 增加 gin context 参数；AlsoFailedRoutes 并入 failedRoutes） |
| `internal/handlers/common/upstream_failover.go` | echo 头块写出（分支 writer 隔离）、败者分类豁免（先于 isClientSideError）、成功路径 won 标记、日志角色 |
| `internal/handlers/common/racing_writer.go` | 分支 writer：pre-commit 私有缓冲、赢家 Commit 桥接真实 writer、败者 Discard |
| `internal/handlers/common/stream_processor.go` + 4 协议 handler | 流式/非流式 claim 点 |
| `internal/autopilot/racing_candidates.go` | 五元组候选选取 |
| `main.go` | RacingHub 注入 + `/api/racing/config` |

## 延迟负反馈学习（竞速姊妹机制）

竞速样本不只驱动阈值，还回流为组合级学习：

- **键粒度**：渠道×keyHash×实际出站模型×任务类（TaskClass 7 值，未分类落 unknown 全量桶）。存储在 ChannelCompatCache 第四分区 `latencyPenalties`（.config/channel_compat.json，TTL 24h，`GET/DELETE /api/compat-cache` 可查/可清 `?section=latency-penalty`）。
- **三个慢信号**：① 竞速 primary 被影子击败（upstream_failover 败者豁免点，非 shadow 分支才记）；② 竞速触发本身（派影子时，主组合记一次）；③ 普通流式成功请求首字超同家族 p90 阈值（MaybeLearnLatencyDegradation）。竞速败出/被取消分支不学习。
- **判定与恢复**：连续慢证据 streak ≥ 3 判劣化（单次是抖动）；任一快样本（首字 < 阈值一半）乐观翻转清零。
- **消费（软降权，非硬排除——延迟差不是能力缺失）**：`ScoringCandidate.LatencyDegraded` → calcPenalty 叠加 −15（介于 degraded −5 与 limited −20 之间）；竞速影子候选排除学习过的慢组合。taskClass 精确桶优先，unknown 桶对任意任务类生效（全量记录）。
- 观测：`[Latency-Learn]`（首次达阈值）/`[Latency-Recover]`（翻转）各一行。

## 与 kiro.rs 的刻意偏离

1. 只做首字/完成阶段竞速，无响应头阶段（CCX preflight 闸门已覆盖主要收益）。
2. 影子用独立完整超时窗口（CCX preflight 超时是 per-attempt 口径，共享剩余窗口会让 5s 短超时渠道竞速失效）。
3. 候选是五元组绑定（跨渠道+同渠道换绑定），非 kiro 的同模型换凭据。
4. 无 SQLite 分钟桶预热（重启后 20 样本内走 floor）；样本记流式败者（消赢家偏差）。
