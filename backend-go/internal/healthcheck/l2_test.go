package healthcheck

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/utils"
)

// genCapture 记录生成端点收到的请求（L2 真实调用断言用）
type genCapture struct {
	mu     sync.Mutex
	paths  []string
	models []string
}

func (c *genCapture) add(path, model string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paths = append(c.paths, path)
	c.models = append(c.models, model)
}

func (c *genCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.paths)
}

func (c *genCapture) lastModel() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.models) == 0 {
		return ""
	}
	return c.models[len(c.models)-1]
}

func (c *genCapture) lastPath() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.paths) == 0 {
		return ""
	}
	return c.paths[len(c.paths)-1]
}

// 各协议 SSE 成功响应（参照能力测试测试里 mock SSE 的写法）
const (
	l2SSEChat      = "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n"
	l2SSEMessages  = "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"
	l2SSEResponses = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"
	l2SSEGemini    = "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]}}]}\n\n"
)

// newL2Server mock 上游：/v1/models 返回模型列表，其余路径视为生成端点。
// genStatus 非 200 时生成端点返回该状态与 genBody；200 时按路径返回对应协议的 SSE。
func newL2Server(t *testing.T, modelsStatus int, modelsBody string, genStatus int, genBody string, cap *genCapture) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(modelsStatus)
			_, _ = w.Write([]byte(modelsBody))
			return
		}

		// 生成端点：记录 path 与请求体中的 model（gemini 的 model 在 URL 上）
		model := ""
		if r.Body != nil {
			var payload struct {
				Model string `json:"model"`
			}
			var body []byte
			body, _ = io.ReadAll(r.Body)
			if json.Unmarshal(body, &payload) == nil {
				model = payload.Model
			}
		}
		if model == "" && strings.HasPrefix(r.URL.Path, "/v1beta/models/") {
			model = strings.TrimPrefix(strings.SplitN(r.URL.Path, ":", 2)[0], "/v1beta/models/")
		}
		cap.add(r.URL.Path, model)

		if genStatus != http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(genStatus)
			_, _ = w.Write([]byte(genBody))
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		switch {
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			_, _ = w.Write([]byte(l2SSEChat))
		case strings.HasSuffix(r.URL.Path, "/messages"):
			_, _ = w.Write([]byte(l2SSEMessages))
		case strings.HasSuffix(r.URL.Path, "/responses"):
			_, _ = w.Write([]byte(l2SSEResponses))
		case strings.Contains(r.URL.Path, ":streamGenerateContent"):
			_, _ = w.Write([]byte(l2SSEGemini))
		default:
			_, _ = w.Write([]byte(l2SSEChat))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func floatPtr(v float64) *float64 { return &v }

func pricedCapability(input, output float64) config.UpstreamModelCapability {
	return config.UpstreamModelCapability{
		Pricing: &config.ModelPricing{
			InputCacheMissPrice: floatPtr(input),
			OutputPrice:         floatPtr(output),
		},
	}
}

// l2Fixture L2 测试夹具：配置只含指定类型的一个渠道
type l2Fixture struct {
	*checkKeyFixture
	cfg       config.Config
	upstreams []config.UpstreamConfig
}

func newL2Fixture(channelType string, channel config.UpstreamConfig, capabilities map[string]config.UpstreamModelCapability) *l2Fixture {
	f := &l2Fixture{checkKeyFixture: newCheckKeyFixture()}
	f.upstreams = []config.UpstreamConfig{channel}
	f.cfg = config.Config{UpstreamModelCapabilities: capabilities}
	switch channelType {
	case "messages":
		f.cfg.Upstream = f.upstreams
	case "chat":
		f.cfg.ChatUpstream = f.upstreams
	case "responses":
		f.cfg.ResponsesUpstream = f.upstreams
	case "gemini":
		f.cfg.GeminiUpstream = f.upstreams
	case "images":
		f.cfg.ImagesUpstream = f.upstreams
	case "vectors":
		f.cfg.VectorsUpstream = f.upstreams
	}
	f.manager.getConfig = func() config.Config { return f.cfg }
	f.manager.RegisterL1Fetcher(channelType, testWrappedFetcher())
	return f
}

func (f *l2Fixture) l2Record(t *testing.T, channelType string) *metrics.KeyHealthRecord {
	t.Helper()
	recs, err := f.store.GetKeyHealthForChannel(channelType, "0")
	if err != nil {
		t.Fatalf("读取记录失败: %v", err)
	}
	for i := range recs {
		if recs[i].CheckKind == CheckKindL2 {
			return &recs[i]
		}
	}
	return nil
}

func (f *l2Fixture) l1Record(t *testing.T, channelType string) *metrics.KeyHealthRecord {
	t.Helper()
	recs, err := f.store.GetKeyHealthForChannel(channelType, "0")
	if err != nil {
		t.Fatalf("读取记录失败: %v", err)
	}
	for i := range recs {
		if recs[i].CheckKind == CheckKindL1 {
			return &recs[i]
		}
	}
	return nil
}

// TestCheckChannelL2四协议成功：VerifyRealCall=true 时对 L1 成功的 key 发 L2 并落 ok 记录（detail 含模型名）
func TestCheckChannelL2四协议成功(t *testing.T) {
	capabilities := map[string]config.UpstreamModelCapability{
		"test-model": pricedCapability(1, 2),
	}
	tests := []struct {
		name        string
		channelType string
		serviceType string
		wantGenPath string
	}{
		{name: "messages", channelType: "messages", serviceType: "claude", wantGenPath: "/v1/messages"},
		{name: "chat", channelType: "chat", serviceType: "openai", wantGenPath: "/v1/chat/completions"},
		{name: "responses", channelType: "responses", serviceType: "openai", wantGenPath: "/v1/responses"},
		{name: "gemini", channelType: "gemini", serviceType: "gemini", wantGenPath: "/v1beta/models/test-model:streamGenerateContent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cap := &genCapture{}
			srv := newL2Server(t, 200, `{"data":[{"id":"test-model"}]}`, 200, "", cap)
			f := newL2Fixture(tt.channelType, config.UpstreamConfig{
				Name:        "ch0",
				BaseURL:     srv.URL,
				APIKeys:     []string{"sk-l2-key-0001"},
				Status:      "active",
				ServiceType: tt.serviceType,
				HealthCheck: &config.ChannelHealthCheckConfig{VerifyRealCall: boolPtr(true)},
			}, capabilities)

			f.manager.checkChannel(tt.channelType, 0)

			l1 := f.l1Record(t, tt.channelType)
			if l1 == nil || l1.LastStatus != StatusOK {
				t.Fatalf("L1 记录异常: %+v", l1)
			}
			l2 := f.l2Record(t, tt.channelType)
			if l2 == nil {
				t.Fatal("缺少 l2 记录")
			}
			if l2.LastStatus != StatusOK {
				t.Fatalf("l2 LastStatus = %q, 期望 ok (detail=%s)", l2.LastStatus, l2.Detail)
			}
			if l2.Detail != "model=test-model" {
				t.Fatalf("l2 Detail = %q, 期望 model=test-model", l2.Detail)
			}
			if l2.ModelCount != 0 {
				t.Fatalf("l2 ModelCount = %d, 期望 0", l2.ModelCount)
			}
			if l2.ConsecutiveFailures != 0 {
				t.Fatalf("l2 ConsecutiveFailures = %d, 期望 0", l2.ConsecutiveFailures)
			}
			if cap.count() != 1 || cap.lastPath() != tt.wantGenPath {
				t.Fatalf("生成端点调用异常: count=%d, lastPath=%s, 期望 %s", cap.count(), cap.lastPath(), tt.wantGenPath)
			}
			if cap.lastModel() != "test-model" {
				t.Fatalf("L2 请求模型 = %q, 期望 test-model", cap.lastModel())
			}
		})
	}
}

