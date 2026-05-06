# Research: final cleanup and commit boundary

- Query: Research final cleanup and commit boundary for AxonHub closeout. Inspect git status, task docs, AxonHub reference artifacts, and existing Trellis migration dirty files.
- Scope: internal
- Date: 2026-05-06

## Findings

### Current state

- `python3 ./.trellis/scripts/task.py current --source` returned exit 1 with no output in this shell. The user provided the exact target task path, so this research was written under `.trellis/tasks/05-06-axonhub-passthrough-followup/research/`.
- `git status --porcelain=v1` shows a mixed dirty worktree: AxonHub backend/frontend/spec/task work, untracked AxonHub reference artifacts, and a separate Trellis v0.5 migration.
- Recent commit style is short English conventional messages, e.g. `chore: bump version to v2.6.70`, `fix: normalize passthrough usage metrics`, `fix: prevent admin probes from using disabled keys`.

### Task and spec evidence

- `.trellis/tasks/05-06-axonhub-passthrough-followup/prd.md:46` defines the final closeout scope.
- `.trellis/tasks/05-06-axonhub-passthrough-followup/prd.md:58` requires cleaning `AxonHub-half.md`, `axonhub.md`, and `axonhub/` after migration completion.
- `.trellis/tasks/05-06-axonhub-passthrough-followup/prd.md:62` explicitly warns not to commit without user confirmation because the worktree contains unrelated dirty files.
- `.trellis/tasks/05-06-axonhub-passthrough-followup/prd.md:84` keeps commit-plan confirmation as an acceptance criterion.
- `.trellis/tasks/05-06-axonhub-passthrough-followup/prd.md:95` keeps full AxonHub orchestrator/pipeline port out of scope.
- `.trellis/spec/backend/quality-guidelines.md:130` documents passthrough decision contracts for backend changes.
- `.trellis/spec/backend/quality-guidelines.md:164` documents that removed passthrough fields must not be exposed.
- `.trellis/spec/frontend/type-safety.md:98` documents frontend passthrough field removal.

### AxonHub reference artifacts

These are untracked migration/reference artifacts and belong to the AxonHub closeout boundary:

- `AxonHub-half.md` - handoff record for the continuation; 16,562 bytes. It identifies P0/P1 state and remaining cleanup, including User-Agent and sensitive header notes.
- `axonhub.md` - earlier AxonHub analysis/reference summary; 11,710 bytes.
- `axonhub/` - full reference checkout/archive; `Get-ChildItem -Recurse -File axonhub` found 2,172 files and about 29.8 MB.

Relevant reference patterns:

- `AxonHub-half.md:5` says this migration should use AxonHub passthrough ideas without copying the full orchestrator/pipeline architecture.
- `AxonHub-half.md:29` says raw request passthrough should preserve unknown fields while patching platform-controlled fields.
- `AxonHub-half.md:30` says raw response/raw SSE should be returned faithfully while usage/metrics/session are collected out of band.
- `AxonHub-half.md:31` says stream failover must release the current attempt reader/body before the next key/channel.
- `AxonHub-half.md:55` says User-Agent passthrough is independent from body/response passthrough.
- `AxonHub-half.md:367` notes that unified stripping for `Cookie` and `Proxy-Authorization` was still unresolved at handoff time.
- `AxonHub-half.md:389` starts the User-Agent passthrough strategy section.
- `axonhub/internal/server/orchestrator/pass_through.go:132` implements AxonHub's User-Agent passthrough middleware.
- `axonhub/internal/server/orchestrator/pass_through.go:210` starts AxonHub's raw stream fan-out capture.
- `axonhub/internal/server/orchestrator/pass_through.go:237` records attempt-level stream cancellation state.
- `axonhub/internal/server/orchestrator/orchestrator.go:253` places User-Agent passthrough before header overrides.
- `axonhub/internal/server/orchestrator/orchestrator.go:280` captures raw provider stream late in the outbound middleware chain.

Cleanup recommendation:

- Remove `AxonHub-half.md`, `axonhub.md`, and `axonhub/` only after final AxonHub verification has passed and this research file remains as the retained closeout record.
- Put that deletion in a dedicated cleanup commit, not mixed with backend/frontend behavior changes, because `axonhub/` is large and noisy.
- If final verification is not green, keep the artifacts until the remaining User-Agent/sensitive-header/raw-stream questions are closed.

### Files that belong to AxonHub work

