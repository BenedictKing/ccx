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

// TestUnsupportedBetaHeaderLearnsAndRetries 用真实 TryUpstreamWithAllKeys 驱动一次完整
// 「上游 400 anthropic-beta 拒绝 -> 学习 -> 同 Key 重试」闭环，断言：
//  1. 第一次调用返 400 + "尚未验证或不支持的 anthropic-beta：context-1m-2025-08-07"
//  2. 学习到 unsupported_beta_header trait，record 返回 true
//  3. 同 Key 立即重试（successKey 不变）
//  4. 重试时 provider 剥离被拒 token（通过自定义 buildRequest 捕获并断言 header）
func TestUnsupportedBetaHeaderLearnsAndRetries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Cleanup(swapChannelCompatCacheForTest(config.NewChannelCompatCache()))

	callCount := 0
	var secondRequestBetaHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"尚未验证或不支持的 anthropic-beta：context-1m-2025-08-07"},"request_id":"trace_xxx"}`))
			return
		}
		// 第二次请求：捕获 anthropic-beta header 值，断言被拒 token 已剥离
		secondRequestBetaHeader = r.Header.Get("Anthropic-Beta")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "beta-header-400",
		ChannelUID:  "ch-400-beta-header",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-beta-1"},
		Status:      "active",
		ServiceType: "claude",
	})
	t.Cleanup(cleanup)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	reqBody := `{"model":"claude-opus-4-8","messages":[{"role":"user","content":"hi"}]}`
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(reqBody))
	// 客户端携带 anthropic-beta header（含被拒 token 与无关 token）
	c.Request.Header.Set("Anthropic-Beta", "context-1m-2025-08-07,prompt-caching-scope-2026-01-05")

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
			// 模拟 ClaudeProvider 的 header 透传 + trait 注入剥离链路
			req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, strings.NewReader(reqBody))
			if err != nil {
				return nil, err
			}
			// 透传客户端 anthropic-beta
			if clientBeta := c.Request.Header.Get("Anthropic-Beta"); clientBeta != "" {
				req.Header.Set("Anthropic-Beta", clientBeta)
			}
			// 模拟 provider 在 ConvertToProviderRequest 里的剥离动作
			// （真实剥离发生在 providers/claude.go 的 stripUnsupportedBetaHeaderTokens）
			if upstreamCopy.IsUnsupportedBetaHeaderEnabled() {
				rejected := upstreamCopy.GetLearnedRejectedBetaTokens()
				if len(rejected) > 0 {
					// 按 token 粒度剥离（与 provider 实现一致）
					parts := strings.Split(req.Header.Get("Anthropic-Beta"), ",")
					rejectedSet := make(map[string]struct{}, len(rejected))
					for _, t := range rejected {
						rejectedSet[t] = struct{}{}
					}
					kept := make([]string, 0, len(parts))
					for _, p := range parts {
						p = strings.TrimSpace(p)
						if _, drop := rejectedSet[p]; !drop && p != "" {
							kept = append(kept, p)
						}
					}
					if len(kept) > 0 {
						req.Header.Set("Anthropic-Beta", strings.Join(kept, ","))
					} else {
						req.Header.Del("Anthropic-Beta")
					}
				} else {
					req.Header.Del("Anthropic-Beta")
				}
			}
			return req, nil
		},
		func(apiKey string) {},
		func(url string) {},
		func(url string) {},
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			return nil, nil
		},
		"claude-opus-4-8",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
	)

	if !handled {
		t.Fatal("400 anthropic-beta 拒绝学习后同 Key 重试成功，应处理完成")
	}
	if failoverErr != nil {
		t.Fatalf("failoverErr = %#v, want nil", failoverErr)
	}
	if lastErr != nil {
		t.Fatalf("lastErr = %v, want nil", lastErr)
	}
	if successKey != "sk-beta-1" {
		t.Fatalf("successKey = %q, want sk-beta-1", successKey)
	}
	if callCount != 2 {
		t.Fatalf("上游调用次数 = %d, want 2（首次 400 学习后同 Key 重试一次）", callCount)
	}

	// 断言学到 unsupported_beta_header trait
	keyHash := autopilot.KeyHashFromAPIKey("sk-beta-1")
	state, ok := channelCompatCache.Trait(upstream.ChannelUID, keyHash, "claude-opus-4-8", config.TraitUnsupportedBetaHeader)
	if !ok || !state.Enabled {
		t.Fatalf("应学到 unsupported_beta_header 结论, ok=%v state=%+v", ok, state)
	}

	// 断言第二次请求的 anthropic-beta header 已剥离 context-1m-2025-08-07
	if secondRequestBetaHeader == "" {
		t.Fatal("第二次请求应带 anthropic-beta header（剥离后剩余无关 token），实际为空")
	}
	if strings.Contains(secondRequestBetaHeader, "context-1m-2025-08-07") {
		t.Fatalf("第二次请求不应含被拒 token context-1m-2025-08-07，实际 header=%q", secondRequestBetaHeader)
	}
	if !strings.Contains(secondRequestBetaHeader, "prompt-caching-scope-2026-01-05") {
		t.Fatalf("第二次请求应保留无关 token prompt-caching-scope-2026-01-05，实际 header=%q", secondRequestBetaHeader)
	}
}

// TestExtractRejectedBetaTokens 校验从错误文案中提取被拒 token 名的启发式规则。
func TestExtractRejectedBetaTokens(t *testing.T) {
	tests := []struct {
		name     string
		evidence string
		want     []string
	}{
		{
			name:     "中文冒号格式",
			evidence: "尚未验证或不支持的 anthropic-beta：context-1m-2025-08-07",
			want:     []string{"context-1m-2025-08-07"},
		},
		{
			name:     "英文反引号格式",
			evidence: "anthropic-beta `prompt-caching-scope-2026-01-05` is not enabled for this API key",
			want:     []string{"prompt-caching-scope-2026-01-05"},
		},
		{
			name:     "英文冒号格式",
			evidence: "unsupported anthropic-beta header: interleaved-thinking-2025-05-14",
			want:     []string{"interleaved-thinking-2025-05-14"},
		},
		{
			name:     "无 token 名（拒绝配置但文案不点名 token）",
			evidence: "unsupported anthropic-beta configuration",
			want:     nil,
		},
		{
			name:     "空 Evidence",
			evidence: "",
			want:     nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractRejectedBetaTokens(tt.evidence)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("got[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
