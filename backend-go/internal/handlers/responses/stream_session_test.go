package responses

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/thinkingcache"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

func TestHandleStreamSuccess_SessionWritebackPreservesReasoningEncryptedContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5","stream":true}`))

	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)

	reader, writer := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       reader,
	}

	go func() {
		defer writer.Close()
		writeSSE := func(s string) { io.WriteString(writer, s) }

		// reasoning output_item (passthrough Responses 流格式)
		writeSSE("event: response.output_item.added\n")
		writeSSE("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"reasoning\"}}\n\n")

		writeSSE("event: response.reasoning_summary_text.delta\n")
		writeSSE("data: {\"type\":\"response.reasoning_summary_text.delta\",\"output_index\":0,\"summary_index\":0,\"text\":\"Let me think...\"}\n\n")

		writeSSE("event: response.output_item.done\n")
		writeSSE("data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"id\":\"rs_abc123\",\"status\":\"completed\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"Let me think...\"}],\"encrypted_content\":\"ENCRYPTED_BLOB_XYZ\"}}\n\n")

		// message output_item
		writeSSE("event: response.output_item.added\n")
		writeSSE("data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"message\",\"role\":\"assistant\"}}\n\n")

		writeSSE("event: response.output_text.delta\n")
		writeSSE("data: {\"type\":\"response.output_text.delta\",\"output_index\":1,\"content_index\":0,\"delta\":\"Hello!\"}\n\n")

		writeSSE("event: response.output_item.done\n")
		writeSSE("data: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello!\"}]}}\n\n")

		// completed
		writeSSE("event: response.completed\n")
		writeSSE("data: {\"type\":\"response.completed\",\"sequence_number\":5,\"response\":{\"id\":\"resp_test_001\",\"status\":\"completed\",\"output\":[]},\"usage\":{\"input_tokens\":10,\"output_tokens\":20}}\n\n")
	}()

	originalReq := &types.ResponsesRequest{
		Model:  "gpt-5",
		Input:  "hello",
		Stream: true,
	}

	usage, err := handleStreamSuccess(
		c, resp, "responses",
		&config.EnvConfig{LogLevel: "info"},
		sessionManager,
		time.Now(),
		originalReq,
		[]byte(`{"model":"gpt-5","stream":true}`),
		common.StreamPreflightTimeouts{FirstContentTimeoutMs: 5000, InactivityTimeoutMs: 3000},
	)
	if err != nil {
		t.Fatalf("handleStreamSuccess() err = %v", err)
	}
	_ = usage

	// 验证 session 已写入（通过 responseID 映射查找）
	sess, err := sessionManager.GetSessionByResponseID("resp_test_001")
	if err != nil {
		t.Fatalf("GetSessionByResponseID(resp_test_001) err = %v", err)
	}

	var foundReasoning bool
	var foundMessage bool
	var encContent string
	for _, item := range sess.Messages {
		switch {
		case item.Type == "reasoning":
			foundReasoning = true
			encContent = item.EncryptedContent
		case item.Type == "message" && item.Role == "assistant":
			foundMessage = true
		}
	}
	if !foundReasoning {
		t.Fatalf("session 应包含 reasoning item，当前 messages: %+v", sess.Messages)
	}
	if encContent != "ENCRYPTED_BLOB_XYZ" {
		t.Fatalf("reasoning encrypted_content 应被完整保留，期望 %q，实际 %q", "ENCRYPTED_BLOB_XYZ", encContent)
	}
	if !foundMessage {
		t.Fatalf("session 应包含 assistant message，当前 messages: %+v", sess.Messages)
	}
}

