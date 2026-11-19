package httpclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/utils"
	"golang.org/x/net/proxy"
)

// ClientManager HTTP 客户端管理器
type ClientManager struct {
	mu      sync.RWMutex
	clients map[string]*http.Client
}

var globalManager = &ClientManager{
	clients: make(map[string]*http.Client),
}

// GetManager 获取全局客户端管理器
func GetManager() *ClientManager {
	return globalManager
}

// GetStandardClient 获取标准客户端（有超时，用于普通请求）
// 注意：启用自动压缩让Go处理gzip，配合请求头清理确保正确解压
// proxyURL: 可选的代理地址（支持 http/https/socks5 协议）
func (cm *ClientManager) GetStandardClient(timeout time.Duration, insecure bool, proxyURL ...string) *http.Client {
	return cm.getStandardClient(timeout, 0, insecure, true, proxyURL...)
}

// GetStandardClientWithResponseHeaderTimeout 获取标准客户端，并显式指定等待响应头超时。
func (cm *ClientManager) GetStandardClientWithResponseHeaderTimeout(timeout time.Duration, responseHeaderTimeout time.Duration, insecure bool, proxyURL ...string) *http.Client {
	return cm.getStandardClient(timeout, responseHeaderTimeout, insecure, true, proxyURL...)
}

// NewStandardClient 获取标准客户端但不进入缓存（适用于临时代理等高变参数）
func (cm *ClientManager) NewStandardClient(timeout time.Duration, insecure bool, proxyURL ...string) *http.Client {
	return cm.getStandardClient(timeout, 0, insecure, false, proxyURL...)
}

func (cm *ClientManager) getStandardClient(timeout time.Duration, responseHeaderTimeout time.Duration, insecure bool, useCache bool, proxyURL ...string) *http.Client {
	responseHeaderTimeout = normalizeResponseHeaderTimeout(responseHeaderTimeout)
	// 提取代理 URL
	proxyAddr := ""
	if len(proxyURL) > 0 {
		proxyAddr = proxyURL[0]
	}

	key := fmt.Sprintf("standard-%d-%t-%d-%s", timeout, insecure, responseHeaderTimeout, proxyAddr)

	if useCache {
		cm.mu.RLock()
		if client, ok := cm.clients[key]; ok {
			cm.mu.RUnlock()
			return client
		}
		cm.mu.RUnlock()
	}

	if useCache {
		cm.mu.Lock()
		defer cm.mu.Unlock()

		if client, ok := cm.clients[key]; ok {
			return client
		}
	}

	transport := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		DisableCompression:    false,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}

	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		envCfg := config.NewEnvConfig()
		if envCfg.IsProduction() {
			log.Printf("[HttpClient-Warn] 生产环境启用了 insecureSkipVerify，存在中间人攻击风险")
		}
	}

	applyProxy(transport, proxyAddr)

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
	applySSRFProtection(client, transport)

	if useCache {
		cm.clients[key] = client
	}
	return client
}

// GetStreamClient 获取流式客户端（无超时，用于 SSE 流式响应）
// proxyURL: 可选的代理地址（支持 http/https/socks5 协议）
func (cm *ClientManager) GetStreamClient(insecure bool, proxyURL ...string) *http.Client {
	return cm.GetStreamClientWithResponseHeaderTimeout(0, insecure, proxyURL...)
}

// GetStreamClientWithResponseHeaderTimeout 获取流式客户端，并显式指定等待响应头超时。
func (cm *ClientManager) GetStreamClientWithResponseHeaderTimeout(responseHeaderTimeout time.Duration, insecure bool, proxyURL ...string) *http.Client {
	responseHeaderTimeout = normalizeResponseHeaderTimeout(responseHeaderTimeout)
	// 提取代理 URL
	proxyAddr := ""
	if len(proxyURL) > 0 {
		proxyAddr = proxyURL[0]
	}

	key := fmt.Sprintf("stream-%t-%d-%s", insecure, responseHeaderTimeout, proxyAddr)

	cm.mu.RLock()
	if client, ok := cm.clients[key]; ok {
		cm.mu.RUnlock()
		return client
	}
	cm.mu.RUnlock()

	cm.mu.Lock()
	defer cm.mu.Unlock()

	// 双重检查
	if client, ok := cm.clients[key]; ok {
		return client
	}

	transport := &http.Transport{
		MaxIdleConns:          200, // 流式连接池更大
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       120 * time.Second,
		DisableCompression:    true, // 流式响应禁用压缩
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}

	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	applyProxy(transport, proxyAddr)

	client := &http.Client{
		Transport: transport,
		Timeout:   0, // 流式请求无超时
	}
	applySSRFProtection(client, transport)

	cm.clients[key] = client
	return client
}

