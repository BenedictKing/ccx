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
		ModelMapping:     map[string]string{"gpt-5": "gpt-5.4"},
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
}

func TestGetUpstreams_IncludesStripBillingHeaderField(t *testing.T) {
	enabled := true
	cm := setupTestConfigManager(t, []config.UpstreamConfig{{
		Name:               "msg-ch",
		ServiceType:        "claude",
		BaseURL:            "https://api.example.com",
		APIKeys:            []string{"sk-1"},
		StripBillingHeader: &enabled,
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
	if got := resp.Channels[0]["stripBillingHeader"]; got != true {
		t.Fatalf("stripBillingHeader = %v, want true", got)
	}
}

func TestGetUpstreams_IncludesStripEmptyTextBlocksField(t *testing.T) {
	channel := config.UpstreamConfig{
		Name:        "msg-ch",
		ServiceType: "claude",
		BaseURL:     "https://api.example.com",
		APIKeys:     []string{"sk-1"},
	}
	// 该兼容开关已无手工字段：用可落盘的种子表达"生效值为 true"。
	channel.SetCompatSeed(config.TraitStripEmptyTextBlocks, true)
	cm := setupTestConfigManager(t, []config.UpstreamConfig{channel})

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
	if got := resp.Channels[0]["stripEmptyTextBlocks"]; got != true {
		t.Fatalf("stripEmptyTextBlocks = %v, want true", got)
	}
}

func TestGetUpstreams_IncludesNormalizeSystemRoleToTopLevelField(t *testing.T) {
	cm := setupTestConfigManager(t, []config.UpstreamConfig{{
		Name:                          "msg-ch",
		ServiceType:                   "claude",
		BaseURL:                       "https://api.example.com",
		APIKeys:                       []string{"sk-1"},
		NormalizeSystemRoleToTopLevel: true,
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
	if got := resp.Channels[0]["normalizeSystemRoleToTopLevel"]; got != true {
		t.Fatalf("normalizeSystemRoleToTopLevel = %v, want true", got)
	}
}

func TestGetUpstreams_IncludesStatusAndDisabledApiKeys(t *testing.T) {
	cm := setupTestConfigManager(t, []config.UpstreamConfig{{
		Name:        "msg-ch",
		ServiceType: "claude",
		BaseURL:     "https://api.example.com",
		APIKeys:     []string{"sk-1"},
		Status:      "suspended",
		DisabledAPIKeys: []config.DisabledKeyInfo{{
			Key:        "sk-disabled",
			Reason:     "insufficient_balance",
			Message:    "no balance",
			DisabledAt: "2026-04-11T00:00:00Z",
		}},
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
	if got := resp.Channels[0]["status"]; got != "suspended" {
		t.Fatalf("status = %v, want suspended", got)
	}
	if got := resp.Channels[0]["adminState"]; got != "suspended" {
		t.Fatalf("adminState = %v, want suspended", got)
	}
	if got := resp.Channels[0]["effectiveState"]; got != "suspended" {
		t.Fatalf("effectiveState = %v, want suspended", got)
	}
	if got := resp.Channels[0]["runtimeState"]; got != "disabled_keys_present" {
		t.Fatalf("runtimeState = %v, want disabled_keys_present", got)
	}
	disabledKeys, ok := resp.Channels[0]["disabledApiKeys"].([]interface{})
	if !ok || len(disabledKeys) != 1 {
		t.Fatalf("disabledApiKeys = %#v, want len 1", resp.Channels[0]["disabledApiKeys"])
	}
}

func TestPingChannel_WithoutBaseURLReturnsError(t *testing.T) {
	cm := setupTestConfigManager(t, []config.UpstreamConfig{{
		Name:        "msg-ch",
		ServiceType: "claude",
	}})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/messages/ping/:id", PingChannel(cm))

	req := httptest.NewRequest(http.MethodGet, "/messages/ping/0", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got := resp["error"]; got == nil {
		t.Fatalf("error = %v, want non-nil", got)
	}
}

func TestPingAllChannels_WithoutBaseURLMarksChannelError(t *testing.T) {
	cm := setupTestConfigManager(t, []config.UpstreamConfig{{
		Name:        "msg-ch",
		ServiceType: "claude",
	}})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/messages/ping", PingAllChannels(cm))

	req := httptest.NewRequest(http.MethodGet, "/messages/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	var resp []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(resp) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(resp))
	}
	if got := resp[0]["error"]; got == nil {
		t.Fatalf("error = %v, want non-nil", got)
	}
}
