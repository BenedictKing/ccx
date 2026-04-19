package messages

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func TestGetUpstreams_IncludesAdvancedOptionFields(t *testing.T) {
	cm := setupTestConfigManager(t, []config.UpstreamConfig{{
		Name:             "msg-ch",
		ServiceType:      "responses",
		BaseURL:          "https://api.example.com",
		APIKeys:          []string{"sk-1"},
		ModelMapping:     map[string]string{"gpt-5": "gpt-5.2"},
		ReasoningMapping: map[string]string{"gpt-5": "high"},
		TextVerbosity:    "medium",
		FastMode:         true,
	}})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/messages/channels", GetUpstreams(cm))

	req := httptest.NewRequest(http.MethodGet, "/messages/channels", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Channels []map[string]interface{} `json:"channels"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.Channels) != 1 {
		t.Fatalf("len(channels) = %d, want 1", len(resp.Channels))
	}
	ch := resp.Channels[0]
	if ch["textVerbosity"] != "medium" {
		t.Fatalf("textVerbosity = %v, want medium", ch["textVerbosity"])
	}
	if ch["fastMode"] != true {
		t.Fatalf("fastMode = %v, want true", ch["fastMode"])
	}
	rm, ok := ch["reasoningMapping"].(map[string]interface{})
	if !ok || rm["gpt-5"] != "high" {
		t.Fatalf("reasoningMapping = %#v, want gpt-5=high", ch["reasoningMapping"])
	}
}

func TestGetUpstreams_IncludesNormalizeMetadataUserIdField(t *testing.T) {
	enabled := true
	cm := setupTestConfigManager(t, []config.UpstreamConfig{{
		Name:                    "msg-ch",
		ServiceType:             "claude",
		BaseURL:                 "https://api.example.com",
		APIKeys:                 []string{"sk-1"},
		NormalizeMetadataUserID: &enabled,
	}})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/messages/channels", GetUpstreams(cm))

	req := httptest.NewRequest(http.MethodGet, "/messages/channels", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp struct {
		Channels []map[string]interface{} `json:"channels"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp.Channels) != 1 {
		t.Fatalf("len(channels) = %d, want 1", len(resp.Channels))
	}
	if got := resp.Channels[0]["normalizeMetadataUserId"]; got != true {
		t.Fatalf("normalizeMetadataUserId = %v, want true", got)
	}
	if got := resp.Channels[0]["strictRequestPassthroughEnabled"]; got != true {
		t.Fatalf("strictRequestPassthroughEnabled = %v, want true", got)
	}
}
