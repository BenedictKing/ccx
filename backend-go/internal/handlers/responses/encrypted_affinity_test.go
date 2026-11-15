package responses

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/gin-gonic/gin"
)

func newEncryptedAffinityTestScheduler(t *testing.T, upstreams []config.UpstreamConfig) (*config.ConfigManager, *scheduler.ChannelScheduler) {
	t.Helper()
	cm := setupResponsesTestConfigManager(t, upstreams)
	managers := make([]*metrics.MetricsManager, 5)
	for i := range managers {
		managers[i] = metrics.NewMetricsManager()
		t.Cleanup(managers[i].Stop)
	}
	s := scheduler.NewChannelScheduler(cm, managers[0], managers[1], managers[2], managers[3], managers[4], session.NewTraceAffinityManager(), nil)
	return cm, s
}

func TestResponsesEncryptedContinuationKeepsSuccessfulChannelAndCanFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var primaryCalls, affinityCalls atomic.Int32
	var affinityUnavailable atomic.Bool
	const plainInput = `[{"type":"message","role":"user","content":"continue"}]`
	const encryptedInput = `[{"type":"reasoning","id":"rs_test","encrypted_content":"sealed+/=="},{"type":"message","role":"user","content":"continue"}]`
	const response = `{"id":"resp_test","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if primaryCalls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"message":"temporarily unavailable"}}`)
			return
		}
		_, _ = io.WriteString(w, response)
	}))
	defer primary.Close()
	affinity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := affinityCalls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if call > 1 && !strings.Contains(string(body), `"encrypted_content":"sealed+/=="`) {
			t.Error("密文被改写")
		}
		w.Header().Set("Content-Type", "application/json")
		if affinityUnavailable.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":{"message":"temporarily unavailable"}}`)
			return
		}
		_, _ = io.WriteString(w, response)
	}))
	defer affinity.Close()
	cm, s := newEncryptedAffinityTestScheduler(t, []config.UpstreamConfig{
		{Name: "primary", BaseURL: primary.URL, APIKeys: []string{"primary"}, ServiceType: "responses", Status: "active", Priority: 1, CircuitBreakerDisabled: true},
		{Name: "affinity", BaseURL: affinity.URL, APIKeys: []string{"affinity"}, ServiceType: "responses", Status: "active", Priority: 4, CircuitBreakerDisabled: true},
	})
	router := gin.New()
	router.POST("/v1/responses", Handler(&config.EnvConfig{ProxyAccessKey: "secret", MaxRequestBodySize: 1 << 20}, cm, session.NewSessionManager(time.Hour, 100, 100000), s))
	request := func(input string) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"gpt-6.1-sol","input":`+input+`}`))
		r.Header.Set("x-api-key", "secret")
		r.Header.Set("Session_id", "encrypted-session")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("请求失败: %d %s", w.Code, w.Body.String())
		}
	}
	request(plainInput)     // 首选故障，备用成功并建立亲和。
	request(encryptedInput) // 首选已恢复，加密续轮仍应复用备用。
	if primaryCalls.Load() != 1 || affinityCalls.Load() != 2 {
		t.Fatalf("加密续轮切回首选: primary=%d affinity=%d", primaryCalls.Load(), affinityCalls.Load())
	}
	request(plainInput) // 普通请求仍允许自动优先级覆盖。
	if primaryCalls.Load() != 2 || affinityCalls.Load() != 2 {
		t.Fatalf("普通请求未恢复优先级选择: primary=%d affinity=%d", primaryCalls.Load(), affinityCalls.Load())
	}
	// 重新建立备用亲和，验证它故障时仍可切换渠道。
	requirement := common.BuildResponsesContextRequirement([]byte(`{"model":"gpt-6.1-sol","input":`+encryptedInput+`}`), cm.GetConfig().ContextRouting)
	s.SetTraceAffinityForRequirement("encrypted-session", 1, scheduler.ChannelKindResponses, requirement)
	affinityUnavailable.Store(true)
	request(encryptedInput)
	if primaryCalls.Load() != 3 || affinityCalls.Load() != 3 {
		t.Fatalf("亲和渠道失败后未切换: primary=%d affinity=%d", primaryCalls.Load(), affinityCalls.Load())
	}
}

func TestResponsesWebSocketEncryptedContinuationPreservesAffinity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cm, s := newEncryptedAffinityTestScheduler(t, []config.UpstreamConfig{
		{Name: "primary", BaseURL: "https://primary.example", APIKeys: []string{"primary"}, ServiceType: "responses", Status: "active", Priority: 1},
		{Name: "affinity", BaseURL: "https://affinity.example", APIKeys: []string{"affinity"}, ServiceType: "responses", Status: "active", Priority: 4},
	})
	for _, encrypted := range []bool{false, true} {
		input := `[{"type":"message","role":"user","content":"continue"}]`
		want := 0
		if encrypted {
			input = `[{"type":"reasoning","encrypted_content":"sealed"}]`
			want = 1
		}
		payload := []byte(`{"type":"response.create","model":"gpt-6.1-sol","input":` + input + `}`)
		requirement := common.BuildResponsesContextRequirement(payload, cm.GetConfig().ContextRouting)
		s.SetTraceAffinityForRequirement("ws-session", 1, scheduler.ChannelKindResponses, requirement)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
		c.Request.Header.Set("Session_id", "ws-session")
		got, err := selectResponsesWebSocketChannel(c, cm, s, payload)
		if err != nil {
			t.Fatal(err)
		}
		if got.ChannelIndex != want {
			t.Fatalf("encrypted=%v 选择渠道 %d，期望 %d", encrypted, got.ChannelIndex, want)
		}
	}
}
