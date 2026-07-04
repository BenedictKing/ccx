# CCX - Go 后端

> CCX 的 Go 后端负责 HTTP API、上游适配、协议转换、多渠道调度、指标与日志记录，并嵌入前端管理界面。

## 特性

- 覆盖六类代理能力：Messages、Chat Completions、Responses、Gemini、Images、Vectors
- 支持多渠道调度、故障转移、熔断与恢复
- 支持多 API Key 轮换、代理、自定义请求头、模型过滤与路由前缀
- 支持 Responses 会话跟踪与多轮上下文延续
- 支持渠道级指标、历史统计与请求日志
- 前端构建产物嵌入二进制，便于单文件部署

## 支持的上游能力

- Claude / Anthropic
- OpenAI Chat Completions
- OpenAI / Codex Responses
- Gemini 原生协议
- OpenAI Images（generations / edits / variations）
- OpenAI Embeddings

## 快速开始

### 方式一：使用根目录命令（推荐）

```bash
make dev
make run
make build
```

说明：
- `make dev`：同时启动前端开发服务器和后端热重载
- `make run`：构建前端并运行后端
- `make build`：构建前端并编译后端

### 方式二：在 `backend-go/` 目录开发

```bash
cd "backend-go"
make dev
make run
make build
make test
make test-cover
```

`make run` 会复制前端构建产物后直接运行；如果前端尚未构建，会给出提示。

### 方式三：从源码构建

```bash
git clone https://github.com/BenedictKing/ccx.git
cd ccx
cp backend-go/.env.example backend-go/.env
make build
```

## 配置概览

### 环境变量

常见变量示例：

```env
PORT=3688
ENV=production
# BIND_HOST=127.0.0.1  # 可选：仅本机访问；不设置时监听所有网卡
# Claude Desktop 等客户端需要本地 HTTPS 时设置为 true
ENABLE_HTTPS=false
ENABLE_WEB_UI=true
PROXY_ACCESS_KEY=your-secure-access-key
# EXTRA_PROXY_ACCESS_KEYS=client-key-a,client-key-b
ADMIN_ACCESS_KEY=your-admin-access-key
LOG_LEVEL=info
MAX_REQUEST_BODY_SIZE_MB=50
QUIET_POLLING_LOGS=true
```

`EXTRA_PROXY_ACCESS_KEYS` 仅用于代理 API；启用后必须设置独立 `ADMIN_ACCESS_KEY`，管理接口不再回退到 `PROXY_ACCESS_KEY`。

`ENABLE_HTTPS=true` 会让 CCX 在本地端口启用 HTTPS，同时保留同端口 HTTP 兼容；未配置 `TLS_CERT_FILE`/`TLS_KEY_FILE` 时默认自动生成 localhost 临时自签名证书。完整说明见 `../docs/guide/environment.md`。

### 运行时配置

渠道与调度配置默认保存在：

- `backend-go/.config/config.json`

命令行版可以用 `--config` 指定配置文件位置，用 `--statedir` 指定运行时状态目录，用 `--logdir` 指定日志目录：

```bash
ccx --config ~/.config/ccx/config.json --statedir ~/.local/state/ccx --logdir ~/.local/state/ccx/logs
```

说明：`--config` 只改变配置文件位置；`--statedir` 会让 `metrics.db`、`conversation_state.json`、`scheduled_recovery_state.json` 写入指定目录，未指定时保持默认 `.config`；`--logdir` 只影响日志目录，优先级高于 `LOG_DIR` 环境变量。

当前按渠道类型分组维护，例如：
- `messagesUpstream`
- `responsesUpstream`
- `chatUpstream`
- `geminiUpstream`
- `imagesUpstream`
- `vectorsUpstream`

## 代理入口概览

> 说明：以下代理入口通常都支持 `/:routePrefix/...` 变体，便于为渠道添加自定义路径前缀。

