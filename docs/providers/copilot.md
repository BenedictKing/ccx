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

- 原生 Responses 默认直接透传，无需打开工具兼容开关；仅在目标模型不接受 Codex 工具格式且已验证转换有效时开启对应兼容选项。
- 如模型端点不支持图片工具，可开启 `移除 image_generation 工具`
- 按需配置 `Model Mapping`，将 Codex 客户端常用别名映射到 Copilot 实际模型名

## 可用端点

| 协议 | 端点 |
|------|------|
| Responses | `POST /responses` |
| OpenAI Chat | `POST /chat/completions` |
| Anthropic Messages | `POST /v1/messages` |
| Responses WebSocket | `ws:/responses`（需模型明确声明） |
| Models | `GET /models` |

每个模型支持的端点不同，以 `/models` 的 `supported_endpoints` 为准；不能因为模型名称是 Claude、GPT 或 Gemini 就推断协议。发现结果保留 `modelDetails`，文本能力测试与实际请求共用同一个模型路由解析器。

## 跨协议转换

四类文本入口都可以使用 `serviceType: "copilot"`。CCX 先应用模型映射，再从实际模型声明的端点中选择协议：优先与客户端相同的协议，其次依次选择 Responses、Messages、Chat。请求转换和响应解析使用同一结果，Copilot 认证与渠道统计身份保持独立。

| 场景 | CCX 行为 |
|------|----------|
| Responses → 支持 Responses 的模型 | 默认原生透传，接收原生 Responses SSE |
| Responses → 仅支持 Messages/Chat 的模型 | 使用已有转换器，响应转回 Responses |
| Chat/Messages → 支持相同协议的模型 | 使用原生协议，不经 Responses 中转 |
| Gemini 或不匹配的客户端协议 | 转换为模型支持的实际协议 |
| WebSocket → 声明 `ws:/responses` 的模型 | 使用原生 WebSocket v2，保留续轮字段与加密内容 |
| WebSocket → 未声明 WS 的模型 | 使用已有 HTTP/SSE bridge；不可无损重建的续轮明确失败 |

无论从哪个入口进入，CCX 都会：

1. 用 GitHub OAuth token 换取短期 Copilot token，读取模型的端点信息
2. 按映射后的模型选择协议，必要时转换请求体
3. 注入受保护的 Copilot 请求头，并请求选定端点；自定义头不能覆盖 Copilot 认证与身份头

使用默认 Base URL 时，优先采用 token exchange 返回的 API 地址；显式配置的非默认地址保持有效。端点路径按元数据精确拼接，不统一补 `/v1`。

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

模型能力缓存按 runtime token、API 地址、代理和 TLS 配置隔离，5 分钟后刷新。日志中的 `CopilotRoute` 显示实际模型、协议和路由来源；未记录 token。模型没有端点元数据、或旧接口明确以 404/405 表示不提供 `/models` 时，HTTP 保留历史 `/responses` 默认值并标记 `legacy-default`，不表示该模型已验证可用。认证失败、临时发现失败、模型未列出或端点均不支持时，返回诊断并尝试其它合格 Key/渠道；不猜测协议、不沿用过期能力。

自动发现中的工具、图片和思考回传测试目前保留“未验证”，不会使用错误协议的探测结果自动开启兼容改写；文本连通性按模型端点实际测试。

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
- Responses 加密历史无法解密后转成 Messages/Chat；包括通过 `previous_response_id` 找到的本地历史快照。此类请求应继续使用兼容的原生模型/渠道，不能通过删除密文来伪装成功。
- Copilot 的本地 compact v1/v2 也按摘要模型选择端点，响应按实际协议解析；显式推理续轮复用已固定的 Copilot 认证。
- 如需代理访问，请在渠道「代理地址」字段填写 HTTP/HTTPS/SOCKS5 代理 URL
