# Research: raw stream expansion for Chat/OpenAI and Gemini

- Query: Research raw stream expansion for active task `.trellis/tasks/05-06-axonhub-passthrough-followup`, focusing on Chat/OpenAI and Gemini native stream handlers/providers/tests, Responses raw stream comparison, and common raw stream helpers.
- Scope: internal
- Date: 2026-05-06

## Findings

### Files found

- `.trellis/workflow.md` - Trellis workflow requires research findings to be persisted under the task `research/` directory.
- `.trellis/spec/backend/index.md` - backend spec entry point; pre-development checklist points to directory, error handling, logging, and quality guidelines.
- `.trellis/spec/backend/quality-guidelines.md` - includes the current Passthrough Decision Contract and proxy handler contracts.
- `.trellis/tasks/05-06-axonhub-passthrough-followup/prd.md` - current task says final closeout still needs same-format Chat/OpenAI and Gemini native stream byte preservation while Responses raw stream remains unchanged and covered.
- `backend-go/internal/passthrough/passthrough.go` - central API format decision helper; currently recognizes Messages, Responses, and Chat paths, but not Gemini native model paths.
- `backend-go/internal/handlers/common/upstream_failover.go` - shared attempt/key/baseURL retry loop, selected API key context, request preprocessing, metrics finalization, and failover classification.
- `backend-go/internal/handlers/common/stream.go` - Messages stream preflight, raw fan-out pilot, usage collection, cancellation cleanup, and stream error helpers.
- `backend-go/internal/handlers/chat/handler.go` - Chat endpoint handler, OpenAI-compatible request builder, stream response passthrough, and Claude-to-Chat conversion stream path.
- `backend-go/internal/handlers/gemini/handler.go` - Gemini endpoint handler, native/cross-format request builder, non-stream response conversion, and stream dispatch.
- `backend-go/internal/handlers/gemini/stream.go` - Gemini stream conversion/passthrough implementations.
- `backend-go/internal/handlers/responses/handler.go` - Responses handler; contains existing raw stream passthrough comparison point.
- `backend-go/internal/providers/openai.go` - Messages-to-OpenAI provider stream converter; normalizes OpenAI SSE into Claude Messages events.
- `backend-go/internal/providers/gemini.go` - Messages-to-Gemini provider stream converter; normalizes Gemini SSE into Claude Messages events and usage.
- `backend-go/internal/providers/responses.go` - Messages-to-Responses provider stream converter; normalizes Responses SSE into Claude Messages events and usage.
- `backend-go/internal/providers/provider.go` - provider interface plus shared stream cancellation and SSE field normalization helpers.
- `backend-go/internal/handlers/chat/*_test.go` - Chat has request/header/non-stream matrix tests, but no stream raw preservation tests found.
- `backend-go/internal/handlers/gemini/*_test.go` - Gemini has request/header/non-stream matrix tests, but no handler-level native stream raw preservation tests found.
- `backend-go/internal/handlers/responses/handler_response_matrix_test.go` - Responses raw non-stream and raw stream preservation/metrics tests.
- `backend-go/internal/handlers/messages/handler_response_matrix_test.go` - completed Messages -> Claude raw stream fan-out pilot tests for byte preservation, metrics, cleanup before failover, and cross-format non-raw behavior.
- `backend-go/internal/providers/gemini_stream_test.go`, `backend-go/internal/providers/responses_stream_test.go`, `backend-go/internal/providers/sse_normalization_test.go`, `backend-go/internal/providers/stream_cancel_test.go` - provider-level normalized stream usage/cancel/SSE parsing coverage.

### Related specs and task requirements

- `.trellis/spec/backend/quality-guidelines.md` says raw body/response passthrough is allowed only when inbound and outbound API formats match. It explicitly requires raw Responses stream bytes to be unchanged and usage to be side-channel only, and says raw stream preflight must complete before response headers/body are written.
- The same spec also requires raw stream fan-out cleanup to be attempt-scoped: cancel attempt, close provider response body, drain branches, and wait for the fan-out goroutine before the next key/channel attempt.
- The current PRD says `/v1/messages -> claude` and `/v1/responses -> responses` are already complete, while `/v1/chat/completions -> openai` and Gemini native stream routes still need raw byte preservation.
- The PRD also says cross-format streams must remain on existing conversion paths and the final increment should prefer shared raw stream helpers over per-protocol ad hoc logic.

