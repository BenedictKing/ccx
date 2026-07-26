# Autopilot 模型与思考强度联合路由实施计划

- 状态：草案
- 创建日期：2026-07-26
- 影响范围：`backend-go/internal/autopilot`、`backend-go/internal/config`、`backend-go/internal/providers`、`backend-go/internal/handlers/common`、Web 前端与 Desktop 前端渠道编辑
- 关联设计：[docs/design/channel-autopilot.md](../../design/channel-autopilot.md)
- 关联计划：[2026-07-24-volcengine-afp-aware-routing.md](2026-07-24-volcengine-afp-aware-routing.md)、[2026-07-23-autopilot-trace-rollout.md](2026-07-23-autopilot-trace-rollout.md)

## 目录

1. 背景、目标与验收标准
2. 现状、缺口与设计不变量
3. 评测证据模型：以 `model × effort` 为评测单元
4. 联合决策：候选枚举、筛选与排序
5. 决策落地：请求改写与协议适配
6. 手工映射表退役与升级迁移
7. 可观测性与 Trace 契约
8. 分任务实施步骤
9. 测试、灰度与回滚

## 1. 背景、目标与验收标准

### 1.1 业务触发

一条真实请求暴露了当前路由的语义断层：

```json
{
  "originalModel": "claude-sonnet-5",
  "model": "gpt-5.6-terra",
  "originalReasoningEffort": "none",
  "actualReasoningEffort": "none",
  "selectionReason": "priority_order"
}
```

模型被改写为 `gpt-5.6-terra`，但思考强度沿用了客户端的 `none`。不同模型在不同思考档位下的实际表现差异很大，`claude-sonnet-5` 的 `none` 与 `gpt-5.6-terra` 的 `none` 不是同一能力点。把模型换掉却保留原思考档位，等于把请求投放到一个从未被评测过的能力点上。

### 1.2 本轮交付目标

1. 自动路由的决策单元从「模型」提升为「模型 + 思考强度」的不可分割组合。
2. 内部评测证据以 `canonical_model × effort × task_domain` 为键组织，使同一模型的不同思考档位成为可比较的独立能力点。
3. 决定目标模型的同时决定目标思考强度，并按上游协议派系正确注入请求。
4. 渠道级手工 `modelMapping` / `reasoningMapping` 在升级迁移中整体退役，不保留为运行期 fallback。
5. 客户端原始思考强度降级为「记录 + 可配置策略」，不再默认作为硬约束。

### 1.3 验收标准

- 对同一请求，`model` 与 `actualReasoningEffort` 必须来自同一次联合决策；不存在「模型自动选、effort 透传」的混合结果。
- 目标模型不支持思考控制时，不注入任何思考参数，且该组合在候选枚举阶段即被折叠为单一默认变体。
- `RespectClientThinking` 开启时，客户端显式思考强度作为上限生效；关闭时完全由 Autopilot 决定。
- 升级后任意渠道的 `modelMapping` / `reasoningMapping` 均为空，且自动路由质量不低于迁移前基线。
- Trace 中可解释「为何选择该 effort」，包含所用评测证据来源与置信度。
- 客户端显式 `none` 时，`actualReasoningEffort` 不得出现非 `none`/`off`/空值的档位，账单不因自动决策翻倍。
- `ReasoningEffort.Enabled=false` 时，`effortDecisionSource` 必须为 `passthrough`；画像缺失导致候选折叠时，Trace 中必须有明确的回退原因字段。
- 单次路由决策 P99 延迟不超过当前基线的 150%；`ExpandVariants=true` 时候选数增长不超过 4 倍（由 `SupportedEffortLevels` 长度控制）。

## 2. 现状、缺口与设计不变量

### 2.1 已有基础设施

以下字段与配置已存在，本轮是接通而非新建：

| 能力 | 位置 | 现状 |
| --- | --- | --- |
| 统一思考档位枚举 `EffortLevel` | `autopilot/model_profile.go:80` | 已定义 off/minimal/low/medium/high/max |
| 模型思考控制能力声明 | `autopilot/model_profile.go:341` | `SupportsEffortControl`、`SupportedEffortLevels` 已有，选型不读取 |
| 档位质量加分表 | `autopilot/task_domain.go:493` | `effortBonusTable` 与 `EffortQualityBonus()` 已实现，无生产调用方 |
| 评测证据的采样档位 | `config/config.go:413` | `ModelBenchmarkEvidence.Effort` 已记录，匹配时不参与过滤 |
| 前沿点的档位维度 | `autopilot/model_frontier.go:18` | `FrontierPoint.Effort` 已有，`ComputeFrontierForest` 无生产调用方 |
| 档位展开配置 | `config/autopilot_config.go:291` | `ReasoningEffortConfig` 四个字段与默认值已就位，无读取方 |

