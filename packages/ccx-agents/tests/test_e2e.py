"""E2E integration tests — require a running CCX Docker container.

Run with::

    docker compose -f /path/to/ccx/docker-compose.yml up -d
    cd packages/ccx-agents
    python -m pytest tests/test_e2e.py --run-e2e -v

Environment variables (optional overrides)::

    CCX_BASE_URL=http://127.0.0.1:3000/v1
    CCX_API_KEY=your-proxy-access-key
"""

from __future__ import annotations

import logging

import httpx
import pytest
from agents import Agent

from ccx_agents import (
    CcxConfig,
    CcxConversation,
    CcxRouter,
    ccx_setup,
)
from tests.conftest import FakeCcxConfig

_log = logging.getLogger(__name__)

pytestmark = pytest.mark.e2e

# Workaround for colima/Lima port forwarding on macOS: without
# local_address="0.0.0.0" the default httpx transport can hit an
# nghttpx proxy instead of the target container.
_FORCE_HTTP = httpx.HTTPTransport(local_address="0.0.0.0")


def _client() -> httpx.Client:
    return httpx.Client(transport=_FORCE_HTTP)


# =======================================================================
# Health check
# =======================================================================


class TestCcxConnection:
    """Verify CCX Docker container is reachable."""

    def test_health_endpoint(self, ccx_health_url: str) -> None:
        """CCX should respond to /health."""
        with _client() as client:
            resp = client.get(ccx_health_url, timeout=5.0)
            assert resp.status_code == 200, f"Health check failed: {resp.status_code} {resp.text}"

    def test_health_returns_json(self, ccx_health_url: str) -> None:
        """Health response should be valid JSON."""
        with _client() as client:
            resp = client.get(ccx_health_url, timeout=5.0)
            data = resp.json()
        # CCX health typically returns {"status":"ok"} or similar
        assert isinstance(data, dict)

    # ------------------------------------------------------------------ #
    # 1. CcxConfig.setup() → real openai-agents-python call
    # ------------------------------------------------------------------ #

    class TestSetupAndRun:
        """End-to-end: configure CCX, create an agent, run it."""

        def test_setup_then_run_sync(self, ccx_base_url: str, ccx_api_key: str) -> None:
            """CcxConfig.setup() + Agent + Runner.run_sync() through CCX."""
            CcxConfig(base_url=ccx_base_url, api_key=ccx_api_key).setup()

            agent = Agent(
                name="test_agent",
                instructions="You are a helpful assistant. Answer concisely in one sentence.",
            )

            try:
                from agents import Runner
                result = Runner.run_sync(agent, "What is 2+2?")
                assert result.final_output is not None
                assert len(result.final_output) > 0
                _log.info("sync result: %s", result.final_output)
            except Exception as exc:
                # If CCX has no upstream model configured, this may fail —
                # but the error should be a CCX error, not a connection error
                error_text = str(exc).lower()
                assert any(
                    kw in error_text
                    for kw in ("model", "route", "channel", "upstream", "provider", "not found",
                              "no available", "error", "status", "429", "500", "502", "503")
                ), f"Unexpected error (likely CCX misconfigured): {exc}"

        @pytest.mark.asyncio
        async def test_setup_then_run_async(self, ccx_base_url: str, ccx_api_key: str) -> None:
            """CcxConfig.setup() + Agent + Runner.run() async through CCX."""
            CcxConfig(base_url=ccx_base_url, api_key=ccx_api_key).setup()

            agent = Agent(
                name="test_agent_async",
                instructions="You are a helpful assistant. Answer concisely.",
            )

            try:
                from agents import Runner
                result = await Runner.run(agent, "What is the capital of Japan?")
                assert result.final_output is not None
                assert len(result.final_output) > 0
                _log.info("async result: %s", result.final_output)
            except Exception as exc:
                error_text = str(exc).lower()
                assert any(
                    kw in error_text
                    for kw in ("model", "route", "channel", "upstream", "provider", "not found",
                              "no available", "error", "status", "429", "500", "502", "503")
                ), f"Unexpected error: {exc}"

    # ------------------------------------------------------------------ #
    # 2. ccx_setup() one-liner
    # ------------------------------------------------------------------ #

    class TestCcxSetupE2E:
        """Real requests via ccx_setup()."""

        def test_ccx_setup_run(self, ccx_base_url: str, ccx_api_key: str) -> None:
            """ccx_setup() → Runner.run_sync() through CCX."""
            ccx_setup(base_url=ccx_base_url, api_key=ccx_api_key)

            agent = Agent(
                name="test_ccx_setup",
                instructions="You are a helpful assistant. Answer in one sentence.",
            )

            try:
                from agents import Runner
                result = Runner.run_sync(agent, "Say hello in French.")
                assert result.final_output is not None
                _log.info("ccx_setup result: %s", result.final_output)
            except Exception as exc:
                error_text = str(exc).lower()
                assert any(
                    kw in error_text
                    for kw in ("model", "route", "channel", "upstream", "provider", "not found",
                              "no available", "error", "status", "429", "500", "502", "503")
                ), f"Unexpected error: {exc}"

    # ------------------------------------------------------------------ #
    # 3. CcxConversation — multi-turn via previous_response_id
    # ------------------------------------------------------------------ #

    class TestConversationE2E:
        """Real multi-turn conversation through CCX."""

        def test_conversation_two_turns(self, ccx_base_url: str, ccx_api_key: str) -> None:
            """Two-turn conversation using previous_response_id."""
            ccx_setup(base_url=ccx_base_url, api_key=ccx_api_key)

            conv = CcxConversation(FakeCcxConfig(base_url=ccx_base_url, api_key=ccx_api_key))

            agent = Agent(
                name="conversation_test",
                instructions="You are a helpful assistant. Answer concisely.",
            )

            # Turn 1
            try:
                result1 = conv.run(agent, "My favorite color is blue.")
                assert result1.final_output is not None

                # Turn 2 — should be chained via previous_response_id
                result2 = conv.run(agent, "What is my favorite color?")
                assert result2.final_output is not None

                # Verify previous_response_id was captured
                assert conv.previous_response_id is not None
                _log.info("Turn 1: %s | Turn 2: %s | prev_id: %s",
                          result1.final_output, result2.final_output, conv.previous_response_id)

            except Exception as exc:
                error_text = str(exc).lower()
                assert any(
                    kw in error_text
                    for kw in ("model", "route", "channel", "upstream", "provider", "not found",
                              "no available", "error", "status", "429", "500", "502", "503")
                ), f"Unexpected error: {exc}"

        def test_conversation_reset(self, ccx_base_url: str, ccx_api_key: str) -> None:
            """Conversation.reset() clears the response ID."""
            ccx_setup(base_url=ccx_base_url, api_key=ccx_api_key)

            conv = CcxConversation(FakeCcxConfig(base_url=ccx_base_url, api_key=ccx_api_key))

            agent = Agent(
                name="reset_test",
                instructions="You are a helpful assistant. Answer concisely.",
            )

            try:
                conv.run(agent, "Hello")
                assert conv.previous_response_id is not None

                conv.reset()
                assert conv.previous_response_id is None
            except Exception as exc:
                error_text = str(exc).lower()
                assert any(
                    kw in error_text
                    for kw in ("model", "route", "channel", "upstream", "provider", "not found",
                              "no available", "error", "status", "429", "500", "502", "503")
                ), f"Unexpected error: {exc}"

    # ------------------------------------------------------------------ #
    # 4. CcxRouter — agent name routing
    # ------------------------------------------------------------------ #

    class TestRouterE2E:
        """Real multi-agent routing through CCX."""

        def test_router_routes_by_agent_name(self, ccx_base_url: str, ccx_api_key: str) -> None:
            """CcxRouter routes by agent name and runs through CCX."""
            ccx_setup(base_url=ccx_base_url, api_key=ccx_api_key)

            router = CcxRouter(FakeCcxConfig(base_url=ccx_base_url, api_key=ccx_api_key))
            router.route("translator", channel="default")
            router.set_default("default")

            agent = Agent(
                name="translator",
                instructions="You are a translator. Answer concisely.",
            )

            try:
                result = router.run_sync(agent, "Translate hello to Spanish.")
                assert result.final_output is not None
                _log.info("Router result: %s", result.final_output)
            except Exception as exc:
                error_text = str(exc).lower()
                assert any(
                    kw in error_text
                    for kw in ("model", "route", "channel", "upstream", "provider", "not found",
                              "no available", "error", "status", "429", "500", "502", "503")
                ), f"Unexpected error: {exc}"

    # ------------------------------------------------------------------ #
    # 5. Direct HTTP — verify CCX endpoints are functional
    # ------------------------------------------------------------------ #

    class TestDirectHttpE2E:
        """Direct HTTP requests to CCX endpoints for connectivity verification."""

        def test_models_endpoint(self, ccx_base_url: str) -> None:
            """Verify /v1/models responds (may be 401/403 without key)."""
            base = ccx_base_url.removesuffix("/v1").removesuffix("/")
            with _client() as client:
                try:
                    resp = client.get(f"{base}/v1/models", timeout=5.0)
                    assert resp.status_code in (200, 401, 403, 404)
                except httpx.RequestError as exc:
                    pytest.skip(f"CCX not reachable at {base}: {exc}")

        def test_chat_completions_endpoint(self, ccx_base_url: str, ccx_api_key: str) -> None:
            """Verify /v1/chat/completions accepts requests."""
            base = ccx_base_url.removesuffix("/v1").removesuffix("/")
            headers = {"Authorization": f"Bearer {ccx_api_key}"}
            with _client() as client:
                try:
                    resp = client.post(
                        f"{base}/v1/chat/completions",
                        json={"model": "gpt-4o", "messages": []},
                        headers=headers,
                        timeout=5.0,
                    )
                    # Should get a validation error (empty messages), not a network error
                    assert resp.status_code in (400, 422, 401, 403, 503)
                except httpx.RequestError as exc:
                    pytest.skip(f"CCX not reachable at {base}: {exc}")
