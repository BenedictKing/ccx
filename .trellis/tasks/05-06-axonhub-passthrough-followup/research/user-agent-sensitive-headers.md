# Research: User-Agent passthrough and sensitive inbound header stripping

- Query: Research User-Agent passthrough and sensitive inbound header stripping for AxonHub passthrough follow-up. Inspect `backend-go/internal/utils/headers.go` and tests, provider/handler request builders, and config/channel API/frontend payload as needed. Do not edit production code.
- Scope: internal
- Date: 2026-05-06

## Findings

### Files Found

- `backend-go/internal/utils/headers.go` - central upstream header helper, auth setters, custom header application, User-Agent compatibility helper.
- `backend-go/internal/utils/headers_test.go` - unit tests for proxy header removal, general header passthrough, auth setters, custom header override behavior, and User-Agent behavior.
- `backend-go/internal/providers/claude.go` - Messages-family Claude provider request builder; uses shared passthrough headers and Claude default User-Agent fallback.
- `backend-go/internal/providers/openai.go` - Messages-family OpenAI provider request builder; currently uses shared passthrough headers.
- `backend-go/internal/providers/gemini.go` - Messages-family Gemini provider request builder; currently uses shared passthrough headers.
- `backend-go/internal/providers/responses.go` - Responses-family provider request builder; uses shared passthrough headers, then explicitly deletes auth-like headers before final auth.
- `backend-go/internal/handlers/messages/handler.go` - Messages proxy handler delegates upstream request construction to provider builders.
- `backend-go/internal/handlers/responses/handler.go` - Responses proxy handler delegates upstream request construction to provider builders.
- `backend-go/internal/handlers/chat/handler.go` - Chat proxy request builder uses shared passthrough headers directly.
- `backend-go/internal/handlers/gemini/handler.go` - Gemini proxy request builder uses shared passthrough headers directly.
- `backend-go/internal/handlers/responses/compact.go` - compact endpoint local retry path builds its own upstream request with shared passthrough headers.
- `backend-go/internal/handlers/images/handler.go` - Images proxy request builder has a local clone of shared passthrough header stripping.
- `backend-go/internal/handlers/*/channels.go` - admin channel/model-list/probe request builders; most do not forward inbound headers, but some have custom header ordering gaps.
- `backend-go/internal/handlers/capability_test_handler.go` - capability-test request builder sets auth/protocol headers and then applies channel custom headers.
- `backend-go/internal/config/config.go` - channel config shape exposes generic `customHeaders`, not User-Agent or sensitive-header policy settings.
- `frontend/src/services/api.ts` - frontend `Channel` and `ChannelModelsRequest` types expose `customHeaders`.
- `frontend/src/utils/channelPayload.ts` - channel create/update payload forwards `form.customHeaders` unchanged.
- `frontend/src/components/AddChannelModal.vue` - generic custom-header UI lets admins add arbitrary header names/values.
- `backend-go/internal/handlers/common/request.go` and `backend-go/internal/utils/json.go` - request diagnostics mask only auth-like headers, not cookies or proxy credentials.

### Current Behavior

