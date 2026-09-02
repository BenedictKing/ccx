---
name: model-update
description: 新增或更新 CCX 项目中的模型注册技能。适用于 Claude 与非 Claude 模型，覆盖能力表、路由白名单、基准映射、代码生成与验证。
version: 2.1.0
author: https://github.com/BenedictKing/ccx/
allowed-tools: Bash, Read, Write, Edit, Agent
context: fork
---

# CCX 模型注册更新技能

当用户输入包含以下关键词时触发：
- "新模型"、"添加模型"、"model registry"、"模型注册"、"模型版本更新"、"model update"

## 适用范围

适用于三类场景：

1. **新增独立模型（默认情形）**：仓库中还没有该 canonical model，需要补完整能力定义与必要映射。**一般添加模型都是新的独立模型**——带新主次版本号的模型（如 `claude-fable-5` → `claude-fable-5-1`、`claude-mythos-5` → `claude-mythos-5-1`）一律默认独立注册，不要归并进前代。归并会把 benchmark 证据混进前代 profile，图表张冠李戴、选型画像互相污染，事后拆分成本远高于一次独立注册。
2. **已有模型的 dated suffix / 快照别名**：如 `DeepSeek-V4-Pro-0813`、`claude-haiku-4-5-20251001`，通常只需要放宽 pattern、补测试，并检查特定 route 白名单是否也要同步放开。
3. **错误归并的别名拆分为独立模型**：发现既有归并搞错了（别名次代其实是独立模型），按"别名拆分检查清单"执行。

## 执行流程

### 0. 先判定属于哪一类变更

先回答这 4 个问题：

1. 仓库里是否已经存在 canonical model（如 `deepseek-v4-pro`）？
2. 新上线的是**全新 canonical model**，还是**已有模型的日期后缀/快照别名**？默认假设是独立模型；只有纯日期后缀（`-20260902` / `-0813`）或官方明确的快照别名才按别名处理。拿不准时按独立模型处理或向用户确认，不要默认归并。
3. 是否有 provider route 对该模型做了 `SupportedModels` 白名单限制？
4. 是否有 benchmark / preset / builtin manifest 依赖生成产物自动同步？

如果只是 dated suffix 更新，优先最小化修改，避免误改优先级和 alias。

### 1. 收集模型信息

优先从官方文档、项目上下文或现有同族模型推断以下字段：

| 字段 | 示例 |
|------|------|
| canonical model | `deepseek-v4-pro` |
| 新上线别名 | `DeepSeek-V4-Pro-0813` |
| provider | `deepseek` / `anthropic` / `openai` |
| 是否已有同族条目 | 是 / 否 |
| 是否只是日期后缀 | 是 / 否 |
| 是否影响 bare alias | 如 `sonnet` / `opus` / `deepseek-chat` |
| 是否影响特定协议路由白名单 | 如 Responses route |

### 2. 更新 single source of truth

**核心文件：** `shared/model-registry/ccx_model_registry.json`

#### 2.1 如果是新增 canonical model

在 `upstreamCapabilities` 中新增完整条目，按家族就近放置。

#### 2.2 如果是 dated suffix / 快照别名更新

优先修改已有 `patterns`，不要重复新增能力条目。注意此分支仅适用于纯日期后缀/快照别名；次代版本模型走 2.1 新增独立条目。

典型规则：
- 仅支持 `-YYYY-MM-DD` 与 `-YYMMDD` / `-YYYYMMDD` 时，pattern 常是 `-\\d{6,8}`
- 若 DeepSeek 新版本使用 `-MMDD`，需放宽到 `-\\d{4,8}`

例如：

```json
"(?:^|[-/])deepseek-v4-pro(?:-\\d{4}-\\d{2}-\\d{2}|-\\d{4,8})?(?=$|@)"
```

### 3. 检查是否需要改 route 白名单

**重点文件：** `backend-go/internal/config/provider_templates.go`

检查：
- `SupportedModels`
- provider 描述文案
- route 描述文案
- 对应测试 `backend-go/internal/config/provider_templates_test.go`

典型场景：
- 上游某协议端点此前只支持 `flash`
- 新版本上线后 `pro` 也支持该端点
- 此时不仅要放宽注册表 pattern，还要把 route 白名单从
  `[]string{"deepseek-v4-flash"}`
  扩成
  `[]string{"deepseek-v4-flash", "deepseek-v4-pro"}`

### 4. 仅在确有需要时更新其他源文件

按模型类型选择，不要机械全改：

#### Claude 系列常见附加点
- `backend-go/internal/config/model_registry.go` 里的 AgentModelProfile
- `shared/model-priority/model-priority.ts`
- bare alias（如 `sonnet` / `opus`）

#### DeepSeek / 其他非 Claude 系列常见附加点
- benchmark 映射（若是新 canonical model）
- preset / builtin manifest（通常由生成脚本自动产出）
- provider template 的 `SupportedModels`

**原则：**
- 新 canonical model：检查映射、优先级、预设
- 仅 dated suffix：优先只改 pattern + 测试 + 必要白名单

### 5. 补测试

至少覆盖：
- 裸模型名
- `-MMDD`（若适用）
- `-YYYY-MM-DD`
- `-YYMMDD`
- `-YYYYMMDD`
- 带命名空间前缀的变体（如 `deepseek-ai/...`）
- 若新增 route 白名单，补/改对应 provider template 测试

