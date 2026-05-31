# Changelog

## v0.1.0 (2025-05-30)

Initial release.

### Features

- **CcxConfig** — one-line setup (`CcxConfig.setup()`) and multi-channel routing (`CcxConfig.setup_with_routing()`)
- **CcxConfigModel** — per-model configuration with `route_prefix`, `api_type`, custom headers
- **CcxRouter** — multi-agent routing by agent name (`route(name, channel)`) with `run_sync` / `run_async` / `run_streamed`
- **CcxConversation** — multi-turn conversation using CCX's `previous_response_id` mechanism
- **CcxTracing** — tracing integration via `set_default_openai_client(client, use_for_tracing=True)`
- **ccx_setup()** — one-liner: `from ccx_agents import ccx_setup; ccx_setup()`
- **Model mapping** — automatic `model → api_type` heuristics (Claude → messages, O-series → responses, others → chat)
- **Streaming** — full streaming support via `Runner.run_streamed()` and `CcxRouter.run_streamed()`
- **Build** — Hatchling-based, published to PyPI

### Examples

- `basic.py` — single-endpoint setup
- `router.py` — multi-channel routing
- `01_basic_setup.py` — basic initialization
- `02_low_code.py` — low-code mode
- `03_multi_agent_routing.py` — multi-agent routing
- `04_conversation.py` — multi-turn conversation
- `05_tools.py` — tool calling example
- `06_streaming.py` — streaming output
