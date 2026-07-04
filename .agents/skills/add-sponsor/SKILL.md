---
skill: add-sponsor
description: Add a new sponsor to CCX project following the established integration pattern
triggerWords: [add sponsor, new sponsor, integrate sponsor]
tags: [sponsor, integration, documentation]
---

# Add Sponsor Skill

自动化添加新赞助商的完整集成流程。

## 用法

```
/add-sponsor <sponsor-name> <description> <urls>
```

或者交互式调用：
```
/add-sponsor
```

## 执行流程

本 skill 会按照既定的赞助商集成模式，自动完成以下任务：

### 1. 收集赞助商信息

通过交互式问答收集：
- 赞助商名称（中英文）
- 简短介绍（中英文）
- 详细描述（中英文）
- Base URL
- 控制台 URL
- 推广链接（可选）
- 图标文件路径
- 赞助商顺序位置（在哪个赞助商之后插入）

### 2. 更新 README 文件

在两个 README 文件中按照赞助商顺序添加：
- `README.md` - 英文版本
- `README.zh-CN.md` - 中文版本

**位置**:
- 查找现有赞助商列表
- 根据指定的顺序位置插入新赞助商

**格式**:
```markdown
### [赞助商名称](推广链接)

<div align="center">
  <img src="docs/sponsors/sponsor-id.jpg" alt="Sponsor Logo" width="120"/>
</div>

赞助商详细描述...
```

### 3. 保存图标文件

- 复制图标到 `docs/sponsors/<sponsor-id>.jpg`
- 复制图标到 `desktop/frontend/src/assets/<sponsor-id>.jpg`

### 4. 前端 Web UI 集成

#### 4.1 快速识别配置
**文件**: `frontend/src/utils/quickInputParser.ts`

在 `knownOpenAIUrls` 或 `knownClaudeUrls` 中按顺序添加 Base URL：
```typescript
const knownOpenAIUrls = new Set([
  // ... 现有 URL
  'https://previous-sponsor.com/v1',
  'https://new-sponsor.com/v1',  // 新增
  'https://next-sponsor.com/v1',
])
```

### 5. 桌面端 Agent 配置集成

#### 5.1 前端类型定义
**文件**: `desktop/frontend/src/types/index.ts`

添加到 `AgentProvider` 类型：
```typescript
export type AgentProvider = '...' | 'new-sponsor' | '...'
```

#### 5.2 Provider 表单
**文件**: `desktop/frontend/src/components/agent/ProviderForm.vue`

在下拉选项中按顺序添加：
```vue
<option value="previous-sponsor">{{ t('agent.provider.previousDirect') }}</option>
<option value="new-sponsor">{{ t('agent.provider.newSponsorDirect') }}</option>
<option value="next-sponsor">{{ t('agent.provider.nextDirect') }}</option>
```

并添加图标导入：
```typescript
import newSponsorIcon from '@/assets/new-sponsor.jpg'

const providerIcons: Record<string, string> = {
  'new-sponsor': newSponsorIcon,
}
```

#### 5.3 Agent 配置逻辑
**文件**: `desktop/frontend/src/composables/useAgentConfig.ts`

在以下位置按顺序添加 `newSponsor`:

1. `claudeProviderLabels` - 添加标签
2. `codexProviderLabels` 的 computed - 添加标签
3. `claudeProviderKeys` - 添加空字符串
4. `isClaudeProvider` - 添加判断条件
5. `isCodexThirdPartyWithMode` - 添加判断条件
6. `claudeTargetBaseUrl()` - 添加 case 语句
7. `codexTargetBaseUrl()` - 添加 case 语句
8. `openCodeTargetBaseUrl()` - 添加 case 语句

**示例**:
```typescript
const claudeProviderLabels: Record<AgentProvider | 'custom', string> = {
  // ...
  'previous-sponsor': 'Previous Sponsor',
  'new-sponsor': 'New Sponsor',
  'next-sponsor': 'Next Sponsor',
}

const claudeTargetBaseUrl = () => {
  switch (selectedClaudeProvider.value) {
    case 'new-sponsor':
      return 'https://new-sponsor.com/v1'
    // ...
  }
}
```

#### 5.4 外部链接配置
**文件**: `desktop/frontend/src/lib/external-link.ts`

