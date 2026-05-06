# Type Safety

> Type safety patterns in this project.

---

## Overview

TypeScript runs in `strict` mode in this frontend.
The codebase relies on explicit interfaces, discriminated unions, and imported contract types more than on runtime schema libraries.
Runtime validation is mostly manual at boundaries; compile-time safety is the primary tool inside the app.

---

## Type Organization

- Shared API-facing types live centrally in `src/services/api.ts`.
Examples:
  `Channel`,
  `ChannelMetrics`,
  `CapabilityTestJob`,
  `ChannelStatus`.
- Local form/payload helper types live near the helper that owns them.
Example:
  `ChannelFormLike` in `src/utils/channelPayload.ts`.
- Small view-only unions are defined inside the component when they are not shared.
Example:
  `DisplayStatus` in `src/components/ChannelStatusBadge.vue`.

---

## Validation

- There is no Zod/Yup/io-ts layer in the current frontend.
- Validate and normalize data with explicit functions at the boundary.
Examples:
  `buildChannelPayload()` in `src/utils/channelPayload.ts`,
  `normalizeAdvancedChannelOptions()` in `src/utils/channelAdvancedOptions.ts`.
- Service-layer response parsing should treat unknown server payloads carefully.
Example:
  `parseResponseBody()` and `ApiError.details?: unknown` in `src/services/api.ts`.

---

## Common Patterns

- Use union types for limited backend/frontend states.
Examples:
  `ChannelStatus`,
  `CircuitState`,
  capability lifecycle/status unions in `src/services/api.ts`.
- Use imported types in components instead of re-declaring the same contract locally.
Example:
  `ChannelStatusBadge.vue` imports `ChannelStatus` and `ChannelMetrics`.
- Use typed translation helpers instead of raw string maps where possible.
Example:
  parameter typing in `useI18n()` from `src/i18n/index.ts`.

### Channel Workflow Contracts

When adding a managed channel kind, update the full frontend contract set together:

- `ManagedChannelType` in `src/utils/channelTypeApi.ts`
- route-derived channel type unions in route/views/components that receive `channelType` or `apiType`
- `ApiService` methods for dashboard, metrics, reorder, status, resume, promotion, ping, key, and model-list operations
- Pinia channel store data bucket, dashboard cache bucket, save/delete/ping routing, and clear-state routing
- tab labels in `src/i18n/messages.ts`

Payload creation stays in `src/utils/channelPayload.ts`. Persistent channel payloads must not include capability-test-only controls such as transient RPM. Capability test RPM belongs in `StartCapabilityTestOptions` and is sent only to capability-test endpoints.

Model-list preview requests use `ChannelModelsRequest`:

```typescript
export interface ChannelModelsRequest {
  key: string
  baseUrl?: string
  proxyUrl?: string
  insecureSkipVerify?: boolean
  customHeaders?: Record<string, string>
  baseUrls?: string[]
  routePrefix?: string
  supportedModels?: string[]
}
```

Good cases:

- Images channels use `serviceType: 'openai'` and `/images/*` management endpoints.
- Temporary model-query settings include proxy, TLS, headers, route prefix, base URLs, and supported model filters.
- Claude-only passthrough/failover fields are normalized to safe defaults for non-Claude services.

Bad cases:

- Adding a tab only to `App.vue` without store/API/type/test coverage.
- Sending `rpm` in channel create/update payloads.
- Re-declaring channel-kind unions locally without updating shared service/store contracts.

### Passthrough Field Removal

Frontend channel contracts must not expose passthrough compatibility switches. Backend passthrough is decided internally from API format consistency.

Removed channel fields:

```typescript
normalizeMetadataUserId
streamPassthroughEnabled
sub2apiPassthroughEnabled
strictRequestPassthroughEnabled
```

Contracts:

- `Channel` in `src/services/api.ts` must not include these fields.
- `buildChannelPayload()` must not send these fields for any channel type.
- Channel forms and cards must not display controls or status chips for these removed fields.
- Tests should assert these fields are absent when payload omission is relevant.
- Raw body/response passthrough, stream passthrough, and User-Agent passthrough must not be exposed as frontend toggles. Backend decides raw passthrough from API format consistency, while `customHeaders` remains the only frontend-managed upstream metadata escape hatch.
- Model-list preview `customHeaders` may include metadata such as `User-Agent`, but auth-like custom headers are not authoritative; backend must apply the selected preview key after custom headers.

---

## Forbidden Patterns

- Do not introduce new `any` types unless there is a strong boundary reason and no better `unknown`-first option.
- Do not duplicate API contract types across components.
- Do not normalize payload shapes inline in large components when a typed utility function can own the logic.
- Do not weaken existing unions to `string` just to avoid fixing call sites.

Note:

- `ApiService.request()` currently returns `any` as a legacy convenience point in `src/services/api.ts`.
  Treat that as an existing exception, not a pattern to copy.
