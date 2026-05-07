# Research: pricing / cost 类型与 Calculate 算法（axonhub → ccx）

- **Query**: ccx PR3 T6 工作单元——把 axonhub `internal/objects/price.go` + `cost.go` + `biz/cost_calc.go` 迁到 `ccx/backend-go/internal/pricing/`
- **Scope**: internal（axonhub 源文件 + ccx PR3 PRD 第 50-73 行）
- **Date**: 2026-05-07

---

## 1. 文件来源

| axonhub 源 | 行数 | ccx 目标位置 |
|---|---|---|
| `axonhub/internal/objects/price.go` | 337 | `backend-go/internal/pricing/price.go` |
| `axonhub/internal/objects/cost.go` | 18 | `backend-go/internal/pricing/cost.go` |
| `axonhub/internal/server/biz/cost_calc.go` | 180 | `backend-go/internal/pricing/calculator.go` |

**包名变更**：`package objects` → `package pricing`；`package biz` → `package pricing`。

**依赖**：

```go
import "github.com/shopspring/decimal"
```

PR3 PRD 已确认引入 `github.com/shopspring/decimal`。需要 `cd backend-go && go get github.com/shopspring/decimal@v1.4.0`（或最新稳定版）。

**llm 包路径变更**：axonhub 用 `github.com/looplj/axonhub/llm`（独立 module），ccx 用 `github.com/BenedictKing/ccx/internal/llm`。`Usage / PromptTokensDetails / CompletionTokensDetails` 在 ccx 自己的 llm 包里需要确认字段名（PR1/PR2 应该已经迁过来）。

---

## 2. 核心 Struct（带 json tag、保持不变）

### 2.1 Pricing 与模式（来自 `price.go:9-36`）

```go
package pricing

import "github.com/shopspring/decimal"

// PricingMode —— 3 种计费模式
type PricingMode string

const (
    // 固定费用：每次请求收固定 FlatFee，不看 token 数
    PricingModeFlatFee PricingMode = "flat_fee"

    // 按用量线性计费：单价 UsagePerUnit × token 数（轴单位为 1M tokens，见 unitsInMillionTokens）
    // 注意：单价单位是"每百万 token"，不是"每 token"
    PricingModeUsagePerUnit PricingMode = "usage_per_unit"

    // 阶梯计费：按 PriceTier 分段累加
    PricingModeTiered PricingMode = "usage_tiered"
)

// Pricing —— 单个 ItemCode 的计费规则
type Pricing struct {
    Mode         PricingMode      `json:"mode"`
    FlatFee      *decimal.Decimal `json:"flatFee,omitempty"`
    UsagePerUnit *decimal.Decimal `json:"usagePerUnit,omitempty"`
    UsageTiered  *TieredPricing   `json:"usageTiered,omitempty"`
}
```

**判别 3 种模式的逻辑**（ccx 计算时复用）：见 `cost_calc.go:23` switch on `pricing.Mode`，**已经写在 `computeItemSubtotal` 里**，不需要重写。

### 2.2 Tier（来自 `price.go:172-218`）

```go
type TieredPricing struct {
    Tiers []PriceTier `json:"tiers"`
}

// PriceTier —— 单个阶梯
type PriceTier struct {
    // 上界（含），nil 表示无上界（必须是最后一个 tier）
    UpTo *int64 `json:"upTo,omitempty"`

    // 该阶梯的"每百万 token"单价
    PricePerUnit decimal.Decimal `json:"pricePerUnit"`
}
```

`Validate()` 要求最后一个 tier 的 `UpTo == nil`，其他 tier `UpTo != nil`。直接搬不改。

### 2.3 PriceItem 与 Cache 变体（来自 `price.go:220-290`）

