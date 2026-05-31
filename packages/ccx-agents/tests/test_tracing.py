"""Tests for ccx_agents.tracing — run_with_ccx_tracing / run_sync_with_ccx_tracing."""

from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from ccx_agents.tracing import (
    _make_trace_headers,
    run_sync_with_ccx_tracing,
    run_with_ccx_tracing,
)


def test_make_trace_headers_with_workflow() -> None:
    """_make_trace_headers includes X-Ccx-Trace-Workflow when workflow_name is set."""
    run_config = MagicMock()
    run_config.workflow_name = "test-workflow"
    headers = _make_trace_headers(run_config)
    assert headers == {"X-Ccx-Trace-Workflow": "test-workflow"}


def test_make_trace_headers_no_run_config() -> None:
    """_make_trace_headers returns empty dict when run_config is None."""
    assert _make_trace_headers(None) == {}


def test_make_trace_headers_no_workflow() -> None:
    """_make_trace_headers returns empty dict when workflow_name is not set."""
    run_config = MagicMock()
    run_config.workflow_name = None
    headers = _make_trace_headers(run_config)
    assert headers == {}


class TestRunWithCcxTracing:
    """Verify run_with_ccx_tracing injects headers and delegates to Runner.run."""

    @pytest.mark.asyncio
    async def test_injects_trace_headers(self) -> None:
        """Trace headers are injected into the client when workflow_name is set."""
        mock_client = MagicMock()
        mock_client.default_headers = {}

        mock_run_config = MagicMock()
        mock_run_config.workflow_name = "my-workflow"

        mock_result = MagicMock()
        mock_result.raw_responses = [MagicMock()]
        mock_result.last_agent = None

        with (
            patch("ccx_agents._client.get_ccx_client", return_value=mock_client),
            patch("ccx_agents.tracing.Runner.run", new=AsyncMock(return_value=mock_result)),
        ):
            agent = MagicMock()
            agent.name = "test-agent"

            result = await run_with_ccx_tracing(
                agent, "Hello", run_config=mock_run_config
            )

        assert result is mock_result
        assert mock_client.default_headers == {"X-Ccx-Trace-Workflow": "my-workflow"}

    @pytest.mark.asyncio
    async def test_no_trace_headers_when_no_run_config(self) -> None:
        """No headers injected when run_config is None."""
        mock_client = MagicMock()
        mock_client.default_headers = {}

        mock_result = MagicMock()
        mock_result.raw_responses = []

        with (
            patch("ccx_agents._client.get_ccx_client", return_value=mock_client),
            patch("ccx_agents.tracing.Runner.run", new=AsyncMock(return_value=mock_result)),
        ):
            agent = MagicMock()
            agent.name = "test-agent"

            result = await run_with_ccx_tracing(agent, "Hello")

        assert result is mock_result
        # Headers should be unchanged (empty dict, no trace headers added)
        assert mock_client.default_headers == {}

    @pytest.mark.asyncio
    async def test_no_client_does_not_crash(self) -> None:
        """Gracefully handles get_ccx_client() returning None."""
        mock_result = MagicMock()
        mock_result.raw_responses = []

        with (
            patch("ccx_agents._client.get_ccx_client", return_value=None),
            patch("ccx_agents.tracing.Runner.run", new=AsyncMock(return_value=mock_result)),
        ):
            agent = MagicMock()
            agent.name = "test-agent"

            # Should not raise even with no client
            result = await run_with_ccx_tracing(
                agent,
                "Hello",
                run_config=MagicMock(workflow_name="test"),
            )

        assert result is mock_result

    @pytest.mark.asyncio
    async def test_passes_kwargs(self) -> None:
        """Extra kwargs are forwarded to Runner.run."""
        mock_client = MagicMock()
        mock_client.default_headers = {}
        mock_result = MagicMock()
        mock_result.raw_responses = []

        mock_run = AsyncMock(return_value=mock_result)

        with (
            patch("ccx_agents._client.get_ccx_client", return_value=mock_client),
            patch("ccx_agents.tracing.Runner.run", new=mock_run),
        ):
            agent = MagicMock()
            agent.name = "test-agent"

            await run_with_ccx_tracing(
                agent,
                "Hello",
                max_turns=5,
                context={"user": "test"},
            )

        # Ensure Runner.run was called with the extra kwargs
        _call_kwargs = mock_run.call_args.kwargs
        assert _call_kwargs.get("max_turns") == 5
        assert _call_kwargs.get("context") == {"user": "test"}


class TestRunSyncWithCcxTracing:
    """Verify run_sync_with_ccx_tracing wraps the async version."""

    @patch("ccx_agents.tracing.run_with_ccx_tracing", new_callable=AsyncMock)
    def test_sync_wrapper(self, mock_async_run: AsyncMock) -> None:
        """Synchronous wrapper calls async version via asyncio.run."""
        mock_async_run.return_value = "sync-result"

        agent = MagicMock()
        agent.name = "test"

        result = run_sync_with_ccx_tracing(agent, "Hello")

        assert result == "sync-result"
        mock_async_run.assert_called_once()

    @patch("ccx_agents.tracing.run_with_ccx_tracing", new_callable=AsyncMock)
    def test_sync_passes_kwargs(self, mock_async_run: AsyncMock) -> None:
        """Kwargs are forwarded to the async version."""
        mock_async_run.return_value = "result"

        agent = MagicMock()
        agent.name = "test"

        run_sync_with_ccx_tracing(
            agent,
            "Hello",
            max_turns=3,
            context={"key": "val"},
        )

        _call_kwargs = mock_async_run.call_args.kwargs
        assert _call_kwargs.get("max_turns") == 3
        assert _call_kwargs.get("context") == {"key": "val"}