// TestCheckChannelL2选最便宜模型：多模型不同定价时选 input+output 单价最低者
func TestCheckChannelL2选最便宜模型(t *testing.T) {
	capabilities := map[string]config.UpstreamModelCapability{
		"expensive-model": pricedCapability(10, 30),
		"cheap-model":     pricedCapability(1, 2),
		"mid-model":       pricedCapability(5, 5),
	}
	cap := &genCapture{}
	srv := newL2Server(t, 200, `{"data":[{"id":"expensive-model"},{"id":"cheap-model"},{"id":"mid-model"}]}`, 200, "", cap)
	f := newL2Fixture("chat", config.UpstreamConfig{
		Name:        "ch0",
		BaseURL:     srv.URL,
		APIKeys:     []string{"sk-l2-key-0002"},
		Status:      "active",
		ServiceType: "openai",
		HealthCheck: &config.ChannelHealthCheckConfig{VerifyRealCall: boolPtr(true)},
	}, capabilities)

	f.manager.checkChannel("chat", 0)

	if got := cap.lastModel(); got != "cheap-model" {
		t.Fatalf("自动选模型 = %q, 期望 cheap-model", got)
	}
}

// TestCheckChannelL2选模型遵循SupportedModels约束：最便宜模型被白名单排除时选次便宜
func TestCheckChannelL2选模型遵循SupportedModels约束(t *testing.T) {
	capabilities := map[string]config.UpstreamModelCapability{
		"expensive-model": pricedCapability(10, 30),
		"cheap-model":     pricedCapability(1, 2),
	}
	cap := &genCapture{}
	srv := newL2Server(t, 200, `{"data":[{"id":"expensive-model"},{"id":"cheap-model"}]}`, 200, "", cap)
	f := newL2Fixture("chat", config.UpstreamConfig{
		Name:            "ch0",
		BaseURL:         srv.URL,
		APIKeys:         []string{"sk-l2-key-0003"},
		Status:          "active",
		ServiceType:     "openai",
		SupportedModels: []string{"expensive-*"},
		HealthCheck:     &config.ChannelHealthCheckConfig{VerifyRealCall: boolPtr(true)},
	}, capabilities)

	f.manager.checkChannel("chat", 0)

	if got := cap.lastModel(); got != "expensive-model" {
		t.Fatalf("受 SupportedModels 约束后选模型 = %q, 期望 expensive-model", got)
	}
}

