# 工具调用能力实测 设计文档

> 范围：把「渠道×模型能否真的执行工具调用」从模型名级静态推断升级为渠道实例级实测——能力测试主动探针落库、运行期被动负信号学习、SmartRouter 硬约束自动收紧。
> 状态：已落地（2026-08-29）。
> 证据来源：seekai 渠道路由 trace（2026-08-29 00:05，`corr_e1e69198c54dea19a0f146f2`）、代码锚点见各节。

## 1. 背景与问题现象

2026-08-29 用户复盘：某中转渠道（seekai）声称提供 `claude-opus-4-8`，真实会话中模型全程不产生 tool_use，纯文本回复（甚至反咬客户端系统提示是注入攻击）。而 CCX 侧该次请求的 trace 是：

```
statusCode=200, success=true, durationMs=15851, selectionReason=priority_order
stages=...protocol_federation:41,smart_filter:26,model_circuit_filter:26
  selected=5:seekai-cc/priority_order
```

**全程 200、零负信号**：对 CCX 而言这是一次成功请求，不触发熔断、不降权、不收紧画像，渠道画像的成功率反而漂亮。会话级后果是 agent 反复尝试"不用工具作答"，用户侧表现为能力残废。

问题的本质：**工具调用硬约束存在，但其数据源是按模型名解析的静态能力表**——它回答的是"`claude-opus-4-8` 这个模型支持工具吗"（是），而不是"seekai 这家中转真的实现了该模型的工具调用吗"（否）。假渠道只要报一个真实模型名就天然绕过。

## 2. 改造前现状盘点

| 环节 | 现状 | 缺口 |
|---|---|---|
| 路由硬约束 | `routingHardConstraintReasons`（`smart_router.go`）→ `CapabilityFloorReasons`（`capability_floor.go:16`）：`ToolUseNeed && !SupportsToolCalls` 时过滤候选 | 约束本身健全 |
| `SupportsToolCalls` 数据源 | 唯一写入点是静态能力表解析（`smart_router.go` buildChannelEntry，仅 `resolved.Known` 时填），解析链 channel→global→builtin（`model_registry.go:810`） | **按模型名判定**，对"渠道是否真实现"无感知 |
| endpoint 画像 | `KeyEndpointProfile.SupportsToolCalls`（`key_endpoint_profile.go:209`）字段预留 | 从未被任何探针写入；且画像被 L1 刷新循环反复 Upsert，写进去也会被冲掉 |
| 渠道发现 | `runDiscoveryToolCallProbe`（`channel_discovery.go:803`）有真实活动探针：强制 `tool_choice` + `ccx_probe` 工具，检查 SSE 是否返回工具调用，四协议通吃 | 跑在 **transient channel** 上（无 channelUID），结果只写 DiscoveryEvidence 展示与 codex 兼容推荐，不落库、不参与路由 |
| 能力测试 | `capability_test_runner.go` 逐渠道×协议×模型测试协议兼容 | **无工具调用测试项** |
| 运行期学习 | document 有完整先例：错误信号识别（`document_unsupported_signal.go`）→ 共享缓存 Record → `learnedDocumentUnsupported`（`document_capability_memory.go`）→ buildChannelEntry 收紧（只收紧不放松） | 工具无对应物；"200 但全程无 tool_use"被记为成功 |

## 3. 核心设计决策

### 3.1 落库点：ChannelCompatCache，而非 KeyEndpointProfile

初版设想把探针结果写进 `KeyEndpointProfile.SupportsToolCalls`（字段已预留、聚合链已消费）。核实后否决，理由：

1. **会被覆盖**：L1 画像刷新循环（`DeriveEndpointProfile` → Upsert）每次整体重建画像且不填该字段，探针结论下一轮即丢。
2. **粒度不符**：画像 key 是 endpoint（channel×baseURL×keyHash），不含模型维度；而工具能力是**渠道×模型**粒度的事实（同渠道不同模型表现可完全不同）。
3. **现成先例**：document 不支持记忆就是走 `ChannelCompatCache`——handlers 写 / autopilot 读经 `config.SharedChannelCompatCache()` 共享（依赖方向正确），24h TTL 自动重学习，落盘 `.config/channel_compat.json`，读取口径"任一 Key 命中即不支持"（保守，因路由决策先于选 Key）。

新增 trait `TraitNoToolCallSupport`（`no_tool_call_support`），与 `TraitNoDocumentSupport` 同类：**不可自动改写、仅供路由规避**的事实（剥掉 tools 等于改变用户意图）。

