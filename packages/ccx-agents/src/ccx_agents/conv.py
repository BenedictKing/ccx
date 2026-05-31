# src/ccx_agents/conv.py
"""Multi-turn conversation with ``previous_response_id`` and streaming."""

from __future__ import annotations

import logging
from typing import Any

from agents import Agent, Runner

from ._client import ccx_client, get_ccx_client
from .config import CcxConfigProtocol
from .utils import resolve_api_key

_log = logging.getLogger(__name__)


class CcxConversation:
    """Multi-turn conversation using CCX's ``previous_response_id``.

    Each ``run()`` call automatically chains to the previous response,
    letting CCX (not the client) maintain conversation history.

    Usage::

        conv = CcxConversation(config)
        result1 = conv.run(agent, "What's the weather?")
        result2 = conv.run(agent, "And tomorrow?")  # auto-continues
    """

    def __init__(self, ccx_config: CcxConfigProtocol | None = None) -> None:
        self._previous_response_id: str | None = None
        self._ccx_config = ccx_config

    # ── public helpers ──────────────────────────────────────────────────

    @property
    def previous_response_id(self) -> str | None:
        """The last response ID, for chaining."""
        return self._previous_response_id

    def reset(self) -> None:
        """Start a fresh conversation."""
        self._previous_response_id = None

    # ── sync run ────────────────────────────────────────────────────────

    def run(
        self,
        agent: Agent[Any],
        input_data: str,
        **kwargs: Any,
    ) -> Any:
        """Run synchronously, chaining ``previous_response_id``."""
        from agents import set_default_openai_client

        client = self._build_client()
        set_default_openai_client(client)

        extra = self._build_extra(self._previous_response_id)
        merged_kwargs = {**kwargs, **extra}

        result = Runner.run_sync(agent, input_data, **merged_kwargs)
        self._capture_response_id(result)
        return result

    # ── async run ───────────────────────────────────────────────────────

    async def run_async(
        self,
        agent: Agent[Any],
        input_data: str,
        **kwargs: Any,
    ) -> Any:
        """Run asynchronously, chaining ``previous_response_id``."""
        from agents import set_default_openai_client

        client = self._build_client()
        set_default_openai_client(client)

        extra = self._build_extra(self._previous_response_id)
        merged_kwargs = {**kwargs, **extra}

        result = await Runner.run(agent, input_data, **merged_kwargs)
        self._capture_response_id(result)
        return result

    # ── internal ────────────────────────────────────────────────────────

    def _build_client(self) -> Any:
        """Build or reuse a CCX client."""
        if self._ccx_config is not None:
            base_url = getattr(self._ccx_config, "base_url", None) or "http://localhost:3000/v1"
            api_key = resolve_api_key(getattr(self._ccx_config, "api_key", None))
            return ccx_client(base_url=base_url, api_key=api_key, timeout=30.0)
        return get_ccx_client()

    def _build_extra(self, prev_id: str | None) -> dict[str, Any]:
        """Build extra-body kwargs including ``previous_response_id``."""
        extra: dict[str, Any] = {}
        if prev_id:
            extra["previous_response_id"] = prev_id
        return extra

    def _capture_response_id(self, result: Any) -> None:
        """Extract and store ``response_id`` from the run result."""
        try:
            if result and result.raw_responses:
                raw = result.raw_responses[-1]
                rid = getattr(raw, "id", None) or getattr(raw, "response_id", None)
                if rid:
                    self._previous_response_id = rid
                    _log.debug("CcxConversation: captured response_id=%s", rid)
        except Exception:
            _log.debug("CcxConversation: could not capture response_id", exc_info=True)