- `PrepareUpstreamHeaders` clones all inbound request headers, then strips only `x-proxy-key`, `X-Forwarded-For`, `X-Forwarded-Host`, `X-Forwarded-Proto`, `X-Real-IP`, `Via`, `Forwarded`, and `Accept-Encoding`; it overwrites `Host` and `Content-Type` (`backend-go/internal/utils/headers.go:106`, `backend-go/internal/utils/headers.go:107`, `backend-go/internal/utils/headers.go:110`, `backend-go/internal/utils/headers.go:113`, `backend-go/internal/utils/headers.go:119`, `backend-go/internal/utils/headers.go:123`, `backend-go/internal/utils/headers.go:126`).
- The helper does not strip inbound `Authorization`, `x-api-key`, `x-goog-api-key`, `Cookie`, `Proxy-Authorization`, hop-by-hop `Connection` family headers, or vendor client-IP headers. Auth-like inbound headers are usually removed later only because final auth setters run after custom headers (`backend-go/internal/utils/headers.go:146`, `backend-go/internal/utils/headers.go:148`, `backend-go/internal/utils/headers.go:150`, `backend-go/internal/utils/headers.go:164`, `backend-go/internal/utils/headers.go:165`, `backend-go/internal/utils/headers.go:167`).
- `PrepareMinimalHeaders` exists but has no production call sites; `rg PrepareMinimalHeaders backend-go/internal` finds only the declaration/comments (`backend-go/internal/utils/headers.go:131`, `backend-go/internal/utils/headers.go:134`). Current production proxy behavior is passthrough-oriented, not minimal.
- `ApplyCustomHeaders` trims blank names/values and then blindly sets any header, including auth-like or sensitive names (`backend-go/internal/utils/headers.go:173`, `backend-go/internal/utils/headers.go:180`). Tests explicitly lock generic override behavior for `Authorization` at helper level (`backend-go/internal/utils/headers_test.go:221`, `backend-go/internal/utils/headers_test.go:238`, `backend-go/internal/utils/headers_test.go:242`).
- `EnsureCompatibleUserAgent` only sets `claude-cli/2.0.34 (external, cli)` when service type is `claude` and the header is missing; otherwise it preserves the inbound or custom `User-Agent` (`backend-go/internal/utils/headers.go:184`, `backend-go/internal/utils/headers.go:188`, `backend-go/internal/utils/headers.go:191`). Tests assert passthrough for non-Claude and non-Claude-CLI User-Agent values (`backend-go/internal/utils/headers_test.go:159`, `backend-go/internal/utils/headers_test.go:168`, `backend-go/internal/utils/headers_test.go:175`, `backend-go/internal/utils/headers_test.go:189`, `backend-go/internal/utils/headers_test.go:196`).
- Messages provider builders all use `PrepareUpstreamHeaders` and apply channel custom headers before final selected auth, so selected auth wins. This applies to Claude, OpenAI, Gemini, and Responses provider paths (`backend-go/internal/providers/claude.go:84`, `backend-go/internal/providers/claude.go:85`, `backend-go/internal/providers/claude.go:86`, `backend-go/internal/providers/openai.go:110`, `backend-go/internal/providers/openai.go:111`, `backend-go/internal/providers/openai.go:112`, `backend-go/internal/providers/gemini.go:68`, `backend-go/internal/providers/gemini.go:69`, `backend-go/internal/providers/gemini.go:70`, `backend-go/internal/providers/responses.go:62`, `backend-go/internal/providers/responses.go:67`, `backend-go/internal/providers/responses.go:69`).
- Claude provider additionally calls `EnsureCompatibleUserAgent`, so missing UA gets the Claude CLI default and supplied UA is passed through (`backend-go/internal/providers/claude.go:87`).
- Messages and Responses handlers delegate request building to provider builders in both affinity and non-affinity paths (`backend-go/internal/handlers/messages/handler.go:136`, `backend-go/internal/handlers/messages/handler.go:233`, `backend-go/internal/handlers/responses/handler.go:133`, `backend-go/internal/handlers/responses/handler.go:223`).
- Chat handler uses `PrepareUpstreamHeaders`, `Content-Type`, custom headers, then auth. For Claude service type it also sets `anthropic-version`, but it does not call `EnsureCompatibleUserAgent` (`backend-go/internal/handlers/chat/handler.go:347`, `backend-go/internal/handlers/chat/handler.go:350`, `backend-go/internal/handlers/chat/handler.go:352`, `backend-go/internal/handlers/chat/handler.go:355`, `backend-go/internal/handlers/chat/handler.go:357`).
- Gemini handler uses `PrepareUpstreamHeaders`, `Content-Type`, custom headers, then auth according to upstream service type; it does not call `EnsureCompatibleUserAgent` for Claude service type (`backend-go/internal/handlers/gemini/handler.go:446`, `backend-go/internal/handlers/gemini/handler.go:448`, `backend-go/internal/handlers/gemini/handler.go:451`, `backend-go/internal/handlers/gemini/handler.go:454`, `backend-go/internal/handlers/gemini/handler.go:457`, `backend-go/internal/handlers/gemini/handler.go:462`).
- Responses compact local retry path mirrors provider order: `PrepareUpstreamHeaders`, explicit delete for `authorization`/`x-api-key`, `Content-Type`, custom headers, final auth (`backend-go/internal/handlers/responses/compact.go:323`, `backend-go/internal/handlers/responses/compact.go:324`, `backend-go/internal/handlers/responses/compact.go:326`, `backend-go/internal/handlers/responses/compact.go:327`, `backend-go/internal/handlers/responses/compact.go:328`). It does not delete inbound `x-goog-api-key` before applying custom headers, though `SetAuthenticationHeader` deletes it later.
- Images handler has a duplicated helper that clones inbound headers and strips the same limited proxy headers as `PrepareUpstreamHeaders`; it also preserves inbound `User-Agent` and does not strip cookies/proxy auth/hop-by-hop headers (`backend-go/internal/handlers/images/handler.go:349`, `backend-go/internal/handlers/images/handler.go:386`, `backend-go/internal/handlers/images/handler.go:387`, `backend-go/internal/handlers/images/handler.go:389`, `backend-go/internal/handlers/images/handler.go:396`).
- Provider auth override tests cover selected auth winning for Claude, OpenAI, Gemini, and Responses provider builders (`backend-go/internal/providers/claude_passthrough_test.go:14`, `backend-go/internal/providers/claude_passthrough_test.go:35`, `backend-go/internal/providers/claude_passthrough_test.go:60`, `backend-go/internal/providers/claude_passthrough_test.go:78`, `backend-go/internal/providers/claude_passthrough_test.go:95`, `backend-go/internal/providers/claude_passthrough_test.go:112`, `backend-go/internal/providers/claude_passthrough_test.go:137`, `backend-go/internal/providers/claude_passthrough_test.go:143`).
- Handler auth override tests exist for Chat, Gemini, Images, and Responses compact custom headers, matching the backend spec's upstream header override contract (`backend-go/internal/handlers/chat/header_override_handler_test.go:11`, `backend-go/internal/handlers/gemini/header_override_test.go:20`, `backend-go/internal/handlers/images/header_override_test.go:19`, `backend-go/internal/handlers/responses/header_override_test.go:12`).
- Admin/model-list paths mostly create fresh requests and do not clone inbound proxy-client headers. Messages/Chat/Responses use only `Authorization` and `Content-Type`; Gemini uses `x-goog-api-key` and `Content-Type` (`backend-go/internal/handlers/messages/channels.go:605`, `backend-go/internal/handlers/messages/channels.go:611`, `backend-go/internal/handlers/chat/channels.go:533`, `backend-go/internal/handlers/responses/channels.go:568`, `backend-go/internal/handlers/gemini/channels.go:613`).
- Images model-list request accepts `customHeaders`, sets selected `Authorization`, then applies custom headers, so custom `Authorization` can override the supplied/validated key on that admin path (`backend-go/internal/handlers/images/channels.go:375`, `backend-go/internal/handlers/images/channels.go:381`, `backend-go/internal/handlers/images/channels.go:485`, `backend-go/internal/handlers/images/channels.go:487`).
- Capability-test request builder sets selected auth/protocol headers first and then applies `channel.CustomHeaders`, so custom auth-like headers can override selected keys and protocol-specific synthetic User-Agent values on capability tests (`backend-go/internal/handlers/capability_test_handler.go:1209`, `backend-go/internal/handlers/capability_test_handler.go:1212`, `backend-go/internal/handlers/capability_test_handler.go:1220`, `backend-go/internal/handlers/capability_test_handler.go:1226`, `backend-go/internal/handlers/capability_test_handler.go:1230`, `backend-go/internal/handlers/capability_test_handler.go:1233`).
- Background models health check creates fresh requests and does not use inbound headers or custom headers (`backend-go/internal/config/config_models_health_check.go:213`, `backend-go/internal/config/config_models_health_check.go:217`, `backend-go/internal/config/config_models_health_check.go:218`).
- Runtime logging masks `Authorization`, `x-api-key`, and `x-goog-api-key`, but does not mask `Cookie` or `Proxy-Authorization` if such headers survive to request/original-header diagnostics (`backend-go/internal/utils/json.go:766`, `backend-go/internal/utils/json.go:768`, `backend-go/internal/utils/json.go:771`, `backend-go/internal/handlers/common/request.go:156`, `backend-go/internal/handlers/common/request.go:210`).

