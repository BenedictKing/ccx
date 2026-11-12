package responses

import (
	"encoding/json"
	"errors"
	"fmt"
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

	if _, err := handleSuccess(c, resp, provider, nil, "", "responses", envCfg, sessionManager, time.Now(), originalReq, []byte(`{"model":"gpt-5","input":"hello"}`), false, common.StreamPreflightTimeouts{}); err != nil {
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
		}, false,
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
		common.StreamPreflightTimeouts{}, false,
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
		common.StreamPreflightTimeouts{}, false,
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
		common.StreamPreflightTimeouts{}, false,
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

func TestResponsesErrorFailoverWithoutWhitelist(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, eventType := range []string{"error", "response.error", "response.failed"} {
		for _, blacklisted := range []bool{false, true} {
			for _, folded := range []bool{false, true} {
				name := fmt.Sprintf("%s/blacklist=%v/fold=%v", eventType, blacklisted, folded)
				t.Run(name, func(t *testing.T) {
					w := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(w)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					code, message := "unrecognized_upstream_error", "unexpected upstream failure"
					if blacklisted {
						code, message = "invalid_api_key", "Invalid API key"
					}
					errObj := map[string]interface{}{"code": code, "message": message}
					if blacklisted {
						errObj["type"] = "invalid_request_error"
					}
					event := map[string]interface{}{"type": eventType, "error": errObj}
					if eventType == "response.failed" {
						delete(event, "error")
						event["response"] = map[string]interface{}{"error": errObj, "status": "failed"}
					}
					payload, err := json.Marshal(event)
					if err != nil {
						t.Fatal(err)
					}
					resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("event: " + eventType + "\ndata: " + string(payload) + "\n\n"))}
					defer resp.Body.Close()
					if folded {
						emitter := newResponsesFoldHTTPEmitter(c, resp, nil, &types.ResponsesRequest{Model: "gpt-5"})
						err = emitter.emit(event)
					} else {
						_, err = handleStreamSuccess(c, resp, "responses", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{Model: "gpt-5"}, nil, common.StreamPreflightTimeouts{}, false)
					}
					var blacklist *common.ErrBlacklistKey
					if blacklisted {
						var rejected *common.UpstreamRequestRejectedError
						if !errors.As(err, &blacklist) || errors.As(err, &rejected) || errors.Is(err, common.ErrEmptyStreamResponse) {
							t.Fatalf("want blacklist error, got %v", err)
						}
					} else if !errors.Is(err, common.ErrEmptyStreamResponse) || !strings.Contains(err.Error(), code) {
						t.Fatalf("want failover with diagnostic, got %v", err)
					}
					if c.Writer.Written() || w.Body.Len() != 0 {
						t.Fatal("preflight error committed response")
					}
				})
			}
		}
	}
}

func TestResponsesRequestRejectionIsTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, eventType := range []string{"error", "response.error", "response.failed"} {
		for _, code := range []string{"invalid_request_error", "context_length_exceeded"} {
			for _, field := range []string{"type", "code"} {
				for _, folded := range []bool{false, true} {
					name := fmt.Sprintf("%s/%s/%s/fold=%v", eventType, field, code, folded)
					t.Run(name, func(t *testing.T) {
						w := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(w)
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
						message := "request rejected"
						errObj := map[string]interface{}{field: code, "message": message}
						event := map[string]interface{}{"type": eventType, "error": errObj}
						if eventType == "response.failed" {
							delete(event, "error")
							event["response"] = map[string]interface{}{"error": errObj, "status": "failed"}
						}
						payload, err := json.Marshal(event)
						if err != nil {
							t.Fatal(err)
						}
						resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("event: " + eventType + "\ndata: " + string(payload) + "\n\n"))}
						defer resp.Body.Close()
						if folded {
							emitter := newResponsesFoldHTTPEmitter(c, resp, nil, &types.ResponsesRequest{Model: "gpt-5"})
							err = emitter.emit(event)
						} else {
							_, err = handleStreamSuccess(c, resp, "responses", &config.EnvConfig{}, nil, time.Now(), &types.ResponsesRequest{Model: "gpt-5"}, nil, common.StreamPreflightTimeouts{}, false)
						}
						var rejected *common.UpstreamRequestRejectedError
						var blacklist *common.ErrBlacklistKey
						if !errors.As(err, &rejected) || errors.Is(err, common.ErrEmptyStreamResponse) || errors.As(err, &blacklist) {
							t.Fatalf("want terminal request rejection, got %v", err)
						}
						if rejected.Message != message || (field == "type" && rejected.Type != code) || (field == "code" && rejected.Code != code) {
							t.Fatalf("request rejection lost error fields: %#v", rejected)
						}
						if c.Writer.Written() || w.Body.Len() != 0 {
							t.Fatal("preflight error committed response")
						}
					})
				}
			}
		}
	}
}

func TestFoldCommittedErrorDoesNotTriggerFailover(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	emitter := newResponsesFoldHTTPEmitter(c, &http.Response{Header: make(http.Header)}, nil, nil)
	if err := emitter.emit(map[string]interface{}{"type": "response.output_text.delta", "delta": "hello"}); err != nil {
		t.Fatal(err)
	}
	if !emitter.committed {
		t.Fatal("expected committed response")
	}
	if err := emitter.emit(map[string]interface{}{"type": "response.error", "error": map[string]interface{}{"message": "unexpected upstream failure"}}); err != nil {
		t.Fatalf("committed error should be forwarded: %v", err)
	}
	if !strings.Contains(w.Body.String(), "response.error") {
		t.Fatal("missing forwarded error")
	}
}
