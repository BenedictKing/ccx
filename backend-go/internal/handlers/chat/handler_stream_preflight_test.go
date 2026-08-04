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

// TestPreflightChatStream_FlushRemainder_NoByteDuplication 确保未完整的尾行不会被重复缓冲。
func TestPreflightChatStream_FlushRemainder_NoByteDuplication(t *testing.T) {
	writer, resultCh := newPreflightPipe(t, "openai")

	// 完整语义行 + 紧随其后的不完整行（无 \n\n 结尾），触发 remainder 非空
	chunkA := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"wr"
	chunkB := "ite\"}}]}" // 第二块续写，仍未闭合 \n\n，remainder 保持非空

	if _, err := writer.Write([]byte(chunkA)); err != nil {
		t.Fatalf("write chunkA: %v", err)
	}
	if _, err := writer.Write([]byte(chunkB)); err != nil {
		t.Fatalf("write chunkB: %v", err)
	}
	// 关闭 writer 触发 EOF，覆盖尾行清理路径。
	_ = writer.Close()

	result := waitForPreflight(t, resultCh, 300*time.Millisecond)
	assertNoFirstContentTimeout(t, result)

	want := chunkA + chunkB
	got := string(result.preflight.buffered)
	if got != want {
		t.Fatalf("buffered 字节不一致（说明重复写入或丢字节）:\n  want=%q\n  got =%q", want, got)
	}
}