添加控制台和推广链接：
```typescript
export const providerConsoleLinks: Record<string, string> = {
  // ...
  'new-sponsor': 'https://new-sponsor.com/dashboard',
}

export const providerPromotionLinks: Record<string, string> = {
  // ...
  'new-sponsor': 'https://new-sponsor.com/register?aff=ccx',
}
```

#### 5.5 国际化翻译
**文件**:
- `desktop/frontend/src/locales/zh-CN.json`
- `desktop/frontend/src/locales/en.json`

添加翻译键：
```json
{
  "agent.provider.newSponsorDirect": "New Sponsor 直连"
}
```

### 6. 桌面端渠道中心集成

#### 6.1 渠道顺序配置
**文件**: `desktop/frontend/src/components/channel/ChannelTab.vue`

在 `presetOrder` 数组中按顺序添加：
```typescript
const presetOrder = [
  // ...
  'previous-sponsor',
  'new-sponsor',
  'next-sponsor',
  // ...
]
```

### 7. 后端 Go 代码集成

#### 7.1 配置服务
**文件**: `desktop/internal/configservice/service.go`

1. 添加 Provider 常量：
```go
const (
    // ...
    ProviderPreviousSponsor = "previous-sponsor"
    ProviderNewSponsor      = "new-sponsor"
    ProviderNextSponsor     = "next-sponsor"
)
```

2. 添加 Base URL 常量：
```go
const (
    // ...
    newSponsorBaseURL = "https://new-sponsor.com/v1"
)
```

3. 在所有 switch/case 和列表中按顺序添加处理逻辑

#### 7.2 渠道预设
**文件**: `desktop/internal/channelpreset/preset.go`

1. 添加 Provider 常量：
```go
const (
    // ...
    ProviderNewSponsor = "new-sponsor"
)
```

2. 添加到 `providerConsoleURLs`：
```go
var providerConsoleURLs = map[string]string{
    // ...
    ProviderNewSponsor: "https://new-sponsor.com/dashboard",
}
```

3. **使用辅助函数添加 Preset 配置**（推荐方式）：

对于标准的全协议支持赞助商：
```go
newFullCapabilityPreset(
    ProviderNewSponsor,
    "New Sponsor",
    "赞助商详细描述...",
    45,  // Order 值
    dualProtocolPlansSimple("https://new-sponsor.com/v1"),  // 统一入口
),
```

或者使用分离的 URL：
```go
newFullCapabilityPreset(
    ProviderNewSponsor,
    "New Sponsor",
    "赞助商详细描述...",
    45,
    dualProtocolPlans("https://new-sponsor.com/anthropic", "https://new-sponsor.com/v1"),
),
```

对于复杂配置（多个 Plans），手动指定：
```go
{
    ID:                  ProviderNewSponsor,
    Order:               45,
    Label:               "New Sponsor",
    Description:         "赞助商详细描述...",
    DirectAgent:         true,
    NativeMessages:      true,
    ChatCompatible:      true,
    ResponsesCompatible: true,
    Plans: []ProviderPlan{
        {ID: "anthropic", Label: "Anthropic-compatible", BaseURL: "https://new-sponsor.com/v1", Description: "Claude Messages 原生入口", Recommended: true},
        {ID: "openai-chat", Label: "OpenAI-compatible", BaseURL: "https://new-sponsor.com/v1", Description: "Chat / Responses 通用入口"},
    },
    Targets:      defaultTargets(),  // 使用辅助函数
    DefaultTarget: TargetMessages,
}
```

4. **如果支持原生 Responses 协议**，在 `applyTargetDefaults()` 中添加特殊处理：
```go
case TargetResponses:
    payload.ServiceType = "openai"
    payload.CodexToolCompat = true
    payload.StripCodexClientTools = true
    if provider == ProviderRunAPI || provider == ProviderUnity2 || provider == ProviderNewSponsor {
        payload.ServiceType = "responses"
        payload.CodexToolCompat = false
        payload.StripCodexClientTools = false
    }
```

5. **配置 Responses 目标**（如果需要过滤图像生成工具）：
```go
TargetResponses: {
    ProviderNewSponsor: {
        CodexToolCompat:            boolRef(false),
        StripCodexClientTools:      boolRef(false),
        StripImageGenerationTool:   true,  // 过滤图像生成工具
    },
}
```