AxonHub task docs and research:

- `.trellis/tasks/05-06-axonhub-passthrough-followup/prd.md` - requirements and acceptance criteria for this task.
- `.trellis/tasks/05-06-axonhub-passthrough-followup/task.json` - task metadata.
- `.trellis/tasks/05-06-axonhub-passthrough-followup/implement.jsonl` - implementation context.
- `.trellis/tasks/05-06-axonhub-passthrough-followup/check.jsonl` - check context.
- `.trellis/tasks/05-06-axonhub-passthrough-followup/research/axonhub-midpoint.md` - midpoint/handoff summary.
- `.trellis/tasks/05-06-axonhub-passthrough-followup/research/messages-stream-lifecycle.md` - Messages stream lifecycle research.
- `.trellis/tasks/05-06-axonhub-passthrough-followup/research/responses-raw-stream.md` - Responses raw stream research.
- `.trellis/tasks/05-06-axonhub-passthrough-followup/research/axonhub-raw-fanout.md` - AxonHub fan-out research.
- `.trellis/tasks/05-06-axonhub-passthrough-followup/research/final-cleanup-commit-boundary.md` - this closeout boundary research.

Backend and frontend specs that appear AxonHub-specific:

- `.trellis/spec/backend/quality-guidelines.md` - header override and passthrough contracts.
- `.trellis/spec/frontend/type-safety.md` - removed passthrough field frontend contract.

Backend production and test files in AxonHub boundary:

- `backend-go/internal/config/config.go`
- `backend-go/internal/config/config_blacklist_test.go`
- `backend-go/internal/config/config_chat.go`
- `backend-go/internal/config/config_claude_passthrough_mode_test.go` (deleted)
- `backend-go/internal/config/config_failover_rules_test.go` (untracked)
- `backend-go/internal/config/config_gemini.go`
- `backend-go/internal/config/config_images.go`
- `backend-go/internal/config/config_messages.go`
- `backend-go/internal/config/config_responses.go`
- `backend-go/internal/config/config_test_helpers_test.go` (untracked)
- `backend-go/internal/config/config_utils.go`
- `backend-go/internal/handlers/capability_test_handler.go`
- `backend-go/internal/handlers/channel_dashboard_test.go`
- `backend-go/internal/handlers/chat/channels.go`
- `backend-go/internal/handlers/chat/handler.go`
- `backend-go/internal/handlers/chat/handler_test.go`
- `backend-go/internal/handlers/chat/header_override_handler_test.go` (untracked)
- `backend-go/internal/handlers/common/channel_view.go`
- `backend-go/internal/handlers/common/stream.go`
- `backend-go/internal/handlers/common/stream_test.go`
- `backend-go/internal/handlers/common/upstream_failover.go`
- `backend-go/internal/handlers/common/upstream_failover_model_unavailable_test.go`
- `backend-go/internal/handlers/common/upstream_failover_passthrough_test.go`
- `backend-go/internal/handlers/gemini/channels.go`
- `backend-go/internal/handlers/gemini/handler.go`
- `backend-go/internal/handlers/gemini/header_override_test.go` (untracked)
- `backend-go/internal/handlers/images/handler.go`
- `backend-go/internal/handlers/images/header_override_test.go` (untracked)
- `backend-go/internal/handlers/messages/channels.go`
- `backend-go/internal/handlers/messages/channels_advanced_test.go`
- `backend-go/internal/handlers/messages/handler.go`
- `backend-go/internal/handlers/messages/handler_response_matrix_test.go`
- `backend-go/internal/handlers/responses/channels.go`
- `backend-go/internal/handlers/responses/compact.go`
- `backend-go/internal/handlers/responses/handler.go`
- `backend-go/internal/handlers/responses/handler_response_matrix_test.go`
- `backend-go/internal/handlers/responses/header_override_test.go` (untracked)
- `backend-go/internal/passthrough/passthrough.go` (untracked)
- `backend-go/internal/passthrough/passthrough_test.go` (untracked)
- `backend-go/internal/providers/claude.go`
- `backend-go/internal/providers/claude_passthrough_test.go`
- `backend-go/internal/providers/gemini.go`
- `backend-go/internal/providers/gemini_stream_test.go`
- `backend-go/internal/providers/matrix_responses_test.go`
- `backend-go/internal/providers/openai.go`
- `backend-go/internal/providers/provider.go`
- `backend-go/internal/providers/responses.go`
- `backend-go/internal/providers/responses_stream_test.go`
- `backend-go/internal/providers/sse_normalization_test.go`
- `backend-go/internal/providers/stream_cancel_test.go` (untracked)

