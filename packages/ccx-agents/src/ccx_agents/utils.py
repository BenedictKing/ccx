# src/ccx_agents/utils.py
"""Internal utilities."""

from __future__ import annotations

import os


def resolve_api_key(api_key: str | None = None) -> str | None:
    """Resolve API key from argument or environment variables."""
    return api_key or os.environ.get("CCX_API_KEY") or os.environ.get("PROXY_ACCESS_KEY")


def resolve_base_url(base_url: str | None = None) -> str:
    """Resolve base URL from argument or environment variable."""
    url = base_url or os.environ.get("CCX_BASE_URL", "http://localhost:3000/v1")
    return url.rstrip("/") + "/"


def resolve_channel(channel: str | None = None) -> str | None:
    """Resolve channel from argument or environment variable."""
    return channel or os.environ.get("CCX_CHANNEL")


def resolve_model(model: str | None = None) -> str | None:
    """Resolve model from argument or environment variable."""
    return model or os.environ.get("CCX_MODEL")


def resolve_api(api: str | None = None) -> str:
    """Resolve API type from argument or environment variable."""
    return api or os.environ.get("CCX_API", "responses")
