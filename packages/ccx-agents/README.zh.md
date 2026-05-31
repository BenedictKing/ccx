# ccx-agents

> **让 openai-agents-python 通过 CCX 多模型网关运行。**
> 一行代码切换，零代码可选，多路路由，全功能可流式。

**[English](README.md)** · **中文**

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

然后照常使用 openai-agents-python：

```python
from agents import Agent, Runner

agent = Agent(name="assistant", instructions="用中文回答")
result = Runner.run_sync(agent, "你好")
print(result.final_output)
```

### 方式二：低代码（一行初始化）

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

### 方式三：按模型名路由

不同模型自动匹配不同的 CCX 上游通道：

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

### 方式四：按 Agent 名路由

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
| 一行初始化 | `ccx_setup()` | 注入 CCX 作为默认 LLM 提供者 |
| 单端点配置 | `CcxConfig().setup()` | 所有请求走同一 CCX 端点 |
| 模型路由 | `CcxConfig.setup_with_routing()` | 按模型名 → 渠道路由 |
| Agent 路由 | `CcxRouter` | 按 Agent 名路由，支持同步/异步/流式 |
| 多轮对话 | `CcxConversation` | 利用 `previous_response_id` 延续上下文 |
| 模型配置 | `CcxConfigModel` | 单模型配置（api_type, route_prefix, 自定义请求头） |
| 跟踪集成 | `CcxTracing` | openai-agents 跟踪支持 |

---

## Streaming 支持

完整支持 openai-agents 的流式输出：

```python
from ccx_agents import CcxConversation, CcxConfig
from agents import Agent

conv = CcxConversation(CcxConfig())
agent = Agent(name="storyteller", instructions="讲短故事")

# 同步流式
result = conv.run(agent, "讲个故事")
for event in result.stream_events():
    print(event)

# 异步流式
result = await conv.run_async(agent, "继续")
async for event in result.stream_events():
    print(event)
```

Router 同样支持流式：

```python
router = CcxRouter(config)
result = router.run_streamed(agent, "分析这段代码")
```

---

## 多 Agent 路由详解

### 场景：不同 Agent 使用不同模型

```
Agent "翻译助手" ──► channel "claude-4"  ──► Claude 4
Agent "代码审查" ──► channel "gpt-4o"    ──► GPT-4o
Agent "文生图"   ──► channel "gemini"    ──► Gemini 2.5
```

### 路由优先级

1. `CcxRouter.route(agent_name, channel=...)` — 精确匹配最高
2. `CcxRouter.set_default(channel=...)` — 默认渠道
3. `CcxConfig.channel` — 配置级默认
4. `CCX_CHANNEL` — 环境变量

---

## 多轮对话

利用 CCX 的 Responses API 原生 `previous_response_id` 机制，无需客户端维护消息历史：

```python
from ccx_agents import CcxConversation, CcxConfig
from agents import Agent

conv = CcxConversation(CcxConfig())
agent = Agent(name="assistant", instructions="You are helpful.")

result1 = conv.run(agent, "今天天气如何？")
result2 = conv.run(agent, "那明天呢？")  # 自动延续上下文
```

---

## 环境变量

| 变量 | 默认值 | 说明 |
|:-----|:-------|:-----|
| `CCX_BASE_URL` | `http://localhost:3000/v1` | CCX 网关地址 |
| `CCX_API_KEY` | — | CCX API Key |
| `CCX_API` | `responses` | 使用的 API |
| `CCX_CHANNEL` | — | 默认渠道名称 |
| `CCX_MODEL` | — | 默认模型名 |
| `PROXY_ACCESS_KEY` | — | CCX 代理密钥（回退） |

---

## 端到端集成测试

确保已启动 CCX Docker 容器：

```bash
# 启动 CCX
docker compose -f /path/to/ccx/docker-compose.yml up -d

# 运行 E2E 测试
cd packages/ccx-agents
python -m pytest tests/test_e2e.py --run-e2e -v
```

测试内容包括：

- **健康检查** — 验证 CCX `/health` 端点
- **CcxConfig.setup() + real Agent** — 真实调用 CCX
- **ccx_setup() one-liner** — 一行初始化验证
- **CcxConversation** — 多轮对话验证
- **CcxRouter** — Agent 路由验证
- **Direct HTTP** — 端点可达性验证

无 `--run-e2e` 时所有 E2E 测试自动跳过。

---

## 开发

```bash
# 克隆仓库
git clone https://github.com/BenedictKing/ccx.git
cd ccx/packages/ccx-agents

# 安装开发依赖
python -m venv .venv
source .venv/bin/activate
pip install -e ".[dev]"

# 运行测试（52 个单元测试）
make test

# 运行全部测试（含 E2E，需 Docker）
python -m pytest tests/ --run-e2e -v

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
├── Makefile                # 常用命令
├── CHANGELOG.md            # 变更日志
├── .gitignore
├── examples/               # 8 个示例文件
│   ├── basic.py            # 单端点 setup
│   ├── router.py           # 多 channel 路由
│   ├── 01_basic_setup.py   # 基础初始化
│   ├── 02_low_code.py      # 低代码模式
│   ├── 03_multi_agent_routing.py  # 多 Agent 路由
│   ├── 04_conversation.py  # 多轮对话
│   ├── 05_tools.py         # 工具调用
│   └── 06_streaming.py     # 流式输出
├── src/ccx_agents/
│   ├── __init__.py         # 公开 API 入口
│   ├── _client.py          # 客户端工厂 + CcxConfig
│   ├── _setup.py           # ccx_setup() 一行初始化
│   ├── config.py           # CcxConfigModel
│   ├── conv.py             # 多轮对话
│   ├── models.py           # 模型枚举
│   ├── router.py           # 双模式路由
│   ├── tracing.py          # 跟踪集成
│   └── utils.py            # 工具函数
└── tests/
    ├── conftest.py         # 共享 fixture 和 E2E 配置
    ├── test_ccx_agents.py  # 42 个综合测试
    ├── test_client.py      # 10 个客户端测试
    └── test_e2e.py         # 10 个 E2E 集成测试（需 Docker）
```

---

## License

MIT
