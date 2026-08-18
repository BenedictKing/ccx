# Xiaomi MiMo Setup

## Get API Key

MiMo models are available through [SiliconFlow](https://cloud.siliconflow.cn/) (recommended).

1. Visit [SiliconFlow](https://cloud.siliconflow.cn/)
2. Sign up and log in
3. Go to "API Keys" page
4. Create a new API Key

## Add Channel in CCX

### Via SiliconFlow

| Field | Value |
|-------|-------|
| Name | `MiMo (SiliconFlow)` |
| Service Type | `openai` |
| Base URL | `https://api.siliconflow.cn/v1` |
| API Keys | Your SiliconFlow API Key |

### Steps

1. Go to CCX admin console, select **Chat** endpoint
2. Click "Add Channel"
3. Set service type to `OpenAI Chat`
4. Set Base URL to `https://api.siliconflow.cn/v1`
5. Paste your API Key
6. Save

### Model Mapping (Optional)

```json
{
  "mimo-pro": "XiaomiMiMo/MiMo-V2.5-Pro",
  "mimo": "XiaomiMiMo/MiMo-V2.5"
}
```

## Available Models

| Model | Description |
|-------|-------------|
| `XiaomiMiMo/MiMo-V2.5-Pro` | Latest flagship, 1.02T total / 42B active |
| `XiaomiMiMo/MiMo-V2.5` | 310B total / 15B active, native multimodal |

::: tip
Model IDs on SiliconFlow use the format `Organization/ModelName`. Use the full identifier.
:::

## Notes

- MiMo is accessed through OpenAI-compatible platforms
- SiliconFlow China: `https://api.siliconflow.cn/v1`
- SiliconFlow International: `https://api.siliconflow.com/v1`
- MiMo is a reasoning model that supports `reasoning_content` field
- SiliconFlow also provides an Anthropic-compatible endpoint (`/anthropic/v1/messages`)
