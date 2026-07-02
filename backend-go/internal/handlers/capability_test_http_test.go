package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/gin-gonic/gin"
)

func resetCapabilityTestState() {
	capabilityJobs = &capabilityTestJobStore{jobs: make(map[string]*CapabilityTestJob), lookupKey: make(map[string]string)}
	capabilitySnapshots = newCapabilitySnapshotStore()
	capabilityTestDispatcherPool = newCapabilityTestDispatcherPool()
	capabilityCache.Lock()
	capabilityCache.entries = make(map[string]*capabilityCacheEntry)
	capabilityCache.Unlock()
}

func captureCapabilityLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	oldWriter := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(oldWriter)
	})
	return &buf
}

func TestCancelCapabilityTestJob_HTTP(t *testing.T) {
	resetCapabilityTestState()
	gin.SetMode(gin.TestMode)

	job := newCapabilityTestJob(0, "channel", "messages", "claude", []string{"messages"}, 10*time.Second, 10)
	job.Status = CapabilityJobStatusRunning
	job.Lifecycle = CapabilityLifecycleActive
	job.Tests[0].ModelResults = []CapabilityModelJobResult{
		{Model: "queued", Status: CapabilityModelStatusQueued, Lifecycle: CapabilityLifecyclePending, Outcome: CapabilityOutcomeUnknown},
		{Model: "running", Status: CapabilityModelStatusRunning, Lifecycle: CapabilityLifecycleActive, Outcome: CapabilityOutcomeUnknown},
	}
	capabilityJobs.create(job)
	capabilityJobs.setCancelFunc(job.JobID, func() {})

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configFile, []byte(`{"upstream":[]}`), 0644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("create config manager failed: %v", err)
	}
	defer cfgManager.Close()

	r := gin.New()
	r.DELETE("/messages/channels/:id/capability-test/:jobId", CancelCapabilityTestJob(cfgManager, "messages"))

	req := httptest.NewRequest(http.MethodDelete, "/messages/channels/0/capability-test/"+job.JobID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want=%d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	stored, ok := capabilityJobs.get(job.JobID)
	if !ok {
		t.Fatalf("job not found after cancel")
	}
	if stored.Lifecycle != CapabilityLifecycleCancelled {
		t.Fatalf("job lifecycle=%s, want cancelled", stored.Lifecycle)
	}
	if stored.Tests[0].ModelResults[1].Outcome != CapabilityOutcomeCancelled {
		t.Fatalf("running model outcome=%s, want cancelled", stored.Tests[0].ModelResults[1].Outcome)
	}
}

func TestGetCapabilityTestJobStatus_HTTP(t *testing.T) {
	resetCapabilityTestState()
	gin.SetMode(gin.TestMode)

	job := newCapabilityTestJob(0, "channel", "messages", "claude", []string{"messages"}, 10*time.Second, 10)
	job.Lifecycle = CapabilityLifecycleDone
	job.Outcome = CapabilityOutcomePartial
	job.Status = CapabilityJobStatusCompleted
	capabilityJobs.create(job)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configFile, []byte(`{"upstream":[{"name":"channel","service_type":"claude","base_url":"https://example.com","api_keys":["test"]}]}`), 0644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("create config manager failed: %v", err)
	}
	defer cfgManager.Close()

	r := gin.New()
	r.GET("/messages/channels/:id/capability-test/:jobId", GetCapabilityTestJobStatus(cfgManager, "messages"))

	req := httptest.NewRequest(http.MethodGet, "/messages/channels/0/capability-test/"+job.JobID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want=%d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp CapabilityTestJob
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if resp.Outcome != CapabilityOutcomePartial {
		t.Fatalf("outcome=%s, want partial", resp.Outcome)
	}
}

