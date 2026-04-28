# Task: Integrate Upstream Config And Channel Schema

## Background

Upstream `origin/main` advanced from the local merge base `v2.6.64` to `v2.6.68`. The review summary identifies config/schema integration as the first blocker. This worktree starts from `codex/claude-channel-rules-passthrough`; inspect upstream with `git show origin/main:<path>` and `git diff v2.6.64..origin/main -- <path>`.

You are not alone in the codebase. Other agents are working in parallel on handlers, capability/metrics, and frontend workflows. Do not revert edits made by others. Keep this task focused on config/schema and tests, and leave handler/frontend implementation to the other worktrees unless a tiny compile adapter is unavoidable.

## Ownership

Primary files:
- `backend-go/internal/config/config.go`
- `backend-go/internal/config/config_chat.go`
- `backend-go/internal/config/config_gemini.go`
- `backend-go/internal/config/config_messages.go`
- `backend-go/internal/config/config_responses.go`
- `backend-go/internal/config/config_loader.go`
- `backend-go/internal/config/config_utils.go`
- create or integrate `backend-go/internal/config/config_images.go`
- related config tests under `backend-go/internal/config/*_test.go`

Avoid editing:
- `backend-go/internal/handlers/*` except for minimal compile fallout.
- `frontend/*`.

## Requirements

- Integrate upstream Images channel config support from `origin/main`, including `imagesUpstream`, defaults, loader behavior, and any helper functions needed by later handler work.
- Preserve local Claude passthrough, key affinity, failover rules, models health check, pause/blacklist/cooldown, and strict passthrough config fields.
- Align upstream RPM migration: capability-test RPM must not be persisted as a channel config field if upstream removed it.
- Preserve upstream BaseURL canonical/equivalence behavior and tests.
- Ensure defaulting/migration works for all channel kinds: messages, responses, chat, gemini, and images.
- Add or adapt tests that prove old configs load safely and new Images config initializes correctly.

## Acceptance Criteria

- `cd backend-go && go test ./internal/config/...` passes.
- `cd backend-go && go test ./...` should pass or, if blocked by other modules not owned here, document the unrelated blocker in the final report.
- No local Claude config fields are dropped during update/default/load paths.
- Images config exists and is treated consistently with other channel kinds where appropriate.
- The final report lists changed files and any expected integration points for the handler/frontend agents.

