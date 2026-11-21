package common

import (
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/types"
)

func TestRecordChannelLog_TruncatesAndMasks(t *testing.T) {
	store := metrics.NewChannelLogStore()
	longError := strings.Repeat("x", 260)

	RecordChannelLog(
		store,
		"test-metrics-key",
		3,
		"model-a",
		"model-orig",
		502,
		123,
		false,
		"sk-test-very-secret",
		"https://example.com",
		longError,
		"Responses",
		true,
	)

	logs := store.Get("test-metrics-key")
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}

	got := logs[0]
	if got.Model != "model-a" {
		t.Fatalf("model = %q, want model-a", got.Model)
	}
	if got.OriginalModel != "model-orig" {
		t.Fatalf("originalModel = %q, want model-orig", got.OriginalModel)
	}
	if got.StatusCode != 502 {
		t.Fatalf("statusCode = %d, want 502", got.StatusCode)
	}
	if got.DurationMs != 123 {
		t.Fatalf("durationMs = %d, want 123", got.DurationMs)
	}
	if got.Success {
		t.Fatalf("success = true, want false")
	}
	if got.BaseURL != "https://example.com" {
		t.Fatalf("baseURL = %q, want https://example.com", got.BaseURL)
	}
	if got.InterfaceType != "Responses" {
		t.Fatalf("interfaceType = %q, want Responses", got.InterfaceType)
	}
	if got.RequestSource != metrics.RequestSourceProxy {
		t.Fatalf("requestSource = %q, want %q", got.RequestSource, metrics.RequestSourceProxy)
	}
	if !got.IsRetry {
		t.Fatalf("isRetry = false, want true")
	}
	if len(got.ErrorInfo) != 200 {
		t.Fatalf("errorInfo len = %d, want 200", len(got.ErrorInfo))
	}
	if got.KeyMask == "sk-test-very-secret" || got.KeyMask == "" {
		t.Fatalf("keyMask = %q, want masked non-empty value", got.KeyMask)
	}
}

func TestRecordChannelLogWithSource_UsesExplicitSource(t *testing.T) {
	store := metrics.NewChannelLogStore()

	RecordChannelLogWithSource(
		store,
		"test-metrics-key-2",
		1,
		"model-b",
		"",
		200,
		45,
		true,
		"sk-test-another-secret",
		"https://example.com",
		"",
		"Messages",
		false,
		metrics.RequestSourceCapabilityTest,
	)

	logs := store.Get("test-metrics-key-2")
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}
	if logs[0].RequestSource != metrics.RequestSourceCapabilityTest {
		t.Fatalf("requestSource = %q, want %q", logs[0].RequestSource, metrics.RequestSourceCapabilityTest)
	}
}

func TestCreatePendingLog_WithSelectionTrace(t *testing.T) {
	store := metrics.NewChannelLogStore()

	CreatePendingLog(
		store,
		"test-metrics-key-selection",
		1,
		"trace-channel",
		"model-a",
		"",
		"",
		"",
		"sk-test-secret",
		"https://example.com",
		"Messages",
		"",
		metrics.RequestSourceProxy,
		nil,
		"",
		WithChannelSelectionTrace("priority_order", "stages=active_model_filter:1 selected=1:trace-channel/priority_order"),
	)

	logs := store.Get("test-metrics-key-selection")
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}
	if logs[0].SelectionReason != "priority_order" {
		t.Fatalf("selectionReason = %q, want priority_order", logs[0].SelectionReason)
	}
	if !strings.Contains(logs[0].SelectionTraceSummary, "selected=1:trace-channel/priority_order") {
		t.Fatalf("selectionTraceSummary = %q, want selected channel summary", logs[0].SelectionTraceSummary)
	}
}

func TestCompleteLog_MapsClientCanceledToCancelledStatus(t *testing.T) {
	store := metrics.NewChannelLogStore()
	requestID := CreatePendingLog(store, "test-metrics-key-3", 0, "test-channel", "model-a", "", "", "", "sk-test-secret", "https://example.com", "Responses", "edits", metrics.RequestSourceProxy, nil, "")

	CompleteLog(store, "test-metrics-key-3", requestID, 200, false, "client canceled", false)

	logs := store.Get("test-metrics-key-3")
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}
	if logs[0].Operation != "edits" {
		t.Fatalf("operation = %q, want edits", logs[0].Operation)
	}
	if logs[0].Status != metrics.StatusCancelled {
		t.Fatalf("status = %q, want %q", logs[0].Status, metrics.StatusCancelled)
	}
	if logs[0].Success {
		t.Fatalf("success = true, want false")
	}
}

