# Directory Structure

> How frontend code is organized in this project.

---

## Overview

The frontend is a Vue 3 application in `frontend/src/`.
It is organized by responsibility: app bootstrap, route shell, stores, service contracts, UI components, and utility modules.
Most shared domain types currently live in `src/services/api.ts`, while reusable pure logic lives in `src/utils/`.

---

## Directory Layout

```text
frontend/src/
├── main.ts           # app bootstrap, Pinia, router, Vuetify
├── App.vue           # top-level shell and cross-feature orchestration
├── views/            # route-level wrappers
├── components/       # reusable UI and feature-heavy components
├── stores/           # Pinia setup stores
├── services/         # API client and typed contracts
├── utils/            # pure helpers and payload normalization
├── composables/      # lightweight shared stateful helpers
├── i18n/             # locale resolution and translation helpers
├── plugins/          # Vuetify setup, theme, icon registration
├── router/           # route definitions
├── styles/           # SCSS settings
└── assets/           # global CSS and static assets
```

---

## Module Organization

- Keep route wrappers thin.
Example:
  `frontend/src/views/ChannelsView.vue` mainly derives route type and renders the main surface.
- Put app-wide state and server refresh orchestration in Pinia stores.
Examples:
  `frontend/src/stores/channel.ts`,
  `frontend/src/stores/auth.ts`,
  `frontend/src/stores/system.ts`.
- Put API contracts and fetch logic in `src/services/api.ts`, not directly inside components.
- Put pure transformation logic in `src/utils/` with tests beside it.
Examples:
  `frontend/src/utils/channelPayload.ts`,
  `frontend/src/utils/channelAdvancedOptions.ts`,
  `frontend/src/utils/quickInputParser.ts`.
- Keep `src/composables/` small. This project uses only a few shared stateful helpers, not a composable-heavy architecture.
- Keep Vuetify registration and icon/theme rules centralized in `src/plugins/vuetify.ts`.

Avoid these patterns:

- Duplicating API calls in multiple components
- Creating a new store for state that only one component owns
- Mixing icon registration logic into component files

---

## Naming Conventions

- Vue components use `PascalCase.vue`.
Examples:
  `AddChannelModal.vue`,
  `ChannelStatusBadge.vue`,
  `CapabilityTestDialog.vue`.
- Store files use short lowercase nouns such as `auth.ts`, `channel.ts`, `preferences.ts`.
- Composables use `use*.ts`.
Examples:
  `useTheme.ts`.
- Utility filenames are mostly descriptive camelCase or multi-word hyphenated names, depending on the existing file family.
Examples:
  `channelPayload.ts`,
  `channelAdvancedOptions.ts`,
  `add-channel-modal-state.ts`.
- Use the `@/` alias for internal imports whenever the file is under `src/`.

---

## Examples

- Bootstrap and plugin wiring: `frontend/src/main.ts`
- Central API contract module: `frontend/src/services/api.ts`
- Feature-heavy UI surface: `frontend/src/components/ChannelOrchestration.vue`
- Pure, tested transformation module: `frontend/src/utils/channelPayload.ts`
