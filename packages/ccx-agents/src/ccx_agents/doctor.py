# src/ccx_agents/doctor.py
"""Environment diagnostics for ccx-agents.

Used by ``ccx-agents doctor`` to verify that the system is ready.
"""

from __future__ import annotations

import importlib.metadata
import os
import platform


def diagnose_environment() -> list[str]:
    """Run all diagnostic checks and return a list of issues (empty = all OK)."""
    issues: list[str] = []

    # -- Dependencies --
    deps = [
        ("openai-agents", "openai-agents"),
        ("openai", "openai"),
    ]
    for display, pkg in deps:
        try:
            importlib.metadata.version(pkg)
        except importlib.metadata.PackageNotFoundError:
            issues.append(f"Missing dependency: {display}")

    # -- Environment variables --
    env_vars = ["CCX_BASE_URL", "CCX_API_KEY", "CCX_CHANNEL"]
    set_vars = [v for v in env_vars if os.environ.get(v)]
    if not set_vars:
        issues.append("No CCX environment variables set — will use defaults")

    # -- Platform --
    system = platform.system()
    if system == "Windows":
        # Windows is supported but may have quirks
        pass

    return issues


def check_ccx_connection(url: str | None = None) -> tuple[bool, str]:
    """Try to reach the CCX health endpoint.

    Returns ``(ok, message)``.
    """
    if url is None:
        url = os.environ.get("CCX_BASE_URL", "http://localhost:3000/v1")

    try:
        import httpx
        health_url = url.rstrip("/").replace("/v1", "").replace("/v1/", "") + "/health"
        resp = httpx.get(health_url, timeout=5.0)
        if resp.is_success:
            return True, f"CCX reachable at {health_url}"
        return False, f"CCX returned {resp.status_code}: {resp.text[:200]}"
    except ImportError:
        return False, "httpx not installed (pip install httpx)"
    except Exception as exc:
        return False, f"Could not reach CCX at {url}: {exc}"
