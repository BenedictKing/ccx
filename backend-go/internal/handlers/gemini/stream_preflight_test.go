package gemini

import (
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/handlers/common"
)

func newGeminiPreflightPipe(t *testing.T, upstreamType string) (*io.PipeWriter, <-chan error) {
	t.Helper()

	reader, writer := io.Pipe()
	resp := &http.Response{Body: reader}
	resultCh := make(chan error, 1)
	go func() {
		_, _, _, err := preflightGeminiStream(resp, upstreamType, common.StreamPreflightTimeouts{
			FirstContentTimeoutMs: 10,
			InactivityTimeoutMs:   250,
		})
		resultCh <- err
	}()
	t.Cleanup(func() {
		_ = writer.Close()
		_ = reader.Close()
	})

	return writer, resultCh
}

func writeGeminiSSELine(t *testing.T, writer *io.PipeWriter, jsonData string) {
	t.Helper()
	if _, err := writer.Write([]byte("data: " + jsonData + "\n\n")); err != nil {
		t.Fatalf("write SSE line: %v", err)
	}
}

func waitForGeminiPreflight(t *testing.T, resultCh <-chan error, timeout time.Duration) error {
	t.Helper()
	select {
	case err := <-resultCh:
		return err
	case <-time.After(timeout):
		t.Fatalf("preflight did not return within %s", timeout)
	}
	return nil
}

// TestPreflightGeminiStream_DataErrorTriggersBlacklist 流内错误对象中的认证
// 错误应在预检阶段被识别为拉黑条件（此前 HTTP 200 + 错误体会原样透传）。
func TestPreflightGeminiStream_DataErrorTriggersBlacklist(t *testing.T) {
	writer, resultCh := newGeminiPreflightPipe(t, "openai")

	writeGeminiSSELine(t, writer, `{"error":{"code":401,"message":"Invalid API key provided"}}`)

	err := waitForGeminiPreflight(t, resultCh, 300*time.Millisecond)
	var blErr *common.ErrBlacklistKey
	if !errors.As(err, &blErr) {
		t.Fatalf("expected ErrBlacklistKey, got %v", err)
	}
	if blErr.Reason != "authentication_error" {
		t.Fatalf("reason = %q, want authentication_error", blErr.Reason)
	}
}

// TestPreflightGeminiStream_DataErrorNonBlacklistFailsOver 非拉黑类流内错误按
// 空响应语义进入 failover。
func TestPreflightGeminiStream_DataErrorNonBlacklistFailsOver(t *testing.T) {
	writer, resultCh := newGeminiPreflightPipe(t, "openai")

	writeGeminiSSELine(t, writer, `{"error":{"code":503,"message":"The model is overloaded","status":"UNAVAILABLE"}}`)

	err := waitForGeminiPreflight(t, resultCh, 300*time.Millisecond)
	if !errors.Is(err, common.ErrEmptyStreamResponse) {
		t.Fatalf("expected ErrEmptyStreamResponse, got %v", err)
	}
}

// TestPreflightGeminiStream_EmptyStreamAtEOFTriggersFailover usage-only 流在流结束
// 时仍未出现语义内容：按空响应进入 failover（此前任意非空行都会放行）。
func TestPreflightGeminiStream_EmptyStreamAtEOFTriggersFailover(t *testing.T) {
	writer, resultCh := newGeminiPreflightPipe(t, "gemini")

	writeGeminiSSELine(t, writer, `{"candidates":[{"content":{"parts":[{"role":"model"}]},"finishReason":"STOP"}],"usageMetadata":{"totalTokenCount":5}}`)
	_ = writer.Close()

	err := waitForGeminiPreflight(t, resultCh, 300*time.Millisecond)
	if !errors.Is(err, common.ErrEmptyStreamResponse) {
		t.Fatalf("expected ErrEmptyStreamResponse, got %v", err)
	}
}

// TestPreflightGeminiStream_ContentStreamStillPasses 正常内容流不受错误检测影响。
func TestPreflightGeminiStream_ContentStreamStillPasses(t *testing.T) {
	writer, resultCh := newGeminiPreflightPipe(t, "gemini")

	writeGeminiSSELine(t, writer, `{"candidates":[{"content":{"parts":[{"text":"hello world"}]}}]}`)
	writeGeminiSSELine(t, writer, `{"candidates":[{"content":{"parts":[{"text":"more content"}]}}]}`)

	err := waitForGeminiPreflight(t, resultCh, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("preflight returned unexpected error: %v", err)
	}
}
