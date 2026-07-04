package common

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/gin-gonic/gin"
)

type nopFlusher struct{}

func (nopFlusher) Flush() {}

func TestBuildStreamErrorEvent_EmptyStreamUsesRetryMessage(t *testing.T) {
	tests := []error{
		ErrEmptyStreamResponse,
		ErrStreamPostCommitStalled,
	}

	for _, err := range tests {
		event := BuildStreamErrorEvent(err)
		expectedMessage := `"message":"Empty response from upstream; please try again."`
		if !strings.Contains(event, expectedMessage) {
			t.Fatalf("BuildStreamErrorEvent(%v) = %s, want retry message", err, event)
		}
		if strings.Contains(event, err.Error()) {
			t.Fatalf("BuildStreamErrorEvent(%v) leaked internal error: %s", err, event)
		}
	}
}

func TestBuildStreamErrorEvent_GenericErrorKeepsDiagnostic(t *testing.T) {
	event := BuildStreamErrorEvent(errors.New("provider disconnected"))
	if !strings.Contains(event, `"message":"Stream processing error: provider disconnected"`) {
		t.Fatalf("BuildStreamErrorEvent generic error = %s, want diagnostic message", event)
	}
}

func TestResolveStreamPreflightTimeouts_ToolCallIdleTimeoutIsIndependent(t *testing.T) {
	upstream := &config.UpstreamConfig{}
	global := metrics.CircuitBreakerParams{
		StreamFirstContentTimeoutMs: 30000,
		StreamInactivityTimeoutMs:   20000,
		StreamToolCallIdleTimeoutMs: 30000,
	}

	timeouts := ResolveStreamPreflightTimeouts(upstream, global)

	if timeouts.InactivityTimeoutMs != 20000 {
		t.Fatalf("InactivityTimeoutMs = %d, want 20000", timeouts.InactivityTimeoutMs)
	}
	if timeouts.ToolCallIdleTimeoutMs != 30000 {
		t.Fatalf("ToolCallIdleTimeoutMs = %d, want 30000", timeouts.ToolCallIdleTimeoutMs)
	}
}

func TestResolveStreamPreflightTimeouts_DefaultToolCallIdleIsLongerThanStreamIdle(t *testing.T) {
	upstream := &config.UpstreamConfig{}
	global := metrics.NewMetricsManager().GetCircuitBreakerConfig()

	timeouts := ResolveStreamPreflightTimeouts(upstream, global)

	if timeouts.ToolCallIdleTimeoutMs != 120000 {
		t.Fatalf("ToolCallIdleTimeoutMs = %d, want 120000", timeouts.ToolCallIdleTimeoutMs)
	}
	if timeouts.ToolCallIdleTimeoutMs <= timeouts.InactivityTimeoutMs {
		t.Fatalf("tool call idle timeout should be longer than regular stream idle: tool=%d stream=%d", timeouts.ToolCallIdleTimeoutMs, timeouts.InactivityTimeoutMs)
	}
}

func TestResolveStreamPreflightTimeouts_ToolCallIdleChannelOverride(t *testing.T) {
	upstream := &config.UpstreamConfig{StreamToolCallIdleTimeoutMs: 60000}
	global := metrics.CircuitBreakerParams{
		StreamFirstContentTimeoutMs: 30000,
		StreamInactivityTimeoutMs:   20000,
		StreamToolCallIdleTimeoutMs: 30000,
	}

	timeouts := ResolveStreamPreflightTimeouts(upstream, global)

	if timeouts.ToolCallIdleTimeoutMs != 60000 {
		t.Fatalf("ToolCallIdleTimeoutMs = %d, want 60000", timeouts.ToolCallIdleTimeoutMs)
	}
}

func TestResolveStreamToolCallIdleTimeout_ClampsToRange(t *testing.T) {
	tests := []struct {
		name        string
		channel     int
		global      int
		wantTimeout int
	}{
		{name: "below minimum", channel: 999, global: 3000, wantTimeout: 30000},
		{name: "above maximum", channel: 300001, global: 120000, wantTimeout: 300000},
		{name: "global below minimum", channel: 0, global: 999, wantTimeout: 30000},
		{name: "global above maximum", channel: 0, global: 300001, wantTimeout: 300000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveStreamToolCallIdleTimeout(tt.channel, tt.global)
			if got != tt.wantTimeout {
				t.Fatalf("ResolveStreamToolCallIdleTimeout(%d, %d) = %d, want %d", tt.channel, tt.global, got, tt.wantTimeout)
			}
		})
	}
}

func TestResolveStreamInactivityTimeout_ClampsToRange(t *testing.T) {
	tests := []struct {
		name        string
		channel     int
		global      int
		wantTimeout int
	}{
		{name: "below minimum", channel: 999, global: 3000, wantTimeout: 1000},
		{name: "above maximum", channel: 180001, global: 3000, wantTimeout: 180000},
		{name: "global below minimum", channel: 0, global: 999, wantTimeout: 1000},
		{name: "global above maximum", channel: 0, global: 180001, wantTimeout: 180000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveStreamInactivityTimeout(tt.channel, tt.global)
			if got != tt.wantTimeout {
				t.Fatalf("ResolveStreamInactivityTimeout(%d, %d) = %d, want %d", tt.channel, tt.global, got, tt.wantTimeout)
			}
		})
	}
}

func TestProcessStreamEvent_TracksPendingToolCall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx := NewStreamContext(&config.EnvConfig{})
	event := "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_test\",\"name\":\"Bash\",\"input\":{}}}\n\n"

	ProcessStreamEvent(c, c.Writer, nopFlusher{}, event, ctx, &config.EnvConfig{}, nil)

	if ctx.ToolCallTracker == nil || !ctx.ToolCallTracker.HasPendingToolCall() {
		t.Fatalf("expected tool call tracker to mark pending tool call")
	}
}

func TestSummarizeStreamEventForIdleLog_RedactsPayload(t *testing.T) {
	event := "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_test\",\"name\":\"Write\",\"input\":{\"file_path\":\"/tmp/secret-plan.md\"}}}\n\n"

	summary := summarizeStreamEventForIdleLog(event)

	for _, want := range []string{"data_type=content_block_start", "block_type=tool_use", "tool=Write"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary %q should contain %q", summary, want)
		}
	}
	if strings.Contains(summary, "secret-plan") || strings.Contains(summary, "file_path") {
		t.Fatalf("summary %q should not include raw tool arguments", summary)
	}
}

