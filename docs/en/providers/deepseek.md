# DeepSeek Setup

## Get API Key

1. Visit [DeepSeek Platform](https://platform.deepseek.com/)
2. Sign up and log in
3. Go to [API Keys](https://platform.deepseek.com/api_keys) page
4. Create a new API Key

## How It Works

CCX supports multiple protocol endpoints for DeepSeek:

```text
Claude Code CLI       ──→  /v1/messages          ──→  CCX  ──→  DeepSeek Anthropic endpoint
Codex CLI/App         ──→  /v1/responses         ──→  CCX  ──→  DeepSeek Chat endpoint
OpenAI-compatible app ──→  /v1/chat/completions  ──→  CCX  ──→  DeepSeek Chat endpoint
```

A single CCX instance serves all paths simultaneously.

## Scenario 1: OpenAI Chat Protocol (General)

For any tool compatible with OpenAI Chat protocol.

### Configuration

1. Go to CCX admin console, select **Chat** endpoint
2. Click "Add Channel"
3. Fill in:

| Field | Value |
|-------|-------|
| **Service Type** | `OpenAI Chat` |
| **Name** | `DeepSeek Chat` |
| **Base URL** | `https://api.deepseek.com` |
| **API Keys** | Your DeepSeek API Key |
| **Models** | `deepseek-v4-pro`, `deepseek-v4-flash` |

### Client Configuration

```bash
export OPENAI_API_KEY="your-ccx-proxy-key"
export OPENAI_BASE_URL="http://localhost:3000/v1"
```

---

## Scenario 2: Claude Code CLI

Claude Code CLI uses the Messages API. Configure a Claude service type channel pointing to DeepSeek's Anthropic-compatible endpoint.

### Configuration

1. Go to CCX admin console, select **Messages** endpoint
2. Click "Add Channel"
3. Fill in:

| Field | Value |
|-------|-------|
| **Service Type** | `Claude` |
| **Name** | `DeepSeek Claude` |
| **Base URL** | `https://api.deepseek.com/anthropic` |
| **API Keys** | Your DeepSeek API Key |
| **Models** | `deepseek-v4-pro`, `deepseek-v4-flash` |

### Model Mapping (Recommended)

Claude Code CLI sends requests with Claude model names (e.g., `claude-opus-4-7`). Configure model mapping for automatic redirection:

| Request Model | Redirects To |
|---------------|--------------|
| `opus` | `deepseek-v4-pro` |
| `sonnet` | `deepseek-v4-pro` |
| `haiku` | `deepseek-v4-flash` |

### Client Configuration

```bash
export ANTHROPIC_API_KEY="your-ccx-proxy-key"
export ANTHROPIC_BASE_URL="http://localhost:3000"
```

::: warning
`ANTHROPIC_BASE_URL` should point to the CCX gateway root — do not append `/v1` or `/v1/messages`.
:::

---

## Scenario 3: Codex CLI / App

Codex CLI uses the OpenAI Responses API. Configure a Chat service type channel under the Responses endpoint.

### Configuration

1. Go to CCX admin console, select **Responses** endpoint
2. Click "Add Channel"
3. Fill in:

| Field | Value |
|-------|-------|
| **Service Type** | `OpenAI Chat` |
| **Name** | `DeepSeek Chat` |
| **Base URL** | `https://api.deepseek.com` |
| **API Keys** | Your DeepSeek API Key |
| **Models** | `deepseek-v4-pro`, `deepseek-v4-flash` |

4. After saving, **edit** the channel and enable **Normalize Non-standard Chat Roles**

::: tip Why enable this?
After Codex Responses requests are converted to Chat Completions, they may contain roles like `developer` that DeepSeek doesn't support. This option normalizes them to `user` before sending upstream.
:::

### Model Mapping (Recommended)

| Request Model | Redirects To |
|---------------|--------------|
| `astra` | `deepseek-v4-pro` |
| `sol` | `deepseek-v4-flash` |
| `luna` | `deepseek-v4-flash` |

CCX uses the longest matching key first. `astra` matches the flagship alias, while `sol` also matches names like `gpt-6.1-sol`.

### Client Configuration

**Codex CLI:**

```bash
export OPENAI_API_KEY="your-ccx-proxy-key"
export OPENAI_BASE_URL="http://localhost:3000/v1"
codex "hello"
```

**Codex App (VS Code / JetBrains):**

| Setting | Value |
|---------|-------|
| API Key | `your-ccx-proxy-key` |
| Base URL | `http://localhost:3000/v1` |
| Model | `gpt-5` (CCX redirects to `deepseek-v4-pro`) |

---

## Available Models

| Model | Description |
|-------|-------------|
| `deepseek-v4-pro` | DeepSeek-V4 Pro flagship model |
| `deepseek-v4-flash` | DeepSeek-V4 Flash fast model |
| `deepseek-chat` | DeepSeek-V3 general chat (legacy) |
| `deepseek-reasoner` | DeepSeek-R1 reasoning model |

## Verify Configuration

```bash
curl http://localhost:3000/v1/models \
  -H "Authorization: Bearer your-ccx-proxy-key"
```

## Troubleshooting

| Issue | Solution |
|-------|----------|
| `401 Unauthorized` | Verify the key matches CCX's `PROXY_ACCESS_KEY` |
| `Model not found` | Check model names in channel config |
| `Connection refused` | Confirm CCX is running and Base URL is correct |
| Channel unhealthy | Check DeepSeek API Key and network access to `api.deepseek.com` |
| Claude Code format error | Ensure `ANTHROPIC_BASE_URL` points to root, not `/v1` |
