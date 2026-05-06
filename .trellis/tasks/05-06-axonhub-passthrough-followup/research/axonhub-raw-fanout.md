# Research: AxonHub raw stream fan-out/reset reference

- Query: Research AxonHub raw stream fan-out/reset reference for the active passthrough follow-up task; inspect local AxonHub reference files named in `AxonHub-half.md` and persist only minimal contracts worth porting to ccx.
- Scope: internal
- Date: 2026-05-06

## Findings

### Files found

- `AxonHub-half.md` - handoff note naming the AxonHub reference files and narrowing the next ccx increment to a raw stream fan-out pilot, not a full orchestrator/pipeline copy.
- `axonhub/internal/server/orchestrator/pass_through.go` - AxonHub passthrough request/response/stream middleware; includes the raw stream fan-out and pass-through stream wrapper.
- `axonhub/internal/server/orchestrator/state.go` - per-request state fields used by passthrough, including per-attempt raw stream channel, error pointer, and cancel function.
- `axonhub/internal/server/orchestrator/outbound.go` - persistent outbound stream wrapper and retry reset hooks that cancel stale fan-out state before retrying.
- `axonhub/internal/server/orchestrator/request_execution.go` - persistence middleware for request execution creation, completion, and failure status.
- `axonhub/llm/pipeline/stream.go` - stream pipeline ordering around raw stream middleware, outbound transform, LLM middleware, inbound transform, and inbound raw stream middleware.
- `backend-go/internal/passthrough/passthrough.go` - ccx centralized same-format passthrough decision helper.
- `backend-go/internal/providers/provider.go` - ccx provider stream channel helpers and SSE field normalization.
- `backend-go/internal/providers/claude.go` - ccx Claude stream reader currently scans upstream body into event strings.
- `backend-go/internal/handlers/common/stream.go` - ccx Messages stream preflight, direct passthrough forwarding, usage collection, and client-cancel cleanup.
- `backend-go/internal/handlers/common/upstream_failover.go` - ccx key/baseURL retry loop, stream failover classification, metrics, and channel-log lifecycle.
- `backend-go/internal/handlers/messages/handler.go` - ccx Messages handler path that delegates stream attempts into `TryUpstreamWithAllKeys`.

### Minimal contracts worth porting

1. Gate raw fan-out by the existing ccx same-format decision, not a new channel flag.
   - AxonHub checks both enabled passthrough and inbound/outbound API format equality before activating passthrough (`axonhub/internal/server/orchestrator/pass_through.go:18`, `axonhub/internal/server/orchestrator/pass_through.go:30`, `axonhub/internal/server/orchestrator/pass_through.go:35`).
   - ccx already has the narrower desired contract: `passthrough.Decide` sets `RawResponse` and `SkipPreprocess` from `AllowsRawResponsePassthrough` and `AllowsStrictBodyPassthrough` (`backend-go/internal/passthrough/passthrough.go:30`, `backend-go/internal/passthrough/passthrough.go:40`, `backend-go/internal/passthrough/passthrough.go:44`, `backend-go/internal/passthrough/passthrough.go:45`), and both helpers require matching formats (`backend-go/internal/passthrough/passthrough.go:82`, `backend-go/internal/passthrough/passthrough.go:91`).
   - For the pilot, the fan-out trigger should be exactly same-format `/v1/messages -> claude` stream passthrough through `ShouldDirectPassthroughForRequest`, not a resurrected `streamPassthroughEnabled` or broader pipeline switch.

2. Split a successful upstream stream into two consumers: raw client branch and internal metrics/lifecycle branch.
   - AxonHub creates `pipelineCh` and `rawStreamCh`, stores the raw branch on state, and returns a channel stream for the pipeline branch (`axonhub/internal/server/orchestrator/pass_through.go:210`, `axonhub/internal/server/orchestrator/pass_through.go:222`, `axonhub/internal/server/orchestrator/pass_through.go:224`, `axonhub/internal/server/orchestrator/pass_through.go:280`).
   - Later, AxonHub returns the raw branch as the inbound stream while draining the transformed pipeline stream in the background so internal middleware still runs (`axonhub/internal/server/orchestrator/pass_through.go:284`, `axonhub/internal/server/orchestrator/pass_through.go:308`, `axonhub/internal/server/orchestrator/pass_through.go:316`).
   - For ccx, do not port `pipeline.Middleware`; instead add a focused attempt-scoped helper near `common.HandleStreamResponse` or the provider stream path. It should let one branch write untouched upstream SSE bytes to `gin.ResponseWriter`, while the other branch feeds existing preflight/usage collection enough to preserve cooldown/blacklist decisions and final metrics.

