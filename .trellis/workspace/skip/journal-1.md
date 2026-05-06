# Journal - skip (Part 1)

> AI development session journal
> Started: 2026-04-28

---



## Session 1: Fix review-summary issues

**Date**: 2026-04-28
**Task**: Fix review-summary issues
**Branch**: `codex/claude-channel-rules-passthrough`

### Summary

修复 review-summary 的 3 个问题，记录剩余 1 个前端并发时序问题。

### Main Changes

## 修复内容
- 修复 review-summary 列出的 3 个问题：后端 API Key 渠道路由 `id` 校验、`messages/responses` 请求体 JSON 解析错误时立即返回稳定 `400`、`AddChannelModal` 在上游上下文变化时刷新模型检测缓存状态。

## 变更文件
- `backend-go/internal/handlers/chat/channels.go`
- `backend-go/internal/handlers/chat/channels_advanced_test.go`
- `backend-go/internal/handlers/gemini/channels.go`
- `backend-go/internal/handlers/gemini/channels_advanced_test.go`
- `backend-go/internal/handlers/messages/handler.go`
- `backend-go/internal/handlers/messages/handler_response_matrix_test.go`
- `backend-go/internal/handlers/responses/channels.go`
- `backend-go/internal/handlers/responses/channels_advanced_test.go`
- `backend-go/internal/handlers/responses/handler.go`
- `backend-go/internal/handlers/responses/handler_response_matrix_test.go`
- `frontend/src/components/AddChannelModal.vue`

## 备注
- `AddChannelModal` 仍有 1 个前端并发时序观察项，已单独记录，不属于这次 review-summary 修复范围。

## 验证
- 后端与前端对应修复都已落地，具体验证结果以关联提交和后续会话记录为准。


### Git Commits

| Hash | Message |
|------|---------|
| `916703f` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 2: Fix missing routed model breaker classification

**Date**: 2026-04-28
**Task**: Fix missing routed model breaker classification
**Branch**: `codex/claude-channel-rules-passthrough`

### Summary

修复缺失路由模型错误的 failover 分类，避免把无可用模型误判为渠道故障并触发熔断。

### Main Changes

## 修复摘要
- 针对 `model_not_found` 且消息包含 `No available channel for model ... under group ...` 的上游错误，新增专门识别逻辑。
- 这类错误现在继续尝试下一个上游 key，不写 cooldown，不计入 breaker 熔断。
- 匹配范围刻意收窄到“路由组下无可用模型”的场景，避免误伤其他 `model_not_found`。

## 变更文件
- `backend-go/internal/handlers/common/failover.go`
- `backend-go/internal/handlers/common/upstream_failover.go`
- `backend-go/internal/handlers/common/failover_test.go`
- `backend-go/internal/handlers/common/upstream_failover_model_unavailable_test.go`

## 验证
- 已执行 `cd backend-go && go test ./internal/handlers/common/...`
- 测试通过，覆盖新增 failover 与 breaker 回归场景。

## 已知风险
- `backend-go/internal/handlers/responses/compact.go` 仍有独立失败处理分支；如果 Codex 请求走该链路，同类错误可能仍被冷却或计入熔断。


### Git Commits

| Hash | Message |
|------|---------|
| `d4a7f59` | (see git log) |

### Testing

- [OK] `cd backend-go && go test ./internal/handlers/common/...`

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 3: Fix channel selection, frontend checks, and codex branch release tag

**Date**: 2026-04-28
**Task**: Fix channel selection, frontend checks, and codex branch release tag
**Branch**: `codex/claude-channel-rules-passthrough`

### Summary

修复 review-summary 中已完成的后端/前端问题，补齐回归测试，并在当前分支推送 codex release tag 触发 Draft Release。

### Main Changes

﻿| Area | Description |
|------|-------------|
| Backend config | ?? messages / responses / chat / gemini ?????? active ?????? suspended/disabled ??????????????????????? 2 ?????? |
| Frontend services | ?? prerelease ???????? `ApiService.request()` ???? 10 ?????? `api.test.ts` ? `version.test.ts`? |
| Frontend quality | ?? lint ????? `frontend/package-lock.json`?????? bun ???? |
| Verification | ?? `go test ./...`?`bun run lint`?`bun run type-check`?`bun run build`?`bun x vitest run src/services/version.test.ts src/services/api.test.ts`????? |
| Release trigger | ? `codex/claude-channel-rules-passthrough` ????? tag `v0.0.0-codex-claude-rules-20260428-0001` ? `BugMasterLab/ccx`???? draft release workflows? |


### Git Commits

| Hash | Message |
|------|---------|
| `32069eb` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 4: Record sub2api passthrough usage release

**Date**: 2026-04-28
**Task**: Record sub2api passthrough usage release
**Branch**: `codex/claude-channel-rules-passthrough`

