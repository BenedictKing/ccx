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

// 422 可达性回归：兼容性学习块曾被嵌套在 `resp.StatusCode == 400` 守卫内，
// 导致声称支持的 422 分支永远不可达。修复后学习块的位置在 shouldFailover 判断之前，
// 与分类结果无关地先执行一次。这里用真实的 TryUpstreamWithAllKeys 驱动一次完整
// 「上游 422 developer role 报错 -> 学习 -> 同 Key 重试」闭环，而不是分别验证互不相连的
// 两个环节。

func TestCompatTraitFromErrorAcceptsUnprocessableEntity(t *testing.T) {
	body := []byte(`{"error":{"message":"messages[0].role: unknown variant ` + "`developer`" +
		`, expected one of ` + "`system`" + `, ` + "`user`" + `"}}`)

	for _, statusCode := range []int{http.StatusBadRequest, http.StatusUnprocessableEntity} {
		signal := CompatTraitFromError(statusCode, body, CompatSignalContext{HasDeveloperRole: true})
		if signal == nil {
			t.Fatalf("statusCode=%d 应识别出兼容性信号", statusCode)
		}
		if signal.Trait != config.TraitDowngradeDeveloperRole {
			t.Errorf("statusCode=%d Trait = %q, want %q", statusCode, signal.Trait, config.TraitDowngradeDeveloperRole)
		}
	}
}

func TestUnprocessableEntityReachesCompatLearningViaRealFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Cleanup(swapChannelCompatCacheForTest(config.NewChannelCompatCache()))

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error":{"message":"messages[0].role: unknown variant ` +
				"`developer`" + `, expected one of ` + "`system`" + `, ` + "`user`" + `"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "developer-role-422",
		ChannelUID:  "ch-422-developer-role",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-422-1"},
		Status:      "active",
		ServiceType: "openai",
	})
	t.Cleanup(cleanup)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	reqBody := `{"model":"gpt-5","input":[{"type":"message","role":"developer","content":"dev"}]}`
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
		messagesMetrics,
		upstream,
		[]warmup.URLLatencyResult{{URL: server.URL, OriginalIdx: 0}},
		[]byte(reqBody),
		nil,
		false,
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

	if !handled {
		t.Fatal("422 学习后同 Key 重试成功，应处理完成")
	}
	if failoverErr != nil {
		t.Fatalf("failoverErr = %#v, want nil", failoverErr)
	}
	if lastErr != nil {
		t.Fatalf("lastErr = %v, want nil", lastErr)
	}
	if successKey != "sk-422-1" {
		t.Fatalf("successKey = %q, want sk-422-1", successKey)
	}
	if callCount != 2 {
		t.Fatalf("上游调用次数 = %d, want 2（首次 422 学习后同 Key 重试一次）", callCount)
	}

	keyHash := autopilot.KeyHashFromAPIKey("sk-422-1")
	state, ok := channelCompatCache.Trait(upstream.ChannelUID, keyHash, "gpt-5", config.TraitDowngradeDeveloperRole)
	if !ok || !state.Enabled {
		t.Fatalf("应学到 developer role 降级结论, ok=%v state=%+v", ok, state)
	}
}
