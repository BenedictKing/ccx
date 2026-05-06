# Research: Responses raw stream passthrough

- Query: Research current ccx Responses raw stream passthrough and usage/metrics side-channel behavior, with guidance for the Messages -> Claude raw stream fan-out pilot.
- Scope: internal
- Date: 2026-05-06

## Findings

### Files found

- `backend-go/internal/handlers/responses/handler.go` - Responses proxy handler, raw passthrough decision, non-stream raw response handling, raw SSE copy path, stream usage extraction, session accounting.
- `backend-go/internal/handlers/responses/handler_response_matrix_test.go` - Handler regression tests for raw JSON passthrough, raw SSE byte preservation, same-format raw stream behavior, and metrics recording.
- `backend-go/internal/handlers/responses/handler_usage_test.go` - Unit tests for Responses usage patching, prompt total handling, and cached token fallback.
- `backend-go/internal/handlers/responses/handler_session_test.go` - Regression test for previous response ID/session behavior during successful Responses handling.
- `backend-go/internal/passthrough/passthrough.go` - Central same-format passthrough decision helpers.
- `backend-go/internal/handlers/common/upstream_failover.go` - Shared attempt/key failover loop that records connected/finalized metrics around protocol-specific success handlers.
- `backend-go/internal/metrics/channel_metrics.go` - Metrics normalization and pending request finalization with usage.
- `backend-go/internal/handlers/common/stream.go` - Existing Messages stream direct passthrough path, usage side-channel, and stream error/cancel behavior.
- `backend-go/internal/handlers/messages/handler_response_matrix_test.go` - Messages raw passthrough metrics regression for non-stream low-quality normalization.
- `backend-go/internal/handlers/common/stream_test.go` - Existing direct passthrough stream byte preservation and stream error behavior tests.
- `backend-go/internal/providers/matrix_responses_test.go` - Provider-level same-format Responses request passthrough coverage.
- `backend-go/internal/types/responses.go` and `backend-go/internal/types/types.go` - Responses usage and internal metrics usage shapes.

### Raw passthrough decision

- Responses stores a per-attempt context flag under `responsesRawPassthroughContextKey` after `TryUpstreamWithAllKeys` has cloned the upstream and selected the actual BaseURL/key attempt. Both multi-channel and single-channel paths set it with `passthrough.AllowsRawResponsePassthrough("/v1/responses", upstreamCopy)` before invoking `handleSuccess` (`backend-go/internal/handlers/responses/handler.go:142`, `backend-go/internal/handlers/responses/handler.go:230`).
- `AllowsRawResponsePassthrough` is centralized and only returns true when inbound API format and outbound service format match. `/v1/responses` maps to OpenAI Responses and `ServiceType == "responses"` maps to the same outbound format (`backend-go/internal/passthrough/passthrough.go:49`, `backend-go/internal/passthrough/passthrough.go:63`, `backend-go/internal/passthrough/passthrough.go:91`).
- The backend spec already makes this a contract: raw body/response passthrough is same-format only; raw Responses non-stream responses return upstream JSON unchanged while parsing usage/session; raw Responses streams may return upstream SSE bytes unchanged, but metrics parsing must be side-channel only.

### Non-stream raw Responses path

- `handleSuccess` branches on stream first, then reads non-stream bodies into memory. When the raw passthrough flag is true, it unmarshals the body into `types.ResponsesResponse` only for side effects. It then patches usage on the parsed copy, records session state, forwards upstream headers, and writes the original `bodyBytes` unchanged with `c.Data` (`backend-go/internal/handlers/responses/handler.go:260`, `backend-go/internal/handlers/responses/handler.go:300`, `backend-go/internal/handlers/responses/handler.go:311`, `backend-go/internal/handlers/responses/handler.go:315`, `backend-go/internal/handlers/responses/handler.go:320`).
- Metrics usage is returned from the patched parsed copy, not from the raw bytes written to the client. The code derives `PromptTokensTotal` from the original upstream usage before patching, then converts `ResponsesUsage` to internal `types.Usage` (`backend-go/internal/handlers/responses/handler.go:322`, `backend-go/internal/handlers/responses/handler.go:327`).
- `TestResponsesHandler_NonStreamRawPassthroughPreservesUnknownFieldsAndRecordsMetrics` locks this down: the raw JSON response, including unknown top-level/vendor fields, must be byte-for-byte identical, while metrics still record one success with `input_tokens=23` and `output_tokens=11` (`backend-go/internal/handlers/responses/handler_response_matrix_test.go:99`, `backend-go/internal/handlers/responses/handler_response_matrix_test.go:122`, `backend-go/internal/handlers/responses/handler_response_matrix_test.go:142`).