`ReasoningEffortConfig` 的默认值已经描述了期望语义（`config/autopilot_config.go:528`）：`supervisor` 用 `high`/`max`，`worker` 用 `medium`，`lightweight` 用 `off`/`minimal`，`long_context` 用 `medium`/`high`。这套默认值当前完全没有生效。

### 2.2 需要消除的缺口

1. **决策只产出模型。** `ModelResolver.ResolveModel()`（`autopilot/model_resolver.go:105`）返回单个模型名，经 `EndpointAttemptPolicy.ResolvedModelByEndpointUID`（`autopilot/endpoint_policy.go:103`）传出，没有并行的 effort 出口。
2. **请求改写只改模型。** `handlers/common/upstream_failover.go:475` 用 `sjson.SetBytes(attemptBody, "model", mm)` 覆写模型，思考参数原样保留。
3. **effort 仍由渠道手工表决定。** `config.ResolveReasoningEffort()`（`config/config_utils.go:191`）按原始模型名查 `ReasoningMapping`，在 provider 层注入（如 `providers/claude.go:770`）。这条路径与自动选型互不知情。
4. **手工映射优先级高于自动决策。** `resolveMappedModel()`（`autopilot/endpoint_policy.go:787`）先查 endpoint 显式 `ModelMapping` 再回落自动解析，与新方案的优先级相反。
5. **请求特征不携带思考强度。** `RequestProfile`（`autopilot/request_profile.go:7`）只有布尔 `ReasoningNeed`，`CapabilityFloor` 只有 `NeedsReasoning`，客户端 effort 从未进入决策上下文。
6. **评测数据未按档位分层。** 现有证据中同一模型在同一 benchmark 源下没有多档 effort 对照，`resolveRelativeBenchmarkEvidence()`（`autopilot/task_domain.go:366`）仅按 `Domain` 匹配。

### 2.3 设计不变量

- **决策原子性**：模型与 effort 必须一起产生、一起落地、一起记录。任何只改其一的路径视为缺陷。
- **能力真实性**：只在模型声明支持思考控制且档位在 `SupportedEffortLevels` 内时才注入该档位，不伪造上游不接受的参数。
- **证据有界性**：评测证据只做有界软修正，不把某个 harness 的原始指标当作绝对能力值。沿用 `relativeBenchmarkMaxDelta` 的既有约束思路。
- **Fail-open**：证据缺失、画像缺失或配置关闭时，回退到「不改写 effort」而非猜测档位，并在 Trace 中标注回退原因。
- **可解释性**：每个决策必须能回答「选了哪个组合、依据哪条证据、置信度多少、是否被客户端上限截断」。

### 2.4 需额外覆盖的交互子系统

**ManualRoutingIntent。** 仓库已有手动意图覆盖机制（`autopilot/manual_routing_intent.go`），删除手工表后它是唯一的显式覆盖通道。必须明确：

- 手动意图指定了模型但未指定 effort 时，由 Autopilot 补选 effort；指定了 `MappedModel` 时，`MappedModel` 优先于 `ResolvedRouteTarget.Model`。
- 手动意图指定 `max` 档位时，仍受 `maxTrafficPercent` 成本护栏约束，不能绕过。
- 手动意图的优先级高于自动决策，但低于客户端显式 `none` 的硬约束（客户端明确关思考时，手动意图不得覆盖）。

**Images 与 Vectors 渠道。** 这两类端点不接受思考参数，`ReasoningNeed` 始终为 false。计划中它们**只参与模型选择**，跳过 effort 候选枚举，保留现有行为不变。实施时不改造 images/vectors 的 provider 注入路径。

**Gemini 派系。** Gemini 的思考参数是 `generationConfig.thinkingConfig.thinkingLevel`，协议路径与 Claude/OpenAI/Responses 完全不同。Gemini 渠道参与联合决策，但注入路径独立：在 `handlers/gemini/` 的请求构造环节写入 `thinkingConfig`，而非走 `ApplyReasoningParamStyle`。`reasoning_log.go` 的 Gemini 提取路径已存在，仅需对齐归一化命名。

**请求缓存键。** 当联合决策对同一原始请求在不同 endpoint 上决定不同 effort 时，缓存键必须包含 effort 维度（prompt hash + effort），否则"第一次选 `high` 并缓存响应，第二次命中缓存但系统选了 `medium`"会导致不一致。若系统当前无此缓存，此项列为设计约束而非实施项。

**Key 级模型权限约束。** `EXTRA_PROXY_ACCESS_KEYS` 支持不同 key 有不同模型访问权限。自动决策改写模型后，如果新模型不在该 key 的权限范围内，请求会 403。候选枚举阶段需要感知 key 级别的模型权限约束，将无权限的模型×effort 组合提前排除。

