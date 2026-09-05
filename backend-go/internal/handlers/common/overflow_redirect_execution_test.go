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

// tryUpstreamHarness 聚合一次 TryUpstreamWithAllKeys 跨模型执行所需的依赖。
type tryUpstreamHarness struct {
	cfgManager      *config.ConfigManager
	channelScheduler *scheduler.ChannelScheduler
	messagesMetrics *metrics.MetricsManager
	upstream        *config.UpstreamConfig
	executionRoute  scheduler.ChannelRouteRef
	cleanup         func()
}

func newCrossModelHarness(t *testing.T, serverURL string) *tryUpstreamHarness {
	t.Helper()
	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "gpt-chat",
		ChannelUID:  "ch_gpt_chat",
		BaseURL:     serverURL,
		APIKeys:     []string{"sk-gpt"},
		Status:      "active",
		ServiceType: "openai",
	})
	cfg := cfgManager.GetConfig()
	return &tryUpstreamHarness{
		cfgManager:      cfgManager,
		channelScheduler: channelScheduler,
		messagesMetrics: messagesMetrics,
		upstream:        &cfg.Upstream[0],
		executionRoute:  scheduler.ChannelRouteRef{Kind: string(scheduler.ChannelKindChat), Index: 0, ChannelUID: "ch_gpt_chat"},
		cleanup:         cleanup,
	}
}

// run 发起一次 responses→chat 的跨模型执行（溢出/联邦同链路）。
func (h *tryUpstreamHarness) run(t *testing.T, requestBody []byte, extraOpts ...TryUpstreamOption) (handled bool, failoverErr *FailoverError, lastErr error, recorder *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))

	opts := append([]TryUpstreamOption{
		WithExecutionRoute(h.executionRoute),
		WithExecutionModel("gpt-5.6-openai"),
	}, extraOpts...)
	handled, _, _, failoverErr, _, lastErr = TryUpstreamWithAllKeys(
		c,
		config.NewEnvConfig(),
		h.cfgManager,
		h.channelScheduler,
		scheduler.ChannelKindResponses,
		"Responses",
		h.messagesMetrics,
		h.upstream,
		[]warmup.URLLatencyResult{{URL: h.upstream.BaseURL, OriginalIdx: 0}},
		requestBody,
		nil,
		false,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return h.cfgManager.GetNextAPIKey(upstream, failedKeys, ChannelAPIType(scheduler.ChannelKindChat))
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
		h.channelScheduler.GetChannelLogStoreForRoute(h.executionRoute),
		opts...,
	)
	return handled, failoverErr, lastErr, w
}

// openCircuitOn 快通道阈值（5 分钟内 2 次失败）触发指定模型的熔断 open。
func (h *tryUpstreamHarness) openCircuitOn(model string) {
	tracker := h.channelScheduler.GetMetricsManagerForRoute(h.executionRoute).ModelCircuit()
	keyHash := metrics.ModelCircuitKeyHash("sk-gpt")
	tracker.RecordModelFailure("ch_gpt_chat", keyHash, model, "test failure")
	tracker.RecordModelFailure("ch_gpt_chat", keyHash, model, "test failure")
}

// TestTryUpstreamCircuitBindsExecutionModel 验证跨模型执行时模型级熔断按执行模型
// 检查 Key（A3）：执行模型熔断 → Key 被跳过、请求不发出；原模型熔断而执行模型
// 正常 → Key 放行、请求成功（旧实现读侧固定原始模型，两个方向都会判错）。
func TestTryUpstreamCircuitBindsExecutionModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	received := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		received <- req.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","object":"chat.completion","choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	requestBody := []byte(`{"model":"gpt-5.6-sol","input":"ping","stream":false}`)

	t.Run("execution model open skips key", func(t *testing.T) {
		h := newCrossModelHarness(t, server.URL)
		defer h.cleanup()
		h.openCircuitOn("gpt-5.6-openai")

		handled, _, lastErr, _ := h.run(t, requestBody)
		if handled || lastErr == nil {
			t.Fatalf("execution-model circuit open must skip the key: handled=%v lastErr=%v", handled, lastErr)
		}
		select {
		case <-received:
			t.Fatal("upstream must not receive the request while execution model is circuit-open")
		default:
		}
	})

	t.Run("original model open does not block execution", func(t *testing.T) {
		h := newCrossModelHarness(t, server.URL)
		defer h.cleanup()
		h.openCircuitOn("gpt-5.6-sol")

		handled, failoverErr, lastErr, _ := h.run(t, requestBody)
		if !handled || failoverErr != nil || lastErr != nil {
			t.Fatalf("original-model circuit must not block cross-model execution: handled=%v failoverErr=%#v lastErr=%v", handled, failoverErr, lastErr)
		}
		select {
		case model := <-received:
			if model != "gpt-5.6-openai" {
				t.Fatalf("upstream model = %q, want gpt-5.6-openai", model)
			}
		default:
			t.Fatal("upstream never received the request")
		}
	})
}