Frontend files in AxonHub boundary:

- `frontend/src/components/AddChannelModal.vue`
- `frontend/src/components/ChannelCard.vue`
- `frontend/src/i18n/messages.ts`
- `frontend/src/services/api.ts`
- `frontend/src/utils/channelPayload.test.ts`
- `frontend/src/utils/channelPayload.ts`

Observed AxonHub code patterns:

- `backend-go/internal/passthrough/passthrough.go:30` centralizes passthrough decisions.
- `backend-go/internal/passthrough/passthrough.go:82` and `backend-go/internal/passthrough/passthrough.go:91` gate strict body/raw response passthrough by format consistency.
- `backend-go/internal/passthrough/passthrough.go:119` patches platform fields without dropping unknown request fields.
- `backend-go/internal/handlers/common/stream.go:517` starts attempt-scoped raw stream fan-out.
- `backend-go/internal/handlers/common/stream.go:594` cleans up raw stream fan-out before failover/cancel returns.
- `backend-go/internal/handlers/common/stream.go:1227` gates raw Messages stream passthrough.
- `backend-go/internal/handlers/common/stream.go:1234` handles raw Messages stream passthrough.
- `backend-go/internal/handlers/responses/handler.go:957` handles raw Responses stream passthrough.
- `backend-go/internal/handlers/messages/handler_response_matrix_test.go:139` covers raw Messages stream byte preservation and metrics.
- `backend-go/internal/handlers/messages/handler_response_matrix_test.go:191` covers raw stream cleanup before failover.
- `backend-go/internal/handlers/messages/handler_response_matrix_test.go:248` covers cross-format stream not using raw passthrough.
- `backend-go/internal/handlers/responses/handler_response_matrix_test.go:157` covers Responses raw stream byte preservation.
- `backend-go/internal/handlers/responses/handler_response_matrix_test.go:207` covers same-format Responses stream staying raw.
- `backend-go/internal/providers/stream_cancel_test.go:9` and `backend-go/internal/providers/stream_cancel_test.go:29` cover context-cancel send helpers.
- `backend-go/internal/handlers/chat/header_override_handler_test.go:11`, `backend-go/internal/handlers/gemini/header_override_test.go:13`, `backend-go/internal/handlers/images/header_override_test.go:19`, and `backend-go/internal/handlers/responses/header_override_test.go:12` cover selected-key authentication overriding custom auth-like headers.
- `frontend/src/utils/channelPayload.test.ts:56` through `frontend/src/utils/channelPayload.test.ts:59` assert removed passthrough fields are absent from frontend payloads.

### Files that look unrelated: Trellis v0.5 migration dirty files

These should not be staged with the AxonHub work unless the user explicitly decides to combine tasks:

- `.trellis/tasks/05-06-migrate-to-0.5.0-beta.19/prd.md` and `task.json`.
- `.agents/skills/before-dev/SKILL.md` through other deleted legacy skill paths.
- `.agents/skills/trellis-before-dev/`, `.agents/skills/trellis-brainstorm/`, `.agents/skills/trellis-break-loop/`, `.agents/skills/trellis-check/`, `.agents/skills/trellis-continue/`, `.agents/skills/trellis-finish-work/`, `.agents/skills/trellis-meta/`, `.agents/skills/trellis-update-spec/`.
- `.codex/agents/check.toml`, `.codex/agents/implement.toml`, `.codex/agents/research.toml` deleted.
- `.codex/agents/trellis-check.toml`, `.codex/agents/trellis-implement.toml`, `.codex/agents/trellis-research.toml` and `.backup` files.
- `.codex/config.toml`, `.codex/config.toml.new`, `.codex/hooks.json`, `.codex/hooks.json.new`, `.codex/hooks/session-start.py`, `.codex/hooks/session-start.py.new`, `.codex/hooks/inject-workflow-state.py`.
- `.codex/skills/parallel/SKILL.md` deleted.
- `.trellis/.gitignore`, `.trellis/.template-hashes.json`, `.trellis/.version`, `.trellis/config.yaml`, `.trellis/workflow.md`.
- `.trellis/scripts/common/__init__.py`, `cli_adapter.py`, `config.py`, `git_context.py`, `paths.py`, `session_context.py`, `task_context.py`, `task_store.py`, `tasks.py`, `types.py`.
- `.trellis/scripts/common/active_task.py` and `.trellis/scripts/common/workflow_phase.py` untracked.
- `.trellis/scripts/common/phase.py`, `.trellis/scripts/create_bootstrap.py`, `.trellis/scripts/multi_agent/*`, and `.trellis/worktree.yaml` deleted.
- `.trellis/scripts/task.py`.
- `AGENTS.md` and `AGENTS.md.new`.

