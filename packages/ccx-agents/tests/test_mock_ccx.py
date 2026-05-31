"""Mock CCX service tests — no Docker required.

Uses ``respx`` to intercept HTTP requests made by the OpenAI SDK,
returning fake CCX-like responses so we can verify end-to-end integration
without a real CCX container.

Run as::

    pytest tests/test_mock_ccx.py -v

Or with coverage::

    make cover
"""

from __future__ import annotations

import httpx
import pytest
import respx
from agents import Agent

from ccx_agents import CcxConfig, CcxConversation, CcxRouter, ccx_setup
from ccx_agents.config import CcxConfigModel
from tests.conftest import FakeCcxConfig

# ---------------------------------------------------------------------------
# Fixtures: mock CCX HTTP endpoints
# ---------------------------------------------------------------------------


@pytest.fixture
def mock_ccx(respx_mock: respx.MockRouter) -> respx.MockRouter:
    """Mock CCX endpoints so the OpenAI SDK talks to fake responses.

    This is a session-level fixture that registers httpx interceptors for
    the three major API families the OpenAI SDK may call.
    """
    model_list = {
        "object": "list",
        "data": [
            {"id": "gpt-4o", "object": "model"},
            {"id": "claude-sonnet-4-20250514", "object": "model"},
            {"id": "o3-2025-04-01", "object": "model"},
        ],
    }

    chat_completion = {
        "id": "chatcmpl-mock-001",
        "object": "chat.completion",
        "created": 1700000000,
        "model": "gpt-4o",
        "choices": [
            {
                "index": 0,
                "message": {
                    "role": "assistant",
                    "content": "This is a mock response from CCX.",
                },
                "finish_reason": "stop",
            }
        ],
        "usage": {"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20},
    }

    response_obj = {
        "id": "resp-mock-001",
        "object": "response",
        "created": 1700000000,
        "model": "gpt-4o",
        "status": "completed",
        "output": [
            {
                "type": "message",
                "role": "assistant",
                "content": [
                    {"type": "output_text", "text": "Mock response via Responses API."}
                ],
            }
        ],
        "usage": {"input_tokens": 10, "output_tokens": 10, "total_tokens": 20},
    }

    messages_response = {
        "id": "msg-mock-001",
        "object": "message",
        "role": "assistant",
        "content": [{"type": "text", "text": "Mock response via Messages API."}],
        "model": "claude-sonnet-4",
        "usage": {"input_tokens": 10, "output_tokens": 10},
    }

    # --- Models list ---
    respx_mock.get("http://127.0.0.1:3000/v1/models").respond(
        200, json=model_list
    )

    # --- Chat completions ---
    respx_mock.post("http://127.0.0.1:3000/v1/chat/completions").respond(
        200, json=chat_completion
    )

    # --- Responses API (used by openai-agents) ---
    respx_mock.post("http://127.0.0.1:3000/v1/responses").respond(
        200, json=response_obj
    )

    # --- Messages API (used for Claude models) ---
    respx_mock.post("http://127.0.0.1:3000/v1/messages").respond(
        200, json=messages_response
    )

    return respx_mock


# ---------------------------------------------------------------------------
# Tests: CcxConfig.setup() + real Agent Runner (mocked HTTP)
# ---------------------------------------------------------------------------


class TestMockSetupAndRun:
    """Verify CcxConfig.setup() works through mocked CCX."""

    def test_setup_then_run_sync(self, mock_ccx: respx.MockRouter) -> None:
        """Configure CCX, create an agent, run it synchronously."""
        CcxConfig(base_url="http://127.0.0.1:3000/v1", api_key="test-key").setup()

        agent = Agent(
            name="test_agent",
            instructions="You are helpful.",
        )

        from agents import Runner

        result = Runner.run_sync(agent, "Hello")
        assert result.final_output is not None
        assert len(result.final_output) > 0

    @pytest.mark.asyncio
    async def test_setup_then_run_async(self, mock_ccx: respx.MockRouter) -> None:
        """Configure CCX, create an agent, run it asynchronously."""
        CcxConfig(base_url="http://127.0.0.1:3000/v1", api_key="test-key").setup()

        agent = Agent(name="test_agent_async", instructions="You are helpful.")

        from agents import Runner

        result = await Runner.run(agent, "What is the capital of Japan?")
        assert result.final_output is not None
        assert len(result.final_output) > 0


class TestMockCcxSetup:
    """Verify ccx_setup() one-liner through mocked CCX."""

    def test_ccx_setup_run(self, mock_ccx: respx.MockRouter) -> None:
        """ccx_setup() → Runner.run_sync() through mocked CCX."""
        ccx_setup(base_url="http://127.0.0.1:3000/v1", api_key="test-key")

        agent = Agent(
            name="test_ccx_setup",
            instructions="You are helpful.",
        )

        from agents import Runner

        result = Runner.run_sync(agent, "Say hello")
        assert result.final_output is not None


class TestMockConversation:
    """Verify CcxConversation multi-turn dialog through mocked CCX."""

    def test_conversation_two_turns(self, mock_ccx: respx.MockRouter) -> None:
        """Two-turn conversation using previous_response_id (mocked)."""
        ccx_setup(base_url="http://127.0.0.1:3000/v1", api_key="test-key")

        fcfg = FakeCcxConfig(base_url="http://127.0.0.1:3000/v1", api_key="test-key")
        conv = CcxConversation(fcfg)
        agent = Agent(name="conversation_test", instructions="You are helpful.")

        result1 = conv.run(agent, "My favorite color is blue.")
        assert result1.final_output is not None

        result2 = conv.run(agent, "What is my favorite color?")
        assert result2.final_output is not None

        # Verify previous_response_id was captured from the mock response
        assert conv.previous_response_id is not None

    def test_conversation_reset(self, mock_ccx: respx.MockRouter) -> None:
        """Conversation.reset() clears the response ID."""
        ccx_setup(base_url="http://127.0.0.1:3000/v1", api_key="test-key")

        fcfg = FakeCcxConfig(base_url="http://127.0.0.1:3000/v1", api_key="test-key")
        conv = CcxConversation(fcfg)
        agent = Agent(name="reset_test", instructions="You are helpful.")

        conv.run(agent, "Hello")
        assert conv.previous_response_id is not None

        conv.reset()
        assert conv.previous_response_id is None


class TestMockRouter:
    """Verify CcxRouter agent-name routing through mocked CCX."""

    def test_router_routes_by_agent_name(self, mock_ccx: respx.MockRouter) -> None:
        """Router routes by agent name and runs through mocked CCX."""
        ccx_setup(base_url="http://127.0.0.1:3000/v1", api_key="test-key")

        router = CcxRouter(FakeCcxConfig(base_url="http://127.0.0.1:3000/v1", api_key="test-key"))
        router.route("translator", channel="default")
        router.set_default("default")

        agent = Agent(name="translator", instructions="You are a translator.")
        result = router.run_sync(agent, "Translate hello to Spanish.")
        assert result.final_output is not None

    def test_router_model_map(self, mock_ccx: respx.MockRouter) -> None:
        """Router add_map dispatches by model name."""
        CcxConfig(base_url="http://127.0.0.1:3000/v1", api_key="test-key").setup_with_routing(
            default_url="http://127.0.0.1:3000/v1",
            router={
                "gpt-4o": CcxConfigModel(
                    base_url="http://127.0.0.1:3000/v1",
                    route_prefix="azure",
                ),
            },
        )

        agent = Agent(name="test", instructions="You are helpful.")
        from agents import Runner

        result = Runner.run_sync(agent, "Hello")
        assert result.final_output is not None

    @pytest.mark.asyncio
    async def test_router_run_async(self, mock_ccx: respx.MockRouter) -> None:
        """Router.run_async works through mocked CCX."""
        ccx_setup(base_url="http://127.0.0.1:3000/v1", api_key="test-key")

        router = CcxRouter(FakeCcxConfig(base_url="http://127.0.0.1:3000/v1", api_key="test-key"))
        router.set_default("default")

        agent = Agent(name="async_test", instructions="You are helpful.")
        result = await router.run_async(agent, "Tell me a story")
        assert result.final_output is not None


class TestMockDirectHttp:
    """Verify CCX HTTP endpoints respond correctly (mocked)."""

    def test_models_endpoint(self, mock_ccx: respx.MockRouter) -> None:
        """GET /v1/models returns model list via mocked CCX."""
        resp = httpx.get("http://127.0.0.1:3000/v1/models", timeout=5.0)
        assert resp.status_code == 200
        data = resp.json()
        assert isinstance(data, dict)
        assert "data" in data
        assert len(data["data"]) > 0

    def test_chat_completions_endpoint(self, mock_ccx: respx.MockRouter) -> None:
        """POST /v1/chat/completions returns mocked response."""
        resp = httpx.post(
            "http://127.0.0.1:3000/v1/chat/completions",
            json={"model": "gpt-4o", "messages": [{"role": "user", "content": "Hi"}]},
            headers={"Authorization": "Bearer test-key"},
            timeout=5.0,
        )
        assert resp.status_code == 200
        data = resp.json()
        assert "choices" in data
        assert len(data["choices"]) > 0
        assert "content" in data["choices"][0]["message"]
