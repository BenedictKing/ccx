package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func TestBuildTestRequestWithModelAndReasoningEffort_ResponsesOverride(t *testing.T) {
	channel := &config.UpstreamConfig{
		BaseURL: "https://example.com",
		APIKeys: []string{"test-key"},
	}

	req, err := buildTestRequestWithModelAndReasoningEffort("responses", channel, "deepseek-flash", "xhigh")
	if err != nil {
		t.Fatalf("build request failed: %v", err)
	}
	defer req.Body.Close()

	var body struct {
		Model     string `json:"model"`
		Reasoning struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body failed: %v", err)
	}
	if body.Model != "deepseek-flash" {
		t.Fatalf("model=%q, want deepseek-flash", body.Model)
	}
	if body.Reasoning.Effort != "xhigh" {
		t.Fatalf("reasoning effort=%q, want xhigh", body.Reasoning.Effort)
	}
}

func TestCapabilityRetest_ForceBypassesCacheAndPreservesReasoningEffort(t *testing.T) {
	resetCapabilityTestState(t)
	gin.SetMode(gin.TestMode)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid request"}}`))
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	configJSON := fmt.Sprintf(`{"upstream":[{"name":"channel","serviceType":"claude","baseUrl":%q,"apiKeys":["test"]}]}`, server.URL)
	if err := os.WriteFile(configFile, []byte(configJSON), 0644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("create config manager failed: %v", err)
	}
	defer cfgManager.Close()

	cfg := cfgManager.GetConfig()
	channel := cfg.Upstream[0]
	protocols := []string{"responses"}
	models := []string{"deepseek-flash"}
	cacheKey := buildCapabilityCacheKey(server.URL, "test", channel.ServiceType, protocols, models, "", "xhigh")
	setCapabilityCache(cacheKey, CapabilityTestResponse{
		Tests:               []ProtocolTestResult{{Protocol: "responses", Success: true, TestedAt: time.Now().Format(time.RFC3339Nano)}},
		CompatibleProtocols: protocols,
	})

	r := gin.New()
	r.POST("/messages/channels/:id/capability-test", TestChannelCapability(cfgManager, nil, "messages"))
	body := `{"targetProtocols":["responses"],"models":["deepseek-flash"],"reasoningEffort":"xhigh","force":true,"timeout":10000}`
	req := httptest.NewRequest(http.MethodPost, "/messages/channels/0/capability-test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want=%d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	var resp struct {
		Job CapabilityTestJob `json:"job"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if resp.Job.CacheHit {
		t.Fatal("force retest unexpectedly returned cached result")
	}
	if resp.Job.ReasoningEffort != "xhigh" {
		t.Fatalf("reasoningEffort=%q, want xhigh", resp.Job.ReasoningEffort)
	}
	if len(resp.Job.Tests) != 1 || len(resp.Job.Tests[0].ModelResults) != 1 || resp.Job.Tests[0].ModelResults[0].Model != "deepseek-flash" {
		t.Fatalf("unexpected job models: %+v", resp.Job.Tests)
	}
}
