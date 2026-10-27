package providers

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/types"
)

func TestOpenAIProvider_HandleStreamResponse_DeepSeekCacheUsage(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}],"model":"deepseek-chat"}`,
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}],"model":"deepseek-chat"}`,
		`data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"model":"deepseek-chat"}`,
		`data: {"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":100,"completion_tokens":5,"prompt_cache_hit_tokens":80,"prompt_cache_miss_tokens":20}}`,
		`data: [DONE]`,
		"",
	}, "\n")

	provider := &OpenAIProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	events := collectStreamEvents(eventChan)
	select {
	case streamErr := <-errChan:
		if streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}
	default:
	}

	messageDelta := extractMessageDelta(t, events)
	usage, ok := messageDelta["usage"].(map[string]interface{})
	if !ok {
		t.Fatalf("usage field missing in message_delta: %v", messageDelta)
	}

	inputTokens, _ := usage["input_tokens"].(float64)
	outputTokens, _ := usage["output_tokens"].(float64)
	cacheRead, _ := usage["cache_read_input_tokens"].(float64)

	if int(inputTokens) != 100 {
		t.Fatalf("input_tokens = %v, want 100", inputTokens)
	}
	if int(outputTokens) != 5 {
		t.Fatalf("output_tokens = %v, want 5", outputTokens)
	}
	if int(cacheRead) != 80 {
		t.Fatalf("cache_read_input_tokens = %v, want 80", cacheRead)
	}
}

func TestOpenAIProvider_HandleStreamResponse_OpenAINestedCachedTokens(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}],"model":"gpt-4o"}`,
		`data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"model":"gpt-4o"}`,
		`data: {"id":"chatcmpl-2","choices":[],"usage":{"prompt_tokens":200,"completion_tokens":10,"prompt_tokens_details":{"cached_tokens":150}}}`,
		`data: [DONE]`,
		"",
	}, "\n")

	provider := &OpenAIProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	events := collectStreamEvents(eventChan)
	select {
	case streamErr := <-errChan:
		if streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}
	default:
	}

	messageDelta := extractMessageDelta(t, events)
	usage, ok := messageDelta["usage"].(map[string]interface{})
	if !ok {
		t.Fatalf("usage field missing in message_delta: %v", messageDelta)
	}

	cacheRead, _ := usage["cache_read_input_tokens"].(float64)
	if int(cacheRead) != 150 {
		t.Fatalf("cache_read_input_tokens = %v, want 150", cacheRead)
	}
}

func TestOpenAIProvider_HandleStreamResponse_ToolCallStopWithUsage(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"chatcmpl-3","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_weather","arguments":"{\"city\":\"NYC\"}"}}]},"finish_reason":null}],"model":"gpt-4o"}`,
		`data: {"id":"chatcmpl-3","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"model":"gpt-4o"}`,
		`data: {"id":"chatcmpl-3","choices":[],"usage":{"prompt_tokens":50,"completion_tokens":20,"prompt_cache_hit_tokens":30}}`,
		`data: [DONE]`,
		"",
	}, "\n")

	provider := &OpenAIProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	events := collectStreamEvents(eventChan)
	select {
	case streamErr := <-errChan:
		if streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}
	default:
	}

	messageDelta := extractMessageDelta(t, events)

	delta, _ := messageDelta["delta"].(map[string]interface{})
	stopReason, _ := delta["stop_reason"].(string)
	if stopReason != "tool_use" {
		t.Fatalf("stop_reason = %q, want \"tool_use\"", stopReason)
	}

	usage, ok := messageDelta["usage"].(map[string]interface{})
	if !ok {
		t.Fatalf("usage field missing in message_delta: %v", messageDelta)
	}

	cacheRead, _ := usage["cache_read_input_tokens"].(float64)
	if int(cacheRead) != 30 {
		t.Fatalf("cache_read_input_tokens = %v, want 30", cacheRead)
	}
}