func TestCapabilityCacheHit_DoesNotBindExecutionLookupKey(t *testing.T) {
	resetCapabilityTestState()
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	configJSON := `{"upstream":[{"name":"channel-a","serviceType":"claude","baseUrl":"https://example.com","apiKeys":["test"]},{"name":"channel-b","serviceType":"claude","baseUrl":"https://example.com","apiKeys":["test"]}]}`
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
	baseURL := channel.GetAllBaseURLs()[0]
	apiKey := channel.APIKeys[0]
	protocols := []string{"messages"}
	cacheKey := buildCapabilityCacheKey(baseURL, apiKey, channel.ServiceType, protocols, nil, hashModelMapping(channel.ModelMapping))
	identityKey := resolveCapabilityIdentityKey(&channel)
	executionLookupKey := buildCapabilityExecutionLookupKey(identityKey, "messages", protocols, nil, hashModelMapping(channel.ModelMapping))

	setCapabilityCache(cacheKey, CapabilityTestResponse{
		ChannelID:           0,
		ChannelName:         "channel-a",
		SourceType:          channel.ServiceType,
		Tests:               []ProtocolTestResult{{Protocol: "messages", Success: true, TestedAt: time.Now().Format(time.RFC3339Nano)}},
		CompatibleProtocols: []string{"messages"},
		TotalDuration:       12,
	})

	r := gin.New()
	r.POST("/messages/channels/:id/capability-test", TestChannelCapability(cfgManager, nil, "messages"))

	body := `{"targetProtocols":["messages"],"timeout":10000}`
	req := httptest.NewRequest(http.MethodPost, "/messages/channels/1/capability-test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want=%d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	if _, ok := capabilityJobs.getByLookupKey(executionLookupKey); ok {
		t.Fatal("expected cache-hit path not to bind execution lookup key")
	}

	var resp struct {
		Resumed bool              `json:"resumed"`
		Job     CapabilityTestJob `json:"job"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if resp.Resumed {
		t.Fatal("expected cache-hit response not to be marked resumed")
	}
	if resp.Job.ExecutionKey != "" {
		t.Fatalf("executionKey=%q, want empty", resp.Job.ExecutionKey)
	}
}

func TestRetryCapabilityTestModel_HTTP_RejectsUnknownModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	job := newCapabilityTestJob(0, "channel", "messages", "claude", []string{"messages"}, 10*time.Second, 10)
	job.Status = CapabilityJobStatusCompleted
	job.Lifecycle = CapabilityLifecycleDone
	job.Outcome = CapabilityOutcomeFailed
	job.Tests[0].ModelResults = []CapabilityModelJobResult{{Model: "known", Status: CapabilityModelStatusFailed, Lifecycle: CapabilityLifecycleDone, Outcome: CapabilityOutcomeFailed}}
	capabilityJobs.create(job)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configFile, []byte(`{"upstream":[{"name":"channel","service_type":"claude","base_url":"https://example.com","api_keys":["test"]}]}`), 0644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("create config manager failed: %v", err)
	}
	defer cfgManager.Close()

	r := gin.New()
	r.POST("/messages/channels/:id/capability-test/:jobId/retry", RetryCapabilityTestModel(cfgManager, nil, "messages"))

	body := `{"protocol":"messages","model":"unknown"}`
	req := httptest.NewRequest(http.MethodPost, "/messages/channels/0/capability-test/"+job.JobID+"/retry", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want=%d, body=%s", w.Code, http.StatusNotFound, w.Body.String())
	}
}

func TestRetryCapabilityTestModel_HTTP_RejectsRunningJob(t *testing.T) {
	gin.SetMode(gin.TestMode)

	job := newCapabilityTestJob(0, "channel", "messages", "claude", []string{"messages"}, 10*time.Second, 10)
	job.Status = CapabilityJobStatusRunning
	job.Lifecycle = CapabilityLifecycleActive
	job.Tests[0].ModelResults = []CapabilityModelJobResult{
		{Model: "known", Status: CapabilityModelStatusFailed, Lifecycle: CapabilityLifecycleDone, Outcome: CapabilityOutcomeFailed},
	}
	capabilityJobs.create(job)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configFile, []byte(`{"upstream":[{"name":"channel","service_type":"claude","base_url":"https://example.com","api_keys":["test"]}]}`), 0644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("create config manager failed: %v", err)
	}
	defer cfgManager.Close()

	r := gin.New()
	r.POST("/messages/channels/:id/capability-test/:jobId/retry", RetryCapabilityTestModel(cfgManager, nil, "messages"))

	body := `{"protocol":"messages","model":"known"}`
	req := httptest.NewRequest(http.MethodPost, "/messages/channels/0/capability-test/"+job.JobID+"/retry", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d, want=%d, body=%s", w.Code, http.StatusConflict, w.Body.String())
	}
}

func TestRetryCapabilityTestModel_HTTP_RejectsNonRetryableModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	job := newCapabilityTestJob(0, "channel", "messages", "claude", []string{"messages"}, 10*time.Second, 10)
	job.Status = CapabilityJobStatusCompleted
	job.Lifecycle = CapabilityLifecycleDone
	job.Outcome = CapabilityOutcomeSuccess
	job.Tests[0].ModelResults = []CapabilityModelJobResult{
		{Model: "known", Status: CapabilityModelStatusSuccess, Lifecycle: CapabilityLifecycleDone, Outcome: CapabilityOutcomeSuccess, Success: true},
	}
	capabilityJobs.create(job)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configFile, []byte(`{"upstream":[{"name":"channel","service_type":"claude","base_url":"https://example.com","api_keys":["test"]}]}`), 0644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("create config manager failed: %v", err)
	}
	defer cfgManager.Close()

	r := gin.New()
	r.POST("/messages/channels/:id/capability-test/:jobId/retry", RetryCapabilityTestModel(cfgManager, nil, "messages"))

	body := `{"protocol":"messages","model":"known"}`
	req := httptest.NewRequest(http.MethodPost, "/messages/channels/0/capability-test/"+job.JobID+"/retry", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d, want=%d, body=%s", w.Code, http.StatusConflict, w.Body.String())
	}
}

func TestExecuteModelTest_RecordsCapabilityLogOnSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	job := newCapabilityTestJob(0, "channel", "messages", "claude", []string{"messages"}, 10*time.Second, 10)
	job.Tests[0].ModelResults = []CapabilityModelJobResult{{Model: "claude-test", Status: CapabilityModelStatusQueued, Lifecycle: CapabilityLifecyclePending, Outcome: CapabilityOutcomeUnknown}}
	capabilityJobs.create(job)

	store := metrics.NewChannelLogStore()
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	initialConfig := `{
		"upstream": [{
			"name": "channel",
			"baseUrl": "REPLACE_ME",
			"apiKeys": ["test-key"],
			"serviceType": "claude"
		}]
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"hello\"}}\n\n"))
	}))
	defer server.Close()

	initialConfig = strings.Replace(initialConfig, "REPLACE_ME", server.URL, 1)
	if err := os.WriteFile(configFile, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("create config manager failed: %v", err)
	}
	defer cfgManager.Close()

	cfg := cfgManager.GetConfig()
	metricsKey := metrics.GenerateMetricsIdentityKey(server.URL, "test-key", "claude")
	result := executeModelTest(context.Background(), &cfg.Upstream[0], "messages", "claude-test", 5*time.Second, job.JobID, cfgManager, 0, "messages", "test-key", store)
	if !result.Success {
		t.Fatalf("result.Success=false, want true")
	}

	logs := store.Get(metricsKey)
	if len(logs) != 1 {
		t.Fatalf("logs count=%d, want 1", len(logs))
	}
	if logs[0].RequestSource != metrics.RequestSourceCapabilityTest {
		t.Fatalf("requestSource=%q, want %q", logs[0].RequestSource, metrics.RequestSourceCapabilityTest)
	}
	if !logs[0].Success {
		t.Fatalf("success=false, want true")
	}
	if logs[0].InterfaceType != "messages" {
		t.Fatalf("interfaceType=%q, want messages", logs[0].InterfaceType)
	}
}