func TestHasSSEFrame_DetectsEmptyKeepaliveFrame(t *testing.T) {
	event := "\n"

	if HasStreamEventActivity(event) {
		t.Fatalf("empty frame should not count as regular stream activity")
	}
	if !HasSSEFrame(event) {
		t.Fatalf("empty frame should still count as an upstream SSE frame for tool-call idle")
	}
	if HasSSEFrame("") {
		t.Fatalf("empty string should not count as an upstream SSE frame")
	}
}

func TestPatchUsageFieldsWithLog_NilInputTokens(t *testing.T) {
	tests := []struct {
		name           string
		usage          map[string]interface{}
		estimatedInput int
		hasCacheTokens bool
		wantPatched    bool
		wantValue      int
	}{
		{
			name:           "nil input_tokens without cache - should patch",
			usage:          map[string]interface{}{"input_tokens": nil, "output_tokens": float64(100)},
			estimatedInput: 10920,
			hasCacheTokens: false,
			wantPatched:    true,
			wantValue:      10920,
		},
		{
			name:           "nil input_tokens with cache - should also patch",
			usage:          map[string]interface{}{"input_tokens": nil, "output_tokens": float64(100)},
			estimatedInput: 10920,
			hasCacheTokens: true,
			wantPatched:    true,
			wantValue:      10920,
		},
		{
			name:           "valid input_tokens - should not patch",
			usage:          map[string]interface{}{"input_tokens": float64(5000), "output_tokens": float64(100)},
			estimatedInput: 10920,
			hasCacheTokens: true,
			wantPatched:    false,
			wantValue:      5000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patchUsageFieldsWithLog(tt.usage, tt.estimatedInput, 100, tt.hasCacheTokens, false, "test", false)

			if tt.wantPatched {
				if v, ok := tt.usage["input_tokens"].(int); !ok || v != tt.wantValue {
					t.Errorf("expected input_tokens=%d, got %v", tt.wantValue, tt.usage["input_tokens"])
				}
			} else if tt.usage["input_tokens"] == nil {
				// nil case - expected to remain nil
			} else if v, ok := tt.usage["input_tokens"].(float64); ok && int(v) != tt.wantValue {
				t.Errorf("expected input_tokens=%d, got %v", tt.wantValue, tt.usage["input_tokens"])
			}
		})
	}
}

func TestPatchMessageStartInputTokensIfNeeded(t *testing.T) {
	requestBody := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello world hello world hello world"}]}]}`)
	estimated := utils.EstimateRequestTokens(requestBody)
	if estimated <= 0 {
		t.Fatalf("expected estimated input tokens > 0, got %d", estimated)
	}

	extractInputTokens := func(t *testing.T, event string) float64 {
		t.Helper()
		for _, line := range strings.Split(event, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data); err != nil {
				t.Fatalf("failed to unmarshal data: %v", err)
			}
			msg, ok := data["message"].(map[string]interface{})
			if !ok {
				t.Fatalf("missing message field")
			}
			usage, ok := msg["usage"].(map[string]interface{})
			if !ok {
				t.Fatalf("missing message.usage field")
			}
			v, ok := usage["input_tokens"].(float64)
			if !ok {
				t.Fatalf("missing input_tokens field")
			}
			return v
		}
		t.Fatalf("no data line found")
		return 0
	}

	t.Run("input_tokens=0 should patch in message_start", func(t *testing.T) {
		event := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n"
		hasUsage, needInputPatch, _, usageData := CheckEventUsageStatus(event, false)
		if !hasUsage {
			t.Fatalf("expected hasUsage=true")
		}
		if !needInputPatch {
			t.Fatalf("expected needInputPatch=true")
		}

		patched := PatchMessageStartInputTokensIfNeeded(event, requestBody, needInputPatch, usageData, false, false)
		got := extractInputTokens(t, patched)
		if got != float64(estimated) {
			t.Fatalf("expected input_tokens=%d, got %v", estimated, got)
		}
	})

	t.Run("input_tokens<10 should patch in message_start", func(t *testing.T) {
		event := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n"
		hasUsage, needInputPatch, _, usageData := CheckEventUsageStatus(event, false)
		if !hasUsage {
			t.Fatalf("expected hasUsage=true")
		}
		if needInputPatch {
			t.Fatalf("expected needInputPatch=false")
		}

		patched := PatchMessageStartInputTokensIfNeeded(event, requestBody, needInputPatch, usageData, false, false)
		got := extractInputTokens(t, patched)
		if got != float64(estimated) {
			t.Fatalf("expected input_tokens=%d, got %v", estimated, got)
		}
	})

	t.Run("cache hit should not patch input_tokens", func(t *testing.T) {
		event := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":0,\"output_tokens\":0,\"cache_read_input_tokens\":100}}}\n\n"
		hasUsage, needInputPatch, _, usageData := CheckEventUsageStatus(event, false)
		if !hasUsage {
			t.Fatalf("expected hasUsage=true")
		}
		if needInputPatch {
			t.Fatalf("expected needInputPatch=false")
		}

		patched := PatchMessageStartInputTokensIfNeeded(event, requestBody, needInputPatch, usageData, false, false)
		got := extractInputTokens(t, patched)
		if got != 0 {
			t.Fatalf("expected input_tokens=0, got %v", got)
		}
	})

	t.Run("valid input_tokens should not patch", func(t *testing.T) {
		event := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":50,\"output_tokens\":0}}}\n\n"
		hasUsage, needInputPatch, _, usageData := CheckEventUsageStatus(event, false)
		if !hasUsage {
			t.Fatalf("expected hasUsage=true")
		}
		if needInputPatch {
			t.Fatalf("expected needInputPatch=false")
		}

		patched := PatchMessageStartInputTokensIfNeeded(event, requestBody, needInputPatch, usageData, false, false)
		got := extractInputTokens(t, patched)
		if got != 50 {
			t.Fatalf("expected input_tokens=50, got %v", got)
		}
	})
}

