# 安全分类请求能力自学习 设计文档

> 范围：把「渠道×模型能否完成格式约束型安全分类请求」纳入运行期自学习与路由硬约束——分类形状请求（`</severity>` 停止序列）实测不遵循格式的组合自动规避；同时补齐出站尝试的可观测性缺口。
> 状态：已落地（2026-08-31）。
> 证据来源：agentrouter 渠道 deepseek-v4-flash 分类请求 29/29 失效（2026-08-31，metrics.db `route_model=claude-sonnet-5`），代码锚点见各节。

## 1. 背景与问题现象

2026-08-31 排查「Claude Code 安全分类器总是失败」：CC 的安全监控子请求要求模型仅输出 `<severity>N</severity>`（`max_tokens=64`、`stop_sequences=["</severity>"]`、无 tools）。请求模型 `claude-sonnet-5` 经 autopilot 自动映射落到 agentrouter 的 `deepseek-v4-flash` 后，上游 2xx 正常完成，但输出是中文闲聊（"分类器暂时不可用…"）+ 幻觉工具调用，**不含任何 `<severity>` 标记**。客户端无法解析 → 重试 → 会话粘性又落回同一渠道×模型 → 恒定失败。近 24h 该组合 29/29 全挂（`output_tokens` 恒为 9），而同为等价映射的 `gpt-5.6-sol` 13/13 正常。

问题本质与工具调用能力（`tool-call-capability.md`）同构：**基准等价 ≠ 行为等价**。注册表能回答"分数够不够档"，回答不了"这个渠道实例上的这个模型会不会严格遵守输出格式约束"——后者只能从真实流量自学习。期间还暴露一个可观测性缺口：完整出站体日志控制台侧被截断 1000 字符，`model` 字段位于 JSON 尾部，控制台视角"看不到实际发送的模型"，排查映射问题只能翻原始日志文件。

## 2. 设计

### 2.1 请求形状判定（无歧义信号）

`autopilot.SeverityClassRequestShape(body)`（`severity_class_memory.go`）：停止序列含 `</severity>` 即判定为分类形状请求。覆盖 messages（`stop_sequences` 数组）与 openai chat（`stop` 字符串/数组）两形态，协议转换保留该标记，对转换后的出站体同样有效。

**刻意只认停止序列**：它是客户端为精确截断输出设置的机器标记；system 提示词内容（"You are a security monitor…"）属弱特征，易误判普通安全类问答。

### 2.2 落库：ChannelCompatCache 新 trait

`config.TraitNoSeverityClass = "no_severity_classification"`，与 `TraitNoToolCallSupport` 同类：无请求改写可兜底、不进 `AllCompatTraits`、仅供路由侧读取。粒度沿既有键 `渠道:KeyHash:模型`；协议不进键（与工具调用先例一致：行为结论按渠道×模型事实处理），证据摘要记录触发现象。24h TTL 自动重学习；管理端 `GET/DELETE /api/compat-cache` 可查看/按 trait 清除（无需重启，此前运维只能删 `.config/channel_compat.json`）。

### 2.3 写入：仅运行期被动信号（层3b）

挂载点与 `MaybeLearnForcedToolChoiceMiss` 同一处（`upstream_failover.go` handleSuccess 之后），同样仅 messages/responses 流式路径——只有这两条路径接了标记扫描：

- **扫描**：`SeverityTagScanner`（`severity_class_signal.go`）跨增量检测 `<severity` 开标签（闭标签可能被 stop_sequence 截断，故只认开标签；SSE 分片切开由 N-1 尾部拼接兜底）。messages 挂在流循环文本增量处（`stream_processor.go`）；responses 挂两处——预检完成分支（短分类响应可能整体在预检阶段完成）与 post-commit 逐 SSE 行。
- **学习口径（防误杀红线）**：仅分类形状请求（对**实际出站体**判定）且 `handleSuccess` 无错误时学习——中途出错/客户端取消/空响应一律不学。输出含标记 → 能力确认（清除负结论，Record 翻转语义保证不重复落盘）；不含 → 负结论。

不做主动探针：分类请求频次高（每次工具调用前都有一发），被动学习收敛速度足够，主动探测反而白花钱。

### 2.4 读取：三层收紧（与工具调用完全同款）

1. **画像**：`RequestProfile.SeverityClassNeed` ← `SeverityClassRequestShape(入站体)`，经 `BuildCapabilityFloorFromRequestProfile` → `CapabilityFloor.NeedsSeverityClass`。
2. **渠道评分**（`smart_router.go` buildChannelEntry）：`SupportsSeverityClass` 默认 true（**注册表无此维度，构造处必须显式置位**，零值 false 会杀光全部候选），`learnedSeverityClassUnsupported` 命中则收紧为 false；`CapabilityFloorReasons` 以「安全分类格式能力不满足」过滤候选。
3. **模型映射**（`model_resolver.go` ResolveModel Step 3.5 与兜底枚举路径）：`filterSeverityClassCapable` 剔除该渠道上实测不行的模型，等价/精确命中路径同样看不到被剔除者；全部剔除走既有 `no_capable_model` → fail-open 透传链路。

零记忆 = 零影响（全链 fail-open）；学习命中只影响分类形状请求这一极小流量切片。

## 3. 出站尝试摘要日志（可观测性补缺）

`upstream_failover.go` 发送前新增一行（`EnableRequestLogs` 门控）：

```
[Messages-UpstreamAttempt] 渠道=[5] agentrouter-org (Messages) url=https://agentrouter.org/v1 模型 "claude-sonnet-5" -> "deepseek-v4-flash" 映射=auto_resolve key=sk-1fu***4Cj
```

映射字段三态：`auto_resolve`（命中自动映射）/ `fail_open:<原因>`（映射未命中透传）/ `passthrough`（无映射）。完整出站体仍走既有 `[X-Request-Body] 实际请求体`（控制台截断、文件全量）。

## 4. 边界与保守策略

- 误杀面分析：即使错误学到负结论，被影响的只是"停止序列含 `</severity>`"的请求；上游行为变化后 24h TTL 自动恢复，或管理端手动清除。
- 乐观翻转：同组合后续输出含标记即清除负结论（能力确认），中转商修复后无需等 TTL。
- 非流式路径不学习（CC 分类请求均为流式；非流式无标记扫描，`sawSeverityTag=false` 无法区分"没输出"与"没观测"）。
- gemini/chat 入站路径不学习（无扫描接线，同工具调用先例的约束）。

## 5. 验证

- 单测：`severity_class_memory_test.go`（形状判定/读取/候选过滤）、`channel_compat_severity_test.go`（记录/查询/翻转/清除/快照/跨 trait 隔离）、`severity_class_signal_test.go`（跨增量扫描/学习口径五分支）、`capability_floor_test.go`（硬约束三态）。
- 实测路径：重启后端后复跑 CC 分类请求 → 观察 `[SeverityClassCompat]` 学习日志 → 第二发请求应经硬约束避开 deepseek-v4-flash（trace FilterReasons 出现「安全分类格式能力不满足」或映射候选变化）。
