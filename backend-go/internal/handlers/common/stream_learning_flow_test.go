package common

import (
	"io"
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
	"github.com/tidwall/gjson"
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

// 预置学习结论后，非流式请求应做流式桥接改写：上游收到 stream:true 的请求体并按
// SSE 正常返回（上游计数 1，无额外失败往返）。gemini 入口不支持桥接，退回发送前跳过。
func TestStreamRequirementRewritesRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Cleanup(swapChannelCompatCacheForTest(config.NewChannelCompatCache()))

	const (
		channelUID = "ch-stream-bridge"
		apiKey     = "sk-stream-2"
		model      = "gpt-5"
	)

	var sawStreamTrue, sawIncludeUsage bool
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		body, _ := io.ReadAll(r.Body)
		if gjson.GetBytes(body, "stream").Bool() {
			sawStreamTrue = true
		}
		if gjson.GetBytes(body, "stream_options.include_usage").Bool() {
			sawIncludeUsage = true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-5\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"},\"finish_reason\":null}]}\n" +
			"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n" +
			"data: [DONE]\n"))
	}))
	t.Cleanup(server.Close)

	keyHash := autopilot.KeyHashFromAPIKey(apiKey)
	channelCompatCache.Record(channelUID, keyHash, model, config.TraitRequiresStream, true,
		config.CompatSourceErrorSignal, "预置测试结论")

	cfgManager, channelScheduler, responsesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "stream-bridge-rewrite",
		ChannelUID:  channelUID,
		BaseURL:     server.URL,
		APIKeys:     []string{apiKey},
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
			// 模拟真实 handler 闭包：从 context 读取改写后的请求体（流式桥接改写经
			// RestoreRequestBody 落到 requestBodyBytes，此处才可观测）。
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL,
				strings.NewReader(string(GetEffectiveRequestBody(c, []byte(reqBody)))))
		},
		func(apiKey string) {},
		func(url string) {},
		func(url string) {},
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			body, _ := io.ReadAll(resp.Body)
			if !strings.Contains(string(body), `"finish_reason":"stop"`) {
				t.Errorf("上游响应应为 SSE 流, got: %s", body)
			}
			return nil, nil
		},
		model,
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindResponses),
	)

	if !handled {
		t.Fatalf("流式桥接改写后请求应成功交付, lastErr=%v failoverErr=%#v", lastErr, failoverErr)
	}
	if successKey != apiKey {
		t.Fatalf("successKey = %q, want %q", successKey, apiKey)
	}
	if callCount != 1 {
		t.Fatalf("上游调用次数 = %d, want 1", callCount)
	}
	if !sawStreamTrue {
		t.Fatal("上游应收到 stream:true 的请求体")
	}
	// responses 体不附 stream_options（仅 chat 体）
	if sawIncludeUsage {
		t.Fatal("responses 体不应附 stream_options")
	}
}

// gemini 入口请求体无顶层 stream 字段语义，不支持桥接：预置学习结论后发送前直接跳过，
// 不消耗必然失败的上游往返。
func TestStreamRequirementSkipsUnsupportedExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Cleanup(swapChannelCompatCacheForTest(config.NewChannelCompatCache()))

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(streamRequiredErrorBody))
	}))
	t.Cleanup(server.Close)

	const (
		channelUID = "ch-stream-skip-gemini"
		apiKey     = "sk-stream-4"
		model      = "gemini-3.8-flash"
	)
	keyHash := autopilot.KeyHashFromAPIKey(apiKey)
	channelCompatCache.Record(channelUID, keyHash, model, config.TraitRequiresStream, true,
		config.CompatSourceErrorSignal, "预置测试结论")

	cfgManager, channelScheduler, geminiMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "stream-required-skip-gemini",
		ChannelUID:  channelUID,
		BaseURL:     server.URL,
		APIKeys:     []string{apiKey},
		Status:      "active",
		ServiceType: "gemini",
	})
	t.Cleanup(cleanup)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	reqBody := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3.8-flash:generateContent", strings.NewReader(reqBody))

	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]

	handled, _, _, _, _, _ := TryUpstreamWithAllKeys(
		c,
		config.NewEnvConfig(),
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindGemini,
		"Gemini",
		geminiMetrics,
		upstream,
		[]warmup.URLLatencyResult{{URL: server.URL, OriginalIdx: 0}},
		[]byte(reqBody),
		nil,
		false, // 非流式请求
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, "Gemini")
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
		model,
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindGemini),
	)

	if handled {
		t.Fatal("不支持桥接的入口应跳过该 Key 而非发送请求")
	}
	if callCount != 0 {
		t.Fatalf("上游调用次数 = %d, want 0（发送前跳过）", callCount)
	}
	// 全部 Key 被跳过时循环自然退出（与 circuit skip 形态一致，返回全 nil 由上层
	// HandleAllChannelsFailed 兜底 503），跳过原因已由 RequestLogf 记录。
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
