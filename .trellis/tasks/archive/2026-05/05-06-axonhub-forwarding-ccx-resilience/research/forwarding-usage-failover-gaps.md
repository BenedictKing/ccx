# Research: forwarding usage failover gaps

- Query: Research current task `05-06-axonhub-forwarding-ccx-resilience`; compare existing forwarding / usage / failover integration against PRD MVP.
- Scope: internal
- Date: 2026-05-07

## Findings

### Task and PRD

- Target task: `.trellis/tasks/05-06-axonhub-forwarding-ccx-resilience`.
- PRD goal: combine AxonHub-style forwarding/usage statistics with CCX control-plane resilience.
- PRD MVP: same-format raw passthrough plus AxonHub-style request/token/cache usage stats; preserve CCX scheduler, failover, circuit breaker, blacklist, cooldown, channel logs, and metrics finalize.
- Active task caveat: `python3 ./.trellis/scripts/task.py current --source` returned no current task in this Codex session, and `task.py start .trellis/tasks/05-06-axonhub-forwarding-ccx-resilience` exited non-zero without output. Research below uses the user-provided task path directly.

### Files found

- `backend-go/internal/handlers/common/upstream_failover.go`: shared per-channel/BaseURL/key retry loop, circuit checks, channel log lifecycle, failover classification, metrics finalize, and AxonHub forwarding usage classification/recording.
- `backend-go/internal/forwarding/builder.go`: data-plane request builder for method, URL, body, safe inbound headers, custom headers, auth, and raw strategy flags.
- `backend-go/internal/forwarding/url.go`: endpoint URL and Gemini native URL construction helpers.
- `backend-go/internal/passthrough/passthrough.go`: central same-format vs cross-format passthrough decision.
- `backend-go/internal/handlers/common/request.go`: JSON passthrough helper that forwards bytes unchanged while tee-decoding usage for metrics.
- `backend-go/internal/handlers/common/stream.go`: shared raw stream passthrough, preflight, side-channel usage collection, and metrics usage normalization.
- `backend-go/internal/handlers/messages/handler.go`: Messages handler using shared failover and provider request builders; non-stream same-format Claude passthrough uses metrics-only usage parsing.
- `backend-go/internal/handlers/responses/handler.go`: Responses handler raw non-stream and raw stream passthrough, usage collection, and conversion paths.
- `backend-go/internal/handlers/chat/handler.go`: Chat handler request build paths, raw OpenAI stream passthrough, non-stream usage extraction, and conversion paths.
- `backend-go/internal/handlers/gemini/handler.go`: Gemini handler native same-format request build, non-stream raw response preservation, usage extraction, and conversion paths.
- `backend-go/internal/providers/claude.go`: Claude provider request path strips route prefix, builds upstream URL, patches top-level model, and sets raw passthrough flags.
- `backend-go/internal/providers/responses.go`: Responses provider same-format and conversion request construction through forwarding builder.
- `backend-go/internal/providers/openai.go`: Messages-to-OpenAI provider conversion request construction through forwarding builder.
- `backend-go/internal/metrics/channel_metrics.go`: ordinary request metrics, circuit state, token/cache accounting, and AxonHub forwarding aggregation.
- `backend-go/internal/metrics/persistence.go`: persisted request record shape for normal metrics.
- `backend-go/internal/metrics/sqlite_store.go`: SQLite persistence for request records and circuit state.
- `backend-go/internal/handlers/channel_metrics_handler.go`: admin metrics response surface including `axonHubForwarding`.
- Existing tests: `backend-go/internal/handlers/common/upstream_failover_passthrough_test.go`, `backend-go/internal/metrics/channel_metrics_axonhub_test.go`, protocol `handler_response_matrix_test.go` files, `backend-go/internal/handlers/common/request_test.go`, `backend-go/internal/handlers/common/stream_test.go`, `backend-go/internal/forwarding/builder_test.go`, `backend-go/internal/passthrough/passthrough_test.go`.

### Code patterns

