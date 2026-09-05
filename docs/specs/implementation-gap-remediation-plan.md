# Specs 实现缺口修复计划

> 基线：`6958cab5`（2026-09-05）
> 来源：对 `docs/specs/` 与当前实现的两轮逐项核验
> 状态：已完成（2026-09-05）
> 提交：Phase A=`540a06f5`（O1-O4）· Phase B=`8e9014ad`（Q1-Q2）· Phase C=`db445c13`（R1-R3）· Phase D=`6651419b`（Q3-Q4）· Phase E=`df5104b6`（C1）

## 1. 目标与范围

本计划修复规格标注“已实现”但当前代码仍存在的行为偏差，覆盖：

- `autopilot.md` §5.25 的上下文窗口学习与溢出重定向；
- `quota-truth-scheduling.md` 的状态刷新、并发安全、configured 数据源和前端真相展示；
- `route-preview.md` 的画像一致性、无副作用和错误响应契约；
- `ChannelCompatCache` 新增上下文窗口分区后的管理 API 闭环。

本计划不包括已有专项文档明确列为未来工作的能力，例如 estimated 配额、SQLite
配额持久化、DRR、manifest 自动回填和 Tier-2 OmniRoute backlog。修复应保持现有 JSON
配置向后兼容，不改变公开代理路由，不增加新依赖，不扩大自动模型重定向触发条件。

## 2. 问题清单与优先级

| ID | 严重度 | 问题 | 主要影响 | Phase |
|---|---|---|---|---|
| O1 | P1 | 溢出后处理条件恒假 | 密文未剥离，重定向头与日志缺失 | A |
| O2 | P1 | 溢出候选过早按协议+模型去重 | 可用物理渠道丢失，结果受 map 顺序影响 | A |
| O3 | P1 | 跨模型执行仍按原模型检查 Key 熔断 | 错放或错杀具体 Key | A |
| O4 | P1 | 成功实证过期后无法重新学习 | 7 天后放宽证据永久失效 | A |
| Q1 | P1 | 同来源配额刷新不覆盖旧值 | 调度长期使用陈旧余量 | B |
| Q2 | P1 | `quota.Manager` 暴露内部可变状态 | 并发读写与 data race 风险 | B |
| R1 | P1 | Route Preview 不加载全局 scenario | 预演与真实路由不一致 | C |
| R2 | P2 | Route Preview 通过 `BuildPlan` 写 trace | 违反无副作用契约并污染统计 | C |
| R3 | P2 | SmartRouter 未初始化返回 200 | 与规格和 dry-run 的 503 契约不一致 | C |
| Q3 | P2 | configured 配额入口无生产调用点 | 第三级真相只存在于测试 | D |
| Q4 | P2 | provider UI 不填真相字段 | 真相徽章和来源提示不显示 | D |
| C1 | P2 | compat-cache 不查看/清除窗口分区 | 运维无法诊断或重置新学习状态 | E |

优先级规则：先处理会错误发送请求或污染调度状态的 P1，再处理管理面和可观测性 P2。
每个 Phase 独立验证并提交，后续 Phase 不得依赖尚未验证的顺手重构。

## 3. Phase A：上下文溢出逃生链

### A1. 修复模型改写与后处理顺序（O1）

修改 `internal/handlers/common/upstream_failover.go`：

1. 在任何改写前保存 `originalModel := model`。
2. 计算 `executionModel`，仅在不同于原模型时更新请求体和运行时 `model`。
3. `overflowRedirect` 后处理以 `originalModel != executionModel` 为条件。
4. Responses 执行路由为 responses 且发生跨模型时，发送前调用
   `stripResponsesEncryptedContent`。
5. 响应头固定表达 `originalModel -> executionModel`，日志使用同一对值。
6. `sjson.SetBytes` 失败时不得静默更新运行时模型；应返回构建错误或保持原模型，禁止
   请求体与能力判断使用不同模型。

