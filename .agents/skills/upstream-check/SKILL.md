---
name: upstream-check
description: 检查 Claude Code / Codex 上游版本变更，对比本地版本，识别协议/工具/用法相关更新并追加 TODO 提醒
version: 1.0.0
author: https://github.com/BenedictKing/ccx/
allowed-tools: Bash, Read, Write, Edit
context: fork
---

# 上游版本检查技能

当用户输入包含以下关键词时，自动触发上游版本检查流程：

## 触发条件

- "检查上游版本"、"上游检查"、"检查更新"、"upstream check"、"check upstream"

## 执行步骤

### 1. 运行检查脚本

```bash
bash .agents/skills/upstream-check/scripts/upstream-check.sh
```

### 2. 读取当前状态

```bash
cat .agents/skills/upstream-check/scripts/upstream-state.json
```

### 3. AI 判断协议变更

**重要**：脚本的关键词匹配（`matched_keywords`）仅作为初步筛选，存在误报风险（如 "system clipboard" 误报为 `system` 协议变更）。

必须通过 AI 二次判断：

1. 读取脚本输出的 `release_body_snippet`（前 800 字符）
2. 如果 `matched_keywords` 非空，分析每个关键词的上下文：
   - 是否涉及**协议格式变更**（如消息结构、字段定义、请求/响应格式）
   - 是否涉及**新工具/能力引入**（如新的 API 端点、工具类型、功能模块）
   - 是否涉及**核心用法变化**（如参数行为调整、默认值改变、废弃警告）
3. 排除以下情况的误报：
   - Bug 修复中的偶然关键词（如 "system clipboard"、"session's model"）
   - UI/UX 改进（如 "environment variables" 在设置说明中）
   - 性能优化、日志调整、错误提示改进
4. 输出最终判断：`真实协议变更` 或 `误报（仅 bug 修复/体验改进）`

**判断标准**：
- ✅ **真实变更**：影响 CCX 代理层协议转换、请求构造、响应解析的变更
- ❌ **误报**：仅影响 Claude Code/Codex 客户端内部行为，不影响 API 协议的变更

### 4. 升级建议逻辑

| 条件 | 输出 |
|------|------|
| `up_to_date: true` 且 `protocol_changes: false` | "✅ 已是最新版本，无需关注。" |
| `up_to_date: true` 且 `protocol_changes: true` | "✅ 版本已是最新。以下版本发布说明涉及协议/工具/用法变更，值得阅读了解：[关键字列表]" |
| `up_to_date: false` 且 `protocol_changes: false` | "⬆️ 有新版本 [remote_version] 可用（本地: [local_version]）。非紧急，可在方便时升级。" |
| `up_to_date: false` 且 `protocol_changes: true` | "⬆️ 有新版本 [remote_version] 可用（本地: [local_version]），涉及协议/工具/用法变更：[关键字列表]。**建议关注并评估对 ccx 的影响。**" |

### 5. 更新 TODO.md

**仅当 AI 判断为"真实协议变更"且远程 tag 不在 `seen_tags` 中时**，追加 TODO 条目。

**去重检查（必须）**：

1. 读取 `.agents/skills/upstream-check/scripts/upstream-state.json`，检查远程 tag 是否已在 `seen_tags` 中
2. 如已在，跳过 TODO 追加
3. 追加后，将 tag 加入 `seen_tags`（上限 5 条，超出时删除最早的）

**TODO.md 更新**：

- 检查 TODO.md 是否已有 `---` 分隔线 + `> 上游版本变更` 引用块标题，如无则追加
- 追加格式必须遵循仓库 TODO 规范：**每个待办项前面都要有 `[ ]`**
- 待办项统一使用 `## [ ]` 二级标题，与 TODO.md 其他条目保持一致
- 分组标识使用分隔线 + 引用块，不使用 `##` 标题，避免与待办项层级冲突
- 推荐追加格式：

```markdown
---

> **上游版本变更**

## [ ] Claude Code vX.Y.Z 上游协议/工具变更评估

发现协议/工具/用法变更：keyword1, keyword2。请评估对 ccx Messages 渠道的影响。

## [ ] Codex rust-vX.Y.Z 上游协议/工具变更评估

发现协议/工具/用法变更：keyword1, keyword2。请评估对 ccx Responses 渠道的影响。
```

### 6. 更新状态文件

将新的远程 tag 追加到 `.agents/skills/upstream-check/scripts/upstream-state.json` 的 `seen_tags` 数组中：

```json
{
  "last_checked_at": "2026-06-07T12:00:00Z",
  "claude_code": {
    "remote_tag": "v2.1.168",
    "local_version": "2.1.168",
    "seen_tags": ["v2.1.168"]
  },
  "codex": {
    "remote_tag": "rust-v0.137.0",
    "local_version": "0.137.0",
    "seen_tags": ["rust-v0.137.0"]
  }
}
```

### 7. 输出摘要报告

格式示例：

```
📋 上游版本检查结果

## Claude Code
- 本地版本: 2.1.168
- 远程版本: 2.1.168
- 状态: ✅ 已是最新
- 协议/工具变更: 无

## Codex
- 本地版本: 0.137.0
- 远程版本: 0.138.0
- 状态: ⬆️ 有新版本可用
- 协议/工具变更: 发现变更 - multi-agent, skills, plugins, web search
```

**重要**：协议/工具变更字段的逻辑：
- **仅当 `up_to_date: false` 且 AI 判断为"真实协议变更"时**，显示"发现变更 - [变更要点简述]"
- **其他情况（版本一致、无关键词匹配或 AI 判断为误报）**，显示"无"

### 8. 输出功能与体验变更报告

在摘要报告之后，追加一份面向用户的**功能与体验变更报告**：

1. 通过 `gh release view` 获取本地版本到最新远程版本之间所有中间版本的 Release Notes
2. 从每个版本的 Release Notes 中筛选**新增功能**（New Features）和**体验改进**（Improvements / 性能优化 / UX 修复等）
3. 按 Claude Code / Codex 分类，以表格和列表结构化呈现
4. 末尾附「对 CCX 的潜在影响要点」，关联到 CCX 五类渠道（Messages / Chat / Responses / Gemini / Images）
5. 语调：技术导向但易读，面向开发者

仅当存在版本差异（`up_to_date: false`）时才生成此报告；版本一致时跳过。

## 说明

- 脚本只输出 JSON 到 stdout，不写磁盘
- 状态持久化由 skill 负责
- 脚本网络失败时（exit 1），不更新状态，仅输出错误
- `matched_keywords` 仅作为初步筛选，最终是否为协议变更由 AI 判断
- AI 判断时需排除 bug 修复、UI 改进中的偶然关键词匹配（如 "system clipboard"、"session's model"）
