# Research: Messages streaming passthrough/failover lifecycle

- Query: Current ccx Messages -> Claude streaming passthrough/failover lifecycle, with raw SSE preservation seams, cancellation/failover behavior, tests, and risks.
- Scope: internal
- Date: 2026-05-06

## Findings

### Files found

- `backend-go/internal/handlers/messages/handler.go` - Claude Messages HTTP handler; reads/preprocesses requests, selects single vs multi-channel mode, and delegates all upstream attempts to shared failover helpers.
- `backend-go/internal/handlers/common/stream.go` - shared Messages stream pipeline; performs preflight before headers, chooses direct raw passthrough vs local event patching, collects usage side-channel data, and maps stream errors.
- `backend-go/internal/handlers/common/upstream_failover.go` - shared BaseURL/key failover loop; classifies client cancellation, retryable upstream failures, stream preflight failures, cooldown, blacklist, and success metrics.
- `backend-go/internal/handlers/common/multi_channel_failover.go` - outer multi-channel selection loop; stops channel failover on request context cancellation and delegates per-channel attempts to `TryUpstreamWithAllKeys`.
- `backend-go/internal/providers/provider.go` - provider contract and stream cancellation helpers; provider streams return event/error channels.
- `backend-go/internal/providers/claude.go` - Claude request builder and stream reader; patches mapped model, builds upstream request with selected key, groups SSE lines into complete events.
- `backend-go/internal/handlers/common/stream_test.go` - stream pipeline tests for raw passthrough preservation, error events, cancellation, and preflight cooldown.
- `backend-go/internal/handlers/common/upstream_failover_passthrough_test.go` - passthrough decision and request body preprocessing tests.
- `backend-go/internal/handlers/common/upstream_failover_model_unavailable_test.go` - failover loop tests including stream cooldown continuing to the next key.
- `backend-go/internal/handlers/common/client_error_test.go` - distinguishes `context.Canceled` from connection failures such as broken pipe/reset.
- `backend-go/internal/providers/claude_passthrough_test.go` - Claude provider request passthrough tests for unknown fields and mapped model patching.
- `backend-go/internal/providers/sse_normalization_test.go` - verifies SSE field spacing normalization in provider readers.
- `backend-go/internal/providers/stream_cancel_test.go` - verifies provider channel sends return promptly when stream context is canceled.
- `backend-go/internal/handlers/messages/handler_response_matrix_test.go` - end-to-end Messages handler tests, including raw Claude JSON passthrough metrics normalization.

### Current call flow

1. `messages.Handler` authenticates, reads the request body, runs handler-level preprocessing, normalizes `metadata.user_id`, stores the body under `requestBodyBytes`, unmarshals `types.ClaudeRequest`, extracts `userID`, and dispatches to single or multi-channel mode (`backend-go/internal/handlers/messages/handler.go:24`, `backend-go/internal/handlers/messages/handler.go:35`, `backend-go/internal/handlers/messages/handler.go:56`, `backend-go/internal/handlers/messages/handler.go:57`, `backend-go/internal/handlers/messages/handler.go:78`).
2. Multi-channel mode calls `common.HandleMultiChannelFailover`; each selected channel calls `common.TryUpstreamWithAllKeys` with `scheduler.ChannelKindMessages`, the current body, `claudeReq.Stream`, provider request builder, and stream/non-stream success callback (`backend-go/internal/handlers/messages/handler.go:85`, `backend-go/internal/handlers/messages/handler.go:121`, `backend-go/internal/handlers/messages/handler.go:153`).
3. Single-channel mode obtains the current upstream, validates keys/provider, builds default URL ordering, then calls the same `common.TryUpstreamWithAllKeys` path (`backend-go/internal/handlers/messages/handler.go:218`, `backend-go/internal/handlers/messages/handler.go:246`).
4. `TryUpstreamWithAllKeys` loops over BaseURLs and keys, prepares the per-attempt body, restores it into Gin context, records selected key, clones the upstream with the current BaseURL, builds the provider request, records metrics/log start, and calls `SendRequest` (`backend-go/internal/handlers/common/upstream_failover.go:154`, `backend-go/internal/handlers/common/upstream_failover.go:246`, `backend-go/internal/handlers/common/upstream_failover.go:268`).
5. For stream responses, the success callback calls `common.HandleStreamResponse(c, resp, provider, envCfg, actualRequestBody, upstreamCopy)` (`backend-go/internal/handlers/messages/handler.go:153`, `backend-go/internal/handlers/messages/handler.go:246`).
6. `HandleStreamResponse` wraps the request context with `attemptCtx`, asks the provider to produce event/error channels, preflights the stream before sending headers, then either returns safe failover errors, sets SSE headers, raw-forwards passthrough events, or sends events through the local patching pipeline (`backend-go/internal/handlers/common/stream.go:856`, `backend-go/internal/handlers/common/stream.go:874`, `backend-go/internal/handlers/common/stream.go:907`, `backend-go/internal/handlers/common/stream.go:917`, `backend-go/internal/handlers/common/stream.go:977`).
7. `ClaudeProvider.ConvertToProviderRequest` reads the cached body, patches only mapped `model`, builds the upstream URL, uses `http.NewRequestWithContext(c.Request.Context(), ...)`, applies headers/custom headers, then sets the selected authentication header last (`backend-go/internal/providers/claude.go:35`, `backend-go/internal/providers/claude.go:46`, `backend-go/internal/providers/claude.go:82`, `backend-go/internal/providers/claude.go:92`, `backend-go/internal/providers/claude.go:94`).
8. `ClaudeProvider.HandleStreamResponse` reads upstream SSE with a scanner, normalizes SSE field spacing, groups lines into complete SSE events separated by blank lines, sends them through `eventChan`, closes the body on context cancellation, and suppresses expected tool-use disconnect scanner errors (`backend-go/internal/providers/claude.go:110`, `backend-go/internal/providers/claude.go:118`, `backend-go/internal/providers/claude.go:124`, `backend-go/internal/providers/claude.go:146`, `backend-go/internal/providers/claude.go:155`, `backend-go/internal/providers/claude.go:174`).

