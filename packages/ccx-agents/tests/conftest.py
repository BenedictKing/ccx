"""pytest conftest — shared fixtures & E2E helpers."""

from __future__ import annotations

import dataclasses
import os

import pytest

# ---------------------------------------------------------------------------
# E2E test marker / helpers
# ---------------------------------------------------------------------------


def pytest_addoption(parser: pytest.Parser) -> None:
    parser.addoption(
        "--run-e2e",
        action="store_true",
        default=False,
        help="Run E2E tests against a real CCX Docker container",
    )


def pytest_configure(config: pytest.Config) -> None:
    config.addinivalue_line("markers", "e2e: tests requiring a running CCX Docker instance")


def pytest_collection_modifyitems(config: pytest.Config, items: list[pytest.Item]) -> None:
    if config.getoption("--run-e2e"):
        return  # run all tests including E2E

    skip_e2e = pytest.mark.skip(reason="use --run-e2e to enable (requires ccx Docker)")
    for item in items:
        if "e2e" in item.keywords:
            item.add_marker(skip_e2e)


# ---------------------------------------------------------------------------
# Shared config types
# ---------------------------------------------------------------------------


@dataclasses.dataclass
class FakeCcxConfig:
    """Stand-in for a CCX config object (CcxConfig / CcxConfigModel).

    Used by CcxConversation, CcxRouter, etc.  Carries the three attributes
    those classes read via ``getattr``:
    """

    base_url: str = "http://127.0.0.1:3000/v1"
    api_key: str | None = "test-key"
    channel: str | None = None


# ---------------------------------------------------------------------------
# fixtures
# ---------------------------------------------------------------------------


@pytest.fixture(scope="session")
def ccx_base_url() -> str:
    return os.environ.get("CCX_BASE_URL", "http://127.0.0.1:3001/v1")


@pytest.fixture(scope="session")
def ccx_api_key() -> str:
    return os.environ.get("CCX_API_KEY", "test-proxy-key-for-e2e")


@pytest.fixture(scope="session")
def ccx_health_url(ccx_base_url: str) -> str:
    base = ccx_base_url.removesuffix("/v1").removesuffix("/")
    return f"{base}/health"


@pytest.fixture
def fake_ccx_config() -> FakeCcxConfig:
    """A fake CCX config with default test values."""
    return FakeCcxConfig()
