# Task: Integrate Upstream Handlers, Failover, And Images Routing

## Background

Upstream `origin/main` advanced from `v2.6.64` to `v2.6.68`. The review summary marks backend handlers, stream/failover, and Images routing as a P1 merge hotspot. This worktree starts from `codex/claude-channel-rules-passthrough`; inspect upstream with `git show origin/main:<path>` and `git diff v2.6.64..origin/main -- <path>`.

You are not alone in the codebase. Other agents are working in parallel on config, capability/metrics, and frontend workflows. Do not revert edits made by others. Keep ownership focused on backend request handling and route behavior.

## Ownership

Primary files:
- `backend-go/internal/handlers/chat/channels.go`
- `backend-go/internal/handlers/gemini/channels.go`
- `backend-go/internal/handlers/messages/channels.go`
- `backend-go/internal/handlers/responses/channels.go`
- create or integrate `backend-go/internal/handlers/images/channels.go`
- create or integrate `backend-go/internal/handlers/images/handler.go`
- create or integrate `backend-go/internal/handlers/images/multipart.go`
- `backend-go/internal/handlers/common/stream.go`
- `backend-go/internal/handlers/common/upstream_failover.go`
- `backend-go/internal/handlers/common/failover.go`
- related handler tests

Secondary files only if required for route registration:
- `backend-go/main.go`
- `backend-go/internal/handlers/common/channel_view.go`
- `backend-go/internal/handlers/common/ping.go`

Avoid editing:
- `frontend/*`.
- Broad config migrations unless needed for compile; leave schema work to the config agent.
- Metrics/scheduler internals unless needed to call existing APIs.

## Requirements

- Integrate upstream shared channel-management handler behavior while preserving local Claude passthrough, key affinity, failover rules, strict passthrough, and health-check behavior.
- Add independent Images routing for `/v1/images/generations`, `/v1/images/edits`, and `/v1/images/variations`, including `routePrefix` variants if upstream has them.
- Preserve upstream multipart handling requirements: keep file fields, model mapping, custom headers, proxy/TLS options, and do not log raw binary multipart bodies.
- Preserve upstream channel log lifecycle behavior: pending, connecting, first byte, streaming, completed, failed, cancelled.
- Ensure `context.Canceled` is recorded as `cancelled` and does not blacklist keys.
- Ensure quota/auth/rate-limit classification works for non-streaming, SSE, and multipart Images paths.
- Make Images support explicit: either wire shared failover behavior where semantically valid or isolate Claude-only controls clearly in backend responses.

## Acceptance Criteria

- `cd backend-go && go test ./internal/handlers/...` passes.
- `cd backend-go && go test ./...` should pass or, if blocked by config/metrics work from other agents, document the exact compile/test blocker.
- Images proxy endpoints are registered and covered by tests from upstream or adapted tests.
- Existing Claude passthrough and failover-rule tests still pass.
- The final report lists changed files and any assumptions handed off to config or frontend integration.