**Desktop Agent 配置同步。** Desktop 已保存的 Agent 配置快照中包含 `modelMapping`/`reasoningMapping`，backend 迁移后这些快照不会被自动清除。`desktop/internal/autopilotagent/` 的 re-sync 逻辑需要在迁移后跳过已废弃字段，避免幽灵配置回写。Desktop "Agent 配置到本地 CCX" 流程在配置结构变更后需验证不 break。

## 3. 评测证据模型：以 `model × effort` 为评测单元

### 3.1 证据键的提升

当前证据匹配只用 `Domain`，导致 `effort=max` 采集的分数被用来解释任意档位。证据键提升为四元组：

```text
canonical_model + effort + task_domain + benchmark/benchmarkVersion
```

`ModelBenchmarkEvidence.Effort` 字段已存在，无需改结构；改的是 `resolveRelativeBenchmarkEvidence()` 的匹配与选择逻辑：先按 `(domain, effort)` 精确匹配，再按同域跨档位回退，回退时必须降低置信度并标注回退距离。

### 3.2 档位归一化

各厂商档位命名不统一（`none`/`off`、`xhigh`/`ultra`/`max`）。引入单一归一化入口，把证据侧、能力侧、请求侧三处命名统一到 `EffortLevel` 枚举：

- 证据采集值（如 `ultra`、`default`）归一到最近的枚举档位；`default` 视为「未声明档位」，不能作为任一具体档位的精确证据。
- 上游能力侧沿用 `NormalizeReasoningEffortForUpstream()`（`config/config_utils.go:218`）的收敛思路，在注入前做派系收敛。
- 归一化必须是纯函数且可测，不同输入到同一档位的映射保持确定。

### 3.3 缺档位证据时的推导

同一模型缺少某档位的直接证据时，不允许凭空造分。按以下优先级推导，且每级都携带更低置信度：

1. **同模型同域相邻档位**：用相邻档位证据加上 `EffortQualityBonus()` 的档位差做有界修正。
2. **同模型跨域**：用该模型其他域的相对位置加家族先验。
3. **家族先验**：回退到 `seedDomainMatrix`（`autopilot/task_domain.go:17`）。
4. **无证据**：不参与档位排序，仅保留模型默认变体。

`EffortQualityBonus()` 在此正式接入：它提供档位间的单调质量差，用于步骤 1 的插值，而不是直接作为绝对分数。

### 3.4 成本的档位敏感性

高思考档位通常显著增加输出 token，成本必须按档位估算而非按模型估算。候选点的成本证据沿用 `CostEvidence`，但估算输出 token 时引入档位系数；系数缺失时标注低置信度，并让成本维度在排序中降权，避免用假成本压制高质量档位。

档位系数的来源：由内部评测中同一模型不同档位的实际输出 token 统计得出，落库到 `ModelBenchmarkProfile` 的扩展字段中。评测数据不存在时，不猜测系数，而是在成本维度标注低置信度，让排序权重自动降到最低，避免用假成本压制高质量档位。灰度期可先用保守默认系数（如 `high=1.5×`、`max=2.0×`），待实测数据积累后替换。

## 4. 联合决策：候选枚举、筛选与排序

### 4.1 决策对象

自动解析的返回值从模型名提升为组合对象，作为贯穿全链路的唯一载体：

```go
// ResolvedRouteTarget 是一次自动路由决策的原子结果。
type ResolvedRouteTarget struct {
    Model         string      // 目标模型 ID
    Effort        EffortLevel // 目标思考档位；空串表示不改写
    EffortDecided bool        // 是否由 Autopilot 决定档位
    Reason        string      // 决策原因，用于 Trace 与日志
}
```

`EffortDecided=false` 且 `Effort` 为空是合法的 fail-open 结果，表示「模型可能被改写，但思考参数保持原样」。

### 4.2 请求侧上下文扩展

`RequestProfile` 增补客户端原始档位，仅作为策略输入与日志事实，不作为默认硬约束：

- `ClientEffort EffortLevel`：客户端显式声明的档位，未声明为空。
- `ClientEffortExplicit bool`：区分「显式 none」与「未声明」。这两者语义不同，前者是用户意图，后者只是协议默认。

`CapabilityFloor` 增补档位下界，由 `ReasoningEffortConfig.PerTaskClass` 按 `TaskClass` 推导，使 `supervisor` 类请求不会被选到 `off` 档位。

### 4.3 候选枚举

对每个通过能力硬过滤的 endpoint 模型，枚举其可用档位组合：

1. 模型 `SupportsEffortControl=false` 时，只产生一个「默认变体」组合，`Effort` 为空。
2. 支持控制时，候选档位取 `SupportedEffortLevels` 与 `PerTaskClass[taskClass]` 的交集；交集为空时回退到 `SupportedEffortLevels` 内不低于档位下界的最小档位。
3. `ExpandVariants=false` 时不展开多档位，只取该任务类的首选档位，用于降低计算量与灰度期风险。