### Security Risks

- Sensitive inbound credential leak: because proxy builders clone inbound headers, client cookies, proxy credentials, or unrelated bearer/API-key headers can be forwarded to arbitrary configured upstreams unless later auth setters happen to replace that exact header name.
- Hop-by-hop header confusion: `Connection`, `Keep-Alive`, `TE`, `Trailer`, `Transfer-Encoding`, `Upgrade`, `Proxy-Connection`, and `Expect` are not stripped by the shared helper. These should not be forwarded by application-layer proxy code.
- Cross-provider metadata leak: non-Claude provider paths also use `PrepareUpstreamHeaders`, so Anthropic/Claude-Code/client metadata headers and browser User-Agent values can reach OpenAI/Gemini/Responses upstreams. This may be desired for transparent passthrough, but it should be an explicit contract.
- Logging exposure: if future diagnostics log original or actual upstream headers with `Cookie`/`Proxy-Authorization`, current masking does not redact them.
- Custom-header override gaps are not inbound-header leaks, but they violate the existing "selected auth wins" contract on capability tests and Images model-list preview. These are admin/test paths, not normal user proxy passthrough, but they can produce false capability/model results or send a different credential than the UI-selected key.

### Recommended Final Contract

- Keep User-Agent passthrough as default for production proxy requests: inbound `User-Agent` should be forwarded when present, and channel `customHeaders["User-Agent"]` should override inbound User-Agent because custom headers are admin-configured upstream metadata.
- Keep Claude fallback behavior centralized: when the final upstream service/protocol needs Claude compatibility and no User-Agent remains after base passthrough plus custom headers, set the existing Claude CLI default. Apply this consistently to Claude-targeted provider/handler paths, not only `ClaudeProvider`.
- Strip sensitive inbound headers in the base header helper before custom headers and before final auth. Minimum blocklist:
  - Auth/credential: `Authorization`, `Proxy-Authorization`, `x-api-key`, `x-goog-api-key`, `Cookie`, `Set-Cookie`.
  - Proxy access/internal: `x-proxy-key`.
  - Client/proxy identity: `X-Forwarded-For`, `X-Forwarded-Host`, `X-Forwarded-Proto`, `X-Real-IP`, `Forwarded`, `Via`, plus common vendor IP headers if accepted by this proxy surface (`CF-Connecting-IP`, `True-Client-IP`, `X-Client-IP`, `X-Cluster-Client-IP`).
  - Hop-by-hop: `Connection`, `Keep-Alive`, `Proxy-Connection`, `TE`, `Trailer`, `Transfer-Encoding`, `Upgrade`, `Expect`.
  - Compression: keep current `Accept-Encoding` deletion so Go transport can manage decompression behavior.
