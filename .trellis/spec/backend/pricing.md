# Pricing 包契约（PR3 T6 落地）

`backend-go/internal/pricing/` 提供模型级计费：搬自 axonhub `internal/objects/{price,cost,cost_calc}.go`，去 ent 依赖换 `embed.FS + fsnotify` 热重载。

## 核心类型

```go
type ModelPrice struct {
    ModelID string                  `json:"modelId"`
    Mode    PricingMode             `json:"mode"`         // flat_fee / usage_per_unit / usage_tiered
    Items   []ModelPriceItem        `json:"items,omitempty"`
    Tiers   []TieredPricing         `json:"tiers,omitempty"`
}

type ModelPriceItem struct {
    ItemCode  PriceItemCode               `json:"itemCode"`  // prompt_tokens / completion_tokens / prompt_cached_tokens / prompt_write_cached_tokens
    Variant   PromptWriteCacheVariantCode `json:"variant,omitempty"` // five_min / one_hour（仅 write_cached）
    UnitPrice decimal.Decimal             `json:"unitPrice"` // 单价 = 每百万 token 美元数
}

type CostItem struct {
    ItemCode                    PriceItemCode               `json:"itemCode"`
    PromptWriteCacheVariantCode PromptWriteCacheVariantCode `json:"variant,omitempty"`
    Quantity                    int64                       `json:"quantity"`
    TierBreakdown               []TierCost                  `json:"tierBreakdown,omitempty"`
    Subtotal                    decimal.Decimal             `json:"subtotal"` // JSON 序列化为 string
}
```

## 关键函数

```go
func Calculate(usage types.Usage, price *ModelPrice) (totalCost decimal.Decimal, items []CostItem, err error)

type Loader struct { /* 持有 prices map + fsnotify watcher */ }
func NewLoaderFromEnv() (*Loader, error)            // 读 CCX_PRICES_FILE 环境变量；无则 fallback embed
func (l *Loader) GetPrice(modelID string) (*ModelPrice, bool)
func (l *Loader) Version() string                   // sha256 前 12 字符（用作 UsageRecord.PriceVersion）
```

## 计费模式

| 模式 | 用途 | 示例模型 |
|------|------|----------|
| `flat_fee` | 单价 × 总 token，不区分 prompt/completion | 早期 GPT-3 |
| `usage_per_unit` | prompt_tokens × 单价1 + completion_tokens × 单价2，可选 cache 字段 | gpt-4o, claude-3-5-sonnet |
| `usage_tiered` | 按窗口大小阶梯计费（如 gemini-1.5-pro 128K 边界） | gemini-1.5-pro/flash |

## 单价单位

**每百万 token 美元数**。Calculate 内部除以 1_000_000 后再乘 quantity。

## 文件加载与热重载

- 默认：`//go:embed prices.json` 内置 12 模型基线
- override：环境变量 `CCX_PRICES_FILE` 指向外部文件
- 热重载：fsnotify 监听文件变化；解析失败时**保留旧表**并以 `[Pricing-Reload]` 日志告警，不影响在跑请求

## decimal 序列化

所有 decimal 字段通过 `shopspring/decimal` 默认行为序列化为 JSON **string**（非 number），保留任意精度，跨语言反序列化无损。

`*decimal.Decimal + omitempty`：nil 时 JSON 字段省略（用于"无定价数据"语义）。

## 测试覆盖

`go test -count=1 -cover ./internal/pricing/...` 84.2%。覆盖：
- 3 种 mode × 5 主流模型（gpt-4o / claude-3-5-sonnet / gemini-1.5-pro / o1 / gpt-4o-mini）
- cache write 5min / 1hour 双变体
- 热重载触发 + 解析失败回退
- nil price 行为

## prices.json 12 模型基线

gpt-4o / gpt-4o-mini / gpt-4-turbo / claude-3-5-sonnet / claude-3-5-haiku / claude-3-opus / gemini-2.0-flash / gemini-1.5-pro / gemini-1.5-flash / o1 / o1-mini / o3-mini

文件头有 `_lastVerified` / `_source` 注释字段。**新模型上线需手动更新 prices.json**（PRD Out-of-Scope：拒绝接入外部价格源）。

## 与 UsageStore 的契约

T7 `UsageRecord.CostItems []pricing.CostItem` 直接引用本包类型。`UsageRecord.PriceVersion = loader.Version()` 在 attempt 结束时 snapshot 当前价格表 sha256，方便后期复算时对账。

## 相关文件

- `backend-go/internal/pricing/{price,cost,calculator,loader}.go`
- `backend-go/internal/pricing/prices.json`
- `backend-go/internal/pricing/{price,calculator,loader}_test.go`
- `.trellis/tasks/05-07-axonhub-migration-pr3-cutover-and-billing/research/02-pricing-cost-types.md`
