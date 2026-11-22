# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with this repository.

## 项目概述

CCX 是一个支持 Claude、OpenAI Chat、OpenAI Images、OpenAI Embeddings、Codex Responses 和 Gemini 的多上游 AI API 代理与协议转换网关，提供统一代理入口和 Web 管理界面。

**技术栈**: Go 1.25 + Gin（后端）+ Vue 3 + Vuetify 3 + TypeScript（前端）+ Docker  
**版本管理**: 根目录 `VERSION` 是唯一版本源，构建时通过 `backend-go/Makefile` 的 `-ldflags` 注入运行时版本信息。

## 常用命令

```bash
# 根目录（推荐）
make dev              # 同时启动前端开发服务器和后端热重载
make run              # 构建前端并运行 Go 后端
make frontend-dev     # 前端开发服务器
make build            # 构建前端并编译 Go 后端
make clean            # 清理构建文件

# Go 后端开发 (backend-go/)
make dev              # 热重载开发模式
make run              # 复制前端产物后直接运行
make build            # 构建生产版本
make test             # 运行所有测试
make test-cover       # 测试 + 覆盖率报告
make fmt              # 格式化代码
make lint             # 代码检查
make deps             # 更新依赖

# 前端开发 (frontend/)
bun install
bun run dev
bun run build
bun run type-check
```

## 架构概览

```text
Client -> backend :3000 ->
  |- /                            -> Web UI
  |- /api/*                       -> Admin API
  |- /v1/messages                 -> Claude Messages proxy
  |- /v1/chat/completions         -> OpenAI Chat proxy
  |- /v1/responses                -> Codex Responses proxy
  |- /v1/images/{...}             -> OpenAI Images proxy
  |- /v1/embeddings               -> OpenAI Embeddings proxy
  |- /v1/models                   -> Models API
  `- /v1beta/models/*             -> Gemini proxy
```

六类正式渠道：
- `messages`
- `chat`
- `responses`
- `gemini`
- `images`
- `vectors`

## 核心设计模式

1. **Provider Pattern** - `backend-go/internal/providers/` 负责上游请求构造与响应处理。
2. **Converter Pattern** - `backend-go/internal/converters/` 负责 Responses 场景的协议转换。
3. **Session Manager** - `backend-go/internal/session/` 负责 Responses 多轮会话与 Trace 亲和性。
4. **Scheduler Pattern** - `backend-go/internal/scheduler/` 负责优先级、促销期、熔断与故障转移。

## API 总览

### 代理入口
- `POST /v1/messages`
- `POST /v1/messages/count_tokens`
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/responses/compact`
- `GET /v1/models`
- `GET /v1/models/:model`
- `POST /v1beta/models/{model}:generateContent`
- `POST /v1/images/generations`
- `POST /v1/images/edits`
- `POST /v1/images/variations`
- `POST /v1/embeddings`
- `GET /health`

### 管理入口
- `/api/messages/channels/*`
- `/api/chat/channels/*`
- `/api/responses/channels/*`
- `/api/gemini/channels/*`
- `/api/images/channels/*`
- `/api/vectors/channels/*`

说明：实际路由以 `backend-go/main.go` 为准，文档只保留概览层信息。

## 关键配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PORT` | 3000 | 服务器端口 |
| `ENV` | production | 运行环境 |
| `PROXY_ACCESS_KEY` | - | 代理访问密钥 |
| `ADMIN_ACCESS_KEY` | - | 可选管理密钥 |
| `QUIET_POLLING_LOGS` | true | 静默轮询日志 |
| `MAX_REQUEST_BODY_SIZE_MB` | 50 | 请求体最大大小 |

完整配置参考 `ENVIRONMENT.md` 与 `backend-go/.env.example`。

## 特性规格驱动

- `docs/specs/` 是当前产品行为与设计实现的单一事实源，总索引为 `docs/specs/README.md`。
- 所有新需求和行为变化先修改对应规格，再修改代码和测试；同一交付中回写实现差异、未验证项和代码索引。
- 历史计划、调研和未实施方案位于 `docs/superpowers/`、`docs/design/`，不得作为现行规格。
- 纯重构、测试补充或不改变行为的文档修正可不先改规格；规格与代码冲突时先确认预期行为。

## 常见任务

1. 添加新的上游能力：修改 `backend-go/internal/providers/`
2. 调整 Responses 协议转换：修改 `backend-go/internal/converters/`
3. 调整调度策略：修改 `backend-go/internal/scheduler/`
4. 修改管理界面：修改 `frontend/src/components/` 与 `frontend/src/services/api.ts`

## 编码约定

### 命名规范
- Go 文件名：`snake_case`
- Go 函数：PascalCase 导出 / camelCase 私有
- Vue 组件：PascalCase
- TS 文件名：`kebab-case`

### 日志格式
所有后端日志使用 `[Component-Action]` 标签格式，禁止 emoji：

```go
log.Printf("[Scheduler-Channel] 选择渠道: %s", channelName)
```

### 测试风格
Go 测试优先使用表驱动测试 + `httptest`。

## 前端注意事项

- `frontend/` 与 `desktop/frontend/` 均以 Bun 为主包管理器，`bun.lock` 是权威锁文件；`pnpm-lock.yaml` 仅用于兼容验证
- 不要为了运行 `npm audit` 重新生成 `package-lock.json`，避免重复锁文件和旧锁文件误报
- 安全检查优先使用 `bun install` 触发 Socket 扫描器，漏洞审计使用 `bun audit --registry=https://registry.npmjs.org`
- Vuetify 组件采用手动按需导入
- 图标使用 `@mdi/js` SVG 按需导入
- 新增 `mdi-xxx` 图标时，必须同时补 `@mdi/js` 导入和 `iconMap` 映射
- 前端构建产物通过 `embed.FS` 嵌入 Go 二进制

## 模块文档

- [backend-go/CLAUDE.md](backend-go/CLAUDE.md) - 后端模块索引
- [frontend/CLAUDE.md](frontend/CLAUDE.md) - Web 前端模块索引
- [desktop/CLAUDE.md](desktop/CLAUDE.md) - 桌面模块索引
- [desktop/frontend/CLAUDE.md](desktop/frontend/CLAUDE.md) - 桌面前端模块索引
- [backend-go/README.md](backend-go/README.md) - 后端专项文档
- [现行特性规格](docs/specs/README.md) - 设计、需求边界、运行逻辑与验收的单一事实源
- [ARCHITECTURE.md](docs/guide/architecture.md) - 架构说明
- [DEVELOPMENT.md](docs/guide/development.md) - 开发指南
- [ENVIRONMENT.md](docs/guide/environment.md) - 环境变量说明
- [RELEASE.md](docs/guide/release.md) - 发布流程

## 重要提示

- 修改 `.env` 后需要重启服务
- `.config/config.json` 修改后会自动热重载
- 默认假设用户已通过 `make dev` 或 `make frontend-dev` 启动开发服务，不要自动杀进程重启