```go
type PriceItemCode string

const (
    PriceItemCodeUsage             PriceItemCode = "prompt_tokens"        // 输入（不含 cache）
    PriceItemCodeCompletion        PriceItemCode = "completion_tokens"    // 输出
    PriceItemCodePromptCachedToken PriceItemCode = "prompt_cached_tokens" // cache 读
    PriceItemCodeWriteCachedTokens PriceItemCode = "prompt_write_cached_tokens" // cache 写
)

type PromptWriteCacheVariantCode string

const (
    PromptWriteCacheVariantCode5Min  PromptWriteCacheVariantCode = "five_min"
    PromptWriteCacheVariantCode1Hour PromptWriteCacheVariantCode = "one_hour"
)

// 区分 5min / 1h cache 写的不同价格（Anthropic 特有）
type PromptWriteCacheVariant struct {
    VariantCode PromptWriteCacheVariantCode `json:"variantCode"`
    Pricing     Pricing                     `json:"pricing"`
}

type ModelPriceItem struct {
    ItemCode                 PriceItemCode             `json:"itemCode"`
    Pricing                  Pricing                   `json:"pricing"`
    PromptWriteCacheVariants []PromptWriteCacheVariant `json:"promptWriteCacheVariants,omitempty"`
}

// FindPromptWriteCacheVariantPricing 找不到变体时回退到 item.Pricing
func (i *ModelPriceItem) FindPromptWriteCacheVariantPricing(code PromptWriteCacheVariantCode) Pricing
```

### 2.4 ModelPrice（顶层）

```go
// ModelPrice —— 单个模型的完整价格表（多个 ItemCode）
type ModelPrice struct {
    Items []ModelPriceItem `json:"items"`
}
```

### 2.5 CostItem（来自 `cost.go`，全文 18 行）

```go
type TierCost struct {
    UpTo     *int64          `json:"upTo,omitempty"`
    Units    int64           `json:"units"`
    Subtotal decimal.Decimal `json:"subtotal"`
}

type CostItem struct {
    ItemCode                    PriceItemCode               `json:"itemCode"`
    PromptWriteCacheVariantCode PromptWriteCacheVariantCode `json:"promptWriteCacheVariantCode,omitempty"`
    Quantity                    int64                       `json:"quantity"`
    TierBreakdown               []TierCost                  `json:"tierBreakdown,omitempty"`
    Subtotal                    decimal.Decimal             `json:"subtotal"`
}
```

---

## 3. Calculate 函数签名 + 算法

源文件 `axonhub/internal/server/biz/cost_calc.go`，180 行，**整体可以原文 copy**（仅改 import 路径）。

### 3.1 入口

```go
// ComputeUsageCost 输入 usage + 价格表，输出 cost items 列表 + total
func ComputeUsageCost(usage *llm.Usage, price ModelPrice) ([]CostItem, decimal.Decimal)
```

输入：
- `*llm.Usage`（包含 `PromptTokens`、`CompletionTokens`、`PromptTokensDetails.{CachedTokens, WriteCachedTokens, WriteCached5MinTokens, WriteCached1HourTokens}`）
- `ModelPrice`（含 N 个 ModelPriceItem）

输出：
- `[]CostItem`（每个 PriceItemCode 一项；cache 写带变体时是 2 项）
- `decimal.Decimal`（合计）

### 3.2 关键逻辑要点（迁移时一字不动）

```go
func unitsInMillionTokens(units int64) decimal.Decimal {
    if units <= 0 {
        return decimal.Zero
    }
    return decimal.NewFromInt(units).Div(decimal.NewFromInt(1_000_000))
}
```

**单价单位 = $/1M tokens**，输入 token 数被除以 1M —— 这条信息**必须写到 spec**，否则容易把价格写小一百万倍。

PromptTokens 在算 `PriceItemCodeUsage` 时会**减掉 cached + writeCached**：

```go
quantity = usage.PromptTokens
quantity -= usage.PromptTokensDetails.CachedTokens
quantity -= usage.PromptTokensDetails.WriteCachedTokens
if quantity < 0 { quantity = 0 }
```

