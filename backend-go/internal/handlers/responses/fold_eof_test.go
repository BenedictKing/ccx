package responses

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/providers"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/gin-gonic/gin"
)

func TestRunResponsesFoldRetriesEOFBeforeContent(t *testing.T) {
	baseBody := map[string]interface{}{
		"model":  "gpt-5.5",
		"stream": true,
		"input":  "hello",
	}
	opens := 0
	result, err := runResponsesFold(
		baseBody,
		responsesFoldTestResp(responsesFoldLifecycleOnly()),
		func(body map[string]interface{}) (*http.Response, []byte, error) {
			opens++
			if body["model"] != baseBody["model"] || body["input"] != baseBody["input"] {
				t.Fatalf("retry body changed: %#v", body)
			}
			return responsesFoldTestResp(responsesFoldReasoningRound("rs_retry", 20, "OK", true)), nil, nil
		},
		func(event map[string]interface{}) error { return nil },
	)
	if err != nil {
		t.Fatalf("runResponsesFold() err = %v", err)
	}
	if opens != 1 || result.EOFRetries != 1 {
		t.Fatalf("opens = %d, retries = %d, want 1 and 1", opens, result.EOFRetries)
	}
	if result.IncompleteReason != "" {
		t.Fatalf("IncompleteReason = %q, want empty", result.IncompleteReason)
	}
}

func TestRunResponsesFoldRetriesCurrentContinuationBody(t *testing.T) {
	baseBody := map[string]interface{}{"model": "gpt-5.5", "stream": true, "input": "hello"}
	var openedBodies []map[string]interface{}
	result, err := runResponsesFold(
		baseBody,
		responsesFoldTestResp(responsesFoldReasoningRound("rs_1", responsesFoldStep-2, "FIRST", true)),
		func(body map[string]interface{}) (*http.Response, []byte, error) {
			openedBodies = append(openedBodies, cloneFoldTestMap(body))
			if len(openedBodies) == 1 {
				return responsesFoldTestResp(responsesFoldLifecycleOnly()), nil, nil
			}
			return responsesFoldTestResp(responsesFoldReasoningRound("rs_2", 20, "SECOND", true)), nil, nil
		},
		func(event map[string]interface{}) error { return nil },
	)
	if err != nil {
		t.Fatalf("runResponsesFold() err = %v", err)
	}
	if len(openedBodies) != 2 {
		t.Fatalf("continuation opens = %d, want 2", len(openedBodies))
	}
	if !reflect.DeepEqual(openedBodies[0], openedBodies[1]) {
		t.Fatalf("EOF retry did not reuse continuation body:\nfirst: %#v\nretry: %#v", openedBodies[0], openedBodies[1])
	}
	if result.EOFRetries != 1 || result.IncompleteReason != "" {
		t.Fatalf("result = %#v, want one successful EOF retry", result)
	}
}

