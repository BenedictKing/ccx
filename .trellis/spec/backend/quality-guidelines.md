# Quality Guidelines

> Code quality standards for backend development.

---

## Overview

Backend quality in this repo comes from small focused packages, explicit error handling, and strong test coverage around config, converters, handlers, scheduler, and metrics.
Changes should follow the existing shape instead of introducing new architectural layers for small features.

---

## Forbidden Patterns

- Do not add backward-compatibility-only branches for old formats unless the current code still reads them intentionally.
- Do not hide business logic in `main.go`; keep it in `internal/*`.
- Do not introduce broad abstraction layers such as `repository`, `service`, or `manager` unless the existing code already has a natural home.
- Do not ignore errors from file IO, JSON parsing, HTTP, or request binding.
- Do not log secrets or raw credentials.
- Do not make one protocol family diverge from the others without a real protocol reason.

---

## Required Patterns

- Run `go fmt ./...` after backend edits.
- Prefer focused helpers and ownership by package.
- Keep handler-level request validation close to the HTTP boundary.
- Use contextual error wrapping with `%w` where upstream callers need the cause.
- Add tests next to the changed package when behavior changes.
- Reuse existing normalization and masking helpers before writing new ones.

Examples:

- Config behavior locked by tests: `backend-go/internal/config/config_baseurl_test.go`
- Table-driven unit tests: `backend-go/internal/providers/url_builder_test.go`
- HTTP-focused tests with `httptest`: `backend-go/internal/middleware/auth_test.go`

---

## Testing Requirements

- Backend changes should usually come with `_test.go` coverage unless the change is documentation-only.
- Prefer table-driven tests for pure logic.
- Use `httptest` for handler and middleware behavior.
- Keep regression tests beside the package that owns the logic.

Common commands:

- `cd backend-go && make test`
- `cd backend-go && make test-cover`
- `cd backend-go && make lint`

Examples:

- Config regression coverage: `backend-go/internal/config/config_baseurl_test.go`
- Scheduler coverage: `backend-go/internal/scheduler/channel_scheduler_test.go`
- Converter coverage: `backend-go/internal/converters/responses_converter_test.go`

## Proxy Handler Contracts

### Scope / Trigger

Use this contract when adding or changing a protocol proxy family under `backend-go/internal/handlers/<protocol>/`.

### Signatures

- Public proxy endpoint handlers should expose `Handler(envCfg, cfgManager, channelScheduler) gin.HandlerFunc`.
- Channel admin files should live beside the handler as `channels.go`.
- Shared retry paths should use `common.TryUpstreamWithAllKeys(...)` instead of protocol-local key loops.

### Contracts

- Register both default and `/:routePrefix/...` proxy routes when the protocol supports route prefixes.
- Add a distinct `scheduler.ChannelKind*`, metrics manager, and channel log store for independent routing families.
- Channel logs for proxy attempts must move through `pending -> connecting -> first_byte -> streaming -> completed|failed|cancelled`.
- `context.Canceled` is `cancelled`: it must finalize metrics as client cancel and must not blacklist or cooldown keys.
- Multipart proxy paths must preserve file parts and content type boundaries; never log raw binary multipart bodies.

### Validation & Error Matrix

| Case | Expected behavior |
|------|-------------------|
| Non-2xx retryable upstream error | classify with failover rules/default classifier, finalize failed attempt, try next key/channel |
| SSE preflight auth/quota/rate-limit error | apply blacklist/cooldown decision before headers are sent |
| `context.Canceled` during send or stream | finalize as `cancelled`, stop failover, do not mark key failed |
| Multipart image request with files | forward file parts intact and only rewrite safe form fields such as `model` |

### Good/Base/Bad Cases

- Good: `/v1/images/generations` and `/:routePrefix/v1/images/generations` use the same Images handler and `ChannelKindImages`.
- Base: JSON proxy requests may rewrite `model` through `config.RedirectModel`.
- Bad: reusing Chat metrics/log stores for Images, because failures and route health would contaminate another protocol family.

### Tests Required

- Handler package tests for route parsing, JSON validation, multipart preservation, and upstream header/auth forwarding.
- Common failover tests for client cancellation, stream preflight classification, and channel log terminal states.
- At minimum run `go test ./internal/handlers/...` after handler changes.

## Local Retry Loop Contracts

### Scope / Trigger

Use this contract when a protocol handler keeps a local key retry loop instead of delegating the whole attempt to `common.TryUpstreamWithAllKeys(...)`.
Current example: `backend-go/internal/handlers/responses/compact.go`.

### Signatures

- Shared classifier: `common.IsModelRouteUnavailableError(bodyBytes []byte) bool`.
- Local retry record helper should decide both cooldown and metrics failure behavior before calling:
  - `ConfigManager.MarkKeyAsFailed(apiKey, apiType)`
  - `ChannelScheduler.RecordFailure(baseURL, apiKey, serviceType, kind)`

### Contracts

- A routed-model-missing upstream response is identified by:
  - `error.code == "model_not_found"`
  - one of `message`, `detail`, `error_description`, or `msg` contains both `No available channel for model` and `under group`
- This case must continue failover to the next key/channel.
- This case must not write key cooldown state through `MarkKeyAsFailed`.
- This case must not count as a breaker/metrics failure through `RecordFailure`.
- It should still write a failed channel log entry for observability.

### Validation & Error Matrix

| Case | Expected behavior |
|------|-------------------|
| Routed model miss | Try next key/channel, no cooldown, no breaker failure, log failed attempt |
| Auth/quota/rate-limit failure | Apply normal classifier, mark failed or blacklist as configured, record metrics failure |
| Non-retryable client error | Return upstream response without retrying |
| Successful retry after routed model miss | Return success and count only the successful metrics request |

### Good/Base/Bad Cases

- Good: `responses/compact.go` calls `common.IsModelRouteUnavailableError(...)` before recording compact failover side effects.
- Base: Shared `common.TryUpstreamWithAllKeys(...)` already handles routed model misses internally.
- Bad: A local loop calls `ShouldRetryWithNextKey(...)`, then unconditionally calls `MarkKeyAsFailed(...)` and `RecordFailure(...)` for all retryable responses.

### Tests Required

- Add handler-level regression tests when changing a local retry loop.
- Assert all of these for routed model misses:
  - the next key/channel is attempted,
  - `ConfigManager.IsKeyFailed(key, apiType)` remains false,
  - metrics `FailureCount` remains unchanged for that key/channel,
  - channel logs still include the failed routed-model attempt.

---

## Code Review Checklist

- Is the change in the right package, or did it add logic to the wrong layer?
- Are all new/changed errors mapped to sensible HTTP status codes?
- Are secrets masked in logs and responses?
- If config or persisted shape changed, were defaulting/migration/tests updated too?
- If behavior exists for Messages, Responses, Gemini, and Chat, were all relevant channel families considered?
- Did the author reuse existing helpers before adding a new utility?
