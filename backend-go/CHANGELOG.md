# CHANGELOG

## [Unreleased]

### Added

- 新增流式与非流式 `<think>...</think>` 标签提取状态机（`thinkTagStateMachine`），支持跨 chunk 边界的字符级状态机解析，将 Minimax 2.7 等模型的思考内容提取为原生 `reasoning_content`。
- 新增 `extractThinkTag` 共享函数，支持非流式响应（`ConvertOpenAIChatToResponsesNonStream` 与 `OpenAIChatResponseToResponses`）中的思考标签提取。
- 新增 `think_tag_fuzz_test.go`，包含 `SplitInvariant` 与 `ExtractThinkTag` 两个 fuzz 测试，覆盖 150 万次以上随机切分与边界输入。
- 新增 `responses_converter_think_test.go`，覆盖 think 在开头、与原生 `reasoning_content` 共存合并、middle 不剥离、only tool_calls 无空 message、未闭合 think 等单测场景。
- `ChannelLimiter` 增加 `lastActivity` 追踪与 `LastActivity()` 方法，支持 reaper 判断条目活跃度
- `ratelimit.Manager` 新增后台清理过期 scoped limiter 协程（48 小时无活动自动清理，1 小时检查周期），附带 `Stop()` 生命周期方法
- `ChannelScheduler` 新增后台 reaper（30 秒周期）自动推进到期的 loadShed 状态，附带 `Start()`/`Stop()` 生命周期方法
- main.go 启动/关闭 scheduler reaper 和 ratelimit manager 清理协程
- 目标模型下拉框异步数据到达后自动恢复显示（`watch(targetModelDatalist)`），修复首次 focus 时列表未返回导致下拉不出现的问题

### Changed

- 重构 `ConvertOpenAIChatToResponses`，将内联的 reasoning 和 content 处理逻辑封装为 `handleReasoningPart` 和 `handleContentPart`，使流式事件发射与状态机解耦。
- 移除 PR #83 引入的 `stripThinkTags` 直接丢弃逻辑，升级为协议级提取与原生推理字段转换。
- `ShouldDeferForRateLimit` 新增第三返回值 `inCooldown`；cooldown 检查提前到无限速配置判断之前，确保仅靠上游 Retry-After 学到 cooldown 的 scoped limiter 也能被软跳过；cooldown 不再写入 loadShed 状态，到期后立即可用，软跳时报告 utilization=1.0 防止调用方误判为低水位
- 30%–50% 使用率区间恢复策略优化：维持 `lowSince` 不变等待自然恢复，避免误重置恢复计时器
- CNY→USD 汇率从 1/7.2 更新为 1/6.8

### Fixed

- 修复被拉黑 Key 的历史统计数据在渠道图表（渠道历史、Key 趋势、活跃度）中消失的问题。统计查询现使用 `APIKeys ∪ HistoricalAPIKeys ∪ DisabledAPIKeys` 合并集合，确保拉黑 Key 的数据保留。
- 同步修正渠道日志的 metricsKey 枚举口径，保持日志与统计图表对拉黑 Key 的归属一致性。
- failover 增加 "exceeded" 关键词检测，修复火山方舟 `AccountQuotaExceeded` 等错误未被识别为余额不足的问题
- `cooldownUntil` 注释逻辑描述修正（"之前"→"之后"）
- 模型映射下拉框 blur 事件统一为 `hideTargetDropdown`，修复 `target-edit-end` 与下拉隐藏行为不一致
- 清理前端调试 `console.log` 日志，规范 `fetchTargetModels` 错误日志格式
- 临时 token-per-minute 配额限流（如 Gemini `Quota exceeded for quota metric ... tokens per minute`）不再被 "exceeded" 关键词误判为余额不足，避免可恢复 Key 被永久拉黑
- loadShed reaper 现在会为 high-watermark shedding 状态启动恢复计时器；实际恢复由 `ShouldDeferForRateLimit` 基于 limiter 实际利用率确认，避免空闲渠道永久被软跳过，也避免 limiter 仍有活跃请求时误删状态
- scoped limiter 清理前检查 cooldown 状态，避免上游要求的长 cooldown 被绕过

## [v2.7.5] - 2026-05-18

### Added

- 新增内置 OTA 更新能力：后端提供 `/api/system/update/check` 与 `/api/system/update/apply` 管理接口，支持 GitHub Release 版本检查、SHA256 校验、二进制替换备份与 Docker 环境禁用升级提示。
- 新增前端系统更新对话框，版本徽标优先通过后端检查更新，失败时保留 GitHub 直连降级路径，并支持升级后健康检查轮询。
- 发布工作流为 Linux、macOS、Windows 各平台资产生成并上传独立 `.sha256` 校验文件。
### Fixed

- 修复启用严格 Claude 兼容开关的 Messages 渠道会透传历史 `thinking` / `redacted_thinking` 块的问题，避免跨上游复用签名导致 `signature: Field required` 或 `Invalid signature in thinking block`。
- 补充空 `signature` 清理、畸形 thinking 块移除与 provider 层 thinking 剥离回归测试，确保普通 text 块空签名仍会删除。

- 修复 Responses 转 Chat 时孤儿 reasoning 生成 `content:null` 的 assistant 消息，避免 Codex 停止生成后继续输入触发 DeepSeek `Invalid assistant message: content or tool_calls must be set` 错误。
- 修复 Responses 转 Chat 时缺少 `type` 但包含 `role/content` 的输入消息被丢弃的问题，避免 Codex 简化 input 触发上游 `messages` 异常。
- 修复公共 `/v1/models` 与 `/v1/models/:model` 未纳入 Chat 渠道的问题，统一按 `messages → responses → chat` 聚合与回退模型查询，并保留 routePrefix 与已拉黑 key fallback 语义。
- 补充 `/v1/models` Chat 聚合与模型详情回退回归测试，覆盖去重优先级、routePrefix 与已拉黑 key fallback 行为。

- 修复 capability-test 在取消后恢复旧任务时返回过期的 `cancelled` job 快照，避免前端误判任务已结束而停止轮询。
- 为 capability-test 增加取消后恢复场景的 HTTP 回归测试，覆盖恢复响应状态正确性。
- 将 capability-test 的限速、共享结果与运行复用收敛到 upstream identity 维度，并新增 shared snapshot API 与单协议测试交互提示。
- 修复 capability-test 的 `chat` 与 `responses(codex)` 协议默认探测模型顺序不一致问题，统一将 `gpt-5.5` 提升为首位，并同步前端占位模型列表与后端探测配置。
