# CCX 架构说明

本文档描述 CCX 当前的系统边界、核心模块和主要请求流。

## 系统概览

CCX 是一个单端口部署的 AI API 代理与协议转换网关：

- Go 后端负责路由、认证、调度、协议转换、日志与指标
- Vue 3 + Vuetify 前端提供 Web 管理界面
- 前端构建产物通过 `embed.FS` 嵌入后端二进制
- 统一入口同时承载 Web UI、管理 API 与代理 API

## 仓库结构

```text
ccx/
├── backend-go/                 # Go 后端
│   ├── main.go                # 路由与服务启动
│   └── internal/
│       ├── config/            # 配置与热重载
│       ├── handlers/          # HTTP 处理器
│       ├── providers/         # 上游适配
│       ├── converters/        # Responses 转换器
│       ├── scheduler/         # 多渠道调度
│       ├── session/           # 会话与 Trace 亲和性
│       ├── metrics/           # 指标、日志、持久化
│       └── middleware/        # 认证、CORS、压缩、日志
├── frontend/                  # Vue 3 + Vuetify 管理界面
└── .config/                   # 运行时配置与持久化数据
```

## 渠道类型与路由面

CCX 当前内建六类渠道，每类渠道拥有独立的调度、指标和日志空间：

| 渠道类型 | 代理入口 | 管理入口 | 说明 |
| --- | --- | --- | --- |
| Messages | `/v1/messages` | `/api/messages/channels/*` | Claude Messages 语义 |
| Chat | `/v1/chat/completions` | `/api/chat/channels/*` | OpenAI Chat Completions |
| Responses | `/v1/responses`、`/v1/responses/compact` | `/api/responses/channels/*` | Codex/OpenAI Responses |
| Gemini | `/v1beta/models/*` | `/api/gemini/channels/*` | Gemini 原生协议 |
| Images | `/v1/images/generations`、`/v1/images/edits`、`/v1/images/variations` | `/api/images/channels/*` | OpenAI Images |
| Vectors | `/v1/embeddings` | `/api/vectors/channels/*` | OpenAI Embeddings |

大多数代理入口都支持 `/:routePrefix/...` 变体，用于为渠道配置附加自定义前缀。

## 核心请求流

```text
Client
  -> Auth Middleware
  -> Route Handler
  -> Channel Scheduler
  -> Provider / Converter
  -> Upstream API
  -> Metrics / Channel Logs
  -> Client Response
```

分阶段说明：
1. 中间件完成认证、CORS、压缩和请求日志处理。
2. 各协议 handler 解析请求并选择对应渠道类型。
3. 调度器根据渠道状态、优先级、促销期、Trace 亲和性和熔断状态选择上游。
4. Provider 负责将请求转换成上游协议并处理流式/非流式响应。
5. 指标和渠道日志记录请求生命周期，再返回统一响应。

## 核心模块职责

### `internal/config/`
- 维护 `.config/config.json`
- 支持热重载与配置备份
- 提供各渠道配置读写能力

### Model Registry 数据分发

`shared/model-registry/ccx_model_registry.json` 是模型能力、定价与 benchmark 的唯一权威源。生成流程将其同步为公开文档站和后端内嵌的 preset shard；运行时不再生成或依赖 Go/TypeScript 数据镜像。

```text
shared/model-registry/ccx_model_registry.json
  -> docs/public/presets/model-registry.json
  -> backend-go/internal/presetstore/embedded/model-registry.json
  -> PresetStore 原子快照
  -> 后端模型解析与 /api/presets
  -> Web / Desktop runtime registry
```

后端启动时使用 embedded JSON，随后可由已校验磁盘缓存或文档站远程 preset 整包替换。`upstreamCapabilities` 与 `benchmarkProfiles` 随同一个 `PresetBundle` 原子发布，前端统一通过 `/api/presets` 获取当前生效版本，不直接请求文档站。

### `internal/handlers/`
- 承载 Messages、Chat、Responses、Gemini、Images、Vectors 代理与管理接口
- 处理模型查询、能力测试、日志查询、状态切换等管理请求

### `internal/providers/`
- 封装上游 API 的请求构造与响应处理
- 屏蔽 Claude、OpenAI、Gemini、Responses 等上游差异

### `internal/converters/`
- 主要服务于 Responses 场景
- 负责 Responses 与其他协议之间的结构转换

### `internal/scheduler/`
- 多渠道选路核心
- 管理优先级、促销期、Trace 亲和性、故障转移与恢复

### `internal/session/`
- 为 Responses API 提供 `previous_response_id` 驱动的会话跟踪
- 维护 Trace 亲和性所需的会话级信息

### `internal/metrics/`
- 记录渠道指标、历史统计和请求日志
- 支持独立的熔断状态、持久化和自动恢复调度

## 调度与高可用

每类渠道有自己的 `MetricsManager` 与日志存储，避免不同协议互相污染健康状态。

调度时会综合考虑：
- 渠道配置状态（`active` / `suspended` / `disabled`）
- 促销期
- 优先级
- Trace 亲和性
- 熔断与可用 key 状态
- 模型过滤规则
- 上下文窗口与最大输出能力