### Raw SSE preservation pattern

- Streaming Responses checks the raw passthrough flag before any scanner/preflight/conversion path. Same-format raw streams go directly to `handleRawResponsesStreamPassthrough` (`backend-go/internal/handlers/responses/handler.go:563`, `backend-go/internal/handlers/responses/handler.go:577`).
- The raw SSE helper forwards upstream headers, defaults `Content-Type` only when missing, sets status, then reads `resp.Body` using a 32 KiB byte buffer. Each chunk is written directly to `c.Writer` and flushed. This avoids `bufio.Scanner`, line normalization, SSE event reconstruction, injected usage events, and completed-event patching (`backend-go/internal/handlers/responses/handler.go:929`, `backend-go/internal/handlers/responses/handler.go:930`, `backend-go/internal/handlers/responses/handler.go:937`, `backend-go/internal/handlers/responses/handler.go:940`, `backend-go/internal/handlers/responses/handler.go:952`, `backend-go/internal/handlers/responses/handler.go:955`).
- Usage parsing is side-channel only. The raw copy loop appends at most 1 MiB of raw stream bytes to a `metricsBuffer`; after EOF, it parses usage from that buffer and returns metrics usage. The bytes already written to the client are not rewritten (`backend-go/internal/handlers/responses/handler.go:938`, `backend-go/internal/handlers/responses/handler.go:939`, `backend-go/internal/handlers/responses/handler.go:944`, `backend-go/internal/handlers/responses/handler.go:967`).
- `collectRawResponsesStreamUsage` normalizes CRLF to LF only inside the metrics parser, splits events on blank lines, extracts text for possible side use, and collects usage via `checkResponsesEventUsage`. This parser is detached from client output (`backend-go/internal/handlers/responses/handler.go:975`, `backend-go/internal/handlers/responses/handler.go:978`, `backend-go/internal/handlers/responses/handler.go:980`, `backend-go/internal/handlers/responses/handler.go:981`, `backend-go/internal/handlers/responses/handler.go:998`).
- `TestResponsesHandler_StreamRawPassthroughPreservesSSEBytes` asserts comments, `id:`, `event:`, `retry:`, compact `data:` formatting, blank lines, and final newlines survive unchanged while metrics still collect `input_tokens=2` and `output_tokens=1` (`backend-go/internal/handlers/responses/handler_response_matrix_test.go:157`, `backend-go/internal/handlers/responses/handler_response_matrix_test.go:159`, `backend-go/internal/handlers/responses/handler_response_matrix_test.go:192`, `backend-go/internal/handlers/responses/handler_response_matrix_test.go:196`).
- `TestResponsesHandler_StreamSameFormatAlwaysUsesRawPassthrough` separately asserts same-format `/v1/responses -> responses` stream responses remain raw (`backend-go/internal/handlers/responses/handler_response_matrix_test.go:207`, `backend-go/internal/handlers/responses/handler_response_matrix_test.go:238`, `backend-go/internal/handlers/responses/handler_response_matrix_test.go:242`).

### Usage parsing and normalization

