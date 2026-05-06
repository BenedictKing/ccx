# Continue AxonHub Passthrough Migration

## Goal

Continue the AxonHub-inspired passthrough migration without porting AxonHub's full orchestrator/pipeline architecture. Header override hardening and the Messages -> Claude raw stream fan-out pilot are complete; the final increment is to bring the remaining same-format stream routes, User-Agent behavior, sensitive header handling, and migration references to a 100% closeout state.

## What I Already Know

- `AxonHub-half.md` is the handoff record for this continuation.
- Backward compatibility is explicitly out of scope; removed passthrough fields must not be reintroduced.
- P0 is already complete: old passthrough config/UI/API fields were removed and passthrough decisions now depend on inbound/outbound API format consistency.
- P1 stream cleanup is mostly complete: provider stream readers accept context, attempt cancellation closes provider bodies, and cooldown stream errors can continue failover.
- P1 header ordering is complete: provider and handler paths apply custom headers before final auth headers, and handler-level regression tests now cover the contract.

## Requirements

### Completed: Header Override Hardening

- Add an `Upstream Header Override` backend spec contract that fixes the order:
  - Build base upstream headers first.
  - Apply platform-controlled content type as needed.
  - Apply channel custom headers before selected authentication headers.
  - Set final selected auth headers last so custom headers cannot replace them.
- Add handler-level matrix/regression tests proving custom auth-like headers cannot override the selected key for:
  - Chat handler custom `Authorization`.
  - Gemini handler custom `x-goog-api-key`.
  - Images handler custom `Authorization`.
  - Responses compact handler custom `Authorization`.
- Keep the scope narrow. Do not implement raw stream fan-out, per-attempt raw stream state, or User-Agent passthrough in this task.
- Do not reintroduce any removed fields:
  - `streamPassthroughEnabled`
  - `sub2apiPassthroughEnabled`
  - `strictRequestPassthroughEnabled`
  - `normalizeMetadataUserId`

### Next: Raw Stream Fan-Out Pilot

- Add a minimal attempt-scoped raw stream fan-out path for same-format Messages -> Claude passthrough.
- Preserve raw upstream SSE bytes to the client for same-format stream passthrough.
- Feed a parsed/normalized branch into the existing stream lifecycle enough to preserve usage, metrics, blacklist/cooldown preflight behavior, and log/session accounting currently owned by the handler/provider path.
- Ensure retry/failover cleanup cancels the current attempt fan-out and closes provider response bodies before the next key/channel attempt.
- Fan-out goroutines must never block forever when the client disconnects, the handler stops reading, or the attempt context is canceled.
- Keep cross-format routes on the existing conversion path.
- Keep the pilot narrow: do not port AxonHub's full orchestrator/pipeline structure.

### Final: 100% AxonHub Closeout

- Expand raw stream preservation to every same-format streaming route that has a native upstream stream:
  - `/v1/messages -> claude` is already complete.
  - `/v1/responses -> responses` is already complete.
  - `/v1/chat/completions -> openai` must preserve raw OpenAI-compatible SSE bytes while collecting usage where available.
  - Gemini native stream routes must preserve raw Gemini SSE bytes while keeping existing accounting/failover behavior.
- Keep cross-format stream routes on their existing conversion paths.
- Define and implement a User-Agent passthrough strategy independent from body/response passthrough.
- Define and implement a unified sensitive inbound header stripping strategy for upstream requests, including at minimum `Cookie` and `Proxy-Authorization`.
- Keep final selected authentication headers authoritative after custom headers and User-Agent handling.
- Update backend/frontend specs and tests for the final contracts.
- Clean up AxonHub migration reference artifacts when the migration is complete:
  - `AxonHub-half.md`
  - `axonhub.md`
  - `axonhub/`
- Prepare a commit plan for the AxonHub work. Because the worktree contains unrelated dirty files, do not commit without an explicit user confirmation of the staged file groups.

## Acceptance Criteria

