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

## Upstream Header Override Contract

### Scope / Trigger

Use this contract when building upstream proxy requests that combine inbound client headers, platform-controlled headers, channel `customHeaders`, and the selected channel API key.

### Contracts

- Build the base upstream header set first, including safe inbound header passthrough and removal of proxy-only or hop-by-hop headers.
- Apply platform-controlled headers such as `Content-Type`, `Host`, and protocol version headers where the handler owns them.
- Apply channel `customHeaders` before authentication so channels can add non-auth metadata headers but cannot replace the selected key.
- Preserve inbound `User-Agent` by default for upstream proxy requests. Channel `customHeaders["User-Agent"]` may override the inbound value because it is admin-configured upstream metadata.
- For Claude-targeted upstream requests, set the Claude CLI fallback `User-Agent` only when no inbound or custom `User-Agent` is present.
- Strip sensitive inbound headers before custom headers and final authentication are applied. At minimum this includes `Authorization`, `x-api-key`, `x-goog-api-key`, `Cookie`, `Set-Cookie`, and `Proxy-Authorization`.
- Set the final selected authentication header last. For OpenAI-compatible paths this normally means `Authorization: Bearer <selected key>`; for Gemini paths it means `x-goog-api-key: <selected key>`.
- Final authentication setters must remove conflicting auth-like headers used by other protocol families when appropriate, such as `Authorization`, `x-api-key`, and `x-goog-api-key`.

### Validation & Error Matrix

| Case | Expected behavior |
|------|-------------------|
| Chat custom `Authorization` | Final upstream request uses the selected channel key |
| Gemini custom `x-goog-api-key` | Final upstream request uses the selected Gemini key |
| Images custom `Authorization` | Final upstream request uses the selected channel key |
| Responses compact custom `Authorization` | Final upstream request uses the selected channel key |
| Inbound `Cookie` or `Proxy-Authorization` | Header is stripped before the upstream request is sent |
| Missing Claude-target `User-Agent` | Claude CLI fallback is set |
| Custom `User-Agent` | Custom value wins over inbound and fallback values |

### Tests Required

- Handler-level regression tests must cover auth-like `customHeaders` for Chat, Gemini, Images, and Responses compact.
- Tests should assert the selected key wins on the actual upstream request header, not only on helper return values.

## Passthrough Decision Contracts

### Scope / Trigger

Use this contract when changing request body passthrough, raw response passthrough, or proxy preprocessing skip behavior.

### Forwarding Builder Boundary

- The forwarding builder is a data-plane helper: it may prepare upstream URL, method, body, safe headers, custom headers, final authentication, and raw response strategy.
- The forwarding builder must not own scheduler choice, key/BaseURL retry, failover classification, key blacklist/cooldown, circuit breaker state, channel log terminal status, trace affinity, or metrics finalization.
- Same-format raw stream handlers must route through shared attempt-scoped cleanup helpers before the client response is committed. Protocol-specific code may provide side-channel usage collectors, but it must not read `resp.Body` directly for raw stream passthrough.
- Raw stream preflight failures are control-plane inputs to `TryUpstreamWithAllKeys`; empty stream, invalid raw body, cooldown, and blacklist decisions must be returned while the client response headers are still uncommitted.

### Signatures

- Central decision entrypoint:

```go
passthrough.Decide(path string, kind scheduler.ChannelKind, upstream *config.UpstreamConfig, apiKey string) passthrough.Decision
```

- Response/body helpers:

```go
passthrough.AllowsStrictBodyPassthrough(path string, upstream *config.UpstreamConfig) bool
passthrough.AllowsRawResponsePassthrough(path string, upstream *config.UpstreamConfig) bool
passthrough.PatchTopLevelModel(body []byte, upstream *config.UpstreamConfig) []byte
passthrough.PatchPlatformFields(body []byte, upstream *config.UpstreamConfig) []byte
```

### Contracts

