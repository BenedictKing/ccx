# 请求侧工具输出压缩 设计文档

> 范围：请求发出前对 messages 历史中的 tool_result 内容做 RTK 模式压缩。
> 状态：**已实现**（2026-09-01 落地，对应 omniroute-benchmark-upgrades.md §3 方向 B）。
> 蓝本：OmniRoute `open-sse/services/compression/`（fidelityGate.ts、pipelineGuards.ts、engines/rtk/）。

## 1. 背景与目标

| 环节 | 现状 | 缺口 |
|---|---|---|
| 会话压缩 | `handlers/responses/compact.go`、`compact_v2.go`、`compact_local.go`；`session/manager.go` `CreateCompactedSession` | 客户端主动调用的事后会话压缩，非网关带内 |
| 请求前处理 | `handlers/common/body_clamp.go` 等请求体处理链 | 无内容压缩钩子 |
| 遥测 | `metrics/sqlite_store.go` request_records token 四类计数 | 无压缩节省维度 |

**目标**：在请求发往上游前压缩 tool_result 历史，直接降低上游计费 token 与网络开销。

**作用域收窄**：只压 messages 历史中的 tool_result / 工具输出内容；**不碰 system、不碰最后一条 user 消息、不碰 tool_use 工具参数、不碰响应体**。CC 代理流量工具输出占大头，收益集中在这一块。

## 2. 核心设计决策

1. **分类器 + 结构化 filter 起步**：命令输出先分类（git / test / build / package / docker / shell / error / generic，共 8 类起步），每类 filter 保错误/警告/摘要/变更文件/tail 上下文，剥进度条/重复行/ANSI 噪声。
2. **保真门是红线**：压缩后校验受保护内容存活——JSON key 完整、数字字面量完整、diff hunk 完整、受保护 token 存活率阈值（≥95%）；任一不达标**回退原文**。压缩后体积反而变大也整体回退（膨胀回退）。
3. **fail-open**：压缩器 panic/超 CPU 预算时按原文放行，绝不因压缩失败拒绝请求。
4. **开关层级**：全局默认关 → 渠道级开启 → 请求头 opt-out（`x-ccx-compression: off`）；场景预设联动（`batch_cheap` 等价格敏感预设建议默认开）。
5. **遥测闭环**：每次压缩记录原始/压缩后 token 估算与回退原因，进 metrics（request_records 扩列），成本报表（`CostReportView.vue`）展示节省。

## 3. 架构

```text
[入站请求（messages/chat/responses/gemini）]
     │
     ▼
TryUpstreamWithAllKeys (upstream_failover.go)
     │  ┌─ preCall guardrail ─┐        ┌─ RTK 压缩 ────────────────────┐
     ├─►│ credential-masker  │──►───►│ Classifier → Filter → 保真门   │
     │  └────────────────────┘        │ → 膨胀回退 → 回退原文/发出     │
     │                                └──────────────────────────────-┘
     ▼
[发往 upstream]
```

### 3.1 Classifier 命令输出分类

`ClassifyCommand(text, command)` 从已知 command 或文本首行推断类别。

| 类别 | 识别依据 | 代表 filter |
|---|---|---|
| git | git status/diff/log/branch 命令或 On branch/diff --git/commit 特征 | 剥 diff --git/--- a/+++ b，保 @@ 和 [+-] 行 |
| test | go test/make test/jest/pytest/cargo test 命令或 FAIL/ok/Test Suites 特征 | 保 FAIL/●/Expected/Received/Test Suites，剥 PASS/✓ |
| build | tsc/bun build/vite build/eslint 命令或 error TS/✓ modules 特征 | 保 error/warning/✓/✗/built in |
| package | npm/pnpm/bun/pip/poetry 命令或 added/removed/audited 特征 | 保 added/removed/vulnerabilit/ERR! |
| docker | docker logs/ps/compose logs 命令或 CONTAINER/ERROR 特征 | 保 ERROR/WARN/failed/started |
| shell | ls/find/grep/rg 命令 | 剥空行，智能截断 |
| error | Traceback/panic/at .+:/Error: 特征 | 保 Error/Exception/Traceback/at/panic |
| generic | 兜底 | 剥 ANSI + 空行，保 error/failed/exception priority |

