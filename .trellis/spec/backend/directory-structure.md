# Directory Structure

> How backend code is organized in this project.

---

## Overview

The backend is a single Go service in `backend-go/`.
`main.go` wires middleware, route groups, config, scheduler, metrics, session management, and embedded frontend assets.
Most feature logic lives under `backend-go/internal/`, grouped by responsibility rather than by HTTP endpoint only.

---

## Directory Layout

```text
backend-go/
├── main.go
├── version.go
├── internal/
│   ├── config/        # config loading, defaults, migration, JSON persistence
│   ├── handlers/      # HTTP handlers, split by protocol plus shared helpers
│   ├── providers/     # upstream protocol adapters
│   ├── converters/    # request/response conversion across APIs
│   ├── scheduler/     # channel selection and failover logic
│   ├── metrics/       # in-memory metrics and SQLite persistence
│   ├── session/       # responses session state and trace affinity
│   ├── middleware/    # auth, CORS, logger, gzip
│   ├── httpclient/    # outbound HTTP client construction
│   ├── logger/        # standard log setup with rotation
│   ├── utils/         # focused helpers such as JSON, compression, headers
│   ├── warmup/        # URL health/order management
│   └── types/         # shared request/response structs
└── frontend/dist/     # built UI copied here and embedded by Go
```

---

## Module Organization

- Keep HTTP wiring in `main.go`, but move behavior into `internal/*`.
- Put protocol-specific admin handlers under `internal/handlers/<protocol>/`.
Examples:
  `internal/handlers/messages/channels.go`,
  `internal/handlers/responses/handler.go`,
  `internal/handlers/gemini/handler.go`.
- Put code shared by multiple handler families under `internal/handlers/common/`.
Examples:
  `internal/handlers/common/request.go`,
  `internal/handlers/common/stream.go`,
  `internal/handlers/common/failover.go`.
- Put config mutation and normalization in `internal/config/`, not in handlers.
Examples:
  `internal/config/config_loader.go`,
  `internal/config/config_messages.go`,
  `internal/config/config_responses.go`.
- Put upstream HTTP payload conversion in `internal/providers/` or `internal/converters/` depending on whether the logic is protocol transport or schema translation.
- Keep persistence-specific code inside the owning module instead of creating a generic data layer. SQLite storage is owned by `internal/metrics/sqlite_store.go`.

Avoid these patterns:

- Catch-all packages such as `helpers`, `misc`, or `service`
- Feature logic staying in `main.go` after it outgrows bootstrap wiring
- Handlers mutating config structs directly instead of going through `ConfigManager`

---

## Naming Conventions

- Files use `snake_case.go`.
Examples:
  `channel_scheduler.go`,
  `config_loader.go`,
  `trace_affinity.go`.
- Exported types and functions use `PascalCase`; internal helpers use `camelCase`.
- Protocol variants are usually expressed as sibling files or sibling packages instead of flags everywhere.
Examples:
  `handlers/messages`,
  `handlers/responses`,
  `handlers/chat`,
  `handlers/gemini`.
- Tests stay beside production files with `_test.go`.

---

## Examples

- Bootstrap and route composition: `backend-go/main.go`
- Config-owned mutations: `backend-go/internal/config/config_messages.go`
- Shared handler helpers: `backend-go/internal/handlers/common/stream.go`
- Module-owned persistence: `backend-go/internal/metrics/sqlite_store.go`
