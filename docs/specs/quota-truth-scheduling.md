# 配额真相分级与按余量调度

> 模块：配额余量感知调度（Quota Truth & Headroom-Aware Scheduling）
> 状态：**已实现**（2026-09-01 落地，v3.x 起）
> 来源：[omniroute-benchmark-upgrades.md §2](./omniroute-benchmark-upgrades.md#2-方向-a配额真相分级与按余量调度)
> 蓝本：OmniRoute `src/lib/quota/`（providerQuotaTelemetry.ts / accountBuckets.ts / quotaShareStrategy.ts）

## 1. 背景与目标

CCX 的调度系统已有质量、稳定性、速度、成本、场景域等多维度评分，但缺少「这个账号本窗口还剩多少配额」这一维度。结果是：

- 临近耗尽的账号仍被优先选择，连续 429 后才由熔断/限速兜底，体验差。
- SmartRouter 不知道账号余量，可能把关键请求路由到只剩 5% 额度的账号。
- 前端只能展示原始数字，用户无法判断数据可信度（是官方 API 查的还是配置里填的）。

**目标**：引入「配额真相分级」体系，让配额余量参与选路，但不破坏 fail-open 原则——无数据时绝不阻断调度。

**非目标**：
- 不做精确计费（已由 Subscription 模块 + CostReport 覆盖）
- 不实现 DRR（Deficit Round Robin）公平排队（二期）
- 不实现 TTFB 拥挤度（与配额共用采集管道，但各自独立）
- 不动 `handlers/common/` 转发链

## 2. 核心概念

### 2.1 真相五级（TruthLevel）

| 等级 | 含义 | 行为 |
|------|------|------|
| `healthy` | 配额充足（> 20% 余量） | 正常参与选路 |
| `approaching_limit` | 接近上限（≤ 20% 余量） | SmartRouter 降分 + scheduler 沉底 |
| `exhausted` | 已耗尽（remaining ≤ 0） | SmartRouter 大幅降分 + scheduler 沉底 |
| `unavailable` | provider 支持查询但本次失败 | 中性分，不惩罚（等同于 unknown） |
| `unknown` | 无任何配额数据 | 中性分 0.5，不惩罚（fail-open） |

**硬原则：unknown ≠ exhausted。** 没有配额数据的渠道永远不会因为配额原因被排除。

### 2.2 来源优先级（SourcePriority）

按可信度从高到低排列，每维度独立取最优来源：

| 来源 | 说明 | 示例 |
|------|------|------|
| `provider_api` | provider 官方账单/用量 API（最高可信度） | Kimi Console API、火山 Plan API、MiniMax Token Plan |
| `response_headers` | 响应头中解析到的速率/配额信息 | Anthropic `anthropic-ratelimit-input-tokens-*`、OpenAI `x-ratelimit-*-tokens` |
| `configured` | 配置中静态声明的配额 | new-api multiplier、手动设置的额度 |
| `estimated` | 基于历史用量的估算 | 本地消耗推算 |
| `unknown` | 无数据 | 冷启动 / 不支持的 provider |

**设计原则**：按维度逐项取最优来源。provider_api 的 token 配额可以和 response_headers 的 request 配额共存，各维度独立。

### 2.3 懒重置饱和桶

每个 `(accountUID, dimension)` 一个饱和桶，**读取时判断 `now >= resetsAtMs` 即清零**，无后台 cron。

- 零调度成本：不依赖 `scheduler/recovery.go` 的定时恢复循环
- 内存态为主：单机内存 + 可选 SQLite 持久化（二期）
- Fail-open：缺失条目 → 未饱和
- 时钟注入：所有时间输入通过 `nowMs` 参数传递，测试时驱动确定性时钟

## 3. 实现架构

```
                        ┌─────────────────────┐
                        │  Subscription /      │  provider_api 级
                        │  Console Fetcher    │─────────────┐
                        └─────────────────────┘             │
                                                              ▼
┌──────────────┐  response_headers  ┌──────────────────────────────────┐
│  Upstream    │──────────────────▶│  quota.Manager                   │
│  Response    │                    │  ┌─────────────────────────────┐ │
└──────────────┘                    │  │ ChannelState map            │ │
                                    │  │ (truth / values / headroom) │ │
┌──────────────┐  configured 级     │  └─────────────────────────────┘ │
│  Config /    │──────────────────▶│  ┌─────────────────────────────┐ │
│  new-api     │                    │  │ BucketManager               │ │
└──────────────┘                    │  │ (lazy-reset saturate buckets)│ │
                                    │  └─────────────────────────────┘ │
                                    └──────┬───────────────┬───────────┘
                                           │ headroom      │ saturated?
                                           ▼               ▼
                                ┌─────────────────┐  ┌────────────────┐
                                │  SmartRouter    │  │  scheduler     │
                                │  ScoreCandidate │  │  select.go     │
                                │  +quotaHeadroom │  │  quota sinking │
                                └─────────────────┘  └────────────────┘
```

**两层接线**：
1. **SmartRouter 层（评分）**：`ScoreCandidate` 增加 `quotaHeadroom` 因子，归一化 0.0-1.0，unknown 给 0.5 中性分。
2. **底层选择层（沉底）**：`scheduler/select.go` 饱和账号沉底排序而非剔除；全员饱和时 fail-open 全体回候选。

## 4. 配额管理器（quota 包）

**代码位置**：`backend-go/internal/quota/`

### 4.1 包结构

| 文件 | 职责 |
|------|------|
| `doc.go` | 包文档 |
| `truth.go` | TruthLevel 五级枚举、Source 优先级、Value/ChannelState 数据结构、Headroom 计算 |
| `buckets.go` | BucketManager 懒重置饱和桶、TTFB 拥挤度采集预留接口 |
| `headers.go` | per-provider 响应头映射表、解析函数、reset/retry-after 时间解析 |
| `manager.go` | Manager 对外入口（获取状态/headroom、各级数据更新、饱和判断） |

### 4.2 核心 API

```go
// 获取渠道 headroom（0.0-1.0，无数据时 0.5 中性分）
func (m *Manager) GetChannelHeadroom(channelUID string) float64

// 获取渠道真相等级
func (m *Manager) GetChannelTruth(channelUID string) TruthLevel

// provider_api 级数据更新（订阅/console fetcher 调用）
func (m *Manager) UpdateChannelProviderAPI(channelUID, accountUID string, values []Value, err error)

// response_headers 级更新（响应头回调中调用）
func (m *Manager) UpdateChannelResponseHeaders(channelUID, accountUID, provider string, headers http.Header)

// configured 级更新（配置同步、new-api multiplier 调用）
func (m *Manager) UpdateChannelConfigured(channelUID, accountUID string, values []Value)

// 饱和判断（scheduler 沉底用）
func (m *Manager) IsChannelSaturated(channelUID string, nowMs int64) bool
```

### 4.3 响应头映射（per-provider）

只映射**显式确认**的头名，绝不猜测通用头名：

| Provider | 维度 | 头名 |
|----------|------|------|
| Anthropic | input_tokens | `anthropic-ratelimit-input-tokens-{limit,remaining,reset}` |
| Anthropic | output_tokens | `anthropic-ratelimit-output-tokens-{limit,remaining,reset}` |
| Anthropic | requests | `anthropic-ratelimit-requests-{limit,remaining,reset}` |
| OpenAI | tokens | `x-ratelimit-{limit,remaining,reset}-tokens` |
| OpenAI | requests | `x-ratelimit-{limit,remaining,reset}-requests` |

新 provider 通过 `RegisterHeaderMapping()` 注册。

### 4.4 共享采集管道预留

`ObservationCollector` 接口预留 TTFB 拥挤度观测入口，与配额共用同一采集管道，避免双份观测开销。当前仅定义接口，TTFB 拥挤度实现时填充。

## 5. SmartRouter 评分接线

### 5.1 评分因子扩展

`ScoringCandidate` 增加 `QuotaHeadroomScore` 字段（0.0-1.0），`ScoreCandidate` 增加第 10 个加权项 `WQuotaHeadroom`。

**中性分保证**：`QuotaHeadroomScore <= 0` 时修正为 0.5，确保未知/无数据的渠道不被惩罚（对齐冷候选中性分纪律）。

### 5.2 权重分配

默认权重从既有因子匀出，保持总权重基本不变：

| TaskClass | WQuotaHeadroom | 来源 |
|-----------|---------------|------|
| Supervisor | 0.3 | 从 WDomain (0.5→0.2) 匀出 |
| Worker | 0.5 | 从 WSavings (3→2.5) 匀出 |
| Lightweight | 0.5 | 从 WSavings (3→2.5) 匀出 |
| Vision | 0.3 | 从 WDomain (0.5→0.2) 匀出 |
| ImageGen | 0.5 | 从 WSavings (2→1.5) 匀出 |
| Embedding | 0.5 | 从 WSavings (3→2.5) 匀出 |
| LongContext | 0.3 | 从 WDomain (0.5→0.2) 匀出 |

**设计考量**：
- 质量/稳定性/速度等核心因子不动，保证调度基本盘稳定
- 成本敏感场景（worker/lightweight/embedding）从 savings 匀出——配额余量本质上也是"成本可用性"
- 质量敏感场景（supervisor/vision/longcontext）从 domain 匀出——域优势是较"软"的信号

### 5.3 数据填充

`buildChannelEntry` 在三个画像分支（物理画像 / 兄弟画像 / 无画像）末尾统一调用 `applyQuotaHeadroom()`，从 `quotaManager.GetChannelHeadroom(channelUID)` 获取分数。

`quotaManager` 为 nil 时不填充，ScoreCandidate 中的 0 值会被修正为 0.5（fail-open）。

## 6. 底层调度沉底排序

### 6.1 设计原则

沿用速率卸载的水位线模式，但**饱和账号沉底排序而非过滤剔除**：

- 非饱和渠道按优先级正常遍历
- 饱和渠道进入 `quotaSunk` 列表，排在非饱和渠道之后
- **全员饱和时全体回候选（fail-open）**：配额数据不准确时绝不断调度

### 6.2 实现位置

`scheduler/select.go` 优先级遍历循环中，速率限制软跳过之后、视觉保留之前，增加配额饱和判断：

```go
// 配额饱和沉底：配额接近耗尽或已耗尽的渠道沉到非饱和渠道之后
if s.quotaManager != nil && upstream.ChannelUID != "" {
    if s.quotaManager.IsChannelSaturated(upstream.ChannelUID, time.Now().UnixMilli()) {
        quotaSunk = append(quotaSunk, ...)
        continue
    }
}
```

主循环结束后、降级选择之前，尝试配额沉底渠道作为回退。

### 6.3 饱和判断逻辑

- `exhausted`：通过 `BucketManager` 懒重置检查（窗口翻转后自动恢复）
- `approaching_limit`：直接判为饱和（用于沉底排序，桶阈值是 100% 不含此档）
- `healthy / unknown / unavailable`：不参与沉底

`ChannelSaturationRank()` 返回排序权重（2=exhausted, 1=approaching, 0=healthy, -1=unknown），可用于更细粒度的排序调整。

## 7. 数据源接入

### 7.1 provider_api 级

`SubscriptionBalanceFetcher` 接口及各厂商 fetcher（Kimi Console、火山 Plan、MiniMax Token Plan 等）的结果归入 `provider_api` 级，调用 `UpdateChannelProviderAPI()`。

这是最高可信度来源，每个维度有 provider_api 数据时就不会被更低优先级来源覆盖。

### 7.2 response_headers 级

响应头解析挂在 `ratelimit.SetUpstreamSignalCallback` 同一挂点（与速率发现器共享回调），调用 `UpdateChannelResponseHeaders()`。

与限速发现的区别：
- 限速发现学 RPM/并发，用于动态调整令牌桶
- 配额响应头学 token 余量，用于评分和沉底排序
- 两者共享同一观测管道，不重复解析头

### 7.3 configured 级

new-api 同步的 multiplier、手动配置的额度等归入 `configured` 级，调用 `UpdateChannelConfigured()`。

**边界**：不推翻 `MultiplierSource` 状态机（`config.go`），配额分级只消费其产出。状态机的状态转换、置信度计算保留原逻辑。

### 7.4 estimated 级（预留）

基于本地消耗记录推算配额余量，用于没有官方 API 也没有响应头的 provider。当前未实现，接口已预留。

## 8. 前端展示

### 8.1 UsageQuotaRows 真相等级列

`UsageQuotaRows.vue` 新增第 5 列「真相等级徽章」，展示每行配额数据的可信等级：

| 等级 | 颜色 | 标签 |
|------|------|------|
| healthy | 绿色 (#10b981) | 充足 |
| approaching_limit | 琥珀色 (#f59e0b) | 趋紧 |
| exhausted | 红色 (#dc2626) | 耗尽 |
| unavailable | 灰色 (#6b7280) | 获取失败 |
| unknown | 浅灰 (#9ca3af) | 未知 |

徽章格式：圆点 + 文字标签，圆角胶囊形，悬浮 tooltip 显示数据来源（官方 API / 响应头 / 配置声明 / 估算）。

### 8.2 数据结构扩展

`UsageQuotaItem` 接口新增两个可选字段：
- `truthLevel?: 'healthy' | 'approaching_limit' | 'exhausted' | 'unavailable' | 'unknown'`
- `truthSource?: 'provider_api' | 'response_headers' | 'configured' | 'estimated' | 'unknown'`

各 provider 适配器（MiniMax / 火山 / Kimi / Compshare / MiMo）在构建 `UsageQuotaItem` 时填入对应等级。

## 9. 边界与 fail-open 策略

| 场景 | 行为 | 理由 |
|------|------|------|
| 无配额数据（unknown） | headroom = 0.5 中性分，不参与沉底 | 冷启动/不支持的 provider 不影响调度 |
| 查询失败（unavailable） | 等同 unknown，不惩罚 | 临时故障不应导致渠道被跳过 |
| 全部渠道饱和 | 全体回候选，不阻断请求 | 配额数据可能不准确，fail-open 是红线 |
| quotaManager 为 nil | 全量 0.5 中性分，scheduler 不沉底 | 功能开关，关闭时零影响 |
| 响应头解析失败 | 静默跳过，不更新状态 | 解析错误不应污染已有数据 |
| 桶重置时间未知 | 不触发懒重置，但也不会永久卡住 | 未知重置时间 → 等下一次观测更新 |
| accountUID 为空 | ChannelState 正常工作，桶功能降级 | channelUID 是主键，accountUID 用于桶聚合 |

**核心红线**：配额系统的任何故障都不能导致请求失败或渠道消失。最坏情况是配额不参与决策，调度退化为原有行为。

## 10. 测试与验证

### 10.1 单元测试

`internal/quota/` 包内测试覆盖：

| 测试文件 | 覆盖点 |
|----------|--------|
| `truth_test.go` | Source 优先级、TruthLevel 解析、Value.Headroom/IsExhausted/IsApproaching、ChannelState 合并与综合状态 |
| `buckets_test.go` | 饱和/恢复、懒重置、用量下降恢复、过期信号、fail-open、批量更新 |
| `headers_test.go` | Anthropic/OpenAI 头解析、未知 provider、空响应头、自定义映射注册、时间格式解析 |
| `manager_test.go` | Get/Update 全路径、来源优先级竞争、饱和判断、nil safety、headroom 消费接口 |

### 10.2 验证要求

```bash
cd backend-go && make test && go build ./...
```

前端涉及改动时：`cd frontend && bun run type-check`

## 11. 与其他模块的关系

| 模块 | 关系 | 交互边界 |
|------|------|----------|
| `autopilot/scoring.go` | 评分扩展因子 | `QuotaHeadroomScore` 字段 + `WQuotaHeadroom` 权重 |
| `autopilot/smart_router.go` | 数据填充 | `applyQuotaHeadroom()` → `quotaManager.GetChannelHeadroom()` |
| `scheduler/select.go` | 沉底排序 | `quotaManager.IsChannelSaturated()` → quotaSunk 列表 |
| `ratelimit/hints.go` | 共享观测管道 | `SetUpstreamSignalCallback` 同一挂点 |
| `autopilot/subscription_balance_fetcher.go` | provider_api 数据源 | FetchBalance 结果 → `UpdateChannelProviderAPI()` |
| `config.MultiplierSource` | configured 数据源 | newapi multiplier 同步 → `UpdateChannelConfigured()` |
| TTFB 拥挤度（未来） | 共享采集管道 | `ObservationCollector` 接口预留 |

**与 TTFB 拥挤度的关系**：配额余量管"还能用多久"，TTFB 拥挤度管"有多快"。两者共用采集管道、各管一个维度，在 SmartRouter 评分处合流为"时效 × 余量"双维度。

## 12. 二期预留

| 项 | 说明 |
|----|------|
| DRR 公平排队 | Deficit Round Robin：每轮加 weight/totalWeight 量子，选 deficit 最大者。实现长期按 key Weight 比例精确收敛。与现有 Weight 排序兼容。 |
| SQLite 持久化 | 桶状态和 ChannelState 落盘 metrics.db，重启后恢复。当前仅内存态。 |
| TTFB 拥挤度 | 共用 `ObservationCollector` 接口，实现四层拥挤度学习。与 quotaHeadroom 合成"时效 × 余量"双维评分。 |
| estimated 级 | 基于本地 request_records 消耗推算配额余量，用于没有官方 API 的 provider。 |
| 多维度细粒度调度 | 按 token/request/credits 等维度分别判断饱和，当前取所有维度的最差情况。 |