// Gemini 的 OpenAI 兼容端点在调用工具后以 finish_reason "stop" 结束，且每个并行调用单独一个 chunk。
func TestOpenAIProvider_HandleStreamResponse_ToolCallsWithFinishReasonStop(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"chatcmpl-4","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"id":"call_paris","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]}}],"model":"gemini-3.5-flash"}`,
		`data: {"id":"chatcmpl-4","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"id":"call_tokyo","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Tokyo\"}"}}]}}],"model":"gemini-3.5-flash"}`,
		`data: {"id":"chatcmpl-4","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":"stop"}],"model":"gemini-3.5-flash"}`,
		`data: [DONE]`,
		"",
	}, "\n")

	provider := &OpenAIProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	events := collectStreamEvents(eventChan)
	select {
	case streamErr := <-errChan:
		if streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}
	default:
	}

	toolUseBlocks := 0
	for _, event := range events {
		if strings.Contains(event, `"type":"tool_use"`) {
			toolUseBlocks++
		}
	}
	if toolUseBlocks != 2 {
		t.Fatalf("tool_use blocks = %d, want 2", toolUseBlocks)
	}

	messageDelta := extractMessageDelta(t, events)
	delta, _ := messageDelta["delta"].(map[string]interface{})
	stopReason, _ := delta["stop_reason"].(string)
	if stopReason != "tool_use" {
		t.Fatalf("stop_reason = %q, want \"tool_use\"", stopReason)
	}
}

// 没有工具调用时 finish_reason "stop" 仍是 end_turn。
func TestOpenAIProvider_HandleStreamResponse_TextWithFinishReasonStop(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"chatcmpl-5","choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}],"model":"gemini-3.5-flash"}`,
		`data: {"id":"chatcmpl-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"model":"gemini-3.5-flash"}`,
		`data: [DONE]`,
		"",
	}, "\n")

	provider := &OpenAIProvider{}
	eventChan, _, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	messageDelta := extractMessageDelta(t, collectStreamEvents(eventChan))
	delta, _ := messageDelta["delta"].(map[string]interface{})
	stopReason, _ := delta["stop_reason"].(string)
	if stopReason != "end_turn" {
		t.Fatalf("stop_reason = %q, want \"end_turn\"", stopReason)
	}
}

func TestOpenAIProvider_HandleStreamResponse_MultipleUsageChunksMerge(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"chatcmpl-4","choices":[{"index":0,"delta":{"content":"OK"},"finish_reason":null}],"model":"m","usage":{"prompt_tokens":100,"completion_tokens":3}}`,
		`data: {"id":"chatcmpl-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"model":"m"}`,
		`data: {"id":"chatcmpl-4","choices":[],"usage":{"prompt_cache_hit_tokens":60}}`,
		`data: [DONE]`,
		"",
	}, "\n")

	provider := &OpenAIProvider{}
	eventChan, errChan, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	events := collectStreamEvents(eventChan)
	select {
	case streamErr := <-errChan:
		if streamErr != nil {
			t.Fatalf("unexpected stream error: %v", streamErr)
		}
	default:
	}

	messageDelta := extractMessageDelta(t, events)
	usage, ok := messageDelta["usage"].(map[string]interface{})
	if !ok {
		t.Fatalf("usage field missing in message_delta: %v", messageDelta)
	}

	inputTokens, _ := usage["input_tokens"].(float64)
	cacheRead, _ := usage["cache_read_input_tokens"].(float64)
	if int(inputTokens) != 100 {
		t.Fatalf("input_tokens = %v, want 100 (merged from first chunk)", inputTokens)
	}
	if int(cacheRead) != 60 {
		t.Fatalf("cache_read_input_tokens = %v, want 60 (merged from second chunk)", cacheRead)
	}
}

func TestOpenAIProvider_ConvertToClaudeResponse_DeepSeekCache(t *testing.T) {
	respBody := `{"id":"chatcmpl-5","choices":[{"index":0,"message":{"role":"assistant","content":"Hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":200,"completion_tokens":10,"prompt_cache_hit_tokens":150,"prompt_cache_miss_tokens":50}}`

	provider := &OpenAIProvider{}
	claudeResp, err := provider.ConvertToClaudeResponse(&types.ProviderResponse{
		StatusCode: 200,
		Body:       []byte(respBody),
	})
	if err != nil {
		t.Fatalf("ConvertToClaudeResponse error: %v", err)
	}

	if claudeResp.Usage == nil {
		t.Fatal("usage is nil")
	}
	if claudeResp.Usage.InputTokens != 200 {
		t.Fatalf("InputTokens = %d, want 200", claudeResp.Usage.InputTokens)
	}
	if claudeResp.Usage.OutputTokens != 10 {
		t.Fatalf("OutputTokens = %d, want 10", claudeResp.Usage.OutputTokens)
	}
	if claudeResp.Usage.CacheReadInputTokens != 150 {
		t.Fatalf("CacheReadInputTokens = %d, want 150", claudeResp.Usage.CacheReadInputTokens)
	}
	if claudeResp.Usage.PromptTokensTotal != 200 {
		t.Fatalf("PromptTokensTotal = %d, want 200", claudeResp.Usage.PromptTokensTotal)
	}
}

func TestOpenAIProvider_ConvertToClaudeResponse_OpenAINestedCache(t *testing.T) {
	respBody := `{"id":"chatcmpl-6","choices":[{"index":0,"message":{"role":"assistant","content":"Hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":300,"completion_tokens":15,"prompt_tokens_details":{"cached_tokens":250}}}`

	provider := &OpenAIProvider{}
	claudeResp, err := provider.ConvertToClaudeResponse(&types.ProviderResponse{
		StatusCode: 200,
		Body:       []byte(respBody),
	})
	if err != nil {
		t.Fatalf("ConvertToClaudeResponse error: %v", err)
	}

	if claudeResp.Usage == nil {
		t.Fatal("usage is nil")
	}
	if claudeResp.Usage.CacheReadInputTokens != 250 {
		t.Fatalf("CacheReadInputTokens = %d, want 250", claudeResp.Usage.CacheReadInputTokens)
	}
}