### 4.4 客户端上限语义

`RespectClientThinking=true` 时，`ClientEffortExplicit=true` 的请求以 `ClientEffort` 为**上限**而非等值约束：客户端要 `high`，系统可选 `medium`，不可选 `max`。显式 `none` 是最强约束，直接折叠为不开思考。

`RespectClientThinking=false` 时完全忽略客户端档位，仅记录到 `originalReasoningEffort`。默认值当前为 `true`（`config/autopilot_config.go:531`），灰度期保持不变。

### 4.5 排序

在既有 `rankEligibleModels()` 的比较链上扩展，而不是新建一套评分体系。关键约束是避免「档位膨胀」——高档位天然质量分更高，若不加约束会一律选 `max`：

1. 先按质量下界满足与否分层，只在满足 `QualityTarget` 的最低档位集合内比较。
2. 同质量层内优先低成本、低延迟档位，即「够用即止」。
3. 质量收益必须超过成本升级门槛才允许升档，沿用 AFP 计划中「质量收益与成本升级门槛」的既有思路。
4. 证据置信度低的高档位不得压制置信度高的中档位。

**Frontier/CandidateLadder 的定位。** `FrontierPoint.Effort` 和 `ComputeFrontierForest` 已存在但无生产调用方。本轮**不启用** Frontier/CandidateLadder 路径：它适合多候选的 Pareto 分层场景，但联合决策的候选数仍在可控范围内（endpoint 数 × 档位数），直接在 `rankEligibleModels()` 内扩展比较链更简单、更可测。`FrontierPoint.Effort` 字段保留在数据结构中但不参与本轮排序，留待后续多模型多档位大规模候选场景再启用。

## 5. 决策落地：请求改写与协议适配

### 5.1 出口扩展

`EndpointAttemptPolicy` 增补组合出口，与既有 `ResolvedModelByEndpointUID` 并列：

```go
// ResolvedTargetByEndpointUID 返回 endpointUID 的自动路由决策（模型 + 档位）。
ResolvedTargetByEndpointUID func(endpointUID string) *ResolvedRouteTarget
```

保留 `ResolvedModelByEndpointUID` 作为过渡期兼容出口，实现上从同一份 `targetByUID` 派生，避免两个真值源。灰度结束后移除旧出口。

### 5.2 改写点

`handlers/common/upstream_failover.go:475` 的改写块扩展为一次原子改写：确定 endpoint 后取 `ResolvedRouteTarget`，同时写入 `model` 与思考参数。两者要么都写入成功，要么都不写入——`sjson` 任一步失败即整体放弃本次改写并保持原始 body，防止出现「模型改了、档位没改」的中间态。

改写后同步更新 `attemptModel` 与用于日志的实际档位，使 `actualReasoningEffort` 反映真实发出的值。

### 5.3 协议适配

思考参数的写入形态按渠道派系区分，复用既有能力而非重写：

- 统一入口沿用 `config.ApplyReasoningParamStyle()`（`config/config_utils.go:264`），它已覆盖 `thinking` / `reasoning_effort` / `reasoning` 三种形态。
- Claude 派系的 `thinking.effort` 细节沿用 `providers/claude.go:85` 的 `applyClaudeThinkingEffort()` 逻辑，包括清理互斥字段 `reasoning`、`reasoning_effort`、`output_config.effort`。
- `ThinkingMode=adaptive_only` 的模型不接受手动 enabled/disabled，此类模型在候选枚举阶段就应折叠为默认变体，不进入档位展开。

**`adaptive_only` 在新改写点的守卫。** 当前 `applyClaudeReasoningEffort` 在 `ThinkingMode=adaptive_only` 时直接 `return`（`providers/claude.go:189`），但如果 autopilot 的 effort 改写从 `upstream_failover.go` 发起而非 provider 层发起，这个守卫逻辑需要被复制到新改写点。实施时在 `upstream_failover.go` 的原子改写块中增加 `adaptive_only` 守卫：若目标模型的 `ThinkingMode=adaptive_only`，则跳过思考参数注入，仅改写模型。守卫逻辑调用 `config.ResolveUpstreamCapability()` 获取 `ThinkingMode`，与 `applyProviderQualityReasoningControl`（`provider_quality_protocol.go:187`）的判断方式一致。

注意 `ApplyReasoningParamStyle` 的 `thinking` 分支当前只写 `type: enabled` 而不写 `effort`（`config/config_utils.go:280`），这与 Claude 派系需要的 `effort` 字段不一致。本轮需要统一这两处行为，使联合决策的档位真正落到请求体。

### 5.4 与 provider 层的关系

provider 层不再承担「决定档位」的职责，只负责协议形态适配。原先由 `ResolveReasoningEffort()` 驱动的注入路径随手工表退役一并移除，避免 provider 层与 autopilot 层对同一字段的双重写入。

