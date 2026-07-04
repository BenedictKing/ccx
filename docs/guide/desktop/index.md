# CCX Desktop

CCX Desktop 是面向普通用户的本地管理客户端，可以在桌面端完成安装、密钥配置、服务管理、渠道添加和客户端接入。

推荐使用路径：

**安装 → 配置密钥 → 启动服务 → Agent 配置 → 添加渠道 → 验证请求**

## 安装

### 下载

Windows 用户推荐在 Microsoft Store 中搜索 **CCX Desktop** 安装，由 Store 负责签名与自动更新。

macOS 用户也可通过 Homebrew 安装：

```bash
brew tap BenedictKing/ccx
brew install --cask ccx-desktop
```

开发者或无法使用 Store / Homebrew 的环境，可从 [GitHub Releases](https://github.com/BenedictKing/ccx/releases) 下载：

| 平台 | 文件名格式 |
|------|-----------|
| macOS (Apple Silicon) | `CCX-Desktop-{version}-darwin-arm64.dmg` |
| macOS (Intel) | `CCX-Desktop-{version}-darwin-amd64.dmg` |
| Windows (GitHub) | `CCX-Desktop-{version}-windows-{arch}-setup.exe` |
| Windows (Store/MSIX) | `CCX-Desktop-{version}-windows-{arch}-store.msix` |
| Linux | `CCX-Desktop-{version}-linux-amd64.AppImage` |

`*-store.msix` 主要用于 Microsoft Store 提交与验证，公开安装优先使用 Store；直接 sideload `.msix` 仍可能需要受信任签名环境。

可使用 Release 页面附带的 `.sha256` 校验文件验证下载完整性：

```bash
shasum -a 256 -c CCX-Desktop-*.sha256
```

### 安装方式

#### macOS

1. 双击 `.dmg` 文件。
2. 将 `CCX Desktop` 拖入 `Applications` 文件夹。
3. 打开 `Applications` 中的 `CCX Desktop`。macOS 版本已使用 Apple Developer ID 签名并完成公证，正常情况下可直接运行。

#### Windows

1. 在 Microsoft Store 中搜索 **CCX Desktop** 安装，签名与更新由 Store 处理。
2. 使用 GitHub 安装包时，双击 `-setup.exe` 并按提示完成安装。
3. 如果触发 SmartScreen 警告，点击 **更多信息 → 仍要运行**。

#### Linux

```bash
chmod +x CCX-Desktop-*.AppImage
./CCX-Desktop-*.AppImage
```

AppImage 支持应用内自动更新。若通过 deb/rpm 安装，需通过系统包管理器更新。

## 配置密钥

打开 CCX Desktop 后，首次启动向导会生成并写入 `PROXY_ACCESS_KEY`。

![CCX Desktop 首次启动向导](/images/desktop/setup-wizard.png)

### `PROXY_ACCESS_KEY` 的作用

- 用于客户端访问 CCX 网关。
- 配置 Agent 时，CCX Desktop 会把它写入客户端配置。
- 手动配置客户端时，应把客户端的 API Key 设为同一个 `PROXY_ACCESS_KEY`。

### 常见理解

| 密钥类型 | 说明 |
|---------|------|
| `PROXY_ACCESS_KEY` | 客户端访问 CCX 的代理密钥 |
| `ADMIN_ACCESS_KEY` | 管理密钥，用于 Web UI 登录与管理接口 |
| 上游 API Key | 上游服务商密钥，仅填写在 CCX 的渠道配置中 |

可在托盘菜单或 **Gateway Monitor** 中复制当前 `PROXY_ACCESS_KEY`，再粘贴到客户端配置或环境变量中。

## 启动服务

在 **Gateway Monitor** 中点击 **启动服务**。

![Gateway Monitor 网关监控](/images/desktop/gateway-monitor.png)

首次启动前，如果提示“二进制文件未找到”，可先在源码目录构建后端：

```bash
cd backend-go && make build
```

启动后请确认：

- 状态指示灯变绿。
- 页面显示网关端口与运行时长。
- Log Viewer 无明显错误。

Gateway Monitor 还支持：

- 停止 / 重启服务。
- 查看实时日志。
- 复制 Web UI 地址。

## Agent 配置

进入 **Agent Config**，可一键将本地 CCX 配置写入对应 Agent 工具：

![Agent Config 配置界面](/images/desktop/agent-config.png)

- **Claude Code**
- **Codex**
- **OpenCode**

推荐使用 CCX 作为 Provider，这样请求会经过本地网关，支持多渠道调度与故障转移。

CCX Desktop 主要写入的内容包括：

- 客户端应访问的 CCX 地址。
- 代理密钥（`PROXY_ACCESS_KEY`）。
- 相关模型或 provider 配置。

应用配置后，建议重启对应客户端，使新配置生效。

不同客户端的协议入口和 Base URL 细节，可在以下页面查看：

- [Claude Code 接入](/guide/clients/claude-code)
- [Codex CLI / Codex App 接入](/guide/clients/codex)
- [OpenCode 接入](/guide/clients/opencode)

## 添加渠道

进入 **Channel Center**，为目标入口添加至少一个可用渠道：

![Channel Center 渠道中心](/images/desktop/channel-center.png)

- Messages 渠道：用于 Claude Code。
- Responses 渠道：用于 Codex CLI / Codex App。
- Chat 渠道：用于 OpenCode。

可先选择预设模板快速创建，再在渠道编辑中补充：

- 上游 API Key
- Base URL
- 模型名或模型映射
- 相关兼容选项

::: tip
上游 API Key 只填写在渠道中，客户端中的 API Key 应填写为 CCX 的 `PROXY_ACCESS_KEY`。
:::

添加渠道后，建议在 Web UI 或日志中确认渠道状态可用。

## 验证请求

完成安装、密钥、服务、Agent 配置和渠道后，再进行端到端验证。

### 方式一：使用客户端验证

1. 启动 CCX Desktop 并确认服务已运行。
2. 重启对应客户端（Claude Code / Codex / OpenCode）。
3. 发送一条测试消息，例如：`你好`。
4. 在 CCX Desktop 的 Log Viewer 或 Web UI 中确认请求到达。

### 方式二：使用命令行验证

可以先验证模型接口是否可访问：

```bash
curl http://localhost:3688/v1/models \
  -H "Authorization: Bearer your-ccx-proxy-key"
```

再结合客户端实际请求路径确认协议入口是否正确：

- Claude Code 请求路径应为 `/v1/messages`
- Codex 请求路径应为 `/v1/responses`
- OpenCode 请求路径应为 `/v1/chat/completions`

## 环境配置

进入 **Environment Params** 可编辑 `.env` 文件，常用配置如下：

![Environment Params 环境参数](/images/desktop/env-params.png)

安装包运行时，`.env` 位于 CCX Desktop 数据目录，不是源码目录的 `backend-go/.env`。可在 **Gateway Monitor** 的 **Data dir** 查看并打开该目录；如需给 Claude Desktop 启用本地 HTTPS，请参考 [环境配置 - 本地 HTTPS](../environment#本地-https)，并在 `TLS_CERT_FILE` / `TLS_KEY_FILE` 中使用实际绝对路径。

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | 3688 | 网关端口 |
| `BIND_HOST` | 未设置 | 监听主机；不设置时监听所有网卡，填 `127.0.0.1` 仅允许本机访问 |
| `PROXY_ACCESS_KEY` | - | 客户端访问 CCX 的代理密钥 |
| `ADMIN_ACCESS_KEY` | - | 管理密钥 |
| `LOG_LEVEL` | info | 日志级别 |

修改 `.env` 后需重启服务生效。

## Web UI 与系统托盘

### Web UI

在 **Gateway Monitor** 中可直接打开内嵌 Web UI，用于管理渠道、查看日志和验证服务状态。

### 系统托盘

关闭窗口后，CCX Desktop 会最小化到系统托盘。

![CCX Desktop 侧边栏与守护面板](/images/desktop/sidebar.png)

托盘菜单常用功能：

- 查看运行状态、端口和 PID。
- 启动 / 停止 / 重启服务。
- 打开 Web UI。
- 复制 Web UI 地址和 `PROXY_ACCESS_KEY`。
- 开机自启开关。
- 检查更新。

## 自动更新

GitHub 安装包版内置自动更新：

- 启动 5 秒后自动检查一次。
- 之后每 30 分钟检查一次。
- 也可在侧边栏底部**点击版本号**手动检查更新。

Microsoft Store 版不走 GitHub Releases 自动更新，侧边栏和托盘会提示由 Store 更新。

GitHub 版更新流程：

1. 发现新版本 → 弹出更新对话框。
2. 下载安装包。
3. SHA256 校验。
4. macOS：打开 DMG 手动替换。
5. Windows：自动启动安装程序。
6. Linux (AppImage)：自动替换并重启。

## 首次成功检查清单

确认以下事项，即表示桌面端基本可用：

- [ ] `PROXY_ACCESS_KEY` 已生成并复制。
- [ ] 网关已启动，Gateway Monitor 状态正常。
- [ ] 至少一个目标渠道已添加并启用。
- [ ] Agent 已写入配置并重启。
- [ ] 客户端发送请求后，CCX Desktop 有对应日志。

更详细的启动失败、更新问题、Agent 配置无效等说明，请参考 [常见问题](./troubleshooting)。
