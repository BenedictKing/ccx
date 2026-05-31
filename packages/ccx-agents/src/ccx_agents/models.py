# src/ccx_agents/models.py
"""Model name mappings and helpers."""

from __future__ import annotations

CcxChannel = str
"""A CCX upstream channel name, e.g. ``"claude-4"``, ``"gpt-4o"``."""


def api_type_for_model(model: str) -> str:
    """Map a model name to the CCX API type we should use.

    openai-agents-python defaults to the Responses API (``/v1/responses``).
    For models / providers that don't support Responses, we fall back.
    """
    model_lower = model.lower()
    if any(k in model_lower for k in ("claude", "gemini", "sonnet", "opus", "haiku")):
        return "messages"
    if any(k in model_lower for k in ("o1", "o3", "gpt-5")):
        return "responses"
    return "chat"
