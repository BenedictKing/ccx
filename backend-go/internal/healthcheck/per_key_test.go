package healthcheck

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/utils"
)

// readAllBody 读取响应体用于测试断言
func readAllBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	_ = resp.Body.Close()
	return buf
}

// runL1WithFetcher 用指定 fetcher 执行单 key L1 并返回该 key 写入的记录。
// 按 keyMask 过滤，避免 fakeKeyHealthStore 的 map 遍历顺序导致取错记录。
func runL1WithFetcher(t *testing.T, f *checkKeyFixture, u *config.UpstreamConfig, baseURLs []string, apiKey string, fetcher L1Fetcher) metrics.KeyHealthRecord {
	t.Helper()
	f.manager.checkKeyL1("messages", 0, "0", u, baseURLs, apiKey, defaultTestPolicy(2*time.Second), nil, fetcher)
	keyMask := utils.MaskAPIKey(apiKey)
	recs, _ := f.store.GetKeyHealthForChannel("messages", "0")
	for i := range recs {
		if recs[i].KeyMask == keyMask && recs[i].CheckKind == CheckKindL1 {
			return recs[i]
		}
	}
	return metrics.KeyHealthRecord{}
}

// TestCheckKeyL1绑定Key只访问自己端点 覆盖 per-key BaseURL 路由：
// 混合套餐渠道中已绑定端点的 Key 只在自己的 BaseURL 上探测，
// 不参与渠道级 BaseURL 笛卡尔积，避免把 Agent Plan Key 误打到 Coding Plan 入口。
func TestCheckKeyL1绑定Key只访问自己端点(t *testing.T) {
	agentSrv := newModelsServer(t, 200, `{"data":[{"id":"agent-model"}]}`)
	codingSrv := newModelsServer(t, 401, `{"error":{"type":"authentication_error","message":"invalid"}}`)
	defer agentSrv.Close()
	defer codingSrv.Close()

	// 自定义 fetcher 记录每个 key 实际访问的 BaseURL，并转发到对应测试 server
	var mu sync.Mutex
	visited := map[string][]string{}
	recordFetcher := func(ctx context.Context, req L1Request) (L1Response, error) {
		mu.Lock()
		visited[req.APIKey] = append(visited[req.APIKey], req.BaseURL)
		mu.Unlock()
		target := agentSrv.URL
		if strings.Contains(req.BaseURL, "/api/coding") {
			target = codingSrv.URL
		}
		httpReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, target+"/v1/models", nil)
		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			return L1Response{StatusCode: http.StatusBadGateway, Body: []byte(`{"error":"network"}`)}, nil
		}
		body := readAllBody(t, resp)
		if resp.StatusCode == http.StatusUnauthorized {
			wrapped := []byte(`{"error":"上游 API Key 无效","statusCode":401,"details":"` + strings.ReplaceAll(string(body), `"`, `\"`) + `"}`)
			return L1Response{StatusCode: http.StatusBadRequest, Body: wrapped}, nil
		}
		return L1Response{StatusCode: resp.StatusCode, Body: body}, nil
	}

	f := newCheckKeyFixture()
	u := &config.UpstreamConfig{
		ServiceType: "claude",
		BaseURLs:    []string{codingSrv.URL + "/api/coding", agentSrv.URL + "/api/plan"},
		APIKeys:     []string{"ark-agent", "ark-coding"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "ark-agent", BaseURL: agentSrv.URL + "/api/plan"},
			{Key: "ark-coding", BaseURL: codingSrv.URL + "/api/coding"},
		},
	}

	// ark-agent 绑定 agent 端点（200 成功），不应访问 coding 端点
	rec := runL1WithFetcher(t, f, u, u.BaseURLsForKey("ark-agent"), "ark-agent", recordFetcher)
	if rec.LastStatus != StatusOK {
		t.Fatalf("ark-agent LastStatus = %q, 期望 ok（绑定端点 200）: detail=%s", rec.LastStatus, rec.Detail)
	}
	agentVisited := visited["ark-agent"]
	if len(agentVisited) != 1 || !strings.HasSuffix(agentVisited[0], "/api/plan") {
		t.Fatalf("ark-agent 应只访问 /api/plan 一次, 实际: %v", agentVisited)
	}

	// ark-coding 绑定 coding 端点（401 鉴权失败），不应访问 agent 端点
	mu.Lock()
	visited = map[string][]string{}
	mu.Unlock()
	rec = runL1WithFetcher(t, f, u, u.BaseURLsForKey("ark-coding"), "ark-coding", recordFetcher)
	if rec.LastStatus != StatusAuthFailed {
		t.Fatalf("ark-coding LastStatus = %q, 期望 auth_failed（绑定端点 401）: detail=%s", rec.LastStatus, rec.Detail)
	}
	codingVisited := visited["ark-coding"]
	if len(codingVisited) != 1 || !strings.HasSuffix(codingVisited[0], "/api/coding") {
		t.Fatalf("ark-coding 应只访问 /api/coding 一次, 实际: %v", codingVisited)
	}
}

