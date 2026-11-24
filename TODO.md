# TODO

## 更新规范

### 提交问题

在本文档末尾添加新条目，格式：

```markdown
## [ ] 简短标题

问题描述，包含复现条件和预期行为。
```

如有对应 GitHub Issue，在标题中标注，如 `## [ ] 标题 (#issue号)`。

### 解决更新

问题修复后，将 `[ ]` 改为 `[x]`，并在描述下方追加：

```markdown
**关键提交：**
- `commit_hash` commit message
```

如涉及多文件变更，可补充 `**关键变更：**` 列出受影响文件。

---

## [x] 管理后台使用报表导出 (#229)

来源：https://github.com/BenedictKing/ccx/issues/229

需求：在管理后台为渠道使用统计增加“导出使用报表”能力，支持按月份（默认当前月，后续可考虑自定义日期范围）导出当前渠道或全部渠道的使用数据。

建议范围：优先支持浏览器下载 CSV；JSON 可作为可选格式；XLSX 和定时邮件属于 nice-to-have，暂不作为首批必做项。

导出内容应覆盖当前仪表盘已有的核心指标：渠道名、服务类型、日期/小时桶、请求数、可用率、输入/输出 token、缓存读写、RPM、TPM。

完成：管理后台可选择月份、全部或单个渠道、按小时或按日导出 CSV；报表覆盖六协议及核心用量指标。费用按当前价格估算，历史数据受指标保留期限制。

---

## [x] Codex 自动审批模型映射说明 (#269)

来源：https://github.com/BenedictKing/ccx/issues/269

完成：Codex 请求 `codex-auto-review` 时，应在 Responses 渠道映射到上游支持的模型；文档同时说明模型白名单检查。

---

## [x] Codex 上下文窗口配置说明 (#271)

来源：https://github.com/BenedictKing/ccx/issues/271

完成：文档说明 CCX 渠道上游能力用于网关上下文路由，不会改变 Codex App 自身的上下文预算显示；渠道能力需配置在映射后的实际模型名上。

---

## [ ] Chat 转 Responses 的上下文用量差异 (#263)

来源：https://github.com/BenedictKing/ccx/issues/263

部分完成：Responses 渠道应用“低质量”开关的本地 usage 估算，偏差超过 5% 时修补；有缓存信号时保留上游输入计数。转换器内部输入不含缓存，客户端出口会加回缓存，不能只检查转换器中间值判断上下文低报。

2026-11-24 本地核查：三轮高缓存、Chat 转 Responses / 原生 Responses、HTTP / SSE、低质量开 / 关共 24 次假上游请求验证了出口用量和重复 developer 消息的保留。发现并修复原生 Responses 流低质量修补重复累加缓存的问题；该问题不等同于 #263 的 llama.cpp 低报。

仍需真实验收：同一轮的原始 Responses 请求、转换后 Chat 请求、上游完整 usage、llama.cpp `n_tokens` 和 Codex App 显示值；目前 issue 没有这些脱敏样本，本地估算不保证等于上游 tokenizer 计数。

**关键提交：**
- `4cc04b13` 转换路径 usage 出口对齐官方 OpenAI 口径（#274）。
- `68d7b282` 增加多轮高缓存出口回归并修正原生 Responses 缓存总量。

---

> **上游版本变更**

## [x] Claude Code v2.1.330 上游协议/工具变更评估

发现协议/工具/用法变更：`max_tokens`、`system`。2.1.330 恢复了 `max_tokens` 超过模型或网关限制时的自动重试，并将包含 `sonnet` 或 `haiku` 的未识别模型名改为使用较短系统提示词。请评估对 ccx Messages 渠道请求构造、错误重试和自定义模型兼容性的影响。

评估完成（2026-11-24）：两项均由客户端执行，未新增 Messages 字段或工具类型。CCX 已按渠道模型输出上限 clamp `max_tokens`；当前无需新增重试或系统提示词改写。输出上限回归通过，未进行 Claude Code 真实上游超限验收。

来源：https://github.com/anthropics/claude-code/releases/tag/v2.1.330

**关键提交：**
- `eedc7f3c` 按模型限制下调输出 token。

## [x] Codex rust-v0.171.0 上游协议/工具变更评估

发现协议/工具/用法变更：Responses 请求国家元数据、上游代理工具调用归因、Bedrock incremental tools。请评估对 ccx Responses 渠道请求元数据、工具调用和上游兼容性的影响。

评估完成（2026-11-24）：`request_country` 仅在 Codex 后端路径注入；归因头针对执行网络代理的 HTTP/CONNECT 请求；incremental tools 的默认开关针对 Bedrock。常规 CCX 自定义 API Key 接入无新增适配要求，现有原生增量工具透传回归通过。

来源：https://github.com/openai/codex/releases/tag/rust-v0.171.0 （PR #61535、#61860、#61426）。

**关键提交：**
- `b72d8dcf` 原生协议默认透明转发。

## [x] Claude Code v2.1.331 上游协议/工具变更评估

发现协议/工具/用法变更：`CLAUDE_CODE_DISABLE_RESPONSE_COMPRESSION`、无名称工具调用的响应处理。新增代理兼容开关可请求未压缩的模型响应，并将自定义 API 端点返回无名称工具调用改为报错后继续会话。请评估对 ccx Messages 渠道压缩响应转发和工具调用解析的影响。

评估完成（2026-11-24）：修复明确的 `Accept-Encoding: identity` 被移除的问题；Messages HTTP/SSE 假上游验证了实际编码协商、可读响应和请求头不被原地修改。其他编码仍按原有 transport 行为处理。无名称工具调用变化属于客户端容错，保留 CCX 现有工具校验边界。未启动 2.1.331 客户端做真实上游验收。

来源：https://github.com/anthropics/claude-code/releases/tag/v2.1.331

**关键提交：**
- `68d7b282` 保留禁用压缩请求并修正 Responses 缓存总量。

## [x] Codex rust-v0.172.0 上游协议/工具变更评估

发现协议/工具/用法变更：Responses 请求支持可选 Apps 工具结果、连接器结果透传及 WebSocket 传递，并即时上报响应流中的 provider 错误。请评估对 ccx Responses 渠道请求转换、工具结果透传、WebSocket 和流式错误处理的影响。

评估完成（2026-11-24）：私有 Apps 结果仅向官方 ChatGPT 地址发送，并要求匹配的 ChatGPT 认证、请求头和无自定义查询参数；自定义 CCX 地址不启用该路径。错误即时上报改变客户端处理时机，没有要求新事件类型；CCX 已识别 `error` / `response.error` / `response.failed`，现有错误、提交后不重放及 WS 回归通过。无需新增 Apps 结果转发功能，真实连接器验收不在本次范围。

来源：https://github.com/openai/codex/releases/tag/rust-v0.172.0 （PR #62388、#62392、#62421、#62474、#62356）。

**关键提交：**
- `b72d8dcf` 原生协议默认透明转发，保留现有流式错误边界。

---

