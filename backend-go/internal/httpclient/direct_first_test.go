package httpclient

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// roundTripFunc 函数适配器，便于用桩替代真实 transport。
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// stubTransport 记录调用次数与最后一次请求体，按给定状态码/错误返回。
type stubTransport struct {
	status   int
	err      error
	calls    int
	lastBody []byte
}

func (s *stubTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	s.calls++
	if r.Body != nil {
		s.lastBody, _ = io.ReadAll(r.Body)
	}
	if s.err != nil {
		return nil, s.err
	}
	return &http.Response{
		StatusCode: s.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("stub-body")),
	}, nil
}

// newReplayableRequest 构造带 GetBody 的可重放请求（与项目内 bytes.NewReader 用法一致）。
func newReplayableRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://upstream.test/v1/x", bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	if req.GetBody == nil {
		t.Fatal("bytes.NewReader 构造的请求应带 GetBody")
	}
	return req
}

func TestDirectFirstRoundTripper(t *testing.T) {
	tests := []struct {
		name           string
		directStatus   int
		directErr      error
		proxiedStatus  int
		wantProxiedHit bool
		wantStatus     int
	}{
		{name: "直连200不走代理", directStatus: 200, proxiedStatus: 200, wantProxiedHit: false, wantStatus: 200},
		{name: "直连451回退代理", directStatus: 451, proxiedStatus: 200, wantProxiedHit: true, wantStatus: 200},
		{name: "直连403回退代理", directStatus: 403, proxiedStatus: 200, wantProxiedHit: true, wantStatus: 200},
		{name: "直连401不回退", directStatus: 401, proxiedStatus: 200, wantProxiedHit: false, wantStatus: 401},
		{name: "直连500不回退", directStatus: 500, proxiedStatus: 200, wantProxiedHit: false, wantStatus: 500},
		{name: "直连429不回退", directStatus: 429, proxiedStatus: 200, wantProxiedHit: false, wantStatus: 429},
		{name: "直连网络错误回退代理", directErr: errors.New("dial tcp: connection refused"), proxiedStatus: 200, wantProxiedHit: true, wantStatus: 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			direct := &stubTransport{status: tt.directStatus, err: tt.directErr}
			proxied := &stubTransport{status: tt.proxiedStatus}
			rt := &directFirstRoundTripper{direct: direct, proxied: proxied, proxyURL: "http://proxy.test:7890"}

			req := newReplayableRequest(t, "payload-1")
			resp, err := rt.RoundTrip(req)
			if err != nil {
				t.Fatalf("RoundTrip 返回错误: %v", err)
			}
			defer resp.Body.Close()

			if direct.calls != 1 {
				t.Errorf("direct 应被调用 1 次，实际 %d", direct.calls)
			}
			if tt.wantProxiedHit && proxied.calls != 1 {
				t.Errorf("proxied 应被调用 1 次，实际 %d", proxied.calls)
			}
			if !tt.wantProxiedHit && proxied.calls != 0 {
				t.Errorf("proxied 不应被调用，实际 %d 次", proxied.calls)
			}
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("状态码 = %d，期望 %d", resp.StatusCode, tt.wantStatus)
			}
			if tt.wantProxiedHit && string(proxied.lastBody) != "payload-1" {
				t.Errorf("回退请求体 = %q，期望完整重放 %q", proxied.lastBody, "payload-1")
			}
		})
	}
}

// TestDirectFirstRoundTripper_NonReplayableBody 请求体不可重放时不得回退，原样返回直连结果。
func TestDirectFirstRoundTripper_NonReplayableBody(t *testing.T) {
	direct := &stubTransport{status: 451}
	proxied := &stubTransport{status: 200}
	rt := &directFirstRoundTripper{direct: direct, proxied: proxied, proxyURL: "http://proxy.test:7890"}

	// io.NopCloser 非已知类型，http.NewRequest 不会设置 GetBody
	req, err := http.NewRequest(http.MethodPost, "http://upstream.test/v1/x", io.NopCloser(strings.NewReader("one-shot")))
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	if req.GetBody != nil {
		t.Fatal("该请求的 GetBody 应为 nil")
	}

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip 返回错误: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 451 {
		t.Errorf("状态码 = %d，期望直连原样返回 451", resp.StatusCode)
	}
	if proxied.calls != 0 {
		t.Errorf("不可重放请求不应回退代理，实际调用 %d 次", proxied.calls)
	}
}

