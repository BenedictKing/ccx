package common

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

// realClientWriteError 在本机 TCP 上制造真实的「写给已断开的对端」错误：
// 对端关闭后持续写，内核返回 EPIPE（broken pipe）或 ECONNRESET。
func realClientWriteError(t *testing.T) error {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, _ := ln.Accept()
		accepted <- conn
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	peer := <-accepted
	_ = peer.Close() // 客户端主动断开
	chunk := []byte(strings.Repeat("data: x\n\n", 1024))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := conn.Write(chunk); err != nil {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("没有拿到写错误")
	return nil
}

func runFailoverWithHandleSuccessError(t *testing.T, handleErr error) (*config.ConfigManager, *metrics.MetricsManager, *config.UpstreamConfig) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {}\n\n"))
	}))
	t.Cleanup(server.Close)

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
		Name:        "broken-pipe",
		BaseURL:     server.URL,
		APIKeys:     []string{"sk-broken-pipe"},
		Status:      "active",
		ServiceType: "openai",
	})
	t.Cleanup(cleanup)
	cfg := cfgManager.GetConfig()
	upstream := &cfg.Upstream[0]

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"gpt-5.5","stream":true}`))

	TryUpstreamWithAllKeys(
		c, config.NewEnvConfig(), cfgManager, channelScheduler, scheduler.ChannelKindMessages, "Messages",
		messagesMetrics, upstream, []warmup.URLLatencyResult{{URL: upstream.BaseURL}},
		[]byte(`{"model":"gpt-5.5","messages":[],"stream":true}`), nil, true,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, "Messages")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, strings.NewReader(`{}`))
		},
		func(apiKey string) {}, nil, nil,
		func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
			// 已开始向客户端出流，随后写客户端失败
			c.Header("Content-Type", "text/event-stream")
			c.Status(http.StatusOK)
			_, _ = c.Writer.Write([]byte("data: {}\n\n"))
			return nil, handleErr
		},
		"gpt-5.5", "", 0, channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
	)
	return cfgManager, messagesMetrics, upstream
}

// Issue #292：流式响应中客户端主动断开（write: broken pipe）不应计入 Key 失败 / 模型熔断。
func TestClientBrokenPipeMidStreamNotCountedAsKeyFailure(t *testing.T) {
	realErr := realClientWriteError(t)
	t.Logf("real client write error: %v", realErr)
	cases := map[string]error{
		"real socket write error": realErr,
		"write: broken pipe (as in issue log)": &net.OpError{Op: "write", Net: "tcp",
			Err: os.NewSyscallError("write", syscall.EPIPE)},
		"write: connection reset by peer": &net.OpError{Op: "write", Net: "tcp",
			Err: os.NewSyscallError("write", syscall.ECONNRESET)},
	}
	for name, handleErr := range cases {
		t.Run(name, func(t *testing.T) {
			cfgManager, _, upstream := runFailoverWithHandleSuccessError(t, handleErr)
			key := upstream.APIKeys[0]
			if cfgManager.IsKeyFailed(key, "Messages") {
				t.Errorf("客户端断开 (%v) 不应标记 Key 失败", handleErr)
			}
		})
	}
}

// 读上游时的连接重置仍是真实渠道故障，必须照旧计入失败。
func TestUpstreamReadResetStillCountedAsKeyFailure(t *testing.T) {
	readErr := &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", syscall.ECONNRESET)}
	cfgManager, _, upstream := runFailoverWithHandleSuccessError(t, readErr)
	if !cfgManager.IsKeyFailed(upstream.APIKeys[0], "Messages") {
		t.Fatal("上游读连接重置应照旧标记 Key 失败")
	}
	if errors.Is(readErr, syscall.EPIPE) {
		t.Fatal("unreachable")
	}
}