- CCX control-plane center: `common.TryUpstreamWithAllKeys` accepts `nextAPIKey`, `buildRequest`, `handleSuccess`, URL success/failure callbacks, scheduler/metrics/log stores, and owns retry/finalize flow (`backend-go/internal/handlers/common/upstream_failover.go:193`).
- Circuit open keys are skipped; half-open probes require `TryAcquireProbe` and are released on defer/success (`backend-go/internal/handlers/common/upstream_failover.go:260`, `backend-go/internal/handlers/common/upstream_failover.go:266`, `backend-go/internal/handlers/common/upstream_failover.go:231`, `backend-go/internal/handlers/common/upstream_failover.go:567`).
- Attempt start creates normal active/request metrics and pending channel log before sending upstream (`backend-go/internal/handlers/common/upstream_failover.go:295`, `backend-go/internal/handlers/common/upstream_failover.go:298`, `backend-go/internal/handlers/common/upstream_failover.go:299`).
- AxonHub stats are classified once per attempt from inbound path + channel kind + upstream service type; same-format raw uses `same_format_raw`, otherwise `cross_format_converted` (`backend-go/internal/handlers/common/upstream_failover.go:112`, `backend-go/internal/handlers/common/upstream_failover.go:130`, `backend-go/internal/handlers/common/upstream_failover.go:135`).
- Every finalize branch currently records AxonHub forwarding stats with either the returned `usage` or nil: send error/client cancel/failure branches (`backend-go/internal/handlers/common/upstream_failover.go:310`, `backend-go/internal/handlers/common/upstream_failover.go:320`, `backend-go/internal/handlers/common/upstream_failover.go:345`, `backend-go/internal/handlers/common/upstream_failover.go:429`, `backend-go/internal/handlers/common/upstream_failover.go:455`) and success branch (`backend-go/internal/handlers/common/upstream_failover.go:564`, `backend-go/internal/handlers/common/upstream_failover.go:565`).
- Client-side cancellation is explicitly only `context.Canceled`, not generic broken pipe/connection reset (`backend-go/internal/handlers/common/upstream_failover.go:23`); cancel finalize does not blacklist/cooldown (`backend-go/internal/handlers/common/upstream_failover.go:308`, `backend-go/internal/handlers/common/upstream_failover.go:482`).
- HTTP error handling preserves failover/cooldown/blacklist: pause rules, channel failover rules, model route miss skip, default retry classifier, blacklist classifier (`backend-go/internal/handlers/common/upstream_failover.go:340`, `backend-go/internal/handlers/common/upstream_failover.go:357`, `backend-go/internal/handlers/common/upstream_failover.go:378`, `backend-go/internal/handlers/common/upstream_failover.go:384`, `backend-go/internal/handlers/common/upstream_failover.go:391`).
- SSE preflight sentinel errors return before headers and re-enter failover/cooldown/blacklist handling (`backend-go/internal/handlers/common/upstream_failover.go:489`, `backend-go/internal/handlers/common/upstream_failover.go:503`, `backend-go/internal/handlers/common/upstream_failover.go:520`).
- `passthrough.Decide` is the central contract: inbound path -> inbound format, upstream service type -> outbound format, raw/strict passthrough only when formats match (`backend-go/internal/passthrough/passthrough.go:30`, `backend-go/internal/passthrough/passthrough.go:49`, `backend-go/internal/passthrough/passthrough.go:72`, `backend-go/internal/passthrough/passthrough.go:87`, `backend-go/internal/passthrough/passthrough.go:91`, `backend-go/internal/passthrough/passthrough.go:100`).
- Forwarding builder is data-plane only: creates request with inbound context, safe headers, custom headers, compatible user-agent, and final selected auth (`backend-go/internal/forwarding/builder.go:24`, `backend-go/internal/forwarding/builder.go:45`, `backend-go/internal/forwarding/builder.go:72`, `backend-go/internal/forwarding/builder.go:77`).
- Messages same-format request body preprocessing is skipped only when passthrough decision is strict+raw; strip-billing-header remains honored (`backend-go/internal/handlers/common/upstream_failover.go:71`, `backend-go/internal/handlers/common/upstream_failover.go:165`, `backend-go/internal/handlers/common/upstream_failover.go:166`).
- Messages/Claude provider patches only top-level model, builds upstream URL preserving route-prefix semantics, and sets raw flags from passthrough decision (`backend-go/internal/providers/claude.go:31`, `backend-go/internal/providers/claude.go:37`, `backend-go/internal/providers/claude.go:39`, `backend-go/internal/providers/claude.go:43`, `backend-go/internal/providers/claude.go:50`, `backend-go/internal/providers/claude.go:59`).
- Messages non-stream same-format raw path forwards response unchanged while tee-decoding usage (`backend-go/internal/handlers/messages/handler.go:266`, `backend-go/internal/handlers/common/request.go:65`, `backend-go/internal/handlers/common/request.go:71`, `backend-go/internal/handlers/common/request.go:81`).
- Messages stream same-format raw path now enters attempt-scoped raw stream passthrough before provider stream parsing (`backend-go/internal/handlers/common/stream.go:1099`, `backend-go/internal/handlers/common/stream.go:1109`, `backend-go/internal/handlers/common/stream.go:1234`).
- Responses raw non-stream path parses usage/session state but sends original `bodyBytes` unchanged (`backend-go/internal/handlers/responses/handler.go:306`, `backend-go/internal/handlers/responses/handler.go:312`, `backend-go/internal/handlers/responses/handler.go:316`, `backend-go/internal/handlers/responses/handler.go:321`, `backend-go/internal/handlers/responses/handler.go:328`).
- Responses raw stream path invokes shared raw stream passthrough with side-channel collector (`backend-go/internal/handlers/responses/handler.go:592`, `backend-go/internal/handlers/responses/handler.go:959`, `backend-go/internal/handlers/common/stream.go:1427`).
- Responses raw stream collector is incremental by event, bounded by side-event size, and returns usage for metrics finalize (`backend-go/internal/handlers/responses/handler.go:951`, `backend-go/internal/handlers/responses/handler.go:962`, `backend-go/internal/handlers/responses/handler.go:975`).
- Chat same-format OpenAI request builder uses forwarding builder and raw flags; stream raw path calls `HandleRawOpenAIChatStreamPassthrough`; non-stream extracts OpenAI usage including cached tokens through common helper (`backend-go/internal/handlers/chat/handler.go:255`, `backend-go/internal/handlers/chat/handler.go:260`, `backend-go/internal/handlers/chat/handler.go:262`, `backend-go/internal/handlers/chat/handler.go:271`, `backend-go/internal/handlers/chat/handler.go:708`, `backend-go/internal/handlers/chat/handler.go:592`, `backend-go/internal/handlers/common/stream.go:1828`).
- Gemini same-format native request builder uses forwarding builder with Gemini auth and raw flags; non-stream returns original Gemini JSON bytes and derives usage from `usageMetadata`; stream raw path uses common raw Gemini passthrough (`backend-go/internal/handlers/gemini/handler.go:334`, `backend-go/internal/handlers/gemini/handler.go:335`, `backend-go/internal/handlers/gemini/handler.go:341`, `backend-go/internal/handlers/gemini/handler.go:343`, `backend-go/internal/handlers/gemini/handler.go:579`, `backend-go/internal/handlers/gemini/handler.go:581`, `backend-go/internal/handlers/gemini/stream.go:31`).
- AxonHub usage stats shape includes request count, input/output/cache tokens, and by-route family/mode (`backend-go/internal/metrics/channel_metrics.go:141`, `backend-go/internal/metrics/channel_metrics.go:146`, `backend-go/internal/metrics/channel_metrics.go:156`).
- AxonHub token extraction normalizes prompt/cache semantics via the existing `types.Usage` object (`backend-go/internal/metrics/channel_metrics.go:739`, `backend-go/internal/metrics/channel_metrics.go:1223`, `backend-go/internal/metrics/channel_metrics.go:1245`).
- Channel metrics response exposes `axonHubForwarding` when present (`backend-go/internal/handlers/channel_metrics_handler.go:35`, `backend-go/internal/handlers/channel_metrics_handler.go:56`).
- Persistence record shape stores ordinary tokens/cache/model/API type, but no AxonHub inbound family or forwarding mode (`backend-go/internal/metrics/persistence.go:57`, `backend-go/internal/metrics/persistence.go:63`, `backend-go/internal/metrics/sqlite_store.go:627`).

