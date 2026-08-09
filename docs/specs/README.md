# CCX 实现设计文档

> 本文档集合基于当前代码实现整理，覆盖 Autopilot、LogicalChannel、New-API 集成、Healthcheck 四个子系统及其交互边界。
> 目标：把代码现状沉淀为可审阅、可讨论、可改造的设计文档。

## 文档索引

| 文档 | 模块 | 核心关注点 |
|------|------|-----------|
| [autopilot.md](./autopilot.md) | Autopilot 智能路由 | 模型画像、渠道选择、硬约束、失败学习、自适应并发、TTFB 拥塞 |
| [logical-channel.md](./logical-channel.md) | 同站多协议合一 | 数据模型、归组逻辑、CRUD、Dashboard、删除原子性 |
| [new-api-integration.md](./new-api-integration.md) | New-API 多账号集成 | 账号/Key/凭证模型、verify/provision、渠道纳入调度 |
| [healthcheck.md](./healthcheck.md) | 火山 Plan 健康探针 | L1/L2 探针、动态频率、稀疏模型、恢复、凭证回填 |
| [web-ui-dialogs.md](./web-ui-dialogs.md) | Web 管理界面 | 所有对话框/弹窗布局、交互、状态流转、跳转关系 |
| [web-ui-pages.md](./web-ui-pages.md) | Web 页面层 | 8 个 View 布局、导航信息架构、全局区块显隐、数据流、IA 问题 |
| [cross-module-integration.md](./cross-module-integration.md) | 跨模块集成 | 路由决策链、事件传播、状态一致性边界 |

## 总览关系图

```text
[Client Request]
       │
       ▼
┌─────────────────┐
│   Entry Route   │  /v1/messages /v1/chat/completions /v1/responses ...
└────────┬────────┘
         │
         ▼
┌─────────────────┐     ┌─────────────────┐
│  LogicalChannel │◄────│  Model Registry │ (capability, effort, context, benchmark)
│  (归组/聚合层)   │     └─────────────────┘
└────────┬────────┘
         │
         ▼
┌─────────────────┐     ┌─────────────────┐
│    Autopilot    │◄────│   Healthcheck   │ (L1/L2 probe state, circuit)
│  SmartRouter    │     └─────────────────┘
│  EndpointPolicy │
└────────┬────────┘
         │
         ▼
┌─────────────────┐     ┌─────────────────┐
│    Scheduler    │◄────│   New-API Sync  │ (group multiplier, key ownership)
│ channel picker  │     └─────────────────┘
└────────┬────────┘
         │
    ┌────┴────┐
    ▼         ▼
[New-API]  [Direct Providers]
Channels   (Claude/OpenAI/Gemini/...)
    │
    ▼
[Upstream Response]
    │
    ▼
[Autopilot Learning]  ← 失败/成功反馈更新 channel/model profile
```

## 文档状态

| 文档 | 状态 | 备注 |
|------|------|------|
| autopilot.md | ✅ 完成 | 覆盖核心结构体、流程、边界、新增功能 |
| logical-channel.md | ✅ 完成 | 覆盖归组算法、CRUD 原子性、前端聚合、待补充项详解 |
| new-api-integration.md | ✅ 完成 | 覆盖数据模型、接口、同步、边界、待补充项详解 |
| healthcheck.md | ✅ 完成 | 覆盖 L1/L2、稀疏探针、恢复、凭证回填、待补充项详解 |
| web-ui-dialogs.md | ✅ 完成 | 覆盖所有对话框布局、交互、状态流转、跳转关系 |
| web-ui-pages.md | ✅ 完成 | 覆盖 8 个 View、导航 IA、全局区块显隐、ego-browser 实测、IA 问题清单 |
| cross-module-integration.md | ✅ 完成 | 覆盖交互边界、事件传播、配置传播、竞态处理、事件总线 |

## 待补充

所有文档的待补充项已补齐。以下是跨文档汇总的关键改造方向，供后续排期参考。

### 已修复（2026-08-09）

高优先级 3 项 + 中优先级 2 项 + Web UI 3 项，经 ultracode workflow 实现并对战验证，提交 `d876784d`：