新增主链测试，不只测试辅助函数：断言实际传给 `buildRequest` 的 body 已移除
`encrypted_content`，且响应含 `X-CCX-Model-Redirect`。

### A2. 按物理候选去重（O2）

修改 `internal/autopilot/overflow_redirect.go`：

- 用 `route.Key()+model` 作为候选唯一键，不再使用 `kind|model`；
- 在 `ProbeSuccess`、协议画像匹配、有效窗口和路由存在性检查全部通过后再写 `seen`；
- 用显式有序协议切片替换 map 遍历，顺序固定为请求协议优先，再按预定协议顺序；
- 保留同模型的多个物理渠道，最终由 fanout 上限裁剪；
- 排序追加 `Route.Kind`、`Route.Index`、`ChannelUID` 稳定兜底键。

新增多渠道同模型测试：第一个窗口不足时第二个仍入选；两个均可用时可同时保留；重复运行
结果顺序一致。

### A3. 熔断检查绑定实际模型（O3）

把 `modelCircuitChecker` 的创建移到执行模型确定之后，或修改闭包使用调用参数中的模型。
scheduler 渠道级过滤、keypool 候选过滤、持久限制和请求结果回写必须统一使用
`executionModel/attemptModel`。新增用例覆盖“原模型正常、目标模型仅一把 Key 熔断”和反向场景。

### A4. 允许过期实证重新学习（O4）

`RecordContextWindowProven` 的更新条件改为：首次记录、证据已过期、或输入量高于当前棘轮值。
证据过期后重新学习时以本次成功值重新建立棘轮，不应保留一个已无法重新证明的历史高值。
新增确定性时钟或显式 `now` 用例，覆盖过期前不降、过期后较小成功值可重新起算。

## 4. Phase B：配额真相状态内核

### B1. 定义同来源刷新语义（Q1）

当前 `MergeValues` 只接受更高优先级来源，同来源后续观测会被永久忽略。调整为：

- 新维度直接写入；
- 更高优先级来源覆盖；
- 相同来源由更新调用中的最新观测覆盖；
- 更低优先级来源保持忽略；
- 查询失败不以空值清除最后一次成功数据，只更新 `Error/FetchedAtMs`。

本阶段无需给 `Value` 增加时间戳：Manager 的一次 update 已代表同来源的新快照。若未来需要接收
乱序异步事件，再单独引入 `ObservedAtMs`，本次遵循 YAGNI。

新增表驱动测试：provider API 80%→5%、响应头 0→重置后 100%、configured 倍率更新，以及
低优先级数据不能覆盖高优先级数据。

### B2. 收紧 Manager 并发边界（Q2）

采用“Manager 拥有状态，调用方只拿快照”的模型：

1. `GetChannelState` 返回深拷贝，至少复制 `Values` map。
2. `GetChannelHeadroom`、`GetChannelTruth`、`IsChannelSaturated` 和
   `ChannelSaturationRank` 在 Manager 读锁保护下读取一致快照。
3. 三个 `UpdateChannel*` 方法在写锁内完成 ChannelState 字段修改与 `MergeValues`。
4. BucketManager 更新放在 Manager 锁外，使用已复制的 values，避免扩大临界区和锁顺序反转。
5. 禁止调用方通过返回指针修改 Manager 内部状态。

避免给 `ChannelState` 再加一把锁：状态只由 Manager 管理，双层锁会增加顺序约束且没有额外收益。

新增并发测试：多个 goroutine 同时写 provider/header 数据并读取 headroom/truth，使用
`go test -race ./internal/quota -count=1` 验证。测试还应修改返回快照，断言内部状态不受影响。

## 5. Phase C：Route Preview 契约对齐

### C1. 对齐真实请求画像（R1）

`buildRoutePreviewProfile` 必须接收 `ScenarioRoutingConfig`，由 handler 从
`smartRouter.ConfigManager().GetConfig().AutopilotRouting.Scenario` 获取并传入。继续复用现有纯函数
提取方式，但增加对拍字段：

