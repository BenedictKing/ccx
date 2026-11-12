package common

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
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

func TestModelFallbackWithRealUpstreamAttempts(t *testing.T) {
	for _, tc := range []struct {
		name         string
		allFail      bool
		keys         int
		limit        int
		wantStatus   int
		wantAttempts int
	}{{"model_missing", false, 1, 6, 200, 2}, {"attempt_budget", true, 3, 2, 500, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			var attempts atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				w.Header().Set("Content-Type", "application/json")
				if tc.allFail {
					w.WriteHeader(500)
					_, _ = w.Write([]byte(`{"error":{"message":"temporary upstream error"}}`))
					return
				}
				if body["model"] == "primary" {
					w.WriteHeader(404)
					_, _ = w.Write([]byte(`{"error":{"code":"model_not_found","message":"model not found"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"backup works"}}]}`))
			}))
			defer srv.Close()
			channel := config.UpstreamConfig{Name: "relay", ServiceType: "openai", BaseURL: srv.URL, Status: "active", SupportedModels: []string{"primary", "backup"}, CircuitBreakerDisabled: tc.allFail}
			for i := 0; i < tc.keys; i++ {
				key := string(rune('a' + i))
				channel.APIKeys = append(channel.APIKeys, key)
				channel.APIKeyConfigs = append(channel.APIKeyConfigs, config.APIKeyConfig{Key: key, Name: key})
			}
			cfg := config.Config{ChatUpstream: []config.UpstreamConfig{channel}, FuzzyModeEnabled: true, ModelFallbackPolicies: map[string]config.ModelFallbackPolicy{"chat:client": {Protocol: "chat", RequestModel: "client", Models: []string{"primary", "backup"}, MaxAttempts: tc.limit, MaxWaitSeconds: 10}}}
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
			routingStore, _ := metrics.NewRoutingStore(nil)
			defer routingStore.Close()
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), routingStoreContextKey{}, routingStore))
			finishObservation := ObserveRoutingRequest(c, "chat")
			recordRoutingModel(c, body)
			env := config.NewEnvConfig()
			HandleMultiChannelFailover(c, env, sch, scheduler.ChannelKindChat, "Chat", "", "client", "", func(selection *scheduler.SelectionResult) MultiChannelAttemptResult {
				handled, key, index, failover, usage, lastErr := TryUpstreamWithAllKeys(c, env, cm, sch, scheduler.ChannelKindChat, "Chat", manager, selection.Upstream, []warmup.URLLatencyResult{{URL: srv.URL}}, body, nil, false,
					func(up *config.UpstreamConfig, failed map[string]bool) (string, error) {
						return cm.GetNextChatAPIKey(up, failed)
					},
					func(c *gin.Context, up *config.UpstreamConfig, key string) (*http.Request, error) {
						payload, _ := json.Marshal(map[string]string{"model": config.RedirectModel("client", up)})
						return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, srv.URL, bytes.NewReader(payload))
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
			finishObservation()
			records, _ := routingStore.Snapshot("chat", time.Now().Add(-time.Hour), time.Now().Add(time.Second))
			if len(records) != 1 || records[0].RequestedModel != "client" || len(records[0].Attempts) != tc.wantAttempts {
				t.Fatalf("routing records=%+v", records)
			}
			first := records[0].Attempts[0]
			if first.EffectiveModel != "primary" || first.TransformReason != "model_fallback" || first.MappingOutcome != metrics.MappingOutcomeUnmatched {
				t.Fatalf("first model fallback=%+v", first)
			}
			if !tc.allFail {
				last := records[0].Attempts[len(records[0].Attempts)-1]
				if last.SentModel != "backup" || last.EffectiveModel != "backup" {
					t.Fatalf("last fallback=%+v", last)
				}
			}
			if recorder.Code != tc.wantStatus || int(attempts.Load()) != tc.wantAttempts {
				t.Fatalf("status=%d attempts=%d body=%s", recorder.Code, attempts.Load(), recorder.Body.String())
			}
		})
	}
}
