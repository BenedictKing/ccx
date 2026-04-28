# Error Handling

> How errors are handled in this project.

---

## Overview

The backend mainly uses plain `error` values with contextual wrapping via `fmt.Errorf`.
Handlers translate those errors into HTTP responses close to the edge.
There is no large custom error hierarchy; most consistency comes from repeated handler patterns and a few sentinel errors in shared packages.

---

## Error Types

- Prefer plain errors plus `%w` wrapping.
Examples:
  `backend-go/internal/converters/responses_converter.go`,
  `backend-go/internal/providers/gemini.go`,
  `backend-go/internal/handlers/gemini/handler.go`.
- Use sentinel errors only where shared control flow needs them.
Examples:
  `ErrEmptyStreamResponse` and `ErrInvalidResponseBody` in `backend-go/internal/handlers/common/stream.go`.
- Some protocol handlers return typed JSON bodies for protocol compatibility instead of generic `gin.H`.
Example:
  `types.GeminiError` responses in `backend-go/internal/handlers/gemini/handler.go`.

---

## Error Handling Patterns

- Validate request path/body early and return `400` immediately on malformed input.
Examples:
  `backend-go/internal/handlers/messages/channels.go`,
  `backend-go/internal/handlers/settings.go`,
  `backend-go/internal/handlers/capability_test_handler.go`.
- Translate config-layer errors to HTTP status at the handler boundary.
Common pattern:
  invalid index -> `404`,
  validation failure -> `400`,
  save/load failure -> `500`.
- Wrap lower-level errors with context instead of replacing them silently.
Examples:
  `fmt.Errorf("create request failed: %w", err)`,
  `fmt.Errorf("%w: %v", common.ErrInvalidResponseBody, err)`.
- Log operational detail on the server, but keep client responses short and stable.
- When upstream returns a body that the admin UI needs, pass through safe detail intentionally.
Example:
  model-fetch flows in `backend-go/internal/handlers/messages/channels.go`.

---

## API Error Responses

- Most admin/API endpoints return:

```go
gin.H{"error": "..."}
```

- Success payloads often use:

```go
gin.H{"message": "...", "success": true}
```

- Keep the response shape simple; this codebase does not use a global error envelope with codes everywhere.
- Use status codes consistently:
  `400` invalid request input,
  `401` auth failure,
  `404` missing channel/job,
  `500` local processing/save failure,
  `502/503` upstream or availability failure.

Examples:

- Auth failure: `backend-go/internal/middleware/auth.go`
- Admin CRUD validation: `backend-go/internal/handlers/messages/channels.go`
- Failover response shaping: `backend-go/internal/handlers/common/failover.go`

---

## Common Mistakes

- Do not ignore `ShouldBindJSON`, `strconv.Atoi`, `io.ReadAll`, or `client.Do` errors.
- Do not leak raw secrets in error strings or logs; use masking helpers when keys/URLs appear.
- Do not let handlers build business rules by string-matching every error ad hoc if the same mapping already exists elsewhere.
- Do not return `500` for user input errors just because the config layer returned a plain `error`; inspect the cause and map it properly.
- Do not log and return the same noisy detail for expected client disconnects; shared stream code already treats some disconnect cases as expected.
