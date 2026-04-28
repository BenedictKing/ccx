# State Management

> How state is managed in this project.

---

## Overview

The frontend uses Pinia setup stores for shared application state.
Local component state still lives in `ref`/`reactive` when it is form-specific or dialog-specific.
Server state is fetched imperatively through `ApiService` and cached in stores instead of using Vue Query or similar libraries.

---

## State Categories

- Local component state:
  forms, dialog visibility, temporary errors, chart UI preferences.
Examples:
  `frontend/src/components/AddChannelModal.vue`,
  `frontend/src/components/CapabilityTestDialog.vue`.
- Global application state:
  auth, preferences, system flags, channel datasets.
Examples:
  `frontend/src/stores/auth.ts`,
  `frontend/src/stores/preferences.ts`,
  `frontend/src/stores/channel.ts`,
  `frontend/src/stores/system.ts`.
- Route state:
  current channel type is derived from the router, then mirrored into store state when needed.
Example:
  `currentChannelType` watcher in `frontend/src/stores/channel.ts`.
- Server state:
  fetched through `api` service methods and cached in the relevant store.

---

## When to Use Global State

Promote state to Pinia when at least one of these is true:

- Multiple components need the same state
- The state survives route changes
- The state coordinates network refresh/polling
- The state needs persistence

Examples:

- Auth persistence and lockout state: `frontend/src/stores/auth.ts`
- Channel dashboard cache and auto-refresh: `frontend/src/stores/channel.ts`
- UI preferences persisted across sessions: `frontend/src/stores/preferences.ts`

Keep state local when it only affects one component instance.

---

## Server State

- All backend requests should go through `src/services/api.ts`.
- Stores own refresh timing, caching, and optimistic local merges.
Examples:
  `refreshChannels()` and `mergeChannelsWithLocalData()` in `frontend/src/stores/channel.ts`.
- The project uses manual cache structures instead of a separate query library.
Example:
  `dashboardCache` keyed by API tab in `frontend/src/stores/channel.ts`.
- Persist only the minimal shared fields that need session continuity.
Example:
  `persist.pick` in `frontend/src/stores/auth.ts`.

---

## Common Mistakes

- Do not duplicate the same shared state in both a store and several sibling components.
- Do not persist transient UI state such as loading spinners or dialog open flags.
- Do not fetch the same backend resource separately in many components if a store already aggregates it.
- Do not keep route-derived state disconnected from the router; follow the existing route-to-store sync pattern.
