# Hook Guidelines

> How hooks are used in this project.

---

## Overview

This is a Vue project, so "hooks" means composables and similar `use*` helpers.
The codebase uses them sparingly.
Shared stateful logic usually goes into Pinia stores, while pure data shaping goes into `src/utils/`.

---

## Custom Hook Patterns

- Use composables for lightweight shared stateful behavior or app wiring, not as the default place for all shared code.
Examples:
  `frontend/src/composables/useTheme.ts`,
  `frontend/src/i18n/index.ts` (`useI18n`).
- If the logic is only formatting, normalization, or payload construction, keep it in `src/utils/` instead.
Examples:
  `frontend/src/utils/channelPayload.ts`,
  `frontend/src/utils/channelAdvancedOptions.ts`.
- If the logic owns app-wide mutable state or polling, prefer a Pinia store.
Example:
  `frontend/src/stores/channel.ts`.

---

## Data Fetching

- Data fetching is not composable-driven in this project.
- Use `src/services/api.ts` as the network boundary.
- Put refresh/caching/orchestration behavior in stores.
Example:
  `refreshChannels()` and dashboard caching in `frontend/src/stores/channel.ts`.
- Components generally consume store state or service helpers, not raw `fetch`.

Exceptions that still belong in the service layer:

- `fetchUpstreamModels(...)` in `src/services/api.ts`
- `fetchHealth()` in `src/services/api.ts`

---

## Naming Conventions

- Composables must use the `use*` prefix.
Examples:
  `useAppTheme`,
  `useI18n`.
- File names should match the exported composable purpose.
Example:
  `useTheme.ts`.
- Keep composable public APIs small and explicit. Return only what callers actually need.

---

## Common Mistakes

- Do not create a composable when a Pinia store already owns the same state.
- Do not put API fetch orchestration into many ad hoc composables; reuse `ApiService` and stores.
- Do not store pure stateless helpers in composables just to get a `use*` name.
- Do not let composables become hidden global state containers when Pinia would make ownership clearer.