**字段清理职责归属。** `applyClaudeThinkingEffort()`（`providers/claude.go:85`）中的字段清理（删除 `reasoning`、`reasoning_effort`、`output_config.effort`）是 Claude 派系特有的互斥约束，而非"决定档位"。新路径中这些清理逻辑**仍由 provider 层负责**：`upstream_failover.go` 的原子改写写入思考参数后，provider 层在后续处理中执行字段清理。这样职责边界清晰：autopilot 决定"写什么"，provider 层保证"写成上游接受的形态"。

## 6. 手工映射表退役与升级迁移

### 6.0 评测证据数据的格式迁移

已有评测证据键是 `canonical_model + domain + benchmark`，需要升级为 `canonical_model + effort + domain + benchmark`。迁移策略：

- **旧证据标记为 `effort=unknown`**，不丢弃。匹配时按 §3.3 的缺证据规则回退处理，置信度自动降一档。这样既保留了历史数据的价值，又不会让无档位信息的证据被当作精确证据。
- **不丢弃旧证据**，避免评测数据真空期。待新格式证据逐步采集后，`effort=unknown` 的旧证据自然被替代。
- 证据迁移与配置迁移独立执行，不耦合在同一 Task 中。证据迁移是纯数据格式变更，可提前执行且完全可逆。

### 6.1 退役范围

- 渠道配置的 `ModelMapping`、`ReasoningMapping`（`config/config.go:40`、`config/config.go:45`）。
- endpoint 画像的显式 `ModelMapping` 及 `resolveMappedModel()` 中的优先级分支（`autopilot/endpoint_policy.go:794`）。
- 六类渠道的映射更新端点与 `UpdateModelMapping` 系列函数（`config_messages.go`、`config_gemini.go`、`config_vectors.go` 等）。
- 前端映射编辑 UI（`frontend/src/components/edit-channel/ModelMappingSection.vue`）与 Desktop 对应实现。
- Desktop 渠道预设中的 `ModelMapping` / `ReasoningMapping` 字段（`desktop/internal/channelpreset/generated_*_presets.go`）。

`ReasoningParamStyle` **不退役**：它描述上游协议形态，属于客观能力事实，仍由联合决策的注入环节使用。

### 6.2 迁移顺序

顺序不能颠倒，否则会出现「手工表已删、自动决策还没有画像可用」的空窗：

1. 联合决策与请求改写落地，`ReasoningEffortConfig.Enabled` 保持关闭。
2. 以 shadow 模式运行，记录「若启用会选什么组合」，与现有手工表结果对照。
3. 对照通过后，按渠道分批启用自动决策，手工表仍在但优先级下调为最低。
4. 确认自动决策覆盖率与质量达标后，执行配置迁移删除手工表，并移除优先级分支代码。
5. 清理前端与预设中的映射编辑入口。

**Shadow 对照的评估标准。** 不是简单地"与手工表一致"，而是两条标准并行：

- **手工表已覆盖的场景**：自动决策的结果质量不劣于手工表结果（允许模型不同但质量分不低于）。
- **手工表未覆盖的场景**（现有 `reasoningMapping` 为空的映射行）：自动决策的结果质量显著优于"透传客户端 effort"的基线。这才是本次升级的核心价值所在。

**迁移粒度。** 逐渠道独立执行：每个渠道的 shadow 对照、优先级下调、配置迁移可独立完成，不等所有渠道都全量后再一次性迁移。这样单渠道的问题不会阻塞其他渠道的推进。

**手工表优先级下调需要独立代码变更。** §2.2 缺口 4 描述了 `resolveMappedModel()` 先查显式 `ModelMapping` 的优先级。要实现"下调为最低"，需要把自动决策的优先级提到显式 `ModelMapping` 之前，这是一个独立的代码变更，列入 Task 7 中作为显式步骤。中间态（手工表存在但优先级最低）的行为：当自动决策与手工表映射冲突时，以自动决策为准，手工表仅在自动决策 fail-open 时生效。

### 6.3 迁移前的覆盖率门槛

删除手工表前必须验证：目标渠道的每个 endpoint 都有可用模型画像，且画像来源不是纯 `builtin_registry` 猜测。原先存在 `ModelMapping` 的渠道会被 `auto_discovery.go:1309` 跳过自动写入 `SupportedModels`，这些渠道的画像可能长期缺失，必须在删除映射后重新触发发现与能力探测，并等待探测完成再启用自动决策。

### 6.4 配置迁移的安全性

配置迁移是破坏性变更，需满足：

- 迁移前自动备份 `.config/config.json`（已有 `.config/backups/` 机制）。
- 迁移写入带版本标记，可判断是否已迁移，避免重复执行。
- 提供只读的迁移预演，输出将被删除的映射条目清单，供人工确认后再执行。
- 迁移不可逆部分（删除用户手工配置）必须在发布说明中显式声明。

