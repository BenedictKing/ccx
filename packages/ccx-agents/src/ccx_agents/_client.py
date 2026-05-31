# src/ccx_agents/_client.py
"""ccx_client factory & global CcxConfig entry-point."""

from __future__ import annotations

import logging
from typing import Any

from agents import set_default_openai_client
from openai import AsyncOpenAI

from .config import CcxConfigModel
from .router import CcxRouter
from .utils import resolve_api_key, resolve_base_url

_log = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# internal singleton state
# ---------------------------------------------------------------------------

_client: AsyncOpenAI | None = None
_router: CcxRouter | None = None

# ---------------------------------------------------------------------------
# client factory
# ---------------------------------------------------------------------------


def ccx_client(
    base_url: str,
    api_key: str | None = None,
    route_prefix: str | None = None,
    extra_headers: dict[str, str] | None = None,
    **openai_kwargs: Any,
) -> AsyncOpenAI:
    """Build an :class:`AsyncOpenAI` client pre-configured for the CCX proxy.

    Parameters
    ----------
    base_url:
        Full CCX endpoint URL, e.g. ``"http://localhost:3000/v1"``.
    api_key:
        CCX proxy access key (``PROXY_ACCESS_KEY``).  Falls back to the
        ``CCX_API_KEY`` or ``PROXY_ACCESS_KEY`` environment variable.
    route_prefix:
        If set, every request carries the ``HTTP-Route-Prefix`` header so
        CCX pins the request to a specific upstream channel.
    extra_headers:
        Additional HTTP headers sent with every request.
    **openai_kwargs:
        Forwarded directly to ``AsyncOpenAI(…)``.
    """
    resolved_key = resolve_api_key(api_key)

    headers: dict[str, str] = {}
    if route_prefix:
        headers["HTTP-Route-Prefix"] = route_prefix
    if extra_headers:
        headers.update(extra_headers)

    client = AsyncOpenAI(
        base_url=base_url.rstrip("/") + "/",
        api_key=resolved_key,
        default_headers=headers,
        **openai_kwargs,
    )
    return client


def get_ccx_client() -> AsyncOpenAI | None:
    """Return the currently active global CCX client (if any)."""
    return _client


# ---------------------------------------------------------------------------
# one-line setup API
# ---------------------------------------------------------------------------


def _setup_single(
    base_url: str,
    api_key: str | None = None,
    route_prefix: str | None = None,
    extra_headers: dict[str, str] | None = None,
    use_for_tracing: bool = False,
) -> None:
    """Build a single client and register it as the SDK default."""
    global _client, _router  # noqa: PLW0603
    _client = ccx_client(
        base_url=base_url,
        api_key=api_key,
        route_prefix=route_prefix,
        extra_headers=extra_headers,
    )
    _router = None
    set_default_openai_client(_client, use_for_tracing=use_for_tracing)
    _log.info("CCX client set - base_url=%s route_prefix=%s", base_url, route_prefix)



class CcxConfig:
    """One-line entry-point for routing ``openai-agents-python`` through CCX.

    **Quick start** - everything via a single CCX endpoint::

        from ccx_agents import CcxConfig

        CcxConfig(base_url="http://localhost:3000/v1", api_key="sk-xxx").setup()

    **Advanced routing** - different models to different CCX channels::

        CcxConfig(api_key="sk-xxx").setup_with_routing(
            default_url="http://localhost:3000/v1",
            router={
                "gpt-4o":        CcxConfigModel(...),
                "claude-sonnet-4": CcxConfigModel(api_type="messages"),
            },
        )

    After setup, import ``agents.Agent / Runner`` and use them normally.
    """

    def __init__(
        self,
        base_url: str | None = None,
        api_key: str | None = None,
    ) -> None:
        """Configure the CCX connection parameters.

        Parameters
        ----------
        base_url:
            CCX proxy endpoint URL. Falls back to ``CCX_BASE_URL`` env var
            or ``http://localhost:3000/v1``.
        api_key:
            CCX proxy access key. Falls back to ``CCX_API_KEY`` or
            ``PROXY_ACCESS_KEY`` env var.
        """
        self.base_url = base_url
        self.api_key = api_key

    def setup(
        self,
        base_url: str | None = None,
        api_key: str | None = None,
        route_prefix: str | None = None,
        extra_headers: dict[str, str] | None = None,
        use_for_tracing: bool = False,
    ) -> None:
        """Configure CCX as the sole LLM provider for the Agents SDK.

        Resolution order for ``base_url`` and ``api_key``:
        explicit argument > instance attribute > environment variable > default.

        All model requests go to the same CCX endpoint.  Routing to the
        correct upstream channel is handled by CCX itself (model matching,
        ``route_prefix``, etc.).
        """
        url = resolve_base_url(base_url or self.base_url)
        key = resolve_api_key(api_key or self.api_key)
        return _setup_single(
            base_url=url,
            api_key=key,
            route_prefix=route_prefix,
            extra_headers=extra_headers,
            use_for_tracing=use_for_tracing,
        )

    def setup_with_routing(
        self,
        default_url: str | None = None,
        api_key: str | None = None,
        router: dict[str, CcxConfigModel] | CcxRouter | None = None,
        use_for_tracing: bool = False,
    ) -> None:
        """Configure CCX with a multi-channel model router.

        Resolution order: explicit argument > instance attribute > env var.

        If ``router`` is a dict, it's treated as a static ``{model: config}``
        map.  If it's a :class:`CcxRouter`, rules are evaluated in order.

        Models not matched by any rule fall through to the ``default_url``
        endpoint.
        """
        global _client, _router  # noqa: PLW0603
        url = resolve_base_url(default_url or self.base_url)
        key = resolve_api_key(api_key or self.api_key)

        if isinstance(router, dict):
            _router = CcxRouter()
            _router.add_map(router)
        elif isinstance(router, CcxRouter):
            _router = router
        else:
            _router = None

        # Build the default client — the model-aware provider will
        # override this per-request when a routing match is found.
        _client = ccx_client(
            base_url=url,
            api_key=key,
        )
        set_default_openai_client(_client, use_for_tracing=use_for_tracing)
        _log.info(
            "CCX client with routing set - default=%s router=%s",
            url,
            "present" if _router else "none",
        )
