# Display total and cache tokens in UI

## Goal

Improve the global stats summary UI so token usage is easier to read: show total tokens as `inputTokens + outputTokens`, show cache read/write token counts, and clarify through tooltip text that these values are usage summaries only, not pricing, balance, deduction, refund, or billing ledger data.

## What I already know

* The user wants a UI-only change.
* Current global stats summary cards show total requests, success rate, input tokens, and output tokens.
* Backend global stats and channel metrics already expose `cacheCreationTokens` and `cacheReadTokens`.
* AxonHub-style billing scope in this project is usage/statistics only.

## Requirements

* Add a visible "Total Token" summary value using `totalInputTokens + totalOutputTokens`.
* Add visible cache read and cache write token values using existing summary fields:
  * cache read = `totalCacheReadTokens`
  * cache write = `totalCacheCreationTokens`
* Add tooltip copy explaining that total tokens do not include price, balance, deduction, refund, or ledger semantics; it is only usage aggregation.
* Keep this as a frontend display change; do not change backend metrics semantics.
* Keep text localized through existing i18n patterns.

## Acceptance Criteria

* [ ] Non-compact global stats summary shows total tokens and cache read/write tokens.
* [ ] Compact global stats summary includes total tokens without overcrowding the row.
* [ ] Tooltip clarifies usage-only scope and no price/balance/deduction/refund meaning.
* [ ] Existing input/output token values continue to display.
* [ ] Frontend type-check/build passes.

## Definition of Done

* Frontend implementation follows existing Vue/Vuetify/i18n conventions.
* `cd frontend && bun run type-check` passes.
* `cd frontend && bun run build` passes.

## Out of Scope

* Backend API changes.
* Persisted billing ledger.
* Balance, price table, deduction ledger, refund, or billing transaction behavior.
* Chart series changes beyond the summary display.

## Technical Notes

* Likely component: `frontend/src/components/GlobalStatsChart.vue`.
* Existing summary type: `frontend/src/services/api.ts` `GlobalStatsSummary`.
* Existing labels: `frontend/src/i18n/messages.ts`.