## 7. 可观测性与 Trace 契约

### 7.1 请求日志语义

`ChannelLog` 的两个既有字段语义收紧（`metrics/channel_log.go:19`）：

- `originalReasoningEffort`：客户端请求中的原始档位事实，保持现有提取逻辑（`handlers/common/reasoning_log.go:14`）。
- `actualReasoningEffort`：实际发往上游的档位，必须与联合决策结果一致。

新增字段用于区分「谁决定了档位」：

- `effortDecisionSource`：`autopilot` | `client` | `passthrough`，其中 `passthrough` 表示 fail-open 未改写。
- `effortClampedByClient`：布尔，标记是否被客户端上限截断。

预期迁移后本文开头那条请求变为 `actualReasoningEffort: "high"`、`effortDecisionSource: "autopilot"`。

### 7.2 路由 Trace

`CandidateScore.Dimension` 已预留 `effort` 维度（`autopilot/routing_trace.go:33`），本轮开始真实写入。候选记录需携带：候选的 `model × effort` 组合、该组合的质量分与置信区间、证据来源与回退级别、成本估算及其置信度、以及未被选中的原因（质量不足 / 成本门槛未过 / 客户端上限 / 档位不受支持）。

### 7.3 驾驶舱展示

沿用 Trace v2 的只读回放通道，不新增写路径。展示上把「模型」列扩展为「模型 + 档位」，并在候选对比中显示同一模型不同档位作为独立候选，使档位选择可被人工复核。

### 7.4 脱敏

档位与评测证据不含用户内容，可直接记录。但证据来源 URL 与采集时间属于外部引用，沿用既有 Trace 脱敏策略，不因新增字段放宽边界。

## 8. 分任务实施步骤

任务按依赖排序。Task 1~3 是纯计算与纯数据层，可独立测试；Task 4 起接触请求链路。

### Task 1：档位归一化与证据键提升

> **已完成**（提交 `62fe9189`）。`autopilot/effort_normalize.go` 提供 `NormalizeEffortLevel`/`EffortLevelOrdinal`/`EffortLevelDistance`/`EffortFallbackConfidence`；`task_domain.go` 的 `resolveRelativeBenchmarkEvidence()` 已按 `(domain, effort)` 匹配。

- 新增档位归一化纯函数，统一证据侧、能力侧、请求侧命名到 `EffortLevel`。
- 改造 `resolveRelativeBenchmarkEvidence()`，按 `(domain, effort)` 精确匹配优先、同域跨档位回退次之，回退降低置信度。
- 接入 `EffortQualityBonus()` 用于相邻档位插值。
- 交付：`autopilot/task_domain.go` 与新增归一化文件，表驱动测试覆盖各档位命名与回退级别。

### Task 2：候选枚举与决策对象

> **部分完成**。`ResolvedRouteTarget`（`autopilot/route_target.go`）、`RequestProfile.ClientEffort`/`ClientEffortExplicit`（`request_profile.go`）、`CapabilityFloor.EffortFloor`（`model_resolver.go`）均已落地；`resolveEffortVariants()` 已实现按 `SupportedEffortLevels` × `PerTaskClass` 展开与 `adaptive_only`/不支持控制模型的折叠。**但 ManualRoutingIntent 集成与 Key 级模型权限感知两个子项未实现**：经代码核实，`ManualRoutingIntent` 结构体（`manual_routing_intent.go`）目前没有 `Effort` 字段，候选枚举不会为手动意图补选 effort；也未找到按 key 权限排除模型×effort 组合的过滤逻辑。这两项仍是缺口，不属于本轮已完成范围。

- 定义 `ResolvedRouteTarget`。
- `RequestProfile` 增补 `ClientEffort`、`ClientEffortExplicit`；`CapabilityFloor` 增补档位下界。
- 实现按 `SupportedEffortLevels` × `PerTaskClass` 的候选展开，含 `adaptive_only` 与不支持控制模型的折叠规则。
- **ManualRoutingIntent 集成**（未完成）：手动意图指定模型但未指定 effort 时，由候选枚举补选 effort；手动意图指定 `MappedModel` 时，`MappedModel` 优先于 `ResolvedRouteTarget.Model`；客户端显式 `none` 优先于手动意图的 effort 覆盖。
- **Key 级模型权限感知**（未完成）：候选枚举时排除当前 key 无权限访问的模型×effort 组合。
- 交付：`autopilot/model_resolver.go`、`autopilot/request_profile.go`、`autopilot/capability_floor.go`、`autopilot/manual_routing_intent.go` 及测试。

### Task 3：联合排序与档位膨胀约束

> **已完成**（提交 `b6a5ff8d`）。`rankEligibleModels()`/`betterRankedModel()` 已接入 `EffortQualityBonus * 0.1` 与 anti-effort-inflation tiebreak（同质量档优先选低 effort）；`filterEffortFloor()` 实现档位下界过滤。10 个表驱动测试覆盖展开/过滤/反膨胀场景。