- Responses usage shape includes OpenAI fields (`input_tokens`, `output_tokens`, `total_tokens`, `input_tokens_details.cached_tokens`) plus Claude cache extensions (`cache_creation_input_tokens`, 5m/1h creation fields, `cache_read_input_tokens`, `cache_ttl`) (`backend-go/internal/types/responses.go:67`, `backend-go/internal/types/responses.go:74`, `backend-go/internal/types/responses.go:81`).
- `patchResponsesUsage` is used on non-stream parsed copies and non-raw converted responses. It estimates missing usage, patches suspicious `<=1` input/output values, and recalculates total tokens including cache tokens. It intentionally treats Claude-native cache fields differently from OpenAI `input_tokens_details.cached_tokens` so OpenAI cached-token details do not suppress input patching (`backend-go/internal/handlers/responses/handler.go:433`, `backend-go/internal/handlers/responses/handler.go:438`, `backend-go/internal/handlers/responses/handler.go:443`, `backend-go/internal/handlers/responses/handler.go:446`, `backend-go/internal/handlers/responses/handler.go:469`, `backend-go/internal/handlers/responses/handler.go:479`).
- Stream usage detection only reads `response.completed.response.usage`. It extracts token fields, cache fields, `input_tokens_details.cached_tokens` fallback, and TTL classification into `responsesStreamUsage` (`backend-go/internal/handlers/responses/handler.go:1082`, `backend-go/internal/handlers/responses/handler.go:1101`, `backend-go/internal/handlers/responses/handler.go:1104`, `backend-go/internal/handlers/responses/handler.go:1134`, `backend-go/internal/handlers/responses/handler.go:1171`, `backend-go/internal/handlers/responses/handler.go:1182`).
- Collected stream usage uses max input/output/total values and replaces cache fields when positive. This supports multiple events without double-counting totals (`backend-go/internal/handlers/responses/handler.go:1201`, `backend-go/internal/handlers/responses/handler.go:1203`, `backend-go/internal/handlers/responses/handler.go:1206`, `backend-go/internal/handlers/responses/handler.go:1212`, `backend-go/internal/handlers/responses/handler.go:1227`).
- `metricsUsageFromResponsesUsage` maps Responses usage to internal usage and falls back from `input_tokens_details.cached_tokens` to `CacheReadInputTokens` when the Claude field is absent (`backend-go/internal/handlers/responses/handler.go:415`, `backend-go/internal/handlers/responses/handler.go:416`, `backend-go/internal/handlers/responses/handler.go:421`).
- `promptTokensTotalFromResponsesInput` only preserves the upstream prompt-total denominator for same-format `responses` upstreams with meaningful input tokens, or tiny input values backed by Claude cache fields. Cross-format converted routes return zero so metrics do not treat converted prompt values as authoritative (`backend-go/internal/handlers/responses/handler.go:405`, `backend-go/internal/handlers/responses/handler.go:406`, `backend-go/internal/handlers/responses/handler.go:409`).
- Unit coverage records these edge cases: recalculated total includes cache tokens, patched tiny input without Claude cache is ignored for prompt total, Claude-cache-backed tiny input keeps total, non-Responses upstream never records prompt total, and cached-token fallback populates `CacheReadInputTokens` (`backend-go/internal/handlers/responses/handler_usage_test.go:19`, `backend-go/internal/handlers/responses/handler_usage_test.go:37`, `backend-go/internal/handlers/responses/handler_usage_test.go:85`).
- Metrics normalizes cache hit accounting by subtracting cache read tokens from `PromptTokensTotal` when both are present, and falls back cache creation tokens from 5m/1h TTL splits when aggregate creation is absent (`backend-go/internal/metrics/channel_metrics.go:667`, `backend-go/internal/metrics/channel_metrics.go:671`, `backend-go/internal/metrics/channel_metrics.go:673`, `backend-go/internal/metrics/channel_metrics.go:681`). Tests cover the normalized cache hit denominator and TTL fallback (`backend-go/internal/metrics/channel_metrics_cache_stats_test.go:55`, `backend-go/internal/metrics/channel_metrics_cache_stats_test.go:88`).

### Metrics and attempt accounting

- `TryUpstreamWithAllKeys` records request start and a pending connected request before sending upstream. It passes the protocol-specific `usage` returned by `handleSuccess` into `RecordRequestFinalizeSuccess` after the response handler completes (`backend-go/internal/handlers/common/upstream_failover.go:244`, `backend-go/internal/handlers/common/upstream_failover.go:248`, `backend-go/internal/handlers/common/upstream_failover.go:499`).
- On success finalization, metrics increments `SuccessCount`, extracts token fields from usage, writes them into the pending history record, and persists them when a store is configured (`backend-go/internal/metrics/channel_metrics.go:1008`, `backend-go/internal/metrics/channel_metrics.go:1014`, `backend-go/internal/metrics/channel_metrics.go:1020`, `backend-go/internal/metrics/channel_metrics.go:1026`).
- If a stream/client error occurs in the success handler, failover behavior depends on the returned error. Existing shared handling distinguishes client cancellation, retryable stream errors, cooldown/blacklist sentinel errors, and generic processing errors. Generic errors after handler invocation finalize the attempt as failure and return handled (`backend-go/internal/handlers/common/upstream_failover.go:430`, `backend-go/internal/handlers/common/upstream_failover.go:441`, `backend-go/internal/handlers/common/upstream_failover.go:457`, `backend-go/internal/handlers/common/upstream_failover.go:489`).

