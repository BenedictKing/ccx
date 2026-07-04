---
name: model-update
description: 新增或更新 CCX 项目中的 Claude 模型注册。当用户提到新模型发布、添加模型、更新模型元数据、model registry、模型注册时触发。覆盖上游能力表、AgentModelProfile、优先级排序和代码生成。
version: 1.0.0
author: https://github.com/BenedictKing/ccx/
allowed-tools: Bash, Read, Write, Edit, Agent
context: fork
---

# CCX 模型注册更新技能

当用户输入包含以下关键词时触发：
- "新模型"、"添加模型"、"model registry"、"模型注册"、"添加 claude"、"新 claude"、"model update"

## 执行流程

### 0. 收集模型信息

向用户确认以下信息（可从发布文档或 skill 上下文提取）：

| 字段 | 示例 |
|------|------|
| 模型 ID | `claude-sonnet-5` |
| 显示名 | Claude Sonnet 5 |
| 上下文窗口 | 1000000 |
| 最大输出 tokens | 128000 |
| Thinking 模式 | `adaptive_only` / `adaptive_always_on` / `adaptive` / `extended` |
| Reasoning efforts | `["low", "medium", "high", "xhigh", "max"]` |
| Provider | `anthropic` |
| 是否更新 "sonnet"/"opus" 等 bare alias | 需要用户确认 |

### 1. 更新上游能力源（single source of truth）

**文件：** `shared/model-registry/ccx_model_registry.json`

在 `upstreamCapabilities` 数组中插入新条目，位置按家族分组（同家族内新版本在前）：

```json
{
  "patterns": ["(?:^|[-/])claude-<MODEL_ID>(?:-\\d{4}-\\d{2}-\\d{2}|-\\d{6,8})?(?=$|@)"],
  "provider": "anthropic",
  "displayName": "Claude <Display Name>",
  "contextWindowTokens": <N>,
  "maxOutputTokens": <N>,
  "thinkingMode": "<mode>",
  "reasoningEfforts": ["low", "medium", "high", "xhigh", "max"]
}
```

**注意：** 如果该模型存在 `-thinking` 变体（如 `claude-sonnet-4-6-thinking`），使用合并 pattern：`claude-<ID>(?:-thinking)?(?:-...)?`。4.6 系列以后的新模型通常不需要单独的 thinking 条目。

### 2. 更新后端 AgentModelProfile

**文件：** `backend-go/internal/config/model_registry.go` → `BuiltinAgentModelProfiles()`

添加 glob pattern 条目：

```go
"claude-<MODEL_ID>*": {
    DisplayName:         "Claude <Display Name>",
    ContextWindowTokens: <N>,
    MaxOutputTokens:     <N>,
    ReasoningEfforts:    []string{"low", "medium", "high", "xhigh", "max"},
},
```

如果需要更新 bare alias（如 `sonnet` → sonnet-5），同步更新：

```go
"sonnet": {
    DisplayName:         "Claude Sonnet alias",
    ContextWindowTokens: 1000000,
    MaxOutputTokens:     128000,  // 从 64000 更新到 128000
},
```

### 3. 更新模型优先级排序

**文件：** `shared/model-priority/model-priority.ts`

在 `modelPriorityPatterns` 数组中按正确位置插入 regex：

```typescript
/sonnet-5/i,   // 新 Sonnet 5 放在 opus-4-7 之后、sonnet-4-7 之前
```

**定位规则：** Claude 家族优先级从高到低为 `fable > opus > sonnet > haiku`，版本号大的在前。

### 4. 运行代码生成

```bash
node scripts/generate-model-registry.mjs
```

自动更新以下三个生成文件（DO NOT EDIT）：

- `backend-go/internal/config/generated_model_registry.go`
- `frontend/src/generated/modelRegistry.ts`
- `desktop/frontend/src/generated/model-registry.ts`

### 5. 验证

1. 确认生成文件包含新模型 ID：
   ```bash
   grep -r "claude-<MODEL_ID>" backend-go/internal/config/generated_model_registry.go frontend/src/generated/modelRegistry.ts desktop/frontend/src/generated/model-registry.ts
   ```

2. 编译检查（可选）：
   ```bash
   cd backend-go && go build ./...
   ```

3. 前端类型检查（可选）：
   ```bash
   cd frontend && bun run type-check
   ```

## 检查清单

完成更新后逐项核对：

- [ ] `ccx_model_registry.json` 中新增条目且 pattern 正确
- [ ] `model_registry.go` 中新增 AgentModelProfile
- [ ] `model-priority.ts` 中正确位置插入优先级模式
- [ ] bare alias 是否需要更新（向用户确认）
- [ ] `generate-model-registry.mjs` 已运行，三端生成文件已更新
- [ ] 验证 grep 确认新模型 ID 出现在生成文件中
