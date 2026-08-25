# 公开与临时 Key 优先消耗设计

> 状态：已实现（运行时语义 + 前后端对接）
> 范围：Key 级成本、消耗策略、EndpointPolicy、FastDecay、管理 API 与 Web UI
> 关键提交：`4509aeda` feat(autopilot): 实现 public-key-routing 运行时语义

## 1. 背景与目标

部分上游 Key 来自公开分享、临时活动、即将过期的试用额度或其他所有权不稳定的来源。它们的共同特征不是“货币汇率低”，而是：

- 对当前用户的边际成本很低或为零；
- 可用窗口短，若不尽快消费，额度可能被其他人抢先用完或自然失效；
- 可靠性通常低于私有付费 Key，可能随时出现鉴权失败、额度耗尽或限流；
- 同一渠道内可与稳定的私有 Key 并存，不能用渠道级字段表达。

本设计引入 **Key 级消耗策略**，目标是在满足模型能力、健康状态、限速和会话约束的前提下：

1. 优先使用公开或即将失效的 Key；
2. 公开 Key 失效时快速退出，并无缝回退到稳定 Key；
3. 将“真实成本”和“尽快消耗意图”分开建模，避免滥用汇率或价格字段；
4. 保持历史渠道和未配置 Key 的行为不变；
5. 让每次优先选择、降权和回退都可追踪、可解释。

典型场景：同一渠道有 `public-key` 与 `private-key`。前者被标记为机会性 Key，应先尝试；连续失败或熔断后，后者承接流量。

## 2. 设计原则

- **粒度必须是 Key。** 公开属性属于凭证，不属于物理渠道、协议 facade 或全局汇率。
- **成本不等于优先级。** 零成本表达经济事实；“抢先用掉”由独立的消耗策略表达。
- **健康约束高于消耗偏好。** 已确认不可用、被禁用、超过倍率上限或处于冷却期的 Key 不因机会性标签而复活。
- **只在可替换集合内比较。** 模型映射、协议能力、附件能力和上下文约束不匹配的 Key 不参与优先排序。
- **失败开放到稳定池。** 机会性 Key 全部不可用时必须回退常规 Key，不能因优先消耗目标阻断请求。
- **零值是有效业务值。** `0` 与“未配置”严格区分，任何归一化、序列化和默认逻辑不得使用 truthy 判断。
- **不复用 `Weight`。** 现有 `Weight` 是通用 Key 顺序权重，无法表达成本、来源与快速衰减语义。


## 3. 领域模型

### 3.1 新增 Key 级消耗策略

在 `config.APIKeyConfig` 增加：

```go
type KeyConsumptionPolicy string

const (
    KeyConsumptionNormal        KeyConsumptionPolicy = "normal"
    KeyConsumptionOpportunistic KeyConsumptionPolicy = "opportunistic"
)

type APIKeyConfig struct {
    // 既有字段省略。
    GroupMultiplier  *float64            `json:"groupMultiplier,omitempty"`
    ConsumptionPolicy KeyConsumptionPolicy `json:"consumptionPolicy,omitempty"`
}
```

语义：

| `ConsumptionPolicy` | 行为 |
|---|---|
| `""` / `normal` | 常规 Key，保持历史行为 |
| `opportunistic` | 可抢先消耗且易失效；在健康约束内优先，并启用快速衰减 |

`omitempty` 保证旧配置不产生无意义 diff。读取时空值规范化为 `normal`，未知值拒绝写入；加载旧配置时未知值降级为 `normal` 并记录一次告警。

### 3.2 成本倍率

`GroupMultiplier` 保持现有字段，但补全业务契约：

| 值 | 成本语义 |
|---:|---|
| `nil` | 未配置，按 `1.0` 处理 |
| `0` | 用户边际成本为零 |
| `(0, 1)` | 折扣成本 |
| `1` | 标准成本 |
| `> 1` | 溢价成本 |

