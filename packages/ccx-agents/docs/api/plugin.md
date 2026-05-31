# ccx-agents Plugin System

The plugin system allows third-party extensions to hook into ccx-agents lifecycle events.

## Usage

```python
from ccx_agents.plugin import CcxPluginRegistry, CcxPlugin

class MyPlugin(CcxPlugin):
    def on_setup(self, base_url: str, api_key: str | None) -> None:
        print(f"Setup called with {base_url}")

registry = CcxPluginRegistry()
registry.register(MyPlugin())
```

## Built-in plugins

- `CcxLoggingPlugin` — structured logging of all events
- `CcxMetricsPlugin` — metrics collection for monitoring

## Writing a plugin

1. Subclass `CcxPlugin`
2. Override any hooks you need
3. Register with `CcxPluginRegistry`