对于其他目标，如无特殊配置可留空：
```go
TargetMessages: {
    ProviderNewSponsor: {},
},
TargetChat: {
    ProviderNewSponsor: {},
},
```

#### 7.3 国际化翻译
**文件**:
- `desktop/frontend/src/locales/zh-CN.json`
- `desktop/frontend/src/locales/en.json`

在 `channel.preset.*` 区域按顺序添加：

**中文版本**：
```json
{
  "channel.preset.new-sponsor.label": "New Sponsor",
  "channel.preset.new-sponsor.description": "赞助商详细中文描述...",
  "channel.preset.new-sponsor.plan.anthropic.label": "Anthropic 兼容",
  "channel.preset.new-sponsor.plan.anthropic.description": "Claude Messages 原生入口",
  "channel.preset.new-sponsor.plan.openai-chat.label": "OpenAI 兼容",
  "channel.preset.new-sponsor.plan.openai-chat.description": "Chat / Responses 通用入口"
}
```

**英文版本**：
```json
{
  "channel.preset.new-sponsor.label": "New Sponsor",
  "channel.preset.new-sponsor.description": "Detailed English description...",
  "channel.preset.new-sponsor.plan.anthropic.label": "Anthropic-compatible",
  "channel.preset.new-sponsor.plan.anthropic.description": "Claude Messages native endpoint",
  "channel.preset.new-sponsor.plan.openai-chat.label": "OpenAI-compatible",
  "channel.preset.new-sponsor.plan.openai-chat.description": "Common Chat / Responses endpoint"
}
```

### 8. Git 提交流程

创建功能分支并提交：
```bash
git checkout -b feat/add-<sponsor-id>-sponsor
git add <所有修改的文件>
git commit -m "feat(sponsors): add <SponsorName> sponsor integration"
git push -u origin feat/add-<sponsor-id>-sponsor
```

提交信息格式：
```
feat(sponsors): add <SponsorName> sponsor integration

- Add <SponsorName> to README files (en & zh-CN)
- Add <SponsorName> to channel quick input parser
- Add <SponsorName> to desktop agent configuration
- Add <SponsorName> to desktop channel center presets
- Add <SponsorName> icon to assets
- Follow sponsor order: <Previous> → <SponsorName> → <Next>
```

## 检查清单

执行完成后，确认以下所有项都已完成：

### 文档
- [ ] README.md 已更新
- [ ] README.zh-CN.md 已更新
- [ ] 图标已保存到 docs/sponsors/
- [ ] 图标已保存到 desktop/frontend/src/assets/

### 前端 Web UI
- [ ] quickInputParser.ts 已添加 URL

### 桌面端前端
- [ ] types/index.ts 已添加类型
- [ ] ProviderForm.vue 已添加选项和图标
- [ ] useAgentConfig.ts 已完整集成（8处）
- [ ] external-link.ts 已添加链接
- [ ] locales/zh-CN.json 已添加完整翻译（label + description + plans）
- [ ] locales/en.json 已添加完整翻译（label + description + plans）
- [ ] ChannelTab.vue 已添加排序

### 桌面端后端
- [ ] configservice/service.go 已完整集成
- [ ] channelpreset/preset.go 已添加 Provider 常量
- [ ] channelpreset/preset.go 已添加到 providerConsoleURLs
- [ ] channelpreset/preset.go 已使用辅助函数添加 Preset 配置
- [ ] channelpreset/preset.go 已配置 Responses 目标（如需要）
- [ ] channelpreset/preset.go 已添加原生 Responses 支持（如适用）

### Git
- [ ] 已创建功能分支
- [ ] 已提交所有更改
- [ ] 提交信息格式正确
- [ ] 已推送到远程仓库

### 编译测试
- [ ] Go 后端编译通过：`go build ./desktop/internal/channelpreset`
- [ ] 前端类型检查通过：`cd desktop/frontend && bun run type-check`

## 注意事项

1. **严格遵守赞助商顺序**：所有文件中的顺序必须一致
2. **Targets 顺序固定**：必须按照 **Messages → Responses → Chat** 的顺序排列，不能颠倒
3. **优先使用辅助函数**：
   - 使用 `defaultTargets()` 代替手动定义 Targets
   - 使用 `newFullCapabilityPreset()` 简化完整能力 Preset
   - 使用 `dualProtocolPlans()` 或 `dualProtocolPlansSimple()` 生成双协议 Plans
