"""Example 05: Agent with tools."""

from ccx_agents import ccx_setup

ccx_setup(base_url="http://localhost:3000/v1", api_key="sk-ccx-key")

from agents import Agent, Runner, function_tool


@function_tool
def get_weather(city: str) -> str:
    """Get the weather for a given city.

    Args:
        city: The city name.
    """
    return f"The weather in {city} is sunny, 22°C."


agent = Agent(
    name="weather_bot",
    instructions="You are a weather assistant. Use the weather tool.",
    tools=[get_weather],
)

result = Runner.run_sync(agent, "What's the weather in Beijing?")
print(result.final_output)
