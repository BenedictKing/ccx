# Claude / OpenAI Chat / Codex Responses / Gemini API Proxy - CCX

[![GitHub release](https://img.shields.io/github/v/release/BenedictKing/ccx)](https://github.com/BenedictKing/ccx/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

CCX is a high-performance AI API proxy and protocol translation gateway for Claude, OpenAI Chat / Codex Responses, and Gemini. It provides a unified entrypoint, built-in web administration, failover, multi-key management, channel orchestration, and model routing.

## Features

- Integrated backend + frontend architecture with single-port deployment
- Dual-key authentication with `PROXY_ACCESS_KEY` and optional `ADMIN_ACCESS_KEY`
- Web admin console for channel management, testing, and monitoring
- Support for Claude Messages, OpenAI Chat Completions, Codex Responses, and Gemini APIs
- Protocol conversion across Claude Messages, OpenAI Chat, Gemini, and Responses
- Smart scheduling with priorities, promotion windows, health checks, and circuit breaking
- Per-channel API key rotation, proxy support, custom headers, and model allowlists
- Model remapping, fast mode, and verbosity controls
- Streaming and non-streaming support
- Responses session tracking for multi-turn workflows

## Screenshots

### Channel Orchestration

Visual channel management with drag-and-drop priority adjustment and real-time health monitoring.

![Channel Orchestration](docs/screenshots/channel-orchestration.png)

### Add Channel

Supports multiple upstream service types (Claude/Codex/Gemini) with flexible API key, model mapping, and request parameter configuration.

<img src="docs/screenshots/add-channel-modal.png" width="500" alt="Add Channel">

### Traffic Stats

Real-time monitoring of request traffic, success rate, and response latency per channel.

![Traffic Stats](docs/screenshots/traffic-stats.png)

## Architecture

CCX exposes one backend entrypoint:

```text
Client -> backend :3000 ->
  |- /                    -> Web UI
  |- /api/*               -> Admin API
  |- /v1/messages         -> Claude Messages proxy
  |- /v1/chat/completions -> OpenAI Chat proxy
  |- /v1/responses        -> Codex Responses proxy
  |- /v1/models           -> Models API
  `- /v1beta/models/*     -> Gemini proxy
```

Key properties:

- Single port
- Embedded frontend assets
- No separate reverse proxy required
- Admin and proxy traffic can use different keys

See [ARCHITECTURE.md](ARCHITECTURE.md) for detailed design notes.

## Quick Start

### Option 1: Binary

1. Download the latest binary from [Releases](https://github.com/BenedictKing/ccx/releases/latest)
2. Create a `.env` file next to the binary:

```bash
PROXY_ACCESS_KEY=your-super-strong-secret-key
PORT=3000
ENABLE_WEB_UI=true
APP_UI_LANGUAGE=en
```

3. Run the binary and open `http://localhost:3000`

### Option 2: Docker

```bash
docker run -d \
  --name ccx \
  -p 3000:3000 \
  -e PROXY_ACCESS_KEY=your-super-strong-secret-key \
  -e APP_UI_LANGUAGE=en \
  -v $(pwd)/.config:/app/.config \
  crpi-i19l8zl0ugidq97v.cn-hangzhou.personal.cr.aliyuncs.com/bene/ccx:latest
```

### Option 3: Build From Source

```bash
git clone https://github.com/BenedictKing/ccx
cd ccx
cp backend-go/.env.example backend-go/.env
make run
```

Useful commands:

```bash
make run
make dev
make build
```

## UI Language

Set the default admin UI language with:

```bash
APP_UI_LANGUAGE=en
```

Supported values:

- `en`
- `id`
- `zh-CN`

Invalid values fall back to `en`.

## Core Environment Variables

```bash
PORT=3000
ENV=production
ENABLE_WEB_UI=true
PROXY_ACCESS_KEY=your-super-strong-secret-key
ADMIN_ACCESS_KEY=your-admin-secret-key
APP_UI_LANGUAGE=en
LOG_LEVEL=info
REQUEST_TIMEOUT=300000
```

## Main Endpoints

- Web UI: `GET /`
- Health: `GET /health`
- Admin API: `/api/*`
- Claude Messages: `POST /v1/messages`
- OpenAI Chat: `POST /v1/chat/completions`
- Codex Responses: `POST /v1/responses`
- Gemini: `POST /v1beta/models/{model}:generateContent`

## Development

Recommended local workflow:

```bash
make dev
```

Frontend only:

```bash
cd frontend
bun install
bun run dev
```

Backend only:

```bash
cd backend-go
make dev
```

## Additional Docs

- [ARCHITECTURE.md](ARCHITECTURE.md)
- [DEVELOPMENT.md](DEVELOPMENT.md)
- [ENVIRONMENT.md](ENVIRONMENT.md)
- [RELEASE.md](RELEASE.md)

## Community

Join the QQ group for discussion: **642217364**

<img src="docs/qrcode_1769645166806.png" width="300" alt="QQ Group QR Code">

## License

MIT
