package pricing

import "github.com/shopspring/decimal"

// TierCost 阶梯计费的单段消费明细。
type TierCost struct {
	// UpTo 该段上界（与 PriceTier.UpTo 对应）；nil 表示无上界（最后一段）。
	UpTo *int64 `json:"upTo,omitempty"`

	// Units 该段实际消耗的 token 数（int64，便于跨段累加）。
	Units int64 `json:"units"`

	// Subtotal 该段小计（USD）。
	Subtotal decimal.Decimal `json:"subtotal"`
}

// CostItem 单个计费项的最终费用条目。
//
// 一个 PriceItemCode 可对应多个 CostItem，例如 cache 写在拆 5min / 1hour 变体时
// 会输出两条（PromptWriteCacheVariantCode 字段区分）。
type CostItem struct {
	// ItemCode 计费项编码。
	ItemCode PriceItemCode `json:"itemCode"`

	// PromptWriteCacheVariantCode cache 写变体（仅 ItemCode == prompt_write_cached_tokens 时使用）。
	PromptWriteCacheVariantCode PromptWriteCacheVariantCode `json:"promptWriteCacheVariantCode,omitempty"`

	// Quantity 实际计入该 CostItem 的 token 数。
	Quantity int64 `json:"quantity"`

	// TierBreakdown 阶梯计费时按段拆分的明细；非阶梯模式为空。
	TierBreakdown []TierCost `json:"tierBreakdown,omitempty"`

	// Subtotal 该计费项小计（USD）。decimal 序列化为字符串以避免 JS Number 精度问题。
	Subtotal decimal.Decimal `json:"subtotal"`
}
