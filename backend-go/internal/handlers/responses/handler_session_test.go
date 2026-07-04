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
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

func TestHandleSuccess_PreservesPreviousResponseID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)
	provider := &providers.ResponsesProvider{SessionManager: sessionManager}
	envCfg := &config.EnvConfig{}

	sess, err := sessionManager.GetOrCreateSession("")
	if err != nil {
		t.Fatalf("GetOrCreateSession() err = %v", err)
	}
	if err := sessionManager.UpdateLastResponseID(sess.ID, "resp_prev"); err != nil {
		t.Fatalf("UpdateLastResponseID() err = %v", err)
	}
	sessionManager.RecordResponseMapping("resp_prev", sess.ID)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"id":"resp_new",
			"model":"gpt-5",
			"status":"completed",
			"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],
			"usage":{"input_tokens":12,"output_tokens":8,"total_tokens":20}
		}`)),
	}

	originalReq := &types.ResponsesRequest{
		PreviousResponseID: "resp_prev",
		Input:              "hello",
	}

	if _, err := handleSuccess(c, resp, provider, "responses", envCfg, sessionManager, time.Now(), originalReq, []byte(`{"model":"gpt-5","input":"hello"}`), false, common.StreamPreflightTimeouts{}); err != nil {
		t.Fatalf("handleSuccess() err = %v", err)
	}

	body := w.Body.String()
	if !strings.Contains(body, `"previous_id":"resp_prev"`) {
		t.Fatalf("response body should preserve previous response id, got %s", body)
	}
	if strings.Contains(body, `"previous_id":"resp_new"`) {
		t.Fatalf("response body should not rewrite previous_id to current response id, got %s", body)
	}
}

func TestHandleStreamSuccess_PostCommitActivityResetsIdleWatchdog(t *testing.T) {
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

	writeStream := func(s string) {
		if _, err := io.WriteString(writer, s); err != nil {
			return
		}
	}
	go func() {
		defer writer.Close()
		writeStream("event: response.output_text.delta\n")
		writeStream("data: {\"type\":\"response.output_text.delta\",\"delta\":\"a\"}\n\n")
		writeStream("event: response.output_text.delta\n")
		writeStream("data: {\"type\":\"response.output_text.delta\",\"delta\":\"b\"}\n\n")

		for i := 0; i < 2; i++ {
			time.Sleep(60 * time.Millisecond)
			writeStream("event: response.in_progress\n")
			writeStream("data: {\"type\":\"response.in_progress\"}\n\n")
		}

		time.Sleep(60 * time.Millisecond)
		writeStream("event: response.completed\n")
		writeStream("data: {\"type\":\"response.completed\",\"response\":{\"output\":[]},\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}\n\n")
	}()

	_, err := handleStreamSuccess(
		c,
		resp,
		"responses",
		&config.EnvConfig{LogLevel: "info"},
		nil, // sessionManager
		time.Now(),
		&types.ResponsesRequest{Model: "gpt-5"},
		[]byte(`{"model":"gpt-5","stream":true}`),
		common.StreamPreflightTimeouts{
			FirstContentTimeoutMs: 1000,
			InactivityTimeoutMs:   150,
			ToolCallIdleTimeoutMs: 80,
		},
	)
	if err != nil {
		t.Fatalf("handleStreamSuccess() err = %v", err)
	}
	if !strings.Contains(w.Body.String(), "response.in_progress") {
		t.Fatalf("expected forwarded progress event, got %s", w.Body.String())
	}
}

func TestHandleStreamSuccess_AcceptsLargeSSEDataLine(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5","stream":true}`))

	largeDelta := strings.Repeat("x", 1024*1024+1)
	body := `event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"` + largeDelta + `"}

event: response.completed
data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}

`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	_, err := handleStreamSuccess(
		c,
		resp,
		"responses",
		&config.EnvConfig{LogLevel: "info"},
		nil, // sessionManager
		time.Now(),
		&types.ResponsesRequest{Model: "gpt-5"},
		[]byte(`{"model":"gpt-5","stream":true}`),
		common.StreamPreflightTimeouts{},
	)
	if err != nil {
		t.Fatalf("handleStreamSuccess() err = %v", err)
	}
	if !strings.Contains(w.Body.String(), `"type":"response.completed"`) {
		t.Fatalf("expected completed event, got body length %d", w.Body.Len())
	}
	if !strings.Contains(w.Body.String(), largeDelta) {
		t.Fatalf("expected large delta to be forwarded, got body length %d", w.Body.Len())
	}
}

func TestHandleStreamSuccess_RetryableResponsesErrorPreflightTriggersFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5","stream":true}`))

	body := `event: response.created
data: {"type":"response.created","response":{"id":"resp_1","status":"in_progress"}}

event: response.in_progress
data: {"type":"response.in_progress","response":{"id":"resp_1","status":"in_progress"}}

event: error
data: {"type":"error","error":{"type":"service_unavailable_error","code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}

event: response.failed
data: {"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}}

`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	_, err := handleStreamSuccess(
		c,
		resp,
		"responses",
		&config.EnvConfig{LogLevel: "info"},
		nil, // sessionManager
		time.Now(),
		&types.ResponsesRequest{Model: "gpt-5"},
		[]byte(`{"model":"gpt-5","stream":true}`),
		common.StreamPreflightTimeouts{},
	)
	if !errors.Is(err, common.ErrEmptyStreamResponse) {
		t.Fatalf("handleStreamSuccess() err = %v, want ErrEmptyStreamResponse", err)
	}
	if !strings.Contains(err.Error(), "server_is_overloaded") {
		t.Fatalf("expected diagnostic to include server_is_overloaded, got %v", err)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("preflight error should not commit response body, got %q", w.Body.String())
	}
}

func TestHandleStreamSuccess_ResponseFailedQuotaTriggersBlacklist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5","stream":true}`))

	body := `event: response.failed
data: {"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"insufficient_quota","message":"insufficient account balance"}}}

`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	_, err := handleStreamSuccess(
		c,
		resp,
		"responses",
		&config.EnvConfig{LogLevel: "info"},
		nil, // sessionManager
		time.Now(),
		&types.ResponsesRequest{Model: "gpt-5"},
		[]byte(`{"model":"gpt-5","stream":true}`),
		common.StreamPreflightTimeouts{},
	)
	var blacklistErr *common.ErrBlacklistKey
	if !errors.As(err, &blacklistErr) {
		t.Fatalf("handleStreamSuccess() err = %v, want ErrBlacklistKey", err)
	}
	if blacklistErr.Reason != "insufficient_balance" {
		t.Fatalf("blacklist reason = %q, want insufficient_balance", blacklistErr.Reason)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("preflight error should not commit response body, got %q", w.Body.String())
	}
}
