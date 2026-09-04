# Connect Claude Desktop to CCX

Claude Desktop (the desktop app) supports a "Third-Party Inference" mode: instead of signing in to a Claude account, it sends inference requests to a self-hosted gateway. The CCX **Messages** endpoint implements the Anthropic Messages protocol (including streaming and tool use) plus the `/v1/models` discovery endpoint, so CCX can serve as the inference gateway for Claude Desktop.

## How it works

```text
Claude Desktop  ->  HTTPS wrapper  ->  CCX /v1/messages  ->  Messages channel  ->  upstream Anthropic-compatible endpoint
```

## Prerequisites

1. CCX is running (for example `http://localhost:3000`) with `PROXY_ACCESS_KEY` set
2. At least one working channel under the Messages endpoint
3. A recent version of [Claude Desktop](https://claude.com/download) (supported when the Developer menu exists)

## 1. Configure a CCX channel

Same as [Claude Code](./claude-code) — Claude Desktop speaks the Messages protocol:

1. Open the CCX admin console and go to **Messages**
2. Click "Add Channel" and fill in the channel configuration for your upstream provider

Common examples:

| Upstream | Service type | Base URL example |
|----------|--------------|------------------|
| Anthropic Claude | `Claude` | `https://api.anthropic.com` |
| DeepSeek Anthropic-compatible | `Claude` | `https://api.deepseek.com/anthropic` |
| Kimi coding endpoint | `Claude` | `https://api.kimi.com/coding/` |
| GLM Anthropic-compatible | `Claude` | `https://open.bigmodel.cn/api/anthropic` |

::: tip
For upstream API keys, model names, and special options, see the matching [provider setup guide](/en/providers/).
:::

## 2. Put CCX behind HTTPS (required)

The third-party inference gateway in Claude Desktop **only accepts HTTPS URLs**. CCX listens over plain HTTP locally (for example `http://localhost:3000`), so you cannot put that address into the Gateway base URL directly — wrap local CCX with a reverse proxy first. The recommended approach is [Caddy](https://caddyserver.com/) with its locally trusted certificates (`tls internal`):

1. Install Caddy:

```bash
# macOS
brew install caddy
```

```powershell
# Windows (either one)
winget install CaddyServer.Caddy
scoop install caddy
```

2. Create a `Caddyfile` in any directory. Replace `127.0.0.1:3000` with your actual CCX address and port:

```text
https://127.0.0.1:8443 {
    reverse_proxy 127.0.0.1:3000
    tls internal
}
```

3. Trust the local Caddy CA on first use, then start it:

```bash
caddy trust
caddy run
```

::: warning
`caddy trust` requires sudo on macOS. Claude Desktop then reaches CCX through `https://127.0.0.1:8443`. You can pick a different port, but the scheme must be HTTPS.
:::

## 3. Enable Developer Mode and configure the gateway

1. Launch Claude Desktop. **No Claude sign-in is required**
2. Enable Developer Mode:
   - macOS: menu bar **Help → Troubleshooting → Enable Developer Mode**
   - Windows: **☰** application menu in the top-left of the sign-in screen → **Help → Troubleshooting → Enable Developer Mode**
3. Open **Developer → Configure Third-Party Inference…** and fill in the **Connection** section:

| Field | Value |
|-------|-------|
| Inference provider | `Gateway` (default) |
| Gateway base URL | `https://127.0.0.1:8443` (the HTTPS wrapper address from the previous step; do not append `/v1`) |
| Gateway API key | The CCX `PROXY_ACCESS_KEY` |
| Gateway auth scheme | `bearer` (default) or `x-api-key`; CCX accepts both |
| Credential kind | `Static API key` (default) |

4. Click **Test connection** to verify connectivity
5. Click **Apply Changes** to save and apply

After applying, the model picker in Claude Desktop shows the models served by the gateway, and chat requests are forwarded through CCX to your configured upstream channels.

## 4. Configure the model list

Claude Desktop can populate the model picker in two ways:

- **Model discovery (automatic, on by default)**: requests `{Gateway base URL}/v1/models`. CCX implements this endpoint, so it works out of the box. However, discovery mainly recognizes Claude-style model IDs; if your channels serve models under non-Claude names (such as `glm-4.7` or `deepseek-v4`), they may not appear in the picker.
- **Model list (manual)**: click **Add model** to list model IDs one by one. This overrides discovery, and the **first entry is the default model**.

Recommendation: keep discovery on when the upstream is Anthropic Claude or your channel maps to Claude model names; otherwise disable Model discovery and list models explicitly, putting your most-used model first.

## 5. Managing multiple configurations

To switch between gateways (for example local CCX and a remote CCX deployment):

1. The **Select configuration** dropdown at the top-right of the configuration window defaults to `Default`
2. Use the dropdown to duplicate, rename, or create configurations; each keeps its own base URL, API key, and model list
3. **Developer → Open Developer Config File…** opens the JSON file behind the current configuration for backup and migration
4. **Export** produces a `.mobileconfig` (macOS) or `.reg` (Windows) artifact for MDM fleet deployment

## Troubleshooting

### "Can't reach 127.0.0.1:8443"

Claude Desktop cannot reach the HTTPS wrapper:

1. Is `caddy run` still running?
2. Does the port in the Gateway base URL match the Caddyfile listener?
3. Is CCX itself running (can you open the admin console at `http://localhost:3000`)?

### Test connection returns 401

The Gateway API key does not match the CCX `PROXY_ACCESS_KEY`. Check the CCX startup configuration and the key entered in the configuration window.

### Connected but the model picker is empty

Discovery only recognizes Claude-style model IDs. Make sure the Messages channel allowlist/mapping includes usable models; if the model names are not Claude-style, switch to a manual **Model list** (see section 4 above).

### Can I just enter `http://localhost:3000`?

No. The third-party inference gateway must be reached over HTTPS; plain `http://` addresses fail to connect. Use the HTTPS wrapper from section 2. If CCX is deployed on a remote server that already has a valid TLS certificate, enter `https://your-domain` directly and skip Caddy.

### Switching back to official Claude

Open **Developer → Configure Third-Party Inference…**, switch the Inference provider from `Gateway` back to the official sign-in (or simply sign in to a Claude account) to restore the official service.