倍率必须是有限非负数。`MaxGroupMultiplier` 仍是安全上限，而不是排序偏好；`GroupMultiplier > MaxGroupMultiplier` 的 Key 不参与调度。

### 3.3 为什么不直接使用 `PoolTag`

当前 `PoolTag` 位于运行时 `KeyEndpointProfile`，枚举为 `temp/regular/premium`，并非 `APIKeyConfig` 的权威持久化字段。直接让 UI 修改画像会在下次画像刷新时丢失，也无法随 `ChannelV3` 完整往返。

`ConsumptionPolicy` 是用户意图的配置事实；Profiler 将其投影为运行时 `PoolTagTemp`，供 FastDecay 和健康中心消费：

```text
APIKeyConfig.ConsumptionPolicy=opportunistic
                    │
                    ▼
KeyEndpointProfile.PoolTag=temp
                    │
                    ▼
FastDecayScorer + EndpointPolicy
```

### 3.4 标识与归属

策略跟随 `KeyUID`，而非明文 Key：

- 轮换明文 Key 且保留 `KeyUID` 时，策略保留；
- 新建 `KeyUID` 时默认 `normal`；
- 同一明文 Key 出现在不同账号或渠道时，不跨 `CredentialUID/KeyUID` 传播策略；
- new-api 同步只能更新其远端倍率和状态，不覆盖用户设置的 `ConsumptionPolicy`。


## 4. 调度语义

### 4.1 两级选择

调度分为渠道级和 Key 级，二者不能混为一个成本值：

1. **SmartRouter 渠道级选择**：判断一个渠道是否值得进入候选集，并计算该渠道当前可用 Key 的代表成本。
2. **EndpointPolicy/KeyPool Key 级选择**：在已选渠道内，按资格、消耗策略、健康和评分选择具体 Key。

### 4.2 Key 资格过滤

每个 Key 先经过硬过滤：

```text
Enabled / Disabled / Cooldown
→ Model & BaseURL binding
→ GroupMultiplier eligibility
→ Capability & learned compatibility
→ Circuit / health hard constraints
```

`opportunistic` 只影响通过硬过滤后的顺序，不绕过任何安全约束。

### 4.3 Key 排序键

已实现（`47415362`，`EndpointPolicy.SortKeyBindings` 赋值于 `endpoint_policy.go:361`，`classifyKeyBinding` 在 `:163`）：`classifyKeyBinding` 计算**连续效用分** `UtilityScore = healthScore × fastDecayScore × opportunisticMultiplier`（opportunistic ×1.5；health×decay < 0.67 时自动输给健康 normal Key——「尽快消耗」不被很小的延迟差抵消，同时明显 degraded 的公开 Key 压不过 healthy 私有 Key），排序键为：

```text
utilityScore                      降序（连续效用分）
effectiveCost                     升序
configuredWeight                  降序
originalIndex                     升序
```

- `originalIndex` 保证同分结果稳定，避免每次请求抖动；
- assist/dry-run 模式只记录建议顺序，不修改真实请求顺序。

### 4.4 渠道代表成本

SmartRouter 当前从 `APIKeyConfigs` 中取第一个正倍率，这既忽略零倍率，也依赖数组顺序。改为从“本请求可用 Key 集合”聚合：

```text
代表成本 = min(每个可用 Key 的 effective cost)
```

理由：渠道一旦入选，EndpointPolicy 会优先选择最低成本/机会性 Key；用最小可用成本能表达渠道当前可提供的最佳路径。必须排除 disabled、倍率超限、stale、relink-required 以及模型不匹配的 Key，避免以不可用的零成本 Key 虚假抬高渠道排名。

当机会性 Key 在渠道选择后、实际尝试前失效，EndpointPolicy 过滤并回退同渠道稳定 Key；若整个渠道失败，沿用 scheduler 的跨渠道 failover。

### 4.5 会话与人工覆盖