- `Complexity` 与 `DomainHints`；
- `ScenarioPreset` 与 `QualityTarget`；
- `ClientEffort` 和显式声明标记；
- `ToolUseNeed`、`VisionNeed`、`DocumentNeed`、`SeverityClassNeed`；
- `EstTokens`、`ContextNeed` 和 vectors 的 embedding dimension（如请求携带）。

若部分真实画像函数因包依赖无法直接复用，应抽到不依赖 Gin/handlers 的叶子包或 autopilot
纯函数中，由真实入口和 preview 共用；禁止继续维护两份逐渐漂移的复制实现。

### C2. 提供无 trace 的计划计算入口（R2）

保留 `BuildPlan` 当前行为供既有 dry-run API 使用，内部抽取：

```go
type BuildPlanOptions struct {
    RecordTrace bool
}
```

或等价的私有 `buildPlan(profile, recordTrace)`。Route Preview 显式传 `RecordTrace=false`；不得通过
临时替换 `SmartRouter.traceStore` 实现，因为并发请求会互相影响。scheduler 继续使用
`DryRun=true`，不更新亲和、override TTL 或最近选择状态。

测试使用真实 TraceStore：调用前后记录数和 SQLite 行数均保持不变，同时验证普通 dry-run 仍按
原契约记录 trace。

### C3. 统一错误契约（R3）

- SmartRouter 为 nil：返回 HTTP 503，与 `/api/smart-routing/diagnose` 一致；
- 外层或嵌套 body JSON 非法：返回 HTTP 400；
- 不支持的 `channelKind`：返回 HTTP 400，而不是退化为 messages；
- scheduler 不可用但 SmartRouter 可用：响应可保留 plan，但必须给出结构化降级原因；
- kill switch：保持 HTTP 200 + 明确 message，不执行 scheduler 预演，以免展示不会真实生效的选择。

端到端测试应挂载与生产一致的 `WebAuthMiddleware`，覆盖无管理凭证返回 401。

## 6. Phase D：配额生产接线与前端展示

### D1. 接通 configured 配额来源（Q3）

新增一个集中式同步函数，消费现有 `MultiplierSource`/new-api 产出并调用
`quota.Manager.UpdateChannelConfigured`。接线位置选择配置热更新和 new-api reconcile 的共同
成功出口，避免在各 provider handler 重复写入。要求：

- 只写有限、非负且有明确来源的额度值；
- 不把 `GroupMultiplier=0` 误判为“无数据”；
- 配置删除或来源失效时不把旧 provider_api 数据降级覆盖；
- 记录 channelUID/accountUID，不能只用数组 index。

补充 new-api 同步、热重载和手工配置三类集成测试，确认 `TruthLevel`/`Source` 可被 SmartRouter
和 scheduler 消费。

### D2. 补齐 UsageQuotaItem 真相字段（Q4）

为 MiniMax、火山、Kimi、Compshare、MiMo 的 item builder 统一填充：

- 成功读取 provider 官方快照：`truthLevel=healthy/approaching_limit/exhausted`，
  `truthSource=provider_api`；
- 已知支持但本次失败：`unavailable/provider_api`；
- 只有配置声明：`configured`；
- 无证据：`unknown/unknown`。

建议新增 `buildQuotaTruth` 前端纯函数，避免每个 provider 各自复制阈值逻辑。后端接口如未
提供行级真相数据，必须明确使用 provider 快照字段映射，不凭 UI 数值反推“官方可信度”。

补充各 provider builder 单测和 `UsageQuotaRows` 渲染测试，确认 unknown 不显示红色，tooltip
显示来源。

## 7. Phase E：兼容缓存运维闭环

### E1. 扩展 compat-cache 快照（C1）

`Snapshot` 响应新增上下文窗口分区，建议保持现有 `entries` 兼容并增加：

