package quota

import (
	"net/http"
	"testing"
	"time"
)

// ── 响应头解析测试 ──

func TestParseResponseHeadersAnthropic(t *testing.T) {
	headers := make(http.Header)
	headers.Set("anthropic-ratelimit-input-tokens-limit", "50000")
	headers.Set("anthropic-ratelimit-input-tokens-remaining", "45000")
	resetTime := time.Now().Add(time.Hour).Format(time.RFC3339)
	headers.Set("anthropic-ratelimit-input-tokens-reset", resetTime)

	values := ParseResponseHeaders("anthropic", headers)
	if len(values) == 0 {
		t.Fatal("expected parsed values, got none")
	}

	// 找到 input_tokens 维度
	var found bool
	for _, v := range values {
		if v.Dimension == DimInputTokens {
			found = true
			if v.Source != SourceResponseHeaders {
				t.Errorf("source = %v, want response_headers", v.Source)
			}
			if v.Limit == nil || *v.Limit != 50000 {
				t.Errorf("limit = %v, want 50000", v.Limit)
			}
			if v.Remaining == nil || *v.Remaining != 45000 {
				t.Errorf("remaining = %v, want 45000", v.Remaining)
			}
			if v.ResetAtMs == 0 {
				t.Error("resetAtMs should be set")
			}
		}
	}
	if !found {
		t.Error("expected input_tokens dimension in parsed values")
	}
}

func TestParseResponseHeadersOpenAI(t *testing.T) {
	headers := make(http.Header)
	headers.Set("x-ratelimit-limit-tokens", "200000")
	headers.Set("x-ratelimit-remaining-tokens", "150000")
	headers.Set("x-ratelimit-reset-tokens", "6m0s") // 相对时长格式

	values := ParseResponseHeaders("openai", headers)
	if len(values) == 0 {
		t.Fatal("expected parsed values, got none")
	}

	// 注意："6m0s" 不是纯数字，resetHeaderToMs 无法解析 → ResetAtMs 为 0
	// 这是预期的：OpenAI 的 reset 头是相对时长，需要额外处理才能转成绝对时间
	// 当前实现只处理绝对时间戳和 HTTP-date
	var found bool
	for _, v := range values {
		if v.Dimension == DimTokens {
			found = true
			if v.Limit == nil || *v.Limit != 200000 {
				t.Errorf("limit = %v, want 200000", v.Limit)
			}
			if v.Remaining == nil || *v.Remaining != 150000 {
				t.Errorf("remaining = %v, want 150000", v.Remaining)
			}
		}
	}
	if !found {
		t.Error("expected tokens dimension in parsed values")
	}
}

func TestParseResponseHeadersUnknownProvider(t *testing.T) {
	headers := make(http.Header)
	headers.Set("x-some-rate-limit", "100")

	values := ParseResponseHeaders("unknown_provider", headers)
	if len(values) != 0 {
		t.Errorf("unknown provider should return 0 values, got %d", len(values))
	}
}

func TestParseResponseHeadersEmpty(t *testing.T) {
	headers := make(http.Header)

	values := ParseResponseHeaders("anthropic", headers)
	if len(values) != 0 {
		t.Errorf("empty headers should return 0 values, got %d", len(values))
	}
}

func TestRegisterHeaderMapping(t *testing.T) {
	// 快照全局映射表，defer 恢复完整原始表：
	// 只回填单条映射会清掉其余默认项，污染同包后续测试的解析行为。
	original := make([]HeaderMapping, len(knownHeaderMappings))
	copy(original, knownHeaderMappings)
	defer func() {
		knownHeaderMappings = original
	}()

	RegisterHeaderMapping(HeaderMapping{
		Provider:  "custom_provider",
		Limit:     "x-custom-limit",
		Remaining: "x-custom-remaining",
		Dimension: DimTokens,
		Unit:      "tokens",
	})

	headers := make(http.Header)
	headers.Set("x-custom-limit", "1000")
	headers.Set("x-custom-remaining", "900")

	values := ParseResponseHeaders("custom_provider", headers)
	if len(values) != 1 {
		t.Fatalf("expected 1 value, got %d", len(values))
	}
	if *values[0].Limit != 1000 {
		t.Errorf("limit = %v, want 1000", values[0].Limit)
	}
	if *values[0].Remaining != 900 {
		t.Errorf("remaining = %v, want 900", values[0].Remaining)
	}
}

// ── resetHeaderToMs 测试 ──

