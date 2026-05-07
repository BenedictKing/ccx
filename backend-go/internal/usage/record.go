// Package usage 提供 ccx 用量审计落账（NDJSON）能力。
//
// 设计目标：
//  1. 字段命名对齐 axonhub `usage_log.go`（snake_case），便于将来反向迁移；
//  2. decimal 字段以 string 序列化（精度无损 + 跨语言同构）；
//  3. 仅提供 Append/Close 接口，查询能力不在 PR3 范围。
package usage

import (
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BenedictKing/ccx/internal/pricing"
)

// UsageRecord 单次请求落账记录。
//
// JSON key 全部 snake_case，对齐 axonhub `usage_log` 字段；
// 当字段对应到 axonhub schema 时复用相同 key（prompt_tokens / completion_tokens 等）。
//
// 字段来源说明：
//   - 与 axonhub 同名：channel_id / model_id / format / prompt_tokens / completion_tokens /
//     prompt_cached_tokens / prompt_write_cached_tokens / total_tokens / total_cost / cost_items /
//     cost_price_reference_id / prompt_write_cached_tokens_5m / prompt_write_cached_tokens_1h /
//     completion_reasoning_tokens
//   - ccx 新增：channel_name（NDJSON 不能 JOIN 必须冗余）/ api_key_masked（脱敏）/
//     duration_ms / success / error_code / timestamp（显式 RFC3339）
//   - 命名变更：request_id 类型 string（ccx trace UUID）≠ axonhub int 自增
type UsageRecord struct {
	// 基础元信息
	RequestID    string    `json:"request_id"`
	Timestamp    time.Time `json:"timestamp"`
	ChannelID    int       `json:"channel_id"`
	ChannelName  string    `json:"channel_name,omitempty"`
	APIKeyMasked string    `json:"api_key_masked,omitempty"`
	ModelID      string    `json:"model_id"`
	Format       string    `json:"format,omitempty"`

	// Token 计数（核心）
	InputTokens              int64 `json:"prompt_tokens"`
	OutputTokens             int64 `json:"completion_tokens"`
	CacheReadInputTokens     int64 `json:"prompt_cached_tokens,omitempty"`
	CacheCreationInputTokens int64 `json:"prompt_write_cached_tokens,omitempty"`
	TotalTokens              int64 `json:"total_tokens"`

	// Token 计数（推荐补充：cache 写变体 + 推理 token，参考 axonhub）
	CacheCreationInputTokens5m int64 `json:"prompt_write_cached_tokens_5m,omitempty"`
	CacheCreationInputTokens1h int64 `json:"prompt_write_cached_tokens_1h,omitempty"`
	CompletionReasoningTokens  int64 `json:"completion_reasoning_tokens,omitempty"`

	// 计费
	TotalCost    *decimal.Decimal    `json:"total_cost,omitempty"`
	CostItems    []pricing.CostItem  `json:"cost_items,omitempty"`
	PriceVersion string              `json:"cost_price_reference_id,omitempty"`

	// 运行期指标
	DurationMs int64  `json:"duration_ms"`
	Success    bool   `json:"success"`
	ErrorCode  string `json:"error_code,omitempty"`
}

// MaskAPIKey 对 api key 做脱敏处理，仅保留首尾各 4 位字符，中间替换为 ***。
//
// 规则：
//   - 空字符串返回空字符串；
//   - 长度 <= 8 时整体替换为 ***（视为短 key 没有可暴露的部分）；
//   - 长度  > 8 时返回 prefix(4) + "***" + suffix(4)。
func MaskAPIKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "***" + key[len(key)-4:]
}