### Where raw SSE could be preserved

- The current raw SSE preservation point is `common.HandleStreamResponse` direct passthrough branch: it writes buffered preflight events and subsequent events with `fmt.Fprint(c.Writer, event)` while collecting usage side-channel data via `collectPassthroughStreamUsage`; client bytes are not modified in this branch (`backend-go/internal/handlers/common/stream.go:917`, `backend-go/internal/handlers/common/stream.go:922`, `backend-go/internal/handlers/common/stream.go:931`, `backend-go/internal/handlers/common/stream.go:933`, `backend-go/internal/handlers/common/stream.go:996`, `backend-go/internal/handlers/common/stream.go:1248`).
- The direct passthrough branch is gated by `ShouldDirectPassthroughForRequest(c.Request.URL.Path, upstream, SelectedAPIKeyFromContext(c))`, which delegates to the central `passthrough.Decide(...)` helper (`backend-go/internal/handlers/common/stream.go:917`, `backend-go/internal/handlers/common/upstream_failover.go:50`, `backend-go/internal/handlers/common/upstream_failover.go:62`).
- `upstream == nil` also forces direct passthrough in `HandleStreamResponse`; tests use that seam to validate raw SSE behavior without constructing passthrough config (`backend-go/internal/handlers/common/stream.go:917`, `backend-go/internal/handlers/common/stream_test.go:691`).
- True byte-for-byte upstream SSE is not preserved at the provider reader layer because `ClaudeProvider.HandleStreamResponse` normalizes `data:`, `event:`, `id:`, and `retry:` spacing before assembling events (`backend-go/internal/providers/provider.go:75`, `backend-go/internal/providers/provider.go:79`, `backend-go/internal/providers/claude.go:146`). The current raw-preservation test uses a fake provider and therefore proves the common handler does not alter already-received event strings, not that the Claude provider preserves upstream spacing.
- If the goal is upstream raw SSE byte preservation for Claude same-format passthrough, the earliest useful seam is the provider interface or a sibling raw streaming path before `normalizeSSEFieldLine`. The common handler can already preserve event strings once received, but it cannot recover original spacing that the provider normalized.
- The non-direct stream path intentionally mutates events: `ProcessStreamEvent` injects or patches usage, model, input tokens, and message deltas before writing to the client (`backend-go/internal/handlers/common/stream.go:469`, `backend-go/internal/handlers/common/stream.go:440`, `backend-go/internal/handlers/common/stream.go:977`). This path should not be described as raw passthrough.

### Cancellation and failover behavior

