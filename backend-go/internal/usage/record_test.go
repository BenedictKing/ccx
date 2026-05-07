package usage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// TestUsageRecord_JSONRoundtrip 验证：
//   - JSON key 全 snake_case 且对齐 axonhub 命名（prompt_tokens / completion_tokens）；
//   - decimal 字段以字符串形式落账，反序列化后仍精度无损；
//   - nil decimal 不出现 total_cost key；
//   - omitempty 字段在零值时不出现。
func TestUsageRecord_JSONRoundtrip(t *testing.T) {
	cost := decimal.RequireFromString("0.012345678901234567")
	rec := UsageRecord{
		RequestID:                "trace-abc",
		Timestamp:                time.Date(2026, 5, 7, 12, 30, 0, 0, time.UTC),
		ChannelID:                7,
		ChannelName:              "anthropic-primary",
		APIKeyMasked:             "abcd***wxyz",
		ModelID:                  "claude-sonnet-4",
		Format:                   "anthropic/messages",
		InputTokens:              1000,
		OutputTokens:             200,
		CacheReadInputTokens:     800,
		CacheCreationInputTokens: 100,
		TotalTokens:              1200,
		TotalCost:                &cost,
		PriceVersion:             "abc123def456",
		DurationMs:               1234,
		Success:                  true,
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	str := string(data)
	for _, key := range []string{
		`"request_id"`, `"channel_id"`, `"channel_name"`, `"api_key_masked"`,
		`"model_id"`, `"prompt_tokens"`, `"completion_tokens"`,
		`"prompt_cached_tokens"`, `"prompt_write_cached_tokens"`,
		`"total_tokens"`, `"total_cost"`, `"cost_price_reference_id"`,
		`"duration_ms"`, `"success"`,
	} {
		if !strings.Contains(str, key) {
			t.Errorf("expected key %s in JSON, got: %s", key, str)
		}
	}
	// 不应该出现 PascalCase
	for _, k := range []string{`"InputTokens"`, `"OutputTokens"`, `"TotalCost"`} {
		if strings.Contains(str, k) {
			t.Errorf("PascalCase key leaked: %s", k)
		}
	}
	// decimal 必须是字符串形式
	if !strings.Contains(str, `"total_cost":"0.012345678901234567"`) {
		t.Errorf("expected decimal as string, got: %s", str)
	}
	// 反序列化精度无损
	var back UsageRecord
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if back.TotalCost == nil || !back.TotalCost.Equal(cost) {
		t.Errorf("decimal precision lost: %v vs %v", back.TotalCost, cost)
	}
	if back.InputTokens != 1000 || back.OutputTokens != 200 {
		t.Errorf("token roundtrip mismatch: in=%d out=%d", back.InputTokens, back.OutputTokens)
	}
}

// TestUsageRecord_NilDecimalOmitted nil *decimal.Decimal 不应出现在 JSON 中。
func TestUsageRecord_NilDecimalOmitted(t *testing.T) {
	rec := UsageRecord{
		RequestID:    "trace-x",
		Timestamp:    time.Now().UTC(),
		ChannelID:    1,
		ModelID:      "m",
		InputTokens:  10,
		OutputTokens: 20,
		TotalTokens:  30,
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(data), "total_cost") {
		t.Errorf("nil total_cost should be omitted, got: %s", data)
	}
}

func TestMaskAPIKey(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"   ", ""},
		{"short", "***"},
		{"12345678", "***"},
		{"123456789", "1234***6789"},
		{"sk-abc1234567890xyz", "sk-a***0xyz"},
	}
	for _, c := range cases {
		got := MaskAPIKey(c.in)
		if got != c.want {
			t.Errorf("MaskAPIKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