func TestNormalizeOpenAIUsage_NilOnEmpty(t *testing.T) {
	result := normalizeOpenAIUsage(map[string]interface{}{"unknown_field": "value"})
	if result != nil {
		t.Fatalf("expected nil for unrecognized fields, got %v", result)
	}
}

func TestNormalizeOpenAIUsage_HitPlusMissSynthesizesInputTokens(t *testing.T) {
	u := map[string]interface{}{
		"prompt_cache_hit_tokens":  float64(80),
		"prompt_cache_miss_tokens": float64(20),
		"completion_tokens":        float64(5),
	}
	result := normalizeOpenAIUsage(u)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result["input_tokens"] != 100 {
		t.Fatalf("input_tokens = %v, want 100 (hit+miss)", result["input_tokens"])
	}
	if result["cache_read_input_tokens"] != 80 {
		t.Fatalf("cache_read_input_tokens = %v, want 80", result["cache_read_input_tokens"])
	}
}

func TestNormalizeOpenAIUsage_HitOnlyDoesNotSynthesizeInputTokens(t *testing.T) {
	u := map[string]interface{}{
		"prompt_cache_hit_tokens": float64(60),
	}
	result := normalizeOpenAIUsage(u)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if _, exists := result["input_tokens"]; exists {
		t.Fatalf("input_tokens should not be set when only hit is present, got %v", result["input_tokens"])
	}
	if result["cache_read_input_tokens"] != 60 {
		t.Fatalf("cache_read_input_tokens = %v, want 60", result["cache_read_input_tokens"])
	}
}

func TestNormalizeOpenAIUsage_InputTokensDetailsCachedTokens(t *testing.T) {
	u := map[string]interface{}{
		"prompt_tokens":     float64(100),
		"completion_tokens": float64(5),
		"input_tokens_details": map[string]interface{}{
			"cached_tokens": float64(70),
		},
	}
	result := normalizeOpenAIUsage(u)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result["cache_read_input_tokens"] != 70 {
		t.Fatalf("cache_read_input_tokens = %v, want 70", result["cache_read_input_tokens"])
	}
}

func TestMergeUsageMaps(t *testing.T) {
	dst := map[string]interface{}{"input_tokens": 100, "output_tokens": 5}
	src := map[string]interface{}{"cache_read_input_tokens": 80}
	merged := mergeUsageMaps(dst, src)
	if merged["input_tokens"] != 100 {
		t.Fatalf("input_tokens lost after merge")
	}
	if merged["cache_read_input_tokens"] != 80 {
		t.Fatalf("cache_read_input_tokens not merged")
	}
}

func TestExtractOpenAICacheToUsage(t *testing.T) {
	tests := []struct {
		name     string
		raw      map[string]interface{}
		wantRead int
	}{
		{
			name:     "DeepSeek prompt_cache_hit_tokens",
			raw:      map[string]interface{}{"prompt_cache_hit_tokens": float64(80)},
			wantRead: 80,
		},
		{
			name:     "OpenAI prompt_tokens_details.cached_tokens",
			raw:      map[string]interface{}{"prompt_tokens_details": map[string]interface{}{"cached_tokens": float64(120)}},
			wantRead: 120,
		},
		{
			name:     "direct cache_read_input_tokens",
			raw:      map[string]interface{}{"cache_read_input_tokens": float64(50)},
			wantRead: 50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := &types.Usage{}
			extractOpenAICacheToUsage(tt.raw, usage)
			if usage.CacheReadInputTokens != tt.wantRead {
				t.Fatalf("CacheReadInputTokens = %d, want %d", usage.CacheReadInputTokens, tt.wantRead)
			}
		})
	}
}

func TestOpenAIProvider_ConvertToClaudeResponse_CacheFieldInJSON(t *testing.T) {
	respBody := `{"id":"chatcmpl-7","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":500,"completion_tokens":20,"prompt_cache_hit_tokens":400}}`

	provider := &OpenAIProvider{}
	claudeResp, err := provider.ConvertToClaudeResponse(&types.ProviderResponse{
		StatusCode: 200,
		Body:       []byte(respBody),
	})
	if err != nil {
		t.Fatalf("ConvertToClaudeResponse error: %v", err)
	}

	respJSON, _ := json.Marshal(claudeResp)
	var parsed map[string]interface{}
	if err := json.Unmarshal(respJSON, &parsed); err != nil {
		t.Fatalf("unmarshal claudeResp json failed: %v", err)
	}

	usage, _ := parsed["usage"].(map[string]interface{})
	if usage == nil {
		t.Fatal("usage missing in JSON output")
	}
	cacheRead, _ := usage["cache_read_input_tokens"].(float64)
	if int(cacheRead) != 400 {
		t.Fatalf("cache_read_input_tokens in JSON = %v, want 400", cacheRead)
	}
}