### Current central passthrough decision state

- `passthrough.Decide(...)` derives inbound format from path and outbound format from `upstream.ServiceType`, then enables `RawResponse` only when formats match (`backend-go/internal/passthrough/passthrough.go:30`, `backend-go/internal/passthrough/passthrough.go:39`, `backend-go/internal/passthrough/passthrough.go:44`).
- `InboundFormatFromPath` recognizes `/v1/messages`, `/v1/responses`, and `/v1/chat/completions` only (`backend-go/internal/passthrough/passthrough.go:49`). It defines `APIFormatGeminiContents`, but no Gemini path currently maps to it (`backend-go/internal/passthrough/passthrough.go:19`, `backend-go/internal/passthrough/passthrough.go:49`).
- `OutboundFormatForService("openai")` maps to `APIFormatOpenAIChat`, and `"gemini"` maps to `APIFormatGeminiContents` (`backend-go/internal/passthrough/passthrough.go:63`).
- `common.ShouldDirectPassthroughForRequest(path, upstream, apiKey)` is already the shared wrapper over `passthrough.Decide(...).RawResponse` (`backend-go/internal/handlers/common/upstream_failover.go:54`).
- `passthroughPathForChannelKind` maps `ChannelKindChat` to `/v1/chat/completions`, but has no `ChannelKindGemini` entry (`backend-go/internal/handlers/common/upstream_failover.go:76`).
- Existing tests cover direct passthrough only for Messages/Claude and Responses/Responses; Chat/OpenAI and Gemini/Gemini are not covered in `TestShouldDirectPassthroughForRequestRequiresProtocolConsistency` (`backend-go/internal/handlers/common/upstream_failover_passthrough_test.go:52`).

### Shared failover, metrics, and cancel flow

- Both Chat and Gemini handlers call `common.TryUpstreamWithAllKeys(...)` for single-channel and multi-channel modes (`backend-go/internal/handlers/chat/handler.go:121`, `backend-go/internal/handlers/chat/handler.go:205`, `backend-go/internal/handlers/gemini/handler.go:142`, `backend-go/internal/handlers/gemini/handler.go:233`).
- `TryUpstreamWithAllKeys` stores the selected key in Gin context before request construction (`backend-go/internal/handlers/common/upstream_failover.go:231`, `backend-go/internal/handlers/common/upstream_failover.go:234`).
- Success handling is responsible for returning `*types.Usage`; the shared loop records success metrics using that usage (`backend-go/internal/handlers/common/upstream_failover.go:418`, `backend-go/internal/handlers/common/upstream_failover.go:499`).
- `context.Canceled` is treated as client-side cancellation, finalizes as client cancel, stops failover, and completes the channel log as cancelled (`backend-go/internal/handlers/common/upstream_failover.go:23`, `backend-go/internal/handlers/common/upstream_failover.go:422`).
- `ErrEmptyStreamResponse`, `ErrInvalidResponseBody`, `ErrCooldownKey`, and `ErrBlacklistKey` returned before response commit continue failover with the expected cooldown/blacklist behavior (`backend-go/internal/handlers/common/upstream_failover.go:428`, `backend-go/internal/handlers/common/upstream_failover.go:441`, `backend-go/internal/handlers/common/upstream_failover.go:457`).

### Existing common raw stream helper pattern

