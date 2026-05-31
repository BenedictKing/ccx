"""Example 02: Low-code — environment variable driven setup."""

import os

# Set environment variables
os.environ["CCX_BASE_URL"] = "http://localhost:3000/v1"
os.environ["CCX_API_KEY"] = "sk-ccx-key"
os.environ["CCX_CHANNEL"] = "default"
os.environ["CCX_API"] = "responses"

# Simply import ccx_setup — all defaults from env
from ccx_agents import ccx_setup

ccx_setup()

# Use openai-agents-python normally
from agents import Agent, Runner

agent = Agent(name="assistant", instructions="You are helpful.")
result = Runner.run_sync(agent, "What is the meaning of life?")
print(result.final_output)