- Raw body/response passthrough is allowed only when inbound and outbound API formats match.
- Same-format request body passthrough keeps unknown client fields but must patch platform-controlled fields such as mapped `model`.
- Responses same-format passthrough must patch `model`, `reasoning`, `text`, and `service_tier` without dropping unknown request fields.
- Raw Responses non-stream responses must return the upstream JSON body unchanged while parsing usage for metrics and session state.
- Raw Responses streams may return upstream SSE bytes unchanged, but metrics parsing must be side-channel only and must not rewrite SSE events.
- Raw Responses stream usage parsing must be incremental by SSE event. Do not depend on a fixed prefix buffer, because `response.completed` can arrive after a large stream body.
- Raw Messages streams may bypass provider SSE normalization only for same-format `/v1/messages` -> Claude passthrough. The raw client branch must read from upstream `resp.Body` before provider stream parsing, while internal usage/preflight parsing stays side-channel only.
- Raw Chat streams may bypass SSE normalization only for same-format `/v1/chat/completions` -> OpenAI-compatible passthrough. The client branch must preserve upstream OpenAI SSE bytes unchanged while usage is parsed side-channel. OpenAI `prompt_tokens_details.cached_tokens` must become `Usage.CacheReadInputTokens`, with `Usage.PromptTokensTotal` set to the upstream `prompt_tokens`.
- Raw Gemini native streams may bypass SSE normalization only for same-format Gemini contents passthrough. The client branch must preserve upstream Gemini SSE bytes unchanged while `usageMetadata` is parsed side-channel. Gemini `cachedContentTokenCount` must become `Usage.CacheReadInputTokens`, `Usage.PromptTokensTotal` must be `promptTokenCount`, and `Usage.InputTokens` must be `max(promptTokenCount - cachedContentTokenCount, 0)`.
- Same-format non-stream Chat/OpenAI and Gemini/Gemini passthrough must use the same metrics usage normalization as their raw stream paths while returning upstream response bytes unchanged.
- Raw stream preflight must finish before response headers or body are written. Any empty stream, invalid raw event, cooldown, or blacklist decision must return to failover with the client response still uncommitted.
- Raw stream fan-out is attempt-scoped. Retry/failover/client-cancel cleanup must cancel the attempt, close the provider response body, drain branches, and wait for the fan-out goroutine to exit before the next key/channel attempt starts.
- Raw stream side-channel buffers must be bounded; oversized raw events or preflight buffers are invalid stream responses rather than unbounded memory growth.
- Channel config no longer exposes `streamPassthroughEnabled`, `sub2apiPassthroughEnabled`, `strictRequestPassthroughEnabled`, or `normalizeMetadataUserId`.
- Request preprocessing may be skipped only for Messages-family passthrough decisions that also produce direct raw response behavior.

### Validation & Error Matrix

| Case | Expected behavior |
|------|-------------------|
| `/v1/responses` -> `responses` | Return raw JSON/SSE and collect usage for metrics where available |
| `/v1/responses` -> `claude` | Do not raw passthrough; use conversion path |
| `/v1/messages` -> `claude` | Preserve unknown request fields and patch mapped `model` |
| `/v1/messages` stream -> `claude` | Preserve upstream raw SSE bytes and collect usage side-channel data |
| `/v1/chat/completions` -> `openai` with cached tokens | Preserve upstream bytes; metrics input is `prompt_tokens - prompt_tokens_details.cached_tokens`, cache read is `cached_tokens` |
| Gemini native -> `gemini` with cached content | Preserve upstream bytes; metrics input is `promptTokenCount - cachedContentTokenCount`, cache read is `cachedContentTokenCount` |
| Responses raw stream with usage after a large prefix | Preserve all bytes and still collect `response.completed.response.usage` |
| Cross-protocol request | Do not skip preprocessing and do not raw passthrough |
| Raw stream preflight cooldown/blacklist | Do not write headers; close the current attempt and fail over according to the sentinel error |
| Provider stream error during direct passthrough | Write SSE error unless the error is `context.Canceled` |

### Good/Base/Bad Cases

- Good: common handler code calls `passthrough.Decide(...)` instead of reimplementing format checks.
- Good: raw passthrough tests assert unknown top-level JSON fields and SSE `event:`, `id:`, `retry:`, comment, and `data:` formatting survive unchanged.
- Good: Messages, Chat, and Gemini raw stream tests use real upstream bodies with compact SSE fields such as `event:name` and `data:{...}` so provider normalization cannot hide a regression.
- Good: metrics tests assert cache read tokens separately from input tokens for OpenAI `prompt_tokens_details.cached_tokens` and Gemini `cachedContentTokenCount`.
- Base: non-raw protocol conversion may still normalize SSE field spacing and patch missing usage.
- Bad: buffering only the first N bytes of a raw SSE stream for metrics; long streams can place usage after that prefix.
- Bad: adding channel-level passthrough mode switches that override API format consistency.
- Bad: parsing raw SSE by modifying completed events and then calling it passthrough.
- Bad: starting the next key/channel attempt before the previous raw fan-out goroutine has exited and closed its response body.