4. **图标格式**：建议使用 JPG 格式，尺寸约 120x120 像素
5. **URL 格式**：确保所有 URL 格式统一，去除尾部斜杠
6. **国际化**：
   - 中英文翻译都要提供
   - Go 后端的 Label/Description 使用英文
   - 前端通过 `tf()` 函数自动翻译
7. **Base URL 选择**：根据赞助商主要支持的协议选择合适的默认 Target
8. **Order 值**：按 10 的倍数递增（10, 20, 30...），便于后续插入
9. **原生 Responses 支持**：
   - 如果上游原生支持 Responses 协议，需在 `applyTargetDefaults()` 中特殊处理
   - 设置 `ServiceType = "responses"` 并禁用兼容性转换
   - 添加 `StripImageGenerationTool: true` 过滤图像生成工具

## 常见问题

**Q: 如何确定赞助商顺序？**
A: 参考现有赞助商的 Order 值，在合适的位置插入。通常按重要性和合作时间排序。

**Q: 如果赞助商同时支持多种协议怎么办？**
A: 在 Plans 中添加多个选项，并设置 Recommended 标记主推方案。

**Q: 什么时候使用 `newFullCapabilityPreset()`？**
A: 当赞助商支持完整的 Messages/Chat/Responses 三种协议时使用。大部分赞助商都符合这个模式。

**Q: `dualProtocolPlans()` 和 `dualProtocolPlansSimple()` 的区别？**
A:
- `dualProtocolPlansSimple(baseURL)` - 两个协议使用相同的 BaseURL（如 RunAPI、Unity2）
- `dualProtocolPlans(anthropicURL, openaiURL)` - 两个协议使用不同的 URL（如 DeepSeek）

**Q: 如何判断是否支持原生 Responses？**
A: 检查赞助商文档，如果明确支持 `/v1/responses` 端点，则需要在 `applyTargetDefaults()` 中添加特殊处理。

**Q: 为什么要添加 `StripImageGenerationTool`？**
A: Codex Responses 协议不支持图像生成，过滤掉这个工具可以避免上游错误。

**Q: Go 后端的 Label/Description 用中文还是英文？**
A: 统一使用英文。前端通过 i18n 配置自动翻译成对应语言。

**Q: 如何测试集成是否成功？**
A:
1. 编译测试：`go build ./desktop/internal/channelpreset`
2. 类型检查：`cd desktop/frontend && bun run type-check`
3. 启动桌面应用，检查渠道中心是否显示新卡片
4. 在 Agent 配置中检查是否能选择新的直连选项
5. 在 Web UI 粘贴 URL 检查是否自动识别
6. 切换中英文语言，检查翻译是否正确

## 参考实例

### Unity2.ai 集成实例（2026-06-22）

**相关 Commits**:
- `9f855f9c` - 添加 Unity2.ai 到 README
- `1bdf7bef` - 添加 anthropic plan 支持 Messages target
- `571cf71d` - 使用原生 Responses 协议
- `f42d742c` - 过滤图像生成工具
- `4e16a2bd` - 重构配置减少重复代码
- `c2e572ab` - 添加完整 i18n 翻译

**关键实现**:
1. 使用 `newFullCapabilityPreset()` 简化配置
2. 使用 `dualProtocolPlansSimple()` 生成双协议
3. 在 `applyTargetDefaults()` 中添加原生 Responses 支持
4. 添加 `StripImageGenerationTool: true`
5. 完整的中英文 i18n 翻译

**修改文件列表**:
```
README.md
README.zh-CN.md
docs/sponsors/unity2.jpg
frontend/src/utils/quickInputParser.ts
desktop/frontend/src/assets/unity2.jpg
desktop/frontend/src/types/index.ts
desktop/frontend/src/components/agent/ProviderForm.vue
desktop/frontend/src/composables/useAgentConfig.ts
desktop/frontend/src/lib/external-link.ts
desktop/frontend/src/locales/zh-CN.json
desktop/frontend/src/locales/en.json
desktop/frontend/src/components/channel/ChannelTab.vue
desktop/internal/configservice/service.go
desktop/internal/channelpreset/preset.go
```