func TestRunResponsesFoldRetriesEOFOnlyOnce(t *testing.T) {
	opens := 0
	var emitted []map[string]interface{}
	result, err := runResponsesFold(
		map[string]interface{}{"model": "gpt-5.5", "stream": true, "input": "hello"},
		responsesFoldTestResp(responsesFoldLifecycleOnly()),
		func(body map[string]interface{}) (*http.Response, []byte, error) {
			opens++
			return responsesFoldTestResp(responsesFoldLifecycleOnly()), nil, nil
		},
		func(event map[string]interface{}) error {
			emitted = append(emitted, event)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("runResponsesFold() err = %v", err)
	}
	if opens != 1 || result.EOFRetries != 1 {
		t.Fatalf("opens = %d, retries = %d, want 1 and 1", opens, result.EOFRetries)
	}
	if result.IncompleteReason != "upstream_eof" {
		t.Fatalf("IncompleteReason = %q, want upstream_eof", result.IncompleteReason)
	}
	created := 0
	for _, event := range emitted {
		if event["type"] == "response.created" {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("response.created count = %d, want 1", created)
	}
}

func TestRunResponsesFoldDoesNotRetryEOFAfterReasoning(t *testing.T) {
	opens := 0
	result, err := runResponsesFold(
		map[string]interface{}{"model": "gpt-5.5", "stream": true, "input": "hello"},
		responsesFoldTestResp(responsesFoldReasoningThenEOF()),
		func(body map[string]interface{}) (*http.Response, []byte, error) {
			opens++
			return nil, nil, nil
		},
		func(event map[string]interface{}) error { return nil },
	)
	if err != nil {
		t.Fatalf("runResponsesFold() err = %v", err)
	}
	if opens != 0 {
		t.Fatalf("opens = %d, want no replay after downstream content", opens)
	}
	if result.IncompleteReason != "upstream_eof" {
		t.Fatalf("IncompleteReason = %q, want upstream_eof", result.IncompleteReason)
	}
}

func TestHandleFoldedResponsesStreamSuccessReportsCommittedEOF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true}`))

	_, err := handleFoldedResponsesStreamSuccess(
		c,
		responsesFoldTestResp(responsesFoldReasoningThenEOF()),
		&providers.ResponsesProvider{},
		&config.UpstreamConfig{},
		"test-key",
		&config.EnvConfig{},
		nil,
		&types.ResponsesRequest{Model: "gpt-5.5", Input: "hello", Stream: true},
		[]byte(`{"model":"gpt-5.5","stream":true,"input":"hello"}`),
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "upstream_eof") {
		t.Fatalf("handleFoldedResponsesStreamSuccess() err = %v, want upstream_eof failure", err)
	}
	if !strings.Contains(w.Body.String(), `"reason":"upstream_eof"`) {
		t.Fatalf("client response missing upstream_eof: %s", w.Body.String())
	}
}

func responsesFoldLifecycleOnly() string {
	return formatResponsesFoldSSE(map[string]interface{}{
		"type":     "response.created",
		"response": map[string]interface{}{"id": "resp_1", "status": "in_progress"},
	}) + formatResponsesFoldSSE(map[string]interface{}{
		"type":     "response.in_progress",
		"response": map[string]interface{}{"id": "resp_1", "status": "in_progress"},
	})
}

func responsesFoldReasoningThenEOF() string {
	item := map[string]interface{}{"id": "rs_1", "type": "reasoning", "summary": []interface{}{}}
	return responsesFoldLifecycleOnly() + formatResponsesFoldSSE(map[string]interface{}{
		"type": "response.output_item.added", "output_index": 0, "item": item,
	}) + formatResponsesFoldSSE(map[string]interface{}{
		"type": "response.output_item.done", "output_index": 0, "item": item,
	})
}

func TestRunResponsesFoldEOFRetryLifecycle(t *testing.T) {
	createdOnly := formatResponsesFoldSSE(map[string]interface{}{
		"type":     "response.created",
		"response": map[string]interface{}{"id": "resp_1", "status": "in_progress"},
	})
	tests := []struct {
		name           string
		first          string
		wantInProgress int
	}{
		{name: "首次响应完全为空时补发生命周期", first: "", wantInProgress: 1},
		{name: "首次响应已发出生命周期时不重复", first: responsesFoldLifecycleOnly(), wantInProgress: 1},
		// 首次只给出 created：重试轮的生命周期事件属于另一个上游响应，整体抑制以保持 response.id 一致。
		{name: "首次响应仅有 created 时不混入重试轮事件", first: createdOnly, wantInProgress: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var emitted []string
			result, err := runResponsesFold(
				map[string]interface{}{"model": "gpt-5.5", "stream": true, "input": "hello"},
				responsesFoldTestResp(tt.first),
				func(body map[string]interface{}) (*http.Response, []byte, error) {
					return responsesFoldTestResp(responsesFoldReasoningRound("rs_retry", 20, "OK", true)), nil, nil
				},
				func(event map[string]interface{}) error {
					emitted = append(emitted, stringFromInterface(event["type"]))
					return nil
				},
			)
			if err != nil {
				t.Fatalf("runResponsesFold() err = %v", err)
			}
			if result.EOFRetries != 1 || result.IncompleteReason != "" {
				t.Fatalf("result = %#v, want one successful EOF retry", result)
			}
			created, inProgress := 0, 0
			for _, eventType := range emitted {
				switch eventType {
				case "response.created":
					created++
				case "response.in_progress":
					inProgress++
				}
			}
			if len(emitted) == 0 || emitted[0] != "response.created" {
				t.Fatalf("first emitted event = %v, want response.created", emitted)
			}
			if created != 1 {
				t.Fatalf("response.created count = %d, want 1 (emitted: %v)", created, emitted)
			}
			if inProgress != tt.wantInProgress {
				t.Fatalf("response.in_progress count = %d, want %d (emitted: %v)", inProgress, tt.wantInProgress, emitted)
			}
			if emitted[len(emitted)-1] != "response.completed" {
				t.Fatalf("last emitted event = %s, want response.completed", emitted[len(emitted)-1])
			}
		})
	}
}