// TestTryUpstreamOverflowRedirectResponsesCrossModel 验证溢出跨模型重定向的完整主链：
// responses 执行路由上模型从 gpt-5.6-sol 改写为 gpt-5.6-big 时，发往上游的请求体
// 必须剥离 reasoning 项的 encrypted_content（保留 summary）、模型已改写，
// 且响应头回显 X-CCX-Model-Redirect（originalModel -> executionModel）。
//
// 回归背景：旧实现先执行 model = executionModel 再用
// executionModel != model 判定溢出后处理，条件恒假导致密文未剥离、
// 重定向头与日志全部缺失。
func TestTryUpstreamOverflowRedirectResponsesCrossModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	type inputItem struct {
		Type             string          `json:"type"`
		Summary          json.RawMessage `json:"summary,omitempty"`
		EncryptedContent string          `json:"encrypted_content,omitempty"`
	}
	receivedBody := make(chan struct {
		Model string      `json:"model"`
		Input []inputItem `json:"input"`
	}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string      `json:"model"`
			Input []inputItem `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		receivedBody <- req
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_test","object":"response","output":[]}`))
	}))
	defer server.Close()

	h := newCrossModelHarness(t, server.URL)
	defer h.cleanup()
	// 密文剥离仅在 responses 执行路由上发生（跨协议组合本就走转换器）。
	h.executionRoute = scheduler.ChannelRouteRef{Kind: string(scheduler.ChannelKindResponses), Index: 0, ChannelUID: "ch_gpt_chat"}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))

	requestBody := []byte(`{
	  "model": "gpt-5.6-sol",
	  "input": [
	    {"type": "message", "role": "user", "content": "hi"},
	    {"type": "reasoning", "id": "rs_1", "summary": [{"type":"summary_text","text":"thought"}], "encrypted_content": "gAAAA-secret-1"}
	  ]
	}`)

	handled, _, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
		c,
		config.NewEnvConfig(),
		h.cfgManager,
		h.channelScheduler,
		scheduler.ChannelKindResponses,
		"Responses",
		h.messagesMetrics,
		h.upstream,
		[]warmup.URLLatencyResult{{URL: h.upstream.BaseURL, OriginalIdx: 0}},
		requestBody,
		nil,
		false,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return h.cfgManager.GetNextAPIKey(upstream, failedKeys, ChannelAPIType(scheduler.ChannelKindChat))
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
		h.channelScheduler.GetChannelLogStoreForRoute(h.executionRoute),
		WithExecutionRoute(h.executionRoute),
		WithExecutionModel("gpt-5.6-big"),
		WithSelectionTrace(&scheduler.SelectionResult{OverflowRedirect: true}),
	)

	if !handled || failoverErr != nil || lastErr != nil {
		t.Fatalf("overflow redirect attempt failed: handled=%v failoverErr=%#v lastErr=%v", handled, failoverErr, lastErr)
	}

	select {
	case req := <-receivedBody:
		if req.Model != "gpt-5.6-big" {
			t.Fatalf("upstream model = %q, want gpt-5.6-big", req.Model)
		}
		if len(req.Input) != 2 {
			t.Fatalf("input items = %d, want 2", len(req.Input))
		}
		if req.Input[1].EncryptedContent != "" {
			t.Fatalf("cross-model responses redirect must strip encrypted_content, got %q", req.Input[1].EncryptedContent)
		}
		if req.Input[1].Summary == nil {
			t.Fatal("summary must be preserved when stripping encrypted_content")
		}
	default:
		t.Fatal("upstream never received the request body")
	}

	if got := w.Header().Get("X-CCX-Model-Redirect"); got != "gpt-5.6-sol -> gpt-5.6-big" {
		t.Fatalf("X-CCX-Model-Redirect = %q, want %q", got, "gpt-5.6-sol -> gpt-5.6-big")
	}
}
