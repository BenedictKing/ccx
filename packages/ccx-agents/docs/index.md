# ccx-agents

**Route [openai-agents-python](https://github.com/openai/openai-agents-python) through [CCX](https://github.com/BenedictKing/ccx) — the multi-model AI proxy gateway.**

---

## Quick Start

```python
from ccx_agents import ccx_setup

ccx_setup(base_url="http://localhost:3000/v1")

from agents import Agent, Runner
result = Runner.run_sync(Agent(name="Hi", instructions="..."), "Hello")
print(result.final_output)
```

## Installation

```bash
pip install ccx-agents
```

Requires Python 3.10+.

## Key Features

| Feature | Class/Function | Description |
|:--------|:---------------|:------------|
| One-line setup | `ccx_setup()` | Inject CCX as default provider |
| Config class | `CcxConfig.setup()` | Single-endpoint config |
| Model routing | `CcxConfig.setup_with_routing()` | Model name → channel |
| Agent routing | `CcxRouter` | Agent name → channel, with sync/async/stream |
| Conversation | `CcxConversation` | Multi-turn via `previous_response_id` |
| Tracing | `CcxTracing` | CCX admin dashboard tracing |
| Plugin system | `CcxPlugin` | Extend ccx-agents with custom hooks |

## Documentation

- [API Reference](api/ccx_config.md)
- [Examples](examples.md)
- [Contributing](contributing.md)