// TestCheckChannelL2VerifyModel优先：显式指定验活模型时直接使用（无需定价信息）
func TestCheckChannelL2VerifyModel优先(t *testing.T) {
	capabilities := map[string]config.UpstreamModelCapability{
		"cheap-model": pricedCapability(1, 2),
	}
	cap := &genCapture{}
	srv := newL2Server(t, 200, `{"data":[{"id":"cheap-model"}]}`, 200, "", cap)
	f := newL2Fixture("chat", config.UpstreamConfig{
		Name:        "ch0",
		BaseURL:     srv.URL,
		APIKeys:     []string{"sk-l2-key-0004"},
		Status:      "active",
		ServiceType: "openai",
		HealthCheck: &config.ChannelHealthCheckConfig{VerifyRealCall: boolPtr(true), VerifyModel: "designated-model"},
	}, capabilities)

	f.manager.checkChannel("chat", 0)

	if got := cap.lastModel(); got != "designated-model" {
		t.Fatalf("VerifyModel 指定后 L2 模型 = %q, 期望 designated-model", got)
	}
	if l2 := f.l2Record(t, "chat"); l2 == nil || l2.LastStatus != StatusOK || l2.Detail != "model=designated-model" {
		t.Fatalf("l2 记录异常: %+v", l2)
	}
}

// TestCheckChannelL2无定价且无指定跳过：不写 l2 记录、不发真实调用
func TestCheckChannelL2无定价且无指定跳过(t *testing.T) {
	cap := &genCapture{}
	srv := newL2Server(t, 200, `{"data":[{"id":"unknown-a"},{"id":"unknown-b"}]}`, 200, "", cap)
	f := newL2Fixture("chat", config.UpstreamConfig{
		Name:        "ch0",
		BaseURL:     srv.URL,
		APIKeys:     []string{"sk-l2-key-0005"},
		Status:      "active",
		ServiceType: "openai",
		HealthCheck: &config.ChannelHealthCheckConfig{VerifyRealCall: boolPtr(true)},
	}, nil)

	f.manager.checkChannel("chat", 0)

	if l1 := f.l1Record(t, "chat"); l1 == nil || l1.LastStatus != StatusOK {
		t.Fatalf("L1 记录异常: %+v", l1)
	}
	if l2 := f.l2Record(t, "chat"); l2 != nil {
		t.Fatalf("无定价信息不应写 l2 记录: %+v", l2)
	}
	if cap.count() != 0 {
		t.Fatalf("无定价信息不应发 L2 调用, count=%d", cap.count())
	}
}

