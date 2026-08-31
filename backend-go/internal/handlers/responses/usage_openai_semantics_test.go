package responses

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/types"
)

func completedEventWithUsage(t *testing.T, usage map[string]interface{}) string {
	t.Helper()
	payload := map[string]interface{}{
		"type":     "response.completed",
		"response": map[string]interface{}{"usage": usage},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return "event: response.completed\ndata: " + string(b) + "\n\n"
}

func usageField(t *testing.T, event string, path string) map[string]interface{} {
	t.Helper()
	for _, line := range strings.Split(event, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var data map[string]interface{}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data) != nil {
			continue
		}
		if data["type"] != "response.completed" {
			continue
		}
		resp := data["response"].(map[string]interface{})
		return resp["usage"].(map[string]interface{})
	}
	t.Fatalf("completed usage not found in: %s", event)
	return nil
}

// #274：客户端按官方 OpenAI 语义计算"未命中输入 = input_tokens - cached_tokens"，
// 要求 input_tokens 已包含缓存命中部分；否则缓存占比过半时结果为负。
// 转换器内部按"不含缓存"口径输出，出口必须归一加回。
func TestNormalizeCompletedEventUsageToOpenAISemantics_Issue274(t *testing.T) {
	// OpenAI 风格缓存字段：input_tokens_details.cached_tokens
	event := completedEventWithUsage(t, map[string]interface{}{
		"input_tokens":         789,
		"output_tokens":        154,
		"total_tokens":         943,
		"input_tokens_details": map[string]interface{}{"cached_tokens": 9984},
	})
	got := normalizeCompletedEventUsageToOpenAISemantics(event)
	usage := usageField(t, got, "")
	if v := usage["input_tokens"].(float64); v != 10773 {
		t.Fatalf("input_tokens = %v, want 10773 (789+9984)", v)
	}
	if v := usage["total_tokens"].(float64); v != 10927 {
		t.Fatalf("total_tokens = %v, want 10927 (10773+154)", v)
	}
	details := usage["input_tokens_details"].(map[string]interface{})
	if v := details["cached_tokens"].(float64); v != 9984 {
		t.Fatalf("cached_tokens = %v, want 9984", v)
	}
	if uncached := usage["input_tokens"].(float64) - details["cached_tokens"].(float64); uncached != 789 || uncached < 0 {
		t.Fatalf("客户端口径未命中输入 = %v, want 789 且非负", uncached)
	}
}

func TestNormalizeCompletedEventUsageToOpenAISemantics_ClaudeCacheFields(t *testing.T) {
	event := completedEventWithUsage(t, map[string]interface{}{
		"input_tokens":                   789,
		"output_tokens":                  154,
		"cache_read_input_tokens":        9984,
		"cache_creation_5m_input_tokens": 7,
		"cache_creation_1h_input_tokens": 3,
	})
	got := normalizeCompletedEventUsageToOpenAISemantics(event)
	usage := usageField(t, got, "")
	// 789 + 9984(读) + 7 + 3(写)
	if v := usage["input_tokens"].(float64); v != 10783 {
		t.Fatalf("input_tokens = %v, want 10783", v)
	}
	if v := usage["total_tokens"].(float64); v != 10937 {
		t.Fatalf("total_tokens = %v, want 10937", v)
	}
	if v := usage["cache_read_input_tokens"].(float64); v != 9984 {
		t.Fatalf("cache_read_input_tokens 应保留, got %v", v)
	}
}

func TestNormalizeCompletedEventUsageToOpenAISemantics_NoCacheUnchanged(t *testing.T) {
	event := completedEventWithUsage(t, map[string]interface{}{
		"input_tokens":  10,
		"output_tokens": 5,
		"total_tokens":  15,
	})
	got := normalizeCompletedEventUsageToOpenAISemantics(event)
	usage := usageField(t, got, "")
	if v := usage["input_tokens"].(float64); v != 10 {
		t.Fatalf("input_tokens = %v, want 10", v)
	}
	if v := usage["total_tokens"].(float64); v != 15 {
		t.Fatalf("total_tokens = %v, want 15", v)
	}
}

func TestNormalizeCompletedEventUsageToOpenAISemantics_SkipsNonCompleted(t *testing.T) {
	event := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\",\"usage\":{\"input_tokens\":789,\"input_tokens_details\":{\"cached_tokens\":9984}}}\n\n"
	got := normalizeCompletedEventUsageToOpenAISemantics(event)
	if got != event {
		t.Fatalf("非 completed 事件不应被改写:\n%s", got)
	}
}

func TestNormalizeResponsesUsageToOpenAISemantics(t *testing.T) {
	u := types.ResponsesUsage{
		InputTokens:          789,
		OutputTokens:         154,
		CacheReadInputTokens: 9984,
	}
	normalizeResponsesUsageToOpenAISemantics(&u)
	if u.InputTokens != 10773 || u.TotalTokens != 10927 {
		t.Fatalf("got input=%d total=%d, want 10773/10927", u.InputTokens, u.TotalTokens)
	}

	// OpenAI 详情字段兜底
	u2 := types.ResponsesUsage{
		InputTokens:        789,
		OutputTokens:       154,
		InputTokensDetails: &types.InputTokensDetails{CachedTokens: 9984},
	}
	normalizeResponsesUsageToOpenAISemantics(&u2)
	if u2.InputTokens != 10773 {
		t.Fatalf("got input=%d, want 10773", u2.InputTokens)
	}

	// nil 安全
	normalizeResponsesUsageToOpenAISemantics(nil)
}
