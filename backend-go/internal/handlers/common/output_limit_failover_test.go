package common

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

// 回归：Kimi K2.6 在模型注册表里的公开输出上限是 262144（Moonshot 平台口径），
// 注册表 clamp 对 64000 的请求不生效；但火山方舟 coding 端点对该模型硬限 32768，
// 直接透传会 400。这里用真实的 TryUpstreamWithAllKeys 驱动完整闭环：
// 首次 400（Ark InvalidParameter）→ 学习实测上限 → 同 Key 以钳制后的 max_tokens
// 重试 → 上游 200。并验证后续请求在发送前就被主动侧钳制。

const arkMaxTokensErrorBody = `{"error":{"code":"InvalidParameter","message":"The parameter ` +
	"`max_tokens`" + ` specified in the request is not valid: integer above maximum value, ` +
	`expected a value <= 32768, but got 64000 instead. Request id: 20260825183531XXXX"}}`

func TestOutputLimitLearningRetriesWithClampedMaxTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Cleanup(swapChannelCompatCacheForTest(config.NewChannelCompatCache()))

	var (
		mu         sync.Mutex
		maxTokens  []float64
		decodeErrs []error
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxTokens float64 `json:"max_tokens"`
		}
		err := json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		maxTokens = append(maxTokens, body.MaxTokens)
		decodeErrs = append(decodeErrs, err)
		callCount := len(maxTokens)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(arkMaxTokensErrorBody))
			return
		}
		_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","content":[]}`))
	}))
	t.Cleanup(server.Close)

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "ark-coding-kimi",
		ChannelUID:  "ch-ark-coding-kimi",
		BaseURL:     server.URL,
		APIKeys:     []string{"ark-test-1"},
		Status:      "active",
		ServiceType: "claude",
	})
	t.Cleanup(cleanup)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	reqBody := `{"model":"kimi-k2.6","max_tokens":64000,"messages":[{"role":"user","content":"hi"}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(reqBody))

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
		[]byte(reqBody),
		nil,
		false,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, "Messages")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			body := GetEffectiveRequestBody(c, []byte(reqBody))
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL+"/v1/messages", strings.NewReader(string(body)))
		},
		func(apiKey string) {},
		func(url string) {},
		func(url string) {},
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			_ = resp.Body.Close()
			return nil, nil
		},
		"kimi-k2.6",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
	)

	if !handled || failoverErr != nil || lastErr != nil {
		t.Fatalf("学习后同 Key 重试应成功, handled=%v failoverErr=%#v lastErr=%v", handled, failoverErr, lastErr)
	}
	if successKey != "ark-test-1" {
		t.Fatalf("successKey = %q, want ark-test-1", successKey)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(maxTokens) != 2 {
		t.Fatalf("上游调用次数 = %d, want 2", len(maxTokens))
	}
	if decodeErrs[0] != nil || decodeErrs[1] != nil {
		t.Fatalf("请求体解析失败: %v", decodeErrs)
	}
	if maxTokens[0] != 64000 {
		t.Fatalf("首次请求 max_tokens = %v, want 64000（注册表公开上限 262144 不应触发钳制）", maxTokens[0])
	}
	if maxTokens[1] != 32768 {
		t.Fatalf("重试请求 max_tokens = %v, want 32768（应按实测上限钳制）", maxTokens[1])
	}

	keyHash := autopilot.KeyHashFromAPIKey("ark-test-1")
	state, ok := channelCompatCache.OutputLimit(upstream.ChannelUID, keyHash, "kimi-k2.6")
	if !ok || state.MaxOutputTokens != 32768 {
		t.Fatalf("应学到输出上限 32768, ok=%v state=%+v", ok, state)
	}
	if state.RejectedTokens != 64000 {
		t.Errorf("RejectedTokens = %d, want 64000", state.RejectedTokens)
	}
}

// 主动侧回归：已有学习记忆时，新请求应在发送前就被钳制，首轮直达上游成功，
// 不再消耗一次 400 往返。
func TestOutputLimitLearnedMemoryClampsBeforeSend(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Cleanup(swapChannelCompatCacheForTest(config.NewChannelCompatCache()))

	var (
		mu        sync.Mutex
		maxTokens []float64
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxTokens float64 `json:"max_tokens"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		maxTokens = append(maxTokens, body.MaxTokens)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","content":[]}`))
	}))
	t.Cleanup(server.Close)

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "ark-coding-kimi",
		ChannelUID:  "ch-ark-coding-kimi",
		BaseURL:     server.URL,
		APIKeys:     []string{"ark-test-1"},
		Status:      "active",
		ServiceType: "claude",
	})
	t.Cleanup(cleanup)

	// 预置学习记忆：该渠道-Key-模型实测输出上限 32768
	keyHash := autopilot.KeyHashFromAPIKey("ark-test-1")
	if !channelCompatCache.RecordOutputLimit("ch-ark-coding-kimi", keyHash, "kimi-k2.6", 32768,
		config.CompatSourceUpstreamDeclared, "expected a value <= 32768", 64000) {
		t.Fatal("预置学习记忆失败")
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	reqBody := `{"model":"kimi-k2.6","max_tokens":64000,"messages":[{"role":"user","content":"hi"}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(reqBody))

	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]

	handled, _, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
		c,
		config.NewEnvConfig(),
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindMessages,
		"Messages",
		messagesMetrics,
		upstream,
		[]warmup.URLLatencyResult{{URL: server.URL, OriginalIdx: 0}},
		[]byte(reqBody),
		nil,
		false,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, "Messages")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			body := GetEffectiveRequestBody(c, []byte(reqBody))
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL+"/v1/messages", strings.NewReader(string(body)))
		},
		func(apiKey string) {},
		func(url string) {},
		func(url string) {},
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			_ = resp.Body.Close()
			return nil, nil
		},
		"kimi-k2.6",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
	)

	if !handled || failoverErr != nil || lastErr != nil {
		t.Fatalf("应首轮成功, handled=%v failoverErr=%#v lastErr=%v", handled, failoverErr, lastErr)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(maxTokens) != 1 {
		t.Fatalf("上游调用次数 = %d, want 1（记忆命中应免于先撞一次 400）", len(maxTokens))
	}
	if maxTokens[0] != 32768 {
		t.Fatalf("发送的 max_tokens = %v, want 32768（应被主动侧钳制）", maxTokens[0])
	}
}