### Session accounting

- Non-stream Responses success updates session state after usage patching. It appends parsed input items with zero tokens, appends response output items with `responsesResp.Usage.TotalTokens`, updates the last response ID, records response ID to session mapping, and writes `previous_id` into the response object using the previous session value (`backend-go/internal/handlers/responses/handler.go:313`, `backend-go/internal/handlers/responses/handler.go:367`, `backend-go/internal/handlers/responses/handler.go:380`, `backend-go/internal/handlers/responses/handler.go:385`, `backend-go/internal/handlers/responses/handler.go:389`).
- `recordResponsesSession` respects `store:false` and no-ops on nil dependencies (`backend-go/internal/handlers/responses/handler.go:367`, `backend-go/internal/handlers/responses/handler.go:371`).
- Current raw stream passthrough does not update session state; it only returns usage for metrics. There is no stream-side call equivalent to `recordResponsesSession` in `handleRawResponsesStreamPassthrough` (`backend-go/internal/handlers/responses/handler.go:929`, `backend-go/internal/handlers/responses/handler.go:967`).
- `TestHandleSuccess_PreservesPreviousResponseID` covers non-stream session response ID preservation (`backend-go/internal/handlers/responses/handler_session_test.go:18`, `backend-go/internal/handlers/responses/handler_session_test.go:53`, `backend-go/internal/handlers/responses/handler_session_test.go:57`).

### What to copy into Messages -> Claude raw stream fan-out pilot

- Copy the side-channel principle: client bytes must come from the raw upstream stream branch; usage/metrics parsing must happen on a separate branch/buffer and must never mutate client events.
- Copy the same-format gate via `passthrough.Decide`/`AllowsRawResponsePassthrough` rather than adding channel config switches or `serviceType == "claude"` shortcuts. The backend spec calls `passthrough.Decide(...)` the correct contract.
- Copy the attempt-scoped behavior: raw stream state must be created after the selected upstream/key/BaseURL attempt is known, and must be canceled/closed when the attempt fails, is retried, or the client disconnects.
- Copy the metrics return contract: return `*types.Usage` from the success handler so `TryUpstreamWithAllKeys` can finalize the already-connected pending request with success and token counts.
- Copy bounded side-channel buffering or bounded fan-out queues. Responses caps the raw metrics buffer at 1 MiB; a Messages fan-out should have an explicit cap/backpressure/cancel policy so usage parsing cannot block raw delivery forever.
- Copy tests that assert the exact raw SSE body, including comments, `event:`, `id:`, compact `data:`, `retry:`, blank lines, and final delimiters. For Messages -> Claude, use Claude-native SSE event names and ensure the assertion is byte-for-byte.
- Copy metrics assertions from Responses tests and Messages low-quality metrics tests: the response body can remain low-quality/raw while internal metrics are normalized or estimated.

### What not to copy into Messages -> Claude raw stream fan-out pilot

- Do not copy Responses stream conversion/preflight scanner logic into the raw branch. The normal Responses stream path normalizes field spacing and rebuilds events through `line + "\n"`; that is correct for conversion but not for raw byte preservation (`backend-go/internal/handlers/responses/handler.go:592`, `backend-go/internal/handlers/responses/handler.go:608`, `backend-go/internal/handlers/responses/handler.go:797`).
- Do not patch, inject, or rewrite usage events on the raw client branch. Responses raw stream only parses after copying; the non-raw path may inject or patch completed events, but that is explicitly not raw passthrough (`backend-go/internal/handlers/responses/handler.go:828`, `backend-go/internal/handlers/responses/handler.go:847`).
- Do not reuse Responses usage parser for Claude Messages events. Responses usage is under `response.completed.response.usage`; Messages/Claude usage can appear at top-level usage or `message.usage`, and existing common helpers already parse that (`backend-go/internal/handlers/common/stream.go:1248`, `backend-go/internal/handlers/common/stream.go:1255`).
- Do not rely on the existing Messages direct passthrough path as proof of raw upstream byte preservation. It forwards provider-produced `event` strings from `HandleStreamResponse`, not raw `resp.Body` chunks, so the provider may already have parsed/reformatted SSE before the handler writes it (`backend-go/internal/handlers/common/stream.go:832`, `backend-go/internal/handlers/common/stream.go:883`, `backend-go/internal/handlers/common/stream.go:897`).
- Do not let parsing goroutines outlive the attempt. The PRD requires attempt-scoped cleanup; the pilot should close provider response bodies and cancel fan-out before trying the next key/channel.
- Do not add backward-compatible passthrough config fields. The PRD and backend spec both state the removed passthrough switches must not be reintroduced.
- Do not port AxonHub's full orchestrator/pipeline. The PRD asks for a minimal raw stream fan-out pilot only.

