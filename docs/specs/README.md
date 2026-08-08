# CCX 实现设计文档

> 本文档集合基于当前代码实现整理，覆盖 Autopilot、LogicalChannel、New-API 集成、Healthcheck、Benchmark Chart 五个子系统及其交互边界。
> 目标：把代码现状沉淀为可审阅、可讨论、可改造的设计文档。

## 文档索引

| 文档 | 模块 | 核心关注点 |
|------|------|-----------|
| [autopilot.md](./autopilot.md) | Autopilot 智能路由 | 模型画像、渠道选择、硬约束、失败学习、自适应并发、TTFB 拥塞 |
| [logical-channel.md](./logical-channel.md) | 同站多协议合一 | 数据模型、归组逻辑、CRUD、Dashboard、删除原子性 |
| [new-api-integration.md](./new-api-integration.md) | New-API 多账号集成 | 账号/Key/凭证模型、verify/provision、渠道纳入调度 |
| [healthcheck.md](./healthcheck.md) | 火山 Plan 健康探针 | L1/L2 探针、动态频率、稀疏模型、恢复、凭证回填 |
| [benchmark-chart.md](./benchmark-chart.md) | 模型基准图表 | 数据流、图表交互、模型注册、provisional lane |
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
| autopilot.md | ✅ 骨架完成 | 覆盖核心结构体、流程、边界、新增功能 |
| logical-channel.md | ⚠️ 骨架完成 | 归组算法细节待补充 |
| new-api-integration.md | ✅ 完成 | 覆盖数据模型、接口、同步、边界 |
| healthcheck.md | ✅ 完成 | 覆盖 L1/L2、稀疏探针、恢复、凭证回填 |
| benchmark-chart.md | ✅ 完成 | 明确前端缺失，数据链路已通 |
| cross-module-integration.md | ✅ 骨架完成 | 交互边界、事件传播、状态一致性 |

## 待补充

<!-- 各子文档写完后，在此处补充关键设计决策清单和下一步改造方向 -->
