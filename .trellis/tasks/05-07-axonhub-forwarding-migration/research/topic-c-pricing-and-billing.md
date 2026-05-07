# Research Topic C: AxonHub 计费数据模型与价格计算

## 文件
- `axonhub/internal/ent/schema/usage_log.go` —— usage 落账 schema
- `axonhub/internal/objects/price.go` —— ModelPrice 数据结构
- `axonhub/internal/objects/cost.go` —— CostItem 计算结果

## 核心数据模型

### 1. ModelPrice 价格表（按 channel × model 配置）

```go
type ModelPrice struct {
    Items []ModelPriceItem
}

type ModelPriceItem struct {
    ItemCode                  PriceItemCode  // prompt_tokens / completion_tokens / prompt_cached_tokens / prompt_write_cached_tokens
    Pricing                   Pricing        // 计价方式
    PromptWriteCacheVariants  []PromptWriteCacheVariant  // 仅 write_cached_tokens 用：5min / 1hour 不同价
}

type Pricing struct {
    Mode         PricingMode  // flat_fee / usage_per_unit / usage_tiered
    FlatFee      *decimal.Decimal
    UsagePerUnit *decimal.Decimal
    UsageTiered  *TieredPricing
}

type TieredPricing struct {
    Tiers []PriceTier  // [{UpTo:1000,Price:0.01},{UpTo:nil,Price:0.02}]
}
```

3 种计费模式：
- **flat_fee**：每次请求固定费用
- **usage_per_unit**：按 token 计费（最常见，例如 GPT-4o input $2.5/1M）
- **usage_tiered**：阶梯计费（前 1000 token $0.01，之后 $0.02）

价格用 `shopspring/decimal` 库——**避免浮点误差**，对计费场景重要。

### 2. UsageLog 持久化字段

token 维度（来自 llm.Usage）：
- `prompt_tokens` / `completion_tokens` / `total_tokens`
- `prompt_audio_tokens` / `prompt_cached_tokens`
- `prompt_write_cached_tokens` / `prompt_write_cached_tokens_5m` / `prompt_write_cached_tokens_1h`
- `completion_audio_tokens` / `completion_reasoning_tokens`
- `completion_accepted_prediction_tokens` / `completion_rejected_prediction_tokens`

成本维度：
- `total_cost` (Float)
- `cost_items` (JSON `[]CostItem`)
- `cost_price_reference_id` (用了哪个版本的价格表，便于回查)

元信息：
- `request_id` / `api_key_id` / `project_id` / `channel_id` / `model_id`
- `source` (api / playground / test)
- `format` (e.g. openai/chat_completions)

### 3. CostItem 计算结果

```go
type CostItem struct {
    ItemCode                    PriceItemCode
    PromptWriteCacheVariantCode PromptWriteCacheVariantCode  // 命中哪个 variant
    Quantity                    int64                          // token 数量
    TierBreakdown               []TierCost                     // 阶梯计费时拆分
    Subtotal                    decimal.Decimal                // 这一项的小计
}
```

汇总计费 = `sum(CostItem.Subtotal)` = `UsageLog.total_cost`。

## ccx 现状对比

ccx 现有：
- `types.Usage`（`backend-go/internal/types/usage.go`）：InputTokens / OutputTokens / CacheReadInputTokens / CacheCreationInputTokens / PromptTokensTotal
- `metrics.AggregatedMetrics`（`channel_metrics.go`）：总请求数、成功率、token 总和
- **没有 cost 计算**
- **没有价格表**
- **没有 cost 持久化**

ccx 已有 cache token 拆分（`AxonHub-half.md` 已落地），usage 数据足够喂 axonhub 计算公式。

## 行为对齐迁移点

### MVP 实现路径

1. **新增 `internal/pricing/`**
   ```
   pricing.go         - ModelPrice / Pricing / Items 结构（直接搬 axonhub objects/price.go）
   cost.go            - CostItem / TierCost
   calculator.go      - Calculate(usage Usage, price ModelPrice) (totalCost, []CostItem, error)
   prices.json        - 内置价格表（gpt-4o / claude-3.5-sonnet / gemini-2.0 等主流模型）
   loader.go          - 加载 prices.json + 热重载
   ```