- 在 `rankEligibleModels()` 比较链中加入档位维度，实现「够用即止」与成本升级门槛。
- 加入低置信度高档位不得压制高置信度中档位的约束。
- 交付：排序逻辑与针对档位膨胀的回归测试（构造高档位质量略高但成本显著更高的场景，断言不升档）。

### Task 4：请求侧档位提取

> **已完成**（提交 `2b107034`）。`handlers/common/autopilot_request_profile.go` 的 `extractClientEffort()` 复用 `reasoning_log.go` 的多路径提取，区分显式 `none` 与未声明，写入 `RequestProfile.ClientEffortRaw`/`ClientEffortExplicit`。

- 在构建 `RequestProfile` 时提取客户端档位，区分显式 `none` 与未声明。
- 复用 `reasoning_log.go` 的多路径提取能力，避免重复实现。
- 交付：`autopilot/request_profile_builder.go` 及各协议入口的提取测试。

### Task 5：决策出口与原子改写

> **部分完成**（提交 `c432c801`、`3fba827f`、`dac55672`）。`ResolvedTargetByEndpointUID` 出口、`upstream_failover.go` 的 `atomicModelEffortRewrite()` 原子改写、`ApplyReasoningParamStyle` 的 `thinking` 分支写 `effort`、`adaptive_only` 守卫均已落地并有测试覆盖。images/vectors 渠道确认未改造。**唯一未完成的子项是 Gemini 派系注入路径**：经代码核实，`handlers/gemini/handler.go` 的 `buildProviderRequest()` 没有调用任何 effort 注入逻辑，`atomicModelEffortRewrite()` 唯一写出口 `ApplyReasoningParamStyle()` 也没有 `thinkingConfig.thinkingLevel` 分支；已有的 `thinkingLevel` 相关代码（`reasoning_log.go`、`autopilot_request_profile.go`）都只是读取路径，不写请求体。Gemini 渠道模型改写生效，但联合决策选中的 effort 目前不会落到 Gemini 请求，等同于 fail-open passthrough。

- `EndpointAttemptPolicy` 增补 `ResolvedTargetByEndpointUID`，旧出口从同一真值源派生。
- 改造 `upstream_failover.go` 改写块为模型与档位的原子改写，失败则整体放弃。
- 统一 `ApplyReasoningParamStyle` 的 `thinking` 分支使其写入 `effort`。
- **Gemini 派系注入路径**（未完成）：在 `handlers/gemini/` 的请求构造环节写入 `generationConfig.thinkingConfig.thinkingLevel`，不走 `ApplyReasoningParamStyle`；images/vectors 渠道不改造，保留现有行为。
- **`adaptive_only` 守卫**：在 `upstream_failover.go` 的原子改写块中增加守卫，调用 `config.ResolveUpstreamCapability()` 获取 `ThinkingMode`，`adaptive_only` 时跳过思考参数注入。
- 交付：`autopilot/endpoint_policy.go`、`handlers/common/upstream_failover.go`、`handlers/gemini/`、`config/config_utils.go` 及集成测试。

### Task 6：可观测性

> **部分完成**（提交 `ca9f19b1`、`927aad18`）。`effortDecisionSource`/`effortClampedByClient` 已写入 `gin.Context` 并贯通日志；`CandidateScore.Dimension="effort"` 已在 `endpoint_policy.go` 中真实写入。**驾驶舱前端展示未完成**：经检索 `frontend/src` 未找到任何引用 `effortDecisionSource`/`effortClampedByClient`/`MappedEffort` 的组件，模型+档位组合候选尚未在前端呈现。

- 新增 `effortDecisionSource`、`effortClampedByClient` 字段并贯通日志与 Trace。
- 写入 `effort` 维度的 `CandidateScore`。
- 驾驶舱展示模型与档位的组合候选（未完成）。
- 交付：后端字段 + 前端展示 + 契约测试。

### Task 7：shadow 对照与门槛验证

> **部分完成**（提交 `d15e776e`）。画像覆盖率门槛诊断已实现（`autopilot/profile_coverage.go` + `GET /api/health-center/profile-coverage`，区分 `ready`/`not_ready`）；优先级反转开关 `ModelMappingRoutingConfig.PreferAutoOverManual` 已实现（`config/autopilot_config.go`，`resolveMappedModel()` 按该开关切换两种顺序），**但默认值为 `false`（历史行为不变，显式映射仍优先）**，反转是灰度期可选项而非默认落地。**未验证到的部分**：没有找到独立的 shadow 模式决策对照/差异报告代码路径；证据数据格式迁移（旧证据批量标记 `effort=unknown`）也没有找到对应的迁移代码——`ModelBenchmarkEvidence.Effort` 字段已存在（新增证据默认写入具体档位，旧记录该字段为空字符串，由归一化逻辑视作未声明档位处理，并非通过一次显式迁移动作补齐）。

