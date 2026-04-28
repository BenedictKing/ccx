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

---

## Forbidden Patterns

- Do not introduce new `any` types unless there is a strong boundary reason and no better `unknown`-first option.
- Do not duplicate API contract types across components.
- Do not normalize payload shapes inline in large components when a typed utility function can own the logic.
- Do not weaken existing unions to `string` just to avoid fixing call sites.

Note:

- `ApiService.request()` currently returns `any` as a legacy convenience point in `src/services/api.ts`.
  Treat that as an existing exception, not a pattern to copy.