### 3.2 写入源：主动探针 + 两类被动信号

| 写入源 | 粒度 | 信号强度 | 触发条件 |
|---|---|---|---|
| 能力测试探针（层2） | 渠道×协议×模型 | 强（强制 tool_choice 专用探针） | 探针 2xx 且上游有有效输出但未产生工具调用 |
| 错误路径（层3a） | 渠道×Key×模型 | 强（错误文案点名 tools） | 400/422 + 文案点名 tools/tool_use/function calling |
| 流式成功路径（层3b） | 渠道×Key×模型 | 强（强制 tool_choice 真实流量） | 请求强制 tool_choice + 2xx 完成 + 全程零工具调用块 |

**弱信号一律不学**：与 document 不同（document 弱信号兜"通用 invalid_request"），带 tools 的请求占比极高（agent 流量全部带工具），把无具体所指的 400 归因到 tools 的误杀风险远大于 document。tools 的上游拒绝几乎总会显式点名，只学点名强信号。

**auto tool_choice 不做统计学习**：未强制 tool_choice 时"回复纯文本"是模型的合法行为，单次甚至多次无工具调用都不构成证据。误把健康渠道学成不支持工具是本改造最大的风险面，因此 3b 只认**强制 tool_choice**这一种无歧义形态。

### 3.3 读取收紧：只收紧不放松

`buildChannelEntry` 在静态表解析后追加（与 `learnedDocumentUnsupported` 同款、同位置语义）：

```go
if learnedToolCallUnsupported(channelUID, actualModel) {
    entry.SupportsToolCalls = false
}
```

即：注册表说不支持保持不支持；注册表说支持但实测拒绝，收紧为不支持。既有 `CapabilityFloorReasons` 工具硬约束随之自动生效，SmartRouter 与模型解析（`filterByCapabilityFloorInternal` 的 `NeedsToolCalls`）无需改动。

## 4. 三层实现

### 4.1 层1：trait 与路由收紧

- `config/channel_compat_cache.go`：`TraitNoToolCallSupport` 定义 + `AllCompatTraits` 注册 + `IsToolCallUnsupportedForChannelModel(channelUID, model)`（镜像 `IsDocumentUnsupportedForChannelModel`：SplitN 前两冒号精确比对模型名、TTL 内任一 Key `Enabled` 即 true、无记录 fail-open）。
- `autopilot/tool_capability_memory.go`：`learnedToolCallUnsupported` + 可替换 lookup（镜像 `document_capability_memory.go`，测试用内存桩）。
- `autopilot/smart_router.go` buildChannelEntry：document 收紧块之后追加工具收紧块。

### 4.2 层2：能力测试工具探针

`handlers/tool_call_probe.go`：

- `ToolCallProbeSummary{Tested, Supported, StatusCode, Evidence, Error}`，挂到 `ModelTestResult.ToolCalls`（json `toolCalls`，镜像 `CodexImageGeneration` 先例）。
- `runCapabilityToolCallProbe`：按被测模型的**实际模型名**（经 `RedirectModel`）构建四协议探针请求（messages/chat/responses/gemini，复用 `discovery*ToolCallProbeBody` 系列与 `sendCompatProbe`/`discoverySSEHasToolCall`），12s 超时。
- 结论口径（与渠道发现探针一致，但用于落库判定）：
  - SSE 中出现 `ccx_probe` 工具调用 → `Supported=true`；
  - 2xx 且有有效 SSE 内容但无工具调用 → `Supported=false`（**可学习**：上游明确收到了强制工具指令却未执行）；
  - 超时 / 非 2xx / 空或不可识别响应 → inconclusive（**不学习**：失败可能是容量或网关问题，不是能力问题）。
- 挂载点：`executeModelTest` 基础测试**成功后**附加执行（基础测试失败说明模型本身不可用，工具结论无意义）。**仅能力测试路径启用**：`executeModelTest` 增加 `probeToolCalls` 显式参数，能力测试（主任务/单模型重测）传 `true`，渠道发现两处调用传 `false`——发现流程复用同一执行函数，若不显式隔离会让每次发现对每个模型多发一条探针请求（既有用例 `TestRunDiscoveryProtocolProbeReusesBaseModelForThinkingSuffix` 的请求计数即此契约）。`Tested && ConfirmedUnsupported` 时 `Record(channelUID, keyHash, actualModel, TraitNoToolCallSupport, true, CompatSourceProbe, evidence)`，仅首次记录时打日志。

### 4.3 层3a：错误路径被动学习

`handlers/common/tool_unsupported_signal.go`：

