# src/ccx_agents/router.py
"""Agent-name routing and model-name routing for CCX channels.

Two routing styles:

1. **Model-based routing** (``CcxRouter.add_map`` / ``CcxRouter.add_rule``):
   Route by model name to a ``CcxConfigModel``.

2. **Agent-name routing** (``CcxRouter.route``):
   Route by agent name to a CCX channel name.  Supports ``run_sync`` /
   ``run_async`` / ``run_streamed``.
"""

from __future__ import annotations

import logging
from typing import Any, Protocol, runtime_checkable

from agents import Agent, Runner, RunResultStreaming

from .config import CcxConfigModel, CcxConfigProtocol

_log = logging.getLogger(__name__)


@runtime_checkable
class RoutingRule(Protocol):
    """A callable that maps a model name and agent name to a CCX channel config.

    Return ``None`` to fall through to the next rule, or a
    :class:`CcxConfigModel` to use that channel.
    """

    def __call__(self, *, model: str, agent_name: str | None) -> CcxConfigModel | None:
        ...


class _DictRouter:
    """Simple static-map router built from a ``{model: config}`` dict."""

    def __init__(self, mapping: dict[str, CcxConfigModel]) -> None:
        self._mapping = mapping

    def __call__(self, *, model: str, agent_name: str | None) -> CcxConfigModel | None:
        return self._mapping.get(model)


class CcxRouter:
    """Route agents to CCX channels.

    Supports two routing modes:

    **Model-based** (from :meth:`add_map` / :meth:`add_rule`)::

        router = CcxRouter()
        router.add_map({"gpt-4o": CcxConfigModel(base_url="...", route_prefix="gpt")})

    **Agent-name-based** (from :meth:`route`)::

        router = CcxRouter(config)
        router.route("translator", channel="claude-4")
        router.route("coder", channel="gpt-4o")
        result = router.run_sync(agent, "Hello")
    """

    def __init__(self, ccx_config: CcxConfigProtocol | None = None) -> None:
        """``ccx_config`` is an optional object with ``base_url`` / ``api_key`` attrs."""
        self._rules: list[RoutingRule] = []
        self._agent_map: dict[str, str] = {}  # agent_name → channel
        self._default_channel: str | None = None
        self._ccx_config = ccx_config

    # ── Model-based routing (existing API) ──────────────────────────────

    def add_rule(self, rule: RoutingRule) -> None:
        """Append a routing rule (model-based)."""
        self._rules.append(rule)

    def add_map(self, mapping: dict[str, CcxConfigModel]) -> None:
        """Append a static ``{model: config}`` map as a rule."""
        self._rules.append(_DictRouter(mapping))  # type: ignore[arg-type]

    def resolve(self, *, model: str, agent_name: str | None = None) -> CcxConfigModel | None:
        """Evaluate rules in order; return the first match."""
        for rule in self._rules:
            result = rule(model=model, agent_name=agent_name)
            if result is not None:
                return result
        return None

    # ── Agent-name-based routing (new API) ──────────────────────────────

    def route(self, agent_name: str, channel: str) -> None:
        """Route an agent by name to a specific CCX channel.

        Args:
            agent_name: The ``Agent.name`` value to match.
            channel: The CCX upstream channel name (e.g. ``"claude-4"``).
        """
        self._agent_map[agent_name] = channel
        _log.debug("CcxRouter: %s → channel %s", agent_name, channel)

    def set_default(self, channel: str) -> None:
        """Set the default channel for unmatched agents."""
        self._default_channel = channel

    def _get_channel(self, agent: Agent[Any]) -> str | None:
        """Resolve the channel for a given agent."""
        return (
            self._agent_map.get(agent.name)
            or self._default_channel
            or getattr(self._ccx_config, "channel", None)
        )

    def _build_client(self, channel: str | None) -> Any:
        """Build a client for the given channel, or return the global client."""
        if channel and self._ccx_config:
            from ._client import ccx_client

            base_url = getattr(self._ccx_config, "base_url", None)
            api_key = getattr(self._ccx_config, "api_key", None)
            return ccx_client(
                base_url=base_url or "http://localhost:3000/v1",
                api_key=api_key,
                route_prefix=channel,
                timeout=30.0,
            )
        from ._client import get_ccx_client

        return get_ccx_client()

    # ── Run methods (sync / async / streamed) ───────────────────────────

    def run_sync(self, agent: Agent[Any], input_data: str, **kwargs: Any) -> Any:
        """Run an agent synchronously, routing to the correct channel."""
        channel = self._get_channel(agent)
        client = self._build_client(channel)
        from agents import set_default_openai_client
        set_default_openai_client(client)
        return Runner.run_sync(agent, input_data, **kwargs)

    async def run_async(self, agent: Agent[Any], input_data: str, **kwargs: Any) -> Any:
        """Run an agent asynchronously, routing to the correct channel."""
        channel = self._get_channel(agent)
        client = self._build_client(channel)
        from agents import set_default_openai_client
        set_default_openai_client(client)
        return await Runner.run(agent, input_data, **kwargs)

    def run_streamed(
        self, agent: Agent[Any], input_data: str, **kwargs: Any
    ) -> RunResultStreaming[Any]:
        """Run an agent with streaming, routing to the correct channel."""
        channel = self._get_channel(agent)
        client = self._build_client(channel)
        from agents import set_default_openai_client
        set_default_openai_client(client)
        return Runner.run_streamed(agent, input_data, **kwargs)