| 端点 | 方法 | 说明 |
| --- | --- | --- |
| `/health` | GET | 健康检查（无需认证） |
| `/v1/messages` | POST | Claude Messages API |
| `/v1/messages/count_tokens` | POST | Messages Token 计数 |
| `/v1/chat/completions` | POST | OpenAI Chat Completions API |
| `/v1/responses` | POST | Codex/OpenAI Responses API |
| `/v1/responses/compact` | POST | 精简版 Responses API |
| `/v1/models` | GET | 聚合模型列表 |
| `/v1/models/:model` | GET | 单模型详情 |
| `/v1beta/models/{model}:generateContent` | POST | Gemini 原生协议 |
| `/v1/images/generations` | POST | OpenAI Images 生成 |
| `/v1/images/edits` | POST | OpenAI Images 编辑 |
| `/v1/images/variations` | POST | OpenAI Images 变体 |
| `/v1/embeddings` | POST | OpenAI Embeddings 向量 |

## 管理 API 概览

所有管理接口位于 `/api/*` 下。

### 通用渠道管理模式

对于 `type ∈ messages | responses | chat | gemini | images | vectors`，存在以下通用模式：

| 模式 | 方法 | 说明 |
| --- | --- | --- |
| `/api/{type}/channels` | GET / POST | 列表与创建渠道 |
| `/api/{type}/channels/:id` | PUT / DELETE | 更新与删除渠道 |
| `/api/{type}/channels/reorder` | POST | 拖拽排序后重排 |
| `/api/{type}/channels/:id/status` | PATCH | 设置渠道状态 |
| `/api/{type}/channels/:id/resume` | POST | 恢复渠道与相关运行时状态 |
| `/api/{type}/channels/:id/promotion` | POST | 设置促销期 |
| `/api/{type}/channels/:id/models` | POST | 查询单渠道上游模型列表 |
| `/api/{type}/channels/:id/logs` | GET | 查询渠道日志 |
| `/api/{type}/channels/metrics` | GET | 渠道指标概览 |
| `/api/{type}/channels/metrics/history` | GET | 渠道历史指标 |
| `/api/{type}/channels/:id/keys/metrics/history` | GET | 单渠道 key 历史指标 |
| `/api/{type}/ping` | GET | 批量探测渠道连通性 |
| `/api/{type}/ping/:id` | GET | 探测单个渠道 |
| `/api/{type}/models/stats/history` | GET | 模型维度历史统计 |

### Key 管理模式

| 模式 | 方法 | 说明 |
| --- | --- | --- |
| `/api/{type}/channels/:id/keys` | POST | 添加 API Key |
| `/api/{type}/channels/:id/keys/:apiKey` | DELETE | 删除 API Key |
| `/api/{type}/channels/:id/keys/:apiKey/top` | POST | 将 API Key 置顶 |
| `/api/{type}/channels/:id/keys/:apiKey/bottom` | POST | 将 API Key 置底 |
| `/api/{type}/channels/:id/keys/restore` | POST | 恢复被拉黑的 Key |

### 能力测试接口

能力测试目前仅适用于 `messages`、`responses`、`chat`、`gemini` 四类文本协议渠道：

| 模式 | 方法 | 说明 |
| --- | --- | --- |
| `/api/{type}/channels/:id/capability-snapshot` | GET | 查询最近能力测试快照 |
| `/api/{type}/channels/:id/capability-test` | POST | 启动能力测试 |
| `/api/{type}/channels/:id/capability-test/:jobId` | GET / DELETE | 查询或取消测试任务 |
| `/api/{type}/channels/:id/capability-test/:jobId/retry` | POST | 重试单模型测试 |

Images 与 Vectors 当前没有对应的 capability-test / snapshot 路由。

## 管理与调度能力

### 模型发现与过滤

- 通过 `/api/{type}/channels/:id/models` 由后端代理查询上游模型列表
- `supportedModels` 支持空列表、精确匹配与通配符规则
- 调度时会自动跳过不支持当前模型的渠道
- Vectors 的 `supportedModels` 匹配客户端请求的原始模型名；Embedding 兼容性在 `modelMapping` 后按实际上游模型判断
- Vectors 如需安全 fallback，应为兼容的实际 Embedding 模型配置相同 `embeddingSpaceId`、维度和 `normalized` 语义；未配置 `embeddingCapabilities` 的旧配置保持原有行为

### Promotion / Resume

- 普通置顶 / reorder 会持久调整渠道 `priority`，并可覆盖低优先级渠道上的既有 Trace 亲和流量
- Promotion 是临时强制优先，优先级高于 Trace 亲和，并会在首次选择时绕过健康检查尝试促销渠道
- 新渠道可设置临时促销期，提升调度优先级
- `resume` 用于恢复渠道状态与相关运行时保护状态
- 故障转移、熔断和恢复逻辑由调度器与指标模块共同维护

