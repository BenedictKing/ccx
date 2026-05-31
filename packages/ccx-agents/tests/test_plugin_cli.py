"""Tests for plugin system, CLI, scaffolding, and diagnostics."""

from __future__ import annotations

import os
import tempfile
from pathlib import Path

import pytest

from ccx_agents import CcxPlugin, CcxPluginRegistry
from ccx_agents.cli import main
from ccx_agents.doctor import check_ccx_connection, diagnose_environment
from ccx_agents.scaffold import scaffold_project

# ======================================================================
# Plugin system
# ======================================================================


class TestPluginSystem:
    def test_register_and_dispatch(self) -> None:
        events: list[str] = []

        class TestPlugin(CcxPlugin):
            def on_setup(self, base_url, api_key):
                events.append(f"setup:{base_url}")

            def on_run_start(self, agent_name, input_data):
                events.append(f"start:{agent_name}")

        registry = CcxPluginRegistry()
        registry.register(TestPlugin())
        registry.dispatch("on_setup", "http://example.com", "key")
        registry.dispatch("on_run_start", "my_agent", "hello")

        assert "setup:http://example.com" in events
        assert "start:my_agent" in events

    def test_plugin_error_does_not_crash_registry(self) -> None:
        class BrokenPlugin(CcxPlugin):
            def on_setup(self, base_url, api_key):
                raise RuntimeError("boom")

        registry = CcxPluginRegistry()
        registry.register(BrokenPlugin())
        # Should not raise
        registry.dispatch("on_setup", "http://example.com", "key")

    def test_module_singleton(self) -> None:
        registry2 = CcxPluginRegistry.get_instance()
        assert CcxPluginRegistry.get_instance() is registry2

    def test_logging_plugin(self) -> None:
        from ccx_agents.plugin import CcxLoggingPlugin
        p = CcxLoggingPlugin()
        p.on_setup("url", "key")  # just shouldn't crash
        p.on_run_start("agent", "input")
        p.on_run_end("agent", "result")
        p.on_run_error("agent", RuntimeError("test"))

    def test_metrics_plugin(self) -> None:
        from ccx_agents.plugin import CcxMetricsPlugin
        p = CcxMetricsPlugin()
        assert p.run_count == 0
        assert p.error_count == 0
        p.on_run_start("agent", "input")
        p.on_run_error("agent", RuntimeError("test"))
        assert p.run_count == 1
        assert p.error_count == 1

    def test_clear_unregister(self) -> None:
        registry = CcxPluginRegistry()
        p1 = CcxPlugin()
        p2 = CcxPlugin()
        registry.register(p1)
        registry.register(p2)
        assert len(registry.plugins) == 2
        registry.unregister(p1)
        assert len(registry.plugins) == 1
        registry.clear()
        assert len(registry.plugins) == 0


# ======================================================================
# Scaffolding
# ======================================================================


class TestScaffold:
    def test_scaffold_creates_files(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            scaffold_project(tmp, "my-project")
            files = {f.name for f in Path(tmp).iterdir()}
            assert files >= {"README.md", "main.py", "pyproject.toml", ".gitignore"}

    def test_scaffold_skip_existing(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            Path(tmp, "main.py").write_text("original")
            scaffold_project(tmp, "my-project")
            assert Path(tmp, "main.py").read_text() == "original"


# ======================================================================
# CLI
# ======================================================================


class TestCli:
    def test_version(self) -> None:
        with pytest.raises(SystemExit) as exc:
            main(["--version"])
        assert exc.value.code == 0

    def test_help(self) -> None:
        with pytest.raises(SystemExit) as exc:
            main(["--help"])
        assert exc.value.code == 0

    def test_init(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            dest = os.path.join(tmp, "my_proj")
            rc = main(["init", dest, "--name", "test-proj"])
            assert rc == 0
            assert Path(dest, "main.py").exists()
            assert Path(dest, "README.md").exists()

    def test_completion(self) -> None:
        assert main(["completion", "bash"]) == 0
        assert main(["completion", "zsh"]) == 0
        assert main(["completion", "fish"]) == 0

    def test_doctor_ok(self) -> None:
        rc = main(["doctor"])
        # may return 1 if no env vars but shouldn't crash
        assert rc in (0, 1)


# ======================================================================
# Doctor
# ======================================================================


class TestDoctor:
    def test_diagnose_environment_no_crash(self) -> None:
        issues = diagnose_environment()
        assert isinstance(issues, list)

    def test_check_ccx_connection_not_found(self) -> None:
        ok, msg = check_ccx_connection("http://127.0.0.1:1/v1")
        assert ok is False
        assert "Could not reach" in msg or "httpx" in msg
