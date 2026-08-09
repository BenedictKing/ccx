# CCX 架构级改造排期计划

> 基于 `docs/specs/README.md` 与 `docs/specs/cross-module-integration.md` 明确保留的待排期项整理。
> 目标：把两个架构级改造（SmartRouter 感知 LogicalChannel、跨模块事件总线）拆分为可逐段实施、可验证、可回滚的计划。
>
> 状态：草案（待审阅）  
> 创建日期：2026-08-09

## 目录

1. [总体节奏](#1-总体节奏)
2. [Phase A：SmartRouter 感知 LogicalChannel](#2-phase-a-smartrouter-感知-logicalchannel)
3. [Phase B：跨模块事件总线](#3-phase-b-跨模块事件总线)
4. [Phase C：中低优先级项跟进](#4-phase-c-中低优先级项跟进)
5. [风险与回滚策略](#5-风险与回滚策略)
6. [验收清单](#6-验收清单)
7. [附录：待排期项原始出处](#7-附录待排期项原始出处)

## 1. 总体节奏

### 1.1 范围与目标

本计划只聚焦 `docs/specs/README.md` 中标注为**待排期**的改造项，优先级如下：

| 优先级 | 改造项 | 预估工期 | 文档出处 |
|--------|--------|----------|----------|
| 中（架构级） | **SmartRouter 感知 LogicalChannel** | 5–7 工作日 | `logical-channel.md` §16.3 |
| 中（架构级） | **跨模块事件总线** | 5–7 工作日 | `cross-module-integration.md` §10.1 |
| 低 | Benchmark Chart 前端页面落地 | 3–4 工作日 | `benchmark-chart.md` |
| 低 | New-API 周期性自动余额刷新 | 1–2 工作日 | `new-api-integration.md` |
| 低 | 健康/质量/成本/能力标签持久化到 LogicalChannel | 2–3 工作日 | `logical-channel.md` §16.4 |
| 低 | 稀疏 L2 预算动态调整 | 2–3 工作日 | `healthcheck.md` |
| 低 | capability probe schema 版本化与 drift 检测 | 2 工作日 | `healthcheck.md` |
| 低 | 火山 manifest 自动刷新与 drift 告警 | 2 工作日 | `healthcheck.md` |

> **排期建议**：两个架构级改造不并行启动，先完成 Phase A（SmartRouter + LogicalChannel），再进入 Phase B（事件总线）。事件总线会消费 Phase A 新增的逻辑渠道状态事件，顺序实施可减少返工。

### 1.2 里程碑

| 里程碑 | 交付物 | 验收方式 |
|--------|--------|----------|
| M1 | Phase A 设计评审通过 | 文档 + 接口草案 Review |
| M2 | Phase A 实现 + 回归通过 | `go test ./...` / 关键路径压测 |
| M3 | Phase B 设计评审通过 | 事件 schema + 接线清单 Review |
| M4 | Phase B 实现 + 回归通过 | 事件端到端测试 + 前端订阅 demo |
| M5 | Phase C 低优项按需落地 | 每个子项独立 PR |

### 1.3 与日常迭代的并行策略

- **冻结窗口**：Phase A/B 核心改动进入主干前，避免合并同文件的大规模重构（如 `smart_router.go`、`config_loader.go`）。
- **分支策略**：每个 Phase 从 `main` 切独立分支；架构级改动分多个小 PR，每 PR 只改一个模块/子系统。
- **回滚开关**：新增功能默认开启但保留配置开关，出现线上异常可立即回退到物理层行为。

## 2. Phase A：SmartRouter 感知 LogicalChannel

> **状态：✅ 已完成（2026-08-09）**。A.1 身份透传（`22028397`）、A.2 兄弟渠道 fallback 评分（`db380702`）、A.3 dry-run 候选聚合（`7ed94e4e`）。
> 实现与最初设计有一处偏差：A.2 未采用"标签持久化到 LogicalChannel + LogicalChannelResolver 接口"，改用**兄弟渠道画像 fallback**——因 `config` 包不能依赖 `autopilot`（反向包循环），运行时画像源 `ProfileStore` 在 autopilot 内。全部逻辑收敛在 autopilot 内，无跨包依赖与写回循环。标签持久化下沉到 Phase C §4.3。

### 2.1 目标

让 `internal/autopilot` 的候选收集与评分过程识别物理渠道所属 `LogicalChannel`，并在评分、过滤、trace、dry-run 中暴露逻辑渠道身份，统一前后端对“渠道卡片”的语义。

### 2.2 当前状态（来自 `logical-channel.md` §16.3）

- `SmartRouter.collectChannelEntries` 直接从六个物理数组遍历，按 `Status` 和 `APIKeys` 过滤。
- `buildChannelEntry` 只基于单个 `UpstreamConfig` 构造 `channelScoreEntry`。
- `internal/autopilot` 没有任何代码引用 `LogicalChannel` / `LogicalChannelUID`。
- 同一 LogicalChannel 下的多协议/多 BaseURL/多 Key 被当作独立候选分别评分。

### 2.3 技术方案

#### 2.3.1 依赖注入

`SmartRouter` 增加可选接口 `LogicalChannelResolver`：

```go
type LogicalChannelResolver interface {
    Resolve(channelKind, channelUID string) (*config.LogicalChannel, bool)
}
```

由 `ConfigManager` 实现（内存 `Config.LogicalChannels` 索引），在 `main.go` 注入。Autopilot 内部不直接依赖 `config` 的 LogicalChannel 细节，仅通过接口读取。

#### 2.3.2 候选条目扩展

在 `channelScoreEntry` / `RoutingCandidate` 中新增：

```go
LogicalChannelUID  string
LogicalChannelName string
LogicalChannelTags []string
```

`LogicalChannel` 为空时（旧配置或独立物理渠道）回退到现有物理层行为。

#### 2.3.3 评分集成（非破坏式）

- **Phase A.1（透明透传）**：仅把 LogicalChannel 身份带入 entry，用于 trace/dry-run 展示，不改变现有分数。
- **Phase A.2（标签输入）**：把 LogicalChannel 的健康/质量/成本/能力标签作为 `ScoringCandidate` 的 fallback 或加权项。
- **Phase A.3（归一化候选）**：在 LogicalChannel 维度对多协议/多 key 候选做预聚合或去重，避免同一张卡出现多次。

> 建议先落地 A.1，再按实测决定是否推进 A.2/A.3。

#### 2.3.4 Trace 与 dry-run

- `RoutingDecisionTrace` 的候选列表增加 `logicalChannelUid` / `logicalChannelName`。
- dry-run 返回按 LogicalChannel 聚合的分组视图（可选，前端未实现前保留原列表）。

### 2.4 任务拆分

| 编号 | 任务 | 文件 | 验收标准 |
|------|------|------|----------|
| A1 | 定义 `LogicalChannelResolver` 接口并在 `SmartRouter` 注册 | `autopilot/smart_router.go` | 编译通过，接口可空实现 |
| A2 | `ConfigManager` 实现 resolver + 内存索引 | `config/logical_channel.go` 或新文件 | 给定 `channelKind/channelUID` 能在 O(1) 查到 logical |
| A3 | `collectChannelEntries` 回填 `LogicalChannelUID/Name/Tags` | `autopilot/smart_router.go` | 新字段在 entry 中可用 |
| A4 | `RoutingCandidate` 与 trace 透传逻辑身份 | `autopilot/routing_trace.go` | trace JSON 含逻辑身份 |
| A5 | dry-run 增加可选按 LogicalChannel 聚合 | `autopilot/handlers_dryrun.go` | 新旧两种返回格式通过开关切换 |
| A6 | 补充单元测试 + 兼容性测试 | `autopilot/*_test.go` | 旧配置（无 LogicalChannel）行为不变；新配置身份正确 |

### 2.5 验收标准

- 无 LogicalChannel 的旧配置：路由结果与改造前逐字节一致（通过基准测试或快照测试保证）。
- 有 LogicalChannel 的新配置：`RoutingDecisionTrace` 中每个候选都带 `logicalChannelUid`。
- dry-run 接口保持向后兼容，新增聚合视图通过查询参数启用。
- `go test ./internal/autopilot/...` 全绿。

### 2.6 兼容性开关

新增配置项（可选）：

```go
type AutopilotConfig struct {
    // ...
    LogicalChannelScoringMode string // "off" | "identity" | "tag" | "aggregate"
}
```

默认 `"identity"`（Phase A.1），异常时切 `"off"` 立即回退物理层行为。

## 3. Phase B：跨模块事件总线

### 3.1 目标

为后端模块（autopilot、scheduler、healthcheck、config）与前端建立统一事件总线，替代当前分散的回调/hook/轮询机制；优先覆盖熔断、key 状态、配置变更三类高频事件。

### 3.2 当前状态（来自 `cross-module-integration.md` §10.1）

后端事件传播机制分散：

| 机制 | 局限 |
|------|------|
| `ConfigManager.RegisterOnConfigChange` | 粗粒度，接收方需自行 diff |
| `PresetStore.RegisterOnChange` | 仅 model registry 消费 |
| `ratelimit.SetUpstreamSignalCallback` | 单一回调点 |
| `autopilot/event_hub.go` | 仅 autopilot 内部，仅向前端广播 |
| 前端 | 无统一事件总线，依赖 props/store |

### 3.3 技术方案

#### 3.3.1 后端：扩展 `internal/eventbus`

- 将现有 `autopilot/event_hub.go` 升级为 `internal/eventbus` 包。
- 统一 `Event` envelope：

```go
type Event struct {
    UID       string
    Type      string // profile_updated | circuit_breaker_state_changed | ...
    Subject   string // channelUID / keyID / model / subscriptionUID
    Scope     string // autopilot | scheduler | healthcheck | config
    From      string
    To        string
    Cause     string
    Payload   map[string]any
    CreatedAt time.Time
}
```

- 发布非阻塞；慢消费者可配置为丢弃或告警。
- 关键状态事件同时落盘到 `StateTransitionStore`（复用/扩展 `profile_changelog` 表）。

#### 3.3.2 发布点清单

| 事件类型 | 发布位置 | 消费方 |
|----------|----------|--------|
| `circuit_breaker_state_changed` | `metrics/channel_metrics_circuit.go` | 前端健康中心、scheduler |
| `key_blacklisted` / `key_restored` | `config.go BlacklistKey/RestoreKey` | scheduler、前端 |
| `config_reloaded` | `ConfigManager.reload` | 所有模块 |
| `upstream_changed` | `saveConfigLocked` | scheduler、healthcheck |
| `logical_channel_rebuilt` | `RebuildLogicalChannels` | autopilot resolver |
| `preset_bundle_swapped` | `PresetStore` | autopilot、scheduler |

#### 3.3.3 兼容层

- 保留 `RegisterOnConfigChange` / `RegisterOnChange` 作为总线订阅者的薄封装。
- 事件总线未初始化时，回调继续按原方式工作，避免启动顺序依赖。

#### 3.3.4 前端事件总线

- 引入 `mitt`（已出现在 `node_modules`，体积 200B）或 Vue `provide/inject`。
- 后端 WebSocket 事件接入 `EventSource` composable：
  - `frontend/src/composables/useEventSource.ts`：统一 SSE/WebSocket 连接、自动重连、心跳。
  - 按事件类型分发给订阅组件。

### 3.4 任务拆分

| 编号 | 任务 | 文件 | 验收标准 |
|------|------|------|----------|
| B1 | 抽取 `internal/eventbus` 包与统一 `Event` 结构 | `internal/eventbus/*.go` | 单元测试通过 |
| B2 | 迁移 `autopilot/event_hub.go` 为总线发布者 | `internal/eventbus/publisher.go` | 现有前端事件不中断 |
| B3 | ConfigManager 接入总线并发布细粒度事件 | `config/config_manager.go` | `RegisterOnConfigChange` 行为一致 |
| B4 | 熔断状态迁移发布事件 | `metrics/channel_metrics_circuit.go` | 事件含 from/to/subject |
| B5 | Key 拉黑/恢复发布事件 | `config/config.go` | 前端可订阅 |
| B6 | 前端 `useEventSource` + 事件分发 | `frontend/src/composables/useEventSource.ts` | 渠道/健康页面实时刷新 |
| B7 | 事件持久化到 SQLite | `metrics/state_transition_store.go` | 可按 subject 查询历史 |
| B8 | 端到端测试 + 回滚测试 | `tests/` 或 `*_test.go` | 总线关闭/异常时系统不退化 |

### 3.5 验收标准

- 任意 key 被拉黑后，订阅 `key_blacklisted` 的健康中心页面在 1 秒内刷新状态（不依赖 30s 轮询）。
- 配置重载后，scheduler 与 healthcheck 通过事件触发增量刷新，而非全量重加载。
- 事件总线 panic 不扩散到发布者；发布者日志记录 dropped event。
- 事件 schema 文档化，新增事件类型不破坏旧消费者。

### 3.6 风险与缓解

- **事件风暴**：非阻塞发布 + 慢消费者丢弃；关键事件持久化兜底。
- **启动顺序**：总线为可选依赖，未初始化时回调兜底。
- **前后端契约**：事件 schema 在 `docs/specs/` 中维护，新增字段需文档先行。

## 4. Phase C：中低优先级项跟进

### 4.1 Benchmark Chart 前端页面落地

- **范围**：把 `scripts/generate-benchmark-chart.mjs` 中的可视化逻辑迁移到 Web 管理台。
- **输入**：`useRuntimePresets().effectiveBenchmarkProfiles`。
- **输出**：一个独立 View（如 `/benchmarks`），支持模型对比、分类筛选、provisional/verified lane 高亮。
- **依赖**：Phase A 完成后可利用 LogicalChannel 标签做渠道级 benchmark 聚合。
- **工期**：3–4 工作日。

### 4.2 New-API 周期性自动余额刷新

- **现状**：`subscription_refresh_worker.go` 只在启动时同步 + 手动刷新。
- **方案**：给 `NewApiSubscriptionSyncService` 增加定时 ticker（默认 30min），触发 `subscription_balance_fetcher` 拉取余额。
- **风险**：避免与 manual refresh 并发，需加 `singleflight` 或 worker 锁。
- **工期**：1–2 工作日。

### 4.3 健康/质量/成本/能力标签持久化到 LogicalChannel

- **现状**：`LogicalChannel` 只有通用 `Tags []string`，没有专用标签字段。
- **方案**：
  - 新增 `HealthTag / QualityTag / CostTag / CapabilityTags`。
  - 在 `RebuildLogicalChannels` 中根据组内物理渠道状态推导默认值。
  - Autopilot 评分时把这些标签作为 fallback。
- **依赖**：Phase A 的 `LogicalChannelResolver` 已建立，读取成本低。
- **工期**：2–3 工作日。

### 4.4 Healthcheck 其余低优项

| 项 | 现状 | 方案 | 工期 |
|----|------|------|------|
| 稀疏 L2 预算动态调整 | `SparseL2MaxModels` / `SparseL2MaxCostAFP` 静态配置 | 根据大盘负载/历史成功模型数动态调整预算 | 2–3 工作日 |
| capability probe schema 版本化 | 当前无 schema 版本 | 给 probe request/response 加 `schemaVersion` 字段，检测到 drift 记日志 | 2 工作日 |
| 火山 manifest 自动刷新与 drift 告警 | 内置 manifest 静态 | 定时拉取火山远端 manifest，diff 后发布 drift 事件 | 2 工作日 |

### 4.5 Web UI 遗留项

来自 `web-ui-pages.md` §9，不依赖架构级改造，可并行处理：

| 项 | 问题 | 建议 |
|----|------|------|
| P3 | 导航 icon 重复 | 为 conversations / cockpit 分配不同 `mdi-*` 图标 |
| P5 | 健康数据轮询不一致 | 统一 ChannelsView 与 HealthCenterView 的轮询策略，或接入 Phase B 事件总线 |
| P6/P7 | 确认/提示体系分裂 | SubscriptionsView 改用 `ConfirmDialog` / `SnackbarManager` |
| P8 | 路由守卫空转 | 按需实现权限守卫或删除空 guard |
| P9 | 空态覆盖不均 | 补全各 View 的 empty state 组件 |

### 4.6 排期建议

- 架构级 Phase A/B 完成后，优先落地 **4.3 标签持久化**（与 Phase A 强相关）。
- 其余低优项按业务优先级逐个 PR，不阻塞主线。

## 5. 风险与回滚策略

### 5.1 主要风险

| 风险 | 影响 | 发生概率 | 缓解措施 |
|------|------|----------|----------|
| SmartRouter 引入 LogicalChannel 后评分漂移 | 路由结果质量下降 | 中 | Phase A.1 仅透传不评分；A.2/A.3 用 A/B 测试 + shadow 模式验证 |
| 事件总线成为单点瓶颈 | 调度延迟、事件丢失 | 低 | 非阻塞发布；关键事件持久化；发布与消费分离 |
| 旧配置兼容性被破坏 | 升级后路由异常 | 中 | 保留 `"off"` 开关；旧配置走物理层路径；快照测试对比 |
| 前端事件订阅引入内存泄漏 | 页面长期运行后卡顿 | 低 | 组件 unmount 时自动取消订阅；总线限制最大监听数 |
| 多模块同时订阅导致循环触发 | 配置 reload 死循环 | 低 | 事件 envelope 携带 `from` 字段；禁止消费方在同一 scope 内回写触发源 |

### 5.2 回滚策略

- **配置开关回滚**：`AutopilotConfig.LogicalChannelScoringMode="off"` 切回纯物理层；事件总线通过 feature flag 关闭，回调兜底。
- **代码回滚**：每个 Phase 拆分为多个独立 PR，任一 PR 异常可单独 revert。
- **数据回滚**：新增字段均为可选；事件持久化表独立建表，不影响现有业务表。
- **发布灰度**：先在非生产环境跑 24h 压力测试，再合并主干。

## 6. 验收清单

### 6.1 Phase A 验收

- [ ] `LogicalChannelResolver` 接口定义并通过 mock 测试。
- [ ] 新配置下 `RoutingDecisionTrace` 含 `logicalChannelUid` / `logicalChannelName`。
- [ ] 旧配置下路由结果与改造前一致（通过 `TestSmartRouterGolden` 或等价测试）。
- [ ] dry-run 接口新增聚合视图，默认返回保持兼容。
- [ ] `go test ./internal/autopilot/...` 全绿。
- [ ] 压测显示单请求评分耗时增加 < 5%。

### 6.2 Phase B 验收

- [ ] `internal/eventbus` 包发布/订阅单元测试全绿。
- [ ] key 拉黑事件在 1s 内推送到前端。
- [ ] 配置重载后 scheduler 不重新全量加载，而是增量刷新。
- [ ] 事件总线异常不影响发布者运行。
- [ ] 事件 schema 文档同步到 `docs/specs/cross-module-integration.md`。

### 6.3 Phase C 验收

- [ ] Benchmark Chart 前端页面可访问并渲染模型对比。
- [ ] New-API 余额每 30min 自动刷新（可调）。
- [ ] LogicalChannel 新增标签字段并在重建时正确推导。

## 7. 附录：待排期项原始出处

### 7.1 中优先级（架构级）

> 来自 `docs/specs/README.md` §待排期

- **SmartRouter 不感知 LogicalChannel**：候选收集在物理层，逻辑渠道标签无法参与评分（架构级改动，多日）
- **跨模块事件总线缺失**：熔断/Key 状态/配置变更无法实时订阅，需轮询（架构级改动，多日）

### 7.2 低优先级（功能扩展）

> 来自 `docs/specs/README.md` §待排期

- **Benchmark Chart 前端页面落地**：数据链路已通，需决策交互范围与 schema 扩展
- **New-API 周期性自动余额刷新**：仅启动时同步 + 手动刷新
- **健康/质量/成本/能力标签字段持久化到 LogicalChannel**
- **稀疏 L2 预算动态调整**：当前静态配置，不感知大盘负载
- **capability probe schema 版本化与 drift 检测**
- **火山 manifest 自动刷新与 drift 告警**

### 7.3 Web UI 遗留

> 来自 `docs/specs/README.md` §Web UI 遗留 / `docs/specs/web-ui-pages.md` §9

- **P3** 导航 icon 重复（conversations 与 cockpit 同用 `mdi-view-dashboard-outline`）
- **P5** 健康数据轮询不一致（ChannelsView 30s vs HealthCenterView 手动）
- **P6/P7** 确认/提示体系分裂（SubscriptionsView 用原生 confirm + 本地 snackbar）
- **P8** 路由守卫空转（beforeEach 无条件放行）
- **P9** 空态覆盖不均
