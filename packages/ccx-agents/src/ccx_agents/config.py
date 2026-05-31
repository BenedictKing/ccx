# src/ccx_agents/config.py
"""Configuration model for CCX client routing."""

from __future__ import annotations

import dataclasses
from typing import Literal, Protocol, runtime_checkable

CcxApiType = Literal["chat", "responses", "messages", "gemini", "auto"]


@runtime_checkable
class CcxConfigProtocol(Protocol):
    """Protocol for objects that configure a CCX client connection.

    Used by :class:`CcxConversation` and :class:`CcxRouter` instead of
    ``Any`` — any object with ``base_url`` and ``api_key`` attributes
    is accepted.  ``channel`` is optional (accessed via ``getattr``).
    """

    base_url: str | None
    api_key: str | None


@dataclasses.dataclass
class CcxConfigModel:
    """Config for a single CCX upstream channel."""

    #: The full CCX endpoint URL, e.g. ``http://localhost:3000/v1``
    base_url: str
    #: API key (``PROXY_ACCESS_KEY`` or upstream-specific key)
    api_key: str | None = None
    #: Which CCX API bucket to target
    #:   - ``"chat"``      → ``/v1/chat/completions``
    #:   - ``"responses"`` → ``/v1/responses``
    #:   - ``"messages"``  → ``/v1/messages`` (Claude)
    #:   - ``"auto"``      → let openai-agents-python decide (default)
    api_type: CcxApiType = "auto"
    #: If set, adds a ``HTTP-Route-Prefix`` header to route to a specific CCX channel.
    route_prefix: str | None = None
    #: Extra headers to send with every request.
    extra_headers: dict[str, str] | None = None
