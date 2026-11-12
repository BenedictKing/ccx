package providers

import (
	"context"
	"encoding/json"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestNativeResponsesPreservesIncrementalToolsAndClearsAttemptContext(t *testing.T) {
	const raw = `{"model":"test","input":[{"type":"message","role":"user","content":"hello"},{"type":"additional_tools","role":"developer","tools":[{"type":"namespace","name":"ns","tools":[{"type":"custom","name":"exec"}]}]}]}`
	c := newGinContext(http.MethodPost, "/v1/responses", []byte(raw), context.Background())
	p := &ResponsesProvider{}
	compat := &config.UpstreamConfig{BaseURL: "https://example.com", ServiceType: "openai"}
	if _, _, err := p.buildProviderRequestBody(c, "/v1/responses", []byte(raw), compat); err != nil {
		t.Fatal(err)
	}
	body, _, err := p.buildProviderRequestBody(c, "/v1/responses", []byte(raw), &config.UpstreamConfig{BaseURL: "https://api.openai.com", ServiceType: "responses"})
	if err != nil {
		t.Fatal(err)
	}
	var original map[string]interface{}
	_ = json.Unmarshal([]byte(raw), &original)
	if !reflect.DeepEqual(body.(map[string]interface{})["input"], original["input"]) {
		t.Errorf("native input prefix rewritten: %#v", body)
	}
	if hoisted, _ := c.Get("codex_additional_tools_hoisted"); hoisted == true {
		t.Error("previous attempt remap context survived")
	}
}

func TestAdditionalToolsSurvivePreviousResponseID(t *testing.T) {
	sm := session.NewSessionManager(time.Hour, 100, 100000)
	sess, err := sm.GetOrCreateSession("")
	if err != nil {
		t.Fatal(err)
	}
	tools := []interface{}{map[string]interface{}{"type": "custom", "name": "exec"}}
	_ = sm.AppendMessage(sess.ID, types.ResponsesItem{Type: "additional_tools", Role: "developer", Tools: tools}, 0)
	_ = sm.AppendMessage(sess.ID, types.ResponsesItem{Type: "custom_tool_call", Name: "exec", CallID: "c1", Input: "print(1)"}, 0)
	sm.RecordResponseMapping("resp1", sess.ID)
	const raw = `{"model":"test","previous_response_id":"resp1","input":[{"type":"custom_tool_call_output","call_id":"c1","output":"1"}]}`
	c := newGinContext(http.MethodPost, "/v1/responses", []byte(raw), context.Background())
	body, _, err := (&ResponsesProvider{SessionManager: sm}).buildProviderRequestBody(c, "/v1/responses", []byte(raw), &config.UpstreamConfig{BaseURL: "https://example.com", ServiceType: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	if body.(map[string]interface{})["tools"] == nil {
		t.Error("follow-up lost earlier additional_tools declarations")
	}
	if sess.Messages[0].Type != "additional_tools" || sess.Messages[1].Type != "custom_tool_call" {
		t.Error("stored history was mutated")
	}
}
