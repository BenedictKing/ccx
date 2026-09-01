# 路由预演升级（Route Preview）设计文档

> 范围：基于请求体直喂的零上游请求路由预演端点，及配套前端 DiagnosePanel 升级。
> 状态：**已实现**（2026-09-01 落地，v3.0.x）
> 蓝本：OmniRoute `POST /api/omniroute/route/preview` + `decisionTrace.ts`
> 出处：从 [omniroute-benchmark-upgrades.md §5](omniroute-benchmark-upgrades.md) 拆分

## 1. 背景与现状

### 1.1 现状盘点

| 环节 | 现状 | 缺口 |
|---|---|---|
| SmartRouter dry-run | `POST /api/smart-routing/diagnose` 已零上游请求 | 需调用方手工填 capability 布尔、`estTokens` 等特征，特征提取责任在调用方 |
| 请求画像 | `handlers/common/autopilot_request_profile.go` 已有真实请求的特征提取 | 未与 dry-run 打通 |
| 底层调度诊断 | `POST /api/{type}/channels/scheduler/diagnose` | 与 SmartRouter dry-run 两张面，入口分离 |
| 决策原因 | trace stages（protocol_federation / smart_filter / model_circuit_filter…）事后可查 | 预演时无逐阶段淘汰解释 |

### 1.2 目标

1. **请求体直喂**：粘贴原始请求体 + 指定入站协议 → 自动提取特征 → 进 dry-run 管线 → 返回 RoutingPlan + 逐阶段淘汰原因。
2. **两层对齐**：一次调用同时返回 SmartRouter 层（candidates/scores）和 scheduler 层（selection trace/stages）视角。
3. **零上游请求**：预演端点永不发上游请求，请求体仅内存态用于特征提取，不落 trace。
4. **UI 升级**：AutopilotView DiagnosePanel 新增「请求体预演」模式，替代手工填布尔。

## 2. 核心设计决策

### 2.1 架构分层

```text
[请求体 + 协议类型]
        │
        ▼
┌─────────────────────┐
│  特征提取层         │  复用 AttachAutopilotRequestProfile 逻辑
│  (autopilot_request │  但不依赖 gin context 完整链路，直接调用
│   _profile.go)      │  autopilot.BuildRequestProfile
└──────────┬──────────┘
           │ RequestProfile
           ▼
┌─────────────────────┐
│  SmartRouter 层     │  BuildPlan → RoutingPlan（candidates + scores）
│  (smart_router.go)  │
└──────────┬──────────┘
           │ RoutingPlan
           ▼
┌─────────────────────┐
│  Scheduler 层       │  SelectChannelWithOptions(DryRun=true)
│  (select.go)        │  → SelectionResult + SelectionTrace（stages + skipped）
└──────────┬──────────┘
           ▼
[合并响应：plan + schedulerDiagnose]
```

### 2.2 端点设计

**`POST /api/autopilot/route-preview`**

请求体：
```json
{
  "channelKind": "messages",   // messages | chat | responses | gemini | images | vectors
  "model": "claude-opus-5",    // 可选，从 body 中解析不出来时用
  "operation": "completion",   // 可选，默认按 kind 推导
  "body": { ... }              // 原始请求体（任意协议格式，自动识别特征）
}
```

响应体：
```json
{
  "plan": { ... },                  // 同 SmartRoutingDiagnosePlan（SmartRouter 层）
  "mode": "active",                 // 当前路由模式
  "extractedProfile": { ... },      // 从 body 提取出的特征（便于用户核对）
  "schedulerDiagnose": {            // 底层 scheduler 视角
    "ok": true,
    "kind": "messages",
    "reason": "priority_sort",
    "summary": "...",
    "trace": {
      "kind": "messages",
      "stages": [{ "name": "active_model_filter", "count": 5 }, ...],
      "candidates": [{ "channelIndex": 0, "channelName": "...", "stage": "...", "reason": "..." }, ...],
      "selected": { "channelIndex": 2, "channelName": "...", "reason": "..." }
    },
    "selected": { "channelIndex": 2, "channelName": "...", "serviceType": "..." }
  }
}
```

### 2.3 安全与隔离

1. **管理鉴权**：挂载在 `/api` 路由组下，天然经过 `WebAuthMiddleware`。
2. **零上游请求**：SmartRouter.BuildPlan 纯计算；scheduler DryRun=true 不更新状态、不发请求。
3. **内存态 body**：请求体只用于特征提取，函数返回即释放，不写入 trace、不写日志、不入 metrics。
4. **无副作用**：不触发任何学习（compat-cache、rate-limit discovery、health 等）。

### 2.4 逐阶段淘汰原因

