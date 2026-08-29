package handlers

import (
	"context"
	"net/http"

	"github.com/BenedictKing/ccx/internal/config"
)

// 渠道保活验证（internal/healthcheck）L2 真实调用验活复用能力测试的请求构建与发送路径。
// 探测提示词与 max_tokens 保持能力测试的现行自然值，不做任何"最小化"改动。

// BuildHealthCheckL2Request 供保活验证 L2 构建真实调用请求（语义同 buildTestRequestWithModel）。
// channel 应为调用方按 key 裁剪后的副本（APIKeys 只含待验 key）。
func BuildHealthCheckL2Request(protocol string, channel *config.UpstreamConfig, model string) (*http.Request, error) {
	return buildTestRequestWithModel(protocol, channel, model)
}

// SendHealthCheckL2Stream 供保活验证 L2 发送请求并做流式预检（语义同 sendAndCheckStream）。
// 返回 (success, streamingSupported, statusCode, respBody, upstreamModel, err)；
// upstreamModel 为上游响应自报的模型名（用于观测厂商侧隐式模型重定向），无则空串。
func SendHealthCheckL2Stream(ctx context.Context, channel *config.UpstreamConfig, req *http.Request, protocol string) (bool, bool, int, []byte, string, error) {
	return sendAndCheckStream(ctx, channel, req, protocol)
}