本项目里 DeepSeek 日期后缀测试位置：
- `backend-go/internal/config/model_registry_test.go`
- `backend-go/internal/config/provider_templates_test.go`

### 6. 运行生成脚本

```bash
node scripts/generate-model-registry.mjs
```

该脚本校验 registry（pattern 正则合法性等）并调用 `generate-preset-manifest.mjs`，同步更新：
- `backend-go/internal/presetstore/embedded/`（model-registry、builtin-manifest、channel-presets、capability-probe-schema 等 JSON + sha256 + index）
- `docs/public/presets/`（同名 JSON + sha256 + index）

修改 benchmark 映射后另需 `make benchmark-update` 重抓数据源并再生成图表（`docs/public/benchmark/`），registry 中的证据合并与计数字段由管线自动维护。

### 7. 验证

建议最少执行：

```bash
node scripts/generate-model-registry.mjs
cd backend-go && go test ./internal/config/...
```

必要时再补：

```bash
rg -n "<canonical-model>|<dated-suffix>" shared/model-registry backend-go/internal/config frontend/src/generated desktop/frontend/src/generated
```

## DeepSeek dated suffix 专项检查清单

当用户说“模型版本从 `deepseek-v4-pro` 更新为 `DeepSeek-V4-Pro-0813`”这类需求时，按下面顺序检查：

- [ ] `shared/model-registry/ccx_model_registry.json` 中该模型 pattern 是否仍是 `-\d{6,8}`，若是则改为 `-\d{4,8}`
- [ ] `backend-go/internal/config/model_registry_test.go` 是否补了 `-0813` 与大小写混合别名用例
- [ ] `backend-go/internal/config/provider_templates.go` 的 DeepSeek Responses route 是否还只白名单 `deepseek-v4-flash`
- [ ] `backend-go/internal/config/provider_templates_test.go` 是否同步更新
- [ ] 已运行 `node scripts/generate-model-registry.mjs`
- [ ] 生成产物与测试结果已核对

## 别名拆分专项检查清单

当发现既有"版本别名"归并搞错了（如 `claude-fable-5-1` 被并进 `claude-fable-5`），需要拆分为独立 canonical model 时，按下面顺序检查：

- [ ] `ccx_model_registry.json` 两处 pattern 都拆：`upstreamCapabilities` 与 `benchmarkProfiles` 中的 `(?:[.-]N)?` 归并段移除，新增独立条目/profile
- [ ] benchmark 证据按 `sourceModel` 前缀分到新 profile；新 profile 必须补齐 `sources`（非空）、`verifiedAt`、`lane`、`sharedResults`/`comparableCategories`/`totalCategories`（正数），否则 presetstore 校验 panic（口径见 `scripts/update-benchmark-data.mjs` 的 `createProfile`/`ensureEvidenceProfileMetadata`）
- [ ] 旧 profile 的 `sources` 清掉新模型专属 URL；`categoryScores`/`overallScore` 等聚合字段不用手工洗，下次 `make benchmark-update` 全量覆盖自愈
- [ ] benchmark 源映射全部改为自身映射：`scripts/benchmark-sources/artificialanalysis.mjs`、`mapper.mjs`（DEEPSWE/BENCHLM 两张表）、`litellm.mjs`；`rg -n "'<model-5-1>': '<model-5>'" scripts/` 确认无残留
- [ ] `backend-go/internal/config/model_registry.go` 的 `BuiltinAgentModelProfiles()` 新增 `model-5-1*` 条目（`resolvePatternValue` 按 pattern 长度降序，更长者优先，不会被 `model-5*` 遮蔽）
- [ ] `shared/model-priority/model-priority.ts` 新版本的更精确 pattern 放在前代之前（先新后旧）
- [ ] `shared/builtin-models-manifest/builtin-models-manifest.json` 与 `backend-go/internal/config/builtin_models_manifest.go` 手写副本同步新增；注意 manifest 测试里的 ModelIDs 数量断言
- [ ] 反转/重写变体解析测试（如 `TestResolveUpstreamCapability_ClaudeFable5Variants`）：次代变体解析到新 displayName
- [ ] 家族级逻辑（tier、路由策略、别名迁移、探测默认模型、渠道预设别名键）通常版本无关，不要顺手改
- [ ] 依次运行 `node scripts/generate-model-registry.mjs`、`cd backend-go && go test ./internal/...`、`make benchmark-update`、`make build`
- [ ] 抽查 `docs/public/benchmark/benchmark-chart.html` 中新模型独立成行

## 检查清单

完成更新后逐项核对：

- [ ] 先判断是"新增独立模型"（默认）、"日期后缀/快照别名"还是"别名拆分"
- [ ] 只修改必要文件，避免过度扩散
- [ ] `ccx_model_registry.json` 中条目或 pattern 正确
- [ ] 如有 route 白名单限制，`provider_templates.go` 与测试已同步
- [ ] 测试覆盖裸名与日期后缀变体
- [ ] `generate-model-registry.mjs` 已运行
- [ ] 相关生成产物已更新
- [ ] 验证命令已执行并记录结果
