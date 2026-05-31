"""Example 01: Basic setup — CcxConfig().setup() one-liner."""

from ccx_agents import CcxConfig

# One-line setup — everything goes through CCX
CcxConfig(
    base_url="http://localhost:3000/v1",
    api_key="sk-ccx-key",
).setup()

# Now use openai-agents-python normally
from agents import Agent, Runner

agent = Agent(name="assistant", instructions="You are a helpful assistant.")
result = Runner.run_sync(agent, "Hello! Tell me about yourself.")
print(result.final_output)
