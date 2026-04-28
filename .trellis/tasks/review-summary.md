# Review Summary

审查日期：2026-04-28

审查范围：
- 只检查本次 integrate 合并后与既有 review findings 相关的文件和交叉点。
- 不做源码修复。
- 重点确认 Images active-only、Images rename 唯一性、capability snapshot 路由、Images multipart raw log 四个问题是否真正关闭。
- 额外检查是否引入明显的透传功能回归。

验证命令：
- `cd backend-go && go test ./internal/config ./internal/handlers/images ./internal/handlers`
- `cd backend-go && go test ./internal/handlers/common ./internal/metrics ./internal/scheduler`
- `cd frontend && bun run type-check`

验证结果：全部通过。

## backend-go/config

### 已关闭：Images 单渠道回退到非 active 渠道

状态：已解决。

证据：
- `backend-go/internal/config/config_images.go`
- `GetCurrentImagesUpstream()` 在未找到 active 渠道时返回错误，不再回退第一个 suspended/disabled 渠道。
- `GetCurrentImagesUpstreamWithIndex()` 在未找到 active 渠道时返回 `nil, -1, error`。
- `backend-go/internal/config/config_images_test.go` 覆盖 no-active 场景。

剩余问题：
- [P3] `config_images.go` 中 `GetCurrentImagesUpstream` 上方注释仍描述“若无则回退到第一个渠道”，与当前 active-only 实现不一致。代码行为正确，但注释会误导后续维护。

### 已关闭：Images 重命名缺少唯一性校验

状态：已解决。

证据：
- `backend-go/internal/config/config_images.go`
- `UpdateImagesUpstream()` 在写入 `updates.Name` 前调用 `validateImagesUpstreamNameLocked(...)`。
- `backend-go/internal/config/config_images_test.go` 覆盖 duplicate rename 不应修改原渠道名。

剩余问题：
- 未发现 P0/P1/P2 问题。

## backend-go/capability-test

### 已关闭：能力测试 snapshot 接口不可达

状态：已解决。

证据：
- `backend-go/main.go`
- 已注册以下路由：
  - `GET /messages/channels/:id/capability-snapshot`
  - `GET /responses/channels/:id/capability-snapshot`
  - `GET /gemini/channels/:id/capability-snapshot`
  - `GET /chat/channels/:id/capability-snapshot`
- `frontend/src/services/api.ts` 的 `getChannelCapabilitySnapshot(...)` 路径与后端路由一致。

剩余问题：
- 未发现 P0/P1/P2 问题。

## backend-go/images

### 已关闭：Images multipart 请求可能记录原始二进制 body

状态：已解决。

证据：
- `backend-go/internal/handlers/images/handler.go`
- Images handler 通过 `logImagesOriginalRequest(...)` 分流日志。
- multipart 请求只记录方法和路径，并输出 omitted marker，不再调用通用 `common.LogOriginalRequest(...)` 记录 body。
- JSON/non-multipart 请求仍保留原有日志路径。
- `backend-go/internal/handlers/images/handler_test.go` 覆盖 multipart body 不应出现在日志中。

剩余问题：
- 未发现 P0/P1/P2 问题。

## passthrough/regression

### 透传功能保留情况

状态：未发现回归。

证据：
- 后端 channel view 仍返回：
  - `streamPassthroughEnabled`
  - `sub2apiPassthroughEnabled`
  - `keyAffinityEnabled`
  - `strictRequestPassthroughEnabled`
  - `failoverRules`
- 前端 `AddChannelModal.vue`、`channelPayload.ts`、`api.ts` 仍保留上述字段。
- `frontend && bun run type-check` 通过。
- `backend-go/internal/handlers/common`、`metrics`、`scheduler` 相关测试通过。

剩余问题：
- 未发现 P0/P1/P2 问题。

## 当前结论

原 review findings 中的 4 个 P1/P2 问题均已关闭。

本次只发现 1 个非阻塞问题：
- [P3] `backend-go/internal/config/config_images.go` 的注释仍保留旧 fallback 描述，与 active-only 代码行为不一致。
