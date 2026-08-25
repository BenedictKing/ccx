package httpclient

import (
	"log"
	"net/http"

	"github.com/BenedictKing/ccx/internal/utils"
)

// proxyFallbackStatuses 触发代理回退的 HTTP 状态码。
// 451（Unavailable For Legal Reasons）与 403（Cloudflare/WAF 地域封锁常见形态）
// 都出现在"直连被站方按地区/策略拦截"的场景；回退代理可绕过。
// 401/429/5xx 等属于上游业务语义，回退代理只会放大请求量，故不在集合内。
var proxyFallbackStatuses = map[int]bool{
	http.StatusForbidden:                  true,
	http.StatusUnavailableForLegalReasons: true,
}

// isProxyFallbackStatus 判断状态码是否命中代理回退集合。
func isProxyFallbackStatus(code int) bool {
	return proxyFallbackStatuses[code]
}

// directFirstRoundTripper 直连优先回退传输：先经 direct 直连发起请求；
// 当出现底层网络错误，或直连返回命中 proxyFallbackStatuses 的状态码时，
// 在请求可安全重放（GetBody 可用或无 body）的前提下改用 proxied 重放该请求。
// 流式请求在响应头阶段即可决策，回退不会截断已开始的 SSE 流。
type directFirstRoundTripper struct {
	direct   http.RoundTripper
	proxied  http.RoundTripper
	proxyURL string // 仅用于日志脱敏展示
}

// RoundTrip 实现 http.RoundTripper。
func (t *directFirstRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.direct.RoundTrip(req)
	if err == nil && !isProxyFallbackStatus(resp.StatusCode) {
		return resp, nil
	}

	// 请求体不可重放时无法安全重试，原样返回直连结果
	if req.Body != nil && req.GetBody == nil {
		return resp, err
	}

	if err == nil {
		// 关闭直连响应体（此时仅收到响应头，未开始消费流式 body，关闭安全）
		resp.Body.Close()
		log.Printf("[HttpClient-Proxy] 直连优先: %s 返回 %d，回退代理 %s", req.URL.Host, resp.StatusCode, utils.RedactURLCredentials(t.proxyURL))
	} else {
		log.Printf("[HttpClient-Proxy] 直连优先: %s 直连失败(%v)，回退代理 %s", req.URL.Host, err, utils.RedactURLCredentials(t.proxyURL))
	}

	retry := req.Clone(req.Context())
	if req.GetBody != nil {
		body, bodyErr := req.GetBody()
		if bodyErr != nil {
			return resp, err
		}
		retry.Body = body
	}
	return t.proxied.RoundTrip(retry)
}
