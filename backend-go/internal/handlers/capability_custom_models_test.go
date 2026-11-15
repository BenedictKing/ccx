package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func TestStartCapabilityTest_UsesCustomModelOutsideDefaults(t *testing.T) {
	for _, tc := range []struct {
		name        string
		channelKind string
		protocol    string
		mapped      bool
	}{
		{name: "native", channelKind: "chat", protocol: "chat"},
		{name: "same protocol mapping", channelKind: "chat", protocol: "chat->chat", mapped: true},
		{name: "cross protocol", channelKind: "responses", protocol: "responses->chat"},
		{name: "cross protocol mapping", channelKind: "responses", protocol: "responses->chat", mapped: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetCapabilityTestState(t)
			gin.SetMode(gin.TestMode)
			const customModel = "custom-model-outside-defaults"
			actualModel := customModel
			if tc.mapped {
				actualModel = "private-upstream-model"
			}
			var requests atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				var body struct {
					Model string `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode upstream request: %v", err)
				}
				if r.URL.Path != "/v1/chat/completions" || body.Model != actualModel {
					t.Errorf("upstream request path=%s model=%s, want chat request for %s", r.URL.Path, body.Model, actualModel)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n\n"))
			}))
			defer upstream.Close()

			channel := config.UpstreamConfig{
				Name: "custom-model-test", ServiceType: "openai", BaseURL: upstream.URL,
				APIKeys: []string{"test-key"}, SupportedModels: []string{"unrelated-default-model"}, Status: "active",
			}
			if tc.mapped {
				channel.ModelMapping = map[string]string{customModel: actualModel}
			}
			data, err := json.Marshal(config.Config{
				ChatUpstream: []config.UpstreamConfig{channel}, ResponsesUpstream: []config.UpstreamConfig{channel},
			})
			if err != nil {
				t.Fatal(err)
			}
			configFile := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(configFile, data, 0o600); err != nil {
				t.Fatal(err)
			}
			cfgManager, err := config.NewConfigManager(configFile, "")
			if err != nil {
				t.Fatal(err)
			}
			defer cfgManager.Close()
			router := gin.New()
			route := "/" + tc.channelKind + "/channels/:id/capability-test"
			router.POST(route, TestChannelCapability(cfgManager, nil, tc.channelKind))
			body := `{"targetProtocols":["` + tc.protocol + `"],"sourceTab":"` + tc.channelKind + `","models":["` + customModel + `"],"rpm":60}`
			url := "/" + tc.channelKind + "/channels/0/capability-test"
			start := func() *CapabilityTestJob {
				t.Helper()
				req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("start status=%d body=%s", w.Code, w.Body.String())
				}
				var resp struct {
					Job CapabilityTestJob `json:"job"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatal(err)
				}
				return &resp.Job
			}
			started := start()
			deadline := time.Now().Add(3 * time.Second)
			for {
				job, ok := capabilityJobs.get(started.JobID)
				if ok && job.Lifecycle == CapabilityLifecycleDone {
					if len(job.Tests) != 1 || job.Tests[0].Protocol != tc.protocol || len(job.Tests[0].ModelResults) != 1 {
						t.Fatalf("unexpected protocol results: %+v", job.Tests)
					}
					result := job.Tests[0].ModelResults[0]
					if result.Model != customModel || !result.Success || (tc.mapped && result.ActualModel != actualModel) {
						t.Fatalf("custom model result=%+v", result)
					}
					if len(job.CompatibleProtocols) != 1 || job.CompatibleProtocols[0] != tc.protocol {
						t.Fatalf("compatible protocols=%v, want %s", job.CompatibleProtocols, tc.protocol)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("custom model test did not finish: %+v (requests=%d)", job, requests.Load())
				}
				time.Sleep(10 * time.Millisecond)
			}
			cancelAndWaitCapabilityWorkers(t, capabilityJobs)
			cached := start()
			if cached.RunMode != CapabilityRunModeCacheHit || len(cached.Tests) != 1 || cached.Tests[0].ModelResults[0].Model != customModel || !cached.Tests[0].ModelResults[0].Success {
				t.Fatalf("cached custom model result=%+v", cached)
			}
			if requests.Load() != 1 {
				t.Fatalf("upstream requests=%d, want 1", requests.Load())
			}
		})
	}
}
