# Logging Guidelines

> How logging is done in this project.

---

## Overview

The backend uses the standard library `log` package.
Output is configured once in `internal/logger/logger.go`, with lumberjack-based file rotation and optional console mirroring.
Log structure is convention-based rather than enforced by a structured logger library.

---

## Log Levels

- The codebase does not use typed log levels in the logger API.
- Severity is expressed through message prefixes and wording such as:
  `Warning`, `Warn`, `Fatal`, `Init`, `Shutdown`, `Error`.
- Use `log.Printf` for most operational events.
- Use `log.Fatal` / `log.Fatalf` only for startup failures that must terminate the process.
Examples:
  `backend-go/main.go`,
  `backend-go/internal/logger/logger.go`.
- Respect runtime log suppression helpers where present.
Examples:
  `envCfg.ShouldLog(...)` and `QuietPollingLogs` in `backend-go/internal/middleware/auth.go` and `backend-go/internal/middleware/logger.go`.

---

## Structured Logging

- Every log line should start with a bracketed component tag.
Preferred format:

```go
log.Printf("[Component-Action] message: %v", value)
```

- Existing tag families include:
  `[Config-*]`,
  `[Scheduler-*]`,
  `[Messages-*]`,
  `[Responses-*]`,
  `[Gemini-*]`,
  `[Auth-*]`,
  `[Metrics-*]`,
  `[Server-*]`.
- Include operational context that helps debugging:
  channel id/name, status code, model, duration, API family, or migration version.
- Do not add emoji or decorative prefixes.

Examples:

- Startup/shutdown: `backend-go/main.go`
- Auth logging: `backend-go/internal/middleware/auth.go`
- Config watcher/migration: `backend-go/internal/config/config_loader.go`
- Scheduler decisions: `backend-go/internal/scheduler/channel_scheduler.go`

---

## What to Log

- Startup and shutdown lifecycle.
- Config migration, reload, and backup events.
- Scheduler choices, fallback, circuit-breaker transitions, and promotion windows.
- Upstream request/response diagnostics when they explain failures or protocol mismatches.
- Capability-test job lifecycle and metrics persistence events.

Examples:

- Session and metrics init: `backend-go/main.go`
- Circuit state changes: `backend-go/internal/metrics/channel_metrics.go`
- Capability-test execution: `backend-go/internal/handlers/capability_test_handler.go`
- Model fetch diagnostics: `backend-go/internal/handlers/messages/channels.go`

---

## What NOT to Log

- Never log full API keys.
Use helpers such as `utils.MaskAPIKey(...)`.
- Never log raw proxy credentials.
Use `utils.RedactURLCredentials(...)`.
- Do not dump noisy polling/auth success logs when `QuietPollingLogs` should suppress them.
- Avoid raw request/response body dumping unless the code path already treats it as an intentional diagnostic mode.

Examples of safe redaction:

- `backend-go/internal/config/config_messages.go`
- `backend-go/internal/handlers/messages/models.go`
- `backend-go/internal/httpclient/client.go`