### Tests Required

- `go test ./internal/passthrough`
- `go test ./internal/handlers/common`
- `go test ./internal/handlers/responses`
- `go test ./internal/handlers/messages`
- `go test ./internal/handlers/chat`
- `go test ./internal/handlers/gemini`
- `go test ./internal/providers`
- Include assertions for protocol mismatch, unknown field preservation, SSE byte preservation, stream error/cancel behavior, failover cleanup before retry, and metrics usage collection.
- Include raw passthrough metrics assertions for OpenAI cached tokens, Gemini cached content tokens, and Responses usage events that arrive after more than 1 MiB of earlier SSE bytes.

### Wrong vs Correct

#### Wrong

```go
if upstream.ServiceType == "claude" {
    return rawResponse
}
```

#### Correct

```go
decision := passthrough.Decide(path, kind, upstream, apiKey)
if decision.RawResponse {
    return rawResponse
}
```

#### Wrong

```go
metricsBuffer.Write(firstMiBOnly)
usage := parseUsage(metricsBuffer.String())
```

#### Correct

```go
collector.Feed(rawChunk)
_, _ = c.Writer.Write(rawChunk)
usage := collector.Finish()
```

## AxonHub-Style Forwarding Usage Stats Contract

### 1. Scope / Trigger

Use this contract when recording observability for AxonHub-style forwarding data-plane paths.

### 2. Signatures

- Metrics recorder:
  - `(*metrics.MetricsManager).RecordAxonHubForwardingUsage(baseURL, apiKey, serviceType, inboundFamily string, mode metrics.AxonHubForwardingMode, usage *types.Usage)`
- Response field:
  - `metrics.MetricsResponse.AxonHubForwarding *metrics.AxonHubForwardingUsageStats`
- JSON field under channel metrics responses:
  - `axonHubForwarding.requestCount`
  - `axonHubForwarding.inputTokens`
  - `axonHubForwarding.outputTokens`
  - `axonHubForwarding.cacheCreationTokens`
  - `axonHubForwarding.cacheReadTokens`
  - `axonHubForwarding.byRoute[].inboundFamily`
  - `axonHubForwarding.byRoute[].mode`

### 3. Contracts

- Forwarding usage stats are side-channel metrics only. They must not read, buffer, rewrite, or replay client-visible response bodies or SSE bytes.
- Keep ccx-owned scheduler, failover, key retry, blacklist, cooldown, circuit breaker, and normal metrics finalization as the source of control-plane behavior.
- Record request counts for finalized AxonHub-style forwarding attempts under the existing channel metrics surface.
- Retry/failover attempts are finalized attempts: failed attempts without usage still increment `requestCount`, while the successful attempt contributes token/cache usage from its returned `types.Usage`.
- Add token usage only from the usage object already produced by existing response/stream side-channel parsing.
- Classify stats by inbound protocol family (`messages`, `chat`, `responses`, `gemini`) and forwarding mode (`same_format_raw`, `cross_format_converted`).
- Do not count non-forwarding families such as Images in AxonHub forwarding usage stats.
- Include historical API keys in channel-level `axonHubForwarding` aggregation the same way ordinary request/token metrics include them.

### 4. Validation & Error Matrix

- Empty inbound family or empty mode -> skip AxonHub forwarding stats recording.
- Missing usage object -> still increment request count, add zero tokens.
- Unknown/non-forwarding channel kind -> skip AxonHub forwarding stats recording.
- Historical API key present -> aggregate its AxonHub forwarding stats into the channel response but do not expose it as an active key metric.

### 5. Good/Base/Bad Cases

- Good: same-format `/v1/messages` -> Claude records `messages/same_format_raw` request count and uses the existing usage object for tokens.
- Good: cross-format `/v1/responses` -> OpenAI-compatible failover records `responses/cross_format_converted`; retryable failed attempts increment request count with zero tokens, and the successful attempt adds input/output/cache tokens.
- Base: cross-format `/v1/responses` -> Claude records `responses/cross_format_converted` while response conversion and normal metrics finalization remain unchanged.
- Bad: reading `resp.Body` a second time just to compute AxonHub usage stats; this can corrupt raw response/SSE passthrough.

### 6. Tests Required

- Metrics tests must cover request count and token aggregation by inbound family and forwarding mode.
- Metrics tests must cover active + historical API key aggregation.
- Common failover tests must prove stats are appended next to existing finalize calls without replacing failover or metrics finalization.
- Common failover tests must include at least one cross-format converted path where a retryable upstream error moves to the next key/BaseURL/channel and preserves `axonHubForwarding` request count plus token/cache dimensions.
- Classification tests must prove non-forwarding families such as Images are not counted.