- `X-Channel`、ManualRoutingIntent、Promotion 和 Trace 亲和仍决定渠道边界；本策略只在允许的 Key 集合中排序。
- 会话不固定明文 Key。机会性 Key 失效后可换同渠道 Key，不破坏渠道/模型级亲和。
- 用户显式禁用 Key 的优先级高于所有自动恢复与机会性策略。


## 5. 成本计算

### 5.1 统一零成本语义

所有成本路径必须接受有限非负倍率：

```text
configuredMultiplier = GroupMultiplier ?? 1.0
effectiveCost = listCost × timeMultiplier × configuredMultiplier × billingFactor
```

当 `configuredMultiplier == 0`：

```text
effectiveCost = 0
EffectiveCostAvailable = true
EffectiveCostReason = "manual_zero_cost"
```

不得使用 `multiplier > 0` 判断“是否配置”，应使用指针是否为 `nil`。`finitePositive` 仅适用于除数、到账数量和汇率单价；倍率与最终成本使用 `finiteNonNegative`。

### 5.2 与订阅到账规则的关系

对于带完整 billing terms 的订阅：

```text
EffectiveMultiplier = GroupMultiplier
                    × TimeMultiplier
                    × PaymentAmount × PaymentUSDPrice
                    ÷ (CreditAmount × CreditUSDPrice)
```

`GroupMultiplier=0` 时结果合法为零。此时无需因最终 `EffectiveMultiplier` 非正而判失败，但以下除数仍必须严格为正：

- `CreditAmount`；
- `CreditUSDPrice`；
- 汇率图中参与路径的单位价格。

手工零成本是显式用户事实。即使 billing terms 或汇率图缺失，也可直接得到 `0 USD`，不应回退标价；非零倍率仍沿用现有降级链。

### 5.3 与机会性策略的正交关系

允许以下组合：

| 倍率 | 策略 | 含义 |
|---:|---|---|
| `0` | `opportunistic` | 免费且易失效，典型公开 Key |
| `0.2` | `opportunistic` | 低价但限时，应尽快消耗 |
| `1` | `opportunistic` | 名义成本正常，但额度即将过期 |
| `0` | `normal` | 稳定免费 Key，仅按成本参与常规评分 |
| `nil` | `normal` | 历史默认行为 |

因此 UI 可提供“公开/临时 Key”快捷操作，同时允许高级用户分别调整两个字段。

### 5.4 数值边界

- 拒绝 `NaN`、正负无穷和负数；
- 接受 `0`，并在 JSON、ChannelV3 投影、前端表单和 PATCH 中原样保留；
- 前端不得用 `value || 1`，应使用 `value ?? 1`；
- `NormalizeSavingsScore` 必须把 `0` 视为有效最低成本；
- 全部候选成本同为 `0` 时，成本维度得中性同分，由健康和稳定排序决定。


## 6. FastDecay 与故障处理

### 6.1 适用范围

`ConsumptionPolicy=opportunistic` 自动投影为 `PoolTagTemp`，进入 FastDecay。常规 Key 不因低倍率自动变成临时 Key：稳定免费服务与公开抢用 Key 的故障特征不同。

FastDecay 的记录和读取使用完整 `EndpointUID(channelUID, baseURL, keyHash)`，已在 `autopilot` 包统一。

### 6.4 失败分类

| 失败 | 动作 |
|---|---|
| 401/403、Key 无效 | 立即禁用或拉黑，FastDecay 仅作辅助记录 |
| 额度耗尽 | 按既有恢复时间禁用；无恢复时间则持续禁用 |
| 429 | 进入现有限速/cooldown；不等同永久失效 |
| 5xx、网络错误 | FastDecay 指数衰减，允许后续探测恢复 |
| 流式断流 | 使用更激进衰减 |
| 成功 | 清零连续失败并逐步恢复分数 |

公开来源不改变错误分类，也不得为了“尽快用完”绕过上游限速保护。


## 7. 配置、API 与 UI

### 7.1 PATCH API

扩展既有端点，不新增平行资源：

