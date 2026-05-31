# src/ccx_agents/_setup.py
"""One-line ccx_setup() convenience function."""

from __future__ import annotations

import logging

from agents import set_default_openai_client

from ._client import ccx_client
from .utils import resolve_api, resolve_api_key, resolve_base_url, resolve_channel, resolve_model

_log = logging.getLogger(__name__)


def ccx_setup(
    base_url: str | None = None,
    api_key: str | None = None,
    channel: str | None = None,
    model: str | None = None,
    api: str | None = None,
    use_for_tracing: bool = False,
) -> None:
    """One-line setup: inject CCX as the default LLM provider.

    All arguments are optional — they fall back to environment variables::

        CCX_BASE_URL  (default: http://localhost:3000/v1)
        CCX_API_KEY
        CCX_CHANNEL
        CCX_MODEL
        CCX_API       (default: responses)

    Usage::

        from ccx_agents import ccx_setup
        ccx_setup(base_url="http://localhost:3000/v1")

        # Now use openai-agents-python normally
        from agents import Agent, Runner
        result = Runner.run_sync(Agent(name="Hi", instructions="..."), "Hello")
    """
    url = resolve_base_url(base_url)
    key = resolve_api_key(api_key)
    ch = resolve_channel(channel)
    mdl = resolve_model(model)
    api_type = resolve_api(api)

    # Build the OpenAI client pointed at CCX
    client = ccx_client(
        base_url=url,
        api_key=key,
        route_prefix=ch,
    )

    set_default_openai_client(client, use_for_tracing=use_for_tracing)
    _log.info(
        "ccx_setup: base_url=%s channel=%s model=%s api=%s",
        url, ch, mdl, api_type,
    )
