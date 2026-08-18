package utils

import (
	"context"
	"io"
	"net/http"
	"strings"
)

// 上游 models 端点的客户端指纹校验特征（如 AgentRouter 的 new-api 风控）：
// 裸请求（无客户端 User-Agent）会被 401/403 拒绝，带 Claude Code 客户端伪装头后放行。
// body 读取上限与大站模型清单（数百模型 ID）留足余量。
const clientFingerprintBodyLimit = 1024 * 1024

// IsClientFingerprintRejection 判断响应是否为上游的客户端指纹校验拦截
// （区别于普通 API Key 无效：带客户端伪装头重试可能通过）。
func IsClientFingerprintRejection(statusCode int, body []byte) bool {
	if statusCode != http.StatusUnauthorized && statusCode != http.StatusForbidden {
		return false
	}
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "unauthorized client") ||
		strings.Contains(lower, "unauthorized_client") ||
		strings.Contains(lower, "client detected")
}

// FetchUpstreamModels 执行 GET models 请求，统一处理客户端伪装头策略：
//   - useProbeHeaders=true：首发即带 Claude Code 探针头（Anthropic 风格端点的协议行为，
//     与 capability test 探测一致），返回 usedProbeHeaders=false；
//   - useProbeHeaders=false：首发裸请求；若响应命中客户端指纹拦截特征，
//     带 Claude Code 探针头重试一次；重试返回 2xx 时返回 usedProbeHeaders=true
//     （调用方应将该上游学习为"需要客户端伪装"）。重试仍失败则返回重试响应，
//     usedProbeHeaders=false，避免把 Key 无效误学为指纹拦截。
//
// applyAuth 由调用方注入认证头；customHeaders 最后应用，保证用户显式配置可覆盖探针头。
func FetchUpstreamModels(
	ctx context.Context,
	client *http.Client,
	modelsURL string,
	applyAuth func(http.Header),
	customHeaders map[string]string,
	useProbeHeaders bool,
) (statusCode int, body []byte, usedProbeHeaders bool, err error) {
	doRequest := func(withProbeHeaders bool) (int, []byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
		if err != nil {
			return 0, nil, err
		}
		if applyAuth != nil {
			applyAuth(req.Header)
		}
		if withProbeHeaders {
			ApplyClaudeCodeProbeHeaders(req.Header, "")
		}
		ApplyCustomHeaders(req.Header, customHeaders)
		resp, err := client.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer func() { _ = resp.Body.Close() }()
		respBody, err := io.ReadAll(io.LimitReader(resp.Body, clientFingerprintBodyLimit))
		if err != nil {
			return resp.StatusCode, nil, err
		}
		return resp.StatusCode, respBody, nil
	}

	statusCode, body, err = doRequest(useProbeHeaders)
	if err != nil || useProbeHeaders {
		return statusCode, body, false, err
	}
	if !IsClientFingerprintRejection(statusCode, body) {
		return statusCode, body, false, nil
	}

	retryStatus, retryBody, retryErr := doRequest(true)
	if retryErr != nil {
		return retryStatus, retryBody, false, retryErr
	}
	learned := retryStatus >= 200 && retryStatus < 300
	return retryStatus, retryBody, learned, nil
}