- The Messages pilot has a raw fan-out helper that reads from the upstream `resp.Body` with `bufio.Reader.ReadBytes('\n')`, groups bytes into raw SSE events at blank lines, preserves each event as raw `[]byte`, and separately stores text for preflight/metrics (`backend-go/internal/handlers/common/stream.go:502`, `backend-go/internal/handlers/common/stream.go:517`, `backend-go/internal/handlers/common/stream.go:521`).
- Raw event size and raw preflight buffers are bounded at 1MB (`backend-go/internal/handlers/common/stream.go:66`).
- `preflightRawStreamEvents` buffers raw events while using normalized text for empty-stream detection and stream failover action detection, before any headers are sent (`backend-go/internal/handlers/common/stream.go:170`, `backend-go/internal/handlers/common/stream.go:196`, `backend-go/internal/handlers/common/stream.go:206`).
- `cleanupRawStreamFanout` cancels the attempt, drains raw channels, and waits up to five seconds for fan-out completion before allowing the caller to proceed (`backend-go/internal/handlers/common/stream.go:579`).
- `handleRawMessagesStreamPassthrough` writes the buffered and subsequent raw event bytes exactly to the client while collecting usage from event text and returning metrics usage at end (`backend-go/internal/handlers/common/stream.go:1196`, `backend-go/internal/handlers/common/stream.go:1246`, `backend-go/internal/handlers/common/stream.go:1252`, `backend-go/internal/handlers/common/stream.go:1265`).
- `finalizePassthroughStreamUsage` converts collected side-channel usage into metrics shape without mutating events sent to the client (`backend-go/internal/handlers/common/stream.go:1309`).
- `collectPassthroughStreamUsage` uses `CheckEventUsageStatus`, which detects both top-level `usage` and `message.usage`, and supports Claude/OpenAI field aliases such as `input_tokens`/`prompt_tokens` and `output_tokens`/`completion_tokens` (`backend-go/internal/handlers/common/stream.go:1392`, `backend-go/internal/handlers/common/stream.go:1465`, `backend-go/internal/handlers/common/stream.go:1599`).

### Responses raw stream comparison

- Responses sets a context flag per attempt using `passthrough.AllowsRawResponsePassthrough("/v1/responses", upstreamCopy)` before `handleSuccess` (`backend-go/internal/handlers/responses/handler.go:142`, `backend-go/internal/handlers/responses/handler.go:230`).
- Non-stream raw Responses passthrough parses the body for usage/session accounting but returns the original JSON bytes unchanged (`backend-go/internal/handlers/responses/handler.go:300`, `backend-go/internal/handlers/responses/handler.go:311`, `backend-go/internal/handlers/responses/handler.go:315`, `backend-go/internal/handlers/responses/handler.go:320`).
- Stream raw Responses passthrough immediately forwards upstream headers/status, reads raw chunks from `resp.Body`, writes the exact chunks to the client, and keeps a bounded 1MB metrics buffer (`backend-go/internal/handlers/responses/handler.go:929`, `backend-go/internal/handlers/responses/handler.go:940`, `backend-go/internal/handlers/responses/handler.go:944`, `backend-go/internal/handlers/responses/handler.go:952`).
- Raw Responses stream usage is parsed after stream completion from the metrics buffer via `collectRawResponsesStreamUsage`, which splits events using normalized `\n\n` internally but does not affect bytes already sent (`backend-go/internal/handlers/responses/handler.go:967`, `backend-go/internal/handlers/responses/handler.go:975`, `backend-go/internal/handlers/responses/handler.go:998`).
- Tests already assert raw Responses stream preserves comments, `id:`, `event:`, `retry:`, no-space `data:`, and metrics usage (`backend-go/internal/handlers/responses/handler_response_matrix_test.go:157`).
- Important comparison caveat: Responses raw stream currently does not run the same bounded preflight/failover fan-out as Messages. The PRD says Responses raw stream should remain unchanged, so this research treats it as comparison/baseline, not as a requested implementation target.

### Chat/OpenAI current flow

