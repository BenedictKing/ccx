"""Example 03: Multi-agent routing — different agents → different CCX channels."""

from ccx_agents import CcxConfig, CcxRouter

# Step 1: Configure CCX
config = CcxConfig()
config.base_url = "http://localhost:3000/v1"
config.api_key = "sk-ccx-key"
config.setup()

# Step 2: Create the router
router = CcxRouter(config)
router.route("translator", channel="claude-4")   # Translation → Claude 4
router.route("coder", channel="gpt-4o")           # Coding → GPT-4o
router.route("reviewer", channel="gemini")         # Review → Gemini
# router.set_default("gpt-4o")                     # Fallback channel

# Step 3: Create agents
from agents import Agent

translator = Agent(name="translator", instructions="Translate to Chinese.")
coder = Agent(name="coder", instructions="Write Python code.")

# Step 4: Run through router
result1 = router.run_sync(translator, "Hello world, this is a translation test.")
print(f"[translator via claude-4]\n{result1.final_output}\n")

result2 = router.run_sync(coder, "Write a Python function to sort a list.")
print(f"[coder via gpt-4o]\n{result2.final_output}")