## Code Patterns

- Same-format decision:
  - `passthrough.AllowsRawResponsePassthrough(path, upstream)` checks inbound/outbound API formats and returns true only on match (`backend-go/internal/passthrough/passthrough.go:91`).
  - Responses handler sets an attempt-scoped context flag before `handleSuccess` (`backend-go/internal/handlers/responses/handler.go:142`, `backend-go/internal/handlers/responses/handler.go:230`).
- Raw JSON side-channel:
  - Parse raw body into a typed copy for usage/session, write the original bytes to the client (`backend-go/internal/handlers/responses/handler.go:300`, `backend-go/internal/handlers/responses/handler.go:320`).
- Raw SSE side-channel:
  - Read raw chunks, append bounded metrics buffer, write the same chunk to client, flush, then parse usage after EOF (`backend-go/internal/handlers/responses/handler.go:937`, `backend-go/internal/handlers/responses/handler.go:944`, `backend-go/internal/handlers/responses/handler.go:952`, `backend-go/internal/handlers/responses/handler.go:967`).
- Metrics finalization:
  - Protocol success handlers return usage; `TryUpstreamWithAllKeys` finalizes success with that usage (`backend-go/internal/handlers/common/upstream_failover.go:499`).
  - Metrics extracts normalized token fields from `types.Usage` (`backend-go/internal/metrics/channel_metrics.go:667`, `backend-go/internal/metrics/channel_metrics.go:1020`).
- Existing Messages passthrough pattern:
  - Direct passthrough currently forwards provider events and collects usage side-channel, but it is not raw `resp.Body` fan-out (`backend-go/internal/handlers/common/stream.go:879`, `backend-go/internal/handlers/common/stream.go:883`, `backend-go/internal/handlers/common/stream.go:893`, `backend-go/internal/handlers/common/stream.go:1248`).

## External References

- None. This research is based on the current repository code, tests, PRD, and Trellis backend specs only.

## Related Specs

- `.trellis/spec/backend/index.md` - Backend spec index and pre-development checklist.
- `.trellis/spec/backend/directory-structure.md` - Places handler logic under protocol packages and shared handler helpers under `internal/handlers/common`.
- `.trellis/spec/backend/error-handling.md` - Uses sentinel errors only for shared control flow and maps errors at handler boundaries.
- `.trellis/spec/backend/logging-guidelines.md` - Requires bracketed component tags and avoiding noisy/raw diagnostics outside intentional modes.
- `.trellis/spec/backend/quality-guidelines.md` - Contains the Proxy Handler Contract and Passthrough Decision Contracts directly relevant to this task.
- `.trellis/spec/guides/index.md` - Flags cross-layer thinking because this work spans handlers, providers, passthrough decisions, metrics, and session/accounting.

## Caveats / Not Found

- No production code was edited.
- `python3 ./.trellis/scripts/task.py current --source` returned no active task in this session, so this research used the task directory explicitly requested by the user: `.trellis/tasks/05-06-axonhub-passthrough-followup/`.
- Raw Responses stream passthrough currently does not update Responses session state; only non-stream success calls `recordResponsesSession`.
- The raw Responses stream side-channel parses only the first 1 MiB of stream bytes for metrics. Very large streams whose usage event appears after that cap may return zero usage to metrics.
- The raw Responses stream helper returns write/read errors directly. The shared failover layer handles those through its existing success-handler error paths, but raw bytes may already have been sent to the client, so not every failure is retry-safe.
- Existing Messages direct passthrough tests prove provider-event string preservation, not raw upstream byte preservation from `resp.Body`. The Messages -> Claude pilot needs new tests at the raw upstream body/fan-out boundary.