### Already covered against PRD MVP

- Same-format format decision is centralized and no longer depends on removed channel-level passthrough switches.
- The main proxy families (`messages`, `responses`, `chat`, `gemini`) all route through `TryUpstreamWithAllKeys`, so key selection, scheduler choice, URL retry, failover, cooldown, blacklist, circuit breaker, logs, and metrics finalize remain outside the forwarding builder.
- Same-format raw response/stream behavior is mostly present:
  - Messages/Claude: raw non-stream JSON usage tee and raw stream attempt-scoped passthrough.
  - Responses/Responses: raw non-stream JSON bytes preserved and raw stream bytes preserved with side-channel usage.
  - Chat/OpenAI: raw stream bytes preserved and non-stream response bytes preserved through passthrough helper.
  - Gemini/Gemini: raw stream bytes preserved and non-stream JSON bytes preserved.
- Cross-format paths still convert through existing providers/converters and then return usage into the same metrics finalize path.
- AxonHub-style usage dimensions already exist: request count, input tokens, output tokens, cache creation tokens, cache read tokens, by inbound family and mode.
- Cache token dimensions are fed from existing usage objects; OpenAI cached tokens and Gemini cached content are normalized into `types.Usage`.
- Existing tests already cover several key MVP cases:
  - passthrough decision and AxonHub classification (`backend-go/internal/handlers/common/upstream_failover_passthrough_test.go:55`, `backend-go/internal/handlers/common/upstream_failover_passthrough_test.go:131`);
  - request preprocessing skip for same-format Messages and active preprocessing for cross-format (`backend-go/internal/handlers/common/upstream_failover_passthrough_test.go:185`);
  - AxonHub stats aggregation including historical keys (`backend-go/internal/metrics/channel_metrics_axonhub_test.go:9`, `backend-go/internal/metrics/channel_metrics_axonhub_test.go:59`);
  - raw response/stream matrix coverage for Messages, Responses, Chat, Gemini (`backend-go/internal/handlers/messages/handler_response_matrix_test.go:98`, `backend-go/internal/handlers/responses/handler_response_matrix_test.go:99`, `backend-go/internal/handlers/chat/handler_response_matrix_test.go:81`, `backend-go/internal/handlers/gemini/handler_response_matrix_test.go:93`);
  - common JSON passthrough preserves body while extracting usage (`backend-go/internal/handlers/common/request_test.go:312`).

