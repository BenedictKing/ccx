# Quality Guidelines

> Code quality standards for backend development.

---

## Overview

Backend quality in this repo comes from small focused packages, explicit error handling, and strong test coverage around config, converters, handlers, scheduler, and metrics.
Changes should follow the existing shape instead of introducing new architectural layers for small features.

---

## Forbidden Patterns

- Do not add backward-compatibility-only branches for old formats unless the current code still reads them intentionally.
- Do not hide business logic in `main.go`; keep it in `internal/*`.
- Do not introduce broad abstraction layers such as `repository`, `service`, or `manager` unless the existing code already has a natural home.
- Do not ignore errors from file IO, JSON parsing, HTTP, or request binding.
- Do not log secrets or raw credentials.
- Do not make one protocol family diverge from the others without a real protocol reason.

---

## Required Patterns

- Run `go fmt ./...` after backend edits.
- Prefer focused helpers and ownership by package.
- Keep handler-level request validation close to the HTTP boundary.
- Use contextual error wrapping with `%w` where upstream callers need the cause.
- Add tests next to the changed package when behavior changes.
- Reuse existing normalization and masking helpers before writing new ones.

Examples:

- Config behavior locked by tests: `backend-go/internal/config/config_baseurl_test.go`
- Table-driven unit tests: `backend-go/internal/providers/url_builder_test.go`
- HTTP-focused tests with `httptest`: `backend-go/internal/middleware/auth_test.go`

---

## Testing Requirements

- Backend changes should usually come with `_test.go` coverage unless the change is documentation-only.
- Prefer table-driven tests for pure logic.
- Use `httptest` for handler and middleware behavior.
- Keep regression tests beside the package that owns the logic.

Common commands:

- `cd backend-go && make test`
- `cd backend-go && make test-cover`
- `cd backend-go && make lint`

Examples:

- Config regression coverage: `backend-go/internal/config/config_baseurl_test.go`
- Scheduler coverage: `backend-go/internal/scheduler/channel_scheduler_test.go`
- Converter coverage: `backend-go/internal/converters/responses_converter_test.go`

---

## Code Review Checklist

- Is the change in the right package, or did it add logic to the wrong layer?
- Are all new/changed errors mapped to sensible HTTP status codes?
- Are secrets masked in logs and responses?
- If config or persisted shape changed, were defaulting/migration/tests updated too?
- If behavior exists for Messages, Responses, Gemini, and Chat, were all relevant channel families considered?
- Did the author reuse existing helpers before adding a new utility?