3. Fan-out sends must block for backpressure but always observe attempt cancellation.
   - AxonHub intentionally uses blocking sends to avoid silently dropping provider events, but each send is inside `select` with `attemptCtx.Done()` (`axonhub/internal/server/orchestrator/pass_through.go:256`, `axonhub/internal/server/orchestrator/pass_through.go:260`, `axonhub/internal/server/orchestrator/pass_through.go:269`).
   - ccx already uses context-aware channel sends in provider helpers (`backend-go/internal/providers/provider.go:52`, `backend-go/internal/providers/provider.go:61`) and cancels/drains on client disconnect in direct passthrough (`backend-go/internal/handlers/common/stream.go:912`, `backend-go/internal/handlers/common/stream.go:914`, `backend-go/internal/handlers/common/stream.go:915`).
   - Port only the rule: every raw/internal branch send must select on the attempt context. This is the most important leak-prevention contract.

4. Store error state per attempt, not as a shared mutable field reused across retries.
   - AxonHub creates a local `rawStreamErr` variable for each fan-out attempt and stores a pointer to it for that attempt's stream wrapper (`axonhub/internal/server/orchestrator/pass_through.go:226`, `axonhub/internal/server/orchestrator/pass_through.go:229`, `axonhub/internal/server/orchestrator/pass_through.go:231`).
   - AxonHub snapshots the current error pointer before returning the raw stream, so a future retry cannot replace the error observed by the current stream (`axonhub/internal/server/orchestrator/pass_through.go:298`, `axonhub/internal/server/orchestrator/pass_through.go:300`).
   - In ccx, model this as a small attempt object owning `ctx`, `cancel`, raw branch, internal branch, and error reporting. Avoid package-level/shared error fields and avoid putting retry-spanning mutable state on provider structs.

5. Reset/cancel fan-out before every same-channel or next-channel retry.
   - AxonHub state has `RawStreamCancel`, `RawStreamCh`, and `RawStreamErrRef` as explicit stream attempt state (`axonhub/internal/server/orchestrator/state.go:64`, `axonhub/internal/server/orchestrator/state.go:67`, `axonhub/internal/server/orchestrator/state.go:73`).
   - `resetPassThroughStreamState` calls the current cancel function, then clears channel and error state (`axonhub/internal/server/orchestrator/outbound.go:471`, `axonhub/internal/server/orchestrator/outbound.go:475`, `axonhub/internal/server/orchestrator/outbound.go:481`).
   - AxonHub calls this reset before moving to the next channel and before same-channel retry (`axonhub/internal/server/orchestrator/outbound.go:487`, `axonhub/internal/server/orchestrator/outbound.go:490`, `axonhub/internal/server/orchestrator/outbound.go:579`, `axonhub/internal/server/orchestrator/outbound.go:587`).
   - In ccx, the retry loop is `TryUpstreamWithAllKeys`; cleanup needs to happen inside each stream attempt before `continue` paths for `ErrEmptyStreamResponse`, `ErrCooldownKey`, `ErrBlacklistKey`, and generic stream failure (`backend-go/internal/handlers/common/upstream_failover.go:428`, `backend-go/internal/handlers/common/upstream_failover.go:441`, `backend-go/internal/handlers/common/upstream_failover.go:457`, `backend-go/internal/handlers/common/upstream_failover.go:488`). The attempt helper should close the current `resp.Body` and cancel its fan-out before the next key/baseURL attempt starts.

6. Keep preflight before response headers are sent.
   - ccx currently creates provider stream channels, runs `PreflightStreamEvents`, handles preflight errors/empty streams, and only then calls `SetupStreamHeaders` (`backend-go/internal/handlers/common/stream.go:827`, `backend-go/internal/handlers/common/stream.go:837`, `backend-go/internal/handlers/common/stream.go:838`, `backend-go/internal/handlers/common/stream.go:863`, `backend-go/internal/handlers/common/stream.go:870`).
   - This is a stronger fit for ccx than AxonHub's full middleware stack. Preserve it: raw branch must not start committing client headers until preflight has decided the stream is not an early failover/cooldown/blacklist case.