- Chat parses body and `stream` at handler entry, then routes through `TryUpstreamWithAllKeys` (`backend-go/internal/handlers/chat/handler.go:37`, `backend-go/internal/handlers/chat/handler.go:72`, `backend-go/internal/handlers/chat/handler.go:81`).
- For `ServiceType` `openai`, `responses`, or empty, `buildProviderRequest` unmarshals the client body, patches `model`, optional `reasoning`, `text`, and `service_tier`, marshals JSON, and sends to `/v1/chat/completions` (`backend-go/internal/handlers/chat/handler.go:255`, `backend-go/internal/handlers/chat/handler.go:261`, `backend-go/internal/handlers/chat/handler.go:272`, `backend-go/internal/handlers/chat/handler.go:276`).
- For `ServiceType` `claude`, Chat converts OpenAI Chat to Claude Messages and sends `/v1/messages`; this is cross-format and must remain conversion (`backend-go/internal/handlers/chat/handler.go:282`).
- For `ServiceType` `gemini` and default, Chat sends an OpenAI-compatible `/v1/chat/completions` request while minimally patching `model` when needed (`backend-go/internal/handlers/chat/handler.go:298`, `backend-go/internal/handlers/chat/handler.go:313`).
- Request headers use shared upstream header preparation, then `Content-Type`, custom headers, and final auth (`backend-go/internal/handlers/chat/handler.go:347`, `backend-go/internal/handlers/chat/handler.go:350`, `backend-go/internal/handlers/chat/handler.go:352`, `backend-go/internal/handlers/chat/handler.go:354`).
- Stream success sets SSE headers before reading from upstream, dispatches `claude` through `streamClaudeToChat`, and all other service types through `streamPassthrough` (`backend-go/internal/handlers/chat/handler.go:696`, `backend-go/internal/handlers/chat/handler.go:705`, `backend-go/internal/handlers/chat/handler.go:717`).
- `streamPassthrough` reads arbitrary raw chunks from `resp.Body`, parses complete `data: ` lines for OpenAI `usage`, writes the original `buf[:n]` bytes to the client, and flushes (`backend-go/internal/handlers/chat/handler.go:732`, `backend-go/internal/handlers/chat/handler.go:741`, `backend-go/internal/handlers/chat/handler.go:750`, `backend-go/internal/handlers/chat/handler.go:772`).
- Raw byte preservation status: chunk bytes are written unchanged in the happy path, so no scanner whitespace rewrite occurs for Chat passthrough. However, there is no regression test proving this, and the usage side-channel only detects `data: ` with a space, not `data:` without a space (`backend-go/internal/handlers/chat/handler.go:752`).

### Chat/OpenAI raw preservation gaps

- Headers are committed before any stream preflight, so stream error/cooldown/blacklist/empty response cannot safely return to the shared failover loop once `handleStreamSuccess` starts (`backend-go/internal/handlers/chat/handler.go:705`).
- Same-format raw response decision is not centralized: `streamPassthrough` applies to all non-Claude Chat upstream types, including `gemini`, `responses`, empty, and default, rather than checking `common.ShouldDirectPassthroughForRequest("/v1/chat/completions", upstream, selectedKey)` (`backend-go/internal/handlers/chat/handler.go:717`).
- `streamPassthrough` ignores client request cancellation and does not close the upstream body early on cancel; it also ignores write errors from `c.Writer.Write(...)` and treats all read errors as normal loop termination (`backend-go/internal/handlers/chat/handler.go:741`, `backend-go/internal/handlers/chat/handler.go:772`, `backend-go/internal/handlers/chat/handler.go:777`).
- Usage extraction is OpenAI-only and only detects complete `data: ` lines. It misses no-space `data:{...}` lines and does not share the more tolerant usage parser already used by common raw Messages helpers (`backend-go/internal/handlers/chat/handler.go:750`, `backend-go/internal/handlers/common/stream.go:1392`).
- Because the handler returns usage after writing, metrics success still works when usage is parsed, but missing usage will record success with nil/zero usage through the shared metrics finalizer (`backend-go/internal/handlers/common/upstream_failover.go:499`).

### Gemini native current flow

- Gemini parses `types.GeminiRequest`, derives `isStream` from the URL containing `streamGenerateContent`, and routes through `TryUpstreamWithAllKeys` (`backend-go/internal/handlers/gemini/handler.go:45`, `backend-go/internal/handlers/gemini/handler.go:77`, `backend-go/internal/handlers/gemini/handler.go:87`).
- Native Gemini upstream request handling applies model mapping, optional thought signature patching/stripping, then sends `/v1beta/models/{model}:streamGenerateContent?alt=sse` for streams (`backend-go/internal/handlers/gemini/handler.go:328`, `backend-go/internal/handlers/gemini/handler.go:330`, `backend-go/internal/handlers/gemini/handler.go:349`, `backend-go/internal/handlers/gemini/handler.go:353`).
- Cross-format Gemini handler branches convert to Claude, OpenAI, or Responses and must remain conversion paths (`backend-go/internal/handlers/gemini/handler.go:358`, `backend-go/internal/handlers/gemini/handler.go:370`, `backend-go/internal/handlers/gemini/handler.go:382`).
- Stream dispatch sets SSE headers before reading and sends native `ServiceType == "gemini"` through `streamGeminiToGemini`; cross-format streams go through conversion functions (`backend-go/internal/handlers/gemini/stream.go:18`, `backend-go/internal/handlers/gemini/stream.go:27`, `backend-go/internal/handlers/gemini/stream.go:39`).
- `streamGeminiToGemini` uses `bufio.Scanner`, strips line delimiters, re-emits each line with `\n`, parses `usageMetadata`, and returns Chat-style metrics usage (`backend-go/internal/handlers/gemini/stream.go:61`, `backend-go/internal/handlers/gemini/stream.go:68`, `backend-go/internal/handlers/gemini/stream.go:73`, `backend-go/internal/handlers/gemini/stream.go:91`).