```http
PATCH /api/{kind}/channels/{channelUID}/keys/{keyUID}/multiplier
Content-Type: application/json

{
  "groupMultiplier": 0,
  "maxGroupMultiplier": 0,
  "consumptionPolicy": "opportunistic"
}
```

请求字段继续使用三态语义：缺失表示不修改，`null` 表示清除，具体值表示设置。`consumptionPolicy` 接受 `normal/opportunistic`；显式 `null` 规范化为 `normal`。

响应补充：

```json
{
  "keyUid": "kuid_xxx",
  "groupMultiplier": 0,
  "maxMultiplier": 0,
  "consumptionPolicy": "opportunistic",
  "effectiveCostClass": "zero",
  "eligible": true,
  "reason": "ok"
}
```

new-api Key 的 `GroupMultiplier` 仍由远端同步、禁止手改，但 `ConsumptionPolicy` 是本地用户意图，允许独立修改。同步服务合并配置时必须保留它。

### 7.2 Web UI

在渠道编辑弹窗的 Key 管理区增加：

- “消耗策略”选择：`常规` / `优先消耗（公开或即将过期）`；
- 成本倍率输入，允许 `0`；
- 快捷动作“标记为公开 Key”，一次设置 `GroupMultiplier=0` 与 `ConsumptionPolicy=opportunistic`；
- 风险提示：公开 Key 可能被其他人耗尽，失败后系统会自动回退；
- Key 行展示 `公开/临时`、`零成本`、FastDecay 分数和当前资格状态。

快捷动作只预填表单，仍由用户保存，避免误触立即改变生产流量。不得展示或记录完整 Key。

### 7.3 校验与并发

- PATCH 沿用配置写入和 ChannelV3 权威保存链；
- 支持现有 expected-version/冲突机制时应一并使用，避免两个编辑窗口互相覆盖；
- `GroupMultiplier=0, MaxGroupMultiplier=nil` 不满足现有成对安全闸门。快捷动作必须同时设置为 `0/0`；高级编辑若只设置一项，应明确报错而不是保存后静默不参与调度；
- 所有六类渠道共用同一验证与更新函数，禁止复制六份字段赋值逻辑。


## 8. 可观测性

### 8.1 Trace

渠道决策和 endpoint 尝试摘要增加非敏感字段：

- `consumptionPolicy`；
- `configuredCostMultiplier`；
- `effectiveCostUSD` 与来源；
- `fastDecayScore`；
- `selectionReason`：如 `opportunistic_preferred`、`opportunistic_decayed`、`fallback_regular`；
- `keyUid/keyMask`，禁止完整 Key。

### 8.2 指标与事件

建议增加：

```text
ccx_key_attempts_total{consumption_policy,result}
ccx_key_fallback_total{from_policy,to_policy,reason}
ccx_opportunistic_keys{state}
ccx_opportunistic_saved_cost_usd_total
```

状态变化发布事件：

- `key_consumption_policy_changed`；
- `opportunistic_key_degraded`；
- `opportunistic_pool_exhausted`；
- `opportunistic_key_recovered`。

事件 payload 只含稳定 UID、掩码、渠道 UID 和原因。

### 8.3 成本报表

请求成本账本应同时保留：

- 官方标价 `ListCostUSD`；
- 应用倍率后的 `EffectiveCostUSD`；
- `EffectiveCostMultiplier=0`；
- “节省金额 = 标价 - effective cost”。

零成本不能因 `omitempty` 或 `> 0` 条件从报表中消失。报表需区分“已确认零成本”和“成本证据缺失”。

## 9. 兼容性与迁移