func TestCompleteLog_LeavesRealFailuresAsFailed(t *testing.T) {
	store := metrics.NewChannelLogStore()
	requestID := CreatePendingLog(store, "test-metrics-key-4", 0, "test-channel", "model-a", "", "", "", "sk-test-secret", "https://example.com", "Responses", "", metrics.RequestSourceProxy, nil, "")

	CompleteLog(store, "test-metrics-key-4", requestID, 502, false, "upstream timeout", false)

	logs := store.Get("test-metrics-key-4")
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}
	if logs[0].Status != metrics.StatusFailed {
		t.Fatalf("status = %q, want %q", logs[0].Status, metrics.StatusFailed)
	}
}

func TestCompleteLog_NormalizesEmptyStreamErrorInfo(t *testing.T) {
	store := metrics.NewChannelLogStore()
	requestID := CreatePendingLog(store, "test-metrics-key-5", 0, "test-channel", "model-a", "", "", "", "sk-test-secret", "https://example.com", "Messages", "", metrics.RequestSourceProxy, nil, "")

	CompleteLog(store, "test-metrics-key-5", requestID, 200, false, "upstream returned empty stream response: 检测到空流，但未匹配到明确类别", false)

	logs := store.Get("test-metrics-key-5")
	if len(logs) != 1 {
		t.Fatalf("logs count = %d, want 1", len(logs))
	}
	if !strings.HasPrefix(logs[0].ErrorInfo, "空流响应：") {
		t.Fatalf("errorInfo = %q, want empty stream display text", logs[0].ErrorInfo)
	}
	if strings.Contains(logs[0].ErrorInfo, "断流") {
		t.Fatalf("errorInfo = %q, should not classify empty stream as stalled stream", logs[0].ErrorInfo)
	}
	if !strings.Contains(logs[0].ErrorInfo, "检测到空流") {
		t.Fatalf("errorInfo = %q, want diagnostic preserved", logs[0].ErrorInfo)
	}
}

func TestApplyChannelLogUsage(t *testing.T) {
	firstByteAt := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	logEntry := &metrics.ChannelLog{FirstByteAt: &firstByteAt}
	usage := &types.Usage{
		InputTokens:                100,
		OutputTokens:               40,
		PromptTokensTotal:          180,
		CacheReadInputTokens:       80,
		CacheCreation5mInputTokens: 20,
	}

	applyChannelLogUsage(logEntry, usage, firstByteAt.Add(2*time.Second))

	if logEntry.InputTokens != 100 {
		t.Fatalf("inputTokens = %d, want 100", logEntry.InputTokens)
	}
	if logEntry.OutputTokens != 40 {
		t.Fatalf("outputTokens = %d, want 40", logEntry.OutputTokens)
	}
	if logEntry.CacheCreationTokens != 20 {
		t.Fatalf("cacheCreationTokens = %d, want 20", logEntry.CacheCreationTokens)
	}
	if logEntry.CacheReadTokens != 80 {
		t.Fatalf("cacheReadTokens = %d, want 80", logEntry.CacheReadTokens)
	}
	if logEntry.ContextTokens != 200 {
		t.Fatalf("contextTokens = %d, want 200", logEntry.ContextTokens)
	}
	if logEntry.TokensPerSecond != 20 {
		t.Fatalf("tokensPerSecond = %v, want 20", logEntry.TokensPerSecond)
	}
}

func TestApplyChannelLogUsage_WithoutFirstByteOmitsTPS(t *testing.T) {
	logEntry := &metrics.ChannelLog{}
	applyChannelLogUsage(logEntry, &types.Usage{InputTokens: 10, OutputTokens: 40}, time.Now())

	if logEntry.ContextTokens != 10 {
		t.Fatalf("contextTokens = %d, want 10", logEntry.ContextTokens)
	}
	if logEntry.TokensPerSecond != 0 {
		t.Fatalf("tokensPerSecond = %v, want omitted", logEntry.TokensPerSecond)
	}
}

func TestCompleteLog_PreservesContextEstimateSeparatelyFromUsage(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage *types.Usage
		want  int
	}{
		{name: "missing usage"},
		{name: "empty usage", usage: &types.Usage{}},
		{name: "reported usage", usage: &types.Usage{InputTokens: 100, CacheReadInputTokens: 80}, want: 180},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := metrics.NewChannelLogStore()
			requestID := CreatePendingLog(store, "context-estimate", 0, "test", "model", "", "", "", "sk-test", "https://example.com", "Responses", "", metrics.RequestSourceProxy, nil, "", WithEstimatedContextTokens(120000))
			CompleteLog(store, "context-estimate", requestID, 400, false, "context window exceeded", true, tc.usage)
			log := store.Get("context-estimate")[0]
			if log.EstimatedContextTokens != 120000 || log.ContextTokens != tc.want {
				t.Fatalf("estimated/reported context = %d/%d, want 120000/%d", log.EstimatedContextTokens, log.ContextTokens, tc.want)
			}
			if log.InputTokens != 0 && tc.usage == nil {
				t.Fatalf("missing usage must not record estimated tokens as input usage: %+v", log)
			}
		})
	}
}