### Gemini raw preservation gaps

- Native Gemini stream is not byte-preserving: `bufio.Scanner.Text()` removes line endings, the handler re-adds `\n`, so CRLF, final newline shape, and exact event byte framing are not preserved (`backend-go/internal/handlers/gemini/stream.go:68`, `backend-go/internal/handlers/gemini/stream.go:73`, `backend-go/internal/handlers/gemini/stream.go:91`).
- Headers are committed before stream preflight, so stream auth/quota/rate-limit errors embedded in SSE cannot safely trigger key cooldown/blacklist failover before response commit (`backend-go/internal/handlers/gemini/stream.go:27`).
- Gemini native path is not represented in `passthrough.InboundFormatFromPath`, so a centralized same-format decision cannot currently identify Gemini/Gemini native routes (`backend-go/internal/passthrough/passthrough.go:49`).
- Existing common `preflightRawStreamEvents` is Claude/Messages semantics-oriented: empty detection uses Claude event helpers such as `HasClaudeSemanticContent`, `IsMessageStopEvent`, and `ExtractTextFromEvent`. It is not directly correct for Gemini native events without a protocol-specific text/complete/usage detector (`backend-go/internal/handlers/common/stream.go:216`, `backend-go/internal/handlers/common/stream.go:221`, `backend-go/internal/handlers/common/stream.go:232`).
- Provider-level Gemini conversion already handles `usageMetadata`, cached content subtraction, and monotonic output tokens for Messages conversion (`backend-go/internal/providers/gemini.go:411`, `backend-go/internal/providers/gemini.go:418`, `backend-go/internal/providers/gemini.go:432`). The native handler has simpler usage parsing and should preserve that behavior for metrics (`backend-go/internal/handlers/gemini/stream.go:80`).

### Provider-level stream converters are not raw passthrough

- `OpenAIProvider.HandleStreamResponse` normalizes OpenAI Chat SSE into Claude Messages SSE events, using `normalizeSSEFieldLine`, `strings.TrimSpace`, JSON parsing, and synthesized `message_start`/`content_block_*`/`message_stop` events (`backend-go/internal/providers/openai.go:371`, `backend-go/internal/providers/openai.go:404`, `backend-go/internal/providers/openai.go:415`, `backend-go/internal/providers/openai.go:448`).
- `GeminiProvider.HandleStreamResponse` normalizes Gemini SSE into Claude Messages SSE events and emits final `message_delta` usage from `usageMetadata` (`backend-go/internal/providers/gemini.go:360`, `backend-go/internal/providers/gemini.go:393`, `backend-go/internal/providers/gemini.go:411`, `backend-go/internal/providers/gemini.go:590`).
- `ResponsesProvider.HandleStreamResponse` normalizes Responses SSE into Claude Messages SSE events and usage (`backend-go/internal/providers/responses.go:468`, `backend-go/internal/providers/responses.go:511`, `backend-go/internal/providers/responses.go:626`, `backend-go/internal/providers/responses.go:687`).
- These provider converters should continue to be used for cross-format Messages paths. They are useful for side-channel parsing concepts and cancellation helpers, but not for raw byte passthrough because they intentionally rewrite event structure and field spacing.

### Current test coverage

