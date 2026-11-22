# 仓库协作指南

## 重要约定
- **始终使用简体中文回复**。
- 遵循 SOLID / KISS / DRY / YAGNI；优先修复根因，避免无关重构。
- **禁止停止或杀掉本项目 3688 端口上的运行进程**；如需让代码生效，只能提示用户手动重启或使用用户明确指定的安全方式。
- **不要把 3688 当作当前运行端口**：它是示例端口和上述进程保护约定。验证前先核对目标进程的工作目录、启动环境中的 `PORT`、工作目录下的 `.env` 和实际监听端口，再请求该实例的 `/health` 确认版本。用户未指定端口时，不要直接假定 3688、3399 或 3000；也不要为验证自动停止或重启已运行实例。
- 修改文档时，优先以以下文件为事实源：`VERSION`、`Makefile`、`backend-go/Makefile`、`frontend/package.json`、`backend-go/main.go`。

## 特性规格驱动
- `docs/specs/` 是当前产品行为与设计实现的单一事实源，总索引为 `docs/specs/README.md`。
- **所有新需求和行为变化先修改对应规格，再修改代码和测试**；同一交付中回写实现差异、未验证项和代码索引。
- 纯重构、测试补充或不改变行为的文档修正可不先改规格；规格与代码冲突时先确认预期行为，不得静默选择一边。
- 界面需求必须覆盖加载、空态、错误、权限、并发、未保存和窄屏；运行需求必须覆盖状态、异常、重试、幂等、持久化和安全边界。
- `docs/superpowers/` 与 `docs/design/` 保存历史计划、调研或未实施方案，不得作为现行规格直接引用；未实施内容不得写入现行规格。

## 项目概览
- CCX 是一个多上游 AI API 代理与协议转换网关，当前正式支持六类渠道：`messages`、`chat`、`responses`、`gemini`、`images`、`vectors`。
- 根目录 `VERSION` 是唯一发布版本源；后端构建时通过 `backend-go/Makefile` 的 `-ldflags` 注入运行时版本信息。

## 项目结构与模块
- `backend-go/`：主 Go 服务（Gin），负责路由、认证、调度、协议转换、指标与日志；前端构建产物嵌入到 `backend-go/frontend/dist/`。
- `frontend/`：Vue 3 + Vite + Vuetify 管理界面。
- `dist/`：发布构建产物，禁止手动编辑。
- `.config/`：运行时配置与持久化目录（如 `config.json`、`metrics.db`、`backups/`）。
- `refs/`：外部参考项目存档，仅供对照，默认只读。
- 文档入口：`README.md`、`README.zh-CN.md`、`backend-go/README.md`、`docs/guide/architecture.md`、`docs/guide/development.md`、`docs/guide/environment.md`、`docs/guide/release.md`。

## 构建 / 测试 / 开发命令
- 全栈开发（推荐）：根目录 `make dev`（前端 `bun run dev` + 后端 `air` 热重载）。
- 根目录常用命令：`make run`、`make build`、`make frontend-dev`、`make clean`。
- 仅后端：`cd backend-go && make dev|run|build|test|test-cover|fmt|lint|deps`。
- 后端本地构建：`cd backend-go && make build-local`。
- 前端：`cd frontend && bun install` 后执行 `bun run dev|build|preview|type-check|lint`。
- Docker：`docker-compose up -d` 用于接近生产环境的验证。

## 代码风格
- Go：保持包职责单一、接口清晰；修改后运行 `go fmt ./...`。
- 前端：遵循现有 Vuetify / TypeScript / Prettier 风格；保持 strict 类型约束。
- 不要手动编辑生成产物：`dist/`、`frontend/dist/`、`backend-go/frontend/dist/`。
- 配置/密钥：`.env` / `.json` 只提交示例文件或文档化内容，禁止提交真实密钥。

## 路由与能力边界
- 实际代理与管理路由以 `backend-go/main.go` 为准。
- 常见代理入口包括：
  - `/v1/messages`
  - `/v1/chat/completions`
  - `/v1/responses`
  - `/v1/images/generations`
  - `/v1/images/edits`
  - `/v1/images/variations`
  - `/v1/embeddings`
  - `/v1beta/models/*`
- 管理入口统一位于 `/api/{type}/channels/*`。
- 能力测试当前只适用于 `messages`、`chat`、`responses`、`gemini`；不要假设 `images` 或 `vectors` 具备 capability-test。

## 测试规范
- 新增/修改后端逻辑尽量补 `_test.go`，优先表驱动 + `httptest`。
- 前端当前以构建验证为主；如增加复杂逻辑，再补轻量单测。
- 纯文档变更至少校验相对链接、占位符、Markdown 结构并执行 `cd docs && bun run build`，不声称完成代码或真实上游验证。
- 接口说明或代码行为调整后，除文档站检查外，至少验证：`make build`、`cd backend-go && make test`、`cd frontend && bun run build`。

## 安全与配置提示
- 部署前必须设置强 `PROXY_ACCESS_KEY`；如需分离管理权限，再配置 `ADMIN_ACCESS_KEY`。
- `.config/config.json` 支持热重载；修改 `backend-go/.env` 后需要重启服务。
- 代理端点统一鉴权（`x-api-key` / `Authorization: Bearer`）；生产环境不建议依赖 query `key`。
- 记录或展示日志时注意脱敏，尤其是 API Key、Authorization 头和 multipart 请求内容。