func TestExecuteModelTest_SuccessfulStreamDoesNotLogRawResponseBody(t *testing.T) {
	resetCapabilityTestState()
	t.Setenv("ENV", "development")
	t.Setenv("ENABLE_RESPONSE_LOGS", "true")
	gin.SetMode(gin.TestMode)

	logBuf := captureCapabilityLogs(t)
	job := newCapabilityTestJob(0, "channel", "responses", "responses", []string{"responses"}, 10*time.Second, 10)
	job.Tests[0].ModelResults = []CapabilityModelJobResult{{Model: "gpt-test", Status: CapabilityModelStatusQueued, Lifecycle: CapabilityLifecyclePending, Outcome: CapabilityOutcomeUnknown}}
	capabilityJobs.create(job)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	initialConfig := `{
		"upstream": [{
			"name": "channel",
			"baseUrl": "REPLACE_ME",
			"apiKeys": ["test-key"],
			"serviceType": "responses"
		}]
	}`
	const rawSSEText = "capability-success-stream-body-should-not-log"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"" + rawSSEText + "\"}\n\n"))
	}))
	defer server.Close()

	initialConfig = strings.Replace(initialConfig, "REPLACE_ME", server.URL, 1)
	if err := os.WriteFile(configFile, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("create config manager failed: %v", err)
	}
	defer cfgManager.Close()

	cfg := cfgManager.GetConfig()
	result := executeModelTest(context.Background(), &cfg.Upstream[0], "responses", "gpt-test", 5*time.Second, job.JobID, cfgManager, 0, "responses", "test-key", nil)
	if !result.Success {
		t.Fatalf("result.Success=false, want true, error=%v, logs=%s", result.Error, logBuf.String())
	}

	logs := logBuf.String()
	if strings.Contains(logs, "[Responses-Response] 响应体") {
		t.Fatalf("successful capability stream logged response body: %s", logs)
	}
	if strings.Contains(logs, rawSSEText) {
		t.Fatalf("successful capability stream leaked raw SSE body: %s", logs)
	}
}

