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

	transport := buildTransport(false, responseHeaderTimeout, insecure)
	applyProxy(transport, proxyAddr)

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}

	if useCache {
		cm.clients[key] = client
	}
	return client
}

// buildTransport 构造标准/流式 transport；proxyAddr 经 applyProxy 注入（空=直连）。
func buildTransport(stream bool, responseHeaderTimeout time.Duration, insecure bool) *http.Transport {
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
	if stream {
		// 流式连接池更大，且禁用压缩
		transport.MaxIdleConns = 200
		transport.MaxIdleConnsPerHost = 20
		transport.IdleConnTimeout = 120 * time.Second
		transport.DisableCompression = true
	}

	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		if !stream {
			envCfg := config.NewEnvConfig()
			if envCfg.IsProduction() {
				log.Printf("[HttpClient-Warn] 生产环境启用了 insecureSkipVerify，存在中间人攻击风险")
			}
		}
	}
	return transport
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

	transport := buildTransport(true, responseHeaderTimeout, insecure)
	applyProxy(transport, proxyAddr)

	client := &http.Client{
		Transport: transport,
		Timeout:   0, // 流式请求无超时
	}

	cm.clients[key] = client
	return client
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

// ClientOptions 描述一次上游请求客户端的全部可变参数（由渠道级配置展开而来）。
type ClientOptions struct {
	Stream                bool          // true=流式客户端（无整体超时、禁用压缩）
	Timeout               time.Duration // 非流式整体超时（Stream=true 时忽略）
	ResponseHeaderTimeout time.Duration // 等待响应头超时（0=继承全局配置）
	Insecure              bool          // 跳过 TLS 证书验证
	ProxyURL              string        // HTTP/HTTPS/SOCKS5 代理地址，空=直连
	ProxyPreferDirect     bool          // 配置了代理时先直连，失败（网络错误/451/403）自动回退代理
}

// GetClient 按 ClientOptions 获取客户端。
// ProxyPreferDirect=false 或 ProxyURL 为空时与 Get 系列方法行为完全一致（含缓存）。
func (cm *ClientManager) GetClient(opts ClientOptions) *http.Client {
	if !opts.ProxyPreferDirect || opts.ProxyURL == "" {
		if opts.Stream {
			return cm.GetStreamClientWithResponseHeaderTimeout(opts.ResponseHeaderTimeout, opts.Insecure, opts.ProxyURL)
		}
		return cm.GetStandardClientWithResponseHeaderTimeout(opts.Timeout, opts.ResponseHeaderTimeout, opts.Insecure, opts.ProxyURL)
	}
	return cm.getDirectFirstClient(opts)
}

// NewClient 与 GetClient 相同但不进入缓存（适用于表单临时代理等高变参数）。
func (cm *ClientManager) NewClient(opts ClientOptions) *http.Client {
	if !opts.ProxyPreferDirect || opts.ProxyURL == "" {
		if opts.Stream {
			return cm.GetStreamClientWithResponseHeaderTimeout(opts.ResponseHeaderTimeout, opts.Insecure, opts.ProxyURL)
		}
		return cm.NewStandardClient(opts.Timeout, opts.Insecure, opts.ProxyURL)
	}
	responseHeaderTimeout := normalizeResponseHeaderTimeout(opts.ResponseHeaderTimeout)
	direct := buildTransport(opts.Stream, responseHeaderTimeout, opts.Insecure)
	proxied := buildTransport(opts.Stream, responseHeaderTimeout, opts.Insecure)
	applyProxy(proxied, opts.ProxyURL)
	timeout := opts.Timeout
	if opts.Stream {
		timeout = 0 // 流式请求无整体超时
	}
	return &http.Client{
		Transport: &directFirstRoundTripper{direct: direct, proxied: proxied, proxyURL: opts.ProxyURL},
		Timeout:   timeout,
	}
}

// getDirectFirstClient 构造直连优先客户端：direct/proxied 两个 transport 由
// directFirstRoundTripper 编排，先直连、命中回退条件时改走代理。
func (cm *ClientManager) getDirectFirstClient(opts ClientOptions) *http.Client {
	responseHeaderTimeout := normalizeResponseHeaderTimeout(opts.ResponseHeaderTimeout)
	key := fmt.Sprintf("df-%t-%d-%d-%t-%s", opts.Stream, opts.Timeout, responseHeaderTimeout, opts.Insecure, opts.ProxyURL)

	cm.mu.RLock()
	if client, ok := cm.clients[key]; ok {
		cm.mu.RUnlock()
		return client
	}
	cm.mu.RUnlock()

	cm.mu.Lock()
	defer cm.mu.Unlock()

	if client, ok := cm.clients[key]; ok {
		return client
	}

	direct := buildTransport(opts.Stream, responseHeaderTimeout, opts.Insecure)
	proxied := buildTransport(opts.Stream, responseHeaderTimeout, opts.Insecure)
	applyProxy(proxied, opts.ProxyURL)

	timeout := opts.Timeout
	if opts.Stream {
		timeout = 0 // 流式请求无整体超时
	}
	client := &http.Client{
		Transport: &directFirstRoundTripper{direct: direct, proxied: proxied, proxyURL: opts.ProxyURL},
		Timeout:   timeout,
	}
	cm.clients[key] = client
	return client
}