// TestInferImplicitCacheRead 测试隐式缓存推断逻辑
func TestInferImplicitCacheRead(t *testing.T) {
	tests := []struct {
		name                    string
		messageStartInputTokens int
		collectedInputTokens    int
		existingCacheRead       int
		wantCacheRead           int
	}{
		{
			name:                    "large diff ratio (>10%) should infer cache",
			messageStartInputTokens: 100000,
			collectedInputTokens:    20000,
			existingCacheRead:       0,
			wantCacheRead:           80000,
		},
		{
			name:                    "large diff value (>10k) should infer cache",
			messageStartInputTokens: 50000,
			collectedInputTokens:    38000,
			existingCacheRead:       0,
			wantCacheRead:           12000,
		},
		{
			name:                    "small diff should not infer cache",
			messageStartInputTokens: 10000,
			collectedInputTokens:    9500,
			existingCacheRead:       0,
			wantCacheRead:           0,
		},
		{
			name:                    "existing cache_read should not be overwritten",
			messageStartInputTokens: 100000,
			collectedInputTokens:    20000,
			existingCacheRead:       50000,
			wantCacheRead:           50000,
		},
		{
			name:                    "zero message_start should not infer",
			messageStartInputTokens: 0,
			collectedInputTokens:    20000,
			existingCacheRead:       0,
			wantCacheRead:           0,
		},
		{
			name:                    "zero collected should not infer",
			messageStartInputTokens: 100000,
			collectedInputTokens:    0,
			existingCacheRead:       0,
			wantCacheRead:           0,
		},
		{
			name:                    "negative diff should not infer",
			messageStartInputTokens: 10000,
			collectedInputTokens:    15000,
			existingCacheRead:       0,
			wantCacheRead:           0,
		},
		{
			name:                    "exactly 10% diff should not infer",
			messageStartInputTokens: 10000,
			collectedInputTokens:    9000,
			existingCacheRead:       0,
			wantCacheRead:           0,
		},
		{
			name:                    "just over 10% diff should infer",
			messageStartInputTokens: 10000,
			collectedInputTokens:    8900,
			existingCacheRead:       0,
			wantCacheRead:           1100,
		},
		{
			name:                    "10k diff but ratio <10% should infer (diff > 10k takes precedence)",
			messageStartInputTokens: 150000,
			collectedInputTokens:    139000,
			existingCacheRead:       0,
			wantCacheRead:           11000,
		},
		{
			name:                    "diff exactly 10k with ratio <10% should not infer",
			messageStartInputTokens: 150000,
			collectedInputTokens:    140000,
			existingCacheRead:       0,
			wantCacheRead:           0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &StreamContext{
				MessageStartInputTokens: tt.messageStartInputTokens,
				CollectedUsage: CollectedUsageData{
					InputTokens:          tt.collectedInputTokens,
					CacheReadInputTokens: tt.existingCacheRead,
				},
			}

			inferImplicitCacheRead(ctx, false)

			if ctx.CollectedUsage.CacheReadInputTokens != tt.wantCacheRead {
				t.Errorf("CacheReadInputTokens = %d, want %d",
					ctx.CollectedUsage.CacheReadInputTokens, tt.wantCacheRead)
			}
		})
	}
}

// TestPatchTokensInEventWithCache 测试带缓存推断的事件修补
func TestPatchTokensInEventWithCache(t *testing.T) {
	extractCacheRead := func(t *testing.T, event string) float64 {
		t.Helper()
		for _, line := range strings.Split(event, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var data map[string]interface{}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data); err != nil {
				continue
			}
			if usage, ok := data["usage"].(map[string]interface{}); ok {
				if v, ok := usage["cache_read_input_tokens"].(float64); ok {
					return v
				}
			}
		}
		return 0
	}

	t.Run("should write inferred cache_read when not present", func(t *testing.T) {
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":20000,\"output_tokens\":100}}\n\n"
		patched := PatchTokensInEventWithCache(event, 20000, 100, 80000, true, false, false)
		got := extractCacheRead(t, patched)
		if got != 80000 {
			t.Errorf("expected cache_read_input_tokens=80000, got %v", got)
		}
	})

	t.Run("should not overwrite existing cache_read", func(t *testing.T) {
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":20000,\"output_tokens\":100,\"cache_read_input_tokens\":50000}}\n\n"
		patched := PatchTokensInEventWithCache(event, 20000, 100, 80000, true, false, false)
		got := extractCacheRead(t, patched)
		if got != 50000 {
			t.Errorf("expected cache_read_input_tokens=50000 (unchanged), got %v", got)
		}
	})

	t.Run("should not write when inferredCacheRead is 0", func(t *testing.T) {
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":20000,\"output_tokens\":100}}\n\n"
		patched := PatchTokensInEventWithCache(event, 20000, 100, 0, false, false, false)
		got := extractCacheRead(t, patched)
		if got != 0 {
			t.Errorf("expected cache_read_input_tokens=0, got %v", got)
		}
	})

	t.Run("should not overwrite explicit zero from upstream", func(t *testing.T) {
		// 上游显式返回 cache_read_input_tokens: 0 表示"明确无缓存"，不应被推断值覆盖
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":20000,\"output_tokens\":100,\"cache_read_input_tokens\":0}}\n\n"
		patched := PatchTokensInEventWithCache(event, 20000, 100, 80000, true, false, false)
		got := extractCacheRead(t, patched)
		if got != 0 {
			t.Errorf("expected cache_read_input_tokens=0 (explicit zero preserved), got %v", got)
		}
	})
}

func TestPreflightStreamEvents_ToolUseNotEmpty(t *testing.T) {
	// 模拟纯 tool_use 响应：message_start → content_block_start(tool_use) → ... → message_stop
	// 这类响应没有 delta.text，但不应被判定为空响应
	events := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":478,\"output_tokens\":1}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_test\",\"name\":\"Bash\",\"input\":{}}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"command\\\":\\\"ls\\\"}\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"input_tokens\":2559,\"output_tokens\":23}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}

	eventChan := make(chan string, len(events))
	errChan := make(chan error)
	for _, e := range events {
		eventChan <- e
	}
	close(eventChan)
	close(errChan)

	result := PreflightStreamEvents(eventChan, errChan, StreamPreflightTimeouts{})
	if result.IsEmpty {
		t.Errorf("tool_use response should NOT be detected as empty, got IsEmpty=true (buffered %d events)", len(result.BufferedEvents))
	}
}

func TestStreamPreflightEmptyErrorIncludesDiagnostic(t *testing.T) {
	err := streamPreflightEmptyError(&StreamPreflightResult{
		Diagnostic: "malformed tool call: Write",
	})

	if !errors.Is(err, ErrEmptyStreamResponse) {
		t.Fatalf("expected error to wrap ErrEmptyStreamResponse, got %v", err)
	}
	if !strings.Contains(err.Error(), "malformed tool call: Write") {
		t.Fatalf("expected diagnostic in error, got %q", err.Error())
	}
}