func applySSRFProtection(client *http.Client, transport *http.Transport) {
	if transport.DialContext == nil {
		dialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		// ControlContext 在 DNS 解析完成后、实际建连前拿到目标 IP，避免校验与连接之间的 DNS 重绑定。
		dialer.ControlContext = func(_ context.Context, _, address string, _ syscall.RawConn) error {
			return validateDialAddress(address)
		}
		transport.DialContext = dialer.DialContext
	} else {
		dialContext := transport.DialContext
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			if err := validateDialAddress(address); err != nil {
				return nil, err
			}
			return dialContext(ctx, network, address)
		}
	}

	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if err := utils.ValidateBaseURL(req.URL.String()); err != nil {
			return fmt.Errorf("SSRF 防护拦截重定向: %w", err)
		}
		return nil
	}
}

func validateDialAddress(address string) error {
	if _, _, err := net.SplitHostPort(address); err != nil {
		return fmt.Errorf("无效的上游连接地址: %w", err)
	}
	targetURL := (&url.URL{Scheme: "http", Host: address}).String()
	if err := utils.ValidateBaseURL(targetURL); err != nil {
		return fmt.Errorf("SSRF 防护拦截上游连接: %w", err)
	}
	return nil
}

func normalizeResponseHeaderTimeout(responseHeaderTimeout time.Duration) time.Duration {
	if responseHeaderTimeout > 0 {
		return responseHeaderTimeout
	}
	envConfig := config.NewEnvConfig()
	return time.Duration(config.GetRuntimeResponseHeaderTimeoutMs(envConfig.ResponseHeaderTimeout*1000)) * time.Millisecond
}

// applyProxy 为 transport 配置代理
// 支持 http://, https://, socks5:// 协议
func applyProxy(transport *http.Transport, proxyAddr string) {
	if proxyAddr == "" {
		return
	}

	u, err := url.Parse(proxyAddr)
	if err != nil {
		// 对代理 URL 进行脱敏处理，避免泄露凭证
		redactedProxyURL := utils.RedactURLCredentials(proxyAddr)
		log.Printf("[HttpClient-Proxy] 警告: 代理地址解析失败: %s, 错误: %v", redactedProxyURL, err)
		return
	}

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "socks5", "socks5h":
		// SOCKS5 代理：通过 golang.org/x/net/proxy 创建 DialContext
		var auth *proxy.Auth
		if u.User != nil {
			password, _ := u.User.Password()
			auth = &proxy.Auth{
				User:     u.User.Username(),
				Password: password,
			}
		}
		dialer, dialErr := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
		if dialErr != nil {
			redactedProxyURL := utils.RedactURLCredentials(proxyAddr)
			log.Printf("[HttpClient-Proxy] 警告: SOCKS5 代理创建失败: %s, 错误: %v", redactedProxyURL, dialErr)
			return
		}
		// proxy.Dialer 实现了 ContextDialer 接口
		if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
			transport.DialContext = contextDialer.DialContext
		} else {
			// 兜底：将 Dial 包装为 DialContext
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			}
		}
		// SOCKS5 代理不支持 HTTP/2 (需要直连 TLS)
		transport.ForceAttemptHTTP2 = false

	case "http", "https":
		// HTTP/HTTPS 代理
		transport.Proxy = http.ProxyURL(u)

	default:
		log.Printf("[HttpClient-Proxy] 警告: 不支持的代理协议: %s (地址: %s)", scheme, utils.RedactURLCredentials(proxyAddr))
	}
}