func TestExecuteModelTest_NativeProtocolDoesNotExposeActualModel(t *testing.T) {
	resetCapabilityTestState()
	gin.SetMode(gin.TestMode)

	job := newCapabilityTestJob(0, "channel", "messages", "claude", []string{"messages"}, 10*time.Second, 10)
	job.Tests[0].ModelResults = []CapabilityModelJobResult{{Model: "claude-test", Status: CapabilityModelStatusQueued, Lifecycle: CapabilityLifecyclePending, Outcome: CapabilityOutcomeUnknown}}
	capabilityJobs.create(job)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	initialConfig := `{
		"upstream": [{
			"name": "channel",
			"baseUrl": "REPLACE_ME",
			"apiKeys": ["test-key"],
			"serviceType": "claude",
			"modelMapping": {"claude-test": "claude-redirected"}
		}]
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"hello\"}}\n\n"))
	}))
	defer server.Close()

	initialConfig = strings.Replace(initialConfig, "REPLACE_ME", server.URL, 1)
	if err := os.WriteFile(configFile, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("create config manager failed: %v", err)
	}
	defer cfgManager.Close()

	cfg := cfgManager.GetConfig()
	result := executeModelTest(context.Background(), &cfg.Upstream[0], "messages", "claude-test", 5*time.Second, job.JobID, cfgManager, 0, "messages", "test-key", nil)
	if !result.Success {
		t.Fatalf("result.Success=false, want true")
	}
	if result.ActualModel != "" {
		t.Fatalf("result.ActualModel=%q, want empty for native protocol", result.ActualModel)
	}

	stored, ok := capabilityJobs.get(job.JobID)
	if !ok {
		t.Fatal("job not found")
	}
	if got := stored.Tests[0].ModelResults[0].ActualModel; got != "" {
		t.Fatalf("job model ActualModel=%q, want empty for native protocol", got)
	}
}

func TestExecuteModelTest_RecordsCapabilityLogOnFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	job := newCapabilityTestJob(0, "channel", "messages", "claude", []string{"messages"}, 10*time.Second, 10)
	job.Tests[0].ModelResults = []CapabilityModelJobResult{{Model: "claude-test", Status: CapabilityModelStatusQueued, Lifecycle: CapabilityLifecyclePending, Outcome: CapabilityOutcomeUnknown}}
	capabilityJobs.create(job)

	store := metrics.NewChannelLogStore()
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	initialConfig := `{
		"upstream": [{
			"name": "channel",
			"baseUrl": "REPLACE_ME",
			"apiKeys": ["test-key"],
			"serviceType": "claude"
		}]
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"forbidden"}`))
	}))
	defer server.Close()

	initialConfig = strings.Replace(initialConfig, "REPLACE_ME", server.URL, 1)
	if err := os.WriteFile(configFile, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("create config manager failed: %v", err)
	}
	defer cfgManager.Close()

	cfg := cfgManager.GetConfig()
	metricsKey := metrics.GenerateMetricsIdentityKey(server.URL, "test-key", "claude")
	result := executeModelTest(context.Background(), &cfg.Upstream[0], "messages", "claude-test", 5*time.Second, job.JobID, cfgManager, 0, "messages", "test-key", store)
	if result.Success {
		t.Fatalf("result.Success=true, want false")
	}

	logs := store.Get(metricsKey)
	if len(logs) != 1 {
		t.Fatalf("logs count=%d, want 1", len(logs))
	}
	if logs[0].RequestSource != metrics.RequestSourceCapabilityTest {
		t.Fatalf("requestSource=%q, want %q", logs[0].RequestSource, metrics.RequestSourceCapabilityTest)
	}
	if logs[0].Success {
		t.Fatalf("success=true, want false")
	}
	if logs[0].StatusCode != http.StatusForbidden {
		t.Fatalf("statusCode=%d, want %d", logs[0].StatusCode, http.StatusForbidden)
	}
	if logs[0].ErrorInfo == "" {
		t.Fatal("errorInfo is empty, want non-empty")
	}
}

func TestExecuteModelTest_TruncatesLargeFailureBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	job := newCapabilityTestJob(0, "channel", "messages", "claude", []string{"messages"}, 10*time.Second, 10)
	job.Tests[0].ModelResults = []CapabilityModelJobResult{{Model: "claude-test", Status: CapabilityModelStatusQueued, Lifecycle: CapabilityLifecyclePending, Outcome: CapabilityOutcomeUnknown}}
	capabilityJobs.create(job)

	store := metrics.NewChannelLogStore()
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	initialConfig := `{
		"upstream": [{
			"name": "channel",
			"baseUrl": "REPLACE_ME",
			"apiKeys": ["test-key"],
			"serviceType": "claude"
		}]
	}`

	largeBody := strings.Repeat("x", 260)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(largeBody))
	}))
	defer server.Close()

	initialConfig = strings.Replace(initialConfig, "REPLACE_ME", server.URL, 1)
	if err := os.WriteFile(configFile, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("create config manager failed: %v", err)
	}
	defer cfgManager.Close()

	cfg := cfgManager.GetConfig()
	result := executeModelTest(context.Background(), &cfg.Upstream[0], "messages", "claude-test", 5*time.Second, job.JobID, cfgManager, 0, "messages", "test-key", store)
	if result.Success {
		t.Fatalf("result.Success=true, want false")
	}
	if result.Error == nil || len(*result.Error) != 200 {
		t.Fatalf("result.Error len=%d, want 200", len(*result.Error))
	}

	metricsKey := metrics.GenerateMetricsIdentityKey(server.URL, "test-key", "claude")
	logs := store.Get(metricsKey)
	if len(logs) != 1 {
		t.Fatalf("logs count=%d, want 1", len(logs))
	}
	if len(logs[0].ErrorInfo) != 200 {
		t.Fatalf("log errorInfo len=%d, want 200", len(logs[0].ErrorInfo))
	}
}

func TestExecuteModelTest_RespectsAutoBlacklistBalance(t *testing.T) {
	gin.SetMode(gin.TestMode)

	job := newCapabilityTestJob(0, "channel", "messages", "claude", []string{"messages"}, 10*time.Second, 10)
	capabilityJobs.create(job)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	initialConfig := `{
		"upstream": [{
			"name": "channel",
			"baseUrl": "REPLACE_ME",
			"apiKeys": ["test-key"],
			"serviceType": "claude",
			"autoBlacklistBalance": false
		}]
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"INSUFFICIENT_BALANCE","message":"Insufficient account balance"}`))
	}))
	defer server.Close()

	initialConfig = strings.Replace(initialConfig, "REPLACE_ME", server.URL, 1)
	if err := os.WriteFile(configFile, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("create config manager failed: %v", err)
	}
	defer cfgManager.Close()

	cfg := cfgManager.GetConfig()
	if len(cfg.Upstream) != 1 {
		t.Fatalf("upstream count=%d, want 1", len(cfg.Upstream))
	}

	result := executeModelTest(context.Background(), &cfg.Upstream[0], "messages", "claude-test", 5*time.Second, job.JobID, cfgManager, 0, "messages", "test-key", nil)
	if result.Success {
		t.Fatalf("result.Success=true, want false")
	}

	updated := cfgManager.GetConfig()
	if len(updated.Upstream[0].DisabledAPIKeys) != 0 {
		t.Fatalf("DisabledAPIKeys=%+v, want empty when autoBlacklistBalance=false", updated.Upstream[0].DisabledAPIKeys)
	}
	if len(updated.Upstream[0].APIKeys) != 1 || updated.Upstream[0].APIKeys[0] != "test-key" {
		t.Fatalf("APIKeys=%v, want original key preserved", updated.Upstream[0].APIKeys)
	}
}

func TestResumedCancelledJob_ReturnsUpdatedState(t *testing.T) {
	resetCapabilityTestState()
	gin.SetMode(gin.TestMode)

	// 创建一个已取消的 job
	job := newCapabilityTestJob(0, "test-channel", "messages", "claude", []string{"messages"}, 10*time.Second, 10)
	job.ChannelKind = "messages"
	job.Lifecycle = CapabilityLifecycleCancelled
	job.Outcome = CapabilityOutcomeCancelled
	job.Status = CapabilityJobStatusCancelled
	job.FinishedAt = time.Now().Format(time.RFC3339Nano)
	job.Tests[0].Lifecycle = CapabilityLifecycleCancelled
	job.Tests[0].Outcome = CapabilityOutcomeCancelled
	for i := range job.Tests[0].ModelResults {
		job.Tests[0].ModelResults[i].Status = CapabilityModelStatusSkipped
		job.Tests[0].ModelResults[i].Lifecycle = CapabilityLifecycleCancelled
		job.Tests[0].ModelResults[i].Outcome = CapabilityOutcomeCancelled
	}
	capabilityJobs.create(job)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configFile, []byte(`{"upstream":[{"name":"test-channel","serviceType":"claude","baseUrl":"https://example.com","apiKeys":["test"]}]}`), 0644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("create config manager failed: %v", err)
	}
	defer cfgManager.Close()

	cfg := cfgManager.GetConfig()
	channel := cfg.Upstream[0]
	job.IdentityKey = resolveCapabilityIdentityKey(&channel)
	baseURL := ""
	if len(channel.GetAllBaseURLs()) > 0 {
		baseURL = channel.GetAllBaseURLs()[0]
	}
	apiKey := ""
	if len(channel.APIKeys) > 0 {
		apiKey = channel.APIKeys[0]
	} else if len(channel.DisabledAPIKeys) > 0 {
		apiKey = channel.DisabledAPIKeys[0].Key
	}

	// 绑定 execution lookupKey，模拟取消后保留的 identity 运行复用键
	executionLookupKey := buildCapabilityExecutionLookupKey(resolveCapabilityIdentityKey(&channel), "messages", []string{"messages"}, nil, hashModelMapping(channel.ModelMapping))
	cacheKey := buildCapabilityCacheKey(baseURL, apiKey, channel.ServiceType, []string{"messages"}, nil, hashModelMapping(channel.ModelMapping))
	lookupKey := buildCapabilityJobLookupKey(cacheKey, "messages", 0)
	capabilityJobs.bindLookupKey(executionLookupKey, job.JobID)
	capabilityJobs.bindLookupKey(lookupKey, job.JobID)

	r := gin.New()
	r.POST("/messages/channels/:id/capability-test", TestChannelCapability(cfgManager, nil, "messages"))

	body := `{"targetProtocols":["messages"],"timeout":10000}`
	req := httptest.NewRequest(http.MethodPost, "/messages/channels/0/capability-test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want=%d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		JobID   string            `json:"jobId"`
		Resumed bool              `json:"resumed"`
		Job     CapabilityTestJob `json:"job"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}

	if resp.Resumed {
		t.Fatalf("resumed=%v, want false", resp.Resumed)
	}
	if resp.JobID == job.JobID {
		t.Fatalf("jobId=%s, want a new job id", resp.JobID)
	}
	if resp.Job.Lifecycle == CapabilityLifecycleCancelled {
		t.Fatalf("job.lifecycle=%s, want not cancelled (should be pending or active)", resp.Job.Lifecycle)
	}
	if resp.Job.RunMode != CapabilityRunModeFresh {
		t.Fatalf("job.runMode=%s, want fresh", resp.Job.RunMode)
	}
	if resp.Job.FinishedAt != "" {
		t.Fatalf("job.finishedAt=%s, want empty", resp.Job.FinishedAt)
	}
	if resp.Job.Outcome != CapabilityOutcomeUnknown {
		t.Fatalf("job.outcome=%s, want unknown", resp.Job.Outcome)
	}
}

