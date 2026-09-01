package common

import (
	"github.com/BenedictKing/ccx/internal/guardrails"
	"github.com/gin-gonic/gin"
)

// ApplyRequestGuardrails 在请求发往上游前应用 preCall guardrail 链。
// 挂在转发主链头部，保持改动最小。
//
// 返回：(改写后的请求体, 是否发生改写)
// fail-open：任一 guardrail 异常仅记日志，不阻断请求。
func ApplyRequestGuardrails(c *gin.Context, bodyBytes []byte, model string, provider string, channelUID string, channelName string) ([]byte, bool) {
	reg := guardrails.DefaultRegistry()
	if len(reg.List()) == 0 {
		return bodyBytes, false
	}

	ctx := &guardrails.Context{
		Model:       model,
		Provider:    provider,
		ChannelUID:  channelUID,
		ChannelName: channelName,
		Headers:     c.Request.Header,
	}

	masked, results := reg.RunPreCall(bodyBytes, ctx)

	// 写入 gin context 供日志/trace 审计
	modified := false
	for _, r := range results {
		if r.Modified {
			modified = true
			break
		}
	}
	if modified {
		c.Set("guardrailResults", results)
	}

	return masked, modified
}

// ApplyResponseGuardrails 在响应/错误返回客户端前应用 postCall guardrail 链。
// 用于 upstream_failover 错误路径和日志写前扫描。
func ApplyResponseGuardrails(c *gin.Context, bodyBytes []byte, model string, provider string, channelUID string, channelName string) ([]byte, bool) {
	reg := guardrails.DefaultRegistry()
	if len(reg.List()) == 0 {
		return bodyBytes, false
	}

	ctx := &guardrails.Context{
		Model:       model,
		Provider:    provider,
		ChannelUID:  channelUID,
		ChannelName: channelName,
		Headers:     c.Request.Header,
	}

	masked, results := reg.RunPostCall(bodyBytes, ctx)

	modified := false
	for _, r := range results {
		if r.Modified {
			modified = true
			break
		}
	}

	return masked, modified
}

// ApplyErrorInfoGuardrails 对日志中的错误信息字符串做 guardrail 扫描。
// 专用于 channel_log_helper 写日志前。非字节级扫描，直接处理 string。
func ApplyErrorInfoGuardrails(errorInfo string, c *gin.Context) string {
	if errorInfo == "" {
		return errorInfo
	}
	reg := guardrails.DefaultRegistry()
	if len(reg.List()) == 0 {
		return errorInfo
	}

	ctx := &guardrails.Context{
		Headers: c.Request.Header,
	}

	masked, _ := reg.RunPostCall([]byte(errorInfo), ctx)
	return string(masked)
}

// MaskErrorInfoForLog 是日志侧的独立掩码入口，不依赖 gin.Context。
// 日志脱敏是安全底线，不受 x-ccx-disabled-guardrails 请求头影响。
// 失败时 fail-open（返回原文）。
func MaskErrorInfoForLog(errorInfo string) string {
	if errorInfo == "" {
		return errorInfo
	}
	reg := guardrails.DefaultRegistry()
	if len(reg.List()) == 0 {
		return errorInfo
	}

	// 传空 Context——日志侧不检查豁免头
	masked, _ := reg.RunPostCall([]byte(errorInfo), &guardrails.Context{})
	return string(masked)
}