func TestStreamPreflightEmptyErrorWithoutDiagnosticReturnsSentinel(t *testing.T) {
	if err := streamPreflightEmptyError(&StreamPreflightResult{}); err != ErrEmptyStreamResponse {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

func TestPreflightStreamEvents_ThinkingStartOnlyIsEmpty(t *testing.T) {
	// 模拟仅有 thinking start 但没有 thinking delta 的响应
	events := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":100,\"output_tokens\":1}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}

	eventChan := make(chan string, len(events))
	errChan := make(chan error)
	for _, e := range events {
		eventChan <- e
	}
	close(eventChan)
	close(errChan)

	result := PreflightStreamEvents(eventChan, errChan, StreamPreflightTimeouts{})
	if !result.IsEmpty {
		t.Errorf("thinking start-only response should be detected as empty, got IsEmpty=false")
	}
}

func TestPreflightStreamEvents_ThinkingDeltaOnlyIsEmpty(t *testing.T) {
	events := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":100,\"output_tokens\":1}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"analysis\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}

	eventChan := make(chan string, len(events))
	errChan := make(chan error)
	for _, e := range events {
		eventChan <- e
	}
	close(eventChan)
	close(errChan)

	result := PreflightStreamEvents(eventChan, errChan, StreamPreflightTimeouts{})
	if !result.IsEmpty {
		t.Errorf("thinking-only response should be detected as empty, got IsEmpty=false")
	}
}

func TestPreflightStreamEventsWithOptions_ThinkingDeltaOnlyNotEmpty(t *testing.T) {
	events := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"deepseek-v4-flash\",\"usage\":{\"input_tokens\":100,\"output_tokens\":1}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"analysis\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}

	eventChan := make(chan string, len(events))
	errChan := make(chan error)
	for _, e := range events {
		eventChan <- e
	}
	close(eventChan)
	close(errChan)

	result := PreflightStreamEventsWithOptions(eventChan, errChan, StreamPreflightTimeouts{}, StreamPreflightOptions{
		TreatThinkingAsContent: true,
	})
	if result.IsEmpty {
		t.Errorf("thinking-only response should be accepted when enabled, got IsEmpty=true")
	}
}

func TestPreflightStreamEvents_ThinkingThenTextNotEmpty(t *testing.T) {
	events := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":100,\"output_tokens\":1}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"analysis\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"answer\"}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}

	eventChan := make(chan string, len(events))
	errChan := make(chan error)
	for _, e := range events {
		eventChan <- e
	}
	close(eventChan)
	close(errChan)

	result := PreflightStreamEvents(eventChan, errChan, StreamPreflightTimeouts{})
	if result.IsEmpty {
		t.Errorf("thinking + text response should NOT be detected as empty, got IsEmpty=true")
	}
}

func TestPreflightStreamEvents_ShortTextUnexpectedEOFIsRetryableStall(t *testing.T) {
	eventChan := make(chan string)
	errChan := make(chan error)

	go func() {
		eventChan <- "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":0}}}\n\n"
		eventChan <- "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"
		eventChan <- "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"## 新版本 v2.9\"}}\n\n"
		errChan <- errors.New("unexpected EOF")
		close(eventChan)
		close(errChan)
	}()

	result := PreflightStreamEvents(eventChan, errChan, StreamPreflightTimeouts{
		InactivityTimeoutMs: 200,
	})

	if !result.HasError {
		t.Fatal("expected short stream EOF to be returned as a preflight error")
	}
	if !errors.Is(result.Error, ErrStreamStalled) {
		t.Fatalf("expected ErrStreamStalled, got %v", result.Error)
	}
}

func TestPreflightStreamEvents_ShortTextCloseBeforeMessageStopIsRetryableStall(t *testing.T) {
	eventChan := make(chan string, 3)
	errChan := make(chan error)

	eventChan <- "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":0}}}\n\n"
	eventChan <- "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"
	eventChan <- "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"short answer\"}}\n\n"
	close(eventChan)
	close(errChan)

	result := PreflightStreamEvents(eventChan, errChan, StreamPreflightTimeouts{
		InactivityTimeoutMs: 200,
	})

	if !result.HasError {
		t.Fatal("expected short stream close without message_stop to be returned as a preflight error")
	}
	if !errors.Is(result.Error, ErrStreamStalled) {
		t.Fatalf("expected ErrStreamStalled, got %v", result.Error)
	}
}

func TestPreflightStreamEvents_LongTextPassesThresholdBeforeMessageStop(t *testing.T) {
	eventChan := make(chan string, 4)
	errChan := make(chan error)

	eventChan <- "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":0}}}\n\n"
	eventChan <- "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"
	eventChan <- "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"This is a normal streaming answer with enough text to pass the short EOF retry threshold.\"}}\n\n"
	eventChan <- "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" It should be released before message_stop so long streams keep low latency.\"}}\n\n"

	result := PreflightStreamEvents(eventChan, errChan, StreamPreflightTimeouts{
		InactivityTimeoutMs: 200,
	})

	if result.HasError {
		t.Fatalf("expected no preflight error, got %v", result.Error)
	}
	if result.IsEmpty {
		t.Fatal("expected long text stream to be non-empty")
	}
	if got := len(result.BufferedEvents); got != 4 {
		t.Fatalf("BufferedEvents = %d, want 4", got)
	}
	close(eventChan)
	close(errChan)
}

func TestPreflightStreamEvents_TrueEmptyStillDetected(t *testing.T) {
	// 真正的空响应：有 message_start 和 message_stop 但没有任何 content block
	events := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":100,\"output_tokens\":0}}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}

	eventChan := make(chan string, len(events))
	errChan := make(chan error)
	for _, e := range events {
		eventChan <- e
	}
	close(eventChan)
	close(errChan)

	result := PreflightStreamEvents(eventChan, errChan, StreamPreflightTimeouts{})
	if !result.IsEmpty {
		t.Errorf("truly empty response should be detected as empty, got IsEmpty=false")
	}
	if result.Diagnostic == "" {
		t.Fatal("expected diagnostic for empty preflight result")
	}
}