- Chat handler tests cover request building, header override, non-stream OpenAI/Claude conversion, and non-stream passthrough for Gemini/Responses, but no stream raw preservation/metrics/cancel/failover tests were found (`backend-go/internal/handlers/chat/handler_response_matrix_test.go:38`, `backend-go/internal/handlers/chat/handler_response_matrix_test.go:71`, `backend-go/internal/handlers/chat/handler_response_matrix_test.go:167`).
- Gemini handler tests cover request building, header override, non-stream all-four upstream matrix, and function calls, but no native stream raw preservation/metrics/cancel/failover tests were found (`backend-go/internal/handlers/gemini/handler_response_matrix_test.go:38`, `backend-go/internal/handlers/gemini/handler_response_matrix_test.go:72`).
- Responses already has raw stream preservation and metrics tests (`backend-go/internal/handlers/responses/handler_response_matrix_test.go:157`, `backend-go/internal/handlers/responses/handler_response_matrix_test.go:207`).
- Messages already has raw stream pilot tests for exact raw SSE preservation, metrics, cleanup before failover, and cross-format stream non-raw behavior (`backend-go/internal/handlers/messages/handler_response_matrix_test.go:139`, `backend-go/internal/handlers/messages/handler_response_matrix_test.go:191`, `backend-go/internal/handlers/messages/handler_response_matrix_test.go:248`).
- Provider tests cover normalized stream parsing and cancellation primitives, not handler raw passthrough (`backend-go/internal/providers/gemini_stream_test.go:41`, `backend-go/internal/providers/sse_normalization_test.go:68`, `backend-go/internal/providers/stream_cancel_test.go:9`).

### Suggested minimal implementation

1. Add/extend a shared raw SSE fan-out helper under `handlers/common` rather than duplicating scanner loops in Chat/Gemini. The existing unexported Messages helper already handles raw event byte preservation, bounded event/preflight buffers, cancellation, body close, and cleanup wait. Minimal options:
   - Keep existing Messages helper and add exported wrappers with protocol-specific preflight/usage callbacks.
   - Or add narrow `HandleRawStreamPassthrough(...)` helpers for OpenAI Chat and Gemini native that reuse the same `startRawStreamFanout`/cleanup internals.
2. For Chat/OpenAI:
   - In `handleSuccess`, pass `upstreamCopy`/selected key or a raw decision into stream handling.
   - Use raw fan-out only when `common.ShouldDirectPassthroughForRequest(c.Request.URL.Path, upstreamCopy, apiKey)` is true, which should mean `/v1/chat/completions -> openai`.
   - Preflight before headers using OpenAI Chat semantics: valid content if a chunk has `choices[].delta.content`, tool call delta, non-empty finish, or usage-only terminal chunk; error/cooldown/blacklist if `error` or configured failover rule matches event payload.
   - After preflight, forward upstream response headers/status consistently, write buffered raw bytes and subsequent raw bytes unchanged, and collect metrics usage from OpenAI `usage.prompt_tokens`/`completion_tokens` without mutating SSE.
   - Keep Claude stream conversion and any cross-format Chat streams on existing behavior unless the task explicitly reclassifies them.
3. For Gemini native:
   - Add Gemini path recognition to `passthrough.InboundFormatFromPath` for model routes ending in `:generateContent` and `:streamGenerateContent`, including `/v1beta/models/...`, `/v1/models/...`, and route-prefixed variants if the registered routes support them.
   - Add `ChannelKindGemini` to `passthroughPathForChannelKind` or avoid this helper for URL-based handler decisions and call `ShouldDirectPassthroughForRequest(c.Request.URL.Path, upstreamCopy, apiKey)` directly.
   - Use raw fan-out only when inbound Gemini native format matches outbound `ServiceType == "gemini"`.
   - Preflight before headers using Gemini semantics: valid content if any candidate part has `text`, `functionCall`, semantic part, `finishReason`, or `usageMetadata` with a completed/meaningful event depending on empty-stream policy.
   - Replace `streamGeminiToGemini` scanner passthrough with raw byte write after preflight. Usage collection should parse each event text for `usageMetadata`, subtract `cachedContentTokenCount`, clamp input/output at zero, and keep latest input plus monotonic output like the provider conversion path.
4. For Responses:
   - Do not change raw Responses stream in this increment unless tests reveal a regression, because the PRD explicitly says Responses raw stream remains unchanged and covered.
