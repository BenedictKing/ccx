# DeepSeek 配置指南

## 获取 API Key

1. 访问 [DeepSeek 开放平台](https://platform.deepseek.com/)
2. 注册并登录账号
3. 进入 [API Keys](https://platform.deepseek.com/api_keys) 页面
4. 点击「创建 API Key」，复制生成的密钥

## 工作原理

CCX 支持通过多种协议入口接入 DeepSeek：

```text
Claude Code CLI       ──→  /v1/messages          ──→  CCX  ──→  DeepSeek Anthropic 端点
Codex CLI/App         ──→  /v1/responses         ──→  CCX  ──→  DeepSeek Chat 端点
OpenAI 兼容工具       ──→  /v1/chat/completions  ──→  CCX  ──→  DeepSeek Chat 端点
```

一个 CCX 实例可以同时服务所有路径，按需配置对应协议的渠道即可。

## 场景一：OpenAI Chat 协议（通用）

适用于所有兼容 OpenAI Chat 协议的工具。

### 配置步骤

1. 进入 CCX 管理界面，选择 **Chat** 入口

![Chat 渠道列表](/images/guide/channel-list.png)

2. 点击「添加渠道」，切换到详细配置模式
3. 填写以下信息：

| 字段 | 值 |
|------|-----|
| **服务类型** | `OpenAI Chat` |
| **名称** | `DeepSeek Chat` |
| **Base URL** | `https://api.deepseek.com` |
| **API Keys** | 你的 DeepSeek API Key |
| **模型白名单** | `deepseek-v4-pro`, `deepseek-v4-flash` |

![添加 DeepSeek 渠道](/images/guide/add-channel-deepseek.png)

4. 保存

### 客户端配置

```bash
export OPENAI_API_KEY="your-ccx-proxy-key"
export OPENAI_BASE_URL="http://localhost:3000/v1"
```

---

## 场景二：Claude Code CLI

Claude Code CLI 使用 Messages API。需要在 Messages 入口配置 Claude 服务类型的渠道，指向 DeepSeek 的 Anthropic 兼容端点。

### 配置步骤

1. 进入 CCX 管理界面，选择 **Messages** 入口

![Messages 渠道列表](/images/guide/messages-channel-list.png)

2. 点击「添加渠道」
3. 填写以下信息：

| 字段 | 值 |
|------|-----|
| **服务类型** | `Claude` |
| **名称** | `DeepSeek Claude` |
| **Base URL** | `https://api.deepseek.com/anthropic` |
| **API Keys** | 你的 DeepSeek API Key |
| **模型白名单** | `deepseek-v4-pro`, `deepseek-v4-flash` |

4. 保存

### 模型映射（推荐）

Claude Code CLI 默认使用 Claude 模型名（如 `claude-opus-4-7`）发起请求。配置模型映射让 CCX 自动重定向到 DeepSeek 模型：

| 请求模型 | 重定向到 |
|----------|----------|
| `opus` | `deepseek-v4-pro` |
| `sonnet` | `deepseek-v4-pro` |
| `haiku` | `deepseek-v4-flash` |

![模型映射配置](/images/guide/model-mapping-deepseek.png)

### 客户端配置

```bash
export ANTHROPIC_API_KEY="your-ccx-proxy-key"
export ANTHROPIC_BASE_URL="http://localhost:3000"
```

验证：

```bash
claude "你好"
```

::: warning 注意
`ANTHROPIC_BASE_URL` 指向 CCX 网关根地址，不要加 `/v1` 或 `/v1/messages`。
:::

---

## 场景三：Codex CLI / App

Codex CLI 使用 OpenAI Responses API。需要在 Responses 入口配置 Chat 服务类型的渠道。

### 配置步骤

1. 进入 CCX 管理界面，选择 **Responses** 入口

![Responses 渠道列表](/images/guide/responses-channel-list.png)

2. 点击「添加渠道」
3. 填写以下信息：

| 字段 | 值 |
|------|-----|
| **服务类型** | `OpenAI Chat` |
| **名称** | `DeepSeek Chat` |
| **Base URL** | `https://api.deepseek.com` |
| **API Keys** | 你的 DeepSeek API Key |
| **模型白名单** | `deepseek-v4-pro`, `deepseek-v4-flash` |

4. 保存后，**编辑**该渠道，启用 **规范化非标准 Chat role** 开关

![高级选项开关](/images/guide/advanced-options.png)

::: tip 为什么需要启用？
Codex 的 Responses 请求转换为 Chat Completions 后，可能包含 `developer` 等 DeepSeek 不支持的 role。启用此选项后，CCX 会在发往上游前将其规范化为 `user`。
:::

### 模型映射（推荐）

Codex CLI/App 默认使用 GPT 模型名。配置映射让 CCX 自动重定向：

| 请求模型 | 重定向到 |
|----------|----------|
| `astra` | `deepseek-v4-pro` |
| `sol` | `deepseek-v4-flash` |
| `luna` | `deepseek-v4-flash` |

![Responses 渠道模型映射](/images/guide/model-mapping-deepseek.png)

::: tip 映射规则
CCX 优先使用更长的匹配键。内置预设使用 `astra`、`sol`、`terra`、`luna` 和 `codex-auto-review` 作为源模型别名；`sol` 也能匹配 `gpt-6.1-sol` 等真实模型名。需要区分具体型号时，为真实模型名配置更精确的映射即可。
:::

### 客户端配置

**Codex CLI：**

```bash
export OPENAI_API_KEY="your-ccx-proxy-key"
export OPENAI_BASE_URL="http://localhost:3000/v1"
codex "你好"
```

**Codex App（VS Code / JetBrains）：**

| 设置项 | 值 |
|--------|-----|
| API Key | `your-ccx-proxy-key` |
| Base URL | `http://localhost:3000/v1` |
| Model | `gpt-5`（CCX 自动重定向到 `deepseek-v4-pro`） |

---

## 可用模型

| 模型 | 说明 |
|------|------|
| `deepseek-v4-pro` | DeepSeek-V4 Pro 旗舰模型 |
| `deepseek-v4-flash` | DeepSeek-V4 Flash 快速模型 |
| `deepseek-chat` | DeepSeek-V3 通用对话模型（旧版） |
| `deepseek-reasoner` | DeepSeek-R1 推理模型 |

## 图片/视觉支持

DeepSeek 不支持图片输入。在 CCX 中配置 DeepSeek 渠道时，建议**关闭**视觉支持（编辑渠道时点击右上角的眼睛图标，使其变为关闭状态）。

![编辑渠道界面：DeepSeek 渠道的视觉支持已关闭（右上角眼睛图标为灰色），鼠标悬停时显示提示「此渠道不支持图片输入」。关闭后，包含图片的请求将自动跳过此渠道，failover 到下一个支持视觉的渠道](/screenshots/channel-deepseek-vision.png)

<figcaption>编辑渠道界面 — 关闭视觉支持后，眼睛图标变为灰色，悬停提示「此渠道不支持图片输入」</figcaption>

关闭后的行为：
- 纯文本请求正常路由到 DeepSeek 渠道
- 包含图片的请求会自动跳过该渠道，**failover 到调度队列中下一个支持视觉的渠道**
- 无需手动干预，CCX 会自动完成路由切换

::: tip
如果你同时配置了 DeepSeek（纯文本）和另一个支持视觉的渠道，CCX 会智能区分请求类型，将图片请求路由到支持视觉的渠道，文本请求仍走 DeepSeek。
:::

---

## 验证配置

```bash
curl http://localhost:3000/v1/models \
  -H "Authorization: Bearer your-ccx-proxy-key"
```

返回的模型列表中应包含你配置的 DeepSeek 模型。

## 故障排查

| 问题 | 解决方案 |
|------|----------|
| `401 Unauthorized` | 确认工具中的 Key 与 CCX 的 `PROXY_ACCESS_KEY` 一致 |
| `Model not found` | 确认渠道中的模型名称正确 |
| `Connection refused` | 确认 CCX 正在运行，Base URL 指向正确地址 |
| 渠道 unhealthy | 检查 DeepSeek API Key 是否正确，网络是否能访问 `api.deepseek.com` |
| Claude Code 响应格式异常 | 确认 `ANTHROPIC_BASE_URL` 指向根地址，不含 `/v1` |
