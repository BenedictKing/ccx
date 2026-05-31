# src/ccx_agents/tracing.py
"""Agent-run tracing helpers that emit metrics to CCX's admin panel."""

from __future__ import annotations

import logging
import time
from typing import Any

from agents import Agent, Runner, RunResult
from agents.run import RunConfig

_log = logging.getLogger(__name__)


class CcxTracing:
    """Tracing integration marker class.

    Allows ``set_default_openai_client(client, use_for_tracing=True)``
    to be called from ``CcxConfig.setup()``.
    """

    pass


def _make_trace_headers(run_config: RunConfig | None) -> dict[str, str]:
    """Build CCX-trace headers that appear in the CCX admin dashboard."""
    headers: dict[str, str] = {}

    if run_config and run_config.workflow_name:
        headers["X-Ccx-Trace-Workflow"] = run_config.workflow_name

    return headers


async def run_with_ccx_tracing(
    agent: Agent[Any],
    input_data: str | list[Any],
    *,
    context: Any = None,
    max_turns: int | None = None,
    run_config: RunConfig | None = None,
    **kwargs: Any,
) -> RunResult:
    """Drop-in replacement for ``Runner.run()`` that adds CCX trace headers.

    Usage::

        from ccx_agents.tracing import run_with_ccx_tracing as run

        result = await run(agent, "Hello")
    """
    t0 = time.perf_counter()

    # Inject trace headers into the default_headers of the active client
    from ._client import get_ccx_client

    client = get_ccx_client()
    trace_headers = _make_trace_headers(run_config)
    if client is not None and trace_headers:
        # _custom_headers is the mutable backing of the default_headers property
        existing = client._custom_headers or {}
        client._custom_headers = {**existing, **trace_headers}
        _log.debug("CCX trace headers injected: %s", trace_headers)

    result = await Runner.run(
        agent,
        input_data,
        context=context,
        max_turns=max_turns,
        run_config=run_config,
        **kwargs,
    )

    elapsed = time.perf_counter() - t0
    _log.info(
        "CCX agent run completed - agent=%s turns=%d elapsed=%.2fs",
        agent.name,
        len(result.raw_responses) if result.raw_responses else 0,
        elapsed,
    )
    return result


def run_sync_with_ccx_tracing(
    agent: Agent[Any],
    input_data: str | list[Any],
    *,
    context: Any = None,
    max_turns: int | None = None,
    run_config: RunConfig | None = None,
    **kwargs: Any,
) -> RunResult:
    """Synchronous wrapper around :func:`run_with_ccx_tracing`."""
    import asyncio

    return asyncio.run(
        run_with_ccx_tracing(
            agent,
            input_data,
            context=context,
            max_turns=max_turns,
            run_config=run_config,
            **kwargs,
        )
    )