```json
{
  "entries": [],
  "contextWindows": [],
  "total": 0
}
```

窗口项至少返回 `channelUid`、`kind`、`model`、`provenInputTokens`、`provenAt`、
`modelsApiWindow`、`modelsApiAt` 和是否新鲜。禁止把 `channel|protocol|model` 原始复合键直接交给
前端解析。

`DELETE /api/compat-cache` 的空 trait 必须同时清除 traits、context limits、output limits 和
context windows；指定 trait 时保持现有 trait 语义，不误删窗口分区。若需要单独清窗口，增加
显式 `section=context-window`，避免与已有 trait 名称冲突。

补充 API handler 测试：快照字段、全清、按 trait 清除、按窗口分区清除及持久化文件内容。

## 8. 测试矩阵

### 后端单元与集成

| 范围 | 命令/测试 | 目标 |
|---|---|---|
| 溢出执行链 | `go test ./internal/handlers/common ./internal/scheduler ./internal/autopilot` | 模型改写、密文剥离、候选稳定排序、实际模型熔断 |
| 配额内核 | `go test ./internal/quota -count=1` | 同来源刷新、快照隔离、桶恢复 |
| 配额生产接线 | `go test ./internal/autopilot ./internal/config` | provider/configured 来源与热更新 |
| Route Preview | `go test ./internal/autopilot -run RoutePreview` | 画像对拍、无 trace、503/400/401 契约 |
| compat-cache | `go test ./internal/handlers -run CompatCache ./internal/config` | 新分区查看与清除 |
| 竞态 | `go test -race ./internal/quota ./internal/scheduler ./internal/handlers/common` | Manager 与失败转发并发安全 |
| 全量 | `go test ./...`、`go build ./...` | 防止跨模块回归 |

### 前端

- `bun run type-check`；
- `bun run build`；
- 真相徽章 builder 单测覆盖五种 TruthLevel 和四种 Source；
- Route Preview UI 回归确认 503/400 错误可展示，且 preview 不产生 trace 列表新增。

### 静态审查

- `rg` 确认 `UpdateChannelConfigured` 至少存在一条非测试生产调用链；
- `rg` 确认 preview 使用 `RecordTrace=false`；
- `rg` 确认所有 overflow 重定向均透传 `WithExecutionModel`、`WithExecutionRoute` 和
  `WithSelectionTrace`；
- 检查新增 API 不回显明文 Key，不把请求 body 写入 trace/log/metrics。

## 9. 实施顺序与提交边界

1. **提交 1：Phase A**：修复溢出执行链并补主链测试；不混入配额或 UI。
2. **提交 2：Phase B**：修复 quota Manager 状态覆盖与锁边界；补 race 测试。
3. **提交 3：Phase C**：Route Preview 画像、无 trace、错误契约；补后端/前端回归。
4. **提交 4：Phase D/E**：configured 接线、真相字段和 compat-cache 运维面。
5. 每个提交先运行其 Phase 定向测试，再运行全量验证；任何 P1 测试失败不得进入下一提交。
6. 修改现有 specs 时只更新“当前实现/验证状态”，不把计划中的修复提前写成“已完成”。

## 10. 完成定义

计划完成必须满足：

- O1–O4、Q1–Q2、R1–R3、Q3–Q4、C1 均有代码、测试和文件行号证据；
- `go test ./...`、`go build ./...`、前端 `type-check/build` 全部通过；
- 关键包 race 测试通过；
- Route Preview 不新增 trace、不发上游请求、不更新调度状态；
- 上下文溢出重定向能够正确改写模型、执行对应协议、剥离 Responses 密文并回显重定向头；
- 配额同来源刷新使用最新值，unknown/unavailable 仍保持 fail-open；
- 兼容缓存可查看、清除并持久化新窗口分区；
- 文档索引和 CHANGELOG 与实际提交同步，未保留“已实现但未接线”的描述。