- [x] `.trellis/spec/backend/quality-guidelines.md` documents the header override contract.
- [x] Handler-level tests cover Chat, Gemini, Images, and Responses compact auth override behavior.
- [x] Tests verify the selected channel key wins over channel custom auth headers.
- [x] `go fmt ./...` is run after backend test changes.
- [x] `cd backend-go && go test ./...` passes.
- [x] `git diff --check` passes.
- [x] Same-format `/v1/messages -> claude` stream passthrough returns upstream raw SSE bytes unchanged.
- [x] The raw fan-out path still records/collects usage or preserves the existing metrics behavior for stream completion.
- [x] Cancel/failover tests prove the current attempt body/fan-out is released before trying the next key/channel.
- [x] Cross-format stream routes still use conversion and do not enter raw stream fan-out.
- [x] `go fmt ./...`, targeted handler/provider tests, `cd backend-go && go test ./...`, and `git diff --check` pass after the fan-out changes.
- [x] Same-format Chat/OpenAI stream preserves raw upstream SSE bytes.
- [x] Same-format Gemini stream preserves raw upstream SSE bytes.
- [x] Responses raw stream remains unchanged and covered by tests.
- [x] User-Agent passthrough behavior is explicit, tested, and not coupled to body/response passthrough.
- [x] Sensitive inbound headers such as `Cookie` and `Proxy-Authorization` are stripped consistently before upstream requests.
- [x] Backend/frontend specs reflect final raw stream, User-Agent, and sensitive header contracts.
- [x] AxonHub reference artifacts are removed or archived after final verification.
- [ ] Commit plan is prepared and confirmed before any git commit.

## Definition of Done

- Tests added/updated beside the owning backend packages.
- Backend verification is green.
- No unrelated refactor or old passthrough compatibility code is added.
- Raw stream fan-out behavior is covered by regression tests.
- User-Agent passthrough and sensitive header behavior are covered by regression tests.
- Reference artifacts are cleaned up or intentionally retained with a documented reason.

## Out of Scope

- Full AxonHub orchestrator/pipeline port.
- Reintroducing removed passthrough compatibility fields.
- Broad UI redesign unrelated to any final config/API fields.

## Technical Approach

The raw stream fan-out pilot should be test-first and incremental. Prefer an attempt-scoped helper/state object over a broad pipeline refactor. The helper should own cancel/reset behavior for the current attempt and should be easy to remove or expand later if a full orchestrator is introduced.

For the first pilot, target same-format Messages -> Claude stream passthrough because it has the clearest raw SSE preservation requirement. Cross-format streams should continue through the existing provider conversion readers.

For closeout, prefer shared raw stream helpers and shared header helpers over per-protocol ad hoc logic. User-Agent passthrough should be its own header concern and should not be inferred from raw body/response passthrough. Sensitive header stripping should happen before custom headers and before final auth header assignment, so channel-owned custom headers can still intentionally add non-auth metadata while final auth remains selected-key controlled.

## Technical Notes

- Handoff source: `AxonHub-half.md`.
- Relevant spec: `.trellis/spec/backend/quality-guidelines.md`.
- Likely handler files:
  - `backend-go/internal/handlers/chat/handler.go`
  - `backend-go/internal/handlers/gemini/handler.go`
  - `backend-go/internal/handlers/images/handler.go`
  - `backend-go/internal/handlers/responses/compact.go`
- Existing provider-level header coverage: `backend-go/internal/providers/claude_passthrough_test.go`.
- Existing handler test files to extend:
  - `backend-go/internal/handlers/chat/handler_test.go`
  - `backend-go/internal/handlers/gemini/handler_test.go`
  - `backend-go/internal/handlers/images/handler_test.go`
  - `backend-go/internal/handlers/responses/compact_test.go`
- Raw stream fan-out likely touches:
  - `backend-go/internal/handlers/messages/handler.go`
  - `backend-go/internal/handlers/common/stream.go`
  - `backend-go/internal/providers/claude.go`
  - `backend-go/internal/providers/provider.go`
  - `backend-go/internal/passthrough/`

## Verification Notes

- `go fmt ./...` passed.
- `go vet ./...` passed.
- `go test -count=1 ./internal/handlers/chat ./internal/handlers/gemini ./internal/handlers/images ./internal/handlers/responses` passed.
- `go test ./...` passed.
- `git diff --check` passed.
- Full `golangci-lint run` still reports unrelated existing/parallel dirty-worktree findings outside this task scope.
- Raw stream fan-out pilot verification passed:
  - `go test ./internal/handlers/messages -run "TestMessagesHandler_(StreamRawPassthroughPreservesUpstreamSSEBytesAndMetrics|StreamRawPassthroughCancelsFirstAttemptBeforeFailover|CrossFormatStreamDoesNotUseRawPassthrough)" -count=1 -v`
  - `go test ./internal/handlers/common -run "TestHandleStreamResponse|TestPreflightStreamEvents|TestShouldDirectPassthrough" -count=1`
  - `go test ./internal/handlers/messages ./internal/handlers/common ./internal/providers ./internal/passthrough -count=1`
  - `go vet ./...`
  - `go test ./...`
  - `git diff --check`
- `trellis-check` fixed raw fan-out cleanup to synchronously wait for the fan-out goroutine and response body to exit before returning failover/cancel paths.
- AxonHub reference artifacts were removed after final verification:
  - `AxonHub-half.md`
  - `axonhub.md`
  - `axonhub/`