实际选路顺序为：基础可用性过滤 > 模型过滤 > 路由前缀过滤 > 上下文能力过滤 > Vectors Embedding 兼容性过滤 > 手动排序 > Promotion 渠道 > Trace 亲和 > 普通 priority 顺序。Trace 亲和会让位给更高优先级且健康的候选渠道，因此普通置顶 / reorder 会把低优先级亲和流量迁移到置顶渠道；Promotion 则是临时强制优先，会在首次选择时绕过健康检查尝试促销渠道。

失败场景下会执行故障转移，并结合熔断状态和定时恢复逻辑控制重试范围。

Vectors 的 `/v1/embeddings` 在存在 `embeddingCapabilities` 元数据时会启用严格兼容过滤：候选渠道先按客户端原始 `model` 命中 `supportedModels`，再按各渠道 `modelMapping` 后的实际上游模型解析 Embedding 兼容元数据。fallback 只会在相同 `embeddingSpaceId`（未配置时使用实际模型名）、有效维度和归一化语义的候选之间发生；被过滤掉的候选不会发送上游请求，也不会计入失败或熔断指标。完全没有 Embedding 元数据的旧配置继续保持原有调度行为。

### 上下文路由

上下文路由用于解决同一渠道组内不同实际模型上下文窗口不一致的问题。CCX 在调度前估算当前请求需要的上下文与输出预算，并结合下游 agent 请求模型的内置 profile 得到最小上下文窗口要求，再按渠道 `ModelMapping` 后的实际模型能力做资格过滤。该步骤只过滤不可用渠道，不改变用户在驾驶舱中设置的渠道顺序。

能力来源优先级：

```text
下游 agentModelProfiles
> CCX 内置 agent 模型 profile

渠道 modelCapabilities
> 全局 upstreamModelCapabilities
> CCX 内置模型能力库
> 渠道 defaultCapability
> unknown 策略
```

关键行为：

- `modelCapabilities` 的 key 匹配 `ModelMapping` 后的实际模型名，支持与 `supportedModels` 相同的通配符形式。
- 下游 agent profile 只提供最小窗口要求；如果本次请求估算更大，则以本次请求需求为准。
- 未知能力模型默认只允许承载不超过 `contextRouting.unknownSafeWindowTokens` 的请求，默认 200000 tokens。
- 渠道设置 `allowUnknownContext=true` 后，未知能力模型也可承载大上下文请求。
- 显式输出上限超过实际模型 `maxOutputTokens` 时会过滤该渠道。
- `X-Channel` 指定渠道仍必须满足上下文与输出能力，不会静默切换到其他渠道。
- Trace 亲和性按上下文桶隔离，避免 1M 请求选中的渠道污染小上下文请求。
- Responses `compaction_trigger` 会跳过原始请求窗口校验，但仍校验显式输出上限，以保证本地 compact 流程有机会执行。

配置示例：

```json
{
  "contextRouting": {
    "enabled": true,
    "defaultOutputReserveTokens": 8192,
    "unknownSafeWindowTokens": 200000
  },
  "upstreamModelCapabilities": {
    "vendor-1m-*": {
      "contextWindowTokens": 1000000,
      "maxOutputTokens": 128000
    }
  },
  "upstream": [
    {
      "name": "claude-1m",
      "modelMapping": {
        "sonnet": "claude-sonnet-4-6"
      },
      "modelCapabilities": {
        "claude-sonnet-4-6": {
          "contextWindowTokens": 1000000,
          "maxOutputTokens": 64000
        }
      },
      "defaultCapability": {
        "contextWindowTokens": 200000,
        "maxOutputTokens": 64000
      }
    }
  ]
}
```

## 可观测性

CCX 提供三类核心可观测信息：

1. **渠道指标**
   - 请求量、成功率、失败率、延迟
   - 全局统计与按模型统计历史数据

2. **渠道日志**
   - 每个渠道保留最近请求日志
   - 记录 `status`、`statusCode`、`requestSource`、`interfaceType`、`baseUrl`、`keyMask` 等字段
   - Images 请求额外记录 `operation`

3. **运行时状态**
   - 熔断状态
   - 黑名单 key 恢复
   - Promotion / Resume 等管理动作

### Images 日志 `operation`

Images 请求会在渠道日志中记录具体端点类型：
- `generations`
- `edits`
- `variations`

该字段仅对 Images 渠道有语义，其余协议为空。

## 构建与版本

- 根目录 `VERSION` 是唯一版本源
- `backend-go/Makefile` 在构建时读取 `../VERSION`
- 版本、构建时间和 Git 提交通过 `-ldflags` 注入 `backend-go/version.go`
- 前端构建产物嵌入到 `backend-go/frontend/dist`

## 扩展点

### 添加新上游能力
1. 在 `internal/providers/` 中扩展或新增上游适配实现
2. 必要时在 `internal/converters/` 中补充协议转换
3. 为对应渠道类型补齐 handler、管理接口和前端配置入口
4. 将指标、日志和模型过滤纳入统一链路

### 调整调度策略
- 修改 `internal/scheduler/`
- 如涉及健康状态或恢复机制，同时检查 `internal/metrics/`

### 扩展管理界面
- 修改 `frontend/src/components/` 与 `frontend/src/services/api.ts`
- 如新增图标或 Vuetify 组件，需同步更新前端插件注册

## 相关文档

- 入口说明：`README.md`
- 后端专项：`backend-go/README.md`
- 开发流程：`development.md`
- 环境变量：`environment.md`
- 发布流程：`release.md`
