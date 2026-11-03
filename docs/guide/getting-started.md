# 快速开始

如果你希望通过桌面应用快速上手，可先阅读 [CCX Desktop 用户教程](/guide/desktop/)，按“安装 → 配置密钥 → 启动服务 → Agent 配置 → 添加渠道 → 验证请求”的路径完成基础配置。

## 安装

### Docker 部署（推荐）

```bash
docker run -d \
  --name ccx \
  -p 3000:3000 \
  -v ./.config:/app/.config \
  -e PROXY_ACCESS_KEY=your-proxy-key \
  crpi-i19l8zl0ugidq97v.cn-hangzhou.personal.cr.aliyuncs.com/bene/ccx:latest
```

### 二进制部署

从 [GitHub Releases](https://github.com/BenedictKing/ccx/releases) 下载对应平台的二进制文件，然后运行：

```bash
export PROXY_ACCESS_KEY=your-proxy-key
./ccx
```

服务默认监听 `http://localhost:3000`。

## 基本概念

### 渠道 (Channel)

渠道是 CCX 的核心概念，每个渠道对应一个上游 API 的配置，包括：

- **API Key**：上游服务的认证密钥
- **Base URL**：上游 API 的地址
- **模型列表**：该渠道支持的模型
- **优先级**：调度时的优先级权重

### 六类代理入口

| 入口 | 路径 | 说明 |
|------|------|------|
| Claude Messages | `/v1/messages` | Claude 原生协议 |
| OpenAI Chat | `/v1/chat/completions` | OpenAI Chat 协议 |
| Codex Responses | `/v1/responses` | OpenAI Responses 协议 |
| Gemini | `/v1beta/models/*` | Gemini 原生协议 |
| Images | `/v1/images/*` | OpenAI Images 协议 |
| Vectors | `/v1/embeddings` | OpenAI Embeddings 协议 |

## 访问管理界面

启动后访问 `http://localhost:3000`，使用 `ADMIN_ACCESS_KEY` 登录管理界面。

![渠道管理界面](/images/guide/channel-list.png)

在管理界面中，你可以：
- 添加和管理渠道
- 查看请求日志和流量统计
- 测试渠道连通性
- 调整渠道优先级

### 编辑渠道模型白名单与备注

打开渠道编辑窗口，在“基础信息”的“允许的模型（白名单）”中按协议路由填写规则，然后点击窗口底部的“保存”。每行一条规则，也可用逗号分隔；支持精确模型名、`gpt*`、`*luna`、`*deepseek*` 等通配符，以及 `!*luna` 等排除规则。留空并保存表示清除该路由的模型限制。

渠道列表和卡片优先显示非空的渠道备注，悬停可查看原名称；未填写备注时显示原名称。此显示方式不会修改渠道原名称或路由规则。

## 下一步

前往 [客户端接入](/guide/clients/) 配置 Claude Code、Codex CLI / App 或 OpenCode；前往 [配置教程](/providers/) 了解如何配置各个 LLM 提供商。
