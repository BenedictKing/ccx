# 客户端接入总览

如果你正在使用 **CCX Desktop**，建议先按桌面教程完成 [安装、密钥、启动服务、Agent 配置与添加渠道](/guide/desktop/)，再回到本页确认各客户端的接入差异。

## 选择客户端

| 客户端 | 推荐入口 | CCX Base URL | 适用场景 |
|--------|----------|--------------|----------|
| [Claude Code](./claude-code) | Messages | `http://localhost:3000` | 使用 Claude Messages 协议的编码助手 |
| [Claude Desktop](./claude-desktop) | Messages | `https://127.0.0.1:8443`（需 HTTPS 包装） | 不登录官方账号、经网关使用任意上游模型的桌面客户端 |
| [Codex CLI / Codex App](./codex) | Responses | `http://localhost:3000/v1` | 使用 OpenAI Responses 协议的 Codex 工具 |
| [OpenCode](./opencode) | Chat | `http://localhost:3000/v1` | 使用 OpenAI Chat 兼容协议的编码工具 |

## 接入前准备

1. 启动 CCX，确认服务地址，例如 `http://localhost:3000`
2. 设置代理访问密钥 `PROXY_ACCESS_KEY`
3. 在 CCX 管理界面中为目标入口添加至少一个可用渠道
4. 在客户端中把 API Key 填为 `PROXY_ACCESS_KEY`

## 入口关系

```text
Claude Code           ->  /v1/messages          ->  Messages 渠道
Claude Desktop        ->  /v1/messages          ->  Messages 渠道（经 HTTPS 包装层）
Codex CLI / App       ->  /v1/responses         ->  Responses 渠道
OpenCode              ->  /v1/chat/completions  ->  Chat 渠道
```

::: tip
不同客户端的 Base URL 规则不同。Claude Code 填网关根地址；Codex 和 OpenCode 通常填带 `/v1` 的 OpenAI 兼容地址。
:::

## 通用排查

更具体的问题请进入对应客户端页面查看。

| 现象 | 检查项 |
|------|--------|
| `401 Unauthorized` | 客户端 API Key 是否等于 CCX 的 `PROXY_ACCESS_KEY` |
| `Connection refused` | CCX 是否正在运行，端口是否为 `3000` |
| `model_not_found` | 渠道模型白名单和模型映射是否覆盖客户端请求的模型名 |
| 请求没有到达渠道 | 客户端是否使用了对应协议入口 |
