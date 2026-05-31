# tests/test_client.py
"""Tests for ccx_agents client & routing."""

from typing import Any
from unittest.mock import patch

import pytest
from openai import AsyncOpenAI

from ccx_agents import CcxConfig, ccx_client, get_ccx_client
from ccx_agents.config import CcxConfigModel


class TestCcxClient:
    def test_ccx_client_creates_openai_client(self) -> None:
        client = ccx_client("http://127.0.0.1:3000/v1", api_key="test-key")
        assert isinstance(client, AsyncOpenAI)
        assert "127.0.0.1:3000" in str(client.base_url)

    def test_ccx_client_default_key_from_env(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.setenv("CCX_API_KEY", "env-key")
        client = ccx_client("http://127.0.0.1:3000/v1")
        assert client.api_key == "env-key"

    def test_ccx_client_route_prefix_header(self) -> None:
        client = ccx_client(
            "http://127.0.0.1:3000/v1",
            api_key="k",
            route_prefix="gpt",
        )
        assert client.default_headers.get("HTTP-Route-Prefix") == "gpt"

    def test_ccx_client_extra_headers(self) -> None:
        client = ccx_client(
            "http://127.0.0.1:3000/v1",
            api_key="k",
            extra_headers={"X-Custom": "value"},
        )
        assert client.default_headers.get("X-Custom") == "value"


class TestCcxGlobal:
    def test_setup_stores_client(self) -> None:
        CcxConfig(base_url="http://127.0.0.1:3000/v1", api_key="test").setup()
        client = get_ccx_client()
        assert client is not None
        assert isinstance(client, AsyncOpenAI)

    @patch("ccx_agents._client.set_default_openai_client")
    def test_setup_calls_sdk_registration(self, mock_set: Any) -> None:
        CcxConfig(base_url="http://127.0.0.1:3000/v1", api_key="k").setup()
        mock_set.assert_called_once()
        assert mock_set.call_args[0][0] is get_ccx_client()

    def test_setup_with_routing_dict(self) -> None:
        CcxConfig(api_key="k").setup_with_routing(
            default_url="http://127.0.0.1:3000/v1",
            router={
                "gpt-4o": CcxConfigModel(base_url="http://127.0.0.1:3000/v1", route_prefix="gpt"),
            },
        )
        client = get_ccx_client()
        assert client is not None


class TestModelMapping:
    """Verify the model → API-type heuristics in api_type_for_model."""

    def test_claude_model(self) -> None:
        from ccx_agents.models import api_type_for_model
        assert api_type_for_model("claude-sonnet-4-20250514") == "messages"
        assert api_type_for_model("gemini-2.5-pro") == "messages"

    def test_o_series_model(self) -> None:
        from ccx_agents.models import api_type_for_model
        assert api_type_for_model("o3-2025-04-01") == "responses"
        assert api_type_for_model("o1-preview") == "responses"

    def test_chat_model(self) -> None:
        from ccx_agents.models import api_type_for_model
        assert api_type_for_model("gpt-4o") == "chat"
        assert api_type_for_model("gpt-4o-mini") == "chat"