- Do not treat channel `customHeaders` as inbound headers. They are admin-configured and should remain available for upstream metadata, but selected auth must always be applied last so `Authorization`, `x-api-key`, and `x-goog-api-key` cannot replace the selected key.
- Centralize the stripping policy in `backend-go/internal/utils/headers.go`; avoid duplicated policy in Images. A helper such as `PrepareUpstreamHeaders` plus request-specific `Content-Type` handling is enough, or add an options struct only if Images multipart content-type needs it.
- Remove or repurpose `PrepareMinimalHeaders` if implementation confirms it remains unused. Its comment says non-Claude should be minimal, but production code currently does not follow it; keeping a dead helper creates misleading guidance.
- Align diagnostic masking with the final sensitive header strip list, especially `Cookie` and `Proxy-Authorization`, even if those should no longer reach upstream.

### Config / API / UI Need

- No new persisted config or UI switch is recommended. Header stripping is a security invariant, not a channel option.
- Do not add per-channel passthrough toggles. Backend spec explicitly says passthrough compatibility switches were removed and passthrough should be internally decided from API format consistency (`.trellis/spec/backend/quality-guidelines.md`, Passthrough Decision Contracts; `.trellis/spec/frontend/type-safety.md`, Passthrough Field Removal).
- Existing `customHeaders` remains the admin escape hatch for upstream-required metadata. Backend config exposes it on `UpstreamConfig` and `UpstreamUpdate` (`backend-go/internal/config/config.go:21`, `backend-go/internal/config/config.go:37`, `backend-go/internal/config/config.go:302`, `backend-go/internal/config/config.go:305`), frontend `Channel` and `ChannelModelsRequest` expose it (`frontend/src/services/api.ts:168`, `frontend/src/services/api.ts:184`, `frontend/src/services/api.ts:653`, `frontend/src/services/api.ts:658`), and channel payload creation sends it (`frontend/src/utils/channelPayload.ts:21`, `frontend/src/utils/channelPayload.ts:78`).
- UI does not need a dedicated User-Agent field; admins can already set `User-Agent` through the custom-header UI (`frontend/src/components/AddChannelModal.vue:985`, `frontend/src/components/AddChannelModal.vue:1022`, `frontend/src/components/AddChannelModal.vue:1046`).
- Existing frontend `ChannelModelsRequest.customHeaders` is broader than most backend model-list handlers consume. Images backend consumes it; Messages/Chat/Responses/Gemini model-list request structs currently only accept key/baseUrl and ignore custom headers. If model-list preview header parity matters, fix backend model-list request structs/builders; no new frontend payload field is needed.

