# Task: Quality Inspection Follow-Up

## Background

Parallel upstream integration work has already been merged into `codex/claude-channel-rules-passthrough`.
The remaining follow-up is to close quality inspection items found after reading the prior agent logs and journal.

## Scope

- Verify and keep the existing Trellis multi-agent script fixes that are already present in the worktree.
- Fix the `responses/compact.go` failover path so routed-model-missing upstream errors do not write key cooldowns or breaker failures.
- Add focused regression tests for the compact handler behavior.
- Keep the change scoped; do not refactor unrelated handler or scheduler logic.

## Acceptance Criteria

- `feat-quality-inspection` is the active Trellis task.
- No unresolved git conflict files remain.
- Compact failover behavior matches shared common upstream failover behavior for `model_not_found` with `No available channel for model ... under group ...`.
- Relevant backend tests pass.
- Trellis script changes are validated with direct Python command checks where practical.