淘汰原因分两层输出，对齐 trace stages 口径：

**SmartRouter 层**（来自 `RoutingPlanCandidate.FilterReasons`）：
- 每个候选的 `filterReasons` 数组已有（capability_mismatch / context_window_too_small 等）。

**Scheduler 层**（来自 `SelectionTrace`）：
- `stages`：阶段名 + 剩余候选数（active_model_filter / route_prefix_filter / default_route_filter / priority_sort / override / promotion / trace_affinity / smart_filter / model_circuit_filter…）。
- `candidates`：每个被跳过候选的 `stage` + `reason` + `details`。

## 3. 后端实现

### 3.1 新增文件

`backend-go/internal/autopilot/handlers_route_preview.go`

核心逻辑：
1. 绑定请求体（channelKind + model + operation + rawBody）
2. 从 rawBody 提取特征 → 构造 `RequestProfileFeatures`
3. 调用 `smartRouter.BuildPlan(profile)` 得到 RoutingPlan
4. 调用 `scheduler.SelectChannelWithOptions(DryRun=true, SmartFilter=...)` 得到 SelectionTrace
5. 合并响应返回

### 3.2 特征提取策略

复用 `handlers/common/autopilot_request_profile.go` 中的纯函数逻辑，但在 autopilot 包内重新组装，避免循环依赖。提取内容：

| 特征 | 提取方式 |
|---|---|
| model | 从 body.model 读取，请求体中没有则用请求级 model 字段 |
| hasImage / visionNeed | 扫描 messages 中是否有 image 类型内容 |
| toolUseNeed | 检查 body.tools 是否非空 |
| reasoningNeed | 检查 thinking / reasoning_effort / reasoningEffort 等字段 |
| estTokens / contextNeed | 按协议类型估算 token 数 |
| imageGenNeed | kind == images 时为 true |
| embeddingNeed | kind == vectors 时为 true |

### 3.3 main.go 接线

在 `RegisterDryRunRoutes` 附近，新增一行：
```go
autopilot.RegisterRoutePreviewRoutes(apiGroup, autopilotManager.SmartRouter(), channelScheduler)
```

### 3.4 一致性对拍测试

新增 `handlers_route_preview_test.go`：
- 构造一组已知特征的请求体
- 分别调用 route-preview 端点 和 手工构造的 dry-run 请求
- 断言两者产生的 RequestProfile 关键特征一致（TaskClass、QualityNeed、ToolUseNeed、VisionNeed 等）
- 断言两者的 RoutingPlan 候选列表一致（相同 channelUid 集合、相同 selected）

## 4. 前端实现

### 4.1 API 扩展

`frontend/src/services/autopilot-api.ts` 新增：
- `RoutePreviewRequest` / `RoutePreviewResponse` 类型
- `previewRoute(request)` 函数

### 4.2 DiagnosePanel 升级

`AutopilotDiagnosePanel.vue` 新增「请求体预演」模式：

- 顶部加 `v-tabs`：`手工填写` / `请求体预演`
- 请求体预演模式：
  - 协议选择（同现有 channelKind）
  - 模型输入（可选）
  - 大文本框粘贴请求体 JSON
  - 「运行预演」按钮
  - 结果展示区复用现有候选表格
  - 新增 Scheduler Trace 折叠面板（展示 stages + skipped candidates）
- 提取出的特征展示为 chips，便于用户核对

### 4.3 Scheduler Trace 展示

新增折叠面板展示：
- 阶段进度条（stages 逐步缩减）
- 被跳过渠道列表（按 stage 分组）
- 最终选择渠道及原因

## 5. 边界与保守策略

1. **协议覆盖不全时 fail-open**：未知字段不报错，保持零值（与真实请求路径一致）。
2. **body 解析失败**：返回 400，明确告知「请求体 JSON 解析失败」。
3. **SmartRouter 未初始化**：返回 503，与 dry-run 一致。
4. **空 body**：合法，相当于全部特征走零值推导。
5. **不影响现有 dry-run**：纯新增端点，不动既有管线。

## 6. 验证要求

### 6.1 后端

```bash
cd backend-go && make test && go build ./...
```

重点测试：
- 特征提取正确性（messages/chat/responses/gemini/images/vectors 六类协议）
- route-preview 与 dry-run 一致性对拍
- scheduler diagnose 与 route-preview 的 scheduler 层结果一致
- 零上游请求保证（DryRun=true 断言）

### 6.2 前端

```bash
cd frontend && bun run build
```

### 6.3 集成

- 预演结果与同特征 dry-run 输出一致
- 请求体不进 trace、不进日志、不进 metrics
- 管理鉴权生效（无 key 返回 401）
