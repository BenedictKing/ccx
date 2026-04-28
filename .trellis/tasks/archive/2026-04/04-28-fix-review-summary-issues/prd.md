# Fix review summary issues

## Goal
Fix all issues listed in `.trellis/tasks/review-summary.md` without expanding scope beyond the reported problems.

## Requirements
- Fix backend channel API key route `id` validation for chat, responses, and gemini handlers.
- Fix backend request body JSON parsing flow so malformed bodies return stable `400` responses immediately.
- Fix frontend model detection cache invalidation in `AddChannelModal.vue` when upstream context changes.

## Acceptance Criteria
- [ ] Invalid channel `id` path params no longer fall through to channel `0` mutations.
- [ ] Malformed request JSON returns `400` at the HTTP boundary without entering later failover/processing paths.
- [ ] Model detection cache/state refreshes when `baseUrl` or `serviceType` changes in the add-channel modal.

## Technical Notes
- Keep edits scoped to files implicated by the review summary and necessary adjacent tests.
- Follow existing handler/component patterns; do not add backward-compatibility branches.
