"""Example 06: Streaming output with CcxRouter and CcxConversation."""

from ccx_agents import CcxConfig, CcxRouter, CcxConversation

# ── Streaming via Router ────────────────────────────────────────────
config = CcxConfig()
config.base_url = "http://localhost:3000/v1"
config.api_key = "sk-ccx-key"
config.setup()

router = CcxRouter(config)
router.route("storyteller", channel="claude-4")

from agents import Agent

agent = Agent(
    name="storyteller",
    instructions="You are a storyteller. Tell short engaging stories.",
)

print("=== Router streaming ===")
streamed_result = router.run_streamed(agent, "Tell me a short story about a robot.")
async for event in streamed_result.stream_events():
    print(event)

# ── Streaming via Conversation ──────────────────────────────────────
print("\n=== Conversation streaming ===")
conv = CcxConversation(config)

result = conv.run(agent, "Continue the robot story.")
for event in result.stream_events():
    print(event)