- `isClientSideError` treats only `context.Canceled` as client-side cancellation for upstream/failover purposes; broken pipe, connection reset, deadline exceeded, and EOF are not classified as client cancel (`backend-go/internal/handlers/common/upstream_failover.go:24`, `backend-go/internal/handlers/common/upstream_failover.go:26`).
- If `SendRequest` returns client cancellation, metrics finalize as client cancel, channel request end is recorded, the channel log is completed as "client canceled", and `TryUpstreamWithAllKeys` returns `handled=true` without trying failover (`backend-go/internal/handlers/common/upstream_failover.go:268`, `backend-go/internal/handlers/common/upstream_failover.go:272`, `backend-go/internal/handlers/common/upstream_failover.go:274`).
- If `SendRequest` returns a real upstream/connection error, the key is failed, cooldown is recorded, metrics failure is recorded, URL failure may be marked, and the loop continues to the next key/BaseURL (`backend-go/internal/handlers/common/upstream_failover.go:268`).
- Stream preflight happens before headers are written; `ErrEmptyStreamResponse`, `ErrInvalidResponseBody`, `ErrCooldownKey`, and `ErrBlacklistKey` can therefore safely trigger failover in `TryUpstreamWithAllKeys` (`backend-go/internal/handlers/common/stream.go:68`, `backend-go/internal/handlers/common/stream.go:874`, `backend-go/internal/handlers/common/stream.go:890`, `backend-go/internal/handlers/common/stream.go:896`, `backend-go/internal/handlers/common/stream.go:904`, `backend-go/internal/handlers/common/upstream_failover.go:449`, `backend-go/internal/handlers/common/upstream_failover.go:462`, `backend-go/internal/handlers/common/upstream_failover.go:478`).
- Direct passthrough stream errors after headers are sent write an SSE `event: error` unless the error is `context.Canceled`; the function then returns the error. At this point failover cannot produce a clean alternate response because headers/body have already been sent (`backend-go/internal/handlers/common/stream.go:945`, `backend-go/internal/handlers/common/stream.go:1571`).
- Direct passthrough request context cancellation calls `cancelAttempt`, drains provider channels, and returns `context.Canceled` without writing an SSE error (`backend-go/internal/handlers/common/stream.go:951`, `backend-go/internal/handlers/common/stream.go:954`).
- The provider-side helpers make cancellation cooperative: `closeStreamBodyOnCancel` closes the upstream body, and `sendStreamEvent` / `sendStreamError` avoid blocking after context cancellation (`backend-go/internal/providers/provider.go:38`, `backend-go/internal/providers/provider.go:53`, `backend-go/internal/providers/provider.go:62`).
- In multi-channel mode, the outer loop checks `c.Request.Context().Done()` before selecting each channel and stops channel failover on cancellation (`backend-go/internal/handlers/common/multi_channel_failover.go:63`).

### Test seams

- `testStreamProvider` in `common/stream_test.go` directly supplies `eventChan` and `errChan` to `HandleStreamResponse`, which is the best seam for stream preflight, raw event preservation, post-header error, and cancellation behavior (`backend-go/internal/handlers/common/stream_test.go:19`, `backend-go/internal/handlers/common/stream_test.go:33`).
- `TestHandleStreamResponse_DirectPassthroughPreservesRawSSEFields` asserts common raw passthrough preserves comments, `id:`, `retry:`, compact `event:`, and compact `data:` exactly as the fake provider emitted them (`backend-go/internal/handlers/common/stream_test.go:691`).
- `TestHandleStreamResponse_DirectPassthroughErrChanWritesErrorEvent` and `TestHandleStreamResponse_DirectPassthroughContextCanceledReturnsWithoutErrorEvent` cover post-header direct passthrough error vs cancellation behavior (`backend-go/internal/handlers/common/stream_test.go:724`, `backend-go/internal/handlers/common/stream_test.go:762`).
- `TestHandleStreamResponse_PreflightRateLimitReturnsCooldown` covers pre-header SSE error classification into `ErrCooldownKey` with no response body written (`backend-go/internal/handlers/common/stream_test.go:798`).
- `TestTryUpstreamWithAllKeys_CooldownStreamErrorContinuesFailover` proves stream cooldown from `handleSuccess` marks the first key failed and retries the second key successfully (`backend-go/internal/handlers/common/upstream_failover_model_unavailable_test.go:120`).
- `TestShouldDirectPassthroughForRequestRequiresProtocolConsistency` and `TestPrepareRequestBodyForUpstream_PassthroughAndPreprocess` cover same-format passthrough vs cross-protocol preprocessing (`backend-go/internal/handlers/common/upstream_failover_passthrough_test.go:52`, `backend-go/internal/handlers/common/upstream_failover_passthrough_test.go:100`).
- `TestClaudeProvider_StrictPassthrough_PreservesUnknownFieldsAndPatchesModel` covers request-side Claude same-format passthrough preserving unknown fields while patching mapped model (`backend-go/internal/providers/claude_passthrough_test.go:150`).
- `TestNormalizeSSEFieldLine` explicitly locks in provider-level SSE spacing normalization (`backend-go/internal/providers/sse_normalization_test.go:9`).
- `TestSendStreamEventReturnsWhenContextCanceled` and `TestSendStreamErrorReturnsWhenContextCanceled` cover provider channel cancellation non-blocking behavior (`backend-go/internal/providers/stream_cancel_test.go:9`).
- `TestMessagesHandler_RawPassthroughLowQualityRecordsNormalizedMetrics` verifies raw Claude JSON passthrough response remains unchanged while metrics usage can be normalized separately (`backend-go/internal/handlers/messages/handler_response_matrix_test.go:96`).