注释原文（`cost_calc.go:115-127`）解释：input token cost 不能再二次收 cache 部分，cache 部分单独收。

**WriteCachedTokens 的变体处理**（`cost_calc.go:136-167`）：
- 如果 `WriteCached5MinTokens > 0` 或 `WriteCached1HourTokens > 0` → 按变体单独算两条 CostItem，**跳过共享 pricing**
- 否则 → 用 `PromptTokensDetails.WriteCachedTokens` 走共享 pricing

### 3.3 ccx llm.Usage 字段对齐检查

ccx PR1/PR2 应该已经在 `internal/llm` 里有 `Usage`。**implement 阶段必须确认下面字段都存在**：

| 字段 | axonhub 源 | 必需 |
|---|---|---|
| `Usage.PromptTokens int64` | ✓ | ✓ |
| `Usage.CompletionTokens int64` | ✓ | ✓ |
| `Usage.PromptTokensDetails *PromptTokensDetails` | ✓ | ✓ |
| `PromptTokensDetails.CachedTokens int64` | ✓ | ✓ |
| `PromptTokensDetails.WriteCachedTokens int64` | ✓ | ✓ |
| `PromptTokensDetails.WriteCached5MinTokens int64` | ✓ | 可缺，缺则只走总额 |
| `PromptTokensDetails.WriteCached1HourTokens int64` | ✓ | 可缺，同上 |

**找不到时的兜底**：在 ccx 的 Usage 上**先补字段**（最小变更），别在 calculator 里写"if exists"，否则单测覆盖会很难。

---

## 4. prices.json 的加载方式

PR3 PRD 第 60 行明确目录：

```
backend-go/internal/pricing/
    loader.go         # 加载 prices.json + 文件热重载
    prices.json       # 内置主流模型价格
```

### 4.1 三种方案对比

| 方案 | 优点 | 缺点 | PR3 推荐 |
|---|---|---|---|
| `embed.FS` 嵌入二进制 | 零外部依赖；零部署改动 | 价格更新需要重发版本 | 主推 |
| 运行时 fs（`os.ReadFile`） | 改价不需重启 | 容器需挂载文件 | 配合 fsnotify 作为 override |
| 内置 Go 常量 | 类型安全 | 维护痛苦 | 不推荐 |

**建议**：`embed.FS` 提供 default，配合 `os.ReadFile` 检测 `.config/prices.json` 做 override（参照 ccx 现有 `config.go` 的模式）+ `fsnotify` 热重载。

### 4.2 axonhub 的做法（参考但不照搬）

axonhub 不是从 json 加载 —— 它存到 ent schema `channel_model_price`（数据库），按 channel 维度独立配置。ccx 当前没用 ent，所以选 json + embed 方案。**不要把 ent 搬过来**（PR3 范围外）。

### 4.3 Loader 接口建议

```go
type Loader interface {
    GetPrice(modelID string) (ModelPrice, bool) // false 表示该模型没价格
    Reload() error
    Version() string // 用于 UsageRecord.PriceVersion
}

func NewEmbeddedLoader() Loader              // 仅用 embed.FS
func NewFSLoader(path string) (Loader, error) // 文件 + 热重载
```

`Version()` 推荐用文件 sha256 短哈希（前 12 字符），满足 PR3 PRD usage_log 的 `cost_price_reference_id` 字段。

---

## 5. 12 个主流模型初版价格建议

PRD 第 70-73 行列出 12 个模型。**axonhub 没有 prices.json / config 价格表可以直接抄**（价格在 ent DB）。下面给出**公开渠道（OpenAI / Anthropic / Google 官网 2026-Q1）**单价建议（单位 USD / 1M tokens），待 implement 阶段二次校验。

