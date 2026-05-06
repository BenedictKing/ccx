# AxonHub forwarding with CCX resilience

## Goal

Use AxonHub-style forwarding, usage accounting, and billing-oriented data-plane behavior while keeping CCX's existing scheduler, circuit breaker, cooldown, blacklist, failover, channel logs, and resilience metrics as the control-plane source of truth.

## What I Already Know

* User wants AxonHub forwarding logic plus usage/billing mechanisms combined with CCX mechanisms such as circuit breaker and blacklist.
* Current CCX already routes proxy requests through `common.TryUpstreamWithAllKeys` for Messages, Responses, Chat, Gemini, and Images handlers.
* `TryUpstreamWithAllKeys` owns per BaseURL/key retry, circuit state checks, half-open probe acquisition, cooldown, blacklist, failover classification, request metrics, channel logs, and current AxonHub forwarding usage stats.
* Current `axonHubForwarding` stats aggregate request count, input/output tokens, cache creation tokens, and cache read tokens by inbound family and forwarding mode.
* Current AxonHub-related stats are observability/usage aggregation; a full billing ledger or pricing engine has not been confirmed in the CCX code inspected so far.
* `forwarding.Build` exists as a data-plane helper for building upstream URL/method/body/headers/auth and preserving safe inbound headers.
* Chat and Gemini have partial use of `forwarding.Build`; Messages and Responses still primarily rely on provider `ConvertToProviderRequest` builders.
* `passthrough.Decide` currently decides raw request/response passthrough only when inbound and outbound formats match.

## Requirements

* MVP scope: prioritize same-format raw passthrough plus completion of existing AxonHub forwarding usage statistics; keep cross-format paths working through existing conversion/control-plane behavior before broadening conversion-builder coverage.
* Keep CCX control-plane behavior unchanged: scheduler choice, key selection, BaseURL retry, failover, cooldown, blacklist, circuit breaker, logs, and metrics stay outside forwarding builders.
* Use forwarding builder/data-plane helpers for protocol request construction where feasible.
* Use AxonHub-style usage accounting for forwarded requests, including cache token dimensions needed for billing.
* Billing scope is usage/billing statistics only: request count and token dimensions for reporting/aggregation.
* Do not implement account balance, price tables, deduction ledger, refunds, or idempotent billing transactions in this task.
* Preserve same-format raw passthrough behavior for matching formats.
* Preserve cross-format conversion behavior where inbound and outbound formats differ.
* Preserve blacklist/cooldown behavior for HTTP errors and stream preflight errors.
* Preserve client-cancel behavior: client cancellation must not blacklist or cooldown keys.
* Do not let billing/usage collection read or mutate client-visible response bytes in a way that breaks raw passthrough.
* Full backend `golangci-lint run` must pass; clean the existing lint baseline without changing billing scope or introducing balance, pricing, deduction ledger, or refund behavior.

## Acceptance Criteria

* [ ] A same-format request path can use AxonHub-style raw forwarding without bypassing CCX key/channel control.
* [ ] A cross-format request path still converts format and runs through CCX failover and metrics.
* [ ] Forwarded requests record usage/billing dimensions consistently: request count, input tokens, output tokens, cache creation tokens, and cache read tokens.
* [ ] No balance deduction or pricing ledger is introduced.
* [ ] Retryable upstream errors still try the next key/BaseURL/channel.
* [ ] Blacklist-triggering errors still move keys to disabled/blacklisted state according to existing config.
* [ ] Circuit-open keys are skipped; half-open keys use probe semantics.
* [ ] Tests cover at least one same-format raw path and one cross-format converted path against control-plane behavior.
* [ ] `cd backend-go && golangci-lint run` passes for the full backend, not only new diff.

## Open Questions

* None for MVP. Cross-format builder expansion can be handled after same-format raw passthrough and usage statistics are verified.

## Technical Notes

* Main control-plane function: `backend-go/internal/handlers/common/upstream_failover.go`.
* Data-plane forwarding helper: `backend-go/internal/forwarding/builder.go`.
* URL construction helper: `backend-go/internal/forwarding/url.go`.
* Passthrough decision helper: `backend-go/internal/passthrough/passthrough.go`.
* Scheduler and channel selection: `backend-go/internal/scheduler/channel_scheduler.go`.
* Circuit breaker metrics state: `backend-go/internal/metrics/channel_metrics.go`.
* Current partial builder usage: Chat and Gemini handlers.
* Current provider builder usage: Messages and Responses handlers via `providers.ConvertToProviderRequest`.

## Out of Scope

* Replacing CCX scheduler or metrics ownership with AxonHub logic.
* Full billing ledger: user accounts, model price tables, balance deduction, refunds, and billing idempotency.
* Maintaining backward compatibility with removed/old passthrough switches.
* Frontend UI changes unless required to expose a new setting.