- 不设置新字段的旧配置等价于 `ConsumptionPolicy=normal`；
- 不自动把历史 `GroupMultiplier=0` 推断为公开 Key，避免改变已有稳定免费 Key 的可靠性策略；
- 现有 `Weight`、Key 数组顺序、new-api 倍率同步和 `MaxGroupMultiplier` 语义保持不变；
- ChannelV3 双向投影、脱敏视图、编辑 payload 合并、disabled-key 快照与恢复路径必须携带新字段；
- Desktop 若共享 `APIKeyConfig` 契约，需要同步类型、编辑界面和 payload，不能只改 Web；
- 回滚到不认识该字段的旧版本时 JSON 会忽略它，但旧版本不会执行优先消耗策略。该行为应在发布说明中明确。

### 9.1 已实现的能力

| 位置 | 实现文件 | 说明 |
|---|---|---|
| `APIKeyConfig` 与 `KeyConsumptionPolicy` | `backend-go/internal/config/config.go` | 新增字段与合法性校验 |
| 配置加载规范化 | `backend-go/internal/config/config_loader.go` | 未知值降级为 `normal` 并告警 |
| 配置合并保留策略 | `backend-go/internal/config/config_utils.go`、`api_key_config_merge_test.go` | new-api 同步不覆盖本地 `ConsumptionPolicy` |
| 零倍率资格判断 | `backend-go/internal/config/key_group_multiplier_test.go` | `0` 合法且 eligible |
| Key multiplier PATCH | `backend-go/internal/autopilot/handlers_key_multiplier.go`、`*_test.go` | 支持三态 `consumptionPolicy` 字段 |
| EndpointPolicy 排序/过滤 | `backend-go/internal/autopilot/endpoint_policy.go`、`*_test.go` | 连续效用分排序（`UtilityScore`，opportunistic ×1.5），FastDecay 软过滤，常规池回退 |
| SmartRouter 代表成本 | `backend-go/internal/autopilot/smart_router.go` | 按可用 Key 最小有效成本聚合，保留 `0` |
| FastDecay 投影 | `backend-go/internal/autopilot/fast_decay.go` | `PoolTagTemp` 按 opportunistic 投影 |
| Trace 字段 | `backend-go/internal/handlers/common/autopilot_outcome.go` | `consumptionPolicy`、`configuredCostMultiplier` 已补齐 |
| Web UI | `frontend/src/components/edit-channel/ApiKeyManagementSection.vue`、`*_test.ts` | 消耗策略选择、快捷动作、chip 展示 |
| Desktop UI | `desktop/frontend/src/components/console/channel-edit/AuthPanel.vue` | 消耗策略选择、标记为 opportunistic |
| 公共类型 | `frontend/src/services/api-types.ts`、`desktop/frontend/src/services/admin-api.ts` | 类型已对齐 |

### 9.2 仍需补充的运营能力

- ~~成本报表中区分「已确认零成本」与「成本证据缺失」~~ ✅ 已实现（`47d6c205`）：后端输出 `zeroCostCount/configuredMultiplierCount/subscriptionCostCount/unpricedCostCount`，前端成本报表 chip 列 + CSV 字段；
- `ccx_opportunistic_*` 系列指标与事件（可选，尚未实现）；
- ~~shadow 对比报告~~ ✅ 已实现（`692d3235`）：auto 模式同时计算实际排序与忽略 opportunistic 的基线排序（`SortKeyBindingsBaseline`），首选不同时写 gin context `ccx.public_key_routing_shadow_diff` 并打 `[<apiType>-PublicKeyRouting-ShadowDiff]` 日志。


## 10. 实施分期

### Phase 1：语义闭环（已完成）

- `APIKeyConfig` 增加 `ConsumptionPolicy` 并完成所有投影/合并；
- 修复零倍率在 SmartRouter、effective cost、报表中的语义；
- 扩展 PATCH API 与 Web/Desktop 类型；
- Trace 记录策略和建议。

### Phase 2：主动排序（已完成）

- EndpointPolicy 在 active/auto 模式启用机会性优先；
- keypool 使用同一排序 helper，避免不同请求路径语义分叉；
- FastDecay 使用完整 EndpointUID，并按机会性/常规池回退；
- UI 快捷动作与健康状态展示已落地。

