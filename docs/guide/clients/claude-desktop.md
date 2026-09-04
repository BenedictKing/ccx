# Claude Desktop 接入 CCX

Claude Desktop（桌面客户端）支持「第三方推理（Third-Party Inference）」模式：不登录 Claude 官方账号，把推理请求发给自建网关。CCX 的 Messages 入口实现了 Anthropic Messages 协议（含流式与工具调用）以及 `/v1/models` 模型发现端点，可直接作为 Claude Desktop 的推理网关。

## 工作方式

```text
Claude Desktop  ->  HTTPS 包装层  ->  CCX /v1/messages  ->  Messages 渠道  ->  上游 Anthropic 兼容端点
```

## 前提条件

1. CCX 已启动（例如 `http://localhost:3000`），并已设置 `PROXY_ACCESS_KEY`
2. Messages 入口下至少有一个可用渠道
3. 安装较新版本的 [Claude Desktop](https://claude.com/download)（菜单中存在 Developer 菜单即支持）

## 一、配置 CCX 渠道

与 [Claude Code](./claude-code) 相同，Claude Desktop 走 Messages 协议：

1. 打开 CCX 管理界面，进入 **Messages** 入口
2. 点击「添加渠道」，按上游提供商填写渠道配置

常见配置：

| 上游 | 服务类型 | Base URL 示例 |
|------|----------|---------------|
| Claude 官方 | `Claude` | `https://api.anthropic.com` |
| DeepSeek Anthropic 兼容 | `Claude` | `https://api.deepseek.com/anthropic` |
| Kimi 编码端点 | `Claude` | `https://api.kimi.com/coding/` |
| GLM Anthropic 兼容 | `Claude` | `https://open.bigmodel.cn/api/anthropic` |

::: tip
具体上游的 API Key、模型名和特殊开关，请参考对应的[提供商配置教程](/providers/)。
:::

## 二、为 CCX 提供 HTTPS 入口（必须）

Claude Desktop 的第三方推理网关**只接受 HTTPS 地址**。CCX 本地默认以 HTTP 监听（如 `http://localhost:3000`），不能直接填入 Gateway base URL，需要先用反向代理把本地 CCX 包装成 HTTPS。推荐使用 [Caddy](https://caddyserver.com/) 的本地自签证书（`tls internal`）：

1. 安装 Caddy：

```bash
# macOS
brew install caddy
```

```powershell
# Windows（任选其一）
winget install CaddyServer.Caddy
scoop install caddy
```

2. 在任意目录创建 `Caddyfile`，把 `127.0.0.1:3000` 换成你的 CCX 实际地址和端口：

```text
https://127.0.0.1:8443 {
    reverse_proxy 127.0.0.1:3000
    tls internal
}
```

3. 首次运行先让 Caddy 把本地 CA 安装进系统信任库，再启动：

```bash
caddy trust
caddy run
```

::: warning
`caddy trust` 在 macOS 上需要 sudo 权限。之后 Claude Desktop 通过 `https://127.0.0.1:8443` 访问 CCX，端口可自行调整，但协议必须是 HTTPS。
:::

## 三、启用开发者模式并配置网关

1. 启动 Claude Desktop，**无需登录** Claude 账号
2. 启用开发者模式：
   - macOS：顶部菜单栏 **Help → Troubleshooting → Enable Developer Mode**
   - Windows：登录界面左上角 **☰** 应用菜单 → **Help → Troubleshooting → Enable Developer Mode**
3. 打开 **Developer → Configure Third-Party Inference…**，在 **Connection** 区域填写：

| 字段 | 填写值 |
|------|--------|
| Inference provider | `Gateway`（默认） |
| Gateway base URL | `https://127.0.0.1:8443`（上一步的 HTTPS 包装地址，不要带 `/v1`） |
| Gateway API key | CCX 的 `PROXY_ACCESS_KEY` |
| Gateway auth scheme | `bearer`（默认）或 `x-api-key`，CCX 两者都接受 |
| Credential kind | `Static API key`（默认） |

4. 点击 **Test connection** 验证连通性
5. 点击 **Apply Changes** 保存并生效

配置完成后，Claude Desktop 主界面的模型选择器会显示网关提供的模型，对话请求经 CCX 转发到你配置的上游渠道。

## 四、配置模型列表

Claude Desktop 提供两种方式填充模型选择器：

- **Model discovery（自动发现，默认开启）**：自动请求 `{Gateway base URL}/v1/models` 拉取模型列表。CCX 已实现该端点，无需额外配置。但自动发现主要识别 Claude 系模型 ID，若渠道以非 Claude 模型名提供服务（如 `glm-4.7`、`deepseek-v4` 等），可能不会出现在选择器中。
- **Model list（手动列表）**：点击 **Add model** 逐个添加模型 ID，会覆盖自动发现的结果，**第一项为默认模型**。

建议：上游是 Claude 官方或渠道已做 Claude 模型名映射时，保留自动发现即可；使用非 Claude 模型名时，关闭 Model discovery 并用 Model list 显式列出，第一个填你最常用的模型。

## 五、多配置管理

如果有多套网关（例如本地 CCX 与远程部署的 CCX），可以通过配置切换：

1. 配置窗口右上角 **Select configuration** 下拉框当前为 `Default`
2. 在下拉框中复制现有配置、重命名或新建配置，每套配置独立保存 base URL、API key 和模型列表
3. 菜单栏 **Developer → Open Developer Config File…** 可直达当前配置对应的 JSON 文件，便于备份和迁移
4. **Export** 按钮可导出 `.mobileconfig`（macOS）或 `.reg`（Windows），供企业 MDM 批量部署

## 常见问题

### 提示 Can't reach 127.0.0.1:8443

Claude Desktop 连不上 HTTPS 包装层：

1. `caddy run` 是否仍在运行
2. Gateway base URL 的端口与 Caddyfile 监听端口是否一致
3. CCX 本身是否在运行（`http://localhost:3000` 能否访问管理界面）

### Test connection 返回 401

Gateway API key 与 CCX 的 `PROXY_ACCESS_KEY` 不一致。检查 CCX 启动配置和配置窗口中填写的密钥。

### 能连通但模型选择器为空

自动发现只认 Claude 系模型 ID。确认 Messages 渠道的模型白名单/映射里包含可用模型；若模型名非 Claude 系，改用 **Model list** 手动添加（见上文第四节）。

### 能否直接填 `http://localhost:3000`

不行。Claude Desktop 第三方推理要求网关地址为 HTTPS，`http://` 地址无法完成连接。必须按第二节做 HTTPS 包装。若 CCX 部署在远程服务器且已有有效 TLS 证书，可直接填 `https://你的域名`，无需 Caddy。

### 切换回官方 Claude

打开 **Developer → Configure Third-Party Inference…**，把 Inference provider 从 `Gateway` 切回官方登录（或直接登录 Claude 账号），即可恢复官方服务。