### Gaps and risks

- Active task pointer is not available to sub-agents in this session unless the platform supplies a Trellis session identity. This matters because Codex implement/check agents resolve the active task via `task.py current --source`.
- This research file is not yet added to `implement.jsonl` / `check.jsonl`. The user explicitly forbade writes outside research, so the next planning step should add `.trellis/tasks/05-06-axonhub-forwarding-ccx-resilience/research/forwarding-usage-failover-gaps.md` to both contexts before implementation/check.
- AxonHub forwarding stats are in-memory only. Ordinary request records persist token/cache/model data, but not `inboundFamily` or `mode`; after restart, AxonHub route statistics cannot be reconstructed from SQLite without a schema/record extension.
- AxonHub `requestCount` currently increments for every finalized forwarding attempt where classification is enabled, including failed attempts and client cancellations with nil usage. That matches an "attempt count" metric, but is risky if later UI or billing wording treats it as "billable request count".
- `RecordAxonHubForwardingUsage` is separate from normal `RecordRequestFinalize*` and not persisted atomically. A future panic or early return between normal finalize and AxonHub record would diverge ordinary metrics from forwarding stats; current code places calls adjacent, but the contract should stay explicit.
- Same-format raw request body is not always byte-for-byte raw. The current contract allows patching platform fields and preserving unknown fields, but Responses/Chat/Gemini paths may parse/re-marshal when model/reasoning/text/service-tier or protocol-specific options are applied. If "raw passthrough" is interpreted as exact request bytes, the implementation does not fully satisfy that stricter reading.
- Chat handler has a semantic inconsistency to verify: `passthrough.OutboundFormatForService("responses")` classifies outbound as Responses API, but `buildChatRequest` treats `serviceType == "responses"` as OpenAI-compatible `/v1/chat/completions`. This can make AxonHub classification say `chat/cross_format_converted` while the actual upstream request remains Chat-compatible.
- Responses raw non-stream parses the entire body before forwarding to validate/derive usage/session state. It still sends original bytes unchanged, but very large non-stream responses are fully buffered.
- Stream preflight coverage is strongest for Responses/Messages and present for Chat/Gemini raw paths, but AxonHub-specific stats assertions are currently mostly unit-level in metrics/common classification. End-to-end handler tests should assert `axonHubForwarding` updates after actual same-format and cross-format proxy calls.
- Channel metrics API exposes `axonHubForwarding`, but no dedicated billing/reporting surface exists. PRD says no ledger/pricing, so this is acceptable for MVP, but dashboard consumers may need a clear label that these are forwarding usage stats, not a financial billing ledger.

### Recommended MVP implementation order

