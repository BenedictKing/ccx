package chat

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/handlers/common"
)

type preflightResult struct {
	preflight *chatStreamPreflight
	err       error
}

func newPreflightPipe(t *testing.T, upstreamType string) (*io.PipeWriter, <-chan preflightResult) {
	t.Helper()

	reader, writer := io.Pipe()
	resp := &http.Response{Body: reader}
	resultCh := make(chan preflightResult, 1)
	go func() {
		preflight, _, _, err := preflightChatStream(resp, upstreamType, common.StreamPreflightTimeouts{
			FirstContentTimeoutMs: 10,
			InactivityTimeoutMs:   250,
		})
		resultCh <- preflightResult{preflight: preflight, err: err}
	}()
	t.Cleanup(func() {
		_ = writer.Close()
		_ = reader.Close()
	})

	return writer, resultCh
}

func writeSSELine(t *testing.T, writer *io.PipeWriter, jsonData string) {
	t.Helper()
	if _, err := writer.Write([]byte("data: " + jsonData + "\n\n")); err != nil {
		t.Fatalf("write SSE line: %v", err)
	}
}

func waitForPreflight(t *testing.T, resultCh <-chan preflightResult, timeout time.Duration) preflightResult {
	t.Helper()
	select {
	case result := <-resultCh:
		return result
	case <-time.After(timeout):
		t.Fatalf("preflight did not return within %s", timeout)
	}
	return preflightResult{}
}

func assertPreflightNotDone(t *testing.T, resultCh <-chan preflightResult, timeout time.Duration) {
	t.Helper()
	select {
	case result := <-resultCh:
		t.Fatalf("preflight returned early: err=%v preflight=%+v", result.err, result.preflight)
	case <-time.After(timeout):
	}
}

func assertNoFirstContentTimeout(t *testing.T, result preflightResult) {
	t.Helper()
	if errors.Is(result.err, common.ErrStreamFirstContentTimeout) {
		t.Fatalf("unexpected first content timeout: %v", result.err)
	}
	if result.err != nil {
		t.Fatalf("preflight returned unexpected error: %v", result.err)
	}
	if result.preflight == nil {
		t.Fatal("expected preflight result")
	}
	if result.preflight.malformedToolName != "" {
		t.Fatalf("unexpected malformed tool call: %s", result.preflight.malformedToolName)
	}
}

func TestPreflightChatStream_OpenAIToolCallsAvoidsFirstContentTimeout(t *testing.T) {
	writer, resultCh := newPreflightPipe(t, "openai")

	writeSSELine(t, writer, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"Read"}}]}}]}`)
	time.Sleep(20 * time.Millisecond)
	assertPreflightNotDone(t, resultCh, 20*time.Millisecond)

	writeSSELine(t, writer, `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"file_path\":\"a"}}]}}]}`)
	writeSSELine(t, writer, `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":".go\"}"}}]}}]}`)
	writeSSELine(t, writer, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)

	result := waitForPreflight(t, resultCh, 200*time.Millisecond)
	assertNoFirstContentTimeout(t, result)
	if !strings.Contains(string(result.preflight.buffered), `"tool_calls"`) {
		t.Fatalf("expected buffered tool_calls stream, got %q", string(result.preflight.buffered))
	}
}

func TestPreflightChatStream_OpenAIFunctionCallCompletesBeforeEOF(t *testing.T) {
	writer, resultCh := newPreflightPipe(t, "openai")

	writeSSELine(t, writer, `{"choices":[{"delta":{"function_call":{"name":"Read"}}}]}`)
	time.Sleep(20 * time.Millisecond)
	assertPreflightNotDone(t, resultCh, 20*time.Millisecond)

	writeSSELine(t, writer, `{"choices":[{"delta":{"function_call":{"arguments":"{\"file_path\":\"a.go\"}"}}}]}`)
	writeSSELine(t, writer, `{"choices":[{"delta":{},"finish_reason":"function_call"}]}`)

	result := waitForPreflight(t, resultCh, 200*time.Millisecond)
	assertNoFirstContentTimeout(t, result)
	if !strings.Contains(string(result.preflight.buffered), `"function_call"`) {
		t.Fatalf("expected buffered function_call stream, got %q", string(result.preflight.buffered))
	}
}

func TestPreflightChatStream_ClaudeToolUseAvoidsFirstContentTimeout(t *testing.T) {
	writer, resultCh := newPreflightPipe(t, "claude")

	writeSSELine(t, writer, `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"Read"}}`)
	time.Sleep(20 * time.Millisecond)
	assertPreflightNotDone(t, resultCh, 20*time.Millisecond)

	writeSSELine(t, writer, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\""}}`)
	writeSSELine(t, writer, `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":":\"a.go\"}"}}`)
	writeSSELine(t, writer, `{"type":"content_block_stop","index":0}`)

	result := waitForPreflight(t, resultCh, 200*time.Millisecond)
	assertNoFirstContentTimeout(t, result)
	if !strings.Contains(string(result.preflight.buffered), `"tool_use"`) {
		t.Fatalf("expected buffered Claude tool_use stream, got %q", string(result.preflight.buffered))
	}
}

