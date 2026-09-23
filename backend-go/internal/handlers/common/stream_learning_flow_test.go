package common

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

const streamRequiredErrorBody = `{"error":{"message":"streaming is required: this endpoint only accepts \"stream\": true"}}`

// newStreamRequirementTestServers 构建两个上游：bad 恒返回「仅接受流式」400，
// good 返回成功响应。返回各自调用计数便于断言往返次数。
func newStreamRequirementTestServers(t *testing.T) (badURL, goodURL string, badCalls, goodCalls *int) {
	t.Helper()
	badCount, goodCount := 0, 0
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		badCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(streamRequiredErrorBody))
	}))
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		goodCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(bad.Close)
	t.Cleanup(good.Close)
	return bad.URL, good.URL, &badCount, &goodCount
}

// 修复前行为回归：该 400 文案含 "is required" 被判成 schema 校验错误，
// 走「非 failover 错误」分支直接把 400 终结给客户端，既不学习也不换渠道。
// 修复后：学习 requires_stream 结论，并按 failover 语义返回（交由上层换渠道）。
func TestStreamRequirementLearnsAndFailsOver(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Cleanup(swapChannelCompatCacheForTest(config.NewChannelCompatCache()))

	badURL, _, badCalls, _ := newStreamRequirementTestServers(t)

	cfgManager, channelScheduler, responsesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "stream-required-400",
		ChannelUID:  "ch-stream-required",
		BaseURL:     badURL,
		APIKeys:     []string{"sk-stream-1"},
		Status:      "active",
		ServiceType: "openai",
	})
	t.Cleanup(cleanup)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	reqBody := `{"model":"gpt-5","input":[{"type":"message","role":"user","content":"hi"}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(reqBody))

	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]

	handled, successKey, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
		c,
		config.NewEnvConfig(),
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindResponses,
		"Responses",
		responsesMetrics,
		upstream,
		[]warmup.URLLatencyResult{{URL: badURL, OriginalIdx: 0}},
		[]byte(reqBody),
		nil,
		false, // 非流式请求
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, "Responses")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, strings.NewReader(reqBody))
		},
		func(apiKey string) {},
		func(url string) {},
		func(url string) {},
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			return nil, nil
		},
		"gpt-5",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses),
	)

	if handled {
		t.Fatal("不应在本层终结请求：该错误已放行 failover，应返回 failover 意图交由上层换渠道")
	}
	if failoverErr == nil || failoverErr.Status != http.StatusBadRequest {
		t.Fatalf("failoverErr = %#v, want Status=400", failoverErr)
	}
	_ = successKey
	_ = lastErr
	if *badCalls != 1 {
		t.Fatalf("bad 上游调用次数 = %d, want 1", *badCalls)
	}

	keyHash := autopilot.KeyHashFromAPIKey("sk-stream-1")
	state, ok := channelCompatCache.Trait(upstream.ChannelUID, keyHash, "gpt-5", config.TraitRequiresStream)
	if !ok || !state.Enabled {
		t.Fatalf("应学到 requires_stream 结论, ok=%v state=%+v", ok, state)
	}
}

// 预置学习结论后，非流式请求应在发送前直接跳过该 Key，不消耗必然失败的上游往返；
// 同渠道其余 Key（可能路由到支持非流式的后端分组）照常交付。
// 上游按授权 Key 区分行为：sk-stream-2a 已学习仅流式（请求根本不该发出），
// sk-stream-2b 正常服务。
func TestStreamRequirementSkipAvoidsUpstreamRoundtrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Cleanup(swapChannelCompatCacheForTest(config.NewChannelCompatCache()))

	const (
		channelUID = "ch-stream-skip"
		skippedKey = "sk-stream-2a"
		servingKey = "sk-stream-2b"
		model      = "gpt-5"
	)

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		callCount++
		if auth == skippedKey {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(streamRequiredErrorBody))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	keyHash := autopilot.KeyHashFromAPIKey(skippedKey)
	channelCompatCache.Record(channelUID, keyHash, model, config.TraitRequiresStream, true,
		config.CompatSourceErrorSignal, "预置测试结论")

	cfgManager, channelScheduler, responsesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "stream-required-skip",
		ChannelUID:  channelUID,
		BaseURL:     server.URL,
		APIKeys:     []string{skippedKey, servingKey},
		Status:      "active",
		ServiceType: "openai",
	})
	t.Cleanup(cleanup)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	reqBody := `{"model":"gpt-5","input":[{"type":"message","role":"user","content":"hi"}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(reqBody))

	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]

	handled, successKey, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
		c,
		config.NewEnvConfig(),
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindResponses,
		"Responses",
		responsesMetrics,
		upstream,
		[]warmup.URLLatencyResult{{URL: server.URL, OriginalIdx: 0}},
		[]byte(reqBody),
		nil,
		false, // 非流式请求
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, "Responses")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, strings.NewReader(reqBody))
			if err != nil {
				return nil, err
			}
			req.Header.Set("Authorization", "Bearer "+apiKey)
			return req, nil
		},
		func(apiKey string) {},
		func(url string) {},
		func(url string) {},
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			return nil, nil
		},
		model,
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses),
	)

	if !handled {
		t.Fatalf("跳过已学习 Key 后应由同渠道其余 Key 成功交付, lastErr=%v failoverErr=%#v", lastErr, failoverErr)
	}
	if successKey != servingKey {
		t.Fatalf("successKey = %q, want %q", successKey, servingKey)
	}
	if callCount != 1 {
		t.Fatalf("上游调用次数 = %d, want 1（被跳过 Key 零往返，仅 serving Key 一次成功调用）", callCount)
	}
}

