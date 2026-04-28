# Backend Development Guidelines

> Best practices for backend development in this project.

---

## Overview

These guidelines document the current Go backend conventions used in `backend-go/`.
They are based on the existing codebase, not aspirational rules.

---

## Guidelines Index

| Guide | Description | Status |
|-------|-------------|--------|
| [Directory Structure](./directory-structure.md) | Module organization and file layout | Ready |
| [Database Guidelines](./database-guidelines.md) | SQLite persistence and config storage patterns | Ready |
| [Error Handling](./error-handling.md) | Error types, handling strategies | Ready |
| [Quality Guidelines](./quality-guidelines.md) | Code standards, forbidden patterns | Ready |
| [Logging Guidelines](./logging-guidelines.md) | Logging setup, tags, and redaction rules | Ready |

---

## Pre-Development Checklist

Read these files before changing backend code:

- `directory-structure.md`
- `error-handling.md`
- `logging-guidelines.md`
- `quality-guidelines.md`

Read `database-guidelines.md` as well when touching:

- `internal/metrics/sqlite_store.go`
- `.config/config.json` persistence
- config migration/defaulting code under `internal/config/`

Also read shared guides:

- `../guides/index.md`

---

**Language**: All documentation should be written in **English**.