// TestCheckKeyL1未绑定Key保持多BaseURL回退 覆盖历史兼容：
// 未绑定端点的历史手填 Key 继续遍历渠道级全部 BaseURL，任一成功即 ok。
func TestCheckKeyL1未绑定Key保持多BaseURL回退(t *testing.T) {
	bad := newModelsServer(t, 500, `{"error":"internal"}`)
	good := newModelsServer(t, 200, `{"data":[{"id":"m1"}]}`)
	defer bad.Close()
	defer good.Close()

	f := newCheckKeyFixture()
	u := &config.UpstreamConfig{
		ServiceType: "claude",
		BaseURLs:    []string{bad.URL, good.URL},
		APIKeys:     []string{"legacy-key"},
		// 无 APIKeyConfigs → 未绑定，回退 GetAllBaseURLs
	}

	rec := runL1WithFetcher(t, f, u, u.BaseURLsForKey("legacy-key"), "legacy-key", testWrappedFetcher())
	if rec.LastStatus != StatusOK {
		t.Fatalf("未绑定 Key 应回退遍历全部 BaseURL, LastStatus = %q, 期望 ok", rec.LastStatus)
	}
	if rec.ModelCount != 1 {
		t.Fatalf("ModelCount = %d, 期望 1（第二个 BaseURL 成功）", rec.ModelCount)
	}
}

// realCallFetcher 模拟火山专用 L1 探针：标记 RealCallVerified=true，
// 200 返回模型列表（模拟 manifest 生成的响应），401 返回鉴权失败。
func realCallFetcher() L1Fetcher {
	return func(ctx context.Context, req L1Request) (L1Response, error) {
		if strings.Contains(req.BaseURL, "/api/plan") {
			return L1Response{
				StatusCode:       http.StatusOK,
				Body:             []byte(`{"data":[{"id":"doubao-seed-2.0-pro"},{"id":"kimi-k3"}]}`),
				RealCallVerified: true,
			}, nil
		}
		return L1Response{StatusCode: 500, Body: []byte(`{"error":"unreachable"}`), RealCallVerified: true}, nil
	}
}

// TestCheckChannel火山L1真实调用跳过L2 覆盖 L1/L2 去重：
// 火山套餐 L1 已是真实推理调用（RealCallVerified=true），同周期不应再发等价 L2。
func TestCheckChannel火山L1真实调用跳过L2(t *testing.T) {
	cap := &genCapture{}
	// L2 生成端点（不应被调用）
	srv := newL2Server(t, 200, `{"data":[{"id":"doubao-seed-2.0-pro"}]}`, 200, "", cap)
	defer srv.Close()

	f := newL2Fixture("messages", config.UpstreamConfig{
		Name:        "volc-agent",
		BaseURL:     "https://ark.cn-beijing.volces.com/api/plan",
		APIKeys:     []string{"ark-agent-key-0001"},
		Status:      "active",
		ServiceType: "claude",
		HealthCheck: &config.ChannelHealthCheckConfig{VerifyRealCall: boolPtr(true)},
	}, map[string]config.UpstreamModelCapability{
		"doubao-seed-2.0-pro": pricedCapability(1, 2),
	})
	// 用标记 RealCallVerified 的 fetcher 替代默认 /v1/models fetcher
	f.manager.RegisterL1Fetcher("messages", realCallFetcher())

	f.manager.checkChannel("messages", 0)

	l1 := f.l1Record(t, "messages")
	if l1 == nil || l1.LastStatus != StatusOK {
		t.Fatalf("L1 记录异常: %+v", l1)
	}
	if l2 := f.l2Record(t, "messages"); l2 != nil {
		t.Fatalf("火山 L1 已真实调用，不应再写 L2 记录: %+v", l2)
	}
	if cap.count() != 0 {
		t.Fatalf("火山 L1 已真实调用，同周期不应再发 L2 真实调用, count=%d", cap.count())
	}
}