### Phase 3：渠道成本聚合与运营闭环（已完成）

- SmartRouter 按请求可用 Key 集合计算渠道代表成本（已完成）；
- 成本报表成本构成列与节省金额展示（已完成，`47d6c205`）；
- shadow 基线对比（`SortKeyBindingsBaseline` + shadow diff 日志）已落地（`692d3235`），后续依据 shadow 数据决定是否调整效用分参数。

## 11. 测试与验收

### 11.1 单元测试

- `nil/0/0.5/1/>1/NaN/Inf/负数` 的解析和资格判断；
- `0` 在 JSON、ChannelV3、API view、disabled snapshot、Web payload 中无损往返；
- `ResolveEffectiveCostUSD(GroupMultiplier=0)` 返回 available zero cost；
- billing terms 缺失但显式零成本时仍返回 zero cost；
- 多 Key 渠道排除 disabled/stale/over-limit 后取最小有效成本；
- `opportunistic` 优先于同健康等级常规 Key；
- degraded 机会性 Key 与 healthy 常规 Key 的既定排序符合规则；
- FastDecay 低于阈值后回退常规池，不重新放回临时 Key；
- 完整 EndpointUID 的记录和读取命中同一状态；
- new-api 同步不覆盖本地 `ConsumptionPolicy`。

### 11.2 集成测试

建立一个渠道、两个 Key：

```text
public:  multiplier=0, policy=opportunistic
private: multiplier=1, policy=normal
```

验收序列：

1. 两者健康时，请求先命中 `public`；
2. `public` 连续网络失败后分数下降并改用 `private`；
3. `public` 返回鉴权失败后立即禁用，不再参与 fail-open；
4. `public` 恢复并通过探测后重新获得优先级；
5. 手动暂停 `public` 后始终使用 `private`；
6. Trace 全程能解释选择与回退，不泄露明文 Key；
7. 未配置策略的历史渠道请求顺序与升级前一致。

### 11.3 发布门槛

- Go 单元测试、竞态测试、前端 type-check/build 通过；
- 六类渠道 PATCH 与 ChannelV3 round-trip 覆盖；
- shadow 数据证明机会性 Key 的消耗占比提升，同时整体成功率无显著下降；
- 关闭功能开关后恢复原排序，无需回滚配置文件。


## 12. 非目标与开放问题

### 12.1 非目标

- 不提供公开 Key 的抓取、发布、交换或远端生命周期管理；
- 不绕过上游鉴权、配额、速率限制或服务条款；
- 不把公开 Key 自动共享给其他 CCX 实例；
- 不用该策略替代熔断、限速、模型兼容性和安全上限；
- 本期不设计余额感知的最优控制算法，只处理显式用户策略。

### 12.2 已决策事项

- 配置粒度：Key，而非 Channel；
- 数据建模：成本倍率与消耗策略正交；
- 零倍率：有效且表示确认的零边际成本；
- 默认值：历史 Key 为 `normal`，不自动推断；
- 失效处理：FastDecay + 既有禁用/熔断，并优先回退常规池；
- API：扩展现有 Key multiplier PATCH，避免新增重复端点。

### 12.3 后续可确认项

1. ~~**健康与策略的精确排序边界。**~~ ✅ 已按折中方案落地（`47415362`）：不采用「healthy/degraded 硬门槛优先」，也不是无条件的策略 B，而是乘法效用分 `UtilityScore = healthScore × fastDecayScore × (opportunistic ×1.5)`——健康与机会性在连续分内互相制约，参数可依据 shadow 数据继续调整。
2. **渠道代表成本置信度惩罚。** `min(可用 Key cost)` 可能让脆弱公开 Key 拉高渠道排名，可在后续加入可用 Key 数量/健康度惩罚。
3. ~~**成本报表与指标。**~~ ✅ 前端已展示资格和策略，后端成本报表已区分「已确认零成本」与「成本证据缺失」（`47d6c205`）。