// TestCheckChannelL2ImagesVectors跳过：images/vectors 不支持 L2（不写记录）
func TestCheckChannelL2ImagesVectors跳过(t *testing.T) {
	for _, channelType := range []string{"images", "vectors"} {
		t.Run(channelType, func(t *testing.T) {
			cap := &genCapture{}
			srv := newL2Server(t, 200, `{"data":[{"id":"test-model"}]}`, 200, "", cap)
			f := newL2Fixture(channelType, config.UpstreamConfig{
				Name:        "ch0",
				BaseURL:     srv.URL,
				APIKeys:     []string{"sk-l2-key-0006"},
				Status:      "active",
				ServiceType: "openai",
				HealthCheck: &config.ChannelHealthCheckConfig{VerifyRealCall: boolPtr(true)},
			}, map[string]config.UpstreamModelCapability{"test-model": pricedCapability(1, 2)})

			f.manager.checkChannel(channelType, 0)

			if l1 := f.l1Record(t, channelType); l1 == nil || l1.LastStatus != StatusOK {
				t.Fatalf("L1 记录异常: %+v", l1)
			}
			if l2 := f.l2Record(t, channelType); l2 != nil {
				t.Fatalf("%s 不应写 l2 记录: %+v", channelType, l2)
			}
			if cap.count() != 0 {
				t.Fatalf("%s 不应发 L2 调用, count=%d", channelType, cap.count())
			}
		})
	}
}

// TestCheckChannelL2鉴权失败拉黑：l2 的 401 → auth_failed + blacklist 回调
func TestCheckChannelL2鉴权失败拉黑(t *testing.T) {
	capabilities := map[string]config.UpstreamModelCapability{"test-model": pricedCapability(1, 2)}
	cap := &genCapture{}
	srv := newL2Server(t, 200, `{"data":[{"id":"test-model"}]}`, 401,
		`{"error":{"type":"authentication_error","message":"invalid api key"}}`, cap)
	f := newL2Fixture("chat", config.UpstreamConfig{
		Name:        "ch0",
		BaseURL:     srv.URL,
		APIKeys:     []string{"sk-l2-key-0007"},
		Status:      "active",
		ServiceType: "openai",
		HealthCheck: &config.ChannelHealthCheckConfig{VerifyRealCall: boolPtr(true)},
	}, capabilities)

	f.manager.checkChannel("chat", 0)

	l2 := f.l2Record(t, "chat")
	if l2 == nil || l2.LastStatus != StatusAuthFailed {
		t.Fatalf("l2 记录异常: %+v", l2)
	}
	if l2.ConsecutiveFailures != 1 {
		t.Fatalf("l2 ConsecutiveFailures = %d, 期望 1", l2.ConsecutiveFailures)
	}
	if len(f.blacklistCalls) != 1 {
		t.Fatalf("blacklist 调用次数 = %d, 期望 1", len(f.blacklistCalls))
	}
	if f.blacklistCalls[0].reason != "authentication_error" || f.blacklistCalls[0].apiKey != "sk-l2-key-0007" {
		t.Fatalf("blacklist 回调参数错误: %+v", f.blacklistCalls[0])
	}
	if len(f.recordFailureCalls) != 0 {
		t.Fatalf("auth_failed 不应喂熔断: %d 次", len(f.recordFailureCalls))
	}
}

// TestCheckChannelL2错误喂熔断：l2 的 500 → error + recordFailure 回调
func TestCheckChannelL2错误喂熔断(t *testing.T) {
	capabilities := map[string]config.UpstreamModelCapability{"test-model": pricedCapability(1, 2)}
	cap := &genCapture{}
	srv := newL2Server(t, 200, `{"data":[{"id":"test-model"}]}`, 500, `{"error":"internal"}`, cap)
	f := newL2Fixture("chat", config.UpstreamConfig{
		Name:        "ch0",
		BaseURL:     srv.URL,
		APIKeys:     []string{"sk-l2-key-0008"},
		Status:      "active",
		ServiceType: "openai",
		HealthCheck: &config.ChannelHealthCheckConfig{VerifyRealCall: boolPtr(true)},
	}, capabilities)

	f.manager.checkChannel("chat", 0)

	l2 := f.l2Record(t, "chat")
	if l2 == nil || l2.LastStatus != StatusError {
		t.Fatalf("l2 记录异常: %+v", l2)
	}
	if len(f.recordFailureCalls) != 1 {
		t.Fatalf("recordFailure 调用次数 = %d, 期望 1", len(f.recordFailureCalls))
	}
	if f.recordFailureCalls[0].baseURL != srv.URL || f.recordFailureCalls[0].apiKey != "sk-l2-key-0008" {
		t.Fatalf("recordFailure 回调参数错误: %+v", f.recordFailureCalls[0])
	}
	if f.recordFailureCalls[0].model != "test-model" {
		t.Fatalf("recordFailure 模型 = %q, 期望 test-model", f.recordFailureCalls[0].model)
	}
	if len(f.blacklistCalls) != 0 {
		t.Fatalf("500 不应拉黑: %d 次", len(f.blacklistCalls))
	}
}