// TestCheckChannel通用L1仍执行L2 覆盖非火山 provider 不回归：
// 通用 /v1/models 拉取（RealCallVerified=false）成功后仍应执行 L2 真实验活。
func TestCheckChannel通用L1仍执行L2(t *testing.T) {
	cap := &genCapture{}
	srv := newL2Server(t, 200, `{"data":[{"id":"test-model"}]}`, 200, "", cap)
	defer srv.Close()

	f := newL2Fixture("chat", config.UpstreamConfig{
		Name:        "generic-chat",
		BaseURL:     srv.URL,
		APIKeys:     []string{"sk-generic-0001"},
		Status:      "active",
		ServiceType: "openai",
		HealthCheck: &config.ChannelHealthCheckConfig{VerifyRealCall: boolPtr(true)},
	}, map[string]config.UpstreamModelCapability{"test-model": pricedCapability(1, 2)})
	// 默认 testWrappedFetcher（RealCallVerified=false）已由 newL2Fixture 注册

	f.manager.checkChannel("chat", 0)

	if l1 := f.l1Record(t, "chat"); l1 == nil || l1.LastStatus != StatusOK {
		t.Fatalf("L1 记录异常: %+v", l1)
	}
	if l2 := f.l2Record(t, "chat"); l2 == nil || l2.LastStatus != StatusOK {
		t.Fatalf("通用 provider L1 成功后应执行 L2, l2=%+v", l2)
	}
	if cap.count() != 1 {
		t.Fatalf("通用 provider 应发 1 次 L2 调用, count=%d", cap.count())
	}
}

// TestL2失败归因绑定BaseURL 覆盖 L2 recordFailure 使用 Key 绑定端点而非渠道首个地址。
func TestL2失败归因绑定BaseURL(t *testing.T) {
	// 渠道首个 BaseURL 是占位 first.example.com，但 L2 应命中并归因到绑定端点 srv
	cap := &genCapture{}
	srv := newL2Server(t, 200, `{"data":[{"id":"test-model"}]}`, 500, `{"error":"internal"}`, cap)
	defer srv.Close()

	f := newL2Fixture("chat", config.UpstreamConfig{
		Name:        "bound-chat",
		BaseURLs:    []string{"https://first.example.com/v3", srv.URL},
		APIKeys:     []string{"sk-bound-0001"},
		Status:      "active",
		ServiceType: "openai",
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "sk-bound-0001", BaseURL: srv.URL},
		},
		HealthCheck: &config.ChannelHealthCheckConfig{VerifyRealCall: boolPtr(true)},
	}, map[string]config.UpstreamModelCapability{"test-model": pricedCapability(1, 2)})

	f.manager.checkChannel("chat", 0)

	if l2 := f.l2Record(t, "chat"); l2 == nil || l2.LastStatus != StatusError {
		t.Fatalf("l2 记录异常: %+v", l2)
	}
	if len(f.recordFailureCalls) != 1 {
		t.Fatalf("recordFailure 调用次数 = %d, 期望 1", len(f.recordFailureCalls))
	}
	if f.recordFailureCalls[0].baseURL != srv.URL {
		t.Fatalf("L2 失败应归因到绑定 BaseURL %s, 实际 %s", srv.URL, f.recordFailureCalls[0].baseURL)
	}
	if f.recordFailureCalls[0].apiKey != "sk-bound-0001" {
		t.Fatalf("recordFailure apiKey = %q, 期望 sk-bound-0001", f.recordFailureCalls[0].apiKey)
	}
}
