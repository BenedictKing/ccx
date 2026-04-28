# Frontend Development Guidelines

> Best practices for frontend development in this project.

---

## Overview

These guidelines document the current Vue 3 + Vuetify frontend conventions used in `frontend/`.
They describe what the codebase already does so future changes stay consistent with existing patterns.

---

## Guidelines Index

| Guide | Description | Status |
|-------|-------------|--------|
| [Directory Structure](./directory-structure.md) | Module organization and file layout | Ready |
| [Component Guidelines](./component-guidelines.md) | Component patterns, props, composition | Ready |
| [Hook Guidelines](./hook-guidelines.md) | Composable naming and ownership boundaries | Ready |
| [State Management](./state-management.md) | Pinia, local state, route state, server state | Ready |
| [Quality Guidelines](./quality-guidelines.md) | Linting, tests, and review expectations | Ready |
| [Type Safety](./type-safety.md) | TypeScript conventions and runtime boundaries | Ready |

---

## Pre-Development Checklist

Read these files before changing frontend code:

- `directory-structure.md`
- `component-guidelines.md`
- `state-management.md`
- `type-safety.md`
- `quality-guidelines.md`

Read `hook-guidelines.md` when adding or refactoring:

- `src/composables/*`
- `src/i18n/index.ts`
- shared stateful logic that might otherwise end up in a Pinia store

Also read shared guides:

- `../guides/index.md`

---

**Language**: All documentation should be written in **English**.