func TestCapabilityPreviousJobReuse_ByIdentityAcrossChannels(t *testing.T) {
	resetCapabilityTestState()
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	configJSON := `{"upstream":[{"name":"channel-a","serviceType":"claude","baseUrl":"https://example.com","apiKeys":["test"]},{"name":"channel-b","serviceType":"claude","baseUrl":"https://example.com","apiKeys":["test"]}]}`
	if err := os.WriteFile(configFile, []byte(configJSON), 0644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("create config manager failed: %v", err)
	}
	defer cfgManager.Close()

	cfg := cfgManager.GetConfig()
	sharedIdentity := resolveCapabilityIdentityKey(&cfg.Upstream[0])

	prevJob := newCapabilityTestJob(0, "channel-a", "messages", "claude", []string{"messages"}, 10*time.Second, 10)
	prevJob.IdentityKey = sharedIdentity
	prevJob.ChannelKind = "messages"
	prevJob.Lifecycle = CapabilityLifecycleDone
	prevJob.Outcome = CapabilityOutcomeSuccess
	prevJob.Status = CapabilityJobStatusCompleted
	prevJob.CompatibleProtocols = []string{"messages"}
	prevJob.Tests[0].Success = true
	prevJob.Tests[0].SuccessCount = 1
	prevJob.Tests[0].AttemptedModels = 1
	prevJob.Tests[0].ModelResults = []CapabilityModelJobResult{{
		Model:              "claude-opus-4-7",
		Status:             CapabilityModelStatusSuccess,
		Lifecycle:          CapabilityLifecycleDone,
		Outcome:            CapabilityOutcomeSuccess,
		Success:            true,
		StreamingSupported: true,
		Latency:            123,
		TestedAt:           time.Now().Format(time.RFC3339Nano),
	}}
	capabilityJobs.create(prevJob)

	r := gin.New()
	r.POST("/messages/channels/:id/capability-test", TestChannelCapability(cfgManager, nil, "messages"))

	body := `{"targetProtocols":["messages"],"timeout":10000,"previousJobId":"` + prevJob.JobID + `"}`
	req := httptest.NewRequest(http.MethodPost, "/messages/channels/1/capability-test", strings.NewReader(body))
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

	if !resp.Job.HasReusedResults {
		t.Fatal("expected job to reuse previous results across same identity")
	}
	if resp.Job.RunMode != CapabilityRunModeReusedPreviousResult {
		t.Fatalf("runMode=%s, want reused_previous_results", resp.Job.RunMode)
	}
}

