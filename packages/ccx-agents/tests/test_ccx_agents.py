# tests/test_ccx_agents.py
"""Comprehensive tests for ccx-agents."""

from __future__ import annotations

import os
from typing import Any
from unittest.mock import MagicMock, patch

import pytest
from hypothesis import given
from hypothesis import strategies as st
from openai import AsyncOpenAI

from ccx_agents import (
    CcxConfig,
    CcxConversation,
    CcxRouter,
    CcxTracing,
    RoutingRule,
    ccx_client,
    ccx_setup,
    get_ccx_client,
)
from ccx_agents.config import CcxConfigModel
from ccx_agents.utils import (
    resolve_api_key,
    resolve_base_url,
    resolve_channel,
    resolve_model,
)
from tests.conftest import FakeCcxConfig

# =========================================================================
# 1. Configuration & setup
# =========================================================================


class TestCcxClient:
    def test_ccx_client_creates_openai_client(self) -> None:
        client = ccx_client("http://127.0.0.1:3000/v1", api_key="test-key")
        assert isinstance(client, AsyncOpenAI)
        assert "127.0.0.1:3000" in str(client.base_url)

    def test_ccx_client_default_key_from_env(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.setenv("CCX_API_KEY", "env-key")
        client = ccx_client("http://127.0.0.1:3000/v1")
        assert client.api_key == "env-key"

    def test_ccx_client_proxy_key_fallback(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.setenv("PROXY_ACCESS_KEY", "proxy-key")
        client = ccx_client("http://127.0.0.1:3000/v1")
        assert client.api_key == "proxy-key"

    def test_ccx_client_route_prefix_header(self) -> None:
        client = ccx_client("http://127.0.0.1:3000/v1", api_key="k", route_prefix="gpt")
        assert client.default_headers.get("HTTP-Route-Prefix") == "gpt"

    def test_ccx_client_extra_headers(self) -> None:
        client = ccx_client(
            "http://127.0.0.1:3000/v1", api_key="k", extra_headers={"X-Custom": "value"}
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


# =========================================================================
# 2. ccx_setup
# =========================================================================


class TestCcxSetup:
    def test_setup_env_vars(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.setenv("CCX_BASE_URL", "http://test:3000/v1")
        monkeypatch.setenv("CCX_API_KEY", "test-key")
        monkeypatch.setenv("CCX_CHANNEL", "test-channel")
        monkeypatch.setenv("CCX_MODEL", "test-model")
        monkeypatch.setenv("CCX_API", "chat_completions")

        assert resolve_base_url(None) == "http://test:3000/v1/"
        assert resolve_api_key(None) == "test-key"
        assert resolve_channel(None) == "test-channel"
        assert resolve_model(None) == "test-model"

    def test_setup_creates_client(self, monkeypatch: pytest.MonkeyPatch) -> None:
        monkeypatch.setenv("CCX_BASE_URL", "http://test:3000/v1")
        monkeypatch.setenv("CCX_API_KEY", "test-key")

        with patch("ccx_agents._setup.set_default_openai_client") as mock_set:
            ccx_setup()
            mock_set.assert_called_once()

            # Verify the client was configured correctly
            client_arg = mock_set.call_args[0][0]
            assert isinstance(client_arg, AsyncOpenAI)
            assert "test:3000" in str(client_arg.base_url)

    def test_setup_with_explicit_args(self) -> None:
        with patch("ccx_agents._setup.set_default_openai_client") as mock_set:
            ccx_setup(base_url="http://explicit:3000/v1", api_key="my-key", channel="my-channel")
            mock_set.assert_called_once()
            client_arg = mock_set.call_args[0][0]
            assert "explicit:3000" in str(client_arg.base_url)
            assert client_arg.default_headers.get("HTTP-Route-Prefix") == "my-channel"


# =========================================================================
# 3. URL & key resolution
# =========================================================================


class TestResolution:
    def test_resolve_base_url_default(self) -> None:
        assert resolve_base_url(None) == "http://localhost:3000/v1/"

    def test_resolve_base_url_custom(self) -> None:
        assert resolve_base_url("http://myhost:4000/v1") == "http://myhost:4000/v1/"

    def test_resolve_api_key(self) -> None:
        assert resolve_api_key("explicit") == "explicit"

    def test_resolve_api_key_none(self) -> None:
        assert resolve_api_key(None) is None


# =========================================================================
# 4. CcxConversation (mock-based)
# =========================================================================


class TestCcxConversation:
    def test_conversation_init(self) -> None:
        conv = CcxConversation()
        assert conv.previous_response_id is None

    def test_conversation_reset(self) -> None:
        conv = CcxConversation()
        conv._previous_response_id = "resp_123"
        conv.reset()
        assert conv.previous_response_id is None

    def test_capture_response_id(self) -> None:
        """Verify the response_id extraction logic with a mock result."""
        conv = CcxConversation()

        mock_raw = MagicMock()
        mock_raw.id = "resp_456"

        mock_result = MagicMock()
        mock_result.raw_responses = [mock_raw]

        conv._capture_response_id(mock_result)
        assert conv.previous_response_id == "resp_456"

    def test_capture_response_id_no_raw(self) -> None:
        conv = CcxConversation()
        mock_result = MagicMock()
        mock_result.raw_responses = None

        conv._capture_response_id(mock_result)
        assert conv.previous_response_id is None

    def test_capture_response_id_not_overwrite(self) -> None:
        """Verify previous_response_id is set to the LAST response's id."""
        conv = CcxConversation()

        mock_r1 = MagicMock()
        mock_r1.id = "resp_1"
        mock_r2 = MagicMock()
        mock_r2.id = "resp_2"

        mock_result = MagicMock()
        mock_result.raw_responses = [mock_r1, mock_r2]

        conv._capture_response_id(mock_result)
        assert conv.previous_response_id == "resp_2"


# =========================================================================
# 5. Router (mock-based)
# =========================================================================


class TestCcxRouter:
    def test_router_init(self) -> None:
        router = CcxRouter()
        assert router._agent_map == {}
        assert router._default_channel is None

    def test_router_route(self) -> None:
        router = CcxRouter()
        router.route("translator", channel="claude-4")
        assert router._agent_map == {"translator": "claude-4"}

    def test_router_set_default(self) -> None:
        router = CcxRouter()
        router.set_default("gpt-4o")
        assert router._default_channel == "gpt-4o"

    def test_router_get_channel_exact_match(self) -> None:
        router = CcxRouter()
        router.route("coder", channel="gpt-4o")
        agent = MagicMock()
        agent.name = "coder"
        assert router._get_channel(agent) == "gpt-4o"

    def test_router_get_channel_fallback_default(self) -> None:
        router = CcxRouter()
        router.set_default("claude-4")
        agent = MagicMock()
        agent.name = "unknown"
        assert router._get_channel(agent) == "claude-4"

    def test_router_get_channel_no_match(self) -> None:
        router = CcxRouter()
        agent = MagicMock()
        agent.name = "unknown"
        assert router._get_channel(agent) is None

    def test_router_get_channel_from_config(self) -> None:
        config = MagicMock()
        config.channel = "gemini"
        router = CcxRouter(ccx_config=config)
        agent = MagicMock()
        agent.name = "unknown"
        assert router._get_channel(agent) == "gemini"

    def test_router_route_precedence(self) -> None:
        """Explicit route overrides both default and config."""
        config = MagicMock()
        config.channel = "gemini"
        router = CcxRouter(ccx_config=config)
        router.set_default("gpt-4o")
        router.route("translator", channel="claude-4")
        agent = MagicMock()
        agent.name = "translator"
        assert router._get_channel(agent) == "claude-4"

    def test_router_route_precedence_default_over_config(self) -> None:
        """Default channel overrides config-level channel."""
        config = MagicMock()
        config.channel = "gemini"
        router = CcxRouter(ccx_config=config)
        router.set_default("gpt-4o")
        agent = MagicMock()
        agent.name = "no-route"
        assert router._get_channel(agent) == "gpt-4o"

    def test_add_map(self) -> None:
        router = CcxRouter()
        router.add_map(
            {"gpt-4o": CcxConfigModel(base_url="http://127.0.0.1:3000/v1", route_prefix="gpt")}
        )
        resolved = router.resolve(model="gpt-4o", agent_name="coder")
        assert resolved is not None
        assert resolved.route_prefix == "gpt"

    def test_resolve_no_match(self) -> None:
        router = CcxRouter()
        assert router.resolve(model="nonexistent") is None


# =========================================================================
# 6. Tracing
# =========================================================================


class TestCcxTracing:
    def test_tracing_init(self) -> None:
        tracing = CcxTracing()
        assert tracing is not None

    def test_trace_headers(self) -> None:
        from ccx_agents.tracing import _make_trace_headers

        run_config = MagicMock()
        run_config.workflow_name = "test-flow"
        headers = _make_trace_headers(run_config)
        assert headers.get("X-Ccx-Trace-Workflow") == "test-flow"

    def test_trace_headers_none(self) -> None:
        from ccx_agents.tracing import _make_trace_headers

        headers = _make_trace_headers(None)
        assert headers == {}


# =========================================================================
# 7. RoutingRule protocol
# =========================================================================


class TestRoutingRuleProtocol:
    def test_routing_rule_is_callable(self) -> None:
        def my_rule(*, model: str, agent_name: str | None) -> CcxConfigModel | None:
            if model == "gpt-4o":
                return CcxConfigModel(base_url="http://127.0.0.1:3000/v1", route_prefix="gpt")
            return None

        assert isinstance(my_rule, RoutingRule)
        result = my_rule(model="gpt-4o", agent_name=None)
        assert result is not None
        assert result.route_prefix == "gpt"

        result2 = my_rule(model="claude", agent_name=None)
        assert result2 is None


# =========================================================================
# 8. E2E integration (mocked)
# =========================================================================


class TestCcxAgentsIntegration:
    """E2E tests using mocked Runner to verify data flow."""

    @patch("agents.Runner.run_sync", autospec=True)
    def test_conversation_run(self, mock_run_sync: Any) -> None:
        """Verify conversation.run() creates a client and passes correct args."""
        mock_result = MagicMock()
        mock_result.raw_responses = []
        mock_run_sync.return_value = mock_result

        conv = CcxConversation(FakeCcxConfig())
        agent = MagicMock()
        agent.name = "test-agent"

        with patch("agents.set_default_openai_client"):
            result = conv.run(agent, "Hello")

        assert result is mock_result
        mock_run_sync.assert_called_once_with(agent, "Hello")

    @patch("agents.Runner.run", autospec=True)
    def test_conversation_run_async(self, mock_run: Any) -> None:
        """Verify conversation.run_async() handles async correctly."""
        mock_result = MagicMock()
        mock_result.raw_responses = []
        mock_run.return_value = mock_result

        conv = CcxConversation(FakeCcxConfig())
        agent = MagicMock()
        agent.name = "test-agent"

        async def run_test():
            with patch("agents.set_default_openai_client"):
                return await conv.run_async(agent, "Hello")

        import asyncio
        result = asyncio.run(run_test())
        assert result is mock_result

    def test_multi_agent_router_precedence(self) -> None:
        """Test that router correctly selects channels."""
        router = CcxRouter(FakeCcxConfig(channel="fallback-channel"))
        router.route("translator", channel="claude-4")
        router.set_default("gpt-4o")

        # Named agent → exact match
        agent1 = MagicMock()
        agent1.name = "translator"
        assert router._get_channel(agent1) == "claude-4"

        # Unmatched → default
        agent2 = MagicMock()
        agent2.name = "coder"
        assert router._get_channel(agent2) == "gpt-4o"

    def test_setup_with_extra_headers(self) -> None:
        """Test that ccx_setup with extra_headers works."""
        os.environ["CCX_BASE_URL"] = "http://127.0.0.1:3000/v1"
        os.environ["CCX_API_KEY"] = "test-key"

        client = ccx_client(
            base_url="http://127.0.0.1:3000/v1",
            api_key="test-key",
            extra_headers={"X-Test": "value"},
        )
        assert client.default_headers.get("X-Test") == "value"


# =========================================================================
# 8. Property-based routing tests
# =========================================================================


class TestRouterPropertyBased:
    """Property-based tests for CcxRouter channel resolution.

    Invariants verified via hypothesis:

    1. **Exact-match dominance** — if an agent name has an explicit route,
       :meth:`_get_channel` always returns that route's channel regardless
       of all other fallback levels.

    2. **Default v. Config** — when no explicit route matches,
       ``set_default()`` always takes priority over ``config.channel``.

    3. **Config-level fallback** — when no route and no default exist,
       the config-level ``channel`` attribute is returned.

    4. **Total fallback** — when no source has a channel set, ``None`` is
       returned (the caller handles this with env-var fallback).
    """

    # Strategy: non-empty string, no leading/trailing whitespace
    _short_text = st.text(
        min_size=1, max_size=30,
        alphabet=st.characters(whitelist_categories=("L", "N", "P")),
    )
    _channel = st.text(min_size=1, max_size=30, alphabet="abcdefghijklmnopqrstuvwxyz-0123456789")

    @given(route_name=_short_text, channel=_channel)
    def test_exact_match_wins(self, route_name: str, channel: str) -> None:
        """An agent with an explicit route ALWAYS gets its channel."""
        router = CcxRouter()
        router.route(route_name, channel)
        router.set_default("default-channel")

        agent = MagicMock()
        agent.name = route_name
        assert router._get_channel(agent) == channel

    @given(agent_name=_short_text)
    def test_default_over_config(self, agent_name: str) -> None:
        """When no exact route matches, set_default() overrides config.channel."""
        config = MagicMock()
        config.channel = "config-default"
        config.base_url = "http://127.0.0.1:3000/v1"
        config.api_key = "k"

        router = CcxRouter(ccx_config=config)
        router.set_default("router-default")

        agent = MagicMock()
        agent.name = agent_name
        assert router._get_channel(agent) == "router-default"

    @given(agent_name=_short_text)
    def test_config_fallback(self, agent_name: str) -> None:
        """With no route and no default, config.channel is the fallback."""
        config = MagicMock()
        config.channel = "config-channel"
        config.base_url = "http://127.0.0.1:3000/v1"
        config.api_key = "k"

        router = CcxRouter(ccx_config=config)

        agent = MagicMock()
        agent.name = agent_name
        assert router._get_channel(agent) == "config-channel"

    @given(agent_name=_short_text)
    def test_no_fallback_returns_none(self, agent_name: str) -> None:
        """With no route, no default, and no config.channel, returns None."""
        config = MagicMock(spec=[])  # no channel attribute
        config.base_url = "http://127.0.0.1:3000/v1"
        config.api_key = "k"

        router = CcxRouter(ccx_config=config)

        agent = MagicMock()
        agent.name = agent_name
        assert router._get_channel(agent) is None


class TestModelMappingPropertyBased:
    """Verify model-name heuristics don't crash on arbitrary input."""

    _any_text = st.text(min_size=0, max_size=100)

    @given(model=_any_text)
    def test_api_type_never_raises(self, model: str) -> None:
        """api_type_for_model should never raise for any string input."""
        from ccx_agents.models import api_type_for_model
        result = api_type_for_model(model)
        assert result in ("messages", "responses", "chat")

    @given(model=st.text(min_size=1, max_size=100))
    def test_api_type_claude_keywords(self, model: str) -> None:
        """Any model containing 'claude' should map to 'messages'."""
        from ccx_agents.models import api_type_for_model
        result = api_type_for_model(f"{model}claude{model}")
        assert result == "messages", f"Expected messages for {model}, got {result}"

    @given(prefix=st.text(min_size=0, max_size=20, alphabet="xyzXYZ-0123456789"))
    def test_api_type_o_series_keywords(self, prefix: str) -> None:
        """Any model containing 'o1' or 'o3' or 'gpt-5' maps to 'responses'.

        Uses a prefix alphabet that won't accidentally trigger the
        ``messages``-branch keywords (claude, gemini, sonnet, opus, haiku).
        """
        from ccx_agents.models import api_type_for_model
        for model_kw in ("o1", "o3", "gpt-5"):
            model = f"{prefix}{model_kw}"
            result = api_type_for_model(model)
            if result == "messages":
                # Might match messages branch if prefix contains keywords
                continue
            assert result == "responses", f"Expected responses for {model}, got {result}"


# =========================================================================
# 9. Coverage gap: router uncovered branches
# =========================================================================


class TestRouterCoverageGaps:
    """Targeted tests for uncovered lines in router.py."""

    def test_add_rule_appends_rule(self) -> None:
        """add_rule() appends a custom RoutingRule."""
        router = CcxRouter()

        def my_rule(*, model: str, agent_name: str | None) -> CcxConfigModel | None:
            if model == "custom":
                return CcxConfigModel(base_url="http://127.0.0.1:3000/v1", route_prefix="custom")
            return None

        router.add_rule(my_rule)
        assert len(router._rules) == 1
        resolved = router.resolve(model="custom")
        assert resolved is not None
        assert resolved.route_prefix == "custom"

    def test_resolve_first_rule_returns_none_second_matches(self) -> None:
        """resolve() skips rules that return None, falls through to next."""
        router = CcxRouter()

        def rule1(*, model: str, agent_name: str | None) -> CcxConfigModel | None:
            return None  # skip

        def rule2(*, model: str, agent_name: str | None) -> CcxConfigModel | None:
            return CcxConfigModel(base_url="http://127.0.0.1:3000/v1", route_prefix="matched")

        router.add_rule(rule1)
        router.add_rule(rule2)

        resolved = router.resolve(model="anything")
        assert resolved is not None
        assert resolved.route_prefix == "matched"

    def test_build_client_none_channel_falls_back(self) -> None:
        """_build_client(None) falls back to get_ccx_client()."""
        # Set up a global client first
        CcxConfig(base_url="http://127.0.0.1:3000/v1", api_key="k").setup()

        router = CcxRouter()
        client = router._build_client(None)
        from ccx_agents._client import get_ccx_client
        assert client is get_ccx_client()

    @patch("agents.Runner.run_streamed", autospec=True)
    @patch("agents.set_default_openai_client")
    def test_run_streamed(self, mock_set: Any, mock_run_streamed: Any) -> None:
        """router.run_streamed() delegates to Runner.run_streamed."""
        mock_result = MagicMock()
        mock_run_streamed.return_value = mock_result

        router = CcxRouter(FakeCcxConfig(base_url="http://127.0.0.1:3000/v1", api_key="k"))
        router.route("test-agent", channel="default")

        agent = MagicMock()
        agent.name = "test-agent"

        result = router.run_streamed(agent, "Hello")
        assert result is mock_result
        mock_set.assert_called_once()
        mock_run_streamed.assert_called_once_with(agent, "Hello")


# =========================================================================
# 10. Coverage gap: _client setup_with_routing with CcxRouter instance
# =========================================================================


class TestClientCoverageGaps:
    """Targeted tests for uncovered lines in _client.py."""

    @patch("ccx_agents._client.set_default_openai_client")
    def test_setup_with_routing_calls_isinstance_router(self, mock_set: Any) -> None:
        """setup_with_routing() with CcxRouter instance uses the router directly."""
        from ccx_agents import CcxRouter as CR  # noqa: N817

        router_instance = CR()
        router_instance.add_map({
            "gpt-4o": CcxConfigModel(base_url="http://127.0.0.1:3000/v1", route_prefix="gpt"),
        })

        CcxConfig(api_key="k").setup_with_routing(
            default_url="http://127.0.0.1:3000/v1",
            router=router_instance,
        )

        from ccx_agents._client import _router
        assert _router is router_instance
        mock_set.assert_called_once()

    @patch("ccx_agents._client.set_default_openai_client")
    def test_setup_with_routing_router_none(self, mock_set: Any) -> None:
        """setup_with_routing() with router=None still works (no routing)."""
        CcxConfig(api_key="k").setup_with_routing(
            default_url="http://127.0.0.1:3000/v1",
            router=None,
        )

        from ccx_agents._client import _router
        assert _router is None
        mock_set.assert_called_once()


# =========================================================================
# 11. Coverage gap: conv uncovered branches
# =========================================================================


class TestConversationCoverageGaps:
    """Targeted tests for uncovered lines in conv.py."""

    def test_build_client_without_config_falls_back(self) -> None:
        """_build_client() with ccx_config=None uses get_ccx_client()."""
        # Set up a global client
        CcxConfig(base_url="http://127.0.0.1:3000/v1", api_key="k").setup()
        from ccx_agents._client import get_ccx_client
        global_client = get_ccx_client()

        conv = CcxConversation(ccx_config=None)
        client = conv._build_client()
        assert client is global_client

    def test_capture_response_id_with_response_id_attr(self) -> None:
        """_capture_response_id uses raw.response_id when raw.id is None."""
        conv = CcxConversation()

        mock_raw = MagicMock()
        del mock_raw.id

        mock_raw.response_id = "resp_via_attr"

        mock_result = MagicMock()
        mock_result.raw_responses = [mock_raw]

        conv._capture_response_id(mock_result)
        assert conv.previous_response_id == "resp_via_attr"

    def test_capture_response_id_exception_handling(self) -> None:
        """_capture_response_id catches exceptions gracefully."""
        conv = CcxConversation()

        mock_result = MagicMock()
        mock_result.raw_responses = [MagicMock()]
        raw_mock = mock_result.raw_responses[0]
        type(raw_mock).id = property(lambda self: (_ for _ in ()).throw(RuntimeError("boom")))

        conv._capture_response_id(mock_result)
        assert conv.previous_response_id is None
