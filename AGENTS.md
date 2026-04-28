# 仓库协作指南

## 重要约定
- **始终使用简体中文回复**。
- 遵循 SOLID / KISS / DRY / YAGNI；优先修复根因，避免无关重构。

## 项目结构与模块
- `backend-go/`：主 Go 服务（Gin），构建后内嵌前端静态资源；Go 代码位于 `backend-go/internal/`。
- `frontend/`：Vue 3 + Vite + Vuetify 管理界面；构建产物复制到 `backend-go/frontend/dist/` 并由后端 embed。
- `dist/`：发布构建产物（Go 二进制/打包后的 UI），不要手动编辑。
- `.config/`：运行时配置目录（`config.json` 及 `backups/`），随容器/本地持久化。
- `refs/`：外部参考项目存档，仅供对照，默认只读。
- 文档入口：`README.md`、`ARCHITECTURE.md`、`DEVELOPMENT.md`、`ENVIRONMENT.md`、`RELEASE.md`。

## 构建/测试/开发命令
- 全栈开发（推荐）：根目录 `make dev`（前端 `bun run dev` + 后端 `air` 热重载）。
- 仅后端：`cd backend-go && make dev`。
- 构建运行：
  - 根目录 `make run` / `make build`（先构建前端再编译后端）。
  - 后端本地构建：`cd backend-go && make build-local`。
- 测试：`cd backend-go && make test`（或 `make test-cover` 生成覆盖率）。
- 前端：统一使用 `bun`，执行 `cd frontend && bun install` 后再运行 `bun run dev|build|type-check`。
- Docker：`docker-compose up -d` 默认拉取镜像；本地构建请按 `docker-compose.yml` 注释说明启用 `build`（可选 `Dockerfile_China`）。

## 代码风格
- Go：保持包职责单一、接口清晰；修改后运行 `go fmt ./...`。
- 前端：遵循现有 Vuetify/Tailwind/Prettier 风格；TypeScript 保持 strict。
- 配置/密钥：`.env`/`.json` 只提交示例文件（`*.example`），禁止提交真实密钥。

## 测试规范
- 新增/修改后端逻辑尽量补 `_test.go`，优先表驱动 + `httptest`。
- 前端目前无测试框架；如增加复杂逻辑再引入轻量单测。

## 安全与配置提示
- 部署前必须设置强 `PROXY_ACCESS_KEY`；生产环境建议关闭详细请求/响应日志。
- 代理端点统一鉴权（Header `x-api-key` / `Authorization: Bearer`）；生产环境不建议使用 query `key`。
<!-- TRELLIS:START -->
# Trellis Instructions

These instructions are for AI assistants working in this project.

Use the `/trellis:start` command when starting a new session to:
- Initialize your developer identity
- Understand current project context
- Read relevant guidelines

Use `@/.trellis/` to learn:
- Development workflow (`workflow.md`)
- Project structure guidelines (`spec/`)
- Developer workspace (`workspace/`)

If you're using Codex, project-scoped helpers may also live in:
- `.agents/skills/` for reusable Trellis skills
- `.codex/agents/` for optional custom subagents

Keep this managed block so 'trellis update' can refresh the instructions.

<!-- TRELLIS:END -->



# Note

**No backward compatibility** - Break old formatsfreely