// TestDirectFirstRoundTripper_NoBody 无 body 的 GET 请求命中回退状态码时应回退。
func TestDirectFirstRoundTripper_NoBody(t *testing.T) {
	direct := &stubTransport{status: 451}
	proxied := &stubTransport{status: 200}
	rt := &directFirstRoundTripper{direct: direct, proxied: proxied, proxyURL: "http://proxy.test:7890"}

	req, err := http.NewRequest(http.MethodGet, "http://upstream.test/v1/models", nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip 返回错误: %v", err)
	}
	defer resp.Body.Close()

	if proxied.calls != 1 {
		t.Errorf("proxied 应被调用 1 次，实际 %d", proxied.calls)
	}
	if resp.StatusCode != 200 {
		t.Errorf("状态码 = %d，期望代理返回 200", resp.StatusCode)
	}
}

// TestDirectFirstRoundTripper_ProxiedError 回退后代理侧错误应原样透传。
func TestDirectFirstRoundTripper_ProxiedError(t *testing.T) {
	direct := &stubTransport{status: 451}
	proxied := &stubTransport{err: errors.New("proxy dial failed")}
	rt := &directFirstRoundTripper{direct: direct, proxied: proxied, proxyURL: "http://proxy.test:7890"}

	req := newReplayableRequest(t, "payload-2")
	_, err := rt.RoundTrip(req)
	if err == nil || !strings.Contains(err.Error(), "proxy dial failed") {
		t.Fatalf("应透传代理侧错误，实际: %v", err)
	}
}

// newTestManager 构造独立 ClientManager，避免污染全局缓存。
func newTestManager() *ClientManager {
	return &ClientManager{clients: make(map[string]*http.Client)}
}

// TestGetClient_FallbackToLegacyPaths ProxyPreferDirect=false 或 ProxyURL 空时应与旧方法行为一致（共享缓存）。
func TestGetClient_FallbackToLegacyPaths(t *testing.T) {
	cm := newTestManager()
	proxy := "http://127.0.0.1:7890"

	legacy := cm.GetStandardClientWithResponseHeaderTimeout(time.Second, 0, false, proxy)
	got := cm.GetClient(ClientOptions{Timeout: time.Second, ProxyURL: proxy})
	if got != legacy {
		t.Error("ProxyPreferDirect=false 应复用标准客户端缓存")
	}

	noProxyLegacy := cm.GetStandardClientWithResponseHeaderTimeout(time.Second, 0, false)
	noProxy := cm.GetClient(ClientOptions{Timeout: time.Second, ProxyPreferDirect: true})
	if noProxy != noProxyLegacy {
		t.Error("ProxyURL 为空时应复用标准客户端缓存（直连优先无意义）")
	}

	streamLegacy := cm.GetStreamClientWithResponseHeaderTimeout(0, false, proxy)
	streamGot := cm.GetClient(ClientOptions{Stream: true, ProxyURL: proxy})
	if streamGot != streamLegacy {
		t.Error("流式 + ProxyPreferDirect=false 应复用流式客户端缓存")
	}
}

// TestGetClient_DirectFirst 直连优先应产出独立客户端并进入自有缓存键。
func TestGetClient_DirectFirst(t *testing.T) {
	cm := newTestManager()
	opts := ClientOptions{Timeout: time.Second, ProxyURL: "http://127.0.0.1:7890", ProxyPreferDirect: true}

	c1 := cm.GetClient(opts)
	if _, ok := c1.Transport.(*directFirstRoundTripper); !ok {
		t.Fatalf("Transport 应为 *directFirstRoundTripper，实际 %T", c1.Transport)
	}
	if c2 := cm.GetClient(opts); c2 != c1 {
		t.Error("相同 ClientOptions 应命中缓存返回同一客户端")
	}

	legacy := cm.GetStandardClientWithResponseHeaderTimeout(time.Second, 0, false, "http://127.0.0.1:7890")
	if c1 == legacy {
		t.Error("直连优先客户端不应与标准客户端共享缓存")
	}

	// NewClient 不进入缓存
	n1 := cm.NewClient(opts)
	n2 := cm.NewClient(opts)
	if n1 == n2 {
		t.Error("NewClient 不应缓存")
	}
	if _, ok := n1.Transport.(*directFirstRoundTripper); !ok {
		t.Fatalf("NewClient Transport 应为 *directFirstRoundTripper，实际 %T", n1.Transport)
	}
	if n1.Timeout != time.Second {
		t.Errorf("非流式 Timeout = %v，期望 1s", n1.Timeout)
	}
}