### 调度优先级链路

`internal/scheduler/channel_scheduler.go` 的 `SelectChannel` 按以下顺序决策，命中即返回：

| 顺序 | 机制 | Reason | 作用域 | 持久化 | 说明 |
| --- | --- | --- | --- | --- | --- |
| 0 | 驾驶舱手动覆盖（Override） | `manual_override` | per-user + per-kind | TTL，默认数小时 | 驾驶舱"切换 NEXT"设置的渠道序列，按序遍历，命中第一个健康且 `active` 的渠道；序列全部不可用时自动清除并回退 |
| 1 | 促销期（Promotion） | `promotion_priority` | 全局 | 持久（带过期时间） | 处于促销时间窗口的渠道。**绕过健康检查**，给促销渠道一次尝试机会 |
| 2 | Trace 亲和性 | `trace_affinity` | per-user + per-kind | 内存 | 同用户粘性。**但会被更高优先级渠道覆盖**：若存在 `priority` 更高的健康渠道，亲和性失效 |
| 3 | 优先级排序 | `priority_order` | 全局 | 持久 | 按渠道 `priority` 字段从高到低遍历，选第一个健康、`active`、未熔断的渠道 |
| 4 | 降级兜底 | `fallback_*` | 全局 | - | 所有健康渠道都失败时，选失败率最低的作为最后尝试 |

各机制之间的关系要点：

- **Override（驾驶舱）排在最前**，一旦命中就短路整个流程，不再走促销/亲和/优先级。这是"per-user 临时强制路由"的语义。
- **置顶 / 拖拽排序**改的是渠道的 `priority` 字段，只影响第 3 步的默认调度路径。它不会撼动 Override 和 Promotion。
- **Promotion 与 Override** 的区别：Promotion 是全局的、持久的、有时间窗口；Override 是 per-user 的、TTL 内有效、只影响发起设置的那个用户。
- **Trace 亲和性**会被高优先级渠道覆盖，所以调高某个渠道的优先级或对其置顶可以"抢走"原本被亲和绑定到低优先级渠道的流量。
- 每一步都会跳过 `failedChannels`（本次请求已在该渠道失败过的）以及非 `active` 状态的渠道。除 Promotion 外都要求渠道通过健康检查（未熔断）。

### 渠道级代理与自定义请求头

- `proxyUrl`：为单个渠道配置 HTTP / HTTPS / SOCKS5 代理
- `customHeaders`：为单个渠道附加或覆盖上游请求头

## 日志与可观测性

### 渠道日志

渠道日志由 `backend-go/internal/metrics/channel_log.go` 定义，前端日志弹窗会直接展示其中的关键字段。

常见字段包括：
- `status`：请求生命周期状态，如 `pending`、`connecting`、`streaming`、`completed`、`failed`、`cancelled`
- `statusCode`：上游响应状态码
- `requestSource`：请求来源，如正式代理流量或能力测试流量
- `interfaceType`：当前请求所属接口类型
- `baseUrl`、`keyMask`：命中的上游与脱敏 key 信息

### Images `operation`

Images 请求会额外记录 `operation`，用于区分具体端点：
- `generations`
- `edits`
- `variations`

该字段仅对 Images 渠道有语义，其余协议为空。

### 指标与历史数据

后端会为不同渠道类型维护独立指标空间，避免 Messages、Responses、Chat、Gemini、Images 之间互相污染健康状态。

## 版本管理

项目发布版本以根目录 `VERSION` 为唯一来源：

- 根版本文件：`../VERSION`
- 构建注入：`backend-go/Makefile`
- 运行时变量：`backend-go/version.go`

构建时会通过 `-ldflags` 注入：
- `Version`
- `BuildTime`
- `GitCommit`

## 相关文档

- 项目入口：`../README.md`
- 中文入口：`../README.zh-CN.md`
- 架构说明：`../docs/guide/architecture.md`
- 开发指南：`../docs/guide/development.md`
- 环境变量：`../docs/guide/environment.md`
- 发布流程：`../docs/guide/release.md`
- 版本历史：`../CHANGELOG.md`
