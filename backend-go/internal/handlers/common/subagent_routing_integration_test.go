package common

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

func TestSubagentRoutingFailoverKeepsPolicyModel(t *testing.T) {
	sentModels := make(chan string, 2)
	newUpstream := func(name string, fail bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			model, _ := body["model"].(string)
			sentModels <- model
			w.Header().Set("Content-Type", "application/json")
			if fail {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"message":"retry"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		}))
	}
	first := newUpstream("first", true)
	defer first.Close()
	second := newUpstream("second", false)
	defer second.Close()

	cfg := config.Config{
		ChatUpstream: []config.UpstreamConfig{
			{Name: "first", ChannelUID: "uid-a", ServiceType: "openai", BaseURL: first.URL, APIKeys: []string{"a"}, Status: "active", Priority: 1, SupportedModels: []string{"sol"}, ModelMapping: map[string]string{"sol": "actual-a"}},
			{Name: "second", ChannelUID: "uid-b", ServiceType: "openai", BaseURL: second.URL, APIKeys: []string{"b"}, Status: "active", Priority: 0, SupportedModels: []string{"sol"}, ModelMapping: map[string]string{"sol": "actual-b"}},
		},
		SubagentRouting:       map[string]config.SubagentRoutingPolicy{"chat": {Model: "sol", Channels: []config.SubagentRoutingChannel{{ChannelUID: "uid-a", Enabled: true}, {ChannelUID: "uid-b", Enabled: true}}}},
		ModelFallbackPolicies: map[string]config.ModelFallbackPolicy{"chat:client": {Protocol: "chat", RequestModel: "client", Models: []string{"client", "strong"}, MaxAttempts: 4, MaxWaitSeconds: 10}},
	}
	data, _ := json.Marshal(cfg)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cm, err := config.NewConfigManager(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()
	manager := metrics.NewMetricsManager()
	defer manager.Stop()
	sch := scheduler.NewChannelScheduler(cm, manager, manager, manager, manager, manager, session.NewTraceAffinityManager(), warmup.NewURLManager(time.Minute, 3))
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"client","messages":[{"role":"user","content":"hello"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	SetRequestLogContextWithAgent(c, "user", 1, &types.AgentContext{AgentRole: "subagent", Confidence: "exact"})
	env := config.NewEnvConfig()

	HandleMultiChannelFailover(c, env, sch, scheduler.ChannelKindChat, "Chat", "user", "client", "subagent", func(selection *scheduler.SelectionResult) MultiChannelAttemptResult {
		handled, key, index, failover, usage, lastErr := TryUpstreamWithAllKeys(c, env, cm, sch, scheduler.ChannelKindChat, "Chat", manager, selection.Upstream, []warmup.URLLatencyResult{{URL: selection.Upstream.BaseURL}}, body, nil, false,
			func(up *config.UpstreamConfig, failed map[string]bool) (string, error) {
				return cm.GetNextChatAPIKey(up, failed)
			},
			func(c *gin.Context, up *config.UpstreamConfig, key string) (*http.Request, error) {
				payload, _ := json.Marshal(map[string]string{"model": config.RedirectModel("client", up)})
				return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, up.BaseURL, bytes.NewReader(payload))
			}, nil, nil, nil,
			func(c *gin.Context, resp *http.Response, up *config.UpstreamConfig, key string, actual []byte) (*types.Usage, error) {
				defer resp.Body.Close()
				payload, err := io.ReadAll(resp.Body)
				if err == nil {
					c.Data(200, "application/json", payload)
				}
				return nil, err
			}, "client", "", selection.ChannelIndex, sch.GetChannelLogStore(scheduler.ChannelKindChat))
		return MultiChannelAttemptResult{Handled: handled, Attempted: true, SuccessKey: key, SuccessBaseURLIdx: index, FailoverError: failover, Usage: usage, LastError: lastErr}
	}, nil, nil)

	if recorder.Code != 200 {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(sentModels) != 2 {
		t.Fatalf("上游尝试次数=%d，期望 2", len(sentModels))
	}
	if got := <-sentModels; got != "actual-a" {
		t.Fatalf("首渠道模型=%q", got)
	}
	if got := <-sentModels; got != "actual-b" {
		t.Fatalf("重试改回主模型或顺序错误，模型=%q", got)
	}
}