### Implementation Files

- Primary: `backend-go/internal/utils/headers.go`
  - Add a single sensitive/hop-by-hop inbound strip policy.
  - Ensure final helper preserves safe `User-Agent`.
  - Consider a helper for request-specific content type, or adapt `PrepareUpstreamHeaders` so Images can share it without losing multipart boundaries.
- Tests: `backend-go/internal/utils/headers_test.go`
  - Add table cases proving inbound `Authorization`, `x-api-key`, `x-goog-api-key`, `Cookie`, `Proxy-Authorization`, `Connection`, `TE`, `Upgrade`, and vendor IP headers are stripped.
  - Add cases proving inbound/custom `User-Agent` survives, and missing Claude User-Agent receives default only in Claude-compatible paths.
- Images proxy: `backend-go/internal/handlers/images/handler.go`
  - Replace `prepareImagesUpstreamHeaders` or make it delegate to the shared strip helper while preserving request-specific content type.
- Direct handlers: `backend-go/internal/handlers/chat/handler.go`, `backend-go/internal/handlers/gemini/handler.go`, `backend-go/internal/handlers/responses/compact.go`
  - Keep custom headers before final auth.
  - Apply Claude User-Agent fallback consistently where the upstream service type is Claude and compatibility requires it.
- Provider builders: `backend-go/internal/providers/claude.go`, `backend-go/internal/providers/openai.go`, `backend-go/internal/providers/gemini.go`, `backend-go/internal/providers/responses.go`
  - They should automatically inherit central inbound stripping through `PrepareUpstreamHeaders`.
  - Re-check `ResponsesProvider` duplicate auth deletion after central strip; it may become redundant but harmless.
- Admin/test/probe builders: `backend-go/internal/handlers/capability_test_handler.go`, `backend-go/internal/handlers/images/channels.go`
  - Move `customHeaders` application before selected auth, or use the same `ApplyCustomHeaders` then final auth pattern as production proxy builders.
- Diagnostics: `backend-go/internal/utils/json.go`
  - Expand `MaskSensitiveHeaders` to mask every sensitive header that the strip helper blocks.

### Tests

- Required focused tests:
  - `go test ./internal/utils`
  - `go test ./internal/providers`
  - `go test ./internal/handlers/chat`
  - `go test ./internal/handlers/gemini`
  - `go test ./internal/handlers/images`
  - `go test ./internal/handlers/responses`