2. **决定价格的索引键**
   - axonhub 是 `channel_id + model_id` 二维（每个 channel 自己定价）
   - ccx MVP 推荐：`model_id` 一维（同一模型不同渠道用同一价格表）
   - 理由：用户选 Q3=A（内置 JSON 表），不维护 channel × model 二维就好

3. **价格表来源**
   - 参考 LiteLLM `model_prices_and_context_window.json` 是社区维护的标准
   - 内置一份初始版本，后续可手动更新

4. **依赖 `shopspring/decimal`**
   - axonhub 已用，ccx 需要 `go get github.com/shopspring/decimal`
   - 比 float64 慢 5-10 倍但准确，单次请求计费量级可忽略

5. **持久化（Q4=B 选 NDJSON）**
   ```
   internal/usage/store.go
       UsageStore interface {
           Append(ctx, UsageRecord) error
           Query(ctx, QueryParams) ([]UsageRecord, error)
           Close() error
       }
   internal/usage/ndjson_store.go      - NDJSON impl
       - 按日切分文件（usage-2025-05-07.ndjson）
       - sync.Mutex + bufio.Writer
       - 启动时清理超过保留期（默认 30 天）的文件
   ```
   未来可加 SQLite impl，上层不改。

### UsageRecord schema（参考 axonhub 但简化）

```go
type UsageRecord struct {
    RequestID     string         // 用 ccx 现有 logRequestID
    Timestamp     time.Time
    ChannelID     int            // ccx ChannelIndex
    ChannelName   string
    APIKeyMasked  string
    ModelID       string
    Format        string         // openai_chat / claude_messages / openai_responses / gemini_contents

    // Usage 维度（沿用 ccx types.Usage 字段）
    InputTokens              int64
    OutputTokens             int64
    CacheReadInputTokens     int64
    CacheCreationInputTokens int64
    TotalTokens              int64

    // Cost 维度
    TotalCost      *decimal.Decimal
    CostItems      []CostItem
    PriceVersion   string  // 用了哪一版价格表
}
```

## 计算公式（伪代码）

```go
func Calculate(usage Usage, price ModelPrice) (decimal.Decimal, []CostItem) {
    var total decimal.Decimal
    var items []CostItem

    for _, item := range price.Items {
        var qty int64
        switch item.ItemCode {
        case "prompt_tokens":
            // 注意：要扣掉 cached_tokens，避免重复计费（与 axonhub 一致）
            qty = usage.InputTokens  // ccx 已经在 metrics 层做过 PromptTokensTotal - CacheReadInputTokens
        case "completion_tokens":
            qty = usage.OutputTokens
        case "prompt_cached_tokens":
            qty = usage.CacheReadInputTokens
        case "prompt_write_cached_tokens":
            qty = usage.CacheCreationInputTokens
        }

        subtotal := computePricing(item.Pricing, qty)  // flat_fee / per_unit / tiered
        total = total.Add(subtotal)
        items = append(items, CostItem{ItemCode: item.ItemCode, Quantity: qty, Subtotal: subtotal})
    }

    return total, items
}
```

## 关键决策

- ✅ 直接搬 `objects/price.go` + `objects/cost.go`（结构稳定，无 ent 耦合）
- ✅ 引入 `shopspring/decimal` 依赖
- ✅ `UsageStore` interface + NDJSON impl（用户选 Q4=B）
- ✅ MVP 用 `model_id` 一维索引（不做 channel × model 二维）
- ❌ 不做 channel × model 二维定价（Q5=A 渠道维度展示，价格也按模型）
- ❌ 不做 ent / GraphQL / Project / API key profile 配额预扣（Q6=A 全部不要）

## 风险

- **价格数据维护**：内置 JSON 静态表，需要用户手动更新（或后续接 LiteLLM）
- **decimal 性能**：每次请求多 50-200ns，计费场景可接受
- **价格缺失**：模型不在 prices.json 时返回 `cost = nil`，UI 显示"—"，**绝不报错阻断请求**
- **NDJSON 并发写**：用 `sync.Mutex + bufio.Writer`，每秒 flush；崩溃时丢失最近 1 秒（可接受）
- **磁盘膨胀**：单条记录 ~500B，1000 req/min × 30 天 ≈ 21GB → 必须有保留期清理