**置信度公式**：`commandMatched ? 0.55 : 0 + contentMatches * 0.25`，最高分 detector 胜出，未命中回 generic。

### 3.2 Filter 表驱动

每个 filter 含：
- `stripPatterns`：匹配的行被移除
- `keepPatterns`：非空时只保留匹配行（覆盖 strip）
- `collapsePatterns`：连续相同行折叠
- `priorityPatterns`：错误/警告/摘要行在截断时优先保留
- `replace` / `stripAnsi` / `deduplicate`：预处理
- `maxLines` / `headLines` / `tailLines` / `maxChars`：截断预算

**智能截断**：保留 head（默认 20-40 行）+ 所有 priority 行 + tail（默认 30-60 行），中间插入 `[compression: truncated middle]` 标记；若字符数超限，按 55% 头 / 45% 尾分字符预算，插入 `[compression: truncated by chars]`。

**强度缩放**（`effectiveMaxLines`）：
- `aggressive`：×0.5
- `standard`：×1.0
- `minimal`：×1.5

### 3.3 FidelityGate 保真门

按从便宜到贵顺序检查，任一失败立即回退：

| 检查项 | 规则 | 阈值 |
|---|---|---|
| Diff hunk | 每个唯一 `@@ -N,M +N,M @@` 头必须出现在压缩后文本 | 100% |
| 数字字面量 | 每个唯一 `\d[\d.,]{0,40}` 必须出现（有界量词防 ReDoS） | 100% |
| 受保护 token | URL / 常量名 / 环境变量 / 版本号 / 点分标识 / 函数调用 / 文件路径 / 行内代码 存活率 | ≥95% |
| JSON key | `"key":` 形式的键名存活率 | ≥90% |

**fail-open**：`CheckFidelity` 内部不返回 error，panic 由 engine 层 recover。

### 3.4 Plan 开关层级

| 优先级 | 来源 | 行为 |
|---|---|---|
| 1 | 请求头 `x-ccx-compression: off` | 强制关闭 |
| 2 | 场景预设 | `batch_cheap` 等价格敏感预设默认开 |
| 3 | 渠道级 | 预留（本期未暴露配置） |
| 4 | 全局 | 默认关 |

### 3.5 Engine 主体

`CompressRequestBody(bodyBytes, plan)`:
1. 校验 JSON 合法、plan 启用
2. 只遍历 `messages` 数组**除最后一条外**的消息
3. 只处理 `content` 数组中 `type=tool_result` 的块
4. 单条字节上限 256KB（超出跳过）
5. 分类 → filter → 保真门 → 单块膨胀检测 → sjson 写回
6. 总体膨胀检测（`totalCompressedTokens >= totalOriginalTokens` → 整体回退）
7. 返回 `Result{Body, Compressed, OriginalTokens, CompressedTokens, SavingsPercent, FilterCount, FidelityPassed}`

**条数预算**：最多处理 50 条 tool_result，超出跳过（防长历史 CPU 爆炸）。

### 3.6 挂载点

| 挂载点 | 位置 | 作用 |
|---|---|---|
| 请求侧 | `upstream_failover.go` `TryUpstreamWithAllKeys` 入口（guardrail 钩子之后） | 四协议（messages/chat/responses/gemini）归一形态上压缩，避免逐协议重复 |

仅 messages 类入口适用；images/vectors 入口直接返回原文。

### 3.7 遥测

- `request_records` 新增 v8 列：`compressed`、`original_tokens`、`compressed_tokens`、`compression_savings_percent`、`compression_technique`、`compression_fallback_reason`
- `CostReportView.vue` 展示"压缩节省"列 + 汇总卡片
- `costReportRow` / `CostReportRow` 新增压缩统计字段

