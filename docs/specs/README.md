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
| [channel-data-model-v2.md](./channel-data-model-v2.md) | 渠道粒度重构 | Channel→Key→Endpoint→Model、能力/凭证边界、new-api 分组共享、三步迁移 |
| [public-key-routing.md](./public-key-routing.md) | 公开与临时 Key 优先消耗 | Key 级零成本、机会性消耗策略、FastDecay 回退、API/UI 与迁移 |
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
| public-key-routing.md | ✅ 完成 | Key 级零成本与机会性优先消耗已落地，前后端已配套 |
| healthcheck.md | ✅ 完成 | 覆盖 L1/L2、稀疏探针、恢复、凭证回填、待补充项详解 |
| channel-data-model-v2.md | ✅ 完成 | 覆盖 Channel→Key→Endpoint→Model 模型、Phase 2/3 落地状态、渠道级计费/指纹机制 |
| web-ui-dialogs.md | ✅ 完成 | 覆盖所有对话框布局、交互、状态流转、跳转关系 |
| web-ui-pages.md | ✅ 完成 | 覆盖 8 个 View、导航 IA、全局区块显隐、ego-browser 实测、IA 问题清单 |
| cross-module-integration.md | ✅ 完成 | 覆盖交互边界、事件传播、配置传播、竞态处理、事件总线 |

> `phase3c-handoff.md` 为 2026-08-11 的历史交接快照（任务已全部完成），不作为现状参考。

## 待补充

各专项文档已覆盖当前实现的完整设计。以下仅列跨文档的**待排期方向**；已落地的能力直接在对应专项文档中作为当前设计描述，不在此重复罗列。

### 待排期

- **火山 manifest 自动回填**：`manifest_drift` 已事件化并在前端健康中心告警，但仍无自动更新内置清单的下游消费者（周期性 `FetchModels` 聚合回写共享 JSON 未实现，详见 `healthcheck.md` §9.1）。
- **稀疏 L2 分时段策略**：成本语义已拆分（`CostValue`+`CostUnit`）且 AFP 余额联动已落地，但高峰/低谷时间窗口自动降/升预算仍未实现（详见 `healthcheck.md` §9.3）。
