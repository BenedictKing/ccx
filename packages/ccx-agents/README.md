# ccx-agents

> **让 openai-agents-python 通过 CCX 多模型网关运行。**
> 一行代码切换，零代码可选，多路路由，全功能可流式。

**English** · [中文](README.zh.md)

---

## 为什么需要 ccx-agents？

**[openai-agents-python](https://github.com/openai/openai-agents-python)** 是 OpenAI 官方发布的 Agent 框架，支持多 Agent 协作、工具调用、Handoff、Streaming。但它默认只使用 OpenAI 的 API。

**[CCX](https://github.com/BenedictKing/ccx)** 是一个多模型 AI 网关，支持 Claude、GPT、Gemini 等 100+ 模型，提供统一 API、多渠道 failover、驾驶舱监控。

**ccx-agents** 连接两者——让你享受 Agent 框架全部能力，同时使用 CCX 的多模型通道。

```
        你的 Agent 代码
              │
         openai-agents-python
              │  ┌──────────────────┐
              └──┤   ccx-agents     │ ← 注入 ccx 的 AsyncOpenAI client
                 └──────┬───────────┘
                        │ HTTP
                   ┌────▼────┐
                   │   CCX   │ ← 多模型网关
                   └──┬──┬───┘
                ┌─────┘  └─────┐
                ▼               ▼
             Claude 4        GPT-4o
```

---

## 安装

```bash
pip install ccx-agents
```

需要 Python 3.10+。

---

## 快速开始

### 方式一：零代码（环境变量）

```bash
export CCX_BASE_URL="http://localhost:3000/v1"
export CCX_API_KEY="sk-ccx-xxx"
export CCX_API="responses"
```

```python
from agents import Agent, Runner

agent = Agent(name="assistant", instructions="用中文回答")
result = Runner.run_sync(agent, "你好")
print(result.final_output)
```

### 方式二：低代码（一行初始化 `ccx_setup` 或 `CcxConfig.setup`）

```python
from ccx_agents import ccx_setup

ccx_setup(base_url="http://localhost:3000/v1")

# 后续所有 agent 请求走 ccx
from agents import Agent, Runner
result = Runner.run_sync(Agent(name="Hi", instructions="..."), "Hello")
```

或使用配置类：

```python
from ccx_agents import CcxConfig

CcxConfig(base_url="http://localhost:3000/v1", api_key="sk-ccx-key").setup()
```

### 方式三：多 Model 多 Channel 路由（模型名称自动匹配）

```python
from ccx_agents import CcxConfig, CcxConfigModel

CcxConfig(api_key="sk-ccx-access-key").setup_with_routing(
    default_url="http://localhost:3000/v1",
    router={
        "gpt-4o":          CcxConfigModel(route_prefix="azure"),
        "claude-sonnet-4": CcxConfigModel(route_prefix="sf", api_type="messages"),
    },
)
```

### 方式四：多 Agent 多 Channel 路由（Agent 名称显式路由）

```python
from ccx_agents import CcxConfig, CcxRouter
from agents import Agent

CcxConfig(base_url="http://localhost:3000/v1", api_key="sk-ccx-key").setup()

router = CcxRouter(config)
router.route("translator", channel="claude-4")   # 翻译走 Claude 4
router.route("coder", channel="gpt-4o")           # 编程走 GPT-4o

result = router.run_sync(Agent(name="translator", ...), "Hello")
```

---

## 核心功能

| 功能 | 类/函数 | 说明 |
|:-----|:---------|:------|
| 初始化 | `ccx_setup()` | 一行注入，走 ccx |
| 快速启动 | `CcxConfig.setup()` | 单端点配置 |
| 路由 | `CcxConfig.setup_with_routing()` | 按模型名 → 渠道路由 |
| 显式路由 | `CcxRouter` | 按 Agent 名 → Channel 路由，支持同步/异步/流式 |
| 对话 | `CcxConversation` | 多轮对话，利用 `previous_response_id` 延续上下文 |
| 配置 | `CcxConfigModel` | 单模型配置（api_type, route_prefix, 自定义请求头） |
| 跟踪 | `CcxTracing` | 跟踪集成 |

---

## Streaming 支持

ccx-agents 完整支持 openai-agents 的流式输出：

```python
from ccx_agents import CcxConversation, CcxConfig
from agents import Agent

conv = CcxConversation(CcxConfig())
agent = Agent(name="storyteller", instructions="Tell short stories.")

# 同步流式
result = conv.run(agent, "Tell me a story")
for event in result.stream_events():
    print(event)

# 异步流式
result = await conv.run_async(agent, "Continue the story")
async for event in result.stream_events():
    print(event)
```

Router 同样支持流式：

```python
router = CcxRouter(config)
result = router.run_streamed(agent, "Analyze this code")
```

---

## 环境变量

| 环境变量 | 默认值 | 说明 |
|:---------|:-------|:------|
| `CCX_BASE_URL` | `http://localhost:3000/v1` | CCX 网关地址 |
| `CCX_API_KEY` | — | CCX API Key |
| `CCX_API` | `responses` | 使用的 API 类型 |
| `CCX_CHANNEL` | — | 默认渠道名称 |
| `CCX_MODEL` | — | 默认模型名 |
| `PROXY_ACCESS_KEY` | — | CCX 代理密钥（回退） |

---

## 多 Agent 路由详解

### 场景：你需要不同的 Agent 使用不同的模型

```
Agent "翻译助手" ──► channel "claude-4"  ──► Claude 4
Agent "代码审查" ──► channel "gpt-4o"    ──► GPT-4o
Agent "文生图"   ──► channel "gemini"    ──► Gemini 2.5
```

### 路由优先级

1. `CcxRouter.route(agent_name, channel=...)` — 精确匹配
2. `CcxRouter.set_default(channel=...)` — 默认渠道
3. `CcxConfig.channel` — 配置级默认
4. `CCX_CHANNEL` — 环境变量

---

## 多轮对话

利用 ccx 的 Responses API 原生 `previous_response_id` 机制，无需客户端维护消息历史：

```python
from ccx_agents import CcxConversation, CcxConfig
from agents import Agent

conv = CcxConversation(CcxConfig())
agent = Agent(name="assistant", instructions="You are helpful.")

result1 = conv.run(agent, "今天天气如何？")
result2 = conv.run(agent, "那明天呢？")  # 自动延续上文
```

---

## 开发

```bash
# 克隆仓库
git clone https://github.com/BenedictKing/ccx.git
cd ccx/packages/ccx-agents

# 安装开发依赖
pip install -e ".[dev]"   # 或: make install

# 运行测试
make test                 # 52 个测试

# 构建
make build

# 查看示例
python examples/01_basic_setup.py
```

### 项目结构

```
packages/ccx-agents/
├── pyproject.toml          # Hatchling 构建
├── LICENSE                 # MIT
├── CONTRIBUTING.md         # 贡献指南
├── Makefile                # 常用命令
├── CHANGELOG.md            # 变更日志
├── .gitignore
├── examples/
│   ├── 01_basic_setup.py   # 基础初始化
│   ├── 02_low_code.py      # 低代码模式
│   ├── 03_multi_agent_routing.py  # 多 Agent 路由
│   ├── 04_conversation.py  # 多轮对话
│   ├── 05_tools.py         # 工具调用
│   └── 06_streaming.py     # 流式输出
├── src/ccx_agents/
│   ├── __init__.py         # 公开 API
│   ├── _client.py          # 客户端工厂 + CcxConfig
│   ├── _setup.py           # ccx_setup() 一行初始化
│   ├── config.py           # CcxConfigModel + CcxConfigProtocol
│   ├── router.py           # 路由（model 和 agent 两种模式）
│   ├── conv.py             # 多轮对话
│   ├── tracing.py          # 跟踪集成
│   ├── models.py           # 模型映射
│   └── utils.py            # 工具函数
└── tests/
    ├── conftest.py         # 共享 fixture
    ├── test_ccx_agents.py  # 单元测试（含 Property-based 路由测试）
    ├── test_client.py      # 客户端测试
    ├── test_e2e.py         # E2E 集成测试（需 --run-e2e）
    └── test_mock_ccx.py    # Mock CCX 服务测试
```

---

## License

MIT
