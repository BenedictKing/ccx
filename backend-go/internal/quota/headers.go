package quota

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ── 响应头配额解析 ──
//
// 设计原则：响应头只经显式 per-provider 映射解析，绝不猜测通用头名。
// 挂点：与 ratelimit/rate_limit_applier.go 同一位置（上游响应返回后）。
//
// 蓝本参考：OmniRoute src/lib/quota/providerQuotaTelemetry.ts parseRateLimitHeaders

// HeaderMapping 定义单个 provider 的响应头字段映射。
// 所有头名不区分大小写。
type HeaderMapping struct {
	Provider   string    // provider 标识（如 "anthropic", "openai"）
	Limit      string    // 上限头名（如 "anthropic-ratelimit-input-tokens-limit"）
	Remaining  string    // 剩余量头名
	Reset      string    // 重置时间头名（秒级时间戳或 HTTP-date）
	RetryAfter string    // 重试后头名（秒数）
	Dimension  Dimension // 对应配额维度
	Unit       string    // 单位
}

// knownHeaderMappings 是已知 provider 的配额响应头映射表。
// 注意：只映射显式确认过的头名，绝不猜测通用头名。
var knownHeaderMappings = []HeaderMapping{
	// Anthropic Messages API
	{
		Provider:  "anthropic",
		Limit:     "anthropic-ratelimit-input-tokens-limit",
		Remaining: "anthropic-ratelimit-input-tokens-remaining",
		Reset:     "anthropic-ratelimit-input-tokens-reset",
		Dimension: DimInputTokens,
		Unit:      "tokens",
	},
	{
		Provider:  "anthropic",
		Limit:     "anthropic-ratelimit-output-tokens-limit",
		Remaining: "anthropic-ratelimit-output-tokens-remaining",
		Reset:     "anthropic-ratelimit-output-tokens-reset",
		Dimension: DimOutputTokens,
		Unit:      "tokens",
	},
	{
		Provider:  "anthropic",
		Limit:     "anthropic-ratelimit-requests-limit",
		Remaining: "anthropic-ratelimit-requests-remaining",
		Reset:     "anthropic-ratelimit-requests-reset",
		Dimension: DimRequests,
		Unit:      "requests",
	},
	// OpenAI Chat API
	{
		Provider:   "openai",
		Limit:      "x-ratelimit-limit-tokens",
		Remaining:  "x-ratelimit-remaining-tokens",
		Reset:      "x-ratelimit-reset-tokens",
		RetryAfter: "retry-after",
		Dimension:  DimTokens,
		Unit:       "tokens",
	},
	{
		Provider:   "openai",
		Limit:      "x-ratelimit-limit-requests",
		Remaining:  "x-ratelimit-remaining-requests",
		Reset:      "x-ratelimit-reset-requests",
		RetryAfter: "retry-after",
		Dimension:  DimRequests,
		Unit:       "requests",
	},
}

// GetHeaderMappings 返回指定 provider 的所有响应头映射。
func GetHeaderMappings(provider string) []HeaderMapping {
	provider = strings.ToLower(strings.TrimSpace(provider))
	var result []HeaderMapping
	for _, m := range knownHeaderMappings {
		if m.Provider == provider {
			result = append(result, m)
		}
	}
	return result
}

// RegisterHeaderMapping 注册自定义响应头映射（用于 provider 插件或测试）。
func RegisterHeaderMapping(mapping HeaderMapping) {
	knownHeaderMappings = append(knownHeaderMappings, mapping)
}

// ── 解析函数 ──

// ParseResponseHeaders 从 HTTP 响应头中解析配额数据。
// provider 指定使用哪套映射；未找到映射时返回空切片（fail-open）。
func ParseResponseHeaders(provider string, headers http.Header) []Value {
	mappings := GetHeaderMappings(provider)
	if len(mappings) == 0 {
		return nil
	}

	var values []Value
	for _, m := range mappings {
		v := parseSingleMapping(headers, m)
		if v != nil {
			values = append(values, *v)
		}
	}
	return values
}

