# LogicalChannel 设计文档

> 同站多协议合一（LogicalChannel）是 CCX 近期新增的核心抽象，用于把同一上游站点在不同协议（messages/chat/responses/gemini/images/vectors）下的物理渠道收敛为单一逻辑卡。

## 1. 背景与目标

- 同一上游站点可能同时提供 OpenAI Chat、Claude Messages、Gemini 等多种协议入口
- 传统模式下每个协议是一个独立渠道，导致渠道列表冗长、配置重复、状态分散
- LogicalChannel 把同账号物理渠道收敛为单一逻辑卡，统一展示、统一调度、统一管理

## 2. 核心数据模型

### 2.1 `LogicalChannel`（`backend-go/internal/config/logical_channel.go`）

| 字段 | 含义 |
|------|------|
| `UID` | 逻辑渠道唯一标识 |
| `Name` | 展示名称 |
| `BaseURL` | 规范化后的上游地址 |
| `Kind` | 逻辑渠道类型（如 `new_api`、`generic`、`official` 等） |
| `Protocols` | 支持的协议列表 |
| `PhysicalChannels` | 关联的物理渠道 UID 列表 |
| `AutoManaged` | 是否自动托管 |
| `AutoManagedKind` | 托管类型（`generic`/`new_api`） |
| `HealthState` | 聚合健康状态 |
| `QualityTier` | 聚合质量档 |
| `CostTier` | 聚合成本档 |

### 2.2 `LogicalChannelProtocol`

表示逻辑渠道支持的协议及对应物理渠道映射。

### 2.3 `LogicalChannelKind`

逻辑渠道分类，用于前端展示和调度策略。

## 3. 归组逻辑 `RebuildLogicalChannels`

`RebuildLogicalChannels` 负责从现有物理渠道列表重建逻辑渠道：

1. 按规范化 `BaseURL` + 账号特征分组
2. 同组内按协议去重，保留最完整/最健康的物理渠道
3. 生成 `LogicalChannel.UID`（稳定哈希）
4. 聚合健康/质量/成本/能力标签

## 4. CRUD 与 REST API

`backend-go/internal/handlers/logicalchannels/logical_channels.go`：

- `GET /api/logical-channels` — 列表
- `POST /api/logical-channels` — 创建
- `PUT /api/logical-channels/:uid` — 更新
- `DELETE /api/logical-channels/:uid` — 删除
- `GET /api/logical-channels/dashboard` — 聚合仪表盘数据

## 5. 删除/更新原子性

- 删除逻辑渠道时，需决定物理渠道是否保留
- 更新逻辑渠道字段时，需同步到所有关联物理渠道
- 当前实现通过 `LogicalChannelCRUD` 保证操作原子性

## 6. 前端统一渠道数据

`frontend/src/utils/unifiedChannels.ts`：

- `buildUnifiedChannelsData`：把物理渠道和逻辑渠道合并为统一视图
- `ChannelGroup`：逻辑渠道分组展示
- `RoutedChannel`：实际路由选中的物理渠道

## 7. 与 Autopilot / Scheduler 的交互

- SmartRouter 在收集候选渠道时，先经过 LogicalChannel 层聚合
- Scheduler 最终选择的是物理渠道，但展示层以逻辑渠道为单位
- 健康状态、熔断状态在逻辑渠道层聚合，在物理渠道层执行

## 8. 布局示意图

### 8.1 逻辑渠道与物理渠道关系

```text
[LogicalChannel: "New-API Relay"]
           │
           ├─ Physical Channel A (messages, baseURL: https://api.example.com)
           ├─ Physical Channel B (chat, baseURL: https://api.example.com)
           ├─ Physical Channel C (responses, baseURL: https://api.example.com)
           └─ Physical Channel D (gemini, baseURL: https://api.example.com)
           │
           ▼
   聚合健康状态 = min(healthStates)
   聚合质量档 = max(qualityTiers)
   聚合成本档 = min(costTiers)
```

### 8.2 前端展示结构

```text
[渠道列表页]
   │
   ├─ LogicalChannel Card
   │     ├─ Name, BaseURL, Health, Quality, Cost
   │     ├─ Protocol badges (M/C/R/G/I/V)
   │     └─ 展开 → PhysicalChannel 列表
   │
   └─ 独立 PhysicalChannel Card (未归组的)
```

## 9. 待补充

- 归组算法的具体分组键定义
- 物理渠道增删时逻辑渠道的自动重建策略
- 与 new-api 多账号的交互
- 与 healthcheck 探针的联动
