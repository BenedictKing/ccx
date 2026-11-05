package utils

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// Claude Code 探针使用的请求特征。仅用于主动兼容性和端点验证探测，
// 与实际客户端请求中的 Claude Code 身份字段保持一致。
const (
	ClaudeCodeProbeVersion    = "2.1.209"
	ClaudeCodeProbeUserAgent  = "claude-cli/" + ClaudeCodeProbeVersion + " (external, cli)"
	ClaudeCodeProbeBetaHeader = "claude-code-20250219,adaptive-thinking-2026-01-28,prompt-caching-scope-2026-01-05,effort-2025-11-24"
)

// ApplyClaudeCodeProbeHeaders 为探测请求注入 Claude Code 客户端特征头。
func ApplyClaudeCodeProbeHeaders(headers http.Header, sessionID string) {
	if strings.TrimSpace(sessionID) == "" {
		sessionID = uuid.NewString()
	}
	headers.Set("Accept", "application/json")
	headers.Set("anthropic-version", "2023-06-01")
	headers.Set("anthropic-beta", ClaudeCodeProbeBetaHeader)
	headers.Set("anthropic-dangerous-direct-browser-access", "true")
	headers.Set("User-Agent", ClaudeCodeProbeUserAgent)
	headers.Set("X-App", "cli")
	headers.Set("X-Claude-Code-Session-Id", sessionID)
}

// IsClientFingerprintRejection 判断 401/403 响应是否为客户端指纹拦截（而非 Key 无效）。
func IsClientFingerprintRejection(statusCode int, body []byte) bool {
	if statusCode != http.StatusUnauthorized && statusCode != http.StatusForbidden {
		return false
	}
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "unauthorized client") ||
		strings.Contains(lower, "unauthorized_client") ||
		strings.Contains(lower, "client detected")
}
