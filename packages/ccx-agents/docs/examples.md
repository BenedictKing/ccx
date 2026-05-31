# Examples

## Basic setup

```python
from ccx_agents import ccx_setup

ccx_setup(base_url="http://localhost:3000/v1")
```

## Multi-model routing

```python
from ccx_agents import CcxConfig, CcxConfigModel

CcxConfig(api_key="sk-ccx-key").setup_with_routing(
    default_url="http://localhost:3000/v1",
    router={
        "gpt-4o":          CcxConfigModel(route_prefix="azure"),
        "claude-sonnet-4": CcxConfigModel(route_prefix="sf", api_type="messages"),
    },
)
```

## Multi-agent routing

```python
from ccx_agents import CcxConfig, CcxRouter

config = CcxConfig(base_url="http://localhost:3000/v1")
config.setup()

router = CcxRouter(config)
router.route("translator", channel="claude-4")
router.route("coder", channel="gpt-4o")

result = router.run_sync(agent, "Hello")
```

## Multi-turn conversation

```python
from ccx_agents import CcxConversation, CcxConfig

conv = CcxConversation(CcxConfig())
result1 = conv.run(agent, "What's the weather?")
result2 = conv.run(agent, "And tomorrow?")  # auto-continues
```

See the `examples/` directory in the repository for complete runnable scripts.