func TestCapabilityPreviousJobReuse_IsolatedByModelMapping(t *testing.T) {
	resetCapabilityTestState()
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	configJSON := `{"upstream":[{"name":"channel-a","serviceType":"claude","baseUrl":"https://example.com","apiKeys":["test"],"modelMapping":{"claude-sonnet-4-6":"old-target"}},{"name":"channel-b","serviceType":"claude","baseUrl":"https://example.com","apiKeys":["test"],"modelMapping":{"claude-sonnet-4-6":"new-target"}}]}`
	if err := os.WriteFile(configFile, []byte(configJSON), 0644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		t.Fatalf("create config manager failed: %v", err)
	}
	defer cfgManager.Close()

	cfg := cfgManager.GetConfig()
	prevJob := newCapabilityTestJob(0, "channel-a", "messages", "claude", []string{"messages"}, 10*time.Second, 10)
	prevJob.IdentityKey = resolveCapabilityIdentityKey(&cfg.Upstream[0])
	prevJob.ChannelKind = "messages"
	prevJob.Lifecycle = CapabilityLifecycleDone
	prevJob.Outcome = CapabilityOutcomeSuccess
	prevJob.Status = CapabilityJobStatusCompleted
	prevJob.CompatibleProtocols = []string{"messages"}
	prevJob.Tests[0].Success = true
	prevJob.Tests[0].SuccessCount = 1
	prevJob.Tests[0].AttemptedModels = 1
	prevJob.Tests[0].ModelResults = []CapabilityModelJobResult{{
		Model:     "claude-sonnet-4-6",
		Status:    CapabilityModelStatusSuccess,
		Lifecycle: CapabilityLifecycleDone,
		Outcome:   CapabilityOutcomeSuccess,
		Success:   true,
		TestedAt:  time.Now().Format(time.RFC3339Nano),
	}}
	capabilityJobs.create(prevJob)

	r := gin.New()
	r.POST("/messages/channels/:id/capability-test", TestChannelCapability(cfgManager, nil, "messages"))

	body := `{"targetProtocols":["messages"],"timeout":10000,"previousJobId":"` + prevJob.JobID + `"}`
	req := httptest.NewRequest(http.MethodPost, "/messages/channels/1/capability-test", strings.NewReader(body))
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

	if resp.Job.HasReusedResults {
		t.Fatal("expected previous results not to be reused across different modelMapping")
	}
	if resp.Job.RunMode == CapabilityRunModeReusedPreviousResult {
		t.Fatalf("runMode=%s, want not reused_previous_results", resp.Job.RunMode)
	}
}