### Risks

- Provider-level SSE normalization conflicts with strict "raw upstream SSE bytes unchanged" semantics. The common handler can preserve received event strings, but `ClaudeProvider` already rewrites field spacing before common handling (`backend-go/internal/providers/provider.go:79`, `backend-go/internal/providers/claude.go:146`).
- `bufio.Scanner` line-based parsing and 1MB max token size may still fail on unusually large single-line SSE data fields; failure after preflight headers are sent becomes a streamed error, not a clean failover (`backend-go/internal/providers/claude.go:124`, `backend-go/internal/handlers/common/stream.go:945`).
- Preflight consumes events before sending headers. It enables safe failover for empty/error streams, but raw passthrough latency and initial event timing depend on preflight reaching a pass condition or 30s timeout (`backend-go/internal/handlers/common/stream.go:68`).
- The handler-level preprocessing stores normalized body at entry, but `TryUpstreamWithAllKeys` later re-prepares each attempt from `rawBodyBytes`; same-format passthrough skips most preprocessing except configured billing-header stripping. Changes in this area can easily double-apply or bypass preprocessing if they ignore `prepareRequestBodyForUpstream` (`backend-go/internal/handlers/messages/handler.go:35`, `backend-go/internal/handlers/messages/handler.go:57`, `backend-go/internal/handlers/messages/handler.go:78`, `backend-go/internal/handlers/common/upstream_failover.go:110`, `backend-go/internal/handlers/common/upstream_failover.go:246`).
- Direct passthrough after headers are flushed cannot fail over on later upstream stream errors without corrupting protocol semantics; the current behavior writes an SSE error event and returns error for metrics/logging (`backend-go/internal/handlers/common/stream.go:907`, `backend-go/internal/handlers/common/stream.go:945`).
- The `upstream == nil` direct passthrough path is useful for tests but can mask real passthrough-decision behavior if future tests rely only on nil upstream (`backend-go/internal/handlers/common/stream.go:917`).
- The existing raw SSE preservation test uses a fake provider, so it does not catch provider-level spacing normalization or scanner regrouping changes. Add provider+handler integration coverage if upstream byte preservation becomes a hard requirement.
- Cancellation classification intentionally treats broken pipe/connection reset as non-client-side for failover-loop purposes, while stream write errors use `IsClientDisconnectError` to suppress noisy logs. This split is tested but easy to misunderstand when changing cancellation behavior (`backend-go/internal/handlers/common/upstream_failover.go:24`, `backend-go/internal/handlers/common/client_error_test.go:10`, `backend-go/internal/handlers/common/client_error_test.go:80`).

## External References

- None. This research is based on local repository code and tests only.

## Related Specs

- `.trellis/spec/backend/directory-structure.md` - shared stream/failover helpers belong in `internal/handlers/common`, provider transport logic belongs in `internal/providers`.
- `.trellis/spec/backend/error-handling.md` - stream code already has sentinel errors and expected disconnect behavior; handlers translate errors at the edge.
- `.trellis/spec/backend/logging-guidelines.md` - logs should use bracketed component tags and avoid leaking secrets.
- `.trellis/spec/backend/quality-guidelines.md:130` - passthrough contract requires central `passthrough.Decide(...)`, same-format raw response only, side-channel metrics parsing for raw streams, and direct passthrough error/cancel tests.

## Caveats / Not Found

- No production code was edited.
- No external docs were consulted.
- I did not run tests; this is static lifecycle research.
- I did not find a provider-level test proving Claude upstream SSE bytes remain byte-for-byte unchanged through `ClaudeProvider.HandleStreamResponse`; existing evidence proves common-handler preservation after events are supplied and separately proves provider field-spacing normalization.
