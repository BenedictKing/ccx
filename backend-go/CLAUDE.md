# backend-go 模块文档

[← 根目录](../CLAUDE.md)

## 模块职责

Go 后端核心服务：HTTP API、多上游适配、协议转换、智能调度、会话管理、配置热重载。

## 启动命令

```bash
make dev          # 热重载开发
make test         # 运行测试
make test-cover   # 测试 + 覆盖率
make build        # 构建二进制
```

## API 端点

| 端点 | 方法 | 功能 |
|------|------|------|
| `/health` | GET | 健康检查（无需认证） |
| `/v1/messages` | POST | Claude Messages API |
| `/v1/messages/count_tokens` | POST | Token 计数 |
| `/v1/responses` | POST | Codex Responses API |
| `/v1/responses/compact` | POST | 精简版 Responses API |
| `/v1/chat/completions` | POST | OpenAI Chat Completions API |
| `/v1/models` | GET | 模型列表查询 |
| `/v1/models/:model` | GET | 模型详情 |
| `/v1beta/models/{model}:generateContent` | POST | Gemini 原生协议 |
| `/api/messages/channels` | CRUD | Messages 渠道管理 |
| `/api/responses/channels` | CRUD | Responses 渠道管理 |
| `/api/chat/channels` | CRUD | Chat 渠道管理 |
| `/api/gemini/channels` | CRUD | Gemini 渠道管理 |
| `/api/{type}/channels/:id/models` | POST | 单渠道模型列表查询 |
| `/api/{type}/channels/:id/capability-test` | POST | 渠道能力测试 |
| `/api/{type}/channels/:id/promotion` | POST | 渠道促销期管理 |
| `/api/messages/channels/metrics` | GET | 渠道指标 |
| `/api/messages/channels/scheduler/stats` | GET | 调度器统计 |

## 指标历史数据聚合粒度

`/api/messages/channels/:id/keys/metrics/history` 端点根据查询时间范围自动选择聚合间隔：

| 时间范围 | 聚合间隔 | 数据点数 |
|----------|----------|----------|
| 1h       | 1 分钟   | ~60 点   |
| 6h       | 5 分钟   | ~72 点   |
| 24h      | 15 分钟  | ~96 点   |
| 7d       | 2 小时   | ~84 点   |
| 30d      | 8 小时   | ~90 点   |

可通过 `interval` 参数手动指定（最小 1 分钟）。最大查询范围为 30 天。

## Provider 接口

所有上游服务实现 `internal/providers/Provider` 接口：

```go
type Provider interface {
    ConvertToProviderRequest(c *gin.Context, upstream *config.UpstreamConfig, apiKey string) (*http.Request, []byte, error)
    ConvertToClaudeResponse(providerResp *types.ProviderResponse) (*types.ClaudeResponse, error)
    HandleStreamResponse(body io.ReadCloser) (<-chan string, <-chan error, error)
}
```

**实现**: `ClaudeProvider`, `OpenAIProvider`, `GeminiProvider`, `ResponsesProvider`

## 核心模块

| 模块 | 职责 |
|------|------|
| `handlers/` | HTTP 处理器（proxy.go, responses.go） |
| `handlers/{messages,chat,responses,gemini}/` | 4 个入站 handler，全部走新 pipeline.Process（PR3） |
| `handlers/adapters/` | 8 个 inbound/outbound adapter 公共契约（4 共享 metadata 键） |
| `handlers/wire/` | LBOutboundAdapter（pipeline.Outbound + Retryable + ChannelRetryable）+ Finalize（cost + UsageStore.Append + per-key metrics） |
| `providers/` | 上游适配器 |
| `converters/` | 协议转换器（工厂模式） |
| `scheduler/` | 多渠道调度。`SelectChannel` 已切到 `LoadBalancer.Sort` + 6 策略（PR3 T4） |
| `loadbalance/` | LB 容器 + 6 strategy（Promotion/TraceAware/WeightRR/ErrorAware/LatencyAware/RateLimitAware）+ 13 方法 ChannelMetricsProvider 接口 |
| `pipeline/` | axonhub 风格 9-hook pipeline + Process 主循环 + 空流检测 + cancel-on-retry cleanup |
| `pipeline/middleware/` | ccx 自家 middleware：CCXKeyFailure（5xx/429/failoverRule）/ CCXPauseRule / SSEErrorEventDetector |
| `metrics/` | KeyMetrics + ChannelMetrics + lbMetricsEntry（FTTL/TPS/ActiveConn/cost/cache/tokens） |
| `pricing/` | embed.FS + fsnotify 热重载，12 模型基线，3 计费模式（flat_fee/usage_per_unit/usage_tiered），decimal-as-string |
| `usage/` | NDJSON UsageStore，按日切分 + 30 天保留期，sync.Mutex + bufio.Writer，snake_case 对齐 axonhub |
| `session/` | 会话管理（Trace 亲和性） |
| `config/` | 配置管理（热重载） |

