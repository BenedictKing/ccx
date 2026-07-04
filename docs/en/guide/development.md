# Development Guide

This document explains how to set up CCX for local development, common commands, and the verification workflow.

> Related docs:
> - Architecture: [architecture.md](architecture.md)
> - Environment variables: [environment.md](environment.md)
> - Release process: [release.md](release.md)

## Environment Setup

### Prerequisites

| Tool | Minimum Version | Install (macOS) | Install (Linux) | Notes |
| --- | --- | --- | --- | --- |
| **Go** | 1.25 | `brew install go` | [go.dev/dl](https://go.dev/dl/) | Backend compilation and runtime |
| **Bun** | 1.x | `brew install oven-sh/bun/bun` | `curl -fsSL https://bun.sh/install \| bash` | Frontend dependency management and build |
| **Git** | - | Comes with Xcode CLT | `apt install git` / `yum install git` | Version control |
| **Make** | - | Comes with Xcode CLT | Usually pre-installed | Build scripts |

macOS users who do not have Xcode Command Line Tools yet, run:

```bash
xcode-select --install
```

### Install All Dependencies at Once

The root Makefile provides `make install` to automatically install frontend dependencies, Go modules, and development tools (Air hot-reload, Wails 3):

```bash
make install
```

### Verify Environment

```bash
go version     # should be >= 1.25
bun --version  # should be >= 1.x
make help      # confirm available commands
```

## Recommended Development Methods

| Method | Use Case | Description |
| --- | --- | --- |
| Root Makefile | Full-stack development | Starts frontend dev server and backend hot-reload simultaneously |
| backend-go Makefile | Backend-only development | Runs Go backend commands only |
| Frontend Bun | Frontend-only development | Runs Vue/Vite frontend commands only |
| Docker | Production-like verification | Not recommended as the primary hot-reload development method |

## Method 1: Root Directory (Recommended)

The root `Makefile` is the source of truth for full-stack development.

```bash
make help
make dev
make run
make frontend-dev
make build
make clean
```

Details:
- `make dev`: Starts the frontend dev server and runs the Go backend with hot-reload in `backend-go/`
- `make run`: Builds the frontend then runs the backend
- `make build`: Builds the frontend and compiles the Go backend
- `make frontend-dev`: Starts only the frontend dev server

## Method 2: backend-go Directory

The `backend-go/Makefile` is the source of truth for backend commands.

```bash
cd "backend-go"
make help
make dev
make run
make build
make test
make test-cover
make fmt
make lint
make deps
```

Details:
- `make dev`: Uses Air for hot-reload
- `make run`: Copies frontend build artifacts then runs directly
- `make build`: Builds production binary to `dist/`
- `make test`: Runs all Go tests
- `make test-cover`: Generates a coverage report

## Method 3: frontend Directory

Frontend scripts follow `frontend/package.json`. Bun is the preferred package manager.

```bash
cd "frontend"
bun install
bun run dev
bun run build
bun run preview
bun run type-check
bun run lint
```

If you need to verify pnpm compatibility, you can run `pnpm install`. For daily development, building, and dependency changes, always use Bun.

## Windows Environment Tips

If `make` is not available, use Go and Bun commands directly:

```powershell
cd backend-go
air
go test ./...
go fmt ./...

cd ../frontend
bun install
bun run dev
```

Recommended installations:
- Go
- Bun
- Make
- Git

## File Change and Reload Rules

### Automatic Hot-Reload

- Go source code: Automatic reload under `make dev` / `cd "backend-go" && make dev`
- Config file: `backend-go/.config/config.json` changes take effect automatically

### Requires Restart

- Environment file: `backend-go/.env`
- Dependency or build config changes

## Common Verification Commands

Before committing changes, at minimum run:

```bash
make build
cd "backend-go" && make test
cd "frontend" && bun run build
```

If you only changed backend code, also run:

```bash
cd "backend-go" && make lint
```

## Local Access Points

- Web admin UI: `http://localhost:3688`
- Proxy API: `http://localhost:3688/v1`
- Health check: `http://localhost:3688/health`
- Frontend dev server: `http://localhost:5173` by default

### Windows / WSL / Docker Access Tips

On Windows, cmd, PowerShell, WSL, and Docker may be in different network environments. `localhost` / `127.0.0.1` may not reach the CCX process. For cross-environment access, use the Windows host LAN IPv4 address, for example:

```text
http://192.168.1.23:3688/v1
```

Get the address:

```powershell
ipconfig
```

Verify connectivity:

```powershell
curl.exe -i http://192.168.1.23:3688/health
```

When `BIND_HOST` is empty, the CCX backend listens on `:PORT` by default, which means all network interfaces. You generally do not need to change it to `0.0.0.0`. Set `BIND_HOST=127.0.0.1` for local-only access. If the LAN IP is not accessible, first check whether the Windows firewall allows inbound traffic on the corresponding port.

## Common Development Tasks

### Backend Only

```bash
cd "backend-go"
make dev
```

### Frontend Only

```bash
cd "frontend"
bun install
bun run dev
```

### Full-Stack Development

```bash
make dev
```

### Verify Production Build

```bash
make build
```
