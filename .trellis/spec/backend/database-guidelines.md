# Database Guidelines

> Database patterns and conventions for this project.

---

## Overview

This project does not use a general ORM.
There are two persistence patterns today:

- Runtime application config is stored as JSON in `.config/config.json` and managed by `ConfigManager`.
- Metrics history is stored in SQLite in `.config/metrics.db` by `internal/metrics/sqlite_store.go`.

If you need persistence, follow one of these existing patterns instead of introducing a new abstraction by default.

---

## Query Patterns

- SQLite access is encapsulated inside `internal/metrics/sqlite_store.go`.
- Schema management is code-driven at startup, not managed by external migration tooling.
- Keep SQL close to the owning feature. There is no shared repository layer.
- Prefer explicit, narrow operations over generic query builders.
- For config persistence, use `ConfigManager` methods and JSON marshal/unmarshal rather than storing config in SQLite.

Examples:

- SQLite initialization and schema migration: `backend-go/internal/metrics/sqlite_store.go`
- Config JSON load/default/migration: `backend-go/internal/config/config_loader.go`
- Config mutation entry points: `backend-go/internal/config/config_messages.go`

---

## Migrations

- SQLite migrations are performed in code during store startup.
Examples:
  `backend-go/internal/metrics/sqlite_store.go` logs `v0 -> v1` and `v1 -> v2` migrations.
- Config format migration is also in code.
Examples:
  `migrateOldFormat()` and `applyConfigDefaults()` in `backend-go/internal/config/config_loader.go`.
- There is no backward-compatibility-first policy here. When formats change, update the code paths and migration/defaulting logic directly.

### Metrics Identity Migration Contract

When metrics keys or BaseURL identity changes, update these contracts together:

- Signatures: `metrics.GenerateMetricsIdentityKey(baseURL, apiKey, serviceType)`, `utils.MetricsIdentityBaseURL(rawURL, serviceType)`, and SQLite migration helpers in `internal/metrics/sqlite_store.go`.
- Stored fields: persisted metrics rows must use the canonical metrics identity BaseURL, while migration candidates must include equivalent legacy variants from `utils.EquivalentBaseURLVariants(...)`.
- Config inputs: migration maps may only read channel kinds that exist in `config.Config`. If a parallel lane has not landed a new channel config yet, omit that kind or add a clearly scoped compile adapter instead of assuming the full config contract exists.
- Validation matrix:
  - Base case: raw BaseURL without version prefix migrates to the service default identity URL.
  - Good case: old rows for `baseUrl`, `baseUrl/`, and `baseUrl + default version` collapse into one identity history.
  - Bad case: a channel kind with no config storage must not be included in migration maps, or startup fails at compile time.
- Tests required: SQLite migration tests must assert idempotency and preservation of equivalent BaseURL history; metrics cache/history tests must assert serviceType-aware lookup.

---

## Naming Conventions

- Keep SQLite naming descriptive and feature-owned.
Current examples:
  `circuit_states` table and metric-related columns in `backend-go/internal/metrics/sqlite_store.go`.
- JSON config keys mirror frontend/backend payload names closely, such as `baseUrl`, `baseUrls`, `modelsHealthCheckEnabled`, and `stripBillingHeader`.
- When adding a persisted field, update the owning Go structs, defaulting logic, migration logic, and tests together.

---

## Common Mistakes

- Do not introduce an ORM for one feature when the codebase currently uses explicit SQLite and JSON persistence.
- Do not duplicate the same persistent concept in both JSON config and SQLite unless the separation is intentional.
- Do not change config field shape without updating `applyConfigDefaults()`, `migrateOldFormat()`, and related tests.
- Do not update `baseUrl`/`baseUrls` semantics in one channel type only; keep Messages, Responses, Gemini, and Chat aligned.

Examples of tests that lock this behavior:

- `backend-go/internal/config/config_baseurl_test.go`
- `backend-go/internal/metrics/sqlite_store_flush_test.go`
- `backend-go/internal/metrics/sqlite_store_query_test.go`