func TestBuildStreamPreflightDetail_IncludesEventShapeAndRedactsSecrets(t *testing.T) {
	preflight := &StreamPreflightResult{
		BufferedEvents: []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":0}}}\n\n",
			"event: error\ndata: {\"type\":\"error\",\"api_key\":\"sk-test-secret-1234567890\",\"error\":{\"message\":\"Authorization: Bearer sk-bearer-secret-1234567890\"}}\n\n",
			"event: done\ndata: [DONE]\n\n",
		},
		IsEmpty:    true,
		Diagnostic: "检测到空流，但未匹配到明确类别",
	}

	detail := buildStreamPreflightDetail(preflight)
	for _, want := range []string{
		"events=3",
		"dataTypes=message_start",
		"topKeys=message,type",
		"usageKeys=message.usage.input_tokens,message.usage.output_tokens",
		"dataTypes=error",
		"jsonParseErrors=1",
		"preview=",
	} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail missing %q:\n%s", want, detail)
		}
	}
	for _, leaked := range []string{"sk-test-secret-1234567890", "sk-bearer-secret-1234567890"} {
		if strings.Contains(detail, leaked) {
			t.Fatalf("detail leaked sensitive value %q:\n%s", leaked, detail)
		}
	}
}

func TestPreflightStreamEvents_ContentBlockStopWithoutPendingStillEmpty(t *testing.T) {
	events := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":100,\"output_tokens\":0}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}

	eventChan := make(chan string, len(events))
	errChan := make(chan error)
	for _, e := range events {
		eventChan <- e
	}
	close(eventChan)
	close(errChan)

	result := PreflightStreamEvents(eventChan, errChan, StreamPreflightTimeouts{})
	if !result.IsEmpty {
		t.Fatalf("text content_block_stop without pending tool call should stay empty, got BufferedEvents=%d", len(result.BufferedEvents))
	}
}

func TestPreflightStreamEvents_UnknownEventTypeRecordedInDiagnostic(t *testing.T) {
	eventChan := make(chan string, 2)
	errChan := make(chan error, 1)

	eventChan <- "event: weird\ndata: {\"type\":\"custom.semantic.delta\",\"foo\":\"bar\"}\n\n"
	close(eventChan)
	close(errChan)

	result := PreflightStreamEvents(eventChan, errChan, StreamPreflightTimeouts{})
	if !result.IsEmpty {
		t.Fatal("expected stream with only unknown event and no semantic content to be empty")
	}
	if result.UnknownEventType != "custom.semantic.delta" {
		t.Fatalf("UnknownEventType = %q, want %q", result.UnknownEventType, "custom.semantic.delta")
	}
	if !strings.Contains(result.Diagnostic, "custom.semantic.delta") {
		t.Fatalf("Diagnostic = %q, want it to mention unknown event type", result.Diagnostic)
	}
}

func TestPreflightStreamEvents_ToolUseStopReasonWithoutContentBlockIsEmpty(t *testing.T) {
	eventChan := make(chan string, 4)
	errChan := make(chan error, 1)

	events := []string{
		"event: message_start\ndata:{\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n",
		"event: message_delta\ndata:{\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}\n\n",
		"event: message_stop\ndata:{\"type\":\"message_stop\"}\n\n",
	}
	for _, e := range events {
		eventChan <- e
	}
	close(eventChan)
	close(errChan)

	result := PreflightStreamEvents(eventChan, errChan, StreamPreflightTimeouts{})
	if !result.IsEmpty {
		t.Fatalf("tool_use stop_reason without content_block should be detected as empty")
	}
}

