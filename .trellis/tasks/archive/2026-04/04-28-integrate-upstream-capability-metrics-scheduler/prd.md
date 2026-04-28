# Task: Integrate Upstream Capability Test, Metrics, Scheduler, And Recovery

## Background

Upstream `origin/main` advanced from `v2.6.64` to `v2.6.68`. The review summary identifies capability-test multi-protocol jobs, metrics identity, scheduler behavior, and automatic recovery as a separate integration lane. This worktree starts from `codex/claude-channel-rules-passthrough`; inspect upstream with `git show origin/main:<path>` and `git diff v2.6.64..origin/main -- <path>`.

You are not alone in the codebase. Other agents are working in parallel on config, handlers/images, and frontend workflows. Do not revert edits made by others. Keep this task focused on backend job/state/metrics/scheduler behavior plus the minimal frontend state required to compile and verify capability polling.

## Ownership

Primary backend files:
- `backend-go/internal/handlers/capability_test_handler.go`
- `backend-go/internal/handlers/capability_test_dispatcher.go`
- `backend-go/internal/handlers/capability_test_jobs.go`
- create or integrate `backend-go/internal/handlers/capability_snapshot_store.go`
- `backend-go/internal/metrics/channel_metrics.go`
- `backend-go/internal/metrics/sqlite_store.go`
- `backend-go/internal/metrics/channel_log.go`
- `backend-go/internal/scheduler/channel_scheduler.go`
- create or integrate `backend-go/internal/transitions/recovery.go`
- create or integrate `backend-go/recovery_state.go`
- related tests under handlers, metrics, scheduler, transitions

Frontend files only for API/state shape needed by capability jobs:
- `frontend/src/stores/channel.ts`
- `frontend/src/services/api.ts`
- `frontend/src/components/CapabilityTestDialog.vue` only if required for protocol-scoped state.

Avoid editing:
- `frontend/src/components/AddChannelModal.vue` and payload builders unless absolutely necessary; frontend workflow agent owns those.
- General channel handler routing unless needed to compile.

## Requirements

- Integrate upstream multi-protocol capability-test job model: protocol-scoped job references, independent polling, cancellation, retry, and cross-tab recovery.
- Preserve local model health-check, Claude probe priority, passthrough/failover interactions, and any local test coverage.
- Ensure RPM is request-scoped, clamped to `1..60`, defaults safely, and is not persisted in channel config.
- Integrate upstream metrics identity/BaseURL canonical behavior and SQLite migration changes.
- Integrate UTC automatic blacklist recovery semantics and preserve local blacklist/cooldown/failover-rule state.
- Ensure Images channel kind is represented in metrics/scheduler only where config/handler support exists; document any temporary compile adapter.

## Acceptance Criteria

- `cd backend-go && go test ./internal/handlers/... ./internal/metrics/... ./internal/scheduler/...` passes, or blockers from other lanes are documented precisely.
- `cd backend-go && go test ./...` should pass when combined with other lanes; document any cross-lane dependency.
- Capability jobs cannot overwrite another protocol's active job.
- Cancelling duplicate job references is idempotent and does not prevent state refresh.
- Metrics migrations are idempotent and preserve equivalent BaseURL history.
- The final report lists changed files and cross-lane assumptions.

