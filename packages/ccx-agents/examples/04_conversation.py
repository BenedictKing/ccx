"""Example 04: Multi-turn conversation with CcxConversation."""

from ccx_agents import CcxConversation, CcxConfig

# Step 1: Configure
config = CcxConfig()
config.base_url = "http://localhost:3000/v1"
config.api_key = "sk-ccx-key"

# Step 2: Create conversation
conv = CcxConversation(config)

from agents import Agent

agent = Agent(name="assistant", instructions="You are a helpful assistant.")

# Step 3: Multi-turn — CCX maintains context via previous_response_id
result1 = conv.run(agent, "My name is Alice.")
print(f"Turn 1: {result1.final_output}")

# Automatically continues the conversation
result2 = conv.run(agent, "What's my name?")
print(f"Turn 2: {result2.final_output}")  # Should know it's Alice

# Start fresh
conv.reset()
result3 = conv.run(agent, "What's my name?")
print(f"Turn 3 (after reset): {result3.final_output}")  # Won't know