5. Preserve shared failover behavior:
   - Raw stream preflight must return `ErrCooldownKey`, `ErrBlacklistKey`, `ErrEmptyStreamResponse`, or `ErrInvalidResponseBody` before any write. This lets `TryUpstreamWithAllKeys` apply cooldown/blacklist/failover correctly.
   - Client disconnect/write cancel must return `context.Canceled` so `TryUpstreamWithAllKeys` finalizes as client cancel and stops failover.
   - On any retryable raw preflight failure, cleanup must cancel the attempt and wait for upstream body/fan-out completion before the next key/channel attempt begins, matching the Messages pilot.

### Suggested tests

- `backend-go/internal/passthrough/passthrough_test.go`
  - Add Chat/OpenAI same-format: `/v1/chat/completions` + `ServiceType: "openai"` returns `RawResponse == true`.
  - Add Chat cross-format: `/v1/chat/completions` + `ServiceType: "claude"`/`"gemini"`/`"responses"` returns `RawResponse == false` unless the project intentionally treats those as OpenAI-compatible Chat same-format.
  - Add Gemini native same-format routes: `/v1beta/models/gemini-2.0-flash:streamGenerateContent` + `ServiceType: "gemini"` returns `RawResponse == true`.
  - Add Gemini cross-format routes to Claude/OpenAI/Responses return `RawResponse == false`.
- `backend-go/internal/handlers/chat/handler_response_matrix_test.go`
  - Add `TestChatHandler_StreamRawPassthroughPreservesOpenAIUpstreamSSEBytesAndMetrics` using raw SSE with comment, `id:`, `event:`, `retry:`, no-space `data:{...}`, `[DONE]`, and terminal OpenAI usage. Assert `w.Body.String() == rawStream` and metrics input/output match.
  - Add `TestChatHandler_CrossFormatStreamDoesNotUseRawPassthrough` for `ServiceType: "claude"` with raw Claude SSE and assert the client receives OpenAI Chat chunks, not upstream raw bytes.
  - Add failover/cancel regression equivalent to Messages: first key emits preflight rate-limit/error SSE and blocks until context close; second key should start only after first attempt closes; final body should be second stream.
- `backend-go/internal/handlers/gemini/handler_response_matrix_test.go`
  - Add a stream request helper for `/v1beta/models/{model}:streamGenerateContent`.
  - Add `TestGeminiHandler_StreamRawPassthroughPreservesNativeSSEBytesAndMetrics` with CRLF or mixed field formatting, comment, `id:`, `event:`, `retry:`, no-space `data:{...}`, and `usageMetadata`. Assert exact bytes, including CRLF if chosen, and metrics input/output with cached-content subtraction.
  - Add `TestGeminiHandler_CrossFormatStreamDoesNotUseRawPassthrough` for `ServiceType: "openai"` or `"claude"` and assert upstream raw OpenAI/Claude SSE is converted, not byte-preserved.
  - Add failover/cancel cleanup test equivalent to Messages for preflight rate-limit/error SSE before headers.
- `backend-go/internal/handlers/common/stream_test.go`
  - Add unit tests around any new raw preflight protocol callbacks: no-space `data:`, comments/id/retry preservation, bounded oversize event/preflight errors, OpenAI error/rate-limit classification, Gemini error/rate-limit classification, and cancel cleanup.
- Verification after implementation:
  - `cd backend-go && go test ./internal/passthrough ./internal/handlers/common ./internal/handlers/chat ./internal/handlers/gemini ./internal/handlers/responses ./internal/providers -count=1`
  - `cd backend-go && go test ./...`
  - `git diff --check`

## Caveats / Not Found

- `task.py current --source` returned no active task in this session, but the user provided the explicit task directory and output file path. This research was therefore persisted to the requested task path only.
- I did not edit production code or tests.
- No external references were needed; this was an internal codebase research pass. Provider/API behavior was inferred from existing project handlers, providers, tests, and Trellis specs.
- I did not run backend tests; this task was research-only.
- Some source comments rendered with mojibake in PowerShell output, but code structure and line references were still readable enough for the above citations.
- The existing Responses raw stream implementation writes bytes unchanged but commits headers before parsing. The current PRD marks Responses raw stream as already complete and asks it to remain unchanged, so this file calls that out as a comparison caveat rather than an implementation target.
- Gemini native same-format cannot currently be decided through `passthrough.Decide(...)` because Gemini URL formats are not recognized in `InboundFormatFromPath`.