// 防误判门控：流式请求（isStream=true）收到同样的 400 不学习结论
// （流式请求携带 stream:true，正常上游不会报此错，命中说明另有原因），
// 但错误分类放行与请求流式无关，仍按 failover 语义返回。
func TestStreamRequirementNotLearnedForStreamingRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Cleanup(swapChannelCompatCacheForTest(config.NewChannelCompatCache()))

	badURL, _, badCalls, _ := newStreamRequirementTestServers(t)

	cfgManager, channelScheduler, responsesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "stream-required-streaming",
		ChannelUID:  "ch-stream-streaming",
		BaseURL:     badURL,
		APIKeys:     []string{"sk-stream-3"},
		Status:      "active",
		ServiceType: "openai",
	})
	t.Cleanup(cleanup)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	reqBody := `{"model":"gpt-5","input":[{"type":"message","role":"user","content":"hi"}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(reqBody))

	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]

	handled, _, _, failoverErr, _, _ := TryUpstreamWithAllKeys(
		c,
		config.NewEnvConfig(),
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindResponses,
		"Responses",
		responsesMetrics,
		upstream,
		[]warmup.URLLatencyResult{{URL: badURL, OriginalIdx: 0}},
		[]byte(reqBody),
		nil,
		true, // 流式请求
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, "Responses")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, strings.NewReader(reqBody))
		},
		func(apiKey string) {},
		func(url string) {},
		func(url string) {},
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			return nil, nil
		},
		"gpt-5",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses),
	)

	if handled {
		t.Fatal("流式请求同样应按 failover 语义返回")
	}
	if failoverErr == nil || failoverErr.Status != http.StatusBadRequest {
		t.Fatalf("failoverErr = %#v, want Status=400", failoverErr)
	}
	if *badCalls != 1 {
		t.Fatalf("bad 上游调用次数 = %d, want 1", *badCalls)
	}

	keyHash := autopilot.KeyHashFromAPIKey("sk-stream-3")
	if _, ok := channelCompatCache.Trait(upstream.ChannelUID, keyHash, "gpt-5", config.TraitRequiresStream); ok {
		t.Fatal("流式请求不应学习 requires_stream 结论")
	}
}
