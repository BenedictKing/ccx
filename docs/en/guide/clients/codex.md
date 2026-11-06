# Connect Codex CLI / Codex App to CCX

If you are using **CCX Desktop**, you can apply Codex settings from [Agent configuration](/en/guide/desktop/#agent-configuration) first, then return here to confirm the Responses endpoint and Base URL behavior.

Codex CLI and Codex App use the same OpenAI-compatible configuration: Base URL, API key, and model name are the same. Only the configuration entry point differs.

## How it works

```text
Codex CLI / App  ->  CCX /v1/responses  ->  Responses channel  ->  upstream Responses or Chat endpoint
```

## 1. Configure a CCX channel

1. Open the CCX admin console and go to **Responses**
2. Click "Add Channel"
3. Choose the service type based on upstream capability

| Upstream capability | Service type | Notes |
|---------------------|--------------|-------|
| Native OpenAI Responses | `Responses` | Direct Responses forwarding |
| OpenAI Chat-compatible | `OpenAI Chat` | CCX converts Responses to Chat Completions |
| Claude | `Claude` | CCX converts Responses to Claude Messages |

If the upstream is Chat-compatible and does not support non-standard Chat roles such as `developer`, edit the channel and enable **Normalize Non-standard Chat Roles**.

## 2. Shared configuration values

Codex CLI and Codex App both use these values:

| Setting | Value |
|---------|-------|
| API Key | `your-ccx-proxy-key` |
| Base URL | `http://localhost:3000/v1` |
| Model | requested model name, for example `gpt-5` |

::: tip
The API key here is CCX `PROXY_ACCESS_KEY`, not the upstream provider API key. Upstream keys are configured only in CCX channels.
:::

## 3. Codex CLI configuration

Set these environment variables:

```bash
export OPENAI_API_KEY="your-ccx-proxy-key"
export OPENAI_BASE_URL="http://localhost:3000/v1"
```

Then run:

```bash
codex "hello"
```

If your Codex CLI version uses a config file instead of environment variables, fill in the same values:

```text
API Key: your-ccx-proxy-key
Base URL: http://localhost:3000/v1
Model: gpt-5
```

## 4. Codex App configuration

In the Codex App model or provider settings, choose an OpenAI-compatible / custom API provider and fill in:

| Setting | Value |
|---------|-------|
| API Key | `your-ccx-proxy-key` |
| Base URL | `http://localhost:3000/v1` |
| Model | `gpt-5` or your mapped model name |

## 5. Recommended model mapping

Codex may request GPT-style model names. If your upstream uses different model names, configure model mapping on the Responses channel:

| Request model match | Example upstream model |
|---------------------|------------------------|
| `gpt` | upstream primary model |
| `mini` | upstream lightweight model |

::: tip
Do not only map `gpt-5` and forget `gpt-5-mini`. CCX prefers longer matching keys first; `gpt` and `mini` usually cover common Codex model names.
:::

## Troubleshooting

### Codex automatic review model is unavailable

If the upstream reports that `codex-auto-review` does not exist, add that request model to the Responses channel's model mapping and map it to a model supported by the upstream. If the channel uses a model allowlist, include either the requested model or the mapped upstream model.

### The Codex App context meter does not change after setting an upstream window in CCX

A channel capability describes the **actual upstream model**. In the Responses channel's model capabilities, set `contextWindowTokens` for the mapped upstream model. When context routing is enabled, CCX uses this value to decide whether a request can be sent to that channel. This does not change the model context budget displayed by the Codex App; that value comes from Codex's client-side model configuration.

For example, if `gpt-5.2` maps to `deepseek-v4`, configure the channel capability for `deepseek-v4`, not only for `gpt-5.2`. Check the channel log for the requested and mapped model names. To change the context usage shown in Codex, adjust the client-side budget in a Codex-supported model configuration. CCX channel capabilities affect gateway routing only.

### Do Codex CLI and Codex App need separate configurations?

No. They use the same CCX OpenAI-compatible configuration:

```text
API Key  = PROXY_ACCESS_KEY
Base URL = http://localhost:3000/v1
Model    = requested model name
```

The CLI uses a terminal or config file. The App uses a graphical settings page.

### 401 Unauthorized

Check:

1. First check whether the current environment has `OPENAI_API_KEY` set. It may override the key you entered in Codex settings.

   ```bash
   printenv OPENAI_API_KEY
   ```

2. `OPENAI_API_KEY` or the App API key matches CCX `PROXY_ACCESS_KEY`
3. You did not accidentally enter the upstream provider API key
4. CCX was started with the same `PROXY_ACCESS_KEY`

### 404 or missing endpoint

Check Base URL.

Correct:

```bash
export OPENAI_BASE_URL="http://localhost:3000/v1"
```

Do not use:

```bash
export OPENAI_BASE_URL="http://localhost:3000"
export OPENAI_BASE_URL="http://localhost:3000/v1/responses"
```

### model_not_found

Check the Responses channel:

1. The model allowlist includes the requested model or mapped upstream model
2. Model mapping covers the actual model requested by Codex
3. The upstream model name is valid for that provider

### Upstream says a role is unsupported

If the error mentions roles such as `developer`, `tool`, or `system`, edit the Responses channel and enable **Normalize Non-standard Chat Roles**. This is common for Chat-compatible upstreams such as DeepSeek.

### Streaming output is interrupted

Check:

1. The upstream reliably supports streaming responses
2. Reverse proxies or network proxies do not have short timeouts
3. The current channel is not circuit-broken or frequently failing over
4. The client is not requesting unsupported tool calls or response formats

### Confirm whether the request uses Responses

Check CCX backend logs or channel logs. The request path should be:

```text
/v1/responses
```

If you see `/v1/chat/completions`, the client is using OpenAI Chat configuration instead of Codex Responses configuration.
