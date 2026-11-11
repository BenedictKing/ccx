# OpenCode 接入 CCX

如果你正在使用 **CCX Desktop**，可先在 [Agent Config](/guide/desktop/#agent-配置) 中写入 OpenCode 配置，再回到本页确认 Chat 入口和 Base URL 规则。

## 工作方式

```text
OpenCode  ->  CCX /v1/chat/completions  ->  Chat 渠道  ->  上游 Chat 兼容端点
```

## 一、配置 CCX 渠道

1. 打开 CCX 管理界面，进入 **Chat** 入口
2. 点击「添加渠道」
3. 添加一个 OpenAI Chat 兼容渠道

常见配置：

| 上游 | 服务类型 | Base URL 示例 |
|------|----------|---------------|
| OpenAI | `OpenAI Chat` | `https://api.openai.com/v1` |
| DeepSeek | `OpenAI Chat` | `https://api.deepseek.com` |
| GLM | `OpenAI Chat` | `https://open.bigmodel.cn/api/paas/v4` |
| MiniMax | `OpenAI Chat` | `https://api.minimax.io/v1` |
| Kimi | `OpenAI Chat` | `https://api.moonshot.ai/v1` |

::: tip
不同上游的模型名、视觉能力和特殊开关不同。先完成对应[提供商配置教程](/providers/)后，再配置 OpenCode。
:::

## 二、配置 OpenCode

### 使用 CCX Desktop 自动配置

在 **Agent Config → OpenCode** 中，默认选择 **CCX 本地网关**。Desktop 会维护：

- `~/.config/opencode/opencode.jsonc`：写入 OpenAI 兼容自定义 provider，例如 `provider.ccx.options.baseURL`
- `~/.local/share/opencode/auth.json`：写入对应 provider 的 API Key，例如 `auth.ccx.key`

默认 CCX 模式写入的 Base URL 为：

```text
http://127.0.0.1:<当前 CCX 端口>/v1
```

OpenCode 使用模型时选择 `ccx/<model>`，请求会通过 Chat 协议进入 CCX，再由 CCX Chat 渠道路由到已配置的国内上游。

Agent Config 也提供直连选项，但只列出当前适合 OpenCode OpenAI Chat 协议的国内厂商，以及 OpenCode Zen / OpenCode Go。其它 provider 仍可按 OpenCode 官方方式手动添加。

### 手动配置

在 OpenCode 中选择 OpenAI 兼容 / 自定义 Provider，并填写：

| 设置项 | 值 |
|--------|----|
| API Key | `your-ccx-proxy-key` |
| Base URL | `http://localhost:3000/v1` |
| Model | 客户端请求模型名，例如 `gpt-5`、`deepseek-v4-pro` |

如果你的 OpenCode 版本使用配置文件，核心仍是同一组值：

```text
API Key: your-ccx-proxy-key
Base URL: http://localhost:3000/v1
Model: your-model-name
```

::: warning
OpenCode 的配置界面和字段名可能随版本变化。只要选择的是 OpenAI Chat 兼容 Provider，就使用上面的 API Key、Base URL 和 Model 值。
:::

## 三、模型映射建议

如果 OpenCode 请求的是 OpenAI 风格模型名，但上游使用自己的模型名，可以在 Chat 渠道中配置模型映射。

示例：

| 请求模型匹配 | 映射到上游模型示例 |
|--------------|--------------------|
| `astra` | 上游旗舰模型 |
| `sol` | 上游主力模型 |
| `luna` | 上游轻量模型 |
| `deepseek` | DeepSeek 模型 |

如果你希望 OpenCode 直接请求上游模型名，也可以不配置映射，直接在 OpenCode 中填写渠道支持的模型名。

## 常见问题

### Base URL 应该写根路径还是 `/v1`？

OpenCode 走 OpenAI Chat 兼容协议时，Base URL 通常填写：

```text
http://localhost:3000/v1
```

不要填写到具体接口：

```text
http://localhost:3000/v1/chat/completions
```

### 返回 401 Unauthorized

检查：

1. OpenCode 中的 API Key 是否等于 CCX 的 `PROXY_ACCESS_KEY`
2. 是否误填了上游厂商 API Key
3. CCX 是否使用同一个 `PROXY_ACCESS_KEY` 启动

### 返回 404 或 Method Not Allowed

通常是 Provider 类型或 Base URL 不匹配。确认：

1. OpenCode 选择的是 OpenAI Chat 兼容 Provider
2. Base URL 是 `http://localhost:3000/v1`
3. CCX 中已配置 **Chat** 渠道，而不是只配置了 Messages 或 Responses 渠道

### 返回 model_not_found

检查 Chat 渠道：

1. 模型白名单是否包含请求模型或映射后的上游模型
2. OpenCode 中填写的模型名是否和映射规则匹配
3. 上游真实模型名是否正确

### 工具调用或多轮上下文异常

OpenCode 走 Chat Completions 协议，不同上游对工具调用、JSON 输出和 system message 的支持程度不同。遇到兼容性问题时：

1. 优先选择原生支持 OpenAI Chat 工具调用的上游
2. 降低模型能力开关或关闭上游不支持的响应格式
3. 如果问题只出现在某个上游，调整渠道优先级或模型映射，让该类请求走兼容性更好的渠道

### 请求没有出现在 Chat 渠道日志中

检查 OpenCode 当前 Provider 是否仍指向其它服务。CCX 侧应看到请求路径：

```text
/v1/chat/completions
```