func parseSingleMapping(headers http.Header, m HeaderMapping) *Value {
	limit := floatHeader(getHeader(headers, m.Limit))
	remaining := floatHeader(getHeader(headers, m.Remaining))
	resetAtMs := resetHeaderToMs(getHeader(headers, m.Reset))
	retryAfter := floatHeader(getHeader(headers, m.RetryAfter))

	// 至少要有一种有意义的数据
	if limit == nil && remaining == nil && resetAtMs == 0 && retryAfter == nil {
		return nil
	}

	v := &Value{
		Dimension: m.Dimension,
		Source:    SourceResponseHeaders,
		Unit:      m.Unit,
		ResetAtMs: resetAtMs,
	}
	if limit != nil {
		v.Limit = limit
	}
	if remaining != nil {
		v.Remaining = remaining
	}

	// 如果有 retry-after 但无 remaining，结合 limit 推导 remaining
	if v.Remaining == nil && v.Limit != nil && retryAfter != nil {
		// retry-after 存在但本身不直接给 remaining，保持 limit 即可
		_ = retryAfter
	}

	return v
}

// getHeader 不区分大小写地获取响应头值。
// http.Header 本身已经是规范化的 CanonicalHeaderKey 形式，
// 但我们也做小写匹配以兼容非常规头名。
func getHeader(headers http.Header, name string) string {
	if name == "" {
		return ""
	}
	// 先尝试标准的 Get（已规范化 key）
	if v := headers.Get(name); v != "" {
		return v
	}
	// 再尝试遍历匹配（兼容大小写不统一的情况）
	target := strings.ToLower(name)
	for k, vals := range headers {
		if strings.ToLower(k) == target && len(vals) > 0 {
			return vals[0]
		}
	}
	return ""
}

// floatHeader 解析数值头值。
func floatHeader(value string) *float64 {
	if value == "" || strings.TrimSpace(value) == "" {
		return nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return nil
	}
	return &f
}

// resetHeaderToMs 将重置时间头值转换为毫秒时间戳。
// 支持：Unix 秒（如 "1234567890"）、Unix 毫秒、HTTP-date（如 "Wed, 21 Oct 2015 07:28:00 GMT"）、
// 相对秒数（如 "12s"、"90000ms" —— 部分 provider 用相对时间）。
func resetHeaderToMs(value string) int64 {
	if value == "" {
		return 0
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}

	// 尝试直接解析为数字（秒或毫秒）
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		// 大于 10^10 视为毫秒时间戳
		if f > 10_000_000_000 {
			return int64(f)
		}
		// 否则视为秒级时间戳
		return int64(f * 1000)
	}

	// 尝试 HTTP-date 格式
	for _, layout := range []string{
		time.RFC1123,
		time.RFC1123Z,
		time.RFC850,
		time.ANSIC,
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UnixMilli()
		}
	}

	// 尝试 duration（如 OpenAI 的 "6m0s"），相对当前时间换算为绝对 reset 时间。
	if d, err := time.ParseDuration(value); err == nil {
		return time.Now().Add(d).UnixMilli()
	}

	return 0
}

// RetryAfterToMs 将 Retry-After 头值转换为毫秒级的相对时长。
// 支持秒数（如 "120"）和 HTTP-date。
func RetryAfterToMs(value string) int64 {
	if value == "" {
		return 0
	}
	value = strings.TrimSpace(value)

	// 秒数
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		return int64(f * 1000)
	}

	// HTTP-date → 相对当前时间的毫秒数
	for _, layout := range []string{
		time.RFC1123,
		time.RFC1123Z,
		time.RFC850,
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, value); err == nil {
			diff := time.Until(t).Milliseconds()
			if diff > 0 {
				return diff
			}
			return 0
		}
	}

	return 0
}