### Summary

Collected usage for Claude sub2api passthrough, pushed tag release workflow, and recorded validation results.

### Main Changes

﻿| Item | Result |
|------|--------|
| Sub2API passthrough usage | Added server-side best-effort usage collection for Claude passthrough without changing client response bodies. |
| Non-stream responses | Forward JSON unchanged while decoding usage for metrics; malformed JSON still passes through without turning success into failure. |
| Streaming responses | Collect usage from SSE events during direct passthrough and return it to metrics. |
| Tests | Added focused common handler tests for JSON passthrough usage and SSE passthrough usage collection. |
| Release | Pushed branch `codex/claude-channel-rules-passthrough` and tag `v0.0.0-codex-claude-rules-20260428-0002` to `myfork`; release workflows triggered. |
| Release status | Linux and Mac release builds succeeded; Windows was still in progress when recorded; Docker workflow failed because Aliyun registry username/password secrets were missing. |

**Validation**:
- `go test ./internal/handlers/common ./internal/handlers/messages`
- `go test ./...`
- `go vet ./...`
- `git diff --check`


### Git Commits

| Hash | Message |
|------|---------|
| `e2cd627` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 5: Quality inspection follow-up

**Date**: 2026-04-29
**Task**: Quality inspection follow-up
**Branch**: `codex/claude-channel-rules-passthrough`

### Summary

(Add summary)

### Main Changes

﻿| Area | Result |
|------|--------|
| Trellis task | Created and archived `04-29-feat-quality-inspection` after completing quality follow-up. |
| Trellis scripts | Fixed Windows Codex command resolution, direct multi-agent status imports, and Windows PID probing for stale registered agents. |
| Responses compact | Reused shared routed-model-miss classification so compact failover continues to the next key/channel without writing cooldown or breaker failures. |
| Tests | Added compact single-channel and multi-channel regressions for `model_not_found` / `No available channel for model ... under group ...`. |
| Specs | Documented local retry-loop contracts in backend quality guidelines. |
| Cleanup | Removed untracked `.agent-log`, task copies, Bun cache, and temp directories from parallel worktrees. |

**Validation**:
- `python -m py_compile .trellis/scripts/common/cli_adapter.py .trellis/scripts/multi_agent/status.py .trellis/scripts/multi_agent/status_display.py .trellis/scripts/multi_agent/status_monitor.py`
- `python ./.trellis/scripts/multi_agent/status.py`
- `python ./.trellis/scripts/multi_agent/status.py --list`
- `python ./.trellis/scripts/multi_agent/status.py --registry`
- `git diff --check`
- `cd backend-go && go test ./...`
- `cd frontend && bun run type-check`
- `cd frontend && bun run build`


### Git Commits

| Hash | Message |
|------|---------|
| `f29a437` | (see git log) |
| `aaf1cef` | (see git log) |
| `16018c3` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete


## Session 6: AxonHub passthrough billing follow-up

**Date**: 2026-05-06
**Task**: Continue AxonHub passthrough migration (`05-06-axonhub-passthrough-followup`)
**Branch**: `axonhub`

### Summary

Continued the AxonHub passthrough follow-up after reading `AxonHub-half.md`. The focus was whether raw passthrough usage/metrics billing was accurate. The review found no duplicate success-billing path, but found several side-channel usage gaps: OpenAI Chat cached tokens were not split into cache-read metrics, Gemini cached content tokens were not recorded as cache-read metrics, and Responses raw stream usage could be missed when `response.completed` arrived after the previous 1 MiB metrics buffer.

### Main Changes

| Area | Result |
|------|--------|
| OpenAI Chat usage | Added shared usage normalization so `prompt_tokens_details.cached_tokens` becomes `CacheReadInputTokens`, with `PromptTokensTotal` preserving upstream `prompt_tokens` for metrics normalization. |
| Chat raw passthrough | Covered both raw stream and non-stream same-format OpenAI paths while preserving upstream response/SSE bytes. |
| Gemini usage | Added shared usage normalization so `cachedContentTokenCount` becomes `CacheReadInputTokens`, `PromptTokensTotal` is `promptTokenCount`, and input metrics use `max(promptTokenCount - cachedContentTokenCount, 0)`. |
| Gemini raw passthrough | Same-format non-stream Gemini now returns upstream JSON bytes unchanged while parsing usage for metrics; raw stream keeps SSE bytes unchanged. |
| Responses raw stream | Replaced fixed-prefix metrics buffering with incremental SSE event usage parsing, so usage after large stream prefixes is still recorded. |
| Tests | Added/updated Chat, Gemini, and Responses matrix tests for cache-read metrics and large-prefix Responses stream usage. |
| Specs | Updated `.trellis/spec/backend/quality-guidelines.md` with the raw passthrough billing side-channel contract. |
| Handoff | Rewrote `AxonHub-half.md` with current progress, reference material, billing judgment, and remaining TODOs. |