func TestHandleStreamSuccess_PostCommitStallSynthesizesIncompleteEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5","stream":true}`))

	reader, writer := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       reader,
	}

	go func() {
		defer writer.Close()
		writeSSE := func(s string) { io.WriteString(writer, s) }

		// 发送一段内容以通过预检
		writeSSE("event: response.output_item.added\n")
		writeSSE("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"role\":\"assistant\"}}\n\n")
		writeSSE("event: response.output_text.delta\n")
		writeSSE("data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"partial\"}\n\n")
		writeSSE("event: response.output_item.done\n")
		writeSSE("data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"partial\"}]}}\n\n")

		// 然后上游沉默（触发 post-commit stall）
		// 保持连接打开，不发送更多数据；stall 由 idle watchdog 触发
		time.Sleep(5 * time.Second)
	}()

	originalReq := &types.ResponsesRequest{
		Model:  "gpt-5",
		Input:  "hello",
		Stream: true,
	}

	_, err := handleStreamSuccess(
		c, resp, "responses",
		&config.EnvConfig{LogLevel: "info"},
		nil, // 不关心 session 回写（此测试聚焦 stall 终端事件）
		time.Now(),
		originalReq,
		[]byte(`{"model":"gpt-5","stream":true}`),
		common.StreamPreflightTimeouts{
			FirstContentTimeoutMs: 5000,
			InactivityTimeoutMs:   500, // 短超时以快速触发 stall
		},
	)

	// handleStreamSuccess 应返回 ErrStreamPostCommitStalled
	if !errors.Is(err, common.ErrStreamPostCommitStalled) {
		t.Fatalf("期望 ErrStreamPostCommitStalled，实际 err = %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, "response.incomplete") {
		t.Fatalf("post-commit stall 应合成 response.incomplete 事件，实际输出:\n%s", body)
	}
	if !strings.Contains(body, "stream_stalled") {
		t.Fatalf("response.incomplete 应包含 incomplete_details.reason=stream_stalled，实际输出:\n%s", body)
	}
}

func TestHandleStreamSuccess_StoresReasoningEncryptedContentToThinkingCache(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5","stream":true}`))

	thinkingcache.ResetForTest()
	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)

	reader, writer := io.Pipe()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       reader,
	}

	go func() {
		defer writer.Close()
		writeSSE := func(s string) { io.WriteString(writer, s) }

		writeSSE("event: response.output_item.added\n")
		writeSSE("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"reasoning\"}}\n\n")
		writeSSE("event: response.reasoning_summary_text.delta\n")
		writeSSE("data: {\"type\":\"response.reasoning_summary_text.delta\",\"output_index\":0,\"summary_index\":0,\"text\":\"thinking\"}\n\n")
		writeSSE("event: response.output_item.done\n")
		writeSSE("data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"id\":\"rs_cache_test\",\"encrypted_content\":\"CACHED_ENCRYPTED_BLOB\"}}\n\n")

		writeSSE("event: response.output_item.added\n")
		writeSSE("data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"message\",\"role\":\"assistant\"}}\n\n")
		writeSSE("event: response.output_text.delta\n")
		writeSSE("data: {\"type\":\"response.output_text.delta\",\"output_index\":1,\"content_index\":0,\"delta\":\"answer\"}\n\n")
		writeSSE("event: response.output_item.done\n")
		writeSSE("data: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"answer\"}]}}\n\n")

		writeSSE("event: response.completed\n")
		writeSSE("data: {\"type\":\"response.completed\",\"sequence_number\":5,\"response\":{\"id\":\"resp_cache_test\",\"status\":\"completed\",\"output\":[]},\"usage\":{\"input_tokens\":5,\"output_tokens\":5}}\n\n")
	}()

	originalReq := &types.ResponsesRequest{
		Model:  "gpt-5",
		Input:  "hello",
		Stream: true,
	}

	if _, err := handleStreamSuccess(
		c, resp, "responses",
		&config.EnvConfig{LogLevel: "info"},
		sessionManager,
		time.Now(),
		originalReq,
		[]byte(`{"model":"gpt-5","stream":true}`),
		common.StreamPreflightTimeouts{FirstContentTimeoutMs: 5000, InactivityTimeoutMs: 3000},
	); err != nil {
		t.Fatalf("handleStreamSuccess() err = %v", err)
	}

	// 通过 responseID 找到 session，验证 reasoning encrypted_content 已存入 thinkingcache
	sess, err := sessionManager.GetSessionByResponseID("resp_cache_test")
	if err != nil {
		t.Fatalf("GetSessionByResponseID() err = %v", err)
	}

	got, ok := thinkingcache.LookupResponsesReasoning(sess.ID, "rs_cache_test")
	if !ok {
		t.Fatal("期望 thinkingcache 命中 rs_cache_test 的 encrypted_content")
	}
	if got != "CACHED_ENCRYPTED_BLOB" {
		t.Fatalf("thinkingcache 中的 encrypted_content = %q, want CACHED_ENCRYPTED_BLOB", got)
	}
}