- `BodyHasTools(body)`：请求侧门控，四协议 tools 字段非空即真（messages/chat/responses `tools` 数组、gemini `tools`）。
- `ToolUnsupportedFromError(statusCode, body, hasTools)`：仅 400/422；强信号正则点名 `tool use / tools / function calling` + not supported/unsupported/not enabled 等（镜像 document 的强信号风格，允许单词间隙与键名冒号如 `unsupported parameter: tools`）。无弱信号（§3.2）。
- 挂载点：`upstream_failover.go` 的 400/422 处理段，紧邻 document 学习块（在其后：具体参数名报错优先被弃用参数/compat-signal/document 块截获）。以 `CompatSourceErrorSignal` 记录 trait + `[ToolCallCompat]` 日志，不做同 Key 重试（无可改写之处，策略与 document 一致）。

### 4.4 层3b：流式成功路径被动学习

- `handlers/common/stream_observer.go`：`StreamTimeoutObserver` 增加 `sawToolCall`，`MarkToolCallActivity` 置位（该标记只在流中出现工具调用块时触发），新增 `SawToolCall()`。
- `handlers/common/tool_unsupported_signal.go`：`MaybeLearnForcedToolChoiceMiss(c, upstream, apiKey, model, attemptBody, sawToolCall)`——`ForcedToolChoiceInBody(attemptBody)` 为真且 `!sawToolCall` 时以 `CompatSourceRuntimeSignal` 记录 trait + `[ToolCallCompat]` 日志。
- 学习来源常量（`config`）：既有 `error_signal`/`probe` 之外新增 `CompatSourceRuntimeSignal`（运行期行为观测），供事后溯源区分证据强度。
- `ForcedToolChoiceInBody`：messages `{type:tool|any}`、chat `{type:function}`（含 legacy string 形态）、responses `{type:function|custom}`（扁平 name）、gemini `tool_config.function_calling_config.mode=ANY`。
- 挂载点：`upstream_failover.go` 成功分支 `FinishStreamTimeoutObservation` 之后。**仅 messages/responses 两种 executionKind**——只有这两条流式路径接了工具活动标记（`common/stream_processor.go` 的 ToolCallTracker / `responses/stream.go`），chat/gemini 流不标记工具活动，`sawToolCall=false` 无法区分"没调用"与"没观测"，参与学习必然误杀（§3.2）。
- observer 每次尝试新建（`StartStreamTimeoutObservation` 在成功分支内），无跨尝试污染。

## 5. 边界与保守策略

1. **非流式不参与 3b**：非流式响应体在各协议 handler 内部消费，逐协议挂钩改动面大；agent 流量（Claude Code/Codex）全部流式，层2 探针 + 层3a 已覆盖主要面。后续若有非流式工具流量诉求，再在协议层补 `responseText` 同款的工具观测标记。
2. **24h TTL**：与全部兼容性记忆一致，上游修复后自动解除误学；也可手动删 `.config/channel_compat.json` 对应条目后重启。
3. **渠道发现保持现状**：仍为 UI 证据展示（transient channel 无 UID，落不了库）；对已有渠道的实测由层2 能力测试承担。发现结果与能力测试探针共用同一套请求体构建与 SSE 判定函数，口径不会漂移。
4. **`KeyEndpointProfile.SupportsToolCalls` 字段保留不动**：聚合链 `AggregateChannelProfile` 的并集逻辑继续存在但恒为 false，无行为影响；未来若引入画像级工具能力（如订阅级共享结论），字段与链路已就绪。
5. **协议边界**：探针与学习仅覆盖 messages/chat/responses/gemini 四类；images/vectors 无工具语义，不参与。
6. **已知残余盲区**：上游把 tools 参数静默丢弃但模型恰好自发表意（无强制 tool_choice 的普通流量）无法检测——这要求无歧义信号，属可接受盲区（§3.2）。

## 6. 验证

- 单测：trait 查询口径（含 TTL 过期、跨模型不串）、错误信号正则（点名/非点名/非 400）、强制 tool_choice 四协议形态识别、探针 SSE 判定（tool_use SSE / 纯文本 SSE / 非 2xx / 空响应）、buildChannelEntry 收紧覆盖注册表、observer sawToolCall 置位。
- 集成口径：能力测试跑 seekai 类渠道 → `ModelTestResult.toolCalls.Supported=false` → compat cache 落盘 → SmartRouter trace 中该渠道在带工具请求下出现"工具调用能力不满足"过滤原因。
- 构建：`cd backend-go && make test`、`go build ./...`。