### Files Touched This Session

- `.trellis/spec/backend/quality-guidelines.md`
- `backend-go/internal/handlers/chat/handler.go`
- `backend-go/internal/handlers/chat/handler_response_matrix_test.go`
- `backend-go/internal/handlers/common/stream.go`
- `backend-go/internal/handlers/gemini/handler.go`
- `backend-go/internal/handlers/gemini/handler_response_matrix_test.go`
- `backend-go/internal/handlers/responses/handler.go`
- `backend-go/internal/handlers/responses/handler_response_matrix_test.go`
- `AxonHub-half.md`

### Validation

- `cd backend-go && go fmt ./internal/handlers/common ./internal/handlers/chat ./internal/handlers/gemini ./internal/handlers/responses`
- `cd backend-go && go test ./internal/handlers/chat -run "TestChatHandler_(StreamRawPassthroughPreservesOpenAIUpstreamSSEBytesAndMetrics|NonStreamRawPassthroughRecordsCachedTokens|StreamRawPassthroughCancelsFirstAttemptBeforeFailover|CrossFormatStreamDoesNotUseRawPassthrough)" -count=1 -v`
- `cd backend-go && go test ./internal/handlers/gemini -run "TestGeminiHandler_(StreamRawPassthroughPreservesNativeSSEBytesAndMetrics|NonStreamRawPassthroughRecordsCachedContentTokens|StreamRawPassthroughCancelsFirstAttemptBeforeFailover|CrossFormatStreamDoesNotUseRawPassthrough)" -count=1 -v`
- `cd backend-go && go test ./internal/handlers/responses -run "TestResponsesHandler_(StreamRawPassthroughPreservesSSEBytes|StreamRawPassthroughRecordsUsageAfterLargePrefix|StreamSameFormatAlwaysUsesRawPassthrough|NonStreamRawPassthroughPreservesUnknownFieldsAndRecordsMetrics)" -count=1 -v`
- `cd backend-go && go test ./internal/handlers/common ./internal/handlers/chat ./internal/handlers/gemini ./internal/handlers/responses ./internal/metrics -count=1`
- `cd backend-go && go vet ./...`
- `cd backend-go && go test ./...`
- `git diff --check`
- `git diff --check -- AxonHub-half.md`

### Notes

- Trellis implement sub-agent first failed with upstream `503 Service Unavailable`.
- A later implement sub-agent wrote partial backend changes but did not complete cleanly before shutdown; the main session reviewed, corrected, formatted, and verified the patch.
- Trellis check sub-agent did not return after multiple waits and was shut down; the main session completed `go vet`, full backend tests, and diff checks instead.
- `trellis-finish-work` archive/journal automation was not run because the working tree has uncommitted code changes outside `.trellis/workspace/` and `.trellis/tasks/`.

### Status

[IN PROGRESS] AxonHub billing side-channel patch is implemented and verified locally, but not committed.

### Next Steps

- Prepare a Phase 3.4 commit plan that includes only the AxonHub billing files listed above.
- Keep Trellis v0.5 migration dirty files out of the AxonHub commit.
- After work commits are made, rerun finish-work/archive flow if desired.


## Session 7: Unify AxonHub forwarding data plane

**Date**: 2026-05-06
**Task**: 统一 AxonHub 转发逻辑 (`05-06-unify-axonhub-forwarding`)
**Branch**: `axonhub`

### Summary

将 ccx 的转发数据面按 AxonHub 风格统一，同时保留 ccx 控制面。当前已经拆出并落盘 6 个 Trellis 子任务，完成了转发契约矩阵、forwarding builder scaffold、same-format Messages/Chat 接入、same-format Responses/Gemini 接入、cross-format outbound request 接入，以及 Responses same-format raw stream 的 preflight/cleanup 收口。控制面边界保持不变：scheduler、failover、key retry、blacklist、cooldown、circuit breaker、metrics finalize 仍走 ccx 现有机制。

### Main Changes

