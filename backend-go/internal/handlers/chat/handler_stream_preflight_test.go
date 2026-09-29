package chat

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/gin-gonic/gin"
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

func TestHandleStreamSuccess_RefusalOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	refusal := `data: {"choices":[{"index":0,"delta":{"content":null,"refusal":"I cannot help with that request."}}]}` + "\n\n"
	tests := []struct {
		name      string
		sse       string
		wantEmpty bool
	}{
		{name: "refusal at EOF", sse: refusal},
		{
			name: "refusal with DONE",
			sse: refusal + `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
				"data: [DONE]\n\n",
		},
		{
			name:      "empty refusal",
			sse:       `data: {"choices":[{"delta":{"content":null,"refusal":""}}]}` + "\n\n",
			wantEmpty: true,
		},
		{
			name:      "blank refusal",
			sse:       `data: {"choices":[{"delta":{"content":null,"refusal":" \t\n"}}]}` + "\n\n",
			wantEmpty: true,
		},
		{
			name:      "null refusal",
			sse:       `data: {"choices":[{"delta":{"content":null,"refusal":null}}]}` + "\n\n",
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5","stream":true}`))
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(tt.sse)),
			}
			t.Cleanup(func() { _ = resp.Body.Close() })

			_, err := handleStreamSuccess(c, resp, "openai", &config.EnvConfig{LogLevel: "error"}, time.Now(), "gpt-5", common.StreamPreflightTimeouts{
				FirstContentTimeoutMs: 1000,
				InactivityTimeoutMs:   1000,
			})
			if tt.wantEmpty {
				if !errors.Is(err, common.ErrEmptyStreamResponse) {
					t.Fatalf("expected empty stream error, got %v", err)
				}
				if c.Writer.Written() || w.Body.Len() != 0 {
					t.Fatal("empty refusal must not commit a client response")
				}
				return
			}
			if err != nil {
				t.Fatalf("refusal must pass preflight, got %v", err)
			}
			if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/event-stream" {
				t.Fatalf("expected SSE success, got status=%d headers=%v", w.Code, w.Header())
			}
			if got := w.Body.String(); got != tt.sse {
				t.Fatalf("refusal stream changed: got %q, want %q", got, tt.sse)
			}
		})
	}
}

// TestHandleStreamSuccess_PassthroughUnterminatedTail 透传流以未终结 data 行收尾时只补齐
// SSE 终止符、不重发尾行正文（重发会被客户端 SSE 解析器拼到同一行，产生尾部重复输出）。
// 覆盖尾行整体落在 preflight 预读缓冲与透传阶段跨 chunk 拼装两种送达方式。
func TestHandleStreamSuccess_PassthroughUnterminatedTail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	content := `data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}` + "\n\n"
	finish := `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"
	doneNoNewline := `data: [DONE]`
	finishNoNewline := `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	truncated := `data: {"choices":[{"index":0,"delta":{"content":"hi"}}`

	tests := []struct {
		name string
		body io.Reader
		want string
	}{
		{
			name: "DONE tail entirely in preflight buffer",
			body: strings.NewReader(content + finish + doneNoNewline),
			want: content + finish + doneNoNewline + "\n\n",
		},
		{
			name: "JSON tail entirely in preflight buffer",
			body: strings.NewReader(content + finish + finishNoNewline),
			want: content + finish + finishNoNewline + "\n\n",
		},
		{
			name: "DONE tail across one-byte chunks",
			body: iotest.OneByteReader(strings.NewReader(content + finish + doneNoNewline)),
			want: content + finish + doneNoNewline + "\n\n",
		},
		{
			name: "JSON tail across one-byte chunks",
			body: iotest.OneByteReader(strings.NewReader(content + finish + finishNoNewline)),
			want: content + finish + finishNoNewline + "\n\n",
		},
		{
			// 截断的 JSON 尾行不是完整事件：不补终止符，也不重发正文。
			name: "truncated JSON tail gets no terminator",
			body: strings.NewReader(content + truncated),
			want: content + truncated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-5","stream":true}`))
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(tt.body),
			}
			t.Cleanup(func() { _ = resp.Body.Close() })

			_, err := handleStreamSuccess(c, resp, "openai", &config.EnvConfig{LogLevel: "error"}, time.Now(), "gpt-5", common.StreamPreflightTimeouts{
				FirstContentTimeoutMs: 1000,
				InactivityTimeoutMs:   1000,
			})
			if err != nil {
				t.Fatalf("stream failed: %v", err)
			}
			if got := w.Body.String(); got != tt.want {
				t.Fatalf("client output mismatch:\ngot:  %q\nwant: %q", got, tt.want)
			}
		})
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
