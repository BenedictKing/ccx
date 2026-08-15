# 火山 Agent Plan AFP 感知模型路由实施计划

> 状态：提案。本文仅定义实施边界与验证路径；不在本计划中直接修改路由策略或生产配置。

**目标：** 让 CCX 对火山 Agent Plan 的模型选择使用可审计的 AFP 实际抵扣成本，并把模型能力、质量与成本作为独立维度处理，避免把 `kimi-k3`、`glm-5.2` 和 DeepSeek 模型按粗粒度套餐类别混为同级候选。

**范围：** `backend-go/internal/config/`、`backend-go/internal/autopilot/`、相关管理 API、Autopilot 路由诊断与中文文档。

**非目标：** 修改用户显式 `modelMapping`、绕过 `X-Channel`/会话 override、以 AFP 定价推断模型质量、把 Agent Plan 的 AFP 规则擅自应用到未经核验的 Coding Plan 计费单位，或自动改写已有渠道优先级。

## 目录

1. [背景、事实源与验收标准](#1-背景事实源与验收标准)
2. [现状、问题与设计不变量](#2-现状问题与设计不变量)
3. [AFP 成本模型与活动规则](#3-afp-成本模型与活动规则)
4. [模型能力、质量与路由决策](#4-模型能力质量与路由决策)
5. [实现分层与数据流](#5-实现分层与数据流)
6. [分任务实施步骤](#6-分任务实施步骤)
7. [测试、灰度与回滚](#7-测试灰度与回滚)
8. [风险、待确认项与提交范围](#8-风险待确认项与提交范围)

## 1. 背景、事实源与验收标准

### 1.1 业务触发

当前火山渠道只识别 Agent/Coding Plan 的套餐身份和窗口用量，未把 Agent Plan 的 AFP 抵扣规则带入模型选优。结果是路由会使用公共 USD 价格、渠道优先级或旧映射做判断，无法解释活动期 `glm-5.2` 的有效系数，也无法区分 `kimi-k3` 与 `glm-5.2` 的显著成本差异。

本计划以用户提供的火山官方套餐说明为事实源；实现前应把来源 URL、抓取日期和适用套餐写入内置数据注释，避免将临时活动误当成永久定价。

### 1.2 Agent Plan 的定价事实

文本模型单次 AFP 消耗为：

`AFP = (inputTokens × inputCoefficient + outputTokens × outputCoefficient) / 10_000`

输入系数按总输入长度分段：`≤32k` 使用模型基础系数的 `0.67`，`(32k, 128k]` 使用 `1`，`>128k` 使用 `2`；输出系数不分段。所有时间以 `Asia/Shanghai` 解释。

| 模型 | 基础输入/输出系数 | 本计划时点（2026-07-24）的有效系数 | 备注 |
|---|---:|---:|---|
| `deepseek-v4-flash` | `0.5 / 0.5` | `0.5 / 0.5` | 低成本标准模型，无活动倍率。 |
| `glm-5.2`、`glm-latest` | `4.5 / 4.5` | `1.125 / 1.125` | `×0.25` 活动至 2026-08-08 23:59:59。 |
| `deepseek-v4-pro` | `5.5 / 5.5` | `5.5 / 5.5` | `×0.4` 活动已于 2026-07-15 00:00:00 结束。 |
| `kimi-k2.6` | `4.5 / 4.5` | `4.5 / 4.5` | `×0.4` 活动已结束。 |
| `kimi-k2.7-code` | `4.5 / 4.5` | `4.5 / 4.5` | `×0.25` 活动已结束。 |
| `kimi-k3` | `10 / 10` | `10 / 10` | 无本轮指定模型活动；基础成本约为 GLM 基础成本的 2.22 倍。 |

截至 2026-08-15，上表中的指定模型折扣均已结束。另有 Auto 模式活动自 2026-06-10 18:00:00 至 2026-11-08 23:59:59（Asia/Shanghai）仍有效：Agent Plan 的 Auto 抵扣系数为 `0.5`，可由火山上游路由到 `kimi-k3`，且 00:00–08:00 会提高其路由比例；Coding Plan 的 Auto 抵扣系数为 `1`。Auto 是火山上游的动态调度模式，不是具有固定基础 AFP 系数的模型；CCX 不将其加入静态 `ResolveVolcengineAFPCost` 目录，也不模拟官方未公布的夜间路由比例。

### 1.3 验收标准

| 领域 | 完成定义 |
|---|---|
| 计费正确性 | 给定模型、输入长度、输出 token、套餐类型与请求时刻，能确定性算出 AFP；边界时刻自动恢复基础系数。 |
| 路由正确性 | 同一 Agent Plan 配额池内以 AFP 比较成本；不会把 AFP 数值直接与其他供应商 USD 数值混算。 |
| 模型区分 | `kimi-k3` 与 `glm-5.2` 具有独立模型画像、质量证据与成本记录；不存在“同一进阶档即等价”的捷径。固定 `QualityTier` 只承担兼容性能力下界，不再直接代表最终选模等级。 |
| 边界分层 | 在同一质量证据口径与成本可比作用域内构造模型能力—成本 Pareto 边界；边界微簇数量由数据决定、不设全局档数上限，再为 `quality_first`、`balanced`、`cost_first` 三种倾向分别物化 3–5 个首选/回退阶段。 |
| 体验兜底 | 当前目标档没有健康且满足硬能力的候选时，按候选阶梯自动选择相邻可用档；不得仅因目标档空缺而拒绝本可完成的请求。 |
| 可解释性 | dry-run、Routing Trace 和诊断输出能说明基础系数、活动规则、有效系数、估算 AFP 与未采用原因。 |
| 安全性 | 显式模型映射、手动渠道、协议/能力/上下文约束和 failover 语义保持优先，AFP 数据缺失时 fail-open 到既有策略。 |

## 2. 现状、问题与设计不变量

### 2.1 现有实现边界

- `config.ProviderTemplate` 已有 `ModelCostMultipliers` 与 `ModelQualityPriorities` 扩展点，但火山 `volcengine` 模板尚未填充两者；Compshare 的静态倍率不能复用为火山事实。
- `SmartRouter.buildChannelEntry` 用模型注册表的公共 USD 定价和 `ProviderTimePricingMultiplier` 构造 `EstimatedCost`；后者只有 provider 级起始时间和每日窗口，不能表达模型、套餐、结束时间或输入分段。
- `ModelResolver.rankEligibleModels` 仅在可比较质量候选之间使用 provider 静态倍率；它同样没有 AFP、活动日历或请求输入长度。
- `VolcengineAccessKeyPair.Plan` 已持久化 `agent_plan` / `coding_plan`，且 `VolcenginePlanUsage` 已提供套餐窗口余量，是识别成本比较作用域的可用事实，但并不包含模型抵扣规则。
- `TaskClassWorker` 的默认评分偏向速度、成本与节省；因此未建模 AFP 时，子代理可被旧渠道优先级或错误成本估计导向 DeepSeek。

### 2.2 根因与影响

1. 目前没有“基础 AFP 系数 × 输入分段 × 活动倍率”的统一计算器。
2. 现有 `CostTier` 过粗，不能表示 `deepseek-v4-flash`、活动期 GLM、`deepseek-v4-pro` 和 `kimi-k3` 的真实相对消耗。
3. 公共 USD 价格、AFP 订阅配额和其他供应商套餐的单位不同；直接把数值放入一个排序公式会制造伪精度。
4. 计费分类“进阶”不是质量排序。价格可证明成本不同，不能单独证明 K3 的能力恰为 GLM 的两倍；质量排序必须由能力声明、探测与可审计的人工证据共同决定。
5. 固定四档 `QualityTier` 无法表达同一模型在不同 effort、任务域、成本口径下形成的多个能力—成本点，也无法为三种成本倾向提供足够细的 fallback 梯度。

### 2.3 设计不变量

- AFP 只在确认同一火山 Agent Plan 配额作用域的候选之间作为可比成本；跨 provider 或跨未知套餐仅记录，不用裸 AFP 覆盖既有排序。
- `deepseek-v4-flash` 的 AFP 更低并不自动输给 GLM；`kimi-k3` 更贵也不自动被禁止。质量目标、上下文、工具、推理和用户显式意图先完成硬约束过滤。
- 能力—成本分层只能在质量证据可比较且成本单位、配额 scope 一致的候选内进行；不同 benchmark 来源或不同成本 scope 不计算共同 Pareto 边界。
- 每个倾向生成的是请求级 `CandidateLadder`，不是给模型永久贴一个全局等级。相同模型可因任务域、effort、输入长度和活动价格进入不同边界微簇与回退阶段。
- 自动降级只放宽软质量目标和成本偏好，不放宽上下文、协议、视觉、工具调用、推理、模型授权与健康等硬约束；必要时允许升级成本以优先保证请求完成。
- 价格活动是数据，不是手工调整渠道 `Priority` 的替代品；活动开始/结束必须由时钟自动生效和回退。
- 活动数据失效、未知模型、未知输入 token 或无套餐身份时不报零成本，不阻断请求，回退到现有公共定价/排序并在 trace 中标记原因。
- Coding Plan 的活动对象虽与 Agent Plan 同属官方活动，但本计划不假设其使用 AFP；未取得其独立计费公式前，只保留通用活动元数据，不将 Agent Plan AFP 套入 Coding Plan。

## 3. AFP 成本模型与活动规则

### 3.1 单一、可版本化的政策目录

在 `backend-go/internal/config/` 新建火山套餐定价目录，而不是把值散落在 `ProviderTemplate`、路由器和前端预设中。目录至少包含：

```go
type VolcengineAFPModelRule struct {
    RuleID, Plan, SourceURL string
    ModelIDs                []string // 精确 ID；glm-latest 作为 glm-5.2 的显式别名
    InputBase, OutputBase   AFPScaledCoefficient
    Promotions              []AFPPromotionRule
}
type AFPPromotionRule struct {
    PromotionID string
    StartsAt, EndsAtExclusive time.Time // Asia/Shanghai，[start, end)
    Multiplier AFPScaledCoefficient
    SourceURL string
}
```

系数使用固定精度整数（建议 1,000,000 分之一 AFP）而不是浮点累积；`4.5 × 0.67 × 0.25` 必须稳定得到 `0.75375`。目录只包含官方已确认数据、来源和生效窗口，首版不自动抓取网页或在运行时联网更新。

### 3.2 解析与计算接口

新增纯函数 `ResolveVolcengineAFPCost(at, plan, modelID, inputTokens, outputTokens)`，返回：

- 是否命中已知 Agent Plan 规则、规则 ID、基础/活动系数、输入分段和活动 ID；
- 输入、输出与总 AFP 的固定精度结果；
- `CostConfidence` 与不可计算原因（未知 plan、未知模型、未知输入 token、未知输出 token）。

匹配顺序固定为：精确模型 ID/别名 -> 套餐类型 -> 输入分段 -> 在 `[startsAt, endsAtExclusive)` 内的活动倍率。官方展示为 `23:59:59` 的结束时刻在目录中规范化为下一秒的排他边界；`00:00:00` 的语义必须由正式页面复核后写入。多个活动重叠是启动期配置错误，不允许“最后一条覆盖”；无活动则乘以 `1`。历史已结束活动仍保留，供 trace 回放和边界测试，不能在活动结束后从目录删除。

### 3.3 请求前估算与请求后核算

路由前只使用请求画像中可安全获得的输入 token 估算，以及显式输出上限或已定义的输出预算；缺失任一值时保留系数向量但降低成本置信度，不能伪造 `0 AFP`。路由后使用上游返回/本地核算的实际输入和输出 token 计算实际 AFP，作为诊断与未来质量/成本证据，不反馈改写已完成请求。

`RequestProfile` 应扩展为“输入 token 估算、输出 token 预算、来源与置信度”，由所有协议入口统一构建。首版不引入 LLM 预测器；客户端显式 `max_tokens`、Scheduler 的上下文约束和已有本地 token 估算是唯一允许输入。

### 3.4 成本比较作用域

引入内部 `CostEvidence`：`{unit, scopeID, estimated, actual, confidence, source}`。同一 `agent_plan` 账号/套餐窗口内的候选使用 `unit=afp`、相同 `scopeID`，可以直接按 `estimated` 排序；公共按量渠道使用 `unit=usd`。不同单位或不同配额池不直接比较数值，只使用各自的健康、质量、用户优先级和已有的保守 fallback 规则。

可选的“AFP 货币化”必须以后续显式用户配置的每 AFP 价值为前提，不在首版隐式猜测。这样不会把“12 AFP”错误地当作“12 美元”，也不会让一个余额已耗尽的套餐因名义 AFP 更低而被强行选中。

### 3.5 关键样例

对 `100k` 输入、`10k` 输出的普通文本请求，活动期 Agent Plan 的预期值如下：

| 实际模型 | 预期 AFP | 路由含义 |
|---|---:|---|
| `deepseek-v4-flash` | `5.5` | 若能力足够，是成本最低的候选。 |
| `glm-5.2` | `12.375` | 活动期高性价比候选，但并不比 Flash 更低成本。 |
| `deepseek-v4-pro` | `60.5` | 其活动已结束，不能继续按折扣价排序。 |
| `kimi-k3` | `110` | 只能在其模型级质量/能力收益足以证明合理时升级使用。 |

该表是路由测试夹具，不是对外账单。真实计费仍以火山控制台为准；差异超过约定容差时只记录告警、暂停 AFP 成本影响，并回退既有策略。

## 4. 模型能力、质量与路由决策

### 4.1 `QualityTier` 降级为兼容性能力带

保留现有 `low / normal / high / premium` 仅用于配置兼容、最低质量下界和无 benchmark 时的保守 fallback；不再增加 `frontier / ultra / super` 等静态枚举来模拟连续能力差异。动态等级由请求级 `FrontierCluster` 表达。上下文、工具调用、推理、视觉和模型授权仍是独立硬能力，不能塞入 `QualityTier`。

真正选模使用模型级、任务域级的连续证据：总体能力分、coding/agentic/reasoning 等域分、置信度、证据 lane（`verified / provisional / heuristic`）、版本和复核时间。优先复用现有 `ModelBenchmarkProfile`、`DomainStrengthEvidence` 与 endpoint 实测，避免再造一套互相冲突的质量源。

原始 benchmark 记录只是 `BenchmarkObservation`，不能直接成为 Frontier 候选点。同一个模型在不同数据源、运行批次、上下文长度或采样设置下出现多个 pass@1 点时，必须先按 `benchmark + datasetVersion + metric + effort + taskDomain` 划分可比 cohort，在 cohort 内计算带置信区间的标准化结果，再按 canonical model + model version + effort + task domain 汇总为一个 `QualityEstimate`。禁止直接平均不同 benchmark 的原始 pass@1，也禁止让重复观测数量较多的模型获得额外权重。

不同 cohort 的 percentile/z-score 仍不天然可比，不能仅因都归一化到 `0..1` 就合并。首版为每个任务域选择一个版本化 primary cohort 构建正常 frontier，其他来源只用于置信度、冲突提示和 shadow 对照；只有存在足够共享锚点模型、完成单调校准且交叉验证误差低于阈值时，才允许生成跨来源的 latent quality score。

同一 cohort 内的重复运行使用带不确定性的稳健中位数/收缩估计；只有任务集、metric、effort 和采样设置一致时才允许加权均值。来源间结论冲突时降低置信度并保留分歧范围，不用一个看似精确的平均分掩盖冲突。

`kimi-k3`、`glm-5.2`、`deepseek-v4-pro` 和 `deepseek-v4-flash` 必须分别建档。AFP 成本只进入成本维度；`10` 对 `4.5` 或活动期 `1.125` 能证明成本不同，不能单独证明质量倍率。缺少可比 K3 证据时，将其标记为 provisional：不得因高价格获得更高质量位置，也不进入正常首选边界；但仍保留在 overflow/emergency fallback 中，避免它恰好是唯一满足硬能力的模型时阻断请求。

### 4.2 请求级能力—成本点

边界计算的最小单位不是模型名，而是请求级路由点：

```go
type FrontierPoint struct {
    CandidateID, CanonicalModel, ModelVersion, Effort string
    Domain                                           TaskDomain
    QualityScore, QualityConfidence                  float64
    QualityLow, QualityHigh                          float64
    Cost                                             CostEvidence
    EndpointCandidates                               []EndpointCandidateRef
    EvidenceVersion                                  string
}
```

同一模型的不同 model version、effort、活动价格、任务域和输入长度可以产生不同点；同一模型的多个 endpoint 不重复生成质量点，而是挂到 `EndpointCandidates` 供健康、容量、延迟和 failover 排序。只有 endpoint 存在持续、可复现的供应商质量差异时，才以带置信度的 provider adjustment 修正模型质量，不能把单次探测波动当作新等级。

图表中的平均成本、中位成本和成本范围用于离线分析与异常识别，不能替代运行时请求成本。Frontier 的横轴必须使用当前请求的输入估算、输出预算、cache 语义、effort、活动时刻和具体 quota scope 计算的 `expectedCostPerRequest`；历史平均/中位成本只能作为 token 预算未知时的低置信度先验，并必须在 trace 中标注。

先执行硬能力、授权、健康和证据新鲜度过滤，再按 `cost.unit + cost.scopeID + benchmarkCohort` 分组；只有同组点可以互相判断支配关系。公开按量渠道可在统一为 USD/task 且 benchmark cohort 相同后组成 USD 比较域；AFP 只能在同一套餐 quota scope 内组成 AFP 比较域，二者绝不合并。

质量更低且成本不低的点属于后续 Pareto rank，默认不进入首选边界，但只要满足硬能力就必须保留在完整候选图和 overflow fallback 中。Pareto 负责确定优先级，不负责删除最后可用的完成路径；endpoint/容量独立性可以把后续 rank 候选提前到相邻回退档。

支配关系必须考虑不确定性：只有候选 A 的质量置信下界不低于 B 的质量上界、成本不高于 B，且至少一个维度严格更优时，才允许以高置信度支配 B。区间重叠时不得删除 B，只能标记为 `uncertain_frontier` 并在档内通过成本、稳定性和延迟排序。

多个可比成本域组成 `FrontierForest`，而不是一条伪全局曲线。每个 AFP quota scope、统一 USD/task 域和未知成本域分别生成 frontier；只有用户提供显式兑换/预算策略时才能跨树比较成本。没有跨域价值配置时，先在各树内生成候选阶梯，再沿用既有用户优先级、质量证据和健康策略合并树间顺序，trace 必须标记 `costIncomparableAcrossScopes=true`。

三种倾向只在每棵可比树内部改变能力—成本取舍，不得借 `cost_first` 名义跨树猜测 AFP 与 USD 的相对价值。树间顺序固定遵循显式用户控制、现有 provider/channel 优先级、满足任务质量目标的证据和健康状态；若用户希望跨树按成本优化，必须先配置可审计的预算或兑换策略。

### 4.3 完整 Pareto 边界与三倾向候选阶梯

首版不采用随机初始化的运行时 K-Means，避免同一输入产生漂移等级。使用确定性的“非支配排序 + ε 近似去重 + 相邻差距微簇”：先按成本升序排列边界点，再根据归一化能力差、对数成本倍率差和质量置信区间重叠合并相邻近似点；任一差距超过当前 cohort 的稳健阈值（中位数 + MAD）即形成自然断点。样本过少时使用版本化的保守最小阈值，不从单次请求临时拟合。能力分和成本是聚类轴；稳定性、延迟、endpoint 独立性只用于簇内排序和可用性判断，不改变能力—成本边界。

`FrontierCluster` 数量由有效数据决定，不设 5 档上限；按能力由低到高赋予请求级序号 `F0...Fn`，序号仅在相同 `frontierVersion + requestDomain + costScope` 内有意义。完整候选图保留全部 Pareto rank；证据、价格或请求域变化时重新生成，不把动态簇写回模型的永久等级。

若某棵树内没有任何 verified/provisional 可比质量点，使用现有 `QualityTier`、硬能力满足度、用户顺序和健康状态构造 `cold_start` 保守阶梯；此时成本只在同 scope 且已知的候选间作末级 tie-break，不生成伪 Pareto 质量结论。随着证据补齐切换到正常 frontier，trace 记录模式变化。

质量证据归一化、cohort 汇总和自然断点阈值按 `evidenceVersion` 离线预计算；请求路径只做硬过滤、请求成本重算、支配关系更新和阶梯物化。结果按 `frontierVersion + requestDomain + costScope + inputBucket + outputBudgetBucket + promotionEpoch + capabilityMask` 缓存，活动切换、模型/能力证据变更、套餐 scope 变更时失效。缓存仅是性能优化，命中与否不得改变排序结果。

Autopilot 三种倾向分别生成自己的候选阶梯，而不是共用一条静态质量序列：

| 倾向 | 首选区域 | 目标档与 2–4 个相邻回退档 | 无首选候选时 |
|---|---|---|---|
| `quality_first` | 边界高能力端 | 能力收益、证据置信度优先，成本用于限制无意义升级 | 向相邻较低能力档逐级降级，同时保留硬能力。 |
| `balanced` | Pareto 膝点附近 | 优先单位成本带来的能力增益，再考虑稳定性与延迟 | 从最近效用档向两侧展开，先保质量再增加成本。 |
| `cost_first` | 满足最低能力的低成本端 | AFP/成本优先，但不得低于请求能力下界 | 允许向更高成本档升级以保证完成，不因便宜档不可用而失败。 |

每种倾向基于完整候选图物化一个 3–5 阶段的活动窗口：1 个目标档加 2–4 个有序回退档。窗口之外的微簇不丢弃，而是作为可继续展开的 `overflow fallback tail`；前 3–5 阶段全部不可用时继续沿当前倾向的效用顺序搜索，直到找到满足硬能力的候选或真正耗尽。

```go
type CandidateLadder struct {
    FrontierVersion string
    Lane            CostPreferenceMode
    Preferred       []LadderStage // 3–5 个活动阶段
    Overflow        []LadderStage // 完整候选图剩余的有序兜底尾部
}
```

每一档可以包含多个近似等价候选，档内先按健康、成功率、endpoint 独立性和延迟排序。模型数量不足时允许重复使用不同 endpoint 的同一模型，但不能为了凑满档数虚构质量差异；有效模型少于 3 个时阶梯自然缩短。`ProviderTemplate.ModelQualityPriorities` 只保留为证据完整时的最终确定性 tie-breaker，不再承担主要分层职责。

活动阶段优先保证 canonical model 多样性：同一模型的多个 benchmark observation 不占多个位置，同一 model/version/effort 的多个 endpoint 收拢到同一 `LadderStage`。只有 model version 或 effort 形成经证据确认的显著能力—成本差异，或没有其他满足硬能力的模型时，才允许同一 canonical model 出现在多个阶段；该重复必须在 trace 中解释。

### 4.4 质量收益与成本升级门槛

候选质量差异小于最小显著阈值、置信区间重叠或任一方证据不足时，视为同一能力收益带并选择 AFP 更低者。只有质量收益经过可比 benchmark 或稳定实测确认，且未超过当前倾向允许的 `maxCostUpgradeRatio`，才允许在正常选优阶段自动升级到显著更贵的点。

`maxCostUpgradeRatio` 是正常选优的软安全闸门，不是质量推断：例如活动期 K3 相对 GLM 约 `8.89×` AFP 时，除非任务域证据和当前倾向明确允许，否则不得只因同为 `premium` 或整数优先级更高而自动选择 K3。具体阈值由 shadow 数据校准并允许用户配置，首版不硬编码未经验证的业务数字。

体验优先的 emergency fallback 在所有倍率内候选都不可用时可以越过该软闸门，选择仍满足硬能力的最小额外成本候选，并记录 `cost_guardrail_overridden_for_availability`。只有用户显式配置的绝对预算、禁止模型/供应商或合规边界才是不可越过的硬成本约束。

为避免动态价格、测量噪声或分段边界造成模型抖动，已有会话在当前模型仍健康且仍处于同一活动阶段时保持粘性。只有新候选效用提升超过版本化 `switchMargin`、当前候选失效，或用户显式改变倾向/模型时才切换；活动结束不会中断在途请求，但下一请求若跨越成本档也必须满足迟滞规则。

### 4.5 自动降级与体验优先

对一个可自动解析的火山 Agent Plan 请求，先保留用户显式 `modelMapping`、`X-Channel`、会话 override、promotion 以及协议/能力硬约束，再从当前倾向的 `CandidateLadder` 选择目标档。目标档为空、全部 cooldown、配额不可用或 endpoint 失败时，按档位顺序继续尝试；一次逻辑请求内已经失败的候选不得重新进入。

降级顺序必须区分“软目标不足”和“硬能力不足”：

1. 先尝试同档其他 endpoint 或同等效用模型。
2. 再尝试相邻档，允许质量小幅下降或成本上升。
3. 只有质量下界导致无候选时，才逐档放宽软质量目标并记录 `quality_fallback`。
4. 上下文、视觉、工具、推理、协议和授权不足不可放宽；无满足硬能力的模型时走现有 Scheduler/failover 或返回真实能力错误。
5. AFP、质量证据或分层计算失败时不阻断请求，整个可比组成本维度退化为中性，禁止把未知成本当成免费或昂贵。
6. 自动跨模型 fallback 只允许发生在下游响应尚未提交时；流式内容、工具调用或其他有副作用的输出一旦已发送，不得透明重放到另一模型，必须保留原错误/中断语义。
7. 同一会话优先保持当前模型或同档 endpoint；正常切换需超过 `switchMargin`，健康/授权/硬能力失效则立即允许 fallback，不让粘性阻断可用性恢复。

策略含义如下：`deepseek-v4-flash` 可出现在低成本端多个倾向的后备档；活动期 `glm-5.2` 可能位于 balanced 膝点或长上下文高收益档；`kimi-k3` 是高成本候选，只有可审计的任务域收益或用户显式策略才能进入更靠前档位。主代理与子代理可使用不同阶梯，但不能仅凭 `subagent` 标签降低复杂任务的硬能力下界。

## 5. 实现分层与数据流

### 5.1 职责划分

| 层 | 计划变更 | 不负责 |
|---|---|---|
| `internal/config` | 内置 AFP 政策目录、计划作用域解析、固定精度计算 DTO、模型级质量元数据的校验与深拷贝。 | 发起火山计费请求、保存密钥或修改渠道排序。 |
| `internal/autopilot` | 提供与 provider 无关的 Frontier/Ladder 引擎，消费标准化质量证据和 `CostEvidence`；AFP 只是成本适配器之一。在 `ModelResolver`、`SmartRouter` 与 EndpointPolicy 复用同一决策证据。 | 取代 Scheduler 的硬约束与 endpoint failover。 |
| `internal/metrics` | 接收实际 token 用量并记录实际 AFP/偏差的安全指标。 | 将估算 AFP 当作火山官方账单。 |
| 管理 API / 前端 | 展示规则来源、活动状态、有效系数、成本置信度和 dry-run 解释。 | 直接编辑内置活动规则或回显凭证。 |

### 5.2 套餐作用域解析

新增只读 `ResolveVolcenginePlanScope(upstream, apiKeyConfig)`：优先通过 `AccountUID + CredentialUID` 查询自动托管账号中的 `VolcengineAccessKeyPair.Plan`，并区分 credential 身份与真正共享 AFP 的 quota scope。再用已验证的火山数据面 URL 作受限回退。返回稳定 `scopeID`、计划版本/层级、套餐状态、用量快照时间、活动资格和可比性；不返回 AK/SK、API Key 或原始 URL。

只有 `plan == agent_plan` 且套餐状态可用时返回 AFP 可比作用域。无法解析 credential、使用手工中转地址、用量过期或配额窗口已耗尽时，将 AFP 置信度降为不足并保留现有调度语义。

### 5.3 请求与结果数据流

`协议入口 -> RequestProfile/PricingSnapshot -> endpoint+model 候选展开 -> PlanScopeResolver/AFPCostResolver -> 硬能力过滤 -> Pareto 分层 -> 三倾向 CandidateLadder -> SmartRouter/EndpointPolicy -> Scheduler/failover -> attempt 级 token 统计 -> AFP outcome/Trace`

请求入口只冻结 `pricingEvaluatedAt`、政策版本、token 估算及其置信度；AFP 成本在实际 model、endpoint 和 credential 已知后按候选生成，不能把单个请求级成本误用于同渠道的多个 Key。所有候选共享入口时钟快照，避免活动边界前后不一致；在途请求跨过活动结束时仍使用入口快照，下一请求自动使用新规则。

### 5.4 可解释输出

在既有 dry-run 与 trace 候选字段中追加可选的安全摘要：`costUnit`、`costScope`（匿名稳定 ID）、`baseInputCoefficient`、`baseOutputCoefficient`、`inputSegment`、`promotionID`、`promotionApplied`、`estimatedAFP`、`computedAFP`、`costConfidence`、`pricingPolicyVersion`、`paretoRank`、`frontierBand`、`routingLane`、`fallbackStep` 和 `qualityEvidenceVersion`。不输出密钥、完整 BaseURL、账号名、控制台响应或请求正文。

当 Trace v2 实施计划先落地时，这些字段进入其 DTO 和持久化详情；若未落地，则先作为向后兼容的可选 JSON 字段写入当前 Trace，避免两项工作互相阻塞。

## 6. 分任务实施步骤

所有后端行为先写失败测试，再补实现；每个任务只提交该任务必要的文件。活动规则以编译期内置目录起步，后续更新必须同时更新来源、有效期和测试，不通过远程网页抓取静默改变运行时成本。

### Task 0：确认当前 DeepSeek 选路根因与优先级边界

**文件：** 只读检查现有 Routing Trace、Scheduler 诊断与脱敏配置；必要时只补诊断测试，不修改用户路由。

1. 对复现请求记录 `selectionReason`、请求/实际模型、渠道、endpoint、任务类和角色，确认是否命中 `manual_override`、`promotion_priority`、session affinity、显式 `modelMapping` 或 Autopilot 成本评分。
2. 明确 AFP 只影响 SmartRouter/自动模型解析路径；所有被显式控制面短路的请求在 trace 中记录 `afpBypassedReason`，避免把未生效误判为定价计算错误。
3. 若 DeepSeek 来自过期自动促销元数据而非用户显式意图，应另建配置修复任务；本计划不静默清除用户主动设置的 promotion 或 override。
4. 用固定夹具覆盖促销/override 优先、普通 SmartRouter 选路和 endpoint 级实际模型回填三条路径，作为后续 AFP 行为基线。

### Task 1：建立 Agent Plan AFP 政策与纯计算器

**文件：** 新建 `backend-go/internal/config/volcengine_afp_pricing.go`、`backend-go/internal/config/volcengine_afp_pricing_test.go`。

1. 为输入分段、固定精度系数、模型规则、活动窗口、计算结果和不可计算原因建立专用类型；禁止复用 `float64` 的 `ModelCostMultipliers` 表示账务事实。
2. 收录 Agent Plan 基础系数、`glm-latest` 别名和历史/当前活动窗口；每条规则附官方来源与最后核验日期。
3. 实现无副作用的 `ResolveVolcengineAFPCost`，返回基础与有效系数、活动命中、总 AFP 和置信度。
4. 先写表驱动测试：三档输入边界、零 token、超大 token、`glm-latest`、未知模型、活动开始/结束精确时刻、已结束的 Pro/Kimi 促销、整数溢出与稳定小数展示。

### Task 2：解析火山套餐作用域与可比较性

**文件：** 新建或修改 `backend-go/internal/config/volcengine_plan_scope.go`、对应 `_test.go`；必要时复用 `config_accounts.go` 的只读查询。

1. 通过 `AccountUID`、`CredentialUID` 和已绑定的火山套餐元数据解析 credential、真实 quota scope、`agent_plan` / `coding_plan`、个人版活动资格、`PlanTier`、状态与用量快照。
2. 仅在凭证关联缺失时，使用已知 `/api/plan`、`/api/coding` URL 作为低置信度 hint；不从渠道名称、API Key 或自由文本猜测套餐。
3. 建立同 scope、不同 scope、未知 scope、失效套餐、用量耗尽、多个 endpoint/key 的测试矩阵；断言返回值不会带出任何密钥或原始 URL。
4. Coding Plan 返回可识别但 `AFPComparable=false`，直到其独立计费规则被补充和测试覆盖。

### Task 3：构造请求快照与候选级成本证据

**文件：** 修改 `backend-go/internal/autopilot/request_profile*.go`、新增 `afp_cost_evidence.go` 及测试。

1. 在统一请求画像中增加输入 token 估算、输出预算、来源和置信度；首版只对文本生成操作生成 AFP 证据，图片、视频与向量保持各自计费边界。
2. 请求入口一次性生成 `PricingSnapshot`（政策版本、评估时间、token 预算），实际 AFP 结果必须在 model、endpoint 与 credential 已知后按候选生成。
3. 定义 `FrontierPoint`、`EndpointCandidateRef` 和可比较组键；不同 canonical model/version/effort/task domain、成本 scope 或有效成本必须产生独立 Frontier 点。同模型、同 scope、同成本的多个 endpoint/key 聚合为执行引用，不重复占据质量点或阶梯阶段。
4. 为显式 `max_tokens`、未知输出预算、分段边界估算不确定、流式/非流式、多个 endpoint/key 写测试，确保未知不会伪装成免费。

### Task 4：实现确定性 Pareto 分层与三倾向候选阶梯

**文件：** 新建 `backend-go/internal/autopilot/model_frontier.go`、`candidate_ladder.go` 及对应测试；修改 `model_resolver.go`、`smart_router.go`；按需收敛 `provider_templates.go` 的静态倍率职责。

1. 将原始 benchmark observation 按 cohort 归一化并按 canonical model/version/effort/domain 汇总为带置信区间的 `QualityEstimate`；缺少可比证据的点标记为 provisional，不得凭价格或静态档位支配其他模型，但必须进入 overflow/emergency fallback。
2. 在相同成本 scope 和 benchmark cohort 内实现带不确定性的非支配排序、ε 去重、稳健自然断点和不限量 `FrontierCluster`；输入相同必须得到相同完整候选图，并提供全体证据不足时的 `cold_start` 阶梯。
3. 为 `quality_first`、`balanced`、`cost_first` 分别从完整候选图物化 3–5 个活动阶段和有序 overflow tail；优先保证 canonical model 多样性，每档保留多个健康/容量 endpoint 后备，并记录 `paretoRank`、cluster、stage 与进入原因。
4. 实现显著质量收益与 `maxCostUpgradeRatio` 门控；K3/GLM、GLM/Flash、活动前后价格变化必须产生可解释且稳定的阶梯变化。

### Task 5：接入候选阶梯与体验优先自动降级

**文件：** 修改 `backend-go/internal/autopilot/model_resolver.go`、`smart_router.go`、`endpoint_policy.go` 及相关测试。

1. 让 ModelResolver、SmartRouter 和 EndpointPolicy 消费同一 `CandidateLadder`，禁止不同阶段重新计算活动状态、模型档位或成本顺序。
2. 先在 shadow 中输出推荐阶梯而不改变实际顺序；主动模式只在用户启用后应用目标档及档内排序。
3. 实现同档 endpoint 替换、相邻档 fallback、软质量逐档降级和成本向上兜底；硬能力、授权与显式用户意图始终不可放宽。
4. 写失败测试覆盖：目标档全 cooldown、低成本档模型无权访问、同渠道不同 plan Key、跨渠道 failover、已失败候选不重试、所有软质量档为空但仍有硬能力可用模型。
5. 未知/低置信度成本使整个可比组成本维度中性；不得把“有 AFP 数据”本身作为优先于未知候选的正向信号。

### Task 6：暴露诊断、计算 AFP 与可控启用

**文件：** 修改 `backend-go/internal/autopilot/handlers_dryrun.go`、`routing_trace*.go`、对应 API/前端类型与测试。

1. dry-run 返回 observation→quality estimate→frontier point→cluster→ladder stage 的安全证据链、AFP 摘要、跨来源/跨 scope 未比较原因和排序影响，便于在不发真实请求时复核规则。
2. 按上游 attempt 使用已报告 token 计算 `computedAFP`，并在 Trace/窗口指标中区分 `estimated`、`computed` 与 `usageUnavailable`；计量失败不影响响应，也不能记录为零消耗。
3. 增加两个正交开关并默认关闭：`frontierRoutingEnabled` 控制通用 Frontier/Ladder 是否影响选路，`afpCostRoutingEnabled` 只控制 AFP 成本适配器是否参与可比树。任一关闭都不得让另一方以不完整证据改变路由。

> **后续更新（2026-08-01）**：`frontierRoutingEnabled` 已废弃——Frontier/Ladder 选型默认启用（无开关、无 shadow），字段仅为 JSON 兼容保留；`afpCostRoutingEnabled` 维持原设计。下文 §7.2 上线顺序与 §7.3 回滚中涉及 `frontierRoutingEnabled=false` 的灰度/回滚步骤随之失效，Frontier 行为调整通过 `costPreference.mode`（balanced/quality_first/cost_first）进行。
4. `shadow` 只记录建议；`assist/auto` 必须经过第 7 节的观察门槛后才能启用。切换开关、证据版本或活动 epoch 时使缓存失效，但不改写用户配置和在途请求快照。
5. 管理端只展示只读活动状态、质量 cohort、Frontier 版本、缓存命中、会话迟滞和来源；不提供在线编辑官方倍率的入口，活动更新通过版本化代码/预设发布。

### Task 7：模型策略与文档更新

**文件：** 修改模型注册表/内置预设、`docs/` 中火山渠道说明及相关单测。

1. 明确“Agent Plan AFP”与“Coding Plan 未核验计费”边界，列出数据来源、活动结束时间和更新责任。
2. 将 `kimi-k3`、`glm-5.2`、`deepseek-v4-pro`、`deepseek-v4-flash` 的能力、质量证据和成本事实分开呈现；不再用“进阶”作为路由同义词。
3. 不在本任务自动改写用户已有映射。若要把主代理定向 K3、子代理/长上下文定向 GLM，另建经用户确认的角色专用渠道序列或 ManualRoutingIntent 变更。

## 7. 测试、灰度与回滚

### 7.1 分层测试矩阵

| 层级 | 验证内容 |
|---|---|
| 纯函数 | AFP 公式、固定精度、输入分段、别名、活动起止、历史回放、未知值和溢出。 |
| 配置 | 套餐 scope 解析、深拷贝、热重载、密钥脱敏、失效/耗尽套餐降置信度。 |
| Frontier/Ladder | observation 去重、同 cohort 归一化、跨来源不直算 Pareto、置信区间支配、FrontierForest、不限量确定性微簇、三倾向各 3–5 个活动阶段与 overflow tail、canonical model 多样性及成本升级门槛。 |
| Resolver/Router | 同 scope AFP 排序、跨 scope 不混算、同档 endpoint 替换、相邻档降级、成本向上兜底、手动映射与 fail-open。 |
| HTTP dry-run | 请求画像到完整候选阶梯、目标档、fallback 路径和未比较原因的安全响应；不发送上游请求。 |
| 请求生命周期 | 版本化缓存命中/失效等价性、会话 `switchMargin` 迟滞、attempt 级 token 与 computed AFP、重试/failover 聚合、streaming 终态、trace 写入失败与活动边界中的在途快照。 |
| 手工 smoke | 使用已授权的非生产测试凭证核对一个 GLM 活动期样本与火山控制台 AFP；不把凭证、原始响应或账号数据写入测试夹具。 |

### 7.2 上线顺序

1. 发布政策目录、计算器、Pareto/阶梯 dry-run 和 trace 字段，但保持 `frontierRoutingEnabled=false`、`afpCostRoutingEnabled=false`；确认所有现有请求不改变渠道选择。
2. 在 shadow 模式同时记录完整边界微簇、三倾向的目标档与 3–5 个活动阶段、overflow tail、实际选择和模拟 fallback，核查规则 ID、scope、输入分段、质量证据版本与未知原因。
3. 先只启用同档 endpoint 排序，再启用相邻档 fallback；质量降级和成本向上兜底分别设置观测门槛，避免一次发布改变全部行为。
4. 只有以下条件同时满足才对用户明确选定的单一作用域启用 assist：计算错误为零、阶梯确定性成立、硬能力零放宽、手动映射零回归、跨单位比较零发生、GLM/Pro/Flash/K3 样例与人工 AFP 核验一致。
5. 自动模式只在 shadow/assist 证据满足既有 Readiness/SLO 门槛后由用户显式启用；不把本轮活动作为全局自动重排的授权。

### 7.3 回滚

- `frontierRoutingEnabled=false` 必须立即恢复既有选路；`afpCostRoutingEnabled=false` 必须让 AFP 成本证据退化为“仅观测”。两者都不删除 trace、模型画像或原有策略。
- 发现来源数据变更、AFP 与控制台偏差超阈值、错误跨 scope 排序或手动路由受影响时，关闭该开关并记录政策版本/活动 ID。
- 过期活动修正应更新目录并发布新版本；不得通过修改历史 trace 或手工拖动渠道优先级掩盖问题。

### 7.4 完成前验证命令

```bash
git diff --check
cd "backend-go" && GOCACHE="/tmp/go-build" GOMODCACHE="/tmp/go-mod" go test ./internal/config ./internal/autopilot ./internal/scheduler
cd "backend-go" && GOCACHE="/tmp/go-build" GOMODCACHE="/tmp/go-mod" go vet ./...
make build
cd "backend-go" && make test
cd "frontend" && bun run build
```

真实火山 smoke 不属于默认 CI；仅由拥有对应测试套餐且明确授权的操作者运行。

## 8. 风险、待确认项与提交范围

### 8.1 风险控制

| 风险 | 控制措施 |
|---|---|
| 官方活动变更或提前结束 | 政策目录保留来源、核验日期和版本；默认关闭成本路由；活动更新走代码审查与边界测试。 |
| 把 AFP 当作跨供应商货币 | 以 `unit + scopeID` 强制隔离；缺少用户定义的兑换规则时禁止跨单位比较。 |
| 价格被误当作质量 | 模型级能力和质量证据独立维护；质量排序变更必须有探测/评审依据。 |
| 聚类结果随样本漂移 | 使用确定性非支配排序、政策版本和最小档位间距；同一输入的阶梯必须可复现，证据变化通过新版本生效。 |
| 自动降级损害体验 | 只逐档放宽软质量目标，硬能力永不放宽；trace 记录 fallback 路径，并以成功率、用户重试率和质量反馈作为灰度门槛。 |
| cost-first 无候选时请求失败 | 允许向更高成本档升级，优先保证请求完成；通过可配置最大升级倍率和预算告警限制意外消耗。 |
| 输出 token 估算不准 | 将估算与实际 AFP 分开；低置信度不影响排序；用控制台样本校准后再灰度。 |
| 临近活动边界的在途请求抖动 | 入口固定 `PricingSnapshot`，只让后续请求使用新活动状态。 |
| 规则泄露账号信息 | scope 仅使用服务器生成的匿名 ID；管理 API、trace、测试夹具都不含 AK/SK、API Key 或完整端点。 |

### 8.2 实施前待确认项

1. 由套餐所有者确认 Agent Plan AFP 表的官方来源、时区和活动结束时刻是否采用闭区间；如官方页面改动，以最新页面为准。
2. 获取 Coding Plan 的独立用量公式和基础系数前，不启用其 AFP 成本比较。
3. 为 `kimi-k3`、`glm-5.2` 等模型确认可复现的质量/能力证据与所需任务类，不以价格倍率代替评测。
4. 确认三种倾向的目标语义、每条阶梯期望档数、最小显著质量差和允许的成本升级倍率；首版默认由 shadow 数据提出建议，不凭主观命名硬编码模型等级。
5. 确认将 AFP 仅用于同一套餐 scope 的相对排序，还是未来引入经用户配置的 AFP 货币化预算；两种策略不得混合上线。
6. 确认 GLM 活动结束后是否需要保留历史 trace 的复算视图；本计划默认保留原政策版本，不重算历史决策。

### 8.3 事实源

- 火山 Agent/Coding Plan 指定模型抵扣系数活动：<https://www.volcengine.com/docs/82379/2533565>
- 火山 Agent Plan AFP 定义、公式与模型抵扣系数：以本计划讨论中提供的官方表为准，实施时补入可公开访问的正式文档 URL 与核验日期。
- CCX 当前事实源：`backend-go/internal/config/provider_templates.go`、`backend-go/internal/autopilot/model_resolver.go`、`backend-go/internal/autopilot/smart_router.go`、`backend-go/internal/config/config.go`。

### 8.4 提交范围

实施时按 Task 拆分提交，避免把 AFP 政策、选路语义、前端展示和用户路由策略混在同一个 commit。计划文档本身使用：

```bash
git add -- docs/superpowers/plans/2026-07-24-volcengine-afp-aware-routing.md
git commit -m "docs: plan volcengine AFP-aware routing"
```