Migration evidence:

- `.trellis/tasks/05-06-migrate-to-0.5.0-beta.19/prd.md:23` says Trellis skills gained a `trellis-` prefix.
- `.trellis/tasks/05-06-migrate-to-0.5.0-beta.19/prd.md:30` lists retired commands and sub-agents.
- `.trellis/tasks/05-06-migrate-to-0.5.0-beta.19/prd.md:44` says the multi-agent pipeline was removed.
- `.trellis/tasks/05-06-migrate-to-0.5.0-beta.19/prd.md:87` discusses active task JSONL path updates after migration.
- `.trellis/tasks/05-06-migrate-to-0.5.0-beta.19/prd.md:89` discusses the Codex hooks feature flag.
- `.trellis/tasks/05-06-migrate-to-0.5.0-beta.19/prd.md:95` says Codex sub-agents were renamed from `implement/check/research` to `trellis-*`.

### Recommended commit grouping

Do not stage anything automatically. Ask for confirmation with exact file groups.

Recommended AxonHub groups:

1. `fix: finish axonhub passthrough closeout`
   - Include backend production/test files and frontend files listed under "Files that belong to AxonHub work".
   - Include `.trellis/spec/backend/quality-guidelines.md` and `.trellis/spec/frontend/type-safety.md` if the final intent is to commit spec contracts together with the behavior they document.
   - Do not include `AxonHub-half.md`, `axonhub.md`, or `axonhub/` in this group.

2. `docs(task): record axonhub passthrough closeout`
   - Include `.trellis/tasks/05-06-axonhub-passthrough-followup/**`, including this research file.
   - Alternative: leave task docs for the later Trellis archive/bookkeeping commit if the project convention prefers task artifacts to move only during finish-work. This must be decided explicitly because the task directory is currently untracked.

3. `chore: remove axonhub migration references`
   - Delete `AxonHub-half.md`, `axonhub.md`, and `axonhub/` after final verification.
   - Keep this separate because it is large, untracked, and reference-only.

Recommended non-AxonHub group:

4. `chore: migrate trellis to v0.5.0-beta.19`
   - Include only the Trellis migration files listed above.
   - This should be planned and confirmed separately from AxonHub closeout.

### Commit grouping risks

- Mixing AxonHub code with Trellis migration files will make review difficult because the combined diff includes both behavior changes and platform/tooling rewrites.
- Mixing `axonhub/` deletion with code changes will bury the behavior diff under a large reference-tree removal.
- `.trellis/spec/backend/quality-guidelines.md` and `.trellis/spec/frontend/type-safety.md` are under `.trellis/`, but their current content is AxonHub-specific. Do not classify all `.trellis/**` as migration work.
- `.trellis/tasks/05-06-axonhub-passthrough-followup/**` and `.trellis/tasks/05-06-migrate-to-0.5.0-beta.19/**` are both untracked task directories; staging by directory prefix `.trellis/tasks/**` would mix two tasks.
- The final cleanup should not run `git add .` or broad pathspecs like `.trellis/**`, `.codex/**`, `backend-go/**`, or `frontend/**` without reviewing the explicit file list.
- The current `PrepareUpstreamHeaders` inspected at `backend-go/internal/utils/headers.go:106` removes proxy-forwarding headers but does not obviously remove `Cookie` or `Proxy-Authorization` in the inspected lines. Since the PRD requires unified sensitive inbound header stripping, final closeout should verify this before claiming that acceptance criterion is done.

## Caveats / Not Found

- I did not edit production code, stage files, commit, or delete reference artifacts.
- I did not run tests or final verification commands in this research pass.
- `python3 ./.trellis/scripts/task.py current --source` did not identify an active task in this shell; the output path came from the user request.
- No external web references were needed; the research used local task docs, local specs, git status/diff metadata, and local AxonHub artifacts.
