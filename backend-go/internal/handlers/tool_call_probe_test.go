package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// sseHandler 返回固定 SSE 响应的上游桩。
func sseHandler(t *testing.T, status int, payload string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		if _, err := w.Write([]byte(payload)); err != nil {
			t.Errorf("写 SSE 响应失败: %v", err)
		}
	}))
}

// messages 强制 tool_choice 探针返回工具调用 → Supported=true。
func TestRunCapabilityToolCallProbeSupported(t *testing.T) {
	server := sseHandler(t, http.StatusOK,
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"t1\",\"name\":\"ccx_probe\",\"input\":{}}}\n\n"+
			"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n")
	defer server.Close()

	channel := &config.UpstreamConfig{BaseURL: server.URL, ServiceType: "claude"}
	summary := runCapabilityToolCallProbe(context.Background(), channel, "messages", "probe-model", "sk-test")

	if !summary.Tested || !summary.Supported {
		t.Fatalf("Supported = %v (Tested=%v, evidence=%s), want true", summary.Supported, summary.Tested, summary.Evidence)
	}
}

// messages 探针返回有效文本但无工具调用 → Supported=false（唯一可学习结论）。
func TestRunCapabilityToolCallProbeUnsupported(t *testing.T) {
	server := sseHandler(t, http.StatusOK,
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"I cannot call tools.\"}}\n\n")
	defer server.Close()

	channel := &config.UpstreamConfig{BaseURL: server.URL, ServiceType: "claude"}
	summary := runCapabilityToolCallProbe(context.Background(), channel, "messages", "probe-model", "sk-test")

	if !summary.Tested || summary.Supported {
		t.Fatalf("Supported = %v (Tested=%v), want false", summary.Supported, summary.Tested)
	}
}

// 非 2xx → inconclusive，不学习。
func TestRunCapabilityToolCallProbeUpstreamErrorInconclusive(t *testing.T) {
	server := sseHandler(t, http.StatusServiceUnavailable, `{"error":{"message":"overloaded"}}`)
	defer server.Close()

	channel := &config.UpstreamConfig{BaseURL: server.URL, ServiceType: "claude"}
	summary := runCapabilityToolCallProbe(context.Background(), channel, "messages", "probe-model", "sk-test")

	if !summary.Tested || summary.Supported {
		t.Fatalf("Supported = %v (Tested=%v), want inconclusive (Tested=true, Supported=false)", summary.Supported, summary.Tested)
	}
}

// 不支持的协议 → Tested=false，不发送任何请求。
func TestRunCapabilityToolCallProbeUnsupportedProtocol(t *testing.T) {
	summary := runCapabilityToolCallProbe(context.Background(), &config.UpstreamConfig{BaseURL: "http://127.0.0.1:1"}, "vectors", "m", "k")
	if summary.Tested {
		t.Fatal("vectors 协议不应执行工具探针")
	}
}

// recordToolCallProbeResult：仅 Tested && !Supported 落库，且写入共享缓存。
func TestRecordToolCallProbeResult(t *testing.T) {
	restore := config.SwapSharedChannelCompatCacheForTest(config.NewChannelCompatCache())
	defer restore()

	channel := &config.UpstreamConfig{ChannelUID: "ch_probe", Name: "probe"}
	unsupported := ToolCallProbeSummary{Tested: true, Supported: false, ConfirmedUnsupported: true, Evidence: "有效内容但无工具调用"}
	recordToolCallProbeResult(channel, "sk-test", "fake-model", unsupported)

	cache := config.SharedChannelCompatCache()
	if !cache.IsToolCallUnsupportedForChannelModel("ch_probe", "fake-model") {
		t.Fatal("不支持结论应写入共享兼容性记忆")
	}

	// Supported=true 与 inconclusive 不落库
	supported := ToolCallProbeSummary{Tested: true, Supported: true}
	recordToolCallProbeResult(channel, "sk-test", "ok-model", supported)
	inconclusive := ToolCallProbeSummary{Tested: true, Supported: false, Error: "timeout"}
	recordToolCallProbeResult(channel, "sk-test", "timeout-model", inconclusive)
	if cache.IsToolCallUnsupportedForChannelModel("ch_probe", "ok-model") {
		t.Fatal("支持结论不应落库")
	}
	if cache.IsToolCallUnsupportedForChannelModel("ch_probe", "timeout-model") {
		t.Fatal("inconclusive 结论不应落库")
	}
}