func TestHasClaudeSemanticContent(t *testing.T) {
	tests := []struct {
		name  string
		event string
		want  bool
	}{
		{
			name:  "tool_use content block",
			event: "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_test\",\"name\":\"Bash\",\"input\":{}}}\n\n",
			want:  true,
		},
		{
			name:  "thinking content block",
			event: "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n",
			want:  false,
		},
		{
			name:  "redacted_thinking content block",
			event: "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"redacted_thinking\",\"thinking\":\"\"}}\n\n",
			want:  false,
		},
		{
			name:  "server_tool_use content block",
			event: "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"server_tool_use\",\"id\":\"srvtoolu_test\",\"name\":\"web_search\"}}\n\n",
			want:  true,
		},
		{
			name:  "text content block - not non-text",
			event: "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
			want:  false,
		},
		{
			name:  "message_start - not content block",
			event: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\"}}\n\n",
			want:  false,
		},
		{
			name:  "content_block_delta input_json_delta counts as non-text",
			event: "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\n",
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasClaudeSemanticContent(tt.event)
			if got != tt.want {
				t.Errorf("HasClaudeSemanticContent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasOpenAIChatSemanticContent(t *testing.T) {
	tests := []struct {
		name  string
		event string
		want  bool
	}{
		{
			name:  "content delta",
			event: `data: {"choices":[{"delta":{"content":"hello"}}]}` + "\n\n",
			want:  true,
		},
		{
			name:  "reasoning delta",
			event: `data: {"choices":[{"delta":{"reasoning_content":"thinking"}}]}` + "\n\n",
			want:  true,
		},
		{
			name:  "vllm reasoning delta",
			event: `data: {"choices":[{"delta":{"reasoning":"thinking"}}]}` + "\n\n",
			want:  true,
		},
		{
			name:  "legacy function call name",
			event: `data: {"choices":[{"delta":{"function_call":{"name":"Read"}}}]}` + "\n\n",
			want:  true,
		},
		{
			name:  "legacy function call arguments",
			event: `data: {"choices":[{"delta":{"function_call":{"arguments":"{}"}}}]}` + "\n\n",
			want:  true,
		},
		{
			name:  "tool calls name",
			event: `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"Read"}}]}}]}` + "\n\n",
			want:  true,
		},
		{
			name:  "tool calls arguments",
			event: `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]}}]}` + "\n\n",
			want:  true,
		},
		{
			name:  "role only",
			event: `data: {"choices":[{"delta":{"role":"assistant"}}]}` + "\n\n",
			want:  false,
		},
		{
			name:  "empty delta",
			event: `data: {"choices":[{"delta":{}}]}` + "\n\n",
			want:  false,
		},
		{
			name:  "done marker",
			event: "data: [DONE]\n\n",
			want:  false,
		},
		{
			name:  "invalid json",
			event: "data: {\n\n",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HasOpenAIChatSemanticContent(tt.event)
			if got != tt.want {
				t.Errorf("HasOpenAIChatSemanticContent() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasClaudeSemanticContent_ToolStopReasonOnly(t *testing.T) {
	tests := []struct {
		name  string
		event string
	}{
		{
			name:  "tool_use stop reason",
			event: `data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null,"stop_details":null}}` + "\n\n",
		},
		{
			name:  "server_tool_use stop reason",
			event: `data: {"type":"message_delta","delta":{"stop_reason":"server_tool_use","stop_sequence":null,"stop_details":null}}` + "\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if HasClaudeSemanticContent(tt.event) {
				t.Fatal("tool stop reason without a tool content block should not be treated as semantic content")
			}
		})
	}
}

func TestEnsureMessageDeltaUsage(t *testing.T) {
	extractUsage := func(t *testing.T, event string) (int, int) {
		t.Helper()
		for _, line := range strings.Split(event, "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			var data map[string]interface{}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data); err != nil {
				t.Fatalf("failed to unmarshal event data: %v", err)
			}
			if data["type"] != "message_delta" {
				continue
			}

			usage, ok := data["usage"].(map[string]interface{})
			if !ok {
				t.Fatalf("usage field missing in message_delta")
			}

			inputTokens, _ := usage["input_tokens"].(float64)
			outputTokens, _ := usage["output_tokens"].(float64)
			return int(inputTokens), int(outputTokens)
		}

		t.Fatalf("message_delta event not found")
		return 0, 0
	}

	t.Run("add usage when missing", func(t *testing.T) {
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n"
		patched := EnsureMessageDeltaUsage(event, 123, 7)

		in, out := extractUsage(t, patched)
		if in != 123 || out != 7 {
			t.Fatalf("expected usage input=123 output=7, got input=%d output=%d", in, out)
		}
	})

	t.Run("keep existing usage", func(t *testing.T) {
		event := "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":10,\"output_tokens\":2}}\n\n"
		patched := EnsureMessageDeltaUsage(event, 999, 888)

		in, out := extractUsage(t, patched)
		if in != 10 || out != 2 {
			t.Fatalf("expected existing usage to be kept, got input=%d output=%d", in, out)
		}
	})
}

func TestProcessStreamEvent_MessageStopInjectsUsageWhenMessageDeltaMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		t.Fatalf("response writer does not implement http.Flusher")
	}

	ctx := &StreamContext{
		ContentBlockTypes: make(map[int]string),
		HasUsage:          true, // message_start 已经有 usage
		LogBuffer:         NewLimitedLogBuffer(MaxUpstreamResponseLogBytes),
	}
	envCfg := &config.EnvConfig{LogLevel: "info"}

	messageStopEvent := "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	ProcessStreamEvent(c, c.Writer, flusher, messageStopEvent, ctx, envCfg, []byte(`{"messages":[{"role":"user","content":"hello"}]}`))

	body := w.Body.String()
	if !strings.Contains(body, "event: message_delta") {
		t.Fatalf("expected injected message_delta usage event before message_stop, body=%s", body)
	}
	if !strings.Contains(body, "\"usage\"") {
		t.Fatalf("expected injected usage field, body=%s", body)
	}
	if !strings.Contains(body, "event: message_stop") {
		t.Fatalf("expected message_stop event to be forwarded, body=%s", body)
	}
}

func TestProcessStreamEvent_PatchedMessageStartDoesNotInferCacheRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{}`))

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		t.Fatalf("response writer does not implement http.Flusher")
	}

	ctx := &StreamContext{
		ContentBlockTypes: make(map[int]string),
		LogBuffer:         NewLimitedLogBuffer(MaxUpstreamResponseLogBytes),
	}
	envCfg := &config.EnvConfig{LogLevel: "info"}
	requestBody := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello world hello world hello world hello world"}]}]}`)

	messageStartEvent := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n"
	ProcessStreamEvent(c, c.Writer, flusher, messageStartEvent, ctx, envCfg, requestBody)

	if ctx.MessageStartInputTokens != 0 {
		t.Fatalf("expected patched message_start estimate not to be recorded for cache inference, got %d", ctx.MessageStartInputTokens)
	}

	messageDeltaEvent := "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"input_tokens\":10473,\"output_tokens\":27}}\n\n"
	ProcessStreamEvent(c, c.Writer, flusher, messageDeltaEvent, ctx, envCfg, requestBody)

	if ctx.CollectedUsage.CacheReadInputTokens != 0 {
		t.Fatalf("expected no inferred cache_read from patched message_start estimate, got %d", ctx.CollectedUsage.CacheReadInputTokens)
	}

	body := w.Body.String()
	if strings.Contains(body, "cache_read_input_tokens") {
		t.Fatalf("expected forwarded stream not to include inferred cache_read_input_tokens, body=%s", body)
	}
}

func TestHasStreamEventActivity(t *testing.T) {
	tests := []struct {
		name  string
		event string
		want  bool
	}{
		{
			name:  "data payload",
			event: "event: response.in_progress\ndata: {\"type\":\"response.in_progress\"}\n\n",
			want:  true,
		},
		{
			name:  "comment keepalive",
			event: ": keepalive\n\n",
			want:  true,
		},
		{
			name:  "done sentinel ignored",
			event: "data: [DONE]\n\n",
			want:  false,
		},
		{
			name:  "blank ignored",
			event: "\n\n",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasStreamEventActivity(tt.event); got != tt.want {
				t.Fatalf("HasStreamEventActivity() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDetectStreamBlacklistError_BalanceMessages(t *testing.T) {
	tests := []struct {
		name        string
		event       string
		wantReason  string
		wantMessage string
	}{
		{
			name: "nested error message semantic balance",
			event: `event: error
data: {"type":"error","error":{"type":"new_api_error","message":"预扣费额度失败, 用户剩余额度: ¥0.053950, 需要预扣费额度: ¥0.191160"}}

`,
			wantReason:  "insufficient_balance",
			wantMessage: "预扣费额度失败, 用户剩余额度: ¥0.053950, 需要预扣费额度: ¥0.191160",
		},
		{
			name: "string error field semantic balance",
			event: `event: error
data: {"type":"error","error":"API Key额度不足，请访问https://right.codes查看详情"}

`,
			wantReason:  "insufficient_balance",
			wantMessage: "API Key额度不足，请访问https://right.codes查看详情",
		},
		{
			name: "top level message semantic balance",
			event: `event: error
data: {"type":"error","message":"API Key额度不足，请访问https://right.codes查看详情"}

`,
			wantReason:  "insufficient_balance",
			wantMessage: "API Key额度不足，请访问https://right.codes查看详情",
		},
		{
			name: "401 nested token status exhausted balance",
			event: `event: error
data: {"type":"error","error":{"type":"new_api_error","message":"该令牌额度已用尽 TokenStatusExhausted[sk-duK***qqX]"}}

`,
			wantReason:  "insufficient_balance",
			wantMessage: "该令牌额度已用尽 TokenStatusExhausted[sk-duK***qqX]",
		},
		{
			name: "401 top level balance exhausted message",
			event: `event: error
data: {"type":"error","message":"账户余额已用尽，请充值"}

`,
			wantReason:  "insufficient_balance",
			wantMessage: "账户余额已用尽，请充值",
		},
		{
			name: "nested daily limit exceeded code",
			event: `event: error
data: {"type":"error","error":{"code":"DAILY_LIMIT_EXCEEDED","message":"daily usage limit exceeded"}}

`,
			wantReason:  "insufficient_balance",
			wantMessage: "daily usage limit exceeded",
		},
		{
			name: "top level usage limit exceeded code",
			event: `event: error
data: {"type":"error","code":"USAGE_LIMIT_EXCEEDED","message":"error: code=429 reason=\"DAILY_LIMIT_EXCEEDED\" message=\"daily usage limit exceeded\" metadata=map[]"}

`,
			wantReason:  "insufficient_balance",
			wantMessage: "error: code=429 reason=\"DAILY_LIMIT_EXCEEDED\" message=\"daily usage limit exceeded\" metadata=map[]",
		},
		{
			name: "nested insufficient user quota code",
			event: `event: error
data: {"type":"error","error":{"message":"用户额度不足, 剩余额度: ¥-0.136964 (request id: 202606221209254492365268268d9d6mwf4XMcd)","type":"new_api_error","param":"","code":"insufficient_user_quota"}}

`,
			wantReason:  "insufficient_balance",
			wantMessage: "用户额度不足, 剩余额度: ¥-0.136964 (request id: 202606221209254492365268268d9d6mwf4XMcd)",
		},
		{
			name: "string error field invalid api key",
			event: `event: error
data: {"type":"error","error":"无效的API Key"}

`,
			wantReason:  "authentication_error",
			wantMessage: "无效的API Key",
		},
		{
			name: "nested permission message without type",
			event: `event: error
data: {"type":"error","error":{"message":"permission denied for this resource"}}

`,
			wantReason:  "permission_error",
			wantMessage: "permission denied for this resource",
		},
		{
			name: "subscription not found code",
			event: `event: error
data: {"type":"error","error":{"code":"SUBSCRIPTION_NOT_FOUND","message":"No active subscription found for this group"}}

`,
			wantReason:  "insufficient_balance",
			wantMessage: "No active subscription found for this group",
		},
		{
			name: "subscription not found message",
			event: `event: error
data: {"type":"error","error":{"message":"No active subscription found for this group"}}

`,
			wantReason:  "insufficient_balance",
			wantMessage: "No active subscription found for this group",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotReason, gotMessage := DetectStreamBlacklistError(tt.event)
			if gotReason != tt.wantReason || gotMessage != tt.wantMessage {
				t.Fatalf("DetectStreamBlacklistError() = (%q, %q), want (%q, %q)", gotReason, gotMessage, tt.wantReason, tt.wantMessage)
			}
		})
	}
}

func TestPreflightStreamEvents_TextThenPendingToolCallReturnsAsNonEmpty(t *testing.T) {
	eventChan := make(chan string, 10)
	errChan := make(chan error, 1)

	e1 := `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0}}}
`
	e2 := `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}
`
	e3 := `event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}
`
	e4 := `event: content_block_stop
data: {"type":"content_block_stop","index":0}
`
	e5 := `event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_test","name":"Read","input":{}}}
`

	events := []string{e1, e2, e3, e4, e5}
	for _, e := range events {
		eventChan <- e
	}

	result := PreflightStreamEvents(eventChan, errChan, StreamPreflightTimeouts{
		InactivityTimeoutMs: 200,
	})

	if result.IsEmpty {
		t.Fatalf("expected IsEmpty=false for stream with text + tool_use, got BufferedEvents=%d", len(result.BufferedEvents))
	}
	if result.HasError {
		t.Fatalf("expected no error because text content already made stream non-empty, got %v", result.Error)
	}
	close(eventChan)
	close(errChan)
}

func TestPreflightStreamEvents_CompletedToolCallWithoutTextIsNotEmpty(t *testing.T) {
	eventChan := make(chan string, 10)
	errChan := make(chan error, 1)

	event1 := `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0}}}
`
	event2 := `event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_calc","name":"calculator","input":{}}}
`

	eventChan <- event1
	eventChan <- event2

	stopEvent := `event: content_block_stop
data: {"type":"content_block_stop","index":0}
`

	eventChan <- stopEvent
	close(eventChan)
	close(errChan)

	result := PreflightStreamEvents(eventChan, errChan, StreamPreflightTimeouts{})
	if result.IsEmpty {
		t.Fatalf("expected completed tool call without text to be non-empty, got BufferedEvents=%d", len(result.BufferedEvents))
	}
	if result.HasError {
		t.Fatalf("expected no error after tool call closed, got %v", result.Error)
	}
}

// TestProcessStreamEvent_TruncatesMalformedToolUse 验证当 text 后跟畸形 tool_use 时，
// 代理层截断畸形 tool_use 并注入 end_turn 终止响应
func TestProcessStreamEvent_TruncatesMalformedToolUse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	envCfg := &config.EnvConfig{}
	ctx := NewStreamContext(envCfg)

	// 1. 先发送一些 text content（模拟正常文本输出）
	textStart := "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"
	textDelta := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello world\"}}\n\n"
	textStop := "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, textStart, ctx, envCfg, nil)
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, textDelta, ctx, envCfg, nil)
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, textStop, ctx, envCfg, nil)

	// 验证 text 已正常透传
	body := rec.Body.String()
	if !strings.Contains(body, "Hello world") {
		t.Fatalf("expected text to be forwarded, got: %s", body)
	}

	// 2. 发送一个畸形 tool_use（Bash 但参数为空）
	toolStart := "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_abc\",\"name\":\"Bash\",\"input\":{}}}\n\n"
	toolStop := "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n"
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, toolStart, ctx, envCfg, nil)
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, toolStop, ctx, envCfg, nil)

	// 3. 验证截断行为
	if !ctx.ToolUseTruncated {
		t.Fatalf("expected ToolUseTruncated to be true")
	}
	if ctx.CommittedToolUseCount != 0 {
		t.Fatalf("expected no committed tool use, got %d", ctx.CommittedToolUseCount)
	}

	// 验证注入了 end_turn
	fullBody := rec.Body.String()
	if !strings.Contains(fullBody, "end_turn") {
		t.Fatalf("expected end_turn in truncated output, got: %s", fullBody)
	}
	if !strings.Contains(fullBody, "message_stop") {
		t.Fatalf("expected message_stop in truncated output, got: %s", fullBody)
	}
	// 确认畸形的 tool_use block 事件没有透传（注：content_block_start 被缓冲了不会出现）
	if strings.Contains(fullBody, "toolu_abc") {
		t.Fatalf("malformed tool_use should not be forwarded, got: %s", fullBody)
	}

	// 4. 后续上游事件不透传
	prevLen := rec.Body.Len()
	messageDelta := "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":100}}\n\n"
	messageStop := "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, messageDelta, ctx, envCfg, nil)
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, messageStop, ctx, envCfg, nil)

	if rec.Body.Len() != prevLen {
		t.Fatalf("post-truncation events should not be forwarded, got additional: %s", rec.Body.String()[prevLen:])
	}
}

