# src/ccx_agents/plugin.py
"""Plugin system for ccx-agents — hook into lifecycle events.

Usage::

    from ccx_agents import plugin_registry
    from ccx_agents.plugin import CcxPlugin

    class MyPlugin(CcxPlugin):
        def on_setup(self, base_url, api_key):
            print(f"Setup: {base_url}")

    plugin_registry.register(MyPlugin())
"""

from __future__ import annotations

import logging
from typing import Any

_log = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# Hook interface
# ---------------------------------------------------------------------------


class CcxPlugin:
    """Base class for ccx-agents plugins.

    Override any combination of hook methods.  All hooks are no-ops by
    default so you only implement what you need.
    """

    # -- Lifecycle hooks ------------------------------------------------

    def on_setup(self, base_url: str, api_key: str | None) -> None:
        """Called when ``ccx_setup()`` or ``CcxConfig.setup()`` completes."""

    def on_routing_created(self, router: Any) -> None:
        """Called when a :class:`CcxRouter` is created and configured."""

    def on_run_start(self, agent_name: str, input_data: str | list[Any]) -> None:
        """Called before an agent run starts."""

    def on_run_end(self, agent_name: str, result: Any) -> None:
        """Called after an agent run completes successfully."""

    def on_run_error(self, agent_name: str, error: Exception) -> None:
        """Called when an agent run raises an unhandled exception."""

    def on_conversation_reset(self) -> None:
        """Called when ``CcxConversation.reset()`` is invoked."""


# ---------------------------------------------------------------------------
# Registry
# ---------------------------------------------------------------------------


class CcxPluginRegistry:
    """Thread-safe (naive) plugin registry.

    Usage::

        registry = CcxPluginRegistry.get_instance()
        registry.register(MyPlugin())
    """

    _instance: CcxPluginRegistry | None = None

    def __init__(self) -> None:
        self._plugins: list[CcxPlugin] = []

    # -- singleton ------------------------------------------------------

    @classmethod
    def get_instance(cls) -> CcxPluginRegistry:
        if cls._instance is None:
            cls._instance = cls()
        return cls._instance

    # -- registration ---------------------------------------------------

    def register(self, plugin: CcxPlugin) -> None:
        """Register a plugin instance."""
        self._plugins.append(plugin)
        _log.info("Plugin registered: %s", type(plugin).__name__)

    def unregister(self, plugin: CcxPlugin) -> None:
        """Unregister a plugin instance."""
        self._plugins.remove(plugin)
        _log.info("Plugin unregistered: %s", type(plugin).__name__)

    def clear(self) -> None:
        """Remove all registered plugins."""
        self._plugins.clear()

    # -- dispatch -------------------------------------------------------

    def dispatch(self, hook: str, *args: Any, **kwargs: Any) -> None:
        """Call *hook* on every registered plugin that implements it."""
        for plugin in self._plugins:
            try:
                getattr(plugin, hook)(*args, **kwargs)
            except Exception:
                _log.exception(
                    "Plugin %s failed on hook %s", type(plugin).__name__, hook
                )

    @property
    def plugins(self) -> list[CcxPlugin]:
        """Read-only list of registered plugins."""
        return list(self._plugins)


# Module-level singleton — import this directly.
plugin_registry = CcxPluginRegistry.get_instance()


# ---------------------------------------------------------------------------
# Built-in plugins
# ---------------------------------------------------------------------------


class CcxLoggingPlugin(CcxPlugin):
    """Log all lifecycle events at INFO level."""

    def on_setup(self, base_url: str, api_key: str | None) -> None:
        _log.info("[Plugin Logging] Setup: base_url=%s", base_url)

    def on_run_start(self, agent_name: str, input_data: str | list[Any]) -> None:
        _log.info("[Plugin Logging] Run start: agent=%s", agent_name)

    def on_run_end(self, agent_name: str, result: Any) -> None:
        _log.info("[Plugin Logging] Run end: agent=%s", agent_name)

    def on_run_error(self, agent_name: str, error: Exception) -> None:
        _log.info("[Plugin Logging] Run error: agent=%s error=%s", agent_name, error)


class CcxMetricsPlugin(CcxPlugin):
    """Collect simple run-count and error-count metrics."""

    def __init__(self) -> None:
        self.run_count = 0
        self.error_count = 0

    def on_run_start(self, agent_name: str, input_data: str | list[Any]) -> None:
        self.run_count += 1

    def on_run_error(self, agent_name: str, error: Exception) -> None:
        self.error_count += 1