- 以 shadow 模式记录决策，与现有手工表结果对照，输出差异报告（未验证到实现）。
- 验证画像覆盖率门槛，标记画像缺失的渠道。
- **将 `resolveMappedModel()` 的优先级从"显式映射 > 自动决策"改为"自动决策 > 显式映射"**，实现手工表优先级下调。中间态行为：自动决策与手工表冲突时以自动决策为准，手工表仅在自动决策 fail-open 时生效。（开关已实现，默认仍关闭）
- **执行评测证据数据格式迁移**：将已有证据键从 `canonical_model + domain + benchmark` 升级为 `canonical_model + effort + domain + benchmark`，旧证据标记为 `effort=unknown`。（未找到独立迁移代码）
- 交付：对照报告、覆盖率检查、优先级变更代码与证据迁移代码。

### Task 8：手工表退役与迁移

- 配置迁移（含预演、备份、版本标记）删除 `ModelMapping` / `ReasoningMapping`。
- 移除 `resolveMappedModel()` 优先级分支、映射更新端点、provider 层 `ResolveReasoningEffort` 注入路径。
- 清理前端映射编辑 UI 与 Desktop 预设字段。
- **Desktop Agent 配置同步**：`desktop/internal/autopilotagent/` 的 re-sync 逻辑在迁移后跳过已废弃的 `modelMapping`/`reasoningMapping` 字段，避免幽灵配置回写。验证 Desktop "Agent 配置到本地 CCX" 流程在配置结构变更后不 break。
- 交付：迁移代码、前后端清理、Desktop 同步兼容性测试、发布说明中的不可逆变更声明。

### Task 9：文档更新

- 更新 `docs/design/channel-autopilot.md` 的决策模型描述。
- 更新 `CLAUDE.md` 与渠道配置相关文档，移除手工映射说明。

## 9. 测试、灰度与回滚

### 9.1 分层测试

| 层级 | 范围 | 重点断言 |
| --- | --- | --- |
| L1 确定性 | 档位归一化、证据匹配与回退、`EffortQualityBonus` 插值 | 同输入恒定输出；回退级别与置信度单调 |
| L2 决策 | 候选枚举、联合排序、客户端上限 | 不发生档位膨胀；`adaptive_only` 与不支持控制模型被折叠；显式 `none` 强约束生效 |
| L3 集成 | `SmartRouter` + `upstream_failover` | 模型与档位原子改写；改写失败时 body 保持原样 |
| L4 协议 | 六类渠道的注入形态 | Claude 写 `thinking.effort`；chat 写 `reasoning_effort`；responses 写 `reasoning.effort`；Gemini 写 `thinkingConfig.thinkingLevel`；互斥字段被清理；`adaptive_only` 模型跳过注入；images/vectors 不注入 |
| L5 迁移 | 配置迁移 | 预演清单准确；备份可用；重复执行幂等；旧证据标记 `effort=unknown` 后匹配回落正确 |
| L6 交互 | ManualRoutingIntent + key 权限 | 手动意图优先级正确；客户端显式 `none` 覆盖手动意图；key 无权限的模型被排除；Desktop 同步不回写幽灵配置 |

Go 侧沿用表驱动测试 + `httptest` 的既有风格。

### 9.2 灰度顺序

1. 代码合入，`ReasoningEffort.Enabled` 关闭，仅 L1~L4 测试保障。
2. shadow 模式开启，只记录不改写，观察决策分布与对照差异。
3. 按渠道小批量启用 active，优先选画像完整、流量可控的渠道。
4. 观察质量与成本指标稳定后扩大范围。
5. 全量后执行手工表迁移。

`RespectClientThinking` 灰度期保持默认 `true`，降低对现有客户端行为的冲击；是否改为默认由 Autopilot 全权决定，留待全量稳定后单独评估。

### 9.3 回滚

- 阶段 2~4 回滚：关闭 `ReasoningEffort.Enabled` 即回到「不改写档位」，无需回滚代码。
- 阶段 5 之后回滚：手工表已删除，只能回滚代码而无法自动恢复用户手工配置，需依赖迁移前备份。这是本计划唯一的不可逆节点，必须在执行前确认备份可用。

### 9.4 观察指标

- 档位分布：各档位被选中的比例，异常集中在 `max` 说明膨胀约束失效。
- fail-open 比例：`effortDecisionSource=passthrough` 占比，过高说明画像或证据覆盖不足。
- 客户端截断比例：`effortClampedByClient` 占比，用于评估上限语义的实际影响面。
- 成本与质量：单请求成本变化与失败率、重试率，确认升档没有带来不成比例的成本增长。