7. The current ccx "direct passthrough" is not true upstream-byte passthrough yet.
   - Claude stream handling scans the upstream body line by line and normalizes each SSE field line before emitting events (`backend-go/internal/providers/claude.go:111`, `backend-go/internal/providers/claude.go:130`, `backend-go/internal/providers/claude.go:134`).
   - The normalization helper rewrites forms like `data:{...}` to `data: {...}` and `event:name` to `event: name` (`backend-go/internal/providers/provider.go:74`, `backend-go/internal/providers/provider.go:76`, `backend-go/internal/providers/provider.go:77`, `backend-go/internal/providers/provider.go:78`).
   - Existing direct passthrough tests assert preservation only after a test provider has already supplied event strings (`backend-go/internal/handlers/common/stream_test.go:691`, `backend-go/internal/handlers/common/stream_test.go:713`, `backend-go/internal/handlers/common/stream_test.go:717`), not end-to-end preservation from real upstream bytes through `ClaudeProvider.HandleStreamResponse`.
   - Therefore the raw pilot should fan out before provider-level SSE normalization if the acceptance criterion is "upstream raw SSE bytes unchanged".

8. Internal metrics/lifecycle branch should reuse existing ccx accounting, not AxonHub request execution persistence.
   - AxonHub's persistent stream records chunks, terminal-event completion, aggregation, and detached persistence (`axonhub/internal/server/orchestrator/outbound.go:76`, `axonhub/internal/server/orchestrator/outbound.go:83`, `axonhub/internal/server/orchestrator/outbound.go:111`, `axonhub/internal/server/orchestrator/outbound.go:143`).
   - ccx already finalizes success/failure in the retry loop after `handleSuccess` returns usage (`backend-go/internal/handlers/common/upstream_failover.go:418`, `backend-go/internal/handlers/common/upstream_failover.go:499`, `backend-go/internal/handlers/common/upstream_failover.go:506`) and direct passthrough collects usage side-channel without rewriting client events (`backend-go/internal/handlers/common/stream.go:879`, `backend-go/internal/handlers/common/stream.go:884`, `backend-go/internal/handlers/common/stream.go:893`, `backend-go/internal/handlers/common/stream.go:960`).
   - Port only the separation of raw delivery from internal parsing. Do not port AxonHub `RequestExecution` persistence or `OutboundPersistentStream`.

### Suggested ccx shape for the pilot

- Add a narrow attempt-scoped raw stream helper rather than a new pipeline layer. The helper should own:
  - `attemptCtx` and `cancel`.
  - an upstream body reader goroutine that closes the provider body on exit.
  - a raw branch preserving upstream SSE bytes/events exactly.
  - an internal branch parsed into the existing event-string shape for `PreflightStreamEvents`, `collectPassthroughStreamUsage`, and related helpers.
  - a per-attempt error slot exposed to both branch consumers.
- Keep `HandleStreamResponse`'s existing preflight-before-headers behavior. The raw branch should buffer/replay preflight bytes only after preflight passes.
- Test with a real `httptest.Server` upstream that emits non-normalized SSE (`event:name`, `data:{...}`, comments, `id:abc`, `retry:1500`) through the Messages handler. This should fail against the current provider-normalized path and prove the raw branch is before normalization.
- Add retry cleanup coverage where the first stream attempt triggers preflight cooldown/empty/error and the second key succeeds; assert the first upstream body is closed/canceled before the second attempt proceeds.

## External References

- None. This research used only local repository sources and the vendored/local `axonhub/` reference tree.

## Related Specs

- `.trellis/spec/backend/index.md` - backend pre-development checklist for handler/provider work.
- `.trellis/spec/backend/quality-guidelines.md` - contains the existing "Passthrough Decision Contracts", "Proxy Handler Contracts", and retry/failover expectations relevant to raw stream fan-out.
- `.trellis/spec/backend/error-handling.md` - reinforces contextual errors, client-cancel handling, and stable handler boundary responses.
- `.trellis/spec/backend/logging-guidelines.md` - keep stream/failover logging tagged and avoid raw credential leakage.
- `.trellis/spec/backend/directory-structure.md` - shared handler helpers belong under `backend-go/internal/handlers/common/`; provider transport behavior belongs under `backend-go/internal/providers/`.

## Caveats / Not Found

- `task.py current --source` returned no active task in this session, but the user specified the task directory and exact research output path, so the artifact was written there.
- `axonhub/llm/pipeline/stream.go` is present. The research did not inspect `axonhub/llm/pipeline/pipeline.go` because the user narrowed this request to `stream.go` plus the orchestrator files.
- `AxonHub-half.md` content rendered with encoding artifacts in PowerShell, but the file paths and fan-out/reset bullets were still identifiable.
- No production code was edited.
- Do not port AxonHub's full orchestrator, middleware, request execution persistence, channel candidate model, or User-Agent passthrough for this ccx pilot.