## 4. 边界与保守策略

1. **fail-open 是铁律**：panic / 保真门不通过 / 膨胀均回退原文，绝不阻断请求。
2. **只压工具结果**：tool_use 参数、system、最后一条 user 消息、响应体永不压缩。
3. **流式响应不压缩**（响应侧不动，与 OmniRoute 同口径）。
4. **images/vectors 入口不适用**。
5. **不误删优先**：保真门之外，priorityPatterns 确保错误/警告/变更行优先存活。
6. **不做扩展字段语义**：压缩只删/折叠行，不改变 JSON 结构完整性。

## 5. 验证

### 表驱动单测（`internal/compression/compression_test.go`）

| 测试 | 覆盖 |
|---|---|
| `TestFidelityGate_DiffHunks` | 全部 hunk 存活通过 / 缺失一个失败 / 无 hunk 通过 |
| `TestFidelityGate_NumericIntegrity` | 数字完整通过 / 缺失失败 / 逗号形式数字通过 |
| `TestCalcProtectedTokenSurvival` | URL/常量/版本/env 等 token 100% 或低存活 |
| `TestCalcJSONKeySurvival` | JSON key 100% 或 ~20% 存活 |
| `TestFidelityGate_ProtectedTokenSurvival` | 端到端：全保留通过 / 大部分丢失失败 |
| `TestFidelityGate_EmptyInputs` | 空输入通过 |
| `TestInflationGuard` | 短内容 compressed=false 合理 |
| `TestFailOpen_InvalidJSON` | 非法 JSON 返回原 body 不报错 |
| `TestFailOpen_PanicRecovery` | 不 panic 即通过 |
| `TestCompressRequestBody_SkipsLastMessage` | 最后一条消息不被触碰 |
| `TestCompressRequestBody_NoToolResults` | 无 tool_result 不压缩 |
| `TestClassifier_CommandDetection` | 6+ 类命令识别 |
| `TestApplyFilter_RemovesNoise` | git diff 剥头保 hunk |
| `TestApplyFilter_GenericKeepsErrors` | generic filter 保 error priority |
| `TestPlan_Resolve` | 6 个开关层级分支 |

### 集成验证

- `make test`：35 个 Go 包全部通过
- `go build ./...`：全量编译通过（含 main embed）
- 前端 `bun run type-check` + `bun run build`：通过

## 6. 文件索引

| 文件 | 职责 |
|---|---|
| `internal/compression/types.go` | Result 结构定义 |
| `internal/compression/classifier.go` | 8 类命令分类器 |
| `internal/compression/filters.go` | 表驱动 filter + 智能截断 |
| `internal/compression/fidelity_gate.go` | 保真门（hunk/数字/token/JSON key） |
| `internal/compression/plan.go` | 开关层级解析 |
| `internal/compression/engine.go` | tool_result 压缩主体（fail-open/膨胀回退） |
| `internal/compression/compression_test.go` | 表驱动单测 |
| `internal/handlers/common/compression.go` | 挂载点适配（ApplyRequestCompression） |
| `internal/handlers/common/upstream_failover.go` | 请求侧挂载 |
| `internal/metrics/sqlite_store.go` | v8 迁移 + 压缩统计聚合 |
| `internal/handlers/cost_report_handler.go` | 压缩节省透传到成本报表 |
| `frontend/src/views/CostReportView.vue` | 压缩节省列 + 汇总卡片 |
| `frontend/src/services/api-types.ts` | CostReportRow 类型扩展 |

## 7. 未来扩展

| 扩展方向 | 说明 |
|---|---|
| 渠道级开关 | 从配置读取 per-channel 压缩开关 |
| 全局配置项 | `compression.enabled` / `compression.level` 进入 config.json |
| 更多 filter | 增大覆盖（terragrunt/kubectl/ffmpeg 等） |
| CPU 预算 | 复杂文本用时间预算限制压缩耗时 |