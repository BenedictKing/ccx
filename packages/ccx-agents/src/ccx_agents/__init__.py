# src/ccx_agents/__init__.py
"""ccx-agents — route openai-agents-python through the CCX proxy gateway.

One-line setup::

    from ccx_agents import ccx_setup

    ccx_setup(base_url="http://localhost:3000/v1")

    from agents import Agent, Runner
    result = Runner.run_sync(Agent(name="Hi", instructions="..."), "Hello")


Multi-agent routing::

    from ccx_agents import CcxConfig, CcxRouter

    config = CcxConfig(base_url="http://localhost:3000/v1")
    config.setup()

    router = CcxRouter(config)
    router.route("translator", channel="claude-4")
    result = router.run_sync(translator_agent, "Hello")
"""

from __future__ import annotations

__version__ = "0.1.0"

from ._client import CcxConfig, ccx_client, get_ccx_client
from ._setup import ccx_setup
from .config import CcxConfigModel, CcxConfigProtocol
from .conv import CcxConversation
from .doctor import check_ccx_connection, diagnose_environment
from .plugin import CcxPlugin, CcxPluginRegistry, plugin_registry
from .router import CcxRouter, RoutingRule
from .tracing import CcxTracing

__all__ = [
    "CcxConfig",
    "CcxConfigModel",
    "CcxConfigProtocol",
    "CcxConversation",
    "CcxPlugin",
    "CcxPluginRegistry",
    "CcxRouter",
    "CcxTracing",
    "RoutingRule",
    "ccx_client",
    "ccx_setup",
    "check_ccx_connection",
    "diagnose_environment",
    "get_ccx_client",
    "plugin_registry",
]