// TestProcessStreamEvent_FlushesValidToolUse 验证正常工具调用被正常透传
func TestProcessStreamEvent_FlushesValidToolUse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	envCfg := &config.EnvConfig{}
	ctx := NewStreamContext(envCfg)

	// tool_use with valid non-empty input
	toolStart := "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_valid\",\"name\":\"Bash\",\"input\":{}}}\n\n"
	toolDelta := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"command\\\":\\\"ls\\\"}\"}}\n\n"
	toolStop := "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"

	ProcessStreamEvent(c, c.Writer, nopFlusher{}, toolStart, ctx, envCfg, nil)
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, toolDelta, ctx, envCfg, nil)
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, toolStop, ctx, envCfg, nil)

	if ctx.ToolUseTruncated {
		t.Fatalf("valid tool use should not trigger truncation")
	}
	if ctx.CommittedToolUseCount != 1 {
		t.Fatalf("expected 1 committed tool use, got %d", ctx.CommittedToolUseCount)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "toolu_valid") {
		t.Fatalf("valid tool_use should be forwarded, got: %s", body)
	}
	if !strings.Contains(body, "input_json_delta") {
		t.Fatalf("tool delta should be forwarded, got: %s", body)
	}
}

func TestProcessStreamEvent_FlushesOverlappingToolUseBlocksSequentially(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	envCfg := &config.EnvConfig{}
	ctx := NewStreamContext(envCfg)

	agentStart := "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_agent\",\"name\":\"Agent\",\"input\":{}}}\n\n"
	agentDelta := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"description\\\":\\\"agent command\\\"}\"}}\n\n"
	mcpStart := "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":3,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_mcp\",\"name\":\"mcp__serena__initial_instructions\",\"input\":{}}}\n\n"
	mcpDelta := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":3,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{}\"}}\n\n"
	agentStop := "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":2}\n\n"
	mcpStop := "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":3}\n\n"

	ProcessStreamEvent(c, c.Writer, nopFlusher{}, agentStart, ctx, envCfg, nil)
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, agentDelta, ctx, envCfg, nil)
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, mcpStart, ctx, envCfg, nil)
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, mcpDelta, ctx, envCfg, nil)
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, agentStop, ctx, envCfg, nil)
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, mcpStop, ctx, envCfg, nil)

	if ctx.ToolUseTruncated {
		t.Fatalf("overlapping valid tool_use blocks should not trigger truncation")
	}
	if ctx.CommittedToolUseCount != 2 {
		t.Fatalf("expected 2 committed tool uses, got %d", ctx.CommittedToolUseCount)
	}

	body := rec.Body.String()
	orderedTokens := []string{
		"toolu_agent",
		"agent command",
		"{\"type\":\"content_block_stop\",\"index\":2}",
		"toolu_mcp",
		"mcp__serena__initial_instructions",
		"{\"type\":\"content_block_stop\",\"index\":3}",
	}
	previous := -1
	for _, token := range orderedTokens {
		position := strings.Index(body, token)
		if position < 0 {
			t.Fatalf("expected forwarded body to include %q, got: %s", token, body)
		}
		if position <= previous {
			t.Fatalf("expected %q to appear after previous token, got body: %s", token, body)
		}
		previous = position
	}
}

