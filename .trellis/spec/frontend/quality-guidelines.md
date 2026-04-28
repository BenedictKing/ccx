# Quality Guidelines

> Code quality standards for frontend development.

---

## Overview

Frontend quality in this repo comes from strict TypeScript, central API contracts, Pinia ownership for shared state, and targeted Vitest coverage for pure logic.
The app favors practical consistency over aggressive decomposition.

---

## Forbidden Patterns

- Do not call backend endpoints directly from arbitrary components when `src/services/api.ts` should own the request.
- Do not add new Vuetify components or icons without registering them in `src/plugins/vuetify.ts`.
- Do not spread shared business rules across multiple components when a store or util already owns them.
- Do not add new `any`-heavy code to bypass strict typing.
- Do not persist transient UI state in Pinia persistence.

---

## Required Patterns

- Use `script setup` with TypeScript for Vue SFCs.
- Keep API contracts in `src/services/api.ts`.
- Put pure normalization/formatting logic in `src/utils/` and test it there.
- Use the existing `@/` alias for internal imports.
- Follow the repository formatting rules from `.prettierrc`:
  no semicolons, single quotes, trailing comma `none`, `tabWidth: 2`, `printWidth: 120`.

Examples:

- Typed, tested utility module: `frontend/src/utils/channelPayload.ts`
- Shared store orchestration: `frontend/src/stores/channel.ts`
- Central icon/theme registration: `frontend/src/plugins/vuetify.ts`

---

## Testing Requirements

- There is no broad component-test suite yet.
- For complex frontend logic, add or update Vitest tests next to the utility or module that owns the logic.
- `bun run build` is a required safety check because it runs `vue-tsc --noEmit` before Vite build.

Useful commands:

- `cd frontend && bun run build`
- `cd frontend && bun run type-check`
- `cd frontend && bun run lint`
- `cd frontend && bun run format`

Examples of existing tests:

- `frontend/src/utils/channelPayload.test.ts`
- `frontend/src/utils/channelTypeApi.test.ts`
- `frontend/src/i18n/index.test.ts`

---

## Code Review Checklist

- Is the request logic kept in `ApiService` or a justified service helper?
- Is shared mutable state in the right Pinia store instead of hidden in a component?
- If a new icon/component is used, was `src/plugins/vuetify.ts` updated too?
- Are types reused from `src/services/api.ts` where appropriate?
- If logic is non-trivial, is there a Vitest regression test in the owning module?
