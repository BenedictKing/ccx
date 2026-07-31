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

## [ ] 管理后台使用报表导出 (#229)

来源：https://github.com/BenedictKing/ccx/issues/229

需求：在管理后台为渠道使用统计增加“导出使用报表”能力，支持按月份（默认当前月，后续可考虑自定义日期范围）导出当前渠道或全部渠道的使用数据。

建议范围：优先支持浏览器下载 CSV；JSON 可作为可选格式；XLSX 和定时邮件属于 nice-to-have，暂不作为首批必做项。

导出内容应覆盖当前仪表盘已有的核心指标：渠道名、服务类型、日期/小时桶、请求数、可用率、输入/输出 token、缓存读写、RPM、TPM。

备注：该需求目前不确定是否有更多用户需要，先记录为待评估项。

---

## [ ] gpt-5.6的适配

2.openai新增了"OpenAI-Beta": “{client_header:OpenAI-Beta}” 这个，来传输子代理相关信息，这个也需要调整
3.工具调用传参之类的都需要优化
4.提醒下同行，oai在最新版本codex里面加入了设备验证相关信息，可能会封pro号

---

## [ ] 在codex里面使用imagegen使用上游的文生图

百炼生图是生成url, 和 openai生成base64不一样，比如我在codex介入ccx, ccx配的是百炼，然后在codex里面用 codex自带技能 imagegen 能调用百炼生图模型 成功生图
https://github.com/QuantumNous/new-api/issues/5513

---

## [ ] 火山方舟团队版套餐自动探测与用量查询

当前状态：火山套餐绑定与用量刷新仅支持个人版 `Agent Plan` / `Coding Plan`。其中个人版分别使用 `GetAFPUsage` 和 `GetCodingPlanUsage`；`cd559c8a` 已修正个人版 Coding Plan 的 OpenAPI 地址、签名和百分比响应解析。团队版暂未纳入套餐识别、持久化和管理界面展示。

团队版候选调用链（统一使用 `open.volcengineapi.com`、`ark` 签名 scope）：

| 套餐 | 自动探测 | 用量查询 |
| --- | --- | --- |
| Agent Plan Team | `GetSeatInfo(Scene="agent_plan_enterprise")`，取得调用身份绑定的 `SeatID` | `GetSeatAFPUsage(SeatIDs=[SeatID])` |
| Coding Plan Team | `GetSeatInfo(Scene="")`，取得调用身份绑定的 `SeatID` | `GetSeatInfoUsage(SeatID=SeatID, Scene="")` |

设计约束：

- 同一火山账号可能同时拥有个人版与团队版，不能把“个人版查询失败后回退团队版”作为套餐判定；各套餐桶应独立探测、独立记录错误。
- 当前 `VolcengineAccessKeyPair` 只有单个 `Plan` 和单份 `Usage`。实现前需确定是增加 `edition: personal|team`、`seatId`，还是改为可同时保存多个套餐桶；不得让团队版结果覆盖个人版快照。
- 当同一产品同时存在个人版和团队版时，需要明确推理 Key 与订阅/席位的关联策略，无法可靠消歧时应展示候选并要求用户选择，不能静默猜测。
- 管理 API 仍不得回显 AK/SK；团队版权限错误和单个套餐桶失败不得阻断其他套餐的探测与展示。
- 保持现有个人版行为和配置文件向后兼容，并为四种套餐组合、无席位、多个套餐并存和 `AccessDenied` 增加测试。

参考：https://github.com/volcengine/ark-cli/blob/main/skills/arkcli-usage/references/arkcli-usage-plan.md

---

> **上游版本变更**

## [ ] Codex rust-v0.145.0 上游协议/工具变更评估

发现协议/工具/用法变更：audio inputs/outputs、reasoning parameters、response item ID prefixes、realtime V3、multi-agent V2、memories/paginated history。请评估对 ccx Responses 渠道的影响。

## [ ] Codex rust-v0.146.0 上游协议/工具变更评估

发现协议/工具/用法变更：Responses item ID 强制分配、自定义 Provider 独立 Web Search、Responses Lite Code Mode 元数据。请评估对 ccx Responses 渠道的影响。

---

## [ ] 英伟达渠道导入

可能模型太多响应太慢导致预检时间过长，或者需要代理？