func TestCapabilityRunningJobReuse_ByIdentityAcrossChannels(t *testing.T) {
	resetCapabilityTestState()
	gin.SetMode(gin.TestMode)

	runningJob := newCapabilityTestJob(0, "channel-a", "messages", "claude", []string{"messages"}, 10*time.Second, 10)
	runningJob.IdentityKey = "shared-identity"
	runningJob.ChannelKind = "messages"
	runningJob.Status = CapabilityJobStatusRunning
	runningJob.Lifecycle = CapabilityLifecycleActive
	capabilityJobs.create(runningJob)

	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")
	configJSON := `{"upstream":[{"name":"channel-a","serviceType":"claude","baseUrl":"https://example.com","apiKeys":["test"]},{"name":"channel-b","serviceType":"claude","baseUrl":"https://example.com","apiKeys":["test"]}]}`
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
	cacheKey := buildCapabilityCacheKey(channel.GetAllBaseURLs()[0], channel.APIKeys[0], channel.ServiceType, []string{"messages"}, nil, hashModelMapping(channel.ModelMapping))
	executionLookupKey := buildCapabilityExecutionLookupKey(resolveCapabilityIdentityKey(&channel), "messages", []string{"messages"}, nil, hashModelMapping(channel.ModelMapping))
	lookupKey := buildCapabilityJobLookupKey(cacheKey, "messages", 0)
	capabilityJobs.bindLookupKey(executionLookupKey, runningJob.JobID)
	capabilityJobs.bindLookupKey(lookupKey, runningJob.JobID)

	r := gin.New()
	r.POST("/messages/channels/:id/capability-test", TestChannelCapability(cfgManager, nil, "messages"))

	body := `{"targetProtocols":["messages"],"timeout":10000}`
	req := httptest.NewRequest(http.MethodPost, "/messages/channels/1/capability-test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want=%d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Resumed bool              `json:"resumed"`
		Job     CapabilityTestJob `json:"job"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}

	if !resp.Resumed {
		t.Fatal("expected running job to be reused across same identity")
	}
	if resp.Job.RunMode != CapabilityRunModeReusedRunning {
		t.Fatalf("runMode=%s, want reused_running", resp.Job.RunMode)
	}
	if resp.Job.JobID != runningJob.JobID {
		t.Fatalf("jobId=%s, want %s", resp.Job.JobID, runningJob.JobID)
	}
	if resp.Job.ChannelID != 1 {
		t.Fatalf("channelId=%d, want 1", resp.Job.ChannelID)
	}
	if resp.Job.ChannelName != "channel-b" {
		t.Fatalf("channelName=%s, want channel-b", resp.Job.ChannelName)
	}
	if resp.Job.SourceType != "claude" {
		t.Fatalf("sourceType=%s, want claude", resp.Job.SourceType)
	}
}

func TestBuildTestRequestWithModel_ChatReasoningEffortUsesProviderCompatibleValue(t *testing.T) {
	channel := &config.UpstreamConfig{
		BaseURL: "https://example.com",
		APIKeys: []string{"test-key"},
	}

	req, err := buildTestRequestWithModel("chat", channel, "test-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer req.Body.Close()

	var body map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body failed: %v", err)
	}
	if body["reasoning_effort"] != "low" {
		t.Fatalf("reasoning_effort=%v, want low", body["reasoning_effort"])
	}
	if body["max_tokens"] != float64(capabilityProbeMaxTokens) {
		t.Fatalf("max_tokens=%v, want %d", body["max_tokens"], capabilityProbeMaxTokens)
	}
}

func TestSendAndCheckStream_ChatReasoningOnlyCountsAsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","created":1,"model":"deepseek-v4-flash","choices":[{"index":0,"finish_reason":null,"delta":{"role":"assistant","content":null,"reasoning_content":"Thinking"}}]}` + "\n\n"))
		_, _ = w.Write([]byte(`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","created":1,"model":"deepseek-v4-flash","choices":[{"index":0,"finish_reason":"length","delta":{"content":"","reasoning_content":null}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"completion_tokens_details":{"reasoning_tokens":1}}}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	channel := &config.UpstreamConfig{
		BaseURL: server.URL,
		APIKeys: []string{"test-key"},
	}
	req, err := buildTestRequestWithModel("chat", channel, "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("build request failed: %v", err)
	}

	success, streamingSupported, statusCode, respBody, err := sendAndCheckStream(context.Background(), channel, req, "chat")
	if err != nil {
		t.Fatalf("sendAndCheckStream returned error: %v", err)
	}
	if !success {
		t.Fatalf("success=false, status=%d, body=%s", statusCode, string(respBody))
	}
	if !streamingSupported {
		t.Fatal("streamingSupported=false, want true")
	}
	if statusCode != http.StatusOK {
		t.Fatalf("statusCode=%d, want %d", statusCode, http.StatusOK)
	}
}

func TestBuildTestRequestWithModel_KimiK27CodeChatUsesRequiredReasoningEffort(t *testing.T) {
	channel := &config.UpstreamConfig{
		BaseURL: "https://example.com",
		APIKeys: []string{"test-key"},
	}

	req, err := buildTestRequestWithModel("chat", channel, "kimi-k2.7-code")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer req.Body.Close()

	var body map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body failed: %v", err)
	}
	if body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort=%v, want high", body["reasoning_effort"])
	}
}

func TestBuildTestRequestWithModel_NoAPIKey(t *testing.T) {
	channel := &config.UpstreamConfig{
		BaseURL: "https://example.com",
	}

	// APIKeys 和 DisabledAPIKeys 都为空时应返回 no_api_key 错误
	_, err := buildTestRequestWithModel("messages", channel, "test-model")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no_api_key") {
		t.Fatalf("error=%q, want contains 'no_api_key'", err.Error())
	}
}

func TestBuildTestRequestWithModel_FallbackToDisabledKey(t *testing.T) {
	channel := &config.UpstreamConfig{
		BaseURL: "https://example.com",
		APIKeys: []string{}, // 活跃 key 已被拉空
		DisabledAPIKeys: []config.DisabledKeyInfo{
			{Key: "disabled-key-1", Reason: "authentication_error"},
		},
	}

	// 应从 DisabledAPIKeys 临时借用 key，不 panic
	req, err := buildTestRequestWithModel("messages", channel, "test-model")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 验证请求使用了被拉黑的 key
	authHeader := req.Header.Get("X-Api-Key")
	if authHeader == "" {
		authHeader = req.Header.Get("Authorization")
	}
	if !strings.Contains(authHeader, "disabled-key-1") {
		t.Fatalf("auth header=%q, want contains 'disabled-key-1'", authHeader)
	}
}

func TestBuildTestRequestWithModel_ClaudeOpus48KeepsSystemMessageByDefault(t *testing.T) {
	channel := &config.UpstreamConfig{
		BaseURL: "https://example.com",
		APIKeys: []string{"test-key"},
	}

	req, err := buildTestRequestWithModel("messages", channel, capabilityProbeModelClaudeOpus48)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer req.Body.Close()

	var body map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body failed: %v", err)
	}
	messages, ok := body["messages"].([]interface{})
	if !ok {
		t.Fatalf("messages=%T, want []interface{}", body["messages"])
	}
	if len(messages) != 4 {
		t.Fatalf("messages len=%d, want 4", len(messages))
	}
	middle, ok := messages[2].(map[string]interface{})
	if !ok {
		t.Fatalf("middle message=%T, want map[string]interface{}", messages[2])
	}
	if middle["role"] != "system" {
		t.Fatalf("middle role=%v, want system", middle["role"])
	}
}

func TestBuildTestRequestWithModel_KimiK27CodeEnablesRequiredThinking(t *testing.T) {
	channel := &config.UpstreamConfig{
		BaseURL: "https://example.com",
		APIKeys: []string{"test-key"},
	}

	req, err := buildTestRequestWithModel("messages", channel, "kimi-k2.7-code")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer req.Body.Close()

	var body map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body failed: %v", err)
	}
	thinking, ok := body["thinking"].(map[string]interface{})
	if !ok {
		t.Fatalf("thinking=%T, want map[string]interface{}", body["thinking"])
	}
	if thinking["type"] != "enabled" {
		t.Fatalf("thinking.type=%v, want enabled", thinking["type"])
	}
	if thinking["effort"] != "high" {
		t.Fatalf("thinking.effort=%v, want high", thinking["effort"])
	}
}

func TestBuildTestRequestWithModel_GlobalThinkingCapabilityEnablesThinking(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	cfg := config.Config{
		UpstreamModelCapabilities: map[string]config.UpstreamModelCapability{
			"custom-thinking-model": {
				ThinkingMode:     "thinking",
				ReasoningEfforts: []string{"high"},
			},
		},
	}
	configBytes, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, configBytes, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	channel := &config.UpstreamConfig{
		BaseURL: "https://example.com",
		APIKeys: []string{"test-key"},
	}

	req, err := buildTestRequestWithModel("messages", channel, "custom-thinking-model", cfgManager)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer req.Body.Close()

	var body map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body failed: %v", err)
	}
	thinking, ok := body["thinking"].(map[string]interface{})
	if !ok {
		t.Fatalf("thinking=%T, want map[string]interface{}", body["thinking"])
	}
	if thinking["type"] != "enabled" {
		t.Fatalf("thinking.type=%v, want enabled", thinking["type"])
	}
	if thinking["effort"] != "high" {
		t.Fatalf("thinking.effort=%v, want high", thinking["effort"])
	}
}

func TestBuildTestRequestWithModel_CompositeUsesGlobalThinkingCapability(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	cfg := config.Config{
		UpstreamModelCapabilities: map[string]config.UpstreamModelCapability{
			"custom-thinking-model": {
				ThinkingMode:     "thinking",
				ReasoningEfforts: []string{"high"},
			},
		},
	}
	configBytes, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, configBytes, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	channel := &config.UpstreamConfig{
		BaseURL:     "https://example.com",
		APIKeys:     []string{"test-key"},
		ServiceType: "claude",
	}

	req, err := buildTestRequestWithModel("messages->messages", channel, "custom-thinking-model", cfgManager)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer req.Body.Close()

	var body map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body failed: %v", err)
	}
	thinking, ok := body["thinking"].(map[string]interface{})
	if !ok {
		t.Fatalf("thinking=%T, want map[string]interface{}", body["thinking"])
	}
	if thinking["type"] != "enabled" {
		t.Fatalf("thinking.type=%v, want enabled", thinking["type"])
	}
	if thinking["effort"] != "high" {
		t.Fatalf("thinking.effort=%v, want high", thinking["effort"])
	}
}

func TestBuildTestRequestWithModel_ClaudeOpus48NormalizesSystemMessageWhenEnabled(t *testing.T) {
	channel := &config.UpstreamConfig{
		BaseURL:                       "https://example.com",
		APIKeys:                       []string{"test-key"},
		NormalizeSystemRoleToTopLevel: true,
	}

	req, err := buildTestRequestWithModel("messages", channel, capabilityProbeModelClaudeOpus48)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer req.Body.Close()

	var body map[string]interface{}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		t.Fatalf("decode request body failed: %v", err)
	}
	messages, ok := body["messages"].([]interface{})
	if !ok {
		t.Fatalf("messages=%T, want []interface{}", body["messages"])
	}
	for _, raw := range messages {
		msg, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if msg["role"] == "system" {
			t.Fatalf("normalized probe should not include system message: %#v", messages)
		}
	}
	systemText, ok := body["system"].(string)
	if !ok {
		t.Fatalf("system=%T, want string", body["system"])
	}
	if !strings.Contains(systemText, "cc_entrypoint=cli") || !strings.Contains(systemText, "Claude agent") {
		t.Fatalf("system=%q, want billing header and Claude agent text", systemText)
	}
}