### 7. Wrong vs Correct

#### Wrong

```go
bodyBytes, _ := io.ReadAll(resp.Body) // corrupts the client-visible stream/body path
metricsManager.RecordAxonHubForwardingUsage(baseURL, key, serviceType, family, mode, parseUsage(bodyBytes))
```

#### Correct

```go
usage, err := handleSuccess(c, resp, upstream, apiKey)
metricsManager.RecordRequestFinalizeSuccess(baseURL, key, serviceType, requestID, usage)
metricsManager.RecordAxonHubForwardingUsage(baseURL, key, serviceType, family, mode, usage)
```

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

## Admin, Automation, and Probe Key Contracts

### Scope / Trigger

Use this contract when adding or changing any non-user proxy path that sends an upstream request with a channel key, including admin model listing, ping endpoints, capability tests, background model health checks, warmups, and scheduler-driven probes.

### Signatures

- Runtime key selection: `ConfigManager.GetNextAPIKey(upstream, failedKeys, apiType)` and `ConfigManager.GetNextAPIKeyForUser(upstream, failedKeys, apiType, userID)`.
- Admin/probe channel key selection: `ConfigManager.GetUsableAPIKeyForChannel(apiType, channelIndex)`.
- Explicit admin request validation: `ConfigManager.ValidateAdminProbeKey(apiType, channelIndex, apiKey)`.
- Background health target collection: `ConfigManager.collectModelsHealthCheckTargets(now, lastRunAt)`.

### Contracts

- These paths must only use keys from active `APIKeys` after filtering out:
  - keys present in `DisabledAPIKeys`,
  - keys in `failedKeys`,
  - keys still inside `failedKeysCache` cooldown.
- Do not fall back to `DisabledAPIKeys` for admin convenience, model listing, capability tests, ping, or automatic health checks.
- Do not use the "oldest failed key" when all active keys are cooling down; return an explicit no-key/unavailable error instead.
- If an admin request supplies a temporary `baseUrl`, unknown keys are allowed for first-time testing, but known disabled or cooling keys on an existing channel remain rejected.
- If a helper must build a request from `APIKeys[0]`, callers must first replace the channel copy with a single key returned by `GetUsableAPIKeyForChannel`.

### Validation & Error Matrix

| Case | Expected behavior |
|------|-------------------|
| Active key is available | Use that key normally |
| Only disabled keys exist | Do not send upstream request; return no available key/no_api_key |
| Only cooldown keys exist | Do not send upstream request; return no available key/no_api_key |
| Disabled key still appears in `APIKeys` due to config drift | Treat it as disabled and skip it |
| Explicit admin model-list key is disabled | Return `400`, do not call upstream |
| Explicit admin model-list key is cooling down | Return `400`, do not call upstream |
| Temporary baseUrl with unknown key | Allow the probe because the key is not yet part of the saved channel |

### Good/Base/Bad Cases

- Good: capability tests call `GetUsableAPIKeyForChannel` before creating or running a job, and re-resolve the key before each model request.
- Base: Images ping may pass a copied upstream with `APIKeys = []string{usableKey}` into a request builder that reads `APIKeys[0]`.
- Bad: a model-list, ping, or background check handler reads `upstream.APIKeys[0]` directly from persisted config.
- Bad: an admin helper uses `DisabledAPIKeys[0]` when `APIKeys` is empty.

### Tests Required

- Config tests must assert disabled and cooldown keys are skipped by `GetNextAPIKey`, `GetAdminAPIKey`, and `GetUsableAPIKeyForChannel`.
- Handler tests must assert explicit disabled/cooldown admin keys return before upstream is called.
- Capability-test tests must assert disabled-only and cooldown-only channels create failed jobs without sending upstream requests.
- Background health-check tests must assert disabled/cooldown keys are omitted from target `apiKeys` and cooldown-only channels are not probed.

---

## Code Review Checklist

- Is the change in the right package, or did it add logic to the wrong layer?
- Are all new/changed errors mapped to sensible HTTP status codes?
- Are secrets masked in logs and responses?
- If config or persisted shape changed, were defaulting/migration/tests updated too?
- If behavior exists for Messages, Responses, Gemini, and Chat, were all relevant channel families considered?
- Did the author reuse existing helpers before adding a new utility?