1. Fix Trellis context first: ensure active task can resolve in the current platform session, then add this research file to `implement.jsonl` and `check.jsonl`.
2. Lock the metrics contract before touching forwarding: decide whether `axonHubForwarding.requestCount` means finalized attempts or successful/billable requests. For MVP, keep the current attempt-count behavior but name/test it explicitly.
3. Add end-to-end handler tests that query channel metrics after real proxy calls:
   - one same-format raw path records `same_format_raw` with tokens/cache;
   - one cross-format converted path records `cross_format_converted`;
   - one retry/failover path proves failed first attempt and successful second attempt preserve normal finalize and forwarding stats.
4. Verify and, if needed, tighten Chat `serviceType == "responses"` behavior before relying on AxonHub mode classification for Chat->Responses.
5. If MVP requires stats to survive restart, extend persistence with forwarding family/mode fields or a side table. If MVP only needs runtime channel metrics, document in PRD/spec that AxonHub forwarding stats are volatile.
6. Keep any implementation inside existing ownership boundaries:
   - forwarding request construction in `internal/forwarding`, providers, or protocol handlers;
   - control-plane behavior only in `handlers/common`, `scheduler`, `metrics`, and `config`;
   - no scheduler/failover/circuit/blacklist/cooldown logic inside `forwarding.Build`.

### Suggested test points

- Same-format Messages `/v1/messages` -> Claude:
  - request body unknown fields preserved except allowed platform patches;
  - raw non-stream body unchanged;
  - raw stream SSE bytes unchanged;
  - `axonHubForwarding.byRoute` contains `messages/same_format_raw`.
- Same-format Responses `/v1/responses` -> Responses:
  - raw non-stream body preserves unknown vendor fields;
  - raw stream usage after large prefix still records tokens;
  - raw stream empty/invalid/blacklist preflight fails over before headers.
- Same-format Chat `/v1/chat/completions` -> OpenAI:
  - non-stream cached tokens map to `CacheReadInputTokens`;
  - raw stream preserves OpenAI SSE bytes and records cached tokens;
  - `axonHubForwarding.byRoute` contains `chat/same_format_raw`.
- Same-format Gemini native -> Gemini:
  - non-stream `cachedContentTokenCount` maps to cache-read tokens and input is `max(prompt - cached, 0)`;
  - raw stream preserves native Gemini SSE bytes;
  - `axonHubForwarding.byRoute` contains `gemini/same_format_raw`.
- Cross-format path, e.g. Responses -> Claude or Messages -> OpenAI:
  - response is converted, not raw;
  - normal failover/metrics finalize is preserved;
  - `axonHubForwarding.byRoute` contains the inbound family with `cross_format_converted`.
- Control-plane preservation:
  - retryable HTTP errors try next key/BaseURL/channel and finalize failed attempt;
  - pause/cooldown rules mark only the intended key and fail over;
  - blacklist errors move the key to disabled/blacklisted state according to config;
  - model route miss skips cooldown/breaker;
  - circuit-open key is skipped and half-open probe acquisition/release works;
  - `context.Canceled` finalizes as client cancel, does not blacklist/cooldown, and stops failover.
- Metrics/reporting:
  - AxonHub stats aggregate active + historical API keys;
  - missing usage increments request count but not tokens;
  - ordinary metrics token history and AxonHub route stats remain consistent for success paths;
  - if persistence is added, restart/load reconstructs route stats exactly.

### External references

- None. This research used local PRD, Trellis specs, and repository code only.

### Related specs

- `.trellis/spec/backend/index.md`: backend checklist points to structure, error, logging, and quality specs.
- `.trellis/spec/backend/directory-structure.md`: forwarding/control-plane changes should stay in existing `internal/*` ownership boundaries.
- `.trellis/spec/backend/error-handling.md`: sentinel errors and `%w` wrapping are appropriate where shared control flow needs retry/failover decisions.
- `.trellis/spec/backend/logging-guidelines.md`: operational logs should use bracketed component tags and mask keys/credentials.
- `.trellis/spec/backend/quality-guidelines.md`: includes proxy handler contracts, passthrough decision contracts, AxonHub forwarding usage stats contract, and required test packages.

## Caveats / Not Found

- Could not set or confirm the active task pointer through `task.py current --source` in this session; the target directory and PRD were read directly.
- No code was modified.
- No external AxonHub documentation was consulted because the task scope asked for repository gap analysis against the local PRD.
- No full billing ledger, price table, balance deduction, refund, or idempotent billing transaction exists in the inspected code; this matches PRD out-of-scope.
- No persistence schema for AxonHub forwarding route stats was found.
