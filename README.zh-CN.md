# Claude / OpenAI Chat / OpenAI Images / OpenAI Embeddings / Codex Responses / Gemini API Proxy - CCX

[English](README.md) | 简体中文

[![GitHub release](https://img.shields.io/github/v/release/BenedictKing/ccx)](https://github.com/BenedictKing/ccx/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

CCX 是一个高性能的 AI API 代理与协议转换网关，支持 Claude、OpenAI Chat、OpenAI Images、OpenAI Embeddings、Codex Responses 与 Gemini。它提供统一入口、内置 Web 管理界面、渠道编排、故障转移、多密钥管理和模型路由能力。

## 功能特性

- 后端与前端一体化架构，单端口部署
- 双密钥认证：`PROXY_ACCESS_KEY` 与可选 `ADMIN_ACCESS_KEY`
- 内置 Web 管理面板，支持渠道管理、测试、日志和监控
- 同时支持 Claude Messages、OpenAI Chat Completions、OpenAI Images、OpenAI Embeddings、Codex Responses、Gemini
- 智能调度：优先级、促销期、健康检查、故障转移与熔断恢复
- 上下文路由：根据实际模型上下文窗口和最大输出能力过滤候选渠道
- 每个渠道支持多 API Key 轮换、代理、自定义请求头、模型白名单和路由前缀
- Responses API 支持多轮会话跟踪
- 前端构建产物嵌入后端，便于单二进制部署

## 赞助商

<table>
<tr>
<td width="180"><a href="https://www.compshare.cn/?ytag=GPU_YY_git_ccx"><img src="docs/sponsors/youyun-zhisuan.png" alt="优云智算" width="150"></a></td>
<td>感谢 <a href="https://www.compshare.cn/?ytag=GPU_YY_git_ccx">优云智算</a> 赞助了本项目！优云智算是 UCloud 旗下 AI 云平台，主打包月、按次的高性价比国模 Agent Plan 套餐，低至 49 元/月起。同时提供官转稳定海外模型，支持接入 Claude Code、Codex 及 API 调用，支持企业高并发、7*24 技术支持、自助开票。通过<a href="https://www.compshare.cn/?ytag=GPU_YY_git_ccx">此链接</a>注册的用户，可得免费 5 元平台体验金！</td>
</tr>
<tr>
<td width="180"><a href="https://runapi.co/register?aff=CqQO"><img src="docs/sponsors/runapi.svg" alt="RunAPI" width="150"></a></td>
<td>感谢 <a href="https://runapi.co/register?aff=CqQO">RunAPI</a> 赞助了本项目！RunAPI 是高效稳定的API OpenRouter平替平台，一个 API Key 即可访问 OpenAI、Claude、Gemini、DeepSeek、Grok 等 150+ 主流模型，低至 1 折，极其稳定，可以无缝兼容 Claude Code、OpenClaw 等工具。RunAPI 为 CCX用户提供专属福利：注册联系管理员即可领取￥7的免费额度</td>
</tr>
<tr>
<td width="180"><a href="https://unity2.ai/register?source=ccx"><img src="docs/sponsors/unity2-ai.jpg" alt="Unity2.ai" width="150"></a></td>
<td>感谢 <a href="https://unity2.ai/register?source=ccx">Unity2.ai</a> 赞助了本项目！Unity2.ai 是面向个人开发者、团队、企业的高性能 AI 模型 API 中转平台，长期服务国内头部企业，日均承载超 300 亿 token 调用，支持 5000 RPM 级高并发。一个 API Key 即可适配 Claude Code、Codex、OpenAI 模型、IDE 插件和 Agent 工作流等场景。具备企业级稳定供应能力，在高并发、持续调用和团队集中采购场景下依然保持低延迟、高可用。同时支持余额计费、组合订阅、首充优惠、企业开票、专属 1v1 对接，适合个人高频使用和企业长期接入。现在注册 Unity2.ai 可领取 $2 余额，加入官方群再送 $10 余额，合计最高可领 $12 免费额度，适合先体验后长期使用。注册地址：<a href="https://unity2.ai/register?source=ccx">https://unity2.ai/register?source=ccx</a></td>
</tr>
</table>

## 界面预览

### 渠道编排

可视化渠道管理，支持拖拽调整优先级，实时查看渠道健康状态和调度统计。

![渠道编排](docs/screenshots/channel-orchestration.png)

### 添加渠道

支持多种上游服务类型，灵活配置 API 密钥、模型映射和请求参数。

<img src="docs/screenshots/add-channel-modal.png" width="500" alt="添加渠道">

### 流量统计

实时监控各渠道的请求流量、成功率和响应延迟。

![流量统计](docs/screenshots/traffic-stats.png)

## 架构概览

CCX 对外提供一个统一后端入口：

```text
客户端 -> backend :3000 ->
  |- /                            -> Web 管理界面
  |- /api/*                       -> 管理 API
  |- /v1/messages                 -> Claude Messages 代理
  |- /v1/chat/completions         -> OpenAI Chat 代理
  |- /v1/responses                -> Codex Responses 代理
  |- /v1/images/{...}             -> OpenAI Images 代理
  |- /v1/embeddings               -> OpenAI Embeddings 代理
  |- /v1/models                   -> Models API
  `- /v1beta/models/*             -> Gemini 代理
```

当前 Images 入口包括：
- `POST /v1/images/generations`
- `POST /v1/images/edits`
- `POST /v1/images/variations`

详细设计请参考 [ARCHITECTURE.md](docs/guide/architecture.md)。

## 快速开始

### 方式零：CCX Desktop

CCX Desktop 提供原生桌面体验，通过 GUI 管理渠道、密钥和客户端配置。

| 平台 | 安装方式 | 说明 |
|------|---------|------|
| **Windows** | 在 [Microsoft Store](https://apps.microsoft.com/detail/ccx-desktop) 搜索 **CCX Desktop** | 推荐方式。自动更新，无需手动签名。也可从 [GitHub Releases](https://github.com/BenedictKing/ccx/releases/latest) 下载 `setup.exe`。 |
| **macOS** | `brew tap BenedictKing/ccx && brew install --cask ccx-desktop` | 或从 [GitHub Releases](https://github.com/BenedictKing/ccx/releases/latest) 下载 `.dmg`（支持 arm64/amd64）。 |
| **Linux** | 从 [GitHub Releases](https://github.com/BenedictKing/ccx/releases/latest) 下载 `.AppImage` | 设置可执行权限后运行。 |

详细安装和配置说明请参考 [CCX Desktop 用户教程](docs/guide/desktop)。

### 方式一：直接运行二进制

1. 从 [Releases](https://github.com/BenedictKing/ccx/releases/latest) 下载最新可执行文件。
2. 在可执行文件同目录创建 `.env`：

```bash
PROXY_ACCESS_KEY=your-proxy-access-key
PORT=3688
ENABLE_WEB_UI=true
APP_UI_LANGUAGE=zh-CN
```

3. 启动后访问 `http://localhost:3000`

Windows 下如果客户端运行在 cmd、PowerShell、WSL 或 Docker 中，且 `localhost` 无法访问 CCX，建议改用 Windows 主机的局域网 IPv4 地址，例如 `http://192.168.1.23:3000`。`BIND_HOST` 留空时，CCX 默认通过 `:PORT` 监听所有网卡地址；如需仅本机访问，设置 `BIND_HOST=127.0.0.1`。

需要后台运行或开机自启动时，参考 [非 Docker 自启动](docs/service/README.md)。

### 方式二：Docker

```bash
docker run -d \
  --name ccx \
  -p 3000:3000 \
  -e PROXY_ACCESS_KEY=your-proxy-access-key \
  -e APP_UI_LANGUAGE=zh-CN \
  -v $(pwd)/.config:/app/.config \
  crpi-i19l8zl0ugidq97v.cn-hangzhou.personal.cr.aliyuncs.com/bene/ccx:latest
```

使用 Docker Compose 后台运行：

```bash
docker compose up -d
```

启用 Watchtower 自动更新：

```bash
docker compose -f docker-compose.yml -f docker-compose.watchtower.yml up -d
```

首次部署后如需立即拉取最新镜像：

```bash
docker compose pull ccx
docker compose up -d ccx
```

### 方式三：源码构建

前置依赖：[Go 1.25+](https://go.dev/dl/)、[Bun](https://bun.sh)、Make（macOS 需先执行 `xcode-select --install`）。

```bash
git clone https://github.com/BenedictKing/ccx
cd ccx
cp backend-go/.env.example backend-go/.env
make install   # 安装所有依赖（前端 + Go 模块 + 开发工具）
make run
```

常用命令：

```bash
make dev
make run
make build
make frontend-dev
```

## 主要环境变量

```bash
PORT=3688
ENV=production
ENABLE_WEB_UI=true
PROXY_ACCESS_KEY=your-proxy-access-key
ADMIN_ACCESS_KEY=your-admin-secret-key
APP_UI_LANGUAGE=zh-CN
LOG_LEVEL=info
```

## 主要接口

- Web UI：`GET /`
- 健康检查：`GET /health`
- 管理 API：`/api/*`
- Claude Messages：`POST /v1/messages`
- OpenAI Chat：`POST /v1/chat/completions`
- Codex Responses：`POST /v1/responses`
- OpenAI Images：`POST /v1/images/generations`、`POST /v1/images/edits`、`POST /v1/images/variations`
- OpenAI Embeddings：`POST /v1/embeddings`
- Gemini：`POST /v1beta/models/{model}:generateContent`
- 模型列表：`GET /v1/models`

## 开发

推荐本地开发方式：

```bash
make dev
```

仅前端：

```bash
cd "frontend"
bun install
bun run dev
```

仅后端：

```bash
cd "backend-go"
make dev
```

## 相关文档

- [CCX Desktop 用户教程](docs/guide/desktop)
- [客户端接入总览](docs/guide/clients)
- [README.md](README.md)
- [backend-go/README.md](backend-go/README.md)
- [ARCHITECTURE.md](docs/guide/architecture.md)
- [DEVELOPMENT.md](docs/guide/development.md)
- [ENVIRONMENT.md](docs/guide/environment.md)
- [docs/service/README.md](docs/service/README.md) - 非 Docker 自启动
- [RELEASE.md](docs/guide/release.md)

## 社区交流

欢迎加入 QQ 群交流讨论：**642217364**

<img src="docs/qrcode_1769645166806.png" width="300" alt="QQ群二维码">

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=BenedictKing/ccx&type=Date)](https://www.star-history.com/#BenedictKing/ccx&Date)

## 许可证

MIT