func TestPreflightChatStream_ResponsesFunctionCallAvoidsFirstContentTimeout(t *testing.T) {
	writer, resultCh := newPreflightPipe(t, "responses")

	writeSSELine(t, writer, `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","name":"Read","call_id":"call_1"}}`)
	time.Sleep(20 * time.Millisecond)
	assertPreflightNotDone(t, resultCh, 20*time.Millisecond)

	writeSSELine(t, writer, `{"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"file_path\":\"a.go\"}"}`)
	writeSSELine(t, writer, `{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","name":"Read","call_id":"call_1","arguments":"{\"file_path\":\"a.go\"}"}}`)

	result := waitForPreflight(t, resultCh, 200*time.Millisecond)
	assertNoFirstContentTimeout(t, result)
	if !strings.Contains(string(result.preflight.buffered), `"function_call"`) {
		t.Fatalf("expected buffered Responses function_call stream, got %q", string(result.preflight.buffered))
	}
}

func TestPreflightChatStream_DoesNotReleaseIncompleteToolCall(t *testing.T) {
	writer, resultCh := newPreflightPipe(t, "openai")

	writeSSELine(t, writer, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"Read"}}]}}]}`)
	writeSSELine(t, writer, `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"file_path\":\"a"}}]}}]}`)
	assertPreflightNotDone(t, resultCh, 40*time.Millisecond)

	writeSSELine(t, writer, `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":".go\"}"}}]}}]}`)
	writeSSELine(t, writer, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)

	result := waitForPreflight(t, resultCh, 200*time.Millisecond)
	assertNoFirstContentTimeout(t, result)
}

func TestPreflightChatStream_MalformedToolCallStillDetected(t *testing.T) {
	writer, resultCh := newPreflightPipe(t, "openai")

	writeSSELine(t, writer, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"Read"}}]}}]}`)
	writeSSELine(t, writer, `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"file_path\":"}}]}}]}`)
	writeSSELine(t, writer, `{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`)

	result := waitForPreflight(t, resultCh, 200*time.Millisecond)
	if result.err != nil {
		t.Fatalf("preflight returned unexpected error: %v", result.err)
	}
	if result.preflight == nil || result.preflight.malformedToolName != "Read" {
		t.Fatalf("expected malformed Read tool call, got %+v", result.preflight)
	}
}

// TestPreflightChatStream_StreamDataErrorTriggersBlacklist 流内错误对象中的认证
// 错误应在预检阶段被识别为拉黑条件，进入统一 failover（此前会原样透传给客户端）。
func TestPreflightChatStream_StreamDataErrorTriggersBlacklist(t *testing.T) {
	writer, resultCh := newPreflightPipe(t, "openai")

	writeSSELine(t, writer, `{"error":{"message":"Invalid API key provided","type":"invalid_api_key"}}`)

	result := waitForPreflight(t, resultCh, 300*time.Millisecond)
	var blErr *common.ErrBlacklistKey
	if !errors.As(result.err, &blErr) {
		t.Fatalf("expected ErrBlacklistKey, got %v", result.err)
	}
	if blErr.Reason != "authentication_error" {
		t.Fatalf("reason = %q, want authentication_error", blErr.Reason)
	}
}

// TestPreflightChatStream_StreamDataErrorNonBlacklistFailsOver 非拉黑类流内错误
// 应按空响应语义进入 failover，而不是透传。
func TestPreflightChatStream_StreamDataErrorNonBlacklistFailsOver(t *testing.T) {
	writer, resultCh := newPreflightPipe(t, "openai")

	writeSSELine(t, writer, `{"error":{"code":503,"message":"The model is overloaded"}}`)

	result := waitForPreflight(t, resultCh, 300*time.Millisecond)
	if !errors.Is(result.err, common.ErrEmptyStreamResponse) {
		t.Fatalf("expected ErrEmptyStreamResponse, got %v", result.err)
	}
}

// TestPreflightChatStream_EmptyStreamAtEOFTriggersFailover usage/role-only 流在
// 流结束时仍未出现语义内容：按空响应进入 failover（对齐 messages/responses）。
func TestPreflightChatStream_EmptyStreamAtEOFTriggersFailover(t *testing.T) {
	writer, resultCh := newPreflightPipe(t, "openai")

	writeSSELine(t, writer, `{"choices":[{"delta":{"role":"assistant"}}]}`)
	writeSSELine(t, writer, `{"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"total_tokens":3}}`)
	writeSSELine(t, writer, `[DONE]`)
	_ = writer.Close()

	result := waitForPreflight(t, resultCh, 300*time.Millisecond)
	if !errors.Is(result.err, common.ErrEmptyStreamResponse) {
		t.Fatalf("expected ErrEmptyStreamResponse, got %v", result.err)
	}
}

// TestPreflightChatStream_ContentStreamStillPasses 正常内容流不受错误检测影响。
func TestPreflightChatStream_ContentStreamStillPasses(t *testing.T) {
	writer, resultCh := newPreflightPipe(t, "openai")

	writeSSELine(t, writer, `{"choices":[{"delta":{"content":"hello world this is fine"}}]}`)
	time.Sleep(20 * time.Millisecond)
	writeSSELine(t, writer, `{"choices":[{"delta":{"content":"more content"}}]}`)

	result := waitForPreflight(t, resultCh, 300*time.Millisecond)
	assertNoFirstContentTimeout(t, result)
}