func TestProcessStreamEvent_LogsBufferedToolUseEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	envCfg := &config.EnvConfig{Env: "development", EnableResponseLogs: true}
	ctx := NewStreamContext(envCfg)

	toolStart := "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_logged\",\"name\":\"Bash\",\"input\":{}}}\n\n"
	toolDelta := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"command\\\":\\\"ls\\\"}\"}}\n\n"
	toolStop := "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"

	ProcessStreamEvent(c, c.Writer, nopFlusher{}, toolStart, ctx, envCfg, nil)
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, toolDelta, ctx, envCfg, nil)
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, toolStop, ctx, envCfg, nil)

	logged := ctx.LogBuffer.String()
	for _, want := range []string{"toolu_logged", "input_json_delta", "content_block_stop"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("expected raw stream log to include %q, got: %s", want, logged)
		}
	}
}

// TestProcessStreamEvent_MalformedAfterValidToolUse 验证有已透传 tool_use 后，畸形的不截断
func TestProcessStreamEvent_MalformedAfterValidToolUse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	envCfg := &config.EnvConfig{}
	ctx := NewStreamContext(envCfg)

	// 先发一个有效 tool_use
	toolStart1 := "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_first\",\"name\":\"Bash\",\"input\":{}}}\n\n"
	toolDelta1 := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"command\\\":\\\"echo hi\\\"}\"}}\n\n"
	toolStop1 := "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, toolStart1, ctx, envCfg, nil)
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, toolDelta1, ctx, envCfg, nil)
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, toolStop1, ctx, envCfg, nil)

	if ctx.CommittedToolUseCount != 1 {
		t.Fatalf("expected 1 committed, got %d", ctx.CommittedToolUseCount)
	}

	// 再发一个畸形 tool_use（参数为空）
	toolStart2 := "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_bad\",\"name\":\"Read\",\"input\":{}}}\n\n"
	toolStop2 := "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n"
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, toolStart2, ctx, envCfg, nil)
	ProcessStreamEvent(c, c.Writer, nopFlusher{}, toolStop2, ctx, envCfg, nil)

	// 不应截断
	if ctx.ToolUseTruncated {
		t.Fatalf("should not truncate when CommittedToolUseCount > 0")
	}
	if ctx.CommittedToolUseCount != 2 {
		t.Fatalf("expected 2 committed, got %d", ctx.CommittedToolUseCount)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "toolu_bad") {
		t.Fatalf("malformed tool_use after valid should still be forwarded, got: %s", body)
	}
}