// TestCheckChannelL2只在L1成功后执行：L1 失败的 key 不做 L2
func TestCheckChannelL2只在L1成功后执行(t *testing.T) {
	capabilities := map[string]config.UpstreamModelCapability{"test-model": pricedCapability(1, 2)}
	cap := &genCapture{}
	srv := newL2Server(t, 401, `{"error":{"type":"authentication_error","message":"invalid api key"}}`, 200, "", cap)
	f := newL2Fixture("chat", config.UpstreamConfig{
		Name:        "ch0",
		BaseURL:     srv.URL,
		APIKeys:     []string{"sk-l2-key-0009"},
		Status:      "active",
		ServiceType: "openai",
		HealthCheck: &config.ChannelHealthCheckConfig{VerifyRealCall: boolPtr(true)},
	}, capabilities)

	f.manager.checkChannel("chat", 0)

	if l1 := f.l1Record(t, "chat"); l1 == nil || l1.LastStatus != StatusAuthFailed {
		t.Fatalf("L1 记录异常: %+v", l1)
	}
	if l2 := f.l2Record(t, "chat"); l2 != nil {
		t.Fatalf("L1 失败不应写 l2 记录: %+v", l2)
	}
	if cap.count() != 0 {
		t.Fatalf("L1 失败不应发 L2 调用, count=%d", cap.count())
	}
	// blacklist 只来自 L1 一次
	if len(f.blacklistCalls) != 1 {
		t.Fatalf("blacklist 调用次数 = %d, 期望 1（仅 L1）", len(f.blacklistCalls))
	}
}

// TestCheckChannelL2连续失败递增与清零
func TestCheckChannelL2连续失败递增与清零(t *testing.T) {
	capabilities := map[string]config.UpstreamModelCapability{"test-model": pricedCapability(1, 2)}
	keyMask := utils.MaskAPIKey("sk-l2-key-0010")

	t.Run("失败递增", func(t *testing.T) {
		cap := &genCapture{}
		srv := newL2Server(t, 200, `{"data":[{"id":"test-model"}]}`, 500, `{"error":"internal"}`, cap)
		f := newL2Fixture("chat", config.UpstreamConfig{
			Name:        "ch0",
			BaseURL:     srv.URL,
			APIKeys:     []string{"sk-l2-key-0010"},
			Status:      "active",
			ServiceType: "openai",
			HealthCheck: &config.ChannelHealthCheckConfig{VerifyRealCall: boolPtr(true)},
		}, capabilities)
		_ = f.store.UpsertKeyHealth(metrics.KeyHealthRecord{
			ChannelType: "chat", ChannelID: "0", KeyMask: keyMask,
			CheckKind: CheckKindL2, LastCheckAt: time.Now().Add(-time.Hour),
			LastStatus: StatusError, ConsecutiveFailures: 2,
		})

		f.manager.checkChannel("chat", 0)

		if l2 := f.l2Record(t, "chat"); l2 == nil || l2.ConsecutiveFailures != 3 {
			t.Fatalf("l2 ConsecutiveFailures = %+v, 期望 3（基于上次递增）", l2)
		}
	})

	t.Run("成功清零", func(t *testing.T) {
		cap := &genCapture{}
		srv := newL2Server(t, 200, `{"data":[{"id":"test-model"}]}`, 200, "", cap)
		f := newL2Fixture("chat", config.UpstreamConfig{
			Name:        "ch0",
			BaseURL:     srv.URL,
			APIKeys:     []string{"sk-l2-key-0010"},
			Status:      "active",
			ServiceType: "openai",
			HealthCheck: &config.ChannelHealthCheckConfig{VerifyRealCall: boolPtr(true)},
		}, capabilities)
		_ = f.store.UpsertKeyHealth(metrics.KeyHealthRecord{
			ChannelType: "chat", ChannelID: "0", KeyMask: keyMask,
			CheckKind: CheckKindL2, LastCheckAt: time.Now().Add(-time.Hour),
			LastStatus: StatusError, ConsecutiveFailures: 2,
		})

		f.manager.checkChannel("chat", 0)

		if l2 := f.l2Record(t, "chat"); l2 == nil || l2.LastStatus != StatusOK || l2.ConsecutiveFailures != 0 {
			t.Fatalf("l2 记录异常: %+v, 期望 ok 且失败计数清零", l2)
		}
	})
}