| 模型 | input | cached_input | output |
|---|---|---|---|
| gpt-4o | 2.50 | 1.25 | 10.00 |
| gpt-4o-mini | 0.15 | 0.075 | 0.60 |
| gpt-4-turbo | 10.00 | — | 30.00 |
| claude-3-5-sonnet | 3.00 | 0.30 (read) / 3.75 (write 5m) | 15.00 |
| claude-3-5-haiku | 0.80 | 0.08 / 1.00 | 4.00 |
| claude-3-opus | 15.00 | 1.50 / 18.75 | 75.00 |
| gemini-2.0-flash | 0.10 | — | 0.40 |
| gemini-1.5-pro | 1.25 / 2.50 (>128K) | 0.3125 | 5.00 / 10.00 (>128K) |
| gemini-1.5-flash | 0.075 / 0.15 (>128K) | 0.01875 | 0.30 / 0.60 (>128K) |
| o1 | 15.00 | 7.50 | 60.00 |
| o1-mini | 3.00 | 1.50 | 12.00 |
| o3-mini | 1.10 | 0.55 | 4.40 |

**标注待查**：gemini-1.5 系列是阶梯计费（128K 切分点），需要 `usage_tiered` + `PriceTier` 表达。所有 claude write cache 写需要拆 5m/1h 变体（值参考 Anthropic 文档：5m=base×1.25，1h=base×2.0）。

**implement 阶段动作**：
1. 不要靠这张表"上线"；implement sub-agent 必须从官方文档/已发布 commit 二次确认每个数字
2. prices.json 文件头部加注释字段 `"_lastVerified": "2026-05-07"` + `"_source": "openai.com/api/pricing, anthropic.com/pricing"`
3. 至少 5 个模型补 calculator 的表驱动单测（覆盖 flat_fee / usage_per_unit / usage_tiered + cache 变体）

---

## 6. JSON 序列化注意

`decimal.Decimal` 默认 MarshalJSON 输出**字符串**（如 `"3.0000"`），**不是 number**。原因：JS Number 精度只有 15 位，长十进制会丢精度。

**ccx 选择保持字符串**（PR3 PRD 已确认 NDJSON 中 decimal 用 string）。

如果未来前端 dashboard 需要 number，应在 API 边界用 `decimal.Decimal.InexactFloat64()` 转，不要改全局序列化。

---

## 7. 单测策略（calculator_test.go）

表驱动覆盖（参考 axonhub `cost_calc_test.go`）：

| 用例 | 配置 | 预期 |
|---|---|---|
| flat_fee | FlatFee=1.0 | 任何 quantity → 1.0 |
| usage_per_unit, 0 token | UsagePerUnit=2.0 | 0 |
| usage_per_unit, 1M token | UsagePerUnit=2.0 | 2.0 |
| usage_per_unit, 1.5M token | UsagePerUnit=2.0 | 3.0 |
| tiered, 在第 1 阶 | tiers=[1M@1.0, nil@2.0], q=500K | 0.5 |
| tiered, 跨 2 阶 | tiers=[1M@1.0, nil@2.0], q=1.5M | 1.0 + 0.5×2.0 = 2.0 |
| tiered, 跨 3 阶 | … | 累加 |
| cached_token | PriceItemCodePromptCachedToken | 用 details.CachedTokens |
| write cache 5m + 1h | 两个变体 | 输出 2 条 CostItem |
| usage 全 0 | — | 总价 0、items 至少有占位 |
| nil PromptTokensDetails | — | cache 项 quantity=0、不报错 |

---

## Caveats / Not Found

- **axonhub 没有 prices.json**，价格存数据库，不能直接搬，需要在 ccx 自建 json
- **PRD 第 70-73 行的 12 模型价格**当前为公开估值，**implement 阶段必须二次校验**，否则会算错钱
- **claude write cache 5m/1h 变体单价**官网未直接给数字，需要按倍率算（5m=1.25×, 1h=2.0×），implement 时确认
- gemini-1.5 是 128K 阶梯计费，必须用 `usage_tiered` 模式
