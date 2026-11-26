package images

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

const qwenGenerationsBody = `{"model":"qwen-image-plus","input":{"messages":[{"role":"user","content":[{"text":"一只猫"}]}]},"parameters":{"size":"1024*1024","n":1}}`

func TestParseImagesRequestSchemas(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name      string
		body      string
		wantOK    bool
		wantModel string
		wantError string
	}{
		{name: "openai", body: `{"model":"gpt-image-1","prompt":"hello"}`, wantOK: true, wantModel: "gpt-image-1"},
		{name: "openai_missing_model", body: `{"prompt":"hello"}`, wantError: "model is required"},
		{name: "openai_missing_prompt", body: `{"model":"gpt-image-1"}`, wantError: "prompt is required"},
		{name: "native_input_messages", body: qwenGenerationsBody, wantOK: true, wantModel: "qwen-image-plus"},
		{name: "native_default_model", body: `{"input":{"messages":[{"role":"user","content":[{"text":"hi"}]}]}}`, wantOK: true, wantModel: defaultImagesModel},
		{name: "native_with_openai_prompt", body: `{"model":"gpt-image-1","prompt":"hello","input":{"messages":[{"role":"user"}]}}`, wantOK: true, wantModel: "gpt-image-1"},
		{name: "native_empty_messages", body: `{"model":"qwen-image-plus","input":{"messages":[]}}`, wantError: "prompt is required"},
		{name: "native_messages_not_array", body: `{"model":"qwen-image-plus","input":{"messages":"hi"}}`, wantError: "prompt is required"},
		{name: "native_input_not_object", body: `{"model":"qwen-image-plus","input":"hi"}`, wantError: "prompt is required"},
		{name: "invalid_json", body: `{"model":`, wantError: "Invalid request body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(tc.body))
			model, _, ok := parseImagesRequest(c, []byte(tc.body), "application/json", operationGenerations)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v, body=%s", ok, tc.wantOK, w.Body.String())
			}
			if !tc.wantOK {
				if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tc.wantError) {
					t.Fatalf("status=%d body=%s, want %q", w.Code, w.Body.String(), tc.wantError)
				}
				return
			}
			if model != tc.wantModel {
				t.Fatalf("model=%q want %q", model, tc.wantModel)
			}
		})
	}
}

func TestBuildOperationRequest_NativeInputMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	newRequest := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(qwenGenerationsBody))
		return c
	}

	req, err := buildOperationRequest(newRequest(), &config.UpstreamConfig{ServiceType: "openai"}, "https://example.com", "sk-test", []byte(qwenGenerationsBody), "qwen-image-plus", operationGenerations, "application/json")
	if err != nil {
		t.Fatalf("buildOperationRequest() error = %v", err)
	}
	if req.URL.String() != "https://example.com/v1/images/generations" {
		t.Fatalf("unexpected url: %s", req.URL.String())
	}
	got, _ := io.ReadAll(req.Body)
	if string(got) != qwenGenerationsBody {
		t.Fatalf("native body changed: %s", got)
	}

	upstream := &config.UpstreamConfig{ServiceType: "openai", ModelMapping: map[string]string{"qwen-image-plus": "actual-image"}}
	req, err = buildOperationRequest(newRequest(), upstream, "https://example.com", "sk-test", []byte(qwenGenerationsBody), "qwen-image-plus", operationGenerations, "application/json")
	if err != nil {
		t.Fatalf("buildOperationRequest() error = %v", err)
	}
	mapped, _ := io.ReadAll(req.Body)
	var gotMap, wantMap map[string]interface{}
	if err := json.Unmarshal(mapped, &gotMap); err != nil {
		t.Fatalf("mapped body invalid: %v", err)
	}
	if err := json.Unmarshal([]byte(qwenGenerationsBody), &wantMap); err != nil {
		t.Fatalf("source body invalid: %v", err)
	}
	if gotMap["model"] != "actual-image" {
		t.Fatalf("model mapping not applied: %s", mapped)
	}
	if !reflect.DeepEqual(gotMap["input"], wantMap["input"]) || !reflect.DeepEqual(gotMap["parameters"], wantMap["parameters"]) {
		t.Fatalf("native fields changed: %s", mapped)
	}
}