## 日志规范

所有日志输出使用 `[Component-Action]` 标签格式，禁止使用 emoji 符号（确保跨平台兼容性）。

**格式规范**:
```go
// 标准格式
log.Printf("[Component-Action] 消息内容: %v", value)

// 警告信息
log.Printf("[Component] 警告: 消息内容")
```

**标签命名示例**:

| 组件 | 标签 | 用途 |
|------|------|------|
| 调度器 | `[Scheduler-Channel]` | 渠道选择 |
| 调度器 | `[Scheduler-Promotion]` | 促销渠道 |
| 调度器 | `[Scheduler-Affinity]` | Trace 亲和性 |
| 调度器 | `[Scheduler-Fallback]` | 降级选择 |
| 认证 | `[Auth-Failed]` | 认证失败 |
| 认证 | `[Auth-Success]` | 认证成功 |
| 指标 | `[Metrics-Store]` | 指标存储 |
| 会话 | `[Session-Manager]` | 会话管理 |
| 配置 | `[Config-Watcher]` | 配置热重载 |
| 压缩 | `[Gzip]` | Gzip 解压缩 |
| Messages | `[Messages-Stream]` | Messages 流式处理 |
| Messages | `[Messages-Stream-Token]` | Messages Token 统计 |
| Messages | `[Messages-Models]` | Messages Models API 操作 |
| Responses | `[Responses-Stream]` | Responses 流式处理 |
| Responses | `[Responses-Stream-Token]` | Responses Token 统计 |
| Responses | `[Responses-Models]` | Responses Models API 操作 |
| Models | `[Models]` | 跨接口的模型列表合并操作 |
| Pipeline | `[Pipeline-Cleanup]` | retry/cancel 前 attempt 资源清理 |
| Pipeline | `[Pipeline-SSEError]` | SSE event:error 帧检测触发 retry |
| Scheduler-LB | `[Scheduler-LB]` | LoadBalancer.Sort 选择渠道 |
| Wire | `[Wire-NextChannel]` | LB 切下一 channel |
| Wire | `[Wire-NextKey]` | 同 channel 内换 key（ChannelRetryable） |
| Wire | `[Wire-Finalize]` | 请求结束记 cost / metrics / NDJSON |
| Pricing | `[Pricing-Reload]` | 价格表 fsnotify 热重载 |
| Usage | `[Usage-NDJSON]` | NDJSON UsageStore 切日 / 保留期清理 |
| Messages | `[Messages-Pipeline]` | Messages 走新 pipeline.Process 主路径 |
| Messages | `[Messages-Outbound]` | Messages outbound 跨格式 dispatch / 流 usage 解析 |
| Chat | `[Chat-Pipeline]` / `[Chat-Outbound]` | 同上，OpenAI Chat 入口 |
| Responses | `[Responses-Pipeline]` / `[Responses-Outbound]` | 同上，Codex Responses 入口 |
| Gemini | `[Gemini-Pipeline]` / `[Gemini-Outbound]` | 同上，Gemini 入口 |

## 扩展指南

**添加新上游服务**:
1. 在 `internal/providers/` 创建新文件
2. 实现 `Provider` 接口
3. 在 `GetProvider()` 注册

**调度优先级规则**（PR3 切到 LoadBalancer 加权打分）:

候选硬过滤（`scheduler.filterCandidates`）：model 兼容 + route prefix + circuit Open + 非 active 状态 + 空 APIKeys。

候选打分（6 strategy 总分降序，OrderingWeight 次级，ID 末级 stable 排序）：
1. PromotionStrategy（0/800）—— 促销期渠道
2. TraceAwareStrategy（0/1000）—— Trace 亲和性
3. WeightRoundRobinStrategy（10-150）—— OrderingWeight + 请求计数衰减
4. ErrorAwareStrategy（0-200）—— ConsecutiveFailures + 5min 衰减
5. LatencyAwareStrategy（0-80）—— FTTL + TPS（stream 模式）/ E2E（非流式）
6. RateLimitAwareStrategy（-10000~100）—— 限流冷却硬负分 + ActiveConnections 衰减

retry：失败后 `ChannelRetryable.NextKey` 在同 channel 内换 key，全部 key 失败再 `Retryable.NextChannel`。stream attempt retry 前 pipeline 自动 cancel + close body + drain fan-out。

## 工具使用注意事项

**Edit 工具与 Tab 缩进**:
- Go 文件使用 tab 缩进，`Edit` 工具匹配时可能因空白字符差异失败
- 失败时可用 `sed -i '' 's/old/new/g' file.go` 替代
