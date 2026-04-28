# Component Guidelines

> How components are built in this project.

---

## Overview

Components are written with Vue SFCs and `script setup` in TypeScript.
The codebase allows fairly large feature components when the UI is domain-heavy, but still tries to keep pure transformation logic in stores or utils.
Vuetify is the primary component system, with custom theme and icon registration layered on top.

---

## Component Structure

- Use `<script setup lang="ts">`.
Examples:
  `frontend/src/components/ChannelStatusBadge.vue`,
  `frontend/src/components/CapabilityTestDialog.vue`,
  `frontend/src/App.vue`.
- Prefer this structure:
  template first,
  typed props,
  local refs/reactive state,
  computed view models,
  watchers/effects,
  helper functions,
  styles.
- Keep components focused on presentation/orchestration; move payload normalization and reusable transforms out to `src/utils/`.
Example:
  `frontend/src/components/AddChannelModal.vue` relies on `src/utils/channelPayload.ts`.

---

## Props Conventions

- Define props inline with `defineProps` and add `withDefaults` only when defaults are needed.
Example:
  `frontend/src/components/ChannelStatusBadge.vue`.
- Use narrow unions and imported API types for props whenever possible.
Examples:
  `ChannelStatus`,
  `ChannelMetrics`,
  capability job status unions from `src/services/api.ts`.
- Derived display states should be computed locally rather than pushed into the parent.
Example:
  `effectiveStatus` and `statusConfig` in `ChannelStatusBadge.vue`.

---

## Styling Patterns

- Prefer component-local `<style scoped>` for component-specific presentation.
Example:
  `frontend/src/components/ChannelStatusBadge.vue`.
- Add a second non-scoped `<style>` block only when teleport/global overlay behavior requires it.
Example:
  tooltip note at the end of `ChannelStatusBadge.vue`.
- Global visual tokens and Vuetify setup belong in:
  `src/assets/style.css`,
  `src/styles/settings.scss`,
  `src/plugins/vuetify.ts`.
- When using Vuetify components or icons, register them in `src/plugins/vuetify.ts`.
Do not assume `<v-icon>mdi-xxx</v-icon>` works unless the icon is added to `iconMap`.

---

## Accessibility

- Keep visible labels/tooltips for status-heavy UI when the interface would otherwise become icon-only.
Example:
  `ChannelStatusBadge.vue` exposes both icon and translated label.
- Preserve semantic/icon metadata in shared icon infrastructure.
Example:
  custom SVG icon renderer in `src/plugins/vuetify.ts` sets `role="img"` and `aria-hidden="true"`.
- On mobile-only compact layouts, ensure the condensed UI still retains an explanation path through tooltip or surrounding context.

---

## Common Mistakes

- Do not add a new Vuetify component and forget to register it in `src/plugins/vuetify.ts`.
- Do not add a new `mdi-*` icon string without importing it from `@mdi/js` and adding it to `iconMap`.
- Do not move reusable pure logic into a component just because the first call site is local.
- Do not fetch API data directly from a component when the same data already has a store/service owner.