| Area | Result |
|------|--------|
| Trellis task split | 为父任务创建并挂载 6 个子任务，分别记录 PRD、实现边界、验收标准、依赖关系，并补齐 `implement.jsonl` / `check.jsonl`。 |
| Contract matrix | 产出 `.trellis/tasks/05-06-forwarding-contract-matrix/research/forwarding-contract-matrix.md`，梳理 Messages、Chat、Responses、Gemini 的 same-format / cross-format body、header、response、SSE、usage 现状与目标行为。 |
| Forwarding builder | 新增 `backend-go/internal/forwarding/`，收敛 outbound request model、header/auth 顺序和 URL 构造规则，并补 builder / URL 测试。 |
| Same-format request construction | Messages -> Claude、Chat -> OpenAI、Responses -> Responses、Gemini native -> Gemini 的 upstream request 构造改走 builder；unknown fields 保留，平台字段 patch 继续存在。 |
| Cross-format request construction | Messages/Responses provider 和 Chat/Gemini cross-format 分支的 outbound request 也改走 builder，但协议转换与 response 回转逻辑保持原状。 |
| Raw stream cleanup | Responses same-format raw stream 改为复用 shared raw fan-out / preflight / cleanup 机制，empty stream、invalid raw body、blacklist preflight 可在 header 提交前回到 `TryUpstreamWithAllKeys` 做 failover。 |
| Spec update | 更新 `.trellis/spec/backend/quality-guidelines.md`，补充 forwarding builder 与 raw passthrough lifecycle 的边界约束。 |

### Files Touched This Session

- `.trellis/spec/backend/quality-guidelines.md`
- `.trellis/tasks/05-06-unify-axonhub-forwarding/`
- `.trellis/tasks/05-06-forwarding-contract-matrix/`
- `.trellis/tasks/05-06-forwarding-builder-scaffold/`
- `.trellis/tasks/05-06-same-format-messages-chat-builder/`
- `.trellis/tasks/05-06-same-format-responses-gemini-builder/`
- `.trellis/tasks/05-06-cross-format-outbound-builder/`
- `.trellis/tasks/05-06-attempt-raw-stream-cleanup-verification/`
- `backend-go/internal/forwarding/`
- `backend-go/internal/providers/claude.go`
- `backend-go/internal/providers/openai.go`
- `backend-go/internal/providers/gemini.go`
- `backend-go/internal/providers/responses.go`
- `backend-go/internal/handlers/chat/handler.go`
- `backend-go/internal/handlers/chat/handler_test.go`
- `backend-go/internal/handlers/common/stream.go`
- `backend-go/internal/handlers/gemini/handler.go`
- `backend-go/internal/handlers/gemini/matrix_test.go`
- `backend-go/internal/handlers/responses/handler.go`
- `backend-go/internal/handlers/responses/handler_response_matrix_test.go`
- `backend-go/internal/handlers/responses/handler_session_test.go`

### Validation

- `cd backend-go && go fmt ./...`
- `cd backend-go && go test ./internal/forwarding ./internal/providers ./internal/handlers/chat ./internal/handlers/messages`
- `cd backend-go && go test ./internal/providers`
- `cd backend-go && go test ./internal/handlers/gemini`
- `cd backend-go && go test ./internal/handlers/responses`
- `cd backend-go && go test ./internal/handlers/...`
- `cd backend-go && go test ./...`
- `git diff --check`

### Notes

- `trellis-research` 子代理先产出了 forwarding contract matrix 和 raw stream cleanup audit，后续实现任务都把这两份研究作为上下文注入。
- `trellis-check` 最后一轮子代理没有拿到可用结果；当前质量结论依赖实现子代理返回的测试结果和主线程对 diff 的复查。
- 当前工作树仍有未提交代码改动，因此没有运行 `trellis-finish-work` 的 archive / add_session 自动化。
- 子任务的 `task.json` 状态还没有回写为 completed，本次只做 Journal 记录，不做归档。

### Status

[IN PROGRESS] AxonHub forwarding unification 已实现并通过本地全量 Go 测试，但尚未提交，也尚未完成 Phase 3.4 的 commit plan 与归档。

### Next Steps

- 先做一次独立的 Phase 2.2 / 3.1 质量检查，确认 builder 接入和 Responses raw stream cleanup 没有遗漏回归。
- 按逻辑批次准备 Phase 3.4 commit plan，只纳入本轮 unify-axonhub-forwarding 的代码与 spec/task 文档。
- 提交后再运行 `/finish-work`，归档父任务与相关子任务，并由 Trellis 自动记录正式 session entry。


## Session 6: Unify AxonHub forwarding data plane

**Date**: 2026-05-06
**Task**: Unify AxonHub forwarding data plane
**Branch**: `axonhub`

### Summary

Unified AxonHub-style forwarding data-plane request construction through a shared builder, preserved ccx control-plane ownership, moved Responses same-format raw stream passthrough onto shared preflight/fan-out/cleanup, and verified backend tests plus diff checks.

### Main Changes

(Add details)

### Git Commits

| Hash | Message |
|------|---------|
| `023e4a9` | (see git log) |
| `e210b02` | (see git log) |
| `2ec5237` | (see git log) |

### Testing

- [OK] (Add test results)

### Status

[OK] **Completed**

### Next Steps

- None - task complete