func TestResetHeaderToMs(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int64
	}{
		{"empty", "", 0},
		{"whitespace", "  ", 0},
		{"unix seconds", "1700000000", 1700000000000},
		{"unix ms", "1700000000000", 1700000000000},
		{"rfc3339", "2024-01-01T00:00:00Z", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()},
		{"rfc1123", "Mon, 01 Jan 2024 00:00:00 GMT", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()},
		{"invalid", "not-a-date", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resetHeaderToMs(tt.input)
			if tt.want != 0 && got != tt.want {
				// 对于时间解析，检查误差在合理范围内
				if tt.name == "rfc3339" || tt.name == "rfc1123" {
					diff := got - tt.want
					if diff < 0 {
						diff = -diff
					}
					if diff > 1000 { // 允许 1 秒误差（时区处理）
						t.Errorf("resetHeaderToMs(%q) = %d, want ~%d (diff=%dms)", tt.input, got, tt.want, diff)
					}
					return
				}
			}
			if got != tt.want {
				t.Errorf("resetHeaderToMs(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// ── RetryAfterToMs 测试 ──

func TestRetryAfterToMs(t *testing.T) {
	tests := []struct {
		name  string
		input string
		check func(int64) bool
	}{
		{"empty", "", func(v int64) bool { return v == 0 }},
		{"seconds", "120", func(v int64) bool { return v == 120000 }},
		{"float seconds", "1.5", func(v int64) bool { return v == 1500 }},
		{"http-date in future", "", func(v int64) bool { return v >= 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RetryAfterToMs(tt.input)
			if !tt.check(got) {
				t.Errorf("RetryAfterToMs(%q) = %d, unexpected value", tt.input, got)
			}
		})
	}

	// HTTP-date 测试
	futureDate := time.Now().Add(2 * time.Hour).Format(time.RFC1123)
	ms := RetryAfterToMs(futureDate)
	if ms <= 0 {
		t.Errorf("RetryAfterToMs(future date) = %d, want positive", ms)
	}

	// 过去的 HTTP-date → 0
	pastDate := time.Now().Add(-2 * time.Hour).Format(time.RFC1123)
	ms = RetryAfterToMs(pastDate)
	if ms != 0 {
		t.Errorf("RetryAfterToMs(past date) = %d, want 0", ms)
	}
}

// ── getHeader 大小写不敏感测试 ──

func TestGetHeaderCaseInsensitive(t *testing.T) {
	headers := make(http.Header)
	headers.Set("X-Custom-Header", "value123")

	// 标准方式
	if v := getHeader(headers, "X-Custom-Header"); v != "value123" {
		t.Errorf("standard get = %q, want value123", v)
	}

	// 全小写
	if v := getHeader(headers, "x-custom-header"); v != "value123" {
		t.Errorf("lowercase get = %q, want value123", v)
	}

	// 不存在
	if v := getHeader(headers, "x-nonexistent"); v != "" {
		t.Errorf("nonexistent = %q, want empty", v)
	}
}

// 裸相对秒数（不少 provider 直接给 "60"）必须按相对时长解析。
// 若照 epoch 秒解析会得到 1970 年，跨窗口剪除会把刚写入的有效观测判定为
// 「窗口已翻转」并在首次读时删除，配额保护静默失效。
func TestResetHeaderToMsBareRelativeSeconds(t *testing.T) {
	before := time.Now()
	got := resetHeaderToMs("60")
	after := time.Now()

	if minWant := before.Add(59 * time.Second).UnixMilli(); got < minWant {
		t.Fatalf("resetHeaderToMs(\"60\") = %d, 早于相对 60s 下界 %d", got, minWant)
	}
	if maxWant := after.Add(61 * time.Second).UnixMilli(); got > maxWant {
		t.Fatalf("resetHeaderToMs(\"60\") = %d, 晚于相对 60s 上界 %d", got, maxWant)
	}
	if got <= time.Now().UnixMilli() {
		t.Fatalf("相对 60s 必须落在未来，got %d", got)
	}
	if got < 1_000_000_000_000 {
		t.Fatalf("不得再返回 1970 量级的毫秒时间戳，got %d", got)
	}

	// 无窗口语义的值仍归一化为 0（0 会被上层当作"无 reset 头"）
	if v := resetHeaderToMs("0"); v != 0 {
		t.Fatalf("resetHeaderToMs(\"0\") = %d, want 0", v)
	}
	if v := resetHeaderToMs("-5"); v != 0 {
		t.Fatalf("resetHeaderToMs(\"-5\") = %d, want 0", v)
	}
}