- Add/extend regression tests:
  - `backend-go/internal/utils/headers_test.go`: central strip list, User-Agent passthrough, Claude default fallback.
  - `backend-go/internal/handlers/images/handler_test.go` or a new header-specific test: Images JSON and multipart paths strip sensitive inbound headers while preserving multipart content type.
  - Existing auth override tests for Chat/Gemini/Images/Responses compact should continue to pass and should gain inbound auth/cookie stripping assertions where they inspect actual upstream requests.
  - `backend-go/internal/handlers/capability_test_handler.go` tests should assert channel `customHeaders` cannot override selected capability-test auth.
  - `backend-go/internal/handlers/images/channels.go` tests should assert model-list `customHeaders.Authorization` cannot override the selected/validated key.
  - If backend model-list parity is implemented, add model-list tests for Messages/Chat/Responses/Gemini custom headers and selected-auth precedence.

### Code Patterns

- Base passthrough pattern: clone inbound headers, strip proxy-only fields, set platform-controlled fields, apply custom headers, then set final auth (`backend-go/internal/utils/headers.go:106`, `backend-go/internal/providers/claude.go:84`, `backend-go/internal/providers/claude.go:85`, `backend-go/internal/providers/claude.go:86`).
- Final auth wins pattern: `SetAuthenticationHeader` and `SetGeminiAuthenticationHeader` delete conflicting auth-like headers before setting selected key (`backend-go/internal/utils/headers.go:146`, `backend-go/internal/utils/headers.go:148`, `backend-go/internal/utils/headers.go:150`, `backend-go/internal/utils/headers.go:164`, `backend-go/internal/utils/headers.go:165`, `backend-go/internal/utils/headers.go:167`).
- User-Agent fallback pattern: `EnsureCompatibleUserAgent` preserves existing `User-Agent` and only injects Claude default when missing (`backend-go/internal/utils/headers.go:184`, `backend-go/internal/utils/headers.go:188`, `backend-go/internal/utils/headers.go:191`).
- Duplicate Images header pattern: local helper mirrors central proxy stripping but is easy to drift (`backend-go/internal/handlers/images/handler.go:386`, `backend-go/internal/handlers/images/handler.go:389`, `backend-go/internal/handlers/images/handler.go:396`).
- Admin/test auth-order exception: capability tests and Images model-list apply custom headers after auth (`backend-go/internal/handlers/capability_test_handler.go:1212`, `backend-go/internal/handlers/capability_test_handler.go:1230`, `backend-go/internal/handlers/images/channels.go:485`, `backend-go/internal/handlers/images/channels.go:487`).

### External References

- No external web references were required; this was an internal codebase research pass.
- Local version references: Go module declares `go 1.25` and `github.com/gin-gonic/gin v1.11.0` in `backend-go/go.mod`; frontend uses Vue `^3.5.32`, Vuetify `^4.0.5`, Vite `^8.0.8`, and Vitest `^4.1.4` in `frontend/package.json`.

### Related Specs

- `.trellis/spec/backend/index.md` - backend pre-development checklist.
- `.trellis/spec/backend/directory-structure.md` - shared helpers belong under `internal/utils`, protocol handlers under `internal/handlers/<protocol>`.
- `.trellis/spec/backend/logging-guidelines.md` - do not log secrets or raw credentials.
- `.trellis/spec/backend/quality-guidelines.md` - upstream header override contract, passthrough decision contracts, admin/probe key contracts, tests required.
- `.trellis/spec/frontend/index.md` - frontend pre-development checklist.
- `.trellis/spec/frontend/type-safety.md` - central API contracts and passthrough field removal.
- `.trellis/spec/frontend/directory-structure.md` - API contracts in `src/services/api.ts`, payload normalization in `src/utils/channelPayload.ts`.

## Caveats / Not Found

- `task.py current --source` returned no active task in this session, so this research used the explicit task path supplied by the user: `.trellis/tasks/05-06-axonhub-passthrough-followup`.
- No production code was edited and no tests were run; this file is research only.
- I did not inspect every line of every channel CRUD handler. The relevant header/config paths were searched by symbol and targeted line ranges.
- There is no dedicated User-Agent config field in backend or frontend; only generic `customHeaders` exists.
- `PrepareMinimalHeaders` is unused. If the intended future contract is truly minimal non-Claude headers rather than User-Agent passthrough, implementation should update the PRD/contract first because current production behavior and tests lean passthrough.
