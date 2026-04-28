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

### Config Channel Schema Contract

When adding or changing a channel family in JSON config:

- **Signatures**: add the persisted slice to `Config`, clone it in `ConfigManager.GetConfig()`, route it through `getUpstreamSliceLocked(apiType)`, and implement channel-specific helpers beside the owning config file, such as `config_images.go`.
- **Contracts**: channel JSON keys use the same shape across families where possible: `baseUrl`, `baseUrls`, `apiKeys`, `serviceType`, `status`, `priority`, `promotionUntil`, `customHeaders`, `proxyUrl`, and `supportedModels`. Do not persist capability-test runtime controls such as request RPM as channel config fields.
- **Defaults**: `createDefaultConfig()`, `applyConfigDefaults()`, and service-type defaulting must cover every persisted channel family. Empty service type defaults are currently `claude` for Messages, `responses` for Responses, `gemini` for Gemini, and `openai` for Chat/Images.
- **Validation matrix**: active channels with no active API keys are saved as `suspended`; Images accepts only `openai` service type; invalid Images service type is normalized to `openai` during config load.
- **BaseURL rules**: canonicalize new/updated BaseURLs through `utils.CanonicalBaseURL(raw, serviceType)` and dedupe multi-URL arrays through `deduplicateBaseURLs(urls, serviceType)`. Preserve trailing `#` as explicit-version semantics.
- **Tests required**: add config package tests that load old JSON, save it again, assert removed fields are dropped, and assert new channel defaults, cloning, BaseURL canonicalization, and key validation behavior.

Wrong:

```go
type UpstreamConfig struct {
    RPM int `json:"rpm"`
}
```

Correct:

```go
type CapabilityTestRequest struct {
    RPM int `json:"rpm"`
}
```

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
