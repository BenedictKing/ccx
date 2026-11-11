# GitHub Copilot

## 获取 GitHub OAuth Token

1. 打开 CCX 管理界面，进入 **Responses** 入口
2. 点击「添加渠道」
3. 选择上游类型 `GitHub Copilot`
4. 填写 `Base URL`（默认 `https://api.githubcopilot.com`，通常不需要修改）
5. 在「身份认证」区域点击「使用 GitHub Copilot 登录」
6. 记录页面显示的授权码，在打开的 GitHub 授权页确认授权
7. 授权成功后，系统会自动把 GitHub OAuth token 写入渠道 `API Keys`

## 在 CCX 中添加渠道

### Responses 入口（推荐）

| 字段 | 值 |
|------|-----|
| 名称 | `GitHub Copilot`（自定义） |
| 服务类型 | `copilot` |
| Base URL | `https://api.githubcopilot.com` |
| API Keys | 上一步授权生成的 GitHub OAuth token（`gho_...`） |

### 配置建议

- 启用 `Codex 原生工具透传`
- 如模型端点不支持图片工具，可开启 `移除 image_generation 工具`
- 按需配置 `Model Mapping`，将 Codex 客户端常用别名映射到 Copilot 实际模型名

## 可用端点

| 协议 | 端点 |
|------|------|
| Responses | `POST /responses` |
| Models | `GET /models` |

## 跨协议转换

GitHub Copilot 上游实际只暴露 Responses 协议（`/responses`）。CCX 会把其它入口的请求统一转换后再发给 Copilot，因此你可以在以下入口都使用 `serviceType: "copilot"` 的渠道：

| 客户端入口 | 客户端协议 | CCX 转换 | Copilot 上游 |
|------------|------------|----------|--------------|
| Responses | Responses | 透传 | `POST /responses` |
| Chat | OpenAI Chat | Chat → Responses | `POST /responses` |
| Messages | Anthropic Messages | Messages → Responses | `POST /responses` |
| Gemini | Gemini generateContent | Gemini → Responses | `POST /responses` |

无论从哪个入口进入，CCX 都会：

1. 把客户端请求体转换成 Responses 格式
2. 用渠道里保存的 GitHub OAuth token 换取短期 Copilot token
3. 注入 Copilot 运行时请求头，请求 `https://api.githubcopilot.com/responses`

说明：`serviceType: "copilot"` 会跳过默认 `/v1` 前缀，最终请求地址为 `https://api.githubcopilot.com/responses`。

## 模型映射建议

GitHub Copilot 的可用模型取决于你的订阅与组织策略，请以诊断结果中 `/models` 返回为准。常见做法是把 Codex 客户端常用别名映射到 Copilot 模型名：

| 源别名 | 目标模型（示例） |
|--------|------------------|
| `sol` | `gpt-5-codex` |
| `astra` | `gpt-5` |
| `luna` | `gpt-5-mini` |

实际可用模型名以「诊断 Copilot」结果中的 `Models` 返回为准，不要照抄示例。

## 渠道诊断

编辑 Copilot 渠道后，在「身份认证」区域点击「诊断 Copilot」，会一次性检查三层：

1. `GitHub`：GitHub OAuth token 是否能验证用户
2. `Token`：GitHub OAuth token 能否换取短期 Copilot token
3. `Models`：短期 Copilot token 能否访问 `/models`

哪一层失败就能快速定位问题。

## 常见问题

- **token exchange 返回 401/403**：GitHub OAuth token 失效或没有 Copilot 订阅，需重新授权。
- **token exchange 返回 429/5xx**：GitHub 侧临时不可用，稍后重试。
- **/models 返回 401/403**：通常是 `Copilot-Integration-Id` / Editor 版本头不匹配，或账号无对应权限。
- **请求误加 `/v1`**：确认服务类型选择的是 `copilot`，该类型不会自动补 `/v1`。


## 注意事项

- 渠道中保存的 `API Key` 是 **GitHub OAuth token**，不是短期 Copilot API token
- CCX 会在请求前自动把 GitHub OAuth token 换成短期 Copilot token，并注入 `Authorization: Bearer <copilot_token>`
- 不要手动把 GitHub OAuth token 当作 `Authorization: Bearer` 调用 `api.githubcopilot.com`
- OAuth 相关接口默认要求管理员鉴权，避免对外暴露 GitHub 登录流程
- 如需代理访问，请在渠道「代理地址」字段填写 HTTP/HTTPS/SOCKS5 代理 URL
