package common

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

// TestTryUpstreamAppliesFederatedExecutionModelBeforeProviderRequest 验证协议联邦候选
// 在构造上游请求前就把模型改写为 sibling 的实际执行模型，并按该模型应用
// model-registry 的固定值参数约束（Kimi K3 的 temperature/top_p 等）。
func TestTryUpstreamAppliesFederatedExecutionModelBeforeProviderRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	type upstreamRequest struct {
		Model            string   `json:"model"`
		Temperature      *float64 `json:"temperature"`
		TopP             *float64 `json:"top_p"`
		PresencePenalty  *float64 `json:"presence_penalty"`
		FrequencyPenalty *float64 `json:"frequency_penalty"`
	}
	received := make(chan upstreamRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request upstreamRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		received <- request
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "k3-chat",
		ChannelUID:  "ch_k3_chat",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-k3"},
		Status:      "active",
		ServiceType: "openai",
	})
	defer cleanup()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"claude-sonnet-5"}`))

	requestBody := []byte(`{"model":"claude-sonnet-5","temperature":0.7,"top_p":0.8,"presence_penalty":0.5,"frequency_penalty":0.5,"messages":[]}`)
	executionRoute := scheduler.ChannelRouteRef{Kind: string(scheduler.ChannelKindChat), Index: 0, ChannelUID: "ch_k3_chat"}

	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]
	handled, successKey, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
		c,
		config.NewEnvConfig(),
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindMessages,
		"Messages",
		messagesMetrics,
		upstream,
		[]warmup.URLLatencyResult{{URL: server.URL, OriginalIdx: 0}},
		requestBody,
		nil,
		false,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, ChannelAPIType(scheduler.ChannelKindChat))
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			body, _ := c.Get("requestBodyBytes")
			raw, _ := body.([]byte)
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, strings.NewReader(string(raw)))
		},
		func(apiKey string) {},
		nil,
		nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			_ = resp.Body.Close()
			return nil, nil
		},
		"claude-sonnet-5",
		"",
		0,
		channelScheduler.GetChannelLogStoreForRoute(executionRoute),
		WithExecutionRoute(executionRoute),
		WithExecutionModel("kimi-k3"),
	)

	if !handled || failoverErr != nil || lastErr != nil {
		t.Fatalf("federated attempt failed: handled=%v failoverErr=%#v lastErr=%v", handled, failoverErr, lastErr)
	}
	if successKey != "sk-k3" {
		t.Fatalf("successKey = %q, want sk-k3", successKey)
	}

	select {
	case request := <-received:
		if request.Model != "kimi-k3" {
			t.Fatalf("upstream model = %q, want kimi-k3", request.Model)
		}
		if request.Temperature != nil || request.TopP != nil || request.PresencePenalty != nil || request.FrequencyPenalty != nil {
			t.Fatalf("K3 fixed-value params were not stripped: %#v", request)
		}
	default:
		t.Fatal("upstream never received the federated request")
	}

	// 指标、日志与 key 归属必须落在执行协议（chat），不是请求协议（messages）。
	chatServiceType := scheduler.NormalizedMetricsServiceType(scheduler.ChannelKindChat, upstream.ServiceType)
	identity := metrics.GenerateMetricsIdentityKey(server.URL, "sk-k3", chatServiceType)
	logs := channelScheduler.GetChannelLogStoreForRoute(executionRoute).Get(identity)
	if len(logs) != 1 {
		t.Fatalf("chat-route channel logs = %d, want 1", len(logs))
	}
	if logs[0].BaseURL != server.URL || logs[0].KeyMask == "" {
		t.Fatalf("log attribution wrong: %#v", logs[0])
	}
	if logs[0].InterfaceType != "" && !strings.EqualFold(logs[0].InterfaceType, "chat") {
		t.Fatalf("interfaceType = %q, want chat execution protocol", logs[0].InterfaceType)
	}
	if messagesLogs := channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages).Get(identity); len(messagesLogs) != 0 {
		t.Fatalf("messages store must not receive sibling execution logs: %#v", messagesLogs)
	}
}

// TestTryUpstreamAppliesFederatedExecutionModelForResponsesToChat 验证 responses 请求
// 联邦到 chat sibling 时，请求体模型被改写为 executionModel，并且指标、日志、Key 降级
// 都落在执行协议（chat）而非请求协议（responses）。
func TestTryUpstreamAppliesFederatedExecutionModelForResponsesToChat(t *testing.T) {
	gin.SetMode(gin.TestMode)

	type upstreamRequest struct {
		Model string `json:"model"`
	}
	received := make(chan upstreamRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request upstreamRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		received <- request
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","object":"chat.completion","choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "gpt-chat",
		ChannelUID:  "ch_gpt_chat",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-gpt"},
		Status:      "active",
		ServiceType: "openai",
	})
	defer cleanup()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-5.6-sol"}`))

	requestBody := []byte(`{"model":"gpt-5.6-sol","input":"ping","stream":false}`)
	executionRoute := scheduler.ChannelRouteRef{Kind: string(scheduler.ChannelKindChat), Index: 0, ChannelUID: "ch_gpt_chat"}

	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]
	handled, successKey, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
		c,
		config.NewEnvConfig(),
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindResponses,
		"Responses",
		messagesMetrics,
		upstream,
		[]warmup.URLLatencyResult{{URL: server.URL, OriginalIdx: 0}},
		requestBody,
		nil,
		false,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, ChannelAPIType(scheduler.ChannelKindChat))
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			body, _ := c.Get("requestBodyBytes")
			raw, _ := body.([]byte)
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, strings.NewReader(string(raw)))
		},
		func(apiKey string) {},
		nil,
		nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			_ = resp.Body.Close()
			return nil, nil
		},
		"gpt-5.6-sol",
		"",
		0,
		channelScheduler.GetChannelLogStoreForRoute(executionRoute),
		WithExecutionRoute(executionRoute),
		WithExecutionModel("gpt-5.4-openai"),
	)

	if !handled || failoverErr != nil || lastErr != nil {
		t.Fatalf("federated responses->chat attempt failed: handled=%v failoverErr=%#v lastErr=%v", handled, failoverErr, lastErr)
	}
	if successKey != "sk-gpt" {
		t.Fatalf("successKey = %q, want sk-gpt", successKey)
	}

	select {
	case request := <-received:
		if request.Model != "gpt-5.4-openai" {
			t.Fatalf("upstream model = %q, want gpt-5.4-openai", request.Model)
		}
	default:
		t.Fatal("upstream never received the federated request")
	}

	// 指标、日志归属必须在执行协议（chat），不是请求协议（responses）。
	chatServiceType := scheduler.NormalizedMetricsServiceType(scheduler.ChannelKindChat, upstream.ServiceType)
	identity := metrics.GenerateMetricsIdentityKey(server.URL, "sk-gpt", chatServiceType)
	logs := channelScheduler.GetChannelLogStoreForRoute(executionRoute).Get(identity)
	if len(logs) != 1 {
		t.Fatalf("chat-route channel logs = %d, want 1", len(logs))
	}
	if logs[0].InterfaceType != "" && !strings.EqualFold(logs[0].InterfaceType, "chat") {
		t.Fatalf("interfaceType = %q, want chat execution protocol", logs[0].InterfaceType)
	}
	if responsesLogs := channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses).Get(identity); len(responsesLogs) != 0 {
		t.Fatalf("responses store must not receive sibling execution logs: %#v", responsesLogs)
	}
}
