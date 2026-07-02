# 仓库协作指南

## 重要约定
- **始终使用简体中文回复**。
- 遵循 SOLID / KISS / DRY / YAGNI；优先修复根因，避免无关重构。
- **禁止停止或杀掉本项目 3688 端口上的运行进程**；如需让代码生效，只能提示用户手动重启或使用用户明确指定的安全方式。
- 修改文档时，优先以以下文件为事实源：`VERSION`、`Makefile`、`backend-go/Makefile`、`frontend/package.json`、`backend-go/main.go`。

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
- 文档或接口说明调整后，至少验证：`make build`、`cd backend-go && make test`、`cd frontend && bun run build`。

## 安全与配置提示
- 部署前必须设置强 `PROXY_ACCESS_KEY`；如需分离管理权限，再配置 `ADMIN_ACCESS_KEY`。
- `.config/config.json` 支持热重载；修改 `backend-go/.env` 后需要重启服务。
- 代理端点统一鉴权（`x-api-key` / `Authorization: Bearer`）；生产环境不建议依赖 query `key`。
- 记录或展示日志时注意脱敏，尤其是 API Key、Authorization 头和 multipart 请求内容。
