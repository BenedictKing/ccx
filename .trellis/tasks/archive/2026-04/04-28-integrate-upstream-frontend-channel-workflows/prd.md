# Task: Integrate Upstream Frontend Channel Workflows

## Background

Upstream `origin/main` advanced from `v2.6.64` to `v2.6.68`. The review summary identifies `AddChannelModal.vue`, payload building, capability UI, and Images tab support as the frontend merge hotspot. This worktree starts from `codex/claude-channel-rules-passthrough`; inspect upstream with `git show origin/main:<path>` and `git diff v2.6.64..origin/main -- <path>`.

You are not alone in the codebase. Other agents are working in parallel on config, backend handlers/images, and capability/metrics. Do not revert edits made by others. Keep this task focused on frontend UI, payload, API typing, and state flow.

## Ownership

Primary files:
- `frontend/src/components/AddChannelModal.vue`
- `frontend/src/utils/channelPayload.ts`
- `frontend/src/utils/add-channel-modal-state.ts`
- `frontend/src/utils/channelTypeApi.ts`
- `frontend/src/services/api.ts`
- `frontend/src/components/CapabilityTestDialog.vue`
- `frontend/src/components/CapabilityModelResults.vue`
- `frontend/src/stores/channel.ts`
- `frontend/src/App.vue` and `frontend/src/views/ChannelsView.vue` only if needed for Images tab wiring
- related frontend tests under `frontend/src/**/*.test.ts`

Avoid editing:
- `backend-go/*` except updating generated API assumptions in comments is not needed.

## Requirements

- Integrate upstream Images channel tab and frontend management flow while preserving local Claude controls: strict passthrough, key affinity, failover rules, batch API key input, model health-check behavior.
- Update payload building so each channel kind has explicit field handling. Do not send upstream-removed persistent `channel.rpm`.
- Preserve upstream silent-save-before-model-query/test behavior and temporary connection parameters: `proxyUrl`, `insecureSkipVerify`, `customHeaders`, `routePrefix`, `supportedModels`.
- Ensure Images UI does not show Claude-only controls unless the backend explicitly supports them.
- Integrate upstream multi-protocol capability test UI/state: independent protocol job references, disabled active buttons rather than hidden buttons, cancellation by deduplicated jobId, and state restoration across tab/channel switches.
- Keep modal draft reset behavior correct when closing, switching add/edit mode, switching channel type, or switching edited channel.

## Acceptance Criteria

- `cd frontend && bun run type-check` passes.
- `cd frontend && bun run build` passes.
- `cd frontend && bun x vitest run` passes if available in this branch.
- Existing payload tests are updated to cover Claude fields, Images fields, and RPM removal.
- No backend code is modified by this task.
- The final report lists changed files and any backend assumptions required for final integration.