- ✅ **new-api 标价回退路径丢失 GroupMultiplier**：已在回退分支叠加 `groupMultiplier`（`smart_router.go`）
- ✅ **汇率图构建失败无日志**：已补日志（按 version 限频防刷屏）+ effective cost 不可用原因采样记录
- ✅ **AccessToken 明文落库**：已 AES-256-GCM 加密落库，读库透明解密，向后兼容旧明文（注意：机器派生密钥依赖 hostname/GOOS/GOARCH，生产建议显式配置 `CCX_SECRET_KEY`）
- ✅ **LogicalChannel 视图不刷新**：`saveConfigLocked` 在 deepCopy 前统一调用 `RebuildLogicalChannels`，物理渠道变更后逻辑卡立即同步；并增强归组（强制刷新 UID/Name、同账号收敛、分歧检测）
- ✅ **CapabilityTestDialog 未接线**：已在 `App.vue` 装配 manager + 挂载对话框，`ChannelOrchestration` 渠道行菜单加"能力测试"入口
- ✅ **Web UI P1** 移动端 cost-report 可达、`P2` 成本报表全页国际化、`P4` 黑名单补 `/subscriptions` `/cockpit`

另修复基线测试 `TestResolveModelBenchmarkProfile_DistinguishesGPT56Variants`（Luna 证据顺序脆弱断言 → 按名查找，提交 `11879926`）。

### 待排期

#### 已完成（2026-08-09）
- ✅ **SmartRouter 感知 LogicalChannel**（Phase A，提交 `22028397` / `db380702` / `7ed94e4e`）：A.1 身份透传 + A.2 兄弟渠道 fallback 评分 + A.3 dry-run 候选聚合。真实路由仍以物理渠道为单位，改动可开关回退。详见 `logical-channel.md` §16.3。
- ✅ **跨模块事件总线**（Phase B，提交 `267f82d6` / `10b99f1c` / `1891c7d2`）：B.1 `internal/eventbus` 叶子包 + 熔断/Key 状态事件 + `StateEventStore`（SQLite `state_events`）+ WS/REST 端点；B.2 config 写路径 `upstream_changed`/`config_reloaded`/`logical_channel_rebuilt` + preset `preset_bundle_swapped`；B.3 前端 mitt 总线 + `useEventStream` 统一 WS + 两视图事件驱动刷新（轮询降级为兜底）。总线为可选依赖、非阻塞、事件仅通知非真相源。详见 `cross-module-integration.md` §10.1。

#### 已完成（2026-08-10）
- ✅ **健康/质量/成本/能力标签持久化到 LogicalChannel**（提交 `a438d12b`）：config.LogicalChannel 新增 4 标签字段，autopilot 经 `RegisterLogicalChannelTagDeriver` 单向 hook 派生，仅值变化时重建避免循环。详见 `logical-channel.md` §16.1。
- ✅ **稀疏 L2 预算动态调整**（提交 `27dc64d2`）：`effectiveSparseBudget` 按模型数/失败数/负载在静态 clamp 内动态放宽。详见 `healthcheck.md` §9.3。
- ✅ **New-API 周期性自动余额刷新**（提交 `009894db`）：`Start/Stop/SweepAll` 30min ticker + sweep guard + 信号量 + per-uid 超时，TTL 提至 35min。
- ✅ **capability probe schema 版本化与 drift 检测**（提交 `18f71d05`）：`capabilityProbeSchemaVersion` 并入缓存 key，`[Capability-Drift]` 观测日志。详见 `healthcheck.md` §9.2。
- ✅ **火山 manifest drift 事件**（提交 `22d4a11c`）：控制面 `FetchModels` 与内置清单 diff 发 `manifest_drift`（观测，无下游自动回填）。详见 `healthcheck.md` §9.1。
- ✅ **Web UI P3/P6/P7/P9**（提交 `e5074f99`/`50873dcc`/`a7c347e2`）：导航图标去重、SubscriptionsView 确认/提示统一、EmptyState 统一空态。
- ✅ **scheduler 联邦回归修复**（提交 `1ab1ed7d`）：收窄 `ensureAutoManagedKind` 迁移条件，显式 `autoManaged=false` 手动渠道不再被联邦化。

#### 待排期（低优先级 / 观测项后续）
- **capability probe 统一 schema 定义**：`shared/capability-probe-schema.json` 前后端单一真相源（当前仍双副本硬编码模型列表）
- **火山 manifest 自动回填**：`manifest_drift` 目前仅观测，无自动更新内置清单/告警的下游消费者
- **capability_drift 上事件总线**：当前仅日志，未发 eventbus/指标
